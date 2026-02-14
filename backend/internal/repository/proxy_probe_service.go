package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func NewProxyExitInfoProber(cfg *config.Config) service.ProxyExitInfoProber {
	insecure := false
	allowPrivate := false
	validateResolvedIP := true
	if cfg != nil {
		insecure = cfg.Security.ProxyProbe.InsecureSkipVerify
		allowPrivate = cfg.Security.URLAllowlist.AllowPrivateHosts
		validateResolvedIP = cfg.Security.URLAllowlist.Enabled
	}
	if insecure {
		log.Printf("[ProxyProbe] Warning: insecure_skip_verify is not allowed and will cause probe failure.")
	}
	return &proxyProbeService{
		insecureSkipVerify: insecure,
		allowPrivateHosts:  allowPrivate,
		validateResolvedIP: validateResolvedIP,
	}
}

const (
	defaultProxyProbeTimeout = 30 * time.Second
)

type probeTarget struct {
	url    string
	parser string // "ip-api" or "httpbin"
}

type proxyProbeService struct {
	insecureSkipVerify bool
	allowPrivateHosts  bool
	validateResolvedIP bool
}

func looksLikeFakeIP(ip netip.Addr) bool {
	// 198.18.0.0/15 is reserved for benchmarking (RFC 2544); commonly used by proxy tools as "fake-ip".
	fakeIPRange := netip.MustParsePrefix("198.18.0.0/15")
	return fakeIPRange.Contains(ip)
}

func (s *proxyProbeService) detectLikelyFakeIP(ctx context.Context, proxyURL string) error {
	u, err := url.Parse(strings.TrimSpace(proxyURL))
	if err != nil {
		return nil
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return nil
	}

	// Don't block explicit IP hosts (users may intentionally use reserved ranges).
	if ip, err := netip.ParseAddr(host); err == nil {
		if looksLikeFakeIP(ip) {
			return fmt.Errorf("proxy host resolved to %s (likely fake-ip). Disable fake-ip/DNS override in your proxy tool or use the real proxy IP", ip.String())
		}
		return nil
	}

	// Resolve using system resolver; if it returns 198.18/15, it's almost certainly fake-ip.
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			continue
		}
		if looksLikeFakeIP(ip) {
			return fmt.Errorf("proxy host %s resolved to %s (likely fake-ip). Disable fake-ip/DNS override in your proxy tool or use the real proxy IP", host, ip.String())
		}
	}
	return nil
}

var authInURLRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/@\s:]+):([^/@\s]+)@`)

func sanitizeErrorText(s string) string {
	if s == "" {
		return s
	}
	// Best-effort redaction for any embedded URL credentials to avoid leaking secrets.
	return authInURLRe.ReplaceAllString(s, `$1$2:***@`)
}

func defaultProbeTargets() []probeTarget {
	// probeURLs 按优先级排列的探测 URL 列表
	// 某些 AI API 专用代理只允许访问特定域名/端口，因此需要多个备选（含 HTTPS 版本）
	return []probeTarget{
		{"https://httpbin.org/ip", "httpbin"},
		{"https://api.ipify.org?format=json", "ipify"},
		{"http://ip-api.com/json/?lang=zh-CN", "ip-api"},
		{"http://httpbin.org/ip", "httpbin"},
	}
}

func probeTargetsFromEnv() []probeTarget {
	raw := strings.TrimSpace(os.Getenv("SUB2API_PROXY_PROBE_URLS"))
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]probeTarget, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		u, err := url.Parse(part)
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}

		host := strings.ToLower(u.Hostname())
		parser := "status"
		switch {
		case strings.Contains(host, "ip-api.com"):
			parser = "ip-api"
		case strings.Contains(host, "httpbin.org"):
			parser = "httpbin"
		case strings.Contains(host, "ipify.org"):
			parser = "ipify"
		}

		out = append(out, probeTarget{url: part, parser: parser})
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *proxyProbeService) ProbeProxy(ctx context.Context, proxyURL string) (*service.ProxyExitInfo, int64, error) {
	if err := s.detectLikelyFakeIP(ctx, proxyURL); err != nil {
		return nil, 0, err
	}

	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:           proxyURL,
		Timeout:            defaultProxyProbeTimeout,
		InsecureSkipVerify: s.insecureSkipVerify,
		ProxyStrict:        true,
		ValidateResolvedIP: s.validateResolvedIP,
		AllowPrivateHosts:  s.allowPrivateHosts,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create proxy client: %w", err)
	}

	targets := probeTargetsFromEnv()
	if len(targets) == 0 {
		targets = defaultProbeTargets()
	}

	var lastErr error
	tried := make([]string, 0, len(targets))
	for _, probe := range targets {
		exitInfo, latencyMs, err := s.probeWithURL(ctx, client, probe.url, probe.parser)
		if err == nil {
			return exitInfo, latencyMs, nil
		}
		tried = append(tried, fmt.Sprintf("%s -> %s", probe.url, sanitizeErrorText(err.Error())))
		lastErr = err
	}

	if len(tried) > 0 {
		return nil, 0, fmt.Errorf("all probe URLs failed (%d tried): %s (last error: %s)", len(tried), strings.Join(tried, "; "), sanitizeErrorText(lastErr.Error()))
	}
	return nil, 0, fmt.Errorf("all probe URLs failed, last error: %w", lastErr)
}

func (s *proxyProbeService) probeWithURL(ctx context.Context, client *http.Client, url string, parser string) (*service.ProxyExitInfo, int64, error) {
	startTime := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req) // #nosec G704 -- probe URL is from constant list, not user input
	if err != nil {
		return nil, 0, fmt.Errorf("proxy connection failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	latencyMs := time.Since(startTime).Milliseconds()

	// "status" probe only checks connectivity; 4xx can still mean "reachable" (e.g. missing auth).
	if parser == "status" {
		if resp.StatusCode == http.StatusProxyAuthRequired {
			return nil, latencyMs, fmt.Errorf("proxy authentication required (407)")
		}
		if resp.StatusCode >= 500 {
			return nil, latencyMs, fmt.Errorf("request failed with status: %d", resp.StatusCode)
		}
		return &service.ProxyExitInfo{}, latencyMs, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, latencyMs, fmt.Errorf("request failed with status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, latencyMs, fmt.Errorf("failed to read response: %w", err)
	}

	switch parser {
	case "ip-api":
		return s.parseIPAPI(body, latencyMs)
	case "httpbin":
		return s.parseHTTPBin(body, latencyMs)
	case "ipify":
		return s.parseIPify(body, latencyMs)
	default:
		return nil, latencyMs, fmt.Errorf("unknown parser: %s", parser)
	}
}

func (s *proxyProbeService) parseIPAPI(body []byte, latencyMs int64) (*service.ProxyExitInfo, int64, error) {
	var ipInfo struct {
		Status      string `json:"status"`
		Message     string `json:"message"`
		Query       string `json:"query"`
		City        string `json:"city"`
		Region      string `json:"region"`
		RegionName  string `json:"regionName"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
	}

	if err := json.Unmarshal(body, &ipInfo); err != nil {
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return nil, latencyMs, fmt.Errorf("failed to parse response: %w (body: %s)", err, preview)
	}
	if strings.ToLower(ipInfo.Status) != "success" {
		if ipInfo.Message == "" {
			ipInfo.Message = "ip-api request failed"
		}
		return nil, latencyMs, fmt.Errorf("ip-api request failed: %s", ipInfo.Message)
	}

	region := ipInfo.RegionName
	if region == "" {
		region = ipInfo.Region
	}
	return &service.ProxyExitInfo{
		IP:          ipInfo.Query,
		City:        ipInfo.City,
		Region:      region,
		Country:     ipInfo.Country,
		CountryCode: ipInfo.CountryCode,
	}, latencyMs, nil
}

func (s *proxyProbeService) parseHTTPBin(body []byte, latencyMs int64) (*service.ProxyExitInfo, int64, error) {
	// httpbin.org/ip 返回格式: {"origin": "1.2.3.4"}
	var result struct {
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, latencyMs, fmt.Errorf("failed to parse httpbin response: %w", err)
	}
	if result.Origin == "" {
		return nil, latencyMs, fmt.Errorf("httpbin: no IP found in response")
	}
	return &service.ProxyExitInfo{
		IP: result.Origin,
	}, latencyMs, nil
}

func (s *proxyProbeService) parseIPify(body []byte, latencyMs int64) (*service.ProxyExitInfo, int64, error) {
	// api.ipify.org?format=json 返回格式: {"ip":"1.2.3.4"}
	var result struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, latencyMs, fmt.Errorf("failed to parse ipify response: %w", err)
	}
	if result.IP == "" {
		return nil, latencyMs, fmt.Errorf("ipify: no IP found in response")
	}
	return &service.ProxyExitInfo{
		IP: result.IP,
	}, latencyMs, nil
}

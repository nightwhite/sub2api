package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

// Compact forwards /v1/responses/compact requests.
// - stream=true: return streaming response
// - stream=false or missing: return final upstream JSON response as-is
// It must not synthesize compaction payload fields locally.
func (s *OpenAIGatewayService) Compact(ctx context.Context, c *gin.Context, account *Account, originalBody []byte) (int, string, []byte, *OpenAIForwardResult, error) {
	if len(originalBody) == 0 {
		if c != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Request body is empty"}})
		}
		return http.StatusBadRequest, "application/json; charset=utf-8", nil, nil, errors.New("empty body")
	}

	var reqBody map[string]any
	if err := json.Unmarshal(originalBody, &reqBody); err != nil {
		if c != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Failed to parse request body"}})
		}
		return http.StatusBadRequest, "application/json; charset=utf-8", nil, nil, fmt.Errorf("parse body: %w", err)
	}

	requestPath := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		requestPath = c.Request.URL.Path
	}
	if requestPath == "" {
		requestPath = "/v1/responses/compact"
	}

	upstreamBody, err := json.Marshal(reqBody)
	if err != nil {
		if c != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "api_error", "message": "Failed to process request"}})
		}
		return http.StatusInternalServerError, "application/json; charset=utf-8", nil, nil, fmt.Errorf("serialize body: %w", err)
	}

	result, raw, err := s.forwardForCompactJSON(ctx, c, account, upstreamBody, requestPath)
	if err != nil {
		return 0, "", nil, nil, err
	}
	if result != nil && result.Stream {
		return 0, "", nil, result, nil
	}

	return http.StatusOK, "application/json; charset=utf-8", raw, result, nil
}

func (s *OpenAIGatewayService) forwardForCompactJSON(ctx context.Context, c *gin.Context, account *Account, body []byte, requestPath string) (*OpenAIForwardResult, []byte, error) {
	startTime := time.Now()

	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		if c != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Failed to parse request body"}})
		}
		return nil, nil, fmt.Errorf("parse request: %w", err)
	}

	reqModel, _ := reqBody["model"].(string)
	reqStream := false
	if rawStream, hasStream := reqBody["stream"]; hasStream {
		streamValue, ok := rawStream.(bool)
		if !ok {
			if c != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "stream must be a boolean"}})
			}
			return nil, nil, errors.New("stream must be a boolean")
		}
		reqStream = streamValue
	}
	clientRequestedStream := reqStream
	promptCacheKey := ""
	if v, ok := reqBody["prompt_cache_key"].(string); ok {
		promptCacheKey = strings.TrimSpace(v)
	}

	bodyModified := false
	originalModel := reqModel

	userAgent := ""
	if c != nil {
		userAgent = c.GetHeader("User-Agent")
	}
	isCodexCLI := openai.IsCodexCLIRequest(userAgent)
	isCompaction := strings.Contains(requestPath, "/responses/compact")

	isOAuthCompact := isCompaction && account != nil && account.Type == AccountTypeOAuth
	applyCompactionStreamPolicy := func() {
		if !isCompaction {
			return
		}
		// Compact stream policy:
		// - If client requests stream=true, always forward stream=true.
		// - Otherwise, OAuth upstream still requires stream=true, while API key/custom
		//   upstreams should forward stream=false explicitly for compatibility.
		if clientRequestedStream {
			if v, ok := reqBody["stream"].(bool); !ok || !v {
				reqBody["stream"] = true
				bodyModified = true
			}
			reqStream = true
			return
		}
		if isOAuthCompact {
			if v, ok := reqBody["stream"].(bool); !ok || !v {
				reqBody["stream"] = true
				bodyModified = true
			}
			reqStream = true
			return
		}
		if v, ok := reqBody["stream"].(bool); !ok || v {
			reqBody["stream"] = false
			bodyModified = true
		}
		reqStream = false
	}

	mappedModel := account.GetMappedModel(reqModel)
	if mappedModel != reqModel {
		reqBody["model"] = mappedModel
		bodyModified = true
	}

	// Normalize Codex model ids for OpenAI upstream consistency.
	if model, ok := reqBody["model"].(string); ok {
		normalizedModel := normalizeCodexModel(model)
		if normalizedModel != "" && normalizedModel != model {
			reqBody["model"] = normalizedModel
			mappedModel = normalizedModel
			bodyModified = true
		}
	}

	if account.Type == AccountTypeOAuth {
		codexResult := applyCodexOAuthTransform(reqBody, isCodexCLI, isCompaction)
		if codexResult.Modified {
			bodyModified = true
		}
		if codexResult.NormalizedModel != "" {
			mappedModel = codexResult.NormalizedModel
		}
		if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		}
	}

	// Apply compact stream policy once after all request-body transforms.
	applyCompactionStreamPolicy()

	// Compact endpoint is JSON: drop streaming-only hints and unsupported fields for non-CLI traffic.
	if !isCodexCLI {
		for _, unsupportedField := range []string{"prompt_cache_retention", "safety_identifier", "previous_response_id"} {
			if _, has := reqBody[unsupportedField]; has {
				delete(reqBody, unsupportedField)
				bodyModified = true
			}
		}
	}

	if bodyModified {
		var err error
		body, err = json.Marshal(reqBody)
		if err != nil {
			if c != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "api_error", "message": "Failed to process request"}})
			}
			return nil, nil, fmt.Errorf("serialize request body: %w", err)
		}
	}

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		// GetAccessToken already returns user-friendly errors upstream.
		return nil, nil, err
	}

	upstreamReq, err := s.buildUpstreamRequest(ctx, c, account, body, token, reqStream, promptCacheKey, isCodexCLI, requestPath)
	if err != nil {
		return nil, nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// Capture upstream request body for ops retry of this attempt.
	if c != nil {
		c.Set(OpsUpstreamRequestBodyKey, string(body))
	}

	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})

		// Always emit a structured warning log so operators can find failures even when the
		// client receives a generic 502 and we do not log upstream bodies.
		clientRequestID, _ := ctx.Value(ctxkey.ClientRequestID).(string)
		slog.Warn("openai_upstream_request_failed",
			"platform", PlatformOpenAI,
			"request_path", requestPath,
			"client_request_id", clientRequestID,
			"account_id", account.ID,
			"account_name", account.Name,
			"account_type", account.Type,
			"model", originalModel,
			"mapped_model", mappedModel,
			"stream", reqStream,
			"proxy_enabled", proxyURL != "",
			"elapsed_ms", time.Since(startTime).Milliseconds(),
			"error", safeErr,
		)

		if c != nil && !c.Writer.Written() {
			c.JSON(http.StatusBadGateway, gin.H{
				"error": gin.H{
					"type":    "upstream_error",
					"message": "Upstream request failed",
				},
			})
		}
		return nil, nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Failover path: let handler switch accounts.
		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "failover",
				Message:            strings.TrimSpace(extractUpstreamErrorMessage(respBody)),
			})
			s.handleFailoverSideEffects(ctx, resp, account)
			return nil, nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody}
		}

		// Non-failover: reuse the existing error mapping behavior (writes to client).
		_, err := s.handleErrorResponse(ctx, resp, c, account)
		return nil, nil, err
	}

	if clientRequestedStream {
		streamResult, err := s.handleStreamingResponse(ctx, resp, c, account, startTime, originalModel, mappedModel)
		if err != nil {
			return nil, nil, err
		}
		usage := streamResult.usage
		if usage == nil {
			usage = &OpenAIUsage{}
		}
		reasoningEffort := extractOpenAIReasoningEffort(reqBody, originalModel)
		return &OpenAIForwardResult{
			RequestID:       resp.Header.Get("x-request-id"),
			Usage:           *usage,
			Model:           originalModel,
			ReasoningEffort: reasoningEffort,
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    streamResult.firstTokenMs,
		}, nil, nil
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	finalBody := rawBody
	if account.Type == AccountTypeOAuth {
		bodyLooksLikeSSE := bytes.Contains(rawBody, []byte("data:")) || bytes.Contains(rawBody, []byte("event:"))
		if isEventStreamResponse(resp.Header) || bodyLooksLikeSSE {
			if extracted, ok := extractCodexFinalResponse(string(rawBody)); ok {
				finalBody = extracted
			} else {
				errMsg := "compact upstream returned stream payload without final response object"
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					Kind:               "invalid_upstream_response",
					Message:            errMsg,
				})
				if c != nil && !c.Writer.Written() {
					c.JSON(http.StatusBadGateway, gin.H{
						"error": gin.H{
							"type":    "upstream_error",
							"message": "Upstream returned an invalid compact response",
						},
					})
				}
				return nil, nil, errors.New(errMsg)
			}
		}
	}

	usage := parseOpenAIUsage(finalBody)
	if usage == nil {
		usage = &OpenAIUsage{}
	}

	if originalModel != mappedModel {
		finalBody = s.replaceModelInResponseBody(finalBody, mappedModel, originalModel)
	}

	reasoningEffort := extractOpenAIReasoningEffort(reqBody, originalModel)

	return &OpenAIForwardResult{
		RequestID:       resp.Header.Get("x-request-id"),
		Usage:           *usage,
		Model:           originalModel,
		ReasoningEffort: reasoningEffort,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, finalBody, nil
}

func parseOpenAIUsage(body []byte) *OpenAIUsage {
	if len(body) == 0 {
		return nil
	}
	var response struct {
		Usage struct {
			InputTokens       int `json:"input_tokens"`
			OutputTokens      int `json:"output_tokens"`
			InputTokenDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil
	}
	return &OpenAIUsage{
		InputTokens:          response.Usage.InputTokens,
		OutputTokens:         response.Usage.OutputTokens,
		CacheReadInputTokens: response.Usage.InputTokenDetails.CachedTokens,
	}
}

func (s *OpenAIGatewayService) encryptCompactionSummary(summary string) (string, error) {
	key, err := s.compactionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	plaintext := []byte(strings.TrimSpace(summary))
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	token := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(token), nil
}

func (s *OpenAIGatewayService) decryptCompactionSummary(encrypted string) (string, error) {
	key, err := s.compactionKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted_content")
	}
	nonce := raw[:gcm.NonceSize()]
	ciphertext := raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (s *OpenAIGatewayService) expandCompactionIntoInstructions(reqBody map[string]any) (bool, error) {
	if reqBody == nil {
		return false, nil
	}
	rawInput, ok := reqBody["input"]
	if !ok || rawInput == nil {
		return false, nil
	}
	input, ok := rawInput.([]any)
	if !ok || len(input) == 0 {
		return false, nil
	}

	summaries := make([]string, 0, 2)
	newInput := make([]any, 0, len(input))

	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			newInput = append(newInput, item)
			continue
		}
		typ, _ := m["type"].(string)
		if typ != "compaction" && typ != "compaction_summary" {
			newInput = append(newInput, item)
			continue
		}
		enc, _ := m["encrypted_content"].(string)
		enc = strings.TrimSpace(enc)
		if enc == "" {
			return false, errors.New("compaction item missing encrypted_content")
		}
		summary, err := s.decryptCompactionSummary(enc)
		if err != nil {
			return false, fmt.Errorf("invalid compaction encrypted_content: %w", err)
		}
		summary = strings.TrimSpace(summary)
		if summary != "" {
			summaries = append(summaries, summary)
		}
		// Drop compaction item from upstream payload; it is a server-side optimization token.
	}

	if len(summaries) == 0 {
		return false, nil
	}

	reqBody["input"] = newInput

	block := "Conversation history summary (from /v1/responses/compact):\n" + strings.Join(summaries, "\n\n")
	existing, _ := reqBody["instructions"].(string)
	existing = strings.TrimSpace(existing)
	if existing == "" {
		reqBody["instructions"] = block
		return true, nil
	}
	reqBody["instructions"] = existing + "\n\n" + block
	return true, nil
}

func (s *OpenAIGatewayService) compactionKey() ([]byte, error) {
	// Optional override for key rotation / dedicated compaction key.
	if v := strings.TrimSpace(os.Getenv("SUB2API_COMPACTION_KEY")); v != "" {
		if key, ok := parse32ByteKey(v); ok {
			return key, nil
		}
		return nil, errors.New("invalid SUB2API_COMPACTION_KEY (expect 32-byte hex/base64)")
	}

	// Prefer TOTP encryption key (already designed for persistent AES-256 encryption).
	if s != nil && s.cfg != nil {
		if v := strings.TrimSpace(s.cfg.Totp.EncryptionKey); v != "" {
			if key, ok := parse32ByteKey(v); ok {
				return key, nil
			}
			// TOTP key is documented as hex, but allow base64 too.
			return nil, errors.New("invalid totp.encryption_key (expect 32-byte hex/base64)")
		}
		if v := strings.TrimSpace(s.cfg.JWT.Secret); v != "" {
			sum := sha256.Sum256([]byte(v))
			return sum[:], nil
		}
	}

	// Last resort: generate per-process key (tokens won't survive restart).
	sum := sha256.Sum256([]byte("sub2api:compaction:ephemeral:" + time.Now().UTC().Format(time.RFC3339Nano)))
	log.Printf("Warning: compaction encryption key not configured; using ephemeral key (tokens won't survive restart)")
	return sum[:], nil
}

func parse32ByteKey(v string) ([]byte, bool) {
	s := strings.TrimSpace(v)
	if s == "" {
		return nil, false
	}

	// Hex-encoded 32 bytes (64 hex chars)
	if len(s) == 64 {
		if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
			return b, true
		}
	}

	// Base64-encoded 32 bytes
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, true
	}

	return nil, false
}

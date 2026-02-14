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

// Compact implements a "real" /v1/responses/compact behavior:
// - If upstream supports /responses/compact (API key accounts), pass through.
// - Otherwise, run a summarization request via /responses and wrap it into response.compaction.
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

	// Compact endpoint returns JSON to the client. For OAuth upstream (ChatGPT internal API),
	// we must request stream=true and convert SSE to JSON server-side.
	wantUpstreamStream := account != nil && account.Type == AccountTypeOAuth
	reqBody["stream"] = wantUpstreamStream

	// NOTE: We do not inject default "instructions". If the client does not provide
	// instructions, we pass the request through as-is (except stream normalization).

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

	// If upstream already returned response.compaction, pass through.
	var objProbe struct {
		Object string `json:"object"`
	}
	_ = json.Unmarshal(raw, &objProbe)
	if strings.TrimSpace(objProbe.Object) == "response.compaction" {
		return http.StatusOK, "application/json; charset=utf-8", raw, result, nil
	}

	// Otherwise, wrap as response.compaction.
	firstUserText := extractFirstUserText(reqBody["input"])
	summaryText := extractAssistantOutputText(raw)
	if strings.TrimSpace(summaryText) == "" {
		if c != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Upstream did not return compaction content"}})
		}
		return http.StatusBadGateway, "application/json; charset=utf-8", nil, nil, errors.New("upstream did not return compaction content")
	}

	encrypted, encErr := s.encryptCompactionSummary(summaryText)
	if encErr != nil {
		if c != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "api_error", "message": "Failed to encrypt compaction result"}})
		}
		return http.StatusInternalServerError, "application/json; charset=utf-8", nil, nil, fmt.Errorf("encrypt compaction: %w", encErr)
	}

	resp := map[string]any{
		"id":         "resp_" + randomHex(12),
		"object":     "response.compaction",
		"created_at": time.Now().Unix(),
		"output": []any{
			map[string]any{
				"id":     "msg_" + randomHex(12),
				"type":   "message",
				"status": "completed",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": firstUserText,
					},
				},
				"role": "user",
			},
			map[string]any{
				"id":                "cmp_" + randomHex(12),
				"type":              "compaction",
				"encrypted_content": encrypted,
			},
		},
		"usage": formatCompactUsage(&result.Usage),
	}

	b, err := json.Marshal(resp)
	if err != nil {
		if c != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "api_error", "message": "Failed to build compaction response"}})
		}
		return http.StatusInternalServerError, "application/json; charset=utf-8", nil, nil, fmt.Errorf("marshal compaction response: %w", err)
	}

	return http.StatusOK, "application/json; charset=utf-8", b, result, nil
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
	reqStream, _ := reqBody["stream"].(bool)
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

	// Compact endpoint returns JSON to the client. For OAuth upstream, stream must be true.
	wantUpstreamStream := account != nil && account.Type == AccountTypeOAuth
	if v, ok := reqBody["stream"].(bool); !ok || v != wantUpstreamStream {
		reqBody["stream"] = wantUpstreamStream
		reqStream = wantUpstreamStream
		bodyModified = true
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

func formatCompactUsage(usage *OpenAIUsage) map[string]any {
	if usage == nil {
		return map[string]any{
			"input_tokens":  0,
			"output_tokens": 0,
			"input_tokens_details": map[string]any{
				"cached_tokens": 0,
			},
			"output_tokens_details": map[string]any{
				"reasoning_tokens": 0,
			},
			"total_tokens": 0,
		}
	}
	total := usage.InputTokens + usage.OutputTokens
	return map[string]any{
		"input_tokens": usage.InputTokens,
		"input_tokens_details": map[string]any{
			"cached_tokens": usage.CacheReadInputTokens,
		},
		"output_tokens": usage.OutputTokens,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": 0,
		},
		"total_tokens": total,
	}
}

func extractFirstUserText(input any) string {
	items, ok := input.([]any)
	if !ok {
		return ""
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "" && typ != "message" {
			continue
		}
		role, _ := m["role"].(string)
		if role != "user" {
			continue
		}
		content := m["content"]
		switch v := content.(type) {
		case string:
			return v
		case []any:
			for _, part := range v {
				pm, ok := part.(map[string]any)
				if !ok {
					continue
				}
				pt, _ := pm["type"].(string)
				if pt == "input_text" || pt == "text" || pt == "output_text" {
					if text, _ := pm["text"].(string); strings.TrimSpace(text) != "" {
						return text
					}
				}
			}
		}
	}
	return ""
}

func extractAssistantOutputText(responseBody []byte) string {
	if len(responseBody) == 0 {
		return ""
	}
	var resp struct {
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(responseBody, &resp); err != nil {
		return ""
	}
	for _, item := range resp.Output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" && strings.TrimSpace(part.Text) != "" {
				return part.Text
			}
		}
	}
	return ""
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

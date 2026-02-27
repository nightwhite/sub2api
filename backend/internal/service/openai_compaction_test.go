package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type compactHTTPUpstreamStub struct {
	do func(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error)
}

func (s *compactHTTPUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	if s.do != nil {
		return s.do(req, proxyURL, accountID, accountConcurrency)
	}
	return nil, nil
}

func (s *compactHTTPUpstreamStub) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ bool) (*http.Response, error) {
	return nil, nil
}

func newCompactTestContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c, recorder
}

func newCompactTestService(stub *compactHTTPUpstreamStub) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		httpUpstream: stub,
		cfg:          &config.Config{},
	}
}

func newCompactTestAccount() *Account {
	return &Account{
		ID:          1,
		Name:        "compact-test-account",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-api-key"},
		Concurrency: 1,
	}
}

func newCompactOAuthTestAccount() *Account {
	return &Account{
		ID:       2,
		Name:     "compact-oauth-test-account",
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "test-oauth-token",
			"chatgpt_account_id": "chatgpt_account_test",
		},
		Concurrency: 1,
	}
}

func TestCompactionEncryptDecrypt_Roundtrip(t *testing.T) {
	svc := &OpenAIGatewayService{
		cfg: &config.Config{
			JWT: config.JWTConfig{Secret: "unit-test-jwt-secret"},
		},
	}

	token, err := svc.encryptCompactionSummary("hello world")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	got, err := svc.decryptCompactionSummary(token)
	require.NoError(t, err)
	require.Equal(t, "hello world", got)
}

func TestExpandCompactionIntoInstructions(t *testing.T) {
	svc := &OpenAIGatewayService{
		cfg: &config.Config{
			JWT: config.JWTConfig{Secret: "unit-test-jwt-secret"},
		},
	}

	token, err := svc.encryptCompactionSummary("SUMMARY")
	require.NoError(t, err)

	req := map[string]any{
		"instructions": "base",
		"input": []any{
			map[string]any{
				"type":              "compaction",
				"encrypted_content": token,
			},
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "hi"},
				},
			},
		},
	}

	changed, err := svc.expandCompactionIntoInstructions(req)
	require.NoError(t, err)
	require.True(t, changed)

	input, ok := req["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)

	instructions, _ := req["instructions"].(string)
	require.Contains(t, instructions, "Conversation history summary")
	require.Contains(t, instructions, "SUMMARY")
}

func TestApplyCodexOAuthTransform_CompactionDoesNotForceStreamWhenMissing(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.1",
		"input": []any{},
	}

	result := applyCodexOAuthTransform(reqBody, false, true)
	require.True(t, result.Modified)

	_, hasStream := reqBody["stream"]
	require.False(t, hasStream)
}

func TestCompact_PassesThroughUpstreamResponseAsIs(t *testing.T) {
	var forwardedBody []byte
	upstreamBody := `{"id":"resp_001","object":"response.compaction","output":[{"id":"cmp_001","type":"compaction","encrypted_content":"abc"}]}`

	stub := &compactHTTPUpstreamStub{
		do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			var err error
			forwardedBody, err = io.ReadAll(req.Body)
			require.NoError(t, err)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"X-Request-Id": []string{"req_compact_1"}},
				Body:       io.NopCloser(strings.NewReader(upstreamBody)),
			}, nil
		},
	}

	svc := newCompactTestService(stub)
	account := newCompactTestAccount()
	c, _ := newCompactTestContext("/v1/responses/compact")

	originalReq := []byte(`{"model":"gpt-5.1-codex-max","stream":false,"input":[{"role":"user","content":"Create page"}]}`)
	status, contentType, respBody, result, err := svc.Compact(context.Background(), c, account, originalReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "application/json; charset=utf-8", contentType)
	require.Equal(t, upstreamBody, string(respBody))
	require.NotContains(t, string(respBody), `"usage"`)
	require.NotNil(t, result)
	require.Equal(t, "req_compact_1", result.RequestID)

	var forwarded map[string]any
	require.NoError(t, json.Unmarshal(forwardedBody, &forwarded))
	streamValue, hasStream := forwarded["stream"]
	require.True(t, hasStream)
	streamBool, ok := streamValue.(bool)
	require.True(t, ok)
	require.False(t, streamBool)
}

func TestCompact_DoesNotBuildLocalCompactionWhenUpstreamObjectIsNotCompaction(t *testing.T) {
	upstreamBody := `{"id":"resp_002","object":"response","status":"completed","output":[]}`
	stub := &compactHTTPUpstreamStub{
		do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"X-Request-Id": []string{"req_compact_2"}},
				Body:       io.NopCloser(strings.NewReader(upstreamBody)),
			}, nil
		},
	}

	svc := newCompactTestService(stub)
	account := newCompactTestAccount()
	c, _ := newCompactTestContext("/v1/responses/compact")

	originalReq := []byte(`{"model":"gpt-5.1-codex-max","input":[{"role":"user","content":"hello"}]}`)
	status, _, respBody, _, err := svc.Compact(context.Background(), c, account, originalReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, upstreamBody, string(respBody))
	require.NotContains(t, string(respBody), `"object":"response.compaction"`)
	require.NotContains(t, string(respBody), `"created_at"`)
}

func TestCompact_SetsStreamFalseBeforeForwarding(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{
			name: "stream_false",
			body: `{"model":"gpt-5.1","stream":false,"input":[{"role":"user","content":"hi"}]}`,
		},
		{
			name: "stream_missing",
			body: `{"model":"gpt-5.1","input":[{"role":"user","content":"hi"}]}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var forwardedBody []byte
			stub := &compactHTTPUpstreamStub{
				do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
					var err error
					forwardedBody, err = io.ReadAll(req.Body)
					require.NoError(t, err)
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"X-Request-Id": []string{"req_compact_stream"}},
						Body:       io.NopCloser(strings.NewReader(`{"id":"resp_003","object":"response.compaction","output":[]}`)),
					}, nil
				},
			}

			svc := newCompactTestService(stub)
			account := newCompactTestAccount()
			c, _ := newCompactTestContext("/v1/responses/compact")

			_, _, _, _, err := svc.Compact(context.Background(), c, account, []byte(tc.body))
			require.NoError(t, err)

			var forwarded map[string]any
			require.NoError(t, json.Unmarshal(forwardedBody, &forwarded))
			streamValue, hasStream := forwarded["stream"]
			require.True(t, hasStream)
			streamBool, ok := streamValue.(bool)
			require.True(t, ok)
			require.False(t, streamBool)
		})
	}
}

func TestCompact_APIKey_StreamTrue_ForwardsAndReturnsSSE(t *testing.T) {
	var forwardedBody []byte
	sseBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":2,"input_tokens_details":{"cached_tokens":0}}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	stub := &compactHTTPUpstreamStub{
		do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			var err error
			forwardedBody, err = io.ReadAll(req.Body)
			require.NoError(t, err)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req_compact_stream_true"},
				},
				Body: io.NopCloser(strings.NewReader(sseBody)),
			}, nil
		},
	}

	svc := newCompactTestService(stub)
	account := newCompactTestAccount()
	c, recorder := newCompactTestContext("/v1/responses/compact")

	originalReq := []byte(`{"model":"gpt-5.1","stream":true,"input":[{"role":"user","content":"hi"}]}`)
	status, contentType, respBody, result, err := svc.Compact(context.Background(), c, account, originalReq)
	require.NoError(t, err)
	require.Equal(t, 0, status)
	require.Equal(t, "", contentType)
	require.Nil(t, respBody)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, "req_compact_stream_true", result.RequestID)
	require.Equal(t, 1, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)

	var forwarded map[string]any
	require.NoError(t, json.Unmarshal(forwardedBody, &forwarded))
	streamValue, hasStream := forwarded["stream"]
	require.True(t, hasStream)
	streamBool, ok := streamValue.(bool)
	require.True(t, ok)
	require.True(t, streamBool)

	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Body.String(), `data: {"type":"response.output_text.delta","delta":"hi"}`)
	require.Contains(t, recorder.Body.String(), `data: [DONE]`)
}

func TestCompact_OAuth_ForcesStreamParameterBeforeForwarding(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{
			name: "missing_stream",
			body: `{"model":"gpt-5.1","input":[{"role":"user","content":"hi"}]}`,
		},
		{
			name: "stream_false",
			body: `{"model":"gpt-5.1","stream":false,"input":[{"role":"user","content":"hi"}]}`,
		},
		{
			name: "stream_true",
			body: `{"model":"gpt-5.1","stream":true,"input":[{"role":"user","content":"hi"}]}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var forwardedBody []byte
			stub := &compactHTTPUpstreamStub{
				do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
					var err error
					forwardedBody, err = io.ReadAll(req.Body)
					require.NoError(t, err)
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"X-Request-Id": []string{"req_compact_oauth_stream"}},
						Body:       io.NopCloser(strings.NewReader(`{"id":"resp_004","object":"response.compaction","output":[]}`)),
					}, nil
				},
			}

			svc := newCompactTestService(stub)
			account := newCompactOAuthTestAccount()
			c, _ := newCompactTestContext("/v1/responses/compact")

			_, _, _, _, err := svc.Compact(context.Background(), c, account, []byte(tc.body))
			require.NoError(t, err)

			var forwarded map[string]any
			require.NoError(t, json.Unmarshal(forwardedBody, &forwarded))
			streamValue, hasStream := forwarded["stream"]
			require.True(t, hasStream)
			streamBool, ok := streamValue.(bool)
			require.True(t, ok)
			require.True(t, streamBool)
		})
	}
}

func TestCompact_RejectsNonBooleanStream(t *testing.T) {
	stub := &compactHTTPUpstreamStub{
		do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"resp_invalid_stream"}`)),
			}, nil
		},
	}

	svc := newCompactTestService(stub)
	account := newCompactTestAccount()
	c, recorder := newCompactTestContext("/v1/responses/compact")

	_, _, _, _, err := svc.Compact(context.Background(), c, account, []byte(`{"model":"gpt-5.1","stream":"true","input":[]}`))
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "stream must be a boolean")
}

func TestParseOpenAIStreamParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reqBody   map[string]any
		wantValue bool
		wantErr   bool
	}{
		{
			name:      "missing stream defaults false",
			reqBody:   map[string]any{"model": "gpt-5.1"},
			wantValue: false,
			wantErr:   false,
		},
		{
			name:      "stream true",
			reqBody:   map[string]any{"stream": true},
			wantValue: true,
			wantErr:   false,
		},
		{
			name:      "stream false",
			reqBody:   map[string]any{"stream": false},
			wantValue: false,
			wantErr:   false,
		},
		{
			name:      "stream invalid type",
			reqBody:   map[string]any{"stream": "false"},
			wantValue: false,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOpenAIStreamParam(tc.reqBody)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantValue, got)
		})
	}
}

func TestCompact_OAuth_NonStreamSSEWithoutFinalResponseReturnsBadGateway(t *testing.T) {
	sseBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	stub := &compactHTTPUpstreamStub{
		do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"req_compact_oauth_invalid_final"},
				},
				Body: io.NopCloser(strings.NewReader(sseBody)),
			}, nil
		},
	}

	svc := newCompactTestService(stub)
	account := newCompactOAuthTestAccount()
	c, recorder := newCompactTestContext("/v1/responses/compact")

	_, _, _, _, err := svc.Compact(context.Background(), c, account, []byte(`{"model":"gpt-5.1","stream":false,"input":[{"role":"user","content":"hi"}]}`))
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Upstream returned an invalid compact response")
}

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayService_APIKeyPassthrough_ImageIntentPreservesGateAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.4","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2","size":"2048x1152"}],"input":"draw"}`)

	t.Run("disabled group rejects before upstream", func(t *testing.T) {
		upstream := &httpUpstreamRecorder{}
		svc := newOpenAIImageGenerationControlTestService(upstream)
		c, recorder := newOpenAIImageGenerationControlTestContext(false, "curl/8.0")
		account := newOpenAIImageGenerationControlTestAccount()
		account.Extra = map[string]any{"openai_passthrough": true}

		// 模型名直接命中的生图意图无法通过剥离工具消除，必须在到达上游前 403。
		modelIntentBody := []byte(`{"model":"gpt-image-2","stream":false,"input":"draw"}`)
		result, err := svc.Forward(context.Background(), c, account, modelIntentBody)

		require.Error(t, err)
		require.Nil(t, result)
		require.Equal(t, http.StatusForbidden, recorder.Code)
		require.Equal(t, "permission_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
		require.Nil(t, upstream.lastReq)
	})

	t.Run("disabled group strips image tool and forwards", func(t *testing.T) {
		// fork 生图过滤语义：工具声明触发的意图先剥离生图工具再放行，
		// 非生图部分请求不受禁用分组影响。
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"resp_stripped","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":2}}`,
			)),
		}}
		svc := newOpenAIImageGenerationControlTestService(upstream)
		c, recorder := newOpenAIImageGenerationControlTestContext(false, "curl/8.0")
		account := newOpenAIImageGenerationControlTestAccount()
		account.Extra = map[string]any{"openai_passthrough": true}

		result, err := svc.Forward(context.Background(), c, account, body)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.NotNil(t, upstream.lastReq)
		require.Equal(t, 0, result.ImageCount)
		var forwarded map[string]any
		require.NoError(t, json.Unmarshal(upstream.lastBody, &forwarded))
		require.False(t, hasOpenAIImageGenerationTool(forwarded), "image tool must be stripped before upstream")
	})

	t.Run("allowed group keeps image billing", func(t *testing.T) {
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"output":[{"id":"ig_1","type":"image_generation_call","result":"final-image","size":"2048x1152"}],"usage":{"input_tokens":1,"output_tokens":2}}`,
			)),
		}}
		svc := newOpenAIImageGenerationControlTestService(upstream)
		c, _ := newOpenAIImageGenerationControlTestContext(true, "curl/8.0")
		account := newOpenAIImageGenerationControlTestAccount()
		account.Extra = map[string]any{"openai_passthrough": true}

		result, err := svc.Forward(context.Background(), c, account, body)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, upstream.lastReq)
		require.Equal(t, body, upstream.lastBody)
		require.Equal(t, 1, result.ImageCount)
		require.Equal(t, "gpt-image-2", result.BillingModel)
		require.Equal(t, "2K", result.ImageSize)
		require.Equal(t, "2048x1152", result.ImageInputSize)
	})
}

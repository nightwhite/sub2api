package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareOpsRequestBodyForQueue_PreserveFullCapsPayload(t *testing.T) {
	raw := []byte(strings.Repeat("x", opsMaxFullExceptionPayloadSize+2048))

	requestBodyJSON, truncated, requestBodyBytes := PrepareOpsRequestBodyForQueue(raw, true)
	require.NotNil(t, requestBodyBytes)
	require.Equal(t, len(raw), *requestBodyBytes)
	require.True(t, truncated)
	require.NotNil(t, requestBodyJSON)

	var stored string
	require.NoError(t, json.Unmarshal([]byte(*requestBodyJSON), &stored))
	require.Len(t, stored, opsMaxFullExceptionPayloadSize)
}

func TestPrepareOpsRequestBodyForQueue_PreserveFullSmallPayload(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.1","input":[{"role":"user","content":"hello"}]}`)

	requestBodyJSON, truncated, requestBodyBytes := PrepareOpsRequestBodyForQueue(raw, true)
	require.NotNil(t, requestBodyBytes)
	require.Equal(t, len(raw), *requestBodyBytes)
	require.False(t, truncated)
	require.NotNil(t, requestBodyJSON)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(*requestBodyJSON), &decoded))
	require.Equal(t, "gpt-5.1", decoded["model"])
}

package logredact

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactText_CommonSecretKeys(t *testing.T) {
	input := "authorization=Bearer abc api_key=key123 x-api-key:xyz proxy-authorization=Basic foo"

	output := RedactText(input)
	require.Contains(t, output, "authorization=***")
	require.Contains(t, output, "api_key=***")
	require.Contains(t, output, "x-api-key:***")
	require.Contains(t, output, "proxy-authorization=***")
	require.NotContains(t, output, "Bearer abc")
	require.NotContains(t, output, "key123")
	require.NotContains(t, output, "xyz")
	require.NotContains(t, output, "Basic foo")
}

func TestRedactMap_CommonSecretKeys(t *testing.T) {
	input := map[string]any{
		"authorization": "Bearer abc",
		"api_key":       "key123",
		"x-api-key":     "xyz",
		"safe":          "ok",
	}

	output := RedactMap(input)
	require.Equal(t, "***", output["authorization"])
	require.Equal(t, "***", output["api_key"])
	require.Equal(t, "***", output["x-api-key"])
	require.Equal(t, "ok", output["safe"])
}

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

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

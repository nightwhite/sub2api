package handler

import "testing"

func TestOpenAIGatewayHandlerParseStreamParam(t *testing.T) {
	t.Parallel()

	h := &OpenAIGatewayHandler{}
	tests := []struct {
		name      string
		reqBody   map[string]any
		wantValue bool
		wantErr   bool
	}{
		{
			name:      "missing stream defaults false",
			reqBody:   map[string]any{"model": "gpt-5"},
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
			reqBody:   map[string]any{"stream": "true"},
			wantValue: false,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.parseStreamParam(tc.reqBody)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if got != tc.wantValue {
				t.Fatalf("expected %v, got %v", tc.wantValue, got)
			}
		})
	}
}

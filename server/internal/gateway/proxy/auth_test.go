package proxy

import (
	"net/http"
	"testing"
)

func TestExtractGatewayKey(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
		wantOK  bool
	}{
		{
			name: "openai bearer",
			headers: map[string]string{
				"Authorization": "Bearer mgw_123",
			},
			want:   "mgw_123",
			wantOK: true,
		},
		{
			name: "anthropic api key",
			headers: map[string]string{
				"x-api-key": "mgw_456",
			},
			want:   "mgw_456",
			wantOK: true,
		},
		{
			name: "rejects non gateway key",
			headers: map[string]string{
				"Authorization": "Bearer sk-proj-123",
			},
			wantOK: false,
		},
		{
			name: "rejects ingest key",
			headers: map[string]string{
				"Authorization": "Bearer mig_123",
			},
			wantOK: false,
		},
		{
			name:   "missing",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			got, ok := ExtractGatewayKey(req)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProtocolForRequest(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/v1/models", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if got := ProtocolForRequest(req, SurfaceModels); got != ProtocolOpenAI {
		t.Fatalf("default models protocol = %q, want %q", got, ProtocolOpenAI)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	if got := ProtocolForRequest(req, SurfaceModels); got != ProtocolAnthropic {
		t.Fatalf("anthropic models protocol = %q, want %q", got, ProtocolAnthropic)
	}
}

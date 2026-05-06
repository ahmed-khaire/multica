package proxy

import "testing"

func TestParseProviderModelRouting(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		headerBackend string
		wantBackend   string
		wantModel     string
		wantSource    string
		wantConflict  bool
	}{
		{
			name:        "prefixed",
			model:       "openrouter:anthropic/claude-sonnet-4",
			wantBackend: "openrouter",
			wantModel:   "anthropic/claude-sonnet-4",
			wantSource:  "model_prefix",
		},
		{
			name:       "unprefixed",
			model:      "gpt-4.1",
			wantModel:  "gpt-4.1",
			wantSource: "default",
		},
		{
			name:          "header only",
			model:         "gpt-4.1",
			headerBackend: "groq",
			wantBackend:   "groq",
			wantModel:     "gpt-4.1",
			wantSource:    "header",
		},
		{
			name:          "matching header and prefix",
			model:         "groq:llama-3.3-70b",
			headerBackend: "groq",
			wantBackend:   "groq",
			wantModel:     "llama-3.3-70b",
			wantSource:    "header_and_model_prefix",
		},
		{
			name:          "conflicting header and prefix",
			model:         "groq:llama-3.3-70b",
			headerBackend: "openrouter",
			wantConflict:  true,
		},
		{
			name:          "trims whitespace",
			model:         " local : qwen-coder ",
			headerBackend: " local ",
			wantBackend:   "local",
			wantModel:     "qwen-coder",
			wantSource:    "header_and_model_prefix",
		},
		{
			name:       "empty prefix is treated as model",
			model:      ":not-a-prefix",
			wantModel:  ":not-a-prefix",
			wantSource: "default",
		},
		{
			name:       "empty suffix is treated as model",
			model:      "local:",
			wantModel:  "local:",
			wantSource: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseModelRouting(tt.model, tt.headerBackend)
			if tt.wantConflict {
				if err == nil {
					t.Fatal("expected conflict error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseModelRouting: %v", err)
			}
			if got.BackendSlug != tt.wantBackend || got.ForwardedModel != tt.wantModel || got.Source != tt.wantSource {
				t.Fatalf("routing = %#v, want backend=%q model=%q source=%q", got, tt.wantBackend, tt.wantModel, tt.wantSource)
			}
		})
	}
}

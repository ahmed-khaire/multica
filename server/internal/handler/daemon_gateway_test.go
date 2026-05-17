package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestSubscriptionProviderForRuntimeProvider(t *testing.T) {
	tests := map[string]string{
		"codex":  "codex",
		"claude": "claude_code",
		"other":  "",
	}
	for input, want := range tests {
		if got := subscriptionProviderForRuntimeProvider(input); got != want {
			t.Fatalf("subscriptionProviderForRuntimeProvider(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGatewayRuntimeRequestJobDecodesRequestBody(t *testing.T) {
	row := db.GatewayRuntimeRequest{
		ID:           gatewayTestUUID(1),
		WorkspaceID:  gatewayTestUUID(2),
		BackendID:    gatewayTestUUID(3),
		CredentialID: gatewayTestUUID(4),
		Provider:     "codex",
		Surface:      "openai_chat_completions",
		RequestBody:  []byte(`{"model":"gpt-5.3-codex","stream":true}`),
	}

	job := gatewayRuntimeRequestJob(row)
	if job.Type != gatewayJobTypeRuntimeRequest {
		t.Fatalf("Type = %q, want %q", job.Type, gatewayJobTypeRuntimeRequest)
	}
	if job.SubscriptionProvider != "codex" {
		t.Fatalf("SubscriptionProvider = %q, want codex", job.SubscriptionProvider)
	}
	if job.Surface != "openai_chat_completions" {
		t.Fatalf("Surface = %q, want openai_chat_completions", job.Surface)
	}
	if job.RequestBody["model"] != "gpt-5.3-codex" || job.RequestBody["stream"] != true {
		t.Fatalf("RequestBody = %#v, want decoded body", job.RequestBody)
	}
}

func gatewayTestUUID(seed byte) pgtype.UUID {
	var bytes [16]byte
	bytes[15] = seed
	return pgtype.UUID{Bytes: bytes, Valid: true}
}

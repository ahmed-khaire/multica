package keyring

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

func testBox(t *testing.T) *secrets.Box {
	t.Helper()
	key := bytes.Repeat([]byte{3}, 32)
	box, err := secrets.NewBox(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewBox returned error: %v", err)
	}
	return box
}

func TestGenerateGatewayKeyFormat(t *testing.T) {
	t.Parallel()

	key, err := GenerateGatewayKey()
	if err != nil {
		t.Fatalf("GenerateGatewayKey returned error: %v", err)
	}
	if !strings.HasPrefix(key, Prefix) {
		t.Fatalf("expected prefix %q, got %q", Prefix, key)
	}
	if len(key) != len(Prefix)+40 {
		t.Fatalf("unexpected key length: %d", len(key))
	}
}

func TestPrepareGatewayKeyStoresHashAndEncryptedValue(t *testing.T) {
	t.Parallel()

	prepared, err := PrepareNewGatewayKey(testBox(t))
	if err != nil {
		t.Fatalf("PrepareNewGatewayKey returned error: %v", err)
	}
	if prepared.Raw == "" {
		t.Fatal("expected raw key for one-time CLI output")
	}
	if prepared.Hash == "" || prepared.Hash == prepared.Raw {
		t.Fatalf("invalid hash: %q", prepared.Hash)
	}
	if prepared.DisplayPrefix != prepared.Raw[:12] {
		t.Fatalf("display prefix mismatch: got %q want %q", prepared.DisplayPrefix, prepared.Raw[:12])
	}
	if bytes.Contains(prepared.Encrypted, []byte(prepared.Raw)) {
		t.Fatalf("encrypted value contains raw key: %q", prepared.Encrypted)
	}
}

func TestDecryptStoredGatewayKey(t *testing.T) {
	t.Parallel()

	box := testBox(t)
	prepared, err := PrepareNewGatewayKey(box)
	if err != nil {
		t.Fatalf("PrepareNewGatewayKey returned error: %v", err)
	}

	got, err := DecryptStoredGatewayKey(box, prepared.Encrypted)
	if err != nil {
		t.Fatalf("DecryptStoredGatewayKey returned error: %v", err)
	}
	if got != prepared.Raw {
		t.Fatalf("raw key mismatch: got %q want %q", got, prepared.Raw)
	}
}

func TestGenerateGatewayKeyProducesUniqueValues(t *testing.T) {
	t.Parallel()

	a, err := GenerateGatewayKey()
	if err != nil {
		t.Fatalf("GenerateGatewayKey a returned error: %v", err)
	}
	b, err := GenerateGatewayKey()
	if err != nil {
		t.Fatalf("GenerateGatewayKey b returned error: %v", err)
	}
	if a == b {
		t.Fatal("expected distinct gateway keys")
	}
}

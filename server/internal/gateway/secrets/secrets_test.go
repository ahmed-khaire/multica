package secrets

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	key := bytes.Repeat([]byte{7}, 32)
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()

	box, err := NewBox(testKey(t))
	if err != nil {
		t.Fatalf("NewBox returned error: %v", err)
	}

	ciphertext, err := box.EncryptString("sk-proj-secret")
	if err != nil {
		t.Fatalf("EncryptString returned error: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("sk-proj-secret")) {
		t.Fatalf("ciphertext contains plaintext: %q", ciphertext)
	}

	plaintext, err := box.DecryptString(ciphertext)
	if err != nil {
		t.Fatalf("DecryptString returned error: %v", err)
	}
	if plaintext != "sk-proj-secret" {
		t.Fatalf("plaintext mismatch: got %q", plaintext)
	}
}

func TestNewBoxRejectsEmptyKey(t *testing.T) {
	t.Parallel()

	_, err := NewBox("")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestNewBoxRejectsWrongKeyLength(t *testing.T) {
	t.Parallel()

	_, err := NewBox(base64.StdEncoding.EncodeToString([]byte("short")))
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	box, err := NewBox(testKey(t))
	if err != nil {
		t.Fatalf("NewBox returned error: %v", err)
	}

	ciphertext, err := box.EncryptString("secret")
	if err != nil {
		t.Fatalf("EncryptString returned error: %v", err)
	}
	ciphertext[len(ciphertext)-1] ^= 0x01

	_, err = box.DecryptString(ciphertext)
	if err == nil {
		t.Fatal("expected tampered ciphertext to fail")
	}
}

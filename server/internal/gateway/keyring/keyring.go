package keyring

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

const (
	Prefix       = "mgw_"
	randomBytes  = 20
	prefixLength = 12
)

type PreparedKey struct {
	Raw           string
	Hash          string
	Encrypted     []byte
	DisplayPrefix string
}

func GenerateGatewayKey() (string, error) {
	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate gateway key: %w", err)
	}
	return Prefix + hex.EncodeToString(b), nil
}

func PrepareNewGatewayKey(box *secrets.Box) (PreparedKey, error) {
	raw, err := GenerateGatewayKey()
	if err != nil {
		return PreparedKey{}, err
	}

	encrypted, err := box.EncryptString(raw)
	if err != nil {
		return PreparedKey{}, fmt.Errorf("encrypt gateway key: %w", err)
	}

	return PreparedKey{
		Raw:           raw,
		Hash:          HashGatewayKey(raw),
		Encrypted:     encrypted,
		DisplayPrefix: raw[:prefixLength],
	}, nil
}

func HashGatewayKey(raw string) string {
	return auth.HashToken(raw)
}

func DecryptStoredGatewayKey(box *secrets.Box, encrypted []byte) (string, error) {
	raw, err := box.DecryptString(encrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt gateway key: %w", err)
	}
	return raw, nil
}

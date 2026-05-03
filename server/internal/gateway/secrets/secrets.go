package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	EnvKeyName = "MULTICA_GATEWAY_SECRET_KEY"
	keyBytes   = 32
	nonceBytes = 12
)

type Box struct {
	aead cipher.AEAD
}

func FromEnv() (*Box, error) {
	return NewBox(os.Getenv(EnvKeyName))
}

func NewBox(encodedKey string) (*Box, error) {
	if encodedKey == "" {
		return nil, fmt.Errorf("%s is required", EnvKeyName)
	}

	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", EnvKeyName, err)
	}
	if len(key) != keyBytes {
		return nil, fmt.Errorf("%s must decode to %d bytes", EnvKeyName, keyBytes)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	return &Box{aead: aead}, nil
}

func (b *Box) EncryptString(plaintext string) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("secrets box is not initialized")
	}

	nonce := make([]byte, nonceBytes)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	sealed := b.aead.Seal(nil, nonce, []byte(plaintext), nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

func (b *Box) DecryptString(ciphertext []byte) (string, error) {
	if b == nil || b.aead == nil {
		return "", errors.New("secrets box is not initialized")
	}
	if len(ciphertext) <= nonceBytes {
		return "", errors.New("ciphertext is too short")
	}

	nonce := ciphertext[:nonceBytes]
	sealed := ciphertext[nonceBytes:]
	plaintext, err := b.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}

	return string(plaintext), nil
}

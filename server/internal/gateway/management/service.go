package management

import (
	"errors"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

var (
	ErrInvalidCapturePolicy       = errors.New("invalid gateway capture policy")
	ErrInvalidGatewayBackend      = errors.New("invalid gateway backend")
	ErrGatewaySecretNotConfigured = errors.New("gateway secret key is not configured")
	ErrGatewayBackendNotFound     = errors.New("gateway backend not found")
	ErrGatewayKeyNotFound         = errors.New("gateway key not found")
)

func ValidateCapturePolicy(policy string) error {
	switch policy {
	case CaptureMetadataOnly, CaptureRedactedContent, CaptureFullContent:
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrInvalidCapturePolicy, policy)
	}
}

func CredentialHint(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) < 13 {
		return "****"
	}
	return secret[:8] + "..." + secret[len(secret)-4:]
}

func BuildGatewayURLs(serverBaseURL string) GatewayURLs {
	baseURL := strings.TrimRight(strings.TrimSpace(serverBaseURL), "/")
	return GatewayURLs{
		OpenAIBaseURL:    baseURL + "/v1",
		AnthropicBaseURL: baseURL,
	}
}

func normalizeSecretError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), secrets.EnvKeyName) {
		return ErrGatewaySecretNotConfigured
	}
	return err
}

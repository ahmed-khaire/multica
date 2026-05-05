package proxy

import (
	"errors"
	"fmt"
	"net/http"
)

var (
	ErrDefaultBackendNotConfigured = errors.New("gateway default backend is not configured")
	ErrBackendDisabled             = errors.New("gateway backend is disabled")
	ErrIncompatibleBackend         = errors.New("gateway backend is not compatible with requested protocol")
	ErrGatewaySecretNotConfigured  = errors.New("gateway secret key is not configured")
	ErrProviderRiskBlocked         = errors.New("gateway provider risk blocks this request")
)

type GatewayError struct {
	StatusCode     int
	PublicMessage  string
	ErrorType      string
	Code           string
	Cause          error
	ResourceType   string
	ResourceID     string
	ResourceLabel  string
	ProviderRiskID string
}

func (e GatewayError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.PublicMessage, e.Cause)
	}
	return e.PublicMessage
}

func (e GatewayError) Unwrap() error {
	return e.Cause
}

func ProviderErrorBody(protocol, message, errorType, code string) map[string]any {
	if protocol == ProtocolAnthropic {
		return map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    errorType,
				"message": message,
			},
		}
	}
	return map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errorType,
			"code":    code,
		},
	}
}

func AuthenticationError(message string, cause error) GatewayError {
	return GatewayError{
		StatusCode:    http.StatusUnauthorized,
		PublicMessage: message,
		ErrorType:     "authentication_error",
		Code:          "gateway_authentication_failed",
		Cause:         cause,
	}
}

func RoutingError(status int, message, code string, cause error) GatewayError {
	return GatewayError{
		StatusCode:    status,
		PublicMessage: message,
		ErrorType:     "invalid_request_error",
		Code:          code,
		Cause:         cause,
	}
}

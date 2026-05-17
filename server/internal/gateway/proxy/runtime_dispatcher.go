package proxy

import (
	"context"
	"errors"
	"net/http"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const runtimeRequestPollInterval = 200 * time.Millisecond

var (
	errSubscriptionCredentialInactive   = errors.New("gateway subscription credential is not active")
	errSubscriptionRuntimeUnavailable   = errors.New("gateway subscription runtime is not connected")
	errSubscriptionProviderUnsupported  = errors.New("gateway subscription provider is unsupported")
	errSubscriptionStreamingUnsupported = errors.New("gateway subscription streaming is not supported")
)

func (s *Service) dispatchRuntimeRequest(ctx context.Context, w http.ResponseWriter, authCtx AuthContext, target BackendTarget, summary RequestSummary) (ProxyResult, error) {
	if s.Queries == nil {
		return ProxyResult{}, RoutingError(http.StatusServiceUnavailable, "gateway runtime dispatcher is not configured", "gateway_runtime_dispatcher_unavailable", errSubscriptionRuntimeUnavailable)
	}
	if target.CredentialID == "" {
		return ProxyResult{}, RoutingError(http.StatusServiceUnavailable, "gateway subscription credential is not active", "gateway_subscription_credential_inactive", errSubscriptionCredentialInactive)
	}
	if summary.Stream {
		return ProxyResult{}, RoutingError(http.StatusBadRequest, "gateway subscription streaming is not supported yet", "gateway_subscription_streaming_unsupported", errSubscriptionStreamingUnsupported)
	}
	runtimeProvider := RuntimeProviderForSubscriptionProvider(target.SubscriptionProvider)
	if runtimeProvider == "" {
		return ProxyResult{}, RoutingError(http.StatusBadRequest, "gateway subscription provider is unsupported", "gateway_subscription_provider_unsupported", errSubscriptionProviderUnsupported)
	}

	workspaceID, err := parseUUID(authCtx.WorkspaceID)
	if err != nil {
		return ProxyResult{}, RoutingError(http.StatusBadRequest, "invalid workspace", "invalid_workspace", err)
	}
	backendID, err := parseUUID(target.ID)
	if err != nil {
		return ProxyResult{}, RoutingError(http.StatusBadRequest, "invalid gateway backend", "invalid_gateway_backend", err)
	}
	credentialID, err := parseUUID(target.CredentialID)
	if err != nil {
		return ProxyResult{}, RoutingError(http.StatusBadRequest, "invalid gateway credential", "invalid_gateway_credential", err)
	}

	runtimes, err := s.Queries.ListValidatedSubscriptionRuntimes(ctx, db.ListValidatedSubscriptionRuntimesParams{
		WorkspaceID:  workspaceID,
		BackendID:    backendID,
		CredentialID: credentialID,
		Provider:     target.SubscriptionProvider,
	})
	if err != nil {
		return ProxyResult{}, err
	}
	if len(runtimes) == 0 {
		return ProxyResult{}, RoutingError(http.StatusServiceUnavailable, "gateway subscription runtime is not connected", "gateway_subscription_runtime_unavailable", errSubscriptionRuntimeUnavailable)
	}

	body := summary.Body
	if mode := normalizedTranslationMode(summary.TranslationMode); mode != TranslationNone {
		translated, _, err := TranslateRequestBody(summary, mode)
		if err != nil {
			return ProxyResult{}, RoutingError(http.StatusBadRequest, "invalid request body", "invalid_request_body", err)
		}
		body = translated
	}
	queued, err := s.Queries.CreateGatewayRuntimeRequest(ctx, db.CreateGatewayRuntimeRequestParams{
		WorkspaceID:  workspaceID,
		Provider:     target.SubscriptionProvider,
		Surface:      summary.Surface,
		RequestBody:  body,
		Stream:       summary.Stream,
		BackendID:    backendID,
		CredentialID: credentialID,
		RuntimeID:    runtimes[0].ID,
	})
	if err != nil {
		return ProxyResult{}, err
	}

	start := time.Now()
	ticker := time.NewTicker(runtimeRequestPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ProxyResult{}, ctx.Err()
		case <-ticker.C:
			current, err := s.Queries.GetGatewayRuntimeRequest(ctx, db.GetGatewayRuntimeRequestParams{
				WorkspaceID: workspaceID,
				ID:          queued.ID,
			})
			if err != nil {
				return ProxyResult{}, err
			}
			switch current.Status {
			case "completed":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				if len(current.ResponseBody) > 0 {
					if _, err := w.Write(current.ResponseBody); err != nil {
						return ProxyResult{}, err
					}
				}
				return ProxyResult{
					StatusCode:   http.StatusOK,
					Status:       StatusSuccess,
					ResponseBody: current.ResponseBody,
					ResponseJSON: decodeObject(current.ResponseBody),
					DurationMS:   time.Since(start).Milliseconds(),
					Streaming:    false,
				}, nil
			case "failed":
				message := current.ErrorMessage
				if message == "" {
					message = "gateway runtime request failed"
				}
				code := current.ErrorType
				if code == "" {
					code = "gateway_runtime_request_failed"
				}
				return ProxyResult{}, GatewayError{
					StatusCode:    http.StatusBadGateway,
					PublicMessage: message,
					ErrorType:     "api_error",
					Code:          code,
					Cause:         errors.New(message),
				}
			}
		}
	}
}

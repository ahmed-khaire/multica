package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/gateway/management"
	"github.com/multica-ai/multica/server/internal/gateway/secrets"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func CompatibleBackendType(protocol, backendType string) bool {
	switch protocol {
	case ProtocolOpenAI:
		return backendType == management.BackendTypeOpenAICompatible
	case ProtocolAnthropic:
		return backendType == management.BackendTypeAnthropic
	default:
		return false
	}
}

func JoinUpstreamPath(base, path string) string {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return strings.TrimRight(base, "/") + path
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	return parsed.String()
}

type Resolver struct {
	queries *db.Queries
	loadBox func() (*secrets.Box, error)
}

func NewResolver(queries *db.Queries) *Resolver {
	return &Resolver{queries: queries, loadBox: secrets.FromEnv}
}

func (r *Resolver) ResolveDefaultBackend(ctx context.Context, workspaceID, protocol string) (BackendTarget, error) {
	workspaceUUID, err := parseUUID(workspaceID)
	if err != nil {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "invalid workspace", "invalid_workspace", err)
	}
	settings, err := r.queries.GetGatewayWorkspaceSettings(ctx, workspaceUUID)
	if errors.Is(err, pgx.ErrNoRows) || !settings.DefaultBackendID.Valid {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway default backend is not configured", "gateway_default_backend_missing", ErrDefaultBackendNotConfigured)
	}
	if err != nil {
		return BackendTarget{}, err
	}
	backend, err := r.queries.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{
		WorkspaceID: workspaceUUID,
		ID:          settings.DefaultBackendID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway default backend is not configured", "gateway_default_backend_missing", ErrDefaultBackendNotConfigured)
	}
	if err != nil {
		return BackendTarget{}, err
	}
	if !backend.Enabled {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway backend is disabled", "gateway_backend_disabled", ErrBackendDisabled)
	}
	if !CompatibleBackendType(protocol, backend.BackendType) {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway backend is not compatible with requested protocol", "gateway_backend_incompatible", ErrIncompatibleBackend)
	}
	reason, blocked, err := r.providerRiskBlockReason(ctx, workspaceUUID, backend)
	if err != nil {
		return BackendTarget{}, err
	}
	if blocked {
		return BackendTarget{}, GatewayError{
			StatusCode:    http.StatusForbidden,
			PublicMessage: "gateway provider is blocked by workspace governance",
			ErrorType:     "invalid_request_error",
			Code:          reason,
			Cause:         ErrProviderRiskBlocked,
			ResourceType:  "provider",
			ResourceID:    util.UUIDToString(backend.ID),
			ResourceLabel: backend.Slug,
		}
	}
	box, err := r.loadBox()
	if err != nil {
		return BackendTarget{}, RoutingError(http.StatusServiceUnavailable, "gateway secret key is not configured", "gateway_secret_unavailable", ErrGatewaySecretNotConfigured)
	}
	secret, err := box.DecryptString(backend.EncryptedCredential)
	if err != nil {
		return BackendTarget{}, err
	}
	return BackendTarget{
		ID:             util.UUIDToString(backend.ID),
		Slug:           backend.Slug,
		BackendType:    backend.BackendType,
		BaseURL:        backend.BaseUrl,
		UpstreamSecret: secret,
		CapturePolicy:  settings.CapturePolicy,
	}, nil
}

func (r *Resolver) providerRiskBlockReason(ctx context.Context, workspaceID pgtype.UUID, backend db.GatewayBackend) (string, bool, error) {
	risks, err := r.queries.ListAIThirdPartyRisk(ctx, workspaceID)
	if err != nil {
		return "", false, err
	}
	backendID := util.UUIDToString(backend.ID)
	for _, risk := range risks {
		if risk.ProviderName != backend.Slug && util.UUIDToString(risk.BackendID) != backendID {
			continue
		}
		reason, blocked := ProviderRiskBlockReason(risk.ContractStatus, risk.SecurityReviewStatus)
		return reason, blocked, nil
	}
	return "", false, nil
}

func ProviderRiskBlockReason(contractStatus, securityReviewStatus string) (string, bool) {
	contractStatus = strings.ToLower(strings.TrimSpace(contractStatus))
	securityReviewStatus = strings.ToLower(strings.TrimSpace(securityReviewStatus))
	switch {
	case securityReviewStatus == "rejected" || contractStatus == "rejected":
		return "provider_risk_rejected", true
	case securityReviewStatus == "expired" || contractStatus == "expired":
		return "provider_risk_expired", true
	default:
		return "", false
	}
}

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(strings.TrimSpace(value)); err != nil {
		return pgtype.UUID{}, err
	}
	if !id.Valid {
		return pgtype.UUID{}, errors.New("invalid uuid")
	}
	return id, nil
}

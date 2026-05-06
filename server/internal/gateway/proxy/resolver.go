package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/gateway/secrets"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func CompatibleBackendType(protocol, backendType string) bool {
	upstreamProtocol := BackendProtocolForType(backendType)
	switch protocol {
	case ProtocolOpenAI, ProtocolAnthropic:
		return upstreamProtocol == ProtocolOpenAI || upstreamProtocol == ProtocolAnthropic
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
	return r.ResolveBackend(ctx, workspaceID, protocol, "")
}

func (r *Resolver) ResolveBackend(ctx context.Context, workspaceID, protocol, backendSlug string) (BackendTarget, error) {
	workspaceUUID, err := parseUUID(workspaceID)
	if err != nil {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "invalid workspace", "invalid_workspace", err)
	}
	settings, err := r.queries.GetGatewayWorkspaceSettings(ctx, workspaceUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway default backend is not configured", "gateway_default_backend_missing", ErrDefaultBackendNotConfigured)
	}
	if err != nil {
		return BackendTarget{}, err
	}

	backendSlug = strings.TrimSpace(backendSlug)
	var backend db.GatewayBackend
	if backendSlug != "" {
		backend, err = r.queries.GetGatewayBackendBySlug(ctx, db.GetGatewayBackendBySlugParams{
			WorkspaceID: workspaceUUID,
			Slug:        backendSlug,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway backend is not configured", "gateway_backend_missing", ErrDefaultBackendNotConfigured)
		}
		if err != nil {
			return BackendTarget{}, err
		}
	} else {
		if !settings.DefaultBackendID.Valid {
			return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway default backend is not configured", "gateway_default_backend_missing", ErrDefaultBackendNotConfigured)
		}
		backend, err = r.queries.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{
			WorkspaceID: workspaceUUID,
			ID:          settings.DefaultBackendID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway default backend is not configured", "gateway_default_backend_missing", ErrDefaultBackendNotConfigured)
		}
		if err != nil {
			return BackendTarget{}, err
		}
	}

	if !backend.Enabled {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway backend is disabled", "gateway_backend_disabled", ErrBackendDisabled)
	}
	if !CompatibleBackendType(protocol, backend.BackendType) {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway backend is not compatible with requested protocol", "gateway_backend_incompatible", ErrIncompatibleBackend)
	}
	reason, blocked, providerRiskID, policyExceptionID, err := r.providerRiskBlockReason(ctx, workspaceUUID, backend)
	if err != nil {
		return BackendTarget{}, err
	}
	if blocked {
		return BackendTarget{}, GatewayError{
			StatusCode:     http.StatusForbidden,
			PublicMessage:  "gateway provider is blocked by workspace governance",
			ErrorType:      "invalid_request_error",
			Code:           reason,
			Cause:          ErrProviderRiskBlocked,
			ResourceType:   "provider",
			ResourceID:     util.UUIDToString(backend.ID),
			ResourceLabel:  backend.Slug,
			ProviderRiskID: providerRiskID,
		}
	}
	box, err := r.loadBox()
	if err != nil {
		return BackendTarget{}, RoutingError(http.StatusServiceUnavailable, "gateway secret key is not configured", "gateway_secret_unavailable", ErrGatewaySecretNotConfigured)
	}
	credentialID := ""
	encryptedCredential := backend.EncryptedCredential
	credentials, err := r.queries.ListActiveGatewayBackendCredentialsForBackend(ctx, db.ListActiveGatewayBackendCredentialsForBackendParams{
		WorkspaceID: workspaceUUID,
		BackendID:   backend.ID,
	})
	if err != nil {
		return BackendTarget{}, err
	}
	if len(credentials) > 0 {
		credentialID = util.UUIDToString(credentials[0].ID)
		encryptedCredential = credentials[0].EncryptedCredential
	}
	secret, err := box.DecryptString(encryptedCredential)
	if err != nil {
		return BackendTarget{}, err
	}
	return BackendTarget{
		ID:                util.UUIDToString(backend.ID),
		Slug:              backend.Slug,
		BackendType:       backend.BackendType,
		CredentialID:      credentialID,
		UpstreamProtocol:  BackendProtocolForType(backend.BackendType),
		BaseURL:           backend.BaseUrl,
		UpstreamSecret:    secret,
		CapturePolicy:     settings.CapturePolicy,
		PolicyExceptionID: policyExceptionID,
	}, nil
}

func (r *Resolver) providerRiskBlockReason(ctx context.Context, workspaceID pgtype.UUID, backend db.GatewayBackend) (string, bool, string, string, error) {
	risks, err := r.queries.ListAIThirdPartyRisk(ctx, workspaceID)
	if err != nil {
		return "", false, "", "", err
	}
	backendID := util.UUIDToString(backend.ID)
	for _, risk := range risks {
		if risk.ProviderName != backend.Slug && util.UUIDToString(risk.BackendID) != backendID {
			continue
		}
		reason, blocked := ProviderRiskBlockReason(risk.ContractStatus, risk.SecurityReviewStatus)
		if blocked {
			exceptionID, err := r.activeProviderExceptionID(ctx, workspaceID, backendID, backend.Slug)
			if err != nil {
				return "", false, "", "", err
			}
			if exceptionID != "" {
				return reason, false, util.UUIDToString(risk.ID), exceptionID, nil
			}
		}
		return reason, blocked, util.UUIDToString(risk.ID), "", nil
	}
	return "", false, "", "", nil
}

func (r *Resolver) activeProviderExceptionID(ctx context.Context, workspaceID pgtype.UUID, backendID, providerSlug string) (string, error) {
	byID, err := json.Marshal(map[string]string{
		"resource_type": "provider",
		"resource_id":   backendID,
	})
	if err != nil {
		return "", err
	}
	byLabel, err := json.Marshal(map[string]string{
		"resource_type":  "provider",
		"resource_label": providerSlug,
	})
	if err != nil {
		return "", err
	}
	exception, err := r.queries.GetActiveAIPolicyExceptionForProvider(ctx, db.GetActiveAIPolicyExceptionForProviderParams{
		WorkspaceID: workspaceID,
		Scope:       byID,
		Scope_2:     byLabel,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return util.UUIDToString(exception.ID), nil
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

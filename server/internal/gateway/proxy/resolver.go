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

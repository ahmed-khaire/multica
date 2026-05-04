package ingest

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/gateway/keyring"
	"github.com/multica-ai/multica/server/internal/gateway/proxy"
	"github.com/multica-ai/multica/server/internal/util"
)

type traceAuthContext struct {
	workspaceID pgtype.UUID
	userID      pgtype.UUID
}

func ExtractTraceIngestKey(r *http.Request) (string, bool) {
	if raw := strings.TrimSpace(r.Header.Get("Authorization")); raw != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(raw, prefix) {
			return acceptedTraceKey(strings.TrimSpace(strings.TrimPrefix(raw, prefix)))
		}
	}
	return acceptedTraceKey(strings.TrimSpace(r.Header.Get("x-api-key")))
}

func acceptedTraceKey(raw string) (string, bool) {
	if strings.HasPrefix(raw, keyring.Prefix) || strings.HasPrefix(raw, keyring.IngestPrefix) {
		return raw, true
	}
	return "", false
}

func (s *Service) authenticateTraceKey(ctx context.Context, raw string) (traceAuthContext, error) {
	if strings.HasPrefix(raw, keyring.Prefix) {
		authContext, err := proxy.AuthenticateGatewayKey(ctx, s.queries, raw)
		if err != nil {
			return traceAuthContext{}, err
		}
		workspaceID := util.ParseUUID(authContext.WorkspaceID)
		userID := util.ParseUUID(authContext.UserID)
		if !workspaceID.Valid || !userID.Valid {
			return traceAuthContext{}, ErrInvalidTracePayload
		}
		return traceAuthContext{workspaceID: workspaceID, userID: userID}, nil
	}

	if strings.HasPrefix(raw, keyring.IngestPrefix) {
		row, err := s.queries.GetGatewayIngestKeyByHash(ctx, keyring.HashGatewayKey(raw))
		if errors.Is(err, pgx.ErrNoRows) {
			return traceAuthContext{}, proxy.ErrGatewayKeyInvalid
		}
		if err != nil {
			return traceAuthContext{}, err
		}
		_ = s.queries.TouchGatewayIngestKeyLastUsed(ctx, row.ID)
		return traceAuthContext{workspaceID: row.WorkspaceID}, nil
	}

	return traceAuthContext{}, proxy.ErrGatewayKeyInvalid
}

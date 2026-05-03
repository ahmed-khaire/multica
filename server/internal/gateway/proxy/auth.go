package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/gateway/keyring"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrGatewayKeyRequired = errors.New("gateway key is required")
	ErrGatewayKeyInvalid  = errors.New("gateway key is invalid")
)

func ExtractGatewayKey(r *http.Request) (string, bool) {
	if raw := strings.TrimSpace(r.Header.Get("Authorization")); raw != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(raw, prefix) {
			key := strings.TrimSpace(strings.TrimPrefix(raw, prefix))
			if strings.HasPrefix(key, keyring.Prefix) {
				return key, true
			}
		}
	}
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); strings.HasPrefix(key, keyring.Prefix) {
		return key, true
	}
	return "", false
}

func AuthenticateGatewayKey(ctx context.Context, q *db.Queries, raw string) (AuthContext, error) {
	row, err := q.GetGatewayUserKeyByHash(ctx, keyring.HashGatewayKey(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthContext{}, ErrGatewayKeyInvalid
	}
	if err != nil {
		return AuthContext{}, err
	}
	_ = q.TouchGatewayUserKeyLastUsed(ctx, row.ID)
	return AuthContext{
		KeyID:       util.UUIDToString(row.ID),
		WorkspaceID: util.UUIDToString(row.WorkspaceID),
		UserID:      util.UUIDToString(row.UserID),
		KeyPrefix:   row.KeyPrefix,
	}, nil
}

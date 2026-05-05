package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/gateway/keyring"
	"github.com/multica-ai/multica/server/internal/gateway/secrets"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrInvalidCapturePolicy       = errors.New("invalid gateway capture policy")
	ErrInvalidGatewayBackend      = errors.New("invalid gateway backend")
	ErrGatewaySecretNotConfigured = errors.New("gateway secret key is not configured")
	ErrGatewayBackendNotFound     = errors.New("gateway backend not found")
	ErrGatewayKeyNotFound         = errors.New("gateway key not found")
)

var governanceStatuses = map[string]struct{}{
	"unknown":     {},
	"not_started": {},
	"in_review":   {},
	"approved":    {},
	"rejected":    {},
	"expired":     {},
}

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

type txStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Service struct {
	queries   *db.Queries
	txStarter txStarter
	loadBox   func() (*secrets.Box, error)
}

func NewService(queries *db.Queries, txStarter txStarter) *Service {
	return &Service{
		queries:   queries,
		txStarter: txStarter,
		loadBox:   secrets.FromEnv,
	}
}

func (s *Service) withTx(ctx context.Context, fn func(*db.Queries) error) error {
	if s.txStarter == nil {
		return fn(s.queries)
	}

	tx, err := s.txStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func uuidValue(id, field string) (pgtype.UUID, error) {
	var value pgtype.UUID
	if err := value.Scan(strings.TrimSpace(id)); err != nil || !value.Valid {
		return pgtype.UUID{}, fmt.Errorf("%w: invalid %s", ErrInvalidGatewayBackend, field)
	}
	return value, nil
}

func optionalUUID(id, field string) (pgtype.UUID, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return pgtype.UUID{}, nil
	}
	return uuidValue(id, field)
}

func uuidString(id pgtype.UUID) string {
	return util.UUIDToString(id)
}

func optionalUUIDString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuidString(id)
}

func textTimestamp(ts pgtype.Timestamptz) string {
	return util.TimestampToString(ts)
}

func optionalTimestamp(ts pgtype.Timestamptz) *string {
	return util.TimestampToPtr(ts)
}

func optionalRFC3339(raw, field string) (pgtype.Timestamptz, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.Timestamptz{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return pgtype.Timestamptz{}, fmt.Errorf("%w: invalid %s", ErrInvalidGatewayBackend, field)
	}
	return pgtype.Timestamptz{Time: parsed, Valid: true}, nil
}

func validateBackendURL(raw, backendType string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%w: invalid base URL: %v", ErrInvalidGatewayBackend, err)
	}
	if backendType == BackendTypeClaudeOAuth && parsed.Scheme == "claude-oauth" {
		return nil
	}
	if parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%w: base URL must be http(s) with host", ErrInvalidGatewayBackend)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *Service) GetOrCreateUserKey(ctx context.Context, workspaceID, userID, serverBaseURL string) (UserKeyResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return UserKeyResponse{}, err
	}
	userUUID, err := uuidValue(userID, "user_id")
	if err != nil {
		return UserKeyResponse{}, err
	}

	box, err := s.loadBox()
	if err != nil {
		return UserKeyResponse{}, normalizeSecretError(err)
	}

	urls := BuildGatewayURLs(serverBaseURL)

	row, err := s.queries.GetActiveGatewayUserKey(ctx, db.GetActiveGatewayUserKeyParams{
		WorkspaceID: workspaceUUID,
		UserID:      userUUID,
	})
	if err == nil {
		raw, err := keyring.DecryptStoredGatewayKey(box, row.EncryptedKeyValue)
		if err != nil {
			return UserKeyResponse{}, normalizeSecretError(err)
		}
		return userKeyResponse(row, raw, urls), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return UserKeyResponse{}, err
	}

	prepared, err := keyring.PrepareNewGatewayKey(box)
	if err != nil {
		return UserKeyResponse{}, normalizeSecretError(err)
	}

	var created db.GatewayUserKey
	if err := s.withTx(ctx, func(q *db.Queries) error {
		row, err := q.CreateGatewayUserKey(ctx, db.CreateGatewayUserKeyParams{
			WorkspaceID:       workspaceUUID,
			UserID:            userUUID,
			KeyHash:           prepared.Hash,
			EncryptedKeyValue: prepared.Encrypted,
			KeyPrefix:         prepared.DisplayPrefix,
		})
		if err != nil {
			return err
		}
		created = row
		return audit(ctx, q, workspaceUUID, userUUID, "gateway.key.create", "gateway_key", uuidString(row.ID), nil, userKeyListItem(row))
	}); err != nil {
		if isUniqueViolation(err) {
			row, readErr := s.queries.GetActiveGatewayUserKey(ctx, db.GetActiveGatewayUserKeyParams{
				WorkspaceID: workspaceUUID,
				UserID:      userUUID,
			})
			if readErr == nil {
				raw, decryptErr := keyring.DecryptStoredGatewayKey(box, row.EncryptedKeyValue)
				if decryptErr != nil {
					return UserKeyResponse{}, normalizeSecretError(decryptErr)
				}
				return userKeyResponse(row, raw, urls), nil
			}
		}
		return UserKeyResponse{}, err
	}

	return userKeyResponse(created, prepared.Raw, urls), nil
}

func (s *Service) GetActiveUserKey(ctx context.Context, workspaceID, userID, serverBaseURL string) (UserKeyResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return UserKeyResponse{}, err
	}
	userUUID, err := uuidValue(userID, "user_id")
	if err != nil {
		return UserKeyResponse{}, err
	}

	box, err := s.loadBox()
	if err != nil {
		return UserKeyResponse{}, normalizeSecretError(err)
	}

	row, err := s.queries.GetActiveGatewayUserKey(ctx, db.GetActiveGatewayUserKeyParams{
		WorkspaceID: workspaceUUID,
		UserID:      userUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UserKeyResponse{}, ErrGatewayKeyNotFound
	}
	if err != nil {
		return UserKeyResponse{}, err
	}

	raw, err := keyring.DecryptStoredGatewayKey(box, row.EncryptedKeyValue)
	if err != nil {
		return UserKeyResponse{}, normalizeSecretError(err)
	}
	return userKeyResponse(row, raw, BuildGatewayURLs(serverBaseURL)), nil
}

func (s *Service) ListUserKeys(ctx context.Context, workspaceID, userID string) ([]UserKeyListItem, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return nil, err
	}
	userUUID, err := uuidValue(userID, "user_id")
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListGatewayUserKeys(ctx, db.ListGatewayUserKeysParams{
		WorkspaceID: workspaceUUID,
		UserID:      userUUID,
	})
	if err != nil {
		return nil, err
	}

	items := make([]UserKeyListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, userKeyListItem(row))
	}
	return items, nil
}

func (s *Service) RevokeUserKey(ctx context.Context, workspaceID, userID, keyID string) (UserKeyListItem, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return UserKeyListItem{}, err
	}
	userUUID, err := uuidValue(userID, "user_id")
	if err != nil {
		return UserKeyListItem{}, err
	}
	keyUUID, err := uuidValue(keyID, "key_id")
	if err != nil {
		return UserKeyListItem{}, err
	}

	var revoked db.GatewayUserKey
	if err := s.withTx(ctx, func(q *db.Queries) error {
		row, err := q.RevokeGatewayUserKey(ctx, db.RevokeGatewayUserKeyParams{
			WorkspaceID: workspaceUUID,
			UserID:      userUUID,
			ID:          keyUUID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayKeyNotFound
		}
		if err != nil {
			return err
		}
		revoked = row
		return audit(ctx, q, workspaceUUID, userUUID, "gateway.key.revoke", "gateway_key", uuidString(row.ID), nil, userKeyListItem(row))
	}); err != nil {
		return UserKeyListItem{}, err
	}

	return userKeyListItem(revoked), nil
}

func (s *Service) CreateIngestKey(ctx context.Context, input CreateIngestKeyInput, serverBaseURL string) (IngestKeyResponse, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return IngestKeyResponse{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return IngestKeyResponse{}, err
	}
	appID := strings.TrimSpace(input.AppID)
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = appID
	}
	if displayName == "" {
		displayName = "Observer SDK"
	}

	box, err := s.loadBox()
	if err != nil {
		return IngestKeyResponse{}, normalizeSecretError(err)
	}
	prepared, err := keyring.PrepareNewIngestKey(box)
	if err != nil {
		return IngestKeyResponse{}, normalizeSecretError(err)
	}

	var created db.GatewayIngestKey
	if err := s.withTx(ctx, func(q *db.Queries) error {
		row, err := q.CreateGatewayIngestKey(ctx, db.CreateGatewayIngestKeyParams{
			WorkspaceID:       workspaceUUID,
			AppID:             appID,
			DisplayName:       displayName,
			KeyHash:           prepared.Hash,
			EncryptedKeyValue: prepared.Encrypted,
			KeyPrefix:         prepared.DisplayPrefix,
			CreatedBy:         actorUUID,
		})
		if err != nil {
			return err
		}
		created = row
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.ingest_key.create", "gateway_ingest_key", uuidString(row.ID), nil, ingestKeyListItem(row))
	}); err != nil {
		return IngestKeyResponse{}, err
	}

	return ingestKeyResponse(created, prepared.Raw, serverBaseURL), nil
}

func (s *Service) ListIngestKeys(ctx context.Context, workspaceID string) ([]IngestKeyListItem, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListGatewayIngestKeys(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}

	items := make([]IngestKeyListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ingestKeyListItem(row))
	}
	return items, nil
}

func (s *Service) RevokeIngestKey(ctx context.Context, workspaceID, actorUserID, keyID string) (IngestKeyListItem, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return IngestKeyListItem{}, err
	}
	actorUUID, err := uuidValue(actorUserID, "actor_user_id")
	if err != nil {
		return IngestKeyListItem{}, err
	}
	keyUUID, err := uuidValue(keyID, "key_id")
	if err != nil {
		return IngestKeyListItem{}, err
	}

	var revoked db.GatewayIngestKey
	if err := s.withTx(ctx, func(q *db.Queries) error {
		row, err := q.RevokeGatewayIngestKey(ctx, db.RevokeGatewayIngestKeyParams{
			WorkspaceID: workspaceUUID,
			ID:          keyUUID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayKeyNotFound
		}
		if err != nil {
			return err
		}
		revoked = row
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.ingest_key.revoke", "gateway_ingest_key", uuidString(row.ID), nil, ingestKeyListItem(row))
	}); err != nil {
		return IngestKeyListItem{}, err
	}

	return ingestKeyListItem(revoked), nil
}

func userKeyResponse(row db.GatewayUserKey, raw string, urls GatewayURLs) UserKeyResponse {
	return UserKeyResponse{
		ID:               uuidString(row.ID),
		Key:              raw,
		KeyPrefix:        row.KeyPrefix,
		OpenAIBaseURL:    urls.OpenAIBaseURL,
		OpenAIAPIKey:     raw,
		AnthropicBaseURL: urls.AnthropicBaseURL,
		AnthropicAPIKey:  raw,
		CreatedAt:        textTimestamp(row.CreatedAt),
		LastUsedAt:       optionalTimestamp(row.LastUsedAt),
	}
}

func ingestKeyResponse(row db.GatewayIngestKey, raw, serverBaseURL string) IngestKeyResponse {
	return IngestKeyResponse{
		ID:             uuidString(row.ID),
		Key:            raw,
		KeyPrefix:      row.KeyPrefix,
		AppID:          row.AppID,
		DisplayName:    row.DisplayName,
		GatewayBaseURL: strings.TrimRight(strings.TrimSpace(serverBaseURL), "/"),
		CreatedAt:      textTimestamp(row.CreatedAt),
		LastUsedAt:     optionalTimestamp(row.LastUsedAt),
		RevokedAt:      optionalTimestamp(row.RevokedAt),
	}
}

func userKeyListItem(row db.GatewayUserKey) UserKeyListItem {
	return UserKeyListItem{
		ID:         uuidString(row.ID),
		KeyPrefix:  row.KeyPrefix,
		RevokedAt:  optionalTimestamp(row.RevokedAt),
		LastUsedAt: optionalTimestamp(row.LastUsedAt),
		CreatedAt:  textTimestamp(row.CreatedAt),
	}
}

func ingestKeyListItem(row db.GatewayIngestKey) IngestKeyListItem {
	return IngestKeyListItem{
		ID:          uuidString(row.ID),
		KeyPrefix:   row.KeyPrefix,
		AppID:       row.AppID,
		DisplayName: row.DisplayName,
		RevokedAt:   optionalTimestamp(row.RevokedAt),
		LastUsedAt:  optionalTimestamp(row.LastUsedAt),
		CreatedAt:   textTimestamp(row.CreatedAt),
	}
}

func (s *Service) Settings(ctx context.Context, workspaceID string) (SettingsResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return SettingsResponse{}, err
	}
	settings, err := getSettingsOrDefaultWithQueries(ctx, s.queries, workspaceUUID)
	if err != nil {
		return SettingsResponse{}, err
	}
	return s.settingsResponse(ctx, s.queries, workspaceUUID, settings)
}

func (s *Service) Status(ctx context.Context, workspaceID, userID, serverBaseURL string) (StatusResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return StatusResponse{}, err
	}
	userUUID, err := uuidValue(userID, "user_id")
	if err != nil {
		return StatusResponse{}, err
	}
	settings, err := getSettingsOrDefaultWithQueries(ctx, s.queries, workspaceUUID)
	if err != nil {
		return StatusResponse{}, err
	}

	backends, err := s.queries.ListGatewayBackends(ctx, workspaceUUID)
	if err != nil {
		return StatusResponse{}, err
	}

	var defaultBackend *BackendResponse
	enabledCount := 0
	for _, row := range backends {
		if row.Enabled {
			enabledCount++
		}
		if settings.DefaultBackendID.Valid && row.ID == settings.DefaultBackendID {
			response := backendResponse(row, settings.DefaultBackendID)
			defaultBackend = &response
		}
	}

	hasActiveKey := false
	if _, err := s.queries.GetActiveGatewayUserKey(ctx, db.GetActiveGatewayUserKeyParams{
		WorkspaceID: workspaceUUID,
		UserID:      userUUID,
	}); err == nil {
		hasActiveKey = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return StatusResponse{}, err
	}

	urls := BuildGatewayURLs(serverBaseURL)
	return StatusResponse{
		OpenAIBaseURL:       urls.OpenAIBaseURL,
		AnthropicBaseURL:    urls.AnthropicBaseURL,
		CapturePolicy:       settings.CapturePolicy,
		DefaultBackend:      defaultBackend,
		BackendCount:        len(backends),
		EnabledBackendCount: enabledCount,
		HasActiveKey:        hasActiveKey,
	}, nil
}

func (s *Service) ListBackends(ctx context.Context, workspaceID string) ([]BackendResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return nil, err
	}
	settings, err := getSettingsOrDefaultWithQueries(ctx, s.queries, workspaceUUID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListGatewayBackends(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}

	responses := make([]BackendResponse, 0, len(rows))
	for _, row := range rows {
		responses = append(responses, backendResponse(row, settings.DefaultBackendID))
	}
	return responses, nil
}

func (s *Service) CreateBackend(ctx context.Context, input CreateBackendInput) (BackendResponse, error) {
	normalized, credential, err := normalizeCreateBackendInput(input)
	if err != nil {
		return BackendResponse{}, err
	}
	workspaceUUID, err := uuidValue(normalized.WorkspaceID, "workspace_id")
	if err != nil {
		return BackendResponse{}, err
	}
	actorUUID, err := uuidValue(normalized.ActorUserID, "actor_user_id")
	if err != nil {
		return BackendResponse{}, err
	}
	box, err := s.loadBox()
	if err != nil {
		return BackendResponse{}, normalizeSecretError(err)
	}
	encryptedCredential, err := box.EncryptString(credential)
	if err != nil {
		return BackendResponse{}, normalizeSecretError(err)
	}
	metadata, err := metadataJSON(normalized.Metadata)
	if err != nil {
		return BackendResponse{}, err
	}

	defaultID := pgtype.UUID{}
	var created db.GatewayBackend

	if err := s.withTx(ctx, func(q *db.Queries) error {
		settings, err := getSettingsOrDefaultWithQueries(ctx, q, workspaceUUID)
		if err != nil {
			return err
		}

		row, err := q.CreateGatewayBackend(ctx, db.CreateGatewayBackendParams{
			WorkspaceID:         workspaceUUID,
			Slug:                normalized.Slug,
			DisplayName:         normalized.DisplayName,
			BackendType:         normalized.BackendType,
			BaseUrl:             normalized.BaseURL,
			EncryptedCredential: encryptedCredential,
			CredentialHint:      CredentialHint(credential),
			Enabled:             normalized.Enabled,
			Metadata:            metadata,
			CreatedBy:           actorUUID,
		})
		if err != nil {
			return err
		}
		created = row
		defaultID = settings.DefaultBackendID

		if normalized.SetDefault || !settings.DefaultBackendID.Valid {
			updatedSettings, err := q.UpsertGatewayWorkspaceSettings(ctx, db.UpsertGatewayWorkspaceSettingsParams{
				WorkspaceID:      workspaceUUID,
				CapturePolicy:    settings.CapturePolicy,
				DefaultBackendID: row.ID,
			})
			if err != nil {
				return err
			}
			defaultID = updatedSettings.DefaultBackendID
			if err := audit(ctx, q, workspaceUUID, actorUUID, "gateway.default_backend.update", "gateway_workspace_settings", uuidString(workspaceUUID), settings, updatedSettings); err != nil {
				return err
			}
		}

		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.backend.create", "gateway_backend", uuidString(row.ID), nil, backendResponse(row, defaultID))
	}); err != nil {
		return BackendResponse{}, err
	}

	return backendResponse(created, defaultID), nil
}

func (s *Service) UpdateCapturePolicy(ctx context.Context, input CapturePolicyInput) (SettingsResponse, error) {
	if err := ValidateCapturePolicy(input.CapturePolicy); err != nil {
		return SettingsResponse{}, err
	}

	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return SettingsResponse{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return SettingsResponse{}, err
	}
	var updated db.GatewayWorkspaceSetting

	if err := s.withTx(ctx, func(q *db.Queries) error {
		current, err := getSettingsOrDefaultWithQueries(ctx, q, workspaceUUID)
		if err != nil {
			return err
		}
		updated, err = q.UpsertGatewayWorkspaceSettings(ctx, db.UpsertGatewayWorkspaceSettingsParams{
			WorkspaceID:      workspaceUUID,
			CapturePolicy:    input.CapturePolicy,
			DefaultBackendID: current.DefaultBackendID,
		})
		if err != nil {
			return err
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.policy.update", "gateway_workspace_settings", uuidString(workspaceUUID), current, updated)
	}); err != nil {
		return SettingsResponse{}, err
	}

	return s.settingsResponse(ctx, s.queries, workspaceUUID, updated)
}

func (s *Service) SetDefaultBackend(ctx context.Context, input SetDefaultBackendInput) (SettingsResponse, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return SettingsResponse{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return SettingsResponse{}, err
	}
	var updated db.GatewayWorkspaceSetting

	if err := s.withTx(ctx, func(q *db.Queries) error {
		backend, err := q.GetGatewayBackendBySlug(ctx, db.GetGatewayBackendBySlugParams{
			WorkspaceID: workspaceUUID,
			Slug:        strings.TrimSpace(input.Slug),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayBackendNotFound
		}
		if err != nil {
			return err
		}

		current, err := getSettingsOrDefaultWithQueries(ctx, q, workspaceUUID)
		if err != nil {
			return err
		}
		updated, err = q.UpsertGatewayWorkspaceSettings(ctx, db.UpsertGatewayWorkspaceSettingsParams{
			WorkspaceID:      workspaceUUID,
			CapturePolicy:    current.CapturePolicy,
			DefaultBackendID: backend.ID,
		})
		if err != nil {
			return err
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.default_backend.update", "gateway_workspace_settings", uuidString(workspaceUUID), current, updated)
	}); err != nil {
		return SettingsResponse{}, err
	}

	return s.settingsResponse(ctx, s.queries, workspaceUUID, updated)
}

func (s *Service) UpdateBackend(ctx context.Context, input UpdateBackendInput) (BackendResponse, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return BackendResponse{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return BackendResponse{}, err
	}
	backendUUID, err := uuidValue(input.BackendID, "backend_id")
	if err != nil {
		return BackendResponse{}, err
	}
	var updated db.GatewayBackend
	defaultID := pgtype.UUID{}

	if err := s.withTx(ctx, func(q *db.Queries) error {
		current, err := q.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{
			WorkspaceID: workspaceUUID,
			ID:          backendUUID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayBackendNotFound
		}
		if err != nil {
			return err
		}

		displayName := current.DisplayName
		if input.DisplayName != nil {
			displayName = strings.TrimSpace(*input.DisplayName)
		}
		baseURL := current.BaseUrl
		if input.BaseURL != nil {
			baseURL = strings.TrimSpace(*input.BaseURL)
		}
		if err := validateBackendURL(baseURL, current.BackendType); err != nil {
			return err
		}
		enabled := current.Enabled
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		metadata := current.Metadata
		if input.Metadata != nil {
			metadata, err = metadataJSON(input.Metadata)
			if err != nil {
				return err
			}
		}
		encryptedCredential := current.EncryptedCredential
		credentialHint := current.CredentialHint
		if input.Key != nil {
			box, err := s.loadBox()
			if err != nil {
				return normalizeSecretError(err)
			}
			credential := strings.TrimSpace(*input.Key)
			if credential == "" && current.BackendType != BackendTypeClaudeOAuth {
				return fmt.Errorf("%w: credential is required", ErrInvalidGatewayBackend)
			}
			if credential == "" {
				credential = "sidecar-managed"
			}
			encryptedCredential, err = box.EncryptString(credential)
			if err != nil {
				return normalizeSecretError(err)
			}
			credentialHint = CredentialHint(credential)
		}

		updated, err = q.UpdateGatewayBackend(ctx, db.UpdateGatewayBackendParams{
			WorkspaceID:         workspaceUUID,
			ID:                  backendUUID,
			DisplayName:         displayName,
			BackendType:         current.BackendType,
			BaseUrl:             baseURL,
			EncryptedCredential: encryptedCredential,
			CredentialHint:      credentialHint,
			Enabled:             enabled,
			Metadata:            metadata,
			UpdatedBy:           actorUUID,
		})
		if err != nil {
			return err
		}

		settings, err := getSettingsOrDefaultWithQueries(ctx, q, workspaceUUID)
		if err != nil {
			return err
		}
		defaultID = settings.DefaultBackendID
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.backend.update", "gateway_backend", uuidString(current.ID), backendResponse(current, defaultID), backendResponse(updated, defaultID))
	}); err != nil {
		return BackendResponse{}, err
	}

	return backendResponse(updated, defaultID), nil
}

func (s *Service) DeleteBackend(ctx context.Context, workspaceID, actorUserID, backendID string) error {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return err
	}
	actorUUID, err := uuidValue(actorUserID, "actor_user_id")
	if err != nil {
		return err
	}
	backendUUID, err := uuidValue(backendID, "backend_id")
	if err != nil {
		return err
	}

	return s.withTx(ctx, func(q *db.Queries) error {
		current, err := q.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{
			WorkspaceID: workspaceUUID,
			ID:          backendUUID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayBackendNotFound
		}
		if err != nil {
			return err
		}
		settings, err := getSettingsOrDefaultWithQueries(ctx, q, workspaceUUID)
		if err != nil {
			return err
		}
		if err := q.DeleteGatewayBackend(ctx, db.DeleteGatewayBackendParams{
			WorkspaceID: workspaceUUID,
			ID:          backendUUID,
		}); err != nil {
			return err
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.backend.delete", "gateway_backend", uuidString(current.ID), backendResponse(current, settings.DefaultBackendID), nil)
	})
}

func (s *Service) ListAudit(ctx context.Context, workspaceID string, limit int32) ([]AuditLogItem, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.queries.ListGatewayAuditLog(ctx, db.ListGatewayAuditLogParams{
		WorkspaceID: workspaceUUID,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]AuditLogItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, AuditLogItem{
			ID:          uuidString(row.ID),
			ActorUserID: uuidString(row.ActorUserID),
			ActorName:   row.ActorName,
			ActorEmail:  row.ActorEmail,
			Action:      row.Action,
			TargetType:  row.TargetType,
			TargetID:    row.TargetID,
			BeforeState: auditJSON(row.BeforeState),
			AfterState:  auditJSON(row.AfterState),
			RequestID:   row.RequestID,
			CreatedAt:   textTimestamp(row.CreatedAt),
		})
	}
	return items, nil
}

func (s *Service) ListProviderRisks(ctx context.Context, workspaceID string) ([]ProviderRiskResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListAIThirdPartyRisk(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	resp := make([]ProviderRiskResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, providerRiskResponse(row))
	}
	return resp, nil
}

func (s *Service) UpsertProviderRisk(ctx context.Context, input UpsertProviderRiskInput) (ProviderRiskResponse, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return ProviderRiskResponse{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return ProviderRiskResponse{}, err
	}
	normalized, err := normalizeProviderRiskInput(input)
	if err != nil {
		return ProviderRiskResponse{}, err
	}
	backendUUID, err := optionalUUID(normalized.BackendID, "backend_id")
	if err != nil {
		return ProviderRiskResponse{}, err
	}
	lastAssessmentAt, err := optionalRFC3339(normalized.LastAssessmentAt, "last_assessment_at")
	if err != nil {
		return ProviderRiskResponse{}, err
	}
	nextReviewAt, err := optionalRFC3339(normalized.NextReviewAt, "next_review_at")
	if err != nil {
		return ProviderRiskResponse{}, err
	}

	var row db.AiThirdPartyRisk
	if err := s.withTx(ctx, func(q *db.Queries) error {
		approvedUseCases, err := stringArrayJSON(normalized.ApprovedUseCases)
		if err != nil {
			return err
		}
		dataCategories, err := stringArrayJSON(normalized.DataCategories)
		if err != nil {
			return err
		}
		regions, err := stringArrayJSON(normalized.Regions)
		if err != nil {
			return err
		}
		evidenceLinks, err := stringArrayJSON(normalized.EvidenceLinks)
		if err != nil {
			return err
		}
		modelList, err := stringArrayJSON(normalized.ModelList)
		if err != nil {
			return err
		}

		row, err = q.UpsertAIThirdPartyRisk(ctx, db.UpsertAIThirdPartyRiskParams{
			WorkspaceID:          workspaceUUID,
			BackendID:            backendUUID,
			ProviderName:         normalized.ProviderName,
			OwnerUserID:          actorUUID,
			ApprovedUseCases:     approvedUseCases,
			DataCategories:       dataCategories,
			Regions:              regions,
			HostingNotes:         normalized.HostingNotes,
			ContractStatus:       normalized.ContractStatus,
			SecurityReviewStatus: normalized.SecurityReviewStatus,
			EvidenceLinks:        evidenceLinks,
			Limitations:          normalized.Limitations,
			ProhibitedUses:       normalized.ProhibitedUses,
			ModelList:            modelList,
			CapabilityClass:      normalized.CapabilityClass,
			RiskScore:            normalized.RiskScore,
			ReviewCadenceDays:    normalized.ReviewCadenceDays,
			LastAssessmentAt:     lastAssessmentAt,
			NextReviewAt:         nextReviewAt,
			ActiveExceptionCount: normalized.ActiveExceptionCount,
		})
		if err != nil {
			return err
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.governance.provider_risk.upsert", "ai_third_party_risk", uuidString(row.ID), nil, providerRiskResponse(row))
	}); err != nil {
		return ProviderRiskResponse{}, err
	}
	return providerRiskResponse(row), nil
}

func normalizeProviderRiskInput(input UpsertProviderRiskInput) (UpsertProviderRiskInput, error) {
	normalized := input
	normalized.ProviderName = strings.ToLower(strings.TrimSpace(normalized.ProviderName))
	normalized.BackendID = strings.TrimSpace(normalized.BackendID)
	normalized.HostingNotes = strings.TrimSpace(normalized.HostingNotes)
	normalized.ContractStatus = strings.TrimSpace(normalized.ContractStatus)
	normalized.SecurityReviewStatus = strings.TrimSpace(normalized.SecurityReviewStatus)
	normalized.Limitations = strings.TrimSpace(normalized.Limitations)
	normalized.ProhibitedUses = strings.TrimSpace(normalized.ProhibitedUses)
	normalized.CapabilityClass = strings.TrimSpace(normalized.CapabilityClass)
	normalized.LastAssessmentAt = strings.TrimSpace(normalized.LastAssessmentAt)
	normalized.NextReviewAt = strings.TrimSpace(normalized.NextReviewAt)
	normalized.ApprovedUseCases = cleanStringSlice(normalized.ApprovedUseCases)
	normalized.DataCategories = cleanStringSlice(normalized.DataCategories)
	normalized.Regions = cleanStringSlice(normalized.Regions)
	normalized.EvidenceLinks = cleanStringSlice(normalized.EvidenceLinks)
	normalized.ModelList = cleanStringSlice(normalized.ModelList)

	if normalized.ProviderName == "" {
		return normalized, fmt.Errorf("%w: provider_name is required", ErrInvalidGatewayBackend)
	}
	if normalized.RiskScore < 0 || normalized.RiskScore > 100 {
		return normalized, fmt.Errorf("%w: risk_score must be between 0 and 100", ErrInvalidGatewayBackend)
	}
	if normalized.ReviewCadenceDays <= 0 {
		normalized.ReviewCadenceDays = 365
	}
	if normalized.ContractStatus == "" {
		normalized.ContractStatus = "unknown"
	}
	if _, ok := governanceStatuses[normalized.ContractStatus]; !ok {
		return normalized, fmt.Errorf("%w: invalid contract_status", ErrInvalidGatewayBackend)
	}
	if normalized.SecurityReviewStatus == "" {
		normalized.SecurityReviewStatus = "unknown"
	}
	if _, ok := governanceStatuses[normalized.SecurityReviewStatus]; !ok {
		return normalized, fmt.Errorf("%w: invalid security_review_status", ErrInvalidGatewayBackend)
	}
	return normalized, nil
}

func providerRiskResponse(row db.AiThirdPartyRisk) ProviderRiskResponse {
	return ProviderRiskResponse{
		ID:                   uuidString(row.ID),
		BackendID:            optionalUUIDString(row.BackendID),
		ProviderName:         row.ProviderName,
		OwnerUserID:          optionalUUIDString(row.OwnerUserID),
		ApprovedUseCases:     jsonStringSlice(row.ApprovedUseCases),
		DataCategories:       jsonStringSlice(row.DataCategories),
		Regions:              jsonStringSlice(row.Regions),
		HostingNotes:         row.HostingNotes,
		ContractStatus:       row.ContractStatus,
		SecurityReviewStatus: row.SecurityReviewStatus,
		EvidenceLinks:        jsonStringSlice(row.EvidenceLinks),
		Limitations:          row.Limitations,
		ProhibitedUses:       row.ProhibitedUses,
		ModelList:            jsonStringSlice(row.ModelList),
		CapabilityClass:      row.CapabilityClass,
		RiskScore:            row.RiskScore,
		ReviewCadenceDays:    row.ReviewCadenceDays,
		LastAssessmentAt:     optionalTimestamp(row.LastAssessmentAt),
		NextReviewAt:         optionalTimestamp(row.NextReviewAt),
		ActiveExceptionCount: row.ActiveExceptionCount,
		CreatedAt:            textTimestamp(row.CreatedAt),
		UpdatedAt:            textTimestamp(row.UpdatedAt),
	}
}

func normalizeCreateBackendInput(input CreateBackendInput) (CreateBackendInput, string, error) {
	normalized := input
	normalized.Provider = strings.ToLower(strings.TrimSpace(normalized.Provider))
	normalized.Slug = strings.TrimSpace(normalized.Slug)
	normalized.DisplayName = strings.TrimSpace(normalized.DisplayName)
	normalized.BackendType = strings.TrimSpace(normalized.BackendType)
	normalized.BaseURL = strings.TrimSpace(normalized.BaseURL)

	if preset, ok := ProviderPresetFor(normalized.Provider); ok {
		if normalized.Slug == "" {
			normalized.Slug = preset.Slug
		}
		if normalized.DisplayName == "" {
			normalized.DisplayName = preset.DisplayName
		}
		if normalized.BackendType == "" {
			normalized.BackendType = preset.BackendType
		}
		if normalized.BaseURL == "" {
			normalized.BaseURL = preset.BaseURL
		}
		if preset.RequiresCredential && strings.TrimSpace(normalized.Key) == "" {
			return CreateBackendInput{}, "", fmt.Errorf("%w: credential is required", ErrInvalidGatewayBackend)
		}
	}

	if normalized.Slug == "" {
		return CreateBackendInput{}, "", fmt.Errorf("%w: slug is required", ErrInvalidGatewayBackend)
	}
	if normalized.DisplayName == "" {
		normalized.DisplayName = normalized.Slug
	}
	if normalized.BackendType == "" {
		return CreateBackendInput{}, "", fmt.Errorf("%w: backend type is required", ErrInvalidGatewayBackend)
	}
	if normalized.BaseURL == "" {
		return CreateBackendInput{}, "", fmt.Errorf("%w: base URL is required", ErrInvalidGatewayBackend)
	}
	if err := validateBackendURL(normalized.BaseURL, normalized.BackendType); err != nil {
		return CreateBackendInput{}, "", err
	}

	credential := strings.TrimSpace(normalized.Key)
	if normalized.BackendType == BackendTypeClaudeOAuth && credential == "" {
		credential = "sidecar-managed"
	}

	return normalized, credential, nil
}

func getSettingsOrDefaultWithQueries(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID) (db.GatewayWorkspaceSetting, error) {
	settings, err := q.GetGatewayWorkspaceSettings(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GatewayWorkspaceSetting{
			WorkspaceID:      workspaceID,
			CapturePolicy:    DefaultCapturePolicy,
			DefaultBackendID: pgtype.UUID{},
		}, nil
	}
	return settings, err
}

func (s *Service) settingsResponse(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, settings db.GatewayWorkspaceSetting) (SettingsResponse, error) {
	var defaultBackend *BackendResponse
	if settings.DefaultBackendID.Valid {
		row, err := q.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{
			WorkspaceID: workspaceID,
			ID:          settings.DefaultBackendID,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return SettingsResponse{}, err
		}
		if err == nil {
			response := backendResponse(row, settings.DefaultBackendID)
			defaultBackend = &response
		}
	}
	return SettingsResponse{
		CapturePolicy:  settings.CapturePolicy,
		DefaultBackend: defaultBackend,
	}, nil
}

func backendResponse(row db.GatewayBackend, defaultID pgtype.UUID) BackendResponse {
	metadata := map[string]any{}
	if len(row.Metadata) > 0 {
		_ = json.Unmarshal(row.Metadata, &metadata)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}

	return BackendResponse{
		ID:             uuidString(row.ID),
		Slug:           row.Slug,
		DisplayName:    row.DisplayName,
		BackendType:    row.BackendType,
		BaseURL:        row.BaseUrl,
		CredentialHint: row.CredentialHint,
		Enabled:        row.Enabled,
		IsDefault:      defaultID.Valid && row.ID == defaultID,
		Metadata:       metadata,
		CreatedAt:      textTimestamp(row.CreatedAt),
		UpdatedAt:      textTimestamp(row.UpdatedAt),
	}
}

func metadataJSON(metadata map[string]any) ([]byte, error) {
	if metadata == nil {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid metadata: %v", ErrInvalidGatewayBackend, err)
	}
	return raw, nil
}

func stringArrayJSON(values []string) ([]byte, error) {
	raw, err := json.Marshal(cleanStringSlice(values))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid string array: %v", ErrInvalidGatewayBackend, err)
	}
	return raw, nil
}

func cleanStringSlice(values []string) []string {
	clean := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		clean = append(clean, value)
	}
	return clean
}

func jsonStringSlice(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return []string{}
	}
	return values
}

func auditJSON(raw []byte) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

func audit(ctx context.Context, q *db.Queries, workspaceID, actorUserID pgtype.UUID, action, targetType, targetID string, before, after any) error {
	beforeState, err := json.Marshal(before)
	if err != nil {
		return err
	}
	afterState, err := json.Marshal(after)
	if err != nil {
		return err
	}
	_, err = q.CreateAIAuditLog(ctx, db.CreateAIAuditLogParams{
		WorkspaceID: workspaceID,
		ActorUserID: actorUserID,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		BeforeState: beforeState,
		AfterState:  afterState,
		RequestID:   "",
	})
	return err
}

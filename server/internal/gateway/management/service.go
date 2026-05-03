package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
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

func uuidValue(id string) pgtype.UUID {
	return util.ParseUUID(id)
}

func uuidString(id pgtype.UUID) string {
	return util.UUIDToString(id)
}

func textTimestamp(ts pgtype.Timestamptz) string {
	return util.TimestampToString(ts)
}

func optionalTimestamp(ts pgtype.Timestamptz) *string {
	return util.TimestampToPtr(ts)
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

func (s *Service) GetOrCreateUserKey(ctx context.Context, workspaceID, userID, serverBaseURL string) (UserKeyResponse, error) {
	box, err := s.loadBox()
	if err != nil {
		return UserKeyResponse{}, normalizeSecretError(err)
	}

	workspaceUUID := uuidValue(workspaceID)
	userUUID := uuidValue(userID)
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
		return UserKeyResponse{}, err
	}

	return userKeyResponse(created, prepared.Raw, urls), nil
}

func (s *Service) GetActiveUserKey(ctx context.Context, workspaceID, userID, serverBaseURL string) (UserKeyResponse, error) {
	box, err := s.loadBox()
	if err != nil {
		return UserKeyResponse{}, normalizeSecretError(err)
	}

	row, err := s.queries.GetActiveGatewayUserKey(ctx, db.GetActiveGatewayUserKeyParams{
		WorkspaceID: uuidValue(workspaceID),
		UserID:      uuidValue(userID),
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
	rows, err := s.queries.ListGatewayUserKeys(ctx, db.ListGatewayUserKeysParams{
		WorkspaceID: uuidValue(workspaceID),
		UserID:      uuidValue(userID),
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
	workspaceUUID := uuidValue(workspaceID)
	userUUID := uuidValue(userID)
	keyUUID := uuidValue(keyID)

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

func userKeyListItem(row db.GatewayUserKey) UserKeyListItem {
	return UserKeyListItem{
		ID:         uuidString(row.ID),
		KeyPrefix:  row.KeyPrefix,
		RevokedAt:  optionalTimestamp(row.RevokedAt),
		LastUsedAt: optionalTimestamp(row.LastUsedAt),
		CreatedAt:  textTimestamp(row.CreatedAt),
	}
}

func (s *Service) Settings(ctx context.Context, workspaceID string) (SettingsResponse, error) {
	workspaceUUID := uuidValue(workspaceID)
	settings, err := s.getSettingsOrDefault(ctx, workspaceID)
	if err != nil {
		return SettingsResponse{}, err
	}
	return s.settingsResponse(ctx, s.queries, workspaceUUID, settings)
}

func (s *Service) Status(ctx context.Context, workspaceID, userID, serverBaseURL string) (StatusResponse, error) {
	workspaceUUID := uuidValue(workspaceID)
	userUUID := uuidValue(userID)
	settings, err := s.getSettingsOrDefault(ctx, workspaceID)
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
	settings, err := s.getSettingsOrDefault(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListGatewayBackends(ctx, uuidValue(workspaceID))
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

	workspaceUUID := uuidValue(normalized.WorkspaceID)
	actorUUID := uuidValue(normalized.ActorUserID)
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

	workspaceUUID := uuidValue(input.WorkspaceID)
	actorUUID := uuidValue(input.ActorUserID)
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
	workspaceUUID := uuidValue(input.WorkspaceID)
	actorUUID := uuidValue(input.ActorUserID)
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
	workspaceUUID := uuidValue(input.WorkspaceID)
	actorUUID := uuidValue(input.ActorUserID)
	backendUUID := uuidValue(input.BackendID)
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
	workspaceUUID := uuidValue(workspaceID)
	actorUUID := uuidValue(actorUserID)
	backendUUID := uuidValue(backendID)

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
	if normalized.BackendType != BackendTypeClaudeOAuth && credential == "" {
		return CreateBackendInput{}, "", fmt.Errorf("%w: credential is required", ErrInvalidGatewayBackend)
	}

	return normalized, credential, nil
}

func (s *Service) getSettingsOrDefault(ctx context.Context, workspaceID string) (db.GatewayWorkspaceSetting, error) {
	return getSettingsOrDefaultWithQueries(ctx, s.queries, uuidValue(workspaceID))
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

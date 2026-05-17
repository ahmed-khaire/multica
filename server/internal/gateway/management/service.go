package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/gateway/keyring"
	gatewaypolicy "github.com/multica-ai/multica/server/internal/gateway/policy"
	"github.com/multica-ai/multica/server/internal/gateway/secrets"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrInvalidCapturePolicy          = errors.New("invalid gateway capture policy")
	ErrInvalidGatewayBackend         = errors.New("invalid gateway backend")
	ErrGatewaySecretNotConfigured    = errors.New("gateway secret key is not configured")
	ErrGatewayBackendNotFound        = errors.New("gateway backend not found")
	ErrGatewayKeyNotFound            = errors.New("gateway key not found")
	ErrGatewayPolicyDecisionNotFound = errors.New("gateway policy decision not found")
	ErrGatewayEvidenceExportNotFound = errors.New("gateway evidence export not found")
)

var governanceStatuses = map[string]struct{}{
	"unknown":     {},
	"not_started": {},
	"in_review":   {},
	"approved":    {},
	"rejected":    {},
	"expired":     {},
}

var incidentStatuses = map[string]struct{}{
	"open":          {},
	"investigating": {},
	"remediated":    {},
	"closed":        {},
}

var policyExceptionStatuses = map[string]struct{}{
	"requested": {},
	"approved":  {},
	"denied":    {},
	"expired":   {},
	"revoked":   {},
}

var gatewayPolicyTypes = map[string]struct{}{
	"provider": {},
	"model":    {},
	"tool":     {},
	"data":     {},
	"budget":   {},
	"approval": {},
	"routing":  {},
	"capture":  {},
}

var gatewayPolicyEnforcementModes = map[string]struct{}{
	"monitor": {},
	"enforce": {},
}

var gatewayPolicyActions = map[gatewaypolicy.Action]struct{}{
	gatewaypolicy.ActionAllow:           {},
	gatewaypolicy.ActionWarn:            {},
	gatewaypolicy.ActionRequireApproval: {},
	gatewaypolicy.ActionRedact:          {},
	gatewaypolicy.ActionRouteToBackend:  {},
	gatewaypolicy.ActionBlock:           {},
}

type defaultControlMapping struct {
	ControlID             string
	ControlTitle          string
	MappedEvidenceQueries []map[string]string
	Status                string
}

const internalGatewayGovernanceFramework = "internal_gateway_governance"

var defaultGatewayControlMappings = []defaultControlMapping{
	{
		ControlID:    "GW-1",
		ControlTitle: "Gateway provider risk blocks are evidenced",
		MappedEvidenceQueries: []map[string]string{{
			"evidence_type": "gateway_policy_decision",
		}},
		Status: "in_progress",
	},
	{
		ControlID:    "GW-2",
		ControlTitle: "Gateway backend administration is auditable",
		MappedEvidenceQueries: []map[string]string{{
			"audit_action_prefix": "gateway.backend.",
		}},
		Status: "not_started",
	},
	{
		ControlID:    "GW-3",
		ControlTitle: "Gateway traffic capture policy is explicit",
		MappedEvidenceQueries: []map[string]string{{
			"setting": "capture_policy",
		}},
		Status: "not_started",
	},
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
	queries    *db.Queries
	txStarter  txStarter
	loadBox    func() (*secrets.Box, error)
	httpClient *http.Client
}

func NewService(queries *db.Queries, txStarter txStarter) *Service {
	return &Service{
		queries:    queries,
		txStarter:  txStarter,
		loadBox:    secrets.FromEnv,
		httpClient: http.DefaultClient,
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

func optionalInt4(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	return &value.Int32
}

func optionalTextString(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
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
	if backendType == BackendTypeSubscriptionRuntime && parsed.Scheme == "daemon" {
		return nil
	}
	if parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%w: base URL must be http(s) with host", ErrInvalidGatewayBackend)
	}
	return nil
}

func transportOrDefault(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return TransportDirectHTTP
	}
	return value
}

func credentialTypeOrDefault(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return CredentialTypeAPIKey
	}
	return value
}

func dispatchScopeOrDefault(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DispatchScopeWorkspaceAuthenticatedDaemons
	}
	return value
}

func encryptedPayloadForCredential(credentialType string, encryptedCredential []byte) []byte {
	if credentialType != CredentialTypeSubscriptionBundle {
		return nil
	}
	return encryptedCredential
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

func (s *Service) Doctor(ctx context.Context, workspaceID, userID, serverBaseURL string) (DoctorResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return DoctorResponse{}, err
	}
	userUUID, err := uuidValue(userID, "user_id")
	if err != nil {
		return DoctorResponse{}, err
	}

	checks := make([]DoctorCheck, 0, 12)
	add := func(check DoctorCheck) {
		checks = append(checks, check)
	}

	urls := BuildGatewayURLs(serverBaseURL)
	add(DoctorCheck{
		ID:       "gateway_urls",
		Category: "gateway",
		Status:   "pass",
		Title:    "Gateway base URLs",
		Detail:   "OpenAI-compatible and Anthropic-compatible Gateway URLs are available.",
		Metadata: map[string]any{
			"openai_base_url":    urls.OpenAIBaseURL,
			"anthropic_base_url": urls.AnthropicBaseURL,
		},
	})

	box, secretErr := s.loadBox()
	if secretErr != nil {
		add(DoctorCheck{
			ID:          "gateway_secret",
			Category:    "gateway",
			Status:      "fail",
			Title:       "Gateway secret key",
			Detail:      "Gateway cannot encrypt or decrypt managed provider credentials.",
			Remediation: "Set MULTICA_GATEWAY_SECRET_KEY to a base64-encoded 32-byte key and restart the server.",
		})
	} else {
		add(DoctorCheck{
			ID:       "gateway_secret",
			Category: "gateway",
			Status:   "pass",
			Title:    "Gateway secret key",
			Detail:   "Gateway credential encryption is configured.",
		})
	}

	if _, err := s.queries.GetActiveGatewayUserKey(ctx, db.GetActiveGatewayUserKeyParams{
		WorkspaceID: workspaceUUID,
		UserID:      userUUID,
	}); errors.Is(err, pgx.ErrNoRows) {
		add(DoctorCheck{
			ID:          "gateway_key",
			Category:    "workspace",
			Status:      "warning",
			Title:       "User Gateway key",
			Detail:      "No active Gateway key exists for this user.",
			Remediation: "Run multica gateway key or generate a key from Settings -> Gateway.",
		})
	} else if err != nil {
		return DoctorResponse{}, err
	} else {
		add(DoctorCheck{
			ID:       "gateway_key",
			Category: "workspace",
			Status:   "pass",
			Title:    "User Gateway key",
			Detail:   "This user has an active Gateway key.",
		})
	}

	settings, err := getSettingsOrDefaultWithQueries(ctx, s.queries, workspaceUUID)
	if err != nil {
		return DoctorResponse{}, err
	}
	if err := ValidateCapturePolicy(settings.CapturePolicy); err != nil {
		add(DoctorCheck{
			ID:          "capture_policy",
			Category:    "policy",
			Status:      "fail",
			Title:       "Capture policy",
			Detail:      "Workspace capture policy is invalid.",
			Remediation: "Set capture policy to metadata_only, redacted_content, or full_content.",
		})
	} else {
		add(DoctorCheck{
			ID:       "capture_policy",
			Category: "policy",
			Status:   "pass",
			Title:    "Capture policy",
			Detail:   "Workspace capture policy is explicit.",
			Metadata: map[string]any{"capture_policy": settings.CapturePolicy},
		})
	}

	backends, err := s.queries.ListGatewayBackends(ctx, workspaceUUID)
	if err != nil {
		return DoctorResponse{}, err
	}
	enabledBackends := 0
	for _, backend := range backends {
		if backend.Enabled {
			enabledBackends++
		}
	}
	if len(backends) == 0 {
		add(DoctorCheck{
			ID:          "backend_count",
			Category:    "backend",
			Status:      "fail",
			Title:       "Managed backends",
			Detail:      "No Gateway backends are configured.",
			Remediation: "Ask an admin to add an enterprise-managed backend with multica gateway add.",
		})
	} else {
		add(DoctorCheck{
			ID:       "backend_count",
			Category: "backend",
			Status:   "pass",
			Title:    "Managed backends",
			Detail:   "Gateway has managed backends configured.",
			Metadata: map[string]any{"backend_count": len(backends), "enabled_backend_count": enabledBackends},
		})
	}

	var defaultBackend *db.GatewayBackend
	if !settings.DefaultBackendID.Valid {
		add(DoctorCheck{
			ID:          "default_backend",
			Category:    "backend",
			Status:      "fail",
			Title:       "Default backend",
			Detail:      "Gateway does not have a default backend.",
			Remediation: "Set a default backend with multica gateway default <backend-slug>.",
		})
	} else {
		backend, err := s.queries.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{
			WorkspaceID: workspaceUUID,
			ID:          settings.DefaultBackendID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			add(DoctorCheck{
				ID:          "default_backend",
				Category:    "backend",
				Status:      "fail",
				Title:       "Default backend",
				Detail:      "Configured default backend no longer exists.",
				Remediation: "Choose an existing backend as the default.",
			})
		} else if err != nil {
			return DoctorResponse{}, err
		} else {
			defaultBackend = &backend
			status := "pass"
			detail := "Default backend exists and is enabled."
			remediation := ""
			if !backend.Enabled {
				status = "fail"
				detail = "Default backend exists but is disabled."
				remediation = "Enable this backend or choose another default backend."
			}
			add(DoctorCheck{
				ID:          "default_backend",
				Category:    "backend",
				Status:      status,
				Title:       "Default backend",
				Detail:      detail,
				Remediation: remediation,
				Metadata: map[string]any{
					"backend_id":   uuidString(backend.ID),
					"backend_slug": backend.Slug,
					"backend_type": backend.BackendType,
				},
			})
		}
	}

	if defaultBackend != nil {
		if secretErr != nil {
			add(DoctorCheck{
				ID:          "backend_credential",
				Category:    "backend",
				Status:      "fail",
				Title:       "Default backend credential",
				Detail:      "Credential decryption cannot run because the Gateway secret is unavailable.",
				Remediation: "Fix MULTICA_GATEWAY_SECRET_KEY and restart the server.",
			})
		} else if _, err := box.DecryptString(defaultBackend.EncryptedCredential); err != nil {
			add(DoctorCheck{
				ID:          "backend_credential",
				Category:    "backend",
				Status:      "fail",
				Title:       "Default backend credential",
				Detail:      "Gateway could not decrypt the default backend credential.",
				Remediation: "Rotate the backend credential or restore the original Gateway secret key.",
			})
		} else {
			add(DoctorCheck{
				ID:       "backend_credential",
				Category: "backend",
				Status:   "pass",
				Title:    "Default backend credential",
				Detail:   "Gateway can decrypt the default backend credential.",
			})
		}
		s.addProviderRiskDoctorCheck(ctx, workspaceUUID, *defaultBackend, add)
	}

	s.addObservabilityDoctorCheck(ctx, workspaceUUID, add)
	s.addGovernanceDoctorChecks(ctx, workspaceUUID, add)

	return DoctorResponse{
		Status:      doctorOverallStatus(checks),
		Checks:      checks,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (s *Service) HealthReport(ctx context.Context, workspaceID, userID, serverBaseURL string) (HealthReportResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return HealthReportResponse{}, err
	}
	if _, err := uuidValue(userID, "user_id"); err != nil {
		return HealthReportResponse{}, err
	}

	settings, err := getSettingsOrDefaultWithQueries(ctx, s.queries, workspaceUUID)
	if err != nil {
		return HealthReportResponse{}, err
	}
	backends, err := s.queries.ListGatewayBackends(ctx, workspaceUUID)
	if err != nil {
		return HealthReportResponse{}, err
	}

	checks := make([]DoctorCheck, 0, len(backends)+8)
	add := func(check DoctorCheck) {
		checks = append(checks, check)
	}

	if err := ValidateCapturePolicy(settings.CapturePolicy); err != nil {
		add(DoctorCheck{
			ID:          "capture_policy",
			Category:    "policy",
			Status:      "fail",
			Title:       "Capture policy",
			Detail:      "Workspace capture policy is invalid.",
			Remediation: "Set capture policy to metadata_only, redacted_content, or full_content.",
		})
	} else {
		add(DoctorCheck{
			ID:       "capture_policy",
			Category: "policy",
			Status:   "pass",
			Title:    "Capture policy",
			Detail:   "Workspace capture policy is explicit.",
			Metadata: map[string]any{"capture_policy": settings.CapturePolicy},
		})
	}

	if len(backends) == 0 {
		add(DoctorCheck{
			ID:          "backend_count",
			Category:    "backend",
			Status:      "fail",
			Title:       "Managed backends",
			Detail:      "No Gateway backends are configured.",
			Remediation: "Add at least one enterprise-managed backend.",
		})
	}
	if !settings.DefaultBackendID.Valid {
		add(DoctorCheck{
			ID:          "default_backend",
			Category:    "backend",
			Status:      "fail",
			Title:       "Default backend",
			Detail:      "Gateway does not have a default backend.",
			Remediation: "Set a default backend with multica gateway default <backend-slug>.",
		})
	}

	box, secretErr := s.loadBox()
	if secretErr != nil {
		add(DoctorCheck{
			ID:          "gateway_secret",
			Category:    "gateway",
			Status:      "fail",
			Title:       "Gateway secret key",
			Detail:      "Gateway cannot decrypt backend credentials for health probes.",
			Remediation: "Set MULTICA_GATEWAY_SECRET_KEY to a base64-encoded 32-byte key and restart the server.",
		})
	} else {
		add(DoctorCheck{
			ID:       "gateway_secret",
			Category: "gateway",
			Status:   "pass",
			Title:    "Gateway secret key",
			Detail:   "Gateway credential decryption is configured.",
		})
	}

	now := time.Now().UTC()
	items := make([]BackendHealthItem, 0, len(backends))
	for _, backend := range backends {
		item, check := s.backendHealthItem(ctx, workspaceUUID, settings.DefaultBackendID, backend, box, secretErr, now)
		items = append(items, item)
		add(check)
	}

	governance := s.governanceHealthSummary(ctx, workspaceUUID, settings.CapturePolicy, add)
	urls := BuildGatewayURLs(serverBaseURL)
	return HealthReportResponse{
		Status:           doctorOverallStatus(checks),
		GeneratedAt:      now.Format(time.RFC3339Nano),
		OpenAIBaseURL:    urls.OpenAIBaseURL,
		AnthropicBaseURL: urls.AnthropicBaseURL,
		Backends:         items,
		Governance:       governance,
		Checks:           checks,
	}, nil
}

func (s *Service) backendHealthItem(ctx context.Context, workspaceID, defaultBackendID pgtype.UUID, backend db.GatewayBackend, box *secrets.Box, secretErr error, now time.Time) (BackendHealthItem, DoctorCheck) {
	credentials, err := s.queries.ListGatewayBackendCredentialsForBackend(ctx, db.ListGatewayBackendCredentialsForBackendParams{
		WorkspaceID: workspaceID,
		BackendID:   backend.ID,
	})
	if err != nil {
		return BackendHealthItem{
				ID:                uuidString(backend.ID),
				Slug:              backend.Slug,
				DisplayName:       backend.DisplayName,
				BackendType:       backend.BackendType,
				BaseURL:           backend.BaseUrl,
				Enabled:           backend.Enabled,
				IsDefault:         defaultBackendID.Valid && backend.ID == defaultBackendID,
				ProbeStatus:       "fail",
				LastError:         err.Error(),
				CredentialSummary: CredentialHealthSummary{},
			}, DoctorCheck{
				ID:          "backend_probe_" + backend.Slug,
				Category:    "backend",
				Status:      "fail",
				Title:       "Backend probe " + backend.Slug,
				Detail:      "Gateway could not read backend credentials.",
				Remediation: "Review backend credential storage.",
			}
	}

	summary := credentialHealthSummary(credentials, now)
	item := BackendHealthItem{
		ID:                uuidString(backend.ID),
		Slug:              backend.Slug,
		DisplayName:       backend.DisplayName,
		BackendType:       backend.BackendType,
		BaseURL:           backend.BaseUrl,
		Enabled:           backend.Enabled,
		IsDefault:         defaultBackendID.Valid && backend.ID == defaultBackendID,
		ProbeStatus:       "skipped",
		CredentialSummary: summary,
	}

	if !backend.Enabled {
		item.LastError = "backend disabled"
		return item, DoctorCheck{
			ID:          "backend_probe_" + backend.Slug,
			Category:    "backend",
			Status:      "warning",
			Title:       "Backend probe " + backend.Slug,
			Detail:      "Backend is disabled; probe skipped.",
			Remediation: "Enable the backend before routing traffic to it.",
		}
	}
	if backend.BackendType == BackendTypeClaudeOAuth {
		item.LastError = "Claude OAuth backends are sidecar-managed"
		return item, DoctorCheck{
			ID:          "backend_probe_" + backend.Slug,
			Category:    "backend",
			Status:      "warning",
			Title:       "Backend probe " + backend.Slug,
			Detail:      "Claude OAuth backend probes require the sidecar runtime.",
			Remediation: "Use the Dario-compatible sidecar health check when Claude OAuth routing is enabled.",
		}
	}
	if secretErr != nil {
		item.ProbeStatus = "fail"
		item.LastError = "gateway secret unavailable"
		return item, DoctorCheck{
			ID:          "backend_probe_" + backend.Slug,
			Category:    "backend",
			Status:      "fail",
			Title:       "Backend probe " + backend.Slug,
			Detail:      "Gateway cannot decrypt backend credentials.",
			Remediation: "Fix MULTICA_GATEWAY_SECRET_KEY and restart the server.",
		}
	}

	encryptedCredential := backend.EncryptedCredential
	for _, credential := range credentials {
		if credential.Enabled && (!credential.RateLimitedUntil.Valid || !credential.RateLimitedUntil.Time.After(now)) {
			encryptedCredential = credential.EncryptedCredential
			break
		}
	}
	secret, err := box.DecryptString(encryptedCredential)
	if err != nil {
		item.ProbeStatus = "fail"
		item.LastError = "credential decrypt failed"
		return item, DoctorCheck{
			ID:          "backend_probe_" + backend.Slug,
			Category:    "backend",
			Status:      "fail",
			Title:       "Backend probe " + backend.Slug,
			Detail:      "Gateway could not decrypt a backend credential.",
			Remediation: "Rotate the backend credential or restore the original Gateway secret key.",
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	modelCount, latency, err := s.probeBackendModels(probeCtx, backend, secret)
	item.ProbeLatencyMS = latency
	item.ModelCount = modelCount
	if err != nil {
		item.ProbeStatus = "fail"
		item.LastError = err.Error()
		return item, DoctorCheck{
			ID:          "backend_probe_" + backend.Slug,
			Category:    "backend",
			Status:      "fail",
			Title:       "Backend probe " + backend.Slug,
			Detail:      "Backend model-list probe failed.",
			Remediation: "Verify the backend base URL, credential, egress path, and provider account status.",
			Metadata:    map[string]any{"backend_slug": backend.Slug, "error": err.Error()},
		}
	}
	item.ProbeStatus = "pass"
	return item, DoctorCheck{
		ID:       "backend_probe_" + backend.Slug,
		Category: "backend",
		Status:   "pass",
		Title:    "Backend probe " + backend.Slug,
		Detail:   "Backend model-list probe passed.",
		Metadata: map[string]any{
			"backend_slug":     backend.Slug,
			"model_count":      modelCount,
			"probe_latency_ms": latency,
		},
	}
}

func credentialHealthSummary(credentials []db.GatewayBackendCredential, now time.Time) CredentialHealthSummary {
	summary := CredentialHealthSummary{Total: len(credentials)}
	for _, credential := range credentials {
		if credential.Enabled {
			summary.Enabled++
		} else {
			summary.Disabled++
		}
		if credential.RateLimitedUntil.Valid && credential.RateLimitedUntil.Time.After(now) {
			summary.RateLimited++
		}
		if credential.LastErrorAt.Valid || strings.TrimSpace(credential.LastError) != "" {
			summary.LastErrors++
		}
	}
	return summary
}

func (s *Service) probeBackendModels(ctx context.Context, backend db.GatewayBackend, secret string) (int, int64, error) {
	path := "/models"
	if backend.BackendType == BackendTypeAnthropic {
		path = "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURLPath(backend.BaseUrl, path), nil)
	if err != nil {
		return 0, 0, err
	}
	switch backend.BackendType {
	case BackendTypeAnthropic:
		req.Header.Set("x-api-key", secret)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	started := time.Now()
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	latency := time.Since(started).Milliseconds()
	if latency <= 0 {
		latency = 1
	}
	if err != nil {
		return 0, latency, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, latency, fmt.Errorf("model-list probe returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, latency, err
	}
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, latency, err
	}
	return len(payload.Data), latency, nil
}

func joinURLPath(base, path string) string {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return strings.TrimRight(base, "/") + path
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	return parsed.String()
}

func (s *Service) governanceHealthSummary(ctx context.Context, workspaceID pgtype.UUID, capturePolicy string, add func(DoctorCheck)) GovernanceHealthSummary {
	summary := GovernanceHealthSummary{CapturePolicy: capturePolicy}

	policies, policyErr := s.queries.ListGatewayPolicies(ctx, workspaceID)
	if policyErr == nil {
		summary.GovernancePolicyCount = len(policies)
		for _, policy := range policies {
			if policy.Enabled {
				summary.EnabledPolicyCount++
			}
		}
	} else {
		add(DoctorCheck{ID: "governance_policies", Category: "governance", Status: "warning", Title: "Governance policies", Detail: "Gateway policies could not be checked.", Remediation: "Review Gateway policy storage."})
	}

	decisions, decisionErr := s.queries.ListGatewayPolicyDecisions(ctx, db.ListGatewayPolicyDecisionsParams{
		WorkspaceID: workspaceID,
		Limit:       100,
		Since:       pgtype.Timestamptz{Time: time.Now().Add(-30 * 24 * time.Hour), Valid: true},
	})
	if decisionErr == nil {
		for _, decision := range decisions {
			approvalStatus := optionalTextString(decision.ApprovalStatus)
			if approvalStatus == "requested" || approvalStatus == "pending" {
				summary.PendingApprovalCount++
			}
		}
	} else {
		add(DoctorCheck{ID: "policy_decisions", Category: "governance", Status: "warning", Title: "Policy decisions", Detail: "Gateway policy decisions could not be checked.", Remediation: "Review Gateway policy decision storage."})
	}

	risks, riskErr := s.queries.ListAIThirdPartyRisk(ctx, workspaceID)
	if riskErr == nil {
		for _, risk := range risks {
			_, blocked := providerRiskBlockReason(risk.ContractStatus, risk.SecurityReviewStatus)
			if blocked {
				summary.ProviderRiskWarningCount++
			}
		}
	} else {
		add(DoctorCheck{ID: "provider_risks", Category: "governance", Status: "warning", Title: "Provider risk register", Detail: "Provider risk records could not be checked.", Remediation: "Review provider risk storage."})
	}

	incidents, incidentErr := s.queries.ListAIIncidents(ctx, db.ListAIIncidentsParams{WorkspaceID: workspaceID, Limit: 100})
	if incidentErr == nil {
		for _, incident := range incidents {
			if incident.Status != "closed" && incident.Status != "remediated" {
				summary.OpenIncidentCount++
			}
		}
	} else {
		add(DoctorCheck{ID: "open_incidents", Category: "governance", Status: "warning", Title: "Open incidents", Detail: "Gateway incidents could not be checked.", Remediation: "Review incident storage."})
	}
	if summary.OpenIncidentCount > 0 {
		add(DoctorCheck{
			ID:          "open_incidents",
			Category:    "governance",
			Status:      "warning",
			Title:       "Open incidents",
			Detail:      fmt.Sprintf("%d Gateway incident(s) need review.", summary.OpenIncidentCount),
			Remediation: "Review and remediate open Gateway incidents.",
			Metadata:    map[string]any{"open_count": summary.OpenIncidentCount},
		})
	} else if incidentErr == nil {
		add(DoctorCheck{ID: "open_incidents", Category: "governance", Status: "pass", Title: "Open incidents", Detail: "No open Gateway incidents need review."})
	}

	evidence, evidenceErr := s.queries.ListAIEvidence(ctx, db.ListAIEvidenceParams{WorkspaceID: workspaceID, Limit: 100})
	if evidenceErr == nil {
		summary.EvidenceCount = len(evidence)
	} else {
		add(DoctorCheck{ID: "evidence", Category: "governance", Status: "warning", Title: "Evidence records", Detail: "Evidence records could not be checked.", Remediation: "Review governance evidence storage."})
	}

	controls, controlsErr := s.queries.ListAIControlMappingsWithEvidence(ctx, workspaceID)
	if controlsErr == nil {
		summary.ControlMappingCount = len(controls)
	} else {
		add(DoctorCheck{ID: "control_mappings", Category: "governance", Status: "warning", Title: "Control mappings", Detail: "Compliance controls could not be checked.", Remediation: "Review compliance mapping storage."})
	}

	if summary.PendingApprovalCount > 0 || summary.ProviderRiskWarningCount > 0 {
		add(DoctorCheck{
			ID:          "governance_attention",
			Category:    "governance",
			Status:      "warning",
			Title:       "Governance attention",
			Detail:      "Gateway governance has pending approvals or provider risk warnings.",
			Remediation: "Review pending decisions and provider risk records before expanding rollout.",
			Metadata: map[string]any{
				"pending_approval_count":      summary.PendingApprovalCount,
				"provider_risk_warning_count": summary.ProviderRiskWarningCount,
			},
		})
	}

	return summary
}

func (s *Service) addProviderRiskDoctorCheck(ctx context.Context, workspaceID pgtype.UUID, backend db.GatewayBackend, add func(DoctorCheck)) {
	risks, err := s.queries.ListAIThirdPartyRisk(ctx, workspaceID)
	if err != nil {
		add(DoctorCheck{
			ID:          "provider_risk",
			Category:    "policy",
			Status:      "warning",
			Title:       "Provider risk register",
			Detail:      "Provider risk could not be checked.",
			Remediation: "Review Gateway provider risk configuration.",
		})
		return
	}
	backendID := uuidString(backend.ID)
	for _, risk := range risks {
		if risk.ProviderName != backend.Slug && optionalUUIDString(risk.BackendID) != backendID {
			continue
		}
		reason, blocked := providerRiskBlockReason(risk.ContractStatus, risk.SecurityReviewStatus)
		if !blocked {
			add(DoctorCheck{
				ID:       "provider_risk",
				Category: "policy",
				Status:   "pass",
				Title:    "Provider risk register",
				Detail:   "Default backend provider risk status allows routing.",
				Metadata: map[string]any{
					"provider_name":          risk.ProviderName,
					"contract_status":        risk.ContractStatus,
					"security_review_status": risk.SecurityReviewStatus,
				},
			})
			return
		}
		exceptionID, err := s.activeProviderExceptionID(ctx, workspaceID, backendID, backend.Slug)
		if err != nil {
			add(DoctorCheck{
				ID:          "provider_risk",
				Category:    "policy",
				Status:      "fail",
				Title:       "Provider risk register",
				Detail:      "Default backend is blocked and policy exceptions could not be checked.",
				Remediation: "Review provider risk and exception records.",
			})
			return
		}
		if exceptionID != "" {
			add(DoctorCheck{
				ID:          "provider_risk",
				Category:    "policy",
				Status:      "warning",
				Title:       "Provider risk register",
				Detail:      "Default backend is blocked by provider risk but has an active exception.",
				Remediation: "Review the exception expiry and complete the underlying provider risk remediation.",
				Metadata: map[string]any{
					"provider_name":       risk.ProviderName,
					"reason_code":         reason,
					"policy_exception_id": exceptionID,
				},
			})
			return
		}
		add(DoctorCheck{
			ID:          "provider_risk",
			Category:    "policy",
			Status:      "fail",
			Title:       "Provider risk register",
			Detail:      "Default backend is blocked by provider risk.",
			Remediation: "Approve the provider risk review, choose another backend, or approve a temporary policy exception.",
			Metadata: map[string]any{
				"provider_name": risk.ProviderName,
				"reason_code":   reason,
			},
		})
		return
	}
	add(DoctorCheck{
		ID:          "provider_risk",
		Category:    "policy",
		Status:      "warning",
		Title:       "Provider risk register",
		Detail:      "Default backend does not have a provider risk assessment.",
		Remediation: "Add a provider risk record before broad enterprise rollout.",
	})
}

func (s *Service) addObservabilityDoctorCheck(ctx context.Context, workspaceID pgtype.UUID, add func(DoctorCheck)) {
	summary, err := s.queries.GetGatewayOverviewSummary(ctx, db.GetGatewayOverviewSummaryParams{
		WorkspaceID: workspaceID,
		Since:       pgtype.Timestamptz{Time: time.Now().Add(-7 * 24 * time.Hour), Valid: true},
	})
	if err != nil {
		add(DoctorCheck{
			ID:          "recent_traffic",
			Category:    "observability",
			Status:      "warning",
			Title:       "Recent Gateway traffic",
			Detail:      "Recent Gateway traffic could not be checked.",
			Remediation: "Review Gateway telemetry storage and database connectivity.",
		})
		return
	}
	if summary.RequestCount == 0 {
		add(DoctorCheck{
			ID:          "recent_traffic",
			Category:    "observability",
			Status:      "warning",
			Title:       "Recent Gateway traffic",
			Detail:      "No Gateway requests were observed in the last 7 days.",
			Remediation: "Send a test request through the generated OpenAI or Anthropic Gateway base URL.",
		})
		return
	}
	add(DoctorCheck{
		ID:       "recent_traffic",
		Category: "observability",
		Status:   "pass",
		Title:    "Recent Gateway traffic",
		Detail:   "Gateway telemetry has recent requests.",
		Metadata: map[string]any{
			"request_count":           summary.RequestCount,
			"llm_call_count":          summary.LlmCallCount,
			"streaming_request_count": summary.StreamingRequestCount,
		},
	})
}

func (s *Service) addGovernanceDoctorChecks(ctx context.Context, workspaceID pgtype.UUID, add func(DoctorCheck)) {
	since := pgtype.Timestamptz{Time: time.Now().Add(-30 * 24 * time.Hour), Valid: true}
	decisions, decisionErr := s.queries.ListGatewayPolicyDecisions(ctx, db.ListGatewayPolicyDecisionsParams{
		WorkspaceID: workspaceID,
		Limit:       10,
		Since:       since,
	})
	if decisionErr == nil && len(decisions) > 0 {
		add(DoctorCheck{ID: "policy_decisions", Category: "governance", Status: "pass", Title: "Policy decisions", Detail: "Gateway policy decisions are being recorded.", Metadata: map[string]any{"recent_count": len(decisions)}})
	} else if decisionErr != nil {
		add(DoctorCheck{ID: "policy_decisions", Category: "governance", Status: "warning", Title: "Policy decisions", Detail: "Policy decisions could not be checked.", Remediation: "Review Gateway policy decision storage."})
	} else {
		add(DoctorCheck{ID: "policy_decisions", Category: "governance", Status: "warning", Title: "Policy decisions", Detail: "No policy decisions were recorded in the last 30 days.", Remediation: "Policy decisions appear when Gateway enforces or records governance outcomes."})
	}

	evidence, evidenceErr := s.queries.ListAIEvidence(ctx, db.ListAIEvidenceParams{WorkspaceID: workspaceID, Limit: 10})
	if evidenceErr == nil && len(evidence) > 0 {
		add(DoctorCheck{ID: "evidence", Category: "governance", Status: "pass", Title: "Evidence records", Detail: "Gateway governance evidence exists.", Metadata: map[string]any{"recent_count": len(evidence)}})
	} else if evidenceErr != nil {
		add(DoctorCheck{ID: "evidence", Category: "governance", Status: "warning", Title: "Evidence records", Detail: "Evidence records could not be checked.", Remediation: "Review governance evidence storage."})
	} else {
		add(DoctorCheck{ID: "evidence", Category: "governance", Status: "warning", Title: "Evidence records", Detail: "No Gateway governance evidence exists yet.", Remediation: "Evidence is generated when Gateway governance decisions create auditable records."})
	}

	controls, controlsErr := s.queries.ListAIControlMappingsWithEvidence(ctx, workspaceID)
	if controlsErr == nil && len(controls) > 0 {
		add(DoctorCheck{ID: "control_mappings", Category: "governance", Status: "pass", Title: "Control mappings", Detail: "Gateway compliance control mappings are configured.", Metadata: map[string]any{"control_count": len(controls)}})
	} else if controlsErr != nil {
		add(DoctorCheck{ID: "control_mappings", Category: "governance", Status: "warning", Title: "Control mappings", Detail: "Compliance control mappings could not be checked.", Remediation: "Review Gateway compliance mapping storage."})
	} else {
		add(DoctorCheck{ID: "control_mappings", Category: "governance", Status: "warning", Title: "Control mappings", Detail: "Gateway compliance control mappings are not initialized.", Remediation: "Open Settings -> Gateway -> Compliance Controls or initialize default mappings."})
	}

	incidents, incidentErr := s.queries.ListAIIncidents(ctx, db.ListAIIncidentsParams{WorkspaceID: workspaceID, Limit: 100})
	if incidentErr != nil {
		add(DoctorCheck{ID: "open_incidents", Category: "governance", Status: "warning", Title: "Open incidents", Detail: "Gateway incidents could not be checked.", Remediation: "Review incident storage."})
		return
	}
	openCount := 0
	for _, incident := range incidents {
		if incident.Status != "closed" && incident.Status != "remediated" {
			openCount++
		}
	}
	if openCount > 0 {
		add(DoctorCheck{ID: "open_incidents", Category: "governance", Status: "warning", Title: "Open incidents", Detail: fmt.Sprintf("%d Gateway incident(s) need review.", openCount), Remediation: "Review and remediate open Gateway incidents.", Metadata: map[string]any{"open_count": openCount}})
	} else {
		add(DoctorCheck{ID: "open_incidents", Category: "governance", Status: "pass", Title: "Open incidents", Detail: "No open Gateway incidents need review."})
	}
}

func (s *Service) activeProviderExceptionID(ctx context.Context, workspaceID pgtype.UUID, backendID, providerSlug string) (string, error) {
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
	exception, err := s.queries.GetActiveAIPolicyExceptionForProvider(ctx, db.GetActiveAIPolicyExceptionForProviderParams{
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
	return uuidString(exception.ID), nil
}

func providerRiskBlockReason(contractStatus, securityReviewStatus string) (string, bool) {
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

func doctorOverallStatus(checks []DoctorCheck) string {
	hasWarning := false
	for _, check := range checks {
		if check.Status == "fail" {
			return "unhealthy"
		}
		if check.Status == "warning" {
			hasWarning = true
		}
	}
	if hasWarning {
		return "healthy_with_warnings"
	}
	return "healthy"
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

func (s *Service) ListBackendCredentials(ctx context.Context, workspaceID, backendID string) ([]BackendCredentialResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return nil, err
	}
	backendUUID, err := uuidValue(backendID, "backend_id")
	if err != nil {
		return nil, err
	}
	if _, err := s.queries.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{
		WorkspaceID: workspaceUUID,
		ID:          backendUUID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGatewayBackendNotFound
	} else if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListGatewayBackendCredentialsForBackend(ctx, db.ListGatewayBackendCredentialsForBackendParams{
		WorkspaceID: workspaceUUID,
		BackendID:   backendUUID,
	})
	if err != nil {
		return nil, err
	}
	items := make([]BackendCredentialResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, backendCredentialResponse(row))
	}
	return items, nil
}

func (s *Service) CreateBackendCredential(ctx context.Context, input CreateBackendCredentialInput) (BackendCredentialResponse, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return BackendCredentialResponse{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return BackendCredentialResponse{}, err
	}
	backendUUID, err := uuidValue(input.BackendID, "backend_id")
	if err != nil {
		return BackendCredentialResponse{}, err
	}
	credential := strings.TrimSpace(input.Key)
	if credential == "" {
		return BackendCredentialResponse{}, fmt.Errorf("%w: credential is required", ErrInvalidGatewayBackend)
	}
	priority := input.Priority
	if priority <= 0 {
		priority = 100
	}
	label := strings.TrimSpace(input.Label)
	if label == "" {
		label = "Gateway credential"
	}

	box, err := s.loadBox()
	if err != nil {
		return BackendCredentialResponse{}, normalizeSecretError(err)
	}
	encryptedCredential, err := box.EncryptString(credential)
	if err != nil {
		return BackendCredentialResponse{}, normalizeSecretError(err)
	}

	var created db.GatewayBackendCredential
	if err := s.withTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{
			WorkspaceID: workspaceUUID,
			ID:          backendUUID,
		}); errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayBackendNotFound
		} else if err != nil {
			return err
		}
		row, err := q.CreateGatewayBackendCredential(ctx, db.CreateGatewayBackendCredentialParams{
			WorkspaceID:          workspaceUUID,
			BackendID:            backendUUID,
			Label:                label,
			EncryptedCredential:  encryptedCredential,
			CredentialHint:       CredentialHint(credential),
			Enabled:              input.Enabled,
			Priority:             priority,
			CredentialType:       credentialTypeOrDefault(input.CredentialType),
			SubscriptionProvider: strings.TrimSpace(input.SubscriptionProvider),
			EncryptedPayload:     encryptedPayloadForCredential(credentialTypeOrDefault(input.CredentialType), encryptedCredential),
			PayloadFormat:        strings.TrimSpace(input.PayloadFormat),
			DispatchScope:        dispatchScopeOrDefault(input.DispatchScope),
			ValidationStatus:     strings.TrimSpace(input.ValidationStatus),
			CreatedBy:            actorUUID,
		})
		if err != nil {
			return err
		}
		created = row
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.backend_credential.create", "gateway_backend_credential", uuidString(row.ID), nil, backendCredentialResponse(row))
	}); err != nil {
		return BackendCredentialResponse{}, err
	}
	return backendCredentialResponse(created), nil
}

func (s *Service) UpdateBackendCredential(ctx context.Context, input UpdateBackendCredentialInput) (BackendCredentialResponse, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return BackendCredentialResponse{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return BackendCredentialResponse{}, err
	}
	backendUUID, err := uuidValue(input.BackendID, "backend_id")
	if err != nil {
		return BackendCredentialResponse{}, err
	}
	credentialUUID, err := uuidValue(input.CredentialID, "credential_id")
	if err != nil {
		return BackendCredentialResponse{}, err
	}

	var updated db.GatewayBackendCredential
	if err := s.withTx(ctx, func(q *db.Queries) error {
		current, err := q.GetGatewayBackendCredentialByID(ctx, db.GetGatewayBackendCredentialByIDParams{
			WorkspaceID: workspaceUUID,
			BackendID:   backendUUID,
			ID:          credentialUUID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayBackendNotFound
		}
		if err != nil {
			return err
		}

		label := current.Label
		if input.Label != nil {
			label = strings.TrimSpace(*input.Label)
			if label == "" {
				label = "Gateway credential"
			}
		}
		encryptedCredential := current.EncryptedCredential
		credentialHint := current.CredentialHint
		if input.Key != nil {
			credential := strings.TrimSpace(*input.Key)
			if credential == "" {
				return fmt.Errorf("%w: credential is required", ErrInvalidGatewayBackend)
			}
			box, err := s.loadBox()
			if err != nil {
				return normalizeSecretError(err)
			}
			encryptedCredential, err = box.EncryptString(credential)
			if err != nil {
				return normalizeSecretError(err)
			}
			credentialHint = CredentialHint(credential)
		}
		enabled := current.Enabled
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		priority := current.Priority
		if input.Priority != nil {
			priority = *input.Priority
			if priority <= 0 {
				priority = 100
			}
		}
		credentialType := current.CredentialType
		if input.CredentialType != nil {
			credentialType = credentialTypeOrDefault(*input.CredentialType)
		}
		subscriptionProvider := current.SubscriptionProvider
		if input.SubscriptionProvider != nil {
			subscriptionProvider = strings.TrimSpace(*input.SubscriptionProvider)
		}
		encryptedPayload := current.EncryptedPayload
		if credentialType == CredentialTypeSubscriptionBundle && input.Key != nil {
			encryptedPayload = encryptedCredential
		}
		payloadFormat := current.PayloadFormat
		if input.PayloadFormat != nil {
			payloadFormat = strings.TrimSpace(*input.PayloadFormat)
		}
		dispatchScope := current.DispatchScope
		if input.DispatchScope != nil {
			dispatchScope = dispatchScopeOrDefault(*input.DispatchScope)
		}
		validationStatus := current.ValidationStatus
		if input.ValidationStatus != nil {
			validationStatus = strings.TrimSpace(*input.ValidationStatus)
		}

		row, err := q.UpdateGatewayBackendCredential(ctx, db.UpdateGatewayBackendCredentialParams{
			WorkspaceID:          workspaceUUID,
			BackendID:            backendUUID,
			ID:                   credentialUUID,
			Label:                label,
			EncryptedCredential:  encryptedCredential,
			CredentialHint:       credentialHint,
			Enabled:              enabled,
			Priority:             priority,
			CredentialType:       credentialType,
			SubscriptionProvider: subscriptionProvider,
			EncryptedPayload:     encryptedPayload,
			PayloadFormat:        payloadFormat,
			DispatchScope:        dispatchScope,
			ValidationStatus:     validationStatus,
			UpdatedBy:            actorUUID,
		})
		if err != nil {
			return err
		}
		updated = row
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.backend_credential.update", "gateway_backend_credential", uuidString(row.ID), backendCredentialResponse(current), backendCredentialResponse(row))
	}); err != nil {
		return BackendCredentialResponse{}, err
	}
	return backendCredentialResponse(updated), nil
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
			WorkspaceID:          workspaceUUID,
			Slug:                 normalized.Slug,
			DisplayName:          normalized.DisplayName,
			BackendType:          normalized.BackendType,
			BaseUrl:              normalized.BaseURL,
			EncryptedCredential:  encryptedCredential,
			CredentialHint:       CredentialHint(credential),
			Enabled:              normalized.Enabled,
			Metadata:             metadata,
			Transport:            normalized.Transport,
			SubscriptionProvider: normalized.SubscriptionProvider,
			DispatchScope:        normalized.DispatchScope,
			ValidationStatus:     normalized.ValidationStatus,
			CreatedBy:            actorUUID,
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
		transport := current.Transport
		if input.Transport != nil {
			transport = transportOrDefault(*input.Transport)
		}
		subscriptionProvider := current.SubscriptionProvider
		if input.SubscriptionProvider != nil {
			subscriptionProvider = strings.TrimSpace(*input.SubscriptionProvider)
		}
		dispatchScope := current.DispatchScope
		if input.DispatchScope != nil {
			dispatchScope = dispatchScopeOrDefault(*input.DispatchScope)
		}
		validationStatus := current.ValidationStatus
		if input.ValidationStatus != nil {
			validationStatus = strings.TrimSpace(*input.ValidationStatus)
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
			WorkspaceID:          workspaceUUID,
			ID:                   backendUUID,
			DisplayName:          displayName,
			BackendType:          current.BackendType,
			BaseUrl:              baseURL,
			EncryptedCredential:  encryptedCredential,
			CredentialHint:       credentialHint,
			Enabled:              enabled,
			Metadata:             metadata,
			Transport:            transport,
			SubscriptionProvider: subscriptionProvider,
			DispatchScope:        dispatchScope,
			ValidationStatus:     validationStatus,
			UpdatedBy:            actorUUID,
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

func (s *Service) RecordExportAudit(ctx context.Context, workspaceID, actorUserID string, details map[string]any) error {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return err
	}
	actorUUID, err := uuidValue(actorUserID, "actor_user_id")
	if err != nil {
		return err
	}
	return audit(ctx, s.queries, workspaceUUID, actorUUID, "gateway.export.read", "gateway_export", uuidString(workspaceUUID), nil, details)
}

func (s *Service) RecordEvidenceExport(ctx context.Context, input RecordEvidenceExportInput) (EvidenceExportDetail, error) {
	exportUUID, err := uuidValue(input.ID, "export_id")
	if err != nil {
		return EvidenceExportDetail{}, err
	}
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return EvidenceExportDetail{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return EvidenceExportDetail{}, err
	}
	sections, err := json.Marshal(input.Sections)
	if err != nil {
		return EvidenceExportDetail{}, err
	}
	snapshot, err := json.Marshal(input.BundleSnapshot)
	if err != nil {
		return EvidenceExportDetail{}, err
	}
	row, err := s.queries.CreateAIEvidenceExport(ctx, db.CreateAIEvidenceExportParams{
		ID:             exportUUID,
		WorkspaceID:    workspaceUUID,
		ActorUserID:    actorUUID,
		ExportType:     strings.TrimSpace(input.ExportType),
		SubjectType:    strings.TrimSpace(input.SubjectType),
		SubjectID:      strings.TrimSpace(input.SubjectID),
		DigestSha256:   strings.TrimSpace(input.DigestSHA256),
		Sections:       sections,
		BundleSnapshot: snapshot,
	})
	if err != nil {
		return EvidenceExportDetail{}, err
	}
	return evidenceExportDetailFromRow(db.GetAIEvidenceExportRow{
		ID:             row.ID,
		WorkspaceID:    row.WorkspaceID,
		ActorUserID:    row.ActorUserID,
		ExportType:     row.ExportType,
		SubjectType:    row.SubjectType,
		SubjectID:      row.SubjectID,
		DigestSha256:   row.DigestSha256,
		Sections:       row.Sections,
		BundleSnapshot: row.BundleSnapshot,
		CreatedAt:      row.CreatedAt,
	}), nil
}

func (s *Service) ListEvidenceExports(ctx context.Context, workspaceID string, limit int32) ([]EvidenceExportItem, error) {
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
	rows, err := s.queries.ListAIEvidenceExports(ctx, db.ListAIEvidenceExportsParams{
		WorkspaceID: workspaceUUID,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}
	items := make([]EvidenceExportItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, evidenceExportItem(row))
	}
	return items, nil
}

func (s *Service) GetEvidenceExport(ctx context.Context, workspaceID, exportID string) (EvidenceExportDetail, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return EvidenceExportDetail{}, err
	}
	exportUUID, err := uuidValue(exportID, "export_id")
	if err != nil {
		return EvidenceExportDetail{}, err
	}
	row, err := s.queries.GetAIEvidenceExport(ctx, db.GetAIEvidenceExportParams{
		WorkspaceID: workspaceUUID,
		ID:          exportUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return EvidenceExportDetail{}, ErrGatewayEvidenceExportNotFound
	}
	if err != nil {
		return EvidenceExportDetail{}, err
	}
	return evidenceExportDetailFromRow(row), nil
}

func (s *Service) ListPolicyDecisions(ctx context.Context, workspaceID string, limit int32) ([]PolicyDecisionItem, error) {
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

	rows, err := s.queries.ListGatewayPolicyDecisions(ctx, db.ListGatewayPolicyDecisionsParams{
		WorkspaceID: workspaceUUID,
		Limit:       limit,
		Since:       pgtype.Timestamptz{Time: time.Now().Add(-30 * 24 * time.Hour), Valid: true},
	})
	if err != nil {
		return nil, err
	}

	items := make([]PolicyDecisionItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, policyDecisionItem(row))
	}
	return items, nil
}

func (s *Service) GovernanceInsights(ctx context.Context, workspaceID string) (GovernanceInsightsResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return GovernanceInsightsResponse{}, err
	}
	now := time.Now()
	since := now.Add(-30 * 24 * time.Hour)
	sinceParam := pgtype.Timestamptz{Time: since, Valid: true}

	decisions, err := s.queries.ListGatewayPolicyDecisions(ctx, db.ListGatewayPolicyDecisionsParams{
		WorkspaceID: workspaceUUID,
		Limit:       100,
		Since:       sinceParam,
	})
	if err != nil {
		return GovernanceInsightsResponse{}, err
	}
	incidents, err := s.ListIncidents(ctx, workspaceID, 100)
	if err != nil {
		return GovernanceInsightsResponse{}, err
	}
	evidence, err := s.ListEvidence(ctx, workspaceID, 100)
	if err != nil {
		return GovernanceInsightsResponse{}, err
	}
	controls, err := s.ListControlMappings(ctx, workspaceID)
	if err != nil {
		return GovernanceInsightsResponse{}, err
	}
	exceptions, err := s.ListPolicyExceptions(ctx, workspaceID, 100)
	if err != nil {
		return GovernanceInsightsResponse{}, err
	}
	risks, err := s.ListProviderRisks(ctx, workspaceID)
	if err != nil {
		return GovernanceInsightsResponse{}, err
	}
	topModels, err := s.queries.ListGatewayTopModels(ctx, db.ListGatewayTopModelsParams{
		WorkspaceID: workspaceUUID,
		Limit:       10,
		Since:       sinceParam,
	})
	if err != nil {
		return GovernanceInsightsResponse{}, err
	}
	topBackends, err := s.queries.ListGatewayTopBackends(ctx, db.ListGatewayTopBackendsParams{
		WorkspaceID: workspaceUUID,
		Limit:       10,
		Since:       sinceParam,
	})
	if err != nil {
		return GovernanceInsightsResponse{}, err
	}

	resp := GovernanceInsightsResponse{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Since:       since.UTC().Format(time.RFC3339),
	}
	reasonCounts := map[string]int{}
	blockedResources := map[string]GovernanceResourceCount{}
	for _, decision := range decisions {
		item := policyDecisionItem(decision)
		switch item.Decision {
		case "block":
			resp.RiskOverview.BlockedDecisionCount++
			key := item.ResourceType + "\x00" + item.ResourceID + "\x00" + item.ResourceLabel
			blocked := blockedResources[key]
			if blocked.Count == 0 {
				blocked = GovernanceResourceCount{ResourceType: item.ResourceType, ResourceID: item.ResourceID, ResourceLabel: item.ResourceLabel}
			}
			blocked.Count++
			blockedResources[key] = blocked
		case "warn":
			resp.RiskOverview.WarnDecisionCount++
		}
		if item.ApprovalStatus == "requested" || item.ApprovalStatus == "pending" || item.Decision == "require_approval" {
			resp.RiskOverview.PendingApprovalCount++
		}
		if item.ReasonCode != "" {
			reasonCounts[item.ReasonCode]++
		}
		if item.ApprovalStatus == "requested" || item.Decision == "require_approval" {
			resp.ActionQueue = append(resp.ActionQueue, GovernanceActionItem{
				Kind:         "policy_decision",
				Severity:     "medium",
				Title:        "Policy decision needs review",
				Detail:       fmt.Sprintf("%s %s requires review: %s", item.ResourceType, item.ResourceLabel, item.ReasonCode),
				ResourceType: item.ResourceType,
				ResourceID:   item.ID,
				CreatedAt:    item.CreatedAt,
			})
		}
	}
	resp.BehaviorTrends.PolicyReasonCounts = governanceReasonCounts(reasonCounts)
	resp.BehaviorTrends.BlockedResources = governanceResourceCounts(blockedResources)

	for _, incident := range incidents {
		open := incident.Status != "closed" && incident.Status != "remediated"
		if !open {
			continue
		}
		resp.RiskOverview.OpenIncidentCount++
		if incident.Severity == "high" || incident.Severity == "critical" {
			resp.RiskOverview.HighSeverityOpenIncidentCount++
		}
		resp.ActionQueue = append(resp.ActionQueue, GovernanceActionItem{
			Kind:         "incident",
			Severity:     incident.Severity,
			Title:        incident.Summary,
			Detail:       incident.RemediationNotes,
			ResourceType: "incident",
			ResourceID:   incident.ID,
			CreatedAt:    incident.OpenedAt,
		})
	}

	resp.ComplianceCoverage.EvidenceCount = len(evidence)
	for _, item := range evidence {
		if item.GeneratedAt > resp.ComplianceCoverage.LastEvidenceGeneratedAt {
			resp.ComplianceCoverage.LastEvidenceGeneratedAt = item.GeneratedAt
		}
	}
	for _, control := range controls {
		resp.ComplianceCoverage.ControlCount++
		switch strings.ToLower(strings.TrimSpace(control.Status)) {
		case "covered":
			resp.ComplianceCoverage.CoveredControlCount++
		case "gap", "not_started":
			resp.ComplianceCoverage.GapControlCount++
			resp.RiskOverview.ControlGapCount++
			resp.ActionQueue = append(resp.ActionQueue, GovernanceActionItem{
				Kind:         "control_gap",
				Severity:     "medium",
				Title:        "Compliance control needs evidence",
				Detail:       control.ControlTitle,
				ResourceType: "control",
				ResourceID:   control.ControlID,
				CreatedAt:    control.UpdatedAt,
			})
		default:
			resp.ComplianceCoverage.PartialControlCount++
		}
		if control.LastEvidenceGeneratedAt == "" {
			resp.ComplianceCoverage.StaleControlCount++
		}
	}

	for _, exception := range exceptions {
		active := exception.Status == "approved"
		if active && exception.ExpiresAt != nil && *exception.ExpiresAt != "" {
			if parsed, err := time.Parse(time.RFC3339, *exception.ExpiresAt); err == nil {
				active = parsed.After(now)
				if active && parsed.Before(now.Add(14*24*time.Hour)) {
					resp.ActionQueue = append(resp.ActionQueue, GovernanceActionItem{
						Kind:         "policy_exception",
						Severity:     "medium",
						Title:        "Policy exception expires soon",
						Detail:       exception.Reason,
						ResourceType: "policy_exception",
						ResourceID:   exception.ID,
						CreatedAt:    exception.CreatedAt,
					})
				}
			}
		}
		if active {
			resp.RiskOverview.ActivePolicyExceptionCount++
		}
	}

	for _, risk := range risks {
		if risk.RiskScore >= 70 {
			resp.RiskOverview.HighRiskProviderCount++
		}
		reason, blocked := providerRiskBlockReason(risk.ContractStatus, risk.SecurityReviewStatus)
		if blocked {
			resp.RiskOverview.ProviderReviewWarningCount++
			resp.ActionQueue = append(resp.ActionQueue, GovernanceActionItem{
				Kind:         "provider_risk",
				Severity:     "high",
				Title:        "Provider risk review blocks routing",
				Detail:       fmt.Sprintf("%s is blocked by %s", risk.ProviderName, reason),
				ResourceType: "provider",
				ResourceID:   risk.ID,
				CreatedAt:    risk.UpdatedAt,
			})
		}
	}

	resp.BehaviorTrends.TopModels = governanceTopModels(topModels)
	resp.BehaviorTrends.TopBackends = governanceTopBackends(topBackends)
	if len(resp.ActionQueue) > 20 {
		resp.ActionQueue = resp.ActionQueue[:20]
	}
	return resp, nil
}

func governanceTopModels(rows []db.ListGatewayTopModelsRow) []GovernanceModelUsage {
	items := make([]GovernanceModelUsage, 0, len(rows))
	for _, row := range rows {
		items = append(items, GovernanceModelUsage{
			Model:       row.Model,
			CallCount:   row.CallCount,
			TotalTokens: row.TotalTokens,
			TotalCost:   managementNumericFloat(row.TotalCost),
		})
	}
	return items
}

func governanceTopBackends(rows []db.ListGatewayTopBackendsRow) []GovernanceBackendUsage {
	items := make([]GovernanceBackendUsage, 0, len(rows))
	for _, row := range rows {
		items = append(items, GovernanceBackendUsage{
			Backend:      row.Backend,
			CallCount:    row.CallCount,
			ErrorCount:   row.ErrorCount,
			AvgLatencyMS: row.AvgLatencyMs,
			TotalTokens:  row.TotalTokens,
			TotalCost:    managementNumericFloat(row.TotalCost),
		})
	}
	return items
}

func governanceReasonCounts(counts map[string]int) []GovernanceReasonCount {
	items := make([]GovernanceReasonCount, 0, len(counts))
	for reason, count := range counts {
		items = append(items, GovernanceReasonCount{ReasonCode: reason, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].ReasonCode < items[j].ReasonCode
		}
		return items[i].Count > items[j].Count
	})
	if len(items) > 10 {
		return items[:10]
	}
	return items
}

func governanceResourceCounts(counts map[string]GovernanceResourceCount) []GovernanceResourceCount {
	items := make([]GovernanceResourceCount, 0, len(counts))
	for _, item := range counts {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].ResourceLabel < items[j].ResourceLabel
		}
		return items[i].Count > items[j].Count
	})
	if len(items) > 10 {
		return items[:10]
	}
	return items
}

func managementNumericFloat(value pgtype.Numeric) *float64 {
	if !value.Valid || value.Int == nil || value.NaN {
		return nil
	}
	rat := new(big.Rat).SetInt(value.Int)
	if value.Exp < 0 {
		rat.Quo(rat, new(big.Rat).SetInt(managementPow10(-value.Exp)))
	} else if value.Exp > 0 {
		rat.Mul(rat, new(big.Rat).SetInt(managementPow10(value.Exp)))
	}
	f, _ := rat.Float64()
	return &f
}

func managementPow10(exp int32) *big.Int {
	out := big.NewInt(1)
	ten := big.NewInt(10)
	for i := int32(0); i < exp; i++ {
		out.Mul(out, ten)
	}
	return out
}

func (s *Service) ApprovePolicyDecision(ctx context.Context, input ApprovePolicyDecisionInput) (PolicyDecisionApprovalResponse, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return PolicyDecisionApprovalResponse{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return PolicyDecisionApprovalResponse{}, err
	}
	decisionUUID, err := uuidValue(input.DecisionID, "decision_id")
	if err != nil {
		return PolicyDecisionApprovalResponse{}, err
	}
	expiresAt, err := optionalRFC3339(input.ExpiresAt, "expires_at")
	if err != nil {
		return PolicyDecisionApprovalResponse{}, err
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "Approved policy decision"
	}

	var decision db.GatewayPolicyDecision
	var exception db.AiPolicyException
	if err := s.withTx(ctx, func(q *db.Queries) error {
		before, err := q.GetGatewayPolicyDecision(ctx, db.GetGatewayPolicyDecisionParams{
			WorkspaceID: workspaceUUID,
			ID:          decisionUUID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayPolicyDecisionNotFound
		}
		if err != nil {
			return err
		}
		if optionalTextString(before.ApprovalStatus) != "requested" {
			return fmt.Errorf("%w: policy decision is not awaiting approval", ErrInvalidGatewayBackend)
		}

		scope, err := policyExceptionScope(before.ResourceType, before.ResourceID, before.ResourceLabel)
		if err != nil {
			return err
		}
		evidenceReferences, err := json.Marshal([]map[string]string{{
			"type": "gateway_policy_decision",
			"id":   uuidString(before.ID),
		}})
		if err != nil {
			return err
		}
		requesterUUID := before.SubjectUserID
		if !requesterUUID.Valid {
			requesterUUID = actorUUID
		}
		exception, err = q.CreateAIPolicyException(ctx, db.CreateAIPolicyExceptionParams{
			WorkspaceID:        workspaceUUID,
			PolicyID:           before.PolicyID,
			RequesterUserID:    requesterUUID,
			ApproverUserID:     actorUUID,
			Reason:             reason,
			Scope:              scope,
			Status:             "approved",
			ExpiresAt:          expiresAt,
			EvidenceReferences: evidenceReferences,
		})
		if err != nil {
			return err
		}

		decision, err = q.UpdateGatewayPolicyDecisionApprovalStatus(ctx, db.UpdateGatewayPolicyDecisionApprovalStatusParams{
			WorkspaceID:    workspaceUUID,
			ID:             decisionUUID,
			ApprovalStatus: pgtype.Text{String: "approved", Valid: true},
		})
		if err != nil {
			return err
		}
		resp := PolicyDecisionApprovalResponse{
			Decision: policyDecisionItem(decision),
		}
		exceptionItem := policyExceptionItem(exception)
		resp.Exception = &exceptionItem
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.governance.policy_decision.approve", "gateway_policy_decision", uuidString(decision.ID), policyDecisionItem(before), resp)
	}); err != nil {
		return PolicyDecisionApprovalResponse{}, err
	}

	exceptionItem := policyExceptionItem(exception)
	return PolicyDecisionApprovalResponse{
		Decision:  policyDecisionItem(decision),
		Exception: &exceptionItem,
	}, nil
}

func (s *Service) DenyPolicyDecision(ctx context.Context, input DenyPolicyDecisionInput) (PolicyDecisionApprovalResponse, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return PolicyDecisionApprovalResponse{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return PolicyDecisionApprovalResponse{}, err
	}
	decisionUUID, err := uuidValue(input.DecisionID, "decision_id")
	if err != nil {
		return PolicyDecisionApprovalResponse{}, err
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "Denied policy decision"
	}

	var decision db.GatewayPolicyDecision
	if err := s.withTx(ctx, func(q *db.Queries) error {
		before, err := q.GetGatewayPolicyDecision(ctx, db.GetGatewayPolicyDecisionParams{
			WorkspaceID: workspaceUUID,
			ID:          decisionUUID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayPolicyDecisionNotFound
		}
		if err != nil {
			return err
		}
		if optionalTextString(before.ApprovalStatus) != "requested" {
			return fmt.Errorf("%w: policy decision is not awaiting approval", ErrInvalidGatewayBackend)
		}

		decision, err = q.UpdateGatewayPolicyDecisionApprovalStatus(ctx, db.UpdateGatewayPolicyDecisionApprovalStatusParams{
			WorkspaceID:    workspaceUUID,
			ID:             decisionUUID,
			ApprovalStatus: pgtype.Text{String: "denied", Valid: true},
		})
		if err != nil {
			return err
		}
		resp := PolicyDecisionApprovalResponse{
			Decision: policyDecisionItem(decision),
		}
		after := map[string]any{
			"decision": resp.Decision,
			"reason":   reason,
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.governance.policy_decision.deny", "gateway_policy_decision", uuidString(decision.ID), policyDecisionItem(before), after)
	}); err != nil {
		return PolicyDecisionApprovalResponse{}, err
	}

	return PolicyDecisionApprovalResponse{
		Decision: policyDecisionItem(decision),
	}, nil
}

func (s *Service) ListEvidence(ctx context.Context, workspaceID string, limit int32) ([]EvidenceItem, error) {
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

	rows, err := s.queries.ListAIEvidence(ctx, db.ListAIEvidenceParams{
		WorkspaceID: workspaceUUID,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]EvidenceItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, evidenceItem(row))
	}
	return items, nil
}

func (s *Service) ListControlMappings(ctx context.Context, workspaceID string) ([]ControlMappingItem, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListAIControlMappingsWithEvidence(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		if err := s.ensureDefaultGatewayControlMappings(ctx, workspaceUUID); err != nil {
			return nil, err
		}
		rows, err = s.queries.ListAIControlMappingsWithEvidence(ctx, workspaceUUID)
		if err != nil {
			return nil, err
		}
	}

	items := make([]ControlMappingItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, controlMappingItem(row))
	}
	return items, nil
}

func (s *Service) ListIncidents(ctx context.Context, workspaceID string, limit int32) ([]IncidentItem, error) {
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

	rows, err := s.queries.ListAIIncidents(ctx, db.ListAIIncidentsParams{
		WorkspaceID: workspaceUUID,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]IncidentItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, incidentItem(row))
	}
	return items, nil
}

func (s *Service) UpdateIncident(ctx context.Context, input UpdateIncidentInput) (IncidentItem, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return IncidentItem{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return IncidentItem{}, err
	}
	incidentUUID, err := uuidValue(input.IncidentID, "incident_id")
	if err != nil {
		return IncidentItem{}, err
	}
	status := strings.ToLower(strings.TrimSpace(input.Status))
	if _, ok := incidentStatuses[status]; !ok {
		return IncidentItem{}, fmt.Errorf("%w: invalid incident status", ErrInvalidGatewayBackend)
	}

	var row db.AiIncident
	if err := s.withTx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.UpdateAIIncident(ctx, db.UpdateAIIncidentParams{
			WorkspaceID:      workspaceUUID,
			ID:               incidentUUID,
			Status:           status,
			RemediationNotes: strings.TrimSpace(input.RemediationNotes),
		})
		if err != nil {
			return err
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.governance.incident.update", "ai_incident", uuidString(row.ID), nil, incidentItem(row))
	}); err != nil {
		return IncidentItem{}, err
	}
	return incidentItem(row), nil
}

func (s *Service) ListPolicyExceptions(ctx context.Context, workspaceID string, limit int32) ([]PolicyExceptionItem, error) {
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

	rows, err := s.queries.ListAIPolicyExceptions(ctx, db.ListAIPolicyExceptionsParams{
		WorkspaceID: workspaceUUID,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]PolicyExceptionItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, policyExceptionItem(row))
	}
	return items, nil
}

func (s *Service) CreatePolicyException(ctx context.Context, input CreatePolicyExceptionInput) (PolicyExceptionItem, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return PolicyExceptionItem{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return PolicyExceptionItem{}, err
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return PolicyExceptionItem{}, fmt.Errorf("%w: reason is required", ErrInvalidGatewayBackend)
	}
	scope, err := policyExceptionScope(input.ResourceType, input.ResourceID, input.ResourceLabel)
	if err != nil {
		return PolicyExceptionItem{}, err
	}
	expiresAt, err := optionalRFC3339(input.ExpiresAt, "expires_at")
	if err != nil {
		return PolicyExceptionItem{}, err
	}

	var row db.AiPolicyException
	if err := s.withTx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.CreateAIPolicyException(ctx, db.CreateAIPolicyExceptionParams{
			WorkspaceID:        workspaceUUID,
			RequesterUserID:    actorUUID,
			Reason:             reason,
			Scope:              scope,
			Status:             "requested",
			ExpiresAt:          expiresAt,
			EvidenceReferences: []byte("[]"),
		})
		if err != nil {
			return err
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.governance.policy_exception.create", "ai_policy_exception", uuidString(row.ID), nil, policyExceptionItem(row))
	}); err != nil {
		return PolicyExceptionItem{}, err
	}
	return policyExceptionItem(row), nil
}

func (s *Service) UpdatePolicyException(ctx context.Context, input UpdatePolicyExceptionInput) (PolicyExceptionItem, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return PolicyExceptionItem{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return PolicyExceptionItem{}, err
	}
	exceptionUUID, err := uuidValue(input.ExceptionID, "exception_id")
	if err != nil {
		return PolicyExceptionItem{}, err
	}
	status := strings.ToLower(strings.TrimSpace(input.Status))
	if _, ok := policyExceptionStatuses[status]; !ok {
		return PolicyExceptionItem{}, fmt.Errorf("%w: invalid policy exception status", ErrInvalidGatewayBackend)
	}
	expiresAt, err := optionalRFC3339(input.ExpiresAt, "expires_at")
	if err != nil {
		return PolicyExceptionItem{}, err
	}

	var row db.AiPolicyException
	if err := s.withTx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.UpdateAIPolicyException(ctx, db.UpdateAIPolicyExceptionParams{
			WorkspaceID:    workspaceUUID,
			ApproverUserID: actorUUID,
			Status:         status,
			ExpiresAt:      expiresAt,
			ID:             exceptionUUID,
		})
		if err != nil {
			return err
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.governance.policy_exception.update", "ai_policy_exception", uuidString(row.ID), nil, policyExceptionItem(row))
	}); err != nil {
		return PolicyExceptionItem{}, err
	}
	return policyExceptionItem(row), nil
}

func (s *Service) ListGovernancePolicies(ctx context.Context, workspaceID string) ([]GovernancePolicyItem, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListGatewayPolicies(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	items := make([]GovernancePolicyItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, governancePolicyItem(row))
	}
	return items, nil
}

func (s *Service) CreateGovernancePolicy(ctx context.Context, input CreateGovernancePolicyInput) (GovernancePolicyItem, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return GovernancePolicyItem{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return GovernancePolicyItem{}, err
	}
	normalized, rules, err := normalizeCreateGovernancePolicyInput(input)
	if err != nil {
		return GovernancePolicyItem{}, err
	}

	var row db.GatewayPolicy
	if err := s.withTx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.CreateGatewayPolicy(ctx, db.CreateGatewayPolicyParams{
			WorkspaceID:     workspaceUUID,
			Name:            normalized.Name,
			Description:     normalized.Description,
			PolicyType:      normalized.PolicyType,
			Enabled:         normalized.Enabled,
			Version:         1,
			RuleDefinition:  rules,
			EnforcementMode: normalized.EnforcementMode,
			CreatedBy:       actorUUID,
		})
		if err != nil {
			return err
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.governance.policy.create", "gateway_policy", uuidString(row.ID), nil, governancePolicyItem(row))
	}); err != nil {
		return GovernancePolicyItem{}, err
	}
	return governancePolicyItem(row), nil
}

func (s *Service) UpdateGovernancePolicy(ctx context.Context, input UpdateGovernancePolicyInput) (GovernancePolicyItem, error) {
	workspaceUUID, err := uuidValue(input.WorkspaceID, "workspace_id")
	if err != nil {
		return GovernancePolicyItem{}, err
	}
	actorUUID, err := uuidValue(input.ActorUserID, "actor_user_id")
	if err != nil {
		return GovernancePolicyItem{}, err
	}
	policyUUID, err := uuidValue(input.PolicyID, "policy_id")
	if err != nil {
		return GovernancePolicyItem{}, err
	}
	normalized, rules, err := normalizeUpdateGovernancePolicyInput(input)
	if err != nil {
		return GovernancePolicyItem{}, err
	}

	var row db.GatewayPolicy
	if err := s.withTx(ctx, func(q *db.Queries) error {
		current, err := q.GetGatewayPolicy(ctx, db.GetGatewayPolicyParams{
			WorkspaceID: workspaceUUID,
			ID:          policyUUID,
		})
		if err != nil {
			return err
		}
		row, err = q.UpdateGatewayPolicy(ctx, db.UpdateGatewayPolicyParams{
			WorkspaceID:     workspaceUUID,
			ID:              policyUUID,
			Name:            normalized.Name,
			Description:     normalized.Description,
			PolicyType:      normalized.PolicyType,
			Enabled:         normalized.Enabled,
			RuleDefinition:  rules,
			EnforcementMode: normalized.EnforcementMode,
			UpdatedBy:       actorUUID,
		})
		if err != nil {
			return err
		}
		return audit(ctx, q, workspaceUUID, actorUUID, "gateway.governance.policy.update", "gateway_policy", uuidString(row.ID), governancePolicyItem(current), governancePolicyItem(row))
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GovernancePolicyItem{}, ErrGatewayBackendNotFound
		}
		return GovernancePolicyItem{}, err
	}
	return governancePolicyItem(row), nil
}

func (s *Service) ensureDefaultGatewayControlMappings(ctx context.Context, workspaceID pgtype.UUID) error {
	for _, control := range defaultGatewayControlMappings {
		mappedEvidenceQueries, err := json.Marshal(control.MappedEvidenceQueries)
		if err != nil {
			return err
		}
		if _, err := s.queries.UpsertAIControlMapping(ctx, db.UpsertAIControlMappingParams{
			WorkspaceID:           workspaceID,
			Framework:             internalGatewayGovernanceFramework,
			ControlID:             control.ControlID,
			ControlTitle:          control.ControlTitle,
			MappedPolicyIds:       []byte("[]"),
			MappedEvidenceQueries: mappedEvidenceQueries,
			Status:                control.Status,
			OwnerUserID:           pgtype.UUID{},
		}); err != nil {
			return err
		}
	}
	return nil
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

func policyDecisionItem(row db.GatewayPolicyDecision) PolicyDecisionItem {
	return PolicyDecisionItem{
		ID:                 uuidString(row.ID),
		PolicyID:           optionalUUIDString(row.PolicyID),
		PolicyVersion:      optionalInt4(row.PolicyVersion),
		SubjectUserID:      optionalUUIDString(row.SubjectUserID),
		SubjectAgentID:     optionalUUIDString(row.SubjectAgentID),
		ResourceType:       row.ResourceType,
		ResourceID:         row.ResourceID,
		ResourceLabel:      row.ResourceLabel,
		Decision:           row.Decision,
		ReasonCode:         row.ReasonCode,
		MatchedRules:       auditJSON(row.MatchedRules),
		RequestID:          optionalUUIDString(row.RequestID),
		SessionID:          optionalUUIDString(row.SessionID),
		SpanRowID:          optionalUUIDString(row.SpanRowID),
		ApprovalStatus:     optionalTextString(row.ApprovalStatus),
		EvidenceReferences: auditJSON(row.EvidenceReferences),
		CreatedAt:          textTimestamp(row.CreatedAt),
	}
}

func evidenceItem(row db.AiEvidence) EvidenceItem {
	return EvidenceItem{
		ID:                   uuidString(row.ID),
		EvidenceType:         row.EvidenceType,
		FrameworkRefs:        auditJSON(row.FrameworkRefs),
		LinkedRequestID:      optionalUUIDString(row.LinkedRequestID),
		LinkedSessionID:      optionalUUIDString(row.LinkedSessionID),
		LinkedSpanRowID:      optionalUUIDString(row.LinkedSpanRowID),
		LinkedPolicyID:       optionalUUIDString(row.LinkedPolicyID),
		LinkedBackendID:      optionalUUIDString(row.LinkedBackendID),
		LinkedProviderRiskID: optionalUUIDString(row.LinkedProviderRiskID),
		Summary:              row.Summary,
		Payload:              auditJSON(row.Payload),
		AttachmentRef:        row.AttachmentRef,
		GeneratedAt:          textTimestamp(row.GeneratedAt),
		RetainUntil:          optionalTimestamp(row.RetainUntil),
	}
}

func controlMappingItem(row db.ListAIControlMappingsWithEvidenceRow) ControlMappingItem {
	return ControlMappingItem{
		ID:                      uuidString(row.ID),
		Framework:               row.Framework,
		ControlID:               row.ControlID,
		ControlTitle:            row.ControlTitle,
		MappedPolicyIDs:         auditJSON(row.MappedPolicyIds),
		MappedEvidenceQueries:   auditJSON(row.MappedEvidenceQueries),
		Status:                  row.Status,
		OwnerUserID:             optionalUUIDString(row.OwnerUserID),
		EvidenceCount:           row.EvidenceCount,
		LastEvidenceGeneratedAt: controlEvidenceTimestamp(row.LastEvidenceGeneratedAt),
		UpdatedAt:               textTimestamp(row.UpdatedAt),
	}
}

func incidentItem(row db.AiIncident) IncidentItem {
	return IncidentItem{
		ID:                   uuidString(row.ID),
		Severity:             row.Severity,
		Category:             row.Category,
		LinkedRequestID:      optionalUUIDString(row.LinkedRequestID),
		LinkedSessionID:      optionalUUIDString(row.LinkedSessionID),
		LinkedSpanRowID:      optionalUUIDString(row.LinkedSpanRowID),
		LinkedPolicyID:       optionalUUIDString(row.LinkedPolicyID),
		LinkedProviderRiskID: optionalUUIDString(row.LinkedProviderRiskID),
		Summary:              row.Summary,
		Status:               row.Status,
		RemediationNotes:     row.RemediationNotes,
		OpenedAt:             textTimestamp(row.OpenedAt),
		ClosedAt:             optionalTimestamp(row.ClosedAt),
	}
}

func policyExceptionItem(row db.AiPolicyException) PolicyExceptionItem {
	return PolicyExceptionItem{
		ID:                 uuidString(row.ID),
		PolicyID:           optionalUUIDString(row.PolicyID),
		RequesterUserID:    optionalUUIDString(row.RequesterUserID),
		ApproverUserID:     optionalUUIDString(row.ApproverUserID),
		Reason:             row.Reason,
		Scope:              auditJSON(row.Scope),
		Status:             row.Status,
		ExpiresAt:          optionalTimestamp(row.ExpiresAt),
		EvidenceReferences: auditJSON(row.EvidenceReferences),
		CreatedAt:          textTimestamp(row.CreatedAt),
		UpdatedAt:          textTimestamp(row.UpdatedAt),
	}
}

func governancePolicyItem(row db.GatewayPolicy) GovernancePolicyItem {
	return GovernancePolicyItem{
		ID:              uuidString(row.ID),
		Name:            row.Name,
		Description:     row.Description,
		PolicyType:      row.PolicyType,
		Enabled:         row.Enabled,
		Version:         row.Version,
		RuleDefinition:  auditJSON(row.RuleDefinition),
		EnforcementMode: row.EnforcementMode,
		CreatedBy:       optionalUUIDString(row.CreatedBy),
		UpdatedBy:       optionalUUIDString(row.UpdatedBy),
		CreatedAt:       textTimestamp(row.CreatedAt),
		UpdatedAt:       textTimestamp(row.UpdatedAt),
	}
}

func evidenceExportItem(row db.ListAIEvidenceExportsRow) EvidenceExportItem {
	return EvidenceExportItem{
		ID:           uuidString(row.ID),
		WorkspaceID:  uuidString(row.WorkspaceID),
		ActorUserID:  optionalUUIDString(row.ActorUserID),
		ActorName:    row.ActorName,
		ActorEmail:   row.ActorEmail,
		ExportType:   row.ExportType,
		SubjectType:  row.SubjectType,
		SubjectID:    row.SubjectID,
		DigestSHA256: row.DigestSha256,
		Sections:     jsonStringSlice(row.Sections),
		CreatedAt:    textTimestamp(row.CreatedAt),
	}
}

func evidenceExportDetailFromRow(row db.GetAIEvidenceExportRow) EvidenceExportDetail {
	return EvidenceExportDetail{
		EvidenceExportItem: EvidenceExportItem{
			ID:           uuidString(row.ID),
			WorkspaceID:  uuidString(row.WorkspaceID),
			ActorUserID:  optionalUUIDString(row.ActorUserID),
			ActorName:    row.ActorName,
			ActorEmail:   row.ActorEmail,
			ExportType:   row.ExportType,
			SubjectType:  row.SubjectType,
			SubjectID:    row.SubjectID,
			DigestSHA256: row.DigestSha256,
			Sections:     jsonStringSlice(row.Sections),
			CreatedAt:    textTimestamp(row.CreatedAt),
		},
		BundleSnapshot: auditJSON(row.BundleSnapshot),
	}
}

func normalizeCreateGovernancePolicyInput(input CreateGovernancePolicyInput) (CreateGovernancePolicyInput, []byte, error) {
	normalized := input
	normalized.Name = strings.TrimSpace(normalized.Name)
	normalized.Description = strings.TrimSpace(normalized.Description)
	normalized.PolicyType = strings.ToLower(strings.TrimSpace(normalized.PolicyType))
	normalized.EnforcementMode = strings.ToLower(strings.TrimSpace(normalized.EnforcementMode))
	if normalized.EnforcementMode == "" {
		normalized.EnforcementMode = "enforce"
	}
	rules, err := validateGatewayPolicy(normalized.Name, normalized.PolicyType, normalized.EnforcementMode, normalized.RuleDefinition)
	return normalized, rules, err
}

func normalizeUpdateGovernancePolicyInput(input UpdateGovernancePolicyInput) (UpdateGovernancePolicyInput, []byte, error) {
	normalized := input
	normalized.Name = strings.TrimSpace(normalized.Name)
	normalized.Description = strings.TrimSpace(normalized.Description)
	normalized.PolicyType = strings.ToLower(strings.TrimSpace(normalized.PolicyType))
	normalized.EnforcementMode = strings.ToLower(strings.TrimSpace(normalized.EnforcementMode))
	if normalized.EnforcementMode == "" {
		normalized.EnforcementMode = "enforce"
	}
	rules, err := validateGatewayPolicy(normalized.Name, normalized.PolicyType, normalized.EnforcementMode, normalized.RuleDefinition)
	return normalized, rules, err
}

func validateGatewayPolicy(name, policyType, enforcementMode string, ruleDefinition any) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidGatewayBackend)
	}
	if _, ok := gatewayPolicyTypes[policyType]; !ok {
		return nil, fmt.Errorf("%w: invalid policy_type", ErrInvalidGatewayBackend)
	}
	if _, ok := gatewayPolicyEnforcementModes[enforcementMode]; !ok {
		return nil, fmt.Errorf("%w: invalid enforcement_mode", ErrInvalidGatewayBackend)
	}
	raw, err := json.Marshal(ruleDefinition)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid rule_definition", ErrInvalidGatewayBackend)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("%w: rule_definition is required", ErrInvalidGatewayBackend)
	}
	rules, err := gatewaypolicy.DecodeRules(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid rule_definition", ErrInvalidGatewayBackend)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("%w: at least one rule is required", ErrInvalidGatewayBackend)
	}
	for _, rule := range rules {
		if strings.TrimSpace(rule.ID) == "" {
			return nil, fmt.Errorf("%w: rule id is required", ErrInvalidGatewayBackend)
		}
		if _, ok := gatewayPolicyActions[rule.Action]; !ok {
			return nil, fmt.Errorf("%w: invalid policy action", ErrInvalidGatewayBackend)
		}
		if strings.TrimSpace(rule.ReasonCode) == "" {
			return nil, fmt.Errorf("%w: reason_code is required", ErrInvalidGatewayBackend)
		}
		if rule.Action == gatewaypolicy.ActionRouteToBackend && strings.TrimSpace(rule.RouteBackendSlug) == "" {
			return nil, fmt.Errorf("%w: route_backend_slug is required", ErrInvalidGatewayBackend)
		}
	}
	return raw, nil
}

func policyExceptionScope(resourceType, resourceID, resourceLabel string) ([]byte, error) {
	scope := map[string]string{
		"resource_type": strings.TrimSpace(resourceType),
	}
	if scope["resource_type"] == "" {
		return nil, fmt.Errorf("%w: resource_type is required", ErrInvalidGatewayBackend)
	}
	if resourceID = strings.TrimSpace(resourceID); resourceID != "" {
		scope["resource_id"] = resourceID
	}
	if resourceLabel = strings.TrimSpace(resourceLabel); resourceLabel != "" {
		scope["resource_label"] = resourceLabel
	}
	if scope["resource_id"] == "" && scope["resource_label"] == "" {
		return nil, fmt.Errorf("%w: resource_id or resource_label is required", ErrInvalidGatewayBackend)
	}
	return json.Marshal(scope)
}

func controlEvidenceTimestamp(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case pgtype.Timestamptz:
		return textTimestamp(typed)
	default:
		return ""
	}
}

func normalizeCreateBackendInput(input CreateBackendInput) (CreateBackendInput, string, error) {
	normalized := input
	normalized.Provider = strings.ToLower(strings.TrimSpace(normalized.Provider))
	normalized.Slug = strings.TrimSpace(normalized.Slug)
	normalized.DisplayName = strings.TrimSpace(normalized.DisplayName)
	normalized.BackendType = strings.TrimSpace(normalized.BackendType)
	normalized.BaseURL = strings.TrimSpace(normalized.BaseURL)
	normalized.Transport = strings.TrimSpace(normalized.Transport)
	normalized.CredentialType = strings.TrimSpace(normalized.CredentialType)
	normalized.SubscriptionProvider = strings.TrimSpace(normalized.SubscriptionProvider)
	normalized.DispatchScope = strings.TrimSpace(normalized.DispatchScope)
	normalized.PayloadFormat = strings.TrimSpace(normalized.PayloadFormat)
	normalized.ValidationStatus = strings.TrimSpace(normalized.ValidationStatus)

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
	if normalized.BackendType == BackendTypeSubscriptionRuntime {
		normalized.Transport = TransportDaemonDispatch
		normalized.CredentialType = CredentialTypeSubscriptionBundle
		normalized.DispatchScope = dispatchScopeOrDefault(normalized.DispatchScope)
		if normalized.ValidationStatus == "" {
			normalized.ValidationStatus = "pending_runtime_validation"
		}
		if normalized.SubscriptionProvider == "" {
			switch normalized.Provider {
			case "claude-code-subscription":
				normalized.SubscriptionProvider = SubscriptionProviderClaudeCode
			case "codex-subscription":
				normalized.SubscriptionProvider = SubscriptionProviderCodex
			}
		}
		if normalized.SubscriptionProvider == "" {
			return CreateBackendInput{}, "", fmt.Errorf("%w: subscription_provider is required", ErrInvalidGatewayBackend)
		}
	} else {
		normalized.Transport = transportOrDefault(normalized.Transport)
		normalized.CredentialType = CredentialTypeAPIKey
		normalized.DispatchScope = dispatchScopeOrDefault(normalized.DispatchScope)
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
		ID:                   uuidString(row.ID),
		Slug:                 row.Slug,
		DisplayName:          row.DisplayName,
		BackendType:          row.BackendType,
		BaseURL:              row.BaseUrl,
		CredentialHint:       row.CredentialHint,
		Transport:            row.Transport,
		Enabled:              row.Enabled,
		IsDefault:            defaultID.Valid && row.ID == defaultID,
		Metadata:             metadata,
		SubscriptionProvider: row.SubscriptionProvider,
		DispatchScope:        row.DispatchScope,
		ValidationStatus:     row.ValidationStatus,
		ValidatedRuntimeID:   optionalUUIDString(row.ValidatedRuntimeID),
		LastValidationAt:     optionalTimestamp(row.LastValidationAt),
		LastValidationError:  row.LastValidationError,
		CreatedAt:            textTimestamp(row.CreatedAt),
		UpdatedAt:            textTimestamp(row.UpdatedAt),
	}
}

func backendCredentialResponse(row db.GatewayBackendCredential) BackendCredentialResponse {
	return BackendCredentialResponse{
		ID:                   uuidString(row.ID),
		BackendID:            uuidString(row.BackendID),
		Label:                row.Label,
		CredentialHint:       row.CredentialHint,
		CredentialType:       row.CredentialType,
		SubscriptionProvider: row.SubscriptionProvider,
		DispatchScope:        row.DispatchScope,
		ValidationStatus:     row.ValidationStatus,
		ValidatedRuntimeID:   optionalUUIDString(row.ValidatedRuntimeID),
		LastValidationAt:     optionalTimestamp(row.LastValidationAt),
		LastValidationError:  row.LastValidationError,
		AccountHint:          row.AccountHint,
		Enabled:              row.Enabled,
		Priority:             row.Priority,
		LastUsedAt:           optionalTimestamp(row.LastUsedAt),
		LastErrorAt:          optionalTimestamp(row.LastErrorAt),
		LastError:            row.LastError,
		RateLimitedUntil:     optionalTimestamp(row.RateLimitedUntil),
		RateLimitRemaining:   optionalInt4(row.RateLimitRemaining),
		RateLimitResetAt:     optionalTimestamp(row.RateLimitResetAt),
		CreatedAt:            textTimestamp(row.CreatedAt),
		UpdatedAt:            textTimestamp(row.UpdatedAt),
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

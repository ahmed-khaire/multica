package management

import "strings"

const (
	BackendTypeOpenAICompatible = "openai_compatible"
	BackendTypeAnthropic        = "anthropic"
	BackendTypeClaudeOAuth      = "claude_oauth"

	CaptureMetadataOnly    = "metadata_only"
	CaptureRedactedContent = "redacted_content"
	CaptureFullContent     = "full_content"
	DefaultCapturePolicy   = CaptureFullContent
)

type ProviderPreset struct {
	Provider           string
	Slug               string
	DisplayName        string
	BackendType        string
	BaseURL            string
	RequiresCredential bool
}

type GatewayURLs struct {
	OpenAIBaseURL    string `json:"openai_base_url"`
	AnthropicBaseURL string `json:"anthropic_base_url"`
}

type BackendResponse struct {
	ID             string         `json:"id"`
	Slug           string         `json:"slug"`
	DisplayName    string         `json:"display_name"`
	BackendType    string         `json:"backend_type"`
	BaseURL        string         `json:"base_url"`
	CredentialHint string         `json:"credential_hint"`
	Enabled        bool           `json:"enabled"`
	IsDefault      bool           `json:"is_default"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

type SettingsResponse struct {
	CapturePolicy  string           `json:"capture_policy"`
	DefaultBackend *BackendResponse `json:"default_backend"`
}

type StatusResponse struct {
	OpenAIBaseURL       string           `json:"openai_base_url"`
	AnthropicBaseURL    string           `json:"anthropic_base_url"`
	CapturePolicy       string           `json:"capture_policy"`
	DefaultBackend      *BackendResponse `json:"default_backend"`
	BackendCount        int              `json:"backend_count"`
	EnabledBackendCount int              `json:"enabled_backend_count"`
	HasActiveKey        bool             `json:"has_active_key"`
}

type DoctorResponse struct {
	Status      string        `json:"status"`
	Checks      []DoctorCheck `json:"checks"`
	GeneratedAt string        `json:"generated_at"`
}

type DoctorCheck struct {
	ID          string         `json:"id"`
	Category    string         `json:"category"`
	Status      string         `json:"status"`
	Title       string         `json:"title"`
	Detail      string         `json:"detail"`
	Remediation string         `json:"remediation"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type UserKeyResponse struct {
	ID               string  `json:"id"`
	Key              string  `json:"key"`
	KeyPrefix        string  `json:"key_prefix"`
	OpenAIBaseURL    string  `json:"openai_base_url"`
	OpenAIAPIKey     string  `json:"openai_api_key"`
	AnthropicBaseURL string  `json:"anthropic_base_url"`
	AnthropicAPIKey  string  `json:"anthropic_api_key"`
	CreatedAt        string  `json:"created_at"`
	LastUsedAt       *string `json:"last_used_at"`
}

type UserKeyListItem struct {
	ID         string  `json:"id"`
	KeyPrefix  string  `json:"key_prefix"`
	RevokedAt  *string `json:"revoked_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

type IngestKeyResponse struct {
	ID             string  `json:"id"`
	Key            string  `json:"key"`
	KeyPrefix      string  `json:"key_prefix"`
	AppID          string  `json:"app_id"`
	DisplayName    string  `json:"display_name"`
	GatewayBaseURL string  `json:"gateway_base_url"`
	CreatedAt      string  `json:"created_at"`
	LastUsedAt     *string `json:"last_used_at"`
	RevokedAt      *string `json:"revoked_at"`
}

type IngestKeyListItem struct {
	ID          string  `json:"id"`
	KeyPrefix   string  `json:"key_prefix"`
	AppID       string  `json:"app_id"`
	DisplayName string  `json:"display_name"`
	RevokedAt   *string `json:"revoked_at"`
	LastUsedAt  *string `json:"last_used_at"`
	CreatedAt   string  `json:"created_at"`
}

type AuditLogItem struct {
	ID          string `json:"id"`
	ActorUserID string `json:"actor_user_id"`
	ActorName   string `json:"actor_name"`
	ActorEmail  string `json:"actor_email"`
	Action      string `json:"action"`
	TargetType  string `json:"target_type"`
	TargetID    string `json:"target_id"`
	BeforeState any    `json:"before_state"`
	AfterState  any    `json:"after_state"`
	RequestID   string `json:"request_id"`
	CreatedAt   string `json:"created_at"`
}

type ProviderRiskResponse struct {
	ID                   string   `json:"id"`
	BackendID            string   `json:"backend_id"`
	ProviderName         string   `json:"provider_name"`
	OwnerUserID          string   `json:"owner_user_id"`
	ApprovedUseCases     []string `json:"approved_use_cases"`
	DataCategories       []string `json:"data_categories"`
	Regions              []string `json:"regions"`
	HostingNotes         string   `json:"hosting_notes"`
	ContractStatus       string   `json:"contract_status"`
	SecurityReviewStatus string   `json:"security_review_status"`
	EvidenceLinks        []string `json:"evidence_links"`
	Limitations          string   `json:"limitations"`
	ProhibitedUses       string   `json:"prohibited_uses"`
	ModelList            []string `json:"model_list"`
	CapabilityClass      string   `json:"capability_class"`
	RiskScore            int32    `json:"risk_score"`
	ReviewCadenceDays    int32    `json:"review_cadence_days"`
	LastAssessmentAt     *string  `json:"last_assessment_at"`
	NextReviewAt         *string  `json:"next_review_at"`
	ActiveExceptionCount int32    `json:"active_exception_count"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
}

type PolicyDecisionItem struct {
	ID                 string `json:"id"`
	PolicyID           string `json:"policy_id"`
	PolicyVersion      *int32 `json:"policy_version"`
	SubjectUserID      string `json:"subject_user_id"`
	SubjectAgentID     string `json:"subject_agent_id"`
	ResourceType       string `json:"resource_type"`
	ResourceID         string `json:"resource_id"`
	ResourceLabel      string `json:"resource_label"`
	Decision           string `json:"decision"`
	ReasonCode         string `json:"reason_code"`
	MatchedRules       any    `json:"matched_rules"`
	RequestID          string `json:"request_id"`
	SessionID          string `json:"session_id"`
	SpanRowID          string `json:"span_row_id"`
	ApprovalStatus     string `json:"approval_status"`
	EvidenceReferences any    `json:"evidence_references"`
	CreatedAt          string `json:"created_at"`
}

type PolicyDecisionApprovalResponse struct {
	Decision  PolicyDecisionItem   `json:"decision"`
	Exception *PolicyExceptionItem `json:"exception,omitempty"`
}

type EvidenceItem struct {
	ID                   string  `json:"id"`
	EvidenceType         string  `json:"evidence_type"`
	FrameworkRefs        any     `json:"framework_refs"`
	LinkedRequestID      string  `json:"linked_request_id"`
	LinkedSessionID      string  `json:"linked_session_id"`
	LinkedSpanRowID      string  `json:"linked_span_row_id"`
	LinkedPolicyID       string  `json:"linked_policy_id"`
	LinkedBackendID      string  `json:"linked_backend_id"`
	LinkedProviderRiskID string  `json:"linked_provider_risk_id"`
	Summary              string  `json:"summary"`
	Payload              any     `json:"payload"`
	AttachmentRef        string  `json:"attachment_ref"`
	GeneratedAt          string  `json:"generated_at"`
	RetainUntil          *string `json:"retain_until"`
}

type ControlMappingItem struct {
	ID                      string `json:"id"`
	Framework               string `json:"framework"`
	ControlID               string `json:"control_id"`
	ControlTitle            string `json:"control_title"`
	MappedPolicyIDs         any    `json:"mapped_policy_ids"`
	MappedEvidenceQueries   any    `json:"mapped_evidence_queries"`
	Status                  string `json:"status"`
	OwnerUserID             string `json:"owner_user_id"`
	EvidenceCount           int64  `json:"evidence_count"`
	LastEvidenceGeneratedAt string `json:"last_evidence_generated_at"`
	UpdatedAt               string `json:"updated_at"`
}

type IncidentItem struct {
	ID                   string  `json:"id"`
	Severity             string  `json:"severity"`
	Category             string  `json:"category"`
	LinkedRequestID      string  `json:"linked_request_id"`
	LinkedSessionID      string  `json:"linked_session_id"`
	LinkedSpanRowID      string  `json:"linked_span_row_id"`
	LinkedPolicyID       string  `json:"linked_policy_id"`
	LinkedProviderRiskID string  `json:"linked_provider_risk_id"`
	Summary              string  `json:"summary"`
	Status               string  `json:"status"`
	RemediationNotes     string  `json:"remediation_notes"`
	OpenedAt             string  `json:"opened_at"`
	ClosedAt             *string `json:"closed_at"`
}

type PolicyExceptionItem struct {
	ID                 string  `json:"id"`
	PolicyID           string  `json:"policy_id"`
	RequesterUserID    string  `json:"requester_user_id"`
	ApproverUserID     string  `json:"approver_user_id"`
	Reason             string  `json:"reason"`
	Scope              any     `json:"scope"`
	Status             string  `json:"status"`
	ExpiresAt          *string `json:"expires_at"`
	EvidenceReferences any     `json:"evidence_references"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

type GovernancePolicyItem struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	PolicyType      string `json:"policy_type"`
	Enabled         bool   `json:"enabled"`
	Version         int32  `json:"version"`
	RuleDefinition  any    `json:"rule_definition"`
	EnforcementMode string `json:"enforcement_mode"`
	CreatedBy       string `json:"created_by"`
	UpdatedBy       string `json:"updated_by"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type CreateBackendInput struct {
	WorkspaceID string
	ActorUserID string
	Provider    string
	Slug        string
	DisplayName string
	BackendType string
	BaseURL     string
	Key         string
	Enabled     bool
	SetDefault  bool
	Metadata    map[string]any
}

type CreateIngestKeyInput struct {
	WorkspaceID string
	ActorUserID string
	AppID       string
	DisplayName string
}

type UpdateBackendInput struct {
	WorkspaceID string
	ActorUserID string
	BackendID   string
	DisplayName *string
	BaseURL     *string
	Key         *string
	Enabled     *bool
	Metadata    map[string]any
}

type CapturePolicyInput struct {
	WorkspaceID   string
	ActorUserID   string
	CapturePolicy string
}

type SetDefaultBackendInput struct {
	WorkspaceID string
	ActorUserID string
	Slug        string
}

type UpsertProviderRiskInput struct {
	WorkspaceID          string
	ActorUserID          string
	ProviderName         string
	BackendID            string
	ApprovedUseCases     []string
	DataCategories       []string
	Regions              []string
	HostingNotes         string
	ContractStatus       string
	SecurityReviewStatus string
	EvidenceLinks        []string
	Limitations          string
	ProhibitedUses       string
	ModelList            []string
	CapabilityClass      string
	RiskScore            int32
	ReviewCadenceDays    int32
	LastAssessmentAt     string
	NextReviewAt         string
	ActiveExceptionCount int32
}

type CreatePolicyExceptionInput struct {
	WorkspaceID   string
	ActorUserID   string
	Reason        string
	ResourceType  string
	ResourceID    string
	ResourceLabel string
	ExpiresAt     string
}

type UpdatePolicyExceptionInput struct {
	WorkspaceID string
	ActorUserID string
	ExceptionID string
	Status      string
	ExpiresAt   string
}

type ApprovePolicyDecisionInput struct {
	WorkspaceID string
	ActorUserID string
	DecisionID  string
	Reason      string
	ExpiresAt   string
}

type DenyPolicyDecisionInput struct {
	WorkspaceID string
	ActorUserID string
	DecisionID  string
	Reason      string
}

type UpdateIncidentInput struct {
	WorkspaceID      string
	ActorUserID      string
	IncidentID       string
	Status           string
	RemediationNotes string
}

type CreateGovernancePolicyInput struct {
	WorkspaceID     string
	ActorUserID     string
	Name            string
	Description     string
	PolicyType      string
	Enabled         bool
	RuleDefinition  any
	EnforcementMode string
}

type UpdateGovernancePolicyInput struct {
	WorkspaceID     string
	ActorUserID     string
	PolicyID        string
	Name            string
	Description     string
	PolicyType      string
	Enabled         bool
	RuleDefinition  any
	EnforcementMode string
}

var providerPresets = map[string]ProviderPreset{
	"openai": {
		Provider:           "openai",
		Slug:               "openai",
		DisplayName:        "OpenAI",
		BackendType:        BackendTypeOpenAICompatible,
		BaseURL:            "https://api.openai.com/v1",
		RequiresCredential: true,
	},
	"groq": {
		Provider:           "groq",
		Slug:               "groq",
		DisplayName:        "Groq",
		BackendType:        BackendTypeOpenAICompatible,
		BaseURL:            "https://api.groq.com/openai/v1",
		RequiresCredential: true,
	},
	"openrouter": {
		Provider:           "openrouter",
		Slug:               "openrouter",
		DisplayName:        "OpenRouter",
		BackendType:        BackendTypeOpenAICompatible,
		BaseURL:            "https://openrouter.ai/api/v1",
		RequiresCredential: true,
	},
	"local": {
		Provider:           "local",
		Slug:               "local",
		DisplayName:        "Local OpenAI-compatible",
		BackendType:        BackendTypeOpenAICompatible,
		BaseURL:            "http://127.0.0.1:11434/v1",
		RequiresCredential: true,
	},
	"anthropic": {
		Provider:           "anthropic",
		Slug:               "anthropic",
		DisplayName:        "Anthropic",
		BackendType:        BackendTypeAnthropic,
		BaseURL:            "https://api.anthropic.com",
		RequiresCredential: true,
	},
	"claude-oauth": {
		Provider:           "claude-oauth",
		Slug:               "claude-oauth",
		DisplayName:        "Claude OAuth",
		BackendType:        BackendTypeClaudeOAuth,
		BaseURL:            "claude-oauth://sidecar",
		RequiresCredential: false,
	},
}

func ProviderPresetFor(provider string) (ProviderPreset, bool) {
	preset, ok := providerPresets[strings.ToLower(strings.TrimSpace(provider))]
	return preset, ok
}

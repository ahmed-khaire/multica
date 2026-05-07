package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/gateway/management"
)

type gatewayCreateBackendRequest struct {
	Provider    string         `json:"provider"`
	Slug        string         `json:"slug"`
	DisplayName string         `json:"display_name"`
	BackendType string         `json:"backend_type"`
	BaseURL     string         `json:"base_url"`
	Key         string         `json:"key"`
	Enabled     *bool          `json:"enabled"`
	SetDefault  bool           `json:"set_default"`
	Metadata    map[string]any `json:"metadata"`
}

type gatewayUpdateBackendRequest struct {
	DisplayName *string        `json:"display_name"`
	BaseURL     *string        `json:"base_url"`
	Key         *string        `json:"key"`
	Enabled     *bool          `json:"enabled"`
	Metadata    map[string]any `json:"metadata"`
}

type gatewayCreateBackendCredentialRequest struct {
	Label    string `json:"label"`
	Key      string `json:"key"`
	Enabled  *bool  `json:"enabled"`
	Priority int32  `json:"priority"`
}

type gatewayUpdateBackendCredentialRequest struct {
	Label    *string `json:"label"`
	Key      *string `json:"key"`
	Enabled  *bool   `json:"enabled"`
	Priority *int32  `json:"priority"`
}

type gatewaySetDefaultRequest struct {
	BackendSlug string `json:"backend_slug"`
}

type gatewayPolicyRequest struct {
	CapturePolicy string `json:"capture_policy"`
}

type gatewayCreateIngestKeyRequest struct {
	AppID       string `json:"app_id"`
	DisplayName string `json:"display_name"`
}

type gatewayProviderRiskRequest struct {
	ProviderName         string   `json:"provider_name"`
	BackendID            string   `json:"backend_id"`
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
	LastAssessmentAt     string   `json:"last_assessment_at"`
	NextReviewAt         string   `json:"next_review_at"`
	ActiveExceptionCount int32    `json:"active_exception_count"`
}

type gatewayUpdateIncidentRequest struct {
	Status           string `json:"status"`
	RemediationNotes string `json:"remediation_notes"`
}

type gatewayCreatePolicyExceptionRequest struct {
	Reason        string `json:"reason"`
	ResourceType  string `json:"resource_type"`
	ResourceID    string `json:"resource_id"`
	ResourceLabel string `json:"resource_label"`
	ExpiresAt     string `json:"expires_at"`
}

type gatewayUpdatePolicyExceptionRequest struct {
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
}

type gatewayPolicyDecisionApprovalRequest struct {
	Reason    string `json:"reason"`
	ExpiresAt string `json:"expires_at"`
}

type gatewayGovernancePolicyRequest struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	PolicyType      string `json:"policy_type"`
	Enabled         bool   `json:"enabled"`
	RuleDefinition  any    `json:"rule_definition"`
	EnforcementMode string `json:"enforcement_mode"`
}

func (h *Handler) GatewayStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.Status(r.Context(), workspaceID, userID, gatewayServerBaseURL(r))
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) GatewayDoctor(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.Doctor(r.Context(), workspaceID, userID, gatewayServerBaseURL(r))
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) GatewayHealthReport(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.HealthReport(r.Context(), workspaceID, userID, gatewayServerBaseURL(r))
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) GetGatewaySettings(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.Settings(r.Context(), workspaceID)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayBackends(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListBackends(r.Context(), workspaceID)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayBackendCredentials(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListBackendCredentials(r.Context(), workspaceID, chi.URLParam(r, "id"))
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) GetGatewayUserKey(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.GetActiveUserKey(r.Context(), workspaceID, userID, gatewayServerBaseURL(r))
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) CreateGatewayUserKey(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.GetOrCreateUserKey(r.Context(), workspaceID, userID, gatewayServerBaseURL(r))
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayUserKeys(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListUserKeys(r.Context(), workspaceID, userID)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) RevokeGatewayUserKey(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.RevokeUserKey(r.Context(), workspaceID, userID, chi.URLParam(r, "id"))
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) CreateGatewayIngestKey(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayCreateIngestKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.CreateIngestKey(r.Context(), management.CreateIngestKeyInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		AppID:       req.AppID,
		DisplayName: req.DisplayName,
	}, gatewayServerBaseURL(r))
	h.writeGatewayResult(w, http.StatusCreated, resp, err)
}

func (h *Handler) ListGatewayIngestKeys(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListIngestKeys(r.Context(), workspaceID)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) RevokeGatewayIngestKey(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.RevokeIngestKey(r.Context(), workspaceID, userID, chi.URLParam(r, "id"))
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) CreateGatewayBackend(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayCreateBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	resp, err := h.Gateway.CreateBackend(r.Context(), management.CreateBackendInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		Provider:    req.Provider,
		Slug:        req.Slug,
		DisplayName: req.DisplayName,
		BackendType: req.BackendType,
		BaseURL:     req.BaseURL,
		Key:         req.Key,
		Enabled:     enabled,
		SetDefault:  req.SetDefault,
		Metadata:    req.Metadata,
	})
	h.writeGatewayResult(w, http.StatusCreated, resp, err)
}

func (h *Handler) UpdateGatewayBackend(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayUpdateBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.UpdateBackend(r.Context(), management.UpdateBackendInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		BackendID:   chi.URLParam(r, "id"),
		DisplayName: req.DisplayName,
		BaseURL:     req.BaseURL,
		Key:         req.Key,
		Enabled:     req.Enabled,
		Metadata:    req.Metadata,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) CreateGatewayBackendCredential(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayCreateBackendCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	resp, err := h.Gateway.CreateBackendCredential(r.Context(), management.CreateBackendCredentialInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		BackendID:   chi.URLParam(r, "id"),
		Label:       req.Label,
		Key:         req.Key,
		Enabled:     enabled,
		Priority:    req.Priority,
	})
	h.writeGatewayResult(w, http.StatusCreated, resp, err)
}

func (h *Handler) UpdateGatewayBackendCredential(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayUpdateBackendCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.UpdateBackendCredential(r.Context(), management.UpdateBackendCredentialInput{
		WorkspaceID:  workspaceID,
		ActorUserID:  userID,
		BackendID:    chi.URLParam(r, "id"),
		CredentialID: chi.URLParam(r, "credentialID"),
		Label:        req.Label,
		Key:          req.Key,
		Enabled:      req.Enabled,
		Priority:     req.Priority,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) DeleteGatewayBackend(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	err := h.Gateway.DeleteBackend(r.Context(), workspaceID, userID, chi.URLParam(r, "id"))
	h.writeGatewayResult(w, http.StatusOK, map[string]bool{"deleted": true}, err)
}

func (h *Handler) ListGatewayAudit(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	limit, ok := gatewayAuditLimit(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListAudit(r.Context(), workspaceID, limit)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayProviderRisks(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListProviderRisks(r.Context(), workspaceID)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayPolicyDecisions(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	limit, ok := gatewayAuditLimit(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListPolicyDecisions(r.Context(), workspaceID, limit)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ApproveGatewayPolicyDecision(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayPolicyDecisionApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.ApprovePolicyDecision(r.Context(), management.ApprovePolicyDecisionInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		DecisionID:  chi.URLParam(r, "id"),
		Reason:      req.Reason,
		ExpiresAt:   req.ExpiresAt,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) DenyGatewayPolicyDecision(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayPolicyDecisionApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.DenyPolicyDecision(r.Context(), management.DenyPolicyDecisionInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		DecisionID:  chi.URLParam(r, "id"),
		Reason:      req.Reason,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayEvidence(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	limit, ok := gatewayAuditLimit(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListEvidence(r.Context(), workspaceID, limit)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayControlMappings(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListControlMappings(r.Context(), workspaceID)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayIncidents(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	limit, ok := gatewayAuditLimit(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListIncidents(r.Context(), workspaceID, limit)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) UpdateGatewayIncident(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayUpdateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.UpdateIncident(r.Context(), management.UpdateIncidentInput{
		WorkspaceID:      workspaceID,
		ActorUserID:      userID,
		IncidentID:       chi.URLParam(r, "id"),
		Status:           req.Status,
		RemediationNotes: req.RemediationNotes,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayPolicyExceptions(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	limit, ok := gatewayAuditLimit(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListPolicyExceptions(r.Context(), workspaceID, limit)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) CreateGatewayPolicyException(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayCreatePolicyExceptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.CreatePolicyException(r.Context(), management.CreatePolicyExceptionInput{
		WorkspaceID:   workspaceID,
		ActorUserID:   userID,
		Reason:        req.Reason,
		ResourceType:  req.ResourceType,
		ResourceID:    req.ResourceID,
		ResourceLabel: req.ResourceLabel,
		ExpiresAt:     req.ExpiresAt,
	})
	h.writeGatewayResult(w, http.StatusCreated, resp, err)
}

func (h *Handler) UpdateGatewayPolicyException(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayUpdatePolicyExceptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.UpdatePolicyException(r.Context(), management.UpdatePolicyExceptionInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		ExceptionID: chi.URLParam(r, "id"),
		Status:      req.Status,
		ExpiresAt:   req.ExpiresAt,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayGovernancePolicies(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.ListGovernancePolicies(r.Context(), workspaceID)
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) CreateGatewayGovernancePolicy(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayGovernancePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.CreateGovernancePolicy(r.Context(), management.CreateGovernancePolicyInput{
		WorkspaceID:     workspaceID,
		ActorUserID:     userID,
		Name:            req.Name,
		Description:     req.Description,
		PolicyType:      req.PolicyType,
		Enabled:         req.Enabled,
		RuleDefinition:  req.RuleDefinition,
		EnforcementMode: req.EnforcementMode,
	})
	h.writeGatewayResult(w, http.StatusCreated, resp, err)
}

func (h *Handler) UpdateGatewayGovernancePolicy(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayGovernancePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.UpdateGovernancePolicy(r.Context(), management.UpdateGovernancePolicyInput{
		WorkspaceID:     workspaceID,
		ActorUserID:     userID,
		PolicyID:        chi.URLParam(r, "id"),
		Name:            req.Name,
		Description:     req.Description,
		PolicyType:      req.PolicyType,
		Enabled:         req.Enabled,
		RuleDefinition:  req.RuleDefinition,
		EnforcementMode: req.EnforcementMode,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) UpsertGatewayProviderRisk(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayProviderRiskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.UpsertProviderRisk(r.Context(), management.UpsertProviderRiskInput{
		WorkspaceID:          workspaceID,
		ActorUserID:          userID,
		ProviderName:         req.ProviderName,
		BackendID:            req.BackendID,
		ApprovedUseCases:     req.ApprovedUseCases,
		DataCategories:       req.DataCategories,
		Regions:              req.Regions,
		HostingNotes:         req.HostingNotes,
		ContractStatus:       req.ContractStatus,
		SecurityReviewStatus: req.SecurityReviewStatus,
		EvidenceLinks:        req.EvidenceLinks,
		Limitations:          req.Limitations,
		ProhibitedUses:       req.ProhibitedUses,
		ModelList:            req.ModelList,
		CapabilityClass:      req.CapabilityClass,
		RiskScore:            req.RiskScore,
		ReviewCadenceDays:    req.ReviewCadenceDays,
		LastAssessmentAt:     req.LastAssessmentAt,
		NextReviewAt:         req.NextReviewAt,
		ActiveExceptionCount: req.ActiveExceptionCount,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) SetGatewayDefaultBackend(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewaySetDefaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.SetDefaultBackend(r.Context(), management.SetDefaultBackendInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		Slug:        req.BackendSlug,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func (h *Handler) UpdateGatewayPolicy(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	var req gatewayPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.Gateway.UpdateCapturePolicy(r.Context(), management.CapturePolicyInput{
		WorkspaceID:   workspaceID,
		ActorUserID:   userID,
		CapturePolicy: req.CapturePolicy,
	})
	h.writeGatewayResult(w, http.StatusOK, resp, err)
}

func gatewayAuditLimit(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 20, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		writeError(w, http.StatusBadRequest, "limit must be a positive integer")
		return 0, false
	}
	if limit > 100 {
		limit = 100
	}
	return int32(limit), true
}

func (h *Handler) gatewayRequestScope(w http.ResponseWriter, r *http.Request) (workspaceID, userID string, ok bool) {
	userID, ok = requireUserID(w, r)
	if !ok {
		return "", "", false
	}

	workspaceID = resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return "", "", false
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return "", "", false
	}
	return workspaceID, userID, true
}

func gatewayServerBaseURL(r *http.Request) string {
	for _, envName := range []string{"MULTICA_GATEWAY_BASE_URL", "MULTICA_SERVER_URL"} {
		if baseURL, ok := normalizeGatewayBaseURL(os.Getenv(envName)); ok {
			return baseURL
		}
	}

	proto := firstHeaderValue(r.Header.Get("X-Forwarded-Proto"))
	if proto != "http" && proto != "https" {
		proto = ""
	}

	host := ""
	if proto != "" {
		forwardedHost := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
		if validGatewayHost(forwardedHost) {
			host = forwardedHost
		}
	}
	if host == "" && validGatewayHost(r.Host) {
		host = strings.TrimSpace(r.Host)
	}

	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	if host == "" {
		host = "localhost"
	}
	if baseURL, ok := normalizeGatewayBaseURL(proto + "://" + host); ok {
		return baseURL
	}
	return proto + "://localhost"
}

func normalizeGatewayBaseURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	if parsed.Host == "" {
		return "", false
	}
	return strings.TrimRight(parsed.String(), "/"), true
}

func firstHeaderValue(raw string) string {
	if idx := strings.Index(raw, ","); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.TrimSpace(raw)
}

func validGatewayHost(raw string) bool {
	host := strings.TrimSpace(raw)
	if host == "" || strings.ContainsAny(host, `/\@`) {
		return false
	}
	for _, r := range host {
		if unicode.IsSpace(r) {
			return false
		}
	}
	parsed, err := url.Parse("http://" + host)
	return err == nil && parsed.Host != ""
}

func (h *Handler) writeGatewayResult(w http.ResponseWriter, status int, payload any, err error) {
	if err == nil {
		writeJSON(w, status, payload)
		return
	}

	switch {
	case errors.Is(err, management.ErrInvalidCapturePolicy), errors.Is(err, management.ErrInvalidGatewayBackend):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, management.ErrGatewayBackendNotFound), errors.Is(err, management.ErrGatewayKeyNotFound), errors.Is(err, management.ErrGatewayPolicyDecisionNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, management.ErrGatewaySecretNotConfigured):
		writeError(w, http.StatusInternalServerError, "gateway secret key is not configured")
	default:
		slog.Error("gateway request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "gateway request failed")
	}
}

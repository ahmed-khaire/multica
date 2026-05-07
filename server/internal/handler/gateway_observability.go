package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/gateway/management"
	"github.com/multica-ai/multica/server/internal/gateway/observability"
)

type gatewayExportResponse struct {
	GeneratedAt     string                            `json:"generated_at"`
	WorkspaceID     string                            `json:"workspace_id"`
	Overview        observability.OverviewResponse    `json:"overview"`
	Sessions        observability.SessionListResponse `json:"sessions"`
	LLMCalls        observability.LLMCallListResponse `json:"llm_calls"`
	PolicyDecisions any                               `json:"policy_decisions"`
	Evidence        any                               `json:"evidence"`
}

type gatewayEvidenceBundleDescriptor struct {
	Subject gatewayEvidenceBundleSubject `json:"subject"`
}

type gatewayEvidenceBundleSubject struct {
	SessionID         string `json:"session_id"`
	IncidentID        string `json:"incident_id"`
	PolicyDecisionID  string `json:"policy_decision_id"`
	ListLimit         int32  `json:"list_limit"`
	GeneratedBy       string `json:"generated_by"`
	CapturePolicyNote string `json:"capture_policy_note"`
}

type gatewayEvidenceBundleExportMetadata struct {
	GeneratedAt  string   `json:"generated_at"`
	WorkspaceID  string   `json:"workspace_id"`
	SubjectID    string   `json:"subject_id"`
	SubjectType  string   `json:"subject_type"`
	DigestSHA256 string   `json:"digest_sha256"`
	Sections     []string `json:"sections"`
}

type gatewayEvidenceBundleResponse struct {
	EvidenceBundle     gatewayEvidenceBundleDescriptor      `json:"evidence_bundle"`
	Export             gatewayEvidenceBundleExportMetadata  `json:"export"`
	SessionDetail      *observability.SessionDetailResponse `json:"session_detail,omitempty"`
	SessionSpans       *observability.SessionSpansResponse  `json:"session_spans,omitempty"`
	LLMCalls           observability.LLMCallListResponse    `json:"llm_calls"`
	PolicyDecisions    []management.PolicyDecisionItem      `json:"policy_decisions"`
	Evidence           []management.EvidenceItem            `json:"evidence"`
	Incidents          []management.IncidentItem            `json:"incidents"`
	ProviderRisks      []management.ProviderRiskResponse    `json:"provider_risks"`
	ControlMappings    []management.ControlMappingItem      `json:"control_mappings"`
	GovernancePolicies []management.GovernancePolicyItem    `json:"governance_policies"`
}

func (h *Handler) GatewayOverview(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	filter, err := observability.ParseFilter(r.URL.Query(), time.Now())
	if err != nil {
		h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
		return
	}

	resp, err := h.GatewayObservability.Overview(r.Context(), workspaceID, filter)
	h.writeGatewayObservabilityResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewaySessions(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	filter, err := observability.ParseFilter(r.URL.Query(), time.Now())
	if err != nil {
		h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
		return
	}

	resp, err := h.GatewayObservability.ListSessions(r.Context(), workspaceID, filter)
	h.writeGatewayObservabilityResult(w, http.StatusOK, resp, err)
}

func (h *Handler) GetGatewaySession(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.GatewayObservability.GetSession(r.Context(), workspaceID, chi.URLParam(r, "id"))
	h.writeGatewayObservabilityResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewaySessionSpans(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.GatewayObservability.ListSessionSpans(r.Context(), workspaceID, chi.URLParam(r, "id"))
	h.writeGatewayObservabilityResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ListGatewayLLMCalls(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	filter, err := observability.ParseFilter(r.URL.Query(), time.Now())
	if err != nil {
		h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
		return
	}

	resp, err := h.GatewayObservability.ListLLMCalls(r.Context(), workspaceID, filter)
	h.writeGatewayObservabilityResult(w, http.StatusOK, resp, err)
}

func (h *Handler) ExportGatewayData(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	now := time.Now()
	filter, err := observability.ParseFilter(r.URL.Query(), now)
	if err != nil {
		h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
		return
	}
	limit, ok := gatewayAuditLimit(w, r)
	if !ok {
		return
	}

	overview, err := h.GatewayObservability.Overview(r.Context(), workspaceID, filter)
	if err != nil {
		h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
		return
	}
	sessions, err := h.GatewayObservability.ListSessions(r.Context(), workspaceID, filter)
	if err != nil {
		h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
		return
	}
	llmCalls, err := h.GatewayObservability.ListLLMCalls(r.Context(), workspaceID, filter)
	if err != nil {
		h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
		return
	}
	policyDecisions, err := h.Gateway.ListPolicyDecisions(r.Context(), workspaceID, limit)
	if err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}
	evidence, err := h.Gateway.ListEvidence(r.Context(), workspaceID, limit)
	if err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}
	if err := h.Gateway.RecordExportAudit(r.Context(), workspaceID, userID, map[string]any{
		"since":    filter.Since.Format(time.RFC3339Nano),
		"until":    filter.Until.Format(time.RFC3339Nano),
		"limit":    limit,
		"sections": []string{"overview", "sessions", "llm_calls", "policy_decisions", "evidence"},
	}); err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}

	writeJSON(w, http.StatusOK, gatewayExportResponse{
		GeneratedAt:     now.UTC().Format(time.RFC3339Nano),
		WorkspaceID:     workspaceID,
		Overview:        overview,
		Sessions:        sessions,
		LLMCalls:        llmCalls,
		PolicyDecisions: policyDecisions,
		Evidence:        evidence,
	})
}

func (h *Handler) ExportGatewayEvidenceBundle(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	now := time.Now()
	filter, err := observability.ParseFilter(r.URL.Query(), now)
	if err != nil {
		h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
		return
	}
	subject, subjectType, subjectID, ok := gatewayEvidenceBundleSubjectFromRequest(w, r, filter.Limit)
	if !ok {
		return
	}

	llmCalls, err := h.GatewayObservability.ListLLMCalls(r.Context(), workspaceID, filter)
	if err != nil {
		h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
		return
	}
	policyDecisions, err := h.Gateway.ListPolicyDecisions(r.Context(), workspaceID, filter.Limit)
	if err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}
	evidence, err := h.Gateway.ListEvidence(r.Context(), workspaceID, filter.Limit)
	if err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}
	incidents, err := h.Gateway.ListIncidents(r.Context(), workspaceID, filter.Limit)
	if err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}
	providerRisks, err := h.Gateway.ListProviderRisks(r.Context(), workspaceID)
	if err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}
	controlMappings, err := h.Gateway.ListControlMappings(r.Context(), workspaceID)
	if err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}
	governancePolicies, err := h.Gateway.ListGovernancePolicies(r.Context(), workspaceID)
	if err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}

	sessionID := subject.SessionID
	if sessionID == "" {
		sessionID = gatewayEvidenceBundleSessionID(subject, policyDecisions, incidents)
	}
	var sessionDetail *observability.SessionDetailResponse
	var sessionSpans *observability.SessionSpansResponse
	if sessionID != "" {
		detail, err := h.GatewayObservability.GetSession(r.Context(), workspaceID, sessionID)
		if err != nil {
			h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
			return
		}
		spans, err := h.GatewayObservability.ListSessionSpans(r.Context(), workspaceID, sessionID)
		if err != nil {
			h.writeGatewayObservabilityResult(w, http.StatusOK, nil, err)
			return
		}
		sessionDetail = &detail
		sessionSpans = &spans
	}

	sections := []string{
		"evidence_bundle",
		"session_detail",
		"session_spans",
		"llm_calls",
		"policy_decisions",
		"evidence",
		"incidents",
		"provider_risks",
		"control_mappings",
		"governance_policies",
	}
	resp := gatewayEvidenceBundleResponse{
		EvidenceBundle:     gatewayEvidenceBundleDescriptor{Subject: subject},
		SessionDetail:      sessionDetail,
		SessionSpans:       sessionSpans,
		LLMCalls:           llmCalls,
		PolicyDecisions:    policyDecisions,
		Evidence:           evidence,
		Incidents:          incidents,
		ProviderRisks:      providerRisks,
		ControlMappings:    controlMappings,
		GovernancePolicies: governancePolicies,
	}
	resp.Export = gatewayEvidenceBundleExportMetadata{
		GeneratedAt:  now.UTC().Format(time.RFC3339Nano),
		WorkspaceID:  workspaceID,
		SubjectID:    subjectID,
		SubjectType:  subjectType,
		DigestSHA256: gatewayEvidenceBundleDigest(resp),
		Sections:     sections,
	}

	if err := h.Gateway.RecordExportAudit(r.Context(), workspaceID, userID, map[string]any{
		"export_type":   "evidence_bundle",
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"limit":         filter.Limit,
		"digest_sha256": resp.Export.DigestSHA256,
		"sections":      sections,
	}); err != nil {
		h.writeGatewayResult(w, http.StatusOK, nil, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func gatewayEvidenceBundleSubjectFromRequest(w http.ResponseWriter, r *http.Request, limit int32) (gatewayEvidenceBundleSubject, string, string, bool) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	incidentID := strings.TrimSpace(r.URL.Query().Get("incident_id"))
	policyDecisionID := strings.TrimSpace(r.URL.Query().Get("policy_decision_id"))
	subjectCount := 0
	subjectType := ""
	subjectID := ""
	if sessionID != "" {
		subjectCount++
		subjectType = "session"
		subjectID = sessionID
	}
	if incidentID != "" {
		subjectCount++
		subjectType = "incident"
		subjectID = incidentID
	}
	if policyDecisionID != "" {
		subjectCount++
		subjectType = "policy_decision"
		subjectID = policyDecisionID
	}
	if subjectCount == 0 {
		writeError(w, http.StatusBadRequest, "one of session_id, incident_id, or policy_decision_id is required")
		return gatewayEvidenceBundleSubject{}, "", "", false
	}
	if subjectCount > 1 {
		writeError(w, http.StatusBadRequest, "only one evidence bundle subject may be provided")
		return gatewayEvidenceBundleSubject{}, "", "", false
	}
	return gatewayEvidenceBundleSubject{
		SessionID:         sessionID,
		IncidentID:        incidentID,
		PolicyDecisionID:  policyDecisionID,
		ListLimit:         limit,
		GeneratedBy:       "multica gateway api",
		CapturePolicyNote: "content visibility follows the workspace Gateway capture policy",
	}, subjectType, subjectID, true
}

func gatewayEvidenceBundleSessionID(subject gatewayEvidenceBundleSubject, policyDecisions []management.PolicyDecisionItem, incidents []management.IncidentItem) string {
	if subject.PolicyDecisionID != "" {
		for _, decision := range policyDecisions {
			if decision.ID == subject.PolicyDecisionID {
				return decision.SessionID
			}
		}
	}
	if subject.IncidentID != "" {
		for _, incident := range incidents {
			if incident.ID == subject.IncidentID {
				return incident.LinkedSessionID
			}
		}
	}
	return ""
}

func gatewayEvidenceBundleDigest(resp gatewayEvidenceBundleResponse) string {
	resp.Export = gatewayEvidenceBundleExportMetadata{}
	raw, err := json.Marshal(resp)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (h *Handler) writeGatewayObservabilityResult(w http.ResponseWriter, status int, payload any, err error) {
	if err == nil {
		writeJSON(w, status, payload)
		return
	}

	switch {
	case errors.Is(err, observability.ErrInvalidFilter), errors.Is(err, observability.ErrInvalidID):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, observability.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		slog.Error("gateway observability request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "gateway observability request failed")
	}
}

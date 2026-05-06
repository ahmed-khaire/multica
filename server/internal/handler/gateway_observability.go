package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
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

package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/gateway/observability"
)

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

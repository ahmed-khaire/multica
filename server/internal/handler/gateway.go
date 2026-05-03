package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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

type gatewaySetDefaultRequest struct {
	BackendSlug string `json:"backend_slug"`
}

type gatewayPolicyRequest struct {
	CapturePolicy string `json:"capture_policy"`
}

func (h *Handler) GatewayStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	resp, err := h.Gateway.Status(r.Context(), workspaceID, userID, gatewayServerBaseURL(r))
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

func (h *Handler) DeleteGatewayBackend(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}

	err := h.Gateway.DeleteBackend(r.Context(), workspaceID, userID, chi.URLParam(r, "id"))
	h.writeGatewayResult(w, http.StatusNoContent, nil, err)
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
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if proto != "" && forwardedHost != "" {
		return strings.TrimRight(proto+"://"+forwardedHost, "/")
	}

	host := strings.TrimSpace(r.Host)
	if proto != "" && host != "" {
		return strings.TrimRight(proto+"://"+host, "/")
	}

	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

func (h *Handler) writeGatewayResult(w http.ResponseWriter, status int, payload any, err error) {
	if err == nil {
		writeJSON(w, status, payload)
		return
	}

	switch {
	case errors.Is(err, management.ErrInvalidCapturePolicy), errors.Is(err, management.ErrInvalidGatewayBackend):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, management.ErrGatewayBackendNotFound), errors.Is(err, management.ErrGatewayKeyNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, management.ErrGatewaySecretNotConfigured):
		writeError(w, http.StatusInternalServerError, "gateway secret key is not configured")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

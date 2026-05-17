package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/gateway/management"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	gatewayJobTypeSubscriptionValidation = "gateway_subscription_validation"
	gatewayJobTypeRuntimeRequest         = "gateway_runtime_request"
)

type daemonGatewayJobResponse struct {
	Job *daemonGatewayJob `json:"job"`
}

type daemonGatewayJob struct {
	ID                   string         `json:"id"`
	Type                 string         `json:"type"`
	WorkspaceID          string         `json:"workspace_id"`
	BackendID            string         `json:"backend_id"`
	CredentialID         string         `json:"credential_id"`
	SubscriptionProvider string         `json:"subscription_provider"`
	Surface              string         `json:"surface,omitempty"`
	RequestBody          map[string]any `json:"request_body,omitempty"`
	EncryptedPayload     string         `json:"encrypted_payload,omitempty"`
	PayloadFormat        string         `json:"payload_format,omitempty"`
}

type daemonGatewayValidationCompleteRequest struct {
	AccountHint        string `json:"account_hint"`
	AccountFingerprint string `json:"account_fingerprint"`
}

type daemonGatewayFailRequest struct {
	ErrorCode    string `json:"error_code"`
	ErrorType    string `json:"error_type"`
	ErrorMessage string `json:"error_message"`
}

type daemonGatewayRuntimeCompleteRequest struct {
	Response map[string]any `json:"response"`
}

func (h *Handler) ClaimGatewayJobByRuntime(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.requireDaemonRuntime(w, r)
	if !ok {
		return
	}
	provider := subscriptionProviderForRuntimeProvider(rt.Provider)
	if provider == "" {
		writeJSON(w, http.StatusOK, daemonGatewayJobResponse{Job: nil})
		return
	}

	validation, err := h.Queries.ClaimGatewaySubscriptionValidation(r.Context(), db.ClaimGatewaySubscriptionValidationParams{
		WorkspaceID: rt.WorkspaceID,
		RuntimeID:   rt.ID,
		Provider:    provider,
	})
	if err == nil {
		job, err := h.gatewayValidationJob(r, validation)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build gateway validation job")
			return
		}
		writeJSON(w, http.StatusOK, daemonGatewayJobResponse{Job: job})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to claim gateway validation: "+err.Error())
		return
	}

	request, err := h.Queries.ClaimGatewayRuntimeRequest(r.Context(), db.ClaimGatewayRuntimeRequestParams{
		WorkspaceID: rt.WorkspaceID,
		RuntimeID:   rt.ID,
		Provider:    provider,
	})
	if err == nil {
		job := gatewayRuntimeRequestJob(request)
		writeJSON(w, http.StatusOK, daemonGatewayJobResponse{Job: &job})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to claim gateway request: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, daemonGatewayJobResponse{Job: nil})
}

func (h *Handler) CompleteGatewayValidation(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.requireDaemonRuntime(w, r)
	if !ok {
		return
	}
	var req daemonGatewayValidationCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	validation, err := h.Queries.CompleteGatewaySubscriptionValidation(r.Context(), db.CompleteGatewaySubscriptionValidationParams{
		WorkspaceID:        rt.WorkspaceID,
		ID:                 parseUUID(chi.URLParam(r, "validationId")),
		RuntimeID:          rt.ID,
		AccountHint:        req.AccountHint,
		AccountFingerprint: req.AccountFingerprint,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to complete gateway validation: "+err.Error())
		return
	}
	if _, err := h.Queries.UpdateGatewayBackendValidationStatus(r.Context(), db.UpdateGatewayBackendValidationStatusParams{
		WorkspaceID:         rt.WorkspaceID,
		ID:                  validation.BackendID,
		ValidationStatus:    "active",
		ValidatedRuntimeID:  rt.ID,
		LastValidationError: "",
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to activate gateway backend: "+err.Error())
		return
	}
	if _, err := h.Queries.UpdateGatewayBackendCredentialValidationStatus(r.Context(), db.UpdateGatewayBackendCredentialValidationStatusParams{
		WorkspaceID:         rt.WorkspaceID,
		BackendID:           validation.BackendID,
		ID:                  validation.CredentialID,
		ValidationStatus:    "active",
		ValidatedRuntimeID:  rt.ID,
		AccountHint:         req.AccountHint,
		AccountFingerprint:  req.AccountFingerprint,
		LastValidationError: "",
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to activate gateway credential: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, validation)
}

func (h *Handler) FailGatewayValidation(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.requireDaemonRuntime(w, r)
	if !ok {
		return
	}
	var req daemonGatewayFailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ErrorMessage == "" {
		req.ErrorMessage = "gateway validation failed"
	}

	validation, err := h.Queries.FailGatewaySubscriptionValidation(r.Context(), db.FailGatewaySubscriptionValidationParams{
		WorkspaceID:  rt.WorkspaceID,
		ID:           parseUUID(chi.URLParam(r, "validationId")),
		RuntimeID:    rt.ID,
		ErrorCode:    req.ErrorCode,
		ErrorMessage: req.ErrorMessage,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to record gateway validation failure: "+err.Error())
		return
	}
	_, _ = h.Queries.UpdateGatewayBackendValidationStatus(r.Context(), db.UpdateGatewayBackendValidationStatusParams{
		WorkspaceID:         rt.WorkspaceID,
		ID:                  validation.BackendID,
		ValidationStatus:    "invalid_credentials",
		ValidatedRuntimeID:  rt.ID,
		LastValidationError: req.ErrorMessage,
	})
	_, _ = h.Queries.UpdateGatewayBackendCredentialValidationStatus(r.Context(), db.UpdateGatewayBackendCredentialValidationStatusParams{
		WorkspaceID:         rt.WorkspaceID,
		BackendID:           validation.BackendID,
		ID:                  validation.CredentialID,
		ValidationStatus:    "invalid_credentials",
		ValidatedRuntimeID:  rt.ID,
		LastValidationError: req.ErrorMessage,
	})

	writeJSON(w, http.StatusOK, validation)
}

func (h *Handler) CompleteGatewayRuntimeRequest(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.requireDaemonRuntime(w, r)
	if !ok {
		return
	}
	var req daemonGatewayRuntimeCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	raw, err := json.Marshal(req.Response)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid response body")
		return
	}
	row, err := h.Queries.CompleteGatewayRuntimeRequest(r.Context(), db.CompleteGatewayRuntimeRequestParams{
		WorkspaceID:  rt.WorkspaceID,
		ID:           parseUUID(chi.URLParam(r, "requestId")),
		RuntimeID:    rt.ID,
		ResponseBody: raw,
	})
	h.writeGatewayResult(w, http.StatusOK, row, err)
}

func (h *Handler) FailGatewayRuntimeRequest(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.requireDaemonRuntime(w, r)
	if !ok {
		return
	}
	var req daemonGatewayFailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ErrorType == "" {
		req.ErrorType = req.ErrorCode
	}
	if req.ErrorMessage == "" {
		req.ErrorMessage = "gateway runtime request failed"
	}
	row, err := h.Queries.FailGatewayRuntimeRequest(r.Context(), db.FailGatewayRuntimeRequestParams{
		WorkspaceID:  rt.WorkspaceID,
		ID:           parseUUID(chi.URLParam(r, "requestId")),
		RuntimeID:    rt.ID,
		ErrorType:    req.ErrorType,
		ErrorMessage: req.ErrorMessage,
	})
	h.writeGatewayResult(w, http.StatusOK, row, err)
}

func (h *Handler) requireDaemonRuntime(w http.ResponseWriter, r *http.Request) (db.AgentRuntime, bool) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, err := h.Queries.GetAgentRuntime(r.Context(), parseUUID(runtimeID))
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return db.AgentRuntime{}, false
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found"); !ok {
		return db.AgentRuntime{}, false
	}
	return rt, true
}

func (h *Handler) gatewayValidationJob(r *http.Request, validation db.GatewaySubscriptionRuntimeValidation) (*daemonGatewayJob, error) {
	credential, err := h.Queries.GetGatewayBackendCredentialByID(r.Context(), db.GetGatewayBackendCredentialByIDParams{
		WorkspaceID: validation.WorkspaceID,
		BackendID:   validation.BackendID,
		ID:          validation.CredentialID,
	})
	if err != nil {
		return nil, err
	}
	return &daemonGatewayJob{
		ID:                   uuidToString(validation.ID),
		Type:                 gatewayJobTypeSubscriptionValidation,
		WorkspaceID:          uuidToString(validation.WorkspaceID),
		BackendID:            uuidToString(validation.BackendID),
		CredentialID:         uuidToString(validation.CredentialID),
		SubscriptionProvider: validation.Provider,
		EncryptedPayload:     base64.StdEncoding.EncodeToString(credential.EncryptedPayload),
		PayloadFormat:        credential.PayloadFormat,
	}, nil
}

func gatewayRuntimeRequestJob(row db.GatewayRuntimeRequest) daemonGatewayJob {
	var body map[string]any
	if len(row.RequestBody) > 0 {
		_ = json.Unmarshal(row.RequestBody, &body)
	}
	if body == nil {
		body = map[string]any{}
	}
	return daemonGatewayJob{
		ID:                   uuidToString(row.ID),
		Type:                 gatewayJobTypeRuntimeRequest,
		WorkspaceID:          uuidToString(row.WorkspaceID),
		BackendID:            uuidToString(row.BackendID),
		CredentialID:         uuidToString(row.CredentialID),
		SubscriptionProvider: row.Provider,
		Surface:              row.Surface,
		RequestBody:          body,
	}
}

func subscriptionProviderForRuntimeProvider(provider string) string {
	switch provider {
	case "codex":
		return management.SubscriptionProviderCodex
	case "claude":
		return management.SubscriptionProviderClaudeCode
	default:
		return ""
	}
}

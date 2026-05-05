package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const maxProxyRequestBody = 16 << 20

type Service struct {
	Queries   *db.Queries
	Resolver  *Resolver
	Forwarder *Forwarder
	Recorder  *Recorder
}

func NewService(queries *db.Queries, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	return &Service{
		Queries:   queries,
		Resolver:  NewResolver(queries),
		Forwarder: NewForwarder(client),
		Recorder:  NewRecorder(queries),
	}
}

func (s *Service) ServeOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, SurfaceOpenAIChatCompletions)
}

func (s *Service) ServeAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, SurfaceAnthropicMessages)
}

func (s *Service) ServeModels(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, SurfaceModels)
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request, surface string) {
	protocol := ProtocolForRequest(r, surface)
	rawKey, ok := ExtractGatewayKey(r)
	if !ok {
		writeProviderError(w, protocol, AuthenticationError("gateway key is required", ErrGatewayKeyRequired))
		return
	}

	authCtx, err := AuthenticateGatewayKey(r.Context(), s.Queries, rawKey)
	if err != nil {
		if errors.Is(err, ErrGatewayKeyInvalid) {
			writeProviderError(w, protocol, AuthenticationError("gateway key is invalid", err))
			return
		}
		writeProviderError(w, protocol, GatewayError{
			StatusCode:    http.StatusInternalServerError,
			PublicMessage: "gateway authentication failed",
			ErrorType:     "server_error",
			Code:          "gateway_authentication_failed",
			Cause:         err,
		})
		return
	}

	summary, err := summarizeRequest(r, surface, protocol)
	if err != nil {
		writeProviderError(w, protocol, RoutingError(http.StatusBadRequest, "invalid request body", "invalid_request_body", err))
		return
	}

	target, err := s.Resolver.ResolveDefaultBackend(r.Context(), authCtx.WorkspaceID, protocol)
	if err != nil {
		gwErr := normalizeGatewayError(err)
		if errors.Is(gwErr, ErrProviderRiskBlocked) {
			s.recordPolicyBlock(context.Background(), authCtx, gwErr)
		}
		writeProviderError(w, protocol, gwErr)
		return
	}
	summary.RoutePath = routePathFor(surface, target)

	obs := s.Recorder.Start(r.Context(), authCtx, target, summary)
	result, err := s.Forwarder.Forward(r.Context(), w, r, target, summary)
	if err != nil && result.StatusCode == 0 {
		gwErr := GatewayError{
			StatusCode:    http.StatusBadGateway,
			PublicMessage: "upstream request failed",
			ErrorType:     "api_error",
			Code:          "gateway_upstream_failed",
			Cause:         err,
		}
		result.StatusCode = gwErr.StatusCode
		result.Status = StatusGatewayError
		result.ErrorType = gwErr.ErrorType
		result.ErrorMessage = gwErr.PublicMessage
		s.Recorder.Complete(context.Background(), obs, result)
		writeProviderError(w, protocol, gwErr)
		return
	}
	s.Recorder.Complete(context.Background(), obs, result)
}

func (s *Service) recordPolicyBlock(ctx context.Context, authCtx AuthContext, gwErr GatewayError) {
	if s.Queries == nil {
		return
	}
	workspaceID, err := parseUUID(authCtx.WorkspaceID)
	if err != nil {
		return
	}
	userID, err := parseUUID(authCtx.UserID)
	if err != nil {
		return
	}
	resourceType := gwErr.ResourceType
	if resourceType == "" {
		resourceType = "gateway"
	}
	resourceID := gwErr.ResourceID
	resourceLabel := gwErr.ResourceLabel
	matchedRules, err := json.Marshal([]map[string]string{{
		"id":          gwErr.Code,
		"action":      "block",
		"reason_code": gwErr.Code,
	}})
	if err != nil {
		matchedRules = []byte("[]")
	}
	if _, err := s.Queries.RecordGatewayPolicyDecision(ctx, db.RecordGatewayPolicyDecisionParams{
		WorkspaceID:        workspaceID,
		SubjectUserID:      userID,
		ResourceType:       resourceType,
		ResourceID:         resourceID,
		ResourceLabel:      resourceLabel,
		Decision:           "block",
		ReasonCode:         gwErr.Code,
		MatchedRules:       matchedRules,
		ApprovalStatus:     pgtype.Text{},
		EvidenceReferences: []byte("[]"),
	}); err != nil {
		return
	}

	payload, err := json.Marshal(map[string]any{
		"decision":        "block",
		"reason_code":     gwErr.Code,
		"resource_type":   resourceType,
		"resource_id":     resourceID,
		"resource_label":  resourceLabel,
		"subject_user_id": authCtx.UserID,
	})
	if err != nil {
		payload = []byte("{}")
	}
	if resourceLabel == "" {
		resourceLabel = resourceID
	}
	_, _ = s.Queries.CreateAIEvidence(ctx, db.CreateAIEvidenceParams{
		WorkspaceID:          workspaceID,
		EvidenceType:         "gateway_policy_decision",
		FrameworkRefs:        []byte(`["internal_gateway_governance"]`),
		LinkedBackendID:      optionalParsedUUID(resourceID),
		LinkedProviderRiskID: optionalParsedUUID(gwErr.ProviderRiskID),
		Summary:              fmt.Sprintf("Gateway blocked provider %s: %s", resourceLabel, gwErr.Code),
		Payload:              payload,
		AttachmentRef:        "",
	})
	_, _ = s.Queries.CreateAIIncident(ctx, db.CreateAIIncidentParams{
		WorkspaceID:          workspaceID,
		Severity:             providerRiskBlockSeverity(gwErr.Code),
		Category:             "gateway_provider_risk_block",
		LinkedProviderRiskID: optionalParsedUUID(gwErr.ProviderRiskID),
		Summary:              fmt.Sprintf("Gateway blocked provider %s: %s", resourceLabel, gwErr.Code),
		Status:               "open",
		RemediationNotes:     "Review provider risk register before enabling this backend.",
	})
}

func optionalParsedUUID(value string) pgtype.UUID {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}
	}
	id, err := parseUUID(value)
	if err != nil {
		return pgtype.UUID{}
	}
	return id
}

func providerRiskBlockSeverity(code string) string {
	if code == "provider_risk_rejected" {
		return "high"
	}
	return "medium"
}

func summarizeRequest(r *http.Request, surface, protocol string) (RequestSummary, error) {
	summary := RequestSummary{
		Protocol: protocol,
		Surface:  surface,
		Method:   r.Method,
	}
	if r.Body == nil || r.Method == http.MethodGet {
		return summary, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProxyRequestBody+1))
	if err != nil {
		return RequestSummary{}, err
	}
	if len(body) > maxProxyRequestBody {
		return RequestSummary{}, fmt.Errorf("request body exceeds %d bytes", maxProxyRequestBody)
	}
	summary.Body = body
	if len(body) == 0 {
		return summary, nil
	}
	var bodyJSON map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&bodyJSON); err != nil {
		return RequestSummary{}, err
	}
	summary.BodyJSON = bodyJSON
	if model, ok := bodyJSON["model"].(string); ok {
		summary.Model = model
	}
	if stream, ok := bodyJSON["stream"].(bool); ok {
		summary.Stream = stream
	}
	return summary, nil
}

func routePathFor(surface string, target BackendTarget) string {
	switch surface {
	case SurfaceOpenAIChatCompletions:
		return "/chat/completions"
	case SurfaceAnthropicMessages:
		if strings.HasSuffix(strings.TrimRight(target.BaseURL, "/"), "/v1") {
			return "/messages"
		}
		return "/v1/messages"
	case SurfaceModels:
		if target.BackendType == "anthropic" && !strings.HasSuffix(strings.TrimRight(target.BaseURL, "/"), "/v1") {
			return "/v1/models"
		}
		return "/models"
	default:
		return rtrimSlash(surface)
	}
}

func writeProviderError(w http.ResponseWriter, protocol string, err GatewayError) {
	status := err.StatusCode
	if status == 0 {
		status = http.StatusInternalServerError
	}
	errorType := err.ErrorType
	if errorType == "" {
		errorType = "api_error"
	}
	code := err.Code
	if code == "" {
		code = "gateway_error"
	}
	message := err.PublicMessage
	if message == "" {
		message = "gateway request failed"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ProviderErrorBody(protocol, message, errorType, code))
}

func normalizeGatewayError(err error) GatewayError {
	var gatewayErr GatewayError
	if errors.As(err, &gatewayErr) {
		return gatewayErr
	}
	return GatewayError{
		StatusCode:    http.StatusInternalServerError,
		PublicMessage: "gateway request failed",
		ErrorType:     "api_error",
		Code:          "gateway_error",
		Cause:         err,
	}
}

func rtrimSlash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") {
		return value
	}
	return "/" + value
}

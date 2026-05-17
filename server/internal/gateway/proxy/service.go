package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	gatewaypolicy "github.com/multica-ai/multica/server/internal/gateway/policy"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const maxProxyRequestBody = 16 << 20

type Service struct {
	Queries   *db.Queries
	Resolver  *Resolver
	Forwarder *Forwarder
	Recorder  *Recorder
	Policy    *gatewaypolicy.Service
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
		Policy:    gatewaypolicy.NewService(queries),
	}
}

func (s *Service) ServeOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, SurfaceOpenAIChatCompletions)
}

func (s *Service) ServeOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, SurfaceOpenAIResponses)
}

func (s *Service) ServeAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, SurfaceAnthropicMessages)
}

func (s *Service) ServeAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, SurfaceAnthropicCountTokens)
}

func (s *Service) ServeModels(w http.ResponseWriter, r *http.Request) {
	protocol := ProtocolForRequest(r, SurfaceModels)
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

	s.serveModels(w, r, authCtx, protocol)
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
		if errors.Is(err, ErrBackendRoutingConflict) {
			writeProviderError(w, protocol, RoutingError(http.StatusBadRequest, "gateway backend header conflicts with model prefix", "gateway_backend_conflict", err))
			return
		}
		writeProviderError(w, protocol, RoutingError(http.StatusBadRequest, "invalid request body", "invalid_request_body", err))
		return
	}

	target, err := s.Resolver.ResolveBackend(r.Context(), authCtx.WorkspaceID, protocol, summary.BackendSlug)
	if err != nil {
		gwErr := normalizeGatewayError(err)
		if errors.Is(gwErr, ErrProviderRiskBlocked) {
			s.recordPolicyBlock(context.Background(), authCtx, gwErr)
		}
		writeProviderError(w, protocol, gwErr)
		return
	}
	summary.RoutePath = routePathFor(surface, target)
	summary.TranslationMode = TranslationModeFor(protocol, target.UpstreamProtocol)
	if target.PolicyExceptionID != "" {
		s.recordExceptionAllow(context.Background(), authCtx, target)
	}

	var policyOK bool
	target, summary, policyOK = s.applyGatewayPolicy(w, r, protocol, authCtx, target, summary)
	if !policyOK {
		return
	}

	obs := s.Recorder.Start(r.Context(), authCtx, target, summary)
	if summary.Surface == SurfaceAnthropicCountTokens && target.UpstreamProtocol != ProtocolAnthropic {
		result := writeEstimatedCountTokensResponse(w, summary)
		s.Recorder.Complete(context.Background(), obs, result)
		return
	}
	var result ProxyResult
	if target.Transport == TransportDaemonDispatch {
		result, err = s.dispatchRuntimeRequest(r.Context(), w, authCtx, target, summary)
	} else {
		result, err = s.Forwarder.Forward(r.Context(), w, r, target, summary)
	}
	if err != nil && result.StatusCode == 0 {
		gwErr := normalizeRuntimeOrUpstreamError(err)
		result.StatusCode = gwErr.StatusCode
		result.Status = StatusGatewayError
		result.ErrorType = gwErr.ErrorType
		result.ErrorMessage = gwErr.PublicMessage
		s.Recorder.Complete(context.Background(), obs, result)
		s.recordCredentialResult(context.Background(), authCtx, target, result)
		writeProviderError(w, protocol, gwErr)
		return
	}
	s.Recorder.Complete(context.Background(), obs, result)
	s.recordCredentialResult(context.Background(), authCtx, target, result)
}

func (s *Service) recordCredentialResult(ctx context.Context, authCtx AuthContext, target BackendTarget, result ProxyResult) {
	if s.Queries == nil || target.CredentialID == "" {
		return
	}
	workspaceID, err := parseUUID(authCtx.WorkspaceID)
	if err != nil {
		return
	}
	backendID, err := parseUUID(target.ID)
	if err != nil {
		return
	}
	credentialID, err := parseUUID(target.CredentialID)
	if err != nil {
		return
	}

	success := result.StatusCode >= 200 && result.StatusCode < 400
	errorMessage := strings.TrimSpace(result.ErrorMessage)
	if errorMessage == "" && !success {
		errorMessage = http.StatusText(result.StatusCode)
	}
	if len(errorMessage) > 512 {
		errorMessage = errorMessage[:512]
	}

	rateLimitedUntil, remaining, resetAt := credentialRateLimitState(result)
	_, _ = s.Queries.RecordGatewayBackendCredentialResult(ctx, db.RecordGatewayBackendCredentialResultParams{
		WorkspaceID:        workspaceID,
		BackendID:          backendID,
		ID:                 credentialID,
		Success:            success,
		LastError:          errorMessage,
		RateLimitedUntil:   rateLimitedUntil,
		RateLimitRemaining: remaining,
		RateLimitResetAt:   resetAt,
	})
}

func credentialRateLimitState(result ProxyResult) (pgtype.Timestamptz, pgtype.Int4, pgtype.Timestamptz) {
	headers := result.ResponseHeaders
	remaining := firstInt4Header(headers,
		"x-ratelimit-remaining-requests",
		"x-ratelimit-remaining-tokens",
		"x-ratelimit-remaining",
	)
	resetAt := firstResetHeader(headers,
		"x-ratelimit-reset-requests",
		"x-ratelimit-reset-tokens",
		"x-ratelimit-reset",
	)
	if result.StatusCode == http.StatusTooManyRequests {
		limitedUntil := retryAfter(headers.Get("Retry-After"))
		if !limitedUntil.Valid {
			if resetAt.Valid && resetAt.Time.After(time.Now()) {
				limitedUntil = resetAt
			} else {
				limitedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}
			}
		}
		return limitedUntil, remaining, resetAt
	}
	return pgtype.Timestamptz{}, remaining, resetAt
}

func firstInt4Header(headers http.Header, names ...string) pgtype.Int4 {
	for _, name := range names {
		value := strings.TrimSpace(headers.Get(name))
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err == nil {
			return pgtype.Int4{Int32: int32(parsed), Valid: true}
		}
	}
	return pgtype.Int4{}
}

func firstResetHeader(headers http.Header, names ...string) pgtype.Timestamptz {
	for _, name := range names {
		if parsed := parseRateLimitTime(headers.Get(name)); parsed.Valid {
			return parsed
		}
	}
	return pgtype.Timestamptz{}
}

func retryAfter(raw string) pgtype.Timestamptz {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.Timestamptz{}
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return pgtype.Timestamptz{Time: time.Now().Add(time.Duration(seconds) * time.Second), Valid: true}
	}
	if parsed, err := http.ParseTime(raw); err == nil {
		return pgtype.Timestamptz{Time: parsed, Valid: true}
	}
	return pgtype.Timestamptz{}
}

func parseRateLimitTime(raw string) pgtype.Timestamptz {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.Timestamptz{}
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		return pgtype.Timestamptz{Time: time.Now().Add(duration), Valid: true}
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if seconds > time.Now().Unix() {
			return pgtype.Timestamptz{Time: time.Unix(seconds, 0), Valid: true}
		}
		return pgtype.Timestamptz{Time: time.Now().Add(time.Duration(seconds) * time.Second), Valid: true}
	}
	if parsed, err := http.ParseTime(raw); err == nil {
		return pgtype.Timestamptz{Time: parsed, Valid: true}
	}
	return pgtype.Timestamptz{}
}

func (s *Service) applyGatewayPolicy(w http.ResponseWriter, r *http.Request, protocol string, authCtx AuthContext, target BackendTarget, summary RequestSummary) (BackendTarget, RequestSummary, bool) {
	if s.Policy == nil {
		return target, summary, true
	}
	req := gatewayPolicyRequest(authCtx, target, summary)
	evaluation, err := s.Policy.Evaluate(r.Context(), req)
	if err != nil {
		writeProviderError(w, protocol, GatewayError{
			StatusCode:    http.StatusInternalServerError,
			PublicMessage: "gateway policy evaluation failed",
			ErrorType:     "server_error",
			Code:          "gateway_policy_failed",
			Cause:         err,
		})
		return target, summary, false
	}
	_ = s.Policy.RecordDecisions(context.Background(), req, evaluation)

	switch evaluation.Decision.Action {
	case gatewaypolicy.ActionBlock, gatewaypolicy.ActionRequireApproval:
		s.recordEnforcedPolicyArtifacts(context.Background(), authCtx, req, evaluation)
		writeProviderError(w, protocol, policyDecisionError(evaluation.Decision))
		return target, summary, false
	case gatewaypolicy.ActionRedact:
		target.CapturePolicy = "redacted_content"
	case gatewaypolicy.ActionRouteToBackend:
		if evaluation.Decision.RouteBackendSlug != "" && evaluation.Decision.RouteBackendSlug != target.Slug {
			routed, err := s.Resolver.ResolveBackend(r.Context(), authCtx.WorkspaceID, protocol, evaluation.Decision.RouteBackendSlug)
			if err != nil {
				writeProviderError(w, protocol, normalizeGatewayError(err))
				return target, summary, false
			}
			target = routed
			summary.BackendSlug = target.Slug
			summary.RoutingSource = "policy"
			summary.RoutePath = routePathFor(summary.Surface, target)
			summary.TranslationMode = TranslationModeFor(protocol, target.UpstreamProtocol)
			if target.PolicyExceptionID != "" {
				s.recordExceptionAllow(context.Background(), authCtx, target)
			}
		}
	}
	return target, summary, true
}

func policyDecisionError(decision gatewaypolicy.Decision) GatewayError {
	message := decision.Message
	if strings.TrimSpace(message) == "" {
		message = "gateway request is blocked by workspace policy"
	}
	code := decision.ReasonCode
	if strings.TrimSpace(code) == "" {
		code = "gateway_policy_blocked"
	}
	status := http.StatusForbidden
	if decision.Action == gatewaypolicy.ActionRequireApproval {
		status = http.StatusForbidden
	}
	return GatewayError{
		StatusCode:    status,
		PublicMessage: message,
		ErrorType:     "invalid_request_error",
		Code:          code,
		Cause:         ErrPolicyBlocked,
		ResourceType:  "policy",
	}
}

func (s *Service) recordEnforcedPolicyArtifacts(ctx context.Context, authCtx AuthContext, req gatewaypolicy.RequestContext, evaluation gatewaypolicy.Evaluation) {
	if s.Queries == nil {
		return
	}
	workspaceID, err := parseUUID(authCtx.WorkspaceID)
	if err != nil {
		return
	}
	for _, applied := range evaluation.Applied {
		if applied.EnforcementMode != gatewaypolicy.EnforcementModeEnforce {
			continue
		}
		if applied.Decision.Action != gatewaypolicy.ActionBlock && applied.Decision.Action != gatewaypolicy.ActionRequireApproval {
			continue
		}
		matchedRules, err := json.Marshal(applied.Decision.MatchedRules)
		if err != nil {
			matchedRules = []byte("[]")
		}
		payload, err := json.Marshal(map[string]any{
			"decision":        string(applied.Decision.Action),
			"reason_code":     applied.Decision.ReasonCode,
			"policy_id":       util.UUIDToString(applied.PolicyID),
			"policy_name":     applied.PolicyName,
			"resource_type":   applied.ResourceType,
			"resource_id":     applied.ResourceID,
			"resource_label":  applied.ResourceLabel,
			"subject_user_id": authCtx.UserID,
			"provider":        req.Provider,
			"model":           req.Model,
			"matched_rules":   json.RawMessage(matchedRules),
		})
		if err != nil {
			payload = []byte("{}")
		}
		summary := policyArtifactSummary(applied)
		_, _ = s.Queries.CreateAIEvidence(ctx, db.CreateAIEvidenceParams{
			WorkspaceID:     workspaceID,
			EvidenceType:    "gateway_policy_decision",
			FrameworkRefs:   []byte(`["internal_gateway_governance"]`),
			LinkedPolicyID:  applied.PolicyID,
			LinkedBackendID: policyLinkedBackendID(applied),
			Summary:         summary,
			Payload:         payload,
			AttachmentRef:   "",
		})
		_, _ = s.Queries.CreateAIIncident(ctx, db.CreateAIIncidentParams{
			WorkspaceID:      workspaceID,
			Severity:         policyArtifactSeverity(applied.Decision.Action),
			Category:         policyArtifactCategory(applied.Decision.Action),
			LinkedPolicyID:   applied.PolicyID,
			Summary:          summary,
			Status:           "open",
			RemediationNotes: policyArtifactRemediation(applied.Decision.Action),
		})
	}
}

func policyLinkedBackendID(applied gatewaypolicy.AppliedDecision) pgtype.UUID {
	if applied.ResourceType != "provider" {
		return pgtype.UUID{}
	}
	return optionalParsedUUID(applied.ResourceID)
}

func policyArtifactSummary(applied gatewaypolicy.AppliedDecision) string {
	resource := applied.ResourceLabel
	if strings.TrimSpace(resource) == "" {
		resource = applied.ResourceID
	}
	if strings.TrimSpace(resource) == "" {
		resource = applied.ResourceType
	}
	action := "blocked"
	if applied.Decision.Action == gatewaypolicy.ActionRequireApproval {
		action = "requires approval for"
	}
	return fmt.Sprintf("Gateway %s %s %s: %s", action, applied.ResourceType, resource, applied.Decision.ReasonCode)
}

func policyArtifactSeverity(action gatewaypolicy.Action) string {
	if action == gatewaypolicy.ActionBlock {
		return "high"
	}
	return "medium"
}

func policyArtifactCategory(action gatewaypolicy.Action) string {
	if action == gatewaypolicy.ActionRequireApproval {
		return "gateway_policy_approval_required"
	}
	return "gateway_policy_block"
}

func policyArtifactRemediation(action gatewaypolicy.Action) string {
	if action == gatewaypolicy.ActionRequireApproval {
		return "Review the policy decision and approve a scoped exception if the request is valid."
	}
	return "Review the Gateway policy and request context before allowing this traffic."
}

func (s *Service) recordExceptionAllow(ctx context.Context, authCtx AuthContext, target BackendTarget) {
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
	matchedRules, err := json.Marshal([]map[string]string{{
		"id":                  "provider_risk_exception_active",
		"action":              "allow_with_exception",
		"policy_exception_id": target.PolicyExceptionID,
	}})
	if err != nil {
		matchedRules = []byte("[]")
	}
	evidenceRefs, err := json.Marshal([]map[string]string{{
		"policy_exception_id": target.PolicyExceptionID,
	}})
	if err != nil {
		evidenceRefs = []byte("[]")
	}
	_, _ = s.Queries.RecordGatewayPolicyDecision(ctx, db.RecordGatewayPolicyDecisionParams{
		WorkspaceID:        workspaceID,
		SubjectUserID:      userID,
		ResourceType:       "provider",
		ResourceID:         target.ID,
		ResourceLabel:      target.Slug,
		Decision:           "allow",
		ReasonCode:         "provider_risk_exception_active",
		MatchedRules:       matchedRules,
		ApprovalStatus:     pgtype.Text{String: "approved", Valid: true},
		EvidenceReferences: evidenceRefs,
	})
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
		Protocol:            protocol,
		Surface:             surface,
		Method:              r.Method,
		ExplicitBackendSlug: strings.TrimSpace(r.Header.Get("X-Multica-Backend")),
		BackendSlug:         strings.TrimSpace(r.Header.Get("X-Multica-Backend")),
		RoutingSource:       RoutingSourceDefault,
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
		routing, err := ParseModelRouting(model, summary.ExplicitBackendSlug)
		if err != nil {
			return RequestSummary{}, err
		}
		summary.Model = routing.ForwardedModel
		summary.RequestedModel = routing.RequestedModel
		summary.ForwardedModel = routing.ForwardedModel
		summary.BackendSlug = routing.BackendSlug
		summary.RoutingSource = routing.Source
		bodyJSON["model"] = routing.ForwardedModel
		rewritten, err := json.Marshal(bodyJSON)
		if err != nil {
			return RequestSummary{}, err
		}
		summary.Body = rewritten
	}
	if stream, ok := bodyJSON["stream"].(bool); ok {
		summary.Stream = stream
	}
	return summary, nil
}

func routePathFor(surface string, target BackendTarget) string {
	if target.UpstreamProtocol == ProtocolAnthropic {
		switch surface {
		case SurfaceModels:
			if !strings.HasSuffix(strings.TrimRight(target.BaseURL, "/"), "/v1") {
				return "/v1/models"
			}
			return "/models"
		case SurfaceAnthropicCountTokens:
			if strings.HasSuffix(strings.TrimRight(target.BaseURL, "/"), "/v1") {
				return "/messages/count_tokens"
			}
			return "/v1/messages/count_tokens"
		default:
			if strings.HasSuffix(strings.TrimRight(target.BaseURL, "/"), "/v1") {
				return "/messages"
			}
			return "/v1/messages"
		}
	}
	switch surface {
	case SurfaceOpenAIChatCompletions, SurfaceAnthropicMessages:
		return "/chat/completions"
	case SurfaceOpenAIResponses:
		return "/responses"
	case SurfaceAnthropicCountTokens:
		return "/messages/count_tokens"
	case SurfaceModels:
		if target.BackendType == "anthropic" && !strings.HasSuffix(strings.TrimRight(target.BaseURL, "/"), "/v1") {
			return "/v1/models"
		}
		return "/models"
	default:
		return rtrimSlash(surface)
	}
}

func writeEstimatedCountTokensResponse(w http.ResponseWriter, summary RequestSummary) ProxyResult {
	bodyJSON := map[string]any{
		"input_tokens": estimateInputTokens(summary.BodyJSON),
	}
	body, _ := json.Marshal(bodyJSON)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return ProxyResult{
		StatusCode:   http.StatusOK,
		Status:       StatusSuccess,
		ResponseBody: body,
		ResponseJSON: bodyJSON,
	}
}

func estimateInputTokens(value any) int64 {
	body, err := json.Marshal(value)
	if err != nil || len(body) == 0 {
		return 1
	}
	estimate := int64(len(body) / 4)
	if estimate < 1 {
		return 1
	}
	return estimate
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

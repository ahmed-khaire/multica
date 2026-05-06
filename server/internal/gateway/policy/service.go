package policy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	EnforcementModeEnforce = "enforce"
	EnforcementModeMonitor = "monitor"
)

type Service struct {
	queries *db.Queries
}

type AppliedDecision struct {
	PolicyID        pgtype.UUID
	PolicyVersion   int32
	PolicyName      string
	EnforcementMode string
	ResourceType    string
	ResourceID      string
	ResourceLabel   string
	Decision        Decision
}

type Evaluation struct {
	Decision Decision
	Applied  []AppliedDecision
}

type RequestContext struct {
	WorkspaceID string
	UserID      string
	AgentID     string
	ProviderID  string
	Provider    string
	Model       string
	Tools       []string
	DataClasses []string
}

func NewService(queries *db.Queries) *Service {
	return &Service{queries: queries}
}

func (s *Service) Evaluate(ctx context.Context, req RequestContext) (Evaluation, error) {
	if s == nil || s.queries == nil {
		return Evaluation{Decision: Decision{Action: ActionAllow}}, nil
	}
	workspaceID, err := parseUUID(req.WorkspaceID)
	if err != nil {
		return Evaluation{}, err
	}
	rows, err := s.queries.ListEnabledGatewayPolicies(ctx, workspaceID)
	if err != nil {
		return Evaluation{}, err
	}

	out := Evaluation{Decision: Decision{Action: ActionAllow}}
	for _, row := range rows {
		rules, err := DecodeRules(row.RuleDefinition)
		if err != nil {
			continue
		}
		decision := Evaluate(rules, Request{
			Provider:    req.Provider,
			Model:       req.Model,
			Tools:       req.Tools,
			DataClasses: req.DataClasses,
			UserID:      req.UserID,
			AgentID:     req.AgentID,
		})
		if decision.Action == ActionAllow {
			continue
		}
		applied := AppliedDecision{
			PolicyID:        row.ID,
			PolicyVersion:   row.Version,
			PolicyName:      row.Name,
			EnforcementMode: row.EnforcementMode,
			ResourceType:    resourceTypeForDecision(decision),
			ResourceID:      resourceIDForDecision(req, decision),
			ResourceLabel:   resourceLabelForDecision(req, decision),
			Decision:        decision,
		}
		out.Applied = append(out.Applied, applied)
		if row.EnforcementMode == EnforcementModeMonitor {
			continue
		}
		if severity(decision.Action) >= severity(out.Decision.Action) {
			out.Decision = decision
		}
	}
	return out, nil
}

func (s *Service) RecordDecisions(ctx context.Context, req RequestContext, evaluation Evaluation) error {
	if s == nil || s.queries == nil || len(evaluation.Applied) == 0 {
		return nil
	}
	workspaceID, err := parseUUID(req.WorkspaceID)
	if err != nil {
		return err
	}
	userID, err := parseUUID(req.UserID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, applied := range evaluation.Applied {
		matchedRules, err := json.Marshal(applied.Decision.MatchedRules)
		if err != nil {
			matchedRules = []byte("[]")
		}
		approvalStatus := pgtype.Text{}
		if applied.Decision.Action == ActionRequireApproval {
			approvalStatus = pgtype.Text{String: "requested", Valid: true}
		}
		_, err = s.queries.RecordGatewayPolicyDecision(ctx, db.RecordGatewayPolicyDecisionParams{
			WorkspaceID:        workspaceID,
			PolicyID:           applied.PolicyID,
			PolicyVersion:      pgtype.Int4{Int32: applied.PolicyVersion, Valid: true},
			SubjectUserID:      userID,
			ResourceType:       applied.ResourceType,
			ResourceID:         applied.ResourceID,
			ResourceLabel:      applied.ResourceLabel,
			Decision:           string(applied.Decision.Action),
			ReasonCode:         applied.Decision.ReasonCode,
			MatchedRules:       matchedRules,
			ApprovalStatus:     approvalStatus,
			EvidenceReferences: []byte("[]"),
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func DecodeRules(raw []byte) ([]Rule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var envelope struct {
		Rules []Rule `json:"rules"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Rules) > 0 {
		return envelope.Rules, nil
	}
	var single Rule
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, err
	}
	if single.ID == "" && single.Action == "" {
		return nil, nil
	}
	return []Rule{single}, nil
}

func resourceTypeForDecision(decision Decision) string {
	if len(decision.MatchedRules) == 0 {
		return "gateway"
	}
	match := decision.MatchedRules[0].Match
	switch {
	case len(match.Providers) > 0:
		return "provider"
	case len(match.Models) > 0:
		return "model"
	case len(match.Tools) > 0:
		return "tool"
	case len(match.DataClasses) > 0:
		return "data_class"
	case len(match.Users) > 0:
		return "user"
	case len(match.Agents) > 0:
		return "agent"
	default:
		return "gateway"
	}
}

func resourceIDForDecision(req RequestContext, decision Decision) string {
	if resourceTypeForDecision(decision) == "provider" {
		return req.ProviderID
	}
	return ""
}

func resourceLabelForDecision(req RequestContext, decision Decision) string {
	if len(decision.MatchedRules) == 0 {
		return "gateway"
	}
	match := decision.MatchedRules[0].Match
	switch {
	case len(match.Providers) > 0:
		return req.Provider
	case len(match.Models) > 0:
		return req.Model
	case len(match.Tools) > 0:
		return firstMatching(match.Tools, req.Tools)
	case len(match.DataClasses) > 0:
		return firstMatching(match.DataClasses, req.DataClasses)
	case len(match.Users) > 0:
		return req.UserID
	case len(match.Agents) > 0:
		return req.AgentID
	default:
		return "gateway"
	}
}

func firstMatching(allowed, values []string) string {
	for _, want := range allowed {
		for _, value := range values {
			if want == value {
				return value
			}
		}
	}
	if len(allowed) > 0 {
		return allowed[0]
	}
	return ""
}

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(strings.TrimSpace(value)); err != nil {
		return pgtype.UUID{}, err
	}
	if !id.Valid {
		return pgtype.UUID{}, errors.New("invalid uuid")
	}
	return id, nil
}

func UUIDString(id pgtype.UUID) string {
	return util.UUIDToString(id)
}

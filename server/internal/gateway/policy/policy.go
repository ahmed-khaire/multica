package policy

type Action string

const (
	ActionAllow           Action = "allow"
	ActionWarn            Action = "warn"
	ActionRequireApproval Action = "require_approval"
	ActionRedact          Action = "redact"
	ActionRouteToBackend  Action = "route_to_backend"
	ActionBlock           Action = "block"
)

type Request struct {
	Provider    string
	Model       string
	Tools       []string
	DataClasses []string
	UserID      string
	AgentID     string
}

type Match struct {
	Providers   []string `json:"providers"`
	Models      []string `json:"models"`
	Tools       []string `json:"tools"`
	DataClasses []string `json:"data_classes"`
	Users       []string `json:"users"`
	Agents      []string `json:"agents"`
}

type Rule struct {
	ID               string `json:"id"`
	Action           Action `json:"action"`
	ReasonCode       string `json:"reason_code"`
	Message          string `json:"message"`
	RouteBackendSlug string `json:"route_backend_slug"`
	Match            Match  `json:"match"`
}

type Decision struct {
	Action           Action
	ReasonCode       string
	Message          string
	RouteBackendSlug string
	MatchedRules     []Rule
}

func Evaluate(rules []Rule, req Request) Decision {
	decision := Decision{Action: ActionAllow}

	for _, rule := range rules {
		if !validAction(rule.Action) {
			continue
		}
		if !matches(rule.Match, req) {
			continue
		}

		decision.MatchedRules = append(decision.MatchedRules, rule)
		if severity(rule.Action) >= severity(decision.Action) {
			decision.Action = rule.Action
			decision.ReasonCode = rule.ReasonCode
			decision.Message = rule.Message
			decision.RouteBackendSlug = rule.RouteBackendSlug
		}
	}

	return decision
}

func validAction(action Action) bool {
	switch action {
	case ActionAllow, ActionWarn, ActionRedact, ActionRouteToBackend, ActionRequireApproval, ActionBlock:
		return true
	default:
		return false
	}
}

func matches(m Match, req Request) bool {
	return matchScalar(m.Providers, req.Provider) &&
		matchScalar(m.Models, req.Model) &&
		matchAny(m.Tools, req.Tools) &&
		matchAny(m.DataClasses, req.DataClasses) &&
		matchScalar(m.Users, req.UserID) &&
		matchScalar(m.Agents, req.AgentID)
}

func matchScalar(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if item == value {
			return true
		}
	}
	return false
}

func matchAny(allowed []string, values []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, allowedValue := range allowed {
		for _, value := range values {
			if allowedValue == value {
				return true
			}
		}
	}
	return false
}

func severity(action Action) int {
	switch action {
	case ActionAllow:
		return 0
	case ActionWarn:
		return 1
	case ActionRedact:
		return 2
	case ActionRouteToBackend:
		return 3
	case ActionRequireApproval:
		return 4
	case ActionBlock:
		return 5
	default:
		return 0
	}
}

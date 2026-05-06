package policy

import "strings"

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
		matchTools(m.Tools, req.Tools) &&
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

func matchTools(allowed []string, values []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, allowedValue := range allowed {
		normalizedAllowed := NormalizeToolName(allowedValue)
		for _, value := range values {
			if allowedValue == value || normalizedAllowed == NormalizeToolName(value) {
				return true
			}
		}
	}
	return false
}

type ToolProfile struct {
	CanonicalName string
	DisplayName   string
	RiskLevel     string
}

var toolProfiles = map[string]ToolProfile{
	"shell":           {CanonicalName: "shell", DisplayName: "Shell command", RiskLevel: "critical"},
	"file_read":       {CanonicalName: "file_read", DisplayName: "File read", RiskLevel: "medium"},
	"file_write":      {CanonicalName: "file_write", DisplayName: "File write", RiskLevel: "high"},
	"web_fetch":       {CanonicalName: "web_fetch", DisplayName: "Web fetch", RiskLevel: "medium"},
	"web_search":      {CanonicalName: "web_search", DisplayName: "Web search", RiskLevel: "medium"},
	"browser":         {CanonicalName: "browser", DisplayName: "Browser automation", RiskLevel: "high"},
	"mcp_tool":        {CanonicalName: "mcp_tool", DisplayName: "MCP tool", RiskLevel: "high"},
	"approval":        {CanonicalName: "approval", DisplayName: "Approval", RiskLevel: "low"},
	"external_action": {CanonicalName: "external_action", DisplayName: "External action", RiskLevel: "medium"},
}

var toolAliases = map[string]string{
	"bash":               "shell",
	"execute_bash":       "shell",
	"execute_command":    "shell",
	"run_command":        "shell",
	"run_shell":          "shell",
	"run_terminal_cmd":   "shell",
	"shell":              "shell",
	"terminal":           "shell",
	"terminal_command":   "shell",
	"read":               "file_read",
	"read_file":          "file_read",
	"file_read":          "file_read",
	"view":               "file_read",
	"write":              "file_write",
	"write_file":         "file_write",
	"edit":               "file_write",
	"file_write":         "file_write",
	"apply_patch":        "file_write",
	"str_replace_editor": "file_write",
	"fetch":              "web_fetch",
	"web_fetch":          "web_fetch",
	"webfetch":           "web_fetch",
	"read_url":           "web_fetch",
	"search":             "web_search",
	"web_search":         "web_search",
	"websearch":          "web_search",
	"brave_web_search":   "web_search",
	"browser":            "browser",
	"browser_click":      "browser",
	"browser_navigate":   "browser",
	"browser_screenshot": "browser",
	"playwright":         "browser",
	"mcp":                "mcp_tool",
	"mcp_tool":           "mcp_tool",
	"approval":           "approval",
	"request_approval":   "approval",
	"external_action":    "external_action",
	"external_api":       "external_action",
	"http_request":       "external_action",
}

var toolAliasFragments = []struct {
	fragment  string
	canonical string
}{
	{"execute_command", "shell"},
	{"run_terminal_cmd", "shell"},
	{"terminal_command", "shell"},
	{"execute_bash", "shell"},
	{"run_command", "shell"},
	{"run_shell", "shell"},
	{"bash", "shell"},
	{"shell", "shell"},
	{"terminal", "shell"},
	{"str_replace_editor", "file_write"},
	{"apply_patch", "file_write"},
	{"write_file", "file_write"},
	{"file_write", "file_write"},
	{"edit", "file_write"},
	{"read_file", "file_read"},
	{"file_read", "file_read"},
	{"read_url", "web_fetch"},
	{"web_fetch", "web_fetch"},
	{"webfetch", "web_fetch"},
	{"brave_web_search", "web_search"},
	{"web_search", "web_search"},
	{"websearch", "web_search"},
	{"browser", "browser"},
	{"playwright", "browser"},
	{"mcp_tool", "mcp_tool"},
	{"request_approval", "approval"},
	{"external_action", "external_action"},
	{"external_api", "external_action"},
	{"http_request", "external_action"},
}

func NormalizeToolName(name string) string {
	normalized := normalizeToolToken(name)
	if normalized == "" {
		return ""
	}
	if canonical, ok := toolAliases[normalized]; ok {
		return canonical
	}
	for _, alias := range toolAliasFragments {
		if strings.Contains(normalized, alias.fragment) {
			return alias.canonical
		}
	}
	return normalized
}

func DescribeTool(name string) ToolProfile {
	canonical := NormalizeToolName(name)
	if profile, ok := toolProfiles[canonical]; ok {
		return profile
	}
	if canonical == "" {
		return ToolProfile{}
	}
	return ToolProfile{
		CanonicalName: canonical,
		DisplayName:   canonical,
		RiskLevel:     "medium",
	}
}

func normalizeToolToken(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.NewReplacer("-", "_", ".", "_", " ", "_", "/", "_", ":", "_").Replace(name)
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	return strings.Trim(name, "_")
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

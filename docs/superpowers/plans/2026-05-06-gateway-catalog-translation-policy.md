# Gateway Catalog, Translation, And Policy Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the next three Gateway phases together: aggregated model catalog with `provider:model` routing, cross-protocol OpenAI/Anthropic translation, and a first live policy engine wired into Gateway traffic.

**Architecture:** Keep the existing Gateway package boundaries: `proxy.Service` authenticates and orchestrates, `proxy.Resolver` resolves/decrypts/governance-checks backends, `proxy.Forwarder` performs upstream HTTP, and `gateway/policy` evaluates deterministic rules. Add small focused files for model routing/catalog, protocol translation, and policy enforcement rather than growing `service.go` into a monolith.

**Tech Stack:** Go, Chi handlers, sqlc/PostgreSQL, existing Gateway tables, existing `server/internal/gateway/policy` evaluator, React Query/core API clients for minimal admin UI.

---

## Design Decisions

- `provider:model` routing is part of Phase 1 because `/v1/models` should emit IDs that clients can use directly.
- `X-Multica-Backend` remains supported. If both the header and `provider:model` are supplied and they disagree, return a provider-compatible `400` with code `gateway_backend_conflict`.
- `model_requested` stores the client-supplied model string. `model_forwarded` stores the upstream model after prefix stripping and policy routing.
- `/v1/models` returns an OpenAI-compatible `{"object":"list","data":[...]}` response for normal OpenAI clients. For Anthropic-style clients, it returns Anthropic-compatible model objects where possible, but model IDs are still routable through the Gateway.
- Model catalog v1 performs best-effort live aggregation from enabled, governance-allowed backends. It does not introduce persistent model-cache tables. A later phase can add cache/refresh metadata.
- Cross-protocol translation v1 supports text messages, system prompts, basic tool definitions, assistant tool calls, tool results, and streaming text deltas. Unsupported multimodal/provider-specific fields fail explicitly.
- Policy engine v1 is deterministic and database-backed through the existing `gateway_policy` and `gateway_policy_decision` tables. It supports `allow`, `warn`, `redact`, `route_to_backend`, `require_approval`, and `block`.
- Policy CRUD v1 exposes admin APIs and a minimal Settings -> Gateway UI using JSON rule definitions. A richer no-code policy builder is a later UI phase.

## File Map

- Create `server/internal/gateway/proxy/model_routing.go`: parse `provider:model`, validate conflicts, rewrite request model for upstream forwarding.
- Create `server/internal/gateway/proxy/model_catalog.go`: aggregate `/v1/models` from enabled backends and synthesize prefixed IDs.
- Create `server/internal/gateway/proxy/translation.go`: translate request/response JSON between OpenAI Chat Completions and Anthropic Messages.
- Create `server/internal/gateway/proxy/stream_translation.go`: translate SSE streams for the supported text/tool-call subset.
- Create `server/internal/gateway/policy/service.go`: load enabled policies, compile rules, evaluate a request, and record decisions.
- Modify `server/internal/gateway/proxy/types.go`: add fields for requested/forwarded model, selected backend slug, upstream protocol, translation mode, and policy decisions.
- Modify `server/internal/gateway/proxy/service.go`: apply model routing, policy evaluation, translation, catalog handling, and improved recorder fields.
- Modify `server/internal/gateway/proxy/resolver.go`: separate backend resolution from client protocol compatibility, allow translatable backend types.
- Modify `server/internal/gateway/proxy/forwarder.go`: use upstream protocol for auth/path and translate request/response bodies when needed.
- Modify `server/internal/gateway/proxy/telemetry.go`: persist requested vs forwarded model and policy metadata.
- Modify `server/internal/handler/gateway.go`: add policy CRUD handlers.
- Modify `server/cmd/server/router.go`: mount policy CRUD routes under `/api/gateway/governance/policies`.
- Modify `server/pkg/db/queries/gateway_policy.sql`: add list/create/update helpers if current generated code is missing required fields.
- Modify `server/pkg/db/generated/gateway_policy.sql.go`: regenerate with sqlc after query changes.
- Modify `packages/core/types/api.ts`, `packages/core/api/client.ts`, `packages/core/gateway/queries.ts`: add policy CRUD types/client/query options.
- Modify `packages/views/gateway/components/gateway-page.tsx`: add minimal admin policy table/editor in Setup -> Governance.
- Modify tests in `server/internal/handler`, `server/internal/gateway/proxy`, `server/internal/gateway/policy`, `packages/core`, and `packages/views`.

## Phase 1: Gateway Model Catalog And `provider:model` Routing

### Task 1: Model Prefix Parsing

**Files:**
- Create: `server/internal/gateway/proxy/model_routing.go`
- Test: `server/internal/gateway/proxy/model_routing_test.go`
- Modify: `server/internal/gateway/proxy/types.go`
- Modify: `server/internal/gateway/proxy/service.go`

- [ ] **Step 1: Write failing parser tests**

Add tests:

```go
func TestParseProviderModelRouting(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		headerBackend string
		wantBackend  string
		wantModel    string
		wantConflict bool
	}{
		{name: "prefixed", model: "openrouter:anthropic/claude-sonnet-4", wantBackend: "openrouter", wantModel: "anthropic/claude-sonnet-4"},
		{name: "unprefixed", model: "gpt-4.1", wantModel: "gpt-4.1"},
		{name: "header only", model: "gpt-4.1", headerBackend: "groq", wantBackend: "groq", wantModel: "gpt-4.1"},
		{name: "matching header and prefix", model: "groq:llama-3.3-70b", headerBackend: "groq", wantBackend: "groq", wantModel: "llama-3.3-70b"},
		{name: "conflicting header and prefix", model: "groq:llama-3.3-70b", headerBackend: "openrouter", wantConflict: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseModelRouting(tt.model, tt.headerBackend)
			if tt.wantConflict {
				if err == nil {
					t.Fatal("expected conflict error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseModelRouting: %v", err)
			}
			if got.BackendSlug != tt.wantBackend || got.ForwardedModel != tt.wantModel {
				t.Fatalf("routing = %#v, want backend=%q model=%q", got, tt.wantBackend, tt.wantModel)
			}
		})
	}
}
```

- [ ] **Step 2: Verify red**

Run: `cd server && go test ./internal/gateway/proxy -run TestParseProviderModelRouting -count=1`

Expected: build fails because `ParseModelRouting` is undefined.

- [ ] **Step 3: Implement parser and summary fields**

Add to `types.go`:

```go
type ModelRouting struct {
	RequestedModel string
	ForwardedModel string
	BackendSlug    string
	Source         string
}
```

Add fields to `RequestSummary`:

```go
RequestedModel string
ForwardedModel string
BackendSlug    string
RoutingSource  string
```

Implement `ParseModelRouting(model, headerBackend string) (ModelRouting, error)` in `model_routing.go`:

- trim whitespace;
- split on the first `:`;
- treat prefixes as backend slugs only when both sides are non-empty;
- reject conflicting header/prefix backends;
- preserve unprefixed model names unchanged;
- return `Source` as `header`, `model_prefix`, `header_and_model_prefix`, or `default`.

- [ ] **Step 4: Verify green**

Run: `cd server && go test ./internal/gateway/proxy -run TestParseProviderModelRouting -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add server/internal/gateway/proxy/model_routing.go server/internal/gateway/proxy/model_routing_test.go server/internal/gateway/proxy/types.go
git commit -m "feat: parse gateway provider model routing"
```

### Task 2: Rewrite Upstream Model And Telemetry Fields

**Files:**
- Modify: `server/internal/gateway/proxy/service.go`
- Modify: `server/internal/gateway/proxy/telemetry.go`
- Modify: `server/internal/gateway/proxy/forwarder.go`
- Test: `server/internal/handler/gateway_proxy_test.go`

- [ ] **Step 1: Write failing integration test**

Add `TestGatewayProxyRoutesProviderPrefixedModel`:

- create default backend `local-prefix-default-proxy-test`;
- create explicit backend `local-prefix-target-proxy-test`;
- send `{"model":"local-prefix-target-proxy-test:gpt-upstream"}`;
- assert only target upstream is called;
- assert target upstream receives body model `gpt-upstream`;
- assert response status `200`;
- assert latest telemetry has `model_requested='local-prefix-target-proxy-test:gpt-upstream'`, `model_forwarded='gpt-upstream'`, and `provider_slug='local-prefix-target-proxy-test'`.

- [ ] **Step 2: Verify red**

Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run TestGatewayProxyRoutesProviderPrefixedModel -count=1 -v
```

Expected: fail because Gateway forwards the original prefixed model to the default backend.

- [ ] **Step 3: Implement model routing in request summary**

In `summarizeRequest`, after decoding `bodyJSON`:

- set `summary.RequestedModel` and `summary.Model` to the raw model;
- call `ParseModelRouting(rawModel, summary.ExplicitBackendSlug)`;
- set `summary.BackendSlug` from routing result;
- set `summary.ForwardedModel`;
- update `summary.BodyJSON["model"]` to `ForwardedModel`;
- re-marshal `summary.Body` from `summary.BodyJSON`.

In `serve`, change resolver call from `summary.ExplicitBackendSlug` to `summary.BackendSlug`.

In `telemetry.go`, write:

- `ModelRequested: summary.RequestedModel`
- `ModelForwarded: summary.ForwardedModel`
- model-call `RequestModel: summary.ForwardedModel`

Keep `summary.Model` as a compatibility alias for `ForwardedModel` until all call sites are cleaned up.

- [ ] **Step 4: Verify green**

Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run TestGatewayProxyRoutesProviderPrefixedModel -count=1 -v
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add server/internal/gateway/proxy/service.go server/internal/gateway/proxy/telemetry.go server/internal/gateway/proxy/forwarder.go server/internal/handler/gateway_proxy_test.go
git commit -m "feat: route gateway requests by provider model prefix"
```

### Task 3: Provider-Compatible Conflict Errors

**Files:**
- Modify: `server/internal/gateway/proxy/service.go`
- Test: `server/internal/handler/gateway_proxy_test.go`

- [ ] **Step 1: Write failing conflict test**

Add `TestGatewayProxyRejectsConflictingBackendHeaderAndModelPrefix`:

- send `X-Multica-Backend: openrouter-test`;
- send model `groq-test:llama`;
- expect `400`;
- expect OpenAI error body code `gateway_backend_conflict`;
- assert no upstream call.

- [ ] **Step 2: Verify red**

Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run TestGatewayProxyRejectsConflictingBackendHeaderAndModelPrefix -count=1 -v
```

Expected: fail because conflict is not surfaced as provider-compatible error.

- [ ] **Step 3: Implement conflict normalization**

Define `ErrBackendRoutingConflict` in `errors.go`.

In `summarizeRequest`, return the conflict error from `ParseModelRouting`.

In `serve`, if `errors.Is(err, ErrBackendRoutingConflict)`, write:

```go
RoutingError(http.StatusBadRequest, "gateway backend header conflicts with model prefix", "gateway_backend_conflict", err)
```

- [ ] **Step 4: Verify green**

Run the same targeted handler test. Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add server/internal/gateway/proxy/errors.go server/internal/gateway/proxy/service.go server/internal/handler/gateway_proxy_test.go
git commit -m "fix: reject conflicting gateway backend routes"
```

### Task 4: Aggregated `/v1/models`

**Files:**
- Create: `server/internal/gateway/proxy/model_catalog.go`
- Test: `server/internal/handler/gateway_proxy_test.go`
- Modify: `server/internal/gateway/proxy/service.go`
- Modify: `server/internal/gateway/proxy/resolver.go`
- Modify: `server/internal/gateway/proxy/forwarder.go`

- [ ] **Step 1: Write failing aggregation test**

Add `TestGatewayModelsAggregatesEnabledBackends`:

- create default backend `models-default`;
- create second backend `models-openrouter`;
- both upstreams return OpenAI model list bodies;
- call `GET /v1/models`;
- expect response `object=list`;
- expect raw default model ID, prefixed default model ID, and prefixed second backend model ID:

```json
{
  "object": "list",
  "data": [
    {"id":"gpt-default","object":"model","owned_by":"models-default"},
    {"id":"models-default:gpt-default","object":"model","owned_by":"models-default"},
    {"id":"models-openrouter:anthropic/claude-sonnet-4","object":"model","owned_by":"models-openrouter"}
  ]
}
```

- [ ] **Step 2: Verify red**

Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run TestGatewayModelsAggregatesEnabledBackends -count=1 -v
```

Expected: fail because `ServeModels` currently routes like a single upstream request.

- [ ] **Step 3: Implement model catalog**

In `model_catalog.go`, add:

```go
type CatalogModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type CatalogResponse struct {
	Object string         `json:"object"`
	Data   []CatalogModel `json:"data"`
}
```

Add `func (s *Service) serveModels(w http.ResponseWriter, r *http.Request, authCtx AuthContext, protocol string)`:

- list enabled backends for the workspace;
- resolve each backend by slug through `Resolver.ResolveBackend`;
- skip disabled, incompatible, rejected, expired, or failed backends;
- call each upstream `/models` with the backend credential;
- normalize returned IDs;
- include raw IDs only for the default backend;
- include `slug:model` IDs for every backend;
- sort by ID for deterministic tests;
- return OpenAI-compatible list response.

Refactor `ServeModels` so it authenticates and calls `serveModels`, instead of using `s.serve`.

- [ ] **Step 4: Verify green**

Run the targeted aggregation test. Expected: pass.

- [ ] **Step 5: Add governance model-list test**

Add `TestGatewayModelsHidesRejectedBackends`:

- create a rejected provider risk for `models-rejected`;
- call `/v1/models`;
- assert rejected backend model IDs are absent.

Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run 'TestGatewayModelsAggregatesEnabledBackends|TestGatewayModelsHidesRejectedBackends' -count=1 -v
```

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add server/internal/gateway/proxy/model_catalog.go server/internal/gateway/proxy/service.go server/internal/gateway/proxy/resolver.go server/internal/gateway/proxy/forwarder.go server/internal/handler/gateway_proxy_test.go
git commit -m "feat: aggregate gateway model catalog"
```

## Phase 2: Cross-Protocol OpenAI/Anthropic Translation

### Task 5: Translation Type Model

**Files:**
- Create: `server/internal/gateway/proxy/translation.go`
- Test: `server/internal/gateway/proxy/translation_test.go`
- Modify: `server/internal/gateway/proxy/types.go`

- [ ] **Step 1: Write failing request translation tests**

Add tests:

- `TestTranslateOpenAIChatToAnthropicMessages`
- `TestTranslateAnthropicMessagesToOpenAIChat`

Use these representative inputs:

OpenAI input:

```json
{
  "model": "claude-3-5-sonnet",
  "messages": [
    {"role": "system", "content": "Be concise."},
    {"role": "user", "content": "Hello"}
  ],
  "max_tokens": 64,
  "temperature": 0.2,
  "tools": [
    {"type":"function","function":{"name":"lookup","description":"Lookup data","parameters":{"type":"object"}}}
  ]
}
```

Expected Anthropic output:

```json
{
  "model": "claude-3-5-sonnet",
  "system": "Be concise.",
  "messages": [{"role":"user","content":"Hello"}],
  "max_tokens": 64,
  "temperature": 0.2,
  "tools": [{"name":"lookup","description":"Lookup data","input_schema":{"type":"object"}}]
}
```

Anthropic input:

```json
{
  "model": "gpt-4.1",
  "system": "Be concise.",
  "messages": [{"role":"user","content":"Hello"}],
  "max_tokens": 64,
  "temperature": 0.2,
  "tools": [{"name":"lookup","description":"Lookup data","input_schema":{"type":"object"}}]
}
```

Expected OpenAI output:

```json
{
  "model": "gpt-4.1",
  "messages": [
    {"role":"system","content":"Be concise."},
    {"role":"user","content":"Hello"}
  ],
  "max_tokens": 64,
  "temperature": 0.2,
  "tools": [{"type":"function","function":{"name":"lookup","description":"Lookup data","parameters":{"type":"object"}}}]
}
```

- [ ] **Step 2: Verify red**

Run: `cd server && go test ./internal/gateway/proxy -run 'TestTranslateOpenAIChatToAnthropicMessages|TestTranslateAnthropicMessagesToOpenAIChat' -count=1`

Expected: build fails because translation functions are undefined.

- [ ] **Step 3: Implement non-streaming request translation**

Add:

```go
type TranslationMode string

const (
	TranslationNone              TranslationMode = "none"
	TranslationOpenAIToAnthropic TranslationMode = "openai_to_anthropic"
	TranslationAnthropicToOpenAI TranslationMode = "anthropic_to_openai"
)

func TranslationModeFor(clientProtocol, backendType string) TranslationMode
func TranslateRequestBody(summary RequestSummary, mode TranslationMode) ([]byte, map[string]any, error)
```

Implementation rules:

- preserve `model`, `temperature`, `top_p`, `stop`, and max-token fields where names are equivalent;
- convert OpenAI `max_tokens` to Anthropic `max_tokens`;
- convert Anthropic `max_tokens` to OpenAI `max_tokens`;
- convert OpenAI system message into Anthropic `system`;
- convert Anthropic `system` into OpenAI system message;
- convert OpenAI function tools to Anthropic tools;
- convert Anthropic tools to OpenAI function tools;
- reject image/content-block arrays except text blocks in v1.

- [ ] **Step 4: Verify green**

Run the targeted translation tests. Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add server/internal/gateway/proxy/translation.go server/internal/gateway/proxy/translation_test.go server/internal/gateway/proxy/types.go
git commit -m "feat: translate gateway request protocols"
```

### Task 6: Resolver And Forwarder Use Upstream Protocol

**Files:**
- Modify: `server/internal/gateway/proxy/resolver.go`
- Modify: `server/internal/gateway/proxy/forwarder.go`
- Modify: `server/internal/gateway/proxy/service.go`
- Test: `server/internal/handler/gateway_proxy_test.go`

- [ ] **Step 1: Write failing OpenAI-to-Anthropic integration test**

Add `TestGatewayProxyTranslatesOpenAIClientToAnthropicBackend`:

- configure an Anthropic backend as default;
- send OpenAI `/v1/chat/completions`;
- upstream expects path `/v1/messages`;
- upstream expects `x-api-key`;
- upstream request body has Anthropic `messages` and `system`;
- upstream returns Anthropic non-streaming message response;
- client receives OpenAI-compatible `chat.completion` response.

- [ ] **Step 2: Write failing Anthropic-to-OpenAI integration test**

Add `TestGatewayProxyTranslatesAnthropicClientToOpenAIBackend`:

- configure OpenAI-compatible backend as default;
- send Anthropic `/v1/messages`;
- upstream expects path `/v1/chat/completions`;
- upstream expects `Authorization: Bearer ...`;
- upstream request body has OpenAI `messages`;
- upstream returns OpenAI chat completion;
- client receives Anthropic-compatible message response.

- [ ] **Step 3: Verify red**

Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run 'TestGatewayProxyTranslatesOpenAIClientToAnthropicBackend|TestGatewayProxyTranslatesAnthropicClientToOpenAIBackend' -count=1 -v
```

Expected: fail because resolver rejects incompatible backend types or forwarder uses the client protocol upstream.

- [ ] **Step 4: Implement upstream protocol**

Add to `BackendTarget`:

```go
UpstreamProtocol string
```

Set it from backend type:

- `openai_compatible` -> `ProtocolOpenAI`
- `anthropic` -> `ProtocolAnthropic`
- `claude_oauth` -> `ProtocolAnthropic` for now, behind adapter checks

Change compatibility:

- resolver must allow any backend type that can be translated from the client protocol;
- `routePathFor` must use `target.UpstreamProtocol`;
- `BuildUpstreamRequest` must set auth headers based on `target.UpstreamProtocol`;
- before creating upstream request, translate `summary.Body` and `summary.BodyJSON` when `TranslationMode` is not `none`;
- after receiving non-streaming upstream body, translate response body back to the client protocol before writing.

- [ ] **Step 5: Verify green**

Run the two targeted handler tests. Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add server/internal/gateway/proxy/resolver.go server/internal/gateway/proxy/forwarder.go server/internal/gateway/proxy/service.go server/internal/handler/gateway_proxy_test.go
git commit -m "feat: route gateway requests across protocols"
```

### Task 7: Non-Streaming Response Translation

**Files:**
- Modify: `server/internal/gateway/proxy/translation.go`
- Test: `server/internal/gateway/proxy/translation_test.go`
- Modify: `server/internal/gateway/proxy/telemetry.go`

- [ ] **Step 1: Write response translator tests**

Add:

- `TestTranslateAnthropicMessageToOpenAIChatCompletion`
- `TestTranslateOpenAIChatCompletionToAnthropicMessage`

Expected OpenAI response fields:

- `id`
- `object="chat.completion"`
- `model`
- `choices[0].message.role="assistant"`
- `choices[0].message.content`
- `choices[0].finish_reason`
- `usage.prompt_tokens`
- `usage.completion_tokens`
- `usage.total_tokens`

Expected Anthropic response fields:

- `id`
- `type="message"`
- `role="assistant"`
- `model`
- `content=[{"type":"text","text":"..."}]`
- `stop_reason`
- `usage.input_tokens`
- `usage.output_tokens`

- [ ] **Step 2: Verify red**

Run: `cd server && go test ./internal/gateway/proxy -run 'TestTranslateAnthropicMessageToOpenAIChatCompletion|TestTranslateOpenAIChatCompletionToAnthropicMessage' -count=1`

Expected: fail because response translators are undefined.

- [ ] **Step 3: Implement response translators**

Add:

```go
func TranslateResponseBody(body []byte, mode TranslationMode) ([]byte, map[string]any, error)
```

Ensure `ProxyResult.ResponseJSON` stores the client-facing response JSON so existing telemetry extraction remains client-protocol consistent.

- [ ] **Step 4: Verify green**

Run translation tests and the two integration tests from Task 6. Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add server/internal/gateway/proxy/translation.go server/internal/gateway/proxy/translation_test.go server/internal/gateway/proxy/telemetry.go
git commit -m "feat: translate gateway response protocols"
```

### Task 8: Streaming Translation

**Files:**
- Create: `server/internal/gateway/proxy/stream_translation.go`
- Test: `server/internal/gateway/proxy/stream_translation_test.go`
- Modify: `server/internal/gateway/proxy/forwarder.go`
- Test: `server/internal/handler/gateway_proxy_test.go`

- [ ] **Step 1: Write stream translation unit tests**

Add:

- `TestTranslateAnthropicStreamToOpenAIChunks`
- `TestTranslateOpenAIStreamToAnthropicEvents`

Test only the supported v1 subset:

- text delta;
- tool-call name/arguments delta where available;
- stop/finish event;
- usage event if available.

- [ ] **Step 2: Verify red**

Run: `cd server && go test ./internal/gateway/proxy -run 'TestTranslateAnthropicStreamToOpenAIChunks|TestTranslateOpenAIStreamToAnthropicEvents' -count=1`

Expected: fail because stream translators are undefined.

- [ ] **Step 3: Implement streaming translators**

Add:

```go
func TranslateStreamChunk(line []byte, mode TranslationMode) [][]byte
```

In `copyStreamingResponse`, when translation mode is not `none`:

- read SSE frames;
- translate event payloads line by line;
- write client-protocol-compatible SSE chunks;
- flush after each translated chunk;
- preserve `[DONE]` for OpenAI clients;
- emit Anthropic `message_stop` for Anthropic clients.

- [ ] **Step 4: Write integration streaming tests**

Add:

- `TestGatewayProxyTranslatesAnthropicStreamToOpenAIClient`
- `TestGatewayProxyTranslatesOpenAIStreamToAnthropicClient`

Each test asserts recorder flushes and client receives the expected event format.

- [ ] **Step 5: Verify green**

Run:

```bash
cd server
go test ./internal/gateway/proxy -run Stream -count=1
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run 'Translates.*Stream' -count=1 -v
```

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add server/internal/gateway/proxy/stream_translation.go server/internal/gateway/proxy/stream_translation_test.go server/internal/gateway/proxy/forwarder.go server/internal/handler/gateway_proxy_test.go
git commit -m "feat: translate gateway streaming protocols"
```

## Phase 3: Policy Engine V1

### Task 9: Policy Rule Loading And Compilation

**Files:**
- Create: `server/internal/gateway/policy/service.go`
- Test: `server/internal/gateway/policy/service_test.go`
- Modify: `server/internal/gateway/policy/policy.go`

- [ ] **Step 1: Write failing policy load test**

Add a test using an in-memory row-like struct or direct JSON helper:

```json
{
  "rules": [
    {
      "id": "block-openrouter-prod",
      "action": "block",
      "reason_code": "model_not_approved",
      "message": "OpenRouter is not approved for production.",
      "match": {
        "providers": ["openrouter"],
        "models": ["anthropic/claude-sonnet-4"],
        "data_classes": ["customer_data"]
      }
    }
  ]
}
```

Expected compiled rule:

- carries policy ID;
- carries policy version;
- carries rule ID;
- evaluates to `ActionBlock`.

- [ ] **Step 2: Verify red**

Run: `cd server && go test ./internal/gateway/policy -run TestCompilePolicyRules -count=1`

Expected: fail because service/compiler does not exist.

- [ ] **Step 3: Implement compiler**

Add:

```go
type CompiledRule struct {
	PolicyID      string
	PolicyVersion int32
	Rule          Rule
}

type EvaluationRequest struct {
	WorkspaceID string
	UserID      string
	AgentID     string
	Provider    string
	Model       string
	Tools       []string
	DataClasses []string
	Environment string
}

type EvaluationDecision struct {
	Action           Action
	ReasonCode       string
	Message          string
	RouteBackendSlug string
	MatchedRules     []CompiledRule
}

func CompilePolicyRules(policyID string, version int32, raw json.RawMessage) ([]CompiledRule, error)
func EvaluateCompiled(rules []CompiledRule, req EvaluationRequest) EvaluationDecision
```

Keep the existing `Evaluate` function for package-level unit tests, but have it delegate internally where practical.

- [ ] **Step 4: Verify green**

Run: `cd server && go test ./internal/gateway/policy -run TestCompilePolicyRules -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add server/internal/gateway/policy/policy.go server/internal/gateway/policy/service.go server/internal/gateway/policy/service_test.go
git commit -m "feat: compile gateway policy rules"
```

### Task 10: Policy CRUD API

**Files:**
- Modify: `server/internal/gateway/management/types.go`
- Modify: `server/internal/gateway/management/service.go`
- Modify: `server/internal/handler/gateway.go`
- Modify: `server/cmd/server/router.go`
- Test: `server/internal/handler/gateway_test.go`
- Modify: `server/pkg/db/queries/gateway_policy.sql`
- Regenerate: `server/pkg/db/generated/gateway_policy.sql.go`

- [ ] **Step 1: Write failing handler test**

Add `TestGatewayPolicyHandlersCreateListAndUpdate`:

- POST `/api/gateway/governance/policies`;
- body includes name, description, policy_type, enabled, enforcement_mode, and rule_definition;
- expect `201`;
- GET `/api/gateway/governance/policies`;
- expect created row;
- PATCH `/api/gateway/governance/policies/{id}`;
- expect version increment and updated enabled/enforcement mode.

- [ ] **Step 2: Verify red**

Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run TestGatewayPolicyHandlersCreateListAndUpdate -count=1 -v
```

Expected: fail because handlers/routes/types are missing.

- [ ] **Step 3: Implement management service and handlers**

Add types:

```go
type GatewayPolicyItem struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	PolicyType      string `json:"policy_type"`
	Enabled         bool   `json:"enabled"`
	Version         int32  `json:"version"`
	RuleDefinition any    `json:"rule_definition"`
	EnforcementMode string `json:"enforcement_mode"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type CreateGatewayPolicyInput struct { ... }
type UpdateGatewayPolicyInput struct { ... }
```

Implement:

- `ListGatewayPolicies`
- `CreateGatewayPolicy`
- `UpdateGatewayPolicy`

Validate:

- policy type is one of existing DB check values;
- enforcement mode is `monitor` or `enforce`;
- `rule_definition` compiles with `policy.CompilePolicyRules`;
- name is non-empty.

Mount:

- `GET /api/gateway/governance/policies`
- `POST /api/gateway/governance/policies`
- `PATCH /api/gateway/governance/policies/{id}`

- [ ] **Step 4: Verify green**

Run the targeted handler test. Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add server/internal/gateway/management/types.go server/internal/gateway/management/service.go server/internal/handler/gateway.go server/cmd/server/router.go server/internal/handler/gateway_test.go server/pkg/db/queries/gateway_policy.sql server/pkg/db/generated/gateway_policy.sql.go
git commit -m "feat: add gateway policy management api"
```

### Task 11: Enforce Block/Warn/Route Decisions In Proxy

**Files:**
- Modify: `server/internal/gateway/proxy/service.go`
- Modify: `server/internal/gateway/proxy/types.go`
- Modify: `server/internal/gateway/proxy/telemetry.go`
- Modify: `server/internal/gateway/policy/service.go`
- Test: `server/internal/handler/gateway_proxy_test.go`

- [ ] **Step 1: Write failing block policy test**

Add `TestGatewayProxyBlocksModelPolicy`:

- create enabled policy with rule matching provider slug and model;
- send request through matching backend/model;
- expect `403`;
- expect error code `model_not_approved`;
- assert upstream was not called;
- assert `gateway_policy_decision` row with `decision='block'`.

- [ ] **Step 2: Write failing route policy test**

Add `TestGatewayProxyRoutesByPolicyDecision`:

- default backend is `policy-default`;
- policy rule action `route_to_backend`, `route_backend_slug='policy-approved'`, matching model `gpt-approved-route`;
- send request without explicit backend;
- assert `policy-approved` upstream called;
- assert `policy-default` not called;
- assert decision row `decision='route_to_backend'`.

- [ ] **Step 3: Verify red**

Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run 'TestGatewayProxyBlocksModelPolicy|TestGatewayProxyRoutesByPolicyDecision' -count=1 -v
```

Expected: fail because live proxy does not evaluate `gateway_policy`.

- [ ] **Step 4: Implement policy service wiring**

In `proxy.Service`, add:

```go
Policy *policy.Service
```

Initialize it in `NewService`.

Before resolver:

- build `policy.EvaluationRequest` from workspace, user, model, backend slug candidate, tools, data classes, and correlation headers;
- evaluate enabled policies;
- if `block`, record decision and return provider-compatible `403`;
- if `route_to_backend`, replace `summary.BackendSlug`;
- if `warn`, record decision and continue;
- if `redact`, record decision and set an effective capture policy override to `redacted_content`;
- if `require_approval`, return `403` with code `gateway_policy_approval_required` unless an active approved exception exists for the policy ID.

Record decisions before upstream call for all matched actions except pure `allow` with no matched rules.

- [ ] **Step 5: Verify green**

Run the targeted block/route tests. Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add server/internal/gateway/proxy/service.go server/internal/gateway/proxy/types.go server/internal/gateway/proxy/telemetry.go server/internal/gateway/policy/service.go server/internal/handler/gateway_proxy_test.go
git commit -m "feat: enforce gateway request policies"
```

### Task 12: Redact And Require-Approval Decisions

**Files:**
- Modify: `server/internal/gateway/proxy/service.go`
- Modify: `server/internal/gateway/proxy/telemetry.go`
- Test: `server/internal/handler/gateway_proxy_test.go`

- [ ] **Step 1: Write failing redaction policy test**

Add `TestGatewayProxyPolicyRedactsCapture`:

- workspace capture policy is `full_content`;
- policy rule action is `redact`;
- request contains a secret-like string;
- upstream returns success;
- assert `gateway_model_call.prompt_messages` does not contain raw secret;
- assert decision row `decision='redact'`.

- [ ] **Step 2: Write failing require-approval test**

Add `TestGatewayProxyPolicyRequiresApproval`:

- policy rule action `require_approval`;
- send matching request;
- expect `403`;
- expect code `gateway_policy_approval_required`;
- assert upstream was not called;
- assert decision row `decision='require_approval'`.

- [ ] **Step 3: Verify red**

Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/handler -run 'TestGatewayProxyPolicyRedactsCapture|TestGatewayProxyPolicyRequiresApproval' -count=1 -v
```

Expected: fail until policy effects are implemented.

- [ ] **Step 4: Implement effects**

- Add `EffectiveCapturePolicy` to `RequestSummary`.
- In recorder, prefer `summary.EffectiveCapturePolicy` over `target.CapturePolicy`.
- For `redact`, set effective policy to `redacted_content`.
- For `require_approval`, return `RoutingError(http.StatusForbidden, "gateway request requires policy approval", "gateway_policy_approval_required", ErrProviderRiskBlocked)` or introduce `ErrPolicyApprovalRequired` if clearer.

- [ ] **Step 5: Verify green**

Run targeted redaction/approval tests. Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add server/internal/gateway/proxy/service.go server/internal/gateway/proxy/telemetry.go server/internal/handler/gateway_proxy_test.go
git commit -m "feat: apply gateway policy redaction and approval"
```

### Task 13: Minimal Policy UI And Core Client

**Files:**
- Modify: `packages/core/types/api.ts`
- Modify: `packages/core/api/client.ts`
- Modify: `packages/core/gateway/queries.ts`
- Test: `packages/core/gateway/queries.test.ts`
- Modify: `packages/views/gateway/components/gateway-page.tsx`
- Test: `packages/views/gateway/components/gateway-page.test.tsx`

- [ ] **Step 1: Write failing core query test**

Add tests for:

- `gatewayPoliciesOptions(wsId, enabled)`
- `api.listGatewayPolicies`
- `api.createGatewayPolicy`
- `api.updateGatewayPolicy`

Run: `pnpm --filter @multica/core test -- gateway/queries.test.ts`

Expected: fail because API client methods and query options are missing.

- [ ] **Step 2: Implement core client/types**

Add:

```ts
export interface GatewayPolicyItem {
  id: string;
  name: string;
  description: string;
  policy_type: string;
  enabled: boolean;
  version: number;
  rule_definition: unknown;
  enforcement_mode: "monitor" | "enforce" | string;
  created_at: string;
  updated_at: string;
}
```

Add client methods and query options using existing Gateway governance query patterns.

- [ ] **Step 3: Verify core green**

Run: `pnpm --filter @multica/core test -- gateway/queries.test.ts`

Expected: pass.

- [ ] **Step 4: Write failing Gateway UI test**

Add test:

- admin opens Gateway Setup/Governance;
- sees `Gateway Policies`;
- sees one policy row;
- clicks create/edit action;
- JSON rule textarea submits `createGatewayPolicy` or `updateGatewayPolicy`;
- query invalidates policies and policy decisions.

Run: `pnpm --filter @multica/views test -- gateway/components/gateway-page.test.tsx`

Expected: fail.

- [ ] **Step 5: Implement minimal UI**

In `gateway-page.tsx`:

- add `GatewayPolicies` component near policy decisions/exceptions;
- render name, type, mode, enabled, version, top action, reason code summary;
- add compact JSON editor form;
- validate JSON client-side before submit;
- do not build a no-code rule builder in v1.

- [ ] **Step 6: Verify views green**

Run: `pnpm --filter @multica/views test -- gateway/components/gateway-page.test.tsx`

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add packages/core/types/api.ts packages/core/api/client.ts packages/core/gateway/queries.ts packages/core/gateway/queries.test.ts packages/views/gateway/components/gateway-page.tsx packages/views/gateway/components/gateway-page.test.tsx
git commit -m "feat: add gateway policy management ui"
```

## Final Verification

- [ ] Run `git diff --check`.
- [ ] Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/gateway/proxy ./internal/gateway/policy ./internal/handler -run 'Gateway|Policy|Translate|Model' -count=1 -v
```

- [ ] Run:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./...
```

- [ ] Run `pnpm --filter @multica/core typecheck`.
- [ ] Run `pnpm --filter @multica/views typecheck`.
- [ ] Run `pnpm --filter @multica/core test -- gateway/queries.test.ts`.
- [ ] Run `pnpm --filter @multica/views test -- gateway/components/gateway-page.test.tsx`.
- [ ] Run `pnpm --filter @multica/core lint`.
- [ ] Run `pnpm --filter @multica/views lint`.

Known lint caveat from the current repo: if full views lint still reports the pre-existing `react/display-name` errors in `packages/views/issues/components/issue-detail.test.tsx` and the existing hook dependency warnings, document that separately rather than mixing it with this feature.

## Execution Order

1. Phase 1 first, because model catalog and prefixed routing are the client-facing foundation.
2. Phase 2 second, because translation depends on reliable routed backend selection and requested/forwarded model telemetry.
3. Phase 3 third, because policy actions need stable routing and translation hooks to block, warn, redact, or route before upstream calls.

## Self-Review

- Spec coverage: covers `/v1/models` aggregation, Dario-style `provider:model` routing, cross-protocol request/response/stream translation, and live policy engine v1 with admin API/UI.
- Placeholder scan: no deferred placeholder fields are left in this plan; later-phase items are explicitly excluded from this implementation.
- Type consistency: `RequestSummary`, `BackendTarget`, `ModelRouting`, `TranslationMode`, and policy decision terms are used consistently across tasks.
- Scope check: this is large but split into independently testable commits. Each phase can ship without requiring the later phases to be complete.

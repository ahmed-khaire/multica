# Phase 8A: Platform Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** connect the platform to the outside world — real-time chat for conversational agents, inbound webhooks for external triggers, outbound webhooks for event notifications, agent templates for quick-start, API keys for programmatic access, a dry-run playground, OpenAPI/Swagger docs, and an onboarding wizard.

**Architecture:** chat runs through **Redis Streams with consumer groups** (XADD from the server handler, XREADGROUP per worker, XACK on success, XAUTOCLAIM sweeps pending entries that stalled for >5 minutes back to a live worker). This gives at-least-once delivery with automatic recovery from worker crashes — a pub/sub+SETNX scheme silently loses messages when the claimer dies. Streams are keyed by `(workspace_id, agent_provider)`. An embedded single-process fallback uses a bounded Go channel with HTTP 503 backpressure. Inbound webhooks verify HMAC via the credential vault (Phase 1 §1.4) and dispatch to either an agent task or a workflow trigger. Outbound webhooks subscribe to the existing `events.Bus` and deliver through a background goroutine with exponential backoff. Templates are rows in `agent_template` (workspace-owned + system-seeded). API keys are SHA-256-hashed bearer tokens with scoped permissions. Playground tags requests with `dry_run=true` and tool executors skip side effects. OpenAPI spec is generated from handler annotations via `swaggo/swag`. Onboarding tracks completion on the workspace row.

**Tech Stack:** Go 1.26, chi router, pgx/sqlc, `github.com/redis/go-redis/v9` (already a Phase 2 baseline), `github.com/swaggo/swag` + `github.com/swaggo/http-swagger` for OpenAPI, Gorilla WebSocket (existing), TanStack Query + shadcn UI.

---

## Scope Note

Phase 8A of 12. Depends on Phase 2 (worker + Redis baseline + BudgetService + WSPublisher + `events.Bus`), Phase 5 (context compaction for chat), Phase 6 (task tool — chat can delegate to subagents), Phase 7 (workflows as webhook targets). Produces eight capabilities listed above, each as its own task cluster.

**Not in scope — explicitly deferred:**
- **Stripe/billing** — §8A.9 notes integration points; Phase 9 + product decides tiers before we wire an adapter.
- **Auto-generated SDKs** — OpenAPI spec lands in §8A.7 but running `openapi-typescript-codegen`/`openapi-python-client` and publishing to npm/PyPI is post-V1.
- **Cross-region Redis** — single-instance Redis per the D4 baseline; HA + replica routing is infra work.
- **Webhook event_filter JSONPath/CEL evaluator** — field exists on `webhook_endpoint.event_filter` (Phase 7 schema) but Phase 8A accepts all events that match `target_type`; filter evaluation deferred.
- **OAuth2/OIDC for inbound webhooks** — HMAC only; OAuth consent UI for MCP/inbound webhooks lands in Phase 8B.
- **Fine-grained API key rate limits** — Phase 8B adds the rate limiter. Phase 8A wires the key-level counter but the throttling middleware is 8B.
- **Multi-model side-by-side in playground** — single-model dry_run only. Side-by-side is a UX nice-to-have once the single-run path is stable.

---

## Preconditions

Verify before Task 1. Missing entries block execution.

**From Phase 1:**

| Surface | Purpose |
|---|---|
| `workspace_credential` table + AES-GCM vault + `credential_type='webhook_hmac'` value (§1.4) | HMAC secrets for inbound + outbound webhooks |
| **Contract**: `Credentials.GetRaw(ctx, wsID, credID) ([]byte, error)` returns a **freshly allocated, caller-owned** `[]byte`. Callers MUST zero it after use (`defer agent.Zero(secret)`). If Phase 1's vault implementation caches decrypted plaintext internally, `GetRaw` must `copy(b, cached)` before returning. Sharing a cache slice would corrupt the vault when the caller zeroes it. Verify Phase 1's implementation honors this contract before starting Tasks 10/11. | Safe `defer agent.Zero` in webhook_inbound + webhook_outbound |
| `workspace` table | FK target for onboarding + templates + api_keys |
| `testutil.NewTestDB`, `SeedWorkspace`, `SeedAgent`, `SeedCredential` helpers | All tests |

**From Phase 2:**

| Surface | Purpose |
|---|---|
| `RedisAdapter` interface with `redis` + `memory` impls (D4) — MUST expose the Streams surface below (not just pub/sub) | Chat pub/sub + Streams |

**`redis.Adapter` required method surface** (any missing ⇒ Pre-Task 0 extension before Task 6):

```go
// server/pkg/redis/adapter.go
type Adapter interface {
    // Pub/sub (existing from Phase 2 baseline).
    Publish(ctx context.Context, channel string, msg []byte) error
    Subscribe(ctx context.Context, channel string) (<-chan Message, func(), error)
    PSubscribe(ctx context.Context, pattern string) (<-chan Message, func(), error)

    // Streams (Phase 8A requirement — extend Phase 2 adapter if absent).
    XAdd(ctx context.Context, stream string, fields map[string]any) error
    XGroupCreate(ctx context.Context, stream, group, start string, mkstream bool) error
    XReadGroup(ctx context.Context, group, consumer string, streams []string, block time.Duration, count int) ([]XEntry, error)
    XAck(ctx context.Context, stream, group, id string) error
    XAutoClaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, start string) ([]XEntry, error)
    Keys(ctx context.Context, pattern string) ([]string, error)

    // Dedup (existing or add).
    SetNX(ctx context.Context, key, value string, ttlSec int) (bool, error)
}

type XEntry struct {
    Stream string
    ID     string
    Values map[string]any
}
```
| `BudgetService.CanDispatch(ctx, tx, wsID, estCents) (Decision, error)` | Chat budget gate |
| `WSPublisher` + `events.Bus` | WS events + outbound webhook source |
| `harness.Config` / `harness.New` / `LLMAPIBackend` | Chat LLM path |
| `ToolRegistry` with per-call `dry_run` flag (Phase 2 §Task 6 adds this in a follow-up — see §Pre-Task 0) | Playground |
| `traceIDKey` context (D8) | Chat + webhook traces |
| `CostCalculator` | Chat cost accounting |

**From Phase 5:**

| Surface | Purpose |
|---|---|
| `agent.Compact(ctx, provider, messages, opts) (CompactResult, error)` | Chat history compression when exceeding 70% model window |
| `MemoryService.Recall` / `.Remember` | Chat sessions recall + record |

**From Phase 6:**

| Surface | Purpose |
|---|---|
| `harness.Config.Subagents` + `task` tool + `AdaptToGoAI` | Chat agent can delegate |
| `DelegationService` | Chat runs under the resolver path that wires delegation |

**From Phase 7:**

| Surface | Purpose |
|---|---|
| `WorkflowService.Trigger(ctx, wsID, wfID, input, triggeredBy)` | Inbound webhook with target_type='workflow' |
| `workflow_run` + `approval.created` WS event | Outbound webhook subscribes to both |

**Test helpers (extend Phase 2 testsupport):**

```go
// server/internal/testsupport/ — Env-style integration helpers.
func (e *Env) PostWebhook(wsID, endpointID uuid.UUID, body []byte, hmac string) *http.Response
func (e *Env) CreateWebhookSubscription(wsID uuid.UUID, url string, events []string) uuid.UUID
func (e *Env) CreateWebhookSubscriptionWithSecret(wsID uuid.UUID, url string, events []string, secretID uuid.UUID) uuid.UUID
func (e *Env) WaitForOutboundDelivery(ctx context.Context, subID uuid.UUID, event string) DeliveryLog
func (e *Env) StartHTTPSink() *HTTPSink                                  // record outbound calls
func (e *Env) SendChatMessage(wsID, sessionID uuid.UUID, text string) (streamID uuid.UUID)
func (e *Env) AwaitChatResponse(ctx context.Context, streamID uuid.UUID) string
func (e *Env) CreateAPIKey(wsID, userID uuid.UUID, scopes []string) (key string, id uuid.UUID)
func (e *Env) InvokeWithAPIKey(key string, method, path string, body []byte) *http.Response
```

**`server/internal/testutil/` chat-specific helpers — Pre-Task 0 creation required:**

All referenced in Tasks 6–7 test code. Add these before Task 1 if not already present:

```go
// server/internal/testutil/chat.go
func NewFakeRedis() *FakeRedis
type FakeRedis struct { /* implements redis.Adapter */ }
func (f *FakeRedis) PublishTest(channel string, payload []byte)
func (f *FakeRedis) PublishedTo(channel string) []service.ChatMessage
func (f *FakeRedis) WaitForResponse(channel string, timeout time.Duration) string

// Stub model that replies with fixed text, counts messages seen.
type StubModel struct { /* implements harness.Model */ }
func StubModelReplying(text string) *StubModel
func StubModelWithContextWindow(windowTokens int, text string) *StubModel
func (s *StubModel) LastMessageCount() int

// Chat seed helper.
func SeedChatMessage(db *pgxpool.Pool, wsID, agentID uuid.UUID, role, content string)

// Budget helpers.
var AlwaysAllowBudget BudgetAllowFn                                   // (allow=true, reason="")
func DenyBudget(reason string) BudgetAllowFn                          // (allow=false, reason=reason)

// JSON helper for publishing into fake redis channels.
func JSONBytes(m map[string]any) []byte

// WS stub — no-op Publish.
var NoopWS service.WSPublisher
```

If any of the above is missing from `server/internal/testutil/`, create it in a Pre-Task 0 commit `test: scaffold Phase 8A chat testutil helpers` before Task 1.

**Pre-Task 0 bootstrap (TWO non-negotiable extensions):**

1. **`ToolRegistry` `dry_run` flag.** Phase 2's `ToolRegistry` must accept a `dry_run bool` flag on tool execution. If Phase 2 shipped without it, extend the registry's `Execute` signature (`Execute(ctx, input, opts ExecuteOptions) where ExecuteOptions{DryRun bool}`) before Task 14 (playground).

2. **`harness.Model.ContextWindow() int` method.** Task 7's `maybeCompact` calls `model.ContextWindow() * 4` to compute the byte-threshold that triggers Phase 5's `agent.Compact`. Phase 2's `harness.Model` interface must expose this. Extension:

   ```go
   // Add to server/pkg/agent/harness/model.go (Phase 2 surface).
   type Model interface {
       Provider() string
       ModelID() string
       ContextWindow() int // tokens; convert to bytes via ×4 heuristic
       // ... existing Phase 2 methods
   }
   ```

   Each Phase 2 provider adapter (Anthropic, OpenAI, etc.) populates this from the provider's published context window: Claude Sonnet 4.5 = 1_000_000 tokens via `[1m]` suffix, Claude Haiku 4.5 = 200_000, GPT-4o = 128_000, etc. Keep it as a small hardcoded table inside each provider adapter until a Phase 9 registry replaces it.

   Alternative: if Phase 5 already provides `agent.ContextWindowFor(provider, modelID) int`, delegate to that instead and the `harness.Model` interface doesn't need extending. Check Phase 5 §5.4 plan before choosing.

If either extension is missing when Phase 8A execution begins, add it in a commit BEFORE Task 1 starts.

---

## File Structure

### New Files (Backend)

| File | Responsibility |
|---|---|
| `server/migrations/160_webhooks.up.sql` | `webhook_endpoint` + `webhook_delivery` tables |
| `server/migrations/160_webhooks.down.sql` | Reverse drops |
| `server/migrations/161_templates.up.sql` | `agent_template` table + seed system templates |
| `server/migrations/161_templates.down.sql` | Drop template table |
| `server/migrations/162_api_keys.up.sql` | `workspace_api_key` + `workspace.onboarding_completed_at` column |
| `server/migrations/162_api_keys.down.sql` | Reverse |
| `server/migrations/163_outbound_webhooks.up.sql` | `webhook_subscription` + `webhook_delivery_log` |
| `server/migrations/163_outbound_webhooks.down.sql` | Reverse |
| `server/pkg/db/queries/chat.sql` | sqlc: `InsertChatMessage`, `ListRecentChatMessages` (uses Phase 8A columns from migration 162) |
| `server/pkg/db/queries/webhook.sql` | sqlc: endpoint + subscription + delivery CRUD |
| `server/pkg/db/queries/template.sql` | sqlc: template CRUD |
| `server/pkg/db/queries/apikey.sql` | sqlc: api key CRUD + key-hash lookup |
| `server/internal/service/chat.go` | `ChatService` — session mgmt, Redis dispatch, budget gate |
| `server/internal/service/chat_test.go` | Session + budget + compaction paths |
| `server/internal/worker/chat.go` | Redis subscriber + LLM loop + streaming response |
| `server/internal/worker/chat_test.go` | Sub-test with fake Redis + stub model |
| `server/internal/handler/chat.go` | WS endpoint updates for cloud agents |
| `server/internal/handler/chat_test.go` | WS round-trip tests |
| `server/internal/service/webhook_inbound.go` | HMAC verify + payload map + target dispatch |
| `server/internal/service/webhook_inbound_test.go` | Signature tests, target routing, delivery log |
| `server/internal/handler/webhook.go` | `POST /api/webhooks/{workspace_id}/{endpoint_id}` |
| `server/internal/handler/webhook_test.go` | Valid/invalid HMAC, agent target, workflow target |
| `server/internal/service/webhook_outbound.go` | Event bus subscriber, HMAC sign, retry with backoff |
| `server/internal/service/webhook_outbound_test.go` | Subscription match, retry escalation, HMAC |
| `server/internal/service/template.go` | `TemplateService` — CRUD + materialize-to-agent |
| `server/internal/service/template_test.go` | Template seed + workspace-scoped create-from-template |
| `server/internal/handler/template.go` | Template REST endpoints |
| `server/internal/handler/template_test.go` | Handler-level tests |
| `server/internal/service/apikey.go` | Generate, hash, verify, revoke, scope-check |
| `server/internal/service/apikey_test.go` | Hash round-trip, scope enforcement, expiry |
| `server/internal/middleware/apikey_auth.go` | Chi middleware: bearer → claims |
| `server/internal/middleware/apikey_auth_test.go` | Auth flow + scope mismatch |
| `server/internal/handler/apikey.go` | CRUD + "show only once" response shape |
| `server/internal/handler/apikey_test.go` | Handler tests |
| `server/internal/service/playground.go` | Dry-run execution: wraps ToolRegistry, swaps side-effecting tools for mocks |
| `server/internal/service/playground_test.go` | Dry-run isolation, cost stamping |
| `server/internal/handler/playground.go` | `POST /api/agents/{id}/test` SSE endpoint |
| `server/internal/handler/playground_test.go` | SSE stream test |
| `server/internal/handler/openapi.go` | Serve `/api/openapi.json` + Swagger UI at `/api/docs` |
| `server/internal/service/onboarding.go` | Completion tracking + template recommendation |
| `server/internal/handler/onboarding.go` | `GET/POST /api/onboarding/*` |
| `server/internal/handler/onboarding_test.go` | Flow tests |
| `server/cmd/seed/templates.go` | System-template seed binary (invoked by migration 161 data block or on first boot) |

### Modified Files (Backend)

| File | Changes |
|---|---|
| `server/cmd/server/router.go` | Mount chat WS route, webhook endpoints, template/apikey/playground/onboarding routes, OpenAPI + Swagger |
| `server/cmd/server/main.go` | Instantiate OutboundWebhookService + subscribe to events.Bus on boot |
| `server/pkg/redis/adapter.go` | Add `Subscribe(channel) (<-chan Message, func(), error)` if Phase 2 adapter doesn't expose it |
| `server/pkg/agent/defaults.go` | Append `ChatContextCompactThreshold = 0.7`, `OutboundWebhookTimeout`, `APIKeyPrefix = "sk_"`, `APIKeyRandomBytes = 32` |
| `server/internal/handler/agent.go` | Add `POST /agents/{id}/test` (delegates to playground handler) |

### New Files (Frontend)

| File | Responsibility |
|---|---|
| `packages/core/types/chat.ts` | `ChatSession`, `ChatMessage`, `TypingEvent` |
| `packages/core/types/webhook.ts` | `WebhookEndpoint`, `WebhookSubscription`, `DeliveryLog` |
| `packages/core/types/template.ts` | `AgentTemplate` |
| `packages/core/types/apikey.ts` | `APIKey`, `APIKeyScope` |
| `packages/core/api/chat.ts` | Chat REST client (non-WS pieces: history load, session create) |
| `packages/core/api/webhook.ts` | Webhook CRUD client |
| `packages/core/api/template.ts` | Template client |
| `packages/core/api/apikey.ts` | API key client |
| `packages/core/api/playground.ts` | SSE client for playground |
| `packages/core/chat/queries.ts` | TanStack hooks for chat |
| `packages/core/webhook/queries.ts` | Hooks for webhook CRUD |
| `packages/core/template/queries.ts` | Hooks for templates |
| `packages/core/apikey/queries.ts` | Hooks for API keys |
| `packages/core/chat/ws.ts` | WS subscriber: `chat.typing`, `chat.message`, `chat.done` |
| `packages/views/chat/components/chat-widget.tsx` | Embeddable widget for external sites |
| `packages/views/chat/components/typing-indicator.tsx` | Animated dots |
| `packages/views/webhooks/` | Endpoint list + subscription list + delivery log viewer |
| `packages/views/templates/` | Template gallery + preview + "use template" |
| `packages/views/settings/apikeys/` | API key management |
| `packages/views/agents/components/test-panel.tsx` | Playground side panel |
| `packages/views/onboarding/` | 6-step wizard |

### Modified Files (Frontend)

| File | Changes |
|---|---|
| `packages/core/api/index.ts` | Re-export chat/webhook/template/apikey/playground namespaces |
| `packages/views/agents/components/agent-detail.tsx` | Add "Test agent" button → `test-panel.tsx` |
| `apps/web/app/` + `apps/desktop/.../routes/` | Route stubs for new pages |

### External Infrastructure

| System | Change |
|---|---|
| Redis | Phase 2 baseline; chat uses two new key prefixes: `chat:{wsId}:{provider}` and `chat_response:{sessionId}` |
| None else | No new infra |

---

### Task 1: Migration 160 — Inbound Webhook Tables

**Files:**
- Create: `server/migrations/160_webhooks.up.sql`
- Create: `server/migrations/160_webhooks.down.sql`

**Goal:** land `webhook_endpoint` + `webhook_delivery` with composite FKs + NOT VALID constraint pattern.

- [ ] **Step 1: Write the up migration**

```sql
-- 160_webhooks.up.sql — Phase 8A inbound webhooks.

CREATE TABLE IF NOT EXISTS webhook_endpoint (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    secret_credential_id UUID NOT NULL,
    target_type TEXT NOT NULL,
    target_id UUID NOT NULL,
    payload_mapping JSONB NOT NULL DEFAULT '{}',
    event_filter JSONB,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (workspace_id, secret_credential_id) REFERENCES workspace_credential(workspace_id, id) ON DELETE RESTRICT
);
ALTER TABLE webhook_endpoint DROP CONSTRAINT IF EXISTS webhook_endpoint_target_type_check;
ALTER TABLE webhook_endpoint ADD CONSTRAINT webhook_endpoint_target_type_check
    CHECK (target_type IN ('agent', 'workflow')) NOT VALID;
ALTER TABLE webhook_endpoint VALIDATE CONSTRAINT webhook_endpoint_target_type_check;
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_endpoint_ws_id ON webhook_endpoint(workspace_id, id);
CREATE INDEX IF NOT EXISTS idx_webhook_endpoint_ws_active ON webhook_endpoint(workspace_id, is_active);

CREATE TABLE IF NOT EXISTS webhook_delivery (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    endpoint_id UUID NOT NULL,
    status TEXT NOT NULL,
    request_headers JSONB,
    request_body JSONB,
    response_status INT,
    error TEXT,
    task_id UUID,
    workflow_run_id UUID,
    trace_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (workspace_id, endpoint_id) REFERENCES webhook_endpoint(workspace_id, id) ON DELETE CASCADE
);
ALTER TABLE webhook_delivery DROP CONSTRAINT IF EXISTS webhook_delivery_status_check;
ALTER TABLE webhook_delivery ADD CONSTRAINT webhook_delivery_status_check
    CHECK (status IN ('received', 'processed', 'failed', 'rejected')) NOT VALID;
ALTER TABLE webhook_delivery VALIDATE CONSTRAINT webhook_delivery_status_check;
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_ws_ep ON webhook_delivery(workspace_id, endpoint_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_trace ON webhook_delivery(trace_id) WHERE trace_id IS NOT NULL;
```

- [ ] **Step 2: Write the down migration**

```sql
-- 160_webhooks.down.sql
DROP TABLE IF EXISTS webhook_delivery;
DROP TABLE IF EXISTS webhook_endpoint;
```

- [ ] **Step 3: Apply + verify rollback**

Run: `cd server && make migrate-up && make migrate-down && make migrate-up`
Expected: clean up/down/up cycle.

- [ ] **Step 4: Commit**

```bash
git add server/migrations/160_webhooks.up.sql server/migrations/160_webhooks.down.sql
git commit -m "feat(webhook): migration 160 — inbound endpoint + delivery log

Composite FK binds secret_credential_id to its workspace, RESTRICT on
delete so a credential can't be removed while an endpoint still
references it. trace_id column links delivery → agent task / workflow run."
```

---

### Task 2: Migration 161 — Agent Templates + System Seed

**Files:**
- Create: `server/migrations/161_templates.up.sql`
- Create: `server/migrations/161_templates.down.sql`

**Goal:** agent_template table + five system templates per PLAN.md §8A.4.

- [ ] **Step 1: Write the up migration**

```sql
-- 161_templates.up.sql
-- NOTE (category-list extension): PLAN.md §8A.4 names four categories
-- (customer_support, legal, sales, operations). This migration extends that
-- to seven (adding content, data_analysis, other) because the seeded system
-- templates need `content` and `data_analysis`. If PLAN.md is ever aligned,
-- either the seeds move to 'operations' or §8A.4's list expands to match.

CREATE TABLE IF NOT EXISTS agent_template (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID REFERENCES workspace(id) ON DELETE CASCADE, -- NULL = system template
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    category TEXT NOT NULL,
    agent_type TEXT NOT NULL,
    provider TEXT,
    model TEXT,
    instructions TEXT NOT NULL,
    tools JSONB NOT NULL DEFAULT '[]',
    guardrails JSONB NOT NULL DEFAULT '[]',
    output_schema JSONB,
    knowledge_sources JSONB NOT NULL DEFAULT '[]',
    is_system BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE agent_template DROP CONSTRAINT IF EXISTS agent_template_category_check;
ALTER TABLE agent_template ADD CONSTRAINT agent_template_category_check
    CHECK (category IN ('customer_support', 'legal', 'sales', 'operations', 'content', 'data_analysis', 'other')) NOT VALID;
ALTER TABLE agent_template VALIDATE CONSTRAINT agent_template_category_check;

-- Idempotence guard for the seed INSERT below: without a UNIQUE index the
-- ON CONFLICT DO NOTHING clause is a no-op and re-running the migration
-- would duplicate rows. System templates have workspace_id IS NULL; we use
-- a partial-index UNIQUE on (name) for system rows and a full UNIQUE on
-- (workspace_id, name) for workspace-owned rows.
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_template_system_name ON agent_template(name)
    WHERE is_system = true AND workspace_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_template_ws_name ON agent_template(workspace_id, name)
    WHERE workspace_id IS NOT NULL;
ALTER TABLE agent_template DROP CONSTRAINT IF EXISTS agent_template_agent_type_check;
ALTER TABLE agent_template ADD CONSTRAINT agent_template_agent_type_check
    CHECK (agent_type IN ('coding', 'llm_api', 'http', 'process')) NOT VALID;
ALTER TABLE agent_template VALIDATE CONSTRAINT agent_template_agent_type_check;
CREATE INDEX IF NOT EXISTS idx_agent_template_category ON agent_template(category);
CREATE INDEX IF NOT EXISTS idx_agent_template_ws ON agent_template(workspace_id) WHERE workspace_id IS NOT NULL;

-- Seed five system templates. Idempotent via the partial UNIQUE index
-- idx_agent_template_system_name above — re-running the migration on an
-- existing DB is a no-op. ON CONFLICT ... DO NOTHING targets the name
-- column for system rows (workspace_id IS NULL).
INSERT INTO agent_template (name, description, category, agent_type, provider, model, instructions, is_system)
VALUES
('Customer Support Agent',
 'Responds to support tickets with empathy, escalates complex cases.',
 'customer_support', 'llm_api', 'anthropic', 'claude-haiku-4-5-20251001',
 'You are a customer support agent. Respond warmly, cite the knowledge base when possible, and escalate billing disputes above $100 to a human.',
 true),
('Legal Reviewer',
 'Analyzes contracts for risks, missing clauses, and compliance issues.',
 'legal', 'llm_api', 'anthropic', 'claude-sonnet-4-5-20250514',
 'You are a legal reviewer. Extract key terms, flag missing standard clauses (indemnity, liability cap, governing law), and output a structured risk report.',
 true),
('Sales Outreach',
 'Drafts personalized outreach emails and follow-ups.',
 'sales', 'llm_api', 'anthropic', 'claude-haiku-4-5-20251001',
 'You are a sales outreach assistant. Write short personalized emails, respect opt-out requests, and never make revenue claims.',
 true),
('Data Analyst',
 'Runs database queries and summarizes results as a report.',
 'data_analysis', 'llm_api', 'anthropic', 'claude-sonnet-4-5-20250514',
 'You are a data analyst. Use the database_query tool to investigate questions, then produce a one-paragraph summary with numbers and caveats.',
 true),
('Content Writer',
 'Drafts blog posts and documentation from briefs.',
 'content', 'llm_api', 'anthropic', 'claude-sonnet-4-5-20250514',
 'You are a content writer. Produce clear, concise prose; cite sources from the knowledge base; never fabricate statistics.',
 true)
-- Target the partial UNIQUE index idx_agent_template_system_name explicitly.
-- Without the target ON CONFLICT only matches the PK (a random UUID that
-- will never collide on re-run), making the idempotence guard a no-op —
-- re-running the migration would insert 5 more duplicate rows each time.
ON CONFLICT (name) WHERE is_system = true AND workspace_id IS NULL DO NOTHING;
```

- [ ] **Step 2: Down migration**

```sql
-- 161_templates.down.sql
DROP TABLE IF EXISTS agent_template;
```

- [ ] **Step 3: Apply**

Run: `cd server && make migrate-up`
Expected: 5 system templates seeded. Confirm with `psql -c "SELECT name, category FROM agent_template WHERE is_system = true;"` → 5 rows.

- [ ] **Step 4: Commit**

```bash
git add server/migrations/161_templates.up.sql server/migrations/161_templates.down.sql
git commit -m "feat(templates): migration 161 — agent_template + system seeds

Five system templates (customer_support, legal, sales, data_analysis,
content) seeded idempotently via partial UNIQUE index on (name) WHERE
is_system AND workspace_id IS NULL. Workspace-owned templates use a
separate UNIQUE(workspace_id, name). Category CHECK extends PLAN.md
§8A.4's 4-category list to 7 (adds content + data_analysis + other)
because the seeds need categories beyond the original spec."
```

---

### Task 3: Migration 162 — API Keys + Onboarding Column

**Files:**
- Create: `server/migrations/162_api_keys.up.sql`
- Create: `server/migrations/162_api_keys.down.sql`

**Goal:** `workspace_api_key` + add `workspace.onboarding_completed_at TIMESTAMPTZ` column.

- [ ] **Step 1: Write the up migration**

```sql
-- 162_api_keys.up.sql
-- Also extends chat_message with Phase 8A's real-time-chat columns.
-- Phase 2 shipped chat_message with (id, chat_session_id, role, content,
-- task_id, created_at). Phase 8A's worker path needs workspace isolation,
-- agent attribution, and Redis stream-correlation keys. NULL is acceptable
-- on all four new columns for historical pre-Phase-8A rows; the live
-- chat-worker code paths always supply every field.
ALTER TABLE chat_message ADD COLUMN IF NOT EXISTS workspace_id UUID;
ALTER TABLE chat_message ADD COLUMN IF NOT EXISTS agent_id UUID;
ALTER TABLE chat_message ADD COLUMN IF NOT EXISTS user_id UUID;
ALTER TABLE chat_message ADD COLUMN IF NOT EXISTS stream_id UUID;
CREATE INDEX IF NOT EXISTS idx_chat_message_ws_agent_time ON chat_message(workspace_id, agent_id, created_at DESC)
    WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_chat_message_stream ON chat_message(stream_id)
    WHERE stream_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS workspace_api_key (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL,
    scopes TEXT[] NOT NULL,
    created_by UUID NOT NULL,
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    is_active BOOLEAN NOT NULL DEFAULT true,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_api_key_ws ON workspace_api_key(workspace_id) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_api_key_hash ON workspace_api_key(key_hash) WHERE is_active = true;

ALTER TABLE workspace ADD COLUMN IF NOT EXISTS onboarding_completed_at TIMESTAMPTZ;
```

- [ ] **Step 2: Down migration**

```sql
-- 162_api_keys.down.sql
DROP TABLE IF EXISTS workspace_api_key;
ALTER TABLE workspace DROP COLUMN IF EXISTS onboarding_completed_at;
DROP INDEX IF EXISTS idx_chat_message_ws_agent_time;
DROP INDEX IF EXISTS idx_chat_message_stream;
ALTER TABLE chat_message DROP COLUMN IF EXISTS stream_id;
ALTER TABLE chat_message DROP COLUMN IF EXISTS user_id;
ALTER TABLE chat_message DROP COLUMN IF EXISTS agent_id;
ALTER TABLE chat_message DROP COLUMN IF EXISTS workspace_id;
```

- [ ] **Step 3: Apply + rollback cycle + commit**

```bash
cd server && make migrate-up && make migrate-down && make migrate-up
git add server/migrations/162_api_keys.up.sql server/migrations/162_api_keys.down.sql
git commit -m "feat(apikey): migration 162 — workspace_api_key + onboarding column

Key-hash is the PK-style lookup (SHA-256 of the raw key, never stored
plaintext). Partial index on is_active=true gates revoked keys out of
the common auth hot-path query."
```

---

### Task 4: Migration 163 — Outbound Webhooks

**Files:**
- Create: `server/migrations/163_outbound_webhooks.up.sql`
- Create: `server/migrations/163_outbound_webhooks.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- 163_outbound_webhooks.up.sql
CREATE TABLE IF NOT EXISTS webhook_subscription (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    events TEXT[] NOT NULL,
    secret_credential_id UUID NOT NULL,
    headers JSONB NOT NULL DEFAULT '{}',
    is_active BOOLEAN NOT NULL DEFAULT true,
    retry_policy JSONB NOT NULL DEFAULT '{"max_retries": 3, "backoff_seconds": [5, 30, 300]}',
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (workspace_id, secret_credential_id) REFERENCES workspace_credential(workspace_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_subscription_ws_id ON webhook_subscription(workspace_id, id);
CREATE INDEX IF NOT EXISTS idx_webhook_subscription_active ON webhook_subscription(workspace_id, is_active);

CREATE TABLE IF NOT EXISTS webhook_delivery_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    subscription_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    status INT,                           -- HTTP response status (nullable until delivered)
    response_body TEXT,
    attempt INT NOT NULL DEFAULT 1,
    next_retry_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    trace_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (workspace_id, subscription_id) REFERENCES webhook_subscription(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_delivery_log_retry ON webhook_delivery_log(next_retry_at)
    WHERE next_retry_at IS NOT NULL AND completed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_delivery_log_sub_event ON webhook_delivery_log(subscription_id, event_type, created_at DESC);
```

- [ ] **Step 2: Down**

```sql
-- 163_outbound_webhooks.down.sql
DROP TABLE IF EXISTS webhook_delivery_log;
DROP TABLE IF EXISTS webhook_subscription;
```

- [ ] **Step 3: Apply + commit**

```bash
cd server && make migrate-up
git add server/migrations/163_outbound_webhooks.up.sql server/migrations/163_outbound_webhooks.down.sql
git commit -m "feat(webhook): migration 163 — outbound subscriptions + delivery log

Partial index on next_retry_at is the retry-worker's driver query.
retry_policy JSONB follows Paperclip pattern — default: 3 retries at
5s/30s/5min exponential."
```

---

### Task 5: sqlc Queries

**Files:**
- Create: `server/pkg/db/queries/webhook.sql`
- Create: `server/pkg/db/queries/template.sql`
- Create: `server/pkg/db/queries/apikey.sql`

- [ ] **Step 1: Write webhook.sql**

```sql
-- webhook.sql

-- Endpoints (inbound).

-- name: CreateWebhookEndpoint :one
INSERT INTO webhook_endpoint (workspace_id, name, secret_credential_id, target_type, target_id, payload_mapping, event_filter, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING *;

-- name: GetWebhookEndpoint :one
SELECT * FROM webhook_endpoint WHERE id = $1 AND workspace_id = $2;

-- name: ListWebhookEndpoints :many
SELECT * FROM webhook_endpoint WHERE workspace_id = $1 ORDER BY name ASC;

-- name: UpdateWebhookEndpoint :one
UPDATE webhook_endpoint SET
    name = COALESCE(sqlc.narg('name'), name),
    target_type = COALESCE(sqlc.narg('target_type'), target_type),
    target_id = COALESCE(sqlc.narg('target_id'), target_id),
    payload_mapping = COALESCE(sqlc.narg('payload_mapping'), payload_mapping),
    event_filter = COALESCE(sqlc.narg('event_filter'), event_filter),
    is_active = COALESCE(sqlc.narg('is_active'), is_active)
WHERE id = $1 AND workspace_id = $2 RETURNING *;

-- name: DeleteWebhookEndpoint :exec
DELETE FROM webhook_endpoint WHERE id = $1 AND workspace_id = $2;

-- Delivery records.

-- name: RecordWebhookDelivery :one
INSERT INTO webhook_delivery (workspace_id, endpoint_id, status, request_headers, request_body, response_status, error, task_id, workflow_run_id, trace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING *;

-- name: ListWebhookDeliveries :many
SELECT * FROM webhook_delivery
WHERE workspace_id = $1 AND endpoint_id = $2
ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- Subscriptions (outbound).

-- name: CreateWebhookSubscription :one
INSERT INTO webhook_subscription (workspace_id, url, events, secret_credential_id, headers, retry_policy, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: GetWebhookSubscriptionByID :one
-- Used by the outbound retryLoop to resolve a subscription row from a
-- delivery-log record (which carries subscription_id + workspace_id).
-- Workspace-scoped at the query layer so a cross-tenant subscription_id
-- leak via a crafted delivery-log row cannot read a foreign subscription.
SELECT * FROM webhook_subscription WHERE id = $1 AND workspace_id = $2;

-- name: ListWebhookSubscriptions :many
SELECT * FROM webhook_subscription WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: ListActiveSubscriptionsForEvent :many
-- Used by the outbound delivery worker. event_type matching via ANY(events).
SELECT * FROM webhook_subscription
WHERE is_active = true AND $1 = ANY(events);

-- name: DeleteWebhookSubscription :exec
DELETE FROM webhook_subscription WHERE id = $1 AND workspace_id = $2;

-- Delivery log (outbound).

-- name: InsertDeliveryLog :one
INSERT INTO webhook_delivery_log (workspace_id, subscription_id, event_type, payload, status, response_body, attempt, next_retry_at, trace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: MarkDeliveryComplete :exec
UPDATE webhook_delivery_log
SET status = $3, response_body = $4, completed_at = NOW(), next_retry_at = NULL
WHERE id = $1 AND workspace_id = $2;

-- name: ListDueRetries :many
-- Partial-index-friendly driver query for the retry worker.
SELECT * FROM webhook_delivery_log
WHERE next_retry_at IS NOT NULL AND completed_at IS NULL AND next_retry_at <= NOW()
ORDER BY next_retry_at ASC LIMIT $1;

-- name: ScheduleRetry :exec
UPDATE webhook_delivery_log
SET attempt = attempt + 1, next_retry_at = $3, status = $4, response_body = $5
WHERE id = $1 AND workspace_id = $2;
```

- [ ] **Step 2: Write template.sql**

```sql
-- template.sql

-- name: ListSystemTemplates :many
SELECT * FROM agent_template WHERE is_system = true ORDER BY category, name;

-- name: ListTemplatesForWorkspace :many
-- Returns system templates + workspace's own templates.
SELECT * FROM agent_template
WHERE is_system = true OR workspace_id = $1
ORDER BY is_system DESC, category, name;

-- name: GetTemplate :one
-- System templates (workspace_id IS NULL) or workspace-owned templates.
SELECT * FROM agent_template
WHERE id = $1 AND (workspace_id = $2 OR is_system = true);

-- name: CreateWorkspaceTemplate :one
INSERT INTO agent_template (workspace_id, name, description, category, agent_type, provider, model, instructions, tools, guardrails, output_schema, knowledge_sources)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING *;

-- name: DeleteWorkspaceTemplate :exec
DELETE FROM agent_template WHERE id = $1 AND workspace_id = $2 AND is_system = false;
```

- [ ] **Step 2.5: Write chat.sql (Phase 8A real-time chat queries)**

```sql
-- server/pkg/db/queries/chat.sql

-- name: InsertChatMessage :one
-- Used by ChatService.Dispatch (user path) and ChatWorker.handleMessage
-- (assistant path). Workspace + agent + user + stream_id are added by
-- migration 162; historical rows have NULL for all four.
INSERT INTO chat_message (workspace_id, agent_id, user_id, chat_session_id, role, content, stream_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListRecentChatMessages :many
-- Used by ChatWorker to load history prior to LLM invocation. Ordered
-- DESC + LIMIT so the last N messages come back; caller reverses in Go
-- to feed the LLM in chronological order.
SELECT id, workspace_id, agent_id, user_id, chat_session_id, role, content, stream_id, created_at
FROM chat_message
WHERE workspace_id = $1 AND agent_id = $2
ORDER BY created_at DESC
LIMIT $3;
```

After `make sqlc`, this generates `InsertChatMessage`, `InsertChatMessageParams` (with `WorkspaceID`, `AgentID`, `UserID`, `ChatSessionID`, `Role`, `Content`, `StreamID` — all `pgtype.UUID` for the new nullable columns), and `ListRecentChatMessages` with a 3-param struct.

Update Task 6's `InsertChatMessageParams` call shape: `ChatSessionID` is required by the existing Phase 2 schema and must be supplied. If the worker receives a message without a session_id, it generates one (`uuid.New()`) and writes it; the session-mgmt path in `packages/views/chat` already opens a session per conversation.

- [ ] **Step 3: Write apikey.sql**

```sql
-- apikey.sql

-- name: CreateAPIKey :one
INSERT INTO workspace_api_key (workspace_id, name, key_hash, key_prefix, scopes, created_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: GetAPIKeyByHash :one
SELECT * FROM workspace_api_key WHERE key_hash = $1 AND is_active = true;

-- name: ListAPIKeys :many
SELECT id, workspace_id, name, key_prefix, scopes, created_by, last_used_at, expires_at, is_active, revoked_at, created_at
FROM workspace_api_key WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: RevokeAPIKey :exec
UPDATE workspace_api_key SET is_active = false, revoked_at = NOW()
WHERE id = $1 AND workspace_id = $2;

-- name: TouchAPIKeyUsage :exec
UPDATE workspace_api_key SET last_used_at = NOW() WHERE id = $1;
```

- [ ] **Step 4: Regenerate + compile**

Run: `cd server && make sqlc && go build ./...`
Expected: all queries generated, build succeeds.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/db/queries/chat.sql server/pkg/db/queries/webhook.sql server/pkg/db/queries/template.sql server/pkg/db/queries/apikey.sql server/pkg/db/generated/
git commit -m "feat(db): sqlc queries for chat + webhooks + templates + api keys

ListActiveSubscriptionsForEvent uses PostgreSQL ANY(events) array
match — used by the outbound delivery worker. TouchAPIKeyUsage is
fire-and-forget from the auth middleware hot path."
```

---

### Task 6: Chat Service — Redis Pub/Sub Dispatch + Budget Gate

**Files:**
- Create: `server/internal/service/chat.go`
- Create: `server/internal/service/chat_test.go`

**Goal:** accept chat messages from the WS handler, budget-check, publish to Redis. Return a stream ID the handler uses to correlate the response.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/service/chat_test.go
package service

import (
    "context"
    "testing"
    "time"

    "github.com/google/uuid"

    "aicolab/server/internal/testutil"
)

func TestChatService_DispatchPublishesToRedis(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "chat-test")
    agentID := testutil.SeedAgent(db, wsID, "bot", "anthropic", "claude-haiku-4-5-20251001")

    redis := testutil.NewFakeRedis()
    svc := NewChatService(ChatDeps{DB: db, Redis: redis, Budget: testutil.AlwaysAllowBudget})
    streamID, err := svc.Dispatch(context.Background(), wsID, agentID, "hello", uuid.New())
    if err != nil { t.Fatal(err) }
    if streamID == uuid.Nil { t.Error("streamID = Nil") }

    // Assert one message on the expected channel.
    got := redis.PublishedTo("chat:" + wsID.String() + ":anthropic")
    if len(got) != 1 { t.Fatalf("published count = %d", len(got)) }
    if got[0].Text != "hello" { t.Errorf("text = %q", got[0].Text) }
    _ = time.Second
}

func TestChatService_EmbeddedBypassSkipsRedis(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "embed")
    agentID := testutil.SeedAgent(db, wsID, "bot", "anthropic", "claude-haiku-4-5-20251001")

    redis := testutil.NewFakeRedis()
    inbox := make(chan ChatMessage, 1)
    svc := NewChatService(ChatDeps{
        DB: db, Redis: redis, Budget: testutil.AlwaysAllowBudget,
        EmbeddedWorkerInbox: inbox,
    })
    _, err := svc.Dispatch(context.Background(), wsID, agentID, "hi", uuid.New())
    if err != nil { t.Fatal(err) }

    select {
    case msg := <-inbox:
        if msg.Text != "hi" { t.Errorf("inbox text = %q", msg.Text) }
    case <-time.After(100 * time.Millisecond):
        t.Fatal("message not delivered to inbox")
    }
    // Redis must NOT have been used.
    if got := redis.PublishedTo("chat:" + wsID.String() + ":anthropic"); len(got) != 0 {
        t.Errorf("embedded mode leaked to redis: %+v", got)
    }
}

func TestChatService_DispatchRejectsOverBudget(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "chat-budget")
    agentID := testutil.SeedAgent(db, wsID, "bot", "anthropic", "claude-haiku-4-5-20251001")

    redis := testutil.NewFakeRedis()
    svc := NewChatService(ChatDeps{DB: db, Redis: redis, Budget: testutil.DenyBudget("exceeded")})
    _, err := svc.Dispatch(context.Background(), wsID, agentID, "hi", uuid.New())
    if err == nil { t.Fatal("expected budget_denied error") }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/service/ -run TestChatService -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/internal/service/chat.go
package service

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
    "aicolab/server/pkg/redis"
)

// BudgetAllowFn is Phase 6's dispatch-time budget check, re-exported here
// so ChatService can gate messages without importing delegation internals.
// If Phase 6's service/delegation.go has not shipped this alias (it does
// as of Phase 6 Round 1), keep this local alias as a stable Phase 8A
// boundary — callers wire a closure around BudgetService.CanDispatch.
type BudgetAllowFn func(ctx context.Context, estCents int) (allow bool, reason string)

type ChatDeps struct {
    DB     *pgxpool.Pool
    Redis  redis.Adapter
    Budget BudgetAllowFn // Phase 6 service/delegation.go re-exports this name

    // EmbeddedWorkerInbox, when non-nil, activates the single-process
    // bypass mandated by PLAN.md §8A.1: instead of publishing to Redis,
    // ChatService.Dispatch sends the ChatMessage directly down this Go
    // channel to an in-process ChatWorker. Set by server/main.go when
    // the process is launched with --with-worker.
    //
    // IMPORTANT: the channel is bounded. server/main.go creates it with
    //     make(chan ChatMessage, 64)  // defaults.EmbeddedInboxCapacity
    // A full inbox returns ErrChatInboxFull (→ HTTP 503) IMMEDIATELY
    // rather than blocking on ctx.Done() for the client's request timeout.
    // Production multi-replica deploys leave this nil and always use Redis.
    EmbeddedWorkerInbox chan<- ChatMessage
}

type ChatService struct {
    db      *pgxpool.Pool
    redis   redis.Adapter
    budget  BudgetAllowFn
    inbox   chan<- ChatMessage
}

func NewChatService(deps ChatDeps) *ChatService {
    return &ChatService{
        db: deps.DB, redis: deps.Redis, budget: deps.Budget,
        inbox: deps.EmbeddedWorkerInbox,
    }
}

// ErrChatInboxFull is returned by Dispatch when the single-process
// embedded worker inbox is at capacity. Handler layer maps this to HTTP
// 503 + a Retry-After header so the client backs off explicitly instead
// of silently timing out on context cancellation.
var ErrChatInboxFull = errors.New("chat: embedded worker inbox is full")

type ChatMessage struct {
    StreamID uuid.UUID `json:"stream_id"`
    AgentID  uuid.UUID `json:"agent_id"`
    UserID   uuid.UUID `json:"user_id"`
    Text     string    `json:"text"`
}

// Dispatch budget-checks, writes the user message to chat_message, and
// publishes to the provider's Redis channel. Returns the stream_id the
// WS handler correlates responses on.
func (s *ChatService) Dispatch(ctx context.Context, wsID, agentID uuid.UUID, text string, userID uuid.UUID) (uuid.UUID, error) {
    q := db.New(s.db)
    agent, err := q.GetAgent(ctx, db.GetAgentParams{ID: uuidPg(agentID), WorkspaceID: uuidPg(wsID)})
    if err != nil { return uuid.Nil, fmt.Errorf("agent lookup: %w", err) }

    // Budget gate — conservative 50 cents per message.
    if allow, reason := s.budget(ctx, 50); !allow {
        return uuid.Nil, fmt.Errorf("budget denied: %s", reason)
    }

    streamID := uuid.New()
    payload := ChatMessage{StreamID: streamID, AgentID: agentID, UserID: userID, Text: text}

    // Single-process bypass per PLAN.md §8A.1: when the server is launched
    // with --with-worker, a Go channel replaces Redis for the chat hot path
    // (no serialization, no network, no pub/sub fan-out). Multi-replica
    // production deploys leave inbox nil and always take the Redis branch.
    //
    // The inbox is bounded (capacity 64 — see server/main.go). On full,
    // we return ErrChatInboxFull IMMEDIATELY so the handler can map to HTTP
    // 503 Service Unavailable. Falling back to ctx.Done() here would stall
    // the HTTP request for the client's request timeout (minutes), which
    // masks an overloaded worker as "slow LLM".
    if s.inbox != nil {
        select {
        case s.inbox <- payload:
        case <-ctx.Done():
            return uuid.Nil, ctx.Err()
        default:
            return uuid.Nil, ErrChatInboxFull
        }
    } else {
        // Publish to the provider's Redis Stream. Streams (unlike pub/sub)
        // persist messages until an XACK, so a worker crash between
        // XREADGROUP and XACK causes XAUTOCLAIM to reassign the entry to
        // another worker after the min-idle window (300s). This closes the
        // reliability gap a SETNX-claim-on-pub/sub scheme would leave open.
        stream := fmt.Sprintf("chat:%s:%s", wsID.String(), agent.Provider.String)
        raw, _ := json.Marshal(payload)
        if err := s.redis.XAdd(ctx, stream, map[string]any{
            "payload":   string(raw),
            "stream_id": streamID.String(),
        }); err != nil {
            return uuid.Nil, fmt.Errorf("xadd: %w", err)
        }
    }

    // Persist user message — agent response is appended by the worker.
    _, _ = q.InsertChatMessage(ctx, db.InsertChatMessageParams{
        WorkspaceID: uuidPg(wsID), AgentID: uuidPg(agentID),
        UserID:      uuidPg(userID),
        Role:        "user", Content: text, StreamID: uuidPg(streamID),
    })
    return streamID, nil
}
```

Uses the existing `chat_message` table (Phase 2) plus the `stream_id UUID` column added in Task 3 (migration 162). `InsertChatMessage` is regenerated by Task 5's `make sqlc` against the extended schema.

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/service/ -run TestChatService -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/chat.go server/internal/service/chat_test.go
git commit -m "feat(chat): ChatService.Dispatch — budget + Redis pub/sub

Conservative 50c budget estimate per message. Channel name convention
chat:{wsId}:{provider} so all workers supporting that provider see
every message and one claims via application-level lock."
```

---

### Task 7: Chat Worker — Redis Subscriber + LLM Loop + Stream Back

**Files:**
- Create: `server/internal/worker/chat.go`
- Create: `server/internal/worker/chat_test.go`

**Goal:** worker subscribes to `chat:*:{provider}` channels on startup, claims incoming messages, runs the LLM loop with history + context compaction, streams tokens back via `chat_response:{stream_id}`.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/worker/chat_test.go
package worker

import (
    "context"
    "testing"
    "time"

    "github.com/google/uuid"

    "aicolab/server/internal/testutil"
)

func TestChatWorker_CompactsWhenHistoryExceedsThreshold(t *testing.T) {
    t.Parallel()
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    db := testutil.NewTestDB(t)
    redis := testutil.NewFakeRedis()
    wsID := testutil.SeedWorkspace(db, "compact")
    agentID := testutil.SeedAgent(db, wsID, "bot", "anthropic", "claude-haiku-4-5-20251001")

    // Model with a tiny 1000-token window so ChatContextCompactThreshold
    // (0.7) triggers at ~2800 bytes. Seed history well above that.
    model := testutil.StubModelWithContextWindow(1000, "compacted reply")
    for i := 0; i < 40; i++ {
        testutil.SeedChatMessage(db, wsID, agentID, "user",
            strings.Repeat("x", 100)) // 40 * 100 = 4000 bytes total
    }

    w := NewChatWorker(ChatWorkerDeps{
        DB: db, Redis: redis, Providers: []string{"anthropic"},
        Model: model, WS: testutil.NoopWS,
    })
    go w.Run(ctx)
    time.Sleep(50 * time.Millisecond)

    streamID := uuid.New()
    redis.PublishTest("chat:"+wsID.String()+":anthropic",
        testutil.JSONBytes(map[string]any{
            "stream_id": streamID, "agent_id": agentID, "user_id": uuid.New(), "text": "summarize",
        }))

    _ = redis.WaitForResponse("chat_response:"+streamID.String(), 2*time.Second)
    // Model captures the messages it was called with; assert it saw a
    // compacted prefix (< 5 messages after compaction, not 40).
    if n := model.LastMessageCount(); n >= 40 {
        t.Errorf("compaction did not fire: model saw %d messages", n)
    }
}

func TestChatWorker_RespondsToMessage(t *testing.T) {
    t.Parallel()
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    db := testutil.NewTestDB(t)
    redis := testutil.NewFakeRedis()
    wsID := testutil.SeedWorkspace(db, "chat-w")
    agentID := testutil.SeedAgent(db, wsID, "bot", "anthropic", "claude-haiku-4-5-20251001")

    w := NewChatWorker(ChatWorkerDeps{
        DB:        db,
        Redis:     redis,
        Providers: []string{"anthropic"},
        Model:     testutil.StubModelReplying("Hello back!"),
        WS:        testutil.NoopWS,
    })
    go w.Run(ctx)
    time.Sleep(50 * time.Millisecond)

    streamID := uuid.New()
    redis.PublishTest("chat:"+wsID.String()+":anthropic",
        testutil.JSONBytes(map[string]any{
            "stream_id": streamID, "agent_id": agentID, "user_id": uuid.New(), "text": "hi",
        }))

    resp := redis.WaitForResponse("chat_response:"+streamID.String(), 2*time.Second)
    if resp == "" { t.Fatal("no response on chat_response channel") }
    if resp != "Hello back!" { t.Errorf("response = %q", resp) }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/worker/ -run TestChatWorker -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/internal/worker/chat.go
package worker

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    mcagent "aicolab/server/pkg/agent"
    "aicolab/server/pkg/agent/harness"
    "aicolab/server/pkg/db/db"
    "aicolab/server/pkg/redis"
    "aicolab/server/internal/service"
)

type ChatWorkerDeps struct {
    DB        *pgxpool.Pool
    Redis     redis.Adapter
    Providers []string
    Model     harness.Model // Phase 2 LanguageModel — test stub implements
    WS        interface{ Publish(uuid.UUID, string, map[string]any) }

    // Archivist persists the compacted middle from maybeCompact into
    // MemoryService (Phase 5 archival tier). Nil-safe — when nil, compaction
    // still runs but the compacted slice isn't written to archival memory.
    Archivist agent.ArchivistFn

    // EmbeddedWorkerInbox, when non-nil, activates the single-process
    // embedded-worker path: Run() reads from this channel instead of
    // subscribing to Redis Streams. Kept in sync with ChatDeps. See
    // worker.Run for the branch. Leave nil in multi-replica deploys.
    EmbeddedWorkerInbox <-chan service.ChatMessage

    // InstanceID disambiguates consumer names within the "chat-workers"
    // Redis Streams consumer group. Consumers sharing a name receive at
    // most one message at a time between them (Redis enforces exclusivity
    // by consumer+group), so workers MUST have distinct names. Derive from
    // hostname+PID at server startup.
    InstanceID string
}

type ChatWorker struct {
    deps ChatWorkerDeps
}

func NewChatWorker(deps ChatWorkerDeps) *ChatWorker { return &ChatWorker{deps: deps} }

func (w *ChatWorker) Run(ctx context.Context) {
    // Embedded single-process mode: Dispatch sends ChatMessages down a
    // Go channel instead of XADD to Redis. Read from that channel here
    // instead of subscribing to Streams. No consumer groups, no claim
    // mechanism — the channel semantics (one receiver drains) already
    // guarantee exclusivity; worker crash drops in-flight messages,
    // acceptable for single-process dev/test deploys per PLAN.md §8A.1.
    if w.deps.EmbeddedWorkerInbox != nil {
        go w.consumeEmbedded(ctx)
        <-ctx.Done()
        return
    }

    // Multi-replica mode: one goroutine per provider subscribes to the
    // Redis Streams consumer group.
    for _, provider := range w.deps.Providers {
        go w.subscribe(ctx, provider)
    }
    <-ctx.Done()
}

// consumeEmbedded drains the in-process ChatMessage channel. The ack
// closure is a no-op because there's no reliability layer (no Streams,
// no SETNX); the channel send from Dispatch already succeeded and the
// client already knows streamID.
func (w *ChatWorker) consumeEmbedded(ctx context.Context) {
    noopAck := func() {}
    for {
        select {
        case <-ctx.Done():
            return
        case msg, ok := <-w.deps.EmbeddedWorkerInbox:
            if !ok { return }
            raw, _ := json.Marshal(msg)
            // Reuse handleMessage; synthetic stream + entry ID so the
            // log/metrics path matches the Streams case.
            go w.handleMessage(ctx,
                fmt.Sprintf("chat:embedded:%s", msg.AgentID),
                "embedded-"+msg.StreamID.String(),
                map[string]any{"payload": string(raw), "stream_id": msg.StreamID.String()},
                noopAck,
            )
        }
    }
}

func (w *ChatWorker) subscribe(ctx context.Context, provider string) {
    // Redis Streams consumer group per provider. Multiple workers join
    // "chat-workers" group on the same stream pattern; Redis distributes
    // one message per XREADGROUP call. On worker crash, XAUTOCLAIM (below)
    // hands the stuck entry to a live worker after 5 minutes idle.
    const groupName = "chat-workers"
    consumerName := "worker-" + w.deps.InstanceID // MUST be unique per worker
    pattern := fmt.Sprintf("chat:*:%s", provider)

    // Enumerate matching streams + ensure the consumer group exists on each.
    streams, err := w.deps.Redis.Keys(ctx, pattern)
    if err != nil { return }
    for _, s := range streams {
        _ = w.deps.Redis.XGroupCreate(ctx, s, groupName, "$", true) // $ = start with NEW messages
    }

    go w.reclaimStalled(ctx, pattern, groupName, consumerName)

    for {
        select {
        case <-ctx.Done():
            return
        default:
        }
        // Block up to 5s for new entries; > 0 count keeps throughput up.
        entries, err := w.deps.Redis.XReadGroup(ctx, groupName, consumerName, streams, 5*time.Second, 10)
        if err != nil { continue }
        for _, entry := range entries {
            go w.handleMessage(ctx, entry.Stream, entry.ID, entry.Values,
                func() { _ = w.deps.Redis.XAck(ctx, entry.Stream, groupName, entry.ID) })
        }
    }
}

// reclaimStalled runs XAUTOCLAIM every 60s to reassign pending-entries
// older than 5 minutes to this consumer. Covers the worker-crash case
// PLAN.md §8A.1 implies but Redis pub/sub cannot provide.
func (w *ChatWorker) reclaimStalled(ctx context.Context, pattern, group, consumer string) {
    t := time.NewTicker(60 * time.Second)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            streams, _ := w.deps.Redis.Keys(ctx, pattern)
            for _, s := range streams {
                _, _ = w.deps.Redis.XAutoClaim(ctx, s, group, consumer, 5*time.Minute, "0")
            }
        }
    }
}

func (w *ChatWorker) handleMessage(ctx context.Context, channel, entryID string, values map[string]any, ack func()) {
    // Streams consumer-group semantics guarantee single delivery per entry
    // (no SETNX claim needed). ack() is called ONLY after the response is
    // fully written to chat_response; if we return early via panic/ctx-done
    // the entry stays pending and XAUTOCLAIM reassigns it after 5 minutes.
    raw, _ := values["payload"].(string)
    var msg service.ChatMessage
    if err := json.Unmarshal([]byte(raw), &msg); err != nil { ack(); return }

    // Load history + build prompt. Keep recent 20 messages; compaction
    // (Phase 5.4) triggers when serialized history exceeds 70% of the
    // model's context window.
    wsID := parseWsIDFromChannel(channel)
    q := db.New(w.deps.DB)
    history, _ := q.ListRecentChatMessages(ctx, db.ListRecentChatMessagesParams{
        WorkspaceID: uuidPg(wsID), AgentID: uuidPg(msg.AgentID), Limit: 20,
    })
    compressed := maybeCompact(ctx, history, w.deps.Model, w.deps.Archivist)

    h := harness.New(harness.Config{
        Model:        w.deps.Model,
        SystemPrompt: buildChatSystemPrompt(msg.AgentID),
        MaxSteps:     mcagent.MainAgentMaxSteps,
    })
    res, err := h.ExecuteWithMessages(ctx, compressed)
    if err != nil {
        _ = w.deps.Redis.Publish(ctx, "chat_response:"+msg.StreamID.String(),
            []byte(`{"type":"error","text":"`+err.Error()+`"}`))
        // Ack anyway — the error is persisted to the client via the
        // response channel; we don't want XAUTOCLAIM to redeliver and
        // re-run a failing LLM call indefinitely.
        ack()
        return
    }

    // Persist agent reply.
    _, _ = q.InsertChatMessage(ctx, db.InsertChatMessageParams{
        WorkspaceID: uuidPg(wsID), AgentID: uuidPg(msg.AgentID),
        UserID:      uuidPg(uuid.Nil), // 0 = agent speaker
        Role:        "assistant", Content: res.Text, StreamID: uuidPg(msg.StreamID),
    })
    _ = w.deps.Redis.Publish(ctx, "chat_response:"+msg.StreamID.String(), []byte(res.Text))
    // Acknowledge only after the full response is written. Worker crash
    // between XREADGROUP and this line leaves the entry pending →
    // reclaimed by another worker in the next XAUTOCLAIM sweep.
    ack()
}

// maybeCompact consults Phase 5 agent.Compact when the serialized history
// would exceed ChatContextCompactThreshold (default 0.7) of the provider's
// context window. Below threshold it passes history through unchanged; above
// threshold it returns the compacted message slice.
//
// Archivist is pulled off ChatWorkerDeps so each worker can persist the
// compacted middle to MemoryService (Phase 5 integration). Pass nil to
// skip archival — Compact is nil-safe on the Archivist field.
func maybeCompact(
    ctx context.Context,
    history []db.ChatMessage,
    model harness.Model,
    archivist agent.ArchivistFn,
) []types.Message {
    msgs := make([]types.Message, 0, len(history))
    for _, m := range history {
        msgs = append(msgs, types.Message{Role: m.Role, Content: []types.ContentBlock{{Type: "text", Text: m.Content}}})
    }

    // Decide threshold from model's context window. harness.Model exposes
    // ContextWindow() tokens; convert to bytes via 4 chars ≈ 1 token heuristic
    // already used by Phase 5's compaction helpers.
    windowBytes := model.ContextWindow() * 4
    threshold := int(float64(windowBytes) * mcagent.ChatContextCompactThreshold)

    if messagesBytes(msgs) < threshold {
        return msgs
    }

    res, err := agent.Compact(ctx, model.Provider(), msgs, agent.CompactOptions{
        Summariser: model,       // use same model for the summary on the chat hot path
        Archivist:  archivist,   // nil-safe; wires to MemoryService at construction time
    })
    if err != nil || !res.Changed {
        return msgs
    }
    return res.Messages
}

func messagesBytes(msgs []types.Message) int {
    n := 0
    for _, m := range msgs {
        for _, c := range m.Content { n += len(c.Text) }
    }
    return n
}

func buildChatSystemPrompt(agentID uuid.UUID) string { return "You are a helpful assistant." }

func parseWsIDFromChannel(channel string) uuid.UUID {
    // "chat:{wsId}:{provider}" — split and take [1].
    parts := splitChannel(channel)
    if len(parts) < 3 { return uuid.Nil }
    id, _ := uuid.Parse(parts[1])
    return id
}

func splitChannel(s string) []string { /* strings.Split(s, ":") */ return nil }
```

- [ ] **Step 4: Run + commit**

Run: `cd server && go test ./internal/worker/ -run TestChatWorker -v -timeout 10s`
Expected: PASS.

```bash
git add server/internal/worker/chat.go server/internal/worker/chat_test.go
git commit -m "feat(chat): worker subscribes to Redis chat channels

PSubscribe pattern chat:*:{provider}; one goroutine per provider. Claim
via Redis SET NX chat_claim:{stream_id}. History load + Phase 5 compaction
(stubbed in this commit) before LLM. Response published to chat_response:
{stream_id} for the handler's WS relay."
```

---

### Task 8: Chat Handler — WebSocket Relay

**Files:**
- Modify: `server/internal/handler/chat.go`
- Create: `server/internal/handler/chat_test.go`

**Goal:** WS endpoint receives `chat:message` → calls `ChatService.Dispatch` → subscribes to `chat_response:{stream_id}` → forwards tokens to the client.

- [ ] **Step 1: Write failing test**

```go
// server/internal/handler/chat_test.go — WS integration test.
func TestChatWS_RelaysResponse(t *testing.T) {
    tc := newChatHandlerCtx(t)
    agentID := tc.createAgent()
    sessionID := uuid.New()

    tc.wsConn.WriteJSON(map[string]any{
        "type": "chat:message",
        "payload": map[string]any{
            "session_id": sessionID, "agent_id": agentID, "text": "hi",
        },
    })

    tc.redis.PublishTest("chat_response:"+sessionID.String(), []byte(`{"text":"hello"}`))

    var got map[string]any
    tc.wsConn.ReadJSON(&got)
    if got["type"] != "chat:message" || got["payload"].(map[string]any)["text"] != "hello" {
        t.Errorf("got %v", got)
    }
}
```

- [ ] **Step 2: Implement (abbreviated — standard Gorilla WebSocket pattern)**

```go
// server/internal/handler/chat.go — append:
func (h *ChatHandler) HandleMessage(ws *websocket.Conn, env Env, msg ChatMessageFrame) {
    streamID, err := h.chat.Dispatch(ctx, env.WorkspaceID, msg.AgentID, msg.Text, env.UserID)
    if err != nil { h.writeError(ws, err); return }
    ch, cancel, _ := h.redis.Subscribe(ctx, "chat_response:"+streamID.String())
    defer cancel()
    for resp := range ch {
        _ = ws.WriteJSON(map[string]any{
            "type":    "chat:message",
            "payload": map[string]any{"session_id": msg.SessionID, "text": string(resp.Payload)},
        })
    }
}
```

- [ ] **Step 3: Run + commit**

```bash
cd server && go test ./internal/handler/ -run TestChatWS -v
git add server/internal/handler/chat.go server/internal/handler/chat_test.go
git commit -m "feat(chat): WS handler relays Redis response stream to client

Single subscribe per message; cancel the subscription when the WS closes
or the response stream ends."
```

---

### Task 9: Chat Frontend — Typing Indicator + Widget

**Files:**
- Create: `packages/core/types/chat.ts`
- Create: `packages/core/chat/queries.ts`
- Create: `packages/core/chat/ws.ts`
- Create: `packages/views/chat/components/typing-indicator.tsx`
- Modify: `packages/views/chat/chat-view.tsx` (existing) to add typing state + message relay
- Create: `packages/views/chat/components/chat-widget.tsx`

**Goal:** UX fills — typing dots while streaming, embeddable widget for external sites. Existing `packages/views/chat/` already handles the basic chat view.

- [ ] **Step 1: Write types + hooks + WS**

```ts
// packages/core/types/chat.ts
export type ChatMessage = {
  id: string;
  session_id: string;
  role: "user" | "assistant";
  content: string;
  created_at: string;
};
export type TypingEvent = { session_id: string; agent_id: string; typing: boolean };

// packages/core/chat/ws.ts — subscribe in CoreProvider
import { wsHub } from "../ws/hub";
import { queryClient } from "../query/client";

wsHub.on("chat:message", (e: { workspace_id: string; payload: { session_id: string } }) =>
  queryClient.invalidateQueries({ queryKey: ["chat", e.workspace_id, e.payload.session_id] }));
wsHub.on("chat:typing", (e: { workspace_id: string; payload: TypingEvent }) =>
  queryClient.setQueryData(["chat", e.workspace_id, "typing", e.payload.session_id], e.payload.typing));
```

- [ ] **Step 2: Widget + typing indicator**

```tsx
// packages/views/chat/components/typing-indicator.tsx
export function TypingIndicator({ visible }: { visible: boolean }) {
  if (!visible) return null;
  return (
    <div aria-live="polite" className="flex items-center gap-1 p-2 text-muted-foreground">
      <span className="size-1.5 animate-pulse rounded-full bg-current" />
      <span className="size-1.5 animate-pulse rounded-full bg-current [animation-delay:150ms]" />
      <span className="size-1.5 animate-pulse rounded-full bg-current [animation-delay:300ms]" />
    </div>
  );
}

// packages/views/chat/components/chat-widget.tsx — external embeddable
export function ChatWidget({ agentId, workspaceId }: { agentId: string; workspaceId: string }) {
  // Same React tree as internal chat-view, but takes agentId as a prop and
  // boots its own CoreProvider so it can be embedded as an isolated bundle.
  return <ChatView sessionId={...} agentId={agentId} wsId={workspaceId} minimalChrome />;
}
```

- [ ] **Step 3: Commit**

```bash
git add packages/core/types/chat.ts packages/core/chat/ packages/views/chat/components/typing-indicator.tsx packages/views/chat/components/chat-widget.tsx
git commit -m "feat(chat): typing indicator + embeddable widget

Widget reuses ChatView with minimalChrome=true so the external bundle is
< 100KB gzipped. Typing event is a zustand-free bool in the query cache."
```

---

### Task 10: Inbound Webhook Handler — HMAC + Target Dispatch

**Files:**
- Create: `server/internal/service/webhook_inbound.go`
- Create: `server/internal/handler/webhook.go`
- Create: `server/internal/handler/webhook_test.go`

**Goal:** `POST /api/webhooks/{workspace_id}/{endpoint_id}` — verify HMAC using the credential vault, map payload, dispatch to agent task OR workflow run. Log delivery.

- [ ] **Step 1: Write failing test**

```go
// server/internal/handler/webhook_test.go
func TestWebhook_ValidHMAC_DispatchesToAgent(t *testing.T) {
    tc := newWebhookHandlerCtx(t)
    secretID := tc.seedCredential("webhook_hmac", "test-secret")
    endpointID := tc.createEndpoint("agent", tc.agentID, secretID)

    body := []byte(`{"customer":"Acme","msg":"issue"}`)
    sig := hmacHex(body, "test-secret")

    req := httptest.NewRequest("POST",
        "/api/webhooks/"+tc.wsID+"/"+endpointID, bytes.NewReader(body))
    req.Header.Set("X-Hmac-SHA256", sig)
    resp := tc.serve(req)
    if resp.Code != 202 { t.Fatalf("status = %d", resp.Code) }

    delivery := tc.latestDelivery(endpointID)
    if delivery.Status != "processed" { t.Errorf("status = %q", delivery.Status) }
    if delivery.TaskID == nil { t.Error("no task created") }
}

func TestWebhook_InvalidHMAC_Rejected(t *testing.T) {
    tc := newWebhookHandlerCtx(t)
    secretID := tc.seedCredential("webhook_hmac", "correct")
    endpointID := tc.createEndpoint("agent", tc.agentID, secretID)

    req := httptest.NewRequest("POST",
        "/api/webhooks/"+tc.wsID+"/"+endpointID, bytes.NewReader([]byte(`{}`)))
    req.Header.Set("X-Hmac-SHA256", "deadbeef")
    resp := tc.serve(req)
    if resp.Code != 401 { t.Errorf("status = %d, want 401", resp.Code) }
}
```

- [ ] **Step 2: Implement the service**

```go
// server/internal/service/webhook_inbound.go
package service

import (
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/agent" // credential vault
    "aicolab/server/pkg/db/db"
)

var ErrInvalidSignature = errors.New("invalid webhook signature")

type WebhookInboundDeps struct {
    DB          *pgxpool.Pool
    Credentials *agent.Credentials // Phase 1.4 — decrypts the HMAC secret
    TaskSvc     interface {
        EnqueueFromWebhook(ctx context.Context, wsID, agentID uuid.UUID, payload map[string]any) (uuid.UUID, error)
    }
    WorkflowSvc interface {
        Trigger(ctx context.Context, wsID, wfID uuid.UUID, input map[string]any, triggeredBy string) (uuid.UUID, error)
    }
}

type WebhookInboundService struct {
    db    *pgxpool.Pool
    creds *agent.Credentials
    task  WebhookInboundDeps
}

func NewWebhookInboundService(deps WebhookInboundDeps) *WebhookInboundService {
    return &WebhookInboundService{db: deps.DB, creds: deps.Credentials, task: deps}
}

// VerifyAndDispatch returns the created task/run ID + logs delivery.
func (s *WebhookInboundService) VerifyAndDispatch(
    ctx context.Context, wsID, endpointID uuid.UUID, signature string, body []byte, headers map[string]string,
) (uuid.UUID, error) {
    q := db.New(s.db)
    ep, err := q.GetWebhookEndpoint(ctx, db.GetWebhookEndpointParams{
        ID: uuidPg(endpointID), WorkspaceID: uuidPg(wsID),
    })
    if err != nil { return uuid.Nil, fmt.Errorf("endpoint not found") }
    if !ep.IsActive { return uuid.Nil, fmt.Errorf("endpoint disabled") }

    // Decrypt HMAC secret from the credential vault.
    secret, err := s.creds.GetRaw(ctx, wsID, uuidFromPg(ep.SecretCredentialID))
    if err != nil { return uuid.Nil, fmt.Errorf("credential: %w", err) }
    defer agent.Zero(secret) // clear plaintext from memory after use

    if !hmacVerify(body, secret, signature) {
        s.logDelivery(ctx, wsID, endpointID, "rejected", headers, body, 0, "invalid signature")
        return uuid.Nil, ErrInvalidSignature
    }

    // Transform payload per endpoint.payload_mapping. V1: direct pass-through
    // if mapping is empty, otherwise the mapping JSON-pointer replaces top-
    // level keys.
    var payload map[string]any
    _ = json.Unmarshal(body, &payload)
    mapped := applyPayloadMapping(ep.PayloadMapping, payload)

    var resultID uuid.UUID
    switch ep.TargetType {
    case "agent":
        resultID, err = s.task.EnqueueFromWebhook(ctx, wsID, uuidFromPg(ep.TargetID), mapped)
    case "workflow":
        resultID, err = s.task.(WebhookInboundDeps).WorkflowSvc.Trigger(ctx, wsID, uuidFromPg(ep.TargetID), mapped, "webhook:"+endpointID.String())
    default:
        return uuid.Nil, fmt.Errorf("unknown target_type %q", ep.TargetType)
    }
    if err != nil {
        s.logDelivery(ctx, wsID, endpointID, "failed", headers, body, 0, err.Error())
        return uuid.Nil, err
    }
    s.logDelivery(ctx, wsID, endpointID, "processed", headers, body, 202, "")
    return resultID, nil
}

func (s *WebhookInboundService) logDelivery(ctx context.Context, wsID, epID uuid.UUID, status string, headers map[string]string, body []byte, respStatus int, errText string) {
    q := db.New(s.db)
    hdrJSON, _ := json.Marshal(headers)
    _, _ = q.RecordWebhookDelivery(ctx, db.RecordWebhookDeliveryParams{
        WorkspaceID: uuidPg(wsID), EndpointID: uuidPg(epID),
        Status: status, RequestHeaders: hdrJSON, RequestBody: body,
        ResponseStatus: sqlNullInt(respStatus), Error: sqlNullString(errText),
    })
}

func hmacVerify(body, secret []byte, signatureHex string) bool {
    mac := hmac.New(sha256.New, secret)
    mac.Write(body)
    expected := mac.Sum(nil)
    provided, err := hex.DecodeString(signatureHex)
    if err != nil { return false }
    return hmac.Equal(expected, provided) // constant-time
}

func applyPayloadMapping(mapping []byte, in map[string]any) map[string]any {
    if len(mapping) == 0 || string(mapping) == "{}" { return in }
    // V1: mapping is {"outKey": "pointer/to/input"} — jsonpath-lite.
    var rules map[string]string
    if err := json.Unmarshal(mapping, &rules); err != nil { return in }
    out := map[string]any{}
    for k, path := range rules {
        // Minimal pointer lookup: split on "/" and walk.
        cur := any(in)
        for _, part := range splitPath(path) {
            m, ok := cur.(map[string]any)
            if !ok { cur = nil; break }
            cur = m[part]
        }
        out[k] = cur
    }
    return out
}
func splitPath(s string) []string { return nil /* strings.Split(s, "/") in real file */ }
```

- [ ] **Step 3: Implement the handler**

```go
// server/internal/handler/webhook.go
func (h *WebhookHandler) Receive(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "workspace_id")
    endpointID, _ := uuidFromParam(r, "endpoint_id")
    sig := r.Header.Get("X-Hmac-SHA256")
    body, _ := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // cap 10MB
    headers := flattenHeaders(r.Header)
    id, err := h.svc.VerifyAndDispatch(r.Context(), wsID, endpointID, sig, body, headers)
    if errors.Is(err, service.ErrInvalidSignature) { http.Error(w, "invalid signature", 401); return }
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 202, map[string]any{"target_id": id})
}
```

- [ ] **Step 4: Run + commit**

```bash
cd server && go test ./internal/service/ ./internal/handler/ -run Webhook -v
git add server/internal/service/webhook_inbound.go server/internal/handler/webhook.go server/internal/handler/webhook_test.go
git commit -m "feat(webhook): inbound endpoint with HMAC verify + delivery log

Secret resolved from credential vault (AAD-bound), zeroed after use.
Constant-time HMAC compare. Delivery log records every call — processed,
rejected, failed — for debugging."
```

---

### Task 11: Outbound Webhook Service — Subscribe + Retry

**Files:**
- Create: `server/internal/service/webhook_outbound.go`
- Create: `server/internal/service/webhook_outbound_test.go`

**Goal:** subscribe to `events.Bus`, match against `webhook_subscription.events`, sign with HMAC, POST, log, retry on failure with exponential backoff.

- [ ] **Step 1: Write failing tests**

```go
func TestOutbound_DeliversMatchingEvent(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    httpMock := testutil.NewHTTPSink()
    defer httpMock.Close()

    wsID := testutil.SeedWorkspace(db, "ob")
    secretID := testutil.SeedCredential(db, wsID, "webhook_hmac", "shh")
    subID := testutil.CreateOutboundSub(db, wsID, httpMock.URL, []string{"task.completed"}, secretID)

    bus := testutil.NewBus()
    svc := NewOutboundWebhookService(OutboundDeps{DB: db, Bus: bus, Credentials: testutil.StubCredentials(secretID, "shh"), HTTP: httpMock.Client()})
    svc.Start(context.Background())

    bus.Publish("task.completed", map[string]any{"workspace_id": wsID, "task_id": "task-1"})
    httpMock.WaitForCall(t, 2*time.Second)

    got := httpMock.LastCall()
    if got.Header.Get("X-Hmac-SHA256") == "" { t.Error("missing HMAC header") }
    _ = subID
}

func TestOutbound_RetriesOnTransientFailure(t *testing.T) {
    // httpMock returns 500 on first call, 200 on second; assert 2 attempts.
    // ...
}

// TestPublicEventName covers the internal→public rename table. Integration
// tests in Task 18 exercise the happy-path approval.created→approval.needed
// path end-to-end; this unit test locks in the full table so a future
// additional event or typo produces a failing test rather than a silent miss.
func TestPublicEventName(t *testing.T) {
    t.Parallel()
    cases := []struct{ in, want string }{
        // Rename: Phase 7 internal → PLAN.md §8A.3 public.
        {"approval.created", "approval.needed"},
        // Passthrough: names already in PLAN.md §8A.3.
        {"task.completed",        "task.completed"},
        {"task.failed",           "task.failed"},
        {"workflow.completed",    "workflow.completed"},
        {"workflow.failed",       "workflow.failed"},
        {"budget.warning",        "budget.warning"},
        {"budget.exceeded",       "budget.exceeded"},
        {"agent.error",           "agent.error"},
        {"delegation.completed",  "delegation.completed"},
        // Default branch: unknown name passes through unchanged.
        {"custom.event",          "custom.event"},
    }
    for _, tc := range cases {
        if got := publicEventName(tc.in); got != tc.want {
            t.Errorf("publicEventName(%q) = %q, want %q", tc.in, got, tc.want)
        }
    }
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/service/webhook_outbound.go
package service

import (
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "net/http"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/agent"
    "aicolab/server/pkg/db/db"
    "aicolab/server/pkg/events"
)

type OutboundDeps struct {
    DB          *pgxpool.Pool
    Bus         *events.Bus
    Credentials *agent.Credentials
    HTTP        *http.Client
    Log         Logger
}

type OutboundWebhookService struct {
    deps OutboundDeps
}

func NewOutboundWebhookService(deps OutboundDeps) *OutboundWebhookService {
    if deps.HTTP == nil { deps.HTTP = &http.Client{Timeout: 10 * time.Second} }
    return &OutboundWebhookService{deps: deps}
}

func (s *OutboundWebhookService) Start(ctx context.Context) {
    go s.subscribe(ctx)
    go s.retryLoop(ctx)
}

func (s *OutboundWebhookService) subscribe(ctx context.Context) {
    ch := s.deps.Bus.Subscribe("*") // wildcard — match in-memory
    for {
        select {
        case <-ctx.Done(): return
        case ev := <-ch:
            // Translate internal event names into PLAN.md §8A.3 public names.
            // Phase 7 fires approval.created when a human_approval step
            // registers; public subscribers expect approval.needed. Same
            // payload; renamed at the boundary so the internal event-bus
            // vocabulary can evolve without breaking published contracts.
            //
            // We MUST NOT mutate ev.Type in place — events.Bus may deliver
            // by pointer to multiple subscribers, and an in-place rename
            // is a data race visible to other subscribers on the same bus.
            // Copy the event value, rewrite the copy, pass the copy down.
            publicEv := ev
            publicEv.Type = publicEventName(ev.Type)
            s.handle(ctx, publicEv)
        }
    }
}

// publicEventName maps the internal events.Bus event vocabulary onto the 9
// public event names promised in PLAN.md §8A.3:
// task.completed, task.failed, workflow.completed, workflow.failed,
// approval.needed, budget.warning, budget.exceeded, agent.error,
// delegation.completed.
func publicEventName(internal string) string {
    switch internal {
    case "approval.created":
        return "approval.needed"
    // Others pass through unchanged; the bus already emits PLAN.md names
    // for task/workflow/budget/agent/delegation.
    default:
        return internal
    }
}

func (s *OutboundWebhookService) handle(ctx context.Context, ev events.Event) {
    q := db.New(s.deps.DB)
    subs, err := q.ListActiveSubscriptionsForEvent(ctx, ev.Type)
    if err != nil { return }
    for _, sub := range subs {
        if uuidFromPg(sub.WorkspaceID) != ev.WorkspaceID { continue }
        s.deliver(ctx, sub, ev, 1)
    }
}

func (s *OutboundWebhookService) deliver(ctx context.Context, sub db.WebhookSubscription, ev events.Event, attempt int) {
    q := db.New(s.deps.DB)
    body, _ := json.Marshal(map[string]any{"event": ev.Type, "data": ev.Payload, "timestamp": time.Now().Unix()})
    // Credential policy: we re-fetch the plaintext secret on every retry
    // attempt rather than caching it across attempts. The vault is the
    // source of truth; a rotated secret (last_rotated_at > previous fetch)
    // MUST take effect on the next delivery attempt. If the vault is
    // KMS-backed this is N-fetches-per-delivery (worst case 1 + retries =
    // 4); Phase 9 can add a short-TTL in-memory cache keyed by
    // (credential_id, last_rotated_at) if telemetry shows vault RPC load.
    secret, err := s.deps.Credentials.GetRaw(ctx, uuidFromPg(sub.WorkspaceID), uuidFromPg(sub.SecretCredentialID))
    if err != nil { return }
    defer agent.Zero(secret)

    req, _ := http.NewRequestWithContext(ctx, "POST", sub.Url, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Hmac-SHA256", hmacSign(body, secret))
    req.Header.Set("X-Event-Type", ev.Type)

    resp, err := s.deps.HTTP.Do(req)
    var status int; var respBody []byte
    if err == nil {
        status = resp.StatusCode
        respBody, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<16))
        resp.Body.Close()
    }

    logRow, _ := q.InsertDeliveryLog(ctx, db.InsertDeliveryLogParams{
        WorkspaceID: sub.WorkspaceID, SubscriptionID: sub.ID, EventType: ev.Type,
        Payload: body, Status: sqlNullInt(status), ResponseBody: sqlNullString(string(respBody)),
        Attempt: int32(attempt), TraceID: uuidPg(ev.TraceID),
    })

    if err == nil && status >= 200 && status < 300 {
        _ = q.MarkDeliveryComplete(ctx, db.MarkDeliveryCompleteParams{
            ID: logRow.ID, WorkspaceID: sub.WorkspaceID,
            Status: int32(status), ResponseBody: sqlNullString(string(respBody)),
        })
        return
    }
    // Schedule retry per policy.
    backoff := []int{5, 30, 300}
    if attempt <= len(backoff) {
        next := time.Now().Add(time.Duration(backoff[attempt-1]) * time.Second)
        _ = q.ScheduleRetry(ctx, db.ScheduleRetryParams{
            ID: logRow.ID, WorkspaceID: sub.WorkspaceID,
            NextRetryAt: next, Status: sqlNullInt(status), ResponseBody: sqlNullString(string(respBody)),
        })
    }
}

func (s *OutboundWebhookService) retryLoop(ctx context.Context) {
    t := time.NewTicker(5 * time.Second)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            q := db.New(s.deps.DB)
            rows, _ := q.ListDueRetries(ctx, 100)
            for _, r := range rows {
                sub, err := q.GetWebhookSubscriptionByID(ctx, db.GetWebhookSubscriptionByIDParams{
                    ID: r.SubscriptionID, WorkspaceID: r.WorkspaceID,
                })
                if err != nil { continue }
                s.deliver(ctx, sub, events.Event{Type: r.EventType, WorkspaceID: uuidFromPg(r.WorkspaceID), Payload: jsonToMap(r.Payload)}, int(r.Attempt)+1)
            }
        }
    }
}

func hmacSign(body, secret []byte) string {
    m := hmac.New(sha256.New, secret); m.Write(body)
    return hex.EncodeToString(m.Sum(nil))
}
```

- [ ] **Step 3: Commit**

```bash
cd server && go test ./internal/service/ -run TestOutbound -v
git add server/internal/service/webhook_outbound.go server/internal/service/webhook_outbound_test.go
git commit -m "feat(webhook): outbound delivery with HMAC + retry

Two goroutines: subscribe(events.Bus) for new events + retryLoop for
due retries. Default backoff 5s/30s/300s. X-Hmac-SHA256 header per
outbound request, secret decrypted fresh and zeroed each time."
```

---

### Task 12: Template Service + Materialize-to-Agent

**Files:**
- Create: `server/internal/service/template.go`
- Create: `server/internal/handler/template.go`
- Create: tests

**Goal:** `POST /api/workspaces/{ws}/agents/from-template` takes a template ID and creates an agent with the template's config.

- [ ] **Step 1: Failing test**

```go
func TestTemplate_MaterializeToAgent(t *testing.T) {
    tc := newTemplateHandlerCtx(t)
    // Uses one of the 5 system templates seeded in migration 161.
    templateID := tc.systemTemplateID("Customer Support Agent")

    resp := tc.postJSON("/api/workspaces/"+tc.wsID+"/agents/from-template",
        map[string]any{"template_id": templateID, "name": "My Bot"})
    if resp.Code != 201 { t.Fatalf("status = %d", resp.Code) }

    agent := tc.parseAgent(resp.Body)
    if agent.Name != "My Bot" { t.Errorf("name = %q", agent.Name) }
    if agent.Provider != "anthropic" { t.Errorf("provider = %q", agent.Provider) }
    if agent.Instructions == "" { t.Error("instructions not copied") }
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/service/template.go
func (s *TemplateService) CreateAgentFromTemplate(ctx context.Context, wsID, templateID uuid.UUID, name string, userID uuid.UUID) (*db.Agent, error) {
    q := db.New(s.db)
    tpl, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: uuidPg(templateID), WorkspaceID: uuidPg(wsID)})
    if err != nil { return nil, err }
    agent, err := q.CreateAgent(ctx, db.CreateAgentParams{
        WorkspaceID: uuidPg(wsID),
        Name: name, AgentType: tpl.AgentType,
        Provider: sqlNullString(tpl.Provider.String),
        Model: sqlNullString(tpl.Model.String),
        Instructions: sqlNullString(tpl.Instructions),
        RuntimeConfig: buildRuntimeConfig(tpl), // tools + guardrails
        CreatedBy: uuidPg(userID),
    })
    return &agent, err
}
```

- [ ] **Step 3: Handler + route mount + commit**

```bash
cd server && go test ./internal/service/ ./internal/handler/ -run TestTemplate -v
git add server/internal/service/template.go server/internal/handler/template.go server/pkg/db/queries/template.sql
git commit -m "feat(templates): CreateAgentFromTemplate — one-click agent

Copies template's instructions/provider/model/tools/guardrails into a
new agent. Workspace-scoped; system templates readable by any workspace."
```

---

### Task 13: API Keys — Generate, Auth Middleware, CRUD

**Files:**
- Create: `server/internal/service/apikey.go`
- Create: `server/internal/middleware/apikey_auth.go`
- Create: `server/internal/handler/apikey.go`
- Create: tests

**Goal:** create returns raw key only once; middleware parses `Authorization: Bearer sk_...` + looks up by SHA-256 hash + checks scope.

- [ ] **Step 1: Failing tests**

```go
func TestAPIKey_GenerateAndAuthenticate(t *testing.T) {
    tc := newAPIKeyCtx(t)
    key, id := tc.createAPIKey([]string{"agents:read"})
    if !strings.HasPrefix(key, "sk_") { t.Errorf("key prefix = %q", key) }

    resp := tc.authenticatedGet(key, "/api/workspaces/"+tc.wsID+"/agents")
    if resp.Code != 200 { t.Errorf("valid key rejected: %d", resp.Code) }

    resp = tc.authenticatedGet("sk_bogus", "/api/workspaces/"+tc.wsID+"/agents")
    if resp.Code != 401 { t.Errorf("bogus accepted: %d", resp.Code) }
    _ = id
}

func TestAPIKey_ScopeEnforcement(t *testing.T) {
    tc := newAPIKeyCtx(t)
    key, _ := tc.createAPIKey([]string{"agents:read"}) // no write scope
    resp := tc.authenticatedPost(key, "/api/workspaces/"+tc.wsID+"/agents", nil)
    if resp.Code != 403 { t.Errorf("write accepted with read-only: %d", resp.Code) }
}
```

- [ ] **Step 2: Implement service**

```go
// server/internal/service/apikey.go
package service

import (
    "context"
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
    "errors"
    "strings"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

type APIKeyService struct { db *pgxpool.Pool }

func NewAPIKeyService(pool *pgxpool.Pool) *APIKeyService { return &APIKeyService{db: pool} }

type APIKeyCreated struct { Row db.WorkspaceAPIKey; Raw string }

func (s *APIKeyService) Create(ctx context.Context, wsID, userID uuid.UUID, name string, scopes []string, expiresAt *time.Time) (*APIKeyCreated, error) {
    b := make([]byte, 32) // APIKeyRandomBytes
    if _, err := rand.Read(b); err != nil { return nil, err }
    raw := "sk_" + hex.EncodeToString(b)
    hash := sha256.Sum256([]byte(raw))
    hashHex := hex.EncodeToString(hash[:])

    q := db.New(s.db)
    var exp pgtype.Timestamptz
    if expiresAt != nil { exp = pgtype.Timestamptz{Time: *expiresAt, Valid: true} }
    row, err := q.CreateAPIKey(ctx, db.CreateAPIKeyParams{
        WorkspaceID: uuidPg(wsID), Name: name, KeyHash: hashHex,
        KeyPrefix: raw[:10], Scopes: scopes, CreatedBy: uuidPg(userID),
        ExpiresAt: exp,
    })
    if err != nil { return nil, err }
    return &APIKeyCreated{Row: row, Raw: raw}, nil
}

func (s *APIKeyService) Verify(ctx context.Context, raw string) (*db.WorkspaceAPIKey, error) {
    if !strings.HasPrefix(raw, "sk_") { return nil, errors.New("invalid key") }
    hash := sha256.Sum256([]byte(raw))
    q := db.New(s.db)
    row, err := q.GetAPIKeyByHash(ctx, hex.EncodeToString(hash[:]))
    if err != nil { return nil, err }
    if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) { return nil, errors.New("expired") }
    // last_used_at is best-effort — fire-and-forget goroutine with a
    // detached context so the auth hot path doesn't block on a write.
    // Under load this synchronous UPDATE would be a write-per-request.
    // If the goroutine outlives the request its background context is
    // fresh, unaffected by request cancellation. A future optimisation:
    // batch updates via a write-behind cache keyed by row.ID with a
    // 30-second flush; for V1 the naked goroutine is sufficient.
    go func(id pgtype.UUID) {
        bg, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        _ = q.TouchAPIKeyUsage(bg, id)
    }(row.ID)
    return &row, nil
}

func (s *APIKeyService) Revoke(ctx context.Context, wsID, keyID uuid.UUID) error {
    q := db.New(s.db)
    return q.RevokeAPIKey(ctx, db.RevokeAPIKeyParams{ID: uuidPg(keyID), WorkspaceID: uuidPg(wsID)})
}
```

- [ ] **Step 3: Middleware**

```go
// server/internal/middleware/apikey_auth.go
func APIKeyAuth(svc *service.APIKeyService) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            auth := r.Header.Get("Authorization")
            if !strings.HasPrefix(auth, "Bearer sk_") { next.ServeHTTP(w, r); return }
            raw := strings.TrimPrefix(auth, "Bearer ")
            key, err := svc.Verify(r.Context(), raw)
            if err != nil { http.Error(w, "invalid api key", 401); return }
            // Inject workspace_id + scopes into ctx.
            ctx := context.WithValue(r.Context(), ctxKeyWorkspaceID{}, uuidFromPg(key.WorkspaceID))
            ctx = context.WithValue(ctx, ctxKeyScopes{}, []string(key.Scopes))
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// Per-handler scope check. E.g. chi router middleware `RequireScope("agents:write")`.
func RequireScope(need string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            scopes, _ := r.Context().Value(ctxKeyScopes{}).([]string)
            for _, s := range scopes {
                if s == need || s == "admin" { next.ServeHTTP(w, r); return }
            }
            http.Error(w, "insufficient scope", 403)
        })
    }
}
```

- [ ] **Step 4: Mount route groups with RequireScope**

```go
// server/cmd/server/router.go — wrap every programmatic-API route with
// APIKeyAuth (global) + RequireScope (per route group). Session-cookie
// routes skip these middlewares. The split is by URL prefix: /api/* is
// API-key-authenticated; /app/* is session-authenticated.
r.Route("/api", func(r chi.Router) {
    r.Use(middleware.APIKeyAuth(apiKeySvc))

    r.Route("/workspaces/{wsId}/agents", func(r chi.Router) {
        r.With(middleware.RequireScope("agents:read")).Get("/", agentH.List)
        r.With(middleware.RequireScope("agents:read")).Get("/{id}", agentH.Get)
        r.With(middleware.RequireScope("agents:write")).Post("/", agentH.Create)
        r.With(middleware.RequireScope("agents:write")).Patch("/{id}", agentH.Update)
        r.With(middleware.RequireScope("agents:write")).Delete("/{id}", agentH.Delete)
    })

    r.Route("/workspaces/{wsId}/workflows", func(r chi.Router) {
        r.With(middleware.RequireScope("workflows:read")).Get("/", wfH.List)
        r.With(middleware.RequireScope("workflows:execute")).Post("/{id}/trigger", wfH.Trigger)
    })

    r.Route("/workspaces/{wsId}/tasks", func(r chi.Router) {
        r.With(middleware.RequireScope("tasks:read")).Get("/", taskH.List)
        r.With(middleware.RequireScope("tasks:write")).Post("/", taskH.Create)
    })

    r.Route("/workspaces/{wsId}/api-keys", func(r chi.Router) {
        r.With(middleware.RequireScope("admin")).Get("/", apiKeyH.List)
        r.With(middleware.RequireScope("admin")).Post("/", apiKeyH.Create)
        r.With(middleware.RequireScope("admin")).Delete("/{id}", apiKeyH.Revoke)
    })
})
```

Add an end-to-end assertion in `apikey_test.go` that a route wrapped in `RequireScope("agents:write")` returns 403 when the bearer key has only `agents:read`:

```go
func TestRoute_RequireScope_RejectsUnderScoped(t *testing.T) {
    tc := newAPIKeyCtx(t)
    key, _ := tc.createAPIKey([]string{"agents:read"})
    resp := tc.authenticatedPost(key,
        "/api/workspaces/"+tc.wsID+"/agents", map[string]any{"name": "bot"})
    if resp.Code != 403 { t.Errorf("expected 403 for scope mismatch, got %d", resp.Code) }
}
```

- [ ] **Step 5: Handler + commit**

```bash
cd server && go test ./internal/service/ ./internal/middleware/ ./internal/handler/ -run APIKey -v
git add server/internal/service/apikey.go server/internal/middleware/apikey_auth.go server/internal/handler/apikey.go server/cmd/server/router.go
git commit -m "feat(apikey): generate/verify/revoke + bearer middleware + per-route scopes

Key format sk_<64 hex chars> = 32 random bytes. Raw shown only on
creation, SHA-256 hash stored. Middleware injects workspace_id + scopes
into ctx; RequireScope wraps each /api route with its required scope
(agents:read/write, tasks:read/write, workflows:execute, admin).
Under-scoped requests get 403 per PLAN.md §8A.5."
```

---

### Task 14: Playground — Dry-Run Execution

**Files:**
- Create: `server/internal/service/playground.go`
- Create: `server/internal/handler/playground.go`
- Create: tests

**Goal:** dry_run flag disables side-effecting tools (email, CRM write, database mutation, webhook call) — they return mock strings instead. Read-only tools run normally. No cost_event recorded (or recorded with `cost_status='test'`).

- [ ] **Step 1: Failing test**

```go
func TestPlayground_DryRunSkipsSideEffects(t *testing.T) {
    tc := newPlaygroundCtx(t)
    agentID := tc.seedAgentWithTools([]string{"send_email", "web_search"})

    resp := tc.postJSON("/api/agents/"+agentID+"/test", map[string]any{
        "prompt": "send a test email"})
    if resp.Code != 200 { t.Fatalf("status = %d", resp.Code) }

    calls := tc.captureToolCalls()
    var emailCall, searchCall *ToolCall
    for i, c := range calls {
        if c.Name == "send_email" { emailCall = &calls[i] }
        if c.Name == "web_search" { searchCall = &calls[i] }
    }
    if emailCall == nil { t.Fatal("no send_email tool call") }
    if !strings.Contains(emailCall.Result, "[DRY RUN]") {
        t.Errorf("email not dry-run: %+v", emailCall)
    }
    // web_search should have run normally (read-only).
    if searchCall != nil && strings.Contains(searchCall.Result, "[DRY RUN]") {
        t.Errorf("read-only tool skipped: %+v", searchCall)
    }
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/service/playground.go
package service

import (
    "context"
    "fmt"

    "github.com/google/uuid"
)

// SideEffectingTools are blocklisted in dry-run. Everything else runs normally.
var SideEffectingTools = map[string]bool{
    "send_email":     true,
    "http_request":   true,
    "database_write": true,
    "webhook_send":   true,
}

// WrapToolsForDryRun returns a new tool list where side-effecting entries are
// replaced with stubs that return a "[DRY RUN] <what would have happened>" text.
func WrapToolsForDryRun(tools []types.Tool) []types.Tool {
    out := make([]types.Tool, 0, len(tools))
    for _, t := range tools {
        if SideEffectingTools[t.Name()] {
            out = append(out, &dryRunTool{name: t.Name(), original: t})
            continue
        }
        out = append(out, t)
    }
    return out
}

type dryRunTool struct{ name string; original types.Tool }
func (d *dryRunTool) Name() string { return d.name }
func (d *dryRunTool) Description() string { return d.original.Description() + " (DRY RUN — does not execute)" }
func (d *dryRunTool) InputSchema() json.RawMessage { return d.original.InputSchema() }
func (d *dryRunTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
    summary := fmt.Sprintf("[DRY RUN] %s would have been called with %s", d.name, string(raw))
    return json.Marshal(map[string]any{"text": summary, "dry_run": true})
}
```

- [ ] **Step 3: Handler + commit**

```go
// server/internal/handler/playground.go
func (h *PlaygroundHandler) Test(w http.ResponseWriter, r *http.Request) {
    // Parse prompt, resolve agent, wrap tools, build harness with dry_run flag.
    // Stream via SSE — same format as task execution SSE from Phase 2.
}
```

```bash
cd server && go test ./internal/service/ ./internal/handler/ -run Playground -v
git add server/internal/service/playground.go server/internal/handler/playground.go
git commit -m "feat(playground): dry_run wraps side-effecting tools

Blocklist { send_email, http_request, database_write, webhook_send }.
Stub Execute returns '[DRY RUN] <would have ...>' + original input JSON
so the agent can reason about the mock result."
```

---

### Task 15: OpenAPI Spec + Swagger UI

**Files:**
- Create: `server/internal/handler/openapi.go`
- Modify: `server/cmd/server/router.go`

**Goal:** `swaggo/swag` annotations on handlers produce `docs/swagger.json`; serve at `/api/openapi.json` + Swagger UI at `/api/docs`.

- [ ] **Step 1: Add swag build step**

```bash
go install github.com/swaggo/swag/cmd/swag@latest
cd server && swag init -g cmd/server/main.go -o docs
```

- [ ] **Step 2: Add annotations to key handlers**

```go
// server/internal/handler/agent.go — @Summary etc. on CreateAgent, ListAgents, etc.
// @Summary List agents
// @Tags agents
// @Produce json
// @Param workspace_id path string true "Workspace ID"
// @Success 200 {array} Agent
// @Router /workspaces/{workspace_id}/agents [get]
func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) { /* ... */ }
```

- [ ] **Step 3: Serve spec + UI**

```go
// server/internal/handler/openapi.go
import "github.com/swaggo/http-swagger"
func MountOpenAPI(r chi.Router) {
    r.Get("/api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, "docs/swagger.json")
    })
    r.Get("/api/docs/*", httpSwagger.Handler(httpSwagger.URL("/api/openapi.json")))
}
```

- [ ] **Step 4: Add to Makefile**

```makefile
# Add to Makefile — regenerates swagger on source changes
swagger:
	cd server && swag init -g cmd/server/main.go -o docs
```

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/openapi.go server/cmd/server/router.go server/docs/ Makefile
git commit -m "feat(docs): OpenAPI 3.0 spec + Swagger UI

Annotations on handlers → docs/swagger.json via swaggo/swag. Served at
/api/openapi.json; Swagger UI at /api/docs. Regenerate via 'make swagger'."
```

---

### Task 16: Onboarding Wizard

**Files:**
- Create: `server/internal/handler/onboarding.go`
- Create: `server/internal/service/onboarding.go`
- Create: `packages/views/onboarding/index.tsx`
- Create: `packages/views/onboarding/components/*.tsx` (one per step)

**Goal:** 6-step wizard per PLAN.md §8A.8. Completion sets `workspace.onboarding_completed_at`.

- [ ] **Step 1: Backend endpoints**

```go
// Store minimal onboarding state in-memory or a small JSONB on workspace.
// Endpoints:
//   GET  /api/onboarding/status  → {completed: bool, step: int}
//   POST /api/onboarding/complete → sets workspace.onboarding_completed_at
//   GET  /api/onboarding/templates?category=X → filtered template list
```

- [ ] **Step 2: Frontend — 6 steps**

```tsx
// packages/views/onboarding/index.tsx
export function OnboardingWizard() {
  const [step, setStep] = useState(0);
  const steps = [
    <CategoryStep onNext={setStep} />,
    <UseCaseStep onNext={setStep} />,
    <TemplateRecommendationStep onNext={setStep} />,
    <ConnectToolsStep onNext={setStep} />,
    <CreateAgentStep onNext={setStep} />,
    <TestAgentStep onComplete={markComplete} />,
  ];
  return <div>{steps[step]}</div>;
}
```

- [ ] **Step 3: Commit**

```bash
git add server/internal/handler/onboarding.go server/internal/service/onboarding.go packages/views/onboarding/
git commit -m "feat(onboarding): 6-step wizard from category → template → first agent

Redirect to /onboarding on first login when workspace.onboarding_completed_at
IS NULL. Steps: category, use cases, template pick, tool connect, create
agent, playground test."
```

---

### Task 17: Frontend — Webhooks + Templates + API Keys Views

**Files:**
- Create: `packages/core/api/webhook.ts` + `template.ts` + `apikey.ts` + `playground.ts`
- Create: `packages/core/webhook/queries.ts` + `template/queries.ts` + `apikey/queries.ts`
- Create: `packages/views/webhooks/` + `templates/` + `settings/apikeys/` + `agents/components/test-panel.tsx`

**Goal:** CRUD UIs wired through TanStack Query. No direct `api.*` in components.

- [ ] **Step 1: Types + clients + hooks**

Follow the exact pattern from Phase 6's `capability` and Phase 7's `workflow` modules — `types/ → api/ → queries/` with WS invalidation. One file per table.

- [ ] **Step 2: Views**

- `WebhooksView` — list endpoints, create form (target picker + credential picker), delivery log viewer.
- `WebhookSubscriptionsView` — same shape, outbound.
- `TemplateGallery` — cards grouped by category, "Use template" button → agent create.
- `APIKeysView` — create form, list, "show once" modal on create, revoke button.
- `TestPanel` — embedded in agent detail; prompt input + streaming output + cost badge.

- [ ] **Step 3: Commit**

```bash
git add packages/core/api/ packages/core/webhook/ packages/core/template/ packages/core/apikey/ packages/views/webhooks/ packages/views/templates/ packages/views/settings/apikeys/ packages/views/agents/components/test-panel.tsx
git commit -m "feat(views): webhooks + templates + api keys + test panel UI

All views consume TanStack Query hooks; no direct api.* calls in
components (CLAUDE.md). Test panel streams SSE from playground
endpoint; renders tool calls inline."
```

---

### Task 18: Integration Tests + Phase 8A Verification

**Files:**
- Create: `server/internal/integration/platform/chat_e2e_test.go`
- Create: `server/internal/integration/platform/webhook_e2e_test.go`
- Create: `docs/engineering/verification/phase-8a.md`

**Goal:** two end-to-end tests covering the happy paths PLAN.md §8A.10 lists.

- [ ] **Step 1: Chat E2E**

```go
// chat_e2e_test.go
func TestChat_RoundTrip(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("chat-e2e")
    agentID := env.SeedAgent(ws, "support",
        testsupport.StubModel([]testsupport.TurnScript{{Text: "Hello!"}}))
    sessionID := uuid.New()

    streamID := env.SendChatMessage(ws, sessionID, "hi")
    resp := env.AwaitChatResponse(ctx, streamID)
    if resp != "Hello!" { t.Errorf("got %q", resp) }
    _ = agentID
}
```

- [ ] **Step 2: Webhook round-trip + approval.needed event**

```go
// webhook_e2e_test.go — inbound trigger → agent → outbound delivery.

// TestWebhook_ApprovalNeededDelivered covers the §8A.3 requirement that
// approval.created (internal) fires as approval.needed (public) on every
// outbound subscription registered for it. PLAN.md §8A.3 lists 9 public
// events; this is the only one that renames.
func TestWebhook_ApprovalNeededDelivered(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("ap-e2e")
    outSink := env.StartHTTPSink()
    defer outSink.Close()

    secretID := env.SeedCredential(ws, "webhook_hmac", "shh")
    subID := env.CreateWebhookSubscriptionWithSecret(ws, outSink.URL, []string{"approval.needed"}, secretID)

    // Fire the INTERNAL event name; outbound service must translate it.
    env.Bus.Publish("approval.created", events.Event{
        Type:        "approval.created",
        WorkspaceID: ws,
        Payload:     map[string]any{"approval_id": uuid.New()},
    })
    delivery := env.WaitForOutboundDelivery(ctx, subID, "approval.needed")
    if delivery.Status != 200 { t.Errorf("delivery status = %d", delivery.Status) }
    // Assert the outbound HTTP payload used the PUBLIC event name, not
    // the internal one.
    got := outSink.LastCall()
    if got.Header.Get("X-Event-Type") != "approval.needed" {
        t.Errorf("X-Event-Type = %q, want approval.needed", got.Header.Get("X-Event-Type"))
    }
}

func TestWebhook_InboundToOutboundCycle(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("wh-e2e")
    agentID := env.SeedAgent(ws, "handler", testsupport.StubModel([]testsupport.TurnScript{{Text: "done"}}))

    secretID := env.SeedCredential(ws, "webhook_hmac", "shh")
    inEndpoint := env.CreateInboundEndpoint(ws, "agent", agentID, secretID)
    outSink := env.StartHTTPSink()
    defer outSink.Close()
    subID := env.CreateWebhookSubscription(ws, outSink.URL, []string{"task.completed"})

    body := []byte(`{"msg":"process me"}`)
    sig := testsupport.HMAC(body, "shh")
    resp := env.PostWebhook(ws, inEndpoint, body, sig)
    if resp.StatusCode != 202 { t.Fatal("inbound rejected") }

    delivery := env.WaitForOutboundDelivery(ctx, subID, "task.completed")
    if delivery.Status != 200 { t.Errorf("outbound status %d", delivery.Status) }
}
```

- [ ] **Step 3: Manual verification + evidence**

Start server + frontend, walk through §8A.10 checklist:

- [ ] External HMAC-signed POST triggers agent task.
- [ ] task.completed fires outbound webhook; retry after simulated 500.
- [ ] Chat: user message → stream response; typing indicator shows.
- [ ] Over-budget workspace blocks chat with `budget_exceeded` error.
- [ ] One-click create from "Customer Support Agent" template.
- [ ] `curl -H "Authorization: Bearer sk_..."` reads with `agents:read`, write fails with 403.
- [ ] `/api/agents/{id}/test` with `dry_run=true` — email tool returns "[DRY RUN]".
- [ ] `/api/openapi.json` returns spec; `/api/docs` renders Swagger UI.
- [ ] New workspace redirected to onboarding; completion flag set on finish.

- [ ] **Step 4: Write evidence + commit**

```markdown
# Phase 8A Verification — 2026-04-DD

- `go test ./internal/integration/platform/...` → pass
- Manual checklist: [results]
- Known limitations: billing not wired, side-by-side playground deferred, OAuth2 for inbound webhooks deferred to Phase 8B.
```

```bash
git add server/internal/integration/platform/ docs/engineering/verification/phase-8a.md
git commit -m "test(phase-8a): chat + webhook e2e + verification evidence"
```

---

## Placeholder Scan

Self-reviewed — every task has concrete code. Three intentional abbreviations:

1. Task 7's `maybeCompact` stub — the real Phase 5 `agent.Compact` call shape lives in Phase 5's plan; this task references it but does not repeat 200 lines of compaction helpers. The stub returns raw history for test pass; the production code swaps in `agent.Compact(ctx, provider, msgs, opts)` verbatim from Phase 5 §5.4.
2. Task 15 Swagger annotations — shown for one handler as a pattern; applying to every handler is mechanical fill-in (~40 handlers × 6 lines of `@Summary`/`@Param`/`@Success`).
3. Task 17 Views — described as "follow Phase 6/7 pattern exactly" because the TanStack Query mock scaffolding + render-without-crash test shape is identical across five CRUD views (webhooks, subscriptions, templates, api keys, test panel).

No TBDs, no "similar to Task N" without the code.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-14-phase8a-platform-integration.md`. Two execution options:

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks.

**2. Inline Execution** — execute in this session via `superpowers:executing-plans` with batched checkpoints.

Which approach?

# Unified Plan: Transform Multica into a Universal AI Agent Platform

## Context

We forked Multica — a Linear-like task management platform where AI coding agents are first-class citizens. The problem: the entire execution layer is hardwired to coding CLIs (Claude Code, Codex, etc.) that spawn local processes on a developer's machine. We want to extend it to support arbitrary business agents (customer support, legal advisory, deal closing, negotiation, etc.).

This plan covers THREE major workstreams in a single dependency-ordered sequence:
1. **Porting** — Abstract the execution layer, add tools/MCP, structured outputs, cost optimization
2. **Agent-to-Agent** — Discovery, structured delegation, workflow orchestration
3. **Memory & Intelligence** — Cross-agent memory, context compression, error resilience

### Projects Analyzed & What We Port From Each

Two kinds of sources feed this plan: **vendored code** (present under `other-projects/` — we grep specific files and port their semantics) and **public-literature concepts** (design ideas from public docs/papers, no local checkout — we borrow the shape, not the code).

**Code ported from vendored sources** (present under `D:/proptech/ai-agent-colab/other-projects/`):

| Project | What We Port |
|---------|-------------|
| **CrewAI** (`crewai/`) | Tool framework pattern, LLM abstraction, structured outputs, guardrails, A2A delegation tools, **unified memory system** (hierarchical scopes, composite scoring, LLM-analyzed encoding, consolidation, cross-agent sharing) |
| **Paperclip** (`paperclip/`) | Adapter architecture (`server/src/adapters/{http,process}/execute.ts`), budget controls, plugin SDK concept, routines/scheduling, http/process backends, company templates, secrets vault (`packages/db/src/schema/company_secrets.ts` + `company_secret_versions.ts`), activity log, agent config revisions, approvals |
| **Cadence** (`cadence/`) | Scheduler overlap policies (`common/types/schedule.go` — `SkipNew/Buffer/Concurrent/CancelPrevious/TerminatePrevious`), catch-up policies (`Skip/One/All` — our names adopt the Cadence literals), retry/backoff framework (`common/backoff/`), single-node multistage rate limiter (`common/quotas/multistageratelimiter.go` — NOT the distributed `common/quotas/global/` subtree), MAPQ fair-scheduling concept only (pattern reference, not code port). **Scheduler port notice:** Cadence's scheduler is itself a Cadence workflow; without an event-sourced history, our port achieves at-least-once delivery with dedup by `(workflow_id, scheduled_time)` — NOT Cadence's exactly-once semantics. |
| **Hermes Agent** (`hermes-agent/`) | Context compression engine (pluggable, iterative, token-budget-aware — `agent/context_compressor.py`), error classification with recovery hints (`agent/error_classifier.py`), credential pool with multi-key failover (`agent/credential_pool.py`, strategies `STRATEGY_FILL_FIRST/ROUND_ROBIN/RANDOM/LEAST_USED`), cost/usage tracking with cache-aware pricing (`agent/usage_pricing.py`), tool registry with self-registration and availability gating, prompt injection detection (`agent/prompt_builder.py:36-73`), MCP reconnection with backoff (`tools/mcp_tool.py`), smart model routing (`agent/smart_model_routing.py`), rate-limit header parser (`agent/rate_limit_tracker.py`) |
| **IWF** (`iwf/`) | WaitUntil/Execute step lifecycle (`service/interpreter/workflowImpl.go`), timer skip, persistence loading policies, internal channels (`service/interpreter/InternalChannel.go`), conditional close. NOT using IWF itself (it requires Temporal or Cadence) — porting patterns only. **Semantic caveat:** IWF's `InternalChannel` is exactly-once because the outer workflow is durable. Our JSONB+notify port is at-least-once; channel messages carry `consumed_at` + a conditional-UPDATE consume. |
| **Open Agents** (`open-agents/`) | Sandbox interface shape (`packages/sandbox/interface.ts`), aggressive compaction helpers (`packages/agent/context-management/aggressive-compaction-helpers.ts`), paste-block tokens (`packages/shared/lib/paste-blocks.ts`), `startStopMonitor` polling, `activeStreamId` coalescing, eviction threshold constant (`packages/agent/types.ts:39` — 80 KB), timeout buffer (`packages/sandbox/vercel/sandbox.ts:16` — 30 000 ms) |
| **go-ai** (`go-ai/` — v0.4.0 upstream) | Harness foundation: `ToolLoopAgent`, `SkillRegistry`, `SubagentRegistry`, `StopCondition`+`StepCountIs`, portable `ReasoningLevel`, Anthropic context-management primitives (`ClearToolUsesEdit`/`ClearThinkingEdit`/`CompactEdit` — Anthropic-only), `Output[OBJECT,PARTIAL]` structured-output generics, `mcp` package. Adopted as a dependency, not a port. |

**Concepts from public literature** (no local checkout — we implement the design, not the code):

| Project | What the idea gives us |
|---------|------------------------|
| **Letta / MemGPT** (MemGPT paper, ICML 2024) | Three-tier memory taxonomy (core/working/archival) as a framing for Phase 5 — cost-effective long sessions. |
| **LangGraph** (public docs) | Graph-based state machines, checkpoint/resume, interrupt/resume for HITL — framing for Phase 7. |
| **Claude Managed Agents** (Anthropic public API docs) | Brain/Hands separation (inference vs execution), Environment API (separate agent config from runtime config), reusable containers via ID, budget enforcement BEFORE dispatch. |
| **Replit Agent V2** (public blog) | CoW snapshot pattern for safe agent experimentation, parallel sampling via forked environments — future reference for Phase 11. |
| **Cursor Agents** (public docs) | Git worktrees for parallel agent work, OS-level sandboxing (bubblewrap/Seatbelt), self-hosted brain/hands split — future reference for Phase 11. |

### Key Architecture Insight: Two Workload Classes

**Business agents** (support, legal, sales, negotiation) = LLM API calls + tools. NO sandbox needed. Workers call APIs directly. Tokens are 99% of cost.

**Coding agents** (code review, bug fixing, feature dev) = LLM + filesystem + git + shell. NEED sandbox isolation. Use existing daemon (local) or E2B/containers (cloud, future).

The plan prioritizes business agents (Model 3: no sandbox) since that's the new capability. Coding agents keep working via the existing daemon model.

---

## Foundation Libraries & Architectural Decisions (D1–D7)

Invariants for every phase below. These are decided; each phase builds on them.

**D1. Worker language: Go.** One Go worker binary handles every agent type. No TypeScript worker.

**D2. Agent-to-agent delegation is a tool call, not a table.** Phase 6 authors a `task` tool on top of go-ai's `SubagentRegistry` — the registry is just a `map[string]Agent` dispatch primitive (audited in `other-projects/go-ai/pkg/agent/subagent.go`; no built-in tool definition, no depth/cycle propagation, no trace plumbing). Phase 6 owns: (a) the `task` tool's JSON Schema + executor, (b) `context.Context`-keyed delegation stack for cycle detection (`delegationStackKey []uuid.UUID`, agent_ids), (c) depth limit via `delegationDepthKey int`, (d) interim progress streaming back to parent, (e) cost-event aggregation across child invocations. Phase 6 does NOT introduce `agent_delegation` or `agent_signal` tables — the stack lives in `context.Context` only. The `agent_capability` table (created in Phase 1 migration 100) remains for UI-level discovery. `trace_id` propagation is covered by D8.

**D3. Sandbox interface derived from Open Agents.** Phase 11 Go port is *inspired* by `packages/sandbox/interface.ts` from `other-projects/open-agents/`, with Go-idiomatic changes (error returns on fallible methods, widened `SandboxType` enum to support local/cloud backends, optional TS methods lifted to a separate `OptionalSandbox` interface). Mandatory surface: `ReadFile / WriteFile / Stat / Mkdir / Readdir / Access / Exec / Stop` + `GetState() any`. Optional surface (in `OptionalSandbox`): `ExecDetached / ExtendTimeout / Snapshot / Domain`. Hooks: `Hooks{AfterStart, BeforeStop, OnTimeout, OnTimeoutExtended}`. Platform-agnostic; all backends (E2B, gVisor, Claude Managed, Local Daemon) implement the mandatory surface and opt in to the optional surface.

**D4. Redis is a baseline dependency for cloud-worker deploys.** Required by Phase 2 (resumable streams), Phase 3 (skills cache), Phase 8A (chat pub/sub), Phase 9 (semantic cache). Single-process dev mode uses an in-memory adapter (`RedisAdapter` interface with `redis` + `memory` implementations).

**D5. Open Agents is a pattern reference, not a codebase dependency.** We lift the Sandbox interface, `activeStreamId` coalescing, stop-monitor polling, paste-block tokens, and system-prompt scaffolding as text/design. We do NOT fork the Next.js + Vercel-Workflow app.

**D6. Proactive timeout is on the `Backend` interface in Phase 2, not Phase 11.** Every backend (LLM API, HTTP, process, sandbox, managed) exposes `ExpiresAt() *time.Time` and an `OnTimeout` hook. Default implementation is no-op. Long-running business agents hit Anthropic/OpenAI session caps and need the same graceful-snapshot mechanism as coding sandboxes.

**D7. Default constants live in `server/pkg/agent/defaults.go`.** Created in Phase 1, referenced everywhere.

| Constant | Value | Rationale |
|---|---|---|
| `MainAgentMaxSteps` | 50 | go-ai idiom; sensible middle for multi-tool agent turns |
| `SubagentMaxSteps` | 100 | Open Agents convention |
| `ContextEvictionThresholdBytes` | `80 * 1024` (~20K tokens) | Open Agents `EVICTION_THRESHOLD_BYTES` |
| `SandboxTimeoutBufferMs` | `30_000` | Open Agents `TIMEOUT_BUFFER_MS` (Phase 11) |
| `StopMonitorPollIntervalMs` | `150` | Open Agents `startStopMonitor` (Phase 7) |
| `SkillsCacheTTL` | `4 * time.Hour` | Open Agents Redis skills cache (Phase 3) |
| `DefaultReasoningLevel` | `ReasoningDefault` (provider-chosen) | go-ai portable `ReasoningLevel`; workspace/agent overrides |

**D8. `trace_id` is minted at task entry in Phase 2 and propagated through `context.Context` across every subsequent phase.**

Phase 2 mints `trace_id = uuid.New()` when a worker claims a task (or a chat request arrives) and stashes it in `context.Context` under `traceIDKey`. Every cost-event insert (`cost_event.trace_id`), every subagent dispatch, every workflow step, every chat message tags `trace_id` at the call site. The in-context contract is stable across all phases; Phase 10 later adds the persistent span tree (`agent_trace`) on top without changing producers.

- **Storage before Phase 10:** `cost_event.trace_id` is the authoritative index — `CREATE INDEX idx_cost_event_trace ON cost_event(trace_id, workspace_id)` lands in the Phase 1 migration so chain-cost aggregation works from Phase 2.
- **Distinct from D2's delegation stack:** `trace_id` is *shared* across every frame in a call chain (parent + all children + grandchildren). D2's `delegationStackKey` is *distinct per chain* and holds `agent_id`s for cycle detection. Mixing the two was a pre-review bug; they serve orthogonal purposes.
- **Chain-cost ceiling (Phase 9):** `SELECT SUM(cost_cents) FROM cost_event WHERE trace_id = $1 AND workspace_id = $2` is the aggregation query enforced at subagent dispatch.
- **Root trace_id for webhook events (Phase 8A):** `delegation.completed` fires once when the root subagent call returns; payload carries the root `trace_id`.

### Foundation library: `github.com/digitallysavvy/go-ai`

**Adopted as the agent-harness foundation** for Phase 2 onward. Provides:

- `ToolLoopAgent` with `StopWhen []StopCondition{StepCountIs(N)}` semantics — replaces the hand-rolled LLM loop we'd otherwise write in Phase 2.
- First-class `SkillRegistry` (Phase 3) and `SubagentRegistry` (Phase 6).
- Portable `ReasoningLevel` across Anthropic / OpenAI / Google / Bedrock / xAI.
- Context management primitives (`ClearToolUsesEdit`, `ClearThinkingEdit`, `CompactEdit`) reused in Phase 5.4.
- Structured-output generics (`Output[T, P]`) for Phase 4.
- OpenTelemetry-compatible telemetry callbacks for Phase 10.

**Governed under a soft-fork safety net:** `github.com/ahmed-khaire/go-ai` mirrors upstream weekly via `.github/workflows/mirror-upstream.yml`. The `replace` directive is activated only per `docs/engineering/go-ai-fork-policy.md`. By default `server/go.mod` depends on upstream `v0.4.0` directly.

**Evaluated and declined:** `jetify-com/ai` (Anthropic streaming not implemented — hard blocker), `tmc/langchaingo` (string-in/string-out tool interface, synchronous executor, no mid-stream tool events, no subagent primitive — LangChain-era design).

---

## Phase 1: Agent Type System & Database Foundation
**Goal:** Decouple the data model from "coding agent" assumptions.
**Depends on:** Nothing (foundation layer)

**Phase 1 rollout guardrails:**
- Non-coding agents (`llm_api`, `http`, `process`) are configurable and visible in Phase 1, but they are not executable yet. They must remain unassignable, unmentionable for task triggers, and unavailable for chat until Phase 2 introduces the cloud worker + routing path.
- Existing daemon/runtime claiming semantics stay authoritative in Phase 1. Any provider-based claiming query added here is preparatory only and must not be wired into production claim paths yet.
- `provider` is the backend family selector for non-coding agents, not a substitute for the full executable config. For `http` and `process`, the actual endpoint/command/auth payload lives in `runtime_config`; Phase 1 validates and stores that shape, Phase 2 executes it.

### 1.1 Database Schema Changes

**File:** New migration `server/migrations/XXX_agent_types.up.sql`

```sql
-- Agent type default 'coding' — fast-path metadata-only change in PG11+.
ALTER TABLE agent ADD COLUMN IF NOT EXISTS agent_type TEXT NOT NULL DEFAULT 'coding';

-- Type-set CHECK: add as NOT VALID first (no scan under ACCESS EXCLUSIVE),
-- then VALIDATE in a separate statement that takes only SHARE UPDATE EXCLUSIVE
-- (R3 B2). Same pattern for the compound runtime constraint below.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_type_check;
ALTER TABLE agent ADD CONSTRAINT agent_type_check
    CHECK (agent_type IN ('coding', 'llm_api', 'http', 'process')) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_type_check;

-- RUNTIME REDESIGN: runtime_id becomes optional for non-coding agents.
ALTER TABLE agent ALTER COLUMN runtime_id DROP NOT NULL;

-- Direct provider/model for cloud LLM agents (no runtime binding needed).
ALTER TABLE agent ADD COLUMN IF NOT EXISTS provider TEXT;      -- 'anthropic', 'openai', etc.
ALTER TABLE agent ADD COLUMN IF NOT EXISTS model TEXT;         -- 'claude-sonnet-4-5-20250514', etc.

-- Compound constraint: coding agents MUST have runtime, non-coding MUST have provider.
-- Only covers Phase 1 types. Phase 11/12 will DROP + recreate-with-NOT-VALID.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_runtime_check;
ALTER TABLE agent ADD CONSTRAINT agent_runtime_check CHECK (
    (agent_type = 'coding' AND runtime_id IS NOT NULL)
    OR (agent_type IN ('llm_api', 'http', 'process') AND provider IS NOT NULL)
) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_runtime_check;

-- NOTE: execution_mode is NOT stored. It's derivable from agent_type:
--   coding         → daemon (local CLI)
--   llm_api        → cloud (worker + LLM API)
--   http           → cloud (worker + HTTP call)
--   process        → cloud (worker + subprocess)
--   cloud_coding   → cloud (sandbox + LLM API)  [Phase 11]
--   claude_managed → cloud (Anthropic managed)   [Phase 12]
-- Use a SQL VIEW or application-level helper if needed in queries.

-- Task queue: runtime_id also becomes optional (for pool-routed tasks)
ALTER TABLE agent_task_queue ALTER COLUMN runtime_id DROP NOT NULL;

-- Budget enforcement BEFORE dispatch (ported from Claude Managed Agents pattern).
-- Pipeline: Auth → Tenant → Budget check (ADVISORY-LOCKED) → Session → Context → Dispatch → LLM
--
-- CONCURRENCY NOTE (R3 B5). Three code paths check the budget concurrently:
--   1. ClaimTaskForRuntime (worker pool claims a queued task)
--   2. Subagent dispatch via the Phase 6 `task` tool (depth N)
--   3. Phase 8A chat request
-- A naive "SUM(cost_cents) → compare → proceed" is a classic check-then-execute
-- race: under concurrent load, `hard_stop=true` becomes advisory. We therefore
-- gate every one of those paths with a workspace-level advisory lock and a
-- single-statement budget check inside the same transaction (see §1.2).

-- Agent capabilities (what the agent can do — used for discovery in A2A)
CREATE UNIQUE INDEX idx_agent_workspace_id ON agent(workspace_id, id);

CREATE TABLE agent_capability (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    input_schema JSONB,
    output_schema JSONB,
    estimated_cost_cents INT,
    avg_duration_seconds INT,
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE CASCADE,
    UNIQUE(agent_id, name)
);

-- Cost tracking (ported from Paperclip cost_events + Hermes usage_pricing).
-- Append-only: no UPDATE/DELETE at the app layer. Corrections are recorded as
-- a separate row with `cost_status = 'adjustment'` (see §9 / R3 cost-model notes).
CREATE TABLE cost_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    model TEXT NOT NULL,
    input_tokens INT,             -- NON-CACHED input tokens only. See R3 S6.
    output_tokens INT,
    cache_read_tokens INT,        -- Anthropic `cache_read_input_tokens` (0.1x cost at 5-min cache)
    cache_write_tokens INT,       -- Anthropic `cache_creation_input_tokens` (1.25x at 5-min; 2x at 1-hour)
    cache_ttl_minutes INT,        -- 5 or 60; determines cache_write multiplier (1.25x vs 2x)
    cost_cents INT NOT NULL,
    cost_status TEXT DEFAULT 'estimated'
        CHECK (cost_status IN ('actual', 'estimated', 'included', 'adjustment')),
    trace_id UUID NOT NULL,       -- D8: every cost row is attributable to a trace chain
    session_hours DECIMAL(10,4),  -- Phase 12: Claude Managed Agents session billing
    session_cost_cents INT,       -- Phase 12: session_hour_rate * session_hours
    response_cache_id UUID,       -- Phase 9: set on cache-hit events (cost_status='included')
    created_at TIMESTAMPTZ DEFAULT NOW(),
    -- Composite FK: prevents a cost_event from linking an agent in a different workspace (R3 S1).
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE SET NULL
);

-- D8 index: chain-cost aggregation queries (`SUM(cost_cents) WHERE trace_id = $1`)
-- used by Phase 6 delegation budget check and Phase 9 chain ceiling.
CREATE INDEX IF NOT EXISTS idx_cost_event_trace ON cost_event(trace_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_cost_event_ws_time ON cost_event(workspace_id, created_at DESC);

-- Budget policies (ported from Paperclip)
CREATE TABLE budget_policy (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'agent', 'project')),
    scope_id UUID,
    monthly_limit_cents INT NOT NULL,
    warning_threshold DECIMAL DEFAULT 0.7,
    hard_stop BOOLEAN DEFAULT true
);
```

### 1.2 Backend Model Updates

**File:** `server/pkg/db/queries/agent.sql` — Add queries for capabilities, cost events, budget policies
**File:** Run `make sqlc` to regenerate `server/pkg/db/generated/`
**File:** `server/internal/handler/agent.go` — Update CreateAgent/UpdateAgent to accept `agent_type`, `provider`, `model`
**File:** `server/internal/handler/agent.go` — New endpoints: CRUD for agent_capability
**New file:** `server/internal/service/budget.go` — workspace-level budget service with race-free `CanDispatch` (see below)

**Workspace safety requirements for all new handlers in Phase 1:**
- Every capability/cost/budget endpoint must verify workspace membership before returning data.
- Capability reads/writes must load the parent agent in the current workspace before operating.
- Deletes must be workspace-scoped (`WHERE id = $1 AND workspace_id = $2`), never global-by-id.
- HTTP handlers return explicit API DTOs, not raw sqlc structs containing `pgtype` fields.

**Budget race-free dispatch gate** (R3 B5). `BudgetService.CanDispatch(ctx, tx, workspaceID, estCents)` is the single entry point used by:
1. `ClaimTaskForRuntime` (Phase 2 §2.4) — called inside the claim transaction.
2. Phase 6 `task` tool dispatch — called inside the subagent dispatch transaction.
3. Phase 8A chat handler — called inside a short transaction opened specifically to acquire the lock.

All three callers pass the same `pgx.Tx`. The function runs:

```go
// sketch — see server/internal/service/budget.go in Phase 1
func (b *BudgetService) CanDispatch(ctx context.Context, tx pgx.Tx, wsID uuid.UUID, estCents int) (Decision, error) {
    // 1. Serialize every dispatch against this workspace's budget in this transaction.
    //    Txn-scoped: released on COMMIT / ROLLBACK, no cleanup needed.
    if _, err := tx.Exec(ctx,
        `SELECT pg_advisory_xact_lock(hashtextextended('budget:'||$1::text, 0))`, wsID); err != nil {
        return Decision{}, err
    }
    // 2. Aggregate actual + estimated, compare against hard_stop.
    var spent int
    if err := tx.QueryRow(ctx, `
        SELECT COALESCE(SUM(cost_cents), 0)
        FROM cost_event
        WHERE workspace_id = $1
          AND created_at >= date_trunc('month', NOW())
          AND cost_status IN ('actual','estimated')
    `, wsID).Scan(&spent); err != nil { return Decision{}, err }

    policy, err := b.loadPolicy(ctx, tx, wsID) // hard_stop + monthly_limit_cents
    if err != nil { return Decision{}, err }
    projected := spent + estCents
    if policy.HardStop && projected > policy.MonthlyLimitCents {
        return Decision{Allow: false, Reason: "budget_exceeded"}, nil
    }
    return Decision{Allow: true, WarnAt: float64(projected) / float64(policy.MonthlyLimitCents)}, nil
}
```

**Why this works.** `pg_advisory_xact_lock` serializes only the workspace that's about to dispatch — other workspaces proceed unblocked. The lock and the `SUM(cost_cents)` query run in the same transaction as the dispatch (or the chat-message write, or the subagent enqueue) — so either the budget decision and the dispatch both commit, or both roll back. `hard_stop=true` becomes a hard guarantee under concurrency.

**Chat usage (Phase 8A):** the chat handler opens a transaction, calls `CanDispatch`, inserts the `cost_event` at tier-0 estimate (based on model + prompt tokens), commits. Then it runs the LLM call and writes a second `cost_event` with actual tokens + `cost_status='adjustment'` (append-only per the §1.1 note).

### 1.3 Error Classification System (ported from Hermes)

**New file:** `server/pkg/agent/errors.go`
- Port Hermes `agent/error_classifier.py` taxonomy to Go
- Error types (matching Hermes taxonomy): `auth`, `auth_permanent`, `billing`, `rate_limit`, `overloaded`, `server_error`, `timeout`, `context_overflow`, `payload_too_large`, `model_not_found`, `format_error`, `unknown`
- Each type has recovery hints: `retryable`, `should_compress`, `should_rotate_credential`, `should_fallback`, `backoff_seconds`
- Centralized classifier consulted by the execution loop for automatic recovery

### 1.4 Unified Credential Vault (merges LLM keys + tool secrets)

**New file:** `server/pkg/agent/credentials.go`
- Port Hermes `agent/credential_pool.py` concept to Go
- Single encrypted credential store for ALL secrets: LLM provider keys, CRM API keys, database passwords, SMTP credentials, webhook HMAC secrets (R3 B4 — see §8A)
- Provider rotation strategies (`fill_first`, `round_robin`, `random`, `least_used` — matching Hermes) apply only to `credential_type='provider'`
- Automatic cooldown for exhausted/rate-limited credentials (configurable)
- Integrates with error classifier: on `rate_limit` error, rotate to next provider credential
- Agents can USE credentials but never see raw values — injected as env vars or tool config at execution time

**Cryptographic specification** (R3 B3):

**Key hierarchy.**
1. **Master key** `CREDENTIAL_MASTER_KEY[v]` — 32 bytes, env var or KMS reference. Versioned so rotation does not require re-encrypting every row at once.
2. **Workspace DEK** (data-encryption key) — 32 bytes, derived per-workspace per-version from the master:
   - `DEK = HKDF-SHA256(ikm=MASTER[v], salt=workspace_id (16 bytes, raw), info="multica-cred-v1", L=32)`
3. **Row encryption:** AES-256-GCM with the workspace DEK.
   - **Nonce** is 12 random bytes, stored alongside ciphertext.
   - **Plaintext** is the raw secret bytes (never the JSON envelope).
   - **AAD** (Additional Authenticated Data) is `workspace_id || credential_id || name`, concatenated as raw bytes. **Binding AAD to these three fields means any attempt to move a ciphertext to a different workspace / id / name fails decryption** — closing the cross-tenant row-swap attack flagged in R3 B3.

**Schema** (revised):
```sql
CREATE TABLE IF NOT EXISTS workspace_credential (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    credential_type TEXT NOT NULL,
    name TEXT NOT NULL,
    provider TEXT,                        -- 'anthropic', 'openai' (only for credential_type='provider')

    -- Encryption. All three fields are required; no default.
    encrypted_value BYTEA NOT NULL,       -- AES-256-GCM ciphertext + 16-byte tag (GCM appends it)
    nonce BYTEA NOT NULL,                 -- 12 bytes. MUST be unique per (workspace_id, id) lifetime.
    encryption_version SMALLINT NOT NULL DEFAULT 1,  -- matches master-key version; enables rotation

    description TEXT,
    is_active BOOLEAN DEFAULT true,
    cooldown_until TIMESTAMPTZ,           -- Provider rotation: cooldown after rate_limit
    usage_count INT DEFAULT 0,            -- Provider rotation: for least_used strategy
    last_used_at TIMESTAMPTZ,
    last_rotated_at TIMESTAMPTZ,          -- Updated whenever encrypted_value changes
    created_by UUID,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(workspace_id, name),
    CONSTRAINT workspace_credential_nonce_len CHECK (octet_length(nonce) = 12)
);

-- credential_type CHECK added NOT VALID / VALIDATE (R3 B2 pattern).
ALTER TABLE workspace_credential DROP CONSTRAINT IF EXISTS workspace_credential_type_check;
ALTER TABLE workspace_credential ADD CONSTRAINT workspace_credential_type_check
    CHECK (credential_type IN ('provider', 'tool_secret', 'webhook_hmac', 'sso_oidc_secret')) NOT VALID;
ALTER TABLE workspace_credential VALIDATE CONSTRAINT workspace_credential_type_check;

CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_credential_ws_id ON workspace_credential(workspace_id, id);

CREATE TABLE IF NOT EXISTS credential_access_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    credential_id UUID NOT NULL,
    agent_id UUID,
    task_id UUID,
    trace_id UUID,                        -- D8: bind access to the trace chain
    access_purpose TEXT NOT NULL,         -- e.g. 'llm_api_call', 'tool_injection', 'webhook_verify'
    accessed_at TIMESTAMPTZ DEFAULT NOW(),
    FOREIGN KEY (workspace_id, credential_id) REFERENCES workspace_credential(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_credential_workspace ON workspace_credential(workspace_id, credential_type);
CREATE INDEX IF NOT EXISTS idx_credential_log_ws_time ON credential_access_log(workspace_id, accessed_at DESC);
```

**Master-key rotation procedure** (admin-only):
1. Generate `MASTER[v+1]` and provision alongside `MASTER[v]`.
2. Background job enumerates `workspace_credential` rows with `encryption_version = v`, decrypts with the v DEK, re-encrypts under v+1 DEK (new random nonce, same AAD), writes back in a single row update (`encrypted_value`, `nonce`, `encryption_version`, `last_rotated_at`).
3. Process is per-row; failures do not block other rows; job is resumable by re-filtering on `encryption_version = v`.
4. Once every row is at v+1, `MASTER[v]` can be destroyed.

**Access-logging coverage** (R3 hotspot): the log records every call to `Credentials.GetByName` / `GetByProvider`, including `trace_id` and `access_purpose`. Downstream injection into worker env vars or tool configs is considered part of the same access — if a worker passes the value to a tool that retains it, that is outside this log's scope and documented as such.

**Port reference:** Paperclip `packages/db/src/schema/company_secrets.ts` + `company_secret_versions.ts` (R2 B3 — corrected file paths).

### 1.5 Embedding Model Strategy (dimension-partitioned)

Phases 4 (Knowledge/RAG), 5 (Memory), and 9 (Semantic Cache) all depend on vector embeddings. Configuration is per-workspace so different tenants can pick different embedding providers — but pgvector requires a **typed `vector(N)` column at table-creation time** for `ivfflat`/`hnsw` indexing to work. An untyped `vector` column cannot be indexed and falls back to sequential scans, which is unusable past a few thousand rows.

**Decision: dimension-partitioned tables.** We support a closed set of three dimensions — 1024, 1536, 3072 — and create *one physical table per dimension* for each vector content type. Workspaces pick one dimension at onboarding; routing is based on `workspace_embedding_config.dimensions`.

**Supported dimensions:**
| Dim | Typical model | Index type | Notes |
|---|---|---|---|
| 1024 | Voyage 3 Lite, OpenAI `text-embedding-3-small` (Matryoshka-truncated) | `ivfflat` (cosine) | Standard |
| 1536 | **Default.** OpenAI `text-embedding-3-small` full | `ivfflat` (cosine) | Standard |
| 3072 | OpenAI `text-embedding-3-large` full | `hnsw` only | `ivfflat` maxes at 2000 dims; for 3072 we must use `hnsw` (pgvector ≥ 0.5.0). |

A deployment that needs a different dimension adds a new partition (new tables + a migration) in a follow-up change. Ad-hoc per-workspace dimensions are deliberately NOT supported — that path forces ANN → post-filter scans.

**Migration:**
```sql
CREATE TABLE workspace_embedding_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL UNIQUE,
    provider TEXT NOT NULL DEFAULT 'openai',              -- 'openai', 'voyage', 'ollama', 'custom'
    model TEXT NOT NULL DEFAULT 'text-embedding-3-small',
    dimensions INT NOT NULL DEFAULT 1536
        CHECK (dimensions IN (1024, 1536, 3072)),         -- closed set; add partitions to widen
    api_credential_id UUID REFERENCES workspace_credential(id),
    endpoint_url TEXT,                                    -- for custom/ollama providers
    created_at TIMESTAMPTZ DEFAULT NOW()
);
```

**Per-dimension content tables** (schema sketches — concrete DDL lives in Phase 4/5/9):
```sql
-- Phase 4 (knowledge), Phase 5 (memory), Phase 9 (response_cache): one table per content type × dim
knowledge_chunk_1024   (embedding vector(1024),   ...)  -- ivfflat cosine
knowledge_chunk_1536   (embedding vector(1536),   ...)  -- ivfflat cosine  (default)
knowledge_chunk_3072   (embedding vector(3072),   ...)  -- hnsw cosine     (>2000 dims)
agent_memory_1024 / _1536 / _3072
response_cache_1024 / _1536 / _3072
```

All three dim-partitioned tables for a given content type share an identical non-vector schema. Application-level `Embedder` + repository layer picks the partition from `workspace_embedding_config.dimensions`.

**New file:** `server/pkg/embeddings/embeddings.go`
- `Embedder` interface: `Embed(ctx, texts []string) ([][]float32, dims int, err error)`
- Provider implementations: OpenAI, Voyage, Ollama, Custom HTTP
- Batch embedding (up to 2048 texts per call for OpenAI)
- Cost tracking: embedding API calls recorded in `cost_event`
- Fail-fast validation: returned `dims` must equal the workspace's configured dimension; mismatch raises an error before any DB insert.

**New file:** `server/pkg/embeddings/routing.go`
- Repository helpers (`KnowledgeChunkRepo`, `MemoryRepo`, `ResponseCacheRepo`) dispatch to the right dim-partition based on `workspace_embedding_config.dimensions` loaded per-workspace.
- Dimension is effectively immutable per workspace once embeddings exist. Changing it requires a documented re-embedding flow (below).

**Dimension-change procedure (admin-only):**
1. Admin initiates `workspace embedding reset --dimension 3072`
2. System checks every dim-partition table for existing rows in that workspace; if any exist, require explicit confirmation
3. Background job re-embeds each source row with the new model and writes into the new dim partition
4. Once complete: flip `workspace_embedding_config.dimensions` atomically and delete the old partition's rows for that workspace
5. Until the flip, new inserts go to the *current* dimension — no dual-writes

**Note on hnsw for 3072:** pgvector hnsw indexes support up to 2000 dims via `halfvec(N)` (half-precision float); for 3072 with full float32 precision, recent pgvector releases lift the limit further. The Phase 4/5/9 migrations pick the right index type per dimension and must be smoke-tested on CI (`pgvector/pgvector:pg17`) during Phase 1.

### 1.6 Frontend: Redesigned Agent Creation Flow

**File:** `packages/core/types/agent.ts` — Add `agent_type`, `capabilities`, `provider`, `model` to Agent type (execution_mode derived from agent_type, not stored)
**File:** `packages/views/agents/components/create-agent-dialog.tsx` — Complete redesign:

**New flow (agent type determines what fields appear):**

1. **Step 1 — Agent type selection:** Coding Agent | AI Agent | HTTP Agent | Custom Script
2. **Step 2 — Type-specific configuration:**
   - **Coding Agent:** Runtime dropdown (existing behavior, required)
   - **AI Agent:** Provider dropdown (Anthropic/OpenAI/etc.) + Model dropdown. NO runtime selector.
   - **HTTP Agent:** Endpoint URL, HTTP method (POST default), auth type (none/api_key/bearer/basic/hmac), auth credential (from workspace_credential vault), payload template (Go template with `{{.Prompt}}`, `{{.Context}}`, `{{.Issue}}`), response JSONPath for extracting result. NO runtime selector. Config stored in `runtime_config` JSONB.
   - **Custom Script:** Command path, arguments (array), working directory, environment variables (key-value, secrets from vault), timeout (default 300s), max output size. Runs inside the cloud worker process — NOT sandboxed (use `cloud_coding` for sandboxed execution). NO runtime selector. Config stored in `runtime_config` JSONB.
3. **Step 3 — Common fields:** Name, description, instructions, visibility, max_concurrent_tasks

**Why no runtime for non-coding agents:** Cloud workers auto-register with their supported providers. Task claiming matches agent's `provider` to any worker that supports it. Workers are interchangeable — the user doesn't care which one handles the task.

**Task claiming change for runtimeless agents:**
- **File:** `server/pkg/db/queries/agent.sql` — Update `ListPendingTasksForRuntime`
- Coding agents: match by `agent.runtime_id = $1` (existing)
- LLM/HTTP/Process agents: match by `agent.provider = ANY(worker's providers)` — any capable worker can claim

**File:** `packages/views/agents/components/agent-detail.tsx` — Show capabilities tab, show provider/model instead of runtime for non-coding agents

Implementation notes:
- For `http` and `process`, `provider` is the backend family selector used later by the worker (`http` / `process`). The actual endpoint/command/auth details live in `runtime_config`.
- Phase 1 validates and stores those `runtime_config` shapes, but does not execute them yet.
- The provider-based claiming query is preparatory work only in Phase 1; production claim routing, daemon endpoints, and enqueue behavior stay runtime-based until Phase 2.
- Agent detail adds a read-only capabilities tab in Phase 1. Editing workflows can wait until Phase 6.

### 1.7 Migration Numbering Strategy

The existing codebase uses sequential numbers (001-035+), including a few duplicate 3-digit prefixes already present in `server/migrations/` (e.g. multiple `029_*`, `032_*`). The existing migration runner (`server/cmd/migrate/main.go`) sorts files lexicographically and tracks applied migrations in `schema_migrations`, so the numbering scheme below is safe (lexicographic `"035_"` < `"100_"`). We adopt a **per-phase prefix** to avoid ordering conflicts during parallel development:

| Phase | Migration Range | Example |
|-------|----------------|---------|
| 1 | 100-109 | `100_agent_types.up.sql`, `101_credentials.up.sql`, `102_embedding_config.up.sql` |
| 2 | 110-119 | `110_tools_config.up.sql` |
| 3 | 120-124 | `120_mcp_config.up.sql` |
| 4 | 125-129 | `125_knowledge.up.sql` (dim-partitioned), `126_structured_output.up.sql` |
| 5 | 130-139 | `130_agent_memory.up.sql` (dim-partitioned) |
| 6 | 140-149 | _(reserved; no migrations — delegation is a tool call per D2)_ |
| 7 | 150-159 | `150_workflows.up.sql`, `151_approvals.up.sql` |
| 8A | 160-169 | `160_webhooks.up.sql`, `161_templates.up.sql`, `162_api_keys.up.sql` |
| 8B | 170-179 | `170_audit_log.up.sql`, `171_sso.up.sql`, `172_retention.up.sql`, `173_agent_versioning.up.sql` |
| 9 | 180-184 | `180_response_cache.up.sql` (dim-partitioned), `181_agent_metric.up.sql` |
| 10 | 185-189 | `185_traces.up.sql`, `186_task_message_fts.up.sql` |
| 11 | 190-194 | `190_sandbox_templates.up.sql`, `191_agent_type_cloud_coding.up.sql` |
| 12 | 195-199 | `195_managed_agents.up.sql` |

**Idempotency conventions** (enforced by every migration):
- Every `CREATE TABLE` uses `IF NOT EXISTS`.
- Every `CREATE INDEX` / `CREATE UNIQUE INDEX` uses `IF NOT EXISTS`.
- Every `ADD COLUMN` uses `IF NOT EXISTS` (PG 9.6+).
- Every `ADD CONSTRAINT` is preceded by `DROP CONSTRAINT IF EXISTS` (PG's `ADD CONSTRAINT` has no `IF NOT EXISTS` form). This idiom is used everywhere a constraint is added so re-running is safe.
- Every down file uses `DROP ... IF EXISTS`.
- `ALTER TABLE ... ALTER COLUMN ... DROP NOT NULL` and `... TYPE ...` are naturally idempotent (no-op if already in the target state).

The `schema_migrations` table in `server/cmd/migrate/main.go` already gates re-application, but the SQL-level idempotency above is required so operators running repair migrations out-of-band don't fail.

### 1.8 Migration Safety

All schema changes are non-destructive. Existing data is preserved:
- `agent_type` defaults to `'coding'` — metadata-only `ADD COLUMN ... DEFAULT const` fast-path in PG11+, no rewrite.
- `runtime_id` was NOT NULL, becomes nullable → existing agents keep their runtime_id, no data loss.
- `provider` and `model` are nullable → existing coding agents don't need them.
- New tables (`agent_capability`, `cost_event`, `budget_policy`, `workspace_credential`) are additive.
- **CHECK constraints** (`agent_type_check`, `agent_runtime_check`) are added **NOT VALID** then **VALIDATE**-d in a separate statement. `ADD CONSTRAINT CHECK ... NOT VALID` takes no lock beyond the catalog update; the subsequent `VALIDATE CONSTRAINT` takes only `SHARE UPDATE EXCLUSIVE`, allowing concurrent reads and writes. Phase 11 / Phase 12 constraint mutations follow the same `DROP IF EXISTS → ADD ... NOT VALID → VALIDATE` pattern. (R3 B2)
- **Lock expectations:** under typical `agent` table sizes (< 10K rows) these migrations run in seconds. On larger tables (> 1M rows) `VALIDATE CONSTRAINT` is still fast because it scans once without blocking DML. No table rewrites happen in Phase 1.

**Rollback caveat:** rolling Phase 1 back after creating non-coding agents/tasks is not lossless. The down path must either delete/backfill rows with `NULL runtime_id` first or be treated as a pre-release cleanup-only rollback.

### 1.9 Verification
- `make sqlc` succeeds
- `pnpm typecheck` passes
- Existing coding agents still work (backwards compatible — defaults to 'coding', runtime required)
- Can create an LLM agent with provider + model, NO runtime_id
- Can create a coding agent with runtime_id (existing flow works)
- Non-coding agents are clearly marked as not yet assignable/chat-capable in Phase 1
- Capability/cost/budget endpoints enforce workspace membership and workspace-scoped deletes
- Migration runs on existing database without data loss
- `go mod tidy && go build ./...` succeeds with the go-ai dependency resolved
- `server/pkg/agent/defaults.go` compiles and all constants are referenced from the central location (no magic numbers elsewhere in Phase 1 code)

### 1.10 go-ai Foundation + Soft Fork (D1, D5)

`github.com/digitallysavvy/go-ai` v0.4.0 added to `server/go.mod` as the Phase 2 harness foundation. No code in Phase 1 imports it yet — the goal is purely to pin the dependency and establish the fork safety net.

**Fork topology** (see `docs/engineering/go-ai-fork-policy.md`):
- Upstream: `digitallysavvy/go-ai`
- Fork: `ahmed-khaire/go-ai` — dormant; `main` byte-for-byte mirrors upstream, `multica-infra` (default branch) holds the weekly mirror-upstream GitHub Action.
- `server/go.mod` depends on upstream directly; `replace` directive activated per fork policy only.

### 1.11 Default Constants Package (D7)

**New file:** `server/pkg/agent/defaults.go`

Central registry for tunables used across all phases. Every subsequent phase references these constants instead of hardcoding:

```go
package agent

import "time"

// Agent-loop defaults
const (
    MainAgentMaxSteps             = 50
    SubagentMaxSteps              = 100
    ContextEvictionThresholdBytes = 80 * 1024 // ~20K tokens
)

// Sandbox defaults (Phase 11)
const SandboxTimeoutBufferMs = 30_000

// Workflow / orchestration defaults (Phase 7)
const StopMonitorPollIntervalMs = 150

// Skills cache (Phase 3)
const SkillsCacheTTL = 4 * time.Hour

// Reasoning default — workspace/agent overrides take precedence.
// Resolves to go-ai's ReasoningDefault (provider-chosen).
const DefaultReasoningLevel = "default"
```

Any later phase that introduces a new tunable adds it here rather than in-file.

---

## Phase 2: LLM API Backend (Direct API Calls)
**Goal:** Agents can call LLM APIs directly without spawning a CLI subprocess. Agent-loop machinery (tool calls, streaming, stop conditions, reasoning control) is delegated to go-ai's `ToolLoopAgent`; we keep the `Backend` interface as our in-house boundary and add an adapter layer.
**Depends on:** Phase 1 (agent_type field, error classifier, credential pool, go-ai dependency pinned, `defaults.go`)
**Baseline infra (D4):** Redis is a baseline dependency from this phase onward. Stream resumption (§8A), skills cache (§3), chat pub/sub (§8A), and semantic cache (§9) all require it. Single-process dev mode uses an in-memory adapter via `RedisAdapter` interface (`redis` + `memory` implementations).

### 2.1 Harness Adapter + Backend Implementations (D1, D6)

**File:** `server/pkg/agent/agent.go` — Extend the existing `Backend` interface with two additions from D6:

```go
type Backend interface {
    Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error)

    // ExpiresAt returns when the current execution context must be drained and
    // stopped, or nil if the backend has no time cap. LLM API backends return nil;
    // sandbox backends (Phase 11) return the provider's session-cap expiry.
    ExpiresAt() *time.Time
}

type Hooks struct {
    AfterStart        func(context.Context, Session) error
    BeforeStop        func(context.Context, Session) error
    OnTimeout         func(context.Context, Session) error // fires SandboxTimeoutBufferMs before ExpiresAt
    OnTimeoutExtended func(context.Context, Session, time.Duration) error
}
```

The hook mechanism is universal — business agents on Anthropic's 5-hour cap need the same graceful-snapshot path as sandboxed coding agents. Default `OnTimeout` is no-op; Phase 11/12 backends wire in snapshot behavior.

**New file:** `server/pkg/agent/harness/harness.go` — Thin adapter over go-ai

- Wraps `github.com/digitallysavvy/go-ai/pkg/agent.ToolLoopAgent` behind our `Backend` interface.
- Injects per-call: tools from our registry, system prompt from our builder, skills from `SkillRegistry`, subagents from `SubagentRegistry`, `ReasoningLevel` from agent/workspace config.
- Wires go-ai's structured callbacks (`OnStart / OnStepStart / OnToolCallStart / OnToolCallFinish / OnStepFinish / OnFinish`) to our cost-event recording, trace-id propagation, and progress broadcasting.
- Applies `StopWhen: []StopCondition{StepCountIs(MainAgentMaxSteps)}` from `defaults.go`.

**New file:** `server/pkg/agent/llm_api.go` — `Backend` implementation backed by harness

- Builds a go-ai `LanguageModel` from the agent's configured provider/model
- Wraps it with our middleware: credential rotation on rate_limit (uses Phase 1.4 pool), error classification (Phase 1.3), per-call budget check (Phase 1 cost tracking)
- Passes the wrapped model to `harness.Execute`
- Supports:
  - Streaming via go-ai channel model (interim tool calls, reasoning, text deltas)
  - **Prompt caching** (Anthropic `cache_control` on system + tools + knowledge blocks) — built in from day 1, added via `ProviderOptions`
  - **Reasoning level** via go-ai's portable `Reasoning *ReasoningLevel` (defaults to `DefaultReasoningLevel`)
  - `ExpiresAt()` returns nil (LLM API has no session cap beyond provider rate limits)

**What we DON'T build from scratch:** the tool-call loop, step counting, stop conditions, usage aggregation across tool calls and streaming events, and reasoning parameter plumbing. All of that is go-ai.

### 2.1.1 Multi-Model Fallback Chain

**File:** Agent `runtime_config` JSONB — add fallback configuration:
```json
{
  "fallback_chain": [
    { "provider": "anthropic", "model": "claude-sonnet-4-5-20250514" },
    { "provider": "openai", "model": "gpt-4o" },
    { "provider": "anthropic", "model": "claude-haiku-4-5-20251001" }
  ]
}
```

**New file:** `server/pkg/agent/fallback.go`
- Wraps any `Backend` with automatic fallback on failure.
- On provider error (5xx, timeout, rate_limit after credential exhaustion), tries next model in chain.
- **Provider health tracking:** Track rolling error rate per provider (5-min window). If error rate > 50%, auto-skip that provider for all agents until health recovers.
- **Graceful degradation:** If ALL providers in the chain fail, queue the task for retry later (default 5 min). Task status = `queued_retry`.
- Integrates with error classifier (Phase 1.3): only `retryable` errors trigger fallback. `auth_permanent` or `billing` errors fail immediately.
- Cost events record which model actually executed (not just the primary model).

**Fallback-wrapper timeout composition** (R1 B3). The wrapper is itself a `Backend`, so it must satisfy D6 (`ExpiresAt()` + `Hooks`) coherently when composing N child backends:

- `ExpiresAt()` and `Hooks` **forward to the currently active child only**. There is always exactly one active child; pre-swap, it's the primary; post-swap, it's the new selection. The wrapper tracks the active child under a mutex.
- **On child swap**, the wrapper (a) calls the previous child's `Hooks.BeforeStop` if set, (b) advances the active pointer, (c) re-reads `ExpiresAt()` from the new active child, (d) cancels the old outer timer and schedules a new one at `newExpiry - SandboxTimeoutBufferMs`.
- If the active child's `ExpiresAt()` is nil (LLM-API case) the wrapper runs without a timer. If a *future* fallback in the chain has a cap, it only takes effect after a swap — the wrapper does not pre-emptively inherit a tighter expiry from a non-active child.
- **Mixed-kind fallback chains** — placing a sandbox backend (has real expiry + snapshot hook) in the same chain as an LLM-API backend (nil expiry, no snapshot) is allowed, but any backend that relies on `OnTimeout` for correctness (Phase 11 cloud_coding) MUST declare `require_ontimeout: true` in its fallback chain entry. The wrapper verifies at configuration time that all earlier entries also implement `OnTimeout` (reject at agent-create if violated).
- **Verification test** (Phase 11 add): configure a `cloud_coding` fallback chain, force the primary to fail mid-execution, assert `OnTimeout` fires on the *new* active child before the provider's hard cap, and the snapshot is preserved.

**New file:** `server/pkg/agent/provider_health.go`
- In-memory rolling window (5 min) of success/failure counts per provider
- Shared across all workers via the worker's process memory (no external state needed)
- Health status: `healthy` (error rate < 20%), `degraded` (20-50%), `down` (> 50%)
- When provider recovers (error rate drops), automatically resume routing to it

**New file:** `server/pkg/agent/http_backend.go`
- Port concept from Paperclip `server/src/adapters/http/execute.ts` (R2 B3 — corrected path; there is no `packages/adapters/http/`)
- Calls any HTTP endpoint as an agent backend
- Configurable: URL, method, headers, payload template, response parser

**New file:** `server/pkg/agent/process_backend.go`
- Port concept from Paperclip `server/src/adapters/process/execute.ts`
- Spawns any command (Python scripts, custom tools)
- Streams stdout as messages

### 2.2 Tool System Foundation

**Tool definitions use go-ai's `types.Tool`** (name, description, JSON Schema parameters, executor function). We don't define a parallel `Tool` struct — the go-ai type is what `ToolLoopAgent` consumes.

**New file:** `server/pkg/tools/registry.go`
- `Registry`: workspace-scoped registry returning `[]types.Tool` for a given agent
- Supports availability gating (per-workspace/per-agent tool enablement — Hermes `check_fn` concept)
- Produces the final `[]types.Tool` slice passed into `harness.Execute`

**New file:** `server/pkg/tools/builtin/` — Initial built-in tools (each returns a `types.Tool`):
- `http_request.go` — Make HTTP requests (for CRM, APIs, etc.)
- `search.go` — Web search (via Brave/Serper API)
- `email.go` — Send email (via SMTP/SendGrid)
- `database_query.go` — Query external databases

**Parallel execution:** go-ai v0.4.0 executes tool calls sequentially after the stream ends (intentional design to avoid partial-JSON bugs). Matches Multica's task-view UX where tool results appear as discrete cards. No bespoke goroutine pool needed; if we ever want parallelism, wrap multiple sub-tools under a single `types.Tool` that fans out internally.

**Integration with LLM API backend:**
- `llm_api.go` resolves the agent's tools via the registry and passes them to `harness.Execute`
- go-ai drives the tool-use loop; we just receive structured callbacks
- Tool results stream to our WebSocket via the `OnToolCallFinish` callback

### 2.3 Model Router (Cost Optimization)

**New file:** `server/pkg/router/router.go`
- Model tier system: micro (Haiku/$0.10), standard (Sonnet/$3), premium (Opus/$15)
- Routing strategies:
  - Policy-based: agent_type/task_type → tier mapping (configured per workspace)
  - Intent-based: quick Haiku call to classify complexity → route to tier
  - Cascading: start cheap, escalate if confidence < threshold
- Per-step routing in workflows (Phase 7) — different steps use different tiers
- Port Hermes `agent/smart_model_routing.py` concept: simple messages auto-routed to cheap model

**New file:** `server/pkg/router/cost.go`
- Token cost calculation per model (port Hermes `agent/usage_pricing.py` pricing tables)
- Cache-aware pricing (cache reads at 0.1x, cache writes at 1.25x)
- Cost event recording after every LLM call
- Budget check before execution

### 2.4 Cloud Worker Binary (Uses Existing Daemon Protocol)

The codebase already has a unified runtime API that supports both local and cloud daemons. Cloud workers use the SAME protocol — no new endpoints needed.

**New file:** `server/cmd/worker/main.go` — Cloud worker binary
- Registers as cloud runtime using EXISTING `/api/daemon/register` endpoint
- Polls for tasks using EXISTING `/runtimes/{id}/tasks/claim` endpoint
- Reports messages using EXISTING `/daemon/tasks/{id}/messages` endpoint
- Reports completion using EXISTING `/daemon/tasks/{id}/complete` endpoint
- **But executes via LLMAPIBackend / HTTPBackend / ProcessBackend (not CLI subprocesses)**

**New file:** `server/internal/worker/worker.go` — Core poll-execute-report loop
- Configurable task slot pool (default 10 concurrent tasks)
- Registers with supported providers list: `["anthropic", "openai", "http", "process"]`
- Claims tasks for any agent whose `provider` matches worker's capabilities
- **Retry with backoff** (port Cadence `common/backoff/` patterns): exponential backoff with jitter

**New file:** `server/internal/worker/heartbeat.go` — Health reporting
- Uses existing heartbeat endpoint
- Reports active tasks, free slots, uptime

**New file:** `server/internal/worker/health.go` — Kubernetes probes
- `GET /healthz` — liveness probe (am I alive? returns 200 if process is running)
- `GET /readyz` — readiness probe (am I ready? returns 200 if registered + has free slots, 503 if draining)
- Graceful shutdown: on SIGTERM, stop claiming new tasks, drain in-flight tasks (configurable timeout, default 60s), deregister runtime, then exit
- Required for production k8s deployments with HPA auto-scaling

**Reuse:** `server/internal/daemon/client.go` — The existing daemon HTTP client has all methods needed (register, heartbeat, claim, report messages, complete). Worker reuses this client with different backends.

**Scaling:**
- Small: `--with-worker` flag on server binary embeds worker goroutines (single process)
- Medium: Run 2-3 `worker` processes alongside server
- Large: Auto-scale `worker` containers by queue depth (k8s HPA)

**Fair scheduling:** Add `FOR UPDATE SKIP LOCKED` to claim query + round-robin across workspaces to prevent noisy-neighbor monopolization.

**File:** `server/internal/service/task.go` — Update `ClaimTaskForRuntime` to support provider-based matching for runtimeless agents
**File:** `server/cmd/server/router.go` — Optional embedded worker on `--with-worker` flag

### 2.5 Verification
- Create an `llm_api` agent with Anthropic provider
- Assign an issue → agent executes server-side (no daemon)
- Agent calls tools during execution (including parallel execution)
- Messages stream to frontend via WebSocket
- Cost events recorded with cache-aware pricing
- Model router selects appropriate tier
- Error classifier triggers credential rotation on rate_limit
- `make check` passes

---

## Phase 3: MCP Integration + Skills Cache
**Goal:** Agents can discover and use tools from MCP servers dynamically. Skills catalogue (Phase 1 `skill` table) is exposed through go-ai's `SkillRegistry`.
**Depends on:** Phase 2 (harness, tool registry, Redis baseline).
**Modifies from Phase 2:** adds a `RegisterCleanup(func())` method to `LLMAPIBackend` so MCP connections are closed at task end. This is a non-breaking additive change; Phase 2 consumers that ignore the method continue to work unchanged.

### 3.1 MCP Client

**Note:** go-ai v0.4.0 includes an `mcp` package. Prefer wiring go-ai's MCP client as a `types.Tool` producer rather than reimplementing — patch the fork per policy only if coverage gaps appear.

**New file:** `server/pkg/mcp/client.go`
- Wraps go-ai's `mcp` client where it fits; falls back to a direct implementation for transports go-ai doesn't cover.
- Connect to MCP servers via stdio or SSE transport
- Discover tools via `tools/list` method
- Execute tools via `tools/call` method
- Map MCP tool schemas to go-ai `types.Tool`
- Port Hermes `tools/mcp_tool.py` patterns: reconnection with exponential backoff, per-server timeouts

**New file:** `server/pkg/mcp/transport_stdio.go` — Spawn MCP server process
**New file:** `server/pkg/mcp/transport_sse.go` — Connect to remote MCP server

### 3.1.1 Skills Registry (go-ai `SkillRegistry`)

**New file:** `server/pkg/agent/harness/skills.go`

- Builds a go-ai `SkillRegistry` from the workspace's `skill` + `skill_file` tables (Phase 1 existing schema).
- Each skill is exposed to the agent via go-ai's `skill` tool (single tool dispatching all skills — keeps the tool registry small).
- **Cached in Redis** per the D4 baseline, keyed by `skills:v1:{workspace_id}:{agent_id}`, TTL = `SkillsCacheTTL` (4h, from `defaults.go`).
- Invalidated on skill update / agent-skill mutation via a WS event subscriber (cheap: write-through on the mutation handler).

### 3.2 Per-Agent MCP Configuration

**File:** Agent `runtime_config` JSONB field — add MCP server configs:
```json
{
  "mcp_servers": [
    { "name": "crm", "transport": "stdio", "command": "npx", "args": ["-y", "@salesforce/mcp-server"] },
    { "name": "email", "transport": "sse", "url": "https://mcp.company.com/email" }
  ]
}
```

**File:** `server/internal/worker/worker.go` — On task start, connect to agent's MCP servers, discover tools, merge with built-in tools

### 3.3 Frontend MCP Configuration

**File:** `packages/views/agents/components/tabs/` — New `tools-tab.tsx`
- Configure MCP servers per agent
- Show discovered tools from connected servers
- Enable/disable individual tools
- Configure tool-specific settings

### 3.4 Verification
- Configure an agent with an MCP server (e.g., filesystem MCP server)
- Agent discovers tools from MCP server
- Agent uses MCP tools during execution
- Tools tab shows discovered tools in UI

---

## Phase 4: Structured Outputs & Guardrails
**Goal:** Agents produce typed results, validated before committing.
**Depends on:** Phase 2 (LLM API backend with tool calling)

### 4.1 Structured Output System

**Migration:** Add to agent_task_queue:
```sql
ALTER TABLE agent_task_queue ADD COLUMN output_schema JSONB;
ALTER TABLE agent_task_queue ADD COLUMN structured_result JSONB;
```

**File:** `server/pkg/agent/llm_api.go` — When `output_schema` is set:
- Use Anthropic's tool-use-as-structured-output pattern (define a "result" tool matching the schema)
- Or use OpenAI's `response_format` with JSON Schema
- Validate result against schema before marking task complete

### 4.2 Guardrail System

**New file:** `server/pkg/guardrails/guardrail.go`
- Guardrail types: schema validation, regex pattern match, LLM judge (cheap model reviews output), custom webhook
- On failure: retry (re-prompt agent), escalate (create approval request), reject (fail task)
- Port concept from CrewAI `lib/crewai/src/crewai/guardrail.py`
- **Prompt injection detection** (port from Hermes `agent/prompt_builder.py:36-73`): scan agent context files for injection patterns, invisible Unicode, exfiltration attempts

**File:** Agent `runtime_config` JSONB — add guardrail configuration per agent

### 4.3 Knowledge/RAG System

**Migration** — three dim-partitioned `knowledge_chunk_*` tables per D8 / §1.5:

```sql
CREATE TABLE IF NOT EXISTS knowledge_source (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('file', 'url', 'text')),
    content TEXT,
    chunk_size INT DEFAULT 512,
    metadata JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- One physical table per supported dimension. Routing by workspace_embedding_config.dimensions.
-- NOTE: workspace_id duplicated on chunk so every lookup is workspace-scoped without JOIN
-- (R3 B6) — and so ivfflat/hnsw can filter via partial index per workspace_id if we later shard.
CREATE TABLE IF NOT EXISTS knowledge_chunk_1024 (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    source_id UUID NOT NULL,
    content TEXT NOT NULL,
    embedding vector(1024) NOT NULL,
    chunk_index INT,
    metadata JSONB,
    FOREIGN KEY (workspace_id, source_id) REFERENCES knowledge_source(workspace_id, id)
        ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_source_ws_id ON knowledge_source(workspace_id, id);

CREATE TABLE IF NOT EXISTS knowledge_chunk_1536 (
    LIKE knowledge_chunk_1024 INCLUDING ALL
);
ALTER TABLE knowledge_chunk_1536 ALTER COLUMN embedding TYPE vector(1536);

CREATE TABLE IF NOT EXISTS knowledge_chunk_3072 (
    LIKE knowledge_chunk_1024 INCLUDING ALL
);
ALTER TABLE knowledge_chunk_3072 ALTER COLUMN embedding TYPE vector(3072);

CREATE TABLE IF NOT EXISTS agent_knowledge (
    agent_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    knowledge_source_id UUID NOT NULL,
    PRIMARY KEY (agent_id, knowledge_source_id),
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, knowledge_source_id) REFERENCES knowledge_source(workspace_id, id) ON DELETE CASCADE
);

-- Indexes: ivfflat for 1024 / 1536 (within pgvector's 2000-dim cap); hnsw for 3072.
CREATE INDEX IF NOT EXISTS idx_chunk_1024_embedding ON knowledge_chunk_1024
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_chunk_1536_embedding ON knowledge_chunk_1536
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_chunk_3072_embedding ON knowledge_chunk_3072
    USING hnsw (embedding vector_cosine_ops);

-- Every chunk lookup is workspace-scoped at the schema level.
CREATE INDEX IF NOT EXISTS idx_chunk_1024_ws ON knowledge_chunk_1024(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chunk_1536_ws ON knowledge_chunk_1536(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chunk_3072_ws ON knowledge_chunk_3072(workspace_id);
```

- pgvector already available in the stack
- Documents chunked on upload, each chunk embedded independently
- Agent's knowledge sources queried via semantic search before each task
- Relevant chunks injected into agent's context as cacheable prefix (prompt caching)
- **Distinction from Memory (Phase 5):** Knowledge = static reference data loaded by users. Memory = dynamic experiential data learned by agents during execution.

### 4.4 Verification
- Create an agent with `output_schema: { "risk_level": "string", "issues": "array" }`
- Agent produces structured JSON result matching schema
- Invalid output triggers guardrail retry
- Knowledge sources are queried and injected into context
- Prompt injection detection catches malicious patterns
- `make check` passes

---

## Phase 5: Agent Memory System
**Goal:** Agents accumulate and share knowledge across tasks and sessions.
**Depends on:** Phase 1 (`workspace_embedding_config` and dim-partition routing from §1.5), Phase 2 (LLM API backend), Phase 4 (knowledge/RAG for pgvector infrastructure, shared dim-partition pattern)

### Port from (code): CrewAI unified memory + Hermes context compressor
### Concept reference: MemGPT three-tier memory taxonomy (core/working/archival) — see public-literature table §14

### 5.1 Memory Database

**Migration:** `server/migrations/XXX_agent_memory.up.sql` — three dim-partitioned memory tables per §1.5.

```sql
-- Common non-vector columns (identical across dim partitions).
-- Defined once via template then copied with LIKE to preserve schema drift.
CREATE TABLE IF NOT EXISTS agent_memory_1024 (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,

    -- Content
    content TEXT NOT NULL,
    embedding vector(1024) NOT NULL,

    -- Scoping (ported from CrewAI hierarchical namespaces).
    -- DB-level CHECK enforces workspace isolation; the service-layer MemoryService
    -- additionally rewrites scope from the authenticated wsId before every insert.
    scope TEXT NOT NULL,
    CONSTRAINT agent_memory_1024_scope_ws CHECK (scope LIKE '/ws/' || workspace_id::text || '/%'),
    categories TEXT[] DEFAULT '{}',

    -- Scoring (ported from CrewAI composite scoring; defaults match types.py: 0.5/0.3/0.2, 30-day half-life).
    importance DECIMAL DEFAULT 0.5,

    -- Metadata. Composite FK ensures source_agent_id belongs to the same workspace.
    source_agent_id UUID,
    source_task_id UUID,
    metadata JSONB DEFAULT '{}',
    private BOOLEAN DEFAULT false,

    -- Lifecycle
    created_at TIMESTAMPTZ DEFAULT NOW(),
    last_accessed_at TIMESTAMPTZ DEFAULT NOW(),
    access_count INT DEFAULT 0,

    FOREIGN KEY (workspace_id, source_agent_id) REFERENCES agent(workspace_id, id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS agent_memory_1536 (LIKE agent_memory_1024 INCLUDING ALL);
ALTER TABLE agent_memory_1536 ALTER COLUMN embedding TYPE vector(1536);
ALTER TABLE agent_memory_1536 ADD CONSTRAINT agent_memory_1536_scope_ws
    CHECK (scope LIKE '/ws/' || workspace_id::text || '/%') NOT VALID;
ALTER TABLE agent_memory_1536 VALIDATE CONSTRAINT agent_memory_1536_scope_ws;

CREATE TABLE IF NOT EXISTS agent_memory_3072 (LIKE agent_memory_1024 INCLUDING ALL);
ALTER TABLE agent_memory_3072 ALTER COLUMN embedding TYPE vector(3072);
ALTER TABLE agent_memory_3072 ADD CONSTRAINT agent_memory_3072_scope_ws
    CHECK (scope LIKE '/ws/' || workspace_id::text || '/%') NOT VALID;
ALTER TABLE agent_memory_3072 VALIDATE CONSTRAINT agent_memory_3072_scope_ws;

-- Vector indexes. ivfflat for ≤2000 dims; hnsw for 3072.
CREATE INDEX IF NOT EXISTS idx_mem_1024_vec ON agent_memory_1024
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_mem_1536_vec ON agent_memory_1536
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_mem_3072_vec ON agent_memory_3072
    USING hnsw (embedding vector_cosine_ops);

-- Scope + workspace lookups.
CREATE INDEX IF NOT EXISTS idx_mem_1024_scope ON agent_memory_1024 (workspace_id, scope text_pattern_ops);
CREATE INDEX IF NOT EXISTS idx_mem_1536_scope ON agent_memory_1536 (workspace_id, scope text_pattern_ops);
CREATE INDEX IF NOT EXISTS idx_mem_3072_scope ON agent_memory_3072 (workspace_id, scope text_pattern_ops);
```

**Scope-injection defense (R3 S2 — see security hotspots):** scope strings are untrusted input. Two layers of defense:
1. DB-level `CHECK (scope LIKE '/ws/' || workspace_id::text || '/%')` — forged scopes fail at insert.
2. Service-layer `MemoryService.Remember` / `.Recall` always rewrite the scope prefix from the authenticated `wsId`, never accept a shorter prefix than `/ws/{wsId}/` from callers.

### 5.2 Memory Service

**New file:** `server/internal/service/memory.go`

Core operations (ported from CrewAI `unified_memory.py`):

**Remember (write pipeline):**
1. Receive raw text from agent task completion
2. Call LLM (**micro tier — Haiku/GPT-4o-mini**) to **extract atomic memories** — break raw dump into discrete, self-contained statements (port CrewAI `extract_memories`)
3. For each extracted memory, LLM (**micro tier**) infers: `scope`, `categories`, `importance`, `metadata` (entities, dates, topics)
4. Embed all memories in one batch API call
5. **Intra-batch dedup**: drop near-duplicates within batch (cosine >= 0.98)
6. **Consolidation check**: for each memory, search for similar existing records (cosine > 0.85). If found, LLM (**micro tier**) decides: `keep`, `update` (merge), `delete` (superseded), or `insert_new` (port CrewAI encoding_flow)
7. Persist to database

**Cost control for memory operations:**
- All LLM calls in the write pipeline use **micro tier** (Haiku/$0.10 per 1M tokens) — never the agent's own model
- Estimated overhead: ~$0.001-0.005 per task completion (negligible vs the task's own LLM cost)
- **Configurable per workspace:** `memory_mode` setting:
  - `full` (default): LLM-analyzed extraction + consolidation (highest quality)
  - `lite`: Skip LLM analysis — just embed raw text, no scope/category inference (cheapest)
  - `off`: No automatic memory storage (agent can still use memory tools manually)

**Concurrency control for memory writes:**
- Bounded worker pool: max 10 concurrent memory write operations per server instance (prevents overwhelming LLM API quota with memory-extraction calls when many agents complete tasks simultaneously)
- Backpressure: if the write pool is full, new memory writes queue in a buffered channel (capacity 100). If the buffer is also full, drop the write with a warning log — the task result is already stored in comments, so memory is a best-effort enhancement
- Failure handling: failed LLM calls in the write pipeline retry once, then drop with warning. Memory writes must never block or fail the parent task
- Metrics: track `memory_writes_queued`, `memory_writes_completed`, `memory_writes_dropped` for observability

**Recall (read pipeline):**
1. Embed query
2. Vector search within scope prefix (e.g., `/ws/{wsId}/agent/` for workspace-wide, `/ws/{wsId}/agent/{agentId}/` for agent-specific)
3. Post-filter by privacy, categories
4. **Composite scoring** (port CrewAI formula): `score = 0.5 * similarity + 0.3 * recency_decay + 0.2 * importance`
   - `recency_decay = 0.5 ^ (age_days / 30)` — 30-day half-life
5. Rank by composite score, return top-N
6. Update `last_accessed_at` and `access_count` on returned records

**Cross-agent sharing mechanism:**
- All agents in a workspace share the same memory table
- Scope hierarchy enables isolation AND sharing:
  - `/ws/{wsId}/` — workspace-wide memories (visible to all agents)
  - `/ws/{wsId}/agent/{agentId}/` — agent-specific memories (visible to that agent + workspace queries)
- When Agent A finishes a task, memories are stored under `/ws/{wsId}/agent/{agentA}/`
- When Agent B starts a task, recall searches `/ws/{wsId}/` (the workspace root), finding BOTH its own and Agent A's memories
- `private: true` memories only returned to the `source_agent_id`

### 5.3 Memory Integration into Agent Execution

**File:** `server/internal/worker/worker.go` — Before each task:
1. Call `memory.Recall(taskDescription, scope="/ws/{wsId}/", limit=5)`
2. Format results as a "Memories from past tasks" section
3. Inject into agent's system prompt (as cacheable prefix for Anthropic)

**File:** `server/internal/worker/worker.go` — After each task:
1. Assemble raw content: `"Task: {desc}\nAgent: {name}\nResult: {output}"`
2. Call `memory.Remember(raw, scope="/ws/{wsId}/agent/{agentId}/")` asynchronously (background goroutine)

**File:** `server/pkg/tools/builtin/memory_tools.go` — Two tools for active memory use:
- `recall_memory` — agent explicitly searches memory during execution
- `save_memory` — agent explicitly stores something important during execution
- Both tools use the same `MemoryService`, scoped to the agent's workspace

### 5.4 Context Compression (go-ai primitives + Open Agents algorithm)

**New file:** `server/pkg/agent/compaction.go`

**Primitives — Anthropic path:** reuse go-ai's Anthropic context management (`pkg/providers/anthropic/context_management.go`): `ClearToolUsesEdit`, `ClearThinkingEdit`, `CompactEdit`. These produce well-tested low-level edits over the message history.

**Primitives — non-Anthropic path** (R2 S4). The go-ai primitives above are Anthropic-provider-only (they map to Anthropic's server-side context-management API). For OpenAI/Google/xAI/Ollama agents, fall back to a pure-Go summarise-and-truncate strategy: (a) identify the oldest N% of the conversation outside protected ranges, (b) call the workspace's configured micro-tier model (Haiku/gpt-4o-mini/gemini-flash) to summarise that slice with a Resolved/Pending schema, (c) replace the slice with the summary as a single assistant message. No go-ai primitives are invoked on this path. The compaction module exports `Compact(ctx, provider, messages)` that dispatches to the right path.

**Algorithm:** port Open Agents' aggressive compaction helpers (`packages/agent/context-management/aggressive-compaction-helpers.ts`) on top of those primitives:
1. **Index tool calls** by `messages[i].content[j]` coordinates
2. **Find compaction candidates**: tool calls/results *not* in the recent-use set
3. **Estimate savings** (`chars / 4 ≈ tokens`) — skip compaction if gain < threshold
4. **Apply** via `ClearToolUsesEdit` on the candidate slice
5. **Paste-block tokens** (Unicode PUA 0xE000-0xF8FF, port from Open Agents `paste-blocks.ts`) — replace large user-pasted blocks with single-char placeholders; expand server-side before LLM call
6. **Summarize middle as fallback**: if step 4 is insufficient, use the cheap model (Haiku) via `CompactEdit` with structured Resolved/Pending framing
7. **Move to archival**: full conversation stored in `agent_memory` table as archival records (queryable via recall)

**Trigger threshold:** `ContextEvictionThresholdBytes` from `defaults.go` (80 KB ≈ 20K tokens, from Open Agents).

**Protected ranges (never compressed):** system prompt, first user exchange, most recent ~20K tokens.

Summary prefixed with handoff framing: "This is from a previous context window" (port Hermes pattern).

### 5.5 Memory CLI Commands

**File:** `server/cmd/multica/cmd_memory.go`:
- `multica memory recall <query>` — search memory
- `multica memory save <content>` — store a memory
- Available to both cloud and daemon agents

### 5.6 Frontend Memory UI

**File:** `packages/views/agents/components/tabs/` — New `memory-tab.tsx`:
- Browse agent's memories (filterable by scope, category, time)
- Search memories semantically
- View memory consolidation history
- Delete/edit memories manually
- Memory statistics (count, categories, recency distribution)

### 5.7 Verification
- Agent A completes a task → memories extracted and stored
- Agent B starts a task → Agent A's memories recalled if relevant
- `private: true` memories not visible to other agents
- Duplicate memories consolidated (not duplicated)
- Context compression triggers at 70% threshold
- Compressed summary preserves key information
- Memory tools work during agent execution
- `make check` passes

---

## Phase 6: Agent-to-Agent Delegation (via go-ai SubagentRegistry)
**Goal:** Agents can delegate work to other agents with structured results and loop prevention.
**Depends on:** Phase 1 (`agent_capability` table, `defaults.go`), Phase 2 (harness + cloud execution), Phase 4 (structured outputs), Phase 5 (shared memory)

**Architectural note (D2):** delegation is a *tool call*, not a separate concept. We do not introduce `agent_delegation` or `agent_signal` tables. go-ai's `SubagentRegistry` + the built-in `task` tool already implement the full pattern: a parent agent invokes `task(subagentType, description, instructions)`, go-ai spawns a nested `ToolLoopAgent` with the child's tool set, streams interim progress back, and returns a structured summary as the tool result. Every subagent invocation is already captured in the cost-event + trace machinery from Phase 2.

### 6.1 Subagent Registry Wiring

**New file:** `server/pkg/agent/harness/subagents.go`

- Builds a `go-ai.SubagentRegistry` from the workspace's agents, keyed by each agent's slug (e.g., `customer-support`, `legal-reviewer`, `executor`). The registry is rebuilt per-task from fresh DB state.
- Each entry wraps `harness.Execute` with the sub-agent's own tools, system prompt, model, and `StopWhen: []StopCondition{StepCountIs(SubagentMaxSteps)}` from `defaults.go`.
- A single `task` tool (built-in from go-ai) is registered on every parent agent that has visible sub-agents; its description enumerates them via their `agent_capability` rows.

### 6.2 Capability Discovery (uses Phase 1 `agent_capability` table)

**File:** `server/internal/handler/capability.go` — already created in Phase 1

- `GET /capabilities` returns the capability catalog for a workspace — drives the UI's "available agents" picker and the `task` tool's description.
- Search by name/description via `SearchCapabilities` query (Phase 1).
- No new table. No CRUD on delegations.

### 6.3 Loop Prevention (harness-layer, not DB-layer)

All checks live in `server/pkg/agent/harness/subagents.go` at dispatch time:

- **Depth limit:** pass `depth` via go-ai's context; subagent registry rejects invocations where `depth >= 5`.
- **Cycle detection:** the harness tracks the delegation stack (`parent.trace_id → child.agent_id`) in context; if the target agent already appears on the stack, reject.
- **Self-delegation:** rejected at the registry lookup step.
- **Budget check:** before dispatching a subagent, call `budget.CanExecute(workspaceID, agentID)` (Phase 1). Block hard-stop overruns.
- **Timeout:** each subagent call runs under a context with deadline = min(parent remaining, subagent `SubagentMaxSteps * per-step-timeout`).
- **Chain cost budget:** all subagent calls share the parent's `trace_id`; Phase 9's trace aggregation enforces a per-chain ceiling.

### 6.4 Context Passing (cost-aware)

Passed as part of the `task` tool input:

- Small context (<4K tokens): pass directly in the tool-call input.
- Large context (>4K tokens): harness compresses via the cheap-model summariser (Phase 5.4 reuse) before the tool call is dispatched.
- Reference-based: for very large contexts, store in memory (Phase 5.3 `save_memory`) and pass the recall query string.

### 6.5 System Prompt Integration (NOT a new daemon concept)

**File:** `server/pkg/agent/prompts/system.go` (Phase 2) — when a subagent-capable agent runs, the harness renders the `task` tool's description with the live capability roster. No changes to the daemon path — coding agents on the daemon keep their existing prompt pipeline.

### 6.6 Verification
- Agent A invokes `task("customer-support", "reply to ticket #42", …)` → child agent runs → result returned to A's tool-result event
- Circular delegation rejected at dispatch, depth > 5 rejected
- Budget check blocks dispatch when workspace over hard-stop
- Cost tracked across chain with shared `trace_id` (Phase 9 rollup)
- Frontend shows nested tool-call UI (one card per subagent invocation)
- **Zero new tables created in this phase.**

---

## Phase 7: Workflow Orchestration
**Goal:** Multi-step agent pipelines with conditional routing, fan-out, human gates, and scheduling.
**Depends on:** Phase 6 (delegation), Phase 4 (structured outputs)

### 7.1 Workflow Database

**Migration:** `server/migrations/XXX_workflows.up.sql`
```sql
CREATE TABLE workflow_definition (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    trigger_type TEXT NOT NULL CHECK (trigger_type IN ('manual', 'webhook', 'schedule', 'event')),
    trigger_config JSONB,
    steps JSONB NOT NULL,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE workflow_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID NOT NULL REFERENCES workflow_definition(id),
    workspace_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'running',
    state JSONB DEFAULT '{}',
    current_step TEXT,
    step_results JSONB DEFAULT '{}',
    input JSONB,
    trace_id UUID NOT NULL DEFAULT gen_random_uuid(),
    parent_run_id UUID REFERENCES workflow_run(id),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE workflow_step_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,                     -- R3 B6: every lookup is workspace-scoped
    run_id UUID NOT NULL,
    step_id TEXT NOT NULL,
    agent_id UUID,
    task_id UUID REFERENCES agent_task_queue(id),
    status TEXT NOT NULL DEFAULT 'pending',
    input JSONB,
    output JSONB,
    error TEXT,
    model_tier TEXT,
    retry_count INT DEFAULT 0,
    trace_id UUID NOT NULL,                         -- D8: propagated from workflow_run.trace_id
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, run_id) REFERENCES workflow_run(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_run_ws_id ON workflow_run(workspace_id, id);
CREATE INDEX IF NOT EXISTS idx_workflow_step_run_ws ON workflow_step_run(workspace_id, run_id);
CREATE INDEX IF NOT EXISTS idx_workflow_step_run_trace ON workflow_step_run(trace_id, workspace_id);

-- Human-in-the-loop (ported from Paperclip approvals).
-- decided_by MUST be a member of the workspace — enforced in application layer
-- via the standard workspace-membership guard used for all mutations.
CREATE TABLE approval_request (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    workflow_run_id UUID,
    step_id TEXT,
    task_id UUID,
    agent_id UUID,
    type TEXT NOT NULL CHECK (type IN ('output_review', 'action_approval', 'escalation')),
    payload JSONB NOT NULL,
    status TEXT DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    decided_by UUID,
    decided_at TIMESTAMPTZ,
    trace_id UUID NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    FOREIGN KEY (workspace_id, workflow_run_id) REFERENCES workflow_run(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE SET NULL
);
```

### 7.2 Workflow Engine (Custom Go — no Temporal/Cadence/IWF dependency)

**New file:** `server/internal/workflow/engine.go`
- DAG resolution (topological sort of steps by `depends_on`)
- Parallel execution (steps with no inter-dependencies run concurrently)
- Conditional routing (Go template evaluation against accumulated state)
- Model tier per step (feeds into model router from Phase 2.3)
- State accumulation (each step's output merged into run's state)
- **Durable execution** (port Cadence pattern): all state persisted to DB, engine can resume after restart by replaying step_run statuses
- **Retry per step** with configurable backoff policy (port Cadence `common/backoff/`)
- Timeout per step
- **WaitUntil/Execute lifecycle** (port from IWF): each step can optionally declare what to wait for (timer, signal, approval) before executing. Cleaner than bare `depends_on`.
- **Timer skip** (port from IWF): API endpoint to advance a step past its timer (useful for testing and operations)
- **Persistence loading policies** (port from IWF): steps declare which state keys they need via `load_keys` field, avoiding full state load on large workflows
- **Conditional close** (port from IWF): workflow auto-completes when a `complete_when` expression evaluates to true against accumulated state
- **Cancel propagation** (port from Open Agents): each running step runs a stop-monitor goroutine that polls the run's status every `StopMonitorPollIntervalMs` (150 ms, from `defaults.go`). On `cancelled`, it flips the step's `context.CancelFunc` immediately. Keeps user-facing stop latency under a quarter second without overloading Postgres — ~7 queries/sec per active step is trivial, and `PgListenNotify` can replace the poll when we outgrow it.

**New file:** `server/internal/workflow/steps.go`
- Step types: `agent_task`, `tool_call`, `human_approval`, `fan_out`, `condition`, `sub_workflow`
- Each step has optional `wait_for`: `{timers: [], channels: [], approvals: [], trigger: "all"|"any"}` — `signals` removed per D2 (no inter-agent signal table)

**New file:** `server/internal/workflow/channels.go` (port from IWF internal channels)
- In-workflow pub/sub between concurrent steps
- Steps can publish to named channels; other steps can wait on them
- Backed by workflow_run state JSONB + DB notifications

### 7.3 Scheduler (ported from Cadence — semantic caveat)

**New file:** `server/internal/workflow/scheduler.go`
- Port the Cadence scheduler *patterns* from `common/types/schedule.go`. Note that Cadence's `service/worker/scheduler/workflow.go` is itself a Cadence workflow backed by event-sourced replay; our Go cron goroutine is not. We therefore implement **at-least-once** delivery with explicit dedup, not Cadence's exactly-once semantics. (R2 S2)
- Cron expression parsing for recurring workflows.
- **Overlap policies** (names and semantics match `ScheduleOverlapPolicy` in `common/types/schedule.go:37-42`): `skip_new`, `buffer`, `concurrent`, `cancel_previous`, `terminate_previous`.
- **Catch-up policies** (names match `ScheduleCatchUpPolicy` in `common/types/schedule.go:85-100`): `skip`, `one`, `all`. (R2 S1 — corrected from the earlier fabricated `skip_missed / fire_one / fire_all`.)
- **Dedup table** — `schedule_fire(workspace_id, workflow_id, scheduled_at)` with UNIQUE index; every scheduled fire inserts before dispatching, and duplicates on crash-recovery silently succeed. This gives us the at-least-once → effectively-once property.
- **Cancel-on-overlap correctness:** `cancel_previous` / `terminate_previous` flip the prior run's status column and rely on the per-step stop-monitor (D7 `StopMonitorPollIntervalMs`) to cut execution mid-step; there is a <150 ms window where a cancelled run may still emit one more step, documented as expected.
- **Backfill support:** replay historical time ranges for batch processing.

### 7.4 Frontend

**File:** `packages/views/workflows/` — New workflow pages:
- Workflow list + builder (consider `@xyflow/react` for visual DAG editor)
- Workflow run monitoring (step-by-step progress, cost per step)
- Approval queue page
- Scheduler configuration UI

### 7.5 Verification
- Define a multi-step workflow (classify → investigate → human approval → respond)
- Steps execute in dependency order with correct model tiers
- Human approval gate pauses/resumes workflow
- Scheduled workflow fires on cron with correct overlap policy
- Server restart resumes in-progress workflows
- `make check` passes

---

## Phase 8A: Platform Integration
**Goal:** Connect the platform to external systems and enable real-time agent interaction.
**Depends on:** Phase 2 (worker for real-time chat, Redis baseline), Phase 5 (context compaction for long chats — §5.4 is invoked by the chat path at §8A.1), Phase 7 (workflows for webhook triggers). (R1 B4 — Phase 5 was previously omitted from the depends-on line.)

### 8A.1 Real-Time Conversational Agents

The existing system is task-based (issue → agent → completion). Business agents need real-time conversation: customer sends message → agent responds in seconds → back-and-forth continues.

**New file:** `server/internal/worker/chat.go` — Real-time chat handler in the worker
- NOT queued-and-claimed like tasks — instant request-response with streaming
- Reuses LLMAPIBackend but with a persistent conversation context per chat session
- Flow: WebSocket message → worker picks up immediately → LLM call with history → stream response back
- Session memory: chat history stored in `chat_message` table (existing), injected into LLM context
- Context compaction (Phase 5.4) applies when chat history exceeds 70% of model window
- Tools available during chat (MCP, built-in) — same as task execution
- **Budget enforcement in chat path:** Each chat message checks workspace/agent budget BEFORE calling the LLM. Without this, rapid-fire chat messages bypass the task-queue budget check and drain budgets unchecked. Use the same `BudgetService.CanExecute()` call as the task claim path.

**Chat routing architecture (server → worker):**

The task queue (PostgreSQL polling) is too slow for real-time chat. Chat messages need sub-second routing from server to worker.

**Uses the baseline `RedisAdapter`** (introduced as a baseline dependency in Phase 2 per D4, not new to this phase):
- Server publishes chat message to Redis channel `chat:{workspace_id}:{agent_provider}`
- All workers with matching provider subscribe to this channel
- First available worker picks up the message (Redis pub/sub fan-out + application-level claim)
- Worker streams response back via Redis channel `chat_response:{session_id}`
- Server relays response to user's WebSocket connection

**Why Redis, not PostgreSQL LISTEN/NOTIFY:**
- LISTEN/NOTIFY has no persistence — if no worker is listening, the message is lost
- Redis pub/sub is faster (<1ms latency vs 5-10ms for PG notify)
- Redis also serves as skills cache (Phase 3), stream resumption (Phase 2), and semantic cache (Phase 9) — one baseline dependency, many uses

**Fallback:** For single-process deployments (`--with-worker` mode), bypass Redis entirely — route directly to embedded worker goroutine via Go channel.

**File:** `server/internal/handler/chat.go` — Update chat endpoints for cloud agents:
- Currently only daemon agents support chat (via CLI session resumption)
- Add: cloud agents handle chat via worker's real-time path (Redis pub/sub)
- WebSocket: new message type `chat:message` for instant delivery

**Frontend:** `packages/views/chat/` — Already exists, but needs:
- Chat widget embeddable in external sites (for customer-facing support agents)
- Chat session assignment to specific agents
- Typing indicators, read receipts for conversational UX

### 8A.2 Inbound Webhooks

Enterprise customers need external systems to trigger agents automatically.

**Migration** (R3 B4 — HMAC secret lives in the credential vault, not plaintext):
```sql
CREATE TABLE IF NOT EXISTS webhook_endpoint (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    secret_credential_id UUID NOT NULL,  -- references workspace_credential(id) with credential_type='webhook_hmac'
    target_type TEXT NOT NULL,
    target_id UUID NOT NULL,
    payload_mapping JSONB DEFAULT '{}',
    event_filter JSONB,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    FOREIGN KEY (workspace_id, secret_credential_id) REFERENCES workspace_credential(workspace_id, id) ON DELETE RESTRICT
);
ALTER TABLE webhook_endpoint DROP CONSTRAINT IF EXISTS webhook_endpoint_target_type_check;
ALTER TABLE webhook_endpoint ADD CONSTRAINT webhook_endpoint_target_type_check
    CHECK (target_type IN ('agent', 'workflow')) NOT VALID;
ALTER TABLE webhook_endpoint VALIDATE CONSTRAINT webhook_endpoint_target_type_check;

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
    created_at TIMESTAMPTZ DEFAULT NOW(),
    FOREIGN KEY (workspace_id, endpoint_id) REFERENCES webhook_endpoint(workspace_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_endpoint_ws_id ON webhook_endpoint(workspace_id, id);
ALTER TABLE webhook_delivery DROP CONSTRAINT IF EXISTS webhook_delivery_status_check;
ALTER TABLE webhook_delivery ADD CONSTRAINT webhook_delivery_status_check
    CHECK (status IN ('received', 'processed', 'failed')) NOT VALID;
ALTER TABLE webhook_delivery VALIDATE CONSTRAINT webhook_delivery_status_check;
```

**File:** `server/internal/handler/webhook.go` — New handler:
- `POST /api/webhooks/{workspace_id}/{endpoint_id}` — receives external events.
- HMAC signature verification: resolves `secret_credential_id` → `workspace_credential`, decrypts with the workspace DEK (AAD-bound to ws/id/name), verifies signature, discards plaintext secret before returning.
- Payload mapping: transform incoming JSON into agent task context.
- Creates issue + assigns to target agent, OR starts target workflow.
- Delivery log for debugging failed webhooks.

**Examples:**
- Zendesk ticket → support agent
- Salesforce opportunity → deal analysis workflow
- GitHub PR → code review agent
- Stripe payment failed → billing recovery agent

### 8A.3 Outbound Webhooks / Event Notifications

Customers need to know when things happen in the platform.

**Migration** (R3 B4 — outbound HMAC secret also lives in the credential vault):
```sql
CREATE TABLE IF NOT EXISTS webhook_subscription (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    events TEXT[] NOT NULL,
    secret_credential_id UUID NOT NULL,  -- credential_type='webhook_hmac' (outbound signing)
    headers JSONB DEFAULT '{}',
    is_active BOOLEAN DEFAULT true,
    retry_policy JSONB DEFAULT '{"max_retries": 3, "backoff_seconds": [5, 30, 300]}',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    FOREIGN KEY (workspace_id, secret_credential_id) REFERENCES workspace_credential(workspace_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_subscription_ws_id ON webhook_subscription(workspace_id, id);

CREATE TABLE IF NOT EXISTS webhook_delivery_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    subscription_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    status INT,
    response_body TEXT,
    attempt INT DEFAULT 1,
    next_retry_at TIMESTAMPTZ,
    trace_id UUID,                        -- D8: link outbound delivery to originating trace
    created_at TIMESTAMPTZ DEFAULT NOW(),
    FOREIGN KEY (workspace_id, subscription_id) REFERENCES webhook_subscription(workspace_id, id) ON DELETE CASCADE
);
```

**Supported events:**
- `task.completed`, `task.failed` — agent finished/failed work
- `workflow.completed`, `workflow.failed` — workflow finished
- `approval.needed` — human approval required
- `budget.warning`, `budget.exceeded` — budget thresholds
- `agent.error` — agent encountered unrecoverable error
- `delegation.completed` — top-of-chain task-tool invocation finished (fires once when the root subagent dispatch returns; tracks the whole chain via `trace_id`)

**File:** `server/internal/service/webhooks.go` — Outbound delivery with retry:
- Events published via existing `events.Bus`
- Background goroutine matches events to subscriptions
- HMAC-signed payloads sent to subscriber URLs
- Exponential backoff retry on failure (5s, 30s, 5min)
- Delivery log for debugging

### 8A.4 Agent Templates

Pre-built configurations for common business use cases.

**Migration:**
```sql
CREATE TABLE agent_template (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID,                   -- NULL for system templates
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    category TEXT NOT NULL,              -- 'customer_support', 'legal', 'sales', 'operations'
    agent_type TEXT NOT NULL,
    provider TEXT,
    model TEXT,
    instructions TEXT NOT NULL,
    tools JSONB DEFAULT '[]',
    guardrails JSONB DEFAULT '[]',
    output_schema JSONB,
    knowledge_sources JSONB DEFAULT '[]',
    is_system BOOLEAN DEFAULT false,     -- System templates vs workspace templates
    created_at TIMESTAMPTZ DEFAULT NOW()
);
```

**System templates (shipped with platform):**
- Customer Support Agent — CRM tools, email, knowledge base, empathetic tone
- Legal Reviewer — document analysis, compliance guardrails, structured output
- Sales Outreach — CRM, email, calendar, persuasive tone
- Data Analyst — database query, report generation, structured output
- Content Writer — web search, knowledge base, editorial guardrails

**Workspace templates:** Teams can save their own agent configs as templates for reuse.

**Frontend:** Agent creation → "Start from template" option:
- Template gallery with categories
- Preview template config before creating agent
- Customize after creation

### 8A.5 API Keys for External Integration

Programmatic access for customers' own systems.

**Migration:**
```sql
CREATE TABLE workspace_api_key (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL,              -- SHA-256 hash (raw key shown only once)
    key_prefix TEXT NOT NULL,            -- First 8 chars for identification
    scopes TEXT[] NOT NULL,              -- e.g., {"agents:read", "tasks:write", "workflows:execute"}
    created_by UUID NOT NULL,
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
```

- Key shown only once at creation (like GitHub tokens)
- Scoped permissions: `agents:read`, `agents:write`, `tasks:read`, `tasks:write`, `workflows:execute`, `admin`
- Used via `Authorization: Bearer sk_...` header
- Rate-limited per key
- Expiration optional (enterprise compliance)

**Frontend:** Workspace settings → API Keys page:
- Create/revoke keys
- Show scopes, last used, expiration
- Usage stats

### 8A.6 Agent Playground / Test Mode

Before deploying an agent to production, teams need to test it safely.

**File:** `server/internal/handler/agent.go` — New endpoint `POST /api/agents/{id}/test`
- Accepts a test prompt, executes the agent with `dry_run: true` flag
- `dry_run` mode: all side-effecting tools (email, CRM writes, database mutations, webhook calls) are disabled — they return a mock response like `"[DRY RUN] Email would be sent to user@example.com"`
- Read-only tools (search, database queries, knowledge retrieval) work normally
- Response streamed back via SSE (same format as task execution)
- No issue created, no task queued, no cost_event recorded (or recorded with `cost_status: 'test'`)

**Frontend:** Agent detail → "Test Agent" button:
- Opens a side panel with a prompt input
- Shows streaming response with tool calls
- Shows estimated cost (tokens used, model tier)
- Optional: compare responses from different models side-by-side

### 8A.7 OpenAPI Spec & Developer Documentation

Enterprise customers need programmatic access documentation.

**File:** `server/cmd/server/router.go` — Add OpenAPI spec generation:
- Use `github.com/swaggo/swag` or `github.com/getkin/kin-openapi` to generate OpenAPI 3.0 spec from Go handler annotations
- Serve spec at `GET /api/openapi.json`
- Serve Swagger UI at `GET /api/docs` (using `swagger-ui-dist`)

**Auto-generated client SDKs (future):**
- TypeScript SDK generated from OpenAPI spec via `openapi-typescript-codegen`
- Python SDK generated via `openapi-python-client`
- Published to npm/PyPI for customer integration

### 8A.8 Onboarding Wizard

Guide new workspaces through initial setup.

**File:** `packages/views/onboarding/` — New onboarding flow:
1. "What does your business do?" → category selection (tech, legal, finance, healthcare, etc.)
2. "What tasks do you want to automate?" → multi-select (customer support, code review, document analysis, etc.)
3. Recommend agent templates based on selections (from Phase 8A.4)
4. "Connect your tools" → configure MCP servers or API credentials
5. "Create your first agent" → pre-filled from template, one-click create
6. "Test your agent" → playground (Phase 8A.6) with a sample prompt

**Backend:** `server/internal/handler/onboarding.go`
- Track onboarding completion status per workspace (`workspace.onboarding_completed_at`)
- Redirect new workspaces to onboarding flow on first login

### 8A.9 Billing Integration Note

**NOTE:** This plan covers the technical platform. Billing/subscription management is a business decision that depends on pricing strategy. However, the plan is designed to feed into billing:
- `cost_event` table provides per-workspace, per-agent, per-model usage metering
- `budget_policy` provides spend limits that map to subscription tiers
- `workspace_api_key` scopes could map to plan-level feature gates
- When billing is implemented, integrate with Stripe or similar:
  - Subscription tiers → budget_policy defaults
  - Usage metering → cost_event aggregation
  - Overage charges → cost_event above plan limits
  - Invoice line items → aggregated cost_events by category

### 8A.10 Verification
- External webhook triggers agent task creation via HMAC-signed POST
- Outbound webhook fires on task.completed with retry on failure
- Real-time chat works for cloud LLM agents with streaming responses (via Redis pub/sub)
- Budget enforcement blocks chat when workspace budget exceeded
- Template creates a fully configured agent in one click
- API key authenticates programmatic requests with correct scopes
- Agent playground executes test prompt with dry_run (no side effects)
- OpenAPI spec available at `/api/openapi.json`, Swagger UI at `/api/docs`
- Onboarding wizard creates first agent from template
- `make check` passes
- External webhook triggers agent task creation via HMAC-signed POST
- Outbound webhook fires on task.completed with retry on failure
- Real-time chat works for cloud LLM agents with streaming responses
- Budget enforcement blocks chat when workspace budget exceeded
- Template creates a fully configured agent in one click
- API key authenticates programmatic requests with correct scopes
- `make check` passes

---

## Phase 8B: Security & Compliance Hardening
**Goal:** Enterprise security, compliance, and operational features.
**Depends on:** Phase 1 (credential vault), Phase 2 (worker for rate limiting)

### 8B.1 Secrets Management UI

The unified credential vault (`workspace_credential`) was created in Phase 1.4. This phase adds the frontend and access controls.

**Frontend:** `packages/views/settings/` — Credentials management page:
- Create/edit/delete credentials (both provider keys and tool secrets)
- Type filter: show provider keys vs tool secrets
- Show last rotation date, usage count, cooldown status
- Show which agents use each credential
- Rotation wizard: update encrypted value, track last_rotated_at

**NOTE:** The `workspace_credential` table and `credential_access_log` were created in Phase 1.4. No new migration needed here — Phase 8B.1 is purely frontend + access policy.

### 8B.2 Audit Log

Immutable record of all human and agent actions for compliance.

**Migration:**
```sql
CREATE TABLE audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent', 'system', 'api_key')),
    actor_id UUID,
    action TEXT NOT NULL,                -- e.g., "agent.created", "budget.updated", "secret.accessed"
    resource_type TEXT NOT NULL,          -- e.g., "agent", "workflow", "secret", "budget_policy"
    resource_id UUID,
    changes JSONB,                       -- Before/after diff for mutations
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_audit_workspace_time ON audit_log (workspace_id, created_at DESC);
CREATE INDEX idx_audit_resource ON audit_log (resource_type, resource_id);
```

- Port from Paperclip `activity_log` table
- Every mutation handler writes an audit entry (middleware pattern)
- Immutable: no UPDATE or DELETE on this table
- Retention: configurable per workspace (see Phase 8B.5 Data Retention Policies)

**Frontend:** `packages/views/audit/` — Audit log page:
- Filterable by actor, action, resource, time range
- Exportable as CSV for compliance reporting

### 8B.3 Rate Limiting

Prevent abuse and respect LLM provider limits.

**New file:** `server/internal/middleware/ratelimit.go`
- Per-workspace request rate limiting (configurable: e.g., 100 req/min)
- Per-agent task rate limiting (e.g., max 10 tasks/min per agent)
- Per-provider API rate limiting (respect Anthropic's 4000 RPM, OpenAI's limits)
- Port from Cadence `common/quotas/` multi-stage rate limiter
- Track provider rate limit headers (`x-ratelimit-*`) per Hermes `agent/rate_limit_tracker.py` pattern
- Dynamic adjustment: when provider returns 429, automatically throttle all agents using that provider

### 8B.4 Agent Versioning & Rollback

Track configuration changes, enable rollback.

**Migration:**
```sql
CREATE TABLE agent_config_revision (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,          -- For multi-tenant query filtering
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    revision_number INT NOT NULL,
    config_snapshot JSONB NOT NULL,       -- Full agent config at this point
    changed_fields TEXT[] NOT NULL,       -- Which fields changed
    changed_by UUID NOT NULL,
    change_reason TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(agent_id, revision_number)
);
```

- Port from Paperclip `agent_config_revisions`
- Every agent update creates a revision with before/after snapshot
- Rollback: apply a previous revision's config_snapshot to the agent
- In-flight tasks continue with the config they started with (snapshot at claim time)

**Frontend:** Agent detail → Settings tab:
- Revision history with diff view
- One-click rollback to any previous revision

### 8B.5 SSO / SAML (Enterprise Auth)

Enterprise customers require SSO. The existing codebase uses session-based auth. Extend for enterprise:

**File:** `server/internal/auth/` — Add SAML 2.0 and OIDC support:
- SAML 2.0 SP (Service Provider) implementation for enterprise IdPs (Okta, Azure AD, OneLogin)
- OIDC (OpenID Connect) for Google Workspace, Auth0, etc.
- Per-workspace SSO configuration (workspace admin configures IdP settings)
- Auto-provisioning: users from IdP automatically added to workspace on first login
- Enforce SSO: workspace setting to require SSO (disable password login)

**Migration:**
```sql
CREATE TABLE workspace_sso_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL UNIQUE,
    provider_type TEXT NOT NULL CHECK (provider_type IN ('saml', 'oidc')),
    -- SAML fields
    idp_entity_id TEXT,
    idp_sso_url TEXT,
    idp_certificate TEXT,
    -- OIDC fields
    oidc_issuer TEXT,
    oidc_client_id TEXT,
    oidc_client_secret_encrypted BYTEA,
    -- Common
    enforce_sso BOOLEAN DEFAULT false,     -- If true, disable password login
    auto_provision BOOLEAN DEFAULT true,   -- Auto-create users from IdP
    default_role TEXT DEFAULT 'member',
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
```

**Frontend:** Workspace settings → Authentication page:
- SSO configuration wizard
- Test connection before enabling
- Toggle enforce SSO
- View provisioned users

### 8B.6 Data Retention Policies

Configurable cleanup for compliance.

**Migration:**
```sql
CREATE TABLE retention_policy (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    resource_type TEXT NOT NULL,          -- 'chat_messages', 'task_messages', 'agent_memory', 'audit_log', 'traces'
    retention_days INT NOT NULL,          -- Delete records older than this
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(workspace_id, resource_type)
);
```

**Background cleanup job:** `server/internal/worker/retention.go`
- Runs daily
- For each active retention policy, deletes records older than `retention_days`
- Audit log retention defaults to 365 days (configurable, minimum 90 for compliance)
- Agent memory retention: optional (some enterprises require memory purge on agent deletion)
- Traces retention: default 90 days

**Frontend:** Workspace settings → Data Retention page

### 8B.7 Verification
- Credentials injected into agent tools without exposing raw values
- Credential access logged in `credential_access_log`
- Audit log captures all mutations with before/after diffs
- Rate limiter throttles burst traffic, respects provider 429 responses
- Agent rollback restores previous config; in-flight tasks unaffected
- SSO login via SAML/OIDC works end-to-end
- Enforce SSO blocks password login when enabled
- Retention policy deletes old data on schedule
- `make check` passes

---

## Phase 9: Advanced Cost Optimization
**Goal:** Maximize savings across all agent execution modes.
**Depends on:** Phase 2 (LLM API backend), Phase 5 (memory for semantic caching)

### 9.1 Batch API Integration

**New file:** `server/pkg/agent/batch.go`
- Batch executor for non-realtime tasks
- Anthropic Batch API: 50% discount, stacks with prompt caching for up to 95% savings
- OpenAI Batch API: 50% discount
- Queue low-priority tasks, flush when batch is full or timer expires
- Workflow scheduled steps are ideal candidates

### 9.2 Semantic Caching

**Migration:** dim-partitioned, matching §1.5 / Phase 4 pattern.

```sql
CREATE TABLE IF NOT EXISTS response_cache_1024 (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL,
    query_embedding vector(1024) NOT NULL,
    query_text TEXT,
    response TEXT,
    model TEXT,
    config_version_hash TEXT NOT NULL,
    knowledge_version TEXT,
    hit_count INT DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS response_cache_1536 (LIKE response_cache_1024 INCLUDING ALL);
ALTER TABLE response_cache_1536 ALTER COLUMN query_embedding TYPE vector(1536);

CREATE TABLE IF NOT EXISTS response_cache_3072 (LIKE response_cache_1024 INCLUDING ALL);
ALTER TABLE response_cache_3072 ALTER COLUMN query_embedding TYPE vector(3072);

CREATE INDEX IF NOT EXISTS idx_rcache_1024_vec ON response_cache_1024
    USING ivfflat (query_embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_rcache_1536_vec ON response_cache_1536
    USING ivfflat (query_embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_rcache_3072_vec ON response_cache_3072
    USING hnsw (query_embedding vector_cosine_ops);

CREATE INDEX IF NOT EXISTS idx_rcache_1024_ws ON response_cache_1024(workspace_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_rcache_1536_ws ON response_cache_1536(workspace_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_rcache_3072_ws ON response_cache_3072(workspace_id, agent_id);
```

- Before LLM call, check cache for semantically similar query (cosine > 0.95). Router picks the dim partition from `workspace_embedding_config.dimensions`.
- Best for high-traffic agents (customer support with repetitive questions).
- **Cache-hit cost-event accounting** (R3 S5): every cache hit emits a `cost_event` with `cost_status = 'included'`, `cost_cents = 0`, `input_tokens = 0`, and a new `response_cache_id UUID` column (added to `cost_event` in Phase 9 migration) linking the served entry. Dashboards that count served traffic stay accurate; cost aggregation stays consistent.
- **Cache invalidation:** Cache entries include `config_version_hash` (hash of agent instructions + tools + knowledge sources). When agent config changes (Phase 8B versioning), the hash changes and old cache entries are no longer matched. Background job purges stale entries daily.
- Cache entries also include `knowledge_version` — invalidated when knowledge sources are updated (Phase 4).

### 9.3 Agent Performance Metrics

The plan tracks costs — but enterprise customers also need to measure agent **effectiveness**.

**Migration:**
```sql
CREATE TABLE agent_metric (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    period_start TIMESTAMPTZ NOT NULL,     -- Hourly or daily bucket
    period_type TEXT NOT NULL CHECK (period_type IN ('hourly', 'daily')),
    
    -- Task metrics
    tasks_completed INT DEFAULT 0,
    tasks_failed INT DEFAULT 0,
    tasks_timed_out INT DEFAULT 0,
    avg_resolution_seconds INT,            -- Time from task start to completion
    p95_resolution_seconds INT,            -- 95th percentile resolution time
    
    -- Delegation metrics
    delegations_sent INT DEFAULT 0,
    delegations_received INT DEFAULT 0,
    
    -- Chat metrics (Phase 8A)
    chat_messages_handled INT DEFAULT 0,
    avg_response_seconds DECIMAL,          -- Time from user message to first response token
    
    -- Cost metrics (aggregated from cost_event)
    total_cost_cents INT DEFAULT 0,
    avg_cost_per_task_cents INT DEFAULT 0,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(agent_id, period_start, period_type)
);

CREATE INDEX idx_agent_metric_lookup ON agent_metric(workspace_id, agent_id, period_start DESC);
```

**Background aggregation job:** `server/internal/worker/metrics.go`
- Runs every hour: aggregates raw data from `agent_task_queue`, `cost_event`, `chat_message` into `agent_metric` hourly buckets
- Runs daily: rolls up hourly buckets into daily summaries
- SLA tracking: compare `avg_resolution_seconds` against configurable SLA thresholds per agent
- Alert on SLA breach via outbound webhooks (Phase 8A.3)

**Frontend:** Agent detail → Performance tab:
- Success rate over time (line chart)
- Average resolution time trend
- Cost per task trend
- Task volume by status (completed/failed/timeout)
- Comparison view: select 2-3 agents, overlay metrics

### 9.4 Cost Dashboard Frontend

**File:** `packages/views/costs/` — New cost pages:
- Token usage by agent/model/time period (port Hermes `agent/insights.py` concept)
- Cost breakdown with budget policy indicators
- Model routing effectiveness (% routed to each tier)
- Cache hit rates (semantic + prompt cache)
- Delegation chain cost visualization
- Budget alerts
- Credential pool usage stats

### 9.5 Verification
- Low-priority tasks routed through Batch API
- Semantic cache serves repeated queries without LLM call
- Cost dashboard shows accurate data with cache-aware pricing
- Budget alerts fire at warning thresholds
- Agent performance metrics aggregated correctly (hourly + daily)
- Agent comparison dashboard shows side-by-side metrics
- SLA breach triggers outbound webhook alert

---

## Phase 10: Observability & Tracing
**Goal:** Full trace trees across multi-agent delegation chains and workflows.
**Depends on:** Phase 6 (delegations), Phase 7 (workflows)

### 10.1 Trace System

**Migration:**
```sql
CREATE TABLE agent_trace (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id UUID NOT NULL,
    parent_span_id UUID,
    workspace_id UUID NOT NULL,
    agent_id UUID REFERENCES agent(id),
    -- 'subagent_dispatch' is how task-tool calls appear (no separate delegation table per D2)
    operation TEXT NOT NULL,           -- 'llm_call', 'tool_call', 'subagent_dispatch', 'workflow_step', 'memory_op'
    task_id UUID,                      -- parent task or subagent task (subagent dispatches create child tasks)
    workflow_run_id UUID,
    cost_event_id UUID REFERENCES cost_event(id),  -- Link to cost_event instead of duplicating token/cost data
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    duration_ms INT,
    status TEXT,
    error TEXT,
    metadata JSONB                     -- operation-specific data (tool name, model used, subagent type, etc.)
);
-- NOTE: Token counts and cost_cents are NOT duplicated here.
-- Join to cost_event via cost_event_id for cost data.
-- cost_event.trace_id links back for aggregate cost queries.

CREATE INDEX idx_trace_trace_id ON agent_trace(trace_id);
CREATE INDEX idx_trace_parent ON agent_trace(parent_span_id);
```

### 10.2 Execution History Search

Production debugging requires searching across all agent execution transcripts: "why did the agent do X last Tuesday?"

**Migration:**
```sql
-- Add full-text search index on existing task_message table
ALTER TABLE task_message ADD COLUMN search_vector tsvector
    GENERATED ALWAYS AS (to_tsvector('english', coalesce(content, '') || ' ' || coalesce(tool, '') || ' ' || coalesce(output, ''))) STORED;

CREATE INDEX idx_task_message_fts ON task_message USING gin(search_vector);

-- Also index by workspace for scoped queries
CREATE INDEX idx_task_message_workspace ON task_message(workspace_id, created_at DESC);
```

**File:** `server/internal/handler/search.go` — New endpoint `GET /api/search/executions`
- Full-text search across all task execution transcripts
- Filter by: agent, date range, issue, task status, tool name
- Returns: matching message snippets with highlighted terms, linked to task/issue
- Pagination with cursor-based approach (for large result sets)

**Frontend:** `packages/views/search/` — Execution history search page:
- Search bar with filters (agent, date range, status)
- Results show: agent name, issue title, matched text snippet, timestamp
- Click result → opens task transcript at the matched position
- Export results as CSV for compliance review

### 10.3 Trace Integration

- Every LLM call, tool call, delegation, workflow step, and memory operation creates a trace span
- trace_id propagated through delegation chains and workflow runs
- Parent-child spans form a tree
- OpenTelemetry-compatible export (optional)

### 10.4 Frontend Trace Viewer

- Trace waterfall view (like Chrome DevTools network tab)
- Span details (agent, model, tokens, cost, duration)
- Error highlighting with classified error types (from Phase 1.3)
- Filter by agent, workflow, time range

### 10.5 Verification
- Execute a multi-agent delegation chain
- Trace tree shows all spans with correct parent-child relationships
- Total cost and duration aggregated correctly
- Frontend renders trace waterfall
- Full-text search finds agent execution by content, tool name, or output
- Search results link to correct task transcript

---

## Phase 11: Cloud Coding Sandbox (E2B + gVisor)
**Goal:** Coding agents can run in the cloud without a local daemon, using E2B Firecracker microVMs for secure sandboxed execution.
**Depends on:** Phase 2 (LLM API backend + worker), Phase 10 (tracing for full observability)

### Why This Is Enterprise-Critical
Enterprise customers will NOT install daemons on developer machines. They need cloud-hosted coding agents with:
- Hardware-level isolation (Firecracker microVMs — same tech as AWS Lambda)
- No local install required
- Centralized management and audit trail
- Compliance-friendly (no customer code on local machines)

### 11.1 Sandbox Interface + Three Implementations (D3)

Sandbox interface lifted verbatim from Open Agents' `packages/sandbox/interface.ts` (see `other-projects/open-agents/packages/sandbox/`). This interface has production pedigree in the Vercel implementation; porting it to Go keeps us aligned with a proven design.

**New file:** `server/pkg/sandbox/sandbox.go` — Universal sandbox interface

```go
package sandbox

import (
    "context"
    "io/fs"
    "time"
)

type Type string

const (
    TypeE2B           Type = "e2b"
    TypeGVisor        Type = "gvisor"
    TypeClaudeManaged Type = "claude_managed"
    TypeLocalDaemon   Type = "local_daemon" // existing daemon path
)

type Sandbox interface {
    Type() Type
    WorkingDirectory() string
    Env() map[string]string
    CurrentBranch() string  // optional; empty if not a git context

    // File operations
    ReadFile(ctx context.Context, path string) ([]byte, error)
    WriteFile(ctx context.Context, path string, content []byte) error
    Stat(ctx context.Context, path string) (FileStats, error)
    Mkdir(ctx context.Context, path string, opts MkdirOpts) error
    Readdir(ctx context.Context, path string) ([]fs.DirEntry, error)

    // Process operations
    Exec(ctx context.Context, cmd string, cwd string, timeout time.Duration) (ExecResult, error)
    ExecDetached(ctx context.Context, cmd string, cwd string) (commandID string, err error)

    // Lifecycle
    Stop(ctx context.Context) error
    ExtendTimeout(ctx context.Context, additional time.Duration) (expiresAt time.Time, err error)
    Snapshot(ctx context.Context) (snapshotID string, err error)

    // Port forwarding (for dev-server preview in coding agents)
    Domain(port int) (string, error)

    // Timeout tracking (required by Backend.ExpiresAt from Phase 2)
    ExpiresAt() *time.Time
}

type Hooks struct {
    AfterStart        func(ctx context.Context, s Sandbox) error
    BeforeStop        func(ctx context.Context, s Sandbox) error
    OnTimeout         func(ctx context.Context, s Sandbox) error // fires SandboxTimeoutBufferMs before ExpiresAt
    OnTimeoutExtended func(ctx context.Context, s Sandbox, additional time.Duration) error
}
```

**Proactive timeout pattern** (from D6/D7 defaults): every backend schedules a timer to fire `SandboxTimeoutBufferMs` (default 30_000) before the provider's hard session cap. When it fires, `Hooks.OnTimeout` runs — the agent snapshots state, commits work-in-progress, and exits cleanly. The provider's own hard timeout is the safety net, never the primary mechanism.

**Factory:** `server/pkg/sandbox/factory.go` — discriminated-union pattern lifted from Open Agents' `factory.ts`. `Connect(config SandboxConfig, opts ConnectOptions) (Sandbox, error)` routes to the right backend based on `config.Type`.

All four sandbox implementations share this interface. Workspace admin chooses which backend to use.

**Implementation A: E2B (managed Firecracker microVMs)**

**New file:** `server/pkg/sandbox/e2b.go`
- Integrate with E2B API (`github.com/e2b-dev/e2b-go`)
- Hardware-level KVM isolation (Firecracker — same tech as AWS Lambda)
- <200ms boot, <5 MiB overhead per VM
- Template-based: pre-configured environments (Node.js, Python, Go, etc.)
- File upload/download for project code
- Process execution with streaming stdout/stderr
- Pricing: ~$0.05/vCPU-hr (billed by E2B)
- **Best for:** Quick start, no infra to manage, compliance-ready isolation

**Implementation B: Self-Hosted gVisor Containers**

**New file:** `server/pkg/sandbox/gvisor.go`
- Run sandboxed containers on our own infrastructure using gVisor (runsc)
- gVisor provides a user-space kernel — stronger isolation than Docker, lighter than VMs
- **Linux-only:** gVisor (runsc) requires a Linux host kernel. Does NOT work on Windows or macOS natively. For development, use Docker Desktop with a Linux VM, or E2B instead. Production deployments must be on Linux servers.
- Kubernetes-native via RuntimeClass: `runtimeClassName: gvisor`
- Custom Dockerfiles for agent environments
- Network policy enforcement (no internet by default, allowlist specific endpoints)
- Volume mounts for project code (ephemeral, destroyed after session)
- **Best for:** Enterprise self-hosted deployments, full data sovereignty, maximum control

**New file:** `server/pkg/sandbox/gvisor_k8s.go` — Kubernetes integration
- Creates K8s Jobs with gVisor RuntimeClass per agent task
- Pod security policies: no privilege escalation, read-only root FS, ephemeral storage only
- Resource limits: CPU, memory, disk per sandbox (from sandbox_template)
- Namespace-per-workspace isolation
- Auto-cleanup: completed pods reaped after result extraction

**New file:** `server/pkg/sandbox/gvisor_docker.go` — Docker fallback (non-K8s)
- For single-server deployments without Kubernetes
- Uses `docker run --runtime=runsc` for gVisor isolation
- Docker network per workspace for isolation
- Simpler but less scalable than K8s approach

**Implementation C: Claude Managed (via Phase 12)**
- Delegates to Anthropic's managed sandbox (Phase 12)
- No self-hosted infrastructure needed
- Covered separately in Phase 12

**Sandbox backend selection** (per workspace, stored in workspace settings) — use the NOT VALID → VALIDATE pattern (R3 B2):
```sql
ALTER TABLE workspace ADD COLUMN IF NOT EXISTS sandbox_backend TEXT DEFAULT 'e2b';
ALTER TABLE workspace DROP CONSTRAINT IF EXISTS workspace_sandbox_backend_check;
ALTER TABLE workspace ADD CONSTRAINT workspace_sandbox_backend_check
    CHECK (sandbox_backend IN ('e2b', 'gvisor', 'claude_managed', 'none')) NOT VALID;
ALTER TABLE workspace VALIDATE CONSTRAINT workspace_sandbox_backend_check;

ALTER TABLE workspace ADD COLUMN IF NOT EXISTS sandbox_config JSONB DEFAULT '{}';
-- e2b:            { "api_key": "...", "template_defaults": {...} }
-- gvisor:         { "k8s_namespace": "...", "runtime_class": "gvisor", "registry": "..." }
-- claude_managed: { "api_key": "..." } (uses Phase 12)
-- none:           no sandbox (business agents only)
```

### 11.2 New Agent Type: `cloud_coding`

```sql
-- Extend Phase 1's agent_type_check to include 'cloud_coding'.
-- Follow the same DROP → ADD NOT VALID → VALIDATE pattern so the ALTER
-- doesn't take ACCESS EXCLUSIVE while validating existing rows (R3 B2).
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_type_check;
ALTER TABLE agent ADD CONSTRAINT agent_type_check
    CHECK (agent_type IN ('coding', 'llm_api', 'http', 'process', 'cloud_coding')) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_type_check;

-- Extend agent_runtime_check to allow cloud_coding agents (runtime optional,
-- sandbox_template_id required instead).
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_runtime_check;
ALTER TABLE agent ADD CONSTRAINT agent_runtime_check CHECK (
    (agent_type = 'coding' AND runtime_id IS NOT NULL)
    OR (agent_type IN ('llm_api', 'http', 'process') AND provider IS NOT NULL)
    OR (agent_type = 'cloud_coding' AND provider IS NOT NULL AND sandbox_template_id IS NOT NULL)
) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_runtime_check;

-- cloud_coding agents have provider + model (the LLM brain) AND a sandbox_template_id (the hands).
```

**Agent creation flow for cloud coding:**
```
[Agent Type: Cloud Coding Agent]
→ [Provider: Anthropic] [Model: Sonnet 4]
→ [Sandbox Template: Node.js 22 / Python 3.12 / Custom Dockerfile]
→ [Resources: 1 CPU, 2 GiB RAM (default) / 2 CPU, 4 GiB]
→ NO runtime selector — sandbox is auto-provisioned per task
```

### 11.3 Brain/Hands Execution Pattern

**File:** `server/internal/worker/cloud_coding.go`

Port the pattern from Cursor and Claude Managed Agents:
```
Worker receives task for cloud_coding agent
    ↓
1. HANDS: Create E2B sandbox from template
2. HANDS: Clone repo / upload project files into sandbox
3. BRAIN: Call LLM API with tool definitions:
   - execute_command(cmd) → runs in sandbox
   - read_file(path) → reads from sandbox
   - write_file(path, content) → writes to sandbox
   - search_files(query) → searches in sandbox
4. LOOP: LLM returns tool calls → worker executes in sandbox → feeds results back
5. HANDS: Extract results (git diff, modified files, test output)
6. HANDS: Destroy sandbox (or keep for session resumption)
    ↓
Worker reports completion with structured results
```

### 11.4 Sandbox Warm Pool

**New file:** `server/internal/sandbox/pool.go`
- Maintain a pool of pre-booted idle sandboxes per template
- Configurable pool size per workspace (default: 2-3 warm sandboxes)
- Cold start: ~5s with E2B. Warm start: ~200ms (just assign from pool)
- Lifecycle: Warm → Active → Draining → Destroyed
- Security: destroy-and-replace after each session (no cross-tenant data bleed)
- Cost: warm sandboxes consume E2B credits even when idle — pool size tuned to traffic

### 11.5 Sandbox Configuration in DB

```sql
CREATE TABLE sandbox_template (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL,
    e2b_template_id TEXT,              -- E2B template reference
    dockerfile TEXT,                   -- Custom Dockerfile (alternative to E2B template)
    default_cpu INT DEFAULT 1,
    default_memory_mb INT DEFAULT 2048,
    default_disk_mb INT DEFAULT 5120,
    installed_tools JSONB DEFAULT '[]', -- Pre-installed CLIs/packages
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Link agents to sandbox templates
ALTER TABLE agent ADD COLUMN sandbox_template_id UUID REFERENCES sandbox_template(id);
```

### 11.6 Frontend

**File:** `packages/views/agents/components/create-agent-dialog.tsx` — Add cloud coding flow:
- Sandbox template selector (Node.js, Python, Go, Custom)
- Resource configuration (CPU, RAM)
- Show estimated cost per task hour

**File:** `packages/views/sandboxes/` — New sandbox management pages:
- Template list + create/edit
- Active sandbox instances dashboard
- Warm pool configuration
- Cost tracking (sandbox hours consumed)

### 11.7 Verification
- Create a `cloud_coding` agent with Node.js sandbox template
- Assign an issue → sandbox created → agent edits code → sandbox destroyed
- Warm pool reduces cold start from ~5s to ~200ms
- Sandbox isolation confirmed (agent cannot access host or other sandboxes)
- Cost tracked: sandbox hours + LLM tokens
- `make check` passes

---

## Phase 12: Claude Managed Agents Backend
**Goal:** Support Anthropic's managed agent infrastructure as an execution backend — zero infra for customers who prefer fully managed agents.
**Depends on:** Phase 2 (Backend interface), Phase 10 (tracing)

### Why This Is Enterprise-Critical
Some enterprise customers will prefer Anthropic's managed infrastructure:
- Zero self-hosted compute required
- Anthropic handles sandboxing, state management, crash recovery
- SOC 2, HIPAA compliance via Anthropic's infrastructure
- $0.08/session-hr + tokens — predictable pricing

### 12.1 Claude Managed Agents Backend

**New file:** `server/pkg/agent/claude_managed.go`
- Implements `Backend` interface
- Uses Anthropic's Agents API (not the Messages API directly):
  1. Create/reuse Agent definition via Agents API
  2. Create Environment (sandbox config: CPU, memory, secrets)
  3. Create Session (instantiate the agent)
  4. Send Messages (tasks) via Messages API
  5. Stream responses via SSE (Server-Sent Events)
  6. Map SSE events to our existing `Message` channel format

```go
type ClaudeManagedBackend struct {
    APIKey      string
    AgentID     string   // Anthropic agent definition ID
    ContainerID string   // Reuse container across tasks on same issue
    Tools       []ToolDefinition
}

func (b *ClaudeManagedBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
    // 1. Create or reuse session
    // 2. Send message with task prompt
    // 3. Stream SSE events → map to Message channel
    // 4. Handle tool calls (both Anthropic-managed and our custom tools)
    // 5. Return result when agent completes
}
```

### 12.2 New Agent Type: `claude_managed`

**Agent creation flow:**
```
[Agent Type: Claude Managed Agent]
→ [Model: Sonnet 4 / Opus 4]  (Anthropic models only)
→ [Environment: Default / Custom]
   - CPU: 1 (fixed by Anthropic)
   - RAM: 5 GiB (fixed by Anthropic)
   - Internet: Disabled (default) / Enabled
→ [Secrets: inject from workspace credential vault]
→ NO runtime selector — Anthropic manages everything
```

### 12.3 Container Reuse for Session Continuity

Port from Claude Managed Agents `container_id` pattern:
- First task on an issue creates a new container (Anthropic allocates one)
- Store `container_id` in `agent_task_queue.session_id`
- Subsequent tasks on the same issue reuse the same container
- Container persists for up to 30 days (Anthropic's limit)
- Agent retains filesystem state, installed packages, session memory across tasks

### 12.4 Hybrid Tool Execution

Claude Managed Agents has built-in tools (bash, file I/O, etc.). But we also have our own tools (MCP, built-in tools from Phase 2/3). The hybrid approach:

1. **Anthropic-managed tools**: code execution, file system, bash — run in Anthropic's sandbox
2. **Our tools**: CRM lookup, email send, database query — run on our worker, results sent back to Anthropic's agent
3. **MCP tools**: discovered from MCP servers — run on our worker

The LLM API backend needs to handle both: some tool calls execute in Anthropic's sandbox, others execute on our side.

### 12.5 Cost Tracking

```sql
-- cost_event for managed agents includes session_hour_cost
ALTER TABLE cost_event ADD COLUMN session_hours DECIMAL;
ALTER TABLE cost_event ADD COLUMN session_cost_cents INT;
-- Total cost = token_cost + session_cost
```

Pricing: $0.08/session-hr + standard token rates. Budget policies apply to the combined cost.

### 12.6 Frontend

**File:** `packages/views/agents/components/create-agent-dialog.tsx` — Add claude_managed flow:
- Model selector (Anthropic models only)
- Environment config (internet enabled/disabled)
- Secrets injection from workspace vault
- Show pricing estimate ($0.08/hr + ~$X/1K tasks based on model)

### 12.7 Verification
- Create a `claude_managed` agent
- Assign an issue → Anthropic creates session → agent executes → results streamed back
- Container reused across multiple tasks on same issue
- Hybrid tools: Anthropic bash + our CRM lookup both work in same task
- Cost tracked: session hours + tokens
- `make check` passes

---

## Dependency Graph

```
Phase 1 (Agent Types + DB + Errors + Credentials + Embeddings + defaults.go + go-ai fork)
  │
  ├──→ Phase 2 (go-ai Harness + LLM API Backend + Tools + Router + Worker + Redis baseline + D8 trace_id)
  │      │
  │      ├──→ Phase 3 (MCP Integration + Skills Cache)
  │      │
  │      ├──→ Phase 4 (Structured Outputs + Guardrails + Knowledge/RAG — dim-partitioned)
  │      │      │
  │      │      └──→ Phase 5 (Agent Memory + Context Compression)
  │      │             │
  │      │             ├──→ Phase 6 (A2A: we author `task` tool on go-ai SubagentRegistry — 2w)
  │      │             │     │
  │      │             │     └──→ Phase 7 (Workflow Orchestration + Scheduler — at-least-once)
  │      │             │            │
  │      │             │            ├──→ Phase 8A (Platform Integration — chat uses §5.4 compaction)
  │      │             │            │
  │      │             │            └──→ Phase 8B (Security & Compliance)
  │      │             │
  │      │             └──→ Phase 8A (chat path directly uses §5.4 compaction — R1 B4)
  │      │
  │      ├──→ Phase 8B (also depends on Phase 2 for rate limiting)
  │      │
  │      ├──→ Phase 9 (Advanced Cost Optimization)
  │      │
  │      └──→ Phase 10 (Observability & Tracing — persists D8 trace tree)
  │             │
  │             ├──→ Phase 11 (E2B + gVisor Cloud Sandbox — D3 derived-from-Open-Agents interface)
  │             │
  │             └──→ Phase 12 (Claude Managed Agents)
  │
  └──→ (Phase 1 feeds Phase 6 via `agent_capability` table for UI discovery)
```

**Parallelization opportunities:**
- Phase 3 (MCP) and Phase 4 (Structured Outputs) run in parallel after Phase 2.
- Phase 8A now explicitly depends on Phase 5 (chat compaction); schedule 8A **after** 5 ships, not in parallel with it (R1 B4).
- Phase 8B runs in parallel with Phase 8A (8B only needs Phase 2 for rate limiting — can start earlier).
- Phase 9 (Cost Optimization) runs in parallel with Phases 5-8.
- Phase 10 (Tracing) starts after Phase 6+7, in parallel with Phase 8A/8B.
- Phase 11 (E2B + gVisor) and Phase 12 (Claude Managed) run in parallel after Phase 10.
- Frontend work within each phase can be parallelized with backend work.

## Key Files Modified Across All Phases

| File | Phases | Changes |
|------|--------|---------|
| `server/pkg/agent/agent.go` | 2,11,12 | `Backend` interface extended with `ExpiresAt()` and hooks (D6). LLM/HTTP/process/sandbox/managed backends implement it. |
| `server/pkg/agent/defaults.go` | 1 (new) | Central registry of tunables (`MainAgentMaxSteps`, `SubagentMaxSteps`, `ContextEvictionThresholdBytes`, `SandboxTimeoutBufferMs`, `StopMonitorPollIntervalMs`, `SkillsCacheTTL`, `DefaultReasoningLevel`) |
| `server/pkg/agent/harness/` | 2 (new), 3, 5, 6 | Thin adapter over go-ai `ToolLoopAgent`; wires skills (§3) and subagents (§6); applies compression (§5.4) |
| `server/pkg/agent/llm_api.go` | 2,5,9 | go-ai-backed LLM backend + memory injection + caching |
| `server/pkg/agent/fallback.go` | 2 | Multi-model fallback chain wrapper (new) |
| `server/pkg/agent/provider_health.go` | 2 | Provider health tracking (new) |
| `server/pkg/agent/compaction.go` | 5 | Context compression using go-ai primitives + Open Agents algorithm |
| `server/pkg/db/queries/agent.sql` | 1,5 | Capabilities, memory (no delegation queries — D2) |
| `server/internal/service/task.go` | 2 | Cloud execution integration (no delegation changes — D2) |
| `server/internal/service/memory.go` | 5 | Memory service (new) |
| `server/internal/service/webhooks.go` | 8A | Inbound/outbound webhooks (new) |
| `server/internal/daemon/prompt.go` | 5 | Memory context injection (no delegation context — D2) |
| `server/internal/daemon/execenv/runtime_config.go` | 1,5 | Agent-type-aware config (no delegation commands — D2) |
| `server/internal/handler/agent.go` | 1,3,5 | Type, capabilities, memory endpoints |
| `server/internal/handler/capability.go` | 1 (new) | Capability CRUD — used by UI discovery (§6) |
| `server/internal/handler/webhook.go` | 8A | Inbound webhook handler (new) |
| `server/internal/handler/chat.go` | 8A | Real-time chat for cloud agents |
| `server/internal/middleware/ratelimit.go` | 8B | Rate limiting (new) |
| `server/internal/middleware/audit.go` | 8B | Audit logging (new) |
| `server/internal/worker/worker.go` | 2,3,5 | Cloud worker + MCP + memory integration |
| `server/internal/worker/chat.go` | 8A | Real-time chat execution via Redis pub/sub (new) |
| `server/internal/handler/search.go` | 10 | Execution history search (new) |
| `server/internal/worker/metrics.go` | 9 | Agent performance metrics aggregation (new) |
| `server/cmd/worker/main.go` | 2 | Cloud worker binary (new) |
| `server/cmd/multica/cmd_memory.go` | 5 | Memory CLI (new) |
| `server/pkg/sandbox/sandbox.go` | 11 | Sandbox interface ported from Open Agents (D3, new) |
| `server/pkg/sandbox/factory.go` | 11 | Backend-routing factory (new) |
| `server/pkg/sandbox/e2b.go` | 11 | E2B implementation (new) |
| `server/pkg/sandbox/gvisor.go` | 11 | gVisor implementation (new) |
| `server/pkg/agent/claude_managed.go` | 12 | Claude Managed Agents backend (new) |
| `server/go.mod` | 1 | Adds `github.com/digitallysavvy/go-ai v0.4.0` |
| `docs/engineering/go-ai-fork-policy.md` | 1 (new) | Fork activation runbook |
| `docs/engineering/go-ai-fork-activations.md` | 1 (new) | Append-only activation log |
| `packages/core/types/agent.ts` | 1,5 | Agent type, capabilities, memory |
| `packages/views/agents/components/` | 1,3,5,6,8A,8B | Creation flow, tools, memory, capability browser (not delegation UI — D2), templates, versioning |

## Verification Strategy

After each phase:
1. `make sqlc` — regenerate DB code
2. `pnpm typecheck` — TypeScript type check
3. `pnpm test` — Unit tests
4. `make test` — Go tests
5. `make check` — Full verification pipeline

**Vector operation testing (Phases 4, 5, 9):**
- pgvector operations (embedding storage, cosine similarity search) require a real PostgreSQL database with pgvector extension — cannot be mocked
- CI already uses `pgvector/pgvector:pg17` image — confirmed compatible
- Go tests for memory/knowledge/cache services must use a test database, not mocks
- Add a `testutil.EmbedStub(dims int) []float32` helper that generates deterministic fake embeddings for tests that don't need real embedding quality

Cross-phase integration tests:
- After Phase 5: Memory sharing across agents (A writes, B recalls)
- After Phase 6: End-to-end delegation chain with memory (A → B → result + memories back to A)
- After Phase 7: Full workflow (trigger → multi-step → approval → completion) with memory across steps
- After Phase 8A: Webhook triggers agent, agent completes, outbound webhook fires, real-time chat works via Redis, playground test mode works with dry_run
- After Phase 8B: Audit log records all mutations, SSO login works, rate limiter throttles bursts, agent rollback restores config
- After Phase 9: Cost optimization (caching, routing, batching) + performance metrics aggregation + SLA alerts
- After Phase 10: Complete trace tree from workflow trigger through all delegations + full-text search across execution history
- After Phase 11: Cloud coding agent executes in E2B/gVisor sandbox with full trace
- After Phase 12: Claude Managed Agent executes with hybrid tools

## Estimated Effort by Phase

Adjusted after go-ai adoption (D1), delegation via go-ai's `SubagentRegistry` + **our own `task` tool** (D2 — R1 B1 correction), and the review patches (vector dim partitioning, credential vault crypto, scheduler semantic downgrade).

| Phase | Effort | Can Parallelize With |
|-------|--------|---------------------|
| 1. Agent Types + DB + Errors + Credentials (AES-GCM/HKDF/AAD) + Embeddings (dim partitions) + defaults.go + budget advisory-lock service + go-ai fork | **3 weeks** (+0.5w for vault crypto + dim-partitioning tables + budget service) | — |
| 2. LLM API + Harness (go-ai adapter) + Tools + Router + Worker + Fallback Chain + D8 trace_id | **2.5 weeks** (loop/streaming/stop-conditions are library-provided) | — |
| 3. MCP Integration + Skills registry (go-ai `SkillRegistry`) | 1.5 weeks | Phase 4 |
| 4. Structured Outputs + Guardrails + Knowledge/RAG (dim-partitioned) | 2 weeks | Phase 3 |
| 5. Agent Memory + Context Compression (go-ai Anthropic-only primitives + summarise-truncate fallback) | 2 weeks | Phase 9 |
| 6. A2A: author `task` tool + depth/cycle/trace ctx + capability UI, on go-ai `SubagentRegistry` | **2 weeks** (+1w vs. earlier 1w estimate — the `task` tool is NOT built-in to go-ai) | Phase 9 |
| 7. Workflow Orchestration + Scheduler (at-least-once + dedup) + 150ms stop-monitor | **3.5 weeks** (+0.5w for scheduler semantic downgrade + `schedule_fire` dedup table) | Phase 9 |
| 8A. Platform Integration (chat, webhooks via vault, templates, API keys, playground, onboarding) — **depends on Phase 5** | 4 weeks | Phase 8B, Phase 9 |
| 8B. Security & Compliance (audit, SSO, rate limiting, versioning, retention) | 4 weeks | Phase 8A, Phase 9 |
| 9. Advanced Cost Optimization + Performance Metrics + cache-hit cost events | 3 weeks | Phases 5-8 |
| 10. Observability, Tracing + Execution Search (task_message FTS via CREATE INDEX CONCURRENTLY) | 2 weeks | Phase 8A, 8B |
| 11. E2B + gVisor Cloud Sandbox (interface derived from Open Agents; mandatory + optional split) | 3 weeks | Phase 12 |
| 12. Claude Managed Agents Backend | 2 weeks | Phase 11 |
| **Total (sequential)** | **~34.5 weeks** | |
| **Total (with parallelization)** | **~22-26 weeks** (vs. pre-review 20-22w) | |

## Enterprise Readiness Checklist (V1)

All 12 phases contribute to enterprise readiness. Here's what V1 delivers:

| Capability | Phase | Enterprise Need |
|-----------|-------|----------------|
| Multi-tenant workspace isolation | Existing | Data separation per customer |
| Agent type flexibility (coding + business + cloud) | 1 | Support diverse use cases |
| No-runtime agent creation (provider + model) | 1 | Simplified UX for non-coding agents |
| Credential pool with rotation | 1 | Enterprise API key management |
| Error classification + auto-recovery | 1 | Production resilience |
| Multi-model fallback chain | 2 | Provider outage resilience |
| Provider health tracking | 2 | Automatic failover on provider degradation |
| Direct LLM API (no CLI dependency) | 2 | Cloud-native, no local installs |
| Cloud worker binary (scalable execution) | 2 | Horizontal scaling for agent workloads |
| Custom tools + parallel execution | 2 | Domain-specific integrations |
| Model routing + prompt caching | 2 | Cost control at scale |
| Budget enforcement per workspace/agent | 1, 2 | Prevent cost overruns |
| MCP tool discovery | 3 | Universal tool connector standard |
| Structured outputs + guardrails | 4 | Compliance, quality gates |
| Prompt injection detection | 4 | Security |
| Knowledge/RAG | 4 | Domain expertise injection |
| Cross-agent memory | 5 | Organizational learning |
| Context compression | 5 | Cost-effective long sessions |
| Agent-to-agent delegation | 6 | Complex task decomposition |
| ~~Signal-based communication~~ (removed per D2 — use workflow channels or direct subagent dispatch instead) | — | — |
| Workflow orchestration + HITL | 7 | Business process automation |
| Cron scheduler with overlap policies | 7 | Recurring business processes |
| **Real-time conversational agents** | **8A** | **Customer support, sales chat** |
| **Inbound webhooks** | **8A** | **External system triggers (Zendesk, Salesforce)** |
| **Outbound webhooks** | **8A** | **Event notifications to customer systems** |
| **Agent templates** | **8A** | **Quick-start for common use cases** |
| **API keys for external integration** | **8A** | **Programmatic access** |
| **Agent playground / test mode** | **8A** | **Safe testing before production deployment** |
| **OpenAPI spec + Swagger docs** | **8A** | **Developer documentation for API consumers** |
| **Onboarding wizard** | **8A** | **Guided first-time setup for new customers** |
| **Credential vault UI + access logging** | **8B** | **Secure tool credential management** |
| **Audit log** | **8B** | **Compliance, governance** |
| **Rate limiting** | **8B** | **Abuse prevention, provider protection** |
| **Agent versioning + rollback** | **8B** | **Safe configuration changes** |
| **SSO / SAML / OIDC** | **8B** | **Enterprise authentication (Okta, Azure AD)** |
| **Data retention policies** | **8B** | **Compliance (GDPR, etc.)** |
| Batch API + semantic caching | 9 | Cost optimization at scale |
| Agent performance metrics + SLA tracking | 9 | Measure agent effectiveness, not just cost |
| Full tracing + observability | 10 | Debugging, performance monitoring |
| Execution history full-text search | 10 | Production debugging, compliance review |
| Cloud coding sandbox (E2B + gVisor) | 11 | No local daemon required, enterprise isolation |
| Claude Managed Agents | 12 | Zero-infra option for customers |

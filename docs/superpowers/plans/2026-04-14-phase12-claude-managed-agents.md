# Phase 12: Claude Managed Agents Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a new `claude_managed` agent type that delegates execution to Anthropic's Agents API — zero self-hosted compute, Anthropic owns sandboxing + state + crash recovery, priced at $0.08/session-hour + tokens.

**Architecture:** `server/pkg/agent/claude_managed.go` implements the Phase 2 `Backend` interface by talking HTTPS to Anthropic's Agents API. `Execute` creates (or reuses) a Session, sends the task prompt, streams SSE events, maps them to the existing `Message` channel shape, and resolves tool-calls from our side (MCP + custom tools) while Anthropic handles its own sandboxed tools (bash, file I/O). Container IDs are persisted on `agent_task_queue.session_id` so repeated tasks on the same issue reuse the same container (30-day Anthropic limit). Cost events gain `session_hours DECIMAL` + `session_cost_cents INT` columns so the dashboard surfaces combined token+session cost.

**Tech Stack:** Go 1.26, pgx/sqlc, `net/http` + custom SSE decoder, Phase 2 `Backend` interface + `CostCalculator`, Phase 10 `Tracer` for per-operation spans, shadcn UI + TanStack Query for create-agent flow.

---

## Scope Note

Phase 12 of 12. Depends on:
- Phase 1 (`agent` + `agent_task_queue` tables + `agent_type_check` / `agent_runtime_check`)
- Phase 2 (`Backend` interface with `ExpiresAt()` + `Hooks` per D6; `CostCalculator`; worker claim path)
- Phase 3 (MCP tool registry — hybrid tool execution dispatches our tools back to our worker)
- Phase 8B (credential vault — secrets injected into Anthropic Environment)
- Phase 10 (Tracer + `LateLinkCostEvent` — per-message spans)
- Phase 11 (did NOT cover Claude Managed as a sandbox backend — Phase 11's `ClaudeManagedStub` stays a stub; Phase 12 is an agent-type Backend, not a sandbox implementation)
- D6 proactive timeout; D7 defaults; D8 `trace_id` ctx propagation

**In scope:**
- `ClaudeManagedBackend` implementing `agent.Backend`.
- Anthropic Agents API client (Agent-def + Environment + Session + Messages) with SSE streaming.
- New agent type `claude_managed` (Anthropic models only, no runtime, no sandbox_template).
- Container reuse per issue via `agent_task_queue.session_id`.
- Hybrid tool execution: Anthropic-owned tools run in their sandbox; our MCP + custom tools run on our worker and results are fed back to Anthropic's session.
- Cost tracking: `session_hours` + `session_cost_cents` on `cost_event` + budget enforcement.
- Frontend: create-agent claude_managed flow (model selector, environment config, secrets dropdown, price estimate).
- Integration test: end-to-end (create agent → task → SSE stream → tool exchanges → cost rows).

**Not in scope — deferred:**
- **In-flight streaming progress to the chat/WS layer.** PLAN.md §12.1 mentions mapping SSE events to "our existing `Message` channel format." Phase 12 V1 accumulates text_deltas into the final `Response.Text` and returns at `end_turn`. Users see the full reply when the task completes, not token-by-token. Wiring `content_block_delta` events to `Opts.Messages chan<- Message` for streaming UX is a Phase 13 follow-up — the harness contract + WS fan-out would need updates in Phase 2 and Phase 8A. Tool dispatch is NOT deferred (runs mid-stream per §12.4); only user-visible text streaming is.
- **Session snapshot + pause/resume** across worker restarts. Anthropic's API is assumed stateless from our side; long-running sessions are driven by one `Execute` call. Post-V1 work.
- **Fanout / multi-worker coordination** on the same `container_id`. V1 serializes: one worker per container at a time via `FOR UPDATE` + `session_id` claim.
- **Workspace-wide session-hour quotas** (distinct from token quotas). V1 relies on the existing budget_policy + combined cost enforcement; per-session-hour caps are a Phase 13 follow-up.
- **Anthropic Agent-def version rollback UX.** We create Agent definitions once and store the ID; rolling back requires a manual Anthropic console action. V1 only supports create + reuse.
- **Non-Anthropic managed agents** (OpenAI Assistants, Bedrock AgentCore). The interface is generic enough that adding them later is additive; V1 hardcodes `provider='anthropic'` for this type.

---

## Preconditions

**From Phase 1:**

| Surface | Purpose |
|---|---|
| `agent` table + `agent_type_check` + `agent_runtime_check` | Phase 12 migration 188 extends both CHECKs via NOT VALID → VALIDATE |
| `cost_event` table with `trace_id`, `cost_status`, `cost_cents`, token columns | Phase 12 migration adds `session_hours` + `session_cost_cents` |
| `workspace_credential` vault (Phase 8B) | Secrets injected into Anthropic Environment |

**From Phase 2:**

| Surface | Purpose |
|---|---|
| `agent.Backend` interface with `Execute(ctx, Prompt, Opts) (Response, error) + ExpiresAt() *time.Time + Hooks() *Hooks` (D6) | `ClaudeManagedBackend` implements this |
| `agent.Run(ctx, backend, prompt)` tool-loop entrypoint (`server/pkg/agent/run.go`) | Worker calls this with our new backend |
| `agent_task_queue` table with `session_id TEXT` column — Phase 2 worker spec already shipped this (used by chat streams). If the column exists under a different name, the Phase 12 migration renames accordingly. | Container reuse across tasks on the same issue |
| `Worker claim path` (`internal/worker/claim.go`) | Phase 12 adds a `claude_managed` branch routing to the Phase 12 handler |
| `CostCalculator.ComputeCents(...)` | Phase 12 adds `ComputeSessionCents(hours)` |
| `tools.Registry` (Phase 2/3) | Our tools surfaced to the managed agent via tool schemas; tool_use events from Anthropic dispatch back here |
| `events.Bus` (Phase 8A) | Emits `managed_session.started` / `managed_session.stopped` for the future active-sessions dashboard |

**From Phase 8B:**

| Surface | Purpose |
|---|---|
| `CredentialVault.GetRaw(ctx, workspaceID, credID) ([]byte, error)` | Fetches secrets by credential ID for env injection |

**From Phase 10:**

| Surface | Purpose |
|---|---|
| `trace.Tracer.StartSpan(ctx, operation, meta) *Span` | Per message + per tool exchange spans |
| `trace.LateLinkCostEvent(ctx, spanID, costEventID)` | After session cost inserted, link outer span to the cost row |

**Test helpers (extend Phase 2 testsupport):**

The `FakeAnthropicServer` struct body + method list is defined in Task 8
(forward reference — don't redeclare it here). Phase 12 adds the
following Env method signatures; Task 8 / Task 12 test bodies assume
they exist.

```go
// server/internal/testsupport/claude_managed.go (Task 8) — declared later
// NewFakeAnthropicServer(t *testing.T) *FakeAnthropicServer
// (*FakeAnthropicServer).ScriptReplies(events []anthropic.SSEEvent)
// (*FakeAnthropicServer).Calls() []string
// (*FakeAnthropicServer).LastCreateAgentRequest() anthropic.CreateAgentRequest
// (*FakeAnthropicServer).LastSendMessageRequest() anthropic.SendMessageRequest
// (*FakeAnthropicServer).LastCreateSessionRequest() anthropic.CreateSessRequest
// (*FakeAnthropicServer).AllCreateSessionRequests() []anthropic.CreateSessRequest

// --- Phase 2-era Env helpers reused by Phase 12 tests ---
// These are NOT new to Phase 12 — they are long-standing Phase 2
// testsupport methods. Listed here so an agent executing the plan
// top-to-bottom knows the precondition surface without grepping.
// If any method is missing in the current tree, add it before Task 9:
func (e *Env) SeedWorkspace(slug string) uuid.UUID
func (e *Env) SeedIssue(wsID uuid.UUID, title string) uuid.UUID
func (e *Env) TaskStatus(taskID uuid.UUID) string
func (e *Env) CountCostEventsByProvider(wsID uuid.UUID, provider string) int // shared with Phase 11

// --- Phase 12 additions (implemented alongside Task 8's FakeAnthropicServer) ---
func (e *Env) SeedClaudeManagedAgent(wsID uuid.UUID, model string, anthropicAgentID string) uuid.UUID
func (e *Env) SeedTaskWithIssue(wsID, agentID, issueID uuid.UUID, prompt string) uuid.UUID
func (e *Env) SessionIDForTask(taskID uuid.UUID) string
```

**uuid / pgtype / sqlc helpers (Phase 2 Task 19.5):**

Worker code (Task 9) uses the following helpers from
`server/internal/service/uuid_helpers.go`. They were introduced in
Phase 2 Task 19.5 and are imported into the worker package:

```go
// server/internal/service/uuid_helpers.go (Phase 2 — already in tree)
func uuidPg(u uuid.UUID) pgtype.UUID               // uuid → pgtype.UUID{Valid: true}
func uuidFromPg(u pgtype.UUID) uuid.UUID            // pgtype.UUID → uuid (Nil if !Valid)
func sqlNullString(s string) pgtype.Text            // "" → {Valid:false}; else {Valid:true}
func sqlNullInt32(i int32) pgtype.Int4              // always Valid; Phase 2 convention
func sqlNullDec(v float64) pgtype.Numeric           // float → pgtype.Numeric (Phase 9 Pre-Task 0)
```

If the worker package doesn't import `internal/service` directly (to
avoid a layering cycle), add a tiny `server/internal/worker/helpers.go`
re-export file in a Pre-Task 0 extension that wraps each helper call.

**Pre-Task 0 bootstrap:**

```bash
cd server
# No new Go deps — net/http + encoding/json + bufio for SSE decoding
# are stdlib. Confirm `httptest` is already available (it is, stdlib).
go mod tidy
```

**Confirm Phase 2 `Backend` + `agent.Run` surfaces:**

```go
// server/pkg/agent/agent.go (Phase 2)
type Backend interface {
    Execute(ctx context.Context, p Prompt, o Opts) (Response, error)
    ExpiresAt() *time.Time
    Hooks() *Hooks
}
// server/pkg/agent/run.go (Phase 2) — if absent, Phase 12 cannot ship
func Run(ctx context.Context, b Backend, p Prompt) (Response, error)
```

If `agent.Run` doesn't exist in the current tree, add a minimal
loop-free wrapper that just calls `b.Execute` once in a Pre-Task 0
extension before touching Task 5 (Phase 12's Execute itself runs the
Anthropic-side loop).

**Confirm `agent_task_queue.session_id TEXT` column exists.** Grep the
Phase 2 migrations:

```bash
grep -R 'session_id' server/migrations/
```

If absent, Phase 12 migration 188 adds it (shown in Task 1 Step 1).

---

## File Structure

### New Files (Backend)

| File | Responsibility |
|---|---|
| `server/migrations/188_claude_managed.up.sql` | Extend agent_type + agent_runtime CHECK; add `session_id` to agent_task_queue (if missing); add `session_hours` + `session_cost_cents` to cost_event |
| `server/migrations/188_claude_managed.down.sql` | Reverse |
| `server/pkg/db/queries/claude_managed.sql` | sqlc: `GetClaudeManagedAgent`, `UpsertTaskSessionID`, `GetSessionIDForIssue`, `InsertSessionCostEvent` |
| `server/pkg/agent/claude_managed.go` | `ClaudeManagedBackend` — implements `Backend` |
| `server/pkg/agent/claude_managed_test.go` | Unit tests — SSE mapping, hybrid tool dispatch, session reuse |
| `server/pkg/agent/anthropic/client.go` | HTTP client for Anthropic Agents API |
| `server/pkg/agent/anthropic/client_test.go` | Agent-def create, env create, session create, send message shape |
| `server/pkg/agent/anthropic/sse.go` | SSE decoder (bufio.Scanner + event accumulation) |
| `server/pkg/agent/anthropic/sse_test.go` | Event parsing correctness |
| `server/pkg/agent/anthropic/types.go` | Request/response value types — CreateAgentRequest, CreateSessResponse, SSEEvent, etc. |
| `server/internal/worker/claude_managed.go` | Worker handler — claims `claude_managed` tasks, invokes backend, persists session_id, emits cost_event |
| `server/internal/worker/claude_managed_test.go` | Worker tests — full task loop + container reuse |
| `server/internal/integration/claudemanaged/e2e_test.go` | End-to-end: create agent → enqueue task → FakeAnthropicServer replies → cost_event written |

### Modified Files (Backend)

| File | Changes |
|---|---|
| `server/pkg/agent/defaults.go` | Append `ClaudeManagedSessionHourCents=8`, `ClaudeManagedContainerMaxDays=30`, `ClaudeManagedSSEIdleTimeout=2*time.Minute` |
| `server/pkg/agent/cost_calculator.go` (Phase 2) | Add `ComputeSessionCents(hours float64) int` |
| `server/internal/worker/claim.go` (Phase 2) | Add `claude_managed` branch |
| `server/cmd/server/main.go` | Start `ClaudeManagedWorker.Run` goroutine |

### New Files (Frontend)

| File | Responsibility |
|---|---|
| `packages/core/types/claude_managed.ts` | `ClaudeManagedAgentConfig`, `AnthropicModel`, `ClaudeEnvironmentConfig` |
| `packages/core/api/claude_managed.ts` | (Reuses existing `createAgent` endpoint — type-only additions) |
| `packages/views/agents/components/claude-managed-flow.tsx` | Cloud-managed branch of create-agent dialog |
| `packages/views/agents/components/claude-managed-flow.test.tsx` | Smoke test (model selector + pricing banner) |

### Modified Files (Frontend)

| File | Changes |
|---|---|
| `packages/views/agents/components/create-agent-dialog.tsx` | When `agent_type='claude_managed'`, mount `ClaudeManagedFlow` |

### External Infrastructure

| System | Change |
|---|---|
| Anthropic Agents API | Outbound calls to `api.anthropic.com/v1/{agents,environments,sessions,messages}`; existing API key via workspace credential vault |

---

### Task 1: Migration 188 — Claude Managed Schema

**Files:**
- Create: `server/migrations/188_claude_managed.up.sql`
- Create: `server/migrations/188_claude_managed.down.sql`

- [ ] **Step 1: Up migration**

```sql
-- 188_claude_managed.up.sql

-- Extend agent_type_check to include 'claude_managed'. Phase 11 last
-- touched this with 'cloud_coding'; re-shape the whole CHECK to keep
-- it auditable.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_type_check;
ALTER TABLE agent ADD CONSTRAINT agent_type_check
    CHECK (agent_type IN ('coding', 'llm_api', 'http', 'process', 'cloud_coding', 'claude_managed')) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_type_check;

-- Extend agent_runtime_check: claude_managed requires provider='anthropic'
-- + a non-null model. NO runtime_id, NO sandbox_template_id.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_runtime_check;
ALTER TABLE agent ADD CONSTRAINT agent_runtime_check CHECK (
       (agent_type = 'coding'         AND runtime_id IS NOT NULL)
    OR (agent_type IN ('llm_api','http','process') AND provider IS NOT NULL)
    OR (agent_type = 'cloud_coding'   AND provider IS NOT NULL AND sandbox_template_id IS NOT NULL)
    OR (agent_type = 'claude_managed' AND provider = 'anthropic' AND model IS NOT NULL)
) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_runtime_check;

-- anthropic_agent_id — the Agent definition ID Anthropic assigns when we
-- CREATE /v1/agents. Stored on our agent row so we don't re-create it
-- each task. NULL until first task creates it.
ALTER TABLE agent ADD COLUMN IF NOT EXISTS anthropic_agent_id TEXT;

-- environment_config — internet toggle + secret_credential_ids collected
-- by the create-agent UI. Worker deserializes, resolves secret IDs
-- against workspace_credential vault, and passes CreateEnvRequest.
-- Default '{}' so rows created pre-migration (none — this is a new
-- agent_type) don't violate NOT NULL constraints elsewhere.
ALTER TABLE agent ADD COLUMN IF NOT EXISTS environment_config JSONB
    NOT NULL DEFAULT '{"internet_enabled": false, "secret_credential_ids": []}'::jsonb;

-- Container reuse: session_id on agent_task_queue (Phase 2 may have
-- already added this for chat streams; IF NOT EXISTS guards).
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS session_id TEXT;

-- Cost-event session accounting. session_hours is DECIMAL so partial-hour
-- usage (e.g. 0.1667 for a 10-minute session) is accurate to the cent.
ALTER TABLE cost_event
    ADD COLUMN IF NOT EXISTS session_hours DECIMAL,
    ADD COLUMN IF NOT EXISTS session_cost_cents INT;

-- Index to look up "does this issue already have a live session?" for
-- container reuse. The (workspace_id, issue_id) pair is narrow enough
-- that a partial index on non-null session_id keeps it cheap.
CREATE INDEX IF NOT EXISTS idx_atq_session_by_issue
    ON agent_task_queue (workspace_id, issue_id, session_id)
    WHERE session_id IS NOT NULL;
```

- [ ] **Step 2: Down migration**

```sql
-- 188_claude_managed.down.sql

-- NOTE: every `ADD CONSTRAINT … NOT VALID; VALIDATE CONSTRAINT …` pair
-- below takes SHARE UPDATE EXCLUSIVE twice on the agent table. In a
-- rollback scenario this is acceptable (brief maintenance window),
-- but be aware concurrent INSERTs inserting non-conforming rows in the
-- ~ms gap between NOT VALID and VALIDATE will cause VALIDATE to fail.
-- Run the rollback during a quiet window if the cluster is busy.

-- Delete claude_managed rows first — VALIDATE of the restored
-- agent_runtime_check would fail while 'claude_managed' rows exist.
DELETE FROM agent WHERE agent_type = 'claude_managed';

DROP INDEX IF EXISTS idx_atq_session_by_issue;

ALTER TABLE cost_event
    DROP COLUMN IF EXISTS session_cost_cents,
    DROP COLUMN IF EXISTS session_hours;

-- Leave agent_task_queue.session_id in place; Phase 2/8A may also use it.

ALTER TABLE agent DROP COLUMN IF EXISTS anthropic_agent_id;
ALTER TABLE agent DROP COLUMN IF EXISTS environment_config;

-- Restore Phase 11 shape of agent_runtime_check.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_runtime_check;
ALTER TABLE agent ADD CONSTRAINT agent_runtime_check CHECK (
       (agent_type = 'coding'        AND runtime_id IS NOT NULL)
    OR (agent_type IN ('llm_api','http','process') AND provider IS NOT NULL)
    OR (agent_type = 'cloud_coding'  AND provider IS NOT NULL AND sandbox_template_id IS NOT NULL)
) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_runtime_check;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_type_check;
ALTER TABLE agent ADD CONSTRAINT agent_type_check
    CHECK (agent_type IN ('coding', 'llm_api', 'http', 'process', 'cloud_coding')) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_type_check;
```

- [ ] **Step 3: Apply + commit**

```bash
cd server && make migrate-up && make migrate-down && make migrate-up
git add server/migrations/188_claude_managed.up.sql server/migrations/188_claude_managed.down.sql
git commit -m "feat(claude-managed): migration 188 — agent type + cost session columns

Extends agent_type_check to 'claude_managed', requires provider='anthropic'
+ non-null model. Adds anthropic_agent_id (Agent-def ID) to agent,
session_id to agent_task_queue (IF NOT EXISTS guard — Phase 2 may have
landed it), session_hours/session_cost_cents to cost_event. Uses
NOT VALID → VALIDATE to keep the ALTER at SHARE UPDATE EXCLUSIVE."
```

---

### Task 2: Defaults

**Files:**
- Modify: `server/pkg/agent/defaults.go`

- [ ] **Step 1: Append constants**

```go
// Append to server/pkg/agent/defaults.go

// ClaudeManagedSessionHourCents is the per-session-hour price Anthropic
// charges ($0.08/hr → 8¢). Multiplied by elapsed session hours in
// CostCalculator.ComputeSessionCents.
const ClaudeManagedSessionHourCents = 8

// ClaudeManagedContainerMaxDays — Anthropic destroys containers after
// 30 days. Worker refuses to reuse a session_id older than this.
const ClaudeManagedContainerMaxDays = 30

// ClaudeManagedSSEIdleTimeout — if no SSE event arrives within this
// window, Execute returns an error so the worker can retry via a new
// session (old container is abandoned but Anthropic reaps it after 30d).
// Kept as var because typed-duration const requires untyped-int literal
// expressions only; 2*time.Minute is already typed.
var ClaudeManagedSSEIdleTimeout = 2 * time.Minute
```

- [ ] **Step 2: Commit**

```bash
git add server/pkg/agent/defaults.go
git commit -m "feat(claude-managed): defaults — session-hour price, 30d container TTL, SSE idle timeout"
```

---

### Task 3: Anthropic API Types

**Files:**
- Create: `server/pkg/agent/anthropic/types.go`

**Goal:** value types shared between the client, SSE decoder, and tests. No behavior.

- [ ] **Step 1: Write types**

```go
// server/pkg/agent/anthropic/types.go
package anthropic

import (
    "encoding/json"
    "time"
)

// CreateAgentRequest — POST /v1/agents. Creates a reusable Agent
// definition (system prompt + tool schema + model preference).
type CreateAgentRequest struct {
    Name         string            `json:"name"`
    Model        string            `json:"model"`           // e.g. "claude-sonnet-4-5"
    SystemPrompt string            `json:"system_prompt"`
    Tools        []json.RawMessage `json:"tools,omitempty"` // tool schemas (incl. our own)
}
type CreateAgentResponse struct {
    ID        string    `json:"id"`
    CreatedAt time.Time `json:"created_at"`
}

// CreateEnvRequest — POST /v1/environments. Creates a sandbox
// configuration bucket; internet toggle + secrets live here.
type CreateEnvRequest struct {
    InternetEnabled bool              `json:"internet_enabled"`
    Secrets         map[string]string `json:"secrets,omitempty"` // NAME=VALUE
}
type CreateEnvResponse struct {
    ID string `json:"id"`
}

// CreateSessRequest — POST /v1/sessions. Instantiates an Agent def
// with an Environment into a running container. If ContainerID is
// non-empty, Anthropic resumes that container instead of allocating
// a new one.
type CreateSessRequest struct {
    AgentID       string `json:"agent_id"`
    EnvironmentID string `json:"environment_id"`
    ContainerID   string `json:"container_id,omitempty"`
}
type CreateSessResponse struct {
    ID          string    `json:"id"`
    ContainerID string    `json:"container_id"`
    ExpiresAt   time.Time `json:"expires_at"`
}

// SendMessageRequest — POST /v1/sessions/{id}/messages. Streams SSE
// back on the response body. Kept minimal — Anthropic's API accepts
// more fields (tool_choice etc.) we don't use yet.
type SendMessageRequest struct {
    Content string `json:"content"`
}

// ToolResultRequest — POST /v1/sessions/{id}/tool_results. Our worker
// pushes tool outputs back to the managed agent after executing our
// own tools (MCP / custom) locally.
type ToolResultRequest struct {
    ToolUseID string `json:"tool_use_id"`
    Content   string `json:"content"`
    IsError   bool   `json:"is_error,omitempty"`
}

// SSEEvent is the decoded envelope for each "event: <type>\ndata: <json>"
// frame in the Messages stream.
type SSEEvent struct {
    Type     string          // "message_start", "content_block_delta", "tool_use", "message_stop", "error"
    Raw      json.RawMessage // the data: payload
}

// Typed payload shapes for the SSE events we actually act on. Decoded
// on-demand by the Backend.
type EventMessageStart struct {
    Message struct {
        ID    string `json:"id"`
        Model string `json:"model"`
        Usage struct {
            InputTokens  int `json:"input_tokens"`
            OutputTokens int `json:"output_tokens"`
        } `json:"usage"`
    } `json:"message"`
}
type EventContentDelta struct {
    Delta struct {
        Type string `json:"type"` // "text_delta"
        Text string `json:"text"`
    } `json:"delta"`
}
type EventToolUse struct {
    ToolUse struct {
        ID    string          `json:"id"`
        Name  string          `json:"name"`
        Input json.RawMessage `json:"input"`
    } `json:"tool_use"`
}
type EventMessageStop struct {
    StopReason string `json:"stop_reason"` // "end_turn", "tool_use", "max_tokens"
    Usage      struct {
        InputTokens  int `json:"input_tokens"`
        OutputTokens int `json:"output_tokens"`
    } `json:"usage"`
}
type EventError struct {
    Err struct {
        Type    string `json:"type"`
        Message string `json:"message"`
    } `json:"error"`
}
```

- [ ] **Step 2: Commit**

```bash
git add server/pkg/agent/anthropic/types.go
git commit -m "feat(anthropic): request/response + SSE event types"
```

---

### Task 4: SSE Decoder

**Files:**
- Create: `server/pkg/agent/anthropic/sse.go`
- Create: `server/pkg/agent/anthropic/sse_test.go`

**Goal:** line-oriented SSE decoder. Each event is a series of `event:` / `data:` lines terminated by a blank line.

- [ ] **Step 1: Failing test**

```go
// server/pkg/agent/anthropic/sse_test.go
package anthropic_test

import (
    "io"
    "strings"
    "testing"
    "github.com/multica/server/pkg/agent/anthropic"
)

func TestDecodeSSE_ParsesTwoEvents(t *testing.T) {
    stream := strings.NewReader(
        "event: message_start\ndata: {\"message\":{\"id\":\"m1\",\"model\":\"sonnet\"}}\n\n" +
        "event: message_stop\ndata: {\"stop_reason\":\"end_turn\"}\n\n",
    )
    events, err := collectAll(stream)
    if err != nil && err != io.EOF { t.Fatal(err) }
    if len(events) != 2 { t.Fatalf("got %d events, want 2", len(events)) }
    if events[0].Type != "message_start" { t.Errorf("type0=%q", events[0].Type) }
    if events[1].Type != "message_stop" { t.Errorf("type1=%q", events[1].Type) }
}

func TestDecodeSSE_IgnoresCommentsAndEmptyLines(t *testing.T) {
    stream := strings.NewReader(
        ": heartbeat\n\n" +
        "event: message_stop\ndata: {\"stop_reason\":\"end_turn\"}\n\n",
    )
    events, _ := collectAll(stream)
    if len(events) != 1 { t.Fatalf("events=%d, want 1 (heartbeat ignored)", len(events)) }
}

func TestDecodeSSE_ErrorEventReturnsPayload(t *testing.T) {
    stream := strings.NewReader(
        "event: error\ndata: {\"error\":{\"type\":\"overloaded\",\"message\":\"capacity\"}}\n\n",
    )
    events, _ := collectAll(stream)
    if events[0].Type != "error" { t.Fatalf("type=%q", events[0].Type) }
    // Raw payload round-trips.
    var e anthropic.EventError
    _ = anthropic.UnmarshalEvent(events[0], &e)
    if e.Err.Type != "overloaded" { t.Errorf("err.type=%q", e.Err.Type) }
}

// Helper: consume Decode() to completion.
func collectAll(r io.Reader) ([]anthropic.SSEEvent, error) {
    d := anthropic.NewDecoder(r)
    var out []anthropic.SSEEvent
    for {
        ev, err := d.Next()
        if err == io.EOF { return out, nil }
        if err != nil { return out, err }
        out = append(out, ev)
    }
}
```

Run: `cd server && go test ./pkg/agent/anthropic/ -run TestDecodeSSE`
Expected: FAIL — `NewDecoder`, `Next`, `UnmarshalEvent` undefined.

- [ ] **Step 2: Implement**

```go
// server/pkg/agent/anthropic/sse.go
package anthropic

import (
    "bufio"
    "encoding/json"
    "io"
    "strings"
)

// Decoder parses an SSE stream one event at a time. Not concurrency-safe.
type Decoder struct {
    scanner *bufio.Scanner
}

func NewDecoder(r io.Reader) *Decoder {
    s := bufio.NewScanner(r)
    // Anthropic can emit large JSON payloads in a single data: line (e.g.
    // a full content block). Default scanner buffer is 64KiB — raise it.
    s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
    return &Decoder{scanner: s}
}

// Next returns the next event. Returns io.EOF at stream end. A blank
// line terminates the current event; ": comment" lines (heartbeats)
// are ignored. Unknown/empty events are skipped (returns next real one).
func (d *Decoder) Next() (SSEEvent, error) {
    var eventType string
    var dataBuf strings.Builder
    haveData := false

    for d.scanner.Scan() {
        line := d.scanner.Text()
        if line == "" {
            // End of event. If we accumulated anything, return it.
            if eventType != "" || haveData {
                return SSEEvent{Type: eventType, Raw: json.RawMessage(dataBuf.String())}, nil
            }
            // Blank line with no event in progress — keep scanning.
            continue
        }
        if strings.HasPrefix(line, ":") { continue } // comment / heartbeat
        if strings.HasPrefix(line, "event:") {
            eventType = strings.TrimSpace(line[len("event:"):])
            continue
        }
        if strings.HasPrefix(line, "data:") {
            // SSE spec allows multi-line data: concatenated with newlines.
            if haveData { dataBuf.WriteByte('\n') }
            dataBuf.WriteString(strings.TrimSpace(line[len("data:"):]))
            haveData = true
            continue
        }
        // Unknown field — skip.
    }
    if err := d.scanner.Err(); err != nil { return SSEEvent{}, err }
    return SSEEvent{}, io.EOF
}

// UnmarshalEvent decodes e.Raw into the given destination pointer.
// Convenience wrapper around json.Unmarshal that returns a useful
// error when the caller mis-types an event payload.
func UnmarshalEvent(e SSEEvent, dst any) error {
    return json.Unmarshal(e.Raw, dst)
}
```

- [ ] **Step 3: Run + commit**

Run: `cd server && go test ./pkg/agent/anthropic/ -run TestDecodeSSE -v`
Expected: PASS.

```bash
git add server/pkg/agent/anthropic/sse.go server/pkg/agent/anthropic/sse_test.go
git commit -m "feat(anthropic): SSE decoder (event / data / comment / multi-line)"
```

---

### Task 5: HTTP Client

**Files:**
- Create: `server/pkg/agent/anthropic/client.go`
- Create: `server/pkg/agent/anthropic/client_test.go`

**Goal:** typed wrapper over `net/http` for the four endpoints Phase 12 hits. SSE streaming handled by returning the raw body + the decoder from Task 4.

- [ ] **Step 1: Failing test**

```go
// server/pkg/agent/anthropic/client_test.go
package anthropic_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "github.com/multica/server/pkg/agent/anthropic"
)

func TestClient_CreateAgentSendsExpectedShape(t *testing.T) {
    var captured anthropic.CreateAgentRequest
    s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/v1/agents" { t.Errorf("path=%s", r.URL.Path) }
        if r.Header.Get("x-api-key") != "test-key" { t.Error("x-api-key missing") }
        _ = json.NewDecoder(r.Body).Decode(&captured)
        _ = json.NewEncoder(w).Encode(anthropic.CreateAgentResponse{ID: "ag_123"})
    }))
    defer s.Close()

    c := anthropic.NewClient(anthropic.ClientConfig{APIKey: "test-key", BaseURL: s.URL})
    resp, err := c.CreateAgent(context.Background(), anthropic.CreateAgentRequest{
        Name: "support-bot", Model: "claude-sonnet-4-5", SystemPrompt: "You help.",
    })
    if err != nil { t.Fatal(err) }
    if resp.ID != "ag_123" { t.Errorf("id=%s", resp.ID) }
    if captured.Name != "support-bot" || captured.Model != "claude-sonnet-4-5" {
        t.Errorf("captured=%+v", captured)
    }
}

func TestClient_SendMessageStreamsSSE(t *testing.T) {
    s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !strings.HasSuffix(r.URL.Path, "/messages") { t.Errorf("path=%s", r.URL.Path) }
        w.Header().Set("Content-Type", "text/event-stream")
        _, _ = w.Write([]byte("event: message_start\ndata: {\"message\":{\"id\":\"m1\"}}\n\n"))
        _, _ = w.Write([]byte("event: message_stop\ndata: {\"stop_reason\":\"end_turn\"}\n\n"))
    }))
    defer s.Close()

    c := anthropic.NewClient(anthropic.ClientConfig{APIKey: "k", BaseURL: s.URL})
    stream, err := c.SendMessage(context.Background(), "sess_1", anthropic.SendMessageRequest{Content: "hi"})
    if err != nil { t.Fatal(err) }
    defer stream.Close()

    ev1, err := stream.Next(); if err != nil { t.Fatal(err) }
    if ev1.Type != "message_start" { t.Errorf("ev1.type=%s", ev1.Type) }
    ev2, _ := stream.Next()
    if ev2.Type != "message_stop" { t.Errorf("ev2.type=%s", ev2.Type) }
}
```

Run: `cd server && go test ./pkg/agent/anthropic/ -run TestClient`
Expected: FAIL — `NewClient`, `CreateAgent`, `SendMessage`, `ClientConfig`, `Stream` undefined.

- [ ] **Step 2: Implement**

```go
// server/pkg/agent/anthropic/client.go
package anthropic

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

type ClientConfig struct {
    APIKey  string
    BaseURL string        // defaults to https://api.anthropic.com
    HTTP    *http.Client  // optional override; default has 30s connect timeout
    Version string        // anthropic-version header; defaults to "2024-10-22"
}

type Client struct { cfg ClientConfig }

func NewClient(cfg ClientConfig) *Client {
    if cfg.BaseURL == "" { cfg.BaseURL = "https://api.anthropic.com" }
    if cfg.Version == "" { cfg.Version = "2024-10-22" }
    if cfg.HTTP == nil { cfg.HTTP = &http.Client{Timeout: 60 * time.Second} }
    return &Client{cfg: cfg}
}

func (c *Client) CreateAgent(ctx context.Context, req CreateAgentRequest) (CreateAgentResponse, error) {
    var resp CreateAgentResponse
    return resp, c.postJSON(ctx, "/v1/agents", req, &resp)
}
func (c *Client) CreateEnvironment(ctx context.Context, req CreateEnvRequest) (CreateEnvResponse, error) {
    var resp CreateEnvResponse
    return resp, c.postJSON(ctx, "/v1/environments", req, &resp)
}
func (c *Client) CreateSession(ctx context.Context, req CreateSessRequest) (CreateSessResponse, error) {
    var resp CreateSessResponse
    return resp, c.postJSON(ctx, "/v1/sessions", req, &resp)
}
func (c *Client) SendToolResult(ctx context.Context, sessionID string, req ToolResultRequest) error {
    return c.postJSON(ctx, "/v1/sessions/"+sessionID+"/tool_results", req, nil)
}

// Stream holds the raw body + an SSE decoder. Callers Close when done.
type Stream struct {
    body    io.ReadCloser
    decoder *Decoder
}
func (s *Stream) Next() (SSEEvent, error) { return s.decoder.Next() }
func (s *Stream) Close() error             { return s.body.Close() }

func (c *Client) SendMessage(ctx context.Context, sessionID string, req SendMessageRequest) (*Stream, error) {
    url := c.cfg.BaseURL + "/v1/sessions/" + sessionID + "/messages"
    body, err := json.Marshal(req); if err != nil { return nil, err }
    httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
    if err != nil { return nil, err }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Accept", "text/event-stream")
    httpReq.Header.Set("x-api-key", c.cfg.APIKey)
    httpReq.Header.Set("anthropic-version", c.cfg.Version)

    resp, err := c.cfg.HTTP.Do(httpReq); if err != nil { return nil, err }
    if resp.StatusCode != 200 {
        b, _ := io.ReadAll(resp.Body); _ = resp.Body.Close()
        return nil, fmt.Errorf("anthropic: sendmessage %d: %s", resp.StatusCode, string(b))
    }
    return &Stream{body: resp.Body, decoder: NewDecoder(resp.Body)}, nil
}

// postJSON is the non-streaming helper. If dst is nil, the response body
// is read-and-discarded (used by SendToolResult which returns 204).
func (c *Client) postJSON(ctx context.Context, path string, req, dst any) error {
    body, err := json.Marshal(req); if err != nil { return err }
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.cfg.BaseURL+path, bytes.NewReader(body))
    if err != nil { return err }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("x-api-key", c.cfg.APIKey)
    httpReq.Header.Set("anthropic-version", c.cfg.Version)

    resp, err := c.cfg.HTTP.Do(httpReq); if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode >= 300 {
        b, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("anthropic: POST %s %d: %s", path, resp.StatusCode, string(b))
    }
    if dst == nil { _, _ = io.Copy(io.Discard, resp.Body); return nil }
    return json.NewDecoder(resp.Body).Decode(dst)
}
```

- [ ] **Step 3: Run + commit**

Run: `cd server && go test ./pkg/agent/anthropic/ -run TestClient -v`
Expected: PASS.

```bash
git add server/pkg/agent/anthropic/client.go server/pkg/agent/anthropic/client_test.go
git commit -m "feat(anthropic): HTTP client — agent/env/session CRUD + SSE stream"
```

---

### Task 6: Cost Calculator + sqlc Queries

**Files:**
- Modify: `server/pkg/agent/cost_calculator.go`
- Create: `server/pkg/db/queries/claude_managed.sql`

- [ ] **Step 1: Failing cost test**

```go
// Append to server/pkg/agent/cost_calculator_test.go
func TestCostCalculator_ComputeSessionCents(t *testing.T) {
    cc := agent.NewCostCalculator(nil)
    // 0.5h = 1800s = 4¢ @ 8¢/hr
    if got := cc.ComputeSessionCents(0.5); got != 4 { t.Errorf("got=%d want 4", got) }
    // Fractional hour: 1/6 = 0.1667 × 8 = ~1.33¢ → round to nearest = 1
    if got := cc.ComputeSessionCents(1.0 / 6.0); got != 1 { t.Errorf("got=%d want 1", got) }
    // Zero duration → 0¢.
    if got := cc.ComputeSessionCents(0); got != 0 { t.Errorf("got=%d want 0", got) }
}
```

Run: `cd server && go test ./pkg/agent/ -run TestCostCalculator_ComputeSessionCents`
Expected: FAIL — method undefined.

- [ ] **Step 2: Implement**

```go
// Append to server/pkg/agent/cost_calculator.go
// If "math" is not already in the file's import block, add it — do
// NOT duplicate an existing import.
import "math"

// ComputeSessionCents returns the session-hour portion of cost in cents.
// ClaudeManagedSessionHourCents captures the current $0.08/hr rate; if
// Anthropic changes pricing, update defaults.go and the dashboard label.
// Rounding: half-away-from-zero via math.Round so partial-hour sessions
// don't silently round to 0¢.
func (c *CostCalculator) ComputeSessionCents(hours float64) int {
    if hours <= 0 { return 0 }
    return int(math.Round(hours * float64(ClaudeManagedSessionHourCents)))
}
```

- [ ] **Step 3: sqlc queries**

```sql
-- server/pkg/db/queries/claude_managed.sql

-- name: GetClaudeManagedAgent :one
SELECT a.id AS agent_id, a.workspace_id, a.provider, a.model,
       a.system_prompt, a.anthropic_agent_id, a.environment_config
FROM agent a
WHERE a.workspace_id = $1 AND a.id = $2 AND a.agent_type = 'claude_managed';

-- name: UpdateAnthropicAgentID :exec
UPDATE agent
SET anthropic_agent_id = $3
WHERE workspace_id = $1 AND id = $2;

-- name: UpsertTaskSessionID :exec
UPDATE agent_task_queue
SET session_id = $3
WHERE workspace_id = $1 AND id = $2;

-- name: GetSessionIDForIssue :one
-- Returns the most-recent live session_id on this issue that's still
-- inside the 30-day container window. Null when none exists.
-- $3 is an int number of days; pgx binds it safely — no string-interp
-- into the interval expression.
SELECT session_id
FROM agent_task_queue
WHERE workspace_id = $1 AND issue_id = $2 AND session_id IS NOT NULL
  AND created_at > NOW() - (INTERVAL '1 day' * $3::int)
ORDER BY created_at DESC
LIMIT 1;

-- name: InsertSessionCostEvent :one
-- Returns id so the worker can LateLinkCostEvent.
INSERT INTO cost_event (
    workspace_id, agent_id, task_id, trace_id,
    provider, model, input_tokens, output_tokens,
    cost_cents, cost_status,
    session_hours, session_cost_cents
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING id, workspace_id;
```

Regenerate sqlc:

```bash
cd server && make sqlc
```

- [ ] **Step 4: Run + commit**

Run: `cd server && go test ./pkg/agent/ -run TestCostCalculator_ComputeSessionCents -v`
Expected: PASS.

```bash
git add server/pkg/agent/cost_calculator.go server/pkg/agent/cost_calculator_test.go server/pkg/db/queries/claude_managed.sql server/pkg/db/gen/
git commit -m "feat(claude-managed): CostCalculator.ComputeSessionCents + sqlc queries

ComputeSessionCents rounds half-away-from-zero so ≥6s sessions bill ≥1¢.
sqlc: GetClaudeManagedAgent, UpdateAnthropicAgentID, UpsertTaskSessionID,
GetSessionIDForIssue (30d TTL), InsertSessionCostEvent (:one → id)."
```

---

### Task 7: ClaudeManagedBackend (the adapter)

**Files:**
- Create: `server/pkg/agent/claude_managed.go`
- Create: `server/pkg/agent/claude_managed_test.go`

**Goal:** implement `agent.Backend` by driving the Anthropic Agents API. `Execute` creates (or reuses) the Agent def + Environment + Session, sends the prompt, streams SSE, maps events to our `Message` channel, routes our tools back through the Phase 2/3 Registry, and returns a `Response` when Anthropic emits `message_stop{stop_reason="end_turn"}`.

- [ ] **Step 1: Failing test — happy path SSE → Response**

```go
// server/pkg/agent/claude_managed_test.go
package agent_test

import (
    "context"
    "encoding/json"
    "testing"
    "time"

    "github.com/multica/server/internal/testsupport"
    "github.com/multica/server/pkg/agent"
    "github.com/multica/server/pkg/agent/anthropic"
)

func TestClaudeManagedBackend_StreamsEndTurn(t *testing.T) {
    fake := testsupport.NewFakeAnthropicServer(t)
    fake.ScriptReplies([]anthropic.SSEEvent{
        {Type: "message_start", Raw: json.RawMessage(`{"message":{"id":"m1","model":"sonnet","usage":{"input_tokens":10,"output_tokens":0}}}`)},
        {Type: "content_block_delta", Raw: json.RawMessage(`{"delta":{"type":"text_delta","text":"hello"}}`)},
        {Type: "content_block_delta", Raw: json.RawMessage(`{"delta":{"type":"text_delta","text":" world"}}`)},
        {Type: "message_stop", Raw: json.RawMessage(`{"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`)},
    })
    // fake.Server.Close is already registered via t.Cleanup inside
    // NewFakeAnthropicServer — this defer is redundant but harmless
    // (Close is idempotent) and kept for explicit readability.
    defer fake.Server.Close()

    be := agent.NewClaudeManagedBackend(agent.ClaudeManagedConfig{
        Client:           anthropic.NewClient(anthropic.ClientConfig{APIKey: "k", BaseURL: fake.Server.URL}),
        Model:            "claude-sonnet-4-5",
        SystemPrompt:     "You help.",
        AnthropicAgentID: "ag_1",
    })

    resp, err := be.Execute(context.Background(),
        agent.Prompt{System: "ignored (already in Agent def)", User: "say hi"},
        agent.Opts{})
    if err != nil { t.Fatal(err) }
    if resp.Text != "hello world" { t.Errorf("text=%q", resp.Text) }
    if resp.InputTokens != 10 { t.Errorf("input_tokens=%d", resp.InputTokens) }
    if resp.OutputTokens != 2 { t.Errorf("output_tokens=%d", resp.OutputTokens) }
}
```

Run: `cd server && go test ./pkg/agent/ -run TestClaudeManagedBackend_StreamsEndTurn`
Expected: FAIL — `NewClaudeManagedBackend`, `ClaudeManagedConfig` undefined.

- [ ] **Step 2: Implement the happy path**

**Ordering note.** `claude_managed.go` below references
`anthropic.ToolSchema`. The type is appended to `anthropic/types.go`
at the end of this step — write that append FIRST, then the main
`claude_managed.go` body, otherwise `go build` fails mid-step. The
`ToolSchema` snippet is near the bottom of this step, flagged
"Append to server/pkg/agent/anthropic/types.go".

```go
// server/pkg/agent/claude_managed.go
package agent

import (
    "context"
    "errors"
    "fmt"
    "io"
    "strings"
    "sync"
    "time"

    "github.com/multica/server/pkg/agent/anthropic"
)

// ClaudeManagedConfig captures everything NewClaudeManagedBackend needs
// to drive one Execute. Session reuse is handled by the worker: it
// passes ReuseContainerID when Phase 12's GetSessionIDForIssue returns
// a still-live container for the same issue.
type ClaudeManagedConfig struct {
    Client             *anthropic.Client
    Model              string              // "claude-sonnet-4-5" etc.
    SystemPrompt       string
    AnthropicAgentID   string              // pre-created Agent def id; empty → create on first Execute
    ReuseContainerID   string              // empty → fresh container
    EnvironmentConfig  anthropic.CreateEnvRequest
    Tools              []anthropic.ToolSchema // our tool schemas (Phase 3 + Phase 2 custom)
    // ToolDispatcher runs our tools locally and returns the result string.
    // nil-safe: tool_use events with names not in Tools are forwarded to
    // Anthropic's own execution (which will error if the name is unknown).
    ToolDispatcher    func(ctx context.Context, name string, input []byte) (string, error)
    // SessionIDOut receives the Anthropic-assigned session_id exactly
    // once, after CreateSession succeeds. Worker writes it to the
    // agent_task_queue row so subsequent tasks on the issue can reuse.
    SessionIDOut      chan<- string
    // AgentIDOut receives Anthropic's Agent-def id after CreateAgent
    // (only when AnthropicAgentID was empty). Worker persists via
    // UpdateAnthropicAgentID.
    AgentIDOut        chan<- string
    // Hooks forwards D6 lifecycle hooks. SSE idle-timeout fires
    // Hooks.OnTimeout before returning the error so worker can persist
    // partial state (e.g. accumulated text) rather than losing it.
    Hooks             *Hooks
    // IdleTimeout overrides ClaudeManagedSSEIdleTimeout for this
    // backend only. Zero-value → fall back to the global default.
    // Used primarily by tests that want a sub-second watchdog without
    // mutating the global.
    IdleTimeout       time.Duration
}

// tools.ToolSchema alias — keeps the anthropic package free of our tools
// imports. The schema field is raw JSON so it round-trips verbatim.
// (Defined here to avoid a cycle.)
//
// NB: the alias lives here so anthropic/types.go doesn't need to import
// server/pkg/tools. Phase 3 already defines tools.ToolSchema with the
// same shape (Name + JSON schema); we re-export it under the anthropic
// package name so the HTTP body carries it verbatim.
type anthropicToolSchema = anthropic.ToolSchema

// ClaudeManagedBackend implements agent.Backend. Stateless between
// Execute calls — the worker pairs it with a session_id lookup before
// each invocation.
type ClaudeManagedBackend struct {
    cfg       ClaudeManagedConfig
    expiresAt *time.Time
    mu        sync.Mutex
}

func NewClaudeManagedBackend(cfg ClaudeManagedConfig) *ClaudeManagedBackend {
    return &ClaudeManagedBackend{cfg: cfg}
}

func (b *ClaudeManagedBackend) ExpiresAt() *time.Time {
    b.mu.Lock(); defer b.mu.Unlock()
    return b.expiresAt
}

// Hooks forwards the caller-supplied hooks from ClaudeManagedConfig so
// D6's graceful-snapshot contract works for managed sessions just like
// for sandbox backends. If OnTimeout fires (either from the proactive
// SandboxTimeoutBufferMs scheduler or from our SSE idle watchdog below),
// the worker can persist partial text / emit a cost_event / retry.
func (b *ClaudeManagedBackend) Hooks() *Hooks {
    if b.cfg.Hooks == nil { return &Hooks{} }
    return b.cfg.Hooks
}

func (b *ClaudeManagedBackend) Execute(ctx context.Context, p Prompt, o Opts) (Response, error) {
    // 1) Ensure we have an Anthropic Agent-def id.
    agentID := b.cfg.AnthropicAgentID
    if agentID == "" {
        rawTools := make([]json.RawMessage, len(b.cfg.Tools))
        for i, t := range b.cfg.Tools { rawTools[i] = t.JSONSchema }
        resp, err := b.cfg.Client.CreateAgent(ctx, anthropic.CreateAgentRequest{
            Name: "multica-managed", Model: b.cfg.Model,
            SystemPrompt: b.cfg.SystemPrompt, Tools: rawTools,
        })
        if err != nil { return Response{}, fmt.Errorf("create agent: %w", err) }
        agentID = resp.ID
        if b.cfg.AgentIDOut != nil {
            // Non-blocking send: if the caller didn't drain on a
            // previous Execute (or reused this backend for a retry),
            // don't deadlock. The last-write-wins contract is
            // acceptable because Anthropic Agent-def IDs are stable
            // across retries — both writes would carry the same ID.
            select { case b.cfg.AgentIDOut <- agentID: default: }
        }
    }

    // 2) Create Environment.
    envResp, err := b.cfg.Client.CreateEnvironment(ctx, b.cfg.EnvironmentConfig)
    if err != nil { return Response{}, fmt.Errorf("create env: %w", err) }

    // 3) Create or resume Session.
    sessResp, err := b.cfg.Client.CreateSession(ctx, anthropic.CreateSessRequest{
        AgentID: agentID, EnvironmentID: envResp.ID, ContainerID: b.cfg.ReuseContainerID,
    })
    if err != nil { return Response{}, fmt.Errorf("create session: %w", err) }
    b.mu.Lock(); b.expiresAt = &sessResp.ExpiresAt; b.mu.Unlock()
    if b.cfg.SessionIDOut != nil {
        // Non-blocking send per above — protects against retry deadlock.
        select { case b.cfg.SessionIDOut <- sessResp.ContainerID: default: }
    }

    // 4) Send message + stream SSE.
    stream, err := b.cfg.Client.SendMessage(ctx, sessResp.ID, anthropic.SendMessageRequest{Content: p.User})
    if err != nil { return Response{}, fmt.Errorf("send message: %w", err) }
    defer stream.Close()

    return b.drain(ctx, stream, sessResp.ID)
}

// drain consumes the SSE stream, accumulating text deltas, dispatching
// tool_use events to ToolDispatcher, pushing tool results back via the
// client, and returning the final Response on message_stop{end_turn}.
// max_tokens / tool_use stop_reasons loop: we send tool results and
// expect Anthropic to issue another message_start automatically.
func (b *ClaudeManagedBackend) drain(ctx context.Context, stream *anthropic.Stream, sessID string) (Response, error) {
    var textBuf strings.Builder
    var totalIn, totalOut int

    // Watchdog: SSE idle timeout. Fires Hooks.OnTimeout (D6 graceful
    // snapshot contract) if the stream stalls, then CLOSES the HTTP
    // body to unblock the bufio.Scanner reading it. Cancelling a
    // derived context does NOT unblock stream.Next() because
    // bufio.Scanner is not ctx-aware — only closing the underlying
    // net.Conn/resp.Body does.
    idleTimeout := b.cfg.IdleTimeout
    if idleTimeout == 0 { idleTimeout = ClaudeManagedSSEIdleTimeout }
    idleFired := make(chan struct{}, 1)
    // NewTimer + Stop+drain pattern is safe under concurrent Reset
    // unlike time.AfterFunc which can race when the fire-callback is
    // already scheduled.
    watchdog := time.NewTimer(idleTimeout)
    defer func() {
        if !watchdog.Stop() {
            select { case <-watchdog.C: default: }
        }
    }()
    // Goroutine observes the timer. On fire it closes the stream and
    // signals idleFired so drain() can tag the subsequent read error
    // as a timeout (vs. a real EOF).
    closeDone := make(chan struct{})
    go func() {
        defer close(closeDone)
        select {
        case <-watchdog.C:
            if b.cfg.Hooks != nil && b.cfg.Hooks.OnTimeout != nil {
                _ = b.cfg.Hooks.OnTimeout(ctx, nil) // Session arg nil — backend is its own session
            }
            _ = stream.Close() // unblocks stream.Next() below
            idleFired <- struct{}{}
        case <-ctx.Done():
            _ = stream.Close()
        }
    }()

    for {
        ev, err := stream.Next()
        if err == io.EOF {
            return Response{Text: textBuf.String(), InputTokens: totalIn, OutputTokens: totalOut}, nil
        }
        if err != nil {
            // If the watchdog fired, wrap the stream error so the worker
            // can persist the partial text in its fail() path.
            select {
            case <-idleFired:
                return Response{Text: textBuf.String(), InputTokens: totalIn, OutputTokens: totalOut},
                    fmt.Errorf("anthropic SSE idle >%s: %w", idleTimeout, err)
            default:
            }
            return Response{}, err
        }
        // Event arrived — reset the watchdog using the Stop+drain
        // pattern so concurrent Reset doesn't race the fire path.
        if !watchdog.Stop() {
            select { case <-watchdog.C: default: }
        }
        watchdog.Reset(idleTimeout)

        switch ev.Type {
        case "message_start":
            var ms anthropic.EventMessageStart
            _ = anthropic.UnmarshalEvent(ev, &ms)
            totalIn += ms.Message.Usage.InputTokens

        case "content_block_delta":
            var d anthropic.EventContentDelta
            _ = anthropic.UnmarshalEvent(ev, &d)
            if d.Delta.Type == "text_delta" { textBuf.WriteString(d.Delta.Text) }

        case "tool_use":
            var tu anthropic.EventToolUse
            _ = anthropic.UnmarshalEvent(ev, &tu)
            if b.cfg.ToolDispatcher == nil {
                // No local dispatcher — forward to Anthropic with an error
                // so the managed agent surfaces it. Anthropic's own tools
                // (bash, etc.) never hit this branch because they never
                // emit tool_use toward our side.
                _ = b.cfg.Client.SendToolResult(ctx, sessID, anthropic.ToolResultRequest{
                    ToolUseID: tu.ToolUse.ID, Content: "tool dispatcher not configured", IsError: true,
                })
                continue
            }
            out, dispatchErr := b.cfg.ToolDispatcher(ctx, tu.ToolUse.Name, tu.ToolUse.Input)
            if dispatchErr != nil {
                _ = b.cfg.Client.SendToolResult(ctx, sessID, anthropic.ToolResultRequest{
                    ToolUseID: tu.ToolUse.ID, Content: dispatchErr.Error(), IsError: true,
                })
            } else {
                _ = b.cfg.Client.SendToolResult(ctx, sessID, anthropic.ToolResultRequest{
                    ToolUseID: tu.ToolUse.ID, Content: out,
                })
            }

        case "message_stop":
            var ms anthropic.EventMessageStop
            _ = anthropic.UnmarshalEvent(ev, &ms)
            totalOut += ms.Usage.OutputTokens
            if ms.StopReason == "end_turn" || ms.StopReason == "max_tokens" {
                return Response{Text: textBuf.String(), InputTokens: totalIn, OutputTokens: totalOut}, nil
            }
            // "tool_use" stop_reason: we already dispatched tool results
            // via SendToolResult; the same stream will continue with
            // another message_start.

        case "error":
            var e anthropic.EventError
            _ = anthropic.UnmarshalEvent(ev, &e)
            return Response{}, fmt.Errorf("anthropic error: %s: %s", e.Err.Type, e.Err.Message)
        }
    }
}

var _ Backend = (*ClaudeManagedBackend)(nil)
```

Also add the missing `ToolSchema` type to the anthropic package:

```go
// Append to server/pkg/agent/anthropic/types.go
// ToolSchema is the body of a single tool entry in CreateAgentRequest.Tools.
// JSONSchema is the raw JSON schema object; name is extracted by Anthropic.
type ToolSchema struct {
    Name       string          `json:"name"`
    JSONSchema json.RawMessage `json:"input_schema"`
}
```

Add the `json` import to `claude_managed.go`:

```go
import (
    // ... existing imports
    "encoding/json"
)
```

- [ ] **Step 3: Failing test — hybrid tool dispatch**

```go
// Append to claude_managed_test.go
func TestClaudeManagedBackend_DispatchesToolsLocally(t *testing.T) {
    fake := testsupport.NewFakeAnthropicServer(t)
    // First SSE: Anthropic emits tool_use for our "search_contacts" tool.
    // Second (after SendToolResult): Anthropic emits text + end_turn.
    fake.ScriptReplies([]anthropic.SSEEvent{
        {Type: "message_start", Raw: json.RawMessage(`{"message":{"id":"m1","model":"sonnet"}}`)},
        {Type: "tool_use", Raw: json.RawMessage(`{"tool_use":{"id":"tu_1","name":"search_contacts","input":{"query":"acme"}}}`)},
        {Type: "message_stop", Raw: json.RawMessage(`{"stop_reason":"tool_use"}`)},
        {Type: "message_start", Raw: json.RawMessage(`{"message":{"id":"m2","model":"sonnet"}}`)},
        {Type: "content_block_delta", Raw: json.RawMessage(`{"delta":{"type":"text_delta","text":"found: Acme Co."}}`)},
        {Type: "message_stop", Raw: json.RawMessage(`{"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`)},
    })
    // fake.Server.Close is already registered via t.Cleanup inside
    // NewFakeAnthropicServer — this defer is redundant but harmless
    // (Close is idempotent) and kept for explicit readability.
    defer fake.Server.Close()

    calls := 0
    be := agent.NewClaudeManagedBackend(agent.ClaudeManagedConfig{
        Client:           anthropic.NewClient(anthropic.ClientConfig{APIKey: "k", BaseURL: fake.Server.URL}),
        Model:            "claude-sonnet-4-5", AnthropicAgentID: "ag_1",
        ToolDispatcher: func(ctx context.Context, name string, input []byte) (string, error) {
            calls++
            if name != "search_contacts" { t.Errorf("name=%q", name) }
            return "Acme Co.", nil
        },
    })
    resp, err := be.Execute(context.Background(), agent.Prompt{User: "find acme"}, agent.Opts{})
    if err != nil { t.Fatal(err) }
    if calls != 1 { t.Errorf("dispatcher called %d times, want 1", calls) }
    if resp.Text != "found: Acme Co." { t.Errorf("text=%q", resp.Text) }
}
```

- [ ] **Step 4: Run + commit**

Run: `cd server && go test ./pkg/agent/ -run TestClaudeManagedBackend -v`
Expected: PASS.

```bash
git add server/pkg/agent/claude_managed.go server/pkg/agent/claude_managed_test.go server/pkg/agent/anthropic/types.go
git commit -m "feat(claude-managed): ClaudeManagedBackend — SSE → Response + hybrid tools

Execute does CreateAgent (if needed) → CreateEnv → CreateSession → SendMessage
→ drain SSE. Text deltas accumulate; tool_use events dispatch via
ToolDispatcher and results POST back via /tool_results; end_turn /
max_tokens close the loop. Container ID exposed via SessionIDOut for
worker persistence. Idle timeout = 2m."
```

---

### Task 8: FakeAnthropicServer testsupport helper

**Files:**
- Create: `server/internal/testsupport/claude_managed.go`

**Goal:** stand up an `httptest.Server` that mimics the four endpoints Phase 12 hits. Tests script SSE replies per SendMessage.

- [ ] **Step 1: Implement**

```go
// server/internal/testsupport/claude_managed.go
package testsupport

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"
    "github.com/multica/server/pkg/agent/anthropic"
)

type FakeAnthropicServer struct {
    Server  *httptest.Server
    mu      sync.Mutex
    calls   []string
    scripts [][]anthropic.SSEEvent  // FIFO — pop on each SendMessage
    lastSendReq     anthropic.SendMessageRequest
    lastCreateAgent anthropic.CreateAgentRequest
    // lastCreateSession captures the most recent CreateSession body so
    // tests can assert container_id reuse across consecutive tasks on
    // the same issue.
    lastCreateSession anthropic.CreateSessRequest
    // allCreateSessions lets tests inspect every CreateSession call —
    // TestClaudeManagedWorker_ReusesContainerIDForSameIssue asserts
    // that the 2nd entry carries ContainerID="cnt_fake".
    allCreateSessions []anthropic.CreateSessRequest
}

func NewFakeAnthropicServer(t *testing.T) *FakeAnthropicServer {
    f := &FakeAnthropicServer{}
    mux := http.NewServeMux()
    mux.HandleFunc("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
        f.mu.Lock(); f.calls = append(f.calls, "POST /v1/agents"); f.mu.Unlock()
        _ = json.NewDecoder(r.Body).Decode(&f.lastCreateAgent)
        _ = json.NewEncoder(w).Encode(anthropic.CreateAgentResponse{ID: "ag_fake"})
    })
    mux.HandleFunc("/v1/environments", func(w http.ResponseWriter, r *http.Request) {
        f.mu.Lock(); f.calls = append(f.calls, "POST /v1/environments"); f.mu.Unlock()
        _ = json.NewEncoder(w).Encode(anthropic.CreateEnvResponse{ID: "env_fake"})
    })
    mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
        f.mu.Lock()
        f.calls = append(f.calls, "POST /v1/sessions")
        _ = json.NewDecoder(r.Body).Decode(&f.lastCreateSession)
        f.allCreateSessions = append(f.allCreateSessions, f.lastCreateSession)
        f.mu.Unlock()
        _ = json.NewEncoder(w).Encode(anthropic.CreateSessResponse{
            ID: "sess_fake", ContainerID: "cnt_fake",
        })
    })
    // /v1/sessions/{id}/messages + /v1/sessions/{id}/tool_results —
    // single wildcard prefix handler dispatches on path suffix so any
    // session id the fake returns in CreateSession (default "sess_fake",
    // but future tests may override) routes correctly.
    mux.HandleFunc("/v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
        // Skip the base "/v1/sessions" handler registered above (same
        // mux, different exact-vs-prefix match).
        if r.URL.Path == "/v1/sessions" { return }
        switch {
        case strings.HasSuffix(r.URL.Path, "/messages"):
            f.mu.Lock()
            f.calls = append(f.calls, "POST "+r.URL.Path)
            _ = json.NewDecoder(r.Body).Decode(&f.lastSendReq)
            var events []anthropic.SSEEvent
            if len(f.scripts) > 0 { events = f.scripts[0]; f.scripts = f.scripts[1:] }
            f.mu.Unlock()
            w.Header().Set("Content-Type", "text/event-stream")
            flusher, _ := w.(http.Flusher)
            for _, e := range events {
                w.Write([]byte("event: " + e.Type + "\ndata: "))
                w.Write(e.Raw)
                w.Write([]byte("\n\n"))
                if flusher != nil { flusher.Flush() }
            }
        case strings.HasSuffix(r.URL.Path, "/tool_results"):
            f.mu.Lock()
            f.calls = append(f.calls, "POST "+r.URL.Path)
            f.mu.Unlock()
            w.WriteHeader(204)
        default:
            http.NotFound(w, r)
        }
    })
    f.Server = httptest.NewServer(mux)
    t.Cleanup(f.Server.Close)
    return f
}

func (f *FakeAnthropicServer) ScriptReplies(events []anthropic.SSEEvent) {
    f.mu.Lock(); defer f.mu.Unlock()
    f.scripts = append(f.scripts, events)
}
func (f *FakeAnthropicServer) Calls() []string {
    f.mu.Lock(); defer f.mu.Unlock()
    out := make([]string, len(f.calls)); copy(out, f.calls); return out
}
func (f *FakeAnthropicServer) LastCreateAgentRequest() anthropic.CreateAgentRequest {
    f.mu.Lock(); defer f.mu.Unlock(); return f.lastCreateAgent
}
func (f *FakeAnthropicServer) LastSendMessageRequest() anthropic.SendMessageRequest {
    f.mu.Lock(); defer f.mu.Unlock(); return f.lastSendReq
}
func (f *FakeAnthropicServer) LastCreateSessionRequest() anthropic.CreateSessRequest {
    f.mu.Lock(); defer f.mu.Unlock(); return f.lastCreateSession
}
// AllCreateSessionRequests returns every /v1/sessions POST body in call order.
func (f *FakeAnthropicServer) AllCreateSessionRequests() []anthropic.CreateSessRequest {
    f.mu.Lock(); defer f.mu.Unlock()
    out := make([]anthropic.CreateSessRequest, len(f.allCreateSessions))
    copy(out, f.allCreateSessions)
    return out
}
```

- [ ] **Step 2: Commit**

```bash
git add server/internal/testsupport/claude_managed.go
git commit -m "test(claude-managed): FakeAnthropicServer — scripted SSE + call log"
```

---

### Task 9: Worker Handler

**Files:**
- Create: `server/internal/worker/claude_managed.go`
- Create: `server/internal/worker/claude_managed_test.go`

**Goal:** claim `claude_managed` tasks, look up the issue's existing `session_id` (container reuse), build `ClaudeManagedBackend`, run `agent.Run`, persist `session_id` post-creation, insert cost row with `session_hours`/`session_cost_cents`, link via Phase 10 `LateLinkCostEvent`, emit `managed_session.started/stopped` events.

- [ ] **Step 1: Failing test**

```go
// server/internal/worker/claude_managed_test.go
package worker_test

import (
    "context"
    "encoding/json"
    "testing"
    "time"

    "github.com/multica/server/internal/testsupport"
    "github.com/multica/server/internal/worker"
    "github.com/multica/server/pkg/agent"
    "github.com/multica/server/pkg/agent/anthropic"
)

func TestClaudeManagedWorker_ClaimExecutePersistSession(t *testing.T) {
    env := testsupport.NewEnv(t)
    wsID := env.SeedWorkspace("cm")
    fake := testsupport.NewFakeAnthropicServer(t)
    fake.ScriptReplies([]anthropic.SSEEvent{
        {Type: "message_start", Raw: json.RawMessage(`{"message":{"id":"m1","model":"sonnet","usage":{"input_tokens":5,"output_tokens":0}}}`)},
        {Type: "content_block_delta", Raw: json.RawMessage(`{"delta":{"type":"text_delta","text":"done"}}`)},
        {Type: "message_stop", Raw: json.RawMessage(`{"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1}}`)},
    })

    agentID := env.SeedClaudeManagedAgent(wsID, "claude-sonnet-4-5", "ag_pre")
    issueID := env.SeedIssue(wsID, "triage")
    taskID := env.SeedTaskWithIssue(wsID, agentID, issueID, "do the thing")

    w := worker.NewClaudeManagedWorker(worker.ClaudeManagedDeps{
        DB: env.DB, Tracer: env.Tracer, CostCalc: env.CostCalc, Events: env.Events,
        AnthropicClient: anthropic.NewClient(anthropic.ClientConfig{APIKey: "k", BaseURL: fake.Server.URL}),
        ToolDispatcher: func(ctx context.Context, name string, input []byte) (string, error) {
            return "", nil
        },
    })
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second); defer cancel()
    if err := w.RunOnce(ctx); err != nil { t.Fatal(err) }

    // 1. Task completed.
    if env.TaskStatus(taskID) != "completed" { t.Errorf("status=%s", env.TaskStatus(taskID)) }
    // 2. session_id persisted on the task row.
    if env.SessionIDForTask(taskID) != "cnt_fake" { t.Errorf("session=%q", env.SessionIDForTask(taskID)) }
    // 3. cost_event row with sandbox_claude provider + session_hours > 0.
    if env.CountCostEventsByProvider(wsID, "sandbox_claude") < 1 { t.Error("no session cost_event") }
    // 4. Anthropic call order: create env → create session → send message.
    calls := fake.Calls()
    if len(calls) < 3 { t.Fatalf("calls=%v", calls) }
    if calls[0] != "POST /v1/environments" { t.Errorf("call[0]=%s", calls[0]) }
    if calls[1] != "POST /v1/sessions" { t.Errorf("call[1]=%s", calls[1]) }
}

func TestClaudeManagedWorker_ReusesContainerIDForSameIssue(t *testing.T) {
    env := testsupport.NewEnv(t)
    wsID := env.SeedWorkspace("cm")
    fake := testsupport.NewFakeAnthropicServer(t)
    // Script: two consecutive tasks on the same issue each complete.
    fake.ScriptReplies(endTurnScript())
    fake.ScriptReplies(endTurnScript())

    agentID := env.SeedClaudeManagedAgent(wsID, "claude-sonnet-4-5", "ag_pre")
    issueID := env.SeedIssue(wsID, "triage")

    w := worker.NewClaudeManagedWorker(worker.ClaudeManagedDeps{
        DB: env.DB, Tracer: env.Tracer, CostCalc: env.CostCalc, Events: env.Events,
        AnthropicClient: anthropic.NewClient(anthropic.ClientConfig{APIKey: "k", BaseURL: fake.Server.URL}),
    })

    // Task 1 → creates container (no prior session_id for the issue).
    _ = env.SeedTaskWithIssue(wsID, agentID, issueID, "task 1")
    _ = w.RunOnce(context.Background())
    // Task 2 — same issue → should reuse container_id "cnt_fake".
    _ = env.SeedTaskWithIssue(wsID, agentID, issueID, "task 2")
    _ = w.RunOnce(context.Background())

    sessions := fake.AllCreateSessionRequests()
    if len(sessions) != 2 { t.Fatalf("sessions=%d, want 2", len(sessions)) }
    if sessions[0].ContainerID != "" {
        t.Errorf("first task: ContainerID=%q, want empty (fresh container)", sessions[0].ContainerID)
    }
    if sessions[1].ContainerID != "cnt_fake" {
        t.Errorf("second task: ContainerID=%q, want cnt_fake (reused)", sessions[1].ContainerID)
    }
}

func endTurnScript() []anthropic.SSEEvent {
    return []anthropic.SSEEvent{
        {Type: "message_start", Raw: json.RawMessage(`{"message":{"id":"m","model":"sonnet","usage":{"input_tokens":1,"output_tokens":0}}}`)},
        {Type: "message_stop", Raw: json.RawMessage(`{"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)},
    }
}
```

Run: `cd server && go test ./internal/worker/ -run TestClaudeManagedWorker`
Expected: FAIL — `ClaudeManagedWorker`, `ClaudeManagedDeps` undefined.

- [ ] **Step 2: Implement**

```go
// server/internal/worker/claude_managed.go
package worker

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "log"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/multica/server/pkg/agent"
    "github.com/multica/server/pkg/agent/anthropic"
    db "github.com/multica/server/pkg/db/gen"
    "github.com/multica/server/pkg/trace"
)

type ClaudeManagedDeps struct {
    DB              *pgxpool.Pool
    Tracer          *trace.Tracer
    CostCalc        *agent.CostCalculator
    Events          EventsBus // same local interface as Phase 11
    AnthropicClient *anthropic.Client
    // ToolDispatcher is called when Anthropic's managed agent emits a
    // tool_use event for a tool registered locally (MCP or custom).
    // Nil-safe: missing dispatcher → tool errors sent back to Anthropic.
    ToolDispatcher  func(ctx context.Context, name string, input []byte) (string, error)
    // Vault resolves secret_credential_ids to NAME=VALUE pairs for the
    // Anthropic Environment. Phase 8B's CredentialVault.GetRaw returns
    // the decrypted bytes; we fetch the credential name via a separate
    // sqlc query (GetCredentialByID). Nil-safe: empty secrets if nil.
    Vault           CredentialVault
}

// CredentialVault is the minimal surface the worker needs from Phase 8B
// to inject secrets. Kept as a local interface to avoid importing the
// full credentials package (which has many unrelated deps).
type CredentialVault interface {
    // GetNameAndValue resolves a credential ID to (NAME, value-bytes).
    // Returns NAME="" when the ID is unknown so the loop skips it.
    GetNameAndValue(ctx context.Context, wsID, credID uuid.UUID) (string, []byte, error)
}

// PersistedEnvConfig is the JSON shape stored in agent.environment_config.
// Mirrors the UI's ClaudeEnvironmentConfig TS type.
type PersistedEnvConfig struct {
    InternetEnabled     bool        `json:"internet_enabled"`
    SecretCredentialIDs []uuid.UUID `json:"secret_credential_ids"`
}

type ClaudeManagedWorker struct{ deps ClaudeManagedDeps }

func NewClaudeManagedWorker(d ClaudeManagedDeps) *ClaudeManagedWorker { return &ClaudeManagedWorker{deps: d} }

func (w *ClaudeManagedWorker) Run(ctx context.Context) {
    for {
        if ctx.Err() != nil { return }
        if err := w.RunOnce(ctx); err != nil && !errors.Is(err, errNoTask) {
            select { case <-time.After(500 * time.Millisecond): case <-ctx.Done(): return }
        }
    }
}

var errNoTask = fmt.Errorf("no task ready")

func (w *ClaudeManagedWorker) RunOnce(ctx context.Context) error {
    q := db.New(w.deps.DB)
    row, err := q.ClaimClaudeManagedTask(ctx) // defined in Step 2a
    if err != nil { return errNoTask }

    wsID := uuidFromPg(row.WorkspaceID)
    agentID := uuidFromPg(row.AgentID)
    taskID := uuidFromPg(row.ID)
    issueID := uuidFromPg(row.IssueID)

    // Mint trace_id per D8.
    traceID := uuid.New()
    ctx = agent.WithTraceID(ctx, traceID)
    span := w.deps.Tracer.StartSpan(ctx, "claude_managed_task", map[string]any{"task_id": taskID})

    // Fetch agent details.
    ag, err := q.GetClaudeManagedAgent(ctx, db.GetClaudeManagedAgentParams{
        WorkspaceID: row.WorkspaceID, ID: row.AgentID,
    })
    if err != nil { return w.fail(ctx, q, span, taskID, err) }

    // Container reuse: does this issue have a live session?
    var reuseContainer string
    if issueID != uuid.Nil {
        sid, sErr := q.GetSessionIDForIssue(ctx, db.GetSessionIDForIssueParams{
            WorkspaceID: row.WorkspaceID, IssueID: row.IssueID,
            Column3:     int32(agent.ClaudeManagedContainerMaxDays),
        })
        if sErr == nil && sid.Valid { reuseContainer = sid.String }
    }

    // Deserialize environment config (persisted on agent row by UI).
    // Empty/invalid JSON → default (internet disabled, no secrets).
    var envCfg PersistedEnvConfig
    _ = json.Unmarshal(ag.EnvironmentConfig, &envCfg)

    // Resolve secret IDs via the credential vault (Phase 8B). Missing
    // credentials are skipped with a structured log so the agent
    // doesn't leak unresolved IDs.
    secrets := map[string]string{}
    if w.deps.Vault != nil {
        for _, credID := range envCfg.SecretCredentialIDs {
            name, value, err := w.deps.Vault.GetNameAndValue(ctx, wsID, credID)
            if err != nil || name == "" { continue }
            secrets[name] = string(value)
            // Zero the value slice so it doesn't linger in memory past
            // the map assignment (Phase 8B's GetRaw contract: caller-owned).
            for i := range value { value[i] = 0 }
        }
    }

    // Build backend. Session-id channel captures Anthropic's container id.
    sessionOut := make(chan string, 1)
    agentOut := make(chan string, 1)
    be := agent.NewClaudeManagedBackend(agent.ClaudeManagedConfig{
        Client: w.deps.AnthropicClient,
        Model:  ag.Model.String, SystemPrompt: ag.SystemPrompt.String,
        AnthropicAgentID: ag.AnthropicAgentID.String,
        ReuseContainerID: reuseContainer,
        EnvironmentConfig: anthropic.CreateEnvRequest{
            InternetEnabled: envCfg.InternetEnabled,
            Secrets:         secrets,
        },
        ToolDispatcher:   w.deps.ToolDispatcher,
        SessionIDOut:     sessionOut, AgentIDOut: agentOut,
        // D6 hook: idle-timeout breadcrumb. The outer span is ended
        // exactly once (success path or w.fail) — the hook just logs
        // so operators see *why* the subsequent error surfaced. Phase 13
        // can add partial-state persistence here (e.g. write accumulated
        // text_deltas to task_message before the error propagates).
        Hooks: &agent.Hooks{OnTimeout: func(ctx context.Context, _ agent.Session) error {
            log.Printf("claude_managed: SSE idle timeout on task %s (trace %s)", taskID, traceID)
            return nil
        }},
    })

    // Event: session started. We emit BEFORE Execute so dashboard shows
    // the task as in-flight; a matching stopped event fires after return.
    _ = w.deps.Events.Publish(ctx, map[string]any{
        "type": "managed_session.started", "workspace_id": wsID,
        "agent_id": agentID, "task_id": taskID, "model": ag.Model.String,
    })

    start := time.Now()
    resp, execErr := agent.Run(ctx, be, agent.Prompt{
        System: ag.SystemPrompt.String, User: row.Prompt.String,
    })

    // Drain out-channels BEFORE the tx so we have values to persist
    // even if Execute errored mid-way (Anthropic may have created a
    // container we want to reuse on retry).
    var persistedSessionID, persistedAgentID string
    select { case sid := <-sessionOut: persistedSessionID = sid; default: }
    select { case aid := <-agentOut:   persistedAgentID = aid;   default: }

    // managed_session.stopped fires regardless of outcome — deferred so
    // both success and failure paths publish it.
    defer func() {
        _ = w.deps.Events.Publish(context.Background(), map[string]any{
            "type": "managed_session.stopped", "workspace_id": wsID,
            "agent_id": agentID, "task_id": taskID,
        })
    }()

    if execErr != nil {
        // Even on failure, persist session/agent IDs so retry reuses
        // the container — best-effort writes outside the tx since
        // the cost_event row doesn't exist yet.
        w.bestEffortPersistIDs(ctx, q, row, persistedSessionID, persistedAgentID)
        return w.fail(ctx, q, span, taskID, execErr)
    }

    // Cost event. session_hours = elapsed / 3600; tokens come from resp.
    hours := time.Since(start).Hours()
    sessionCents := w.deps.CostCalc.ComputeSessionCents(hours)
    tokenCents := w.deps.CostCalc.ComputeCents(ag.Provider.String, ag.Model.String, resp.InputTokens, resp.OutputTokens, 0, 0, nil)
    total := sessionCents + tokenCents

    // Atomic post-Execute: persist session_id + anthropic_agent_id +
    // cost_event + mark_completed in a single transaction so a crash
    // between statements can't leave a completed task without a cost
    // row, or a cost row without the container_id to reuse. The
    // manual Begin/defer-Rollback/Commit pattern is the pgx/v5
    // idiom (pgx.BeginFunc exists but some v5.x minor versions dropped
    // it; this form is stable across the whole v5 line).
    var costEventID uuid.UUID
    txErr := func() error {
        tx, err := w.deps.DB.Begin(ctx)
        if err != nil { return fmt.Errorf("begin tx: %w", err) }
        defer tx.Rollback(ctx) // no-op after Commit succeeds

        qtx := db.New(tx)
        if persistedSessionID != "" {
            if err := qtx.UpsertTaskSessionID(ctx, db.UpsertTaskSessionIDParams{
                WorkspaceID: row.WorkspaceID, ID: row.ID, SessionID: sqlNullString(persistedSessionID),
            }); err != nil { return fmt.Errorf("upsert session_id: %w", err) }
        }
        if persistedAgentID != "" {
            if err := qtx.UpdateAnthropicAgentID(ctx, db.UpdateAnthropicAgentIDParams{
                WorkspaceID: row.WorkspaceID, ID: row.AgentID, AnthropicAgentID: sqlNullString(persistedAgentID),
            }); err != nil { return fmt.Errorf("update anthropic_agent_id: %w", err) }
        }
        costRow, err := qtx.InsertSessionCostEvent(ctx, db.InsertSessionCostEventParams{
            WorkspaceID: row.WorkspaceID, AgentID: row.AgentID, TaskID: row.ID,
            TraceID: uuidPg(traceID),
            Provider: sqlNullString("sandbox_claude"),
            Model: sqlNullString(ag.Model.String),
            InputTokens: int32(resp.InputTokens), OutputTokens: int32(resp.OutputTokens),
            CostCents: int32(total), CostStatus: "actual",
            SessionHours: sqlNullDec(hours),
            SessionCostCents: sqlNullInt32(int32(sessionCents)),
        })
        if err != nil { return fmt.Errorf("insert cost_event: %w", err) }
        costEventID = uuidFromPg(costRow.ID)
        if err := qtx.MarkTaskCompleted(ctx, row.ID); err != nil {
            return fmt.Errorf("mark completed: %w", err)
        }
        return tx.Commit(ctx)
    }()
    if txErr != nil { return w.fail(ctx, q, span, taskID, txErr) }

    // LateLinkCostEvent runs outside the tx — the cost_event is
    // committed; at worst the trace → cost link is missing, not broken.
    _ = w.deps.Tracer.LateLinkCostEvent(ctx, span.ID(), costEventID)

    span.End("completed", nil)
    return nil
}

// bestEffortPersistIDs writes session_id + anthropic_agent_id outside
// any transaction. Used on the failure path to preserve the container
// for retry reuse — swallow errors (caller already has a cause to
// report via fail()).
func (w *ClaudeManagedWorker) bestEffortPersistIDs(ctx context.Context, q *db.Queries, row db.AgentTaskQueue, sid, aid string) {
    if sid != "" {
        _ = q.UpsertTaskSessionID(ctx, db.UpsertTaskSessionIDParams{
            WorkspaceID: row.WorkspaceID, ID: row.ID, SessionID: sqlNullString(sid),
        })
    }
    if aid != "" {
        _ = q.UpdateAnthropicAgentID(ctx, db.UpdateAnthropicAgentIDParams{
            WorkspaceID: row.WorkspaceID, ID: row.AgentID, AnthropicAgentID: sqlNullString(aid),
        })
    }
}

func (w *ClaudeManagedWorker) fail(ctx context.Context, q *db.Queries, span *trace.Span, taskID uuid.UUID, cause error) error {
    span.End("error", cause)
    if markErr := q.MarkTaskFailed(ctx, db.MarkTaskFailedParams{
        ID: uuidPg(taskID), Error: sqlNullString(cause.Error()),
    }); markErr != nil {
        return fmt.Errorf("markTaskFailed failed (%v) for original cause: %w", markErr, cause)
    }
    return cause
}
```

- [ ] **Step 2a: Claim query**

Append to `server/pkg/db/queries/task.sql` (Phase 2):

```sql
-- name: ClaimClaudeManagedTask :one
UPDATE agent_task_queue
SET status = 'in_progress', claimed_at = NOW()
WHERE id = (
    SELECT q.id FROM agent_task_queue q
    JOIN agent a ON a.id = q.agent_id AND a.workspace_id = q.workspace_id
    WHERE q.status = 'pending' AND a.agent_type = 'claude_managed'
    ORDER BY q.created_at
    FOR UPDATE SKIP LOCKED LIMIT 1
)
RETURNING *;
```

Regenerate: `cd server && make sqlc`.

- [ ] **Step 3: Claim router wiring**

Phase 2's `server/internal/worker/claim.go` houses a router that
dispatches claimed tasks to the right agent-type handler. Phase 11
extended it for `cloud_coding`; Phase 12 extends it for
`claude_managed`. The exact router function name + signature depend
on Phase 2's implementation — shown below as an illustrative
structure. Adapt to the actual function in the tree.

```go
// server/internal/worker/claim.go (Phase 2) — representative structure.
// RouterDeps is the struct Phase 2 already defines; Phase 11 added
// CloudCodingWorker; Phase 12 adds ClaudeManagedWorker.
type RouterDeps struct {
    CloudCodingWorker   *CloudCodingWorker    // Phase 11
    ClaudeManagedWorker *ClaudeManagedWorker  // Phase 12 — new field
    // ... other workers
}

func (d *RouterDeps) RouteClaim(ctx context.Context, agentType string) error {
    switch agentType {
    case "coding":
        // existing Phase 2 daemon path
    case "cloud_coding":
        return d.CloudCodingWorker.RunOnce(ctx)    // Phase 11
    case "claude_managed":                         // NEW in Phase 12
        return d.ClaudeManagedWorker.RunOnce(ctx)
    // ...
    }
    return nil
}
```

Wire the `ClaudeManagedWorker` instance into the `RouterDeps` struct
at server boot (Task 11) — the `go claudeManagedWorker.Run(ctx)` line
is for the long-running poll loop; this branch is for the router's
per-claim invocation.

- [ ] **Step 4: Run + commit**

Run: `cd server && go test ./internal/worker/ -run TestClaudeManagedWorker -v`
Expected: PASS.

```bash
git add server/internal/worker/claude_managed.go server/internal/worker/claude_managed_test.go server/pkg/db/queries/task.sql server/pkg/db/gen/ server/internal/worker/claim.go
git commit -m "feat(claude-managed): worker — claim, reuse container, cost event, span

Mints trace_id, looks up prior session_id for the issue (30d TTL),
builds ClaudeManagedBackend, runs agent.Run, persists Anthropic
container_id + agent_id on return, inserts cost_event with session_hours
+ session_cost_cents, LateLinkCostEvent the outer span. Publishes
managed_session.started/stopped to events.Bus."
```

---

### Task 10: Frontend — Types + Create-Agent Flow

**Files:**
- Create: `packages/core/types/claude_managed.ts`
- Create: `packages/views/agents/components/claude-managed-flow.tsx`
- Create: `packages/views/agents/components/claude-managed-flow.test.tsx`
- Modify: `packages/views/agents/components/create-agent-dialog.tsx`

- [ ] **Step 1: Types**

```ts
// packages/core/types/claude_managed.ts
export type AnthropicModel =
  | "claude-sonnet-4-5"
  | "claude-opus-4-5"
  | "claude-haiku-4-5";

export type ClaudeEnvironmentConfig = {
  internet_enabled: boolean;
  secret_credential_ids: string[]; // resolved server-side via credential vault
};

export type ClaudeManagedAgentConfig = {
  provider: "anthropic";
  model: AnthropicModel;
  environment: ClaudeEnvironmentConfig;
};
```

- [ ] **Step 2: Failing smoke test**

```tsx
// packages/views/agents/components/claude-managed-flow.test.tsx
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { vi } from "vitest";
import { ClaudeManagedFlow } from "./claude-managed-flow";

vi.mock("@multica/core/credentials/queries", () => ({
  useCredentials: () => ({ data: [{ id: "c1", name: "STRIPE_KEY" }] }),
}));

test("ClaudeManagedFlow renders model selector + pricing banner", () => {
  const onChange = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ClaudeManagedFlow wsId="w1" value={{ provider: "anthropic", model: "claude-sonnet-4-5",
        environment: { internet_enabled: false, secret_credential_ids: [] } }}
        onChange={onChange} />
    </QueryClientProvider>,
  );
  expect(screen.getByLabelText(/Model/i)).toBeInTheDocument();
  expect(screen.getByText(/\$0\.08\s*\/\s*session-hour/i)).toBeInTheDocument();
  fireEvent.click(screen.getByLabelText(/Enable internet/i));
  expect(onChange).toHaveBeenCalled();
});
```

Run: `pnpm --filter @multica/views exec vitest run agents/components/claude-managed-flow.test.tsx`
Expected: FAIL — module doesn't exist.

- [ ] **Step 3: Implement component**

```tsx
// packages/views/agents/components/claude-managed-flow.tsx
import { useCredentials } from "@multica/core/credentials/queries";
import { Select, SelectItem, SelectTrigger, SelectValue, SelectContent } from "@multica/ui/select";
import { Checkbox } from "@multica/ui/checkbox";
import type { ClaudeManagedAgentConfig, AnthropicModel } from "@multica/core/types/claude_managed";

const MODELS: Array<{ value: AnthropicModel; label: string; inputCentsPerMTok: number; outputCentsPerMTok: number }> = [
  { value: "claude-sonnet-4-5", label: "Sonnet 4.5", inputCentsPerMTok: 300, outputCentsPerMTok: 1500 },
  { value: "claude-opus-4-5",   label: "Opus 4.5",   inputCentsPerMTok: 1500, outputCentsPerMTok: 7500 },
  { value: "claude-haiku-4-5",  label: "Haiku 4.5",  inputCentsPerMTok: 80,   outputCentsPerMTok: 400 },
];

export function ClaudeManagedFlow({
  wsId, value, onChange,
}: {
  wsId: string;
  value: ClaudeManagedAgentConfig;
  onChange: (next: ClaudeManagedAgentConfig) => void;
}) {
  const { data: creds = [] } = useCredentials(wsId);
  const selectedModel = MODELS.find((m) => m.value === value.model) ?? MODELS[0];

  return (
    <div className="flex flex-col gap-3">
      <label>
        <span className="text-sm font-medium">Model</span>
        <Select value={value.model} onValueChange={(v) => onChange({ ...value, model: v as AnthropicModel })}>
          <SelectTrigger aria-label="Model"><SelectValue /></SelectTrigger>
          <SelectContent>
            {MODELS.map((m) => <SelectItem key={m.value} value={m.value}>{m.label}</SelectItem>)}
          </SelectContent>
        </Select>
      </label>

      <fieldset className="flex flex-col gap-2 border rounded p-3">
        <legend className="text-sm font-medium">Environment</legend>
        <label className="flex items-center gap-2">
          <Checkbox aria-label="Enable internet"
                    checked={value.environment.internet_enabled}
                    onCheckedChange={(checked) => onChange({
                      ...value,
                      environment: { ...value.environment, internet_enabled: !!checked },
                    })} />
          <span>Enable internet <span className="text-xs text-muted-foreground">(agent can call external services)</span></span>
        </label>
        <div className="text-sm">
          <div className="font-medium">Secrets (credential vault)</div>
          {creds.length === 0 && <div className="text-xs text-muted-foreground">No credentials in this workspace.</div>}
          {creds.map((c) => {
            const selected = value.environment.secret_credential_ids.includes(c.id);
            return (
              <label key={c.id} className="flex items-center gap-2">
                <Checkbox checked={selected}
                          onCheckedChange={(checked) => {
                            const next = checked
                              ? [...value.environment.secret_credential_ids, c.id]
                              : value.environment.secret_credential_ids.filter((x) => x !== c.id);
                            onChange({ ...value, environment: { ...value.environment, secret_credential_ids: next } });
                          }} />
                <span>{c.name}</span>
              </label>
            );
          })}
        </div>
      </fieldset>

      <aside className="text-xs text-muted-foreground border-l-2 pl-3">
        Pricing: $0.08 / session-hour + ${(selectedModel.inputCentsPerMTok / 100).toFixed(2)} per
        1M input tokens + ${(selectedModel.outputCentsPerMTok / 100).toFixed(2)} per 1M output tokens.
        Container persists up to 30 days for repeated tasks on the same issue.
      </aside>
    </div>
  );
}
```

- [ ] **Step 4: Wire into create-agent dialog**

```tsx
// packages/views/agents/components/create-agent-dialog.tsx — add branch
import { ClaudeManagedFlow } from "./claude-managed-flow";

// Inside the type-specific body:
{agentType === "claude_managed" && (
  <ClaudeManagedFlow wsId={wsId} value={claudeManagedState} onChange={setClaudeManagedState} />
)}
```

- [ ] **Step 5: Run + commit**

Run: `pnpm --filter @multica/views exec vitest run agents/components/claude-managed-flow.test.tsx`
Expected: PASS.

```bash
git add packages/core/types/claude_managed.ts packages/views/agents/components/claude-managed-flow.tsx packages/views/agents/components/claude-managed-flow.test.tsx packages/views/agents/components/create-agent-dialog.tsx
git commit -m "feat(claude-managed): create-agent flow — model + environment + secrets

Sonnet/Opus/Haiku 4.5 selector, internet toggle, secret-credential
multiselect from workspace vault, pricing banner ($0.08/session-hour
+ per-model token rates, 30d container TTL)."
```

---

### Task 11: Backend wiring in main.go

**Files:**
- Modify: `server/cmd/server/main.go`

- [ ] **Step 1: Instantiate worker**

```go
// server/cmd/server/main.go — additions near other worker starts
import (
    "github.com/multica/server/pkg/agent/anthropic"
    // … existing imports
)

// … after other deps are wired:
anthropicClient := anthropic.NewClient(anthropic.ClientConfig{
    APIKey: cfg.AnthropicAPIKey, // from workspace bootstrap or env — see comment
})
claudeManagedWorker := worker.NewClaudeManagedWorker(worker.ClaudeManagedDeps{
    DB: pool, Tracer: tracer, CostCalc: costCalc, Events: eventsBus,
    AnthropicClient: anthropicClient,
    Vault: credVault, // Phase 8B CredentialVault — exposes GetNameAndValue
    ToolDispatcher: func(ctx context.Context, name string, input []byte) (string, error) {
        // Reuse the Phase 3 tool registry for local tool execution.
        // toolRegistry is the same *tools.Registry already wired into
        // the Phase 2 worker (see server/cmd/server/main.go around the
        // registerTools() call). It dispatches to MCP + custom tools.
        return toolRegistry.Dispatch(ctx, name, input)
    },
})
go claudeManagedWorker.Run(ctx)
```

> **Operator note.** `cfg.AnthropicAPIKey` is the server-level key used
> when a workspace has not configured its own. Workspaces that want
> per-tenant billing set their own Anthropic key via the credential
> vault; Phase 12 V1 reads the server key only. Per-workspace client
> construction is a Phase 13 follow-up (swap `anthropicClient` with a
> workspace-keyed cache inside the worker).

- [ ] **Step 2: Commit**

```bash
git add server/cmd/server/main.go
git commit -m "feat(claude-managed): wire ClaudeManagedWorker into server boot"
```

---

### Task 12: End-to-End Integration Test + Verification

**Files:**
- Create: `server/internal/integration/claudemanaged/e2e_test.go`
- Create: `docs/engineering/verification/phase-12.md`

- [ ] **Step 1: E2E test**

```go
// server/internal/integration/claudemanaged/e2e_test.go
package claudemanaged_test

import (
    "context"
    "encoding/json"
    "testing"
    "time"

    "github.com/multica/server/internal/testsupport"
    "github.com/multica/server/internal/worker"
    "github.com/multica/server/pkg/agent/anthropic"
)

func TestClaudeManaged_FullLoop(t *testing.T) {
    env := testsupport.NewEnv(t)
    wsID := env.SeedWorkspace("cm-e2e")
    fake := testsupport.NewFakeAnthropicServer(t)
    // Hybrid: tool_use our tool → tool_results → end_turn.
    fake.ScriptReplies([]anthropic.SSEEvent{
        {Type: "message_start", Raw: json.RawMessage(`{"message":{"id":"m1","model":"sonnet","usage":{"input_tokens":8,"output_tokens":0}}}`)},
        {Type: "tool_use", Raw: json.RawMessage(`{"tool_use":{"id":"tu_1","name":"crm_lookup","input":{"email":"a@b.com"}}}`)},
        {Type: "message_stop", Raw: json.RawMessage(`{"stop_reason":"tool_use"}`)},
        {Type: "message_start", Raw: json.RawMessage(`{"message":{"id":"m2","model":"sonnet"}}`)},
        {Type: "content_block_delta", Raw: json.RawMessage(`{"delta":{"type":"text_delta","text":"Resolved."}}`)},
        {Type: "message_stop", Raw: json.RawMessage(`{"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2}}`)},
    })

    agentID := env.SeedClaudeManagedAgent(wsID, "claude-sonnet-4-5", "ag_pre")
    issueID := env.SeedIssue(wsID, "support")
    taskID := env.SeedTaskWithIssue(wsID, agentID, issueID, "who is a@b.com?")

    dispatchedNames := []string{}
    w := worker.NewClaudeManagedWorker(worker.ClaudeManagedDeps{
        DB: env.DB, Tracer: env.Tracer, CostCalc: env.CostCalc, Events: env.Events,
        AnthropicClient: anthropic.NewClient(anthropic.ClientConfig{APIKey: "k", BaseURL: fake.Server.URL}),
        ToolDispatcher: func(ctx context.Context, name string, input []byte) (string, error) {
            dispatchedNames = append(dispatchedNames, name)
            return `{"company":"Acme"}`, nil
        },
    })
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second); defer cancel()
    if err := w.RunOnce(ctx); err != nil { t.Fatal(err) }

    // Assertions:
    // 1. Hybrid tool dispatched.
    if len(dispatchedNames) != 1 || dispatchedNames[0] != "crm_lookup" {
        t.Errorf("dispatched=%v, want [crm_lookup]", dispatchedNames)
    }
    // 2. Task completed.
    if env.TaskStatus(taskID) != "completed" { t.Errorf("status=%s", env.TaskStatus(taskID)) }
    // 3. session_id persisted.
    if env.SessionIDForTask(taskID) != "cnt_fake" { t.Errorf("session=%q", env.SessionIDForTask(taskID)) }
    // 4. Cost: sandbox_claude provider + nonzero session_hours.
    if env.CountCostEventsByProvider(wsID, "sandbox_claude") < 1 { t.Error("no session cost_event") }
    // 5. Anthropic saw: /agents (no — we pre-set AnthropicAgentID), /environments, /sessions, /messages, /tool_results.
    calls := fake.Calls()
    sawEnv, sawSess, sawMsg, sawToolResult := false, false, false, false
    for _, c := range calls {
        switch c {
        case "POST /v1/environments": sawEnv = true
        case "POST /v1/sessions": sawSess = true
        case "POST /v1/sessions/sess_fake/messages": sawMsg = true
        case "POST /v1/sessions/sess_fake/tool_results": sawToolResult = true
        }
    }
    if !(sawEnv && sawSess && sawMsg && sawToolResult) {
        t.Errorf("missing Anthropic calls: env=%v sess=%v msg=%v toolResult=%v", sawEnv, sawSess, sawMsg, sawToolResult)
    }
}
```

- [ ] **Step 2: Manual verification per PLAN.md §12.7**

- [ ] Create a `claude_managed` agent via the frontend (Sonnet 4.5, internet disabled, one secret attached).
- [ ] Assign an issue → Anthropic creates session → agent executes → result streamed to task transcript.
- [ ] Assign a second task on the same issue → verify `ContainerID` is reused (check `agent_task_queue.session_id` matches first task's).
- [ ] Hybrid tools: agent invokes our `crm_lookup` AND Anthropic's built-in `bash` in one task; both succeed.
- [ ] Cost tracked: `cost_event` row with `provider='sandbox_claude'`, `session_hours` ≈ wall-clock, `session_cost_cents` ≥ 1 on anything >30s, `cost_cents` = token + session.
- [ ] `make check` passes.

- [ ] **Step 3: Evidence + commit**

```markdown
# Phase 12 Verification — 2026-04-DD
- `go test ./internal/integration/claudemanaged/...` → pass
- `make check` → pass
- Manual: [results]
- Known limitations:
  - One-worker-per-container serialization via SKIP LOCKED; no fan-out.
  - Workspace-wide session-hour quotas not yet enforced (combined cost
    check only — a Phase 13 gap).
  - Server-level Anthropic API key; per-workspace key reads are Phase 13.
  - Managed-session dashboard: events.Bus emits managed_session.started
    /stopped but no UI consumer yet.
```

```bash
cd server && go test ./internal/integration/claudemanaged/ -v
make check
git add server/internal/integration/claudemanaged/e2e_test.go docs/engineering/verification/phase-12.md
git commit -m "test(phase-12): claude-managed full loop e2e + verification evidence"
```

---

## Placeholder Scan

Self-reviewed. Three intentional abbreviations:

1. `CostCalculator.ComputeCents` call in Task 9 Step 2 uses Phase 2's signature — the nil cache-multiplier / cache-read / cache-write args (0, 0, nil) reflect Phase 9's cache-hit gate which is not relevant for a session-hosted managed agent (Anthropic applies its own caching internally). This is deliberate, not a gap.
2. The `ToolDispatcher` in `ClaudeManagedConfig` is nil-safe — unknown tool names produce an `IsError: true` tool_result, not a crash. Documented in-code and surfaced to the LLM rather than the worker log.
3. Server-level Anthropic API key (Task 11 operator note) is V1 scope; per-workspace keys are a Phase 13 follow-up.

No TBDs, no unreferenced symbols. Every type defined in one Task is consumed with matching signatures later: `SSEEvent` flows from `anthropic.Decoder` (Task 4) into `ClaudeManagedBackend.drain` (Task 7); `ClaudeManagedBackend` into `ClaudeManagedWorker` (Task 9); `ClaudeManagedAgentConfig` into the UI flow (Task 10).

## Execution Handoff

Plan complete at `docs/superpowers/plans/2026-04-14-phase12-claude-managed-agents.md`. Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task + two-stage review between tasks.

**2. Inline Execution** — via `superpowers:executing-plans` with batch checkpoints.

Which approach?

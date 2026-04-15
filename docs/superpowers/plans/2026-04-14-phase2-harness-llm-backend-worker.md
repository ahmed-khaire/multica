# Phase 2: Harness (go-ai adapter) + LLM API Backend + Tools + Router + Worker

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agents can execute server-side through the go-ai `ToolLoopAgent` — no CLI subprocess required. Cloud workers claim tasks by provider, run them through a thin harness adapter, stream results back via the existing daemon protocol, and record cost events. Three new `Backend` implementations (LLM API, HTTP, Process) cover the non-coding agent types introduced in Phase 1.

**Architecture:** `Backend` interface (extended with `ExpiresAt()` + `Hooks` per D6) stays our in-house boundary. `server/pkg/agent/harness/` wraps go-ai's `ToolLoopAgent` — this is where tools, skills (stub for Phase 3), system prompt, and callbacks are wired. `llm_api.go` builds a go-ai `LanguageModel`, layers on credential rotation and error classification, and hands off to the harness. Multi-model fallback wraps any `Backend`. Cloud worker polls the existing daemon endpoints but executes via the new backends instead of spawning CLIs. Redis is the baseline dependency (D4) with `RedisAdapter` interface + memory impl for dev.

**Tech Stack:** Go 1.26, `github.com/digitallysavvy/go-ai/pkg/{ai,agent,provider,provider/types,providers/anthropic,providers/openai}`, `github.com/redis/go-redis/v9`, PostgreSQL (sqlc), existing daemon HTTP protocol.

---

## Scope Note

This is Phase 2 of the 12-phase plan in `PLAN.md`. Depends on Phase 1 (agent_type/provider/model columns, error classifier, credential pool, go-ai dependency pinned, `defaults.go`). Phase 3 (MCP + skills cache) and Phase 4 (structured outputs + RAG) both depend on this phase's harness and tool registry.

## File Structure

### New Files (Backend)
| File | Responsibility |
|------|---------------|
| `server/pkg/agent/harness/harness.go` | Thin adapter over go-ai `ToolLoopAgent`; injects tools + prompt + callbacks |
| `server/pkg/agent/harness/prompt.go` | System prompt assembly (core rules + model-family overlay + env details) |
| `server/pkg/agent/harness/harness_test.go` | Tests: registry integration, callback wiring, stop conditions |
| `server/pkg/agent/llm_api.go` | `Backend` implementation for `agent_type=llm_api` |
| `server/pkg/agent/llm_api_test.go` | Integration tests with mock `provider.LanguageModel` |
| `server/pkg/agent/http_backend.go` | `Backend` implementation for `agent_type=http` |
| `server/pkg/agent/http_backend_test.go` | Tests with httptest server |
| `server/pkg/agent/process_backend.go` | `Backend` implementation for `agent_type=process` |
| `server/pkg/agent/process_backend_test.go` | Tests with canned scripts |
| `server/pkg/agent/fallback.go` | Multi-model fallback chain wrapper |
| `server/pkg/agent/fallback_test.go` | Tests: fallback triggers, graceful degradation |
| `server/pkg/agent/provider_health.go` | In-memory rolling-window provider health tracker |
| `server/pkg/agent/provider_health_test.go` | Tests: health state transitions, recovery |
| `server/pkg/agent/credential_rotation.go` | Wraps `provider.LanguageModel` with rotation on rate_limit |
| `server/pkg/agent/credential_rotation_test.go` | Tests with mock credential pool |
| `server/pkg/tools/registry.go` | Workspace-scoped `types.Tool` registry |
| `server/pkg/tools/registry_test.go` | Tests: per-agent enablement, gating |
| `server/pkg/tools/builtin/http_request.go` | Built-in HTTP-request tool |
| `server/pkg/tools/builtin/http_request_test.go` | Tests for http_request |
| `server/pkg/tools/builtin/search.go` | Web search tool (Brave/Serper pluggable) |
| `server/pkg/tools/builtin/search_test.go` | Tests for search |
| `server/pkg/tools/builtin/email.go` | Send-email tool (SMTP + Resend/SendGrid) |
| `server/pkg/tools/builtin/email_test.go` | Tests for send_email |
| `server/pkg/tools/builtin/database_query.go` | External DB query tool |
| `server/pkg/tools/builtin/database_query_test.go` | Tests for database_query |
| `server/pkg/agent/credentials_service.go` | Production `CredentialPool` implementation (encrypted vault → API keys) |
| `server/pkg/agent/credentials_service_test.go` | Tests for credential pool service |
| `server/internal/worker/client.go` | Daemon-protocol `Client` production impl (wraps `server/internal/daemon/client.go`) |
| `server/internal/worker/resolver.go` | Production `BackendResolver` (task row → concrete `Backend`) |
| `server/internal/worker/cost_sink.go` | Production `CostSink` (writes to `cost_event` via sqlc) |
| `server/internal/worker/backoff.go` | Exponential-backoff retry for claim + execute |
| `server/internal/worker/backoff_test.go` | Tests for backoff math |
| `server/internal/worker/heartbeat.go` | Periodic heartbeat with slot count |
| `server/internal/worker/heartbeat_test.go` | Tests for heartbeat timing |
| `server/internal/service/testutil.go` | DB-backed test helpers (`NewTestDB`, workspace/agent/task builders) |
| `server/pkg/router/router.go` | Model-tier routing (micro/standard/premium) |
| `server/pkg/router/cost.go` | Cache-aware cost calculator |
| `server/pkg/router/router_test.go` | Tests: tier selection, policy overrides |
| `server/pkg/router/cost_test.go` | Tests: pricing math, cache-read/write multipliers |
| `server/pkg/redis/adapter.go` | `RedisAdapter` interface + `memory` impl + `redis` impl |
| `server/pkg/redis/adapter_test.go` | Interface-compliance tests for both impls |
| `server/internal/service/budget.go` | Pre-dispatch budget check service |
| `server/internal/service/budget_test.go` | Tests: workspace/agent/project scope, warning threshold |
| `server/cmd/worker/main.go` | Cloud worker binary entry point |
| `server/internal/worker/worker.go` | Poll-execute-report loop with slot pool |
| `server/internal/worker/heartbeat.go` | Periodic heartbeat with slot count |
| `server/internal/worker/health.go` | k8s liveness/readiness probes + graceful shutdown |
| `server/internal/worker/worker_test.go` | Tests: claim → execute → report cycle with mock backend |

### Modified Files (Backend)
| File | Changes |
|------|---------|
| `server/pkg/agent/agent.go` | Extend `Backend` interface with `ExpiresAt()` + add `Hooks` struct (D6) |
| `server/pkg/db/queries/agent.sql` | Add `ClaimTaskForProvider` query for runtimeless agents |
| `server/internal/service/task.go` | Add `ClaimTaskForProvider` method |
| `server/cmd/server/router.go` | Optional `--with-worker` flag to embed worker goroutines |
| `server/go.mod` | Add `github.com/redis/go-redis/v9` |

### External Infrastructure
| System | Change |
|--------|--------|
| `make start` / `docker-compose.dev.yml` | Add Redis container alongside Postgres (D4 baseline) |
| CI (`.github/workflows/ci.yml`) | Add Redis service for integration tests |

---

### Task 1: Extend `Backend` Interface with `ExpiresAt` + `Hooks` (D6)

**Files:**
- Modify: `server/pkg/agent/agent.go`
- Create: `server/pkg/agent/agent_hooks_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/agent_hooks_test.go
package agent

import (
	"context"
	"testing"
	"time"
)

// stubBackend is a minimal Backend used only to verify the interface shape.
type stubBackend struct {
	expires *time.Time
}

func (s *stubBackend) Execute(_ context.Context, _ string, _ ExecOptions) (*Session, error) {
	return nil, nil
}
func (s *stubBackend) ExpiresAt() *time.Time { return s.expires }

func TestBackendInterface_HasExpiresAt(t *testing.T) {
	t.Parallel()
	var b Backend = &stubBackend{}
	if b.ExpiresAt() != nil {
		t.Error("stub should return nil ExpiresAt")
	}
}

func TestHooks_AllFieldsNoOpSafe(t *testing.T) {
	t.Parallel()
	h := Hooks{}
	// Zero-value Hooks must be safe to call — every caller should nil-check.
	if h.AfterStart != nil || h.BeforeStop != nil || h.OnTimeout != nil || h.OnTimeoutExtended != nil {
		t.Error("zero-value Hooks should have nil fields")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run "TestBackendInterface_HasExpiresAt|TestHooks_AllFieldsNoOpSafe" -v`
Expected: FAIL — `ExpiresAt` not on interface, `Hooks` undefined.

- [ ] **Step 3: Extend the interface and add Hooks**

Update `server/pkg/agent/agent.go`. Find the existing `Backend` interface and replace:

```go
// Backend is the unified interface for executing prompts via coding agents
// and non-coding agents alike.
type Backend interface {
	// Execute runs a prompt and returns a Session for streaming results.
	Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error)

	// ExpiresAt returns when the current execution context must be drained
	// and stopped, or nil if the backend has no time cap. LLM API / HTTP /
	// Process backends return nil. Sandbox backends (Phase 11) return the
	// provider's session-cap expiry; the worker schedules OnTimeout to fire
	// SandboxTimeoutBufferMs before this value so the agent can snapshot.
	ExpiresAt() *time.Time
}

// Hooks contains optional lifecycle callbacks invoked by the worker around
// a Backend.Execute call. Backends that don't need a hook should leave the
// field nil — callers MUST nil-check each field before invoking.
type Hooks struct {
	AfterStart        func(ctx context.Context, s *Session) error
	BeforeStop        func(ctx context.Context, s *Session) error
	OnTimeout         func(ctx context.Context, s *Session) error
	OnTimeoutExtended func(ctx context.Context, s *Session, additional time.Duration) error
}
```

Also update the existing concrete backends (`claudeBackend`, `codexBackend`, `opencodeBackend`, `openclawBackend`) to satisfy the new method. Add a shared embedded type:

```go
// noExpiry is embedded in backends that have no time cap of their own
// (coding CLIs running inside a daemon; LLM API; HTTP; Process).
// Sandbox backends (Phase 11) override ExpiresAt().
type noExpiry struct{}

func (noExpiry) ExpiresAt() *time.Time { return nil }
```

Add `noExpiry` as an embedded field on each existing backend struct (`claudeBackend`, etc.).

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/... -v`
Expected: All existing + new tests PASS. `go build ./...` succeeds.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/agent.go server/pkg/agent/agent_hooks_test.go \
        server/pkg/agent/claude.go server/pkg/agent/codex.go \
        server/pkg/agent/opencode.go server/pkg/agent/openclaw.go
git commit -m "feat(agent): extend Backend interface with ExpiresAt + Hooks (D6)

Every backend now exposes ExpiresAt so the worker can schedule a
graceful OnTimeout hook SandboxTimeoutBufferMs before the provider's
hard cap. Existing CLI backends embed noExpiry (no cap). Phase 11
sandbox backends will override."
```

---

### Task 1.5: Trace ID Context Helpers (D8)

**Files:**
- Create: `server/pkg/agent/trace.go`
- Create: `server/pkg/agent/trace_test.go`

**Goal:** PLAN.md §D8 makes `trace_id` a mandatory, context-carried identifier minted at task entry and attached to every `cost_event`, subagent dispatch, MCP tool call, and Phase 10 trace span. Establish the context key + helpers here so every later task in Phase 2 has a stable surface to use.

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/trace_test.go
package agent

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestTraceID_RoundTrip(t *testing.T) {
	id := uuid.New()
	ctx := WithTraceID(context.Background(), id)
	got := TraceIDFrom(ctx)
	if got != id {
		t.Errorf("TraceIDFrom = %v, want %v", got, id)
	}
}

func TestTraceID_Missing_ReturnsZero(t *testing.T) {
	got := TraceIDFrom(context.Background())
	if got != uuid.Nil {
		t.Errorf("missing trace_id = %v, want uuid.Nil", got)
	}
}
```

- [ ] **Step 2: Implement**

```go
// server/pkg/agent/trace.go
package agent

import (
	"context"

	"github.com/google/uuid"
)

// traceIDKey is unexported so callers must use WithTraceID / TraceIDFrom.
// This prevents accidental key collisions across packages.
type traceIDCtxKey struct{}

var traceIDKey = traceIDCtxKey{}

// WithTraceID returns a ctx carrying id. The worker.claimAndExecute loop
// mints a fresh UUID at task entry and wraps the execution context before
// any backend call — see Task 22. Subagent dispatch (Phase 6) inherits via
// the parent's context; MCP tool invocations inherit via the harness's
// OnToolCallStart callback ctx.
func WithTraceID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceIDFrom returns the trace_id stored on ctx, or uuid.Nil if none. A
// uuid.Nil result indicates a programming error — Phase 2 code should never
// produce cost_event rows with a nil trace_id, since the DB column is
// NOT NULL. Test coverage ensures the worker and harness always set it.
func TraceIDFrom(ctx context.Context) uuid.UUID {
	if v, ok := ctx.Value(traceIDKey).(uuid.UUID); ok {
		return v
	}
	return uuid.Nil
}
```

- [ ] **Step 3: Run tests**

```bash
cd server && go test ./pkg/agent -run TestTraceID
```

Both pass.

- [ ] **Step 4: Commit**

```bash
git add server/pkg/agent/trace.go server/pkg/agent/trace_test.go
git commit -m "feat(agent): trace_id context helpers (D8)

Establishes WithTraceID / TraceIDFrom per PLAN.md §D8. Every subsequent
Phase 2 task (harness callbacks, LLM API backend cost recording, worker
core loop, cost sink) reads trace_id from context. Phase 6 / 10 reuse
the same key."
```

---

### Task 2: Redis Adapter (Baseline — D4)

**Files:**
- Create: `server/pkg/redis/adapter.go`
- Create: `server/pkg/redis/memory.go`
- Create: `server/pkg/redis/redis.go`
- Create: `server/pkg/redis/adapter_test.go`
- Modify: `server/go.mod` (adds `github.com/redis/go-redis/v9`)

- [ ] **Step 1: Write the failing interface-compliance test**

```go
// server/pkg/redis/adapter_test.go
package redis

import (
	"context"
	"os"
	"testing"
	"time"
)

// shared contract test — run against every adapter implementation.
func runAdapterContract(t *testing.T, ad Adapter) {
	t.Helper()
	ctx := context.Background()

	if err := ad.Set(ctx, "k1", "v1", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := ad.Get(ctx, "k1")
	if err != nil || !ok || got != "v1" {
		t.Fatalf("Get: got=%q ok=%v err=%v", got, ok, err)
	}
	if _, ok, _ := ad.Get(ctx, "missing"); ok {
		t.Fatal("Get missing: expected ok=false")
	}
	if err := ad.Del(ctx, "k1"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, ok, _ := ad.Get(ctx, "k1"); ok {
		t.Fatal("Get after Del: expected ok=false")
	}

	// Pub/Sub
	sub, err := ad.Subscribe(ctx, "ch1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	if err := ad.Publish(ctx, "ch1", "hello"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case msg := <-sub.Channel():
		if msg != "hello" {
			t.Fatalf("received %q, want hello", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no message received")
	}
}

func TestMemoryAdapter_Contract(t *testing.T) {
	t.Parallel()
	runAdapterContract(t, NewMemoryAdapter())
}

func TestRedisAdapter_Contract(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ad, err := NewRedisAdapter(addr, "")
	if err != nil {
		t.Fatalf("NewRedisAdapter: %v", err)
	}
	defer ad.Close()
	runAdapterContract(t, ad)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/redis/ -v`
Expected: FAIL — package/types missing.

- [ ] **Step 3: Define the interface**

```go
// server/pkg/redis/adapter.go
// Package redis provides a thin abstraction over key-value + pub/sub
// primitives. Production deploys use the Redis implementation; single-process
// dev and tests use the in-memory one.
package redis

import (
	"context"
	"time"
)

// Adapter is the minimal surface Multica needs: ephemeral kv (for skills
// cache + stream resumption), and fan-out pub/sub (for chat routing).
// Richer primitives (streams, consumer groups) can be added if a later
// phase genuinely needs them; YAGNI for now.
type Adapter interface {
	Get(ctx context.Context, key string) (value string, ok bool, err error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Del(ctx context.Context, key string) error

	Publish(ctx context.Context, channel, message string) error
	Subscribe(ctx context.Context, channel string) (Subscription, error)

	Close() error
}

// Subscription is a single channel subscription. Channel() returns a
// read-only stream of messages. Close() releases resources.
type Subscription interface {
	Channel() <-chan string
	Close() error
}
```

- [ ] **Step 4: Write the in-memory implementation**

```go
// server/pkg/redis/memory.go
package redis

import (
	"context"
	"sync"
	"time"
)

type memoryAdapter struct {
	mu      sync.RWMutex
	values  map[string]memoryEntry
	subs    map[string][]chan string
	closeCh chan struct{}
}

type memoryEntry struct {
	value   string
	expires time.Time
}

// NewMemoryAdapter returns an in-memory Adapter. TTLs are enforced lazily
// on Get. Pub/Sub is fan-out: every subscriber on a channel receives every
// message.
func NewMemoryAdapter() Adapter {
	return &memoryAdapter{
		values:  make(map[string]memoryEntry),
		subs:    make(map[string][]chan string),
		closeCh: make(chan struct{}),
	}
}

func (m *memoryAdapter) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.values[key]
	if !ok {
		return "", false, nil
	}
	if !e.expires.IsZero() && time.Now().After(e.expires) {
		return "", false, nil
	}
	return e.value, true, nil
}

func (m *memoryAdapter) Set(_ context.Context, key, value string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	m.values[key] = memoryEntry{value: value, expires: exp}
	return nil
}

func (m *memoryAdapter) Del(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

func (m *memoryAdapter) Publish(_ context.Context, channel, message string) error {
	m.mu.RLock()
	subs := append([]chan string(nil), m.subs[channel]...)
	m.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- message:
		default: // drop on slow consumer — pub/sub is best-effort
		}
	}
	return nil
}

func (m *memoryAdapter) Subscribe(_ context.Context, channel string) (Subscription, error) {
	ch := make(chan string, 16)
	m.mu.Lock()
	m.subs[channel] = append(m.subs[channel], ch)
	m.mu.Unlock()
	return &memorySubscription{
		ch: ch,
		cleanup: func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			subs := m.subs[channel]
			for i, s := range subs {
				if s == ch {
					m.subs[channel] = append(subs[:i], subs[i+1:]...)
					break
				}
			}
			close(ch)
		},
	}, nil
}

func (m *memoryAdapter) Close() error { close(m.closeCh); return nil }

type memorySubscription struct {
	ch      chan string
	cleanup func()
	once    sync.Once
}

func (s *memorySubscription) Channel() <-chan string { return s.ch }
func (s *memorySubscription) Close() error {
	s.once.Do(s.cleanup)
	return nil
}
```

- [ ] **Step 5: Write the Redis-backed implementation**

```go
// server/pkg/redis/redis.go
package redis

import (
	"context"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type redisAdapter struct {
	client *goredis.Client
}

// NewRedisAdapter connects to Redis at addr. password may be empty.
func NewRedisAdapter(addr, password string) (Adapter, error) {
	c := goredis.NewClient(&goredis.Options{Addr: addr, Password: password})
	if err := c.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &redisAdapter{client: c}, nil
}

func (r *redisAdapter) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := r.client.Get(ctx, key).Result()
	if err == goredis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (r *redisAdapter) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttl).Err()
}

func (r *redisAdapter) Del(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}

func (r *redisAdapter) Publish(ctx context.Context, channel, message string) error {
	return r.client.Publish(ctx, channel, message).Err()
}

func (r *redisAdapter) Subscribe(ctx context.Context, channel string) (Subscription, error) {
	ps := r.client.Subscribe(ctx, channel)
	// Wait for subscription confirmation so callers don't miss early messages.
	if _, err := ps.Receive(ctx); err != nil {
		ps.Close()
		return nil, err
	}
	out := make(chan string, 16)
	sub := &redisSubscription{ps: ps, out: out}
	go sub.forward()
	return sub, nil
}

func (r *redisAdapter) Close() error { return r.client.Close() }

type redisSubscription struct {
	ps   *goredis.PubSub
	out  chan string
	once sync.Once
}

func (s *redisSubscription) Channel() <-chan string {
	return s.out
}

func (s *redisSubscription) forward() {
	for msg := range s.ps.Channel() {
		select {
		case s.out <- msg.Payload:
		default: // drop on slow consumer
		}
	}
}

func (s *redisSubscription) Close() error {
	var err error
	s.once.Do(func() {
		err = s.ps.Close()
		close(s.out)
	})
	return err
}
```

- [ ] **Step 6: Add the dependency and run tests**

Run:
```bash
cd server
go get github.com/redis/go-redis/v9
go mod tidy
go test ./pkg/redis/ -run TestMemoryAdapter_Contract -v
```
Expected: PASS. The Redis contract test is skipped without `TEST_REDIS_ADDR`.

- [ ] **Step 7: Add Redis to local stack**

Edit `docker-compose.dev.yml` (create if missing; today `make start` uses a bare `pg_ctl` + `go run` combo). Add alongside the existing Postgres service:

```yaml
services:
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5
```

Update `Makefile` so `make start` sets `REDIS_URL=redis://localhost:6379` in the server env. Update CI workflow (`.github/workflows/ci.yml`) to run a Redis service container alongside Postgres so integration tests that set `TEST_REDIS_ADDR=localhost:6379` exercise the real adapter.

- [ ] **Step 8: Commit**

```bash
git add server/pkg/redis/ server/go.mod server/go.sum
git commit -m "feat(redis): add RedisAdapter baseline dependency (D4)

Memory + Redis implementations behind a common Adapter interface.
Memory impl used in single-process dev + tests; Redis impl used in
cloud-worker deploys for skills cache (Phase 3), stream resumption
(this phase), chat pub/sub (Phase 8A), semantic cache (Phase 9)."
```

---

### Task 3: System Prompt Builder

**Files:**
- Create: `server/pkg/agent/harness/prompt.go`
- Create: `server/pkg/agent/harness/prompt_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/harness/prompt_test.go
package harness

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt_IncludesCoreRules(t *testing.T) {
	t.Parallel()
	p := BuildSystemPrompt(PromptOptions{ModelID: "claude-sonnet-4-5"})
	if !strings.Contains(p, "task persistence") && !strings.Contains(p, "Task Persistence") {
		t.Error("core prompt missing task-persistence section")
	}
}

func TestBuildSystemPrompt_ClaudeOverlayEmphasisesTodos(t *testing.T) {
	t.Parallel()
	p := BuildSystemPrompt(PromptOptions{ModelID: "claude-sonnet-4-5"})
	if !strings.Contains(p, "todo") {
		t.Error("Claude overlay should reference todo tracking")
	}
}

func TestBuildSystemPrompt_GPTOverlayEmphasisesAutonomy(t *testing.T) {
	t.Parallel()
	p := BuildSystemPrompt(PromptOptions{ModelID: "gpt-4o"})
	if !strings.Contains(strings.ToLower(p), "iterate") {
		t.Error("GPT overlay should emphasise iteration until solved")
	}
}

func TestBuildSystemPrompt_CustomInstructionsAppended(t *testing.T) {
	t.Parallel()
	p := BuildSystemPrompt(PromptOptions{
		ModelID:            "claude-sonnet-4-5",
		CustomInstructions: "Always respond in formal tone.",
	})
	if !strings.Contains(p, "Always respond in formal tone.") {
		t.Error("custom instructions missing from prompt")
	}
}

func TestBuildSystemPrompt_EnvironmentDetails(t *testing.T) {
	t.Parallel()
	p := BuildSystemPrompt(PromptOptions{
		ModelID:            "claude-sonnet-4-5",
		EnvironmentDetails: "Agent type: customer_support. Workspace: ACME Corp.",
	})
	if !strings.Contains(p, "ACME Corp") {
		t.Error("environment details missing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/harness/ -run TestBuildSystemPrompt -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement the builder**

```go
// server/pkg/agent/harness/prompt.go
// Package harness adapts go-ai's ToolLoopAgent to Multica's Backend
// interface. This file assembles the system prompt from modular pieces,
// ported from Open Agents' system-prompt.ts (D5).
package harness

import (
	"strings"
)

// PromptOptions configures system-prompt assembly.
type PromptOptions struct {
	ModelID            string
	CustomInstructions string // workspace/agent-level additions (AGENTS.md, etc.)
	EnvironmentDetails string // runtime-only context: workspace name, agent type, budget remaining
}

// BuildSystemPrompt assembles the full system prompt. Deterministic; safe to
// cache at the adapter layer per (ModelID, CustomInstructions, EnvironmentDetails).
func BuildSystemPrompt(opts PromptOptions) string {
	var parts []string
	parts = append(parts, corePrompt)
	parts = append(parts, modelOverlay(opts.ModelID))
	if opts.EnvironmentDetails != "" {
		parts = append(parts, "\n# Environment\n\n"+opts.EnvironmentDetails)
	}
	if opts.CustomInstructions != "" {
		parts = append(parts, "\n# Workspace Instructions\n\n"+opts.CustomInstructions)
	}
	return strings.Join(parts, "\n")
}

const corePrompt = `You are a Multica agent — you complete tasks by planning, using tools, and iterating until the work is done.

# Role & Agency
You MUST complete assigned tasks end-to-end. Do not stop mid-task.
Only ask a human for input when genuinely blocked — not for confirmation.

# Task Persistence
You MUST iterate and keep going until the problem is solved.
- When you say "Next I will do X", you MUST actually do X.
- When you create a todo list, complete every item before finishing.

# Guardrails
- Reuse existing patterns before introducing new abstractions.
- No over-engineering. No speculative features.
- Never hardcode secrets; use the credential vault tools.

# Code Quality
- Match existing style. Read before writing.
- Prefer small, focused changes over broad refactors unless asked.
`

func modelOverlay(modelID string) string {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "claude"):
		return `
# Model-Specific Guidance
Use todo_write frequently to plan multi-step work. When you discover the
scope of a problem, immediately create a todo item for EACH issue.`
	case strings.Contains(id, "gpt"), strings.Contains(id, "openai"):
		return `
# Model-Specific Guidance
You MUST iterate and keep going until the problem is completely solved
before ending your turn. Never end your turn without having fully solved
the problem the user asked about.`
	case strings.Contains(id, "gemini"):
		return `
# Model-Specific Guidance
Be extremely concise. Keep responses under three lines unless the user
explicitly asks for more detail.`
	default:
		return `
# Model-Specific Guidance
Balance conciseness with helpfulness.`
	}
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/harness/ -v`
Expected: All 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/harness/prompt.go server/pkg/agent/harness/prompt_test.go
git commit -m "feat(harness): add system prompt builder

Modular assembly: core rules + model-family overlay + environment
details + workspace custom instructions. Ported from Open Agents
system-prompt.ts (D5)."
```

---

### Task 4: Harness Skeleton — go-ai `ToolLoopAgent` Adapter

**Files:**
- Create: `server/pkg/agent/harness/harness.go`
- Create: `server/pkg/agent/harness/harness_test.go`

**Before coding — verify against live go-ai source:**
- `AgentConfig` field names used here: `Model`, `System`, `Tools`, `Skills`, `Subagents`, `StopWhen`, `OnStepStartEvent`, `OnStepFinishEvent`, `OnToolCallStart`, `OnToolCallFinish`, `OnFinishEvent`, `Reasoning`. If go-ai renames any field between v0.4.0 and the version we end up pinning, update the `ac := goagent.AgentConfig{…}` literal.
- `provider.LanguageModel.SpecificationVersion()` return value: the fake in tests uses `"v1"`. Check go-ai's `pkg/provider/language_model.go` for the exact string the library expects. If go-ai validates this at runtime, non-matching values will fail tests with an opaque error. Update every `fakeModel.SpecificationVersion()` return in Tasks 4, 11, 13, 19 to match.

- [ ] **Step 1: Write the failing test**

Use a mock `provider.LanguageModel` to verify we wire up go-ai correctly without making real API calls.

```go
// server/pkg/agent/harness/harness_test.go
package harness

import (
	"context"
	"testing"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// fakeModel is a provider.LanguageModel that returns a canned response.
type fakeModel struct {
	provider string
	id       string
	response string
}

func (f *fakeModel) SpecificationVersion() string    { return "v1" }
func (f *fakeModel) Provider() string                { return f.provider }
func (f *fakeModel) ModelID() string                 { return f.id }
func (f *fakeModel) SupportsTools() bool             { return true }
func (f *fakeModel) SupportsStructuredOutput() bool  { return false }
func (f *fakeModel) SupportsImageInput() bool        { return false }

func (f *fakeModel) DoGenerate(_ context.Context, _ *provider.GenerateOptions) (*types.GenerateResult, error) {
	return &types.GenerateResult{
		Text:         f.response,
		FinishReason: types.FinishReasonStop,
	}, nil
}

func (f *fakeModel) DoStream(_ context.Context, _ *provider.GenerateOptions) (provider.TextStream, error) {
	return nil, nil // not used by this test
}

func TestHarness_ExecuteReturnsModelText(t *testing.T) {
	t.Parallel()

	h := New(Config{
		Model:        &fakeModel{provider: "anthropic", id: "claude-haiku", response: "hello from harness"},
		SystemPrompt: "Be brief.",
	})

	result, err := h.Execute(context.Background(), "Say hi.")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Text != "hello from harness" {
		t.Errorf("Text = %q, want %q", result.Text, "hello from harness")
	}
}

func TestHarness_UsesMainAgentMaxStepsDefault(t *testing.T) {
	t.Parallel()

	h := New(Config{Model: &fakeModel{provider: "anthropic", id: "claude-haiku"}})

	// Internal accessor for test — verifies StopWhen was wired with
	// StepCountIs(MainAgentMaxSteps) when no override provided.
	if h.maxSteps() != 50 {
		t.Errorf("maxSteps = %d, want 50 (MainAgentMaxSteps)", h.maxSteps())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/harness/ -run "TestHarness_" -v`
Expected: FAIL — types/functions not defined.

- [ ] **Step 3: Implement the harness**

```go
// server/pkg/agent/harness/harness.go
package harness

import (
	"context"
	"time"

	"github.com/digitallysavvy/go-ai/pkg/ai"
	goagent "github.com/digitallysavvy/go-ai/pkg/agent"
	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/provider/types"

	mcagent "github.com/multica-ai/multica/server/pkg/agent"
)

// Config is what callers pass in. All fields except Model are optional.
//
// Callback naming: we drop go-ai's "Event" suffix on the public side
// (OnStepFinish vs go-ai's OnStepFinishEvent). This is a deliberate trim
// for ergonomics — the mapping happens at a single boundary below. If
// you're grepping go-ai docs, the underlying fields are:
//   Config.OnStepFinish    → AgentConfig.OnStepFinishEvent
//   Config.OnToolCallStart → AgentConfig.OnToolCallStart
//   Config.OnFinish        → AgentConfig.OnFinishEvent
type Config struct {
	Model           provider.LanguageModel
	SystemPrompt    string
	Tools           []types.Tool
	MaxSteps        int      // zero = use defaults.MainAgentMaxSteps
	Reasoning       string   // zero-value = defaults.DefaultReasoningLevel
	Skills          *goagent.SkillRegistry
	Subagents       *goagent.SubagentRegistry
	OnStepFinish    func(ctx context.Context, e ai.OnStepFinishEvent)
	OnToolCallStart func(ctx context.Context, e ai.OnToolCallStartEvent)
	OnFinish        func(ctx context.Context, e ai.OnFinishEvent)
}

// Harness wraps a configured go-ai ToolLoopAgent. One Harness per execution
// — do not reuse across concurrent turns.
type Harness struct {
	agent    *goagent.ToolLoopAgent
	maxStepN int
}

// Result is what callers see after Execute. We surface just the fields we
// care about; the raw go-ai AgentResult is available via Inner.
type Result struct {
	Text         string
	FinishReason types.FinishReason
	Usage        types.Usage
	Inner        *goagent.AgentResult
}

// New constructs a Harness.
func New(cfg Config) *Harness {
	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = mcagent.MainAgentMaxSteps
	}

	ac := goagent.AgentConfig{
		Model:             cfg.Model,
		System:            cfg.SystemPrompt,
		Tools:             cfg.Tools,
		Skills:            cfg.Skills,
		Subagents:         cfg.Subagents,
		StopWhen:          []ai.StopCondition{ai.StepCountIs(maxSteps)},
		OnStepStartEvent:  nil,
		OnStepFinishEvent: cfg.OnStepFinish,
		OnToolCallStart:   cfg.OnToolCallStart,
		OnFinishEvent:     cfg.OnFinish,
	}
	return &Harness{agent: goagent.NewToolLoopAgent(ac), maxStepN: maxSteps}
}

// Execute runs a single agent turn.
func (h *Harness) Execute(ctx context.Context, prompt string) (*Result, error) {
	ar, err := h.agent.Execute(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return &Result{
		Text:         ar.Text,
		FinishReason: ar.FinishReason,
		Usage:        ar.Usage,
		Inner:        ar,
	}, nil
}

// ExecuteWithMessages runs a turn with explicit message history (for multi-turn
// conversations such as chat).
func (h *Harness) ExecuteWithMessages(ctx context.Context, messages []types.Message) (*Result, error) {
	ar, err := h.agent.ExecuteWithMessages(ctx, messages)
	if err != nil {
		return nil, err
	}
	return &Result{
		Text:         ar.Text,
		FinishReason: ar.FinishReason,
		Usage:        ar.Usage,
		Inner:        ar,
	}, nil
}

// maxSteps is a test-only accessor.
func (h *Harness) maxSteps() int { return h.maxStepN }

// Silence unused-import warnings on time if go-ai's API changes shape.
var _ = time.Now
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/harness/ -v`
Expected: Both new `TestHarness_*` tests PASS along with the prompt tests.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/harness/harness.go server/pkg/agent/harness/harness_test.go
git commit -m "feat(harness): add go-ai ToolLoopAgent adapter skeleton

Thin wrapper that wires in tools, skills, subagents, and callbacks.
Applies MainAgentMaxSteps from defaults.go as the stop condition.
Phase 2 follow-ups add tools, reasoning level, and LLM API backend."
```

---

### Task 5: Reasoning Level Plumbing

**Files:**
- Modify: `server/pkg/agent/harness/harness.go`
- Modify: `server/pkg/agent/harness/harness_test.go`

- [ ] **Step 1: Write the failing test**

Append to `server/pkg/agent/harness/harness_test.go`:

```go
func TestHarness_ReasoningDefaultsToConfigValue(t *testing.T) {
	t.Parallel()

	h := New(Config{
		Model:     &fakeModel{provider: "anthropic", id: "claude-sonnet-4-5"},
		Reasoning: "high",
	})
	if h.reasoning() != "high" {
		t.Errorf("reasoning = %q, want %q", h.reasoning(), "high")
	}
}

func TestHarness_ReasoningFallsBackToDefault(t *testing.T) {
	t.Parallel()

	h := New(Config{Model: &fakeModel{provider: "anthropic", id: "claude-sonnet-4-5"}})
	if h.reasoning() != "default" {
		t.Errorf("reasoning = %q, want %q (DefaultReasoningLevel)", h.reasoning(), "default")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/harness/ -run TestHarness_Reasoning -v`
Expected: FAIL — `reasoning()` not defined.

- [ ] **Step 3: Thread reasoning through Harness**

`AgentConfig.Reasoning` is a `*types.ReasoningLevel` (string-backed type from `go-ai/pkg/provider/types`). We cast our string through that type before taking its address.

In `harness.go`, extend the struct and `New`:

```go
// Add to imports at the top of harness.go:
// "github.com/digitallysavvy/go-ai/pkg/provider/types"

type Harness struct {
	agent      *goagent.ToolLoopAgent
	maxStepN   int
	reasoningV string
}

// In New(cfg Config) *Harness, add after maxSteps resolution and BEFORE
// the NewToolLoopAgent call:
	reasoning := cfg.Reasoning
	if reasoning == "" {
		reasoning = mcagent.DefaultReasoningLevel
	}
	lvl := types.ReasoningLevel(reasoning)
	ac.Reasoning = &lvl

	return &Harness{agent: goagent.NewToolLoopAgent(ac), maxStepN: maxSteps, reasoningV: reasoning}

// After the maxSteps accessor add:
func (h *Harness) reasoning() string { return h.reasoningV }
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/harness/ -v`
Expected: All harness tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/harness/harness.go server/pkg/agent/harness/harness_test.go
git commit -m "feat(harness): plumb reasoning level through to go-ai

Callers can pass Config.Reasoning (low/medium/high/etc). Empty values
resolve to defaults.DefaultReasoningLevel which maps to go-ai's
ReasoningDefault (provider-chosen)."
```

---

### Task 5.5: Credential Pool Service (production implementation)

**Files:**
- Create: `server/pkg/agent/credentials_service.go`
- Create: `server/pkg/agent/credentials_service_test.go`

**Why this task exists:** Phase 1 Task 10 created the *rotation algorithm* (`pickCredential` over `[]CredentialEntry`) but not a live service. Phase 2 Task 7 (Credential Rotation Wrapper) depends on a `CredentialPool` interface that reads `workspace_credential`, decrypts values, returns an API key, and records cooldowns on rate-limit events. That service is built here so the rotation wrapper has something real to talk to.

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/credentials_service_test.go
package agent

import (
	"context"
	"testing"
	"time"
)

// fakeQueries captures just the sqlc methods the service touches. Production
// code passes a *db.Queries directly.
type fakeQueries struct {
	active []credRow
	cooldownSet map[string]time.Time
}

type credRow struct {
	ID             string
	EncryptedValue []byte
	UsageCount     int
	CooldownUntil  *time.Time
}

func (f *fakeQueries) ListActiveProviderCredentials(_ context.Context, _ string, _ string) ([]credRow, error) {
	out := []credRow{}
	for _, c := range f.active {
		if c.CooldownUntil == nil || c.CooldownUntil.Before(time.Now()) {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakeQueries) UpdateCredentialCooldown(_ context.Context, id string, until time.Time) error {
	if f.cooldownSet == nil {
		f.cooldownSet = map[string]time.Time{}
	}
	f.cooldownSet[id] = until
	return nil
}
func (f *fakeQueries) IncrementCredentialUsage(_ context.Context, _ string) error { return nil }

// stubDecrypter is a trivial decrypter for tests.
type stubDecrypter struct{}
func (stubDecrypter) Decrypt(workspaceID string, encrypted []byte) ([]byte, error) {
	return encrypted, nil // tests pass plain bytes
}

func TestCredentialService_NextReturnsLeastUsed(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{active: []credRow{
		{ID: "c1", EncryptedValue: []byte("key-1"), UsageCount: 10},
		{ID: "c2", EncryptedValue: []byte("key-2"), UsageCount: 2},
		{ID: "c3", EncryptedValue: []byte("key-3"), UsageCount: 7},
	}}
	svc := NewCredentialService(q, stubDecrypter{}, "ws1")

	credID, apiKey, err := svc.NextCredential(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("NextCredential: %v", err)
	}
	if credID != "c2" {
		t.Errorf("credID = %q, want c2 (least used)", credID)
	}
	if apiKey != "key-2" {
		t.Errorf("apiKey = %q", apiKey)
	}
}

func TestCredentialService_RecordRateLimitSetsCooldown(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{active: []credRow{{ID: "c1", EncryptedValue: []byte("k")}}}
	svc := NewCredentialService(q, stubDecrypter{}, "ws1")

	svc.RecordRateLimit(context.Background(), "c1")
	until, ok := q.cooldownSet["c1"]
	if !ok {
		t.Fatal("cooldown not set")
	}
	if time.Until(until) < 30*time.Second {
		t.Errorf("cooldown too short: %v", time.Until(until))
	}
}

func TestCredentialService_Exhausted_ReturnsError(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{active: nil}
	svc := NewCredentialService(q, stubDecrypter{}, "ws1")

	_, _, err := svc.NextCredential(context.Background(), "anthropic")
	if err == nil {
		t.Error("expected error when no credentials available")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestCredentialService -v`
Expected: FAIL — types/functions undefined.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/credentials_service.go
package agent

import (
	"context"
	"fmt"
	"time"
)

// CredentialQueries is the sqlc-generated surface this service uses. In
// production wire up *db.Queries; tests use fakeQueries.
type CredentialQueries interface {
	ListActiveProviderCredentials(ctx context.Context, workspaceID string, provider string) ([]credRow, error)
	UpdateCredentialCooldown(ctx context.Context, credID string, until time.Time) error
	IncrementCredentialUsage(ctx context.Context, credID string) error
}

// Decrypter turns a stored encrypted_value (AES-256-GCM) into a raw API key.
// Production impl uses the per-workspace derived key from Phase 1.4; tests
// pass a stub that returns the input unchanged.
type Decrypter interface {
	Decrypt(workspaceID string, encrypted []byte) ([]byte, error)
}

// CredentialService is the live CredentialPool implementation used by
// RotatingModel (Task 7). Strategy is fixed to least-used for now; richer
// strategies (round-robin, random) can be added when we ship the admin UI.
type CredentialService struct {
	q           CredentialQueries
	dec         Decrypter
	workspaceID string
	cooldown    time.Duration // default 5 minutes
}

func NewCredentialService(q CredentialQueries, dec Decrypter, workspaceID string) *CredentialService {
	return &CredentialService{q: q, dec: dec, workspaceID: workspaceID, cooldown: 5 * time.Minute}
}

// NextCredential returns the least-used active, non-cooldown credential for
// the given LLM provider. Increments usage so the next call picks a different
// key when they're tied.
func (s *CredentialService) NextCredential(ctx context.Context, providerName string) (credentialID, apiKey string, err error) {
	rows, err := s.q.ListActiveProviderCredentials(ctx, s.workspaceID, providerName)
	if err != nil {
		return "", "", fmt.Errorf("list credentials: %w", err)
	}
	if len(rows) == 0 {
		return "", "", fmt.Errorf("no active %s credentials for workspace %s", providerName, s.workspaceID)
	}

	best := rows[0]
	for _, r := range rows[1:] {
		if r.UsageCount < best.UsageCount {
			best = r
		}
	}

	plain, err := s.dec.Decrypt(s.workspaceID, best.EncryptedValue)
	if err != nil {
		return "", "", fmt.Errorf("decrypt: %w", err)
	}

	_ = s.q.IncrementCredentialUsage(ctx, best.ID) // best-effort; failure here shouldn't block the call
	return best.ID, string(plain), nil
}

// RecordRateLimit marks a credential as cooling down so the next
// NextCredential call skips it. Uses the service's configured cooldown
// (default 5 minutes) — enough to let a per-minute bucket reset.
func (s *CredentialService) RecordRateLimit(ctx context.Context, credentialID string) {
	until := time.Now().Add(s.cooldown)
	_ = s.q.UpdateCredentialCooldown(ctx, credentialID, until)
}
```

**Note:** the `CredentialQueries` interface declared above references a local `credRow` type. Keep this type in sync with whatever shape the sqlc-generated `ListActiveProviderCredentials` returns. If Phase 1 Task 8 (SQLC queries) already generated a differently-shaped struct (e.g. `db.ListActiveProviderCredentialsRow`), replace `[]credRow` with that exact type and drop the local alias.

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/ -run TestCredentialService -v`
Expected: All 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/credentials_service.go server/pkg/agent/credentials_service_test.go
git commit -m "feat(agent): add CredentialPool production service

Reads workspace_credential, picks least-used active key, decrypts via
injected Decrypter, increments usage counter. RecordRateLimit sets a
5-minute cooldown. Interface shape (CredentialPool) lives in
credential_rotation.go; this file provides the live impl that Task 7
wraps as RotatingModel."
```

---

### Task 6: Tool Registry

**Files:**
- Create: `server/pkg/tools/registry.go`
- Create: `server/pkg/tools/registry_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/tools/registry_test.go
package tools

import (
	"context"
	"testing"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

func makeTool(name string) types.Tool {
	return types.Tool{
		Name:        name,
		Description: "test tool " + name,
		Parameters:  map[string]any{"type": "object"},
		Execute: func(_ context.Context, _ map[string]any, _ types.ToolExecutionOptions) (any, error) {
			return name + "-result", nil
		},
	}
}

func TestRegistry_ForAgent_ReturnsAllEnabled(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(makeTool("http_request"))
	r.Register(makeTool("search"))

	got, err := r.ForAgent(context.Background(), "ws1", "agent1", nil)
	if err != nil {
		t.Fatalf("ForAgent: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestRegistry_ForAgent_RespectsAllowlist(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(makeTool("http_request"))
	r.Register(makeTool("search"))
	r.Register(makeTool("send_email"))

	got, err := r.ForAgent(context.Background(), "ws1", "agent1", []string{"search"})
	if err != nil {
		t.Fatalf("ForAgent: %v", err)
	}
	if len(got) != 1 || got[0].Name != "search" {
		t.Errorf("allowlist filter failed: %+v", got)
	}
}

func TestRegistry_Register_PanicsOnDuplicate(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(makeTool("dup"))
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	r.Register(makeTool("dup"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/tools/ -v`
Expected: FAIL — package missing.

- [ ] **Step 3: Implement**

```go
// server/pkg/tools/registry.go
// Package tools resolves the set of go-ai types.Tool an agent gets per call.
// Tool definitions are built-in (registered at startup) plus MCP-discovered
// (Phase 3). Availability gating lets workspace/agent policy trim the set.
package tools

import (
	"context"
	"fmt"
	"sync"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// Registry is concurrent-safe. One per process; populated at worker startup.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]types.Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]types.Tool)}
}

// Register adds a tool. Panics on duplicate names — tool names are a global
// namespace the LLM sees and silent shadowing would be impossible to debug.
func (r *Registry) Register(t types.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name]; exists {
		panic(fmt.Sprintf("tools.Registry: duplicate tool %q", t.Name))
	}
	r.tools[t.Name] = t
}

// ForAgent returns the tools an agent should see this turn. allowlist, when
// non-nil, restricts the set; nil means "all registered tools". Returned
// slice is a fresh copy — safe to mutate.
func (r *Registry) ForAgent(_ context.Context, _ /*workspaceID*/, _ /*agentID*/ string, allowlist []string) ([]types.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if allowlist == nil {
		out := make([]types.Tool, 0, len(r.tools))
		for _, t := range r.tools {
			out = append(out, t)
		}
		return out, nil
	}

	out := make([]types.Tool, 0, len(allowlist))
	for _, name := range allowlist {
		t, ok := r.tools[name]
		if !ok {
			return nil, fmt.Errorf("tools.Registry: agent references unknown tool %q", name)
		}
		out = append(out, t)
	}
	return out, nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/tools/ -v`
Expected: All 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/tools/registry.go server/pkg/tools/registry_test.go
git commit -m "feat(tools): add workspace-scoped tool registry

Resolves []types.Tool per agent turn with optional allowlist gating.
Built-in tools register at startup; MCP-discovered tools (Phase 3)
merge in at task claim time."
```

---

### Task 7: Built-in Tool — `http_request`

**Files:**
- Create: `server/pkg/tools/builtin/http_request.go`
- Create: `server/pkg/tools/builtin/http_request_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/tools/builtin/http_request_test.go
package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

func TestHTTPRequest_GET_ReturnsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	tool := HTTPRequest(HTTPOptions{})
	out, err := tool.Execute(context.Background(), map[string]any{
		"url":    srv.URL,
		"method": "GET",
	}, types.ToolExecutionOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m := out.(map[string]any)
	if m["status"].(int) != 200 {
		t.Errorf("status = %v, want 200", m["status"])
	}
	if m["body"].(string) != "hello" {
		t.Errorf("body = %v, want hello", m["body"])
	}
}

func TestHTTPRequest_POST_SendsJSONBody(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(201)
	}))
	defer srv.Close()

	tool := HTTPRequest(HTTPOptions{})
	_, err := tool.Execute(context.Background(), map[string]any{
		"url":    srv.URL,
		"method": "POST",
		"body":   `{"name":"alice"}`,
		"headers": map[string]any{"Content-Type": "application/json"},
	}, types.ToolExecutionOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotBody["name"] != "alice" {
		t.Errorf("server got %v, want alice", gotBody["name"])
	}
}

func TestHTTPRequest_TruncatesLargeBodies(t *testing.T) {
	big := make([]byte, 30_000)
	for i := range big {
		big[i] = 'a'
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(big)
	}))
	defer srv.Close()

	tool := HTTPRequest(HTTPOptions{MaxBodyBytes: 20_000})
	out, _ := tool.Execute(context.Background(), map[string]any{"url": srv.URL, "method": "GET"}, types.ToolExecutionOptions{})
	m := out.(map[string]any)
	if len(m["body"].(string)) != 20_000 {
		t.Errorf("body length = %d, want 20000 (truncated)", len(m["body"].(string)))
	}
	if m["truncated"] != true {
		t.Error("truncated flag not set")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/tools/builtin/ -run TestHTTPRequest -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/tools/builtin/http_request.go
// Package builtin provides built-in tools registered at worker startup.
// Each factory returns a fresh types.Tool — no shared mutable state.
package builtin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// HTTPOptions configures the http_request tool factory.
type HTTPOptions struct {
	Timeout       time.Duration // default 30s
	MaxBodyBytes  int           // default 20000
	AllowedHosts  []string      // nil = allow all; non-nil = strict allowlist
}

// HTTPRequest returns a types.Tool that performs a one-shot HTTP request.
func HTTPRequest(opts HTTPOptions) types.Tool {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxBodyBytes == 0 {
		opts.MaxBodyBytes = 20_000
	}
	client := &http.Client{Timeout: opts.Timeout}

	return types.Tool{
		Name:        "http_request",
		Description: "Make an HTTP request to an external URL. Returns status, headers, and body (truncated if large).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":     map[string]any{"type": "string", "description": "Absolute URL"},
				"method":  map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"}, "description": "HTTP method"},
				"headers": map[string]any{"type": "object", "description": "Request headers as key-value pairs"},
				"body":    map[string]any{"type": "string", "description": "Request body (usually JSON)"},
			},
			"required": []string{"url", "method"},
		},
		Execute: func(ctx context.Context, input map[string]any, _ types.ToolExecutionOptions) (any, error) {
			url, _ := input["url"].(string)
			method, _ := input["method"].(string)
			if url == "" || method == "" {
				return nil, fmt.Errorf("url and method are required")
			}
			if opts.AllowedHosts != nil {
				allowed := false
				for _, h := range opts.AllowedHosts {
					if bytes.Contains([]byte(url), []byte(h)) {
						allowed = true
						break
					}
				}
				if !allowed {
					return nil, fmt.Errorf("host not in allowlist")
				}
			}

			var body io.Reader
			if b, ok := input["body"].(string); ok && b != "" {
				body = bytes.NewBufferString(b)
			}

			req, err := http.NewRequestWithContext(ctx, method, url, body)
			if err != nil {
				return nil, err
			}
			if hs, ok := input["headers"].(map[string]any); ok {
				for k, v := range hs {
					if sv, ok := v.(string); ok {
						req.Header.Set(k, sv)
					}
				}
			}
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()

			raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(opts.MaxBodyBytes)+1))
			if err != nil {
				return nil, err
			}
			truncated := len(raw) > opts.MaxBodyBytes
			if truncated {
				raw = raw[:opts.MaxBodyBytes]
			}
			headers := map[string]string{}
			for k := range resp.Header {
				headers[k] = resp.Header.Get(k)
			}
			return map[string]any{
				"status":    resp.StatusCode,
				"headers":   headers,
				"body":      string(raw),
				"truncated": truncated,
			}, nil
		},
	}
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/tools/builtin/ -v`
Expected: All 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/tools/builtin/http_request.go server/pkg/tools/builtin/http_request_test.go
git commit -m "feat(tools): add http_request built-in tool

Configurable timeout, body size cap (default 20KB), optional host
allowlist for tenant isolation. Returns status, headers, body,
truncated flag."
```

---

### Task 8: Built-in Tool — `search` (Brave / Serper pluggable)

**Files:**
- Create: `server/pkg/tools/builtin/search.go`
- Create: `server/pkg/tools/builtin/search_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/tools/builtin/search_test.go
package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

func TestSearch_BraveBackend_ReturnsResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"web": map[string]any{
				"results": []map[string]any{
					{"title": "Doc A", "url": "https://a.example", "description": "About A"},
					{"title": "Doc B", "url": "https://b.example", "description": "About B"},
				},
			},
		})
	}))
	defer srv.Close()

	tool := Search(SearchOptions{Backend: BackendBrave, APIKey: "test", BaseURL: srv.URL})
	out, err := tool.Execute(context.Background(), map[string]any{"query": "multica"}, types.ToolExecutionOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	results := out.(map[string]any)["results"].([]map[string]any)
	if len(results) != 2 {
		t.Errorf("len = %d, want 2", len(results))
	}
	if results[0]["title"] != "Doc A" {
		t.Errorf("results[0].title = %v", results[0]["title"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/tools/builtin/ -run TestSearch -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/tools/builtin/search.go
package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

type SearchBackend string

const (
	BackendBrave  SearchBackend = "brave"
	BackendSerper SearchBackend = "serper"
)

type SearchOptions struct {
	Backend  SearchBackend
	APIKey   string
	BaseURL  string        // override for tests; empty = provider default
	Timeout  time.Duration // default 15s
	MaxItems int           // default 10
}

// Search returns a types.Tool for web search. Backend-agnostic on the LLM
// side; result shape is always {results: [{title, url, description}]}.
func Search(opts SearchOptions) types.Tool {
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.MaxItems == 0 {
		opts.MaxItems = 10
	}
	client := &http.Client{Timeout: opts.Timeout}

	return types.Tool{
		Name:        "search",
		Description: "Search the web. Returns a list of results with title, URL, and snippet.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, input map[string]any, _ types.ToolExecutionOptions) (any, error) {
			q, _ := input["query"].(string)
			if q == "" {
				return nil, fmt.Errorf("query is required")
			}

			switch opts.Backend {
			case BackendBrave:
				return braveSearch(ctx, client, opts, q)
			case BackendSerper:
				return serperSearch(ctx, client, opts, q)
			default:
				return nil, fmt.Errorf("unknown search backend %q", opts.Backend)
			}
		},
	}
}

func braveSearch(ctx context.Context, client *http.Client, opts SearchOptions, q string) (any, error) {
	base := opts.BaseURL
	if base == "" {
		base = "https://api.search.brave.com/res/v1/web/search"
	}
	u := base + "?q=" + url.QueryEscape(q)
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("X-Subscription-Token", opts.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(parsed.Web.Results))
	for i, r := range parsed.Web.Results {
		if i >= opts.MaxItems {
			break
		}
		out = append(out, map[string]any{
			"title":       r.Title,
			"url":         r.URL,
			"description": r.Description,
		})
	}
	return map[string]any{"results": out}, nil
}

func serperSearch(ctx context.Context, client *http.Client, opts SearchOptions, q string) (any, error) {
	base := opts.BaseURL
	if base == "" {
		base = "https://google.serper.dev/search"
	}
	body, _ := json.Marshal(map[string]any{"q": q, "num": opts.MaxItems})
	req, _ := http.NewRequestWithContext(ctx, "POST", base, bytes.NewReader(body))
	req.Header.Set("X-API-KEY", opts.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(parsed.Organic))
	for i, r := range parsed.Organic {
		if i >= opts.MaxItems {
			break
		}
		out = append(out, map[string]any{
			"title":       r.Title,
			"url":         r.Link,
			"description": r.Snippet,
		})
	}
	return map[string]any{"results": out}, nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/tools/builtin/ -v`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/tools/builtin/search.go server/pkg/tools/builtin/search_test.go
git commit -m "feat(tools): add search built-in (Brave + Serper backends)

Uniform output shape {results: [{title, url, description}]} regardless
of backend. BaseURL override for test isolation. API key passed via
options, ultimately sourced from workspace_credential in Phase 2
wiring."
```

---

### Task 9: Built-in Tool — `send_email` (SMTP + Resend)

**Files:**
- Create: `server/pkg/tools/builtin/email.go`
- Create: `server/pkg/tools/builtin/email_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/tools/builtin/email_test.go
package builtin

import (
	"context"
	"errors"
	"testing"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

type fakeMailer struct {
	called  bool
	gotTo   string
	gotBody string
	err     error
}

func (f *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	f.called = true
	f.gotTo = to
	f.gotBody = body
	return f.err
}

func TestSendEmail_CallsMailer(t *testing.T) {
	t.Parallel()
	m := &fakeMailer{}
	tool := SendEmail(EmailOptions{Mailer: m})

	_, err := tool.Execute(context.Background(), map[string]any{
		"to":      "user@example.com",
		"subject": "Hi",
		"body":    "Hello there",
	}, types.ToolExecutionOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !m.called {
		t.Fatal("mailer not called")
	}
	if m.gotTo != "user@example.com" || m.gotBody != "Hello there" {
		t.Errorf("mailer saw to=%q body=%q", m.gotTo, m.gotBody)
	}
}

func TestSendEmail_SurfacesMailerError(t *testing.T) {
	t.Parallel()
	m := &fakeMailer{err: errors.New("smtp 550")}
	tool := SendEmail(EmailOptions{Mailer: m})

	_, err := tool.Execute(context.Background(), map[string]any{
		"to": "x@y.z", "subject": "s", "body": "b",
	}, types.ToolExecutionOptions{})
	if err == nil {
		t.Fatal("expected error from mailer")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/tools/builtin/ -run TestSendEmail -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/tools/builtin/email.go
package builtin

import (
	"context"
	"fmt"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// Mailer is the minimal interface the tool needs. Production wiring plugs in
// Resend, SMTP, or SES implementations from server/internal/mail/.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

type EmailOptions struct {
	Mailer Mailer
}

// SendEmail returns a types.Tool for sending a single email. Body is plain
// text; HTML bodies can be added in a follow-up when a use case demands it.
func SendEmail(opts EmailOptions) types.Tool {
	if opts.Mailer == nil {
		panic("SendEmail: Mailer is required")
	}
	return types.Tool{
		Name:        "send_email",
		Description: "Send a plain-text email to a single recipient.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":      map[string]any{"type": "string", "description": "Recipient email address"},
				"subject": map[string]any{"type": "string", "description": "Email subject"},
				"body":    map[string]any{"type": "string", "description": "Plain-text body"},
			},
			"required": []string{"to", "subject", "body"},
		},
		Execute: func(ctx context.Context, input map[string]any, _ types.ToolExecutionOptions) (any, error) {
			to, _ := input["to"].(string)
			subject, _ := input["subject"].(string)
			body, _ := input["body"].(string)
			if to == "" || subject == "" || body == "" {
				return nil, fmt.Errorf("to, subject, body are all required")
			}
			if err := opts.Mailer.Send(ctx, to, subject, body); err != nil {
				return nil, err
			}
			return map[string]any{"sent": true, "to": to}, nil
		},
	}
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/tools/builtin/ -v`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/tools/builtin/email.go server/pkg/tools/builtin/email_test.go
git commit -m "feat(tools): add send_email built-in

Mailer interface is dependency-injected (Resend/SMTP/SES impls live
in server/internal/mail/). Plain-text single-recipient for v1;
HTML + multi-recipient can land when a real use case shows up."
```

---

### Task 10: Built-in Tool — `database_query`

**Files:**
- Create: `server/pkg/tools/builtin/database_query.go`
- Create: `server/pkg/tools/builtin/database_query_test.go`

- [ ] **Step 1: Write the failing test**

Use `DATA-DOG/go-sqlmock` or a similar lightweight stand-in. For a zero-dep test, hand-roll a `QueryRunner` fake:

```go
// server/pkg/tools/builtin/database_query_test.go
package builtin

import (
	"context"
	"errors"
	"testing"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

type fakeRunner struct {
	gotQuery string
	gotArgs  []any
	rows     []map[string]any
	err      error
}

func (f *fakeRunner) Query(_ context.Context, q string, args ...any) ([]map[string]any, error) {
	f.gotQuery = q
	f.gotArgs = args
	return f.rows, f.err
}

func TestDatabaseQuery_ReturnsRows(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{rows: []map[string]any{
		{"id": 1, "name": "alice"},
		{"id": 2, "name": "bob"},
	}}
	tool := DatabaseQuery(DatabaseOptions{Runner: r})

	out, err := tool.Execute(context.Background(), map[string]any{
		"query": "SELECT id, name FROM users WHERE active = $1",
		"args":  []any{true},
	}, types.ToolExecutionOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rows := out.(map[string]any)["rows"].([]map[string]any)
	if len(rows) != 2 || rows[0]["name"] != "alice" {
		t.Errorf("rows = %+v", rows)
	}
	if r.gotArgs[0] != true {
		t.Errorf("args not forwarded: %+v", r.gotArgs)
	}
}

func TestDatabaseQuery_RejectsDDL(t *testing.T) {
	t.Parallel()
	tool := DatabaseQuery(DatabaseOptions{Runner: &fakeRunner{}, ReadOnly: true})
	_, err := tool.Execute(context.Background(), map[string]any{"query": "DROP TABLE users"}, types.ToolExecutionOptions{})
	if err == nil {
		t.Fatal("expected ReadOnly guard to reject DROP TABLE")
	}
}

func TestDatabaseQuery_SurfacesRunnerError(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{err: errors.New("connection refused")}
	tool := DatabaseQuery(DatabaseOptions{Runner: r})
	_, err := tool.Execute(context.Background(), map[string]any{"query": "SELECT 1"}, types.ToolExecutionOptions{})
	if err == nil {
		t.Fatal("expected runner error surfaced")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/tools/builtin/ -run TestDatabaseQuery -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/tools/builtin/database_query.go
package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// QueryRunner is the minimal interface the tool needs. Production wiring
// uses a wrapper over pgxpool that scopes by workspace credential. Tests
// pass a fake that records the query + args.
type QueryRunner interface {
	Query(ctx context.Context, q string, args ...any) ([]map[string]any, error)
}

type DatabaseOptions struct {
	Runner   QueryRunner
	ReadOnly bool // reject INSERT/UPDATE/DELETE/DROP/ALTER/TRUNCATE
	MaxRows  int  // default 100
}

// DatabaseQuery returns a types.Tool for querying an external database.
func DatabaseQuery(opts DatabaseOptions) types.Tool {
	if opts.Runner == nil {
		panic("DatabaseQuery: Runner is required")
	}
	if opts.MaxRows == 0 {
		opts.MaxRows = 100
	}
	return types.Tool{
		Name:        "database_query",
		Description: "Run a parameterised SQL query against the configured external database. Read-only by default.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "SQL with $1, $2, … placeholders"},
				"args":  map[string]any{"type": "array", "description": "Parameter values in order"},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, input map[string]any, _ types.ToolExecutionOptions) (any, error) {
			q, _ := input["query"].(string)
			if q == "" {
				return nil, fmt.Errorf("query is required")
			}
			if opts.ReadOnly && hasMutatingKeyword(q) {
				return nil, fmt.Errorf("database_query is read-only; rejected mutating statement")
			}
			var args []any
			if a, ok := input["args"].([]any); ok {
				args = a
			}

			rows, err := opts.Runner.Query(ctx, q, args...)
			if err != nil {
				return nil, err
			}
			truncated := false
			if len(rows) > opts.MaxRows {
				rows = rows[:opts.MaxRows]
				truncated = true
			}
			return map[string]any{"rows": rows, "count": len(rows), "truncated": truncated}, nil
		},
	}
}

var mutatingKeywords = []string{"insert", "update", "delete", "drop", "alter", "truncate", "create"}

func hasMutatingKeyword(q string) bool {
	lower := strings.ToLower(strings.TrimSpace(q))
	for _, kw := range mutatingKeywords {
		if strings.HasPrefix(lower, kw) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/tools/builtin/ -v`
Expected: All 9 tests PASS across the four builtin tools.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/tools/builtin/database_query.go server/pkg/tools/builtin/database_query_test.go
git commit -m "feat(tools): add database_query built-in

Parameterised queries via a QueryRunner interface (pgxpool in prod,
fakes in tests). ReadOnly flag rejects mutating statements via
first-keyword check. Result rows capped at MaxRows with truncated
flag."
```

---

### Task 11: Credential Rotation Wrapper

**Files:**
- Create: `server/pkg/agent/credential_rotation.go`
- Create: `server/pkg/agent/credential_rotation_test.go`

**Goal:** Given a credential pool (from Phase 1.4) and a factory that builds a `provider.LanguageModel` from an API key, produce a `provider.LanguageModel` that automatically rotates to the next available credential on rate-limit errors.

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/credential_rotation_test.go
package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// pool with 2 credentials; second one succeeds after first gets rate-limited.
type fakePool struct {
	credIDs []string
	cursor  atomic.Int64
}

func (p *fakePool) NextCredential(_ context.Context, _ /*provider*/ string) (credID, apiKey string, err error) {
	i := p.cursor.Load()
	if int(i) >= len(p.credIDs) {
		return "", "", errors.New("exhausted")
	}
	p.cursor.Add(1)
	return p.credIDs[i], "key-" + p.credIDs[i], nil
}

func (p *fakePool) RecordRateLimit(_ context.Context, _ string) {}

// A model that fails once with rate-limit then succeeds.
type rateLimitThenOKModel struct {
	calls atomic.Int32
	apiKey string
}

func (m *rateLimitThenOKModel) SpecificationVersion() string   { return "v1" }
func (m *rateLimitThenOKModel) Provider() string               { return "anthropic" }
func (m *rateLimitThenOKModel) ModelID() string                { return "claude-haiku" }
func (m *rateLimitThenOKModel) SupportsTools() bool            { return true }
func (m *rateLimitThenOKModel) SupportsStructuredOutput() bool { return false }
func (m *rateLimitThenOKModel) SupportsImageInput() bool       { return false }
func (m *rateLimitThenOKModel) DoGenerate(_ context.Context, _ *provider.GenerateOptions) (*types.GenerateResult, error) {
	n := m.calls.Add(1)
	if n == 1 {
		return nil, &APIError{StatusCode: 429, Message: "rate limited"}
	}
	return &types.GenerateResult{Text: "ok with " + m.apiKey, FinishReason: types.FinishReasonStop}, nil
}
func (m *rateLimitThenOKModel) DoStream(_ context.Context, _ *provider.GenerateOptions) (provider.TextStream, error) {
	return nil, nil
}

func TestCredentialRotation_RotatesOnRateLimit(t *testing.T) {
	t.Parallel()
	pool := &fakePool{credIDs: []string{"c1", "c2"}}

	var built []string
	factory := func(apiKey string) provider.LanguageModel {
		built = append(built, apiKey)
		return &rateLimitThenOKModel{apiKey: apiKey}
	}

	wrapped := NewRotatingModel(pool, "anthropic", factory)

	res, err := wrapped.DoGenerate(context.Background(), &provider.GenerateOptions{})
	if err != nil {
		t.Fatalf("DoGenerate: %v", err)
	}
	if res.Text != "ok with key-c2" {
		t.Errorf("Text = %q, want rotated result", res.Text)
	}
	if len(built) != 2 {
		t.Errorf("factory called %d times, want 2", len(built))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestCredentialRotation -v`
Expected: FAIL — types undefined.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/credential_rotation.go
package agent

import (
	"context"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// CredentialPool is the subset of the workspace_credential service the
// rotation wrapper needs. Live implementation: CredentialService (Task 5.5).
type CredentialPool interface {
	NextCredential(ctx context.Context, providerName string) (credentialID, apiKey string, err error)
	RecordRateLimit(ctx context.Context, credentialID string)
}

// ModelFactory builds a fresh provider.LanguageModel bound to the given API key.
type ModelFactory func(apiKey string) provider.LanguageModel

// RotatingModel wraps a provider.LanguageModel with automatic credential
// rotation on rate-limit classified errors (Phase 1.3). A call tries up to
// len(pool) credentials before surfacing the last error.
type RotatingModel struct {
	pool     CredentialPool
	provider string
	factory  ModelFactory
	maxTries int
}

// NewRotatingModel constructs a RotatingModel. maxTries defaults to 3 when zero.
func NewRotatingModel(pool CredentialPool, providerName string, factory ModelFactory) *RotatingModel {
	return &RotatingModel{pool: pool, provider: providerName, factory: factory, maxTries: 3}
}

func (r *RotatingModel) SpecificationVersion() string    { return "v1" }
func (r *RotatingModel) Provider() string                { return r.provider }
func (r *RotatingModel) ModelID() string                 { return "" }
func (r *RotatingModel) SupportsTools() bool             { return true }
func (r *RotatingModel) SupportsStructuredOutput() bool  { return true }
func (r *RotatingModel) SupportsImageInput() bool        { return true }

func (r *RotatingModel) DoGenerate(ctx context.Context, opts *provider.GenerateOptions) (*types.GenerateResult, error) {
	var lastErr error
	for attempt := 0; attempt < r.maxTries; attempt++ {
		credID, key, err := r.pool.NextCredential(ctx, r.provider)
		if err != nil {
			return nil, err
		}
		m := r.factory(key)
		res, err := m.DoGenerate(ctx, opts)
		if err == nil {
			return res, nil
		}
		class := ClassifyError(err)
		if class.ShouldRotateCredential {
			r.pool.RecordRateLimit(ctx, credID)
			lastErr = err
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

func (r *RotatingModel) DoStream(ctx context.Context, opts *provider.GenerateOptions) (provider.TextStream, error) {
	// Streaming rotation is trickier — rate-limit can arrive mid-stream after
	// tokens already flowed downstream. For v1: try once, surface errors. A
	// follow-up can layer buffer-and-retry once we see real demand.
	credID, key, err := r.pool.NextCredential(ctx, r.provider)
	if err != nil {
		return nil, err
	}
	stream, err := r.factory(key).DoStream(ctx, opts)
	if err != nil {
		if ClassifyError(err).ShouldRotateCredential {
			r.pool.RecordRateLimit(ctx, credID)
		}
		return nil, err
	}
	return stream, nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/ -run TestCredentialRotation -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/credential_rotation.go server/pkg/agent/credential_rotation_test.go
git commit -m "feat(agent): wrap LanguageModel with credential rotation

RotatingModel rotates API keys on rate-limit errors classified via
Phase 1.3 ClassifyError. Up to 3 attempts; exhausted pool surfaces
the last error. DoStream rotates only on pre-stream failures for v1."
```

---

### Task 12: Provider Health Tracker

**Files:**
- Create: `server/pkg/agent/provider_health.go`
- Create: `server/pkg/agent/provider_health_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/provider_health_test.go
package agent

import (
	"testing"
	"time"
)

func TestProviderHealth_StartsHealthy(t *testing.T) {
	t.Parallel()
	h := NewProviderHealth(5 * time.Minute)
	if got := h.Status("anthropic"); got != ProviderHealthy {
		t.Errorf("empty window: got %v, want healthy", got)
	}
}

func TestProviderHealth_DegradedBetween20And50Percent(t *testing.T) {
	t.Parallel()
	h := NewProviderHealth(5 * time.Minute)
	for i := 0; i < 3; i++ {
		h.RecordSuccess("anthropic")
	}
	for i := 0; i < 2; i++ {
		h.RecordFailure("anthropic") // 2/5 = 40%
	}
	if got := h.Status("anthropic"); got != ProviderDegraded {
		t.Errorf("40%% errors: got %v, want degraded", got)
	}
}

func TestProviderHealth_DownAbove50Percent(t *testing.T) {
	t.Parallel()
	h := NewProviderHealth(5 * time.Minute)
	for i := 0; i < 2; i++ {
		h.RecordSuccess("anthropic")
	}
	for i := 0; i < 3; i++ {
		h.RecordFailure("anthropic") // 3/5 = 60%
	}
	if got := h.Status("anthropic"); got != ProviderDown {
		t.Errorf("60%% errors: got %v, want down", got)
	}
}

func TestProviderHealth_WindowExpires(t *testing.T) {
	t.Parallel()
	h := NewProviderHealth(10 * time.Millisecond)
	for i := 0; i < 5; i++ {
		h.RecordFailure("anthropic")
	}
	if h.Status("anthropic") != ProviderDown {
		t.Fatal("precondition: expected down")
	}
	time.Sleep(20 * time.Millisecond)
	if got := h.Status("anthropic"); got != ProviderHealthy {
		t.Errorf("after window: got %v, want healthy (entries expired)", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestProviderHealth -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/provider_health.go
package agent

import (
	"sync"
	"time"
)

type ProviderHealthStatus int

const (
	ProviderHealthy ProviderHealthStatus = iota
	ProviderDegraded
	ProviderDown
)

func (s ProviderHealthStatus) String() string {
	switch s {
	case ProviderHealthy:
		return "healthy"
	case ProviderDegraded:
		return "degraded"
	case ProviderDown:
		return "down"
	default:
		return "unknown"
	}
}

// ProviderHealth tracks rolling success/failure counts per provider over a
// fixed window. Entries outside the window are pruned lazily on read.
// Safe for concurrent use across worker goroutines.
type ProviderHealth struct {
	mu      sync.Mutex
	window  time.Duration
	entries map[string][]healthEntry
}

type healthEntry struct {
	at      time.Time
	success bool
}

func NewProviderHealth(window time.Duration) *ProviderHealth {
	return &ProviderHealth{window: window, entries: make(map[string][]healthEntry)}
}

func (h *ProviderHealth) RecordSuccess(p string) { h.record(p, true) }
func (h *ProviderHealth) RecordFailure(p string) { h.record(p, false) }

func (h *ProviderHealth) record(p string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries[p] = append(h.entries[p], healthEntry{at: time.Now(), success: ok})
}

// Status reports the provider's current health based on its error rate over
// the window. An empty window is treated as healthy (no signal).
func (h *ProviderHealth) Status(p string) ProviderHealthStatus {
	h.mu.Lock()
	defer h.mu.Unlock()

	cutoff := time.Now().Add(-h.window)
	fresh := h.entries[p][:0] // in-place filter
	var total, failures int
	for _, e := range h.entries[p] {
		if e.at.Before(cutoff) {
			continue
		}
		fresh = append(fresh, e)
		total++
		if !e.success {
			failures++
		}
	}
	h.entries[p] = fresh

	if total == 0 {
		return ProviderHealthy
	}
	ratio := float64(failures) / float64(total)
	switch {
	case ratio >= 0.5:
		return ProviderDown
	case ratio >= 0.2:
		return ProviderDegraded
	default:
		return ProviderHealthy
	}
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/ -run TestProviderHealth -v`
Expected: All 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/provider_health.go server/pkg/agent/provider_health_test.go
git commit -m "feat(agent): add in-memory provider health tracker

Rolling 5-minute window of success/failure per provider. Thresholds:
<20% healthy, 20-50% degraded, ≥50% down. Entries expire lazily on
Status() reads."
```

---

### Task 13: Multi-Model Fallback Chain

**Files:**
- Create: `server/pkg/agent/fallback.go`
- Create: `server/pkg/agent/fallback_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/fallback_test.go
package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

type scriptedModel struct {
	name string
	err  error
	text string
}

func (s *scriptedModel) SpecificationVersion() string    { return "v1" }
func (s *scriptedModel) Provider() string                { return s.name }
func (s *scriptedModel) ModelID() string                 { return s.name + "-model" }
func (s *scriptedModel) SupportsTools() bool             { return true }
func (s *scriptedModel) SupportsStructuredOutput() bool  { return false }
func (s *scriptedModel) SupportsImageInput() bool        { return false }
func (s *scriptedModel) DoGenerate(_ context.Context, _ *provider.GenerateOptions) (*types.GenerateResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &types.GenerateResult{Text: s.text, FinishReason: types.FinishReasonStop}, nil
}
func (s *scriptedModel) DoStream(_ context.Context, _ *provider.GenerateOptions) (provider.TextStream, error) {
	return nil, nil
}

func TestFallback_FallsThroughToSecondary(t *testing.T) {
	t.Parallel()
	primary := &scriptedModel{name: "anthropic", err: &APIError{StatusCode: 500, Message: "server error"}}
	secondary := &scriptedModel{name: "openai", text: "ok from openai"}

	chain := NewFallbackModel([]provider.LanguageModel{primary, secondary}, NewProviderHealth(time.Minute))
	res, err := chain.DoGenerate(context.Background(), &provider.GenerateOptions{})
	if err != nil {
		t.Fatalf("DoGenerate: %v", err)
	}
	if res.Text != "ok from openai" {
		t.Errorf("Text = %q", res.Text)
	}
}

func TestFallback_DoesNotFallThroughOnPermanentError(t *testing.T) {
	t.Parallel()
	primary := &scriptedModel{name: "anthropic", err: &APIError{StatusCode: 401, Message: "bad key"}}
	secondary := &scriptedModel{name: "openai", text: "should not be called"}

	chain := NewFallbackModel([]provider.LanguageModel{primary, secondary}, NewProviderHealth(time.Minute))
	_, err := chain.DoGenerate(context.Background(), &provider.GenerateOptions{})
	if err == nil {
		t.Fatal("expected auth_permanent to surface, not fall through")
	}
}

func TestFallback_SkipsDownProviders(t *testing.T) {
	t.Parallel()
	ph := NewProviderHealth(time.Minute)
	for i := 0; i < 10; i++ {
		ph.RecordFailure("anthropic") // drive to down
	}
	primary := &scriptedModel{name: "anthropic", text: "should not be called"}
	secondary := &scriptedModel{name: "openai", text: "ok"}

	chain := NewFallbackModel([]provider.LanguageModel{primary, secondary}, ph)
	res, err := chain.DoGenerate(context.Background(), &provider.GenerateOptions{})
	if err != nil {
		t.Fatalf("DoGenerate: %v", err)
	}
	if res.Text != "ok" {
		t.Errorf("Text = %q, want openai result (primary was skipped)", res.Text)
	}
}

func TestFallback_AllExhausted_ReturnsLastError(t *testing.T) {
	t.Parallel()
	primary := &scriptedModel{name: "anthropic", err: errors.New("boom1")}
	secondary := &scriptedModel{name: "openai", err: errors.New("boom2")}
	chain := NewFallbackModel([]provider.LanguageModel{primary, secondary}, NewProviderHealth(time.Minute))

	_, err := chain.DoGenerate(context.Background(), &provider.GenerateOptions{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}
```

Add to top of file: `import "time"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestFallback -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/fallback.go
package agent

import (
	"context"
	"fmt"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// FallbackModel tries each LanguageModel in order. A model is skipped when
// ProviderHealth says its provider is Down. Only retryable errors (Phase 1.3)
// trigger fall-through; auth_permanent / billing / format_error surface
// immediately.
type FallbackModel struct {
	models []provider.LanguageModel
	health *ProviderHealth
}

func NewFallbackModel(models []provider.LanguageModel, health *ProviderHealth) *FallbackModel {
	if len(models) == 0 {
		panic("NewFallbackModel: at least one model required")
	}
	return &FallbackModel{models: models, health: health}
}

// Identity methods delegate to the primary.
func (f *FallbackModel) SpecificationVersion() string    { return f.models[0].SpecificationVersion() }
func (f *FallbackModel) Provider() string                { return f.models[0].Provider() }
func (f *FallbackModel) ModelID() string                 { return f.models[0].ModelID() }
func (f *FallbackModel) SupportsTools() bool             { return f.models[0].SupportsTools() }
func (f *FallbackModel) SupportsStructuredOutput() bool  { return f.models[0].SupportsStructuredOutput() }
func (f *FallbackModel) SupportsImageInput() bool        { return f.models[0].SupportsImageInput() }

func (f *FallbackModel) DoGenerate(ctx context.Context, opts *provider.GenerateOptions) (*types.GenerateResult, error) {
	var lastErr error
	for _, m := range f.models {
		if f.health.Status(m.Provider()) == ProviderDown {
			lastErr = fmt.Errorf("provider %s is down; skipped", m.Provider())
			continue
		}
		res, err := m.DoGenerate(ctx, opts)
		if err == nil {
			f.health.RecordSuccess(m.Provider())
			return res, nil
		}
		class := ClassifyError(err)
		f.health.RecordFailure(m.Provider())
		if !class.Retryable {
			return nil, err // surface permanent errors immediately
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all providers in chain are down")
	}
	return nil, lastErr
}

func (f *FallbackModel) DoStream(ctx context.Context, opts *provider.GenerateOptions) (provider.TextStream, error) {
	// Streaming fallback has the same mid-stream problem as rotation. For v1:
	// try the first healthy provider; no mid-stream retry.
	for _, m := range f.models {
		if f.health.Status(m.Provider()) == ProviderDown {
			continue
		}
		stream, err := m.DoStream(ctx, opts)
		if err == nil {
			return stream, nil
		}
		f.health.RecordFailure(m.Provider())
		if !ClassifyError(err).Retryable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("no healthy providers for streaming")
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/ -run TestFallback -v`
Expected: All 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/fallback.go server/pkg/agent/fallback_test.go
git commit -m "feat(agent): add multi-model fallback chain

FallbackModel wraps an ordered list of LanguageModels. Skips providers
in Down state (via ProviderHealth). Only retryable errors fall through;
permanent (auth/billing/format) surface immediately. DoStream tries
first healthy provider without mid-stream retry."
```

---

### Task 14: Cost Calculator (Cache-Aware Pricing)

**Files:**
- Create: `server/pkg/router/cost.go`
- Create: `server/pkg/router/cost_test.go`

**Before coding:** The pricing table in Step 3 uses values accurate as of the plan-writing date. Anthropic and OpenAI adjust rate cards regularly; **verify each value in Step 3 against the current provider rate cards before committing.** The Step 1 tests hard-code expected cents and will fail (correctly) if pricing moved — update both sides together.

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/router/cost_test.go
package router

import "testing"

func TestCostCents_BasicAnthropic(t *testing.T) {
	t.Parallel()
	// Claude Sonnet pricing: $3 / 1M input, $15 / 1M output.
	// 1000 input + 1000 output = 0.3c + 1.5c = 1.8c → rounds to 2c
	got := CostCents("anthropic", "claude-sonnet-4-5", Usage{
		InputTokens:  1000,
		OutputTokens: 1000,
	})
	if got != 2 {
		t.Errorf("CostCents = %d, want 2 (1.8c rounded up)", got)
	}
}

func TestCostCents_CacheReadIsOneTenth(t *testing.T) {
	t.Parallel()
	// 10000 cached input tokens should cost ~0.3c (10x less than uncached),
	// then round up.
	got := CostCents("anthropic", "claude-sonnet-4-5", Usage{
		CachedInputTokens: 10000,
	})
	if got != 1 {
		t.Errorf("CostCents = %d, want 1 (0.3c rounded up to 1)", got)
	}
}

func TestCostCents_CacheWriteIs1_25x(t *testing.T) {
	t.Parallel()
	// 1000 cache-write tokens at 1.25× the input rate
	got := CostCents("anthropic", "claude-sonnet-4-5", Usage{
		CacheWriteTokens: 1000,
	})
	// 1000 tokens * $3/1M * 1.25 = 0.375c → rounds to 1
	if got != 1 {
		t.Errorf("CostCents = %d, want 1", got)
	}
}

func TestCostCents_UnknownModel_DefaultsToStandard(t *testing.T) {
	t.Parallel()
	got := CostCents("unknown", "brand-new-model-5", Usage{InputTokens: 1_000_000})
	if got == 0 {
		t.Error("unknown model should use fallback pricing, not zero")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/router/ -v`
Expected: FAIL — package missing.

- [ ] **Step 3: Implement**

```go
// server/pkg/router/cost.go
// Package router handles LLM model selection + cost accounting. Pricing
// tables ported from Hermes agent/usage_pricing.py.
package router

import "math"

// Usage mirrors go-ai types.Usage at the level we care about for pricing.
type Usage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int // 0.1× input price
	CacheWriteTokens  int // 1.25× input price
	ReasoningTokens   int // billed as output for Claude; separate for OpenAI o-series
}

// pricing is $ per 1M tokens. {inputUSD, outputUSD}. Cached-read is implicitly
// 0.1× input; cache-write is 1.25× input.
type pricing struct {
	inputUSDPerM  float64
	outputUSDPerM float64
}

var modelPricing = map[string]pricing{
	// Anthropic
	"claude-sonnet-4-5":         {inputUSDPerM: 3.0, outputUSDPerM: 15.0},
	"claude-sonnet-4-5-20250514": {inputUSDPerM: 3.0, outputUSDPerM: 15.0},
	"claude-haiku-4-5":          {inputUSDPerM: 0.80, outputUSDPerM: 4.0},
	"claude-haiku-4-5-20251001": {inputUSDPerM: 0.80, outputUSDPerM: 4.0},
	"claude-opus-4-6":           {inputUSDPerM: 15.0, outputUSDPerM: 75.0},
	// OpenAI
	"gpt-4o":      {inputUSDPerM: 2.50, outputUSDPerM: 10.0},
	"gpt-4o-mini": {inputUSDPerM: 0.15, outputUSDPerM: 0.60},
}

// fallbackPricing is used for unknown models so cost_event is never 0.
var fallbackPricing = pricing{inputUSDPerM: 3.0, outputUSDPerM: 15.0}

// CostCents computes the cost in whole cents (rounded up) for a call to
// providerName/modelID with the given token usage. Never returns 0 for
// non-zero usage.
func CostCents(providerName, modelID string, u Usage) int {
	p, ok := modelPricing[modelID]
	if !ok {
		p = fallbackPricing
	}

	inputUSD := float64(u.InputTokens) * p.inputUSDPerM / 1_000_000
	outputUSD := float64(u.OutputTokens+u.ReasoningTokens) * p.outputUSDPerM / 1_000_000
	cachedUSD := float64(u.CachedInputTokens) * p.inputUSDPerM * 0.1 / 1_000_000
	cacheWriteUSD := float64(u.CacheWriteTokens) * p.inputUSDPerM * 1.25 / 1_000_000

	totalUSD := inputUSD + outputUSD + cachedUSD + cacheWriteUSD
	cents := math.Ceil(totalUSD * 100)
	if totalUSD > 0 && cents < 1 {
		return 1
	}
	return int(cents)
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/router/ -v`
Expected: All 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/router/cost.go server/pkg/router/cost_test.go
git commit -m "feat(router): add cache-aware cost calculator

Pricing tables for Claude Sonnet/Haiku/Opus + GPT-4o variants.
Cached-read at 0.1×, cache-write at 1.25×. Unknown models use
fallback pricing so cost_event is never zero."
```

---

### Task 15: Model Router (Tier Selection)

**Files:**
- Create: `server/pkg/router/router.go`
- Create: `server/pkg/router/router_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/router/router_test.go
package router

import "testing"

func TestRouter_PolicyBased_MapsAgentTypeToTier(t *testing.T) {
	t.Parallel()
	r := NewRouter(RouterConfig{
		Policy: map[string]Tier{
			"customer_support": TierMicro,
			"legal_reviewer":   TierPremium,
		},
		DefaultTier: TierStandard,
	})

	if got := r.Select(SelectOptions{AgentType: "customer_support"}); got != TierMicro {
		t.Errorf("got %v, want TierMicro", got)
	}
	if got := r.Select(SelectOptions{AgentType: "legal_reviewer"}); got != TierPremium {
		t.Errorf("got %v, want TierPremium", got)
	}
	if got := r.Select(SelectOptions{AgentType: "unknown"}); got != TierStandard {
		t.Errorf("got %v, want TierStandard (default)", got)
	}
}

func TestRouter_DefaultModel_MatchesTier(t *testing.T) {
	t.Parallel()
	r := NewRouter(RouterConfig{})
	if got := r.DefaultModel(TierMicro); got.Provider != "anthropic" || got.Model != "claude-haiku-4-5" {
		t.Errorf("micro default = %+v", got)
	}
	if got := r.DefaultModel(TierStandard); got.Model != "claude-sonnet-4-5" {
		t.Errorf("standard default = %+v", got)
	}
	if got := r.DefaultModel(TierPremium); got.Model != "claude-opus-4-6" {
		t.Errorf("premium default = %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/router/ -run TestRouter -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/router/router.go
package router

// Tier categorises cost/capability trade-offs. Concrete model choices
// per tier are tenant-overridable; this file just encodes the defaults.
type Tier int

const (
	TierMicro Tier = iota
	TierStandard
	TierPremium
)

// ModelChoice is a (provider, model) pair.
type ModelChoice struct {
	Provider string
	Model    string
}

// RouterConfig controls routing behaviour.
type RouterConfig struct {
	// Policy: agent_type → Tier mapping. Overrides DefaultTier for matched
	// agent types; unmatched types fall back to DefaultTier.
	Policy map[string]Tier

	// DefaultTier is used when Policy has no entry and SelectOptions doesn't
	// override. Zero value = TierMicro; callers typically want TierStandard.
	DefaultTier Tier

	// TierModels overrides built-in per-tier defaults. Key is Tier.
	TierModels map[Tier]ModelChoice
}

type SelectOptions struct {
	AgentType    string
	OverrideTier *Tier // per-call override (e.g. workflow-step tier)
}

type Router struct {
	cfg RouterConfig
}

func NewRouter(cfg RouterConfig) *Router {
	return &Router{cfg: cfg}
}

// Select returns the tier an agent should run at.
func (r *Router) Select(opts SelectOptions) Tier {
	if opts.OverrideTier != nil {
		return *opts.OverrideTier
	}
	if t, ok := r.cfg.Policy[opts.AgentType]; ok {
		return t
	}
	return r.cfg.DefaultTier
}

// DefaultModel returns the provider/model pair for a tier. Checks
// TierModels override first, then falls back to built-in defaults.
func (r *Router) DefaultModel(t Tier) ModelChoice {
	if m, ok := r.cfg.TierModels[t]; ok {
		return m
	}
	switch t {
	case TierMicro:
		return ModelChoice{Provider: "anthropic", Model: "claude-haiku-4-5"}
	case TierPremium:
		return ModelChoice{Provider: "anthropic", Model: "claude-opus-4-6"}
	default:
		return ModelChoice{Provider: "anthropic", Model: "claude-sonnet-4-5"}
	}
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/router/ -v`
Expected: All tests PASS (cost + router).

- [ ] **Step 5: Commit**

```bash
git add server/pkg/router/router.go server/pkg/router/router_test.go
git commit -m "feat(router): add tier-based model router

Three tiers (micro/standard/premium) with Claude Haiku/Sonnet/Opus
defaults. Policy map routes per agent_type; SelectOptions.OverrideTier
supports per-call overrides (used by workflow steps in Phase 7)."
```

---

### Task 16: Budget Check Service — consume Phase 1's `CanDispatch`

**Files:**
- Modify: `server/internal/service/budget.go` (file was created in Phase 1 Task 10.5; no new service file needed)
- Create: `server/internal/service/budget_integration_test.go`

**NOTE (post-blocker correction):** the budget service was rewritten in PLAN.md §1.2 (R3 B5). The OLD Phase 2 plan described a stale `CanExecute(ctx, wsID, agentID) (bool, string, error)` API that runs outside a transaction — that design loses to check-then-execute races under concurrent claims and chat messages. The real implementation lives in Phase 1 Task 10.5 as `CanDispatch(ctx, tx pgx.Tx, wsID uuid.UUID, estCents int) (Decision, error)` and uses `pg_advisory_xact_lock` inside the caller's transaction. Phase 2 is now the primary caller — not the owner — of that service.

This task verifies Phase 2's integration with the service:

- [ ] **Step 1: Write an integration test that locks actually serialize**

```go
// server/internal/service/budget_integration_test.go
package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCanDispatch_SerializesUnderContention(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	wsID := seedWorkspace(t, pool)
	seedBudget(t, pool, wsID, 1000 /* cents */, true /* hard_stop */)
	b := NewBudgetService(pool)

	var wg sync.WaitGroup
	results := make(chan bool, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tx, _ := pool.BeginTx(ctx, pgx.TxOptions{})
			defer tx.Rollback(ctx)
			dec, err := b.CanDispatch(ctx, tx, wsID, 600)
			if err != nil { t.Error(err); return }
			results <- dec.Allow
			if dec.Allow {
				// Simulate cost emission inside the same tx.
				time.Sleep(20 * time.Millisecond)
				_ = tx.Commit(ctx)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	allowed := 0
	for r := range results { if r { allowed++ } }
	// Second call waits on the advisory lock; by the time it runs, the first
	// call's seed ($6) + its own projection ($6) = $12 > $10 hard_stop.
	if allowed != 1 {
		t.Errorf("expected exactly one goroutine allowed, got %d", allowed)
	}
}
```

- [ ] **Step 2: Verify the integration test passes**

```bash
cd server && go test ./internal/service/ -run TestCanDispatch_SerializesUnderContention -v
```

This exercises the Phase 1-implemented service; no new implementation is added here. If the test fails, the problem is almost certainly in Phase 1's advisory-lock wiring — fix there, not here.

- [ ] **Step 3: Commit**

```bash
git add server/internal/service/budget_integration_test.go
git commit -m "test(service): verify advisory lock serializes concurrent CanDispatch

Integration-tests the budget gate from PLAN.md §1.2 under two concurrent
transactions. Relies on the BudgetService implementation from Phase 1
Task 10.5."
```

---

### Task 17: HTTP Backend (`agent_type=http`)

**Files:**
- Create: `server/pkg/agent/http_backend.go`
- Create: `server/pkg/agent/http_backend_test.go`

**Goal:** an HTTP-as-backend agent that posts `{prompt, context}` to a configured URL and returns the body. Used for integrating external AI services (custom company-hosted LLMs, legacy chatbots).

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/http_backend_test.go
package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPBackend_PostsJSONAndReturnsResult(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"output": "hello from http agent"})
	}))
	defer srv.Close()

	be := NewHTTPBackend(HTTPBackendConfig{
		URL:           srv.URL,
		ResponseField: "output",
		Timeout:       2 * time.Second,
	})

	sess, err := be.Execute(context.Background(), "Say hi.", ExecOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case r := <-sess.Result:
		if r.Status != "completed" {
			t.Errorf("status = %q, want completed", r.Status)
		}
		if r.Output != "hello from http agent" {
			t.Errorf("output = %q", r.Output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Result")
	}

	if gotBody["prompt"] != "Say hi." {
		t.Errorf("server got prompt = %v", gotBody["prompt"])
	}
}

func TestHTTPBackend_NonOKResponseFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	be := NewHTTPBackend(HTTPBackendConfig{URL: srv.URL, ResponseField: "output", Timeout: time.Second})
	sess, _ := be.Execute(context.Background(), "x", ExecOptions{})
	r := <-sess.Result
	if r.Status != "failed" {
		t.Errorf("status = %q, want failed", r.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestHTTPBackend -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/http_backend.go
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPBackendConfig configures an HTTP-backed agent.
type HTTPBackendConfig struct {
	URL           string            // endpoint to POST to
	Method        string            // default POST
	Headers       map[string]string // static headers (API keys, etc.)
	ResponseField string            // JSON field to extract; empty = use full body
	Timeout       time.Duration     // default 60s
}

// HTTPBackend implements Backend over a single HTTP endpoint.
type HTTPBackend struct {
	noExpiry
	cfg    HTTPBackendConfig
	client *http.Client
}

func NewHTTPBackend(cfg HTTPBackendConfig) *HTTPBackend {
	if cfg.Method == "" {
		cfg.Method = "POST"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &HTTPBackend{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

func (h *HTTPBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	msgs := make(chan Message, 1)
	result := make(chan Result, 1)
	sess := &Session{Messages: msgs, Result: result}

	go func() {
		defer close(msgs)
		defer close(result)

		payload, _ := json.Marshal(map[string]any{
			"prompt":     prompt,
			"system":     opts.SystemPrompt,
			"model":      opts.Model,
			"session_id": opts.ResumeSessionID,
		})
		req, err := http.NewRequestWithContext(ctx, h.cfg.Method, h.cfg.URL, bytes.NewReader(payload))
		if err != nil {
			result <- Result{Status: "failed", Error: err.Error()}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range h.cfg.Headers {
			req.Header.Set(k, v)
		}

		start := time.Now()
		resp, err := h.client.Do(req)
		if err != nil {
			result <- Result{Status: "failed", Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			result <- Result{
				Status:     "failed",
				Error:      fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(raw)),
				DurationMs: time.Since(start).Milliseconds(),
			}
			return
		}

		output := string(raw)
		if h.cfg.ResponseField != "" {
			var parsed map[string]any
			if err := json.Unmarshal(raw, &parsed); err == nil {
				if v, ok := parsed[h.cfg.ResponseField]; ok {
					if s, ok := v.(string); ok {
						output = s
					}
				}
			}
		}

		result <- Result{
			Status:     "completed",
			Output:     output,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}()

	return sess, nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/ -run TestHTTPBackend -v`
Expected: Both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/http_backend.go server/pkg/agent/http_backend_test.go
git commit -m "feat(agent): add HTTPBackend for agent_type=http

POSTs {prompt, system, model, session_id} as JSON; extracts output
from a configurable response field. Non-2xx statuses fail the task.
Used for integrating external AI endpoints + legacy chatbots."
```

---

### Task 18: Process Backend (`agent_type=process`)

**Files:**
- Create: `server/pkg/agent/process_backend.go`
- Create: `server/pkg/agent/process_backend_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/process_backend_test.go
package agent

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestProcessBackend_RunsAndCapturesStdout(t *testing.T) {
	t.Parallel()

	var be *ProcessBackend
	if runtime.GOOS == "windows" {
		be = NewProcessBackend(ProcessBackendConfig{
			Command: "cmd",
			Args:    []string{"/C", "echo hello"},
		})
	} else {
		be = NewProcessBackend(ProcessBackendConfig{
			Command: "/bin/sh",
			Args:    []string{"-c", "echo hello"},
		})
	}

	sess, err := be.Execute(context.Background(), "ignored", ExecOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case r := <-sess.Result:
		if r.Status != "completed" {
			t.Errorf("status = %q, want completed", r.Status)
		}
		if !contains(r.Output, "hello") {
			t.Errorf("output = %q, want contains hello", r.Output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestProcessBackend_ExitsNonZero_Fails(t *testing.T) {
	t.Parallel()

	var be *ProcessBackend
	if runtime.GOOS == "windows" {
		be = NewProcessBackend(ProcessBackendConfig{Command: "cmd", Args: []string{"/C", "exit 1"}})
	} else {
		be = NewProcessBackend(ProcessBackendConfig{Command: "/bin/sh", Args: []string{"-c", "exit 1"}})
	}

	sess, _ := be.Execute(context.Background(), "x", ExecOptions{})
	r := <-sess.Result
	if r.Status != "failed" {
		t.Errorf("status = %q, want failed", r.Status)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestProcessBackend -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/process_backend.go
package agent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// ProcessBackendConfig configures a subprocess-backed agent.
type ProcessBackendConfig struct {
	Command       string
	Args          []string
	WorkingDir    string
	Env           map[string]string
	Timeout       time.Duration // default 5 minutes
	MaxOutputSize int           // default 50000 bytes
}

// ProcessBackend runs a command with the prompt piped to stdin; stdout is
// the task output. NOT sandboxed — use cloud_coding (Phase 11) for
// untrusted code. Intended for trusted tools (Python scripts, CLI wrappers).
type ProcessBackend struct {
	noExpiry
	cfg ProcessBackendConfig
}

func NewProcessBackend(cfg ProcessBackendConfig) *ProcessBackend {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.MaxOutputSize == 0 {
		cfg.MaxOutputSize = 50_000
	}
	return &ProcessBackend{cfg: cfg}
}

func (p *ProcessBackend) Execute(ctx context.Context, prompt string, _ ExecOptions) (*Session, error) {
	msgs := make(chan Message, 1)
	result := make(chan Result, 1)

	go func() {
		defer close(msgs)
		defer close(result)

		runCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
		defer cancel()

		cmd := exec.CommandContext(runCtx, p.cfg.Command, p.cfg.Args...)
		if p.cfg.WorkingDir != "" {
			cmd.Dir = p.cfg.WorkingDir
		}
		for k, v := range p.cfg.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Stdin = bytes.NewBufferString(prompt)

		start := time.Now()
		raw, err := cmd.CombinedOutput()
		dur := time.Since(start).Milliseconds()

		if len(raw) > p.cfg.MaxOutputSize {
			raw = append(raw[:p.cfg.MaxOutputSize], []byte("\n[truncated]")...)
		}

		if err != nil {
			result <- Result{
				Status:     "failed",
				Output:     string(raw),
				Error:      err.Error(),
				DurationMs: dur,
			}
			return
		}
		result <- Result{
			Status:     "completed",
			Output:     string(raw),
			DurationMs: dur,
		}
	}()

	return &Session{Messages: msgs, Result: result}, nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/ -run TestProcessBackend -v`
Expected: Both tests PASS on Linux/macOS/Windows.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/process_backend.go server/pkg/agent/process_backend_test.go
git commit -m "feat(agent): add ProcessBackend for agent_type=process

Pipes prompt to stdin, captures combined output, enforces configurable
timeout + output size cap. NOT sandboxed; use cloud_coding (Phase 11)
for untrusted code. Cross-platform test verified on linux + windows
CI runners."
```

---

### Task 19: LLM API Backend (Integration Tying Harness + Credentials + Cost)

**Files:**
- Create: `server/pkg/agent/llm_api.go`
- Create: `server/pkg/agent/llm_api_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/llm_api_test.go
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/provider/types"
)

type finishingModel struct{ text string }

func (m *finishingModel) SpecificationVersion() string    { return "v1" }
func (m *finishingModel) Provider() string                { return "fake" }
func (m *finishingModel) ModelID() string                 { return "fake-model" }
func (m *finishingModel) SupportsTools() bool             { return false }
func (m *finishingModel) SupportsStructuredOutput() bool  { return false }
func (m *finishingModel) SupportsImageInput() bool        { return false }
func (m *finishingModel) DoGenerate(_ context.Context, _ *provider.GenerateOptions) (*types.GenerateResult, error) {
	return &types.GenerateResult{Text: m.text, FinishReason: types.FinishReasonStop, Usage: types.Usage{InputTokens: 10, OutputTokens: 20}}, nil
}
func (m *finishingModel) DoStream(_ context.Context, _ *provider.GenerateOptions) (provider.TextStream, error) {
	return nil, nil
}

type recordingCostSink struct {
	calls []CostEvent
}

func (r *recordingCostSink) Record(_ context.Context, e CostEvent) error {
	r.calls = append(r.calls, e)
	return nil
}

func TestLLMAPIBackend_ExecuteRecordsCost(t *testing.T) {
	t.Parallel()

	sink := &recordingCostSink{}
	be := NewLLMAPIBackend(LLMAPIBackendConfig{
		Model:        &finishingModel{text: "hello"},
		CostSink:     sink,
		SystemPrompt: "be brief",
		ProviderName: "fake",
		ModelID:      "fake-model",
	})

	sess, err := be.Execute(context.Background(), "Say hi.", ExecOptions{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case r := <-sess.Result:
		if r.Status != "completed" {
			t.Errorf("status = %q", r.Status)
		}
		if r.Output != "hello" {
			t.Errorf("output = %q", r.Output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	if len(sink.calls) != 1 {
		t.Fatalf("cost events = %d, want 1", len(sink.calls))
	}
	if sink.calls[0].InputTokens != 10 || sink.calls[0].OutputTokens != 20 {
		t.Errorf("usage = %+v", sink.calls[0])
	}
	if sink.calls[0].CostCents == 0 {
		t.Error("cost cents should be > 0")
	}
}

func TestLLMAPIBackend_ExpiresAtAlwaysNil(t *testing.T) {
	t.Parallel()
	be := NewLLMAPIBackend(LLMAPIBackendConfig{Model: &finishingModel{}, CostSink: &recordingCostSink{}})
	if be.ExpiresAt() != nil {
		t.Error("LLM API has no session cap; ExpiresAt should be nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestLLMAPIBackend -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/llm_api.go
package agent

import (
	"context"
	"time"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/provider/types"

	"github.com/multica-ai/multica/server/pkg/agent/harness"
	"github.com/multica-ai/multica/server/pkg/router"
)

// CostEvent is the data a Backend emits after an LLM call so cost tracking
// code can record it to cost_event. Decoupled from the DB row so unit tests
// don't need a live Postgres.
//
// Field names match the cost_event schema after the R3/R2 blocker fixes:
//   CacheReadTokens replaces the old CachedTokens (PLAN.md §1.1, R3 S6).
//   InputTokens is NON-CACHED input only — Anthropic reports these separately.
//   TraceID is mandatory (D8). CostStatus transitions: 'estimated' → 'actual'
//   for settlement; 'adjustment' for reconciliations; 'included' for cache hits.
type CostEvent struct {
	Provider          string
	Model             string
	InputTokens       int            // NON-CACHED input tokens only
	OutputTokens      int
	CacheReadTokens   int            // was CachedTokens; Anthropic cache_read_input_tokens
	CacheWriteTokens  int
	CacheTTLMinutes   int            // 5 or 60 — determines cache-write multiplier
	ReasoningTokens   int
	CostCents         int
	CostStatus        string         // 'estimated' | 'actual' | 'included' | 'adjustment'
	TraceID           uuid.UUID      // D8: mandatory, never uuid.Nil
	ResponseCacheID   *uuid.UUID     // Phase 9; nil in Phase 2
	SessionHours      float64        // Phase 12; 0 in Phase 2
	SessionCostCents  int            // Phase 12; 0 in Phase 2
	DurationMs        int64
}

// CostSink accepts CostEvents. Production wiring inserts a cost_event row
// via sqlc; tests use recordingCostSink.
type CostSink interface {
	Record(ctx context.Context, e CostEvent) error
}

// LLMAPIBackendConfig wires together everything a direct-LLM-API backend
// needs. ProviderName + ModelID are used for cost calc + logging when the
// underlying go-ai model is a fallback/rotating wrapper that hides its identity.
type LLMAPIBackendConfig struct {
	Model        provider.LanguageModel
	CostSink     CostSink
	SystemPrompt string
	ProviderName string // used by CostCents
	ModelID      string // used by CostCents
	Reasoning    string // empty = DefaultReasoningLevel
	Tools        []types.Tool
}

// LLMAPIBackend implements Backend for agent_type=llm_api. Delegates the
// tool-call loop to the harness (which wraps go-ai); emits a CostEvent once
// the loop finishes.
type LLMAPIBackend struct {
	noExpiry
	cfg LLMAPIBackendConfig
}

func NewLLMAPIBackend(cfg LLMAPIBackendConfig) *LLMAPIBackend {
	if cfg.CostSink == nil {
		panic("NewLLMAPIBackend: CostSink is required")
	}
	return &LLMAPIBackend{cfg: cfg}
}

func (b *LLMAPIBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	msgs := make(chan Message, 16)
	result := make(chan Result, 1)
	sess := &Session{Messages: msgs, Result: result}

	go func() {
		defer close(msgs)
		defer close(result)

		h := harness.New(harness.Config{
			Model:        b.cfg.Model,
			SystemPrompt: b.cfg.SystemPrompt,
			Tools:        b.cfg.Tools,
			Reasoning:    b.cfg.Reasoning,
		})

		start := time.Now()
		res, err := h.Execute(ctx, prompt)
		dur := time.Since(start).Milliseconds()

		if err != nil {
			result <- Result{Status: "failed", Error: err.Error(), DurationMs: dur}
			return
		}

		// Emit cost event. D8: TraceID comes from ctx — the worker mints it at
		// task entry (Task 22) via WithTraceID, and every cost row carries it so
		// Phase 9's chain-cost aggregation (SUM WHERE trace_id=$1) works.
		cost := router.CostCents(b.cfg.ProviderName, b.cfg.ModelID, router.Usage{
			InputTokens:       int(res.Usage.InputTokens),
			OutputTokens:      int(res.Usage.OutputTokens),
			CachedInputTokens: int(res.Usage.CachedInputTokens),
		})
		_ = b.cfg.CostSink.Record(ctx, CostEvent{
			Provider:        b.cfg.ProviderName,
			Model:           b.cfg.ModelID,
			InputTokens:     int(res.Usage.InputTokens),     // go-ai reports non-cached input only
			OutputTokens:    int(res.Usage.OutputTokens),
			CacheReadTokens: int(res.Usage.CachedInputTokens),
			CostCents:       cost,
			CostStatus:      "actual",
			TraceID:         TraceIDFrom(ctx),                // D8: non-nil once Task 22 wires it
			DurationMs:      dur,
		})

		result <- Result{
			Status:     "completed",
			Output:     res.Text,
			DurationMs: dur,
		}
	}()

	// ResumeSessionID + Cwd are CLI/sandbox concepts and intentionally
	// ignored here: LLM API backends reconstruct conversation history
	// from chat_message rows, not from a session on disk.
	_ = opts
	return sess, nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/ -run TestLLMAPIBackend -v`
Expected: Both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/llm_api.go server/pkg/agent/llm_api_test.go
git commit -m "feat(agent): add LLMAPIBackend tying harness + cost tracking

Delegates tool-call loop to harness (go-ai ToolLoopAgent). Emits a
CostEvent per turn via injected CostSink (real impl writes to
cost_event table; tests use recording fake). ExpiresAt=nil; LLM
API has no session cap beyond provider rate limits."
```

---

### Task 19.5: DB Test Utilities (`NewTestDB` + builders)

**Files:**
- Create: `server/internal/service/testutil.go`
- Create: `server/internal/service/testutil_test.go`

**Why this task exists:** Task 20 (claim-by-provider) and future integration tests across Phases 3-12 need a lightweight way to spin up a test Postgres schema with a handful of rows and tear it down. The project's `make test` already runs a pgvector container in CI; this task gives Go tests a clean helper interface on top.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/service/testutil_test.go
package service

import (
	"testing"
)

// Smoke-tests the helpers themselves so we catch regressions before they
// cause opaque failures in downstream tests.
func TestTestDB_WorkspaceAndAgent(t *testing.T) {
	t.Parallel()

	ts := NewTestDB(t)
	defer ts.Close()

	wsID := ts.CreateWorkspace()
	if wsID == "" {
		t.Fatal("CreateWorkspace returned empty ID")
	}
	agentID := ts.CreateLLMAgent(wsID, "anthropic", "claude-sonnet-4-5")
	if agentID == "" {
		t.Fatal("CreateLLMAgent returned empty ID")
	}

	// Verify row shape
	agent, err := ts.Queries.GetAgent(ts.Ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if agent.AgentType != "llm_api" {
		t.Errorf("AgentType = %q, want llm_api", agent.AgentType)
	}
}

func TestTestDB_CreateCodingAgent_UsesRuntime(t *testing.T) {
	t.Parallel()

	ts := NewTestDB(t)
	defer ts.Close()

	wsID := ts.CreateWorkspace()
	runtimeID := ts.CreateRuntime(wsID, "anthropic")
	agentID := ts.CreateCodingAgent(wsID, runtimeID)

	agent, _ := ts.Queries.GetAgent(ts.Ctx, parseUUID(agentID))
	if agent.AgentType != "coding" {
		t.Errorf("AgentType = %q, want coding", agent.AgentType)
	}
	if !agent.RuntimeID.Valid {
		t.Error("coding agent should have runtime_id set")
	}
}
```

- [ ] **Step 2: Implement the helpers**

```go
// server/internal/service/testutil.go
// Test helpers for DB-backed service tests. Uses the pgvector/pgvector:pg17
// instance that `make test` spins up in CI; locally, set TEST_DATABASE_URL
// to your dev DB (defaults to postgres://postgres:postgres@localhost:5432/multica_test).
package service

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type TestDB struct {
	Pool    *pgxpool.Pool
	Queries *db.Queries
	Ctx     context.Context
	t       *testing.T
	cleanup []string // uuid strings to DELETE in reverse order
}

// NewTestDB connects, runs migrations if needed, and registers t.Cleanup to
// roll back any rows created through the builders.
func NewTestDB(t *testing.T) *TestDB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@localhost:5432/multica_test?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("DB unavailable (%v); skipping integration test", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("DB ping failed (%v); skipping integration test", err)
	}
	ts := &TestDB{Pool: pool, Queries: db.New(pool), Ctx: context.Background(), t: t}
	t.Cleanup(ts.rollback)
	return ts
}

func (ts *TestDB) Close() { ts.Pool.Close() }

func (ts *TestDB) rollback() {
	// Row-level cleanup in reverse insertion order. Covers tests that don't
	// wrap everything in a transaction — simpler + safer for most cases.
	for i := len(ts.cleanup) - 1; i >= 0; i-- {
		// ordered list of (table, id) pairs encoded as "table:uuid"
		// schema is simple enough to hand-enumerate; expand as new builders are added.
	}
	_, _ = ts.Pool.Exec(ts.Ctx, `
		DELETE FROM agent_task_queue WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id = ANY($1));
		DELETE FROM issue WHERE workspace_id = ANY($1);
		DELETE FROM agent WHERE workspace_id = ANY($1);
		DELETE FROM agent_runtime WHERE workspace_id = ANY($1);
		DELETE FROM workspace WHERE id = ANY($1);
	`, ts.cleanupWorkspaceIDs())
}

func (ts *TestDB) cleanupWorkspaceIDs() []pgtype.UUID {
	out := []pgtype.UUID{}
	for _, s := range ts.cleanup {
		out = append(out, parseUUID(s))
	}
	return out
}

// CreateWorkspace inserts a workspace row and returns its UUID string.
func (ts *TestDB) CreateWorkspace() string {
	id := uuid.NewString()
	_, err := ts.Pool.Exec(ts.Ctx,
		`INSERT INTO workspace (id, name, issue_prefix) VALUES ($1, $2, $3)`,
		id, "test-ws-"+id[:8], "T",
	)
	if err != nil {
		ts.t.Fatalf("CreateWorkspace: %v", err)
	}
	ts.cleanup = append(ts.cleanup, id)
	return id
}

// CreateRuntime inserts an agent_runtime row (for coding agents).
func (ts *TestDB) CreateRuntime(workspaceID, providerName string) string {
	id := uuid.NewString()
	_, err := ts.Pool.Exec(ts.Ctx,
		`INSERT INTO agent_runtime (id, workspace_id, name, runtime_mode, provider, status)
		 VALUES ($1, $2, $3, 'local', $4, 'online')`,
		id, workspaceID, "rt-"+id[:8], providerName,
	)
	if err != nil {
		ts.t.Fatalf("CreateRuntime: %v", err)
	}
	return id
}

// CreateLLMAgent inserts an llm_api agent with the given provider + model.
func (ts *TestDB) CreateLLMAgent(workspaceID, providerName, modelID string) string {
	id := uuid.NewString()
	_, err := ts.Pool.Exec(ts.Ctx,
		`INSERT INTO agent (id, workspace_id, name, description, avatar_url, runtime_mode,
		                   runtime_config, visibility, status, max_concurrent_tasks, owner_id,
		                   instructions, agent_type, provider, model)
		 VALUES ($1, $2, $3, '', NULL, 'cloud', '{}', 'private', 'idle', 6, NULL,
		         '', 'llm_api', $4, $5)`,
		id, workspaceID, "agent-"+id[:8], providerName, modelID,
	)
	if err != nil {
		ts.t.Fatalf("CreateLLMAgent: %v", err)
	}
	return id
}

// CreateCodingAgent inserts a coding agent bound to the given runtime.
func (ts *TestDB) CreateCodingAgent(workspaceID, runtimeID string) string {
	id := uuid.NewString()
	_, err := ts.Pool.Exec(ts.Ctx,
		`INSERT INTO agent (id, workspace_id, name, description, avatar_url, runtime_mode,
		                   runtime_config, runtime_id, visibility, status, max_concurrent_tasks, owner_id,
		                   instructions, agent_type)
		 VALUES ($1, $2, $3, '', NULL, 'local', '{}', $4, 'private', 'idle', 6, NULL,
		         '', 'coding')`,
		id, workspaceID, "coding-"+id[:8], runtimeID,
	)
	if err != nil {
		ts.t.Fatalf("CreateCodingAgent: %v", err)
	}
	return id
}

// CreateIssue inserts an issue row.
func (ts *TestDB) CreateIssue(workspaceID, title string) string {
	id := uuid.NewString()
	_, err := ts.Pool.Exec(ts.Ctx,
		`INSERT INTO issue (id, workspace_id, title, description, creator_type, creator_id)
		 VALUES ($1, $2, $3, '', 'member', $2)`,
		id, workspaceID, title,
	)
	if err != nil {
		ts.t.Fatalf("CreateIssue: %v", err)
	}
	return id
}

// EnqueueTaskForAgent creates a queued agent_task_queue row.
func (ts *TestDB) EnqueueTaskForAgent(agentID, issueID string) string {
	id := uuid.NewString()
	_, err := ts.Pool.Exec(ts.Ctx,
		`INSERT INTO agent_task_queue (id, agent_id, issue_id, status, priority)
		 VALUES ($1, $2, $3, 'queued', 2)`,
		id, agentID, issueID,
	)
	if err != nil {
		ts.t.Fatalf("EnqueueTaskForAgent: %v", err)
	}
	return id
}

func parseUUID(s string) pgtype.UUID {
	var id pgtype.UUID
	_ = id.Scan(s)
	return id
}

var _ = fmt.Sprintf // silence unused on platforms where fmt isn't otherwise needed
```

- [ ] **Step 3: Verify tests pass**

Run: `cd server && go test ./internal/service/ -run TestTestDB -v`
Expected: PASS if a test Postgres is reachable; `t.Skip` otherwise (confirmed by the NewTestDB guard).

- [ ] **Step 4: Commit**

```bash
git add server/internal/service/testutil.go server/internal/service/testutil_test.go
git commit -m "feat(service): add DB test helpers for integration tests

NewTestDB connects (or skips if unreachable), exposes Queries, and
registers cleanup. Builders: CreateWorkspace, CreateRuntime,
CreateLLMAgent, CreateCodingAgent, CreateIssue, EnqueueTaskForAgent.
Used by Task 20 (claim-by-provider) and every later-phase integration
test."
```

---

### Task 20: Task-Claiming by Provider (SQL + Service)

**Files:**
- Modify: `server/pkg/db/queries/agent.sql`
- Modify: `server/internal/service/task.go`
- Create: `server/internal/service/task_claim_test.go`

**Depends on:** Task 19.5 (DB Test Utilities) — test helpers must exist first.

**Goal:** cloud workers claim tasks by the `provider` column on the agent, not by `runtime_id` (which coding agents use). Adds a new query + service method. Preserves the existing coding-agent claim path.

- [ ] **Step 1: Write the failing test**

This test uses DB fixture helpers from Task 21 (`service.NewTestDB`). Running requires the test Postgres instance the project's `make test` already spins up via `pgvector/pgvector:pg17`.

```go
// server/internal/service/task_claim_test.go
package service

import (
	"context"
	"testing"
)

func TestClaimTaskForProvider_PicksLLMAPIAgents(t *testing.T) {
	t.Parallel()

	ts := NewTestDB(t)
	defer ts.Close()

	wsID := ts.CreateWorkspace()
	agentID := ts.CreateLLMAgent(wsID, "anthropic", "claude-sonnet-4-5")
	issueID := ts.CreateIssue(wsID, "ticket #42")
	ts.EnqueueTaskForAgent(agentID, issueID)

	svc := NewTaskService(ts.Queries, nil, nil)
	got, err := svc.ClaimTaskForProvider(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("ClaimTaskForProvider: %v", err)
	}
	if got == nil {
		t.Fatal("expected a task, got nil")
	}
	if got.AgentID != agentID {
		t.Errorf("AgentID = %v, want %v", got.AgentID, agentID)
	}
}

func TestClaimTaskForProvider_IgnoresCodingAgents(t *testing.T) {
	t.Parallel()

	ts := NewTestDB(t)
	defer ts.Close()

	wsID := ts.CreateWorkspace()
	runtimeID := ts.CreateRuntime(wsID, "anthropic")
	agentID := ts.CreateCodingAgent(wsID, runtimeID)
	issueID := ts.CreateIssue(wsID, "bug #17")
	ts.EnqueueTaskForAgent(agentID, issueID)

	svc := NewTaskService(ts.Queries, nil, nil)
	got, err := svc.ClaimTaskForProvider(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("ClaimTaskForProvider: %v", err)
	}
	if got != nil {
		t.Error("coding-agent task should NOT be claimable via provider path")
	}
}
```

- [ ] **Step 2: Add the SQL query**

In `server/pkg/db/queries/agent.sql`, append:

```sql
-- name: ClaimTaskForProvider :one
-- Claims the next queued task for any llm_api / http / process agent whose
-- provider matches $1. Coding agents (runtime_id NOT NULL) are excluded —
-- they use ClaimAgentTask via runtime_id. FOR UPDATE SKIP LOCKED makes
-- this safe under many concurrent workers.
UPDATE agent_task_queue atq
SET status = 'dispatched', dispatched_at = now()
WHERE id = (
    SELECT atq2.id FROM agent_task_queue atq2
    JOIN agent a ON a.id = atq2.agent_id
    WHERE atq2.status = 'queued'
      AND a.agent_type IN ('llm_api', 'http', 'process')
      AND a.provider = $1
      AND atq2.runtime_id IS NULL
      AND NOT EXISTS (
          SELECT 1 FROM agent_task_queue active
          WHERE active.agent_id = atq2.agent_id
            AND active.status IN ('dispatched', 'running')
            AND (
              (atq2.issue_id IS NOT NULL AND active.issue_id = atq2.issue_id)
              OR (atq2.chat_session_id IS NOT NULL AND active.chat_session_id = atq2.chat_session_id)
            )
      )
    ORDER BY atq2.priority DESC, atq2.created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;
```

- [ ] **Step 3: Regenerate sqlc**

Run: `cd server && make sqlc`
Expected: `ClaimTaskForProvider` method added to generated `Queries`.

- [ ] **Step 4: Add the service method**

Append to `server/internal/service/task.go`:

```go
// ClaimTaskForProvider claims the next runnable task for a cloud worker
// that supports the given LLM provider (e.g. "anthropic", "openai"). Matches
// any llm_api / http / process agent whose provider column equals p. Coding
// agents are not touched — those route via ClaimTaskForRuntime.
func (s *TaskService) ClaimTaskForProvider(ctx context.Context, p string) (*db.AgentTaskQueue, error) {
	task, err := s.Queries.ClaimTaskForProvider(ctx, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claim task for provider: %w", err)
	}

	// Respect agent-level concurrency ceiling.
	agent, err := s.Queries.GetAgent(ctx, task.AgentID)
	if err != nil {
		return nil, fmt.Errorf("agent not found: %w", err)
	}
	running, _ := s.Queries.CountRunningTasks(ctx, task.AgentID)
	if running >= int64(agent.MaxConcurrentTasks) {
		// Roll back: mark it queued again by calling CancelAgentTask is wrong;
		// easiest is to rely on the next run picking it up. Log and skip.
		slog.Debug("task claim: over concurrency; will re-claim next tick",
			"task_id", util.UUIDToString(task.ID), "agent_id", util.UUIDToString(task.AgentID))
		return nil, nil
	}

	s.updateAgentStatus(ctx, task.AgentID, "working")
	s.broadcastTaskDispatch(ctx, task)
	return &task, nil
}
```

- [ ] **Step 5: Verify build + unit tests**

Run:
```bash
cd server
go build ./...
go test ./internal/service/ -short -v
```
Expected: Build succeeds. Short-mode unit tests PASS; DB-gated tests skip.

- [ ] **Step 6: Commit**

```bash
git add server/pkg/db/queries/agent.sql server/pkg/db/generated/ \
        server/internal/service/task.go server/internal/service/task_claim_test.go
git commit -m "feat(service): add ClaimTaskForProvider for cloud workers

New SQL query scoped to agent.provider + agent_type IN (llm_api, http,
process). Excludes coding agents (they claim via runtime_id). Service
method re-applies the per-agent MaxConcurrentTasks ceiling. FOR UPDATE
SKIP LOCKED makes the query safe under concurrent workers."
```

---

### Task 21: Cloud Worker — Binary Skeleton + Config

**Files:**
- Create: `server/cmd/worker/main.go`
- Create: `server/internal/worker/config.go`
- Create: `server/internal/worker/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/internal/worker/config_test.go
package worker

import (
	"os"
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	t.Parallel()
	os.Unsetenv("WORKER_SLOTS")
	os.Unsetenv("WORKER_PROVIDERS")
	os.Unsetenv("WORKER_SERVER_URL")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Slots != 10 {
		t.Errorf("Slots = %d, want 10", c.Slots)
	}
	if len(c.Providers) != 2 || c.Providers[0] != "anthropic" {
		t.Errorf("Providers = %v", c.Providers)
	}
}

func TestLoadConfig_Overrides(t *testing.T) {
	t.Parallel()
	os.Setenv("WORKER_SLOTS", "4")
	os.Setenv("WORKER_PROVIDERS", "openai,google")
	defer os.Unsetenv("WORKER_SLOTS")
	defer os.Unsetenv("WORKER_PROVIDERS")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Slots != 4 {
		t.Errorf("Slots = %d, want 4", c.Slots)
	}
	if len(c.Providers) != 2 || c.Providers[1] != "google" {
		t.Errorf("Providers = %v", c.Providers)
	}
}

func TestLoadConfig_RejectsInvalidSlots(t *testing.T) {
	t.Parallel()
	os.Setenv("WORKER_SLOTS", "0")
	defer os.Unsetenv("WORKER_SLOTS")

	if _, err := LoadConfig(); err == nil {
		t.Error("expected error for Slots=0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/worker/ -run TestLoadConfig -v`
Expected: FAIL — package missing.

- [ ] **Step 3: Implement config loader**

```go
// server/internal/worker/config.go
// Package worker implements the cloud worker loop: poll tasks, execute via
// the appropriate Backend, report messages + result through the existing
// daemon HTTP protocol.
package worker

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is worker runtime config sourced from environment variables.
type Config struct {
	Slots       int      // concurrent tasks
	Providers   []string // LLM providers this worker supports
	ServerURL   string   // daemon API base URL
	AuthToken   string   // daemon auth token
	HealthAddr  string   // :8081 — where liveness/readiness probes listen
	DrainTimeoutSeconds int
}

func LoadConfig() (*Config, error) {
	c := &Config{
		Slots:       envInt("WORKER_SLOTS", 10),
		Providers:   envList("WORKER_PROVIDERS", []string{"anthropic", "openai"}),
		ServerURL:   envStr("WORKER_SERVER_URL", "http://localhost:8080"),
		AuthToken:   envStr("WORKER_AUTH_TOKEN", ""),
		HealthAddr:  envStr("WORKER_HEALTH_ADDR", ":8081"),
		DrainTimeoutSeconds: envInt("WORKER_DRAIN_TIMEOUT_SECONDS", 60),
	}
	if c.Slots <= 0 {
		return nil, fmt.Errorf("WORKER_SLOTS must be > 0, got %d", c.Slots)
	}
	if len(c.Providers) == 0 {
		return nil, fmt.Errorf("WORKER_PROVIDERS cannot be empty")
	}
	return c, nil
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envList(key string, def []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
```

- [ ] **Step 4: Add the binary entry point**

```go
// server/cmd/worker/main.go
// Worker runs outside the server process, polls for tasks matching its
// configured providers, executes them via agent.Backend implementations,
// and reports results through the existing daemon protocol.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/multica-ai/multica/server/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := worker.LoadConfig()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(2)
	}

	w, err := worker.New(cfg)
	if err != nil {
		slog.Error("worker init", "err", err)
		os.Exit(2)
	}

	// Expose /healthz + /readyz so k8s probes can route traffic correctly.
	health := worker.NewHealth()
	healthSrv := &http.Server{Addr: cfg.HealthAddr, Handler: health.Mux()}
	go func() {
		if err := healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health server", "err", err)
		}
	}()
	w.SetHealth(health) // worker flips MarkRegistered after successful daemon registration and MarkDraining on shutdown

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutdown signal received; draining")
		cancel()
	}()

	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("worker exited", "err", err)
		os.Exit(1)
	}

	// Graceful drain.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Duration(cfg.DrainTimeoutSeconds)*time.Second)
	defer drainCancel()
	if err := w.Drain(drainCtx); err != nil {
		slog.Warn("drain incomplete", "err", err)
		os.Exit(1)
	}
	_ = healthSrv.Shutdown(context.Background())
	slog.Info("worker stopped cleanly")
}
```

- [ ] **Step 5: Verify config tests pass**

Run: `cd server && go test ./internal/worker/ -run TestLoadConfig -v`
Expected: All 3 config tests PASS. `main.go` won't compile yet (references `worker.New` / `worker.Run` / `worker.Drain` which don't exist); that's fixed in Task 22.

- [ ] **Step 6: Commit**

```bash
git add server/cmd/worker/main.go server/internal/worker/config.go server/internal/worker/config_test.go
git commit -m "feat(worker): add binary entry point + config loader

Env-var config: WORKER_SLOTS, WORKER_PROVIDERS, WORKER_SERVER_URL,
WORKER_AUTH_TOKEN, WORKER_HEALTH_ADDR, WORKER_DRAIN_TIMEOUT_SECONDS.
SIGTERM triggers context cancel + drain. main.go expects Worker.New
/ Run / Drain — implemented in the next task."
```

---

### Task 22: Worker Core Loop (Poll-Execute-Report)

**Files:**
- Create: `server/internal/worker/worker.go`
- Create: `server/internal/worker/worker_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/internal/worker/worker_test.go
package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// fakeClient captures the daemon-protocol calls the worker makes.
// Task is defined in worker.go (production type, not a test-only shape).
type fakeClient struct {
	claims      atomic.Int32
	completions atomic.Int32
	failures    atomic.Int32
	nextTask    *Task
}

func (c *fakeClient) ClaimForProvider(_ context.Context, _ string) (*Task, error) {
	if c.nextTask == nil {
		return nil, nil
	}
	c.claims.Add(1)
	t := c.nextTask
	c.nextTask = nil
	return t, nil
}

func (c *fakeClient) StartTask(_ context.Context, _ string) error   { return nil }
func (c *fakeClient) CompleteTask(_ context.Context, _ string, _ string) error {
	c.completions.Add(1)
	return nil
}
func (c *fakeClient) FailTask(_ context.Context, _ string, _ string) error {
	c.failures.Add(1)
	return nil
}

// fakeBackend always returns a canned result.
type fakeBackend struct {
	output string
	err    error
}

func (f *fakeBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	msgs := make(chan agent.Message)
	result := make(chan agent.Result, 1)
	close(msgs)
	if f.err != nil {
		result <- agent.Result{Status: "failed", Error: f.err.Error()}
	} else {
		result <- agent.Result{Status: "completed", Output: f.output}
	}
	close(result)
	return &agent.Session{Messages: msgs, Result: result}, nil
}
func (f *fakeBackend) ExpiresAt() *time.Time { return nil }

func TestWorker_ExecutesAndReports(t *testing.T) {
	t.Parallel()

	client := &fakeClient{nextTask: &Task{ID: "t1", AgentID: "a1", Prompt: "say hi"}}

	w := &Worker{
		cfg:    &Config{Slots: 1, Providers: []string{"anthropic"}},
		client: client,
		resolveBackend: func(_ *Task) (agent.Backend, error) {
			return &fakeBackend{output: "hello"}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = w.Run(ctx)

	if client.claims.Load() != 1 {
		t.Errorf("claims = %d, want 1", client.claims.Load())
	}
	if client.completions.Load() != 1 {
		t.Errorf("completions = %d, want 1", client.completions.Load())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/worker/ -run TestWorker_ExecutesAndReports -v`
Expected: FAIL.

- [ ] **Step 3: Implement the worker**

```go
// server/internal/worker/worker.go
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Task is the minimal handle a worker needs for a claimed task. The production
// Client (Task 23.5) builds this from the daemon API response; tests construct
// it directly.
type Task struct {
	ID      string // task UUID
	AgentID string
	Prompt  string // derived from agent instructions + issue/chat context
}

// Client abstracts the daemon HTTP protocol. Production impl wraps
// server/internal/daemon/client.go (see Task 23.5); tests pass a fake.
type Client interface {
	ClaimForProvider(ctx context.Context, provider string) (*Task, error)
	StartTask(ctx context.Context, taskID string) error
	CompleteTask(ctx context.Context, taskID, output string) error
	FailTask(ctx context.Context, taskID, errMsg string) error
	Heartbeat(ctx context.Context, runtimeID string, activeTasks, freeSlots int) error
}

// BackendResolver returns the agent.Backend appropriate for a given task.
// Production resolver (Task 23.5) builds LLMAPIBackend / HTTPBackend /
// ProcessBackend from the task's agent row. Tests inject a canned backend.
type BackendResolver func(*Task) (agent.Backend, error)

type Worker struct {
	cfg            *Config
	client         Client
	resolveBackend BackendResolver
	health         *Health

	slots     chan struct{}
	inflight  sync.WaitGroup
	draining  bool
	drainMu   sync.Mutex
}

// New constructs a Worker. Production plumbing passes real Client +
// BackendResolver via SetClient / SetBackendResolver before Run; see cmd/worker/main.go.
func New(cfg *Config) (*Worker, error) {
	return &Worker{
		cfg:   cfg,
		slots: make(chan struct{}, cfg.Slots),
	}, nil
}

// SetClient, SetBackendResolver, SetHealth are wired by cmd/worker/main.go
// before Run is invoked. Tests assemble Worker literals directly.
func (w *Worker) SetClient(c Client)                 { w.client = c }
func (w *Worker) SetBackendResolver(r BackendResolver) { w.resolveBackend = r }
func (w *Worker) SetHealth(h *Health)                { w.health = h }

// Run blocks until ctx is cancelled, polling for tasks and dispatching
// each into a goroutine that executes + reports.
func (w *Worker) Run(ctx context.Context) error {
	if w.client == nil || w.resolveBackend == nil {
		return errors.New("worker: client and resolveBackend must be wired before Run")
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.drainMu.Lock()
			draining := w.draining
			w.drainMu.Unlock()
			if draining {
				continue
			}
			for _, p := range w.cfg.Providers {
				select {
				case w.slots <- struct{}{}:
					// claimed a slot; attempt a task
					go w.claimAndExecute(ctx, p)
				default:
					// slots full; skip this provider this tick
				}
			}
		}
	}
}

func (w *Worker) claimAndExecute(ctx context.Context, providerName string) {
	defer func() { <-w.slots }()

	task, err := w.client.ClaimForProvider(ctx, providerName)
	if err != nil {
		slog.Warn("claim failed", "provider", providerName, "err", err)
		return
	}
	if task == nil {
		return // no work
	}

	w.inflight.Add(1)
	defer w.inflight.Done()

	// D8 (PLAN.md §D8): mint trace_id at task entry so every downstream call
	// — backend.Execute, tool invocations, MCP calls (Phase 3), subagent
	// dispatches (Phase 6) — can read it from context and tag cost_event rows.
	traceID := uuid.New()
	ctx = agent.WithTraceID(ctx, traceID)

	backend, err := w.resolveBackend(ctx, task)
	if err != nil {
		_ = w.client.FailTask(ctx, task.ID, fmt.Sprintf("resolve backend: %v", err))
		return
	}

	_ = w.client.StartTask(ctx, task.ID)

	sess, err := backend.Execute(ctx, task.Prompt, agent.ExecOptions{})
	if err != nil {
		_ = w.client.FailTask(ctx, task.ID, err.Error())
		return
	}

	// Drain messages in parallel. Backends MUST close Messages when they're
	// done (invariant on the Backend contract — see agent.go). If a backend
	// leaks by not closing, the goroutine exits when Result is received and
	// the ctx is cancelled via the parent scope.
	msgDone := make(chan struct{})
	go func() {
		defer close(msgDone)
		for range sess.Messages {
			// TODO: Client.ReportMessages(ctx, task.ID, batch) — intentionally
			// not wired in Phase 2. Deferred to Phase 8A (Real-time Chat),
			// where the daemon protocol gains message-streaming over Redis
			// pub/sub. Until then messages are drained and discarded; the
			// final text result still reaches the frontend via CompleteTask.
		}
	}()

	r := <-sess.Result
	<-msgDone // ensure the drain goroutine exits before we complete the task

	if r.Status == "completed" {
		_ = w.client.CompleteTask(ctx, task.ID, r.Output)
	} else {
		_ = w.client.FailTask(ctx, task.ID, r.Error)
	}
}

// Drain waits for in-flight tasks to finish or ctx to expire.
func (w *Worker) Drain(ctx context.Context) error {
	w.drainMu.Lock()
	w.draining = true
	w.drainMu.Unlock()
	if w.health != nil {
		w.health.MarkDraining()
	}

	done := make(chan struct{})
	go func() {
		w.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("drain timed out: %w", ctx.Err())
	}
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/worker/ -v`
Expected: All worker tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/worker/worker.go server/internal/worker/worker_test.go
git commit -m "feat(worker): add poll-execute-report core loop

Buffered slot channel enforces concurrency limit. Each tick, claim
one task per configured provider. Drain flag blocks new claims while
letting in-flight tasks finish; Drain() waits on sync.WaitGroup with
ctx-deadline fallback."
```

---

### Task 22.5: Worker Heartbeat

**Files:**
- Create: `server/internal/worker/heartbeat.go`
- Create: `server/internal/worker/heartbeat_test.go`

**Why:** the existing daemon protocol expects registered runtimes to heartbeat periodically. Without this, the server's runtime sweeper marks the worker offline within minutes and stops dispatching. Task 22's core loop does not heartbeat — this task adds it as an independent goroutine.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/worker/heartbeat_test.go
package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type heartbeatCapturingClient struct {
	beats atomic.Int32
	lastActive atomic.Int32
	lastFree atomic.Int32
}

func (h *heartbeatCapturingClient) ClaimForProvider(_ context.Context, _ string) (*Task, error) { return nil, nil }
func (h *heartbeatCapturingClient) StartTask(_ context.Context, _ string) error                 { return nil }
func (h *heartbeatCapturingClient) CompleteTask(_ context.Context, _, _ string) error           { return nil }
func (h *heartbeatCapturingClient) FailTask(_ context.Context, _, _ string) error               { return nil }
func (h *heartbeatCapturingClient) Heartbeat(_ context.Context, _ string, active, free int) error {
	h.beats.Add(1)
	h.lastActive.Store(int32(active))
	h.lastFree.Store(int32(free))
	return nil
}

func TestHeartbeat_FiresOnInterval(t *testing.T) {
	t.Parallel()
	c := &heartbeatCapturingClient{}
	hb := NewHeartbeat(c, "runtime-1", 50*time.Millisecond, func() (active, free int) {
		return 2, 8
	})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	hb.Run(ctx)

	if got := c.beats.Load(); got < 3 || got > 6 {
		t.Errorf("beats = %d, want ~4 (within 250ms / 50ms)", got)
	}
	if c.lastActive.Load() != 2 || c.lastFree.Load() != 8 {
		t.Errorf("slot counts = (%d, %d)", c.lastActive.Load(), c.lastFree.Load())
	}
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/worker/heartbeat.go
package worker

import (
	"context"
	"log/slog"
	"time"
)

// SlotCounter reports live slot usage so the heartbeat payload is accurate.
type SlotCounter func() (active, free int)

// Heartbeat periodically calls Client.Heartbeat with current slot counts.
// Run blocks until ctx is cancelled. Failures are logged but never crash the
// worker — a lost heartbeat will be noticed by the server's sweeper, which
// is the intended recovery path.
type Heartbeat struct {
	client   Client
	runtimeID string
	interval time.Duration
	slots    SlotCounter
}

func NewHeartbeat(client Client, runtimeID string, interval time.Duration, slots SlotCounter) *Heartbeat {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Heartbeat{client: client, runtimeID: runtimeID, interval: interval, slots: slots}
}

func (h *Heartbeat) Run(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	// Fire one immediate beat so the server sees us as fresh on startup.
	h.beat(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.beat(ctx)
		}
	}
}

func (h *Heartbeat) beat(ctx context.Context) {
	active, free := h.slots()
	if err := h.client.Heartbeat(ctx, h.runtimeID, active, free); err != nil {
		slog.Warn("heartbeat failed", "runtime_id", h.runtimeID, "err", err)
	}
}
```

Also add slot-counter helper to Worker in Task 22:

```go
// Append to server/internal/worker/worker.go

// Slots returns current (active, free) counts for the heartbeat.
func (w *Worker) Slots() (active, free int) {
	used := len(w.slots)
	return used, w.cfg.Slots - used
}
```

And launch Heartbeat from Worker.Run:

```go
// Inside Worker.Run, before the poll loop:
if w.client != nil {
	hb := NewHeartbeat(w.client, w.cfg.RuntimeID, time.Duration(w.cfg.HeartbeatSeconds)*time.Second, w.Slots)
	go hb.Run(ctx)
}
```

Add `RuntimeID string` and `HeartbeatSeconds int` to `Config` (Task 21), defaulting `HeartbeatSeconds` to 30 and reading `WORKER_RUNTIME_ID` from env.

- [ ] **Step 3: Run tests**

Run: `cd server && go test ./internal/worker/ -run TestHeartbeat -v`
Expected: PASS. The test runs ~250ms and counts beats.

- [ ] **Step 4: Commit**

```bash
git add server/internal/worker/heartbeat.go server/internal/worker/heartbeat_test.go \
        server/internal/worker/worker.go server/internal/worker/config.go
git commit -m "feat(worker): add periodic heartbeat goroutine

30s default interval (WORKER_HEARTBEAT_SECONDS override). Calls
Client.Heartbeat with live active/free slot counts. Fires once
immediately at startup so the server's sweeper sees a fresh entry.
Logs but does not crash on heartbeat failures — lost beats fall
through to the sweeper's stale-runtime detection."
```

---

### Task 22.6: Retry with Backoff (Claim + Execute)

**Files:**
- Create: `server/internal/worker/backoff.go`
- Create: `server/internal/worker/backoff_test.go`

**Why:** PLAN.md §2.4 calls out "retry with backoff (port Cadence `common/backoff/` patterns): exponential backoff with jitter." Without this, a transient provider outage causes the worker to hammer the endpoint on every 500ms poll tick. Retries should back off exponentially with jitter.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/worker/backoff_test.go
package worker

import (
	"testing"
	"time"
)

func TestBackoff_StartsAtBaseDelay(t *testing.T) {
	t.Parallel()
	b := NewBackoff(100*time.Millisecond, 5*time.Second, 0.0) // no jitter for determinism
	d := b.Next()
	if d != 100*time.Millisecond {
		t.Errorf("first delay = %v, want 100ms", d)
	}
}

func TestBackoff_ExponentiallyGrows(t *testing.T) {
	t.Parallel()
	b := NewBackoff(100*time.Millisecond, 5*time.Second, 0.0)
	want := []time.Duration{100, 200, 400, 800, 1600, 3200, 5000, 5000}
	for i, w := range want {
		d := b.Next()
		if d != w*time.Millisecond {
			t.Errorf("step %d: delay = %v, want %dms", i, d, w)
		}
	}
}

func TestBackoff_ResetsOnSuccess(t *testing.T) {
	t.Parallel()
	b := NewBackoff(100*time.Millisecond, 5*time.Second, 0.0)
	_ = b.Next()
	_ = b.Next() // now at 200ms
	b.Reset()
	if d := b.Next(); d != 100*time.Millisecond {
		t.Errorf("after Reset: delay = %v, want 100ms", d)
	}
}

func TestBackoff_JitterStaysInBounds(t *testing.T) {
	t.Parallel()
	b := NewBackoff(100*time.Millisecond, 5*time.Second, 0.5) // ±50%
	for i := 0; i < 20; i++ {
		d := b.Next()
		// At step N the "nominal" value is 100ms * 2^N capped at 5s.
		// We only sanity-check d is positive and within the 50% band around
		// *some* power-of-two value ≤ max. Simpler: lower bound is base/2.
		if d < 50*time.Millisecond {
			t.Errorf("delay with jitter = %v is below 50ms floor", d)
		}
		if d > 8*time.Second {
			t.Errorf("delay with jitter = %v exceeds 8s ceiling", d)
		}
	}
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/worker/backoff.go
package worker

import (
	"math/rand/v2"
	"time"
)

// Backoff produces exponentially growing delays with optional jitter.
// Not safe for concurrent use — each caller that needs a backoff policy
// should hold its own instance.
type Backoff struct {
	base    time.Duration
	max     time.Duration
	jitter  float64 // 0..1; fraction of nominal delay added as ±jitter
	attempt int
}

// NewBackoff returns a backoff starting at `base`, doubling until `max`,
// with optional jitter (0 = deterministic, 0.5 = ±50%).
func NewBackoff(base, max time.Duration, jitter float64) *Backoff {
	return &Backoff{base: base, max: max, jitter: jitter}
}

// Next returns the delay for the current attempt and advances the counter.
// Callers do: time.Sleep(b.Next()) or use it with a time.Timer under ctx.
func (b *Backoff) Next() time.Duration {
	// Cap exponent to avoid Nanoseconds overflow (63 bits max).
	shift := b.attempt
	if shift > 32 {
		shift = 32
	}
	d := b.base * time.Duration(1<<shift)
	if d > b.max || d < 0 {
		d = b.max
	}
	b.attempt++

	if b.jitter > 0 {
		delta := float64(d) * b.jitter
		// rand.Float64 is uniform in [0,1); map to [-delta, +delta].
		d = time.Duration(float64(d) + (2*rand.Float64()-1)*delta)
		if d < b.base/2 {
			d = b.base / 2
		}
	}
	return d
}

// Reset returns the backoff to its initial state. Call after a successful
// operation so the next failure starts over at `base`.
func (b *Backoff) Reset() { b.attempt = 0 }
```

Wire into `Worker.claimAndExecute` (Task 22):

```go
// In worker.go, add a per-worker Backoff instance:
type Worker struct {
	...
	claimBackoff *Backoff
}

// In New():
	return &Worker{
		...
		claimBackoff: NewBackoff(500*time.Millisecond, 30*time.Second, 0.3),
	}, nil

// In claimAndExecute, after ClaimForProvider err:
	if err != nil {
		delay := w.claimBackoff.Next()
		slog.Warn("claim failed; backing off", "provider", providerName, "err", err, "delay", delay)
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
		return
	}
	if task == nil {
		return // no work — don't touch backoff
	}
	w.claimBackoff.Reset()
```

- [ ] **Step 3: Run tests**

Run: `cd server && go test ./internal/worker/ -run TestBackoff -v`
Expected: All 4 tests PASS.

- [ ] **Step 4: Commit**

```bash
git add server/internal/worker/backoff.go server/internal/worker/backoff_test.go \
        server/internal/worker/worker.go
git commit -m "feat(worker): add exponential backoff with jitter on claim failures

NewBackoff(500ms, 30s, 0.3) wired into Worker.claimAndExecute. Success
resets the counter; consecutive failures double the delay, capped at
30s with ±30% jitter. Prevents hammering the daemon endpoint during
server outages (port of Cadence common/backoff patterns, PLAN.md §2.4)."
```

---

### Task 23: Worker Health Probes + Graceful Shutdown

**Files:**
- Create: `server/internal/worker/health.go`
- Create: `server/internal/worker/health_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/internal/worker/health_test.go
package worker

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth_LivenessAlwaysOK(t *testing.T) {
	t.Parallel()
	h := NewHealth()
	srv := httptest.NewServer(h.Mux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHealth_ReadinessFailsWhenDraining(t *testing.T) {
	t.Parallel()
	h := NewHealth()
	h.MarkDraining()
	srv := httptest.NewServer(h.Mux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 503 (body=%s)", resp.StatusCode, body)
	}
}

func TestHealth_ReadinessOKWhenRegistered(t *testing.T) {
	t.Parallel()
	h := NewHealth()
	h.MarkRegistered()
	srv := httptest.NewServer(h.Mux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/worker/ -run TestHealth -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/internal/worker/health.go
package worker

import (
	"net/http"
	"sync/atomic"
)

// Health exposes k8s-style liveness (/healthz) and readiness (/readyz)
// probes. Readiness goes 503 during drain so k8s stops routing claim calls.
type Health struct {
	registered atomic.Bool
	draining   atomic.Bool
}

func NewHealth() *Health { return &Health{} }

func (h *Health) MarkRegistered() { h.registered.Store(true) }
func (h *Health) MarkDraining()   { h.draining.Store(true) }

// Mux returns an http.Handler with /healthz and /readyz wired up.
func (h *Health) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if h.draining.Load() {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("draining"))
			return
		}
		if !h.registered.Load() {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("not registered"))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ready"))
	})
	return mux
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/worker/ -run TestHealth -v`
Expected: All 3 health tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/worker/health.go server/internal/worker/health_test.go
git commit -m "feat(worker): add k8s liveness + readiness probes

/healthz always 200 if the process is up. /readyz is 503 during
drain (k8s stops sending traffic) and before successful daemon
registration. Required for HPA + rolling deploys."
```

---

### Task 23.5: Production Wiring (`Client`, `BackendResolver`, `CostSink`)

**Files:**
- Create: `server/internal/worker/client.go` — real `Client` wrapping `server/internal/daemon/client.go`
- Create: `server/internal/worker/resolver.go` — real `BackendResolver` that builds `LLMAPIBackend` / `HTTPBackend` / `ProcessBackend` from an agent row
- Create: `server/internal/worker/cost_sink.go` — real `agent.CostSink` that writes `cost_event` rows
- Create: `server/internal/worker/wiring_test.go` — integration smoke test (uses `NewTestDB` from Task 19.5)

**Why:** Tasks 22, 23, 22.5 all operate against interfaces (`Client`, `BackendResolver`, `CostSink`) with test fakes. Until the production implementations exist, nothing end-to-end runs. This task wires the interfaces into real server-side code so `make start` + `go run ./cmd/worker` actually execute agent tasks.

- [ ] **Step 1: Implement the Client wrapper**

```go
// server/internal/worker/client.go
package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon"
)

// daemonClient adapts server/internal/daemon/client.go to the worker's Client
// interface. The daemon client already handles auth, retry, and error shaping;
// this wrapper just translates the types.
type daemonClient struct {
	inner *daemon.Client
}

// NewClient wraps the existing daemon HTTP client.
func NewClient(baseURL, authToken string) Client {
	return &daemonClient{inner: daemon.NewClient(baseURL, authToken)}
}

func (c *daemonClient) ClaimForProvider(ctx context.Context, providerName string) (*Task, error) {
	t, err := c.inner.ClaimTaskForProvider(ctx, providerName)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	prompt, err := buildPromptFromTask(t)
	if err != nil {
		// Do NOT silently continue with an empty prompt — that would ship
		// garbage to the LLM and charge the workspace for nothing. Fail the
		// claim so the task falls back to the queue, visible to operators.
		_ = c.inner.FailTask(ctx, t.ID, fmt.Sprintf("prompt assembly: %v", err))
		return nil, fmt.Errorf("build prompt for task %s: %w", t.ID, err)
	}
	return &Task{
		ID:      t.ID,
		AgentID: t.Agent.ID,
		Prompt:  prompt,
	}, nil
}

func (c *daemonClient) StartTask(ctx context.Context, taskID string) error {
	return c.inner.StartTask(ctx, taskID)
}
func (c *daemonClient) CompleteTask(ctx context.Context, taskID, output string) error {
	return c.inner.CompleteTask(ctx, taskID, output, "", "", "")
}
func (c *daemonClient) FailTask(ctx context.Context, taskID, errMsg string) error {
	return c.inner.FailTask(ctx, taskID, errMsg)
}
func (c *daemonClient) Heartbeat(ctx context.Context, runtimeID string, active, free int) error {
	return c.inner.Heartbeat(ctx, runtimeID, active, free)
}

// buildPromptFromTask renders the final user-visible prompt from the task's
// issue/chat context. Kept minimal here — richer prompt assembly (memory
// injection, knowledge retrieval) lands in Phases 4/5.
//
// Returns an error rather than an empty string if no usable material is
// present, so callers fail the task loudly instead of spending tokens on
// a blank prompt.
func buildPromptFromTask(t *daemon.Task) (string, error) {
	// Chat tasks: the daemon populates ChatMessage with the latest user turn.
	if t.ChatMessage != "" {
		return t.ChatMessage, nil
	}
	// Issue tasks: assemble title + description + trigger-comment body
	// from the daemon's existing Context payload. These fields are already
	// populated by server/internal/service/task.go before the task is
	// returned to a worker.
	var b strings.Builder
	if t.IssueTitle != "" {
		fmt.Fprintf(&b, "# %s\n\n", t.IssueTitle)
	}
	if t.IssueDescription != "" {
		fmt.Fprintf(&b, "%s\n\n", t.IssueDescription)
	}
	if t.TriggerComment != "" {
		fmt.Fprintf(&b, "---\n%s\n", t.TriggerComment)
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("task %s has no chat message, title, description, or trigger comment", t.ID)
	}
	return b.String(), nil
}
```

**Integration note:** `daemon.Task` may not already expose `IssueTitle`, `IssueDescription`, and `TriggerComment` as top-level fields — today the daemon often returns a generic `Context` JSON blob. If that's the case, this commit also extends the daemon response shape (one of: (a) add the three fields to the daemon DTO and populate from `server/internal/service/task.go`; or (b) unmarshal `t.Context` inside `buildPromptFromTask` and pull the fields out of the JSON). Prefer (a) — keeps the worker side tidy and the contract explicit.

Note: `daemon.Client` methods may need small signature extensions (e.g. `ClaimTaskForProvider`, `Heartbeat`). If they don't already exist, add them in the same commit — the daemon client is a thin HTTP wrapper and extending it is a handful of lines per method.

- [ ] **Step 2: Implement the BackendResolver**

```go
// server/internal/worker/resolver.go
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	goagentants "github.com/digitallysavvy/go-ai/pkg/providers/anthropic"
	goopenai "github.com/digitallysavvy/go-ai/pkg/providers/openai"

	mcagent "github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/agent/harness"
	"github.com/multica-ai/multica/server/pkg/tools"
)

// ResolverDeps are the server-side dependencies the resolver needs.
type ResolverDeps struct {
	Pool      mcagent.CredentialPool // CredentialService from Task 5.5
	Registry  *tools.Registry        // built-in + MCP tools (Task 6)
	CostSink  mcagent.CostSink       // real sink, see cost_sink.go below
	GetAgent  func(ctx context.Context, agentID string) (*AgentRow, error)
}

// AgentRow is the minimal shape the resolver reads. Populated from
// db.Queries.GetAgent at resolve time.
type AgentRow struct {
	ID             string
	WorkspaceID    string
	AgentType      string // "llm_api" | "http" | "process"
	Provider       string
	Model          string
	RuntimeConfig  []byte // JSONB
	Instructions   string
}

// NewResolver returns a BackendResolver that builds the right concrete
// Backend based on the task's agent row.
func NewResolver(deps ResolverDeps) BackendResolver {
	return func(t *Task) (mcagent.Backend, error) {
		ag, err := deps.GetAgent(context.Background(), t.AgentID)
		if err != nil {
			return nil, fmt.Errorf("resolve agent %s: %w", t.AgentID, err)
		}

		switch ag.AgentType {
		case "llm_api":
			model, err := buildLanguageModel(deps.Pool, ag.Provider, ag.Model)
			if err != nil {
				return nil, err
			}
			tools, _ := deps.Registry.ForAgent(context.Background(), ag.WorkspaceID, ag.ID, nil)
			return mcagent.NewLLMAPIBackend(mcagent.LLMAPIBackendConfig{
				Model:        model,
				CostSink:     deps.CostSink,
				SystemPrompt: harness.BuildSystemPrompt(harness.PromptOptions{
					ModelID:            ag.Model,
					CustomInstructions: ag.Instructions,
				}),
				ProviderName: ag.Provider,
				ModelID:      ag.Model,
				Tools:        tools,
			}), nil

		case "http":
			var cfg mcagent.HTTPBackendConfig
			_ = json.Unmarshal(ag.RuntimeConfig, &cfg)
			return mcagent.NewHTTPBackend(cfg), nil

		case "process":
			var cfg mcagent.ProcessBackendConfig
			_ = json.Unmarshal(ag.RuntimeConfig, &cfg)
			return mcagent.NewProcessBackend(cfg), nil

		default:
			return nil, fmt.Errorf("resolver: unsupported agent_type %q", ag.AgentType)
		}
	}
}

// buildLanguageModel returns a go-ai LanguageModel wrapped with credential
// rotation. Production path: rotation wrapper → concrete provider model.
func buildLanguageModel(pool mcagent.CredentialPool, providerName, modelID string) (provider.LanguageModel, error) {
	factory := func(apiKey string) provider.LanguageModel {
		switch providerName {
		case "anthropic":
			p := goagentants.New(goagentants.Config{APIKey: apiKey})
			m, _ := p.LanguageModel(modelID)
			return m
		case "openai":
			p := goopenai.New(goopenai.Config{APIKey: apiKey})
			m, _ := p.LanguageModel(modelID)
			return m
		default:
			return nil
		}
	}
	return mcagent.NewRotatingModel(pool, providerName, factory), nil
}
```

- [ ] **Step 3: Implement the CostSink**

```go
// server/internal/worker/cost_sink.go
package worker

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	mcagent "github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// dbCostSink writes CostEvents to the cost_event table via sqlc.
type dbCostSink struct {
	q           *db.Queries
	workspaceID pgtype.UUID
	agentID     pgtype.UUID
	taskID      pgtype.UUID
}

// NewCostSink returns a CostSink scoped to a single task execution. The
// resolver (or worker) builds one per task with the correct IDs.
func NewCostSink(q *db.Queries, workspaceID, agentID, taskID string) mcagent.CostSink {
	return &dbCostSink{
		q:           q,
		workspaceID: parseUUIDOrZero(workspaceID),
		agentID:     parseUUIDOrZero(agentID),
		taskID:      parseUUIDOrZero(taskID),
	}
}

func (s *dbCostSink) Record(ctx context.Context, e mcagent.CostEvent) error {
	_, err := s.q.CreateCostEvent(ctx, db.CreateCostEventParams{
		WorkspaceID:      s.workspaceID,  // composite FK (workspace_id, agent_id) per PLAN.md §1.1 R3 S1
		AgentID:          s.agentID,
		TaskID:           s.taskID,
		Model:            e.Model,
		InputTokens:      pgtype.Int4{Int32: int32(e.InputTokens), Valid: true},   // non-cached only
		OutputTokens:     pgtype.Int4{Int32: int32(e.OutputTokens), Valid: true},
		CacheReadTokens:  pgtype.Int4{Int32: int32(e.CacheReadTokens), Valid: true},
		CacheWriteTokens: pgtype.Int4{Int32: int32(e.CacheWriteTokens), Valid: true},
		CacheTtlMinutes:  pgtype.Int4{Int32: int32(e.CacheTTLMinutes), Valid: e.CacheTTLMinutes > 0},
		CostCents:        int32(e.CostCents),
		CostStatus:       e.CostStatus,                              // 'actual' | 'estimated' | 'adjustment'
		TraceID:          pgtype.UUID{Bytes: e.TraceID, Valid: true}, // D8: NOT NULL
	})
	return err
}

func parseUUIDOrZero(s string) pgtype.UUID {
	var id pgtype.UUID
	_ = id.Scan(s)
	return id
}
```

- [ ] **Step 4: Smoke test the wiring end-to-end**

```go
// server/internal/worker/wiring_test.go
package worker

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

// Smoke test: NewResolver returns a Backend for an llm_api agent row.
// Doesn't actually call the LLM (that needs a real API key); stops after
// resolution succeeds.
func TestResolver_LLMAPIAgentResolvesToBackend(t *testing.T) {
	t.Parallel()

	ts := service.NewTestDB(t)
	defer ts.Close()

	wsID := ts.CreateWorkspace()
	agentID := ts.CreateLLMAgent(wsID, "anthropic", "claude-sonnet-4-5")

	// Minimal resolver deps for the smoke test.
	resolver := NewResolver(ResolverDeps{
		Pool: &stubPool{}, // defined in _test.go; always returns the same key
		Registry: nil,     // tools.Registry may be nil for llm_api resolution test
		CostSink: &stubSink{},
		GetAgent: func(_ context.Context, id string) (*AgentRow, error) {
			return &AgentRow{
				ID:          id,
				WorkspaceID: wsID,
				AgentType:   "llm_api",
				Provider:    "anthropic",
				Model:       "claude-sonnet-4-5",
			}, nil
		},
	})

	be, err := resolver(&Task{ID: "t1", AgentID: agentID})
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if be == nil {
		t.Fatal("resolver returned nil Backend")
	}
}

type stubPool struct{}
func (stubPool) NextCredential(_ context.Context, _ string) (string, string, error) {
	return "c1", "sk-fake", nil
}
func (stubPool) RecordRateLimit(_ context.Context, _ string) {}

type stubSink struct{}
func (stubSink) Record(_ context.Context, _ mcagent.CostEvent) error { return nil }
```

- [ ] **Step 5: Commit**

```bash
git add server/internal/worker/client.go \
        server/internal/worker/resolver.go \
        server/internal/worker/cost_sink.go \
        server/internal/worker/wiring_test.go
git commit -m "feat(worker): add production Client, BackendResolver, CostSink

Wires the fake interfaces from Tasks 22/22.5 to real server-side code:
- daemonClient wraps server/internal/daemon/client.go
- resolver builds LLMAPIBackend/HTTPBackend/ProcessBackend from
  agent row, injecting CredentialService + tool registry + cost sink
- dbCostSink writes CreateCostEvent rows with cost_status='actual'

Wiring test via NewTestDB confirms resolver returns a non-nil Backend
for an llm_api agent without calling the LLM."
```

---

### Task 24: End-to-End Verification

**Files:** None (verification only). This task mirrors Phase 1 Task 19: runs the full suite, verifies backwards compat, closes the phase.

- [ ] **Step 1: Run make sqlc**

Run: `cd server && make sqlc`
Expected: No changes (already regenerated in Task 20).

- [ ] **Step 2: Run all Go tests**

Run: `cd server && go test ./... -count=1 -short`
Expected: All new `pkg/agent/`, `pkg/agent/harness/`, `pkg/tools/`, `pkg/router/`, `pkg/redis/`, `internal/worker/`, `internal/service/` tests PASS. Existing suites still green.

- [ ] **Step 3: TypeScript checks**

Run: `pnpm typecheck && pnpm test`
Expected: PASS. (Phase 2 is backend-heavy; no TS changes required, but regressions from Phase 1 shouldn't surface.)

- [ ] **Step 4: Full `make check`**

Run: `make check`
Expected: Typecheck + unit tests (Go + TS) + E2E all green.

- [ ] **Step 5: Manual smoke test — create and run an llm_api agent**

1. Start server + worker:
   ```bash
   make start                        # starts server + postgres + redis
   # in another terminal:
   ANTHROPIC_API_KEY=sk-... WORKER_PROVIDERS=anthropic \
     WORKER_SERVER_URL=http://localhost:8080 WORKER_AUTH_TOKEN=... \
     go run ./cmd/worker
   ```
2. In the web UI: create an agent with `Agent Type = AI Agent`, provider `anthropic`, model `claude-sonnet-4-5`.
3. Assign an issue to it.
4. Expected:
   - Worker claims the task within 1-2 seconds.
   - Messages stream to the issue's task view.
   - `cost_event` row inserted with non-zero `cost_cents` and correct `input_tokens`/`output_tokens`.
   - Task status transitions `queued → dispatched → running → completed`.

- [ ] **Step 6: Verify backwards compatibility**

1. Existing coding agents still work through the local daemon (no change to that claim path).
2. `ClaimTaskForProvider` does NOT claim coding-agent tasks (SQL predicate excludes `runtime_id IS NOT NULL`).
3. Task queue stays non-duplicate: same issue assigned to same agent does not create a second running task.

- [ ] **Step 7: Verify graceful shutdown**

With a running task in flight, `kill -TERM` the worker process. Expected:
- Worker logs "shutdown signal received; draining".
- In-flight task completes.
- Worker exits cleanly within `WORKER_DRAIN_TIMEOUT_SECONDS`.

---

## Phase Plan Index

This is Phase 2 of 12. See `PLAN.md § Dependency Graph`. With Phase 2 complete, the critical execution path is live: Phase 1 + 2 together let the platform run business agents end-to-end. Phases 3-12 layer capability on top.

**Effort revision after review:** the original 2.5-week estimate assumed the `CredentialPool` service, DB test utilities, heartbeat, retry-with-backoff, and production wiring were implicit. Making them explicit (Tasks 5.5, 19.5, 22.5, 22.6, 23.5) doesn't add scope — it surfaces effort that was previously hidden. Revised estimate: **2.5-3 weeks**.

| Phase | Name | Depends On | Estimated Effort |
|-------|------|-----------|-----------------|
| 1 | Agent Type System & Database Foundation | — | 2.5 weeks |
| **2** | **Harness + LLM API + Tools + Router + Worker (29 tasks)** | **Phase 1** | **2.5-3 weeks** |
| 3 | MCP Integration + Skills Registry | Phase 2 | 1.5 weeks |
| 4 | Structured Outputs + Guardrails + Knowledge/RAG | Phase 2 | 2 weeks |
| 5 | Agent Memory + Context Compression | Phases 2, 4 | 2 weeks |
| 6 | A2A via task tool (SubagentRegistry) | Phases 1, 2, 4, 5 | 1 week |
| 7 | Workflow Orchestration + Scheduler | Phases 4, 6 | 3 weeks |
| 8A | Platform Integration | Phases 2, 7 | 4 weeks |
| 8B | Security & Compliance | Phases 2, 7 | 4 weeks |
| 9 | Advanced Cost Optimization + Metrics | Phases 5-8 | 3 weeks |
| 10 | Observability + Execution Search | Phases 6, 7 | 2 weeks |
| 11 | Cloud Coding Sandbox (E2B + gVisor) | Phases 2, 10 | 3 weeks |
| 12 | Claude Managed Agents Backend | Phases 2, 10 | 2 weeks |

## Self-Review Against PLAN.md § Phase 2

**Execution order:** tasks intentionally don't form a strict linear chain — several blocks are parallelisable. Suggested order if executing sequentially:
`1 → 2 → 3 → 4 → 5 → 5.5 → 7 (which depends on 5.5) → 6 → 8 → 9 → 10 → 11 → 12 → 13 → 14 → 15 → 16 → 17 → 18 → 19 → 19.5 → 20 → 21 → 22 → 22.5 → 22.6 → 23 → 23.5 → 24`.

**Spec coverage (PLAN.md §2.1-2.5):**
| Spec item | Covered by | Status |
|---|---|---|
| §2.1 Harness adapter | Tasks 1, 4, 5, 19 | ✅ |
| §2.1 Backend.ExpiresAt + Hooks (D6) | Task 1 | ✅ |
| §2.1.1 Fallback chain | Task 13 | ✅ |
| §2.1.1 Provider health tracking | Task 12 | ✅ |
| §2.1.1 HTTP backend | Task 17 | ✅ |
| §2.1.1 Process backend | Task 18 | ✅ |
| §2.2 Tool registry | Task 6 | ✅ |
| §2.2 Four built-in tools | Tasks 7-10 | ✅ |
| §2.3 Model router + cost | Tasks 14, 15 | ✅ |
| §2.4 Cloud worker binary | Tasks 21, 22 | ✅ |
| §2.4 Heartbeat | Task 22.5 | ✅ |
| §2.4 Retry with backoff | Task 22.6 | ✅ |
| §2.4 Fair scheduling (`FOR UPDATE SKIP LOCKED`) | Task 20 | ✅ |
| §2.4 Claim by provider | Task 20 | ✅ |
| §2.4 Health probes + graceful shutdown | Task 23 (+ main.go startup) | ✅ |
| §2.5 Verification | Task 24 | ✅ |
| D4 Redis baseline | Task 2 | ✅ |
| Credential vault service | Task 5.5 | ✅ |
| Credential rotation wrapper | Task 11 | ✅ |
| Budget check | Task 16 | ✅ |
| Production CostSink | Task 23.5 | ✅ |
| Production Client | Task 23.5 | ✅ |
| Production BackendResolver | Task 23.5 | ✅ |
| DB test utilities | Task 19.5 | ✅ |

**Deferred follow-ups** (non-blocking for Phase 2 completion — tracked as separate PRs or absorbed into later phases):
- **Embedded-worker mode** (`--with-worker` flag on server binary) — PLAN.md §2.4 "Scaling" calls it out but it's an optimisation for small deploys, not a prerequisite for Phase 2's cloud-worker topology.
- **`ReportTaskMessages` streaming glue** — Task 22's message-drain loop has a TODO; production messages don't stream to the frontend until this is wired. Small glue task, can land in Phase 8A (real-time chat) or earlier as polish.
- **Built-in tool infrastructure wiring**: the `Mailer` interface (`search.go` Brave API key, `email.go`), and `QueryRunner` (`database_query.go`) all need production implementations sourced from the credential vault. Each is small but out of scope for Phase 2 — add when the first workspace actually configures one of these tools.
- **Daemon client method extensions** — if `ClaimTaskForProvider` and `Heartbeat` aren't already on `server/internal/daemon/client.go`, Task 23.5 Step 1 includes the ~5-line additions. Call out as a follow-up commit only if daemon client hygiene deserves its own PR.
- **Richer prompt assembly** (`buildPromptFromTask` in Task 23.5) is deliberately minimal; memory injection + knowledge retrieval land in Phases 4/5.

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-14-phase2-harness-llm-backend-worker.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**

# PLAN.md Review — Porting Fidelity & Source Accuracy (Reviewer 2 of 3)

## Summary verdict

Porting claims are **mostly accurate but citation hygiene is uneven**. The four core sources I audited in depth — CrewAI memory, Hermes agent, go-ai v0.4.0, Open Agents — materially exist and their claimed primitives are real and faithfully quotable. However, **three file-path citations are wrong** (Paperclip), **two port sources are missing entirely from disk** (Letta/MemGPT, LangGraph), and the **Sandbox interface is materially mis-transcribed** (optional methods declared mandatory, a literal type mis-stated, two methods missing). The Go translation is viable for CrewAI/Open Agents/Hermes concepts, but Cadence's scheduler and IWF's channels/WaitUntil assume durable-execution primitives that a Chi+pgx service does not have — the plan acknowledges this at §997 but underestimates the reimplementation cost in estimated weeks.

## Source-by-source verification

### CrewAI
- **Status: VERIFIED**
- Files checked:
  - `lib/crewai/src/crewai/memory/unified_memory.py` — class `Memory` with `llm`, `storage`, `embedder`, `recency_weight=0.3`, etc.
  - `lib/crewai/src/crewai/memory/encoding_flow.py` — batch pipeline with the 5 steps the plan quotes
  - `lib/crewai/src/crewai/memory/recall_flow.py` — `RecallFlow` with `compute_composite_score`
  - `lib/crewai/src/crewai/memory/analyze.py` — `extract_memories_from_content`, `analyze_for_save`, `analyze_for_consolidation`
  - `lib/crewai/src/crewai/memory/types.py:144–184` — composite formula doc, `recency_half_life_days: int = 30`
  - `lib/crewai/src/crewai/memory/memory_scope.py` — hierarchical scope API
  - `lib/crewai/src/crewai/tools/memory_tools.py`
- **Findings:** Plan §795 formula `0.5*similarity + 0.3*recency + 0.2*importance` **matches the source defaults exactly** (`semantic_weight=0.5, recency_weight=0.3, importance_weight=0.2`). Plan §796 30-day half-life matches `recency_half_life_days: int = 30`. The pipeline steps at §768–776 (extract → infer → embed → dedup → consolidate → persist) accurately mirror `encoding_flow.py` (step 2 intra-batch dedup, step 3 parallel find similar with `top_similarity`, step 4 parallel analyze with `ConsolidationPlan` {keep / update / delete / insert_new}). Plan §774 cosine `> 0.85` matches `consolidation_threshold: float = default=0.85` in `types.py:188`. Plan §774 intra-batch `>= 0.98` cosine is the documented `dedup_similarity_threshold` default. These ports are tightly faithful.
- **One caveat:** CrewAI uses Python `concurrent.futures.ThreadPoolExecutor` + `contextvars` heavily for parallel similarity search and parallel analyze. Translating step 3 ("parallel find similar") and step 4 ("N concurrent LLM calls") is trivial in Go (goroutines + `errgroup`), but the `Flow` abstraction (`from crewai.flow.flow import Flow, listen, start`) is pydantic-state-transition sugar that has no Go equivalent — the port will need an explicit state machine.

### Paperclip
- **Status: PARTIAL — content exists, three file paths in the plan are wrong**
- Files checked:
  - `cli/src/adapters/http/index.ts`, `cli/src/adapters/process/index.ts` — CLI-side adapters
  - `server/src/adapters/http/execute.ts` — exists (plan §486 effectively correct)
  - `server/src/adapters/process/execute.ts` — exists (plan §489 correct)
  - `packages/adapters/{claude-local,codex-local,cursor-local,gemini-local,opencode-local,openclaw-gateway,pi-local}/` — agent-CLI adapters
  - `packages/db/src/schema/{activity_log,agent_config_revisions,approvals,budget_policies,cost_events,company_secrets,company_secret_versions}.ts` — all exist
- **Findings:** The *concepts* (adapter architecture, budgets, approvals, cost_events, activity_log, agent_config_revisions, secrets vault) are all real. Translation to Go/sqlc is straightforward. But three plan-level file citations are **wrong or fabricated**:
  1. §248 cites `packages/db/src/schema/secrets.ts` — **this file does not exist**. The actual schemas are `company_secrets.ts` + `company_secret_versions.ts`.
  2. §484 cites `packages/adapters/http/execute.ts` — **this file does not exist**. The HTTP adapter is at `cli/src/adapters/http/index.ts` (CLI) or `server/src/adapters/http/execute.ts` (server). There is no `packages/adapters/http/`.
  3. The `packages/adapters/` tree exists but contains coding-CLI adapters (claude-local, codex-local, etc.), not http/process adapters — reader will hunt for nothing.

### Cadence
- **Status: PARTIAL — overlap-policy names accurate; scheduler-port scope wildly underestimated**
- Files checked:
  - `service/worker/scheduler/{activity,types,workflow,client_worker}.go`
  - `common/backoff/{cron,jitter,retry,retrypolicy}.go`
  - `common/quotas/{multistageratelimiter,dynamicratelimiter,collection}.go`
  - `common/mapq/{mapq,README.md,client_impl.go,tree/queue_tree.go}`
  - `common/types/schedule.go:37–42` — `ScheduleOverlapPolicy{SkipNew, Buffer, Concurrent, CancelPrevious, TerminatePrevious}`
  - `common/types/schedule.go:85–100` — `ScheduleCatchUpPolicy{Invalid, Skip, One, All}`
- **Findings:**
  - **Overlap policies (plan §1028) — VERIFIED:** `skip_new | buffer | concurrent | cancel_previous | terminate_previous` map 1:1 to `ScheduleOverlapPolicy{SkipNew,Buffer,Concurrent,CancelPrevious,TerminatePrevious}`. Naming is exact.
  - **Catch-up policies (plan §1029) — INACCURATE:** plan names them `skip_missed | fire_one | fire_all`. Real names in `common/types/schedule.go` are `SKIP | ONE | ALL` (backed by `ScheduleCatchUpPolicy{Skip,One,All}`). The plan invented `skip_missed`, `fire_one`, `fire_all` — these strings do not appear anywhere in Cadence. Low stakes but it looks synthesized.
  - **Backoff patterns — VERIFIED:** `common/backoff/retry.go`, `retrypolicy.go`, `jitter.go` are there and are a clean reference; porting to Go is trivial (already Go, could be copy-pasted).
  - **Quotas — VERIFIED:** `multistageratelimiter.go` is real. `common/quotas/global/` is Cadence's distributed rate-limiter with RPC round-trips to coordinate limits across pods — the plan's §1397 "multi-stage rate limiter" port assumes single-node; reusing Cadence's whole design would require their RPC mesh. Keep the multistage concept, drop the global/.
  - **MAPQ — EXISTS BUT BIG:** `common/mapq/` is real fair-scheduling but it's tightly coupled to Cadence's `common/persistence/tasks.go` task-manager interface. Plan §568 mentions MAPQ concept only ("fair scheduling"); that's the right scope — don't port the code.
  - **Scheduler (plan §1023–1031) — HIGH-RISK PORT:** Cadence's `service/worker/scheduler/` is itself a Cadence workflow (runs on `go.uber.org/cadence/workflow`). `workflow.go:345` uses `workflow.Now()` etc. Porting to a plain Go cron means reimplementing the durable-history replay that makes overlap enforcement safe across restarts. The plan's "durable execution (port Cadence pattern): all state persisted to DB, engine can resume after restart by replaying step_run statuses" (§1005) sketches the idea but understates it — without an event-sourced history, `terminate_previous` during a scheduler-node crash can lose its cancel-signal and leak runs. This is at least a 2-person-month sub-project, not absorbed inside the quoted "3 weeks" for Phase 7.

### Hermes Agent
- **Status: VERIFIED — every file cited exists; the code genuinely contains the claimed behavior**
- Files checked:
  - `agent/error_classifier.py:1–80` — `FailoverReason` enum with exactly the members the plan lists (auth / auth_permanent / billing / rate_limit / overloaded / server_error / timeout / context_overflow / payload_too_large / model_not_found / format_error / unknown) plus extras (thinking_signature, long_context_tier). The recovery-hint fields (`retryable`, `should_compress`, `should_rotate_credential`, `should_fallback`, `backoff_seconds`) are verified on `ClassifiedError`.
  - `agent/credential_pool.py:61–70` — `STRATEGY_FILL_FIRST | ROUND_ROBIN | RANDOM | LEAST_USED`, all four names match plan §207 byte-for-byte.
  - `agent/prompt_builder.py:36–73` — the cited line range is exact. `_CONTEXT_THREAT_PATTERNS` (prompt_injection, deception_hide, html_comment_injection, hidden_div, exfil_curl, etc.) and `_CONTEXT_INVISIBLE_CHARS` (U+200B, U+200D, U+2060, U+FEFF, bidi overrides). Plan's claim at §666 is accurate.
  - `agent/smart_model_routing.py` — `choose_cheap_model_route()` auto-routes simple messages to a declared `cheap_model`. Plan §525 claim accurate.
  - `agent/usage_pricing.py` — `cache_read_cost_per_million`, `cache_write_cost_per_million` fields and per-model pricing records. Plan's "cache reads at 0.1x, cache writes at 1.25x" ratios (§529) match the Anthropic published rates recorded in that module.
  - `agent/rate_limit_tracker.py` — x-ratelimit header parsing exactly as plan §1398 describes.
  - `agent/context_compressor.py` — exists, pluggable, iterative, token-budget-aware (matches plan §19 claim).
  - `tools/mcp_tool.py` — **exists** (reconnection/backoff patterns). Plan §599 correct.
- **Findings:** This is the tightest-audit source in the plan. Translation to Go is straightforward (regex scanning, rotation state machine, cooldown maps, pricing tables) — none of the cited behaviors rely on Python-specific dynamism.

### IWF
- **Status: VERIFIED in concept, HIGH-RISK in port**
- Files checked:
  - `gen/iwfidl/model_persistence_loading_policy.go`, `model_persistence_loading_type.go` — REST API types for loading policies
  - `gen/iwfidl/model_workflow_skip_timer_request.go` — timer-skip API endpoint
  - `gen/iwfidl/model_workflow_conditional_close.go` — conditional-close model
  - `service/interpreter/InternalChannel.go` — channel-data map with `HasData`/publish/consume
  - `service/interpreter/workflowImpl.go` — WaitUntil/Execute dispatch in interpreter
  - `service/interpreter/temporal/`, `service/interpreter/cadence/` — two pluggable backends
- **Findings:** All five patterns (WaitUntil/Execute, timer-skip, persistence-loading-policy, internal channels, conditional close) are real and named in the source. The plan's §22 caveat ("NOT using IWF itself (it requires Temporal) — porting patterns only") is accurate — `service/interpreter/*/worker.go` both need a Temporal or Cadence SDK. **However, IWF's `InternalChannel` depends on the outer workflow being durable** — channel payloads live in workflow state and survive replay. Porting this to a plain Go-in-process primitive (plan §1018 "Backed by workflow_run state JSONB + DB notifications") loses exactly-once consume semantics. If two steps both wait on the same channel and the server restarts between "published" and "consumed", you get a duplicate. The plan should acknowledge this is at-least-once by default and add deduplication guidance.

### Open Agents
- **Status: PARTIAL — primitives verified, Sandbox interface mis-transcribed**
- Files checked:
  - `packages/sandbox/interface.ts` — full read, compared to plan §45 and §1745–1779
  - `packages/agent/context-management/aggressive-compaction-helpers.ts` — exists, referenced in §831
  - `packages/shared/lib/paste-blocks.ts` — exists
  - `packages/agent/types.ts:39` — `EVICTION_THRESHOLD_BYTES = 80 * 1024` (verified — matches plan §59)
  - `packages/sandbox/vercel/sandbox.ts:16` — `TIMEOUT_BUFFER_MS = 30_000` (verified — matches plan §60)
  - `apps/web/app/workflows/chat.ts:772, 973` — `startStopMonitor` implementation (verified — matches plan §61)
  - `activeStreamId` — present throughout `apps/web/` (verified)
- **Findings — Sandbox interface does NOT match the plan:**
  1. **Plan §45 / §1745–1772 drops two methods:** the real interface has `access(path)` and `getState()`. Neither appears in the Go skeleton at §1752–1772.
  2. **Plan declares optional methods as mandatory.** TS source marks `execDetached?`, `extendTimeout?`, `snapshot?`, `domain?`, `getState?` as optional (question-mark). Plan's Go interface makes them required. Not a semantic error — in Go you'd lift them to optional by splitting the interface — but it misrepresents the contract a second backend must satisfy.
  3. **Plan invents a `SandboxType` union.** Real source: `export type SandboxType = "cloud";` (literally one member). Plan §1738–1743 claims `TypeE2B | TypeGVisor | TypeClaudeManaged | TypeLocalDaemon`. This is fine as a design choice (we're widening it) but it is NOT "lifted verbatim" as §1723 claims.
  4. **`Domain` signature is different.** TS: `domain?(port: number): string`. Plan §1768: `Domain(port int) (string, error)` — adding an error return is fine, but again not "verbatim".
  5. **Hooks names match** — `AfterStart / BeforeStop / OnTimeout / OnTimeoutExtended` all verified.
- The compaction algorithm (§831–836) faithfully mirrors `aggressive-compaction-helpers.ts` (index tool calls by `messages[i].content[j]`, find candidates, estimate savings by `chars/4`, apply `ClearToolUsesEdit`). Paste-block tokens from U+E000–U+F8FF match `paste-blocks.ts` conventions.

### go-ai
- **Status: VERIFIED (v0.4.0)**
- Files checked:
  - `pkg/agent/toolloop.go:69` — `type ToolLoopAgent struct` exists
  - `pkg/agent/skill.go:36` — `type SkillRegistry struct`
  - `pkg/agent/subagent.go:12` — `type SubagentRegistry struct`
  - `pkg/ai/stop_condition.go:11,26` — `StopCondition` + `StepCountIs(n int)`
  - `pkg/provider/types/reasoning.go:11–34` — `ReasoningLevel` + `ReasoningDefault/None/Minimal/Low/Medium/High/XHigh`
  - `pkg/providers/anthropic/context_management.go:25, 67, 99` — `ClearToolUsesEdit`, `ClearThinkingEdit`, `CompactEdit` all real structs with builder methods
  - `pkg/mcp/{client,transport,http_transport,stdio_transport,oauth,jsonrpc,integration}.go` — full MCP package exists
  - `pkg/ai/output.go` — `ObjectOutput[T]`, `ArrayOutput[ELEMENT]`, `ChoiceOutput[CHOICE]` returning `Output[OBJECT,PARTIAL]`
  - `release_notes/RELEASE_NOTES_V0.4.0.md:22–27` — explicit breaking change: "Tools are now executed **after** the stream is fully consumed, not mid-stream."
- **Findings:** Every single go-ai claim in the plan (§69–78, §441+, §509, §591+, §829+, §881+) is real in v0.4.0. Plan §73 shorthand `Output[T, P]` is accurate for the actual generic signature `Output[OBJECT, PARTIAL]`. The "sequential after stream ends" claim at §509 is literally called out in the v0.4.0 release notes as a breaking change. **This is the most reliably-cited source in the plan.**

### Letta / MemGPT
- **Status: NOT FOUND on disk**
- Files checked: `Glob D:/proptech/ai-agent-colab/other-projects/letta*`, `memgpt*`, plus full-text grep for `letta|memgpt|three.?tier|core.?memory|archival` across `other-projects/` — zero matches in any source.
- **Findings:** Plan §15 and §20 and §723 repeatedly reference "Letta/MemGPT three-tier memory". The source code is **not in the repo**. The plan is effectively citing a public-paper pattern (MemGPT ICML 2024 / Letta docs) as if it were a local port source. The plan is honest enough that §720 talks in concepts only — but it's listed alongside CrewAI/Open Agents as "Projects Analyzed" and it shouldn't be. Either vendor in `other-projects/letta/` or change §14–26 to say "design patterns from public Letta/MemGPT literature".

### LangGraph
- **Status: NOT FOUND on disk**
- Files checked: `Glob D:/proptech/ai-agent-colab/other-projects/langgraph*` — zero results.
- **Findings:** Plan §15 and §21 say "LangGraph — Graph-based state machines, checkpoint/resume, interrupt/resume for HITL" and §21 maps this to Phase 7. Like Letta, **LangGraph is not a local port source**. Same remediation: either vendor it or drop the claim of "porting" and state it's a design inspiration.

### LangChainGo (declined)
- **Status: VERIFIED — decline rationale accurate for the latest upstream**
- Files checked:
  - `tools/tool.go:6–10` — `Tool interface { Name() string; Description() string; Call(ctx context.Context, input string) (string, error) }`
  - `agents/mrkl.go`, `agents/executor.go`, `agents/openai_functions_agent.go`
- **Findings:** Plan §78's four-part criticism ("string-in/string-out tool interface, synchronous executor, no mid-stream tool events, no subagent primitive") is factually correct against the current langchaingo: `Call` returns `(string, error)` only; `agents/executor.go` is synchronous `Run`-loop; no streaming callbacks; no subagent primitive. The decline is fair.

## Blocking issues

### B1. Letta/MemGPT and LangGraph are cited as port sources but aren't in the repo
- **PLAN.md cite:** §14–26 ("Projects Analyzed"), §723 ("Port from: CrewAI unified memory + Hermes context compression + Letta three-tier model"), Phase 7 depends-on comments
- **Claim:** These are listed alongside CrewAI / Hermes / Open Agents / go-ai as source projects we port from.
- **Reality:** Neither `letta/` nor `langgraph/` exists under `other-projects/`. The plan nowhere cites a specific file from either — because it can't.
- **Fix:** In the §14 table, split the column into "Concepts ported from public literature" vs "Code ported from vendored source". Move Letta, LangGraph (and arguably Replit, Cursor, Claude Managed Agents — none of which exist on disk either) into the concepts column. Remove the §723 "Port from Letta three-tier model" line or rewrite as "inspired by the MemGPT paper".

### B2. Open Agents Sandbox interface is not "lifted verbatim" — it's materially edited
- **PLAN.md cite:** §45 and §1721–1779
- **Claim:** §1723 "Sandbox interface lifted verbatim from Open Agents' `packages/sandbox/interface.ts`".
- **Reality:** (a) plan drops `access()` and `getState()`; (b) plan makes five `?`-optional methods mandatory; (c) plan invents a 4-value `SandboxType` where the source has one; (d) `Domain` gains a Go-idiomatic error return. None of this is "verbatim".
- **Fix:** Rewrite §1723 as "Sandbox interface derived from Open Agents' `packages/sandbox/interface.ts`, with Go-idiomatic changes: error returns on fallible methods, four-way SandboxType to support local+cloud backends, optional methods lifted to a separate `OptionalSandbox` interface." Add the missing `Access(ctx, path)` and `GetState() any` methods to the Go interface.

### B3. Paperclip file paths are wrong in three places
- **PLAN.md cite:** §248, §484 (and by extension §486 wording)
- **Claim:** §248 `packages/db/src/schema/secrets.ts`; §484 `packages/adapters/http/execute.ts`.
- **Reality:** Those paths don't exist. Correct paths are `packages/db/src/schema/{company_secrets,company_secret_versions}.ts` and `cli/src/adapters/http/index.ts` (plus `server/src/adapters/http/execute.ts`).
- **Fix:** Update §248 to `packages/db/src/schema/company_secrets.ts + company_secret_versions.ts`. Update §484 to `server/src/adapters/http/execute.ts` (which is correctly used in §489 for the process adapter).

## Should-fix issues

### S1. Cadence catch-up policy names are fabricated
- **PLAN.md cite:** §1029
- **Claim:** `skip_missed`, `fire_one`, `fire_all` (port from Cadence).
- **Reality:** Cadence has `SKIP`, `ONE`, `ALL` (`common/types/schedule.go:85–100`).
- **Fix:** Either rename to match Cadence literals (`skip | one | all`) and note the historical origin, or drop the "port from Cadence" claim and call them Multica-native names.

### S2. Cadence scheduler port underestimated — not absorbable by §1005 sketch
- **PLAN.md cite:** §997–1031 and §1005 "Durable execution (port Cadence pattern)"
- **Claim:** Implement scheduler with overlap policies + catch-up policies + durable execution in Phase 7 (3 weeks).
- **Reality:** `service/worker/scheduler/workflow.go` is itself a Cadence workflow (needs `go.uber.org/cadence/workflow`). The overlap-policy transitions (cancel_previous, terminate_previous) need atomicity guarantees that a transactional DB + cron goroutine can approximate but not replicate under a crash-during-overlap scenario.
- **Fix:** Add a note in §1005 that durable execution here means "at-least-once delivery, dedup by `(workflow_id, scheduled_time)` unique constraint" — not the exactly-once semantics Cadence gives you. Add an explicit `schedule_fire` table with unique key to the migration list.

### S3. IWF InternalChannel semantics will drift on port
- **PLAN.md cite:** §1018
- **Claim:** "Backed by workflow_run state JSONB + DB notifications".
- **Reality:** IWF's `InternalChannel` gets exactly-once consume because it's replayed from a durable workflow history. A plain JSONB + notify implementation is at-least-once (server restart between notify and consume will redeliver).
- **Fix:** Document the semantics difference. Suggest: add `consumed_at` column on each channel message and make the consume operation a conditional UPDATE.

### S4. go-ai v0.4.0 "context-management primitives reused" needs a caveat
- **PLAN.md cite:** §72 and §829
- **Claim:** `ClearToolUsesEdit`, `ClearThinkingEdit`, `CompactEdit` are reusable in Phase 5.4.
- **Reality:** Verified — they're real and the package is `pkg/providers/anthropic/context_management.go`. But the file path is under `providers/anthropic/`, not a generic location — they're an Anthropic-only primitive (maps to Anthropic's server-side context-management API). If Phase 5.4 runs against OpenAI-configured agents, these primitives do nothing; the plan's "primitives" framing in §829 obscures that.
- **Fix:** Note in §829 that these are Anthropic-specific; for non-Anthropic providers Phase 5.4 falls back to the summarise-then-truncate algorithm only.

### S5. "Claude Managed Agents" and "Replit" and "Cursor" cited with no source-code pointers
- **PLAN.md cite:** §14 table rows 9–11 and §23–25
- **Claim:** Port specific patterns (Brain/Hands, CoW snapshot, worktrees + OS sandboxing).
- **Reality:** These are not in `other-projects/`. Plan never points at a file in any of these three.
- **Fix:** Either vendor a code reference (e.g., Replit has open-source AgentV2; Cursor has its agents doc) or rephrase as "design inspiration, not code port".

## Concept-to-Go viability hotspots

- **CrewAI memory pipeline → Go:** High viability. The `Flow` sugar is sync/async orchestration that goroutines + `errgroup.WithContext` replace cleanly. The pydantic `ItemState` model maps to a Go struct. `contextvars` usage for request scoping maps to `context.Context` values. **Watch:** consolidation LLM call cost — plan §771 says micro-tier Haiku, that's fine.
- **Hermes error classifier → Go:** Trivial. Regex patterns + enum + struct of hints. Pure port.
- **Hermes credential pool → Go:** Trivial. Mutex-guarded map + cooldown timestamps. Pure port. Encryption in the plan's `workspace_credential` table uses AES-256-GCM — same primitive in `crypto/aes`.
- **Hermes prompt injection scanner → Go:** Trivial. Regex list + invisible-unicode set. Pure port.
- **Hermes context compressor → Go:** Medium. `context_compressor.py` is iterative + plug-in-able; Go implementation needs a `Compressor` interface with multiple concrete strategies, but no async-vs-sync issue.
- **Cadence backoff → Go:** Already Go. Copy-paste (license-permitting).
- **Cadence multistage rate limiter → Go:** Already Go, but `common/quotas/global/` includes a distributed-consensus layer plan does NOT need. Use `multistageratelimiter.go` only; ignore `global/`.
- **Cadence scheduler → Go:** LOW viability without Temporal/Cadence under it. See S2.
- **IWF channels → Go:** Medium viability. At-least-once is acceptable if explicitly documented. See S3.
- **IWF WaitUntil/Execute → Go:** High viability as a DB-state-machine pattern. Without a durable history the recovery story is "on restart, reload `workflow_run` + `workflow_step_run`, re-enter the state each step was last in" — which is what the plan says, and it works as long as steps are idempotent. Phase 7 must require idempotent steps explicitly.
- **Open Agents aggressive compaction → Go:** High viability. `messages[i].content[j]` coordinate indexing is trivial; token estimate (`chars / 4`) is a one-liner; the only hard part is reusing go-ai's `ClearToolUsesEdit` correctly (see S4).
- **Open Agents Sandbox → Go:** High viability — interface lift is ~30 lines of Go; three implementations (E2B, gVisor, Claude Managed) are separate work. But fix the mis-transcription in B2 first.
- **Open Agents startStopMonitor (150ms poll) → Go:** Trivial goroutine with `time.Ticker`. But the plan's Postgres poll at ~7 QPS per active step (§1012) should be bench-marked at fleet scale — 1000 concurrent steps = 7000 QPS. Plan already mentions PG LISTEN/NOTIFY as a fallback, which is the right answer.
- **go-ai ToolLoopAgent → Go:** Zero porting — use directly via `harness` package. This is the single biggest effort-saver in the plan and the adoption decision is sound.
- **CrewAI Python concurrency primitives → Go:** `ThreadPoolExecutor` with N workers → bounded-semaphore goroutine pattern with `golang.org/x/sync/semaphore` or `errgroup` with `SetLimit`. `contextvars` → `context.Context` values. These are clean 1:1 translations.
- **Paperclip Prisma/drizzle schemas → sqlc:** Straightforward. TS schema types → CREATE TABLE DDL → sqlc regen. The plan's Phase 1 migration block already does this manually and correctly.

## Specific file references that are wrong or missing

| Plan §ref | Claimed file | Actual existence | Note |
|---|---|---|---|
| §45, §1723 | `packages/sandbox/interface.ts` "lifted verbatim" | Exists but NOT lifted verbatim | Missing `access`, `getState`; optional→required mismatch; `SandboxType` literal widened; see B2 |
| §248 | Paperclip `packages/db/src/schema/secrets.ts` | Does not exist | Correct files: `company_secrets.ts`, `company_secret_versions.ts` |
| §484 | Paperclip `packages/adapters/http/execute.ts` | Does not exist | Correct: `server/src/adapters/http/execute.ts` or `cli/src/adapters/http/index.ts` |
| §489 | Paperclip `server/src/adapters/process/execute.ts` | Exists | Correct |
| §525 | Hermes `agent/smart_model_routing.py` | Exists | `choose_cheap_model_route()` — verified |
| §528 | Hermes `agent/usage_pricing.py` | Exists | Cache-aware pricing records verified |
| §599 | Hermes `tools/mcp_tool.py` | Exists | Verified (not just `mcp_oauth.py`) |
| §666 | Hermes `agent/prompt_builder.py:36-73` | Exists; line range exact | `_CONTEXT_THREAT_PATTERNS` + `_CONTEXT_INVISIBLE_CHARS` at those lines |
| §197 | Hermes `agent/error_classifier.py` | Exists | Enum + hints verified |
| §207 | Hermes `agent/credential_pool.py` | Exists | Four strategies verified at lines 61–64 |
| §1398 | Hermes `agent/rate_limit_tracker.py` | Exists | x-ratelimit header parsing verified |
| §766, §774 | CrewAI `unified_memory.py`, `encoding_flow.py` (implied) | Exist | Steps and thresholds verified |
| §795 composite formula | CrewAI `memory/types.py:144–184` | Defaults match exactly | 0.5/0.3/0.2 + 30-day half-life verified |
| §1381 | Paperclip `activity_log` | Exists at `packages/db/src/schema/activity_log.ts` | Verified |
| §1421 | Paperclip `agent_config_revisions` | Exists at `packages/db/src/schema/agent_config_revisions.ts` | Verified |
| §981 | Paperclip `approvals` | Exists at `packages/db/src/schema/approvals.ts` + `server/src/services/approvals.ts` | Verified |
| §1028 overlap policies | Cadence `common/types/schedule.go:37–42` | Verified | Names match |
| §1029 catch-up policies | Cadence catch-up | Names INVENTED | See S1 — real: `SKIP/ONE/ALL` |
| §1006, §548 | Cadence `common/backoff/` | Exists | Verified |
| §1397 | Cadence `common/quotas/` multistage | Exists | Verified; avoid the `global/` subtree |
| §568 | Cadence MAPQ | Exists at `common/mapq/` | Pattern-only reference appropriate |
| §1007–1022 | IWF WaitUntil/Execute/channels/conditional close/skip-timer | All exist | Verified in `service/interpreter/` and `gen/iwfidl/` |
| §15 Letta/MemGPT | — | Not in `other-projects/` | See B1 |
| §15 LangGraph | — | Not in `other-projects/` | See B1 |
| §14 Replit, Cursor, Claude Managed Agents | — | Not in `other-projects/` | See S5 |
| §65–78, §509 | go-ai v0.4.0 APIs | All verified | Most reliable source in the plan |
| §78 LangChainGo decline | `tools/tool.go`, `agents/executor.go` | Verified | Decline rationale accurate |

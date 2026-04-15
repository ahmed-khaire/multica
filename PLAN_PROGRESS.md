# PLAN.md Implementation Plan Progress Tracker

This file is the single source of truth for the Ralph Loop that produces one
implementation plan per phase of `PLAN.md`. Each iteration reads this file,
executes exactly one step for the first phase that is not `done`, updates the
status below, and exits.

When every phase reaches `done`, the driving loop emits the completion promise
`<promise>ALL_PHASES_PLANNED</promise>`.

---

## Per-Iteration Protocol

On every iteration, do the following — in order — then stop.

1. **Re-read this file** and find the first phase whose `status` is not `done`.
   If no such phase exists, emit `<promise>ALL_PHASES_PLANNED</promise>` and stop.
2. **Dispatch the step that matches its current status:**

   | Current `status` | Action |
   |---|---|
   | `pending` | Draft the implementation plan (Step A). |
   | `review-1` | Run independent subagent review round 1, then apply fixes (Step B). |
   | `review-2` | Run independent subagent review round 2, then apply fixes (Step B). |
   | `review-3` | Run independent subagent review round 3, then apply fixes (Step B). Mark phase `done` afterward. |

3. **Update this file** (status, `plan_file`, and the `notes` line for the phase
   you just worked on). Keep notes terse — one line per action.
4. **Stop.** Do not advance to the next phase in the same iteration. One step
   per iteration keeps review/draft work isolated and auditable.

### Step A — Draft a phase plan

1. Invoke the `superpowers:writing-plans` skill. Follow it exactly.
2. Source material: the phase's section in `PLAN.md` (at the repo root). Use
   the line range listed in the phase entry below as your primary anchor, but
   read adjacent sections (`Foundation Libraries D1–D7`, any phase listed under
   `depends_on`, and the global `Context` block) for invariants.
3. Mirror the shape of the existing phase plans in `docs/superpowers/plans/`
   (see `2026-04-13-phase1-agent-type-system.md`, `2026-04-14-phase2-harness-llm-backend-worker.md`,
   `2026-04-14-phase3-mcp-skills-registry.md`). Required top sections:
   - H1 title `# Phase N: <Title>`
   - Agentic-worker callout line (REQUIRED SUB-SKILL note)
   - `**Goal:**`, `**Architecture:**`, `**Tech Stack:**`
   - `## Scope Note` (what's in scope, what's deferred)
   - `## File Structure` (New Files / Modified Files / External Infrastructure tables)
   - Numbered `### Task N:` sections with `- [ ]` checkbox steps and code
     snippets (failing-test first, then implementation, per TDD).
4. Write the file to `docs/superpowers/plans/2026-04-14-<slug>.md` using the
   `plan_file` value listed in the phase entry below.
5. Update this tracker: set `status: review-1`, set `plan_file`, append one
   `notes` line: `drafted (<N> tasks, <M> lines)`.

### Step B — Subagent review round

1. Dispatch a fresh subagent (use the `Agent` tool with
   `subagent_type: feature-dev:code-reviewer`). The subagent MUST be brand new
   — do not reuse a prior review agent or feed it prior rounds' critique. Each
   round gets an independent pair of eyes.
2. Prompt template (fill in `{phase_num}`, `{plan_path}`, `{round}`):

   > You are reviewing an implementation plan, not code. The plan lives at
   > `{plan_path}`. Its authoritative source is the `## Phase {phase_num}`
   > section of `PLAN.md` at the repo root, plus the `Foundation Libraries
   > D1–D7` invariants earlier in `PLAN.md`.
   >
   > Review round: **{round} of 3**.
   >
   > Focus per round:
   > - Round 1 — **Completeness & fidelity**: does the plan cover every
   >   requirement, migration, guardrail, and non-goal listed in the PLAN.md
   >   phase? Any scope drift, missed tables, missed endpoints, missed UI
   >   surfaces? Are cross-phase dependencies respected?
   > - Round 2 — **Technical soundness**: concurrency, transaction boundaries,
   >   error paths, migration safety (up + down), cache invalidation, WS event
   >   shapes, TDD ordering, test coverage gaps, type/schema mismatches.
   > - Round 3 — **Executability**: can an agent follow this plan task-by-task
   >   without re-reading PLAN.md? Are file paths concrete, code snippets
   >   copy-pastable, checkbox steps atomic, and each step self-verifiable?
   >   Flag hand-wavy steps, missing failing-test snippets, or tasks that
   >   implicitly depend on un-written helpers.
   >
   > Read `PLAN.md` (the relevant phase section + D1–D7) and `{plan_path}` in
   > full. Then produce a review with this exact structure:
   >
   > ```
   > ## Blocking Issues
   > - [ ] <one-line issue> — <file>:<section> — <what to change>
   >
   > ## Should Fix
   > - [ ] <one-line issue> — <file>:<section> — <what to change>
   >
   > ## Nits (optional)
   > - <one-line issue>
   >
   > ## Verdict
   > <one short paragraph: is this plan ready to move to the next round, or
   > does it need substantive rework?>
   > ```
   >
   > Be specific: cite section headers and line ranges in the plan file. Do
   > not rewrite the plan — list the required edits only. Stay under 800
   > words total.

3. Save the subagent's full review to
   `docs/superpowers/plans/reviews/<slug>-review-{round}.md`.
4. Apply every `Blocking Issues` and every `Should Fix` item to the plan file.
   Skip `Nits` unless trivially correct.
5. Update this tracker: advance `status` (`review-1` → `review-2` → `review-3`
   → `done`). Append a one-line note: `review {round}: <N> blocking, <M>
   should-fix, applied`.

### Invariants

- **Never skip a step.** If a review round finds zero blocking issues,
  document "0 blocking, 0 should-fix" and still advance the status — but only
  after the subagent actually ran. Do not self-review.
- **Never work on two phases in one iteration.** One step, one phase, one commit-worthy unit of work.
- **Never edit `PLAN.md`** from this loop. It is the spec; deviations must be
  flagged as blocking issues, not silently corrected.
- **Keep notes terse.** One line per action. The tracker is audit trail, not
  a diary.
- **If a draft / review action fails** (subagent timeout, tool error), add a
  `notes` line `FAILED: <reason>` and stop the iteration without advancing
  `status`. The next iteration will retry the same step.

---

## Phases

### Phase 4 — Structured Outputs & Guardrails

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md`
- **plan_md_section:** `PLAN.md` lines 643–718
- **depends_on:** Phase 2 (go-ai harness, `LLMAPIBackend`)
- **notes:**
  - drafted (28 tasks, 2740 lines)
  - review 1: 3 blocking, 5 should-fix, applied
  - review 2: 5 blocking, 4 should-fix, applied
  - review 3: 3 blocking, 5 should-fix, applied

### Phase 5 — Agent Memory System

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase5-agent-memory-system.md`
- **plan_md_section:** `PLAN.md` lines 719–873
- **depends_on:** Phase 1 (`workspace_embedding_config`), Phase 2 (harness), Phase 3 (tool/skill registry)
- **notes:**
  - drafted (19 tasks, 3060 lines)
  - review 1: 2 blocking, 5 should-fix, applied
  - review 2: 3 blocking, 6 should-fix, applied
  - review 3: 7 blocking, 5 should-fix, applied

### Phase 6 — Agent-to-Agent Delegation

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase6-a2a-delegation.md`
- **plan_md_section:** `PLAN.md` lines 874–928
- **depends_on:** Phase 2 (go-ai `SubagentRegistry`), Phase 1 (`agent_capability` table)
- **notes:**
  - drafted (13 tasks, 2383 lines)
  - review 1: 3 blocking, 5 should-fix, applied (B1 architecture-note prose corrected re go-ai's bare map; B2 added subagent.step events + ProgressFn ctx helper + OnStepFinish wiring + test; B3 backend now returns authoritative slug via service.Slugify + removed TS re-slugify; S1 added BudgetAllowFn tx wrapper in Task 7; S2 clarified go-ai adapter paths with compile-time assertion; S3 added useSubagentEvents hook + test as explicit step; S4 relocated Go tests to server/internal/integration/delegation/; S5 added timeout guardrail + SubagentPerStepTimeout default + deadline-propagation test)
  - review 2: 3 blocking, 3 should-fix, applied (B1 e.Summary→e.Text; B2 concrete AdaptToGoAI adapter with goaiAgentShim + compile-time canary test; B3 honest doc of weaker optimistic budget semantics + Phase 9 hardening pointer; S1 removed vacuous-pass test — now asserts err==nil with real success stub; S2 added service.EstimateStepCents/EstimateCostCents delegating to Phase 2 CostCalculator; S3 renamed OnStepFinish → OnStepFinishEvent to match Phase 2 field)
  - review 3: 3 blocking, 4 should-fix, applied (B1/B2/B3 added Preconditions section naming every Phase 1/2 surface + testutil/testsupport helpers + go-ai v0.4.0 go.mod check + Pre-Task 0 bootstrap checklist; S1 moved useSubagentEvents from packages/core/tasks/ to packages/views/tasks/ for CLAUDE.md compliance; S2 clarified Task 6 import-merge instruction; S3 fixed Task 13 evidence template path; S4 added Task 2.5 sqlc GetAgentBySlug + app-side Slugify lookup + tests; N1 lifted useSubagentEvents out of conditional via NestedSubagentCard(callId) child component; N2 widened TestProgressFn run pattern; final line count 3015)

### Phase 7 — Workflow Orchestration

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase7-workflow-orchestration.md`
- **plan_md_section:** `PLAN.md` lines 929–1049
- **depends_on:** Phases 2, 5, 6
- **notes:**
  - drafted (17 tasks, 3438 lines)
  - review 1: 5 blocking, 5 should-fix, applied (B1 Task 11.1 added — catch-up one/all with dedup-preserving replay; B2 fan_out expansion replaces stub — synthetic leaf step IDs, evaluateFanOutJoin with all/any semantics, resumeWaiters case; B3 complete_when DSL field + engine check post state-merge; B4 timer WaitUntil — workflow_timer table, CreateTimer/SkipTimer/TimerElapsedByName sqlc, engine parks step in waiting, real SkipTimer handler; B5 ListActiveWorkflowRunsForWorkflow workspace-scoped variant + applyOverlap uses it; S1 buffer==concurrent V1 equivalence moved to scope-note; S2 sub_workflow now blocks via WaitingError + SubRunStatus resumeWaiters path; S3 WaitingError + resumeWaiters implement approval gate — step stays waiting until decided, dependents blocked; S4 StepEnv.CheckBudget added, execAgentTask + execToolCall gate before work; S5 fixed pnpm add command split into catalog edit + filter install; final line count 4046)
  - review 2: 5 blocking, 3 should-fix, applied (B1 WaitingError early-exit inside retry loop — no more 3× approvals/sub-runs before parking; B2 fan_out expansion now persists augmented wf to steps_snapshot + idempotent re-entry guard + UpdateWorkflowRunStepsSnapshot sqlc query; B3 InsertScheduleFire distinguishes pgx.ErrNoRows from real errors, added Scheduler.log + Logger interface + warn-and-continue; B4 CreateTimer UPSERT changed from DO UPDATE expires_at to DO NOTHING so elapsed timers can't be revived; B5 resumeWaiters uses r.ID (step_run primary key) instead of runID for UpdateStepRunComplete; S1 r.Status normalized to string() for sqlc named-type portability; S3 timer resume now calls TimerElapsedByName with (runID, stepID, timerName) instead of nonexistent TimerElapsed(ID), removed stale TimerElapsed from StepEnv; S5 workspace_id added to every Publisher.Publish payload — step.waiting/step.completed/step.failed/approval.decided/timer.skipped; final line count 4149)
  - review 3: 5 blocking, 5 should-fix, applied (B1 engine.go imports "strings" + "errors" added; B2 scheduler.go imports "errors" + pgx added; B3 mustTraceID now uses `db.New`+`pool` parameter consistently, no more dbPkg alias confusion; B4 runSingleStep captures stepRow from UpsertStepRun and threads stepRow.ID through IncrementStepRunRetry + both UpdateStepRunComplete calls — retries no longer silently no-op; B5 Preconditions section expanded with full testsupport Env + StubModel + TurnScript + all methods used by Tasks 11/12/16; S1/S2/S3 seven testutil helpers enumerated with signatures + uuidPg/uuidFromPg/sqlNullString anchored to server/internal/service/uuid_helpers.go (Phase 2 Task 19.5) + handler-test ctx pattern documented; S4 ListActiveScheduledWorkflows annotated scheduler-only-global with cross-tenant warning; S5 WorkflowBuilder rewritten to use useCreateWorkflow/useUpdateWorkflow mutation hooks — serialize() helper + save() dispatches mutation, no direct api.* calls in component; final line count 4318)

### Phase 8A — Platform Integration

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase8a-platform-integration.md`
- **plan_md_section:** `PLAN.md` lines 1050–1338
- **depends_on:** Phases 2, 3, 4, 5, 6, 7
- **notes:**
  - drafted (18 tasks, 2101 lines)
  - review 1: 3 blocking, 4 should-fix, applied (B1 replaced maybeCompact stub with real agent.Compact at ChatContextCompactThreshold + threshold-crossing test; B2 added publicEventName translator mapping internal approval.created → public approval.needed per PLAN.md §8A.3 + integration test asserting X-Event-Type; B3 added missing GetWebhookSubscriptionByID sqlc query used by outbound retryLoop; S1 category-list extension documented in migration 161 comment + commit message + partial UNIQUE index for system-template idempotence; S2 EmbeddedWorkerInbox channel on ChatDeps wires the --with-worker Go-channel bypass per §8A.1 + test asserting no Redis publish in embedded mode; S3 added router.go snippet wrapping every /api route group with RequireScope + end-to-end TestRoute_RequireScope_RejectsUnderScoped; S4 added chat_message.stream_id column + idx to migration 162 + its down-migration reverse; final line count 2361)
  - review 2: 4 blocking, 4 should-fix, applied (B1 chat switched from pub/sub+SETNX to Redis Streams with consumer groups — XADD/XREADGROUP/XACK + reclaimStalled goroutine using XAUTOCLAIM on 60s ticker for worker-crash recovery — architecture + Dispatch + worker subscribe + handleMessage all rewritten; B2 ON CONFLICT (name) WHERE is_system AND workspace_id IS NULL DO NOTHING — explicit target matching the partial unique index; B3 EmbeddedWorkerInbox bounded at 64 + default-case returns ErrChatInboxFull → HTTP 503 instead of ctx.Done(); B4 Pre-Task 0 promoted to two non-negotiable extensions — dry_run flag on ToolRegistry AND ContextWindow() method on harness.Model with provider-adapter hardcoded table + alternative path through agent.ContextWindowFor; S1 retry-loop vault fetch documented as per-attempt policy (rotation-aware) with Phase 9 cache follow-up; S2 publicEventName renamed into local publicEv copy — no in-place mutation of shared ev; S4 TouchAPIKeyUsage fires in background goroutine with 2s detached context — auth hot path unblocked; S5 GetRaw byte-slice ownership contract added to Preconditions — caller-owned + must-zero, vault implementations that cache internally must copy before returning; final line count 2478)
  - review 3: 6 blocking, 4 should-fix, applied (B1 redis.Adapter Streams surface fully declared in Preconditions — XAdd/XGroupCreate/XReadGroup/XAck/XAutoClaim/Keys signatures + XEntry struct spec; B2 maybeCompact signature aligned — Archivist added to ChatWorkerDeps + threaded into the call; B3 "errors" added to chat.go import block; B4 migration 162 extended to add workspace_id/agent_id/user_id to chat_message + new chat.sql query file with InsertChatMessage + ListRecentChatMessages added to Task 5 (now Step 2.5); B5 Pre-Task 0 listing signatures for every testutil chat helper — FakeRedis, StubModelReplying, StubModelWithContextWindow, SeedChatMessage, AlwaysAllowBudget, DenyBudget, JSONBytes, NoopWS; B6 BudgetAllowFn type defined inline in Task 6 as stable boundary even if Phase 6 renames internally; S1 EmbeddedWorkerInbox consumer wired via consumeEmbedded goroutine with noopAck — Run branches on EmbeddedWorkerInbox!=nil; S2 TestPublicEventName table-driven unit test covers all 9 public events + default passthrough; N2 consumerName now uses w.deps.InstanceID — unique per worker for Streams consumer-group exclusivity; InstanceID added to ChatWorkerDeps; final line count 2677)

### Phase 8B — Security & Compliance Hardening

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase8b-security-compliance.md`
- **plan_md_section:** `PLAN.md` lines 1339–1508
- **depends_on:** Phase 8A
- **notes:**
  - drafted (14 tasks, 1821 lines)
  - review 1: 3 blocking, 3 should-fix, applied (B1 CredentialsView expanded — full Credential type + ListCredentialsWithBindings sqlc query + view renders last_rotated_at/usage_count/cooldown_until/bound_agents per §8B.1; B2 ProviderTracker.Observe wired into Phase 2 LLM backend at step 5 of Task 8 — Allow() gate before send, Observe() after response + integration test asserting cross-agent throttle propagation; B3 Cadence multi-stage deviation documented in Scope Note — V1 uses 2 stages + provider-429 feedback which reads ceiling from upstream, global cluster stage tracked as Phase 9 follow-up; S1 default retention policies — migration 172 seeds audit_log=365/trace=90 for existing workspaces + new Task 11.5 workspace-creation hook upserts defaults for future workspaces; S2 new integration test TestRollback_DoesNotAffectInFlightTask — blocking model, rollback mid-execution, assert running task response unaffected while agent row IS rolled back; S3 credential_access_log added to §8B.7 manual verification checklist; N3 actionFromRequest uses pluralToSingular lookup map instead of naive TrimSuffix("s"); final line count 2083)
  - review 2: 2 blocking, 6 should-fix, applied (B1 audit diff now reads RESPONSE body not request — capturingWriter extended with 1MiB-capped body buffer, response body parsed as "after" with convention that mutating handlers return full post-mutation row; B2 OIDC state validation implemented — signed HMAC-SHA256 state with nonce cookie, 5-min expiry, constant-time compare, test covering bogus/missing-nonce/expired cases; S1 audit-drop observability — CRITICAL structured log + atomic dropCount + DropCount() accessor + Logger dep on service; S2 NextRevisionNumber TOCTOU fixed — INSERT embeds MAX+1 subquery atomically + RevisionService.Snapshot retries up to 3× on 23505 unique-violation; S3 rate-limiter map eviction — limiterEntry{lim, lastUsed} + evictLoop every 5min dropping entries idle >24h; S4 rollback test now captures claim-time harness.Config.SystemPrompt via channel + asserts v2-instructions active BEFORE rollback; S5 SAML shared-signing-key noted as V1 limitation for Phase 9 follow-up; S6 LIKE-on-JSONB replaced — split into ListCredentials + ListAgentsUsingCredential using JSONB @> containment with GIN-index-friendly shape; N2 EnforceSSO extracts workspace_id from body/form with 64KB read cap; final line count 2333)
  - review 3: 5 blocking, 3 should-fix, applied (B1 Rollback rewritten to use R2's embedded-subquery InsertAgentRevision + retry-on-23505 — no more dangling NextRevisionNumber call; B2 revision.go imports — pgtype/pgconn/errors/fmt added; AuditService imports — sync/atomic added; B3 randomBase64 helper defined with crypto/rand + URLEncoding + panic on CSPRNG failure; B4 oidc.go full import block — hmac/rand/sha256/subtle/base64/json/errors/fmt/net/http/strings/time/oidc/uuid/oauth2 + package auth header; NewOIDCService constructor + stateSigningKey field with HKDF-derived init note; B5 jsonb credential-id storage contract documented — pgx default encoding serializes uuid.UUID as quoted JSON string so ::text cast matches containment, with jsonb_typeof verification query for future schema changes; S1 cookie-path "/auth/oidc" kept + RFC 6265 §5.1.4 prefix-match explanation comment added; S2 pre-Task 0 expanded with golang.org/x/time/rate + golang.org/x/oauth2 go get + go mod tidy; S3 harness.Config.SystemPrompt confirmed in Preconditions Phase 2 surface list; final line count 2436)

### Phase 9 — Advanced Cost Optimization

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase9-cost-optimization.md`
- **plan_md_section:** `PLAN.md` lines 1509–1623
- **depends_on:** Phases 2, 5 (Redis, embeddings)
- **notes:**
  - drafted (11 tasks, 1527 lines)
  - review 1: 2 blocking, 5 should-fix, applied (B1 two-tier cache — Redis L1 exact-match via SHA-256(ws+agent+config_hash+query_text) before pgvector L2, restores D4 semantic-cache-is-Redis-consumer baseline + redis.Adapter Get/SetEx added to Preconditions; B2 95%-savings stack explained — cache_control prefixes on batch items, CostCalculator.ComputeCents applies cache multipliers BEFORE DiscountMultiplier, §11 verification item asserts cost_cents ≈ baseline × 0.05; S1 CostBreakdown + RoutingEffectiveness + ModelTier types + CostBreakdown sqlc query with window-function pct_of_total + routing pie chart component; S2 /cost/chain?trace_id= endpoint + CostPerTraceChain sqlc + ChainCostRow type + DelegationCostChart recharts stacked-bar component; S3 /cost/credential-pools endpoint + CredentialPoolStats type + Phase 8B credential-binding reuse + CredentialPoolsTable component; S4 knowledge_version invalidation — DeleteStaleCache* gains $4 nullable parameter + KnowledgeVersionOf callback on CachePurgeDeps; S5 TestCostSummary_PartitionsHitVsMiss asserts cost-weighted CacheHitRate=0 when hits are free (clarifies the distinction from event-weighted rate exposed via /cost/cache-hits); N2 AgentMetric TS type completed with tasks_timed_out/delegations_*/chat_messages_handled/avg_response_seconds; N3 RunDaily body filled in with RollupDailyMetrics sqlc query aggregating 24 hourly rows; final line count 1889)
  - review 2: 4 blocking, 3 should-fix, applied (B1 L1 cache populated AFTER L2 InsertCache* succeeds — populateL1 helper writes Redis only when ID is non-nil, L1 TTL shortened to 1h vs L2 7-day, CacheEntry gains JSON tags including id; B2 completeBatch wraps CompleteTask+InsertCostEvent in pool.BeginTx — partial failure rolls back cleanly, cache_read/write_tokens now passed through preserving R1-B2 95%-savings stack; B3 batch_job persistence — migration 180 adds table, BatchExecutor.resumeInFlight scans status=submitted on boot, markBatchJob fires on terminal state, 24h deadline flips to failed; B4 ListAgentsWithSLA + ListAllAgents documented as intentionally-global scheduler-pattern queries, RunHourly per-agent body wrapped in closure+defer recover + early-return on FK race; S2 L1 rollback reactivation mitigated via 1h TTL vs 24h purge cycle; S3 CacheHitRate renamed to CostWeightedCacheSavingsRate with docstring + test comment distinguishing cost-weighted vs event-weighted; S5 ClaimBatchTasks CTE with FOR UPDATE SKIP LOCKED atomic pending→batch_submitted transition + UnclaimBatchTasks fallback for Submit failures + InsertBatchJob ordering; N1 Similarity cast made type-safe handling float64|pgtype.Float8; N2 multi-turn hit-rate approximation comment; N3 startup daily catch-up via dailyRolledUp + CountDailyMetricsForDay sqlc; final line count 2155)
  - review 3: 5 blocking, 5 should-fix, applied (B1 cache.go imports — crypto/sha256, encoding/hex, encoding/json, pgx/v5/pgtype, pkg/redis all added to import block; B2 sqlNullDec helper defined in Pre-Task 0 bootstrap at uuid_helpers.go with pgtype.Numeric scan; B3 workspaceDimsFor concrete path documented in Pre-Task 0 with inline fallback calling GetWorkspaceEmbeddingConfig; B4 NewResponseCache test calls updated to three-arg (nil redis) — signatures now match; B5 BatchDeps.CostCalc field added + computeCostCents method body delegates to CostCalculator.ComputeCents; S1 SeedCostEvent singular vs SeedCostEvents plural signatures reconciled — both documented explicitly; S2 pgUUIDsToUUIDs([]pgtype.UUID)→[]uuid.UUID helper added + resumeInFlight uses it; S3 CostService struct + CostDeps + NewCostService + Breakdown/CacheHits/ChainCost/CredentialPools method skeletons added; S4 SumCostInRange + CacheHitsPerAgent sqlc queries inlined with SUM FILTER partitioning by cost_status; S5 AggregateDelegations + AggregateChatMessages sqlc stubs added with V1-approximation comment + post-V1 delegation_event table note; final line count 2333)

### Phase 10 — Observability & Tracing

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase10-observability-tracing.md`
- **plan_md_section:** `PLAN.md` lines 1624–1709
- **depends_on:** Phases 2, 6, 7
- **notes:**
  - drafted (11 tasks, 1213 lines)
  - review 1: 2 blocking, 3 should-fix, applied (B1 CSV export — ?format=csv branch on search handler streaming via csv.Writer with stripHTMLTags + download button in SearchPage; B2 TestTrace_WorkflowRunTree — workflow seed, workflow_step+llm_call span shape asserted, ancestry check that llm_call descends from workflow_step; S1 SpanDetails component uses classifyError from @multica/core/errors/classify mapping categories to semantic color tokens + retryable flag; S2 ListTracesForWorkflowRun + ListRecentTraces sqlc + TraceHandler.List with ?workflow_run_id, ?agent_id, ?since filters + trace-filters.tsx component; S3 LateLinkCostEvent Tracer method + UpdateSpanCostEventID sqlc for panic/async paths + ordering contract documented; N1 useDeferredValue in SearchPage; N2 delegation tree test tightened to require operation-shape not just count; N3 optional mcp_call span in Phase 3 MCPClient.CallTool documented as skip-or-add per latency profile; final line count 1519)
  - review 2: 3 blocking, 4 should-fix, 3 nits applied (B1 task_message has no workspace_id — SearchTaskMessages + CountSearchResults rewritten to INNER JOIN through agent_task_queue, workspace filter moved from m.workspace_id to t.workspace_id; B2 CREATE INDEX CONCURRENTLY dropped — custom migrate runner has no notransaction support, regular CREATE INDEX used since task_message is small at Phase 10 land time + manual CONCURRENTLY-before-migration escape hatch documented; B3 Pre-Task 0 expanded with explicit WithTraceID/TraceIDFrom definition if Phase 2 Task 1.5 absent + TraceIDForTask/TraceIDForRun/GetTraceTree/ListSpansForTrace/CountSpans testsupport helpers with bodies; S1 Span.End wrapped in context.WithTimeout(5s) + orphaned-row observability note; S2 LateLinkCostEvent semantic documented as first-write-wins with clear-and-re-link procedure; S3 Span.otel field renamed to otelSpan — avoids package-alias shadowing; S4 classify.ts stub created inline with ErrorCategory + 12 regex patterns mirroring Phase 1.3 Go classifier; N1 ListTracesForWorkflowRun + ListRecentTraces rewritten as GROUP BY instead of DISTINCT+window; N2 TraceWaterfall early-returns on empty span list; N3 ListSpansForTrace gains LIMIT + handler ?limit=500 cap; final line count 1645)
  - review 3: 3 blocking, 4 should-fix, 3 nits applied (B1 groupBy Map-based helper inlined at top of trace-waterfall.tsx with string-keyed map and "__root__" sentinel; B2 stripHTMLTags helper defined inline in search handler via var htmlTagPattern = regexp.MustCompile(`<[^>]+>`); B3 env.Search(wsID, query) testsupport helper added with SearchResults/SearchResultItem JSON-decoded types + URL escaping + workspace-scoped GETAs; S1 oteltrace "go.opentelemetry.io/otel/trace" import added to tracer.go import block; S2 new Step 1.5 introducing packages/core/api/search.ts with searchExecutions REST client + packages/core/search/queries.ts with useSearchExecutions TanStack hook (enabled on non-empty query, 30s staleTime); S3 new Step 3.5 with full trace-filters.tsx body — workflow and agent dropdowns + since datetime-local + TraceFilters type + onChange contract; S4 JSONSpan struct defined distinct from sqlc agent_trace row — uses *string pointers + time.Time + cost_cents/model join fields, TraceTreeFetched.Spans retyped to []JSONSpan with explicit json tags; N1 atoiOrDefault reference pointed to shared Phase 2 handler util; N2 newSearchCtx body fleshed out — searchCtx struct with env/wsID/agentID/taskID + seedTaskMessage + getJSON bound to seeded workspace; N3 CountSearchResults /* same params */ replaced with explicit db.CountSearchResultsParams struct matching the SearchTaskMessages column shape; final line count 1880)

### Phase 11 — Cloud Coding Sandbox (E2B + gVisor)

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase11-cloud-coding-sandbox.md`
- **plan_md_section:** `PLAN.md` lines 1984–2225
- **depends_on:** Phase 2 (Backend interface), Phase 10 (Tracer), D3 sandbox invariant
- **notes:**
  - drafted (15 tasks, 3198 lines)
  - review 1: 4 blocking, 4 should-fix, applied (B1 per-tool spans + LateLinkCostEvent — added `withToolSpan` helper wrapping all 4 tool Executes with `Tracer.StartSpan("tool_call",...)` + ended with status/err; tracer threaded into SandboxBackend constructor; B2 LateLinkCostEvent call added after InsertCostEvent in RunOnce + InsertCostEvent bumped to `:one` returning row in Phase 2 precondition note; B3 CloudCodingDeps.Pool → Pools *PoolRegistry with lazy-per-template construction + ShutdownAll + RunOnce now calls w.deps.Pools.For(templateID) after fetching GetCloudCodingAgentWithTemplate; B4 down migration gained `DELETE FROM agent WHERE agent_type='cloud_coding'` cleanup + VALIDATE CONSTRAINT after each ADD CONSTRAINT NOT VALID; S1 gvisorK8sSandbox + gvisorDockerSandbox + e2bSandbox gained `timeoutCancel func()` field + explicit Stop-body updates calling it before BeforeStop + Task 8 Step 3 clarified; S2 useSandboxInstances gained V1-exception comment for polling vs WS invalidation with Phase 12 switch pointer; S3 Pool gained poolCtx + poolCancel set at NewPool + refillLoop uses poolCtx instead of context.Background() + Shutdown calls poolCancel; S4 Pool.Acquire cold path wrapped in singleflight.Group("cold-create", ...) to coalesce concurrent misses — added `golang.org/x/sync/singleflight` + `go get golang.org/x/sync@latest` in Pre-Task 0; NewNoopTracer + NewSingletonPoolRegistry testsupport signatures added; final line count 3342)
  - review 2: 4 blocking, 4 should-fix, 3 nits applied (B1 singleflight sandbox-sharing race — winner now pushes Sandbox to p.ready and all callers (winner + losers) re-read from the channel; prevents N owners of one Sandbox / double-stop; B2 ScheduleTimeoutHook cancel wrapped in sync.Once — idempotent close(done) prevents panic on double-invoke from Stop + post-fire paths + added TestScheduleTimeoutHook_CancelIsIdempotent asserting double-cancel no-op; B3 outer cloud_coding_task span no longer deferred — explicit span.End("completed", nil) on success + new worker.fail helper calls span.End("error", cause) on every failure path; B4 added `"sync"` + `"fmt"` to cloud_coding.go import block; S1 TestScheduleTimeoutHook_CancelPreventsFire widened to 500ms expiry / 400ms buffer — ~100ms cancel window removes CI flakiness; S2 gvisor-docker ReadFile now uses archive/tar.NewReader to find first TypeReg/TypeRegA entry — handles GNU/POSIX extended headers correctly + imported archive/tar; S3 markFailed replaced with CloudCodingWorker.fail method surfacing both MarkTaskFailed DB error and original cause via fmt.Errorf wrapping — no more silently-stuck in_progress tasks; S4 useSandboxInstances swapped 5s polling for useWSInvalidate(["sandbox.started","sandbox.stopped"], queryKey) + worker now Publish sandbox.started on Register/post-Acquire and sandbox.stopped in defer before Release — honors CLAUDE.md WS-invalidation rule + added EventsBus local interface to CloudCodingDeps; N1 podExecParamCodec replaced with scheme.ParameterCodec from k8s.io/client-go/kubernetes/scheme — real exec now serializes Command/Stdin/Stdout/Stderr; N2 tarSingleFile now uses strings.TrimPrefix(filepath.ToSlash(dstPath), "/") preserving directory path + imported strings; N3 agent.Run citation pointing at server/pkg/agent/run.go as Phase 2's exported tool-loop entrypoint; final line count 3431)
  - review 3: 3 blocking, 4 should-fix, 3 nits applied (B1 dockerClient interface retyped to `*network.NetworkingConfig` + `*ocispec.Platform` — real Docker SDK surface; imports in gvisor_docker.go + gvisor_docker_test.go updated; Pre-Task 0 bootstrap now `go get github.com/opencontainers/image-spec@latest` + `golang.org/x/sync@latest`; B2 ten undeclared env.* helpers explicitly listed in Preconditions with signatures — EnqueueTask/TaskStatus/CountCostEventsByProvider/TraceIDForTask/ListSpans/StubModelReplying/StubLLMFactory + Env fields CostCalc/Events/ActiveSandboxes + RouteTo return type nailed to *httptest.ResponseRecorder; B3 new Task 11 Step 3.5 with full TestSandboxInstanceHandler_List body — seeds registry via ActiveSandboxEntryForTest helper + asserts 200 + one instance + sandbox_id match; S1 new Task 14 Step 4.5 adds failing RTL smoke test SandboxTemplatesPage renders heading + template row with vi.mock of queries; S2 listOpts moved out of gvisor_k8s.go into gvisor_k8s_test.go with metav1 test-only import; S3 server/pkg/db/gen/ already in Task 10 git-add line — verified; S4 Preconditions extended with agent_task_queue.prompt TEXT column note + grep hint for Phase 2 migration rename check; N1 NewPool channel-size=1 comment explains cold-path singleflight handoff requires buffer even at TargetSize=0; N2 post-Task 8 Step 3 import-direction note documents sandbox→agent edge + forbids reverse import + alternate refactor path; N3 RouteTo return type documented in precondition; final line count 3611)

### Phase 12 — Claude Managed Agents Backend

- **status:** `done`
- **plan_file:** `docs/superpowers/plans/2026-04-14-phase12-claude-managed-agents.md`
- **plan_md_section:** `PLAN.md` lines 2227–2325
- **depends_on:** Phases 2, 8B (credential vault), 10 (Tracer)
- **notes:**
  - drafted (12 tasks, 1980 lines)
  - review 1: 3 blocking, 3 should-fix, applied (B1 environment_config JSONB column added to agent in migration 188 with NOT NULL DEFAULT of {internet_enabled:false, secret_credential_ids:[]} + GetClaudeManagedAgent now returns env config + worker deserializes into PersistedEnvConfig + resolves secret IDs via CredentialVault.GetNameAndValue Phase 8B interface + zeros value bytes post-copy per vault contract; CredentialVault local interface added to ClaudeManagedDeps; B2 FakeAnthropicServer gained lastCreateSession + allCreateSessions capture on /v1/sessions handler + AllCreateSessionRequests accessor + reuse test now asserts sessions[0].ContainerID=="" + sessions[1].ContainerID=="cnt_fake"; B3 in-flight SSE streaming to Message channel moved to explicit Scope Note deferral with Phase 13 pointer — clarifies tool dispatch is NOT deferred, only user-visible text streaming; S1 ClaudeManagedSessionHourCents + ClaudeManagedContainerMaxDays switched from var to const per D7 — ClaudeManagedSSEIdleTimeout kept as var (typed-duration can't be const); S2 Hooks() forwards caller-supplied hooks + ClaudeManagedConfig gained Hooks field + drain() wraps SSE read in time.AfterFunc watchdog firing Hooks.OnTimeout and cancelling readCtx + worker passes OnTimeout that logs breadcrumb (span.End stays exclusive to success/fail paths to avoid double-end race); S3 GetSessionIDForIssue rewrote fragile ($3 || ' days')::interval string-interp to typed INTERVAL '1 day' * $3::int + worker now passes int32(ClaudeManagedContainerMaxDays); final line count 2111)
  - review 2: 3 blocking, 4 should-fix, 3 nits applied (B1 watchdog closes stream.Close() instead of cancelRead() — bufio.Scanner is not ctx-aware so only closing the HTTP body unblocks a stalled read; refactored from time.AfterFunc to time.NewTimer + goroutine observing watchdog.C with ctx.Done fallback; B2 post-Execute writes wrapped in pgx.BeginFunc tx — UpsertTaskSessionID + UpdateAnthropicAgentID + InsertSessionCostEvent + MarkTaskCompleted now atomic, rollback on any error leaves task in_progress for retry; added bestEffortPersistIDs helper for failure path so container_id survives retry; B3 InsertSessionCostEvent err now fails the task via w.fail rather than silently swallowing — completed tasks always have a cost row; S1 time.AfterFunc→time.NewTimer with Stop+drain pattern on every Reset + defer Stop drain; S2 sessionOut + agentOut sends wrapped in non-blocking `select { case ch <- v: default: }` to prevent retry-deadlock when the buffered channel has a prior undrained value; S3 down-migration added SHARE UPDATE EXCLUSIVE lock-window comment warning operators about concurrent-write race between NOT VALID and VALIDATE; S4 FakeAnthropicServer /v1/sessions/sess_fake/{messages,tool_results} replaced with single /v1/sessions/ wildcard prefix handler dispatching on path suffix — works for any session id + imported strings; N1 ClaudeManagedConfig gained IdleTimeout time.Duration field with zero-value fallback so tests set sub-second watchdog without mutating the global const; N2 _ = readCtx smell removed (readCtx eliminated; watchdog now closes stream directly); N3 Enable-internet Checkbox gained aria-label so the smoke test getByLabelText works with Base UI primitives; final line count 2209)
  - review 3: 3 blocking, 4 should-fix, 3 nits applied (B1 missing env.SeedWorkspace/SeedIssue/TaskStatus + CountCostEventsByProvider signatures added to Preconditions as Phase 2-era helpers; B2 duplicate FakeAnthropicServer struct stub removed from Preconditions — replaced with forward reference to Task 8 + signature list for the 7 methods; B3 uuid/pgtype/sqlc helper signatures documented as Phase 2 Task 19.5 exports from server/internal/service/uuid_helpers.go with note about cross-package re-export file if layering cycle forbids direct import; S1 tools.Dispatch replaced with toolRegistry.Dispatch + comment pointing at Phase 2's registerTools() wiring + Vault dep added to Deps struct wiring; S2 Task 7 Step 2 gained ordering note at top — append ToolSchema to anthropic/types.go BEFORE writing claude_managed.go body or go build fails mid-step; S3 pgx.BeginFunc replaced with idiomatic pgx/v5 manual Begin/defer-Rollback/Commit IIFE pattern stable across all v5 minor versions + removed unused pgx import; S4 Task 9 Step 3 claim.go snippet expanded to show RouterDeps struct + representative RouteClaim method + explicit wiring note distinguishing the router branch from the long-running Run goroutine; N1 math import in Task 6 gained "only add if not already present" guard comment; N2 SelectTrigger already had aria-label="Model" — no change needed; N3 both `defer fake.Server.Close()` sites gained clarifying comment that t.Cleanup already handles it + the defer is redundant-but-harmless; final line count 2280)

---

## Completion Criteria

All 10 phases above have `status: done` AND a plan file that exists at
`plan_file` AND three review files in `docs/superpowers/plans/reviews/`.

When the criteria are met, emit on a single line:

```
<promise>ALL_PHASES_PLANNED</promise>
```

# PLAN.md — Consolidated Review Report

Three independent Opus reviewers (max reasoning, max effort) audited PLAN.md end-to-end with distinct, non-overlapping lenses:

| # | Reviewer | Lens | Full report |
|---|---|---|---|
| 1 | Architecture & Invariants | D1–D7, go-ai adoption, dependency graph, cross-phase contracts, packaging boundaries | [01-architecture-and-invariants.md](./01-architecture-and-invariants.md) |
| 2 | Porting Fidelity | Verification of every port claim against the 10 source projects in `other-projects/` | [02-porting-fidelity.md](./02-porting-fidelity.md) |
| 3 | Schema / Security / Ops | Migrations, multi-tenancy, credential vault, cost model, concurrency, operational risk | [03-schema-security-operations.md](./03-schema-security-operations.md) |

**Overall assessment.** The plan's structure is sound, the architectural invariants are the right ones to commit to, and go-ai adoption is a legitimate accelerator. But the plan is **not ready to execute as written** — there are 14 blocking issues and 26 should-fix items, the most severe of which fall into two clusters: (1) foundational schema defects that will break Phases 4/5/9 at `CREATE INDEX` time, and (2) security/crypto under-specification that ships cross-tenant leak potential if implemented verbatim.

---

## Blocking issues — the 14 things that must be fixed before implementation starts

Grouped by failure theme, not by reviewer.

### Cluster A — Schema will not compile / will leak

| # | Title | Reviewer | Where |
|---|---|---|---|
| **A1** | **Untyped `vector` columns cannot have `ivfflat` indexes — Phases 4/5/9 all fail on `CREATE INDEX`.** pgvector requires a declared dimension. The plan's "any dimension any time via application-level validation" is incompatible with the index syntax used in migrations. Also: the 2000-dim pgvector limit blocks `text-embedding-3-large` (3072) entirely. | R3 B1 | §286, §689, §700, §736, §757, §1530, §1541 |
| **A2** | **CHECK constraint added on `agent` / `agent_task_queue` without `NOT VALID`** — runs full-table scan under `ACCESS EXCLUSIVE`. Contradicts the §347 "zero-downtime" claim. Same mistake repeats in Phase 11 / 12 when the constraint is mutated. | R3 B2 | §100–115, §341–349 |
| **A3** | **`knowledge_chunk` and `workflow_step_run` have no `workspace_id`** — workspace scoping depends on JOIN discipline. Single-column PK lookups bypass tenant isolation; vector search can't push workspace into `ivfflat` index. | R3 B6 | §685–692, §964 |
| **A4** | **Idempotency claim is false.** Phase 1 migration uses `ADD COLUMN` / `ADD CONSTRAINT` / `CREATE UNIQUE INDEX` without `IF NOT EXISTS`. Re-running fails, contradicting §340. Repo also has existing duplicate-prefix migrations (029, 032, etc.) that lexicographic sort masks. | R3 B7 | §318–340 |

### Cluster B — Security is demo-grade

| # | Title | Reviewer | Where |
|---|---|---|---|
| **B1** | **Credential vault underspecified** — no KDF, no IV/nonce column, no AAD binding, no key version. AES-256-GCM without AAD means ciphertext can be row-moved between workspaces and still decrypt → cross-tenant credential leak. | R3 B3 | §201–248 |
| **B2** | **Webhook HMAC secrets stored plaintext** — inconsistent with the vault pattern (SSO client secret IS encrypted). DB breach → forge inbound webhooks to trigger any tenant's agents; forge outbound webhooks to impersonate the platform. Fails SOC 2 CC6 / ISO 27001 A.10. | R3 B4 | §1105, §1152 |

### Cluster C — Architectural claims don't hold

| # | Title | Reviewer | Where |
|---|---|---|---|
| **C1** | **D2 claims go-ai ships a built-in `task` tool. It does not.** `SubagentRegistry` is 149 lines of `map[string]Agent` — no tool definition, no depth/cycle/trace propagation, no interim streaming. Phase 6 must author the `task` tool itself. Scope is 2–2.5 weeks, not the quoted 1 week. | R1 B1 | §43, §878, §886, §2155 |
| **C2** | **`trace_id` is relied upon in Phases 2 / 6 / 9 but the `agent_trace` table lands in Phase 10.** Phase 6 cycle detection, Phase 8A `delegation.completed` webhook, and Phase 9 chain-cost budget all have no persistence layer or aggregation query until three phases later. | R1 B2 | §164, §901, §905, §923, §1179, §1632 |
| **C3** | **`Backend.ExpiresAt()` contract does not compose with the fallback-chain wrapper.** When Phase 2.1.1 wraps N backends, the outer `ExpiresAt()` / `OnTimeout` behavior is undefined. In Phase 11, `cloud_coding` with a fallback chain can fire the snapshot hook on the wrong backend or never. | R1 B3 | §417–425, §470–476, §1770–1782 |
| **C4** | **Phase 8A's `Depends on` text is incomplete.** Chat calls Phase 5.4 compaction but the text declares only Phase 2 + 7 dependencies. With the plan's own parallelization encouragement in §2070+, teams will schedule 8A in parallel with 5 and hit dangling compaction at runtime. | R1 B4 | §1052, §1063 |

### Cluster D — Porting claims don't match what's actually on disk

| # | Title | Reviewer | Where |
|---|---|---|---|
| **D1** | **Letta/MemGPT and LangGraph are cited as port sources but don't exist in `other-projects/`.** They're public-literature references, not vendored code — presenting them alongside CrewAI/Hermes/Open Agents in the "Projects Analyzed" table is misleading. Same issue for Replit / Cursor / Claude Managed Agents. | R2 B1 | §14–26, §723 |
| **D2** | **Open Agents Sandbox interface is not "lifted verbatim".** Plan drops `access()` and `getState()`, promotes 5 optional TS methods to required Go methods, invents a 4-value `SandboxType` where source has 1, and changes `Domain` signature. Design choices are fine — the "verbatim" framing is wrong and engineers building a second backend will mis-implement the contract. | R2 B2 | §45, §1721–1779 |
| **D3** | **Three Paperclip file paths are fabricated or wrong.** `packages/db/src/schema/secrets.ts` (real: `company_secrets.ts` + `company_secret_versions.ts`); `packages/adapters/http/execute.ts` (real: `cli/src/adapters/http/index.ts` or `server/src/adapters/http/execute.ts`). Engineers will hunt for nothing. | R2 B3 | §248, §484 |

### Cluster E — Operational correctness

| # | Title | Reviewer | Where |
|---|---|---|---|
| **E1** | **Budget enforcement is a check-then-execute race across three independent code paths** (task claim, subagent dispatch, chat). No transactional counter, no `FOR UPDATE`, no advisory lock described. `hard_stop = true` becomes advisory — tenants can overshoot the monthly limit by 2–10× under load. | R3 B5 | §129–132, §903, §1064–1066 |

---

## Top-priority should-fix items (highest-leverage only)

Full list in the individual reports — these are the ones I'd pull forward into any rewrite:

1. **Split D4 (`RedisAdapter`) into four named interfaces** (`StreamStore`, `KVCache`, `PubSub`, `SortedSet`). A single adapter can't serve prompt-stream resume, skills cache, chat pub/sub, and semantic cache without becoming a Redis-verbatim facade. (R1 S4)
2. **Define a stable `TaskHook` interface in Phase 2.** Phases 3/5/9/10 all modify `server/internal/worker/worker.go` with no declared extension point — five phases editing one file without a contract will drift. (R1 S1)
3. **Add a DB-level immutability enforcement for `audit_log`** (role-based REVOKE + optional trigger). Current app-layer "no UPDATE/DELETE" is advisory only. (R3 S11)
4. **Constrain SSO `default_role`** to `('member', 'admin')` — never `'owner'`. Free-text column allows admin to escalate every JIT-provisioned user. (R3 S10)
5. **`agent_memory.scope` is untrusted string input** — add a CHECK constraint `scope LIKE '/ws/' || workspace_id::text || '/%'` and always prefix-rewrite scope from the authenticated `wsId`. Without this, an agent can write/read across workspaces. (R3 S2)
6. **Cache-aware pricing column naming is ambiguous** — rename `cached_tokens` → `cache_read_tokens`, document `input_tokens` is non-cached-only, add parsing fixtures. Anthropic 1-hour cache (2×) is also unmodeled. (R3 S6)
7. **`cost_event.agent_id` FK is not workspace-scoped.** Reuse the composite index at §135 for composite FKs everywhere (`cost_event`, `budget_policy.scope_id`, `approval_request.agent_id`, `agent_config_revision`, `agent_metric`, `agent_trace.agent_id`, `webhook_endpoint.target_id`). (R3 S1)
8. **Fair scheduling is hand-waved.** `FOR UPDATE SKIP LOCKED` + "round-robin across workspaces" needs explicit SQL — default SKIP LOCKED starves small tenants. Pick MAPQ or a `last_claim_at` ordering and show the query. (R3 S7)
9. **Cadence catch-up policy names are fabricated** (`skip_missed`, `fire_one`, `fire_all`). Real: `SKIP`/`ONE`/`ALL`. Either rename or drop the "port from Cadence" framing. (R2 S1)
10. **Cadence scheduler port is underestimated.** Durable execution without a Temporal/Cadence event-sourced history means at-least-once, not exactly-once — `terminate_previous` can leak on crash. Document the semantic downgrade and add a `(workflow_id, scheduled_time)` unique key. (R2 S2)
11. **IWF `InternalChannel` semantics will drift** (at-least-once vs. exactly-once). Add `consumed_at` on channel messages; consume = conditional UPDATE. (R2 S3)
12. **go-ai `ClearToolUsesEdit` / `ClearThinkingEdit` / `CompactEdit` are Anthropic-only.** Phase 5.4 must fall back to summarise-and-truncate for OpenAI/others. (R2 S4)
13. **Retention cleanup needs batching.** Daily unbatched DELETEs on `task_message` (FTS-indexed), `audit_log`, `agent_trace`, `agent_memory` will lock-contend with the task-queue hot path. Add `DELETE ... LIMIT N` loop pattern or `pg_partman` time-partitioning. (R3 S4)
14. **Chat Redis topology needs spelling out.** `PSUBSCRIBE chat:*:{provider}` with a `SETNX chat_claim:{session_id}:{msg_id}` for single-worker claim. (R3 S8)
15. **Dev-mode gap:** in-memory RedisAdapter + separate server/worker processes = chat messages never delivered. Require `--with-worker` when `memory` adapter is selected. (R3 S9)
16. **Declare workflow steps must be idempotent.** Phase 7's durable execution is state-reload on restart; non-idempotent steps re-fire. (R2 S1 / IWF caveat)

---

## Open questions that need a human decision

These are architectural forks where the plan is ambiguous — pick before implementation:

1. **Does the harness own the timeout timer, or each backend?** Phase 2 §441 implies harness; Phase 11 §1782 implies backend. (R1)
2. **Chat path — reuses `agent_task_queue` or a parallel chat-task table?** Budget/audit/trace coherence depends on the answer. (R1)
3. **`claude_managed` — standalone agent_type or a sandbox backend for `cloud_coding`?** §1945 vs §1831 disagree. (R1)
4. **Delegation stack persistence across worker restarts.** D2 forbids tables; Phase 7 has durable workflows. If a subagent chain resumes after crash, cycles can reform. (R1)
5. **Embedding-dimension partitioning strategy.** Per-workspace fixed dimension, or one table per common dimension (1536/1024/3072)? Current "any dimension any time" is incompatible with pgvector indexing. (R3)
6. **Master key rotation plan.** Add `encryption_version` column now or later? Rotating without it means re-encrypting everything in one transaction — not operationally viable at scale. (R3)

---

## Invariant-by-invariant summary (from Reviewer 1)

| Invariant | Status | Evidence |
|---|---|---|
| D1 (single Go worker binary) | **HELD** | Chat, cloud_coding, tasks all run in same worker |
| D2 (delegation is a tool call, no tables) | **Conclusion held, rationale broken** | go-ai has no built-in `task` tool (C1). Phase 6 must build it. |
| D3 (Sandbox interface from Open Agents) | **Materially edited, not verbatim** | Missing methods, optionality mismatch (D2 above) |
| D4 (Redis baseline + in-memory adapter) | **DRIFTING** | Single interface for 4 diverging consumers (S1 above) |
| D5 (Open Agents as pattern reference only) | **HELD** | No `go.mod` entry; all refs are ports |
| D6 (proactive timeout on Backend) | **DRIFTING** | Fallback-wrapper composition undefined (C3 above) |
| D7 (defaults.go centralization) | **HELD** | Every constant referenced by name in later phases |

---

## What "ready to execute" looks like

If the plan is patched to address the 14 blocking issues above, it's a solid 12-phase roadmap:

- The ~20–22 week estimate becomes ~22–26 weeks once Phase 6 absorbs the missing `task` tool work (+1w) and the scheduler port picks up its durability caveats (+1–2w).
- Phases 1 and 11 need their migrations rewritten with `NOT VALID` patterns — mechanical change, hours not days.
- The credential vault rewrite with AAD + IV + KDF + version is a 3–5 day deliverable but must happen in Phase 1 before any tenant data lands.
- The vector-dimension partitioning decision blocks Phases 4 / 5 / 9 and should be settled before Phase 1 ships.

The porting strategy is mostly sound, the go-ai adoption is a genuine accelerator, and the cross-phase architecture is coherent once trace_id and the `TaskHook` contract are hoisted forward. With the 14 blockers fixed, this plan is executable. Without them, it will fail in predictable, recoverable-but-costly ways during implementation of the phases that touch vectors, credentials, or delegation.

---

*Reports generated 2026-04-14 by three Opus reviewers running in parallel with max reasoning.*

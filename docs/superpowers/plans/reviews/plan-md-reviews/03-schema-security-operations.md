# PLAN.md Review — Data Layer, Security, Multi-Tenancy, Operations (Reviewer 3 of 3)

## Summary verdict

The security and multi-tenancy model is **demo-grade, not production-grade**. Three findings are outright blockers: (1) pgvector `ivfflat` indexes on untyped `vector` columns will fail at `CREATE INDEX` time — this affects Phases 4, 5, and 9 simultaneously; (2) the Phase 1 credential vault specifies AES-256-GCM but leaves IV, AAD, key derivation, and per-workspace salt undefined, while webhook secrets (Phase 8A) are stored plaintext in direct contradiction of the vault pattern; (3) the `agent_runtime_check` CHECK constraint added in-place on the (potentially large) `agent` table will full-scan under `ACCESS EXCLUSIVE` lock, defeating the plan's "zero downtime" claim. Budget enforcement is a classic check-then-execute race across three code paths (task claim, chat, subagent dispatch) with no transactional counter. Migration numbering is safe against the existing 001–035 sequence under lexicographic sort, but duplicate 3-digit prefixes are already a real problem in the repo that the plan inherits.

## Blocking issues

### B1. Untyped `vector` columns cannot have `ivfflat` indexes — Phases 4/5/9 all fail

- **PLAN.md cite:** §286 (rationale), §689, §700 (`knowledge_chunk`), §736, §757 (`agent_memory`), §1530, §1541 (`response_cache`).
- **Problem:** The plan says *"All vector columns in later phases use untyped `vector` (no dimension in DDL). Dimension validation happens at the application level"* and then each migration creates `embedding vector` followed by `CREATE INDEX ... USING ivfflat (embedding vector_cosine_ops)`. pgvector requires a declared dimension to build an `ivfflat` or `hnsw` index — `CREATE INDEX` on an untyped `vector` column raises `"column does not have dimensions"`. The same is true for `hnsw`. The only operations that work on untyped `vector` are storage and sequential scans.
- **Failure mode:** Every migration in Phases 4, 5, 9 fails at `CREATE INDEX`. If someone comments out the index to get the migration through, queries fall back to sequential scan over the entire tenant's memory/knowledge/cache tables — latency will be unusable past a few thousand rows, and `ORDER BY embedding <=> $1 LIMIT N` will melt CPU under concurrent workloads.
- **Fix:** Pick one of two approaches and document it in §1.5:
  1. Fix the dimension per workspace at table-creation time (bad — breaks multi-tenancy if two workspaces want different models).
  2. Partition by dimension: create `knowledge_chunk_1536`, `knowledge_chunk_3072`, `knowledge_chunk_1024` (and `response_cache_*`, `agent_memory_*`) each with a typed `vector(N)` column and its own `ivfflat` index, and route writes/reads based on `workspace_embedding_config.dimensions`. Or keep one table per dimension and make `workspace_embedding_config` a choice among a finite enum. The plan's "any dimension any time" model is incompatible with pgvector indexing.
- Also note the 2000-dim hard limit: `text-embedding-3-large` at 3072 dims cannot be `ivfflat`-indexed at all without Matryoshka reduction to ≤2000.

### B2. CHECK constraint added to `agent` and `agent_task_queue` without `NOT VALID` — full-table scan under ACCESS EXCLUSIVE

- **PLAN.md cite:** §100–115 (the `agent_runtime_check` block), §341–349 ("zero downtime migration").
- **Problem:** `ALTER TABLE agent ADD CONSTRAINT agent_runtime_check CHECK (...)` scans every existing row to validate the predicate, holding `ACCESS EXCLUSIVE` for the duration. Only `ADD COLUMN ... DEFAULT const` is fast-path in PG11+ (metadata-only). `ADD CONSTRAINT CHECK` (without `NOT VALID`) is not. Phase 1 also adds the column and constraint in the same migration, and the plan further intends to mutate this constraint in Phase 11 (add `cloud_coding`) and Phase 12 (add `claude_managed`) per §98–99 and §1843.
- **Failure mode:** On any production workspace with non-trivial `agent` or `agent_task_queue` size, `migrate-up` blocks inserts/updates for seconds to minutes. Phases 11/12 repeat the blocking pattern. The "zero downtime" claim in §347 is factually wrong for the Phase 1 migration.
- **Fix:** Change to the two-step pattern:
  ```sql
  ALTER TABLE agent ADD CONSTRAINT agent_runtime_check CHECK (...) NOT VALID;
  ALTER TABLE agent VALIDATE CONSTRAINT agent_runtime_check; -- scan, but only SHARE UPDATE EXCLUSIVE
  ```
  For Phase 11/12, the correct pattern is `DROP CONSTRAINT ... / ADD CONSTRAINT ... NOT VALID / VALIDATE`. Also update §341–349 to drop the "zero downtime" language for Phase 1 or back it up with this pattern.

### B3. Credential vault underspecified — AAD, IV, KDF, and per-workspace key derivation are all missing

- **PLAN.md cite:** §201–248, §245 (`AES-256-GCM with per-workspace derived key (master key from env var CREDENTIAL_MASTER_KEY)`), §221 (`encrypted_value BYTEA`).
- **Problem:** Every single detail that separates a secure vault from a demo vault is omitted: (a) the KDF used to derive per-workspace keys from the master (HKDF? salt source? versioning?), (b) IV/nonce storage (is it prepended to `encrypted_value` or separate?), (c) AAD binding to `workspace_id`, `credential_id`, or `name` to prevent ciphertext swap between workspaces, (d) a key-version column for rotation of `CREDENTIAL_MASTER_KEY` without re-encrypting everything, (e) authenticated metadata (what if an attacker swaps `workspace_id` on a row — does AAD catch it?). Table also has no `encryption_version` or `wrapped_dek` column.
- **Failure mode:** Without AAD binding, a SQL-injection or compromised admin can move a ciphertext row between workspaces and the cipher still decrypts — cross-tenant credential leak. Without KDF salt + version, rotating the master key requires re-encrypting everything in one transaction, which is operationally impossible at scale. Without IV documented, implementers will likely reuse it (catastrophic for GCM). The plan's §1382 audit-log "no UPDATE/DELETE" is also app-layer only — not enforced at DB role level — so a compromised handler bypasses it.
- **Fix:** Specify explicitly in §1.4:
  - Schema: add `encryption_version SMALLINT NOT NULL DEFAULT 1`, `nonce BYTEA NOT NULL` (12 bytes for GCM), store raw ciphertext in `encrypted_value`.
  - KDF: HKDF-SHA256 with `salt = workspace_id::bytea`, `info = "multica-cred-v1"`, output 32 bytes.
  - AAD: `workspace_id || credential_id || name` — any row mutation that changes those invalidates the MAC.
  - Master-key rotation: support `encryption_version`-keyed master secrets; background job re-wraps on rotate. Document this in §248.

### B4. Webhook and SSO secrets stored plaintext — inconsistent with vault pattern

- **PLAN.md cite:** §1105 (`webhook_endpoint.secret TEXT NOT NULL`), §1152 (`webhook_subscription.secret TEXT NOT NULL`), §1454 (`oidc_client_secret_encrypted BYTEA` — here it IS encrypted), §1450 (`idp_certificate TEXT` — public key, OK).
- **Problem:** `secret TEXT` is HMAC signing material with the same sensitivity as an API key. If the DB leaks (backup, snapshot, read-replica exfil), an attacker can forge webhook events for every tenant and replay signed outbound webhooks. The SSO table gets this right for the client secret and wrong for nothing else, which makes the webhook-plaintext decision look like an oversight.
- **Failure mode:** DB breach → forge inbound webhooks → trigger arbitrary agent tasks as any tenant; forge outbound webhooks → impersonate platform to customers. Compliance audits (SOC 2 CC6, ISO 27001 A.10) will flag both columns as plaintext secrets at rest.
- **Fix:** Store both as `secret_credential_id UUID REFERENCES workspace_credential(id)` or add `secret_encrypted BYTEA` + `secret_nonce BYTEA` + `encryption_version SMALLINT` following the same vault scheme as Phase 1.4. Update handlers to read/unwrap on signature verification. Same treatment for `webhook_subscription.secret`.

### B5. Budget enforcement is a check-then-execute race across three independent paths

- **PLAN.md cite:** §129–132 (task claim), §903 (subagent dispatch: `budget.CanExecute(...)`), §1064–1066 (chat: "same `BudgetService.CanExecute()` call").
- **Problem:** `CanExecute` is a read (sum of `cost_event.cost_cents`) followed by a decision. Three concurrent code paths can all read "under budget" and all dispatch. With large N workers + delegation chains + chat bursts, this will routinely overshoot `monthly_limit_cents`. No row-level lock, no atomic counter, no `FOR UPDATE` on a per-workspace budget row is described anywhere.
- **Failure mode:** `hard_stop = true` becomes advisory. Customer burns through budget and exceeds by 2×–10× under load. Trust and pricing both break.
- **Fix:** Either (a) an explicit per-workspace `budget_ledger` row updated `FOR UPDATE` within the claim transaction, or (b) an atomic `INSERT INTO cost_event RETURNING (SELECT SUM(cost_cents) ...)` guarded by an advisory lock `pg_advisory_xact_lock(hashtext('budget:' || workspace_id))`. Document which is chosen in §132. The chat path (§1065) must use the same lock — the current wording "same call" is insufficient because the call itself is not thread-safe.

### B6. `knowledge_chunk` has no `workspace_id` — workspace scoping depends on JOIN discipline

- **PLAN.md cite:** §685–692.
- **Problem:** `knowledge_chunk` only carries `source_id`. CLAUDE.md says "Workspace-scoped queries must key on `wsId`"; the plan's own §187–191 mandates `WHERE id = $1 AND workspace_id = $2` deletes. Any query that goes `SELECT ... FROM knowledge_chunk WHERE id = $1` (direct retrieval by chunk id, e.g. for citation expansion or admin tools) will not verify workspace membership and a bug in one handler becomes a cross-tenant leak. Also vector search `ORDER BY embedding <=> $q LIMIT N` must filter by a joined `workspace_id` — with no chunk-level column, PG can't push this into the index.
- **Failure mode:** (a) A future handler does a direct `WHERE id = $1` lookup and returns another tenant's chunk. (b) Vector search across all workspaces falls into ANN → post-filter by JOIN, losing recall if the filtered fraction is small.
- **Fix:** Add `workspace_id UUID NOT NULL` to `knowledge_chunk` and to `workflow_step_run` (§964 has the same issue — only `run_id`). Add the composite FK pattern from §135/§147 — it's correct there and should be reused everywhere. Include `workspace_id` in the `ivfflat` index predicate via partial indexes per workspace, or use a `workspace_id = $1` filter in every recall query and benchmark.

### B7. Migration numbering: fine against existing sequence, but the existing sequence already has duplicates the plan ignores

- **PLAN.md cite:** §318–340, §338 ("Each migration must be idempotent").
- **Evidence from repo:** `server/migrations/` contains duplicate prefixes: four `029_*` files, three `032_*` files, two `020_*`, two `026_*`, two `033_*`. `server/cmd/migrate/main.go:75` uses `sort.Strings(files)` (lexicographic). So `032_drop_agent_triggers` < `032_issue_search_index` < `032_runtime_owner` < `032_task_usage` — ordering is deterministic but arbitrary-alphabetical.
- **Lexicographic check:** `"035_"` < `"100_"` lexically (`'0' < '1'`), so Phase 1 migrations correctly run AFTER existing 001–035. That part works.
- **Problem:** The plan's idempotency claim is false. Looking at §95–178 Phase 1 migration: it uses `ALTER TABLE agent ADD COLUMN agent_type ...` — PG does NOT support `ADD COLUMN IF NOT EXISTS` on older versions, and even on 9.6+ where it does, the plan doesn't write it. Same for `ADD CONSTRAINT` (no IF NOT EXISTS). `CREATE UNIQUE INDEX idx_agent_workspace_id` (§135) is also missing `IF NOT EXISTS`. So re-running any Phase 1 migration fails — contradicting §340.
- **Fix:** Either (a) add `IF NOT EXISTS` to every DDL that supports it and document that ADD CONSTRAINT uses a guard `DO $$ BEGIN ... EXCEPTION WHEN duplicate_object THEN NULL; END $$;`, or (b) remove the idempotency claim. The migration runner in `server/cmd/migrate/main.go` already tracks `schema_migrations` and short-circuits — idempotency at the SQL level is belt-and-suspenders, but the plan asserts it and fails to deliver it.

## Should-fix issues

### S1. `cost_event.agent_id` FK is not workspace-scoped — cross-tenant link possible
- **Cite:** §152–166. `agent_id UUID REFERENCES agent(id) ON DELETE SET NULL` — single-column FK. Nothing prevents a bug from inserting a `cost_event` with `workspace_id=A` and `agent_id` belonging to workspace B.
- **Fix:** Composite FK `FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id)` — the plan *already* added the composite unique index at §135 to enable this; reuse it everywhere (`cost_event`, `budget_policy.scope_id`, `approval_request.agent_id`, `agent_config_revision`, `agent_metric`, `agent_trace.agent_id`, `webhook_endpoint.target_id`).

### S2. `agent_memory.scope` is untrusted string input — prefix-injection risk
- **Cite:** §739, §758, §793 (`scope="/ws/{wsId}/"` passed into `LIKE prefix queries`).
- **Problem:** The plan says agents can call `save_memory` (§822) and recall walks by `LIKE` prefix. If any code path constructs the scope from agent-provided input without stripping `../` or validating the UUID, an agent in workspace A can write a memory with `scope='/ws/{wsB}/agent/xyz/'` or query with `scope='/ws/'` to read across workspaces.
- **Fix:** Enforce `scope LIKE '/ws/' || workspace_id::text || '/%'` as a CHECK constraint, and always prefix-rewrite scope in `MemoryService` from the authenticated `wsId`. Never accept scope-prefix shorter than `/ws/{wsId}/` from callers.

### S3. Provider health is per-worker-process memory only — §478–482 is a latent footgun
- **Cite:** §480 ("Shared across all workers via the worker's process memory (no external state needed)").
- **Problem:** With N workers, each has its own 5-min rolling window. If Anthropic hiccups for 10 seconds, one worker's window records enough failures to trip "degraded" while others don't — inconsistent routing. The plan presents this as intentional but doesn't say so.
- **Fix:** Either declare this intentional in §482 with the tradeoff explained, or move to Redis (D4 already makes Redis baseline). A Redis sorted set of recent failures keyed by `health:{provider}` is trivial.

### S4. Retention cleanup can run unbounded DELETEs on large tables
- **Cite:** §1486–1494, "Runs daily". Target tables include `task_message` (which §1665 adds a FTS `tsvector` GENERATED column — every DELETE triggers tsvector recompute on cascades), `audit_log`, `agent_trace`, `agent_memory`.
- **Problem:** A single `DELETE WHERE created_at < now() - interval '365 days'` on a multi-million-row table holds locks, bloats indexes, and can conflict with the task-queue hot path. No batching described.
- **Fix:** Document batch-deletion pattern (`DELETE ... LIMIT 10000` in a loop with a 1s sleep) and add `CREATE INDEX` on `(workspace_id, created_at)` for every retention-eligible table. For `agent_trace`, consider `pg_partman` time-partitioning so old partitions `DETACH` + `DROP` instead of DELETE.

### S5. Response cache records $0 cost events (or doesn't) — unspecified
- **Cite:** §1522–1548, §162–163 (`cost_status ∈ actual|estimated|included`).
- **Problem:** Plan doesn't say whether a cache-hit emits a `cost_event` row (with `cost_cents = 0`, `cost_status = 'included'`). If it doesn't, cost dashboards undercount agent activity; if it does, the cost model is consistent but §1544 ("Before LLM call, check cache") must describe the insert.
- **Fix:** Specify in §9.2 that cache hits emit a `cost_event` with `cost_status = 'included'`, `cost_cents = 0`, `input_tokens = 0` — plus a `response_cache_id` FK column for traceability (currently there is no way to link a `cost_event` to the cache entry it served from).

### S6. Cache-aware pricing semantics ambiguous
- **Cite:** §160–162 (`cached_tokens INT`, `cache_write_tokens INT`), §529 ("cache reads at 0.1x, cache writes at 1.25x").
- **Problem:** Anthropic reports usage as `input_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`. The plan's column naming says `cached_tokens` — ambiguous whether this is "tokens served from cache" (read) or total cached tokens ever. If implementers treat `input_tokens` as total including cached (it's not in the Anthropic response), cost is double-counted.
- **Fix:** Rename to `cache_read_tokens`, document that `input_tokens` is "non-cached input tokens only" and pricing = `input_tokens * rate + cache_read_tokens * 0.1 * rate + cache_write_tokens * 1.25 * rate`. Add a property-based test in §1.3 that validates Anthropic's response parsing against fixtures.

### S7. Task queue fair scheduling is hand-waved
- **Cite:** §307, §567 ("`FOR UPDATE SKIP LOCKED` + round-robin across workspaces").
- **Problem:** `SKIP LOCKED` + an `ORDER BY` that uses `MOD(hash(workspace_id), N)` or similar round-robin requires explicit windowing; plan doesn't describe it. With 1 noisy workspace + 50 quiet ones and 10 workers, all workers can lock rows from the noisy workspace and skip past the quiet ones' tasks. Starvation is the default outcome of naïve SKIP LOCKED.
- **Fix:** Document the actual claim query. A known-good pattern is: `SELECT ... FROM agent_task_queue WHERE status='queued' ORDER BY (workspace_id, created_at) LIMIT 1 FOR UPDATE SKIP LOCKED` is still not fair — need a "last claim per workspace" ordering via a materialized `workspace_last_claim_at` or the Cadence MAPQ pattern referenced in §17. Pick one and show the SQL.

### S8. Chat Redis topology is subscribed-wide — document the fan-out cost
- **Cite:** §1072 (`chat:{workspace_id}:{agent_provider}`).
- **Problem:** Every worker subscribes to `chat:*:anthropic`, `chat:*:openai`, etc., not per-workspace channels. Redis pattern subscriptions (`PSUBSCRIBE`) work but have per-message fan-out cost proportional to matching patterns. With thousands of tenants, the plan's wording is ambiguous: is there one channel per (ws, provider)? If yes, workers `PSUBSCRIBE chat:*:anthropic` — OK. If `SUBSCRIBE` per tenant — explosion.
- **Fix:** Explicitly state the worker uses `PSUBSCRIBE chat:*:{provider}` and that the message key carries the workspace; add a claim step (`SETNX chat_claim:{session_id}:{msg_id}`) so only one worker handles a given message.

### S9. Single-process fallback gap: `--with-worker` vs embedded Redis adapter
- **Cite:** §1083 ("For single-process deployments (`--with-worker` mode), bypass Redis entirely — route directly to embedded worker goroutine via Go channel").
- **Problem:** The plan defines `RedisAdapter` with `redis` and `memory` implementations (§47, §411). Memory adapter implies an in-process Redis simulation. But §1083 says bypass adapter entirely for `--with-worker`. What happens when the server runs WITHOUT `--with-worker` but uses the `memory` adapter (e.g. dev environment with a separate worker process)? Messages go into server's memory channel, worker never sees them. This is a real hole in dev topology.
- **Fix:** Either drop the `memory` adapter for chat or require `--with-worker` whenever `memory` adapter is selected (fail-fast startup check).

### S10. SSO auto-provisioning privilege model is undefined
- **Cite:** §1438 ("Auto-provisioning: users from IdP automatically added to workspace on first login"), §1458 (`default_role TEXT DEFAULT 'member'`).
- **Problem:** Existing RBAC is `owner|admin|member` (`server/migrations/001_init.up.sql:30`). The SSO config has `default_role` as a free-text column with no CHECK constraint — an admin can set `'owner'` and every IdP login becomes workspace owner. There's no audit trail on the provisioning event either.
- **Fix:** Add `CHECK (default_role IN ('member', 'admin'))` — never `'owner'`. Emit an `audit_log` row (actor_type='system', action='member.provisioned_via_sso') on each JIT creation. Require an admin to manually approve the first user from a new SSO domain (greylisting).

### S11. Audit log immutability is app-layer only
- **Cite:** §1382 ("Immutable: no UPDATE or DELETE on this table").
- **Problem:** Enforcement described is a convention. A compromised handler or SQL-injection bypasses it. No DB-level `REVOKE UPDATE, DELETE ON audit_log FROM app_user` is prescribed.
- **Fix:** Add a migration step that creates a restricted role, `REVOKE UPDATE, DELETE ON audit_log FROM PUBLIC`, and optionally a `BEFORE UPDATE OR DELETE` trigger that raises. Retention cleanup (§1486–1494) needs a separate privileged role that can DELETE — document this split.

### S12. Plan does not enumerate the WebSocket event catalog required by CLAUDE.md
- **Cite:** CLAUDE.md "WS events invalidate queries". Plan introduces new server-side events for task status, workflow step, approval, webhook delivery, chat message, trace span, agent metric, audit log, budget alert, SSO-provisioned-user — but §441, §1087, §1185, Phase 10 UI, Phase 11 sandbox dashboard all just say "broadcast" or "streams". No central catalog.
- **Fix:** Add a §0 or per-phase subsection listing the exact event name/payload shape for every WS event the backend emits, so the frontend can build `useEffect(() => queryClient.invalidateQueries(...), [event])` without guessing.

## Migration audit

| Phase | Migration # | What it does | Status | Note |
|---|---|---|---|---|
| 1 | 100 agent_types | ADD COLUMN + ADD CONSTRAINT on `agent`, `agent_task_queue`; new tables `agent_capability`, `cost_event`, `budget_policy` | **Needs-fix** | B2 (CHECK w/o NOT VALID), B7 (no IF NOT EXISTS) |
| 1 | 101 credentials | `workspace_credential`, `credential_access_log` | **Needs-fix** | B3 (crypto underspecified). Missing `nonce`, `encryption_version`, `updated_at`, `type`-scoped uniqueness |
| 1 | 102 embedding_config | `workspace_embedding_config` | Safe, but B1 downstream |
| 4 | 125 knowledge | `knowledge_source`, `knowledge_chunk`, `agent_knowledge`, ivfflat index | **Blocking** | B1 (untyped vector + ivfflat), B6 (chunk lacks workspace_id) |
| 4 | 126 structured_output | `ALTER TABLE agent_task_queue ADD COLUMN output_schema/structured_result` | Safe (JSONB nullable) |
| 5 | 130 agent_memory | `agent_memory` + ivfflat + text_pattern_ops index | **Blocking** | B1, S2 (scope injection) |
| 7 | 150 workflows | `workflow_definition`, `workflow_run`, `workflow_step_run`, `approval_request` | **Needs-fix** | B6 (`workflow_step_run` no workspace_id) |
| 8A | 160 webhooks | `webhook_endpoint`, `webhook_delivery`, `webhook_subscription`, `webhook_delivery_log` | **Needs-fix** | B4 (plaintext secrets) |
| 8A | 161 templates | `agent_template` | Safe |
| 8A | 162 api_keys | `workspace_api_key` | Safe (hash stored correctly) |
| 8B | 170 audit_log | `audit_log` | **Needs-fix** | S11 (no DB-level immutability) |
| 8B | 171 agent_config_revision | revision history | Safe |
| 8B | 171 sso | `workspace_sso_config` | **Needs-fix** | S10 (default_role unconstrained) |
| 8B | 172 retention | `retention_policy` + daily cleanup | **Needs-fix** | S4 (no batching / partitioning) |
| 9 | 180 response_cache | `response_cache` + ivfflat | **Blocking** | B1, S5 (cost event for hits unspecified) |
| 9 | 181 agent_metric | aggregation table | Safe |
| 10 | 185 traces | `agent_trace` + `task_message` FTS tsvector generated column | **Needs-fix** | Generated tsvector column on `task_message` — existing 2M-row table rewrite. Use `CREATE INDEX CONCURRENTLY` + a non-stored trigger instead. Also re-adds lock pressure for retention (S4). |
| 11 | 190 sandbox_templates | `sandbox_template`, `workspace.sandbox_backend/sandbox_config`, `agent.sandbox_template_id` | **Needs-fix** | B2 pattern repeats (ADD CONSTRAINT CHECK on `workspace.sandbox_backend`) |
| 12 | 195 managed_agents | `ALTER TABLE cost_event ADD session_hours/session_cost_cents` | Safe; but ALTER'ing the constraint from §111 again needs NOT VALID |

## Multi-tenancy audit

| Table | Has `workspace_id`? | Queries scope by it? | Cross-tenant risk |
|---|---|---|---|
| `agent_capability` (§137) | Yes + composite FK | Plan mandates it (§187–191) | Low |
| `cost_event` (§152) | Yes | Not enforced via FK | **S1** — single-col FK to `agent(id)` |
| `budget_policy` (§169) | Yes | Assumed | `scope_id` is untyped UUID — no FK. Medium |
| `workspace_credential` (§213) | Yes, UNIQUE(ws_id,name) | §242 composite index | Low at schema layer; **B3** at crypto layer |
| `credential_access_log` (§233) | Yes | — | Low; no composite FK to cred |
| `workspace_embedding_config` (§255) | UNIQUE | ✓ | Low |
| `knowledge_source` (§674) | Yes | ✓ | Low |
| `knowledge_chunk` (§685) | **No** | Via JOIN only | **B6 — High** |
| `agent_knowledge` (§694) | No (composite PK) | Via JOIN | Low (join-path safe) |
| `agent_memory` (§730) | Yes + scope prefix | ✓ | **S2** — scope string can be spoofed |
| `workflow_definition` (§937) | Yes | ✓ | Low |
| `workflow_run` (§949) | Yes | ✓ | Low |
| `workflow_step_run` (§964) | **No** | Via JOIN run_id | **B6 — Medium** |
| `approval_request` (§981) | Yes | ✓ | Low (but `decided_by` has no ws check — §982 doesn't constrain actor to ws member) |
| `webhook_endpoint` (§1101) | Yes | ✓ | **B4** plaintext secret |
| `webhook_delivery` (§1114) | Yes | ✓ | Low |
| `webhook_subscription` (§1147) | Yes | ✓ | **B4** plaintext secret |
| `webhook_delivery_log` (§1159) | Yes | ✓ | Low |
| `agent_template` (§1194) | Nullable (system templates) | Plan says so | Low, but ensure reads filter `(workspace_id IS NULL OR workspace_id = $1)` explicitly |
| `workspace_api_key` (§1233) | Yes | ✓ | Low |
| `audit_log` (§1362) | Yes | ✓ | **S11** DB-level mutability |
| `agent_config_revision` (§1407) | Yes | ✓ | Low |
| `workspace_sso_config` (§1443) | UNIQUE | ✓ | **S10** role escalation |
| `retention_policy` (§1476) | Yes | ✓ | Low |
| `response_cache` (§1526) | Yes | ✓ | **B1** index |
| `agent_metric` (§1556) | Yes | ✓ | Low |
| `agent_trace` (§1632) | Yes | ✓ | Low |
| `sandbox_template` (§1894) | Yes | ✓ | Low |

## Security hotspots

- **Credential vault** (§245): B3 — missing AAD, IV scheme, KDF spec, key versioning. Production-grade vaults bind ciphertext to a tuple (ws, id, name) via AAD so DB row-moving attacks fail. Plan doesn't. Fix mandatory before any real tenant data is stored.
- **Webhook secrets** (§1105, §1152): B4 — plaintext HMAC keys. Forgeable cross-tenant on DB breach.
- **SSO** (§1438–1458): S10 — `default_role` free-text, no audit on JIT provisioning, no greylist.
- **Audit log** (§1382): S11 — immutability enforced only at app layer.
- **Scope injection** (§739, §793): S2 — memory scope is a string prefix, not validated against authenticated workspace.
- **API key rate limiting** (§1251): §1393 says "Rate-limited per key" but no schema column for limit, and §1398 rate limiter is keyed by workspace/agent/provider — not by API key. Add per-key limits to the `workspace_api_key` table.
- **Credential access logging coverage** (§246): "Access logged for audit compliance" — but §209 says credentials are "injected as env vars or tool config at execution time". Once injected, subsequent uses inside the worker/tool aren't logged. If the access-log only records the initial `GetCredential` call, that's a big gap for per-tool-call audit requirements.

## Operational risks

- **Concurrency (B5):** Budget race across task claim, chat, subagents — hard_stop becomes advisory.
- **Fair scheduling (S7):** `FOR UPDATE SKIP LOCKED` without per-workspace round-robin starves small tenants.
- **Redis pub/sub semantics (S8, S9):** Dev-mode memory adapter breaks when server and worker are separate processes; production `PSUBSCRIBE` cost is unestimated.
- **Retention cleanup (S4):** Daily unbatched DELETEs can lock-contend with the task-queue hot path. `task_message` in particular (FTS-indexed in §1665) gets expensive.
- **FTS generated-column migration (§1665):** Adding `tsvector GENERATED ALWAYS AS (...) STORED` to an existing populated table rewrites the whole table — contradicts the zero-downtime claim. Use a trigger-based approach with `CREATE INDEX CONCURRENTLY`.
- **Sandbox warm pool (§11.4):** "destroy-and-replace after each session" is sound for isolation; but cost model (§1889) doesn't account for idle pool hours in `cost_event`. No mention of who eats that cost (platform vs tenant).
- **Provider health (S3):** Per-worker-process memory means inconsistent routing — fine for 1–3 workers, bad at scale.
- **CI test coverage (§2127–2142):** Plan acknowledges pgvector needs real PG. But B1 means the integration tests themselves can't create the index — tests will fail before proving anything. Need the dimension-partitioned schema first.
- **Phase 5 cross-agent integration test (§2134):** Listed, which is good. But there's no test that validates S2 (scope spoofing) — add a negative test that attempts to write with a forged scope and asserts it's rejected.

## Cost model consistency

- **S6 (ambiguous column naming):** `cached_tokens` vs Anthropic's `cache_read_input_tokens` — double-count risk. Rename + document.
- **S5 (cache-hit cost events):** Unspecified whether `response_cache` hits emit a `cost_event`. If not, dashboards lie; if yes, FK to cache entry missing.
- **Cache-write pricing (§529):** 1.25x is correct for Anthropic's 5-min cache, but Anthropic also offers a 1-hour cache at 2x. Plan doesn't distinguish — if a tenant opts into 1-hour cache, costs are under-reported.
- **Batch API discount (§1519):** "50% discount, stacks with prompt caching for up to 95% savings" — compounded discounts must be applied multiplicatively. Specify: `cost = base * (1 - batch_discount) * cached_fraction_adjustment`. Implementers will get this wrong without a formula.
- **Session-hour cost (Phase 12, §2011):** `ALTER TABLE cost_event ADD COLUMN session_hours DECIMAL` — `DECIMAL` without precision/scale defaults to unlimited. Specify `DECIMAL(10,4)` or `NUMERIC(10,4)`.
- **Cost-status transitions (§163):** `cost_status` ∈ `actual|estimated|included`. Plan never says when a row moves from `estimated` → `actual`. If an UPDATE is allowed on `cost_event`, that's an audit-log gap (§1382 wants immutability everywhere). Either document the transition as a separate row with `cost_status = 'adjustment'`, or declare `cost_event` append-only and model reconciliation as two rows.
- **Trace ↔ cost coupling (§1642–1652):** `agent_trace.cost_event_id` + `cost_event.trace_id` is a two-way link. Spec currently only FKs one direction; if `cost_event` is logged first (for per-token billing) and `agent_trace` created after the stream closes, there's a window where `cost_event.trace_id` is NULL. Document the ordering — or make trace_id nullable with a CHECK-later backfill.

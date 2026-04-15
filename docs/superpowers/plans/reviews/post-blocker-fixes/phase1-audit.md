# Phase 1 Plan — Post-Blocker Audit

## Summary

The Phase 1 implementation plan predates the blocker-fix pass. ~15 mechanical edits needed across migrations 100–103, sqlc queries, and frontend types. The plan is executable once these land — gaps are mechanical, not structural.

## Required edits (condensed)

**E1.** Migration 100 `agent_type_check` — use `DROP IF EXISTS → ADD ... NOT VALID → VALIDATE` pattern.
**E2.** Migration 100 `agent_runtime_check` — rename from `agent_type_runtime_check`, use NOT VALID + VALIDATE. Update down file.
**E3.** Migration 100 `provider` / `model` — add `IF NOT EXISTS`.
**E4.** Migration 100 idempotency — `CREATE UNIQUE INDEX IF NOT EXISTS`, `CREATE TABLE IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS` on capability indexes.
**E5.** Migration 101 `cost_event` — rename `cached_tokens` → `cache_read_tokens`; add `cache_ttl_minutes`, `session_hours DECIMAL(10,4)`, `session_cost_cents`, `response_cache_id`; `trace_id UUID NOT NULL`; cost_status CHECK enum `('actual','estimated','included','adjustment')` (drop `'test'`); composite FK `(workspace_id, agent_id) REFERENCES agent(workspace_id, id)`; rename index to `idx_cost_event_ws_time`; non-partial `idx_cost_event_trace(trace_id, workspace_id)`.
**E6.** Migration 101 `budget_policy` — add IF NOT EXISTS to every statement.
**E7.** Migration 102 `workspace_credential` — add `nonce BYTEA NOT NULL` (+ 12-byte CHECK), `encryption_version SMALLINT NOT NULL DEFAULT 1`, `updated_at`; `credential_type` CHECK moved to NOT VALID/VALIDATE with enum `('provider','tool_secret','webhook_hmac','sso_oidc_secret')`; composite unique index `idx_workspace_credential_ws_id(workspace_id, id)`.
**E8.** Migration 102 `credential_access_log` — add `trace_id UUID`, `access_purpose TEXT NOT NULL`; composite FK `(workspace_id, credential_id) REFERENCES workspace_credential(workspace_id, id)`; add `idx_credential_log_ws_time`.
**E9.** Migration 103 `workspace_embedding_config` — add `CHECK (dimensions IN (1024, 1536, 3072))`; use CREATE TABLE IF NOT EXISTS.
**E10.** SQLC `CreateCostEvent` — rename param, add new column params.
**E11.** SQLC `SumCostByWorkspaceMonth`/`ByAgentMonth` — filter `cost_status IN ('actual','estimated')` (drop `'test'`).
**E12.** Frontend `CostEvent` TS type — rename + add new fields; `trace_id: string` (non-null).
**E13.** SQLC `CreateCredentialAccessLog` — add `trace_id`, `access_purpose` params.
**E14.** SQLC `CreateCredential` / `UpdateCredentialValue` — add `nonce`, `encryption_version` params; bump `updated_at`.
**E15.** Task 10 commit message — cite Paperclip `company_secrets.ts + company_secret_versions.ts` (not `secrets.ts`).

## Missing tasks

**M1. BudgetService with `pg_advisory_xact_lock`** — `server/internal/service/budget.go` + test. `CanDispatch(ctx, tx, wsID, estCents) (Decision, error)` takes advisory lock in caller's tx, aggregates `SUM(cost_cents) WHERE cost_status IN ('actual','estimated')`, enforces hard_stop. Phase 1 owns the file; later phases wire calls.
**M2. Append-only invariant for cost_event** — lint/test that no UPDATE/DELETE queries target cost_event post-insert.
**M3. D8 verification step** — Phase 1.9 must verify `NULL trace_id` insert fails and `idx_cost_event_trace` is full (not partial).
**M4. Port-source honesty footnote** — Claude Managed Agents is public-API integration, not code port.

## Obsolete tasks

**O1.** `'test'` cost_status literal (now `'adjustment'`).
**O2.** Nullable `trace_id` everywhere (now NOT NULL per D8).
**O3.** Plain `encrypted_value`-only credential schema.
**O4.** Single-column FK `agent_id UUID REFERENCES agent(id)` on cost_event + access_log.
**O5.** Plain `ADD CONSTRAINT CHECK` pattern (must be NOT VALID → VALIDATE).
**O6.** Non-idempotent CREATE TABLE/INDEX without `IF NOT EXISTS`.

## Verdict

Confidence: high (85%). Mechanical edits only. Risk is SQLC-generated Go struct renames (`CachedTokens` → `CacheReadTokens`) rippling into handlers — surface at Task 7 `go build ./...` time.

# Gateway Ingest Keys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add workspace-scoped application ingest keys so Observer SDK telemetry can write to `/v1/traces` without reusing user proxy Gateway keys.

**Architecture:** Keep existing `mgw_` user keys for OpenAI/Anthropic-compatible proxy traffic. Add a separate `mig_` ingest key table, management API, CLI commands, and trace-ingest authentication path. `/v1/traces` accepts both legacy `mgw_` keys and new `mig_` keys during the migration window, while model proxy routes continue to accept only `mgw_`.

**Tech Stack:** Go, PostgreSQL migrations, sqlc, Cobra CLI, existing Gateway management/ingest services.

---

## Scope

- Add `gateway_ingest_key` persistence with encrypted retrievable key values, hashed lookup, last-used timestamp, revocation, creator, display name, and optional app ID.
- Add management service methods for create/list/revoke.
- Add authenticated admin API routes under `/api/gateway/ingest-keys`.
- Add `multica gateway ingest-key`, `multica gateway ingest-keys`, and `multica gateway revoke-ingest-key`.
- Update `/v1/traces` to accept `mig_` keys and persist sessions with workspace context and no user owner.
- Preserve backward compatibility for existing `mgw_` trace ingestion.
- Keep proxy routes rejecting `mig_` keys.

## Verification

- Write failing tests first for key format, management flow, handler ingest, proxy rejection, and CLI behavior.
- Run targeted tests to confirm the new tests fail before implementation.
- Implement schema, generated DB accessors, service logic, handlers, and CLI.
- Run:
  - `go test ./internal/gateway/keyring ./internal/gateway/proxy -count=1`
  - `DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./internal/gateway/management ./internal/handler ./cmd/multica ./cmd/server -run 'Ingest|Gateway' -count=1`
  - `DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./...`
  - `pnpm --filter @multica/observer test`
  - `cd sdks/python && python3 -m pytest`

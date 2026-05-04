# Observer Trace Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the first Observer trace ingestion endpoint so SDKs and enterprise apps can submit sessions, spans, logs, events, agent observations, and tool observations into the existing Gateway telemetry model.

**Architecture:** The first slice reuses existing Multica Gateway keys (`mgw_...`) for authentication and stores telemetry in the current PostgreSQL `gateway_*` tables. The endpoint is synchronous JSON ingest mounted outside normal user JWT auth at `/v1/traces`, using Gateway-key auth to resolve workspace and user context. Dedicated app ingest keys, SDK packages, and OTLP compatibility remain follow-on slices.

**Tech Stack:** Go 1.26, chi, pgx/sqlc, PostgreSQL, existing Gateway keyring and telemetry schema.

---

## File Structure

- Modify `server/pkg/db/queries/gateway_telemetry.sql`: add upsert/read queries needed by ingest.
- Regenerate `server/pkg/db/generated/gateway_telemetry.sql.go` with `make sqlc`.
- Create `server/internal/gateway/ingest/types.go`: request/response structs and validation constants.
- Create `server/internal/gateway/ingest/service.go`: Gateway-key-authenticated ingest service that writes sessions, spans, events, logs, agents, and tools.
- Create `server/internal/gateway/ingest/service_test.go`: service-level validation tests.
- Create `server/internal/handler/gateway_ingest.go`: HTTP handler for `/v1/traces`.
- Create `server/internal/handler/gateway_ingest_test.go`: handler tests covering auth, persistence, and validation.
- Modify `server/internal/handler/handler.go`: add `GatewayIngest *ingest.Service`.
- Modify `server/cmd/server/router.go`: mount `POST /v1/traces`.
- Modify `docs/superpowers/specs/2026-05-03-multica-gateway-design.md`: update default capture-policy text to `full_content`.
- Modify `docs/superpowers/specs/2026-05-03-dario-agentops-feature-backlog.md`: remove the now-stale caution that full content must not be default, replacing it with an enterprise deployment warning.

## Task 1: SQL And Service Contract

- [ ] Write failing tests for ingest payload validation and persistence behavior.
- [ ] Add sqlc queries:
  - `UpsertGatewaySessionForIngest`
  - `UpsertGatewaySpanForIngest`
  - `GetGatewaySpanByTraceSpanID`
  - `CreateGatewayEventForIngest`
  - `CreateGatewayLogForIngest`
  - `CreateGatewayAgentObservationForIngest`
  - `CreateGatewayToolObservationForIngest`
  - `RefreshGatewaySessionIngestSummary`
- [ ] Regenerate sqlc output with `make sqlc`.
- [ ] Implement `server/internal/gateway/ingest` with validation, timestamp parsing, JSONB conversion, span lookup, and transactional writes.
- [ ] Verify with `go test ./internal/gateway/ingest -count=1`.

## Task 2: HTTP Route

- [ ] Write failing handler tests for:
  - missing Gateway key returns 401;
  - invalid payload returns 400;
  - valid payload returns created session/span counts and persists rows.
- [ ] Add `GatewayIngest` to `handler.Handler`.
- [ ] Add `PostGatewayTraceIngest` in `gateway_ingest.go`.
- [ ] Mount `POST /v1/traces` in `server/cmd/server/router.go`.
- [ ] Verify with `go test ./internal/handler -run GatewayIngest -count=1`.

## Task 3: Documentation And Regression

- [ ] Update specs so the documented default capture policy is `full_content`.
- [ ] Run `go test ./internal/gateway/ingest ./internal/handler -run 'GatewayIngest|GatewayObservability' -count=1`.
- [ ] Run `go test ./internal/gateway/... ./internal/handler -run Gateway -count=1`.
- [ ] Run `go test ./...`.
- [ ] Commit the completed slice.

## Acceptance Criteria

- `/v1/traces` accepts `Authorization: Bearer mgw_...` and `x-api-key: mgw_...`.
- Ingest creates or updates a Gateway session by `(workspace_id, trace_id)`.
- Ingest creates or updates spans by `(workspace_id, trace_id, span_id)`.
- Logs, events, agent observations, and tool observations can attach to ingested spans by external span ID.
- The existing Gateway dashboard/session drilldown APIs can read ingested spans, logs, events, agents, and tools without a new read API.
- Bad keys and malformed payloads do not leak internal errors.

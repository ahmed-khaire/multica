# Gateway Observability Read API Plan

## Goal

Expose dashboard-ready Observer Gateway telemetry APIs for the first AgentOps-inspired observability layer:

- Session overview metrics for the selected workspace and time window.
- Session list rows with request/model/token/error aggregates.
- Session drilldown with spans, requests, model calls, events, logs, agents, and tools.
- LLM call list with captured prompt/completion payloads governed by workspace capture policy.
- Typed frontend API client models so the next UI phase can bind to stable contracts.

The hosted Gateway proxy already writes `gateway_session`, `gateway_request`, and `gateway_model_call` rows. This phase turns that stored telemetry into product surfaces.

## References

- Multica Gateway design spec: `docs/superpowers/specs/2026-05-03-multica-gateway-design.md`
- AgentOps dashboard reference: session overview, session drilldown, LLM/chat-history display, waterfall spans, event/tool/error breakdowns.
- Local AgentOps repo references:
  - `app/dashboard/types/ITrace.ts`
  - `app/dashboard/types/ISpan.ts`
  - `app/dashboard/components/spans-list.tsx`
  - `app/dashboard/hooks/useTraces.ts`

## Scope

### API Endpoints

Add authenticated, workspace-member routes under `/api/gateway`:

- `GET /api/gateway/overview`
- `GET /api/gateway/sessions`
- `GET /api/gateway/sessions/{id}`
- `GET /api/gateway/sessions/{id}/spans`
- `GET /api/gateway/llm-calls`

Supported query params:

- `since`: RFC3339 timestamp or relative duration like `24h`, `7d`, `30d`; default `7d`.
- `limit`: default `50`, max `200`.
- `status`: optional session/request status filter where applicable.
- `model`: optional model filter for LLM calls.
- `backend`: optional provider/backend slug filter for sessions and LLM calls.

### SQL Queries

Extend `server/pkg/db/queries/gateway_telemetry.sql` with read-side queries:

- Overview aggregate over sessions/requests/model calls.
- Time-series buckets for request/error/token/cost trends.
- Top models by calls/tokens/cost.
- Top backends by calls/errors/latency.
- Dashboard session list rows with derived request, LLM call, token, backend, model, and cost fields.
- Session detail subresources: requests, model calls, events, logs, agents, tools.
- Global LLM call list rows with session/request context.

Regenerate sqlc output with `make sqlc`.

### Service Layer

Add `server/internal/gateway/observability`:

- Parse and clamp read filters.
- Map sqlc rows to JSON response structs.
- Normalize timestamps, UUIDs, nullable fields, numerics, and JSONB payloads.
- Preserve capture-policy behavior by returning only what was stored in telemetry tables.
- Hide raw database errors behind handler-level generic failures.

### Handler Layer

Add `server/internal/handler/gateway_observability.go`:

- Reuse `gatewayRequestScope` for user/workspace auth and membership checks.
- Return 400 for invalid filters and IDs.
- Return 404 for missing sessions.
- Mount routes in `server/cmd/server/router.go`.

### Frontend Client Types

Add Gateway observability response/request types to `packages/core/types/api.ts` and methods to `packages/core/api/client.ts`:

- `getGatewayOverview`
- `listGatewaySessions`
- `getGatewaySession`
- `getGatewaySessionSpans`
- `listGatewayLLMCalls`

## Tests

Add tests before implementation:

- Unit tests for filter parsing defaults, clamping, and invalid values.
- Handler integration tests that seed telemetry rows and assert:
  - Overview returns totals, time series, top models, top backends.
  - Sessions list aggregates request/model usage.
  - Session detail includes requests, model calls, events, logs, agents, and tools.
  - Session spans returns AgentOps-compatible waterfall fields.
  - LLM calls supports model/backend/status filters.
  - Missing session returns 404 and malformed filters return 400.

## Verification

Run:

- `make sqlc`
- `go test ./internal/gateway/observability -count=1`
- `go test ./internal/handler -run GatewayObservability -count=1`
- `go test ./internal/gateway/... ./internal/handler -run Gateway -count=1`
- TypeScript check for touched packages if available.

## Out Of Scope For This Phase

- Full React Gateway dashboard UI.
- OTLP ingestion/export.
- ClickHouse rollups.
- Governance enforcement and third-party risk dashboards.
- Cost pricing enrichment beyond stored `gateway_model_call.total_cost`.

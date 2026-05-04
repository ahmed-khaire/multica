# TypeScript Observer SDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a lightweight TypeScript SDK that enterprise Node applications and agents can use to create trace context, inject Multica Gateway correlation headers, and submit spans/logs/tools to the new `/v1/traces` ingest endpoint.

**Architecture:** Create a standalone workspace package, `@multica/observer`, so SDK code stays independent from React UI packages and can later be published. The first slice is dependency-light and fetch-based: it generates IDs, builds payloads compatible with the Go ingest endpoint, and submits them with a Multica Gateway key. Deep framework auto-instrumentation, local buffering, npm publishing, and OTLP export are later slices.

**Tech Stack:** TypeScript 5, Vitest, Fetch API, existing pnpm workspace and `@multica/tsconfig`.

---

## File Structure

- Create `packages/observer/package.json`: package metadata, scripts, exports.
- Create `packages/observer/tsconfig.json`: shared TypeScript config.
- Create `packages/observer/vitest.config.ts`: SDK test config.
- Create `packages/observer/index.ts`: public exports.
- Create `packages/observer/types.ts`: SDK types and payload contracts.
- Create `packages/observer/context.ts`: trace/session/span ID generation and header helpers.
- Create `packages/observer/client.ts`: `ObserverClient` with `startTrace`, `span`, `log`, `tool`, `injectHeaders`, `flush`.
- Create `packages/observer/client.test.ts`: behavior tests for headers, payloads, and HTTP submission.

## Scope

Milestone SDK behavior:

- Create a trace context with `trace_id`, `session_id`, and root span ID.
- Generate `X-Multica-*` headers for Gateway model calls.
- Submit traces to `/v1/traces` using `Authorization: Bearer mgw_...`.
- Record spans with AgentOps-compatible kinds.
- Record logs, events, agent observations, and tool observations.
- Fail closed for explicit `flush()` errors by throwing, while keeping local builder methods synchronous and side-effect-light.

Out of scope:

- Browser-specific storage.
- Background queue and retry.
- OpenAI/Anthropic SDK monkey-patching.
- OTLP protocol support.
- Python SDK.
- Publishing to npm.

## Verification

- `pnpm --filter @multica/observer test`
- `pnpm --filter @multica/observer typecheck`
- `pnpm typecheck`
- `DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./...` from `server/`

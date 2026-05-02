# Multica Gateway Design

Date: 2026-05-03

Status: approved architecture, awaiting written spec review

## Goal

Build the first milestone of the Multica Gateway: a hosted, enterprise-managed model gateway inside the existing Multica Go server. Users authenticate with `multica login`, retrieve a Multica-issued gateway key, configure agent tools with OpenAI-compatible and Anthropic-compatible base URLs, and have their model traffic observed according to workspace policy.

The product-facing name is **Observer Gateway** when explaining what it does. Commands, API routes, UI navigation, and code should use the shorter name **Gateway**.

## Background

Multica already has the right product shape for this feature: workspaces, members and roles, agents, skills, local/cloud runtimes, token usage reporting, a Go backend, a shared web/desktop settings page, and a Go `multica` CLI.

Dario provides the routing model to emulate:

- one endpoint used by many tools;
- OpenAI-compatible and Anthropic-compatible request surfaces;
- backend selection by model name;
- explicit `provider:model` routing when needed;
- OpenAI-compatible provider adapters for OpenAI, Groq, OpenRouter, and local servers;
- Claude OAuth/subscription routing as a separate backend with sensitive wire behavior.

AgentOps provides the observability model to emulate:

- sessions;
- spans;
- model calls;
- tool events;
- logs;
- metrics;
- costs;
- replay/debug views.

Milestone 1 should not vendor the AgentOps app into Multica. The AgentOps Python SDK is MIT, but the cloned AgentOps app includes license material that needs review before any hosted enterprise product reuse. The safer design is to implement native Multica telemetry tables, APIs, and UI using AgentOps-like concepts.

## Selected Approach

Use **Approach C: Hybrid Gateway Core**.

The core gateway is native Go inside the existing Multica server for milestone 1. It owns authentication, workspace policy, backend configuration, routing, streaming, request/response capture, cost accounting, and PostgreSQL telemetry writes.

Claude OAuth/subscription routing is isolated behind a `claude-oauth` backend adapter. The adapter can be implemented as a Go port only if we can preserve the required request shape safely. If that is not practical, the adapter may call a Dario-compatible sidecar while the rest of the gateway remains native Multica code.

This keeps the enterprise platform coherent while containing the highest-risk Dario behavior behind one adapter boundary.

## Milestone 1 Scope

Milestone 1 includes:

- hosted Gateway inside the existing Go server;
- `multica login` as the user authentication entry point;
- `multica gateway` command group;
- user gateway key creation/retrieval;
- admin-managed upstream backends;
- OpenAI-compatible endpoint support;
- Anthropic Messages endpoint support;
- streaming support for both OpenAI-compatible and Anthropic-compatible endpoints from day one;
- admin default backend plus optional `provider:model` routing prefix;
- workspace capture policy with default `redacted_content`;
- PostgreSQL telemetry storage;
- minimal telemetry read API for recorded gateway requests and traces;
- minimal Settings -> Gateway UI;
- Claude OAuth/subscription backend type present from day one as `claude-oauth`;
- internal telemetry interface that can later export to OTLP or ClickHouse.

Milestone 1 excludes:

- npm packaging under `@ahmed-khaire/observer`;
- standalone `observer` CLI;
- local proxy mode;
- per-user provider BYOK;
- per-user Claude OAuth accounts;
- ClickHouse storage;
- OTLP export;
- full AgentOps dashboard parity;
- rich trace replay UI;
- full Dario shim/MCP/sub-agent feature parity;
- broad automated strategy generation from observed behavior.

Those are later phases.

## Architecture

The milestone 1 gateway has five main units.

1. **Gateway HTTP surface**

   Adds provider-compatible routes to the existing Go server:

   - `GET /v1/models`
   - `POST /v1/chat/completions`
   - `POST /v1/messages`

   These routes are authenticated by a Multica gateway key, not by the normal web session token. They still resolve a Multica user and workspace.

2. **Gateway management API**

   Adds protected workspace-scoped APIs under `/api/gateway` for the CLI and UI:

   - user key status/create/revoke;
   - backend list/create/update/delete;
   - default backend selection;
   - capture policy read/update;
   - basic gateway status;
   - minimal request/session trace listing and detail reads.

   Backend writes and policy changes require workspace `owner` or `admin`. User key operations require workspace membership.

3. **Gateway service package**

   Adds a focused Go package, tentatively `server/internal/gateway`, that contains:

   - request authentication;
   - backend config loading;
   - model routing;
   - protocol translation;
   - capture policy application;
   - telemetry recording;
   - cost calculation hooks;
   - streaming forwarding.

   This package should avoid depending on handler-specific code. It may depend on `db.Queries`, a small DB executor interface, and explicit configuration structs. That boundary lets the same package move behind `server/cmd/gateway` later.

4. **Backend adapters**

   Each backend implements one adapter interface. The initial backend types are:

   - `openai-compatible`: OpenAI, Groq, OpenRouter, local OpenAI-compatible servers;
   - `anthropic`: Anthropic API-key backend;
   - `claude-oauth`: Claude subscription/OAuth backend.

   The router strips any `provider:` prefix from the model before forwarding, selects the backend, and preserves the original protocol when possible. When the selected backend speaks a different protocol than the client, the gateway translates between OpenAI Chat Completions and Anthropic Messages.

5. **Telemetry recorder**

   The recorder persists AgentOps-like records to PostgreSQL. The gateway should write telemetry through an internal interface rather than directly coupling all request code to SQL. The first implementation is PostgreSQL. Later implementations may dual-export to OTLP or ClickHouse without changing gateway routing code.

## Routing Model

The router supports two selection paths.

Default routing:

- Workspace admins configure one default backend.
- Unprefixed model names route to that default unless a deterministic model-family rule matches a more specific configured backend.
- The first milestone should keep model-family rules conservative. Do not guess aggressively.

Explicit routing:

- A model string may include `provider:model`.
- Examples:
  - `groq:llama-3.3-70b`
  - `openrouter:anthropic/claude-sonnet-4`
  - `local:qwen-coder`
  - `claude-oauth:sonnet`
- The prefix selects a configured backend by slug.
- The forwarded model is the suffix after the first colon.

If the prefix does not match an enabled backend, the gateway returns a provider-compatible 404-style model error and records a failed gateway request with no upstream call.

## Protocol Surfaces

OpenAI-compatible support:

- `POST /v1/chat/completions`
- `GET /v1/models`
- streaming via Server-Sent Events when `stream: true`;
- pass through OpenAI-compatible request fields where the selected backend is OpenAI-compatible;
- normalize upstream errors into OpenAI-compatible error bodies when the client used the OpenAI surface.

Anthropic-compatible support:

- `POST /v1/messages`
- streaming via Anthropic event stream when `stream: true`;
- pass through Anthropic request fields where the selected backend is Anthropic-compatible;
- normalize upstream errors into Anthropic-compatible error bodies when the client used the Anthropic surface.

Translation rules:

- OpenAI client to Anthropic backend: convert messages, tools, tool calls, system content, max tokens, temperature, stop sequences, and stream events.
- Anthropic client to OpenAI-compatible backend: convert system/messages, tools, tool results, max tokens, temperature, stop sequences, and stream events.
- The milestone 1 supported cross-protocol subset is text messages, single or multiple tool definitions, assistant tool calls, client tool results, and streaming deltas for those shapes.
- Unsupported multimodal or provider-specific request shapes must fail with a protocol-compatible error rather than being silently dropped.

## Streaming

Streaming is required from day one because agent tools depend on incremental events and tool-call deltas.

The gateway must:

- flush chunks as they arrive;
- avoid buffering entire streamed responses before returning them;
- capture metadata and usage even when content capture is disabled;
- record partial/failure state when a client disconnects or upstream stream fails;
- preserve the response event format expected by the client protocol;
- close response bodies and drain only where required by the selected backend adapter.

The telemetry recorder may finalize the request after the stream ends. If final upstream usage data is unavailable, it should record token counts as zero or estimated with a clear `usage_source` field.

## Authentication And Keys

`multica login` remains the main login command. It stores the normal Multica token in the existing CLI config.

Gateway commands use that token to call `/api/gateway`.

The user flow is:

```bash
multica login
multica gateway key
```

The CLI prints:

```bash
OPENAI_BASE_URL=https://<multica-host>/v1
OPENAI_API_KEY=<gateway-key>
ANTHROPIC_BASE_URL=https://<multica-host>
ANTHROPIC_API_KEY=<gateway-key>
```

Gateway keys are separate from existing personal access tokens because they have different semantics:

- intended for model-provider-compatible clients;
- scoped to a workspace;
- never allowed to call normal Multica app APIs;
- revocable without revoking the user's app login;
- hash stored server-side;
- encrypted raw value stored server-side so `multica gateway key` can retrieve the active key as requested.

For milestone 1, `multica gateway key` creates or retrieves the active user gateway key for the current workspace. A later `--rotate` flag can revoke the active key and create a replacement.

## CLI Design

Add a `gateway` command group to the existing Go `multica` CLI.

User commands:

```bash
multica gateway status
multica gateway key
multica gateway keys
multica gateway revoke <key-id>
```

Admin commands:

```bash
multica gateway add openai --key=sk-proj-...
multica gateway add groq --key=gsk_... --base-url=https://api.groq.com/openai/v1
multica gateway add openrouter --key=sk-or-... --base-url=https://openrouter.ai/api/v1
multica gateway add local --key=anything --base-url=http://127.0.0.1:11434/v1
multica gateway add anthropic --key=sk-ant-...
multica gateway add claude-oauth
multica gateway backends
multica gateway default <backend-slug>
multica gateway policy redacted_content
```

The CLI should use existing config resolution for `--server-url`, `--workspace-id`, and `--profile`.

## UI Design

Add a minimal Settings -> Gateway tab in `packages/views/settings`.

The tab should include:

- backend list;
- default backend selector;
- capture policy selector;
- generated key instructions;
- current user key status;
- examples for OpenAI and Anthropic environment variables.

Admin-only controls:

- add backend;
- update backend;
- disable/delete backend;
- set default backend;
- change capture policy.

Member controls:

- view instructions;
- create/revoke their own gateway key.

The UI must stay in shared packages where possible:

- shared API client and types in `packages/core`;
- Gateway tab component in `packages/views/settings`;
- app routes remain the existing web/desktop Settings route.

## Capture Policy

Capture policy is workspace-scoped. The milestone 1 allowed values are:

- `metadata_only`
- `redacted_content`
- `full_content`

Default: `redacted_content`.

`metadata_only` records request metadata, backend, model, status, latency, usage, cost, and error details, but not prompt or completion content.

`redacted_content` records content after deterministic redaction. At minimum, redaction should cover common secrets, bearer tokens, API keys, and obvious credential fields. Redaction is not a guarantee of perfect data-loss prevention; the UI should describe it as policy-based capture, not absolute compliance.

Implementation should reuse and extend the existing `server/pkg/redact` package instead of introducing a separate redaction path.

`full_content` records prompts, completions, tool payloads, and logs. It must be admin-selected.

All policies record enough metadata to support usage analytics and operational debugging.

## Data Model

Add PostgreSQL tables for Gateway configuration and telemetry. Exact names can be refined during implementation, but the model should cover these entities:

Configuration:

- `gateway_backend`
  - workspace ID;
  - slug;
  - display name;
  - type;
  - base URL;
  - encrypted credential value;
  - enabled flag;
  - default flag or separate workspace setting;
  - created/updated timestamps.
- `gateway_user_key`
  - workspace ID;
  - user ID;
  - key hash;
  - encrypted key value;
  - display prefix;
  - revoked timestamp;
  - last used timestamp;
  - created timestamp.
- `gateway_workspace_settings`
  - workspace ID;
  - capture policy;
  - default backend ID;
  - created/updated timestamps.

Telemetry:

- `gateway_session`
  - workspace ID;
  - user ID;
  - optional agent ID/task ID if present in request metadata;
  - client protocol;
  - client tool hint;
  - started/ended timestamps.
- `gateway_request`
  - session ID;
  - backend ID;
  - route;
  - method;
  - model requested;
  - model forwarded;
  - streaming flag;
  - status;
  - latency;
  - error type/message;
  - capture policy used.
- `gateway_model_call`
  - request ID;
  - provider/backend;
  - model;
  - input/output/cache token counts;
  - usage source;
  - cost fields.
- `gateway_span`
  - session/request parent;
  - span type;
  - name;
  - start/end timestamps;
  - attributes JSON.
- `gateway_event`
  - session/request/span;
  - event type;
  - redacted payload JSON;
  - timestamp.
- `gateway_model_pricing`
  - provider/backend type;
  - model pattern;
  - input/output/cache token prices;
  - currency;
  - effective timestamp.

Existing `runtime_usage` and `task_usage` should not be overloaded for gateway telemetry. They track Multica agent-runtime usage. Gateway traffic is a broader enterprise observability surface and needs separate tables.

## Cost Tracking

Milestone 1 should store token counts and cost columns, but can start with a conservative pricing registry.

Rules:

- capture upstream-reported usage where available;
- estimate only when explicit and mark `usage_source = estimated`;
- keep model pricing data in `gateway_model_pricing`, separate from individual request rows;
- support unknown cost as null, not zero, when pricing is unknown;
- allow later aggregation into workspace usage views.

## Claude OAuth Backend

`claude-oauth` is a first-class backend type from day one, but isolated from the native gateway core.

Milestone 1 supports an admin-managed shared Claude OAuth pool. Per-user Claude accounts come later.

This backend must not be treated as generic Anthropic API-key routing. It has different credential material, session behavior, compliance implications, upstream limits, and wire-shape sensitivity.

The adapter boundary should support two implementations:

- native Go implementation if request shape, headers, streaming behavior, and OAuth refresh can be safely reproduced;
- sidecar implementation that delegates to a Dario-compatible local or internal service.

The milestone 1 implementation should use the sidecar-backed implementation first. Native Go can replace it later if fidelity and compliance review show it is safe. The gateway core only depends on the adapter interface and should not know which implementation is active.

## Security And Compliance

Security requirements:

- store gateway keys hashed, never plaintext;
- add a small server-side secrets package for Gateway credentials using envelope-friendly encryption;
- require a production secret key such as `MULTICA_GATEWAY_SECRET_KEY` when Gateway credential storage is enabled;
- encrypt upstream provider credentials and retrievable gateway key values at rest;
- mask credential values in API responses, logs, and CLI output;
- restrict backend management to workspace owners/admins;
- scope gateway keys to one workspace;
- apply capture policy before telemetry persistence;
- do not log raw prompt/completion content through general server logs;
- record audit-friendly metadata for backend and policy changes.

Compliance caveat:

Claude OAuth/subscription routing is sensitive because it depends on provider OAuth accounts and Dario-like wire behavior. The implementation must make this backend explicit in UI/API and should not silently route enterprise traffic through it without admin configuration.

## Error Handling

The gateway should return errors in the client protocol format.

Expected error classes:

- invalid or revoked gateway key;
- missing workspace;
- user no longer a workspace member;
- no default backend configured;
- unknown provider prefix;
- disabled backend;
- unsupported route or request shape;
- upstream authentication failure;
- upstream rate limit;
- upstream timeout;
- streaming interruption;
- capture/telemetry persistence failure.

Telemetry persistence failures should not normally fail the model request if the upstream request can still complete. They should be logged as internal errors and exposed in Gateway health/status. Authentication, routing, policy, and backend credential errors should fail before any upstream call.

## Integration Points

Backend:

- add gateway routes in `server/cmd/server/router.go`;
- add handler methods under `server/internal/handler`;
- add core gateway package under `server/internal/gateway`;
- add migrations under `server/migrations`;
- add sqlc queries under `server/pkg/db/queries`;
- regenerate db code with `make sqlc`.

CLI:

- add a `gateway` command group under `server/cmd/multica`;
- use existing CLI config in `server/internal/cli/config.go`.

Frontend:

- add Gateway types and API client methods under `packages/core`;
- add Settings -> Gateway tab under `packages/views/settings`;
- use existing Settings route in web and desktop.

Telemetry read API:

- `GET /api/gateway/requests` lists recent gateway requests for the current workspace with filters for user, backend, model, status, and time range.
- `GET /api/gateway/requests/{id}` returns one request with its spans, model call, and events after applying the same capture policy that governed storage.
- These read APIs are enough for verification and future UI work, but milestone 1 does not build a full trace replay dashboard.

## Testing Strategy

Backend tests:

- gateway key authentication and revocation;
- workspace membership and admin authorization;
- backend CRUD and default selection;
- capture policy validation;
- provider-prefix routing;
- OpenAI non-streaming passthrough;
- OpenAI streaming passthrough;
- Anthropic non-streaming passthrough;
- Anthropic streaming passthrough;
- OpenAI-to-Anthropic translation;
- Anthropic-to-OpenAI translation;
- telemetry writes for success, upstream failure, and client disconnect where practical.

CLI tests:

- `multica gateway key` prints base URLs and key material in the expected format;
- admin commands call the right API payloads;
- commands respect `--server-url`, `--workspace-id`, and `--profile`.

Frontend tests:

- Settings -> Gateway renders for workspace members;
- admin-only controls are hidden or disabled for non-admins;
- backend list and policy selector use workspace-scoped query keys;
- mutations invalidate Gateway queries.

Manual verification:

- configure `OPENAI_BASE_URL` and `OPENAI_API_KEY` against local Multica;
- run an OpenAI-compatible SDK request with and without streaming;
- configure `ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY`;
- run an Anthropic Messages request with and without streaming;
- verify telemetry appears in PostgreSQL;
- verify policy changes alter captured payloads.

## Rollout

1. Add schema and backend package boundaries.
2. Implement management APIs and CLI commands.
3. Implement OpenAI-compatible routing and streaming.
4. Implement Anthropic-compatible routing and streaming.
5. Implement telemetry recorder and capture policy.
6. Add Settings -> Gateway.
7. Add `claude-oauth` adapter boundary and first implementation choice.
8. Run end-to-end checks with real SDK clients.

The implementation plan should keep OpenAI-compatible routing and Anthropic-compatible routing separable enough to test independently.

## Risks

Claude OAuth fidelity:

- Dario relies on sensitive request and runtime behavior. Reimplementing this in Go may not be safe.
- Mitigation: isolate `claude-oauth` and allow a Dario-compatible sidecar implementation.

Streaming translation:

- Tool-call streaming formats differ between OpenAI and Anthropic.
- Mitigation: define a supported subset first and cover it with adapter tests.

Content capture:

- `redacted_content` can reduce risk but cannot guarantee perfect secret removal.
- Mitigation: make policy explicit, default to redacted content, and avoid overclaiming compliance.

Telemetry volume:

- PostgreSQL is fine for milestone 1, but high-volume enterprise traffic may outgrow it.
- Mitigation: write through a telemetry interface and keep the future OTLP/ClickHouse path open.

Credential handling:

- Admin-managed provider keys and Claude OAuth tokens are sensitive.
- Mitigation: encrypted storage or secret references, masked outputs, strict RBAC, and audit metadata.

## Implementation Planning Defaults

These decisions are locked for the first implementation plan unless the written spec review changes them:

- `multica gateway key` retrieves an active key by storing an encrypted key value plus a hash.
- Gateway provider credentials use a new Gateway secrets package and require a production encryption secret.
- The first `claude-oauth` implementation is sidecar-backed through the adapter interface.
- Cross-protocol tool support covers text, tool definitions, tool calls, tool results, and streaming deltas for those shapes.
- Pricing lives in `gateway_model_pricing`; unknown prices produce null cost values.
- Milestone 1 includes minimal telemetry read APIs, but not a full trace replay UI.

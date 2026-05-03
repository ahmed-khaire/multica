# Dario And AgentOps Feature Backlog For Multica Gateway

Date: 2026-05-03

Status: reference backlog for future design and planning

## Purpose

This document preserves the useful features found during the Dario and AgentOps repo review so they can be referenced later when designing follow-on Gateway, Observer, SDK, and governance plans.

This is not an implementation plan and it is not a commitment that every item belongs in Milestone 1. The current Gateway spec already folds the highest-priority concepts into Milestone 1: hosted provider-compatible gateway routing, native trace/span telemetry, lightweight SDK/OTLP app and agent observability, inventory, policy decisions, governance intelligence, and dashboard surfaces.

Use this backlog when deciding what to add after the current Gateway plan set, especially when drafting plans for telemetry ingest, SDKs, diagnostics, backend pooling, framework instrumentation, local proxy modes, and enterprise governance extensions.

## Priority Labels

- **Already in Gateway Milestone 1**: captured in the current Gateway spec or phase-plan boundary.
- **Near-term extension**: likely valuable soon after the current plan set, or as a focused addition inside a future Gateway phase.
- **Later phase**: useful, but should wait until the core hosted control plane works.
- **Caution**: requires legal, security, provider-policy, product-positioning, or operational review before adoption.

## Dario-Derived Candidates

### Universal Provider Router

**Status:** Already in Gateway Milestone 1.

Useful ideas:

- one endpoint for many tools and providers;
- OpenAI-compatible and Anthropic-compatible request surfaces;
- model-name routing plus explicit `provider:model` prefixes;
- backend presets for OpenAI, Groq, OpenRouter, local OpenAI-compatible servers, Anthropic, and Claude OAuth;
- protocol translation between OpenAI Chat Completions and Anthropic Messages;
- streaming passthrough without buffering whole responses.

Why it matters for Multica:

This is the foundation of Observer Gateway. It gives enterprise users and developers one managed model boundary, instead of scattered provider keys and unmanaged base URLs.

Source anchors:

- `../dario/README.md`
- `../dario/docs/commands.md`

### Doctor, Status, Config, And Usage Diagnostics

**Status:** Near-term extension.

Useful ideas:

- `doctor` command that aggregates environment, runtime, auth, backend, compatibility, OAuth, pool, TLS, template, and sub-agent health into one paste-ready report;
- structured JSON mode for automation and support tooling;
- `config` command that explains effective configuration with credentials redacted;
- `usage` command and `/analytics` endpoint for recent request volume, token burn, latency, error rate, estimated cost, backend/account breakdowns, and exhaustion predictions;
- auth-check mode that classifies what a client actually sends without leaking secrets.

Recommended Multica shape:

- `multica gateway doctor` for CLI support reports;
- `GET /api/gateway/health-report` for UI/support automation;
- Gateway Settings health panel showing missing secret key, disabled default backend, invalid capture policy, failed telemetry recorder, stale Claude OAuth sidecar, and ingest-key failures;
- JSON report export that users can attach to support tickets;
- no raw prompts, completions, provider keys, OAuth tokens, gateway keys, or ingest keys in diagnostic output.

Why it matters:

Enterprise rollout fails when setup is opaque. A strong doctor/report flow reduces support cost and makes Gateway deployable by customers without engineering help.

Source anchors:

- `../dario/docs/commands.md`
- `../dario/src/analytics.ts`

### Backend Capacity Pools, Headroom Routing, Stickiness, And Failover

**Status:** Near-term extension for backend routing. Caution for Claude subscription/OAuth behavior.

Useful ideas:

- multiple accounts or credentials per backend;
- select the account/key with the most available headroom;
- parse provider rate-limit headers and feed them back into the router;
- route around rejected/exhausted accounts;
- queue briefly when all accounts are exhausted;
- sticky session binding so long conversations do not bounce between accounts and lose provider-side cache benefits;
- in-flight 429 failover to another healthy credential when safe.

Recommended Multica shape:

- generic `gateway_backend_pool` or `gateway_backend_credential` model for API-key providers first;
- provider-specific rate-limit parsers behind adapter interfaces;
- workspace policy for whether failover is allowed for a backend;
- session/cache-affinity key derived from trace/session ID, not from prompt content when possible;
- UI showing backend pool health, exhausted credentials, fallback counts, and policy exceptions;
- strict audit logs for credential pool changes and failover events.

Why it matters:

Enterprise agents run long workflows. Rate-limit-aware routing and session affinity can reduce failures, preserve cache economics, and provide clearer capacity planning.

Source anchors:

- `../dario/docs/multi-account-pool.md`
- `../dario/src/pool.ts`
- `../dario/src/analytics.ts`

### Billing Bucket And Subscription/Overage Analytics

**Status:** Near-term extension.

Useful ideas:

- classify responses into billing buckets;
- show subscription vs overage/API routing percentages;
- track per-account, per-model, per-provider token usage and costs;
- predict exhaustion windows;
- expose utilization trends in local analytics.

Recommended Multica shape:

- provider billing classification as a normalized telemetry dimension;
- dashboard cards for subscription-backed, API-backed, fallback, unknown, and error traffic;
- budget policies that can warn or block when traffic falls into expensive or unapproved buckets;
- evidence records when routing changes because of backend exhaustion or policy fallback.

Why it matters:

For enterprise governance, "which backend did we route to?" is not enough. Finance and governance need to know which cost bucket and contractual surface were used.

Source anchors:

- `../dario/src/analytics.ts`
- `../dario/docs/multi-account-pool.md`

### Agent Tool Compatibility Map

**Status:** Later phase, with a small policy-aware subset possible sooner.

Useful ideas:

- maintain a compatibility map for common agent tool schemas;
- translate tool calls between client-native schemas and canonical gateway/provider schemas;
- preserve client tool schemas when translation would corrupt semantics;
- hybrid tool mode for request-context fields such as session ID, request ID, user ID, timestamp, and channel ID;
- structural fallback that detects unknown non-compatible clients.

Recommended Multica shape:

- "tool schema registry" linked to Governance inventory;
- canonical tool taxonomy for `shell`, `file_read`, `file_write`, `web_fetch`, `web_search`, `browser`, `mcp_tool`, `approval`, and `external_action`;
- policy decisions based on canonical tool risk even when client tools use different names;
- UI that shows original tool schema, normalized tool class, policy decision, and translation mode;
- avoid silent translation for destructive tools unless the mapping is explicitly reviewed.

Why it matters:

Gateway governance depends on understanding tool actions. A compatibility layer helps Multica detect that `execute_command`, `run_terminal_cmd`, `execute_bash`, and `Bash` are the same risk class.

Source anchors:

- `../dario/docs/agent-compat.md`

### Wire-Fidelity Guardrails For Claude OAuth

**Status:** Caution. Useful only inside the isolated `claude-oauth` adapter boundary.

Useful ideas:

- request body key order capture/replay;
- runtime/TLS classification;
- inter-request pacing and jitter;
- upstream SSE drain behavior when downstream disconnects;
- session ID lifecycle controls;
- template drift detection and refresh;
- doctor rows for wire-fidelity state.

Recommended Multica shape:

- keep all Claude OAuth behavior behind a sidecar-backed adapter first;
- represent wire-fidelity health as adapter health, not as general Gateway behavior;
- log only metadata and health state, not raw credential or prompt content;
- require admin opt-in and clear labeling for Claude OAuth backends.

Why it matters:

Claude OAuth routing is the riskiest Dario-derived area. The feature is useful, but should stay isolated and reviewable.

Source anchors:

- `../dario/docs/wire-fidelity.md`

### Read-Only MCP Server For Gateway Introspection

**Status:** Later phase.

Useful ideas:

- expose status, account/backend list, doctor report, and fingerprint/runtime info through a read-only MCP server;
- keep mutating operations out of MCP;
- redact credentials completely;
- test that forbidden tools stay forbidden.

Recommended Multica shape:

- Multica MCP server that can answer "what is Gateway doing?" from IDEs and agent tools;
- read-only tools for gateway status, policy decisions, application inventory, trace lookup, backend health, and evidence status;
- no mutation tools in the first MCP version;
- separate permissions from normal web UI and CLI.

Why it matters:

Enterprise developers will work inside IDEs and agent environments. Read-only MCP lets them inspect observability and governance state without switching context.

Source anchors:

- `../dario/docs/mcp-server.md`

### Diagnostic Sub-Agent

**Status:** Later phase.

Useful ideas:

- install a Claude Code sub-agent that can run diagnostics inside an active coding session;
- restrict tools to safe read/report actions;
- version marker to detect stale installed agent prompt;
- status command that tells the user whether the local agent hook is current.

Recommended Multica shape:

- optional Multica diagnostic agent or skill that can inspect Gateway health, user key status, local daemon state, and trace links;
- explicit read-only mode by default;
- destructive operations require separate user confirmation outside the sub-agent.

Why it matters:

When developers debug failing agent traffic, the assistant running inside the IDE can inspect Gateway state and point to the failing trace/policy decision.

Source anchors:

- `../dario/docs/sub-agent.md`

### In-Process Shim Mode

**Status:** Later phase. Caution.

Useful ideas:

- patch a child process in-process so there is no localhost proxy hop;
- relay telemetry back to a parent process;
- fail safe if request rewriting cannot be applied;
- use child process priority controls for local machine stability.

Recommended Multica shape:

- not part of hosted Gateway Milestone 1;
- consider only for a future local developer proxy or daemon mode;
- use for telemetry/header injection where supported, not for sensitive provider impersonation;
- document language/runtime limitations clearly.

Why it matters:

Some developer tools do not support custom base URLs or cannot reach a hosted gateway cleanly. A local shim could help, but it is operationally and security sensitive.

Source anchors:

- `../dario/docs/shim.md`

### System Prompt Modes And Enterprise Prompt Policy

**Status:** Later phase. Caution.

Useful ideas:

- operator-selectable system prompt modes;
- doctor visibility into active prompt mode;
- custom prompt file mode;
- empirical A/B evaluation of prompt changes.

Recommended Multica shape:

- enterprise-approved system prompt templates and policy-controlled prompt overlays;
- prompt changes logged as policy/config changes;
- avoid claims about provider billing classifiers;
- treat prompt modes as model behavior controls, not compliance bypasses.

Why it matters:

Enterprises will want consistent agent behavior and organization-specific system prompts. Multica can turn this into governed prompt templates, versioning, and evidence.

Source anchors:

- `../dario/docs/system-prompt.md`

### Egress Routing And Network Policy

**Status:** Later phase.

Useful ideas:

- per-process upstream proxy;
- system VPN and Tailscale exit-node guidance;
- clear startup/doctor visibility for routed egress;
- constraints around proxy schemes and runtime support.

Recommended Multica shape:

- enterprise egress policy per backend/provider;
- allowed regions and network paths on third-party risk records;
- Gateway-side outbound proxy support for self-hosted deployments;
- evidence showing which egress policy was active for a provider/backend.

Why it matters:

Regulated customers care where AI traffic leaves their network. Egress controls should become part of governance and third-party risk evidence.

Source anchors:

- `../dario/docs/vpn-routing.md`

## AgentOps-Derived Candidates

### Trace, Session, Span, And Dashboard Model

**Status:** Already in Gateway Milestone 1.

Useful ideas:

- root session/trace with nested spans;
- span kinds for session, agent, workflow, task, operation, tool, LLM, chain, text, guardrail, HTTP, and unknown;
- parent/child span hierarchy;
- status, duration, attributes, resource attributes, events, links, logs, and metrics;
- session drawer/list, session drilldown, waterfall/timeline, tree, details panel, chat-history view, and aggregate dashboard.

Why it matters for Multica:

This is the core Observer model. It lets Multica show not only "a model call happened" but "this app workflow called this agent, used this tool, hit this guardrail, then called this model."

Source anchors:

- `../agentops/docs/v2/concepts/traces.mdx`
- `../agentops/docs/v2/concepts/spans.mdx`
- `../agentops/docs/v2/usage/dashboard-info.mdx`
- `../agentops/app/dashboard/types/ITrace.ts`
- `../agentops/app/dashboard/types/ISpan.ts`

### Python And TypeScript SDK Shape

**Status:** Already in Gateway Milestone 1 for a small subset. Deeper parity is a later phase.

Useful ideas:

- Python `init`, `configure`, `start_trace`, `end_trace`, metadata update, tags, queue size, flush interval, fail-safe behavior, exporter endpoint, and optional auto-session;
- TypeScript SDK built on OpenTelemetry standards;
- GenAI semantic conventions;
- framework-agnostic instrumentation that can combine several frameworks in one app;
- plugin architecture for instrumentors;
- debug logging.

Recommended Multica shape:

- Milestone 1 SDK stays small: trace context, headers, manual spans, logs, artifacts, OTLP-compatible submission, and `evaluatePolicy`;
- later SDK phases add auto-instrumentors;
- preserve one trace/span schema across manual and auto instrumentation;
- support local fail-open behavior but make export failures visible in logs and SDK diagnostics.

Why it matters:

Gateway-only capture cannot see app runtime decisions. SDKs add the missing context without making developers adopt a full agent framework.

Source anchors:

- `../agentops/docs/v2/usage/sdk-reference.mdx`
- `../agentops/docs/v2/usage/typescript-sdk.mdx`

### Decorators And Manual Instrumentation

**Status:** Already in Gateway Milestone 1 for explicit/manual helpers. Full decorator parity is later.

Useful ideas:

- decorators for session, agent, workflow, operation, task, tool, and guardrail;
- automatic span hierarchy;
- input/output recording;
- exception handling;
- tool cost tracking;
- support for sync, async, generator, and async-generator tools.

Recommended Multica shape:

- Python decorators in the lightweight SDK once base span submission is stable;
- TypeScript wrappers or helper functions rather than decorator-only design because decorators are less universal in Node apps;
- capture controls on every decorator/helper;
- guardrail and tool helpers should optionally call `evaluatePolicy` before execution.

Why it matters:

Decorators make instrumentation cheap for developer teams and create a clear semantic model for governance.

Source anchors:

- `../agentops/docs/v2/concepts/decorators.mdx`

### Framework And Provider Auto-Instrumentation

**Status:** Later phase.

Useful ideas:

- provider instrumentation for OpenAI, Anthropic, Google GenAI, IBM watsonx, and memory/provider utilities;
- agent framework instrumentation for OpenAI Agents, CrewAI, AG2, Agno, Google ADK, Haystack, LangGraph, smolagents, and others;
- common wrappers for streaming, token counting, metrics, span management, and version detection;
- ability to instrument multiple frameworks in one app.

Recommended Multica shape:

- ship manual SDK and OTLP first;
- add framework adapters based on enterprise demand;
- prioritize OpenAI Agents, LangGraph, CrewAI, LangChain/LangSmith-adjacent workflows, LlamaIndex, MCP clients, and provider SDKs used by target customers;
- every adapter emits the same Gateway span kinds and governance attributes;
- adapters must never silently capture prompt/tool content beyond workspace/app capture policy.

Why it matters:

Auto-instrumentation drives adoption, but it can explode scope. Keeping the first SDK small protects Milestone 1 while preserving the path to a richer developer experience.

Source anchors:

- `../agentops/agentops/instrumentation/`
- `../agentops/agentops/instrumentation/agentic/`
- `../agentops/agentops/instrumentation/providers/`

### OpenTelemetry And GenAI Semantic Conventions

**Status:** Already in Gateway Milestone 1 conceptually. Continue refining in later phases.

Useful ideas:

- OpenTelemetry-compatible trace model;
- GenAI attributes for provider/system, request model, response model, token usage, prompt/completion content, streaming timing, finish reason, stop reason, cost, HTTP metadata, and operation metadata;
- resource attributes for service, environment, host, runtime, SDK version, and dependency info;
- compatibility with OTLP collectors.

Recommended Multica shape:

- use OTel and GenAI naming where practical;
- keep Multica-specific governance attributes under a clear namespace;
- map Gateway model calls and SDK spans into one canonical schema;
- keep OTLP ingest and internal JSON ingest behavior aligned.

Why it matters:

Enterprises already using OpenTelemetry should not have to adopt a proprietary-only telemetry shape.

Source anchors:

- `../agentops/agentops/semconv/span_attributes.py`
- `../agentops/agentops/semconv/span_kinds.py`
- `../agentops/agentops/semconv/resource.py`

### Public Read API For Trace Data

**Status:** Later phase.

Useful ideas:

- API-key to bearer-token exchange;
- read-only trace, span, project, and metrics endpoints;
- trace detail includes spans;
- trace metrics include status counts, token counts, reasoning/cache tokens, and cost totals;
- span detail includes attributes, resource attributes, and span attributes.

Recommended Multica shape:

- enterprise read APIs for exporting trace, span, policy decision, evidence, and inventory data;
- short-lived scoped tokens rather than long-lived broad tokens;
- endpoint-level RBAC for observability vs governance exports;
- content visibility tied to capture policy and user permissions.

Why it matters:

Customers will want to pull evidence and observability into SIEM, BI, GRC, or internal audit tooling.

Source anchors:

- `../agentops/docs/v2/usage/public-api.mdx`
- `../agentops/app/api/agentops/public/v1/`

### Metrics And Cost Rollups

**Status:** Already in Gateway Milestone 1 at a base level. Expand later.

Useful ideas:

- trace-level metrics for span count, success/failure/indeterminate counts, prompt tokens, completion tokens, cache read tokens, reasoning tokens, total tokens, prompt cost, completion cost, average trace cost, and total cost;
- dashboard-level aggregate stability, usage, cost, and error trends;
- span processing for charts.

Recommended Multica shape:

- PostgreSQL rollups first;
- explicit rollup table keyed by workspace, app, environment, backend, model, agent, tool, and time bucket;
- later export to warehouse/ClickHouse for high-volume customers;
- governance risk scoring should use rollups as evidence, not recompute everything from raw traces.

Why it matters:

Enterprise buyers need cost and reliability posture, not just raw traces.

Source anchors:

- `../agentops/docs/v2/usage/public-api.mdx`
- `../agentops/app/api/agentops/api/routes/v4/metrics/`
- `../agentops/app/dashboard/components/charts/`

### Self-Hosted App Packaging

**Status:** Later phase.

Useful ideas:

- self-hosted dashboard and API backend;
- documented local app/backend setup;
- open-source deployment path for customers that cannot send telemetry to a hosted SaaS.

Recommended Multica shape:

- preserve self-hostability of Multica Gateway;
- support customer-owned Postgres and future warehouse export;
- document production secrets, egress policy, retention, backup, and migration expectations;
- do not inherit AgentOps app structure directly without license and architecture review.

Why it matters:

Governance and trace data can be sensitive. Some enterprises will only adopt if deployment can stay in their environment.

Source anchors:

- `../agentops/README.md`
- `../agentops/app/README.md`

## Cross-Repo Product Themes

### Setup And Support Should Be First-Class

Dario's `doctor` and AgentOps' quick SDK onboarding both show that observability products need low-friction setup and fast diagnostics. Multica should treat setup health, key status, ingest failures, backend health, and policy errors as product surfaces, not hidden logs.

### Routing, Telemetry, And Governance Must Share Identity

Dario solves routing. AgentOps solves trace semantics. Multica's differentiator should be joining both to enterprise inventory: app, service, owner, environment, AI system, provider, model, data domain, use case, agent, workflow, tool, guardrail, policy, evidence, and risk.

### Tool Actions Are Governance Events

Dario's tool compatibility work and AgentOps' tool/guardrail spans point to the same requirement: normalize tools/actions so policy can reason about them. A shell command, MCP action, browser action, external API call, deployment action, and file write should become governed event classes.

### Lightweight First, Deep Adapters Later

AgentOps' deeper auto-instrumentation is valuable, but Dario's adoption comes from simple base URL setup. Multica should keep the first release small: Gateway base URL plus small SDK/OTLP. Add framework adapters after the core trace/policy/inventory model is proven.

### Read-Only Introspection Is Safer Than In-Tool Mutation

Both Dario's MCP server and sub-agent are careful about read-only boundaries. Multica should follow that pattern for IDE/agent introspection. Let agents inspect Gateway and Governance state first; require explicit user/admin action for mutations.

## Candidate Future Plan Slices

### Slice A: Gateway Doctor And Enterprise Health Reports

Goal:

Add `multica gateway doctor`, a structured health-report API, and UI health panels for Gateway setup, backend routing, SDK/OTLP ingest, policy engine health, and Claude OAuth adapter health.

Likely scope:

- CLI report with redaction;
- JSON output;
- server-side health check aggregation;
- UI health summary;
- tests ensuring secrets never appear.

### Slice B: Backend Credential Pools And Rate-Limit-Aware Routing

Goal:

Support multiple credentials/accounts per backend, route by headroom, preserve session/cache affinity, and emit failover evidence.

Likely scope:

- schema for backend credentials and pool state;
- provider rate-limit parsers;
- routing policy;
- sticky trace/session affinity;
- analytics and audit events;
- API-key provider support before Claude OAuth pooling.

### Slice C: Tool Schema Registry And Governance Normalization

Goal:

Normalize agent tool schemas into governed risk classes so policy can evaluate tool actions across different clients and frameworks.

Likely scope:

- canonical tool taxonomy;
- schema registry;
- mapped vs preserve vs hybrid translation modes;
- policy decisions per tool class;
- UI showing original and normalized tool identity.

### Slice D: SDK Decorators And Framework Auto-Instrumentation

Goal:

Move beyond manual spans by adding Python decorators, TypeScript wrappers, and selected provider/framework auto-instrumentation.

Likely scope:

- Python decorators for session, agent, workflow, operation, task, tool, guardrail;
- TypeScript wrappers/helpers;
- OpenAI and Anthropic provider instrumentation;
- one or two target agent-framework adapters based on customer demand;
- tests proving all adapters emit the same span schema.

### Slice E: Read-Only Gateway/Governance MCP

Goal:

Expose Gateway, Observer, and Governance state to MCP clients without allowing mutation.

Likely scope:

- read-only MCP tools for status, traces, policy decisions, app inventory, backend health, and evidence;
- scoped auth;
- redaction guarantees;
- forbidden mutation tests.

### Slice F: Public Export APIs

Goal:

Give enterprise customers stable APIs for trace, span, policy, evidence, inventory, and metrics export.

Likely scope:

- scoped API tokens;
- read-only export endpoints;
- pagination and filtering;
- capture-policy-aware content visibility;
- audit logs for exports.

### Slice G: Local Proxy/Shim Developer Mode

Goal:

Provide local developer routing and telemetry capture for tools that cannot directly use hosted Gateway.

Likely scope:

- local proxy mode;
- optional in-process header/telemetry shim where feasible;
- local buffering and retry;
- explicit warning that hosted Gateway remains the enterprise enforcement point.

### Slice H: Enterprise Egress And Network Policy

Goal:

Make provider/backend egress routing part of governance and evidence.

Likely scope:

- outbound proxy configuration for self-hosted Gateway;
- allowed region/network labels;
- third-party risk integration;
- evidence records linking traffic to active egress policy.

## Caution List

Do not adopt these without a focused review:

- copying AgentOps app/dashboard code directly into Multica without license and architecture review;
- making broad compliance certification claims from evidence mappings;
- making Claude OAuth routing invisible or automatic;
- implementing provider impersonation or wire-fidelity logic outside the isolated `claude-oauth` adapter;
- exposing mutating Gateway/Governance operations through MCP or diagnostic agents in the first version;
- capturing full prompt, completion, tool input/output, logs, or artifacts by default;
- treating user-supplied app IDs, agent IDs, or correlation headers as authorization.

## Source Files Reviewed

Dario:

- `../dario/README.md`
- `../dario/docs/commands.md`
- `../dario/docs/multi-account-pool.md`
- `../dario/docs/agent-compat.md`
- `../dario/docs/wire-fidelity.md`
- `../dario/docs/shim.md`
- `../dario/docs/mcp-server.md`
- `../dario/docs/sub-agent.md`
- `../dario/docs/vpn-routing.md`
- `../dario/docs/system-prompt.md`
- `../dario/src/analytics.ts`
- `../dario/src/pool.ts`

AgentOps:

- `../agentops/README.md`
- `../agentops/docs/v2/usage/sdk-reference.mdx`
- `../agentops/docs/v2/usage/typescript-sdk.mdx`
- `../agentops/docs/v2/usage/dashboard-info.mdx`
- `../agentops/docs/v2/concepts/traces.mdx`
- `../agentops/docs/v2/concepts/spans.mdx`
- `../agentops/docs/v2/concepts/decorators.mdx`
- `../agentops/docs/v2/usage/public-api.mdx`
- `../agentops/agentops/semconv/span_kinds.py`
- `../agentops/agentops/semconv/span_attributes.py`
- `../agentops/agentops/instrumentation/`
- `../agentops/app/api/agentops/api/routes/v4/`
- `../agentops/app/api/agentops/public/v1/`
- `../agentops/app/dashboard/types/`

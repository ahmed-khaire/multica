# Multica Gateway Design

Date: 2026-05-03

Status: approved architecture, awaiting written spec review

## Goal

Build the first milestone of the Multica Gateway, Observer dashboard, and AI governance platform: a hosted, enterprise-managed model gateway inside the existing Multica Go server, plus native AgentOps-inspired tracking, visualization, governance, compliance evidence, third-party AI risk management, and lightweight SDK/OTLP ingestion for enterprise apps and agents. Users authenticate with `multica login`, retrieve a Multica-issued gateway key, configure agent tools with OpenAI-compatible and Anthropic-compatible base URLs, and have their model traffic, application workflows, sessions, LLM calls, agents, spans, tools, guardrails, logs, artifacts, metrics, costs, policy decisions, and risk signals observed according to workspace policy.

The product-facing name is **Observer Gateway** when explaining what it does. Commands, API routes, UI navigation, and code should use the shorter name **Gateway**.

Milestone 1 should be treated as a full enterprise AI control plane, not only a model proxy. The gateway is the mandatory model boundary, while the lightweight SDK/OTLP layer is the application and agent runtime boundary. The first SDK milestone is intentionally small: trace context propagation, gateway header injection, manual span/log/artifact submission, and OTLP-compatible trace submission. Deep auto-instrumentation for popular frameworks comes later.

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

The AgentOps v2 documentation expands the target dashboard surface beyond raw telemetry. The useful concepts for Multica are:

- an insights dashboard for aggregate stability, usage, cost, and error trends;
- session drilldown with a session drawer/list, execution duration, LLM calls as chat history, event breakdowns, and a waterfall-style timeline;
- session overview across all recorded sessions;
- automatic LLM call tracking;
- named agent tracking for multi-agent workflows;
- trace list/detail, timeline, tree, and analytics views;
- span details for LLM calls, tools, operations, and tasks.

AI governance and third-party risk add a second product surface on top of those observations. The platform should help enterprises continuously inventory AI usage, classify AI systems and providers, enforce workspace policies, monitor behavior drift and risky usage, collect evidence, and generate governance insights. It should align to common frameworks such as NIST AI RMF, NIST AI 600-1 for generative AI, ISO/IEC 42001, EU AI Act concepts, NIST CSF 2.0 governance and supply-chain risk, NIST SP 800-161 supply-chain practices, and OWASP LLM security risks. It should not claim legal certification or automated regulatory compliance without human review.

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
- AgentOps-inspired tracking for sessions/traces, spans, LLM calls, tools, agents, operations, logs, metrics, and costs;
- lightweight TypeScript and Python SDKs for trace context, gateway header injection, explicit spans, logs, artifacts, and policy context;
- OTLP-compatible trace ingestion for apps that already use OpenTelemetry;
- enterprise application, environment, deployment, service, workflow, and external-agent inventory linked to gateway telemetry;
- correlation headers so gateway-created LLM spans attach to SDK-created app, workflow, agent, tool, and guardrail spans;
- Observer dashboard views for session overview, session drilldown, LLM calls, agent tracking, and dashboard visualizations;
- Observer dashboard views for applications, environments, workflows, tools, guardrails, logs, and artifacts;
- AI governance inventory for AI systems, agents, models, tools, data domains, users, providers, and third-party backends;
- policy enforcement for approved providers/models/tools, capture levels, sensitive-data handling, budget thresholds, human-approval requirements, and blocked actions;
- policy evaluation for both gateway model calls and SDK/OTLP-reported app, agent, tool, guardrail, approval, and artifact events;
- continuous compliance evidence collection from gateway telemetry, configuration changes, policy decisions, approvals, incidents, and audit logs;
- third-party AI risk register for provider/backends such as OpenAI, Anthropic, Groq, OpenRouter, local models, Claude OAuth pools, and enterprise-added tools;
- governance dashboard for risk posture, policy violations, exceptions, third-party exposure, compliance evidence status, and behavioral insights;
- governance intelligence for compliance posture, app/agent risk scoring, policy friction, behavior drift, alerts, evidence recommendations, and control gaps;
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
- warehouse/ClickHouse export;
- pixel-for-pixel AgentOps dashboard cloning;
- full AgentOps SDK auto-instrumentation parity for every supported Python/TypeScript framework;
- deep framework-specific auto-instrumentation beyond the lightweight SDK, OTLP ingest, and manual decorators/span helpers;
- legal certification, regulatory attestation, or compliance sign-off without human governance review;
- complete GRC suite parity with mature vendor risk, contract lifecycle, procurement, and audit-management platforms;
- full Dario shim/MCP/sub-agent feature parity;
- broad automated strategy generation from observed behavior.

Those are later phases.

## Architecture

The milestone 1 gateway has eleven main units.

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
   - request/session trace listing and detail reads.

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

6. **Trace ingestion API**

   Gateway traffic creates LLM spans automatically. For agent/tool/operation spans that are not visible from provider-compatible HTTP traffic, Multica also needs a small first-party ingestion API. This API accepts Multica/AgentOps-shaped trace and span payloads from internal Multica agents, lightweight SDKs, OTLP exporters, and enterprise apps.

   Milestone 1 should support enough ingestion to record named apps, environments, deployments, workflows, agents, tools, guardrails, operations, approvals, artifacts, and custom logs. It does not need to auto-instrument every external framework SDK.

7. **Lightweight SDK and OTLP ingestion layer**

   Add minimal TypeScript and Python SDKs that help enterprise developers use the gateway and submit runtime telemetry without adopting a large framework. The SDKs should:

   - create or join trace/session context;
   - inject accepted `X-Multica-*` headers into OpenAI-compatible and Anthropic-compatible SDK calls that target Gateway;
   - expose small span helpers for apps, workflows, agents, operations, tools, guardrails, approvals, logs, and artifacts;
   - submit spans to the trace ingestion API;
   - optionally export OpenTelemetry spans to a Multica OTLP-compatible endpoint;
   - fail open for app execution by default while surfacing export failures locally.

   This layer is not a full AgentOps SDK clone. It is the minimum runtime context layer required for a first-release enterprise control plane.

8. **Application and runtime inventory resolver**

   Gateway and SDK telemetry should resolve into first-class application inventory records. The resolver links raw telemetry to applications, services, environments, deployments, owners, business use cases, AI systems, data domains, providers, models, tools, MCP servers, and external agents.

   This resolver may create draft inventory records when new app/service identifiers appear. Admins can later classify ownership, risk, intended purpose, approval state, and data domains in the Governance UI.

9. **Observer dashboard**

   Add a workspace-level Gateway dashboard area in the shared frontend. Settings -> Gateway remains the configuration surface. The Gateway dashboard is the operational observability surface: overview, sessions, LLM calls, agents, and visualizations.

10. **Governance and policy engine**

   Add a policy layer that evaluates each gateway request, trace-ingestion payload, backend change, and high-risk agent/tool action against workspace governance rules. The first implementation should be deterministic and explainable: rules, decisions, reasons, enforcement action, actor, resource, and evidence references are all persisted.

   The policy engine should support `allow`, `warn`, `require_approval`, `redact`, `route_to_backend`, and `block` decisions. It should not rely on opaque AI judgment for hard enforcement in milestone 1.

11. **Risk and compliance evidence service**

   Add services for AI inventory, third-party risk records, control mappings, policy exceptions, incidents, and evidence bundles. These services use Gateway telemetry as continuous evidence and expose dashboard/API views for governance users. They should map evidence to frameworks, but must present that mapping as compliance support rather than legal certification.

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

## Observer Tracking Model

Multica should port AgentOps tracking concepts into native tables and UI, using OpenTelemetry-style naming where practical. The goal is compatibility of concepts and data shape, not a license-sensitive copy of the AgentOps app.

Trace/session fields:

- trace/session ID;
- root span ID;
- workspace ID;
- user ID;
- optional Multica agent ID and task ID;
- trace name;
- service name;
- tags;
- start/end timestamps;
- duration;
- status/end state;
- span count;
- error count;
- total cost;
- resource attributes when supplied by an SDK or internal runtime.

Span fields:

- span ID;
- parent span ID;
- span name;
- span kind;
- service name;
- start/end timestamps;
- duration;
- status code and message;
- attributes JSON;
- resource attributes JSON;
- events;
- links.

Supported span kinds should match the AgentOps set where useful:

- `workflow`
- `session`
- `task`
- `operation`
- `agent`
- `tool`
- `llm`
- `chain`
- `text`
- `guardrail`
- `http`
- `unknown`

LLM call attributes:

- provider/system;
- request model;
- response model;
- request type;
- max tokens;
- temperature;
- top-p/top-k;
- seed;
- stop sequences;
- streaming flag;
- prompt messages;
- completion messages;
- completion chunks;
- response ID;
- finish reason;
- stop reason;
- prompt tokens;
- completion tokens;
- total tokens;
- cache creation input tokens;
- cache read input tokens;
- reasoning tokens;
- streaming token count;
- prompt cost;
- completion cost;
- total cost;
- time to first token;
- time to generate;
- streaming duration;
- streaming chunk count.

Message and tool-call attributes:

- prompt role/content/type/speaker;
- request tool ID/type/name/description/arguments;
- completion ID/type/role/content/finish reason/speaker;
- completion tool-call ID/type/status/name/description/arguments;
- completion annotations where available.

Agent attributes:

- agent ID;
- agent name;
- role;
- available models;
- available tools;
- handoffs;
- source agent;
- destination agent;
- reasoning summary when supplied.

Tool attributes:

- tool ID;
- tool name;
- description;
- parameters/input;
- result/output;
- status;
- duration.

Operation, HTTP, log, and error attributes:

- operation name;
- operation version;
- entity input/output;
- HTTP method, URL, route, status code, user agent, and request ID;
- error type and message;
- log severity, body, timestamp, and attributes.

Metrics:

- token usage histograms by input/output/cache/reasoning token type;
- operation duration histograms;
- exception counters;
- generation choice counters;
- aggregate success/failure/indeterminate token and cost totals.

Capture source rules:

- Gateway-generated traffic always creates a trace/session and at least one `llm` span.
- Gateway should group requests into an existing session when clients provide an accepted session header such as `X-Multica-Session-ID` or `X-Multica-Trace-ID`.
- Existing Multica agent/runtime context should attach `X-Agent-ID` and `X-Task-ID` when available, producing agent/task linkage.
- External tools that cannot set custom headers still get user/workspace/session/LLM tracking, but named agent tracking will be limited.
- The trace ingestion API is the path for explicit app, agent, tool, guardrail, operation, workflow, artifact, and log spans from internal agents, lightweight SDKs, OTLP exporters, and enterprise apps.
- Captured content fields must obey the workspace capture policy before persistence.

## Enterprise App And Agent Observability Layer

Gateway-only capture is not enough for enterprise application governance. The Gateway sees model-boundary facts: model, provider, prompt, completion, usage, latency, cost, streaming state, error state, and gateway policy decisions. It cannot reliably know the full application workflow, tool execution results, RAG retrieval path, memory reads/writes, app-side guardrail outcomes, human approvals, runtime exceptions, logs, artifacts, or non-gateway model calls unless the application reports them.

Milestone 1 therefore adds a new design layer on top of the Gateway:

1. **Gateway captures LLM calls automatically.**

   Every OpenAI-compatible or Anthropic-compatible Gateway request creates a session/trace when none is supplied and at least one `llm` span. It records the gateway request, model call, routing decision, capture policy, usage, cost, latency, policy decisions, and upstream errors.

2. **SDK/OTLP ingest captures app, agent, workflow, tool, guardrail, log, and artifact telemetry.**

   Enterprise apps can use a small SDK or OTLP-compatible exporter to submit runtime spans. The first SDK milestone should be intentionally narrow:

   - start or join traces;
   - generate and propagate trace/session/span IDs;
   - inject Gateway correlation headers;
   - submit explicit spans for app, workflow, agent, operation, tool, guardrail, approval, HTTP, and task work;
   - submit logs and artifact references;
   - record errors without crashing the host app;
   - respect local capture controls before sending content.

   The SDK should not attempt deep automatic instrumentation of every popular framework in milestone 1. Later releases can add framework adapters for OpenAI Agents, LangChain, LangGraph, CrewAI, Agno, Google ADK, LiteLLM, LlamaIndex, MCP clients, and provider SDKs.

3. **Inventory maps telemetry to apps, owners, environments, AI systems, providers, data domains, and use cases.**

   Telemetry should attach to an application/service identity, environment, deployment, owner, business use case, AI system, data domain, provider/backend, model, tool/MCP server, and end-user context when available. Unknown identifiers should create draft inventory records rather than being silently ignored. Draft records become governance work items for admins to classify.

4. **Policy engine evaluates both gateway calls and app/agent actions.**

   Policies should evaluate gateway requests before upstream calls, and SDK/OTLP events as they arrive. This lets Multica enforce or flag:

   - unapproved production apps using AI;
   - unapproved providers, models, tools, or MCP servers;
   - sensitive data sent to restricted third parties;
   - high-autonomy agents without approval;
   - high-risk tool/action events;
   - missing human approval evidence;
   - stale third-party reviews;
   - direct provider calls reported by SDK instrumentation instead of Gateway.

   Gateway enforcement can block before model execution. SDK-reported events may be post-fact evidence unless the host app asks Multica for a pre-action policy decision. The SDK should support both patterns: fire-and-forget telemetry and explicit `evaluatePolicy` calls for high-risk actions.

5. **Governance intelligence turns traces and decisions into compliance posture, risk scoring, evidence, alerts, and recommendations.**

   The intelligence layer should compute explainable app and agent posture from recorded evidence. Milestone 1 should use deterministic and transparent scoring inputs, not opaque compliance claims. Examples:

   - app risk score based on approval state, environment, data domains, autonomy, third-party exposure, policy violations, and incident history;
   - agent risk score based on tool permissions, model/provider usage, action success/error rate, approval coverage, exception count, and behavior drift;
   - control evidence coverage for NIST AI RMF, ISO/IEC 42001, EU AI Act concepts, NIST CSF, NIST SP 800-161, and OWASP LLM risk categories;
   - alerts for new production apps, unknown tools, sudden provider changes, sensitive-data movement, repeated policy exceptions, cost anomalies, and stale vendor reviews;
   - recommendations for policy changes, missing inventory classifications, evidence gaps, and candidate enterprise-approved skills.

### Correlation Headers

Gateway should accept these headers from SDKs, internal Multica agents, and enterprise apps:

- `X-Multica-Trace-ID`
- `X-Multica-Session-ID`
- `X-Multica-Parent-Span-ID`
- `X-Multica-App-ID`
- `X-Multica-Service-Name`
- `X-Multica-Environment`
- `X-Multica-Deployment-ID`
- `X-Multica-AI-System-ID`
- `X-Multica-Agent-ID`
- `X-Multica-Workflow-ID`
- `X-Multica-End-User-ID`
- `X-Multica-Use-Case`
- `X-Multica-Data-Domains`
- `X-Multica-Tool-Hint`

The Gateway should store these values as request metadata, resource attributes, and span attributes after validation. It should not trust user-supplied IDs blindly for authorization. Workspace membership, gateway key ownership, configured ingest keys, and explicit app registration still gate writes.

### Milestone 1 SDK Scope

The first SDKs should be small enough to ship alongside Gateway Milestone 1:

- TypeScript package for Node apps;
- Python package for Python apps;
- context object with trace/session/span IDs;
- helpers for Gateway client configuration and header injection;
- `startTrace`, `endTrace`, `span`, `log`, `artifact`, and `evaluatePolicy` primitives;
- OTLP-compatible trace submission option;
- local queue with bounded retry and flush-on-exit;
- capture-level controls that default to metadata/redacted behavior;
- examples for OpenAI SDK, Anthropic SDK, and a custom agent workflow.

Later SDK phases add deeper auto-instrumentation. The milestone 1 contract should be stable enough that future framework adapters emit the same trace/span shapes rather than creating a parallel telemetry model.

## Governance And Compliance Platform

The governance layer turns Gateway observations into an enterprise AI control plane. It should continuously answer:

- which enterprise applications and services are using AI;
- what AI systems, agents, models, providers, tools, data domains, and users exist in the workspace;
- which third parties are being used and for what purpose;
- whether usage complies with workspace policy;
- which risks, exceptions, incidents, and evidence gaps need attention;
- how user and agent behavior is changing over time;
- which patterns should inform enterprise-specific skills, approved agent templates, and strategy.

The design aligns to current governance patterns:

- NIST AI RMF functions: govern, map, measure, manage;
- NIST AI 600-1 generative AI risk profile;
- ISO/IEC 42001 AI management-system concepts;
- EU AI Act concepts such as risk classification, logging, transparency, human oversight, post-market monitoring, and technical documentation;
- NIST CSF 2.0 governance and risk-management concepts;
- NIST SP 800-161 supply-chain risk practices;
- OWASP LLM risks such as prompt injection, sensitive information disclosure, supply-chain vulnerabilities, excessive agency, and overreliance.

This is not legal advice and must not be presented as automatic compliance certification. The product provides controls, monitoring, evidence, and workflow support so an enterprise governance team can assess and manage compliance.

Governance inventory:

- enterprise applications, services, environments, deployments, and owners;
- AI systems and business use cases;
- Multica agents and enterprise-created agents;
- model/provider backends;
- third-party tools, MCP servers, plugins, APIs, and data connectors;
- data domains and sensitivity tags;
- users, groups, roles, workspaces, and runtime environments;
- risk classification by intended purpose, domain, data sensitivity, autonomy level, external impact, and third-party dependency.

Third-party risk records:

- provider/tool name;
- backend type;
- owner;
- approved use cases;
- data categories sent to the provider;
- regions and hosting notes when known;
- contract/security-review status;
- terms, DPA, SOC 2, ISO, or security evidence links or attachments where available;
- known limitations and prohibited uses;
- model list and capability class;
- risk score;
- review cadence;
- last assessment date;
- next review date;
- active exceptions.

Policy engine:

- approved, restricted, and blocked providers;
- approved, restricted, and blocked models;
- approved, restricted, and blocked tools/MCP servers;
- data-classification rules for prompts, completions, tool inputs, logs, and files;
- PII/secret/code/customer-data redaction rules;
- budget and rate thresholds by user, group, agent, backend, model, and workspace;
- human approval for high-risk models, sensitive data, external actions, financial actions, production changes, or high-autonomy agents;
- routing policy to force specific backends for specific workspaces, users, agents, data classes, or use cases;
- exception handling with reason, approver, expiration, and evidence.

Enforcement points:

- before gateway request routing;
- before streaming content is forwarded when request metadata is enough to decide;
- during streaming for content classifiers that can flag sensitive output or unsafe tool-call intent;
- before tool/action execution for Multica-managed agents;
- when adding or changing backends;
- when creating or updating agents, skills, MCP/tool connections, and local runtimes;
- when trace ingestion reports an incident, blocked action, or policy override.

Policy decision record:

- policy ID and version;
- subject user/agent/workspace;
- resource backend/model/tool/data class;
- decision: `allow`, `warn`, `require_approval`, `redact`, `route_to_backend`, `block`;
- reason code;
- matched rules;
- request/session/span IDs;
- approver and approval status when applicable;
- evidence references;
- timestamp.

Continuous monitoring:

- policy violations and near-misses;
- anomalous spending, token usage, latency, and error rate;
- unusual provider/model/tool adoption;
- risky prompt or tool-call patterns;
- sensitive-data movement to third parties;
- agent autonomy and external action rates;
- behavior drift for users, agents, and teams;
- repeated exceptions or stale approvals;
- backend health, credential failures, and routing fallback events.

Compliance evidence:

- backend/provider configuration history;
- gateway key lifecycle;
- policy configuration history;
- policy decision logs;
- capture-policy changes;
- approval workflows;
- incidents and remediation notes;
- third-party assessments;
- trace/session samples;
- exported reports for framework/control mappings;
- immutable-enough audit log entries with actor, timestamp, before/after values, and request IDs.

Insights:

- high-risk usage trends;
- departments or teams adopting unapproved providers/tools;
- users or agents with unusual behavior changes;
- third-party concentration risk;
- opportunities to convert recurring safe behavior into approved enterprise skills;
- policy friction points where users repeatedly request exceptions;
- cost, quality, and risk tradeoffs by backend/model/agent.

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

Add three UI surfaces.

1. **Settings -> Gateway**

   This remains the configuration surface in `packages/views/settings`.

   The tab should include:

   - backend list;
   - default backend selector;
   - capture policy selector;
   - registered application and ingest-key list;
   - SDK/OTLP setup instructions;
   - generated key instructions;
   - current user key status;
   - examples for OpenAI and Anthropic environment variables;
   - examples for `X-Multica-*` correlation headers.

   Admin-only controls:

   - add backend;
   - update backend;
   - disable/delete backend;
   - set default backend;
   - change capture policy;
   - create/revoke application ingest keys;
   - approve or archive draft application inventory records.

   Member controls:

   - view instructions;
   - create/revoke their own gateway key.

2. **Gateway dashboard**

   Add a workspace-level Gateway dashboard area in `packages/views/gateway` and wire it into both web and desktop navigation. Use user-facing explanatory copy such as "Observer Gateway captures model traffic according to workspace policy."

   The first dashboard milestone includes:

   - **Overview**: aggregate sessions, LLM calls, tokens, cost, latency, error rate, top models, top backends, and trend charts for the selected date range.
   - **Applications**: app/service inventory from SDK/OTLP telemetry, owners, environments, deployments, AI systems, data domains, usage, risk status, and last activity.
   - **Sessions**: paginated session drawer/list with search, filters, duration, status, total cost, span count, LLM call count, tool count, agent count, and last activity.
   - **Session Drilldown**: metadata panel, chat-history view for LLM prompts/completions when policy permits, event breakdown by span kind, waterfall/timeline, tree view, and selected-span details.
   - **LLM Calls**: table of model calls with backend, model, user, agent, token counts, cost, latency, streaming flag, finish reason, and error status.
   - **Agents**: named-agent view showing agent spans, models used, tools used, handoffs/coordination when supplied, task linkage, error rate, latency, token usage, and cost.
   - **Workflows and Tools**: workflow, operation, tool, MCP server, guardrail, approval, and external action spans with status, duration, policy decisions, inputs/outputs when permitted, and linked artifacts/logs.
   - **Logs and Artifacts**: app/agent logs, artifact references, file metadata, retention state, capture policy state, and linked traces/spans.
   - **Visualizations**: timeline/waterfall, hierarchical tree, and graph view for span parent/child relationships.

   Content visibility must match the capture policy. `metadata_only` should still render useful timings, status, costs, and counts, but prompt/completion/tool bodies should appear as unavailable due to workspace policy.

3. **Governance dashboard**

   Add a workspace-level Governance area in `packages/views/gateway` or a sibling `packages/views/governance` package, depending on implementation size. It should share the same API client and query patterns as the Gateway dashboard.

   The first governance milestone includes:

   - **Risk Overview**: policy violations, exceptions, incidents, risk score trends, third-party exposure, sensitive-data movement, high-risk agent activity, and evidence coverage.
   - **AI Inventory**: applications, services, environments, deployments, AI systems, agents, models, providers, tools, MCP servers, data domains, owners, intended purposes, autonomy levels, risk classifications, and approval state.
   - **Policies**: approved/restricted/blocked providers, models, tools, data classes, budget limits, human-approval rules, routing rules, and capture policies.
   - **Third-Party Risk**: provider and tool risk records, review status, evidence links, approved use cases, data categories, next review dates, and active exceptions.
   - **Evidence**: framework/control mapping, evidence bundles, policy decisions, audit logs, incidents, approvals, and exportable reports.
   - **Insights**: behavioral trends that identify risky usage, repeated exceptions, shadow AI adoption, and candidate workflows for enterprise-approved skills.

   Governance pages must make policy decisions explainable. For every block, warning, approval requirement, redaction, or forced route, the UI should show the matched rule, reason code, affected user/agent/resource, timestamp, and evidence links.

The UI must stay in shared packages where possible:

- shared API client and types in `packages/core`;
- Gateway settings component in `packages/views/settings`;
- Gateway dashboard pages/components in `packages/views/gateway`;
- Governance dashboard pages/components in `packages/views/gateway` or `packages/views/governance`;
- web and desktop only provide route wiring/navigation adapters.

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
- `gateway_policy`
  - workspace ID;
  - name;
  - description;
  - policy type;
  - enabled flag;
  - version;
  - rule definition JSON;
  - enforcement mode;
  - created/updated timestamps.
- `gateway_policy_decision`
  - workspace ID;
  - policy ID/version;
  - subject user/agent;
  - resource backend/model/tool/data class;
  - decision;
  - reason code;
  - matched rules JSON;
  - request/session/span IDs;
  - approval status;
  - evidence references;
  - timestamp.

Application and ingest inventory:

- `gateway_application`
  - workspace ID;
  - stable app key supplied by SDK/OTLP or generated as draft;
  - display name;
  - service name;
  - owner user/member or team reference;
  - business use case;
  - linked AI system ID;
  - approval state;
  - risk tier;
  - data domains;
  - created/updated timestamps.
- `gateway_application_environment`
  - workspace ID;
  - application ID;
  - environment name such as `development`, `staging`, or `production`;
  - region/hosting notes;
  - capture policy override if allowed;
  - approval state;
  - last observed timestamp.
- `gateway_deployment`
  - workspace ID;
  - application ID;
  - environment ID;
  - deployment ID or version;
  - git SHA/build ID when supplied;
  - release owner;
  - started/ended timestamps;
  - resource attributes JSON.
- `gateway_ingest_key`
  - workspace ID;
  - application ID;
  - key hash;
  - encrypted key value or secret reference;
  - key prefix;
  - allowed environments;
  - revoked timestamp;
  - last used timestamp;
  - created timestamp.
- `gateway_artifact`
  - workspace ID;
  - session/span parent;
  - application ID;
  - artifact type;
  - display name;
  - URI or attachment reference;
  - content hash;
  - size bytes;
  - capture policy used;
  - metadata JSON;
  - created timestamp.

Telemetry:

- `gateway_session`
  - workspace ID;
  - user ID;
  - optional agent ID/task ID if present in request metadata;
  - optional application/environment/deployment IDs when supplied or resolved;
  - trace ID;
  - root span ID;
  - name;
  - client protocol;
  - client tool hint;
  - service name;
  - tags;
  - status/end state;
  - started/ended timestamps;
  - duration;
  - span count;
  - error count;
  - total cost.
- `gateway_request`
  - session ID;
  - backend ID;
  - optional application/environment/deployment IDs;
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
  - reasoning token counts;
  - streaming token counts;
  - usage source;
  - prompt/completion/total cost;
  - response ID;
  - finish/stop reason;
  - time to first token;
  - streaming duration;
  - streaming chunk count.
- `gateway_span`
  - session/request parent;
  - optional application/environment/deployment IDs;
  - trace ID;
  - span ID;
  - parent span ID;
  - span type;
  - span kind;
  - name;
  - service name;
  - status code/message;
  - start/end timestamps;
  - duration;
  - attributes JSON;
  - resource attributes JSON.
- `gateway_event`
  - session/request/span;
  - event type;
  - redacted payload JSON;
  - timestamp.
- `gateway_span_link`
  - trace ID;
  - span ID;
  - linked trace ID;
  - linked span ID;
  - attributes JSON.
- `gateway_log`
  - session/request/span;
  - optional application ID;
  - severity;
  - body;
  - attributes JSON;
  - timestamp.
- `gateway_agent_observation`
  - session/span parent;
  - agent ID;
  - agent name;
  - role;
  - models;
  - tools;
  - handoff source/destination;
  - reasoning summary.
- `gateway_tool_observation`
  - session/span parent;
  - tool ID;
  - tool name;
  - description;
  - parameters;
  - result;
  - status;
  - duration.
- `gateway_guardrail_observation`
  - session/span parent;
  - guardrail ID/name;
  - guardrail type;
  - input/output references;
  - decision;
  - reason code;
  - severity;
  - duration.
- `gateway_metric_rollup`
  - workspace ID;
  - date/time bucket;
  - user/backend/model/agent/application/environment dimensions;
  - token counts;
  - cost totals;
  - latency aggregates;
  - request/error counts.
- `gateway_model_pricing`
  - provider/backend type;
  - model pattern;
  - input/output/cache token prices;
  - currency;
  - effective timestamp.

Governance:

- `ai_system_inventory`
  - workspace ID;
  - name;
  - owner user/member;
  - intended purpose;
  - business process;
  - autonomy level;
  - external impact level;
  - data domains;
  - risk classification;
  - approval state;
  - linked agents/backends/tools;
  - created/updated timestamps.
- `ai_third_party_risk`
  - workspace ID;
  - provider/tool/backend ID;
  - owner;
  - approved use cases;
  - data categories;
  - regions/hosting notes;
  - contract/security-review status;
  - evidence links JSON;
  - limitations/prohibited uses;
  - risk score;
  - review cadence;
  - last/next review timestamps;
  - active exception count.
- `ai_control_mapping`
  - workspace ID;
  - framework;
  - control ID;
  - control title;
  - mapped policy IDs;
  - mapped evidence queries;
  - status;
  - owner;
  - updated timestamp.
- `ai_evidence`
  - workspace ID;
  - evidence type;
  - framework/control references;
  - linked request/session/span/policy/backend/provider IDs;
  - summary;
  - payload or attachment reference;
  - generated timestamp;
  - retention timestamp.
- `ai_policy_exception`
  - workspace ID;
  - policy ID;
  - requester;
  - approver;
  - reason;
  - scope;
  - status;
  - expiration timestamp;
  - evidence references.
- `ai_incident`
  - workspace ID;
  - severity;
  - category;
  - linked request/session/span/policy/provider IDs;
  - summary;
  - status;
  - remediation notes;
  - opened/closed timestamps.
- `ai_audit_log`
  - workspace ID;
  - actor;
  - action;
  - target type/ID;
  - before/after JSON;
  - request ID;
  - timestamp.

Existing `runtime_usage` and `task_usage` should not be overloaded for gateway telemetry. They track Multica agent-runtime usage. Gateway traffic is a broader enterprise observability surface and needs separate tables.

If the initial Gateway foundation migration has already landed without the application/environment/deployment/artifact/ingest-key entities above, add them in the telemetry-ingest phase as a forward migration. Do not rewrite an applied foundation migration in an active branch.

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
- restrict governance policy, third-party risk, exceptions, evidence export, and incident-management writes to workspace owners/admins or future explicit governance roles;
- scope gateway keys to one workspace;
- apply capture policy before telemetry persistence;
- apply policy decisions before upstream provider calls or tool/action execution where enforcement is possible;
- do not log raw prompt/completion content through general server logs;
- record audit-friendly metadata for backend, policy, risk, evidence, exception, and incident changes;
- preserve evidence according to workspace retention policy;
- make policy decisions explainable and reviewable by human admins.

Compliance caveat:

Claude OAuth/subscription routing is sensitive because it depends on provider OAuth accounts and Dario-like wire behavior. The implementation must make this backend explicit in UI/API and should not silently route enterprise traffic through it without admin configuration.

Governance caveat:

The platform can support governance, compliance evidence, third-party risk assessment, and continuous monitoring, but it must not claim to certify legal compliance automatically. Final compliance determinations remain a human/legal/governance responsibility.

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
- capture/telemetry persistence failure;
- policy block;
- approval required but missing, expired, or denied;
- governance evidence persistence failure;
- stale third-party review blocking restricted use.

Telemetry persistence failures should not normally fail the model request if the upstream request can still complete. They should be logged as internal errors and exposed in Gateway health/status. Authentication, routing, policy, and backend credential errors should fail before any upstream call.

Policy blocks and approval-required decisions should return protocol-compatible errors to model clients, but the policy decision record must retain the exact rule and reason so admins can review it.

## Integration Points

Backend:

- add gateway routes in `server/cmd/server/router.go`;
- add handler methods under `server/internal/handler`;
- add core gateway package under `server/internal/gateway`;
- add trace ingestion, OTLP ingestion, application inventory resolution, and policy-evaluation services under `server/internal/gateway` or focused subpackages;
- add dashboard query service under the gateway package or a focused handler-adjacent service;
- add governance policy/risk/evidence services under the gateway package or a dedicated `server/internal/governance` package;
- add migrations under `server/migrations`;
- add sqlc queries under `server/pkg/db/queries`;
- regenerate db code with `make sqlc`.

CLI:

- add a `gateway` command group under `server/cmd/multica`;
- use existing CLI config in `server/internal/cli/config.go`.

Frontend:

- add Gateway types and API client methods under `packages/core`;
- add Governance types and API client methods under `packages/core`;
- add Settings -> Gateway tab under `packages/views/settings`;
- add Gateway dashboard views under `packages/views/gateway`;
- add Governance dashboard views under `packages/views/gateway` or `packages/views/governance`;
- add web routes and desktop routes for the Gateway and Governance dashboards.

SDKs and ingestion:

- add a TypeScript SDK package under `packages/` so Node apps can create trace context, inject Gateway headers, submit spans/logs/artifacts, and call policy evaluation;
- add a small Python SDK under a dedicated `sdks/python/` tree or equivalent package boundary;
- keep SDK state local and dependency-light: context, IDs, headers, bounded queue, JSON/OTLP submission, and capture controls;
- add examples for OpenAI SDK, Anthropic SDK, a custom workflow, explicit tool spans, guardrail spans, and artifact references;
- add `/api/gateway/otlp/v1/traces` or an equivalent OTLP-compatible ingest route for apps that already emit OpenTelemetry.

Tracking and dashboard APIs:

- `POST /api/gateway/traces` ingests explicit trace/span/log/artifact payloads from Multica agents, lightweight SDKs, OTLP adapters, and enterprise apps.
- `POST /api/gateway/otlp/v1/traces` accepts OTLP-compatible trace payloads and maps them into the same session/span model.
- `POST /api/gateway/policy/evaluate` evaluates high-risk app, agent, workflow, tool, guardrail, approval, or artifact actions before the host app executes them.
- `GET/POST/PATCH /api/gateway/applications` manages application registration, draft application resolution, owners, environments, and deployment metadata.
- `GET /api/gateway/overview` returns aggregate dashboard metrics for the selected time range.
- `GET /api/gateway/sessions` lists sessions/traces with filters for user, agent, backend, model, status, tags, and time range.
- `GET /api/gateway/sessions/{id}` returns session metadata, summary metrics, and root span details.
- `GET /api/gateway/sessions/{id}/spans` returns all spans, events, links, logs, model calls, agent observations, and tool observations needed for drilldown visualizations.
- `GET /api/gateway/llm-calls` lists model calls with filters for backend, model, user, agent, status, and time range.
- `GET /api/gateway/agents` returns named-agent metrics, agent span summaries, model usage, tool usage, handoffs, and task linkage.
- `GET /api/gateway/applications/{id}/traces` lists traces and spans linked to one app/service/environment/deployment.
- `GET /api/gateway/workflows` lists workflow, tool, guardrail, approval, and operation spans across applications and agents.
- `GET /api/gateway/artifacts` lists artifact references with policy-safe metadata and links to traces/spans.
- `GET /api/gateway/requests` remains useful for low-level gateway request debugging.
- Read APIs must apply workspace membership checks and must not expose captured content that was not stored under the active capture policy.

Governance APIs:

- `GET /api/governance/overview` returns risk posture, policy violations, third-party exposure, exception status, and evidence coverage.
- `GET/POST/PATCH /api/governance/inventory` manages applications, services, environments, deployments, AI systems, agents, models, tools, data domains, intended purpose, owners, and risk classification.
- `GET/POST/PATCH /api/governance/policies` manages deterministic policy rules, versions, enforcement modes, and enabled state.
- `GET /api/governance/policy-decisions` lists explainable policy decisions with filters by user, agent, model, backend, tool, decision, and time range.
- `GET/POST/PATCH /api/governance/third-parties` manages provider/tool risk records, review status, evidence links, approved use cases, and exceptions.
- `GET/POST/PATCH /api/governance/exceptions` manages exception requests, approvals, expirations, and scope.
- `GET/POST/PATCH /api/governance/incidents` manages governance incidents and remediation notes.
- `GET /api/governance/evidence` lists evidence records and control mappings.
- `POST /api/governance/evidence/export` generates exportable evidence bundles for selected frameworks, controls, date ranges, and systems.

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
- telemetry writes for success, upstream failure, and client disconnect where practical;
- generated trace/session, span, model-call, event, log, agent, and tool records;
- explicit trace ingestion validation and authorization;
- OTLP trace ingestion mapping into gateway sessions/spans;
- SDK correlation headers attaching gateway-created LLM spans to app/workflow/agent parent spans;
- application ingest-key authentication, revocation, and allowed-environment checks;
- draft application inventory creation for unknown app/service identifiers;
- application/environment/deployment inventory resolution from gateway headers and SDK/OTLP resources;
- artifact and log ingestion with capture-policy enforcement;
- guardrail, approval, and high-risk tool/action policy evaluation from SDK-submitted events;
- pre-action `evaluatePolicy` returning allow, warn, require approval, redact, route, and block decisions;
- dashboard overview aggregations;
- session list filtering and pagination;
- session drilldown data shape;
- LLM call filtering and cost/token aggregation;
- agent tracking summaries and handoff/tool/model aggregation;
- capture-policy enforcement on dashboard read APIs;
- policy engine decisions for allow, warn, require approval, redact, route, and block;
- policy decision persistence with matched rule and reason code;
- governance inventory CRUD and risk classification;
- third-party risk record CRUD and review-state transitions;
- exception approval and expiration behavior;
- incident creation and remediation updates;
- evidence/control mapping queries and export payloads;
- policy enforcement before upstream provider calls.

CLI tests:

- `multica gateway key` prints base URLs and key material in the expected format;
- admin commands call the right API payloads;
- commands respect `--server-url`, `--workspace-id`, and `--profile`.

Frontend tests:

- Settings -> Gateway renders for workspace members;
- Settings -> Gateway renders SDK/OTLP setup, application ingest keys, and correlation-header examples;
- Gateway dashboard Overview renders aggregate metrics and charts;
- Applications view renders apps, owners, environments, deployments, AI systems, risk status, and last activity;
- Sessions view renders list/search/filter states;
- Session Drilldown renders metadata, timeline/waterfall, tree, and selected-span details;
- LLM Calls view renders model call table and handles redacted/hidden content;
- Agents view renders named agents, tools, handoffs, and metrics;
- Workflows and Tools views render operation/tool/guardrail/approval spans and linked policy decisions;
- Logs and Artifacts views render policy-safe metadata and link back to traces/spans;
- Governance Risk Overview renders posture, violations, exceptions, incidents, and evidence coverage;
- AI Inventory renders applications, services, environments, deployments, systems, agents, providers, models, tools, owners, and risk classifications;
- Policies view renders rules, enforcement modes, versions, and decision history;
- Third-Party Risk view renders provider/tool reviews, evidence links, and active exceptions;
- Evidence view renders control mappings and export states;
- admin-only controls are hidden or disabled for non-admins;
- backend list and policy selector use workspace-scoped query keys;
- mutations invalidate Gateway queries.

Manual verification:

- configure `OPENAI_BASE_URL` and `OPENAI_API_KEY` against local Multica;
- run an OpenAI-compatible SDK request with and without streaming;
- configure `ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY`;
- run an Anthropic Messages request with and without streaming;
- verify telemetry appears in PostgreSQL;
- run the lightweight SDK example and verify Gateway LLM calls attach to the SDK-created parent trace;
- send an OTLP-compatible trace payload and verify it maps to Gateway sessions/spans;
- create an application ingest key, submit app/workflow/tool/guardrail spans, and verify app inventory resolution;
- submit a log and artifact reference and verify capture policy controls visibility;
- verify policy changes alter captured payloads;
- verify Overview, Sessions, Session Drilldown, LLM Calls, and Agents views show the recorded traffic;
- verify an explicit trace-ingestion payload can create agent/tool/operation spans not visible from gateway-only traffic;
- configure a policy that blocks a model/provider/tool and verify the gateway blocks before upstream call;
- configure an approval-required rule and verify denied/missing approvals are recorded and enforced;
- add a third-party provider risk record and verify it appears in Governance dashboard and evidence exports.

## Rollout

1. Add schema and backend package boundaries.
2. Implement management APIs and CLI commands.
3. Implement OpenAI-compatible routing and streaming.
4. Implement Anthropic-compatible routing and streaming.
5. Implement telemetry recorder, capture policy, and correlation-header capture.
6. Add application/environment/deployment/artifact/ingest-key schema extensions if they were not included in the foundation migration.
7. Implement trace ingestion, OTLP-compatible ingestion, application inventory resolution, and pre-action policy evaluation APIs.
8. Implement lightweight TypeScript and Python SDKs for trace context, Gateway header injection, span/log/artifact submission, and `evaluatePolicy`.
9. Implement governance policy engine, policy-decision logging, and enforcement hooks for gateway and SDK/OTLP events.
10. Implement dashboard query APIs for gateway traffic, application telemetry, workflows/tools/guardrails/logs/artifacts, and governance.
11. Implement governance inventory, third-party risk, exceptions, incidents, evidence, recommendations, and export APIs.
12. Add Settings -> Gateway, including SDK/OTLP setup and application ingest-key management.
13. Add Gateway dashboard views: Overview, Applications, Sessions, Session Drilldown, LLM Calls, Agents, Workflows/Tools, Logs/Artifacts, and visualizations.
14. Add Governance dashboard views: Risk Overview, AI Inventory, Policies, Third-Party Risk, Evidence, Insights, alerts, and recommendations.
15. Add `claude-oauth` adapter boundary and first implementation choice.
16. Run end-to-end checks with real SDK clients, OTLP payloads, explicit trace-ingestion payloads, correlated Gateway LLM calls, and governance policy scenarios.

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
- Mitigation: write through a telemetry interface and keep the future warehouse/ClickHouse export path open.

SDK adoption:

- Some enterprise apps may only configure Gateway base URLs and never install the SDK.
- Mitigation: Gateway still provides automatic LLM call capture, while SDK setup should be minimal, copy-pasteable, and useful even without framework adapters.

Telemetry spoofing:

- App/service/agent headers and SDK payloads are user-controlled inputs.
- Mitigation: require workspace-scoped gateway keys or ingest keys, validate IDs against registered inventory where possible, create draft records for unknown identifiers, and never use headers alone for authorization.

Direct provider bypass:

- Apps can call providers directly and avoid Gateway enforcement.
- Mitigation: use SDK/OTLP telemetry to detect reported direct calls, compare observed provider usage with approved Gateway backends, and surface bypass risk in Governance.

Framework instrumentation scope:

- Full auto-instrumentation for every popular agent framework can delay the first release.
- Mitigation: ship the small SDK and OTLP ingest first, define stable span semantics, and add framework adapters incrementally after Milestone 1.

Credential handling:

- Admin-managed provider keys and Claude OAuth tokens are sensitive.
- Mitigation: encrypted storage or secret references, masked outputs, strict RBAC, and audit metadata.

Dashboard scope:

- Adding session drilldown, overview, LLM calls, agent tracking, and visualizations makes milestone 1 meaningfully larger.
- Mitigation: build dashboard APIs and views from the same trace/span model, keep visualizations focused, and defer framework-specific visualizers beyond generic agent/tool/LLM handling.

Agent attribution:

- Some external tools will not let users add custom headers or explicit trace metadata.
- Mitigation: always track workspace/user/session/LLM metadata, attach Multica agent/task IDs where available, infer only conservative client/tool hints, and use explicit trace ingestion for named-agent accuracy.

Governance overclaiming:

- A platform can collect evidence and enforce policy, but cannot guarantee legal compliance automatically.
- Mitigation: label framework mappings as support/evidence, require human approval for compliance status, and avoid certification language.

Policy false positives:

- Blocking or approval rules may interrupt legitimate agent workflows.
- Mitigation: start with explainable deterministic rules, warning mode, scoped exceptions, dry-run evaluation, and clear decision history.

Continuous monitoring volume:

- Policy decision logs, evidence, audit events, and telemetry can grow quickly.
- Mitigation: retention policies, rollups, pagination, and future export paths to warehouse/ClickHouse.

Third-party risk freshness:

- Provider evidence and model capabilities can become stale.
- Mitigation: review cadence fields, stale-review alerts, active exception tracking, and admin-owned review workflows.

## Implementation Planning Defaults

These decisions are locked for the first implementation plan unless the written spec review changes them:

- `multica gateway key` retrieves an active key by storing an encrypted key value plus a hash.
- Gateway provider credentials use a new Gateway secrets package and require a production encryption secret.
- The first `claude-oauth` implementation is sidecar-backed through the adapter interface.
- Cross-protocol tool support covers text, tool definitions, tool calls, tool results, and streaming deltas for those shapes.
- Pricing lives in `gateway_model_pricing`; unknown prices produce null cost values.
- Milestone 1 includes AgentOps-inspired dashboard APIs and UI for Overview, Sessions, Session Drilldown, LLM Calls, Agents, and generic timeline/tree/graph visualizations.
- Milestone 1 records the concrete AgentOps-style fields listed in the Observer Tracking Model section, using native Multica storage and capture policy.
- Milestone 1 includes the lightweight SDK/OTLP enterprise app and agent observability layer as part of the first release control plane.
- The first SDK scope is trace context, Gateway header injection, manual spans, logs, artifacts, bounded submission, capture controls, and `evaluatePolicy`.
- Deep auto-instrumentation for OpenAI Agents, LangChain, LangGraph, CrewAI, Agno, Google ADK, LiteLLM, LlamaIndex, MCP clients, and provider SDKs is deferred until after Milestone 1.
- If existing Gateway foundation work has already created base telemetry and governance tables, the telemetry-ingest phase should add application, environment, deployment, artifact, guardrail, and ingest-key extensions in a new migration.
- Milestone 1 includes governance APIs and UI for AI inventory, deterministic policies, explainable policy decisions, third-party risk, exceptions, incidents, evidence, and insights.
- Governance framework mappings support NIST AI RMF, NIST AI 600-1, ISO/IEC 42001, EU AI Act concepts, NIST CSF, NIST SP 800-161, and OWASP LLM risk categories as evidence/control mappings, not automatic certification.

## References Reviewed

- Related future-feature backlog: `docs/superpowers/specs/2026-05-03-dario-agentops-feature-backlog.md`
- NIST AI RMF: `https://www.nist.gov/itl/ai-risk-management-framework`
- NIST AI 600-1 Generative AI Profile: `https://www.nist.gov/publications/artificial-intelligence-risk-management-framework-generative-artificial-intelligence`
- ISO/IEC 42001: `https://www.iso.org/standard/42001`
- EU AI Act overview: `https://digital-strategy.ec.europa.eu/en/policies/regulatory-framework-ai`
- EU AI Act Q&A: `https://digital-strategy.ec.europa.eu/en/faqs/navigating-ai-act`
- NIST Cybersecurity Framework: `https://www.nist.gov/cyberframework`
- NIST SP 800-161 Rev. 1: `https://csrc.nist.gov/pubs/sp/800/161/r1/upd1/final`
- OWASP Top 10 for LLM Applications 2025: `https://genai.owasp.org/resource/owasp-top-10-for-llm-applications-2025`
- AgentOps dashboard documentation: `https://docs.agentops.ai/v2/usage/dashboard-info`
- AgentOps LLM tracking documentation: `https://docs.agentops.ai/v2/usage/tracking-llm-calls`
- AgentOps agent tracking documentation: `https://docs.agentops.ai/v2/usage/tracking-agents`
- AgentOps trace documentation: `https://docs.agentops.ai/v2/concepts/traces`
- AgentOps span documentation: `https://docs.agentops.ai/v2/concepts/spans`
- Local AgentOps SDK semantic conventions:
  - `agentops/agentops/semconv/span_attributes.py`
  - `agentops/agentops/semconv/span_kinds.py`
  - `agentops/agentops/semconv/agent.py`
  - `agentops/agentops/semconv/tool.py`
  - `agentops/agentops/semconv/message.py`
- Local AgentOps dashboard data shapes:
  - `agentops/app/dashboard/types/ISpan.ts`
  - `agentops/app/dashboard/types/ITrace.ts`
  - `agentops/app/dashboard/components/charts/bar-chart/span-processing.ts`
  - `agentops/app/dashboard/app/(with-layout)/traces/_components/use-trace-stats.ts`
- Local AgentOps storage/cost references:
  - `agentops/app/api/sql/otel_traces_improved.sql`
  - `agentops/app/opentelemetry-collector/builder/costs/__init__.py`

# Subscription Runtime Gateway Design

Date: 2026-05-17

Status: proposed for implementation planning

## Goal

Multica Gateway should support both normal provider API-key backends and subscription-backed Claude Code/Codex backends behind the same OpenAI-compatible and Anthropic-compatible Gateway surfaces. Normal API-key backends continue to use direct HTTP forwarding. Subscription-backed backends store encrypted subscription credential bundles in Multica, dispatch those bundles to compatible authenticated workspace daemons for validation/install, and route model calls through validated daemons because Claude Code and Codex execution live on the daemon host.

## Superseded Design

This design supersedes `docs/superpowers/specs/2026-05-09-claude-oauth-gateway-design.md` for subscription-backed routing. The earlier design chose server-stored Claude OAuth tokens with server-side direct forwarding to Anthropic. That conflicts with the current product direction: subscription credentials are configured in Gateway, but subscription-backed execution must happen through Multica daemon runtimes.

The older direct OAuth path should not be extended for subscription backends. Gateway may store subscription credential material as encrypted escrow for daemon provisioning and validation, but Gateway must not use those credentials in the direct HTTP forwarder.

## Core Model

Gateway backends gain an explicit transport and credential kind.

```text
transport:
  direct_http       # normal provider API key, Gateway calls upstream directly
  daemon_dispatch  # subscription runtime, daemon executes provider locally

credential_kind:
  api_key
  subscription_bundle

subscription_provider:
  claude_code
  codex

dispatch_scope:
  workspace_authenticated_daemons  # default
  owner_daemons_only               # reserved for stricter future controls
  selected_daemons                 # reserved for explicit allowlists
```

The default dispatch policy is intentionally workspace-open:

```text
workspace_authenticated_daemons
```

Any authenticated online daemon registered by any current member of the same workspace may receive, validate, install, and use a subscription credential, if it advertises the matching runtime capability. This default should be visible in UI and CLI output because it means the subscription can be used by the workspace daemon pool.

## Backend Examples

```text
openai-api
  backend_type=openai_compatible
  provider=openai
  transport=direct_http
  credential_kind=api_key

anthropic-api
  backend_type=anthropic
  provider=anthropic
  transport=direct_http
  credential_kind=api_key

claude-code-subscription
  backend_type=subscription_runtime
  provider=claude-code
  transport=daemon_dispatch
  credential_kind=subscription_bundle
  subscription_provider=claude_code
  dispatch_scope=workspace_authenticated_daemons

codex-subscription
  backend_type=subscription_runtime
  provider=codex
  transport=daemon_dispatch
  credential_kind=subscription_bundle
  subscription_provider=codex
  dispatch_scope=workspace_authenticated_daemons
```

## Configuration Flow

Users can configure subscription-backed backends from UI or CLI.

```text
1. User creates a Claude Code or Codex subscription backend.
2. Gateway stores the submitted subscription credential bundle encrypted.
3. Backend enters pending_runtime_validation.
4. Gateway looks for compatible online daemons in the same workspace.
5. If a compatible daemon exists, Gateway dispatches a validation/install job.
6. If no compatible daemon exists, backend remains pending and waits.
7. A compatible daemon claims the validation job.
8. Daemon installs the credential into an isolated provider home.
9. Daemon runs a real probe through Claude Code or Codex.
10. Daemon reports validation success or failure.
11. First successful validation activates the backend.
12. Runtime Gateway requests route only through compatible validated daemons.
```

Backend states:

```text
pending_runtime_validation
validating_on_runtime
active
degraded_no_runtime
invalid_credentials
disabled
```

## Request Flow

For direct API-key backends:

```text
App -> Gateway -> direct HTTP forwarder -> provider API -> Gateway -> App
```

For subscription-backed backends:

```text
App -> Gateway -> daemon dispatch request row -> daemon claim -> local Claude/Codex runtime -> daemon complete -> Gateway -> App
```

The daemon-dispatch flow should use the existing daemon polling style rather than server-to-daemon callbacks. User daemons may sit behind NAT, firewalls, or laptops with changing network addresses.

## Runtime Eligibility

A daemon is eligible to receive a credential or execute a request when all conditions hold:

```text
runtime.workspace_id == backend.workspace_id
runtime.status == online
runtime.owner_user_id is a current workspace member
runtime.provider matches subscription_provider
backend.transport == daemon_dispatch
backend.enabled == true
credential.enabled == true
```

For normal execution, prefer runtimes that have already validated the specific credential. First-time validation can be dispatched to any eligible runtime. Each credential dispatch must be audited.

## Credential Storage

Subscription credentials are encrypted at rest in Multica and treated as provider-specific opaque bundles. Gateway stores, audits, and dispatches them; provider-specific interpretation happens in the daemon/runtime adapter.

Suggested fields on credential rows:

```text
credential_type
subscription_provider
encrypted_payload
payload_format
dispatch_scope
validation_status
validated_runtime_id
last_validation_at
last_validation_error
account_hint
account_fingerprint
expires_at
refreshable
```

The credential is released to daemons only through explicit validation/install or execution job flows. It must not be injected into `BuildUpstreamRequest` or any direct HTTP upstream request.

## Daemon Jobs

Two daemon job types are needed:

```text
gateway_subscription_validation
gateway_runtime_request
```

Validation jobs install and test a credential. Runtime request jobs execute Gateway API requests through an already-validated subscription runtime.

Validation job payload:

```json
{
  "job_type": "gateway_subscription_validation",
  "backend_id": "uuid",
  "credential_id": "uuid",
  "subscription_provider": "codex",
  "requested_models": ["gpt-5.3-codex"],
  "probe_prompt": "Reply with exactly: multica-runtime-ok"
}
```

Runtime request payload:

```json
{
  "job_type": "gateway_runtime_request",
  "backend_id": "uuid",
  "credential_id": "uuid",
  "request_id": "uuid",
  "surface": "openai_chat_completions",
  "request_body": {},
  "stream": false
}
```

## Claude Code Runtime Adapter

Claude Code subscription support should use Dario as the reference implementation, adapted into the daemon runtime boundary.

Important Dario concepts to carry forward:

- OAuth/account import and refresh.
- Live Claude Code template capture on the daemon host.
- Request body/header/template replay for Claude Code subscription classification.
- OpenAI-chat to Anthropic-messages translation.
- Account/rate-limit headroom tracking.
- Auth-failure cooldown.
- Sticky conversation routing.
- Overage guard that halts routing after `representative-claim: overage`.

Multica should not port Dario's local HTTP proxy wholesale. Gateway owns routing, policy, observability, and coordination; daemon owns provider-specific subscription execution.

## Codex Runtime Adapter

Codex subscription support should start from Multica's existing Codex app-server backend in `server/pkg/agent/codex.go`. The first implementation should support a narrow OpenAI Chat Completions subset:

- non-streaming text-only `/v1/chat/completions`
- system/user/assistant messages flattened into a prompt
- one final assistant text response
- clear errors for unsupported tools, structured output, images, embeddings, realtime, and full Responses API behavior

Streaming, session persistence, and richer OpenAI API parity can follow after the daemon-dispatch spine is stable.

## Routing

Routing should be deterministic and explicit. Provider prefixes are preferred for subscription backends:

```text
codex:gpt-5.3-codex      -> Codex daemon subscription backend
claude-code:opus         -> Claude Code daemon subscription backend
openai:gpt-5.5           -> OpenAI direct API backend
anthropic:claude-opus... -> Anthropic direct API backend
```

Default model routing can be configured in Gateway, but `gpt-*` should not automatically imply Codex subscription. Most GPT models are OpenAI API models and callers expect direct API semantics unless a route or prefix says otherwise.

## Security And Audit

Required controls:

- Encrypt subscription bundles at rest.
- Never log credential payloads.
- Redact credential-like values from errors and telemetry.
- Audit every credential dispatch to a daemon.
- Audit every validation result.
- Revoke daemon eligibility when workspace membership is removed.
- Return 503 when no validated compatible daemon is online.
- Surface default workspace-open dispatch behavior in UI and CLI.

## Success Criteria

- Users can create OpenAI/Anthropic API-key backends and they continue to route directly.
- Users can create Claude Code/Codex subscription backends from UI or CLI.
- Subscription credential bundles are stored encrypted and marked `pending_runtime_validation`.
- Any compatible authenticated workspace daemon can claim validation by default.
- The first successful validation activates the subscription backend.
- Gateway routes subscription-backed model calls through validated daemon runtimes only.
- If no validated runtime is online, Gateway returns a provider-compatible 503.
- Codex v1 supports non-streaming text chat through the existing Codex daemon backend.
- Claude Code v1 design path incorporates Dario's overage guard and runtime-side template capture.
- Gateway telemetry records transport, runtime, daemon, provider, requested model, forwarded model, and validation/runtime errors.

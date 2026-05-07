# Gateway Acceptance Checklist

This checklist validates the hosted Multica Gateway flow before an enterprise rollout. It is designed to prove the control plane, provider-compatible proxy, observability capture, export, and audit paths without changing normal Claude, Codex, or provider endpoints.

## Automated Smoke Test

Run the backend smoke test against a migrated PostgreSQL database:

```bash
cd server
DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' \
  go test ./internal/handler -run TestGatewayAcceptanceSmokeRecordsTrafficAndExportsIt -count=1
```

The test uses local fake OpenAI-compatible and Anthropic-compatible upstreams. It verifies:

- `multica`-issued Gateway keys authenticate proxy traffic.
- OpenAI-compatible `/v1/chat/completions` requests route through the default backend.
- Anthropic-compatible `/v1/messages` streaming requests route through an explicitly selected backend.
- Streaming responses flush through the Gateway.
- Gateway telemetry creates observable sessions, requests, and LLM calls.
- `GET /api/gateway/overview` reports the recorded OpenAI-compatible request and token usage.
- `GET /api/gateway/sessions` reports the recorded Anthropic-compatible streaming request.
- `GET /api/gateway/export` includes the recorded sessions and LLM calls.
- `GET /api/gateway/audit` records the export read event.

## Manual Operator Flow

Install and authenticate the CLI:

```bash
npm install -g @ahmed-khaire/observer
multica login
multica gateway status
multica gateway doctor
multica gateway health-report
```

Configure enterprise-managed backends:

```bash
multica gateway add openai \
  --key=sk-proj-... \
  --base-url=https://api.openai.com/v1 \
  --set-default

multica gateway add openrouter \
  --key=sk-or-... \
  --base-url=https://openrouter.ai/api/v1

multica gateway add local \
  --key=anything \
  --base-url=http://127.0.0.1:11434/v1
```

Configure capture and credential pools:

```bash
multica gateway policy full_content
multica gateway backends
multica gateway credential add <backend-id> --label='primary enterprise key' --key=sk-... --priority=10
multica gateway credentials <backend-id>
```

Generate a user Gateway key:

```bash
multica gateway key
```

Use the generated values only in the agent or SDK that should route through Multica:

```bash
export OPENAI_BASE_URL='https://your-multica-host/v1'
export OPENAI_API_KEY='mgw_...'
export ANTHROPIC_BASE_URL='https://your-multica-host'
export ANTHROPIC_API_KEY='mgw_...'
```

Normal Claude, Codex, OpenAI, Anthropic, Groq, and OpenRouter endpoints are unaffected unless a user explicitly configures that tool to use the Multica Gateway base URL and key.

Run the operator smoke check:

```bash
multica gateway smoke --since=24h --limit=10
```

This command checks Gateway status, doctor health, generated user key availability, OpenAI-compatible model catalog reachability through `/v1/models`, and export reachability without printing raw Gateway keys.

Run the health report before and after backend changes:

```bash
multica gateway health-report
multica gateway health-report --output json
```

`health-report` is the operator-facing readiness view. It probes each enabled managed backend through a metadata-only model-list request, reports model count and latency, summarizes credential pool state, and shows governance signals such as capture policy, enabled policies, open incidents, pending approvals, provider risk warnings, evidence records, and control mappings. It never sends prompts or completions and never prints raw provider credentials.

## Manual Request Checks

OpenAI-compatible non-streaming:

```bash
curl "$OPENAI_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"gateway smoke"}]}'
```

OpenAI-compatible streaming:

```bash
curl -N "$OPENAI_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"stream smoke"}]}'
```

Anthropic-compatible streaming:

```bash
curl -N "$ANTHROPIC_BASE_URL/v1/messages" \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-3-5-sonnet-latest","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"stream smoke"}]}'
```

Explicit backend routing:

```bash
curl "$OPENAI_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "X-Multica-Backend: openrouter" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"explicit route smoke"}]}'
```

## UI Checks

In Settings -> Gateway:

- Confirm Gateway URLs and user key instructions are visible.
- Confirm Gateway Health shows backend probe status, credential pool state, capture policy, open incidents, pending approvals, and health checks.
- Confirm Managed Backends lists the configured backends.
- Open a backend credential pool and verify credentials show redacted hints only.
- Confirm capture policy is `full_content` unless the workspace requires a stricter policy.
- Run Export Controls for `24h` and confirm the export summary appears.
- Review Sessions, LLM Calls, and Session Drilldown after sending test traffic.

## Release Gate

Before announcing Gateway as ready for an enterprise pilot, verify:

- The automated smoke test passes against the target database version.
- `multica gateway doctor` is healthy or only reports understood warnings.
- `multica gateway health-report` shows passing backend probes or only understood warnings.
- Default backend is enabled.
- At least one active Gateway key exists for the test user.
- Streaming works for OpenAI-compatible and Anthropic-compatible clients.
- Gateway UI shows the new session and LLM call within the selected time window.
- Export creates an audit row with action `gateway.export.read`.
- No raw provider keys or raw generated Gateway keys are displayed after creation.

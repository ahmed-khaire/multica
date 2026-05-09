# Claude OAuth Gateway (Mode A — Server-Stored Tokens)

Date: 2026-05-09

Status: design approved, awaiting written spec review

## Goal

Make `multica gateway add claude-oauth` actually work end-to-end so users can authenticate the Multica Gateway against their Claude Code Pro/Max subscription via OAuth, not only with raw Anthropic API keys. After this spec ships, a user can pool one or more Claude OAuth credentials behind a single gateway backend and have agent traffic routed through them with automatic token refresh, pool selection, and cooldown handling.

This spec covers **Mode A** (server-stored OAuth tokens) only. Mode B (local sidecar — tokens stay on the user's machine, daemon proxies upstream) is intentionally deferred to a follow-up spec. The OAuth-acquisition code (PKCE flow, browser callback, manual mode, importer) is reusable for Mode B without changes; only the storage and request-time transport differ.

Codex (ChatGPT subscription) authentication is also out of scope. As of 2026-05-09 OpenAI does not expose a public OAuth flow comparable to Anthropic's, and Dario — our reference implementation — explicitly punts on this and accepts only OpenAI API keys for OpenAI-compatible backends. Multica matches Dario's posture for now; users who want subscription-backed Codex will need a separate research effort.

## Background

Multica's Gateway already has placeholders for `claude_oauth` as a backend type:

- `gateway_backend.backend_type` includes `'claude_oauth'` in its `CHECK` constraint (migration 036).
- `providerPresets` in `internal/gateway/management/types.go` lists `claude-oauth` with `BaseURL: "claude-oauth://sidecar"` and `RequiresCredential: false`.
- `cmd_gateway.go` accepts `claude-oauth` as a provider argument to `multica gateway add`.

But the implementation behind those placeholders does not exist. Three concrete gaps make `gateway add claude-oauth` non-functional today:

1. **No OAuth flow code.** Nothing in the codebase performs PKCE + browser launch + code exchange for Anthropic. The CLI cannot acquire OAuth tokens.
2. **Wrong auth header for OAuth.** `forwarder.go:106-150` switches on `UpstreamProtocol` only. `claude_oauth` resolves to `ProtocolAnthropic`, which sets `x-api-key: <secret>`. Claude OAuth tokens must be sent as `Authorization: Bearer <token>` — the current header would 401.
3. **No refresh metadata in schema.** `gateway_backend_credential` stores `encrypted_credential BYTEA` and `credential_hint TEXT` only. No `expires_at`, no `refresh_token`, no `credential_type`. An OAuth access token expires in roughly an hour and would become permanently invalid.

Dario (`/mnt/hdd/project/observer/dario`) is the reference implementation. Relevant Dario code:

- `src/oauth.ts` (709 lines) — single-account OAuth flow, refresh, OS keychain fallback.
- `src/accounts.ts` (425 lines) — multi-account pool, per-alias credential files.
- `src/cc-oauth-detect.ts` (428 lines) — scans the installed Claude Code binary to extract the current OAuth `client_id`.
- `src/cc-authorize-probe.ts`, `src/proxy.ts:1100-1530` — request-time provider routing and Bearer-token injection.

Dario's design is OAuth-only for Anthropic and API-key-only for OpenAI. Multica is mixed (OAuth + API keys for both providers in principle), so the credential model must discriminate at the row level.

## Selected Approach

**Mode A: server-stored OAuth tokens.** The CLI runs the OAuth acquisition flow on the user's machine, ships access + refresh tokens to the Multica server, and the server encrypts and stores them in `gateway_backend_credential`. At request time, the gateway resolves a credential, refreshes it if expiring, and injects `Authorization: Bearer <access_token>` on the upstream request.

This matches the existing `gateway add openai --key=sk-...` data-flow pattern: the CLI hands a credential to the server, the server is the source of truth thereafter. No new transport hops, no daemon dependency, no breaking of the existing proxy path. The trust model is the same as today: users who already trust Multica with their OpenAI API keys are trusting the same surface with their Claude OAuth tokens.

Mode B (local sidecar, tokens stay on user's machine) ships as a follow-up. Mode A is the strict subset that unblocks Claude Code subscription users in the near term.

## Architecture

```
┌────────────────────────┐         ┌──────────────────────────────────┐
│  multica CLI           │  POST   │  multica server                  │
│  (user's machine)      │ ──────> │  /api/gateway/backends/oauth     │
│                        │ tokens  │  /api/gateway/credentials/oauth  │
│  - browser OAuth flow  │         │                                  │
│  - manual mode         │         │  - encrypts & stores             │
│  - import from         │         │  - lazy refresh on demand        │
│    ~/.claude/...       │         │  - background refresh ticker     │
└────────────────────────┘         │  - pool selection at request     │
                                   │  - injects Authorization: Bearer │
                                   └────────────┬─────────────────────┘
                                                │
                                                ▼
                                   https://api.anthropic.com
                                   https://platform.claude.com/v1/oauth/token
```

The CLI never persists tokens locally. After a successful POST to the server, it discards the tokens from memory.

## Data Model

One additive migration: `041_gateway_backend_credentials_oauth.up.sql`.

```sql
ALTER TABLE gateway_backend_credential
    ADD COLUMN credential_type TEXT NOT NULL DEFAULT 'api_key'
        CHECK (credential_type IN ('api_key', 'oauth'));

ALTER TABLE gateway_backend_credential
    ADD COLUMN oauth_refresh_token BYTEA,
    ADD COLUMN oauth_expires_at    TIMESTAMPTZ,
    ADD COLUMN oauth_scope         TEXT NOT NULL DEFAULT '',
    ADD COLUMN oauth_account_uuid  TEXT NOT NULL DEFAULT '',
    ADD COLUMN oauth_last_refresh_at    TIMESTAMPTZ,
    ADD COLUMN oauth_last_refresh_error TEXT NOT NULL DEFAULT '';

CREATE INDEX gateway_backend_credential_oauth_expiry_idx
    ON gateway_backend_credential (oauth_expires_at)
    WHERE credential_type = 'oauth';

ALTER TABLE gateway_backend
    ADD COLUMN credential_selection_strategy TEXT NOT NULL DEFAULT 'priority'
        CHECK (credential_selection_strategy IN ('priority', 'headroom'));
```

Column-shape rationale:

- **`access_token` reuses the existing `encrypted_credential` BYTEA column.** No new encryption code path; same `secrets.Box.EncryptString` machinery as today's API keys.
- **`oauth_refresh_token` is its own encrypted column.** Refresh and proxy happen at different times; we don't decrypt both when only one is needed.
- **`oauth_expires_at` is plaintext and indexed.** The lazy refresh check and the background ticker both query "is this expiring soon?" without decrypting anything. Partial index on `WHERE credential_type = 'oauth'` keeps the index small.
- **`oauth_account_uuid`** lets the server detect "user re-added the same Claude account" and update in place rather than creating duplicates.
- **`credential_selection_strategy` lives on `gateway_backend`** (not credential): it is a per-backend policy.

The `claude-oauth` provider preset in `providerPresets` is updated:

```go
"claude-oauth": {
    BackendType:        BackendTypeClaudeOAuth,
    BaseURL:            "https://api.anthropic.com",  // was "claude-oauth://sidecar"
    DisplayName:        "Claude (OAuth)",
    RequiresCredential: false,                          // CLI handles cred acquisition itself
},
```

`RequiresCredential: false` stays because `gateway add claude-oauth` does not take `--key=...` — credentials are attached via the OAuth sub-flow.

When Mode B ships, it gets a separate backend type (`claude_oauth_sidecar` or similar) — different transport semantics warrant a different type rather than overloading.

Migration safety: all changes are additive. The new `CHECK` constraint is satisfied for existing rows by the `'api_key'` default. The partial index builds instantly because no rows match yet. Down migration drops the columns and index; any OAuth credentials added between up and down are lost (acceptable for a feature rollback, documented in the migration file).

## CLI: OAuth Acquisition Flow

### Command surface

```
multica gateway add claude-oauth                         # auto-import → fall back to browser OAuth
multica gateway add claude-oauth --label=work            # name this credential row (default: account email/uuid)
multica gateway add claude-oauth --selection-strategy=headroom
multica gateway add claude-oauth --manual                # headless: print URL, read pasted code from stdin
multica gateway add claude-oauth --from-claude-code      # import-only; fail if no local CC creds
multica gateway credential add <backend-id> --oauth      # add another OAuth credential to an existing backend
multica gateway credential add <backend-id> --oauth --label=personal --priority=200
```

### Acquisition state machine

```
                 multica gateway add claude-oauth
                              │
                              ▼
                   ┌──────────────────────┐
            ┌──────│ --from-claude-code?  │──────┐
            │ yes  └──────────────────────┘  no  │
            ▼                                    ▼
   ┌──────────────────┐               ┌──────────────────────┐
   │ import only      │               │ try import           │
   │ (file)           │               │ (file)               │
   └────────┬─────────┘               └─────────┬────────────┘
            │ fail                              │
            ▼                                   ▼
       exit error                  ┌────────────────────────┐
                                   │ found existing creds?  │
                                   └────┬───────────────┬───┘
                                  yes   │               │ no
                                        ▼               ▼
                            ┌──────────────────┐   ┌────────────────────┐
                            │ "use these or    │   │ --manual?          │
                            │  start fresh?"   │   └─┬──────────────┬───┘
                            └────┬─────────┬───┘  yes│              │ no
                            use  │         │ fresh   ▼              ▼
                                 │         └──┐ ┌──────────┐ ┌────────────────┐
                                 │            │ │ print URL│ │ open browser   │
                                 │            ▼ │ read code│ │ + callback srv │
                                 │   ┌────────────┴──┐    └─┴────────┬───────┘
                                 │   │ PKCE +        │               │
                                 │   │ token exchange│◄──────────────┘
                                 │   └───────┬───────┘
                                 │           │
                                 ▼           ▼
                            ┌──────────────────────────┐
                            │ POST tokens → server     │
                            │ /api/gateway/credentials │
                            └──────────────────────────┘
```

### Packages

`server/internal/cli/oauth/` — three files:

1. **`importer.go`** — locates existing Claude Code creds at `~/.claude/.credentials.json`. Returns `(tokens, source, err)` where `source` is `"file"` or `"none"`. Uses an `fs` interface so tests do not touch the real filesystem.

2. **`flow.go`** — runs the PKCE + browser + callback flow. Generates a 32-byte verifier → SHA-256 challenge. Spins up `http.Server` on `localhost:0` (random port). Builds the auth URL: `https://claude.ai/oauth/authorize?client_id=...&response_type=code&redirect_uri=http://localhost:<port>/callback&scope=...&code_challenge=<sha256>&code_challenge_method=S256&state=<random>`. Opens the browser via the existing helper that `multica login` uses. Waits for callback with a 5-minute timeout. POSTs to `https://platform.claude.com/v1/oauth/token` to exchange code for tokens. Manual mode skips the callback server: prints the URL and reads the pasted code (or full redirect URL) from stdin.

3. **`clientid.go`** — detects the OAuth `client_id`. Hybrid approach: hardcoded current value (`9d1c250a-e61b-44d9-88ed-5944d1962f5e`, matching Dario's known good value), with optional Claude Code binary scanning for self-healing. On first run, attempts to locate the `claude` binary on PATH and parse it for the `BASE_API_URL:"https://api.anthropic.com"` anchor (mirrors Dario's `cc-oauth-detect.ts:383-423`). Caches detection results by binary hash at `~/.multica/oauth-clientid-cache.json`. Fallback chain on every run: detected → hardcoded → env override (`MULTICA_CLAUDE_OAUTH_CLIENT_ID`).

### Server-side endpoints

Two new endpoints:

```
POST /api/gateway/backends/oauth
  Body: {
    workspace_id, slug, display_name, set_default,
    selection_strategy,
    credential: {
      access_token, refresh_token, expires_at, scope, account_uuid, label, priority
    }
  }
  → creates backend with backend_type='claude_oauth' AND inserts the first credential row.

POST /api/gateway/credentials
  Body: {
    backend_id, credential_type: "oauth",
    access_token, refresh_token, expires_at, scope, account_uuid, label, priority
  }
  → inserts a new credential row on an existing backend.
```

Tokens travel over the existing authenticated multica HTTPS channel. On the wire they are plaintext-over-TLS; encryption happens server-side before storage, identical to today's API keys.

### Dedup behavior

When a user runs `gateway add claude-oauth` twice with the same Claude account, the server detects via `oauth_account_uuid`:

- Existing row on the same backend → update in place (refreshed token, new expiry, possibly new label/priority).
- Existing row on a different backend → return 409 Conflict ("this Claude account is already linked to backend `<other-slug>`. Pass `--force` to move it.").

### Known gap (deferred to v2)

OS keychain import. Newer Claude Code (v2+) stores tokens in the OS keychain rather than `~/.claude/.credentials.json`. Adding keychain support means CGO bindings or shelling out to `security` (macOS), `secret-tool` (Linux), and PowerShell `CredEnumerate` (Windows). For v1, users on CC v2+ fall through to the browser OAuth flow, which still works correctly. Keychain support is tracked as a follow-up.

## Server: Refresh and Request-Time Injection

### New package

`server/internal/gateway/oauth/` — three files:

1. **`refresher.go`** — refresh logic with per-credential dedup via `golang.org/x/sync/singleflight`.
2. **`ticker.go`** — background goroutine for proactive warmth, guarded by Postgres advisory lock.
3. **`token_client.go`** — thin HTTP client for `platform.claude.com/v1/oauth/token`.

### Lazy refresh

```go
type Refresher struct {
    db        *sql.DB
    box       *secrets.Box           // for encrypt/decrypt of refresh tokens
    client    *TokenClient           // POSTs to platform.claude.com
    inflight  *singleflight.Group    // per-credential dedup
    bufferDur time.Duration          // default 30 min
    cooldown  time.Duration          // default 60s after refresh failure
}

// EnsureFresh returns a valid access_token, refreshing if needed.
// Safe to call concurrently for the same credential — refreshes dedupe.
func (r *Refresher) EnsureFresh(ctx context.Context, cred *Credential) (string, error) {
    // 1. If access_token expires > now + bufferDur: return cached, no work.
    // 2. If last refresh failed < cooldown ago: return cached even if stale (let upstream 401 us).
    // 3. Otherwise: singleflight.Do(cred.ID, doRefresh) so concurrent callers share one HTTP roundtrip.
}

func (r *Refresher) doRefresh(ctx context.Context, cred *Credential) (string, error) {
    // POST grant_type=refresh_token to platform.claude.com/v1/oauth/token
    // On success: SELECT FOR UPDATE the row, write new tokens + expires_at, COMMIT.
    // On failure: write oauth_last_refresh_error + oauth_last_refresh_at; surface error.
}
```

Per-credential keying with `singleflight.Group` deduplicates concurrent refresh attempts for the same credential without hand-rolled mutexes.

### Background ticker

```go
type Ticker struct {
    db        *sql.DB
    refresher *Refresher
    interval  time.Duration  // default 5 min
    horizon   time.Duration  // default 35 min — refresh tokens expiring within this window
}

func (t *Ticker) refreshExpiring(ctx context.Context) {
    // Postgres advisory lock to prevent multi-replica races:
    //   SELECT pg_try_advisory_lock(hashtext('multica.oauth.ticker'))
    // If acquired: SELECT credentials WHERE oauth_expires_at < now() + horizon, refresh each.
    // Always: SELECT pg_advisory_unlock(...) on the way out.
}
```

The horizon (35 min) is intentionally larger than the lazy-refresh buffer (30 min). The ticker proactively refreshes before the lazy path would fire. Lazy refresh remains the safety net for cases where the ticker missed (server just started, credential just added, replica without ticker leadership).

`pg_try_advisory_lock` on a constant key ensures only one replica's ticker runs at a time. If the holding replica dies, Postgres releases the lock automatically when its connection drops — no leader election framework required.

### Auth header fix

Current code at `forwarder.go:106-150`:

```go
switch upstreamProtocol {
case ProtocolAnthropic:
    req.Header.Set("x-api-key", target.UpstreamSecret)
default:
    req.Header.Set("Authorization", "Bearer "+target.UpstreamSecret)
}
```

Replace with credential-type-aware injection:

```go
switch target.CredentialType {
case CredentialTypeOAuth:
    req.Header.Set("Authorization", "Bearer "+target.UpstreamSecret)
    if upstreamProtocol == ProtocolAnthropic {
        req.Header.Set("anthropic-beta", "oauth-2025-04-20")
        if req.Header.Get("anthropic-version") == "" {
            req.Header.Set("anthropic-version", defaultAnthropicVersion)
        }
    }
case CredentialTypeAPIKey:
    switch upstreamProtocol {
    case ProtocolAnthropic:
        req.Header.Set("x-api-key", target.UpstreamSecret)
        if req.Header.Get("anthropic-version") == "" {
            req.Header.Set("anthropic-version", defaultAnthropicVersion)
        }
    default:
        req.Header.Set("Authorization", "Bearer "+target.UpstreamSecret)
    }
}
```

The `anthropic-beta: oauth-2025-04-20` header is required by Anthropic's API to accept OAuth-issued bearer tokens. The exact beta string must be verified against current Anthropic documentation at implementation time — Anthropic rotates dated beta flags.

### Resolver integration

`service.go` resolver gets a `*Refresher` injected. When it sees an OAuth credential row:

```go
secret, err := refresher.EnsureFresh(ctx, cred)
if err != nil {
    // Mark credential with last_error_at; pool selector skips it for cooldown window.
    continue  // try next credential per selection strategy
}
target.UpstreamSecret = secret
target.CredentialType = CredentialTypeOAuth
return target, nil
```

API-key credentials bypass `EnsureFresh` entirely — no extra latency for the existing path.

## Pool Selection

Two strategies, configurable per-backend via `gateway_backend.credential_selection_strategy`. Default: `priority`.

The selector returns an **iterator**, not a single pick. If the chosen credential's refresh fails or the upstream returns 429, the resolver falls through to the next without re-running selection logic.

```
resolver.Resolve(ctx, backend, request) {
    creds := loadAllCredentials(backend.ID)
    creds = filterEnabled(creds)
    creds = filterNotInCooldown(creds)

    selector := newSelector(backend.CredentialSelectionStrategy)
    for cred := range selector.Iterate(creds) {
        secret, err := refresher.EnsureFresh(ctx, cred)
        if err == nil {
            return target{secret, cred.CredentialType, ...}, nil
        }
        markRefreshFailure(cred)
    }
    return zero, ErrNoHealthyCredential
}
```

### Strategy `priority`

`server/internal/gateway/proxy/pool/priority.go`:

- Group credentials by `priority` value, ascending (lower = tried first).
- Round-robin within each group via an in-memory `sync.Map[backendID][priority] -> atomic.Uint32` counter modulo group size.
- Counter resets implicitly on process restart — acceptable; the goal is "spread load," not "perfect even distribution across all time."
- Falls through to the next priority group if all credentials in the current group failed for this request.

### Strategy `headroom`

`server/internal/gateway/proxy/pool/headroom.go`:

- For each credential, compute remaining tokens in the current rate-limit window from `gateway_backend_credential_rate_limits` (migration 040):
  ```
  if window_start_at + window_duration < now() { headroom = tokens_limit }
  else                                          { headroom = tokens_limit - tokens_consumed }
  ```
- Sort descending by headroom. Tiebreaker: lower priority value, then older `last_used_at`.
- If rate-limit data is missing for all credentials, degenerates to priority order. Forward-compatible: as observability lands, headroom selection becomes more useful automatically.

For headroom selection to be *useful* (not merely valid), the gateway must capture rate-limit headers from Anthropic responses (`anthropic-ratelimit-tokens-remaining`, `anthropic-ratelimit-requests-remaining`) and write them to `gateway_backend_credential_rate_limits`. Approximately 50 LOC of additions in `forwarder.go`'s response-handling are bundled into this spec's scope so headroom is meaningful at ship time.

### Cooldown integration

Both strategies skip credentials in cooldown. Cooldown triggers:

- `oauth_last_refresh_error != ''` AND `oauth_last_refresh_at > now() - 60s` → recent refresh failure.
- `last_error_at > now() - 30s` AND `last_error LIKE '%429%'` → recent upstream rate limit.
- `last_error_at > now() - 5min` AND `last_error LIKE '%401%'` → recent auth rejection (token revoked, etc.); longer cooldown to avoid spam.

The existing `last_error_at` and `last_error` columns on `gateway_backend_credential` (migration 039) already support this — no schema change needed for cooldown logic.

### Configuration surface

```bash
multica gateway add claude-oauth --selection-strategy=headroom
multica gateway backend update <backend-id> --selection-strategy=priority
multica gateway backend get <backend-id>
# → shows: selection_strategy: headroom
#          credentials: 3 (2 healthy, 1 in cooldown)
```

### Out of scope for v1

- Adaptive strategy switching ("auto-fall-back to priority if rate-limit data is missing"). Strategy is what the user configured.
- Per-credential weighted round-robin ("send 70% to credential A, 30% to B").
- Cross-backend pool (fall back from `claude_oauth` to `anthropic` API-key backend if all OAuth creds exhausted) — would need a workspace-level routing layer.

## Error Handling and Failure Taxonomy

| Class | Trigger | User message | System action |
|---|---|---|---|
| **Refresh expired** | `invalid_grant` from `platform.claude.com` | "Claude account `<label>` was signed out or revoked. Re-add with `multica gateway credential add <backend-id> --oauth`." | Set `oauth_last_refresh_error`; pool skips this credential indefinitely until user re-auths or removes. |
| **Refresh transient** | 5xx, network timeout, DNS fail | (no user message — silently retried) | 60s cooldown, lazy-refresh on next request. |
| **Upstream 401** | Anthropic rejects valid-looking token | "Token rejected by Anthropic. The credential will be re-validated on next request." | Force a refresh on next call (clear `oauth_expires_at` to past); if that also fails, treat as `invalid_grant`. |
| **Upstream 429** | Rate limit | (no message — proxy returns 429 to caller normally) | 30s cooldown on this credential; pool falls through to next. |
| **All credentials in cooldown** | Pool exhausted | Returned to caller as `503 — All credentials for backend '<slug>' are in cooldown. See \`multica gateway doctor\`.` | `gateway doctor` shows full breakdown. |
| **`anthropic-beta` rejected** | Anthropic changed the beta flag | (no user message — logged) | Server logs ERROR with the response body so an operator can update the constant. Falls through to next credential. |

### Edge cases

| Edge case | Handling |
|---|---|
| User clicks "deny" on Claude OAuth screen | Callback receives `?error=access_denied`; CLI exits with friendly message. |
| User closes browser before completing | Callback server times out at 5 min; CLI exits with "OAuth flow timed out." |
| Two `multica gateway add claude-oauth` running concurrently on same machine | Both spin up callback servers on different random ports — no collision. |
| Manual mode: user pastes the full URL instead of just the code | Parser accepts both — looks for `code=` param if it sees `http`. |
| Same Claude account across two workspaces | Allowed. Different workspace → different `gateway_backend_credential` row. Token refresh is per-row; small jitter added to reduce thundering-herd against `platform.claude.com`. |
| Server clock skew vs Anthropic's `expires_in` | Use `expires_at = now + expires_in - 60s` (subtract 1 min as safety margin). |
| Encryption key (`MULTICA_GATEWAY_SECRET_KEY`) rotates | Existing `loadBox()` machinery handles this for `encrypted_credential`; `oauth_refresh_token` goes through the same path. Out-of-band re-encryption job already exists per existing pattern. |
| Anthropic returns no new `refresh_token` in refresh response | Keep existing refresh token (RFC 6749 behavior). |
| Anthropic rotates `client_id` | Hybrid client_id detection self-heals on next run; env-var override available immediately as fallback. |

### Removing / disabling credentials

```bash
multica gateway credential disable <credential-id>   # soft: enabled=false
multica gateway credential remove <credential-id>    # hard delete
```

Disable is the safe operation — the credential row stays for audit; pool skips it. Remove is destructive but lets users prune dead credentials. On remove, the encrypted access_token + refresh_token are zeroed before the row is deleted (overwrite then DELETE). Server **does not** call Anthropic to revoke the OAuth grant — that is the user's choice (they may want to keep the grant alive for use in Claude Code itself). Documented in the CLI help.

## Observability

### `multica gateway doctor` extension

```
$ multica gateway doctor

Gateway backends (workspace: acme-corp):
  ✓ openai-prod (openai_compatible, 2 credentials, all healthy)
  ⚠ claude-team (claude_oauth, 3 credentials)
      ✓ work          access valid 23m, refresh in 7m, used 142 times
      ✗ personal      INVALID_GRANT — re-auth needed
      ⚠ shared-acct   401 in last 5m — auto-recovery on next request
  ✓ groq-fallback (openai_compatible, 1 credential, healthy)

Background refresh ticker:
  ✓ Last successful run 2m ago. 1 credential refreshed proactively.
  ✓ Advisory lock held by replica `multica-server-7d8f`.

Action items:
  • Re-authenticate "personal" account:
      multica gateway credential add <backend-id> --oauth --label=personal
```

`gateway health-report --output json` (existing per `CLI_AND_DAEMON.md:186`) gets parallel additions for OAuth credential health, ticker status, and refresh-error counts in the last 24h.

### Structured logs

- `oauth.refresh.attempt`: credential_id, account_uuid, reason (lazy|ticker)
- `oauth.refresh.success`: credential_id, latency_ms, new_expires_at
- `oauth.refresh.failure`: credential_id, error_class (invalid_grant|timeout|5xx), retry_after
- `oauth.pool.skip`: credential_id, reason (cooldown|disabled|invalid)
- `oauth.pool.exhausted`: backend_id, credentials_total, credentials_in_cooldown

### Metrics

Whatever multica uses today (likely Prometheus, given `gateway/observability/`):

- `multica_oauth_refresh_total{result}` counter
- `multica_oauth_refresh_duration_seconds` histogram
- `multica_oauth_credentials_active{backend_id, status}` gauge — emit on a 30s interval

### Sensitive-data hygiene

Never log:

- access_token, refresh_token, code_verifier
- The OAuth callback `?code=...` query parameter
- Anthropic response body if it contains a token (only the parsed `error` field, if any)

The existing `gateway/observability/filter.go` already does redaction; OAuth-specific redaction patterns are added to it.

## Testing Strategy

### Test pyramid

```
                ┌──────────────────────────┐
                │ E2E (Go, 1 test)         │
                │ multica gateway add      │
                │ claude-oauth --manual    │
                │ → fake-anthropic OAuth   │
                └──────────────────────────┘
            ┌──────────────────────────────────┐
            │ Integration (Go, ~10 tests)      │
            │ Real Postgres, fake Anthropic    │
            │ Resolver + Refresher + DB        │
            └──────────────────────────────────┘
        ┌────────────────────────────────────────────┐
        │ Unit tests (Go, ~40 tests)                 │
        │ Pool selectors, token client, importer,    │
        │ flow state machine, header injection       │
        └────────────────────────────────────────────┘
```

### Unit tests

**`oauth/refresher_test.go`** — refresh logic in isolation, no DB:
- `EnsureFresh` returns cached token when not expiring → no HTTP call.
- `EnsureFresh` triggers refresh when within buffer window → one HTTP call.
- 100 concurrent `EnsureFresh` calls for same credential → exactly one HTTP call (singleflight verified).
- Refresh failure marks `oauth_last_refresh_error` and respects 60s cooldown.
- Refresh response with no new `refresh_token` → keeps existing one.
- `invalid_grant` response → marks credential dead, no retry.

**`oauth/pool/priority_test.go`** and **`headroom_test.go`**:
- Single credential → always picked.
- Three credentials at same priority → counter advances 1→2→3→1.
- Mixed priorities → lower priority value wins.
- Failed credential → iterator yields next.
- All in cooldown → iterator empty → `ErrNoHealthyCredential`.
- Headroom: credentials sorted descending by remaining tokens.
- Headroom: missing rate-limit data → degenerates to priority order.

**`cli/oauth/importer_test.go`**:
- Valid `~/.claude/.credentials.json` → returns parsed tokens.
- Missing file → `(nil, "none", nil)` — not an error.
- Malformed JSON → returns wrapped parse error.
- Tests use an `fs` interface; no real filesystem access.

**`cli/oauth/flow_test.go`**:
- PKCE verifier → SHA-256(verifier) → base64url(challenge) round-trip matches RFC 7636.
- Callback handler receives `?code=...&state=...` → state validated, code returned.
- Callback handler receives `?state=wrong` → rejected.
- Callback handler receives `?error=access_denied` → friendly user message.
- Manual mode: stdin scanner accepts raw code, full URL, and URL with extra params.
- 5-minute timeout fires when no callback arrives.

**`forwarder_test.go` additions**:
- Credential type OAuth + protocol Anthropic → `Authorization: Bearer` + `anthropic-beta: oauth-2025-04-20`.
- Credential type OAuth + protocol OpenAI → `Authorization: Bearer`, no anthropic headers.
- Credential type API key + protocol Anthropic → `x-api-key` (existing behavior unchanged).
- Existing API-key tests must continue to pass — regression guard.

### Integration tests

`server/internal/gateway/oauth/integration_test.go`:

Uses the existing test-database pattern. `httptest.Server` stands in for `platform.claude.com/v1/oauth/token`.

- Full lazy-refresh round trip: insert OAuth credential expiring in 1s → request triggers refresh → DB row updated atomically.
- Concurrent requests across 5 goroutines → singleflight produces one upstream call, all 5 see the new token, DB row written exactly once.
- Background ticker: insert credential expiring in 10s, ticker with horizon=20s → ticker refreshes it before lazy path would.
- Advisory lock: spin up two ticker goroutines simultaneously → one acquires, the other no-ops, DB row touched once.
- Cooldown: refresh fails → next request within 60s skips refresh → request after 60s retries.
- Pool fallthrough: backend has 3 credentials, first dead, second rate-limited, third healthy → resolver returns the third.

### E2E test

`server/cmd/multica/cmd_gateway_oauth_e2e_test.go`:

- Spin up multica server bound to test DB.
- Spin up `httptest.Server` for fake Anthropic OAuth + API endpoints.
- Set `MULTICA_CLAUDE_OAUTH_AUTHORIZE_URL` and `MULTICA_CLAUDE_OAUTH_TOKEN_URL` env vars to point at the fake.
- Run `multica gateway add claude-oauth --manual` in a subprocess; pipe a fake OAuth code to its stdin.
- Verify: backend row created, credential row created, `gateway doctor` reports healthy.
- Send a request through the gateway → fake Anthropic verifies it received `Authorization: Bearer <expected_token>`.

### Excluded from automated tests

- Real Claude OAuth — no tests hit `claude.ai/oauth/authorize` or `platform.claude.com`. All tests use `httptest.Server` returning canned responses matching documented OAuth 2.0 + PKCE behavior.
- Real browser launch — `cli/oauth/flow.go` accepts an `opener func(url string) error` dependency. Production passes the real opener; tests pass a fake that records the URL.
- OS keychain integration — out of scope for v1.

### Test data fixtures

`server/internal/gateway/oauth/testdata/`:

- `credentials_file.json` — sample `~/.claude/.credentials.json` payload.
- `oauth_token_response.json` — canonical successful refresh response.
- `oauth_invalid_grant.json` — `invalid_grant` error response.
- `oauth_rate_limit.json` — Anthropic-style 429 with `Retry-After`.

### Verification gate

Before merge, `make check` (existing pre-push gate per `CLAUDE.md`) must pass. New code adds approximately 600 LOC of test code for approximately 1500 LOC of feature code (~40% test ratio, in line with the rest of the gateway package).

## CLI Release Coordination

The new commands require both server and CLI to be on the same version. The server is backward-compatible (old CLI keeps working with API keys), but the new `--oauth` flag on `credential add` will return `400 unknown_flag` if hitting an old server. The CLI pre-flight checks the server version (extending the existing `multica version` mechanism, or adding a `/api/_meta` endpoint) and produces a clear "server X.Y is too old; needs ≥X.Z" message rather than letting the request fail mysteriously.

## Out of Scope

The following are explicitly deferred:

- **Mode B (local sidecar).** Tokens stay on the user's machine; daemon proxies upstream. Separate spec.
- **Codex / ChatGPT subscription auth.** OpenAI does not expose a public OAuth flow comparable to Anthropic's. Users keep providing OpenAI API keys via `multica gateway add openai --key=sk-...`.
- **OS keychain import.** v2 follow-up (Section: CLI / Known gap).
- **Adaptive selection-strategy fallback.** Magic, hard to debug.
- **Per-credential weighted round-robin.** Defer until users request it.
- **Cross-backend pool fallback.** Workspace-level routing layer; would touch resolver in non-trivial ways.

## Open Questions

None blocking implementation. The `anthropic-beta` value (`oauth-2025-04-20`) must be re-verified against current Anthropic documentation at implementation time and made a constant in `forwarder.go` so future rotations are a one-line change.

## References

- Dario implementation: `/mnt/hdd/project/observer/dario/src/oauth.ts`, `accounts.ts`, `cc-oauth-detect.ts`.
- Existing Multica gateway design: `docs/superpowers/specs/2026-05-03-multica-gateway-design.md`.
- Migration referenced: `server/migrations/036_gateway_foundation.up.sql`, `039_gateway_backend_credentials.up.sql`, `040_gateway_backend_credential_rate_limits.up.sql`.
- Existing gateway forwarder: `server/internal/gateway/proxy/forwarder.go:106-150`.
- Existing CLI gateway commands: `server/cmd/multica/cmd_gateway.go`.

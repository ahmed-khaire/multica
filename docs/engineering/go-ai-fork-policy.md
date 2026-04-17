# go-ai Fork Policy

## What this is

`github.com/digitallysavvy/go-ai` is the foundation of our agent execution layer (Phase 2 onward). It is a single-maintainer, Apache-2.0-licensed library in active development.

To protect ourselves against upstream disappearance, emergency-fix lag, and Multica-specific divergence needs, we maintain a **soft fork** that mirrors upstream exactly by default and only activates as an override when we need it.

## Fork topology

| Remote | URL | Role |
|--------|-----|------|
| `origin` | `https://github.com/ahmed-khaire/go-ai` | Our fork. `main` branch is an exact mirror of upstream `main`. |
| `upstream` | `https://github.com/digitallysavvy/go-ai` | Public source of truth. |

**Known risk — owner:** the fork currently lives on a personal GitHub account (`ahmed-khaire`). If/when Multica adopts an engineering org on GitHub, move the fork to `multica-ai/go-ai` and update every `replace` reference. Tracking as an open action item; does not block Phase 1.

**Branches on the fork:**
- `main` — **exact byte-for-byte mirror of `upstream/main`**. Never commit here directly — including fork infrastructure files. The ancestor check in the mirror workflow flags ANY commit on `main` that upstream doesn't have as divergence. Weekly automation fast-forwards from upstream.
- `multica-infra` — **default branch**. Contains our fork-local infrastructure: the mirror workflow, any fork-specific CI, this policy doc (optionally). Forever a small number of commits ahead of `main` by exactly those infra files. Scheduled GitHub Actions run from this branch.
- `multica/<topic>` — named branches created on demand off `main` when we need to diverge for a patch (see Activation Procedure). Tagged with `v<upstream-version>-multica.N` for the `replace` directive.

**Why two branches?** Scheduled GitHub Actions run from the repo default branch. If the workflow lived on `main`, `main` would be one commit ahead of upstream the moment it was committed, and the mirror check would (correctly) reject every sync attempt as "divergence." Keeping the workflow on `multica-infra` and the mirror on `main` resolves this cleanly.

## Default state

Our `server/go.mod` references **upstream** directly:

```go
require github.com/digitallysavvy/go-ai v0.4.0
```

No `replace` directive. Go fetches the module from `digitallysavvy/go-ai` via the module proxy like any other public dependency. The fork is dormant.

## Weekly upstream mirror automation

A GitHub Action at `.github/workflows/mirror-upstream.yml` on the **`multica-infra` branch** fast-forwards `origin/main` from `upstream/main` on a weekly schedule and on manual dispatch. The job:

1. Checks out `main` (the mirror) with full history.
2. Fetches upstream.
3. Verifies `origin/main` is an ancestor of `upstream/main` (fast-forward-safe). If not, **fails loudly** — upstream has force-pushed or rewritten, triage per this policy.
4. `git merge --ff-only upstream/main` and `git push origin main`.

**On any failure of that workflow**, treat it as a signal that either (a) upstream did something unusual (force-push, rewrite), or (b) `main` accidentally received a fork-local commit. Investigate before accepting any sync.

**Do not move the workflow file to `main`.** It must stay on `multica-infra`. See "Why two branches?" above.

### Tags are not mirrored

The mirror workflow fetches commits only (`git fetch --no-tags`) and never pushes tags. Rationale:

- Some upstream tags reference commits that modify `.github/workflows/*.yml`. The default `GITHUB_TOKEN` (a GitHub App token) is **unconditionally forbidden** from pushing refs that touch workflow files — this is a GitHub App security invariant that no `permissions:` block can override.
- The alternative — a PAT with `workflow` scope — introduces a long-lived credential that needs rotation and monitoring. Not worth it for a convenience feature.
- We don't actually need upstream tags on the fork: the `replace` directive tags live in the `vX.Y.Z-multica.N` namespace, and Go module consumers fetch upstream releases from the module proxy, not from our fork.

If a specific upstream tag ever needs to exist on the fork (rare), push it manually from a user-credentialed checkout: `git push origin vX.Y.Z`.

## Activation triggers

Activate the `replace` directive only when one of these is true:

1. **Maintainer unresponsive** — our PR sits >30 days with no reply and we need the fix.
2. **Multica-only feature** — we need behavior that wouldn't be accepted upstream (e.g., a Multica-specific callback shape).
3. **Emergency fix** — security issue or production-blocking bug, need to ship before next upstream release.
4. **Pinned security patch** — we want to cherry-pick a specific fix onto our older version without upgrading to a new major.
5. **Deprecation shield** — upstream removes an API we depend on; we maintain it locally while we migrate.

For cases 1, 3, 5: the fix also goes upstream as a PR, and is cherry-picked to our fork. We lift the `replace` once upstream ships.

For case 2: we keep it in the fork permanently, merge from upstream regularly, resolve conflicts as needed.

For case 4: time-limited branch, deleted when we upgrade.

## Activation procedure

When an activation trigger is met:

1. **Open a branch on the fork** named `multica/<short-reason>` (e.g. `multica/fix-anthropic-timeout`).
2. **Make the change** — keep commits small and focused. Preserve the original upstream history; do not force-push or rebase upstream commits.
3. **Tag the fork** with a semver-compatible extension: `v0.4.0-multica.1`, `v0.4.0-multica.2`, etc. The base (`v0.4.0`) must match whatever upstream version we're patching.
4. **Update `server/go.mod`** in `aicolab` to add the `replace`:
    ```go
    require github.com/digitallysavvy/go-ai v0.4.0

    replace github.com/digitallysavvy/go-ai => github.com/ahmed-khaire/go-ai v0.4.0-multica.1
    ```
5. **Run `go mod tidy`** to update `go.sum`, commit both files together.
6. **Open an upstream PR** for the change (for triggers 1, 3, 5). Link the PR in the commit message that added the `replace`.
7. **Track the activation** in `docs/engineering/go-ai-fork-activations.md` (one-line entry per activation with date, reason, upstream PR link if any).

## Deactivation procedure

When the reason for activation is resolved (upstream shipped the fix, or we upgraded past the pinned version):

1. **Upgrade the require line** to the new upstream version that contains the fix.
2. **Remove the `replace` directive** from `server/go.mod`.
3. **Run `go mod tidy`**.
4. **Delete the tag** from the fork (optional — tags are cheap, keep them for audit).
5. **Close out the entry** in `go-ai-fork-activations.md` with the deactivation date.

## What stays at Multica's adapter layer, not the fork

Do not patch the fork for things we can do via go-ai's extension points. In particular, **these do NOT require a fork**:

- Cost events per LLM call → `AgentConfig.OnFinishEvent` callback
- Credential rotation on rate_limit → wrap `provider.LanguageModel`
- Custom tools (memory, delegation, skills) → standard `types.Tool` registration
1- Error classification (Phase 1.3) → wrap at our adapter layer
- Tracing → go-ai has first-class OTel integration
- Model selection per workspace/budget → pass different `LanguageModel` per call

All Multica-specific behavior lives in `server/pkg/agent/harness/` (Phase 2). The fork is only for things we cannot express through the library's public API.

## License

Upstream is Apache-2.0. Our fork inherits that license. Any patches we commit to the fork are published under the same license. `NOTICE` files must be preserved.

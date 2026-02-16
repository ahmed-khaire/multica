# Web Tools Policy Optimization Roadmap

Related Linear issue: [MUL-267](https://linear.app/indexlabs/issue/MUL-267/refactor-web-evidence-guard-to-hybrid-policy-and-configurable-rule)

## Context

The current web evidence guard solved the immediate quality issue:
- It enforces `web_search` -> `web_fetch` evidence coverage in runtime.
- It blocks snippet-only finalization in key web-dependent cases.

However, semantic intent detection currently relies on hard-coded regex cue groups in `packages/core/src/agent/web-tools-policy.ts`. This is deterministic but not ideal for long-term maintainability and multilingual robustness.

## Problem Statement

Current limitations:
- Semantic classification logic is tightly coupled with runtime enforcement code.
- Pattern lists are code-level constants, making iteration high-friction.
- Coverage expansion risks overfitting and regression without a stronger benchmark loop.

## Target Architecture

Use a hybrid policy model:
1. Deterministic guardrail layer (must keep)
- Tool-trace based invariants (e.g. search/fetch sequencing, minimum successful fetch count).

2. Semantic decision layer (new)
- Lightweight model/classifier returns decision + confidence + reason codes.

3. Rulepack fallback layer (refactor existing patterns)
- Externalized locale-aware cue packs for conservative fallback only.

## Migration Plan

Phase 1: Decouple configuration
- Move regex cue groups out of `web-tools-policy.ts` into a policy registry.
- Keep behavior equivalent.

Phase 2: Add semantic classifier path
- Add an optional semantic decision step with confidence threshold.
- Preserve deterministic tool-trace constraints as final authority.

Phase 3: Observability and tuning
- Emit run-log fields for policy decision source:
  - `tool-trace`
  - `semantic`
  - `fallback-pattern`
- Add benchmark slices focused on false-positive/false-negative policy triggers.

Phase 4: Reduce hard-coded fallback
- Keep only minimal safety patterns in code.
- Shift language/phrase evolution to versioned config updates.

## Acceptance Criteria

- No large hard-coded regex arrays in runtime policy file.
- Semantic decision path is independently testable and feature-flagged.
- Baseline behavior remains backward-compatible for existing guard cases.
- Benchmark report shows equal or lower policy misfire rate.

## Non-goals

- Replacing deterministic tool-trace enforcement with pure model decisions.
- Expanding scope to unrelated tool policy domains in the same iteration.

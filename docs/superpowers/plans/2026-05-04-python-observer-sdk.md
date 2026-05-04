# Python Observer SDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a lightweight Python SDK that enterprise Python apps and agents can use to create trace context, inject Multica Gateway correlation headers, and submit spans/logs/tools to `/v1/traces`.

**Architecture:** Create a self-contained Python project under `sdks/python` using package import name `multica_observer`. The first version uses only the Python standard library for runtime behavior, with injectable ID/time/transport functions for deterministic tests. It mirrors the TypeScript SDK API shape without adding framework auto-instrumentation, queues, retries, or OTLP export.

**Tech Stack:** Python 3.10+, pytest, stdlib `urllib.request`, dataclasses, type hints.

---

## File Structure

- Create `sdks/python/pyproject.toml`: package metadata and pytest config.
- Create `sdks/python/src/multica_observer/__init__.py`: public exports.
- Create `sdks/python/src/multica_observer/types.py`: typed payload aliases and constants.
- Create `sdks/python/src/multica_observer/context.py`: trace context and header names.
- Create `sdks/python/src/multica_observer/client.py`: `ObserverClient`, `ObserverTrace`, flush transport.
- Create `sdks/python/tests/test_client.py`: behavior tests.

## Scope

Milestone SDK behavior:

- Create `trace_id`, `session_id`, and `root_span_id`.
- Inject the same `X-Multica-*` headers as the TypeScript SDK.
- Build JSON payloads accepted by the Go `/v1/traces` endpoint.
- Record spans, events, logs, agent observations, and tool observations.
- Submit via `Authorization: Bearer mgw_...`.
- Raise a readable `ObserverIngestError` when ingest returns an error.

Out of scope:

- Python framework auto-instrumentation.
- Async client.
- Background queue/retry.
- OTLP exporter.
- PyPI publishing.

## Verification

- `cd sdks/python && python3 -m pytest`
- `DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable' go test ./...` from `server/`
- `pnpm --filter @multica/observer test`

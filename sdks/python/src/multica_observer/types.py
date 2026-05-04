from __future__ import annotations

from typing import Any, Callable, Literal, Protocol

SpanKind = Literal[
    "workflow",
    "session",
    "task",
    "operation",
    "agent",
    "tool",
    "llm",
    "chain",
    "text",
    "guardrail",
    "http",
    "unknown",
]
SpanStatusCode = Literal["unset", "ok", "error"]
LogSeverity = Literal["trace", "debug", "info", "warn", "error", "fatal"]
ToolStatus = Literal["success", "error", "blocked", "unknown"]
TraceStatus = Literal["running", "success", "error", "cancelled", "unknown"]

JsonObject = dict[str, Any]
Clock = Callable[[], str]
TransportResult = tuple[int, bytes]


class Transport(Protocol):
    def __call__(self, url: str, headers: dict[str, str], body: bytes) -> TransportResult:
        ...

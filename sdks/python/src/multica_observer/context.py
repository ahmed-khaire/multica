from __future__ import annotations

from dataclasses import dataclass
from typing import Callable
from uuid import uuid4

IdGenerator = Callable[[], str]

MULTICA_HEADER_NAMES = {
    "trace_id": "X-Multica-Trace-ID",
    "session_id": "X-Multica-Session-ID",
    "parent_span_id": "X-Multica-Parent-Span-ID",
    "app_id": "X-Multica-App-ID",
    "service_name": "X-Multica-Service-Name",
    "environment": "X-Multica-Environment",
    "deployment_id": "X-Multica-Deployment-ID",
    "ai_system_id": "X-Multica-AI-System-ID",
    "agent_id": "X-Multica-Agent-ID",
    "workflow_id": "X-Multica-Workflow-ID",
    "end_user_id": "X-Multica-End-User-ID",
    "use_case": "X-Multica-Use-Case",
    "data_domains": "X-Multica-Data-Domains",
    "tool_hint": "X-Multica-Tool-Hint",
}


@dataclass(frozen=True)
class TraceContext:
    trace_id: str
    session_id: str
    root_span_id: str


def default_id_generator() -> str:
    return str(uuid4())


def create_trace_context(id_generator: IdGenerator = default_id_generator) -> TraceContext:
    return TraceContext(
        trace_id=id_generator(),
        session_id=id_generator(),
        root_span_id=id_generator(),
    )

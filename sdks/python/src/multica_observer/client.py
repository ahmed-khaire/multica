from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Callable
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from .context import IdGenerator, MULTICA_HEADER_NAMES, create_trace_context, default_id_generator
from .types import JsonObject, LogSeverity, SpanKind, SpanStatusCode, ToolStatus, TraceStatus, Transport


class ObserverIngestError(RuntimeError):
    pass


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _compact(value: JsonObject) -> JsonObject:
    return {key: item for key, item in value.items() if item is not None and item != ""}


def _status_to_span_code(status: TraceStatus | None) -> SpanStatusCode:
    if status == "success":
        return "ok"
    if status in {"error", "cancelled"}:
        return "error"
    return "unset"


def _default_transport(url: str, headers: dict[str, str], body: bytes) -> tuple[int, bytes]:
    request = Request(url, data=body, headers=headers, method="POST")
    try:
        with urlopen(request, timeout=30) as response:
            return response.status, response.read()
    except HTTPError as exc:
        return exc.code, exc.read()


@dataclass(frozen=True)
class _ClientDefaults:
    service_name: str
    app_id: str | None
    environment: str | None
    deployment_id: str | None
    ai_system_id: str | None
    id_generator: IdGenerator
    now: Callable[[], str]


class ObserverTrace:
    def __init__(
        self,
        defaults: _ClientDefaults,
        *,
        name: str,
        trace_id: str | None = None,
        session_id: str | None = None,
        root_span_id: str | None = None,
        status: TraceStatus = "success",
        agent_id: str | None = None,
        workflow_id: str | None = None,
        end_user_id: str | None = None,
        use_case: str | None = None,
        data_domains: list[str] | None = None,
        tool_hint: str | None = None,
        tags: list[str] | None = None,
        resource_attributes: JsonObject | None = None,
    ) -> None:
        generated = create_trace_context(defaults.id_generator)
        self.trace_id = trace_id or generated.trace_id
        self.session_id = session_id or generated.session_id
        self.root_span_id = root_span_id or generated.root_span_id
        self._defaults = defaults
        self._name = name
        self._status = status
        self._agent_id = agent_id
        self._workflow_id = workflow_id
        self._end_user_id = end_user_id
        self._use_case = use_case
        self._data_domains = data_domains
        self._tool_hint = tool_hint
        self._tags = tags
        self._extra_resource_attributes = resource_attributes or {}
        self._spans: list[JsonObject] = [
            _compact({
                "span_id": self.root_span_id,
                "kind": "workflow",
                "name": name,
                "service_name": defaults.service_name,
                "status_code": _status_to_span_code(status),
                "started_at": defaults.now(),
                "resource_attributes": self.resource_attributes(),
            })
        ]
        self._events: list[JsonObject] = []
        self._logs: list[JsonObject] = []
        self._agents: list[JsonObject] = []
        self._tools: list[JsonObject] = []

    def span(
        self,
        *,
        name: str,
        kind: SpanKind = "operation",
        span_id: str | None = None,
        parent_span_id: str | None = None,
        service_name: str | None = None,
        status_code: SpanStatusCode = "unset",
        status_message: str | None = None,
        started_at: str | None = None,
        ended_at: str | None = None,
        duration_ms: int | None = None,
        attributes: JsonObject | None = None,
        resource_attributes: JsonObject | None = None,
    ) -> JsonObject:
        payload = _compact({
            "span_id": span_id or self._defaults.id_generator(),
            "parent_span_id": parent_span_id,
            "kind": kind,
            "name": name,
            "service_name": service_name or self._defaults.service_name,
            "status_code": status_code,
            "status_message": status_message,
            "started_at": started_at or self._defaults.now(),
            "ended_at": ended_at,
            "duration_ms": duration_ms,
            "attributes": attributes,
            "resource_attributes": resource_attributes,
        })
        self._spans.append(payload)
        return payload

    def event(
        self,
        *,
        event_type: str,
        span_id: str | None = None,
        payload: JsonObject | None = None,
        occurred_at: str | None = None,
    ) -> JsonObject:
        event = _compact({
            "span_id": span_id,
            "event_type": event_type,
            "payload": payload,
            "occurred_at": occurred_at or self._defaults.now(),
        })
        self._events.append(event)
        return event

    def log(
        self,
        *,
        body: str,
        span_id: str | None = None,
        severity: LogSeverity = "info",
        attributes: JsonObject | None = None,
        occurred_at: str | None = None,
    ) -> JsonObject:
        log = _compact({
            "span_id": span_id,
            "severity": severity,
            "body": body,
            "attributes": attributes,
            "occurred_at": occurred_at or self._defaults.now(),
        })
        self._logs.append(log)
        return log

    def agent(
        self,
        *,
        span_id: str | None = None,
        agent_id: str | None = None,
        agent_name: str | None = None,
        role: str | None = None,
        models: list[str] | None = None,
        tools: list[str] | None = None,
        handoff_source: str | None = None,
        handoff_destination: str | None = None,
        reasoning_summary: str | None = None,
    ) -> JsonObject:
        agent = _compact({
            "span_id": span_id,
            "agent_id": agent_id,
            "agent_name": agent_name,
            "role": role,
            "models": models,
            "tools": tools,
            "handoff_source": handoff_source,
            "handoff_destination": handoff_destination,
            "reasoning_summary": reasoning_summary,
        })
        self._agents.append(agent)
        return agent

    def tool(
        self,
        *,
        span_id: str | None = None,
        tool_id: str | None = None,
        tool_name: str | None = None,
        description: str | None = None,
        parameters: Any | None = None,
        result: Any | None = None,
        status: ToolStatus = "unknown",
        duration_ms: int | None = None,
    ) -> JsonObject:
        tool = _compact({
            "span_id": span_id,
            "tool_id": tool_id,
            "tool_name": tool_name,
            "description": description,
            "parameters": parameters,
            "result": result,
            "status": status,
            "duration_ms": duration_ms,
        })
        self._tools.append(tool)
        return tool

    def context_attributes(self) -> dict[str, str]:
        return {
            key: value
            for key, value in {
                "app_id": self._defaults.app_id,
                "service_name": self._defaults.service_name,
                "environment": self._defaults.environment,
                "deployment_id": self._defaults.deployment_id,
                "ai_system_id": self._defaults.ai_system_id,
                "agent_id": self._agent_id,
                "workflow_id": self._workflow_id,
                "end_user_id": self._end_user_id,
                "use_case": self._use_case,
                "data_domains": ",".join(self._data_domains) if self._data_domains else None,
                "tool_hint": self._tool_hint,
            }.items()
            if value
        }

    def resource_attributes(self) -> JsonObject:
        return _compact({
            "multica.app_id": self._defaults.app_id,
            "service.name": self._defaults.service_name,
            "deployment.environment": self._defaults.environment,
            "deployment.id": self._defaults.deployment_id,
            "multica.ai_system_id": self._defaults.ai_system_id,
            "multica.agent_id": self._agent_id,
            "multica.workflow_id": self._workflow_id,
            "multica.end_user_id": self._end_user_id,
            "multica.use_case": self._use_case,
            "multica.data_domains": self._data_domains,
            **self._extra_resource_attributes,
        })

    def to_payload(self) -> JsonObject:
        return _compact({
            "trace_id": self.trace_id,
            "root_span_id": self.root_span_id,
            "name": self._name,
            "service_name": self._defaults.service_name,
            "client_tool_hint": self._tool_hint or "sdk",
            "status": self._status,
            "tags": self._tags,
            "resource_attributes": self.resource_attributes(),
            "spans": self._spans,
            "events": self._events,
            "logs": self._logs,
            "agents": self._agents,
            "tools": self._tools,
        })


class ObserverClient:
    def __init__(
        self,
        *,
        gateway_base_url: str,
        gateway_key: str,
        service_name: str,
        app_id: str | None = None,
        environment: str | None = None,
        deployment_id: str | None = None,
        ai_system_id: str | None = None,
        id_generator: IdGenerator = default_id_generator,
        now: Callable[[], str] = _now_iso,
        transport: Transport = _default_transport,
    ) -> None:
        self.gateway_base_url = gateway_base_url.rstrip("/")
        self.gateway_key = gateway_key
        self._transport = transport
        self._defaults = _ClientDefaults(
            service_name=service_name,
            app_id=app_id,
            environment=environment,
            deployment_id=deployment_id,
            ai_system_id=ai_system_id,
            id_generator=id_generator,
            now=now,
        )

    def start_trace(self, *, name: str, **kwargs: Any) -> ObserverTrace:
        return ObserverTrace(self._defaults, name=name, **kwargs)

    def inject_headers(
        self,
        trace: ObserverTrace,
        *,
        headers: dict[str, str] | None = None,
        parent_span_id: str | None = None,
    ) -> dict[str, str]:
        context = trace.context_attributes()
        merged = dict(headers or {})
        merged.update(_compact({
            MULTICA_HEADER_NAMES["trace_id"]: trace.trace_id,
            MULTICA_HEADER_NAMES["session_id"]: trace.session_id,
            MULTICA_HEADER_NAMES["parent_span_id"]: parent_span_id or trace.root_span_id,
            MULTICA_HEADER_NAMES["app_id"]: context.get("app_id"),
            MULTICA_HEADER_NAMES["service_name"]: context.get("service_name"),
            MULTICA_HEADER_NAMES["environment"]: context.get("environment"),
            MULTICA_HEADER_NAMES["deployment_id"]: context.get("deployment_id"),
            MULTICA_HEADER_NAMES["ai_system_id"]: context.get("ai_system_id"),
            MULTICA_HEADER_NAMES["agent_id"]: context.get("agent_id"),
            MULTICA_HEADER_NAMES["workflow_id"]: context.get("workflow_id"),
            MULTICA_HEADER_NAMES["end_user_id"]: context.get("end_user_id"),
            MULTICA_HEADER_NAMES["use_case"]: context.get("use_case"),
            MULTICA_HEADER_NAMES["data_domains"]: context.get("data_domains"),
            MULTICA_HEADER_NAMES["tool_hint"]: context.get("tool_hint"),
        }))
        return merged

    def flush(self, trace: ObserverTrace) -> JsonObject:
        body = json.dumps(trace.to_payload(), separators=(",", ":")).encode("utf-8")
        status, response_body = self._transport(
            f"{self.gateway_base_url}/v1/traces",
            {
                "Authorization": f"Bearer {self.gateway_key}",
                "Content-Type": "application/json",
            },
            body,
        )
        if status < 200 or status >= 300:
            raise ObserverIngestError(_error_message(status, response_body))
        return json.loads(response_body.decode("utf-8"))


def _error_message(status: int, body: bytes) -> str:
    try:
        parsed = json.loads(body.decode("utf-8"))
        if isinstance(parsed, dict) and parsed.get("error"):
            return str(parsed["error"])
    except json.JSONDecodeError:
        pass
    return f"Observer ingest failed: HTTP {status}"

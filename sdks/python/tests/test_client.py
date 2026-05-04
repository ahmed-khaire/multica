import json

import pytest

from multica_observer import ObserverClient, ObserverIngestError


def ids(values):
    iterator = iter(values)

    def next_id():
        return next(iterator)

    return next_id


def test_creates_trace_context_and_injects_gateway_headers():
    client = ObserverClient(
        gateway_base_url="https://gateway.example.com",
        gateway_key="mgw_test",
        service_name="checkout-api",
        app_id="checkout",
        environment="production",
        deployment_id="deploy-1",
        ai_system_id="support-bot",
        id_generator=ids(["trace-1", "session-1", "root-1"]),
    )

    trace = client.start_trace(
        name="Checkout run",
        agent_id="agent-checkout",
        workflow_id="workflow-checkout",
        end_user_id="user-123",
        use_case="customer support",
        data_domains=["orders", "payments"],
        tool_hint="openai-sdk",
    )

    assert trace.trace_id == "trace-1"
    assert trace.session_id == "session-1"
    assert trace.root_span_id == "root-1"
    assert client.inject_headers(trace, headers={"Content-Type": "application/json"}, parent_span_id="tool-1") == {
        "Content-Type": "application/json",
        "X-Multica-Trace-ID": "trace-1",
        "X-Multica-Session-ID": "session-1",
        "X-Multica-Parent-Span-ID": "tool-1",
        "X-Multica-App-ID": "checkout",
        "X-Multica-Service-Name": "checkout-api",
        "X-Multica-Environment": "production",
        "X-Multica-Deployment-ID": "deploy-1",
        "X-Multica-AI-System-ID": "support-bot",
        "X-Multica-Agent-ID": "agent-checkout",
        "X-Multica-Workflow-ID": "workflow-checkout",
        "X-Multica-End-User-ID": "user-123",
        "X-Multica-Use-Case": "customer support",
        "X-Multica-Data-Domains": "orders,payments",
        "X-Multica-Tool-Hint": "openai-sdk",
    }


def test_records_spans_logs_tools_and_flushes_to_trace_ingest_endpoint():
    calls = []

    def transport(url, headers, body):
        calls.append((url, headers, json.loads(body.decode("utf-8"))))
        return 201, json.dumps({
            "trace_id": "trace-1",
            "session_id": "server-session-1",
            "span_count": 2,
            "event_count": 1,
            "log_count": 1,
            "agent_count": 1,
            "tool_count": 1,
        }).encode("utf-8")

    client = ObserverClient(
        gateway_base_url="https://gateway.example.com/",
        gateway_key="mgw_test",
        service_name="checkout-api",
        app_id="checkout",
        environment="staging",
        id_generator=ids(["trace-1", "session-1", "root-1", "span-tool-1"]),
        now=lambda: "2026-05-04T12:00:00.000Z",
        transport=transport,
    )

    trace = client.start_trace(name="Checkout run", agent_id="agent-checkout")
    tool_span = trace.span(
        name="Search inventory",
        kind="tool",
        parent_span_id=trace.root_span_id,
        attributes={"tool.name": "inventory.search"},
        status_code="ok",
        duration_ms=42,
    )
    trace.event(span_id=tool_span["span_id"], event_type="tool.completed", payload={"items": 2})
    trace.log(span_id=tool_span["span_id"], severity="info", body="inventory search completed")
    trace.agent(
        span_id=trace.root_span_id,
        agent_id="agent-checkout",
        agent_name="Checkout Agent",
        role="customer-support",
        models=["gpt-4.1"],
        tools=["inventory.search"],
    )
    trace.tool(
        span_id=tool_span["span_id"],
        tool_id="inventory.search",
        tool_name="inventory.search",
        status="success",
        parameters={"sku": "sku_123"},
        result={"available": True},
        duration_ms=42,
    )

    result = client.flush(trace)

    assert result == {
        "trace_id": "trace-1",
        "session_id": "server-session-1",
        "span_count": 2,
        "event_count": 1,
        "log_count": 1,
        "agent_count": 1,
        "tool_count": 1,
    }
    assert len(calls) == 1
    url, headers, body = calls[0]
    assert url == "https://gateway.example.com/v1/traces"
    assert headers == {
        "Authorization": "Bearer mgw_test",
        "Content-Type": "application/json",
    }
    assert body["trace_id"] == "trace-1"
    assert body["root_span_id"] == "root-1"
    assert body["name"] == "Checkout run"
    assert body["service_name"] == "checkout-api"
    assert body["client_tool_hint"] == "sdk"
    assert body["status"] == "success"
    assert body["resource_attributes"] == {
        "multica.app_id": "checkout",
        "service.name": "checkout-api",
        "deployment.environment": "staging",
        "multica.agent_id": "agent-checkout",
    }
    assert body["spans"][0] == {
        "span_id": "root-1",
        "kind": "workflow",
        "name": "Checkout run",
        "service_name": "checkout-api",
        "status_code": "ok",
        "started_at": "2026-05-04T12:00:00.000Z",
        "resource_attributes": body["resource_attributes"],
    }
    assert body["spans"][1]["span_id"] == "span-tool-1"
    assert body["spans"][1]["parent_span_id"] == "root-1"
    assert body["logs"][0]["body"] == "inventory search completed"
    assert body["tools"][0]["parameters"] == {"sku": "sku_123"}
    assert body["tools"][0]["result"] == {"available": True}


def test_flush_raises_readable_error_when_ingest_fails():
    def transport(_url, _headers, _body):
        return 401, b'{"error":"gateway key is invalid"}'

    client = ObserverClient(
        gateway_base_url="https://gateway.example.com",
        gateway_key="mgw_bad",
        service_name="checkout-api",
        id_generator=ids(["trace-1", "session-1", "root-1"]),
        transport=transport,
    )

    with pytest.raises(ObserverIngestError, match="gateway key is invalid"):
        client.flush(client.start_trace(name="Bad key run"))

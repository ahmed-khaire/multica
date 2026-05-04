package ingest

import "testing"

func TestValidateTraceRequestRequiresTraceID(t *testing.T) {
	req := TraceRequest{
		Spans: []SpanPayload{{
			SpanID: "root",
			Name:   "root workflow",
			Kind:   "workflow",
		}},
	}

	if err := ValidateTraceRequest(req); err == nil {
		t.Fatal("ValidateTraceRequest returned nil, want error")
	}
}

func TestValidateTraceRequestRequiresSpanID(t *testing.T) {
	req := TraceRequest{
		TraceID: "trace-validation",
		Spans: []SpanPayload{{
			Name: "missing id",
			Kind: "operation",
		}},
	}

	if err := ValidateTraceRequest(req); err == nil {
		t.Fatal("ValidateTraceRequest returned nil, want error")
	}
}

func TestValidateTraceRequestAcceptsMinimalTrace(t *testing.T) {
	req := TraceRequest{
		TraceID:     "trace-validation",
		ServiceName: "checkout-api",
		Spans: []SpanPayload{{
			SpanID: "root",
			Name:   "checkout workflow",
			Kind:   "workflow",
		}},
	}

	if err := ValidateTraceRequest(req); err != nil {
		t.Fatalf("ValidateTraceRequest returned error: %v", err)
	}
}

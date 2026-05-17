package proxy

import "testing"

func TestGatewayTransportConstants(t *testing.T) {
	if TransportDirectHTTP != "direct_http" {
		t.Fatalf("TransportDirectHTTP = %q", TransportDirectHTTP)
	}
	if TransportDaemonDispatch != "daemon_dispatch" {
		t.Fatalf("TransportDaemonDispatch = %q", TransportDaemonDispatch)
	}
}

func TestBackendTargetSubscriptionFieldsZeroValue(t *testing.T) {
	var target BackendTarget
	if target.Transport != "" || target.SubscriptionProvider != "" || target.DispatchScope != "" {
		t.Fatalf("unexpected zero values: %+v", target)
	}
}

package kubernetes

import "testing"

func TestBuildTCPRouteObject_APIVersion(t *testing.T) {
	cfg := TCPRouteConfig{Name: "t", Namespace: "ns", GatewayName: "gw", SectionName: "l4-tcp-1"}
	for _, av := range []string{"gateway.networking.k8s.io/v1", "gateway.networking.k8s.io/v1alpha2"} {
		if got := BuildTCPRouteObject(cfg, av).APIVersion; got != av {
			t.Errorf("TCPRoute APIVersion = %q, want %q", got, av)
		}
	}
	if k := BuildTCPRouteObject(cfg, "gateway.networking.k8s.io/v1").Kind; k != "TCPRoute" {
		t.Errorf("Kind = %q, want TCPRoute", k)
	}
}

func TestBuildUDPRouteObject_APIVersion(t *testing.T) {
	cfg := UDPRouteConfig{Name: "u", Namespace: "ns", GatewayName: "gw", SectionName: "l4-udp-1"}
	for _, av := range []string{"gateway.networking.k8s.io/v1", "gateway.networking.k8s.io/v1alpha2"} {
		if got := BuildUDPRouteObject(cfg, av).APIVersion; got != av {
			t.Errorf("UDPRoute APIVersion = %q, want %q", got, av)
		}
	}
}

func TestL4RouteGVRV1Consts(t *testing.T) {
	if TCPRouteGVRV1.Version != "v1" || TCPRouteGVRV1.Resource != "tcproutes" {
		t.Errorf("TCPRouteGVRV1 wrong: %+v", TCPRouteGVRV1)
	}
	if UDPRouteGVRV1.Version != "v1" || UDPRouteGVRV1.Resource != "udproutes" {
		t.Errorf("UDPRouteGVRV1 wrong: %+v", UDPRouteGVRV1)
	}
}

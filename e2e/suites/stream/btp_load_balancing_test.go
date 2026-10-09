//go:build e2e

package stream

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestStreamLoadBalancing verifies Envoy Gateway accepts a load-balancer
// policy on both a TCPRoute and a UDPRoute and both routes serve. Config-level
// (no distribution assertion), consistent with the spec decision that the L4
// suite matches the HTTP/gRPC precedent and does not observe weighting.
func TestStreamLoadBalancing(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	lb := &routeplan.BackendTrafficPolicyInput{
		LoadBalancer: &models.LoadBalancerConfig{Type: models.LoadBalancerTypeRoundRobin},
	}

	fx.Route(s, services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15700,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 100},
			},
		},
		BackendTrafficPolicy: lb,
	})
	fx.Route(s, services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolUDP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15701,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9101, Weight: 100},
			},
		},
		BackendTrafficPolicy: lb,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr, err := env.StreamGatewayAddr(ctx, s, 2*time.Minute)
	if err != nil {
		t.Fatalf("resolve stream gateway LB: %v", err)
	}

	if got, err := harness.DialTCP(ctx, addr+":15700", []byte("lb-tcp"), 10*time.Second); err != nil || string(got) != "lb-tcp" {
		t.Fatalf("tcp LB dial: got %q err %v", got, err)
	}
	if got, err := harness.DialUDP(ctx, addr+":15701", []byte("lb-udp"), 20*time.Second); err != nil || string(got) != "lb-udp" {
		t.Fatalf("udp LB dial: got %q err %v", got, err)
	}
}

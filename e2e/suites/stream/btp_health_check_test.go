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

// TestStreamTCPHealthCheck verifies Envoy Gateway accepts a TCP active health
// check on a TCPRoute with two Kubernetes-Service backends and the route
// serves. Config-level by decision (see btp_circuit_breaker_test.go): the
// BTP-Accepted gate is enforced in StreamFixture.Route; a behavioral
// dead-backend-ejection assertion through the kind LoadBalancer is a
// documented follow-up.
func TestStreamTCPHealthCheck(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	interval := "1s"
	unhealthy := uint32(3)
	fx.Route(s, services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15600,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 50},
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-b", Port: 9100, Weight: 50},
			},
		},
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			HealthCheck: &models.HealthCheckConfig{
				Active: &models.ActiveHealthCheckConfig{
					Type:               "TCP",
					Interval:           &interval,
					UnhealthyThreshold: &unhealthy,
					TCP:                &models.TCPActiveHealthCheckConfig{},
				},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr, err := env.StreamGatewayAddr(ctx, s, 2*time.Minute)
	if err != nil {
		t.Fatalf("resolve stream gateway LB: %v", err)
	}
	want := []byte("hc-live")
	got, err := harness.DialTCP(ctx, addr+":15600", want, 10*time.Second)
	if err != nil {
		t.Fatalf("dial tcp %s:15600: %v", addr, err)
	}
	if string(got) != string(want) {
		t.Fatalf("tcp echo with health check: got %q want %q", got, want)
	}
}

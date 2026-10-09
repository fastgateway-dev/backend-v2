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

// i64 returns a pointer to v (the BTP config fields are *int64).
func i64(v int64) *int64 { return &v }

// TestStreamTCPCircuitBreaker verifies Envoy Gateway accepts the TCP
// circuit-breaker subset (maxConnections + maxRequestsPerConnection) on a
// TCPRoute's BackendTrafficPolicy and the route still serves. The BTP-Accepted
// gate is enforced inside StreamFixture.Route (WaitForPoliciesAccepted); this
// is config-level by decision — a behavioral maxConnections trip-assertion
// through the kind LoadBalancer is a documented follow-up (see the suite's
// ledger/spec: behavioral asserts fall back to config-Accepted where they
// cannot be made reliable).
func TestStreamTCPCircuitBreaker(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	fx.Route(s, services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15400,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 100},
			},
		},
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			CircuitBreaker: &models.CircuitBreakerConfig{
				MaxConnections:           i64(2),
				MaxRequestsPerConnection: i64(1),
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr, err := env.StreamGatewayAddr(ctx, s, 2*time.Minute)
	if err != nil {
		t.Fatalf("resolve stream gateway LB: %v", err)
	}
	want := []byte("cb-live")
	got, err := harness.DialTCP(ctx, addr+":15400", want, 10*time.Second)
	if err != nil {
		t.Fatalf("dial tcp %s:15400: %v", addr, err)
	}
	if string(got) != string(want) {
		t.Fatalf("tcp echo with circuit breaker: got %q want %q", got, want)
	}
}

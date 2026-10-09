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

// TestStreamTCPTimeout verifies Envoy Gateway accepts a TCP connect-timeout on
// a TCPRoute's BackendTrafficPolicy and the route still serves. Config-level by
// design — forcing a connect timeout through the kind LoadBalancer is not
// reliably reproducible; the BTP-Accepted gate inside StreamFixture.Route is
// the policy assertion, and the dial below is the liveness assertion.
func TestStreamTCPTimeout(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	fx.Route(s, services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15500,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 100},
			},
		},
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			Timeout: &models.BTPTimeoutConfig{
				TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr, err := env.StreamGatewayAddr(ctx, s, 2*time.Minute)
	if err != nil {
		t.Fatalf("resolve stream gateway LB: %v", err)
	}
	want := []byte("timeout-live")
	got, err := harness.DialTCP(ctx, addr+":15500", want, 10*time.Second)
	if err != nil {
		t.Fatalf("dial tcp %s:15500: %v", addr, err)
	}
	if string(got) != string(want) {
		t.Fatalf("tcp echo with timeout policy: got %q want %q", got, want)
	}
}

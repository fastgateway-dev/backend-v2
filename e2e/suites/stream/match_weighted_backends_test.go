//go:build e2e

package stream

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestStreamWeightedBackends verifies a TCP route with two weighted
// Kubernetes-Service backends goes live and serves. Both entries point at the
// same l4-echo-a service with 80/20 weights, mirroring the HTTP/gRPC
// weighted-backends precedent exactly: this asserts the route serves, NOT the
// traffic distribution (the suite does not observe weighting).
func TestStreamWeightedBackends(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	fx.Route(s, services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15800,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 80},
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 20},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr, err := env.StreamGatewayAddr(ctx, s, 2*time.Minute)
	if err != nil {
		t.Fatalf("resolve stream gateway LB: %v", err)
	}
	want := []byte("weighted-tcp")
	got, err := harness.DialTCP(ctx, addr+":15800", want, 10*time.Second)
	if err != nil {
		t.Fatalf("dial tcp %s:15800: %v", addr, err)
	}
	if string(got) != string(want) {
		t.Fatalf("tcp echo weighted: got %q want %q", got, want)
	}
}

//go:build e2e

package stream

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestStreamTCPTraffic proves a tcp:<port> route carries raw TCP through the
// Stream Gateway's LoadBalancer to the l4-echo backend: the echoed bytes equal
// what was sent.
func TestStreamTCPTraffic(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	fx.Route(s, services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15100,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 100},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr, err := env.StreamGatewayAddr(ctx, s, 2*time.Minute)
	if err != nil {
		t.Fatalf("resolve stream gateway LB: %v", err)
	}

	want := []byte("hello-tcp")
	got, err := harness.DialTCP(ctx, addr+":15100", want, 10*time.Second)
	if err != nil {
		t.Fatalf("dial tcp %s:15100: %v", addr, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("tcp echo: got %q want %q", got, want)
	}
}

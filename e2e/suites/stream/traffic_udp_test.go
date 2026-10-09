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

// TestStreamUDPTraffic proves a udp:<port> route carries raw UDP through the
// Stream Gateway's LoadBalancer to the l4-echo backend. DialUDP retries to the
// deadline because UDP is lossy.
func TestStreamUDPTraffic(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	fx.Route(s, services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolUDP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15101,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9101, Weight: 100},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr, err := env.StreamGatewayAddr(ctx, s, 2*time.Minute)
	if err != nil {
		t.Fatalf("resolve stream gateway LB: %v", err)
	}

	want := []byte("hello-udp")
	got, err := harness.DialUDP(ctx, addr+":15101", want, 20*time.Second)
	if err != nil {
		t.Fatalf("dial udp %s:15101: %v", addr, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("udp echo: got %q want %q", got, want)
	}
}

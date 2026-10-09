//go:build e2e

package stream

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestStreamPortCollision verifies a second route on the same (stream,
// transport, port) is rejected with 409, while the same port on the other
// transport is accepted (tcp/N and udp/N do not collide).
func TestStreamPortCollision(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)

	l4 := func(proto models.RouteProtocol, port, backendPort int) services.CreateRouteInput {
		return services.CreateRouteInput{
			Name:     harness.UniqueName(t),
			Protocol: proto,
			TeamID:   teamID(t),
			Config: models.RouteConfig{
				ListenerPort: port,
				Backends: []models.RouteBackend{
					{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: backendPort, Weight: 100},
				},
			},
		}
	}

	// First TCP route on 15300 (full lifecycle, cleaned up by the fixture).
	fx.Route(s, l4(models.RouteProtocolTCP, 15300, 9100))

	// A second tcp:15300 on the same stream must be rejected 409.
	_, err := env.Editor.CreateStreamRoute(context.Background(), env.ProjectID, s.ID.String(), l4(models.RouteProtocolTCP, 15300, 9100))
	se, ok := err.(*harness.StatusError)
	if !ok || se.StatusCode != 409 {
		t.Fatalf("duplicate tcp:15300: want 409 StatusError, got %v", err)
	}

	// Positive control: udp:15300 on the same stream is a different listener
	// and must be accepted (tcp/N and udp/N do not collide).
	fx.Route(s, l4(models.RouteProtocolUDP, 15300, 9101))
}

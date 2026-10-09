//go:build e2e

package stream

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestStreamRejectsL7Fields verifies the backend rejects (400) an L4 route that
// carries L7-only fields, or that is missing a listener port / backends, at
// write time — before anything reaches the cluster.
func TestStreamRejectsL7Fields(t *testing.T) {
	s := newStream(t)

	base := func() services.CreateRouteInput {
		return services.CreateRouteInput{
			Name:     harness.UniqueName(t),
			Protocol: models.RouteProtocolTCP,
			TeamID:   teamID(t),
			Config: models.RouteConfig{
				ListenerPort: 15200,
				Backends: []models.RouteBackend{
					{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 100},
				},
			},
		}
	}

	cases := map[string]func(*services.CreateRouteInput){
		"matches":      func(c *services.CreateRouteInput) { c.Config.Matches = []models.RouteMatch{{}} },
		"redirect":     func(c *services.CreateRouteInput) { c.Config.RouteType = models.RouteTypeRedirect },
		"missing-port": func(c *services.CreateRouteInput) { c.Config.ListenerPort = 0 },
		"no-backends":  func(c *services.CreateRouteInput) { c.Config.Backends = nil },
		"external-backend": func(c *services.CreateRouteInput) {
			c.Config.Backends = []models.RouteBackend{{Type: models.BackendTypeExternal, Address: "example.com", Port: 80}}
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			mutate(&cfg)
			_, err := env.Editor.CreateStreamRoute(context.Background(), env.ProjectID, s.ID.String(), cfg)
			se, ok := err.(*harness.StatusError)
			if !ok || se.StatusCode != 400 {
				t.Fatalf("want 400 StatusError, got %v", err)
			}
		})
	}
}

//go:build e2e

package httproute

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestHeaderModifier ports test_header_modifier.py. Already had a real
// assertion (both response headers) in the Python source; transcribed
// as-is.
func TestHeaderModifier(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			ResponseHeaderModifier: &models.HeaderModifier{
				Set: []models.HeaderValue{{Name: "X-Response-Set", Value: "set-value"}},
				Add: []models.HeaderValue{{Name: "X-Response-Add", Value: "add-value"}},
			},
			URLRewrite: rewriteTo("/"),
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("header modifier: route never became live: %v", err)
	}

	if got := resp.Header.Get("X-Response-Set"); got != "set-value" {
		t.Fatalf("header modifier: got X-Response-Set %q, want %q", got, "set-value")
	}
	if got := resp.Header.Get("X-Response-Add"); got != "add-value" {
		t.Fatalf("header modifier: got X-Response-Add %q, want %q", got, "add-value")
	}
}

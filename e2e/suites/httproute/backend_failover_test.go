//go:build e2e

package httproute

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestBackendFailover ports tests/http_route_features/test_backend_failover.py.
//
// The primary backend (nginx-service:8080) has nothing listening on that
// port, so every request can only succeed via the weight=1 fallback
// backend (nginx-service:80). The old suite's retry_until already forced
// status_code into (200, 404) before the assertion re-checked the same
// thing, so a route that never deployed (permanent 404) passed identically
// to one that failed over correctly. This asserts the one outcome that
// actually proves failover worked: 200.
func TestBackendFailover(t *testing.T) {
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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: 8080, Weight: 100},
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 1, Fallback: true},
			},
			// nginx only ever serves "/"; without this, the client's
			// unique path would forward unchanged and even the fallback
			// would 404 (indistinguishable from "route not live yet").
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
		t.Fatalf("backend failover: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("backend failover: got status %d, want 200 (fallback backend should have served the request)", resp.StatusCode)
	}
}

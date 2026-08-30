//go:build e2e

package httproute

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestTimeoutRoute ports test_timeout_route.py ("Timeout (Route-Level)").
//
// KNOWN GAP vs the brief's framing of this as a distinct "route-level"
// mechanism: the Python config set a "timeouts" object directly on the
// route's config (route-level timeouts.request/backendRequest). This
// backend has no such field anywhere -- models.RouteConfig has no Timeouts
// field, and BuildHTTPRouteObject (internal/services/kubernetes_service.go)
// never populates gatewayv1.HTTPRouteRule.Timeouts either -- so that field
// was already a silent no-op before this port; it just happened to be
// masked by the old tautological assertion. The only timeout knob this
// backend actually implements end-to-end is
// BackendTrafficPolicy.Timeout (models.BTPTimeoutConfig), which is exactly
// what TestTimeoutBTP (test_timeout_btp.py) exercises too. Both ports
// therefore configure the same BackendTrafficPolicy.Timeout.HTTP.RequestTimeout
// -- they are not two different code paths in this backend today. See
// task-11-report.md.
func TestTimeoutRoute(t *testing.T) {
	t.Parallel()
	podinfoMu.Lock()
	defer podinfoMu.Unlock()

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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/delay/5"),
		},
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
			Timeout: &models.BTPTimeoutConfig{
				HTTP: &models.BTPHTTPTimeoutConfig{RequestTimeout: "2s"},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	// The first non-404 response IS the test: podinfo delays 5s, the
	// timeout is 2s, so Envoy must give up and answer 504 as soon as the
	// route is live.
	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("timeout route: route never became live: %v", err)
	}
	if resp.StatusCode != 504 {
		t.Fatalf("timeout route: got status %d, want 504 (backend delays 5s, timeout is 2s)", resp.StatusCode)
	}
}

//go:build e2e

package httproute

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestFaultInjectionAbort ports test_fault_injection.py. Already had a
// real assertion (status == 503) in the Python source; transcribed as-is.
// Envoy's fault filter aborts the request before it ever reaches the
// backend (percentage: 100), so no urlRewrite is needed.
func TestFaultInjectionAbort(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)
	httpStatus := 503
	pct := float32(100)

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
		},
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			FaultInjection: &models.FaultInjectionConfig{
				Abort: &models.FaultInjectionAbortConfig{
					HTTPStatus: &httpStatus,
					Percentage: &pct,
				},
			},
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
		t.Fatalf("fault injection: route never became live: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Fatalf("fault injection: got status %d, want 503", resp.StatusCode)
	}
}

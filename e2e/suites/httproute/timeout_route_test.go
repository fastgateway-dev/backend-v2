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
	route := fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	// harness.WaitForRouteLive returns the FIRST non-404 (and non-5xx-
	// within-grace) response, on the assumption that it's already the
	// route's real, final answer. That assumption breaks here: the
	// HTTPRoute and its BackendTrafficPolicy are two separate Kubernetes
	// writes that reconcile independently, so there is a real window
	// where the route is live but the BTP's 2s request timeout has not
	// converged yet. In that window podinfo's own /delay/5 answers with a
	// perfectly ordinary 200 after 5s -- not a 404, not a 5xx -- so
	// WaitForRouteLive would return that 200 as "live" and the assertion
	// below would fail for the wrong reason (racing the policy, not
	// testing it). Gate on the BTP itself being Accepted first: that can
	// only become true once Envoy Gateway has actually programmed the
	// timeout, which is the one thing the unconverged state cannot
	// satisfy.
	if err := harness.WaitForPolicyAccepted(ctx, env.Kube, harness.BackendTrafficPolicyGVR, env.Cfg.Namespace, route.ID.String(), routeLiveTimeout); err != nil {
		t.Fatalf("timeout route: BackendTrafficPolicy never accepted: %v", err)
	}

	// "Accepted" at the Kubernetes API level doesn't itself guarantee
	// Envoy has finished pushing the corresponding xDS config (see
	// harness.WaitForPolicyAccepted's doc comment), so the actual proof
	// polls the data plane for the response that can ONLY be produced
	// once the timeout is genuinely enforced: 504. A 200 during any
	// remaining xDS-push tail is retried rather than accepted as final --
	// unlike harness.WaitForRouteLive, whose "first non-404 response is
	// final" rule would wrongly treat that pre-convergence 200 as the
	// route's real answer.
	deadline := time.Now().Add(routeLiveTimeout)
	var lastStatus int
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := env.GW.HTTP(ctx, "GET", path)
		if err != nil {
			lastErr = err
		} else {
			lastStatus = resp.StatusCode
			lastErr = nil
			if resp.StatusCode == 504 {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timeout route: %v", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	if lastErr != nil {
		t.Fatalf("timeout route: never observed 504 within %s: %v", routeLiveTimeout, lastErr)
	}
	t.Fatalf("timeout route: never observed 504 within %s (last status: %d; backend delays 5s, timeout is 2s)", routeLiveTimeout, lastStatus)
}

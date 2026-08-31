//go:build e2e

package httproute

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
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
//
// Getting a real 200 needs a health check. Envoy Gateway's `fallback:
// true` puts the fallback backend at Envoy priority 1, which only takes
// traffic once priority 0 (the primary) has zero HEALTHY hosts. Without
// any active or passive health checking, Envoy assumes every host is
// healthy by default, so it keeps sending 100% of traffic at the dead
// primary forever and every request ends in 503 (upstream connect error)
// -- it never fails over.
//
// This uses an ACTIVE (not passive/outlier-detection) health check:
//   - The primary refuses the TCP connection outright (nothing listens on
//     :8080), so a bare TCP active check against it fails immediately and
//     deterministically on the very first probe. Passive/outlier
//     detection instead needs enough *actual failed requests* driven
//     through the dead port before it trips -- slower and, worse, tied to
//     traffic patterns rather than the backend's real state.
//   - It also sidesteps a real footgun in the passive approach: a
//     connection refusal is a *local origin* failure (Envoy never even
//     gets a response to classify), not an external 5xx from the
//     upstream. Outlier detection's consecutiveLocalOriginFailures
//     counter is the one guaranteed to count that, not
//     consecutive5xxErrors as used by the passive sibling test -- get
//     that knob wrong and the primary would never trip. Active health
//     checking has no such ambiguity: a failed TCP connect is always a
//     failed check, independent of any 5xx/local-origin classification.
//
// UnhealthyThreshold: 1 with a fast Interval/Timeout so the primary is
// ejected within a couple of seconds of the route going live, not
// minutes.
//
// PanicThreshold: 0 is required regardless of active vs. passive. Each
// priority here has exactly one host (one backendRef each), so once the
// primary is marked unhealthy its priority's health is exactly 0%, which
// is below Envoy's default 50% panic threshold. Panic mode would then
// have Envoy treat all hosts in that priority -- including the dead one
// -- as usable and keep sending it traffic instead of overflowing to the
// fallback priority. Disabling panic mode is what makes "0% healthy"
// actually mean "send everything to the next priority" instead of
// "route to the dead host anyway." (Same fix, and same underlying
// reason, as health_check_passive_test.go's MaxEjectionPercent/
// PanicThreshold pairing.)
func TestBackendFailover(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)
	timeoutS := "1s"
	intervalS := "2s"
	unhealthyThreshold := uint32(1)
	healthyThreshold := uint32(1)
	panicThreshold := uint32(0)

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
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			HealthCheck: &models.HealthCheckConfig{
				Active: &models.ActiveHealthCheckConfig{
					Type:               "TCP",
					Timeout:            &timeoutS,
					Interval:           &intervalS,
					UnhealthyThreshold: &unhealthyThreshold,
					HealthyThreshold:   &healthyThreshold,
				},
				PanicThreshold: &panicThreshold,
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	// The primary is dead from the moment the route goes live, so this
	// may well return once WaitForRouteLive's own grace window elapses
	// with a non-200 status -- that is expected, and handled by the
	// polling loop below rather than here. All this call proves is that
	// the route itself got programmed (as opposed to a persistent 404).
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("backend failover: route never became live: %v", err)
	}

	// Poll until the active health check ejects the primary and traffic
	// actually fails over to the fallback (200) -- the one outcome that
	// proves failover really happened, as opposed to asserting
	// "200 or 503" and hoping for the best.
	deadline := time.Now().Add(60 * time.Second)
	var last *harness.Response
	var err error
	for time.Now().Before(deadline) {
		last, err = env.GW.HTTP(ctx, "GET", path)
		if err == nil && last.StatusCode == 200 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	gotStatus := -1
	if last != nil {
		gotStatus = last.StatusCode
	}
	t.Fatalf("backend failover: got status %d, want eventual 200 (fallback backend should have served the request) within 60s (last error: %v)", gotStatus, err)
}

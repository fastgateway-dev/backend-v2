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

// TestHealthCheckPassive ports test_health_check_passive.py.
//
// The Python config only ever probed a plain nginx backend and asserted
// status in (200, 404), so passive (outlier-detection) ejection was never
// actually exercised. This port drives the route's traffic straight at
// podinfo's /status/500 (via urlRewrite) with a low consecutive5xxErrors
// threshold, then polls until the response itself flips to 503 -- proof
// Envoy actually ejected the (single-replica) backend host rather than
// continuing to forward and get 500s back.
//
// Two Envoy Gateway defaults make ejecting a single-replica backend
// impossible without overriding them:
//   - maxEjectionPercent defaults to 10, so floor(1 host * 10%) = 0 hosts
//     may ever be ejected, no matter how many consecutive 5xxs it returns.
//   - panicThreshold defaults to 50%: even once 100% of hosts in the
//     cluster are marked unhealthy, Envoy's panic mode kicks in and load
//     balances across ALL hosts (healthy or not) rather than failing
//     closed.
//
// So this sets MaxEjectionPercent: 100 (allow ejecting the one and only
// host) and PanicThreshold: 0 (disable panic mode so ejecting that last
// host actually removes it from the load-balancing set instead of Envoy
// papering over 100% unhealthy). podinfo itself is kept at 1 replica --
// scaling it is out of scope here since other tests in this package share
// the same deployment and run in parallel within the package (see
// podinfoMu above).
func TestHealthCheckPassive(t *testing.T) {
	t.Parallel()
	podinfoMu.Lock()
	defer podinfoMu.Unlock()

	name, path := uniquePath(t)
	consecutive5xx := uint32(3)
	intervalS := "5s"
	baseEjectS := "10s"
	maxEjectionPercent := int32(100)
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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/status/500"),
		},
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
			HealthCheck: &models.HealthCheckConfig{
				Passive: &models.PassiveHealthCheckConfig{
					Consecutive5xxErrors: &consecutive5xx,
					Interval:             &intervalS,
					BaseEjectionTime:     &baseEjectS,
					MaxEjectionPercent:   &maxEjectionPercent,
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
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("health check passive: route never became live: %v", err)
	}

	// Drive enough consecutive 5xx responses to trip outlier detection
	// (consecutive5xxErrors=3 above); podinfo has a single replica, so
	// ejecting its one host leaves zero healthy upstreams and Envoy
	// answers 503 itself instead of forwarding.
	deadline := time.Now().Add(60 * time.Second)
	var last *harness.Response
	var err error
	for time.Now().Before(deadline) {
		last, err = env.GW.HTTP(ctx, "GET", path)
		if err == nil && last.StatusCode == 503 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	gotStatus := -1
	if last != nil {
		gotStatus = last.StatusCode
	}
	t.Fatalf("health check passive: got status %d after driving /status/500, want eventual 503 (outlier ejection) within 60s (last error: %v)", gotStatus, err)
}

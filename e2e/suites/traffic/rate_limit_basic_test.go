//go:build e2e

package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
)

// TestRateLimitBasic ports rate_limiting/test_basic.py:
// test_rate_limit_enforced. Already a real assertion in the Python source
// (assert got_429, not a status-membership tautology); ported unchanged in
// spirit, using harness.UniqueName for the route instead of the fixed
// "reg-ratelimit-basic".
//
// harness.WaitForRouteLive only proves the HTTPRoute itself is live; the
// BackendTrafficPolicy carrying the rate limit is deployed as a separate
// Kubernetes object that reconciles independently (RouteService's
// CreateHTTPRoute then deployBackendTrafficPolicy), so there is a real
// window where the route serves traffic completely un-rate-limited -- a
// burst run in that window sees straight 200s and this test would fail
// even though the feature was about to start working moments later. This
// port therefore also gates on the BTP itself being Accepted (which the
// unconverged state cannot satisfy), and then retries the burst across
// any brief remaining xDS-push tail rather than giving it only one shot
// (see harness.WaitForPolicyAccepted's doc comment on why "Accepted"
// alone still isn't a guarantee).
func TestRateLimitBasic(t *testing.T) {
	t.Parallel()

	_, path, cfg := backendRouteConfig(t)
	cfg.BackendTrafficPolicy = &routeplan.BackendTrafficPolicyInput{
		RateLimit: &models.RateLimitConfig{
			Global: &models.GlobalRateLimitConfig{
				Rules: []models.RateLimitRule{{Limit: models.RateLimitValue{Requests: 3, Unit: "Minute"}}},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("rate limit basic: route never became live: %v", err)
	}

	if err := harness.WaitForPolicyAccepted(ctx, env.Kube, harness.BackendTrafficPolicyGVR, env.Cfg.Namespace, route.ID.String(), routeLiveTimeout); err != nil {
		t.Fatalf("rate limit basic: BackendTrafficPolicy never accepted: %v", err)
	}

	got429 := false
	const burst = 10
	deadline := time.Now().Add(routeLiveTimeout)
	for !got429 && time.Now().Before(deadline) {
		for i := 0; i < burst; i++ {
			resp, err := env.GW.HTTP(ctx, "GET", path)
			if err != nil {
				t.Fatalf("rate limit basic: request %d: %v", i, err)
			}
			if resp.StatusCode == 429 {
				got429 = true
				break
			}
		}
		if !got429 {
			select {
			case <-ctx.Done():
				t.Fatalf("rate limit basic: %v", ctx.Err())
			case <-time.After(2 * time.Second):
			}
		}
	}
	if !got429 {
		t.Fatalf("rate limit basic: no request among repeated bursts of %d got 429 within %s after exceeding the 3/minute limit", burst, routeLiveTimeout)
	}
}

//go:build e2e

package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestRateLimitBasic ports rate_limiting/test_basic.py:
// test_rate_limit_enforced. Already a real assertion in the Python source
// (assert got_429, not a status-membership tautology); ported unchanged in
// spirit, using harness.UniqueName for the route instead of the fixed
// "reg-ratelimit-basic".
func TestRateLimitBasic(t *testing.T) {
	t.Parallel()

	_, path, cfg := backendRouteConfig(t)
	cfg.BackendTrafficPolicy = &services.BackendTrafficPolicyInput{
		RateLimit: &models.RateLimitConfig{
			Global: &models.GlobalRateLimitConfig{
				Rules: []models.RateLimitRule{{Limit: models.RateLimitValue{Requests: 3, Unit: "Minute"}}},
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
		t.Fatalf("rate limit basic: route never became live: %v", err)
	}

	got429 := false
	const burst = 10
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
		t.Fatalf("rate limit basic: no request among %d got 429 after exceeding the 3/minute limit", burst)
	}
}

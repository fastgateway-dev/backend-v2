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

// TestRateLimitHeaderBased ports rate_limiting/test_header_based.py:
// test_rate_limit_header_based. Already a real assertion in the Python
// source (assert got_429); ported unchanged in spirit, using
// harness.UniqueName instead of the fixed "reg-ratelimit-header".
func TestRateLimitHeaderBased(t *testing.T) {
	t.Parallel()

	_, path, cfg := backendRouteConfig(t)
	cfg.BackendTrafficPolicy = &routeplan.BackendTrafficPolicyInput{
		RateLimit: &models.RateLimitConfig{
			Global: &models.GlobalRateLimitConfig{
				Rules: []models.RateLimitRule{{
					ClientSelectors: []models.RateLimitSelector{{
						Headers: []models.RateLimitHeaderMatch{{Name: "x-user-tier", Value: "free", Type: "Exact"}},
					}},
					Limit: models.RateLimitValue{Requests: 3, Unit: "Minute"},
				}},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-user-tier", "free"))
	}
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("rate limit header based: route never became live: %v", err)
	}

	got429 := false
	const burst = 10
	for i := 0; i < burst; i++ {
		resp, err := env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-user-tier", "free"))
		if err != nil {
			t.Fatalf("rate limit header based: request %d: %v", i, err)
		}
		if resp.StatusCode == 429 {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatalf("rate limit header based: no request among %d got 429 after exceeding the 3/minute limit for x-user-tier=free", burst)
	}
}

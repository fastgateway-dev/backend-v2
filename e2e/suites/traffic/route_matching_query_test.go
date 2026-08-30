//go:build e2e

package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// TestQueryParamMatch ports route_matching/test_query_parameters.py:
// test_query_param_match, replacing the tautological "assert
// resp.status_code in (200, 404)" with two genuine assertions per
// task-15-brief ("matching request -> 200; non-matching -> 404"). Unlike
// route_matching_headers_test.go/route_matching_method_test.go -- whose
// Python originals used SEPARATE hit/miss route configs, ported here as
// separate hit/miss Go tests to keep this package's test count at 16 --
// the Python source has only ONE query-parameter test function, so both
// halves are asserted against the SAME route: the matching request
// (?v=2) proves the route is live via harness.WaitForRouteLive first,
// and only then is the non-matching request (no query string) trusted as
// a genuine 404 rather than "not live yet" -- the same "positive before
// negative" ordering e2e/suites/security's package doc comment describes.
func TestQueryParamMatch(t *testing.T) {
	t.Parallel()

	_, path, cfg := backendRouteConfig(t)
	cfg.Config.Matches[0].QueryParams = []models.QueryParamMatch{{Name: "v", Type: "Exact", Value: "2"}}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	hitProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path+"?v=2")
	}
	hitResp, err := harness.WaitForRouteLive(ctx, hitProbe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("query param match: route never became live: %v", err)
	}
	if hitResp.StatusCode != 200 {
		t.Fatalf("query param match: matching request (?v=2) got status %d, want 200 (body: %s)", hitResp.StatusCode, truncate(hitResp.Body, 300))
	}

	missResp, err := env.GW.HTTP(ctx, "GET", path)
	if err != nil {
		t.Fatalf("query param match: non-matching request: %v", err)
	}
	if missResp.StatusCode != 404 {
		t.Fatalf("query param match: non-matching request (no query string) got status %d, want 404 (body: %s)", missResp.StatusCode, truncate(missResp.Body, 300))
	}
}

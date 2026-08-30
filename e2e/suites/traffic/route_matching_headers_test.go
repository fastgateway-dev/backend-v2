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

// headerMatchRoute builds a route matched by both a unique path prefix AND
// the header x-match=yes (Exact), mirroring
// route_matching/test_headers.py's HIT_CONFIG/MISS_CONFIG shape (the two
// Python configs differ only in path; this helper is shared by both Go
// tests below since the match rule itself is identical).
func headerMatchRoute(t *testing.T) (name, path string, cfg services.CreateRouteInput) {
	t.Helper()
	name, path, cfg = backendRouteConfig(t)
	cfg.Config.Matches[0].Headers = []models.HeaderMatch{{Name: "x-match", Type: "Exact", Value: "yes"}}
	return name, path, cfg
}

// TestHeaderMatchHit ports route_matching/test_headers.py:
// test_header_match_hit, replacing the tautological "assert
// resp.status_code in (200, 404)" with a genuine 200: a request carrying
// the required x-match: yes header must match the route.
func TestHeaderMatchHit(t *testing.T) {
	t.Parallel()

	_, path, cfg := headerMatchRoute(t)
	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-match", "yes"))
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("header match hit: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("header match hit: got status %d, want 200 (body: %s)", resp.StatusCode, truncate(resp.Body, 300))
	}
}

// TestHeaderMatchMiss ports route_matching/test_headers.py:
// test_header_match_miss, already a real assertion in the Python source
// (assert resp.status_code == 404). Since this route's match NEVER
// succeeds without the required header, harness.WaitForRouteLive cannot be
// used for readiness here -- it treats every 404 as "not programmed yet"
// and would spin for its entire timeout. Instead this waits for the
// route's own HTTPRoute object to report Accepted/ResolvedRefs=True at the
// CONTROL-plane level (harness.WaitForHTTPRouteAccepted), which is
// independent of whether any particular request would match it, then
// issues a single miss probe: at that point a 404 is a genuine "header
// didn't match" rejection, not "route not live yet".
func TestHeaderMatchMiss(t *testing.T) {
	t.Parallel()

	_, path, cfg := headerMatchRoute(t)
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	if err := harness.WaitForHTTPRouteAccepted(ctx, env.Kube, env.Cfg.Namespace, route.ID.String(), routeLiveTimeout); err != nil {
		t.Fatalf("header match miss: route never became accepted: %v", err)
	}

	resp, err := env.GW.HTTP(ctx, "GET", path)
	if err != nil {
		t.Fatalf("header match miss: request: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("header match miss: got status %d, want 404 (request had no x-match header, body: %s)", resp.StatusCode, truncate(resp.Body, 300))
	}
}

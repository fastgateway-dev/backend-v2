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

// podinfoService and podinfoPort address the shared "podinfo" Deployment
// (see e2e/deps/podinfo.yaml) directly -- unlike this package's other
// tests, TestMethodMatchPost cannot use nginx as its backend (nginx's
// static index page only serves GET/HEAD and answers a real POST with a
// genuine 405, which is not a route-matching failure at all). podinfo's
// "/status/{code}" endpoint accepts GET, POST, and PUT and always answers
// with the requested code, so it can prove a POST actually reached the
// backend and got a real 200.
const (
	podinfoService = "podinfo"
	podinfoPort    = 9898
)

// TestMethodMatchPost ports route_matching/test_method.py:
// test_method_match_post. The Python original asserted `status_code in
// (200, 404, 405)` -- three co-accepted outcomes retry_until already
// guaranteed, the same tautology shape task-15-brief calls out elsewhere
// in this package (not itself named in the brief's table, but the same
// "central rule" violation, so fixed here too: never transcribe a
// tautology). A route matched by method=POST, given an actual POST
// request, must resolve to a genuine 200. nginx's static index page
// rejects POST with a real 405 (it only serves GET/HEAD), which would
// make this test indistinguishable from a broken method match, so this
// port targets podinfo's "/status/200" instead (see podinfoService/
// podinfoPort above) -- a POST there reaches statusHandler exactly like a
// GET would and gets back a genuine, backend-issued 200.
func TestMethodMatchPost(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)
	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}, Method: "POST"},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/status/200"),
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "POST", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("method match post: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("method match post: got status %d, want 200 (body: %s)", resp.StatusCode, truncate(resp.Body, 300))
	}
}

// TestMethodMatchGetMiss ports route_matching/test_method.py:
// test_method_match_get_miss, already a real assertion in the Python
// source (assert resp.status_code == 404). Like TestHeaderMatchMiss, this
// route's match never succeeds for a GET (it requires POST), so readiness
// is proven at the control-plane level
// (harness.WaitForHTTPRouteAccepted) instead of via
// harness.WaitForRouteLive, which would spin on the permanent 404.
func TestMethodMatchGetMiss(t *testing.T) {
	t.Parallel()

	_, path, cfg := backendRouteConfig(t)
	cfg.Config.Matches[0].Method = "POST"

	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	if err := harness.WaitForHTTPRouteAccepted(ctx, env.Kube, env.Cfg.Namespace, route.ID.String(), routeLiveTimeout); err != nil {
		t.Fatalf("method match get miss: route never became accepted: %v", err)
	}

	resp, err := env.GW.HTTP(ctx, "GET", path)
	if err != nil {
		t.Fatalf("method match get miss: request: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("method match get miss: got status %d, want 404 (route requires POST, request was GET, body: %s)", resp.StatusCode, truncate(resp.Body, 300))
	}
}

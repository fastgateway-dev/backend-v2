//go:build e2e

package security

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// wafPolicy builds the shared OWASP CRS config used by both WAF tests,
// matching security_general_mode/test_waf.py's BLOCK_CONFIG/ALLOW_CONFIG
// (identical wafPolicy settings in both -- mode "block", ruleset
// "owasp-crs", paranoia level 2, anomaly threshold 5).
func wafPolicy() *services.WafPolicyInput {
	anomalyThreshold := 5
	paranoiaLevel := 2
	return &services.WafPolicyInput{
		Mode:             "block",
		Rulesets:         []string{"owasp-crs"},
		ParanoiaLevel:    &paranoiaLevel,
		AnomalyThreshold: &anomalyThreshold,
	}
}

// TestGeneralModeWAFAllowsNormalRequest ports
// security_general_mode/test_waf.py:test_waf_allows_normal_request.
//
// The Python original tolerated `resp.status_code in (200, 404)` -- per
// the package doc comment, a WAF misconfigured to block (or a route that
// never deployed) could not be told apart from one working correctly
// through that assertion. This port uses rewriteTo("/") to get an
// unambiguous 200 from nginx and asserts exactly that, matching the
// brief's "benign request -> 200" requirement.
func TestGeneralModeWAFAllowsNormalRequest(t *testing.T) {
	t.Parallel()

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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
		},
		WafPolicy: wafPolicy(),
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("waf: benign request: %v", err)
	}
}

// TestGeneralModeWAFBlocksSQLInjection ports
// security_general_mode/test_waf.py:test_waf_blocks_sql_injection -- this
// is one of the 8 originally-tautological tests task-13-brief names
// explicitly (the "8 tautologies": all 7 client_mode tests plus this WAF
// test). The Python original never sent an attack payload at all; its
// request was a plain GET with a raw `?id=1' OR '1'='1` query string
// under `retry_until(accepted_status=[403])`, but WITHOUT ever also
// checking that a BENIGN request on the same policy still gets through --
// so a WAF rule blocking EVERYTHING (not just SQLi) would have passed
// this test identically. This port:
//
//  1. First proves the route (and WAF policy) is live and passing normal
//     traffic with a benign GET -> 200, on the SAME route -- so the
//     subsequent SQLi probe's 403 can only be explained by the WAF
//     actually recognizing and blocking the attack pattern, not by a
//     policy that blocks everything or a route that isn't live at all
//     (see the package doc comment's "positive before negative" ordering
//     discipline).
//  2. Sends a real SQL-injection probe in the query string
//     (?id=1%27%20OR%20%271%27=%271, i.e. `1' OR '1'='1` percent-encoded)
//     and asserts exactly 403.
func TestGeneralModeWAFBlocksSQLInjection(t *testing.T) {
	t.Parallel()

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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
		},
		WafPolicy: wafPolicy(),
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	benignProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	if _, err := waitForHTTPStatus(ctx, benignProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("waf: benign request (establishing liveness before the attack probe): %v", err)
	}

	sqliProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path+"?id=1%27%20OR%20%271%27=%271")
	}
	requireStatus(t, ctx, sqliProbe, 403)
}

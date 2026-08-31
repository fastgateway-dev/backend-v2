//go:build e2e

package security

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// wafPolicy builds the shared OWASP CRS config used by both WAF tests,
// matching security_general_mode/test_waf.py's BLOCK_CONFIG/ALLOW_CONFIG
// (identical wafPolicy settings in both -- mode "block", ruleset
// "owasp-crs", paranoia level 2, anomaly threshold 5).
func wafPolicy() *routeplan.WafPolicyInput {
	anomalyThreshold := 5
	paranoiaLevel := 2
	return &routeplan.WafPolicyInput{
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
//  1. First proves the SQLi probe itself reaches its expected 403 -- NOT
//     the benign probe. The WAF's EnvoyExtensionPolicy has no effect on a
//     benign request either way, so a route whose HTTPRoute has landed
//     but whose WAF policy hasn't converged yet returns 200 to a benign
//     GET exactly as readily as one that's actively blocking -- a benign
//     200 proves nothing about whether the WAF is even attached, because
//     that identical unconverged state also produces the SQLi probe
//     wrongly returning 200. Only a 403 on the SQLi probe can prove the
//     WAF policy has actually converged and is recognizing the attack
//     pattern, so that is what this test gates on.
//  2. Sends a real SQL-injection probe in the query string
//     (?id=1%27%20OR%20%271%27=%271, i.e. `1' OR '1'='1` percent-encoded).
//  3. Only once that 403 is observed does it check a benign GET on the
//     SAME route still gets through with 200 -- proving the WAF is
//     recognizing the attack specifically, not blocking everything.
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

	// Gate on the attack probe reaching its denial, not on a benign
	// request reaching 200 -- see the doc comment above for why a benign
	// 200 can't distinguish a converged, enforcing WAF policy from one
	// that hasn't attached yet.
	sqliProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path+"?id=1%27%20OR%20%271%27=%271")
	}
	if _, err := waitForHTTPStatus(ctx, sqliProbe, routeLiveTimeout, 403); err != nil {
		t.Fatalf("waf: sql injection probe (establishing WAF enforcement liveness): %v", err)
	}

	// Now that the SQLi probe has proven the WAF policy is genuinely
	// enforcing, a benign request still reaching 200 proves it isn't
	// blocking everything (a rule broad enough to reject all traffic would
	// also 403 here).
	benignProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	// Bounded poll, not a single call. The negative above already proved
	// the policy is enforcing, so this cannot pass by catching an
	// unconverged route -- but a lone call can still lose a race the
	// enforcement gate does not cover. Envoy fetches a JWKS lazily on
	// first use and answers 401 "Jwks remote fetch is failed" while that
	// fetch is in flight, which is exactly how this failed in CI. A
	// credential that is never accepted still fails, at the timeout.
	if _, err := waitForHTTPStatus(ctx, benignProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("waf: benign request: %v", err)
	}
}

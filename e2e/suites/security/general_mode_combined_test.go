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

// combinedSecurityPolicy builds the shared CORS+IP-authorization config
// used by TestGeneralModeCombinedCORSPreflight and
// TestGeneralModeCombinedIPAuthorization, parameterized on the allowed
// CIDR -- matching security_general_mode/test_combined.py's
// CORS_PREFLIGHT_CONFIG and IP_DENY_CONFIG (which, in the Python source,
// carry identical securityPolicy content; only the allowedCIDRs value
// differs between what this port treats as its "excluded" vs "allow-all"
// variant).
func combinedSecurityPolicy(allowedCIDRs []string) *services.SecurityPolicyInput {
	maxAge := 3600
	return &services.SecurityPolicyInput{
		CORS: &models.CORSConfig{
			AllowOrigins: []string{"https://example.com"},
			AllowMethods: []string{"GET"},
			AllowHeaders: []string{"Content-Type"},
			MaxAge:       &maxAge,
		},
		Authorization: &services.AuthorizationInput{AllowedCIDRs: allowedCIDRs},
	}
}

// TestGeneralModeCombinedCORSPreflight ports
// security_general_mode/test_combined.py:test_combined_cors_preflight_passes.
// Already had a real assertion (the response header) in the Python
// source; transcribed as-is. The OPTIONS preflight is answered by Envoy's
// CORS filter without ever reaching the backend or being subject to the
// IP authorization rule, so no urlRewrite is needed and the CIDR here
// (192.168.1.0/24, which the test runner can never match) is irrelevant
// to this specific test -- it exists only because the Python source's
// CORS_PREFLIGHT_CONFIG combines it with CORS in a single securityPolicy.
func TestGeneralModeCombinedCORSPreflight(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

	cfg := services.CreateRouteInput{
		Name:         name,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
		},
		SecurityPolicy: combinedSecurityPolicy([]string{"192.168.1.0/24"}),
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "OPTIONS", path,
			harness.WithHeader("Origin", "https://example.com"),
			harness.WithHeader("Access-Control-Request-Method", "GET"),
		)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("combined cors preflight: route never became live: %v", err)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("combined cors preflight: got Access-Control-Allow-Origin %q, want %q", got, "https://example.com")
	}
}

// TestGeneralModeCombinedIPAuthorization ports
// security_general_mode/test_combined.py:test_combined_ip_denied, and
// strengthens it per the package doc comment's general "every test
// asserts both cases" requirement: the Python source only ever checked
// the IP_DENY_CONFIG route (a CIDR the test runner can never match), which
// proves CORS+authorization compose to deny, but not that they compose to
// ALLOW when the CIDR does match (a route that denies everything
// regardless of CORS/IP would pass an IP_DENY_CONFIG-only test
// identically). This port adds a second route with the same CORS config
// but an allow-all CIDR (0.0.0.0/0), mirroring
// TestGeneralModeIPAllowlisting's dual-route pattern, to prove the
// combination genuinely lets matching traffic through too.
func TestGeneralModeCombinedIPAuthorization(t *testing.T) {
	t.Parallel()

	fx := harness.NewFixture(t, env)
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	allowName, allowPath := uniquePath(t)
	allowCfg := services.CreateRouteInput{
		Name:         allowName,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: allowPath}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
		},
		SecurityPolicy: combinedSecurityPolicy([]string{"0.0.0.0/0"}),
	}
	fx.Route(allowCfg)

	// Positive first: proves the CORS+authorization combination is live
	// and converged before the deny route's result is trusted (see the
	// package doc comment).
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", allowPath, harness.WithHeader("Origin", "https://example.com"))
	}
	resp, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200)
	if err != nil {
		t.Fatalf("combined ip authorization: allow-all CIDR (0.0.0.0/0): %v", err)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("combined ip authorization: got Access-Control-Allow-Origin %q, want %q", got, "https://example.com")
	}

	denyName, denyPath := uniquePath(t)
	denyCfg := services.CreateRouteInput{
		Name:         denyName,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: denyPath}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
		},
		SecurityPolicy: combinedSecurityPolicy([]string{"192.168.1.0/24"}),
	}
	fx.Route(denyCfg)

	denyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", denyPath)
	}
	if _, err := waitForHTTPStatus(ctx, denyProbe, routeLiveTimeout, 403); err != nil {
		t.Fatalf("combined ip authorization: CIDR that can't match the test runner: %v", err)
	}
}

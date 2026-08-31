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

// TestGeneralModeIPAllowlisting ports
// security_general_mode/test_ip_allowlisting.py:test_ip_denied.
//
// The Python original only ever checked the denied case (a CIDR the test
// runner's real IP can never satisfy) -- per the package doc comment's
// "passes for the wrong reason" discussion, that alone doesn't prove the
// allowlist is enforced rather than, say, the route having simply failed
// to deploy (a completely broken route would 404 forever, and a broken
// retry_until would eventually surface that as a failure, but a route
// that denies EVERYTHING regardless of IP would pass this check
// identically to one that correctly enforces the CIDR). This port adds a
// second route with an allow-all CIDR (0.0.0.0/0, mirroring
// e2e/suites/grpcroute/security_ip_allowlisting_test.go's identical
// dual-route pattern for the gRPC port of the same mechanism) to prove
// the same authorization mechanism actually allows traffic when the CIDR
// matches, not just that it can deny.
//
// Two separate routes (rather than reconfiguring one) sidesteps any
// redeploy-ordering race between the positive and negative checks: each
// route's own liveness is established independently via
// waitForHTTPStatus before its result is trusted.
func TestGeneralModeIPAllowlisting(t *testing.T) {
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
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			Authorization: &routeplan.AuthorizationInput{AllowedCIDRs: []string{"0.0.0.0/0"}},
		},
	}
	fx.Route(allowCfg)

	// Positive first, per the package doc comment: an allow-all CIDR
	// reaching a real 200 proves the authorization mechanism is live and
	// converged before the deny route's result is trusted.
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", allowPath)
	}
	if _, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("ip allowlisting: allow-all CIDR (0.0.0.0/0): %v", err)
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
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			// 192.0.2.0/24 is TEST-NET-1 (RFC 5737): reserved for
			// documentation, guaranteed to never be a real client IP.
			Authorization: &routeplan.AuthorizationInput{AllowedCIDRs: []string{"192.0.2.0/24"}},
		},
	}
	fx.Route(denyCfg)

	denyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", denyPath)
	}
	if _, err := waitForHTTPStatus(ctx, denyProbe, routeLiveTimeout, 403); err != nil {
		t.Fatalf("ip allowlisting: CIDR that can't match the test runner: %v", err)
	}
}

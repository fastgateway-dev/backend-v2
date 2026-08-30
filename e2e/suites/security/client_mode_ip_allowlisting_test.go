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

// TestClientModeIPAllowlisting ports client_mode/test_ip_allowlisting.py,
// fixing the tautology task-13-brief names explicitly, and strengthens the
// negative case beyond what the Python source checked.
//
// This does NOT use the single-route, two-client design most other
// client-mode tests in this package follow (attach one client, probe
// positive; attach a second, probe negative on the SAME route
// discriminated by x-client-id). That idiom only works for auth
// mechanisms with a per-client discriminator -- API key, JWT, mTLS each
// get their own per-client route (an "-ak-<id8>"-suffixed GRPCRoute/
// HTTPRoute plus SecurityPolicy; see internal/services/route_service.go's
// per-client route generation). IP allowlisting has none of that: per
// internal/services/route_service.go's collectClientIPCIDRs (~:2707) and
// buildClientIPAuthorizationConfig (~:2580), every base-route client with
// EnableIPAllowlist attached to a route is flattened into ONE Allow rule
// on that route's own SecurityPolicy, with no per-client discriminator at
// all -- x-client-id is inert for this mechanism. Attaching both an
// allow-all (0.0.0.0/0) and an excluded (192.0.2.0/24) client to the same
// route (the original, broken version of this test) produces the rule
// ["0.0.0.0/0", "192.0.2.0/24"], which allows everything: the excluded
// CIDR is never actually exercised as a denial.
//
// So this instead follows the two-ROUTE idiom used by
// security/general_mode_ip_allowlisting_test.go and
// grpcroute/security_ip_allowlisting_test.go for the same reason (IP
// membership can't be toggled by a request header the way a bearer token
// can): route A gets only clientAllow (CIDR 0.0.0.0/0, which the test
// runner's real IP always matches) attached, and route B gets only
// clientDeny (CIDR 192.0.2.0/24, TEST-NET-1 per RFC 5737, which it never
// matches) attached. Each route's rule then genuinely reflects just its
// own client, so route B's 403 actually proves the excluded CIDR is
// enforced -- not just that x-client-id was missing or unrecognized.
func TestClientModeIPAllowlisting(t *testing.T) {
	t.Parallel()

	fx := harness.NewFixture(t, env)
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	allowName, allowPath := uniquePath(t)
	allowCfg := services.CreateRouteInput{
		Name:         allowName,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: allowPath}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
		},
	}
	allowRoute := fx.Route(allowCfg)

	clientAllow, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("client mode ip allowlisting: create allow client: %v", err)
	}
	cleanupClient(t, clientAllow.ID.String())
	if err := addClientIP(ctx, clientAllow.ID.String(), "0.0.0.0/0", "allow all for testing"); err != nil {
		t.Fatalf("client mode ip allowlisting: add allow-all CIDR: %v", err)
	}
	if _, err := attachAndDeploy(ctx, allowRoute.ID.String(), services.AttachFromRouteInput{
		ClientID:          clientAllow.ID,
		EnableIPAllowlist: true,
	}); err != nil {
		t.Fatalf("client mode ip allowlisting: attach allow client: %v", err)
	}

	// Positive first, per the package doc comment: proves the allow route
	// AND clientAllow's attachment have converged before the deny route's
	// result is trusted.
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", allowPath, harness.WithHeader("x-client-id", clientAllow.ID.String()))
	}
	if _, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("client mode ip allowlisting: allow-all CIDR (0.0.0.0/0): %v", err)
	}

	denyName, denyPath := uniquePath(t)
	denyCfg := services.CreateRouteInput{
		Name:         denyName,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: denyPath}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
		},
	}
	denyRoute := fx.Route(denyCfg)

	clientDeny, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("client mode ip allowlisting: create deny client: %v", err)
	}
	cleanupClient(t, clientDeny.ID.String())
	if err := addClientIP(ctx, clientDeny.ID.String(), "192.0.2.0/24", "excluded CIDR for testing"); err != nil {
		t.Fatalf("client mode ip allowlisting: add excluded CIDR: %v", err)
	}
	if _, err := attachAndDeploy(ctx, denyRoute.ID.String(), services.AttachFromRouteInput{
		ClientID:          clientDeny.ID,
		EnableIPAllowlist: true,
	}); err != nil {
		t.Fatalf("client mode ip allowlisting: attach deny client: %v", err)
	}

	// The deny route is a separate route from the allow route above, so
	// its own liveness has never been separately established -- poll for
	// the wanted 403 rather than asserting on a single probe, the same
	// way general_mode_ip_allowlisting_test.go's deny route does.
	denyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", denyPath, harness.WithHeader("x-client-id", clientDeny.ID.String()))
	}
	if _, err := waitForHTTPStatus(ctx, denyProbe, routeLiveTimeout, 403); err != nil {
		t.Fatalf("client mode ip allowlisting: excluded CIDR (192.0.2.0/24): %v", err)
	}
}

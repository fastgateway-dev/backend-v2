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
// The Python original only ever attached ONE client with an allow-all
// CIDR (0.0.0.0/0) and asserted the resulting request succeeds -- an
// allow-only check that a route letting EVERYTHING through, IP-restricted
// or not, would pass identically (see the package doc comment). Per
// task-13-brief's table ("excluded CIDR -> 403"), this port attaches TWO
// clients to the SAME route: clientAllow (CIDR 0.0.0.0/0, which the test
// runner's real IP always matches) and clientDeny (CIDR 192.0.2.0/24,
// TEST-NET-1 per RFC 5737, which it never matches). Because IP membership
// can't be toggled by a request header the way an API key or bearer token
// can, this dual-client design is what lets both cases be exercised on
// one route: the positive request identifies as clientAllow, the negative
// request identifies as clientDeny -- a REAL client, attached to a REAL
// route, whose own allowlist genuinely excludes the caller.
func TestClientModeIPAllowlisting(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

	cfg := services.CreateRouteInput{
		Name:         name,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
		},
	}

	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	clientAllow, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("client mode ip allowlisting: create allow client: %v", err)
	}
	cleanupClient(t, clientAllow.ID.String())
	if err := addClientIP(ctx, clientAllow.ID.String(), "0.0.0.0/0", "allow all for testing"); err != nil {
		t.Fatalf("client mode ip allowlisting: add allow-all CIDR: %v", err)
	}
	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:          clientAllow.ID,
		EnableIPAllowlist: true,
	}); err != nil {
		t.Fatalf("client mode ip allowlisting: attach allow client: %v", err)
	}

	clientDeny, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("client mode ip allowlisting: create deny client: %v", err)
	}
	cleanupClient(t, clientDeny.ID.String())
	if err := addClientIP(ctx, clientDeny.ID.String(), "192.0.2.0/24", "excluded CIDR for testing"); err != nil {
		t.Fatalf("client mode ip allowlisting: add excluded CIDR: %v", err)
	}
	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:          clientDeny.ID,
		EnableIPAllowlist: true,
	}); err != nil {
		t.Fatalf("client mode ip allowlisting: attach deny client: %v", err)
	}

	// Positive first: proves the route AND clientAllow's attachment have
	// converged before clientDeny's result is trusted (see the package
	// doc comment).
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-client-id", clientAllow.ID.String()))
	}
	if _, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("client mode ip allowlisting: allow-all CIDR (0.0.0.0/0): %v", err)
	}

	denyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-client-id", clientDeny.ID.String()))
	}
	requireStatus(t, ctx, denyProbe, 403)
}

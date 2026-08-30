//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCClientModeIPAllowlisting ports
// grpc_client_mode/test_ip_allowlisting.py: a client attached with an
// allow-all CIDR (0.0.0.0/0) must let traffic through -- but per
// task-12-brief's "passes for the wrong reason" guidance, an allow-only
// check proves nothing about enforcement (a route that let EVERYTHING
// through, allowlist or not, would pass identically).
//
// Mirrors e2e/suites/security/client_mode_ip_allowlisting_test.go's
// two-client design rather than probing for denial before any client is
// attached: deploySecurityPolicy only emits the deny-all authorization
// (route.Config.DefaultTrafficPolicy) once at least one client is
// attached to the route (see countClientAttachments /
// internal/services/route_service.go's deploySecurityPolicy) -- with zero
// attachments no SecurityPolicy is created at all and the route is wide
// open, so a pre-attachment denial probe would spin until timeout on a
// codes.OK it will never stop seeing. Instead this attaches TWO clients
// to the SAME route -- clientAllow (CIDR 0.0.0.0/0, which the test
// runner's real IP always matches) and clientDeny (CIDR 192.0.2.0/24,
// TEST-NET-1 per RFC 5737, which it never matches) -- and identifies the
// negative probe as clientDeny via x-client-id: a REAL client, attached
// to a REAL route, whose own allowlist genuinely excludes the caller.
func TestGRPCClientModeIPAllowlisting(t *testing.T) {
	t.Parallel()

	routeName, match, routeOpt := uniqueMatch(t, "Exact", echoServiceName, "")

	cfg := services.CreateRouteInput{
		Name:         routeName,
		Protocol:     models.RouteProtocolGRPC,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			Matches:              []models.RouteMatch{match},
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
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
	// converged before clientDeny's result is trusted.
	allowClientOpt := harness.WithGRPCMetadata("x-client-id", clientAllow.ID.String())
	allowProbe := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", routeOpt, allowClientOpt)
		return res, err
	}
	res, err := waitForGRPCCodeIn(ctx, allowProbe, routeLiveTimeout, codes.OK)
	if err != nil {
		t.Fatalf("client mode ip allowlisting: allow-all CIDR (0.0.0.0/0): %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("client mode ip allowlisting: got code %v, want %v", res.Code, codes.OK)
	}

	denyClientOpt := harness.WithGRPCMetadata("x-client-id", clientDeny.ID.String())
	denyProbe := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", routeOpt, denyClientOpt)
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, denyProbe, routeLiveTimeout, codes.PermissionDenied, codes.Unauthenticated); err != nil {
		t.Fatalf("client mode ip allowlisting: excluded CIDR (192.0.2.0/24): %v", err)
	}
}

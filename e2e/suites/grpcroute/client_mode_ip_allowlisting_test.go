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
// This does NOT use the single-route, two-client design most other
// client-mode tests in this package follow (attach one client, probe
// positive; attach a second, probe negative on the SAME route
// discriminated by x-client-id). That idiom only works for auth
// mechanisms with a per-client discriminator -- API key, JWT, mTLS each
// get their own per-client route (an "-ak-<id8>"-suffixed GRPCRoute plus
// SecurityPolicy; see internal/services/route_service.go's per-client
// route generation). IP allowlisting has none of that: per
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
// e2e/suites/security/general_mode_ip_allowlisting_test.go and
// security_ip_allowlisting_test.go in this package (IP membership can't
// be toggled by a request header the way a bearer token can): route A
// gets only clientAllow (CIDR 0.0.0.0/0, which the test runner's real IP
// always matches) attached, and route B gets only clientDeny (CIDR
// 192.0.2.0/24, TEST-NET-1 per RFC 5737, which it never matches)
// attached. Each route's rule then genuinely reflects just its own
// client, so route B's PermissionDenied actually proves the excluded CIDR
// is enforced -- not just that x-client-id was missing or unrecognized.
// Each route also still gets exactly one client attachment, which keeps
// deploySecurityPolicy emitting a real deny-all SecurityPolicy for it
// (see countClientAttachments / internal/services/route_service.go's
// deploySecurityPolicy) rather than leaving the route wide open.
func TestGRPCClientModeIPAllowlisting(t *testing.T) {
	t.Parallel()

	fx := harness.NewFixture(t, env)
	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	allowName, allowMatch, allowRouteOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	allowCfg := services.CreateRouteInput{
		Name:         allowName,
		Protocol:     models.RouteProtocolGRPC,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			Matches:              []models.RouteMatch{allowMatch},
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
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

	// Positive first: proves the allow route AND clientAllow's attachment
	// have converged before the deny route's result is trusted.
	allowClientOpt := harness.WithGRPCMetadata("x-client-id", clientAllow.ID.String())
	allowProbe := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", allowRouteOpt, allowClientOpt)
		return res, err
	}
	res, err := waitForGRPCCodeIn(ctx, allowProbe, routeLiveTimeout, codes.OK)
	if err != nil {
		t.Fatalf("client mode ip allowlisting: allow-all CIDR (0.0.0.0/0): %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("client mode ip allowlisting: got code %v, want %v", res.Code, codes.OK)
	}

	denyName, denyMatch, denyRouteOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	denyCfg := services.CreateRouteInput{
		Name:         denyName,
		Protocol:     models.RouteProtocolGRPC,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			Matches:              []models.RouteMatch{denyMatch},
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
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

	denyClientOpt := harness.WithGRPCMetadata("x-client-id", clientDeny.ID.String())
	denyProbe := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", denyRouteOpt, denyClientOpt)
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, denyProbe, routeLiveTimeout, codes.PermissionDenied, codes.Unauthenticated); err != nil {
		t.Fatalf("client mode ip allowlisting: excluded CIDR (192.0.2.0/24): %v", err)
	}
}

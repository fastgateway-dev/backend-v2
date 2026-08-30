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
// through, allowlist or not, would pass identically). This port also
// establishes the denial side: before the client is attached at all, the
// route's defaultTrafficPolicy "deny" must reject the same request.
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

	probe := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", routeOpt)
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, probe, routeLiveTimeout, codes.PermissionDenied, codes.Unauthenticated); err != nil {
		t.Fatalf("client mode ip allowlisting: before any client attached: %v", err)
	}

	client, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("client mode ip allowlisting: create client: %v", err)
	}
	cleanupClient(t, client.ID.String())

	if err := addClientIP(ctx, client.ID.String(), "0.0.0.0/0", "allow all"); err != nil {
		t.Fatalf("client mode ip allowlisting: add client IP: %v", err)
	}

	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:          client.ID,
		EnableIPAllowlist: true,
	}); err != nil {
		t.Fatalf("client mode ip allowlisting: attach client: %v", err)
	}

	res, err := waitForGRPCCodeIn(ctx, probe, routeLiveTimeout, codes.OK)
	if err != nil {
		t.Fatalf("client mode ip allowlisting: after allow-all CIDR attached: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("client mode ip allowlisting: got code %v, want %v", res.Code, codes.OK)
	}
}

//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/e2e/testdata/pb/echo"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCClientModeCombinedAuth ports
// grpc_client_mode/test_combined_auth.py: a client attached with BOTH IP
// allowlisting (allow-all CIDR) and API key auth. Without credentials the
// call must be denied; with a valid API key + client ID (from the allowed
// IP range, which the test runner always is) it must succeed.
//
// The success path must poll, exactly like the deny path above it: it
// depends on a DIFFERENT object graph converging than the base route does
// -- the per-client "-ak-<id8>" GRPCRoute plus its own SecurityPolicy (see
// the per-client route generation in internal/services/route_service.go)
// -- which can still be reconciling after the base route (and even the
// deny path's own SecurityPolicy) is already live. A single un-polled call
// here previously observed codes.Unavailable, which is in this package's
// own grpcNotReady "route not programmed yet" set -- i.e. exactly what a
// too-early call against a not-yet-programmed per-client route looks like,
// not evidence the feature is broken. The HTTP sibling of this test polls
// its success path for the same reason.
func TestGRPCClientModeCombinedAuth(t *testing.T) {
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

	client, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("client mode combined auth: create client: %v", err)
	}
	cleanupClient(t, client.ID.String())

	apiKey, err := generateClientAPIKey(ctx, client.ID.String(), "x-api-key")
	if err != nil {
		t.Fatalf("client mode combined auth: generate api key: %v", err)
	}
	if err := addClientIP(ctx, client.ID.String(), "0.0.0.0/0", "allow all"); err != nil {
		t.Fatalf("client mode combined auth: add client IP: %v", err)
	}

	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:          client.ID,
		EnableIPAllowlist: true,
		EnableAPIKey:      true,
	}); err != nil {
		t.Fatalf("client mode combined auth: attach client: %v", err)
	}

	denyCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", routeOpt)
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, denyCall, routeLiveTimeout, codes.PermissionDenied, codes.Unauthenticated); err != nil {
		t.Fatalf("client mode combined auth: without credentials: %v", err)
	}

	authOpts := []harness.GRPCOpt{
		routeOpt,
		harness.WithGRPCMetadata("x-api-key", apiKey),
		harness.WithGRPCMetadata("x-client-id", client.ID.String()),
	}
	var authedResp *echo.Message
	authedCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, resp, err := echoCall(ctx, "hello-authed", authOpts...)
		authedResp = resp
		return res, err
	}
	res, err := waitForGRPCCodeIn(ctx, authedCall, routeLiveTimeout, codes.OK)
	if err != nil {
		t.Fatalf("client mode combined auth: request with credentials: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("client mode combined auth: with valid api key + client id (allowed IP) got code %v, want %v", res.Code, codes.OK)
	}
	if authedResp == nil || authedResp.Body != "hello-authed" {
		t.Fatalf("client mode combined auth: got echoed body %+v, want %q", authedResp, "hello-authed")
	}
}

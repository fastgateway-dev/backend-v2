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

// TestGRPCClientModeJWT ports grpc_client_mode/test_jwt.py: a client
// attached with per-client JWT auth. A request carrying only the client-id
// header (no token) must be denied; a request with a valid signed token
// (from the test jwt-server) plus the client-id header must succeed.
func TestGRPCClientModeJWT(t *testing.T) {
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
		t.Fatalf("client mode jwt: create client: %v", err)
	}
	cleanupClient(t, client.ID.String())

	if err := configureClientJWT(ctx, client.ID.String(), jwtIssuerURL(), jwtIssuerURL()+"/jwks", []string{"my-api"}); err != nil {
		t.Fatalf("client mode jwt: configure client JWT: %v", err)
	}

	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:  client.ID,
		EnableJWT: true,
	}); err != nil {
		t.Fatalf("client mode jwt: attach client: %v", err)
	}

	clientIDOpt := harness.WithGRPCMetadata("x-client-id", client.ID.String())
	denyCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", routeOpt, clientIDOpt)
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, denyCall, routeLiveTimeout, codes.PermissionDenied, codes.Unauthenticated); err != nil {
		t.Fatalf("client mode jwt: without a token: %v", err)
	}

	token, err := generateJWTToken(ctx, "my-api")
	if err != nil {
		t.Fatalf("client mode jwt: generate token from jwt-server: %v", err)
	}
	authOpts := []harness.GRPCOpt{routeOpt, clientIDOpt, harness.WithGRPCMetadata("authorization", "Bearer "+token)}
	// Polled for codes.OK rather than called once: the negatives above
	// prove the route and its policy are live, but a single call can still
	// land on a transient codes.Unavailable (Envoy reports "no healthy
	// upstream" while an upstream connection is being (re)established),
	// which a one-shot read turns into a spurious failure. Auth that is
	// genuinely broken answers Unauthenticated/PermissionDenied forever,
	// so the poll still fails in that case.
	var resp *echo.Message
	authedCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, msg, err := echoCall(ctx, "hello-authed", authOpts...)
		resp = msg
		return res, err
	}
	res, err := waitForGRPCResult(ctx, authedCall, func(r *harness.GRPCResult) bool {
		return r.Code == codes.OK
	}, routeLiveTimeout)
	if err != nil {
		got := codes.Code(0)
		if res != nil {
			got = res.Code
		}
		t.Fatalf("client mode jwt: with valid token + client id got code %v, want %v: %v", got, codes.OK, err)
	}
	if resp.Body != "hello-authed" {
		t.Fatalf("client mode jwt: got echoed body %q, want %q", resp.Body, "hello-authed")
	}
}

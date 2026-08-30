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

// TestGRPCJWT ports grpc_security/test_jwt.py.
//
// The Python original only ever checked the denied (no token) case -- per
// task-12-brief's "passes for the wrong reason" guidance, that alone
// doesn't prove JWT validation is enforced rather than, say, the route
// having simply failed to deploy. This port also generates a real signed
// token from the test jwt-server (mirroring grpc_client_mode/test_jwt.py's
// use of the same server) and asserts it's accepted.
func TestGRPCJWT(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")

	cfg := services.CreateRouteInput{
		Name:         name,
		Protocol:     models.RouteProtocolGRPC,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{match},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
		SecurityPolicy: &services.SecurityPolicyInput{
			JWT: &services.JWTInput{
				Issuer:    jwtServerURL(),
				JWKSURL:   jwtServerURL() + "/jwks",
				Audiences: []string{"my-api"},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	denyCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt)
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, denyCall, routeLiveTimeout, codes.Unauthenticated); err != nil {
		t.Fatalf("jwt: without a token: %v", err)
	}

	token, err := generateJWTToken(ctx, "my-api")
	if err != nil {
		t.Fatalf("jwt: generate token from jwt-server: %v", err)
	}
	authOpt := harness.WithGRPCMetadata("authorization", "Bearer "+token)
	res, resp, err := echoCall(ctx, "hello-authed", callOpt, authOpt)
	if err != nil {
		t.Fatalf("jwt: request with valid token: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("jwt: with valid token got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != "hello-authed" {
		t.Fatalf("jwt: got echoed body %q, want %q", resp.Body, "hello-authed")
	}
}

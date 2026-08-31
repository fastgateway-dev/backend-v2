//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/e2e/testdata/pb/echo"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"google.golang.org/grpc/codes"
)

// TestGRPCJWT ports grpc_security/test_jwt.py.
//
// The Python original only ever checked the denied (no token) case -- per
// task-12-brief's "passes for the wrong reason" guidance, that alone
// doesn't prove JWT validation is enforced rather than, say, the route
// having simply failed to deploy. This port also generates a real signed
// token from the test jwt-server (mirroring grpc_client_mode/test_jwt.py's
// use of the same server) and asserts it's accepted.
//
// Gate on the POSITIVE call, not the negative one: Envoy's jwt_authn
// filter returns Unauthenticated for EVERY request -- valid token or none
// at all -- until its first JWKS fetch against jwtIssuerURL() succeeds.
// A denial observed during that cold-start window is indistinguishable
// from a genuinely-enforcing filter correctly rejecting a missing token,
// so polling for Unauthenticated proves nothing about convergence. Only a
// real codes.OK, produced by a valid signed token, proves the filter has
// actually finished fetching JWKS and is validating tokens -- this is the
// gRPC mirror of the HTTP security suite's JWT/API-key/WAF tests, which
// invert the same way for the same reason (see
// e2e/suites/security/main_test.go's "Exception" section), but with the
// roles of positive/negative swapped: there, an unconverged route
// produces a false POSITIVE (200 with no policy attached); here, Envoy's
// own JWT filter design produces a false NEGATIVE (401 while JWKS is
// still cold) instead.
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
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			JWT: &routeplan.JWTInput{
				Issuer:    jwtIssuerURL(),
				JWKSURL:   jwtIssuerURL() + "/jwks",
				Audiences: []string{"my-api"},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	token, err := generateJWTToken(ctx, "my-api")
	if err != nil {
		t.Fatalf("jwt: generate token from jwt-server: %v", err)
	}
	authOpt := harness.WithGRPCMetadata("authorization", "Bearer "+token)

	var okResp *echo.Message
	allowCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, resp, err := echoCall(ctx, "hello-authed", callOpt, authOpt)
		okResp = resp
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, allowCall, routeLiveTimeout, codes.OK); err != nil {
		t.Fatalf("jwt: with valid token: %v", err)
	}
	if okResp == nil || okResp.Body != "hello-authed" {
		t.Fatalf("jwt: with valid token got echoed body %q, want %q", okResp.GetBody(), "hello-authed")
	}

	// Now that the positive call has proven the JWT filter is genuinely
	// enforcing (JWKS fetched, validation running), a request with no
	// token reaching Unauthenticated proves it actually rejects a missing
	// credential rather than accepting everything.
	res, _, err := echoCall(ctx, "hello", callOpt)
	if err != nil {
		t.Fatalf("jwt: without a token: %v", err)
	}
	if res.Code != codes.Unauthenticated {
		t.Fatalf("jwt: without a token got code %v, want %v", res.Code, codes.Unauthenticated)
	}
}

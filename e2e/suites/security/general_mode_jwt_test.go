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

// TestGeneralModeJWT ports
// security_general_mode/test_jwt.py:test_jwt_denied_without_token, and
// fixes the "wrong reason" bug task-13-brief calls out by name.
//
// The Python source's CONFIG (test_jwt.py:15) sets
//
//	"issuer": "http://jwt-server:9000"
//
// -- a short Kubernetes Service name that does NOT resolve from the
// envoy-gateway-system namespace where the Envoy proxy pods actually run
// (only "jwt-server.default.svc.cluster.local" does -- see
// e2e/deps/jwt-server.yaml, which puts it in namespace "default"). Envoy's
// JWT filter cannot even reach an unresolvable JWKS host, so
// test_jwt_denied_without_token passes because the issuer is unreachable
// -- every request would 401, WITH or WITHOUT a token, and this test never
// sends one to find out. This port uses the FQDN (jwtIssuerURL(), which
// mirrors e2e/suites/grpcroute/main_test.go's identical constant) so the
// negative case passes because JWT validation actually runs and rejects a
// missing token, and adds the positive case (a real signed token from the
// test jwt-server) to prove the FQDN fix actually restores JWT
// enforcement rather than just changing which way it's broken.
func TestGeneralModeJWT(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

	cfg := services.CreateRouteInput{
		Name:         name,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
		},
		SecurityPolicy: &services.SecurityPolicyInput{
			JWT: &services.JWTInput{
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

	// Negative first, not positive: an HTTPRoute that has landed but whose
	// SecurityPolicy hasn't converged yet answers 200 to a request with NO
	// Authorization header exactly as readily as it would to one carrying a
	// valid token -- a 200 on the positive probe proves nothing about
	// whether the JWT filter is even attached, because the exact same
	// unconverged state also produces a spurious 200 here. A 401/403
	// CANNOT be produced by that unconverged state; it can only come from
	// the SecurityPolicy actually being attached and its jwt_authn filter
	// actually rejecting the missing token. Waiting here for the denial is
	// therefore what proves the policy itself has converged, not just the
	// route.
	denyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	if _, err := waitForHTTPStatus(ctx, denyProbe, routeLiveTimeout, 401, 403); err != nil {
		t.Fatalf("jwt: without token: %v", err)
	}

	// Now that the negative probe has proven the JWT filter is genuinely
	// enforcing, a valid, correctly-issued token reaching 200 proves it
	// actually accepts a real credential rather than rejecting everything
	// (e.g. an issuer/JWKS host that's still unreachable also 401s
	// unconditionally -- see the "wrong reason" discussion above, which is
	// exactly why this can't be the only assertion either).
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("Authorization", "Bearer "+token))
	}
	requireStatus(t, ctx, allowProbe, 200)
}

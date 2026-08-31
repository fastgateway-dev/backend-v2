//go:build e2e

package security

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestExternalAuthorizationGRPC ports
// external_authorization/test_grpc.py:test_grpc_ext_auth_allowed -- an
// HTTP-protocol route (routeType backend) whose SecurityPolicy uses a
// GRPC-type ext-authz backend (grpc-external-auth:9003, the same server
// e2e/suites/grpcroute/security_ext_auth_test.go already exercises for a
// gRPC-protocol route). The Python source only ever asserted the allowed
// path (`accepted_status=[200, 404]`, with no denial counterpart at all
// in this file) -- an allow-only check that a route which let everything
// through regardless of the ext-auth decision would pass identically.
// This port adds the negative case: unlike HTTP-type ext-authz (see
// TestExternalAuthorizationHTTPDefaultHeaders), gRPC ext-authz forwards
// all request headers to the ext-auth backend by default (no
// headersToExtAuth needed -- see internal/services/kubernetes_service.go's
// buildExtAuthConfig, and grpcroute's own grpc-ext-auth test, which relies
// on the identical default), so flipping x-ext-auth-allow to "false" is
// enough to exercise the deny path.
func TestExternalAuthorizationGRPC(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)
	failOpen := false

	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
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
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			ExtAuth: &models.ExtAuthConfig{
				Type: "grpc",
				GRPC: &models.ExtAuthGRPCConfig{
					BackendRef: models.ExtAuthBackendRef{Name: grpcExternalAuthService, Namespace: backendNamespace, Port: grpcExternalAuthPort},
				},
				FailOpen: &failOpen,
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	// NEGATIVE first, not positive. A 200 cannot prove convergence here:
	// it is also exactly what this route returns while the SecurityPolicy
	// is still being programmed, since an unpolicied route just forwards
	// to the backend. Gating on 200 therefore lets the negative probe --
	// which retries only on transport errors, not on a wrong status --
	// read that same unconverged 200 and fail. The denial is the only
	// status the unconverged state CANNOT produce, so it is the only
	// sound gate.
	denyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-ext-auth-allow", "false"))
	}
	if _, err := waitForHTTPStatus(ctx, denyProbe, routeLiveTimeout, 403); err != nil {
		t.Fatalf("external authorization (grpc): with x-ext-auth-allow=false: %v", err)
	}

	// Positive: with ext-authz proven to be enforcing, an allowed request
	// must still get through -- this is what rules out a policy that
	// simply denies everything.
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-ext-auth-allow", "true"))
	}
	// Bounded poll, not a single call. The negative above already proved
	// the policy is enforcing, so this cannot pass by catching an
	// unconverged route -- but a lone call can still lose a race the
	// enforcement gate does not cover. Envoy fetches a JWKS lazily on
	// first use and answers 401 "Jwks remote fetch is failed" while that
	// fetch is in flight, which is exactly how this failed in CI. A
	// credential that is never accepted still fails, at the timeout.
	if _, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("external authorization (grpc): with x-ext-auth-allow=true: %v", err)
	}
}

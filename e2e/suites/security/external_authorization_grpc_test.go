//go:build e2e

package security

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
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
		SecurityPolicy: &services.SecurityPolicyInput{
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

	// Positive first: proves the route and the gRPC ext-authz integration
	// have converged before the negative probe is trusted (see the
	// package doc comment).
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-ext-auth-allow", "true"))
	}
	if _, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("external authorization (grpc): with x-ext-auth-allow=true: %v", err)
	}

	denyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-ext-auth-allow", "false"))
	}
	requireStatus(t, ctx, denyProbe, 403)
}

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

// TestGRPCExtAuth ports grpc_security/test_ext_auth.py.
//
// The Python original only ever checked the "allowed" case (sending
// x-ext-auth-allow=true) -- per task-12-brief's "passes for the wrong
// reason" guidance, a test asserting only an allowed/denied outcome proves
// nothing about whether the ext-authz policy is doing anything unless both
// sides are exercised. e2e/servers/grpc-external-auth's Check RPC (see
// its main.go) deterministically allows only when the "x-ext-auth-allow"
// metadata equals "true" and denies (PermissionDenied) otherwise, so this
// port checks both.
//
// Gate on the POSITIVE call, not the negative one: with FailOpen: false,
// Envoy's ext_authz filter denies EVERY request -- allowed or not -- with
// PermissionDenied while the ext-auth upstream (grpc-external-auth) is
// still cold/unreachable from Envoy's point of view. A denial observed
// during that window is indistinguishable from a genuinely-enforcing
// filter correctly rejecting a disallowed request, so polling for
// PermissionDenied proves nothing about convergence. Only a real
// codes.OK, produced by a request that actually satisfies the ext-auth
// backend's allow condition, proves the filter has actually reached that
// backend and is evaluating its response -- see security_jwt_test.go's
// doc comment for the general shape of this inversion.
func TestGRPCExtAuth(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	failOpen := false

	cfg := services.CreateRouteInput{
		Name:     name,
		Protocol: models.RouteProtocolGRPC,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{match},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
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

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	allowOpt := harness.WithGRPCMetadata("x-ext-auth-allow", "true")

	var okResp *echo.Message
	allowCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, resp, err := echoCall(ctx, "hello-allowed", callOpt, allowOpt)
		okResp = resp
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, allowCall, routeLiveTimeout, codes.OK); err != nil {
		t.Fatalf("ext auth: with x-ext-auth-allow=true: %v", err)
	}
	if okResp == nil || okResp.Body != "hello-allowed" {
		t.Fatalf("ext auth: with x-ext-auth-allow=true got echoed body %q, want %q", okResp.GetBody(), "hello-allowed")
	}

	// Now that the positive call has proven the ext-authz filter is
	// genuinely reaching the ext-auth backend and evaluating it, a
	// request WITHOUT x-ext-auth-allow reaching PermissionDenied proves
	// it actually denies a disallowed request rather than allowing
	// everything.
	res, _, err := echoCall(ctx, "hello", callOpt)
	if err != nil {
		t.Fatalf("ext auth: without x-ext-auth-allow: %v", err)
	}
	if res.Code != codes.PermissionDenied {
		t.Fatalf("ext auth: without x-ext-auth-allow got code %v, want %v", res.Code, codes.PermissionDenied)
	}
}

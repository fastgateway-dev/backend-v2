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

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	allowOpt := harness.WithGRPCMetadata("x-ext-auth-allow", "true")
	callDenied := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt)
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, callDenied, routeLiveTimeout, codes.PermissionDenied); err != nil {
		t.Fatalf("ext auth: without x-ext-auth-allow: %v", err)
	}

	res, resp, err := echoCall(ctx, "hello-allowed", callOpt, allowOpt)
	if err != nil {
		t.Fatalf("ext auth: request with x-ext-auth-allow=true: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("ext auth: with x-ext-auth-allow=true got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != "hello-allowed" {
		t.Fatalf("ext auth: got echoed body %q, want %q", resp.Body, "hello-allowed")
	}
}

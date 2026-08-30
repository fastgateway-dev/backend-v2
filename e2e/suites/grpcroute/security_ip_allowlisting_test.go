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

// TestGRPCIPAllowlisting ports grpc_security/test_ip_allowlisting.py.
//
// The Python original only ever checked the denied case (an allowlist the
// test runner's IP can never satisfy) -- per task-12-brief's "passes for
// the wrong reason" guidance, that alone doesn't prove the allowlist is
// enforced rather than, say, the route having simply failed to deploy.
// This port creates two routes: one with a CIDR that can never match the
// test runner (192.0.2.0/24, the TEST-NET-1 documentation range, RFC
// 5737) to prove denial, and one with an allow-all CIDR (0.0.0.0/0,
// mirroring grpc_client_mode's own allow-all pattern) to prove the same
// mechanism allows traffic when the CIDR actually matches.
func TestGRPCIPAllowlisting(t *testing.T) {
	t.Parallel()

	denyName, denyMatch, denyOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	denyCfg := services.CreateRouteInput{
		Name:         denyName,
		Protocol:     models.RouteProtocolGRPC,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{denyMatch},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
		SecurityPolicy: &services.SecurityPolicyInput{
			Authorization: &services.AuthorizationInput{AllowedCIDRs: []string{"192.0.2.0/24"}},
		},
	}

	allowName, allowMatch, allowOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	allowCfg := services.CreateRouteInput{
		Name:         allowName,
		Protocol:     models.RouteProtocolGRPC,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{allowMatch},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
		SecurityPolicy: &services.SecurityPolicyInput{
			Authorization: &services.AuthorizationInput{AllowedCIDRs: []string{"0.0.0.0/0"}},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(denyCfg)
	fx.Route(allowCfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	denyCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", denyOpt)
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, denyCall, routeLiveTimeout, codes.PermissionDenied); err != nil {
		t.Fatalf("ip allowlisting: CIDR that can't match the test runner: %v", err)
	}

	allowCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", allowOpt)
		return res, err
	}
	res, err := waitForGRPCCodeIn(ctx, allowCall, routeLiveTimeout, codes.OK)
	if err != nil {
		t.Fatalf("ip allowlisting: allow-all CIDR (0.0.0.0/0): %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("ip allowlisting: allow-all CIDR got code %v, want %v", res.Code, codes.OK)
	}
}

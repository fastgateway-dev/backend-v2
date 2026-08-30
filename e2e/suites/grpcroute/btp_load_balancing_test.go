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

// TestGRPCBTPLoadBalancing ports grpc_btp_features/test_load_balancing.py.
//
// httproute's TestLoadBalancing proves distribution by scaling podinfo to 3
// replicas and collecting distinct pod hostnames from repeated requests.
// This package deliberately does not scale the shared "podinfo" Deployment
// (see the package doc comment in main_test.go and
// TestGRPCBTPHealthCheckActive's comment) -- at podinfo's normal 1
// replica, there is nothing to distribute across, so distribution can't be
// observed here regardless. This port is a smoke test: it verifies the
// route deploys with a load-balancer policy attached and correctly serves
// traffic, strictly stronger than the old assertion (`returncode == 0 or
// "rpc error" in stderr`, true of almost any outcome).
func TestGRPCBTPLoadBalancing(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")

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
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
			LoadBalancer: &models.LoadBalancerConfig{Type: models.LoadBalancerTypeRoundRobin},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("load balancing: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("load balancing: got code %v, want %v", res.Code, codes.OK)
	}
}

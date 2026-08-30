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

// TestGRPCBTPHealthCheckPassive ports
// grpc_btp_features/test_health_check_passive.py.
//
// httproute's TestHealthCheckPassive drives real 5xx responses at podinfo
// (via its HTTP /status/500 debug endpoint) to trip outlier detection.
// podinfo's real gRPC server has no analogous "always fail" RPC (see the
// package doc comment in main_test.go), so there is no way to drive
// consecutive gRPC errors against it from here without also touching
// shared cluster state this package deliberately avoids (see
// TestGRPCBTPHealthCheckActive's comment for why). This port is therefore
// a smoke test: it verifies the route deploys with passive health checking
// attached and correctly serves traffic, strictly stronger than the old
// assertion (`returncode == 0 or "rpc error" in stderr`, true of almost
// any outcome).
func TestGRPCBTPHealthCheckPassive(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	consecutive5xx := uint32(5)
	intervalS := "30s"
	baseEjectS := "60s"

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
			HealthCheck: &models.HealthCheckConfig{
				Passive: &models.PassiveHealthCheckConfig{
					Consecutive5xxErrors: &consecutive5xx,
					Interval:             &intervalS,
					BaseEjectionTime:     &baseEjectS,
				},
			},
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
		t.Fatalf("health check passive: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("health check passive: got code %v while healthy, want %v", res.Code, codes.OK)
	}
}

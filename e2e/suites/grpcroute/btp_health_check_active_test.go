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

// TestGRPCBTPHealthCheckActive ports
// grpc_btp_features/test_health_check_active_grpc.py.
//
// httproute's TestHealthCheckActive proves its health check is real by
// scaling the shared "podinfo" Deployment to 0 replicas and asserting the
// gateway starts failing. This package deliberately does NOT do that (see
// the package doc comment in main_test.go): httproute's own podinfoMu only
// serializes tests within httproute's own test binary, and this package
// runs as a separate `go test -tags e2e ./e2e/...` binary with no way to
// coordinate replica changes against it -- scaling the shared backend here
// could silently corrupt httproute's own health-check/load-balancing
// assertions. This port is therefore a smoke test: it verifies the route
// deploys with an active GRPC-type health check attached and correctly
// serves traffic, which the old assertion (`returncode == 0 or "rpc error"
// in stderr`, true of almost any outcome) did not meaningfully check.
func TestGRPCBTPHealthCheckActive(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	timeoutS := "5s"
	intervalS := "10s"
	unhealthy := uint32(3)
	healthy := uint32(2)

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
				Active: &models.ActiveHealthCheckConfig{
					Type:               "GRPC",
					Timeout:            &timeoutS,
					Interval:           &intervalS,
					UnhealthyThreshold: &unhealthy,
					HealthyThreshold:   &healthy,
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
		t.Fatalf("health check active: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("health check active: got code %v while healthy, want %v", res.Code, codes.OK)
	}
}

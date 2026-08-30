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

// TestGRPCCatchallMatch ports grpc_route_matching/test_catchall.py: an
// empty GRPCRouteMatch ({}) (no grpcService/grpcMethod condition) matches
// any gRPC call. The discriminating header (see uniqueMatch / the package
// doc comment in main_test.go) is still required so this test's route
// doesn't also catch every other parallel test's traffic to the shared
// echo.EchoService -- with it present, the route becomes a catch-all for
// "any service/method carrying this header", verified below with a plain
// echo.EchoService/Echo call (same RPC the Python original used).
func TestGRPCCatchallMatch(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "", "", "")

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
		t.Fatalf("catchall match: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("catchall match: got code %v, want %v", res.Code, codes.OK)
	}
}

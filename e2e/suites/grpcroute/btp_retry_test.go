//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"google.golang.org/grpc/codes"
)

// TestGRPCBTPRetry ports grpc_btp_features/test_retry.py.
//
// httproute's TestRetry proves retries actually happen by pointing the
// route at podinfo's HTTP /status/503 debug endpoint (always fails) and
// counting attempts in the pod logs. podinfo's real gRPC server has no
// equivalent "always fail" RPC (see the package doc comment in
// main_test.go), so there is no controllable failure to retry against
// here, and inspecting pod logs for gRPC method names risks false matches
// against other tests' concurrent traffic to the same shared pod (unlike
// HTTP's distinctive "/status/503" path substring). This port is a smoke
// test: it verifies the route deploys with a retry policy attached and
// correctly serves traffic, strictly stronger than the old assertion
// (`returncode == 0 or "rpc error" in stderr`, true of almost any outcome).
func TestGRPCBTPRetry(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	numRetries := int32(3)

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
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			Retry: &models.RetryConfig{
				NumRetries: &numRetries,
				RetryOn:    &models.RetryOn{Triggers: []string{"cancelled", "unavailable"}},
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
		t.Fatalf("retry: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("retry: got code %v, want %v", res.Code, codes.OK)
	}
}

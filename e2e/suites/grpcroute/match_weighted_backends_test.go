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

// TestGRPCWeightedBackends ports
// grpc_route_matching/test_weighted_backends.py: two RouteBackend entries
// pointing at the same podinfo:9999 service with 80/20 weights. Both
// entries reach the same backend, so this cannot observe the weighting
// itself (neither did the Python original); it verifies the config is
// accepted and traffic is routed correctly, which the old assertion
// (`returncode == 0 or "rpc error" in stderr`, true of almost any outcome)
// did not meaningfully check.
func TestGRPCWeightedBackends(t *testing.T) {
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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 80},
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 20},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	const wantBody = "hello-weighted"
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, wantBody, callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("weighted backends: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("weighted backends: got code %v, want %v", res.Code, codes.OK)
	}

	res, resp, err := echoCall(ctx, wantBody, callOpt)
	if err != nil {
		t.Fatalf("weighted backends: request: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("weighted backends: got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != wantBody {
		t.Fatalf("weighted backends: got echoed body %q, want %q", resp.Body, wantBody)
	}
}

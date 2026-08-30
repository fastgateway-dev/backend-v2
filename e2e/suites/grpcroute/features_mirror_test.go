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

// TestGRPCMirror ports grpc_route_features/test_mirror.py: traffic is
// mirrored (fire-and-forget) to nginx-service, a plain HTTP server that
// will simply fail to understand the mirrored gRPC bytes -- but mirroring
// is asynchronous and must never affect the primary response, which is
// what this test actually verifies: the primary Echo call still succeeds
// and echoes correctly, strictly stronger than the old assertion
// (`returncode == 0 or "Code:" in stderr`, true of almost any outcome).
func TestGRPCMirror(t *testing.T) {
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
			Mirrors: []models.MirrorBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	const wantBody = "hello-mirror"
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, wantBody, callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("mirror: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("mirror: got code %v, want %v", res.Code, codes.OK)
	}

	res, resp, err := echoCall(ctx, wantBody, callOpt)
	if err != nil {
		t.Fatalf("mirror: request: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("mirror: got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != wantBody {
		t.Fatalf("mirror: got echoed body %q, want %q -- primary response must be unaffected by mirroring", resp.Body, wantBody)
	}
}

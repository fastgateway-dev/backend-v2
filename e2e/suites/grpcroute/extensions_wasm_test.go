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

// TestGRPCWasm ports grpc_extensions/test_wasm.py.
//
// The old assertion (`returncode == 0 or "rpc error" in stderr or
// "content-type" in stderr`) accepted the wasm filter either working
// normally or visibly breaking gRPC content negotiation, with no way to
// tell which happened. This port asserts what the test's name promises: a
// route with this wasm filter attached (referencing a real, publicly
// hosted example filter with a pinned sha256, same as the Python source)
// still serves the Echo RPC correctly.
func TestGRPCWasm(t *testing.T) {
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
		ExtensionPolicy: &services.EnvoyExtensionPolicyInput{
			Wasm: &models.WasmExtensionConfig{
				Name:   "wasm-filter",
				RootID: "my_root_id",
				Code: models.WasmCodeSource{
					Type: "HTTP",
					HTTP: &models.WasmHTTPSource{
						URL:    "https://raw.githubusercontent.com/envoyproxy/examples/main/wasm-cc/lib/envoy_filter_http_wasm_example.wasm",
						SHA256: "79c9f85128bb0177b6511afa85d587224efded376ac0ef76df56595f1e6315c0",
					},
				},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	const wantBody = "hello-wasm"
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, wantBody, callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("wasm: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("wasm: got code %v, want %v", res.Code, codes.OK)
	}

	res, resp, err := echoCall(ctx, wantBody, callOpt)
	if err != nil {
		t.Fatalf("wasm: request: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("wasm: got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != wantBody {
		t.Fatalf("wasm: got echoed body %q, want %q", resp.Body, wantBody)
	}
}

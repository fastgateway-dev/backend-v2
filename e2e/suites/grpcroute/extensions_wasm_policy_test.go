//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/e2e/testdata/pb/echo"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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
		ExtensionPolicy: &routeplan.EnvoyExtensionPolicyInput{
			Wasm: &models.WasmExtensionConfig{
				Name:   "wasm-filter",
				RootID: "my_root_id",
				Code: models.WasmCodeSource{
					Type: "HTTP",
					HTTP: &models.WasmHTTPSource{
						URL:    harness.WasmFilterURL,
						SHA256: harness.WasmFilterSHA256(t),
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

	// Polled, not read once: Envoy Gateway programs the EnvoyExtensionPolicy
	// as a SEPARATE object AFTER the route, so the route can already serve
	// traffic with the filter not yet applied. The HTTP port of this test
	// polls for the same reason. Bounded by routeLiveTimeout, so a filter
	// that never takes effect still fails rather than hanging.
	deadline := time.Now().Add(routeLiveTimeout)
	var res2 *harness.GRPCResult
	var resp *echo.Message
	for {
		var err error
		res2, resp, err = echoCall(ctx, wantBody, callOpt)
		if err != nil {
			t.Fatalf("wasm: request: %v", err)
		}
		if res2.Code == codes.OK && headerHas(res2.Header, harness.WasmFilterHeader, harness.WasmFilterHeaderValue) {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("wasm: filter never applied: code=%v %s=%v, want OK with %q. "+
				"A non-OK code here means the filter broke the RPC; OK without the header "+
				"means the EnvoyExtensionPolicy never attached to the GRPCRoute.",
				res2.Code, harness.WasmFilterHeader,
				res2.Header.Get(harness.WasmFilterHeader), harness.WasmFilterHeaderValue)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// The filter must not have disturbed the gRPC payload: this is the
	// property the previous demo filter violated by rewriting the body.
	if resp.Body != wantBody {
		t.Fatalf("wasm: got echoed body %q, want %q", resp.Body, wantBody)
	}
}

// headerHas reports whether md carries key with value, case-insensitively on
// the key (gRPC metadata keys are normalised to lower case).
func headerHas(md metadata.MD, key, value string) bool {
	for _, v := range md.Get(key) {
		if v == value {
			return true
		}
	}
	return false
}

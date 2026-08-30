//go:build e2e

package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestExtensionsWasm ports extensions/test_wasm.py, replacing the
// tautological
//
//	assert resp.headers.get("x-wasm-custom") == "FOO" or resp.status_code in (200, 404)
//
// (the trailing `or` makes the header check provably unnecessary --
// exactly the pattern task-15-brief calls out) with the header check
// alone, no escape hatch. This wasm filter
// (envoy_filter_http_wasm_example.wasm, the same publicly hosted example
// with a pinned sha256 the Python source and
// e2e/suites/grpcroute/extensions_wasm_test.go both use) is confirmed, in
// e2e/E2E_TEST-v1.6.2-v1.4.1.md's "WASM Extension" entry, to add a real
// "x-wasm-custom: FOO" response header for an HTTP route (unlike the gRPC
// port, which could not check for it: the same filter's response breaks
// gRPC content-type framing, so e2e/suites/grpcroute's port only asserts
// the RPC itself still succeeds).
func TestExtensionsWasm(t *testing.T) {
	t.Parallel()

	_, path, cfg := backendRouteConfig(t)
	cfg.ExtensionPolicy = &services.EnvoyExtensionPolicyInput{
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
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("extensions wasm: route never became live: %v", err)
	}

	// Polled rather than read once: this header comes from a policy Envoy
	// Gateway programs as a SEPARATE object AFTER the route, so a
	// status-only gate is satisfied by the route serving traffic with the
	// policy not yet applied. Bounded by routeLiveTimeout, so a policy
	// that never takes effect still fails the test.
	resp, err := harness.WaitForResponse(ctx, probe, func(r *harness.Response) bool {
		return r.StatusCode == 200 && r.Header.Get("x-wasm-custom") == "FOO"
	}, routeLiveTimeout)
	if err != nil {
		status, got, body := 0, "", ""
		if resp != nil {
			status, got, body = resp.StatusCode, resp.Header.Get("x-wasm-custom"), truncate(resp.Body, 300)
		}
		t.Fatalf("extensions wasm: status %d, x-wasm-custom header = %q, want 200 with %q (body: %s): %v",
			status, got, "FOO", body, err)
	}
}

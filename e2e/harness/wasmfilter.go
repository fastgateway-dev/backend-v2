//go:build e2e

package harness

import (
	"os"
	"testing"
)

// The gRPC-safe proxy-wasm filter served in-cluster by e2e/servers/wasm-host.
//
// The suite previously pointed at envoyproxy/examples' demo filter, which
// rewrites content-type to text/plain, drops content-length and replaces the
// whole response body. That is fine for an HTTP route but destroys gRPC
// framing, so a gRPC call through it can never return OK -- which is why the
// gRPC wasm test failed the first time it was ever allowed to run.
//
// This filter adds one response header and touches nothing else, so it is
// valid for both protocols and observable on both: HTTP sees a response
// header, gRPC sees it as response metadata in the HEADERS frame.
const (
	// WasmFilterURL is resolvable from inside the cluster only.
	WasmFilterURL = "http://wasm-host-service.default.svc.cluster.local/grpcsafe.wasm"

	// WasmFilterHeader / WasmFilterHeaderValue are what the filter adds and
	// what the tests assert on.
	WasmFilterHeader      = "x-wasm-custom"
	WasmFilterHeaderValue = "FOO"

	// WasmFilterSHA256Env carries the module's digest from the image build to
	// the test run.
	WasmFilterSHA256Env = "E2E_WASM_FILTER_SHA256"
)

// WasmFilterSHA256 returns the digest of the wasm module the cluster is
// serving. It is NOT a pinned constant: the wasm bytes differ between Go patch
// releases, so a hash computed on one machine does not match another. CI reads
// the digest out of the built image and exports it here.
func WasmFilterSHA256(t *testing.T) string {
	t.Helper()
	sha := os.Getenv(WasmFilterSHA256Env)
	if sha == "" {
		t.Fatalf("%s is unset: the wasm filter's digest is computed at image build "+
			"time and exported by the workflow step that builds e2e/servers/wasm-host. "+
			"To run this test outside CI, set it to the output of:\n"+
			"  docker run --rm --entrypoint cat fastgateway-wasm-host:latest "+
			"/usr/share/nginx/html/grpcsafe.wasm.sha256", WasmFilterSHA256Env)
	}
	return sha
}

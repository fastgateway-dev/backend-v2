//go:build e2e

package harness

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

	// WasmFilterSHA256 pins the module. The build is byte-reproducible
	// (-trimpath, -s -w, empty -buildid); e2e/servers/wasm-host/Dockerfile
	// verifies this value at image build time and fails loudly on drift.
	WasmFilterSHA256 = "37d25f3ac04170dd3062e56a773664d330f9dc05743203c193f0a1518867bcf3"

	// WasmFilterHeader / WasmFilterHeaderValue are what the filter adds and
	// what the tests assert on.
	WasmFilterHeader      = "x-wasm-custom"
	WasmFilterHeaderValue = "FOO"
)

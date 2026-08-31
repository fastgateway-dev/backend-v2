// Command grpcsafe is a minimal proxy-wasm filter used by the e2e suite to
// prove that an EnvoyExtensionPolicy Wasm filter attaches and runs on a route.
//
// It is deliberately gRPC-safe: it adds ONE response header and touches
// nothing else. The upstream demo filter the suite used to reference
// (envoyproxy/examples' envoy_filter_http_wasm_example.wasm) rewrites
// content-type to text/plain, drops content-length, and replaces the whole
// response body -- all three destroy gRPC's framing, so a gRPC call through it
// can never succeed. That is why the gRPC port of the wasm test could not pass
// against it.
//
// Adding a response header is safe for both protocols: gRPC carries it in the
// HTTP/2 HEADERS frame, where the client surfaces it as response metadata, and
// the message framing and trailers are left untouched.
package main

// NOTE: this is github.com/proxy-wasm/proxy-wasm-go-sdk, which targets the
// upstream Go compiler (Go 1.24+ WASI reactors) and is what the Dockerfile's
// `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared` produces. Do NOT swap
// this for the github.com/tetratelabs/proxy-wasm-go-sdk fork: that one is
// TinyGo-only, its proxy_abi_version_0_2_0 marker is a cgo `//export` the
// standard Go compiler silently drops, so an official-Go build yields a module
// with no Proxy-Wasm ABI version. Envoy then rejects it ("Missing or unknown
// Proxy-Wasm ABI version") and, fail-closed, 503s every request. Requires the
// proxy's Envoy >= 1.33 (Envoy Gateway 1.8 ships 1.38, so it's fine).
import (
	"github.com/proxy-wasm/proxy-wasm-go-sdk/proxywasm"
	"github.com/proxy-wasm/proxy-wasm-go-sdk/proxywasm/types"
)

// HeaderName and HeaderValue are what the e2e tests assert on.
const (
	HeaderName  = "x-wasm-custom"
	HeaderValue = "FOO"
)

func main() {}

func init() {
	proxywasm.SetVMContext(&vmContext{})
}

type vmContext struct{ types.DefaultVMContext }

func (*vmContext) NewPluginContext(uint32) types.PluginContext {
	return &pluginContext{}
}

type pluginContext struct{ types.DefaultPluginContext }

func (*pluginContext) NewHttpContext(uint32) types.HttpContext {
	return &httpContext{}
}

type httpContext struct{ types.DefaultHttpContext }

// OnHttpResponseHeaders adds the marker header. It returns ActionContinue in
// every case, including when the header cannot be added, so that a filter
// failure can never turn into a failed request -- FastGateway does not expose
// Envoy's failOpen knob for Wasm, so a filter that errors here would otherwise
// fail the request closed.
func (*httpContext) OnHttpResponseHeaders(int, bool) types.Action {
	if err := proxywasm.AddHttpResponseHeader(HeaderName, HeaderValue); err != nil {
		proxywasm.LogErrorf("grpcsafe: failed to add %s: %v", HeaderName, err)
	}
	return types.ActionContinue
}

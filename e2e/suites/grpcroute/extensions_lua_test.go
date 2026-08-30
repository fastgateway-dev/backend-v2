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

// TestGRPCLua ports grpc_extensions/test_lua.py.
//
// The old assertion (`returncode == 0 or "rpc error" in stderr`) never
// actually checked the Lua filter did anything. This port asserts the
// filter's injected response header ("x-lua-grpc: hello", set via
// envoy_on_response) is present in the gRPC response's initial headers,
// which harness.GRPCResult.Header captures separately from the message
// body -- a real, deterministic signal the filter ran.
func TestGRPCLua(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	script := "function envoy_on_response(response_handle)\n  response_handle:headers():add(\"x-lua-grpc\", \"hello\")\nend"

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
			Lua: &models.LuaExtensionConfig{Type: "Inline", Inline: script},
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
		t.Fatalf("lua: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("lua: got code %v, want %v", res.Code, codes.OK)
	}

	res, _, err = echoCall(ctx, "hello", callOpt)
	if err != nil {
		t.Fatalf("lua: request: %v", err)
	}
	got := res.Header.Get("x-lua-grpc")
	if len(got) == 0 || got[0] != "hello" {
		t.Fatalf("lua: response header x-lua-grpc = %v, want [%q]", got, "hello")
	}
}

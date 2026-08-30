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

// TestExtensionsLua ports extensions/test_lua.py: an inline Lua response
// filter that adds "x-lua-custom: FOO" must actually run. Already a real
// assertion in the Python source (`assert resp.headers.get("x-lua-custom")
// == "FOO"`, no tautological escape hatch); ported unchanged in spirit,
// using harness.UniqueName instead of the fixed "reg-lua".
func TestExtensionsLua(t *testing.T) {
	t.Parallel()

	const luaScript = `function envoy_on_response(response_handle)
  response_handle:headers():add("x-lua-custom", "FOO")
end`

	_, path, cfg := backendRouteConfig(t)
	cfg.ExtensionPolicy = &services.EnvoyExtensionPolicyInput{
		Lua: &models.LuaExtensionConfig{Type: "Inline", Inline: luaScript},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("extensions lua: route never became live: %v", err)
	}
	if got := resp.Header.Get("x-lua-custom"); got != "FOO" {
		t.Fatalf("extensions lua: x-lua-custom header = %q, want %q", got, "FOO")
	}
}

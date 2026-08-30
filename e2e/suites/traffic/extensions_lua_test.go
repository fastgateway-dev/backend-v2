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
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("extensions lua: route never became live: %v", err)
	}

	// The Lua filter lives in an EnvoyExtensionPolicy, a separate object
	// programmed after the HTTPRoute, so the route answers 200 without the
	// header for a short window after deploy. Poll for the header the
	// script adds rather than reading it once -- a Lua script that never
	// runs still fails here, it just fails after the timeout instead of
	// immediately.
	resp, err := harness.WaitForResponse(ctx, probe, func(r *harness.Response) bool {
		return r.Header.Get("x-lua-custom") == "FOO"
	}, routeLiveTimeout)
	if err != nil {
		got := ""
		if resp != nil {
			got = resp.Header.Get("x-lua-custom")
		}
		t.Fatalf("extensions lua: x-lua-custom header = %q, want %q: %v", got, "FOO", err)
	}
}

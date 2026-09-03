package routeplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// envoyExtensionPolicyFixture is one BuildEnvoyExtensionPolicyK8sConfig input,
// snapshotted through the builder it names. Build closes over its own inputs
// so each fixture is a self-contained description of one branch through the
// builder.
type envoyExtensionPolicyFixture struct {
	Name  string
	Build func() any
}

// fixtureRoute is the shared base route for every fixture. Fixed UUIDs keep
// the golden output stable.
func fixtureRoute() *models.Route {
	return &models.Route{
		ID:           uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		K8sRouteName: "example-route",
		Protocol:     models.RouteProtocolHTTP,
	}
}

// fixtureRouteDomain is the shared domain for every fixture.
func fixtureRouteDomain() *models.Domain {
	return &models.Domain{
		ID:        uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Namespace: "gateway-ns",
	}
}

// fixtureLuaPolicy is an EnvoyExtensionPolicy configured with only a Lua
// extension.
func fixtureLuaPolicy() *models.EnvoyExtensionPolicy {
	return &models.EnvoyExtensionPolicy{
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{
				Type:   "Inline",
				Inline: "return 1",
			},
		},
	}
}

// fixtureWafPolicy is a WafPolicy configured in blocking mode.
func fixtureWafPolicy() *models.WafPolicy {
	return &models.WafPolicy{
		Config: models.WafPolicyConfig{
			Mode: "block",
		},
	}
}

// fixtureWAFConfig is the shared coraza-proxy-wasm image reference for every
// fixture that exercises the WAF branch.
func fixtureWAFConfig() WAFConfig {
	return WAFConfig{Image: "example.com/coraza-proxy-wasm", Tag: "9.9.9"}
}

// envoyExtensionPolicyFixtures returns every golden fixture for
// BuildEnvoyExtensionPolicyK8sConfig, covering both call sites it unifies:
// the base route (route_deploy.go, with and without WAF) and a per-client
// route (route_clients_apikey.go, which never has a WAF policy in scope).
func envoyExtensionPolicyFixtures() []envoyExtensionPolicyFixture {
	return []envoyExtensionPolicyFixture{
		// Base route, Lua only -- no WafPolicy in scope, mirroring a route
		// with a generic EnvoyExtensionPolicy but no WAF configured.
		{
			Name: "envoyextensionpolicy-base-route-lua-only",
			Build: func() any {
				route := fixtureRoute()
				return BuildEnvoyExtensionPolicyK8sConfig(route, fixtureRouteDomain(), route.K8sRouteName, fixtureLuaPolicy(), nil, WAFConfig{})
			},
		},
		// Base route, WAF only -- no EnvoyExtensionPolicy in scope, mirroring
		// a route with WAF enabled but no Lua/Wasm/ExtProc extensions.
		{
			Name: "envoyextensionpolicy-base-route-waf-only",
			Build: func() any {
				route := fixtureRoute()
				return BuildEnvoyExtensionPolicyK8sConfig(route, fixtureRouteDomain(), route.K8sRouteName, nil, fixtureWafPolicy(), fixtureWAFConfig())
			},
		},
		// Base route, both Lua and WAF configured together -- the branch that
		// appends the coraza-waf wasm entry alongside a generic extension.
		{
			Name: "envoyextensionpolicy-base-route-lua-and-waf",
			Build: func() any {
				route := fixtureRoute()
				return BuildEnvoyExtensionPolicyK8sConfig(route, fixtureRouteDomain(), route.K8sRouteName, fixtureLuaPolicy(), fixtureWafPolicy(), fixtureWAFConfig())
			},
		},
		// Per-client route -- targetRouteName carries the "-ak-" client
		// suffix, and wafPolicy is nil because that call site never has a WAF
		// policy in scope.
		{
			Name: "envoyextensionpolicy-per-client-route",
			Build: func() any {
				route := fixtureRoute()
				clientID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
				targetRouteName := route.K8sRouteName + "-ak-" + clientID.String()[:8]
				return BuildEnvoyExtensionPolicyK8sConfig(route, fixtureRouteDomain(), targetRouteName, fixtureLuaPolicy(), nil, WAFConfig{})
			},
		},
	}
}

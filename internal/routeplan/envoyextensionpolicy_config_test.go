package routeplan

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// CHARACTERIZATION. The expected literal below is copied VERBATIM from
// RouteService.buildEnvoyExtensionPolicyConfig (route_deploy.go:621, the base
// route path) as it stood at 826340b, including its Lua, Wasm, ExtProc and WAF
// (coraza) sections. If this assertion fails, the extraction changed
// behaviour.
func TestBuildEnvoyExtensionPolicyK8sConfig_MatchesOriginalLiteral_BaseRoute(t *testing.T) {
	route := &models.Route{
		ID:           uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		K8sRouteName: "example-route",
		Protocol:     models.RouteProtocolHTTP,
	}
	domain := &models.Domain{
		ID:        uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Namespace: "gateway-ns",
	}
	policy := &models.EnvoyExtensionPolicy{
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{
				Type:   "Inline",
				Inline: "return 1",
			},
			Wasm: &models.WasmExtensionConfig{
				Name:   "my-wasm",
				RootID: "root",
				Code: models.WasmCodeSource{
					Type: "HTTP",
					HTTP: &models.WasmHTTPSource{
						URL:    "https://example.com/wasm.wasm",
						SHA256: "abc123",
					},
				},
			},
			ExtProc: &models.ExtProcExtensionConfig{
				BackendRef: models.ExtProcBackendRef{
					Name:      "ext-proc-svc",
					Namespace: "ext-proc-ns",
					Port:      9000,
				},
				FailOpen: true,
			},
		},
	}
	wafPolicy := &models.WafPolicy{
		Config: models.WafPolicyConfig{
			Mode: "block",
		},
	}
	waf := WAFConfig{Image: "example.com/coraza-proxy-wasm", Tag: "9.9.9"}

	// Original site: `hasGenericExtensions := policy != nil && !policy.Config.IsEmpty()`
	// and `hasWaf := wafPolicy != nil && !wafPolicy.Config.IsEmpty()`, both true
	// here, gate the sections below exactly as they did at the original site.
	corazaConfig, err := BuildCorazaDirectives(&wafPolicy.Config)
	require.NoError(t, err)
	require.NotEmpty(t, corazaConfig)

	want := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  "HTTPRoute",
			Name:  route.K8sRouteName,
		},
		Lua: []kubernetes.LuaExtensionPolicyConfig{
			{
				Type:   "Inline",
				Inline: "return 1",
			},
		},
		Wasm: []kubernetes.WasmExtensionPolicyConfig{
			{
				Name:   "my-wasm",
				RootID: "root",
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "HTTP",
					HTTP: &kubernetes.WasmHTTPSourcePolicyConfig{
						URL:    "https://example.com/wasm.wasm",
						SHA256: "abc123",
					},
				},
			},
			{
				Name:   "coraza-waf",
				RootID: "",
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "Image",
					Image: &kubernetes.WasmImageSourcePolicyConfig{
						URL: waf.ImageURL(),
					},
				},
				Config: &corazaConfig,
			},
		},
		ExtProc: []kubernetes.ExtProcPolicyConfig{
			{
				BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
					Name:      "ext-proc-svc",
					Namespace: "ext-proc-ns",
					Port:      9000,
				},
				FailOpen: true,
			},
		},
	}

	got := BuildEnvoyExtensionPolicyK8sConfig(route, domain, route.K8sRouteName, policy, wafPolicy, waf)
	require.Equal(t, want, got)
}

// CHARACTERIZATION. The expected literal below is copied VERBATIM from
// RouteService.buildAPIKeyEnvoyExtensionPolicyConfig
// (route_clients_apikey.go:548, the per-client route path) as it stood at
// 826340b. That site has no WAF policy in scope, so wafPolicy is nil and the
// zero-value WAFConfig is never consulted. If this assertion fails, the
// extraction changed behaviour.
func TestBuildEnvoyExtensionPolicyK8sConfig_MatchesOriginalLiteral_PerClientRoute(t *testing.T) {
	route := &models.Route{
		ID:           uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		K8sRouteName: "example-route",
		Protocol:     models.RouteProtocolGRPC,
	}
	domain := &models.Domain{
		ID:        uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		Namespace: "gateway-ns",
	}
	clientID := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	targetRouteName := route.K8sRouteName + "-ak-" + clientID.String()[:8]

	policy := &models.EnvoyExtensionPolicy{
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{
				Type:   "ValueRef",
				Inline: "",
				ValueRef: &models.ValueRef{
					Group:     "",
					Kind:      "ConfigMap",
					Name:      "lua-script",
					Namespace: "gateway-ns",
				},
			},
			Wasm: &models.WasmExtensionConfig{
				Name:   "per-client-wasm",
				RootID: "",
				Code: models.WasmCodeSource{
					Type: "Image",
					Image: &models.WasmImageSource{
						URL:    "example.com/wasm-module",
						SHA256: "def456",
					},
				},
			},
			ExtProc: &models.ExtProcExtensionConfig{
				BackendRef: models.ExtProcBackendRef{
					Name:      "shared-ext-proc",
					Namespace: "ext-proc-ns",
					Port:      9001,
				},
				FailOpen: false,
			},
		},
	}

	want := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(targetRouteName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  "GRPCRoute",
			Name:  targetRouteName,
		},
		Lua: []kubernetes.LuaExtensionPolicyConfig{
			{
				Type:   "ValueRef",
				Inline: "",
				ValueRef: &kubernetes.ValueRefPolicyConfig{
					Group:     "",
					Kind:      "ConfigMap",
					Name:      "lua-script",
					Namespace: "gateway-ns",
				},
			},
		},
		Wasm: []kubernetes.WasmExtensionPolicyConfig{
			{
				Name:   "per-client-wasm",
				RootID: "",
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "Image",
					Image: &kubernetes.WasmImageSourcePolicyConfig{
						URL:    "example.com/wasm-module",
						SHA256: "def456",
					},
				},
			},
		},
		ExtProc: []kubernetes.ExtProcPolicyConfig{
			{
				BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
					Name:      "shared-ext-proc",
					Namespace: "ext-proc-ns",
					Port:      9001,
				},
				FailOpen: false,
			},
		},
	}

	got := BuildEnvoyExtensionPolicyK8sConfig(route, domain, targetRouteName, policy, nil, WAFConfig{})
	require.Equal(t, want, got)
}

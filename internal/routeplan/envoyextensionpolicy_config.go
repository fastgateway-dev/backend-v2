package routeplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// BuildEnvoyExtensionPolicyK8sConfig assembles the EnvoyExtensionPolicyK8sConfig
// shared by the base route and per-client route deploy paths. It unifies two
// call sites that previously built the same struct inline and differed only
// in which route name (and its derived TargetRef.Name) they targeted:
//
//   - route_deploy.go, for the base route: targetRouteName = route.K8sRouteName
//   - route_clients_apikey.go, for a per-client route:
//     targetRouteName = route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
//
// wafPolicy and waf are only meaningful for the base route: the per-client
// site has no WAF policy in scope and passes wafPolicy as nil, in which case
// no WAF wasm entry is added and waf is never consulted.
//
// This function only assembles. Whether to build at all is decided by the
// caller's own guard, which stays at the call site:
//   - route_deploy.go returns early on !hasGenericExtensions && !hasWaf
//   - route_clients_apikey.go returns early on policy == nil || policy.Config.IsEmpty()
func BuildEnvoyExtensionPolicyK8sConfig(route *models.Route, domain *models.Domain, targetRouteName string, policy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy, waf WAFConfig) *kubernetes.EnvoyExtensionPolicyK8sConfig {
	// Check if we have any extensions to deploy
	hasGenericExtensions := policy != nil && !policy.Config.IsEmpty()
	hasWaf := wafPolicy != nil && !wafPolicy.Config.IsEmpty()

	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(targetRouteName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  GetRouteKind(route.Protocol),
			Name:  targetRouteName,
		},
	}

	// Add Lua extension configuration (only if policy exists)
	if hasGenericExtensions && policy.Config.Lua != nil {
		luaConfig := kubernetes.LuaExtensionPolicyConfig{
			Type:   policy.Config.Lua.Type,
			Inline: policy.Config.Lua.Inline,
		}
		if policy.Config.Lua.ValueRef != nil {
			luaConfig.ValueRef = &kubernetes.ValueRefPolicyConfig{
				Group:     policy.Config.Lua.ValueRef.Group,
				Kind:      policy.Config.Lua.ValueRef.Kind,
				Name:      policy.Config.Lua.ValueRef.Name,
				Namespace: policy.Config.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Add Wasm extension configuration (only if policy exists)
	if hasGenericExtensions && policy.Config.Wasm != nil {
		wasmConfig := kubernetes.WasmExtensionPolicyConfig{
			Name:   policy.Config.Wasm.Name,
			RootID: policy.Config.Wasm.RootID,
			Code: kubernetes.WasmCodeSourcePolicyConfig{
				Type: policy.Config.Wasm.Code.Type,
			},
			Config: policy.Config.Wasm.Config,
		}
		if policy.Config.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &kubernetes.WasmHTTPSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.HTTP.URL,
				SHA256: policy.Config.Wasm.Code.HTTP.SHA256,
			}
		}
		if policy.Config.Wasm.Code.Image != nil {
			imageConfig := &kubernetes.WasmImageSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.Image.URL,
				SHA256: policy.Config.Wasm.Code.Image.SHA256,
			}
			if policy.Config.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &kubernetes.ValueRefPolicyConfig{
					Group:     policy.Config.Wasm.Code.Image.PullSecret.Group,
					Kind:      policy.Config.Wasm.Code.Image.PullSecret.Kind,
					Name:      policy.Config.Wasm.Code.Image.PullSecret.Name,
					Namespace: policy.Config.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Add ExtProc extension configuration (only if policy exists)
	if hasGenericExtensions && policy.Config.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, BuildExtProcPolicyConfig(policy.Config.ExtProc))
	}

	// Add WAF (coraza) WASM entry if WAF is configured
	if hasWaf {
		corazaConfig, err := BuildCorazaDirectives(&wafPolicy.Config)
		if err == nil && corazaConfig != "" {
			wasmConfig := kubernetes.WasmExtensionPolicyConfig{
				Name:   "coraza-waf",
				RootID: "",
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "Image",
					Image: &kubernetes.WasmImageSourcePolicyConfig{
						URL: waf.ImageURL(),
					},
				},
				Config: &corazaConfig,
			}
			config.Wasm = append(config.Wasm, wasmConfig)
		}
	}

	return config
}

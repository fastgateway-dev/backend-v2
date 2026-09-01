package domainplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// BuildEnvoyExtensionPolicyConfig builds an EnvoyExtensionPolicyK8sConfig for a domain-level extension policy targeting the Gateway
func BuildEnvoyExtensionPolicyConfig(domain *models.Domain, extConfig *models.EnvoyExtensionPolicyConfig) *kubernetes.EnvoyExtensionPolicyK8sConfig {
	if extConfig == nil || extConfig.IsEmpty() {
		return nil
	}

	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      domain.K8sGatewayName + "-eep",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   "",
		DomainID:  domain.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  "Gateway",
			Name:  domain.K8sGatewayName,
		},
	}

	// Add Lua extension configuration
	if extConfig.Lua != nil {
		luaConfig := kubernetes.LuaExtensionPolicyConfig{
			Type:   extConfig.Lua.Type,
			Inline: extConfig.Lua.Inline,
		}
		if extConfig.Lua.ValueRef != nil {
			luaConfig.ValueRef = &kubernetes.ValueRefPolicyConfig{
				Group:     extConfig.Lua.ValueRef.Group,
				Kind:      extConfig.Lua.ValueRef.Kind,
				Name:      extConfig.Lua.ValueRef.Name,
				Namespace: extConfig.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Add Wasm extension configuration
	if extConfig.Wasm != nil {
		wasmConfig := kubernetes.WasmExtensionPolicyConfig{
			Name:   extConfig.Wasm.Name,
			RootID: extConfig.Wasm.RootID,
			Code: kubernetes.WasmCodeSourcePolicyConfig{
				Type: extConfig.Wasm.Code.Type,
			},
			Config: extConfig.Wasm.Config,
		}
		if extConfig.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &kubernetes.WasmHTTPSourcePolicyConfig{
				URL:    extConfig.Wasm.Code.HTTP.URL,
				SHA256: extConfig.Wasm.Code.HTTP.SHA256,
			}
		}
		if extConfig.Wasm.Code.Image != nil {
			imageConfig := &kubernetes.WasmImageSourcePolicyConfig{
				URL:    extConfig.Wasm.Code.Image.URL,
				SHA256: extConfig.Wasm.Code.Image.SHA256,
			}
			if extConfig.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &kubernetes.ValueRefPolicyConfig{
					Group:     extConfig.Wasm.Code.Image.PullSecret.Group,
					Kind:      extConfig.Wasm.Code.Image.PullSecret.Kind,
					Name:      extConfig.Wasm.Code.Image.PullSecret.Name,
					Namespace: extConfig.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Add ExtProc extension configuration
	if extConfig.ExtProc != nil {
		cfg := kubernetes.ExtProcPolicyConfig{
			BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
				Name:      extConfig.ExtProc.BackendRef.Name,
				Namespace: extConfig.ExtProc.BackendRef.Namespace,
				Port:      extConfig.ExtProc.BackendRef.Port,
			},
			FailOpen: extConfig.ExtProc.FailOpen,
		}
		if extConfig.ExtProc.ProcessingMode != nil {
			cfg.ProcessingMode = &kubernetes.ExtProcProcessingModeConfig{}
			if extConfig.ExtProc.ProcessingMode.Request != nil {
				cfg.ProcessingMode.Request = &kubernetes.ExtProcBodyModeConfig{Body: extConfig.ExtProc.ProcessingMode.Request.Body}
			}
			if extConfig.ExtProc.ProcessingMode.Response != nil {
				cfg.ProcessingMode.Response = &kubernetes.ExtProcBodyModeConfig{Body: extConfig.ExtProc.ProcessingMode.Response.Body}
			}
		}
		config.ExtProc = append(config.ExtProc, cfg)
	}

	return config
}

package routeplan

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"sigs.k8s.io/yaml"
)

// BuildExtProcPolicyConfig converts a model ExtProcExtensionConfig to the K8s policy config
func BuildExtProcPolicyConfig(extProc *models.ExtProcExtensionConfig) kubernetes.ExtProcPolicyConfig {
	if extProc == nil {
		return kubernetes.ExtProcPolicyConfig{}
	}
	cfg := kubernetes.ExtProcPolicyConfig{
		BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
			Name:      extProc.BackendRef.Name,
			Namespace: extProc.BackendRef.Namespace,
			Port:      extProc.BackendRef.Port,
		},
		FailOpen: extProc.FailOpen,
	}
	if extProc.ProcessingMode != nil {
		cfg.ProcessingMode = &kubernetes.ExtProcProcessingModeConfig{}
		if extProc.ProcessingMode.Request != nil {
			cfg.ProcessingMode.Request = &kubernetes.ExtProcBodyModeConfig{Body: extProc.ProcessingMode.Request.Body}
		}
		if extProc.ProcessingMode.Response != nil {
			cfg.ProcessingMode.Response = &kubernetes.ExtProcBodyModeConfig{Body: extProc.ProcessingMode.Response.Body}
		}
	}
	return cfg
}

// GenerateAPIKeyEnvoyExtensionPolicyYAML generates EnvoyExtensionPolicy YAML for a per-client HTTPRoute
func GenerateAPIKeyEnvoyExtensionPolicyYAML(route *models.Route, domain *models.Domain, extPolicy *models.EnvoyExtensionPolicy, routeName string) string {
	if extPolicy == nil || extPolicy.Config.IsEmpty() {
		return ""
	}

	extConfig := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(routeName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  GetRouteKind(route.Protocol),
			Name:  routeName,
		},
	}

	// Copy Lua extension from base policy
	if extPolicy.Config.Lua != nil {
		luaConfig := kubernetes.LuaExtensionPolicyConfig{
			Type:   extPolicy.Config.Lua.Type,
			Inline: extPolicy.Config.Lua.Inline,
		}
		if extPolicy.Config.Lua.ValueRef != nil {
			luaConfig.ValueRef = &kubernetes.ValueRefPolicyConfig{
				Group:     extPolicy.Config.Lua.ValueRef.Group,
				Kind:      extPolicy.Config.Lua.ValueRef.Kind,
				Name:      extPolicy.Config.Lua.ValueRef.Name,
				Namespace: extPolicy.Config.Lua.ValueRef.Namespace,
			}
		}
		extConfig.Lua = append(extConfig.Lua, luaConfig)
	}

	// Copy Wasm extension from base policy
	if extPolicy.Config.Wasm != nil {
		wasmConfig := kubernetes.WasmExtensionPolicyConfig{
			Name:   extPolicy.Config.Wasm.Name,
			RootID: extPolicy.Config.Wasm.RootID,
			Code: kubernetes.WasmCodeSourcePolicyConfig{
				Type: extPolicy.Config.Wasm.Code.Type,
			},
			Config: extPolicy.Config.Wasm.Config,
		}
		if extPolicy.Config.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &kubernetes.WasmHTTPSourcePolicyConfig{
				URL:    extPolicy.Config.Wasm.Code.HTTP.URL,
				SHA256: extPolicy.Config.Wasm.Code.HTTP.SHA256,
			}
		}
		if extPolicy.Config.Wasm.Code.Image != nil {
			imageConfig := &kubernetes.WasmImageSourcePolicyConfig{
				URL:    extPolicy.Config.Wasm.Code.Image.URL,
				SHA256: extPolicy.Config.Wasm.Code.Image.SHA256,
			}
			if extPolicy.Config.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &kubernetes.ValueRefPolicyConfig{
					Group:     extPolicy.Config.Wasm.Code.Image.PullSecret.Group,
					Kind:      extPolicy.Config.Wasm.Code.Image.PullSecret.Kind,
					Name:      extPolicy.Config.Wasm.Code.Image.PullSecret.Name,
					Namespace: extPolicy.Config.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		extConfig.Wasm = append(extConfig.Wasm, wasmConfig)
	}

	// Add ExtProc extension
	if extPolicy.Config.ExtProc != nil {
		extConfig.ExtProc = append(extConfig.ExtProc, BuildExtProcPolicyConfig(extPolicy.Config.ExtProc))
	}

	eep := kubernetes.BuildEnvoyExtensionPolicy(extConfig)
	if eep == nil {
		return ""
	}

	yamlBytes, err := yaml.Marshal(eep.Object)
	if err != nil {
		return ""
	}

	return string(yamlBytes)
}

// GenerateEnvoyExtensionPolicyYAML generates EnvoyExtensionPolicy YAML from input
func GenerateEnvoyExtensionPolicyYAML(route *models.Route, domain *models.Domain, extInput *EnvoyExtensionPolicyInput) string {
	if extInput == nil || !extInput.HasContent() {
		return ""
	}

	// Build EnvoyExtensionPolicyK8sConfig
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  GetRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Convert Lua extension
	if extInput.Lua != nil {
		luaConfig := kubernetes.LuaExtensionPolicyConfig{
			Type:   extInput.Lua.Type,
			Inline: extInput.Lua.Inline,
		}
		if extInput.Lua.ValueRef != nil {
			luaConfig.ValueRef = &kubernetes.ValueRefPolicyConfig{
				Group:     extInput.Lua.ValueRef.Group,
				Kind:      extInput.Lua.ValueRef.Kind,
				Name:      extInput.Lua.ValueRef.Name,
				Namespace: extInput.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Convert Wasm extension
	if extInput.Wasm != nil {
		wasmConfig := kubernetes.WasmExtensionPolicyConfig{
			Name:   extInput.Wasm.Name,
			RootID: extInput.Wasm.RootID,
			Code: kubernetes.WasmCodeSourcePolicyConfig{
				Type: extInput.Wasm.Code.Type,
			},
			Config: extInput.Wasm.Config,
		}
		if extInput.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &kubernetes.WasmHTTPSourcePolicyConfig{
				URL:    extInput.Wasm.Code.HTTP.URL,
				SHA256: extInput.Wasm.Code.HTTP.SHA256,
			}
		}
		if extInput.Wasm.Code.Image != nil {
			imageConfig := &kubernetes.WasmImageSourcePolicyConfig{
				URL:    extInput.Wasm.Code.Image.URL,
				SHA256: extInput.Wasm.Code.Image.SHA256,
			}
			if extInput.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &kubernetes.ValueRefPolicyConfig{
					Group:     extInput.Wasm.Code.Image.PullSecret.Group,
					Kind:      extInput.Wasm.Code.Image.PullSecret.Kind,
					Name:      extInput.Wasm.Code.Image.PullSecret.Name,
					Namespace: extInput.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Add ExtProc extension
	if extInput != nil && extInput.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, BuildExtProcPolicyConfig(extInput.ExtProc))
	}

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := kubernetes.BuildEnvoyExtensionPolicy(config)
	if extensionPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(extensionPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating EnvoyExtensionPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// GenerateEnvoyExtensionPolicyYAMLFromDB generates EnvoyExtensionPolicy YAML from database model
func GenerateEnvoyExtensionPolicyYAMLFromDB(route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy) string {
	if policy == nil || policy.Config.IsEmpty() {
		return ""
	}

	// Build EnvoyExtensionPolicyK8sConfig
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  GetRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Convert Lua extension
	if policy.Config.Lua != nil {
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

	// Convert Wasm extension
	if policy.Config.Wasm != nil {
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

	// Add ExtProc extension
	if policy.Config.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, BuildExtProcPolicyConfig(policy.Config.ExtProc))
	}

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := kubernetes.BuildEnvoyExtensionPolicy(config)
	if extensionPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(extensionPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating EnvoyExtensionPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// GenerateEnvoyExtensionPolicyYAMLWithWaf generates EnvoyExtensionPolicy YAML from input with WAF support
func GenerateEnvoyExtensionPolicyYAMLWithWaf(route *models.Route, domain *models.Domain, extInput *EnvoyExtensionPolicyInput, wafInput *WafPolicyInput, waf WAFConfig) string {
	// Check if we have any content
	hasExtensions := extInput != nil && extInput.HasContent()
	hasWaf := wafInput != nil && wafInput.Mode != ""

	if !hasExtensions && !hasWaf {
		return ""
	}

	// Build EnvoyExtensionPolicyK8sConfig
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  GetRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Convert Lua extension
	if hasExtensions && extInput.Lua != nil {
		luaConfig := kubernetes.LuaExtensionPolicyConfig{
			Type:   extInput.Lua.Type,
			Inline: extInput.Lua.Inline,
		}
		if extInput.Lua.ValueRef != nil {
			luaConfig.ValueRef = &kubernetes.ValueRefPolicyConfig{
				Group:     extInput.Lua.ValueRef.Group,
				Kind:      extInput.Lua.ValueRef.Kind,
				Name:      extInput.Lua.ValueRef.Name,
				Namespace: extInput.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Convert Wasm extension
	if hasExtensions && extInput.Wasm != nil {
		wasmConfig := kubernetes.WasmExtensionPolicyConfig{
			Name:   extInput.Wasm.Name,
			RootID: extInput.Wasm.RootID,
			Code: kubernetes.WasmCodeSourcePolicyConfig{
				Type: extInput.Wasm.Code.Type,
			},
			Config: extInput.Wasm.Config,
		}
		if extInput.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &kubernetes.WasmHTTPSourcePolicyConfig{
				URL:    extInput.Wasm.Code.HTTP.URL,
				SHA256: extInput.Wasm.Code.HTTP.SHA256,
			}
		}
		if extInput.Wasm.Code.Image != nil {
			imageConfig := &kubernetes.WasmImageSourcePolicyConfig{
				URL:    extInput.Wasm.Code.Image.URL,
				SHA256: extInput.Wasm.Code.Image.SHA256,
			}
			if extInput.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &kubernetes.ValueRefPolicyConfig{
					Group:     extInput.Wasm.Code.Image.PullSecret.Group,
					Kind:      extInput.Wasm.Code.Image.PullSecret.Kind,
					Name:      extInput.Wasm.Code.Image.PullSecret.Name,
					Namespace: extInput.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Add ExtProc extension
	if hasExtensions && extInput.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, BuildExtProcPolicyConfig(extInput.ExtProc))
	}

	// Add WAF (coraza) WASM entry if WAF is configured
	if hasWaf {
		// Build a temporary WafPolicyConfig to use BuildCorazaDirectives
		wafConfig := &models.WafPolicyConfig{
			Mode:             wafInput.Mode,
			Rulesets:         wafInput.Rulesets,
			AnomalyThreshold: wafInput.AnomalyThreshold,
			ParanoiaLevel:    wafInput.ParanoiaLevel,
			DisabledRuleIDs:  wafInput.DisabledRuleIDs,
			CustomDirectives: wafInput.CustomDirectives,
		}
		corazaConfig, err := BuildCorazaDirectives(wafConfig)
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

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := kubernetes.BuildEnvoyExtensionPolicy(config)
	if extensionPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(extensionPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating EnvoyExtensionPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// GenerateEnvoyExtensionPolicyYAMLFromSnapshot generates EnvoyExtensionPolicy YAML from policy models
// This is a standalone function that can be called from approval_service.go for YAML diff generation
func GenerateEnvoyExtensionPolicyYAMLFromSnapshot(route *models.Route, domain *models.Domain, extPolicy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy, waf WAFConfig) string {
	// Check if we have any extensions to deploy
	hasGenericExtensions := extPolicy != nil && !extPolicy.Config.IsEmpty()
	hasWaf := wafPolicy != nil && !wafPolicy.Config.IsEmpty()

	if !hasGenericExtensions && !hasWaf {
		return ""
	}

	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  GetRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Add Lua extension configuration (only if policy exists)
	if hasGenericExtensions && extPolicy.Config.Lua != nil {
		luaConfig := kubernetes.LuaExtensionPolicyConfig{
			Type:   extPolicy.Config.Lua.Type,
			Inline: extPolicy.Config.Lua.Inline,
		}
		if extPolicy.Config.Lua.ValueRef != nil {
			luaConfig.ValueRef = &kubernetes.ValueRefPolicyConfig{
				Group:     extPolicy.Config.Lua.ValueRef.Group,
				Kind:      extPolicy.Config.Lua.ValueRef.Kind,
				Name:      extPolicy.Config.Lua.ValueRef.Name,
				Namespace: extPolicy.Config.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Add Wasm extension configuration (only if policy exists)
	if hasGenericExtensions && extPolicy.Config.Wasm != nil {
		wasmConfig := kubernetes.WasmExtensionPolicyConfig{
			Name:   extPolicy.Config.Wasm.Name,
			RootID: extPolicy.Config.Wasm.RootID,
			Code: kubernetes.WasmCodeSourcePolicyConfig{
				Type: extPolicy.Config.Wasm.Code.Type,
			},
			Config: extPolicy.Config.Wasm.Config,
		}
		if extPolicy.Config.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &kubernetes.WasmHTTPSourcePolicyConfig{
				URL:    extPolicy.Config.Wasm.Code.HTTP.URL,
				SHA256: extPolicy.Config.Wasm.Code.HTTP.SHA256,
			}
		}
		if extPolicy.Config.Wasm.Code.Image != nil {
			imageConfig := &kubernetes.WasmImageSourcePolicyConfig{
				URL:    extPolicy.Config.Wasm.Code.Image.URL,
				SHA256: extPolicy.Config.Wasm.Code.Image.SHA256,
			}
			if extPolicy.Config.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &kubernetes.ValueRefPolicyConfig{
					Group:     extPolicy.Config.Wasm.Code.Image.PullSecret.Group,
					Kind:      extPolicy.Config.Wasm.Code.Image.PullSecret.Kind,
					Name:      extPolicy.Config.Wasm.Code.Image.PullSecret.Name,
					Namespace: extPolicy.Config.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Add ExtProc extension configuration (only if policy exists)
	if hasGenericExtensions && extPolicy.Config.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, BuildExtProcPolicyConfig(extPolicy.Config.ExtProc))
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

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := kubernetes.BuildEnvoyExtensionPolicy(config)
	if extensionPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(extensionPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating EnvoyExtensionPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

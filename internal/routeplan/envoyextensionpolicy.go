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

// GenerateAPIKeyEnvoyExtensionPolicyYAML generates EnvoyExtensionPolicy YAML for a per-client HTTPRoute.
// Delegates to BuildEnvoyExtensionPolicyK8sConfig (Phase 2H), passing routeName as
// targetRouteName and no WAF policy -- the per-client path never has one in scope.
func GenerateAPIKeyEnvoyExtensionPolicyYAML(route *models.Route, domain *models.Domain, extPolicy *models.EnvoyExtensionPolicy, routeName string) string {
	if extPolicy == nil || extPolicy.Config.IsEmpty() {
		return ""
	}

	config := BuildEnvoyExtensionPolicyK8sConfig(route, domain, routeName, extPolicy, nil, WAFConfig{})

	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	if eep == nil {
		return ""
	}

	yamlBytes, err := yaml.Marshal(eep.Object)
	if err != nil {
		return ""
	}

	return string(yamlBytes)
}

// GenerateEnvoyExtensionPolicyYAMLWithWaf generates EnvoyExtensionPolicy YAML from input with WAF support.
//
// extInput and wafInput are the *input* shapes (routeplan.EnvoyExtensionPolicyInput,
// routeplan.WafPolicyInput) rather than the *models.EnvoyExtensionPolicy /
// *models.WafPolicy the shared builder takes, so this wrapper converts before
// delegating. The conversion is total in both directions:
//   - EnvoyExtensionPolicyInput{Lua, Wasm, ExtProc} maps field-for-field onto
//     models.EnvoyExtensionPolicyConfig{Lua, Wasm, ExtProc} -- same field names,
//     same pointer types, nothing to translate.
//   - WafPolicyInput{Mode, Rulesets, AnomalyThreshold, ParanoiaLevel,
//     DisabledRuleIDs, CustomDirectives} maps field-for-field onto
//     models.WafPolicyConfig, which declares exactly those six fields and no
//     others.
//   - The builder reads only policy.Config and wafPolicy.Config (never the
//     model's ID/RouteID/DomainID/ProjectID/CreatedAt/UpdatedAt/relationship
//     fields), so leaving those zero-valued on the converted models drops
//     nothing the builder consults.
func GenerateEnvoyExtensionPolicyYAMLWithWaf(route *models.Route, domain *models.Domain, extInput *EnvoyExtensionPolicyInput, wafInput *WafPolicyInput, waf WAFConfig) string {
	// Check if we have any content
	hasExtensions := extInput != nil && extInput.HasContent()
	hasWaf := wafInput != nil && wafInput.Mode != ""

	if !hasExtensions && !hasWaf {
		return ""
	}

	var policy *models.EnvoyExtensionPolicy
	if extInput != nil {
		policy = &models.EnvoyExtensionPolicy{
			Config: models.EnvoyExtensionPolicyConfig{
				Lua:     extInput.Lua,
				Wasm:    extInput.Wasm,
				ExtProc: extInput.ExtProc,
			},
		}
	}

	var wafPolicy *models.WafPolicy
	if wafInput != nil {
		wafPolicy = &models.WafPolicy{
			Config: models.WafPolicyConfig{
				Mode:             wafInput.Mode,
				Rulesets:         wafInput.Rulesets,
				AnomalyThreshold: wafInput.AnomalyThreshold,
				ParanoiaLevel:    wafInput.ParanoiaLevel,
				DisabledRuleIDs:  wafInput.DisabledRuleIDs,
				CustomDirectives: wafInput.CustomDirectives,
			},
		}
	}

	config := BuildEnvoyExtensionPolicyK8sConfig(route, domain, route.K8sRouteName, policy, wafPolicy, waf)

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

// GenerateEnvoyExtensionPolicyYAMLFromSnapshot generates EnvoyExtensionPolicy YAML from policy models.
// This is a standalone function that can be called from approval_service.go for YAML diff generation.
// Direct delegation to BuildEnvoyExtensionPolicyK8sConfig (Phase 2H): every argument maps one-to-one,
// with targetRouteName = route.K8sRouteName.
func GenerateEnvoyExtensionPolicyYAMLFromSnapshot(route *models.Route, domain *models.Domain, extPolicy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy, waf WAFConfig) string {
	// Check if we have any extensions to deploy
	hasGenericExtensions := extPolicy != nil && !extPolicy.Config.IsEmpty()
	hasWaf := wafPolicy != nil && !wafPolicy.Config.IsEmpty()

	if !hasGenericExtensions && !hasWaf {
		return ""
	}

	config := BuildEnvoyExtensionPolicyK8sConfig(route, domain, route.K8sRouteName, extPolicy, wafPolicy, waf)

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

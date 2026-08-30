package kubernetes

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// EnvoyExtensionPolicyK8sConfig represents Envoy Gateway EnvoyExtensionPolicy configuration
type EnvoyExtensionPolicyK8sConfig struct {
	Name      string
	Namespace string
	GatewayID string
	RouteID   string
	DomainID  string
	TargetRef EnvoyExtensionPolicyTargetRef
	Lua       []LuaExtensionPolicyConfig
	Wasm      []WasmExtensionPolicyConfig
	ExtProc   []ExtProcPolicyConfig
}

// EnvoyExtensionPolicyTargetRef represents the target reference for EnvoyExtensionPolicy
type EnvoyExtensionPolicyTargetRef struct {
	Group string
	Kind  string
	Name  string
}

// LuaExtensionPolicyConfig represents Lua extension configuration
type LuaExtensionPolicyConfig struct {
	Type     string
	Inline   string
	ValueRef *ValueRefPolicyConfig
}

// WasmExtensionPolicyConfig represents Wasm extension configuration
type WasmExtensionPolicyConfig struct {
	Name   string
	RootID string
	Code   WasmCodeSourcePolicyConfig
	Config *string
}

// WasmCodeSourcePolicyConfig represents Wasm code source
type WasmCodeSourcePolicyConfig struct {
	Type  string
	HTTP  *WasmHTTPSourcePolicyConfig
	Image *WasmImageSourcePolicyConfig
}

// WasmHTTPSourcePolicyConfig represents HTTP source for Wasm
type WasmHTTPSourcePolicyConfig struct {
	URL    string
	SHA256 string
}

// WasmImageSourcePolicyConfig represents OCI Image source for Wasm
type WasmImageSourcePolicyConfig struct {
	URL        string
	SHA256     string
	PullSecret *ValueRefPolicyConfig
}

// ExtProcPolicyConfig holds ext-proc config for K8s manifest generation
type ExtProcPolicyConfig struct {
	BackendRef     ExtProcBackendRefPolicyConfig
	ProcessingMode *ExtProcProcessingModeConfig
	FailOpen       bool
}

// ExtProcBackendRefPolicyConfig holds the user-facing service reference
type ExtProcBackendRefPolicyConfig struct {
	Name      string
	Namespace string
	Port      int
}

// ExtProcProcessingModeConfig holds processing mode for K8s
type ExtProcProcessingModeConfig struct {
	Request  *ExtProcBodyModeConfig
	Response *ExtProcBodyModeConfig
}

// ExtProcBodyModeConfig holds body mode for a phase
type ExtProcBodyModeConfig struct {
	Body string
}

// BuildEnvoyExtensionPolicy builds an EnvoyExtensionPolicy CRD
func BuildEnvoyExtensionPolicy(config *EnvoyExtensionPolicyK8sConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	spec := map[string]interface{}{
		"targetRefs": []map[string]interface{}{
			{
				"group": config.TargetRef.Group,
				"kind":  config.TargetRef.Kind,
				"name":  config.TargetRef.Name,
			},
		},
	}

	hasContent := false

	if len(config.Lua) > 0 {
		luaConfigs := make([]map[string]interface{}, 0, len(config.Lua))
		for _, lua := range config.Lua {
			luaMap := map[string]interface{}{
				"type": lua.Type,
			}
			if lua.Type == "Inline" && lua.Inline != "" {
				luaMap["inline"] = lua.Inline
			}
			if lua.Type == "ValueRef" && lua.ValueRef != nil {
				valueRef := map[string]interface{}{
					"kind": lua.ValueRef.Kind,
					"name": lua.ValueRef.Name,
				}
				if lua.ValueRef.Group != "" {
					valueRef["group"] = lua.ValueRef.Group
				}
				if lua.ValueRef.Namespace != "" {
					valueRef["namespace"] = lua.ValueRef.Namespace
				}
				luaMap["valueRef"] = valueRef
			}
			luaConfigs = append(luaConfigs, luaMap)
		}
		spec["lua"] = luaConfigs
		hasContent = true
	}

	if len(config.Wasm) > 0 {
		wasmConfigs := make([]map[string]interface{}, 0, len(config.Wasm))
		for _, wasm := range config.Wasm {
			wasmMap := map[string]interface{}{
				"name": wasm.Name,
			}
			if wasm.RootID != "" {
				wasmMap["rootID"] = wasm.RootID
			}
			if wasm.Config != nil {
				// Parse the JSON config string and set it directly as an object
				// Envoy Gateway will handle the protobuf wrapping internally
				var configObj interface{}
				if err := json.Unmarshal([]byte(*wasm.Config), &configObj); err == nil {
					wasmMap["config"] = configObj
				}
			}

			codeMap := map[string]interface{}{
				"type": wasm.Code.Type,
			}
			if wasm.Code.Type == "HTTP" && wasm.Code.HTTP != nil {
				codeMap["http"] = map[string]interface{}{
					"url":    wasm.Code.HTTP.URL,
					"sha256": wasm.Code.HTTP.SHA256,
				}
			}
			if wasm.Code.Type == "Image" && wasm.Code.Image != nil {
				imageMap := map[string]interface{}{
					"url": wasm.Code.Image.URL,
				}
				if wasm.Code.Image.SHA256 != "" {
					imageMap["sha256"] = wasm.Code.Image.SHA256
				}
				if wasm.Code.Image.PullSecret != nil {
					pullSecretRef := map[string]interface{}{
						"kind": wasm.Code.Image.PullSecret.Kind,
						"name": wasm.Code.Image.PullSecret.Name,
					}
					if wasm.Code.Image.PullSecret.Group != "" {
						pullSecretRef["group"] = wasm.Code.Image.PullSecret.Group
					}
					if wasm.Code.Image.PullSecret.Namespace != "" {
						pullSecretRef["namespace"] = wasm.Code.Image.PullSecret.Namespace
					}
					imageMap["pullSecretRef"] = pullSecretRef
				}
				codeMap["image"] = imageMap
			}
			wasmMap["code"] = codeMap

			wasmConfigs = append(wasmConfigs, wasmMap)
		}
		spec["wasm"] = wasmConfigs
		hasContent = true
	}

	if len(config.ExtProc) > 0 {
		var extProcEntries []interface{}
		for _, ep := range config.ExtProc {
			// Determine Backend CRD name: domain-level vs route-level
			var extProcBackendName string
			if config.RouteID != "" {
				extProcBackendName = GenerateExtProcBackendName(config.RouteID)
			} else if config.DomainID != "" {
				extProcBackendName = GenerateExtProcBackendNameForDomain(config.TargetRef.Name)
			}
			entry := map[string]interface{}{
				"backendRefs": []interface{}{
					map[string]interface{}{
						"group":     "",
						"kind":      "Backend",
						"name":      extProcBackendName,
						"namespace": config.Namespace,
					},
				},
			}

			if ep.ProcessingMode != nil {
				pm := map[string]interface{}{}
				if ep.ProcessingMode.Request != nil && ep.ProcessingMode.Request.Body != "" && ep.ProcessingMode.Request.Body != "None" {
					pm["request"] = map[string]interface{}{
						"body": ep.ProcessingMode.Request.Body,
					}
				}
				if ep.ProcessingMode.Response != nil && ep.ProcessingMode.Response.Body != "" && ep.ProcessingMode.Response.Body != "None" {
					pm["response"] = map[string]interface{}{
						"body": ep.ProcessingMode.Response.Body,
					}
				}
				if len(pm) > 0 {
					entry["processingMode"] = pm
				}
			}

			if ep.FailOpen {
				entry["failOpen"] = true
			}

			extProcEntries = append(extProcEntries, entry)
		}
		spec["extProc"] = extProcEntries
		hasContent = true
	}

	if !hasContent {
		return nil
	}

	eepLabels := map[string]interface{}{
		"app.kubernetes.io/managed-by": "fastgateway",
		"fastgateway.dev/gateway-id":   config.GatewayID,
	}
	if config.RouteID != "" {
		eepLabels["fastgateway.dev/route-id"] = config.RouteID
	}
	if config.DomainID != "" {
		eepLabels["fastgateway.dev/domain-id"] = config.DomainID
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "EnvoyExtensionPolicy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    eepLabels,
			},
			"spec": spec,
		},
	}
}

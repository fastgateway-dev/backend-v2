package kubernetes

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GatewayConfig represents Gateway configuration
type GatewayConfig struct {
	Name               string
	Namespace          string
	GatewayClassName   string
	Hostname           string
	TLSMode            string // tls_only, no_tls, both
	HTTPPort           int
	HTTPSPort          int
	TLSSecretName      string
	TLSSecretNamespace string
	TLSPolicy          string
	Annotations        map[string]string
}

// BuildGatewayObject builds a Gateway unstructured object from the given config.
func BuildGatewayObject(config *GatewayConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	// Build listeners based on TLS mode
	var listeners []interface{}

	// Convert TLS policy to Gateway API format (capitalized)
	tlsPolicyMode := "Terminate" // default
	if strings.ToLower(config.TLSPolicy) == "passthrough" {
		tlsPolicyMode = "Passthrough"
	}

	// HTTP listener helper
	buildHTTPListener := func() map[string]interface{} {
		return map[string]interface{}{
			"name":     "http",
			"port":     int64(config.HTTPPort),
			"protocol": "HTTP",
			"hostname": config.Hostname,
		}
	}

	// HTTPS listener helper
	buildHTTPSListener := func() map[string]interface{} {
		listener := map[string]interface{}{
			"name":     "https",
			"port":     int64(config.HTTPSPort),
			"protocol": "HTTPS",
			"hostname": config.Hostname,
		}
		// Only add TLS config if secret name is provided
		if config.TLSSecretName != "" {
			certRef := map[string]interface{}{
				"kind": "Secret",
				"name": config.TLSSecretName,
			}
			// Add namespace only for cross-namespace references
			if config.TLSSecretNamespace != "" && config.TLSSecretNamespace != config.Namespace {
				certRef["namespace"] = config.TLSSecretNamespace
			}
			listener["tls"] = map[string]interface{}{
				"mode":            tlsPolicyMode,
				"certificateRefs": []interface{}{certRef},
			}
		}
		return listener
	}

	// Build listeners based on TLS mode
	switch config.TLSMode {
	case "tls_only":
		listeners = []interface{}{buildHTTPSListener()}
	case "no_tls":
		listeners = []interface{}{buildHTTPListener()}
	case "both":
		listeners = []interface{}{buildHTTPListener(), buildHTTPSListener()}
	default:
		// Default to TLS only if TLS secret is provided, otherwise HTTP only
		if config.TLSSecretName != "" {
			listeners = []interface{}{buildHTTPSListener()}
		} else {
			listeners = []interface{}{buildHTTPListener()}
		}
	}

	// Build metadata with annotations
	metadata := map[string]interface{}{
		"name":      config.Name,
		"namespace": config.Namespace,
		"labels": map[string]interface{}{
			"app.kubernetes.io/managed-by": "fastgateway",
		},
	}

	// Add annotations if provided
	if len(config.Annotations) > 0 {
		annotations := make(map[string]interface{})
		for k, v := range config.Annotations {
			annotations[k] = v
		}
		metadata["annotations"] = annotations
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "Gateway",
			"metadata":   metadata,
			"spec": map[string]interface{}{
				"gatewayClassName": config.GatewayClassName,
				"listeners":        listeners,
			},
		},
	}
}

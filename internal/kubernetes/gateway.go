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
	TLSSecretName      string
	TLSSecretNamespace string
	Annotations        map[string]string

	// HostnameListeners are the hostname-routed (HTTP/HTTPS) listeners emitted
	// for a Domain gateway, in the order given. Migrated domains carry exactly
	// "http" and/or "https" names, ordered [http, https], which keeps the
	// rendered Gateway byte-identical to the pre-listener-model builder.
	// Excluded from JSON/YAML (json:"-") so the Domain gateway goldens, which
	// serialize this struct, stay stable.
	HostnameListeners []HostnameListener `json:"-"`

	// Listeners, when non-empty, switches the Gateway to L4 (TCP/UDP) mode:
	// exactly these listeners are emitted and HostnameListeners and the other
	// HTTP/HTTPS fields above (Hostname, TLS*) are ignored. Excluded from
	// JSON/YAML (json:"-") so the Domain gateway goldens, which serialize this
	// struct, stay byte-identical.
	Listeners []L4Listener `json:"-"`
}

// HostnameListener is an HTTP or HTTPS Gateway listener bound to the config's
// Hostname.
type HostnameListener struct {
	Name     string // listener name, e.g. "http" / "https"
	Protocol string // "HTTP" or "HTTPS"
	Port     int
	TLSMode  string // "Terminate" (default) or "Passthrough"; HTTPS only
}

// L4Listener is a TCP or UDP Gateway listener (no hostname, no TLS).
type L4Listener struct {
	Name     string
	Protocol string // "TCP" or "UDP"
	Port     int
}

// buildL4Listener renders an L4 listener whose allowedRoutes is restricted to
// the matching route kind (TCPRoute or UDPRoute).
func buildL4Listener(l L4Listener) map[string]interface{} {
	kind := "TCPRoute"
	if strings.ToUpper(l.Protocol) == "UDP" {
		kind = "UDPRoute"
	}
	return map[string]interface{}{
		"name":     l.Name,
		"port":     int64(l.Port),
		"protocol": strings.ToUpper(l.Protocol),
		"allowedRoutes": map[string]interface{}{
			"kinds": []interface{}{
				map[string]interface{}{"kind": kind},
			},
		},
	}
}

// BuildGatewayObject builds a Gateway unstructured object from the given config.
func BuildGatewayObject(config *GatewayConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	var listeners []interface{}

	// HTTP listener helper
	buildHTTPListener := func(l HostnameListener) map[string]interface{} {
		return map[string]interface{}{
			"name":     l.Name,
			"port":     int64(l.Port),
			"protocol": "HTTP",
			"hostname": config.Hostname,
		}
	}

	// HTTPS listener helper
	buildHTTPSListener := func(l HostnameListener) map[string]interface{} {
		// Gateway API format (capitalized); Terminate is the default.
		tlsMode := "Terminate"
		if strings.EqualFold(l.TLSMode, "passthrough") {
			tlsMode = "Passthrough"
		}
		listener := map[string]interface{}{
			"name":     l.Name,
			"port":     int64(l.Port),
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
				"mode":            tlsMode,
				"certificateRefs": []interface{}{certRef},
			}
		}
		return listener
	}

	// Hostname-routed listeners, emitted in the order given.
	for _, l := range config.HostnameListeners {
		if strings.EqualFold(l.Protocol, "HTTPS") {
			listeners = append(listeners, buildHTTPSListener(l))
		} else {
			listeners = append(listeners, buildHTTPListener(l))
		}
	}

	// L4 (stream) gateways: explicit TCP/UDP listeners replace the HTTP/HTTPS ones.
	if len(config.Listeners) > 0 {
		listeners = make([]interface{}, 0, len(config.Listeners))
		for _, l := range config.Listeners {
			listeners = append(listeners, buildL4Listener(l))
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

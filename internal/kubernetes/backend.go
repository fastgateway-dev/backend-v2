package kubernetes

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// BackendConfig represents the configuration for an Envoy Gateway Backend CRD
type BackendConfig struct {
	Name        string
	Namespace   string
	RouteID     string                  // Route UUID for labeling and cleanup
	GatewayID   string                  // Gateway UUID for labeling
	AddressType string                  // "fqdn" or "ip"
	Address     string                  // FQDN hostname or IP address
	Port        int32                   // Port number
	Fallback    bool                    // If true, sets spec.fallback: true for priority-based failover
	TLS         *BackendTLSPolicyConfig // TLS configuration (optional)
}

// BackendTLSPolicyConfig represents TLS configuration for Backend CRD
type BackendTLSPolicyConfig struct {
	InsecureSkipVerify   bool                          // Skip backend cert verification
	SNI                  string                        // User-provided SNI override (empty = auto-derive)
	ClientCertificateRef *BackendSecretRefConfig       // For mTLS
	CACertificateRefs    []BackendCertificateRefConfig // Required for TLS (unless insecureSkipVerify)
}

// BackendSecretRefConfig represents a reference to a Secret
type BackendSecretRefConfig struct {
	Name      string
	Namespace string
}

// BackendCertificateRefConfig represents a reference to a Secret or ConfigMap
type BackendCertificateRefConfig struct {
	Kind      string // "Secret" or "ConfigMap"
	Name      string
	Namespace string
}

// BuildBackend builds an Envoy Gateway Backend CRD as unstructured object
func BuildBackend(config *BackendConfig) *unstructured.Unstructured {
	// Build endpoint based on address type
	var endpoint map[string]interface{}
	if config.AddressType == "fqdn" {
		endpoint = map[string]interface{}{
			"fqdn": map[string]interface{}{
				"hostname": config.Address,
				"port":     config.Port,
			},
		}
	} else {
		// IP address
		endpoint = map[string]interface{}{
			"ip": map[string]interface{}{
				"address": config.Address,
				"port":    config.Port,
			},
		}
	}

	// Build spec with endpoints
	spec := map[string]interface{}{
		"endpoints": []interface{}{endpoint},
	}

	// Add fallback flag if enabled (for priority-based failover)
	if config.Fallback {
		spec["fallback"] = true
	}

	// Add TLS configuration if present
	if config.TLS != nil {
		tlsSpec := map[string]interface{}{}

		// Add insecureSkipVerify if true
		if config.TLS.InsecureSkipVerify {
			tlsSpec["insecureSkipVerify"] = true
		}

		// Add CA certificate references (only when not skipping verification)
		if !config.TLS.InsecureSkipVerify && len(config.TLS.CACertificateRefs) > 0 {
			caRefs := make([]interface{}, len(config.TLS.CACertificateRefs))
			for i, ref := range config.TLS.CACertificateRefs {
				ns := ref.Namespace
				if ns == "" {
					ns = FastGatewayNamespace
				}
				caRef := map[string]interface{}{
					"group":     "",
					"kind":      ref.Kind,
					"name":      ref.Name,
					"namespace": ns,
				}
				caRefs[i] = caRef
			}
			tlsSpec["caCertificateRefs"] = caRefs
		}

		// Add client certificate reference for mTLS
		if config.TLS.ClientCertificateRef != nil {
			clientNs := config.TLS.ClientCertificateRef.Namespace
			if clientNs == "" {
				clientNs = FastGatewayNamespace
			}
			clientRef := map[string]interface{}{
				"group":     "",
				"kind":      "Secret",
				"name":      config.TLS.ClientCertificateRef.Name,
				"namespace": clientNs,
			}
			tlsSpec["clientCertificateRef"] = clientRef
		}

		// Set SNI: user override takes precedence, otherwise auto-derive from FQDN
		if config.TLS.SNI != "" {
			tlsSpec["sni"] = config.TLS.SNI
		} else if config.AddressType == "fqdn" && config.Address != "" {
			tlsSpec["sni"] = config.Address
		}

		spec["tls"] = tlsSpec
	}

	backend := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "Backend",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    ForRouteInterface(config.GatewayID, config.RouteID),
			},
			"spec": spec,
		},
	}

	return backend
}

// ExtAuthBackendConfig holds configuration for building ext-auth Backend CRD
type ExtAuthBackendConfig struct {
	Name      string // Backend CRD name
	Namespace string
	GatewayID string
	RouteID   string
	ClientID  string // Empty for general mode, set for client mode
	Service   models.ExtAuthBackendRef
}

// BuildExtAuthBackend builds a Backend CRD for external auth service
func BuildExtAuthBackend(config *ExtAuthBackendConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	// Build FQDN endpoint pointing to the K8s service
	serviceNamespace := config.Service.Namespace
	if serviceNamespace == "" {
		serviceNamespace = config.Namespace
	}
	fqdn := fmt.Sprintf("%s.%s.svc.cluster.local", config.Service.Name, serviceNamespace)

	spec := map[string]interface{}{
		"endpoints": []interface{}{
			map[string]interface{}{
				"fqdn": map[string]interface{}{
					"hostname": fqdn,
					"port":     config.Service.Port,
				},
			},
		},
	}

	labels := map[string]interface{}{
		"app.kubernetes.io/managed-by": "fastgateway",
		"fastgateway.dev/gateway-id":   config.GatewayID,
		"fastgateway.dev/route-id":     config.RouteID,
		"fastgateway.dev/type":         "ext-auth",
	}
	if config.ClientID != "" {
		labels["fastgateway.dev/client-id"] = config.ClientID
	}

	backend := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "Backend",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    labels,
			},
			"spec": spec,
		},
	}

	return backend
}

// GenerateExtAuthBackendName generates a unique name for ext-auth Backend CRD
func GenerateExtAuthBackendName(routeID, clientID string) string {
	if clientID != "" {
		// Client mode: include client ID
		return fmt.Sprintf("fg-extauth-%s-%s", routeID[:8], clientID[:8])
	}
	// General mode: just route ID
	return fmt.Sprintf("fg-extauth-%s", routeID[:8])
}

// ExtProcBackendConfig holds config for building an ext-proc Backend CRD
type ExtProcBackendConfig struct {
	Name      string
	Namespace string
	GatewayID string
	RouteID   string
	DomainID  string
	Service   ExtProcBackendRefPolicyConfig
}

// BuildExtProcBackend builds a Backend CRD for an ext-proc service
func BuildExtProcBackend(config *ExtProcBackendConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	fqdn := fmt.Sprintf("%s.%s.svc.cluster.local", config.Service.Name, config.Service.Namespace)

	labels := map[string]interface{}{
		"app.kubernetes.io/managed-by": "fastgateway",
		"fastgateway.dev/gateway-id":   config.GatewayID,
		"fastgateway.dev/type":         "ext-proc",
	}
	if config.RouteID != "" {
		labels["fastgateway.dev/route-id"] = config.RouteID
	}
	if config.DomainID != "" {
		labels["fastgateway.dev/domain-id"] = config.DomainID
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "Backend",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    labels,
			},
			"spec": map[string]interface{}{
				"endpoints": []interface{}{
					map[string]interface{}{
						"fqdn": map[string]interface{}{
							"hostname": fqdn,
							"port":     int64(config.Service.Port),
						},
					},
				},
			},
		},
	}
}

// GenerateExtProcBackendName generates the Backend CRD name for an ext-proc service
func GenerateExtProcBackendName(routeID string) string {
	return fmt.Sprintf("ext-proc-backend-%s", routeID)
}

// GenerateExtProcBackendNameForDomain generates the Backend CRD name for a domain-level ext-proc service
func GenerateExtProcBackendNameForDomain(gatewayName string) string {
	return gatewayName + "-eep-extproc"
}

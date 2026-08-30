package routeplan

import (
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"sigs.k8s.io/yaml"
)

// GenerateBackendYAMLs generates Backend CRD YAML for external backends,
// or for ALL backends when failover is enabled
func GenerateBackendYAMLs(route *models.Route, domain *models.Domain) string {
	hasFailover := route.Config.HasFailover()
	var yamls []string
	for i, backend := range route.Config.Backends {
		// Generate Backend CRD if external, failover is enabled, or TLS is configured
		if backend.Type == models.BackendTypeExternal || hasFailover || backend.TLS != nil {
			backendName := fmt.Sprintf("%s-backend-%d", route.K8sRouteName, i)

			var addressType, address string
			if backend.Type == models.BackendTypeExternal {
				addressType = string(backend.AddressType)
				address = backend.Address
			} else {
				// Kubernetes service - use FQDN format for Backend CRD
				addressType = "fqdn"
				ns := backend.Namespace
				if ns == "" {
					ns = "default"
				}
				address = fmt.Sprintf("%s.%s.svc.cluster.local", backend.Service, ns)
			}

			config := &kubernetes.BackendConfig{
				Name:        backendName,
				Namespace:   domain.Namespace,
				RouteID:     route.ID.String(),
				GatewayID:   domain.ID.String(),
				AddressType: addressType,
				Address:     address,
				Port:        int32(backend.Port),
				Fallback:    backend.Fallback,
			}

			// Add TLS configuration if present
			if backend.TLS != nil {
				config.TLS = &kubernetes.BackendTLSPolicyConfig{
					InsecureSkipVerify: backend.TLS.InsecureSkipVerify,
					SNI:                backend.TLS.SNI,
				}

				// Map CA certificate refs (only when not insecureSkipVerify)
				if !backend.TLS.InsecureSkipVerify && len(backend.TLS.CACertificateRefs) > 0 {
					config.TLS.CACertificateRefs = make([]kubernetes.BackendCertificateRefConfig, len(backend.TLS.CACertificateRefs))
					for j, ref := range backend.TLS.CACertificateRefs {
						config.TLS.CACertificateRefs[j] = kubernetes.BackendCertificateRefConfig{
							Kind:      ref.Kind,
							Name:      ref.Name,
							Namespace: ref.Namespace,
						}
					}
				}

				// Map client certificate ref for mTLS
				if backend.TLS.ClientCertificateRef != nil {
					config.TLS.ClientCertificateRef = &kubernetes.BackendSecretRefConfig{
						Name:      backend.TLS.ClientCertificateRef.Name,
						Namespace: backend.TLS.ClientCertificateRef.Namespace,
					}
				}
			}

			obj := kubernetes.BuildBackend(config)
			yamlBytes, err := yaml.Marshal(obj.Object)
			if err == nil {
				yamls = append(yamls, string(yamlBytes))
			}
		}
	}
	if len(yamls) == 0 {
		return ""
	}
	return strings.Join(yamls, "---\n")
}

// GenerateDirectResponseYAMLs generates HTTPRouteFilter and ConfigMap YAML for direct response routes
func GenerateDirectResponseYAMLs(route *models.Route, domain *models.Domain) (string, string) {
	if route.Config.DirectResponse == nil {
		return "", ""
	}

	hrfName := kubernetes.HTTPRouteFilterName(route.K8sRouteName)
	cmName := route.K8sRouteName + "-dr-cm"

	var configMapYAML string
	// Generate ConfigMap YAML if body is provided
	if route.Config.DirectResponse.Body != nil && route.Config.DirectResponse.Body.Inline != "" {
		cmConfig := &kubernetes.DirectResponseConfigMapConfig{
			Name:        cmName,
			Namespace:   domain.Namespace,
			GatewayID:   domain.ID.String(),
			RouteID:     route.ID.String(),
			BodyContent: route.Config.DirectResponse.Body.Inline,
		}
		cmObj := kubernetes.BuildDirectResponseConfigMap(cmConfig)
		if cmObj != nil {
			yamlBytes, err := yaml.Marshal(cmObj.Object)
			if err == nil {
				configMapYAML = string(yamlBytes)
			}
		}
	}

	// Generate HTTPRouteFilter YAML
	hrfConfig := &kubernetes.HTTPRouteFilterConfig{
		Name:      hrfName,
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		DirectResponse: &kubernetes.DirectResponseFilterConfig{
			StatusCode:  route.Config.DirectResponse.StatusCode,
			ContentType: route.Config.DirectResponse.ContentType,
		},
	}

	// Set body configuration
	if route.Config.DirectResponse.Body != nil && route.Config.DirectResponse.Body.Inline != "" {
		hrfConfig.DirectResponse.Body = &kubernetes.DirectResponseBodyFilterConfig{
			Type: "ValueRef",
			ValueRef: &kubernetes.DirectResponseValueRef{
				Group: "",
				Kind:  "ConfigMap",
				Name:  cmName,
			},
		}
	}

	var httpRouteFilterYAML string
	hrfObj := kubernetes.BuildHTTPRouteFilter(hrfConfig)
	if hrfObj != nil {
		yamlBytes, err := yaml.Marshal(hrfObj.Object)
		if err == nil {
			httpRouteFilterYAML = string(yamlBytes)
		}
	}

	return httpRouteFilterYAML, configMapYAML
}

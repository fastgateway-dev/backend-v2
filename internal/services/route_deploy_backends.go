package services

import (
	"context"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// deployDirectResponse deploys HTTPRouteFilter and ConfigMap for direct response routes
func (s *RouteService) deployDirectResponse(ctx context.Context, route *models.Route, domain *models.Domain) error {
	if route.Config.DirectResponse == nil {
		// Not a direct response route
		return nil
	}

	hrfName := kubernetes.HTTPRouteFilterName(route.K8sRouteName)
	cmName := route.K8sRouteName + "-dr-cm"

	// Check if we need a ConfigMap (body is provided)
	if route.Config.DirectResponse.Body != nil && route.Config.DirectResponse.Body.Inline != "" {
		// Create ConfigMap for the body
		cmConfig := &kubernetes.DirectResponseConfigMapConfig{
			Name:        cmName,
			Namespace:   domain.Namespace,
			GatewayID:   domain.ID.String(),
			RouteID:     route.ID.String(),
			BodyContent: route.Config.DirectResponse.Body.Inline,
		}
		if err := s.k8sRoutes.ApplyDirectResponseConfigMap(ctx, domain.ProjectID, cmConfig); err != nil {
			return fmt.Errorf("failed to apply ConfigMap: %w", err)
		}
	}

	// Create HTTPRouteFilter
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
		// Use ValueRef to reference ConfigMap
		hrfConfig.DirectResponse.Body = &kubernetes.DirectResponseBodyFilterConfig{
			Type: "ValueRef",
			ValueRef: &kubernetes.DirectResponseValueRef{
				Group: "",
				Kind:  "ConfigMap",
				Name:  cmName,
			},
		}
	}

	if err := s.k8sRoutes.ApplyHTTPRouteFilter(ctx, domain.ProjectID, hrfConfig); err != nil {
		return fmt.Errorf("failed to apply HTTPRouteFilter: %w", err)
	}

	return nil
}

// deleteDirectResponse deletes HTTPRouteFilter and ConfigMap for direct response routes
func (s *RouteService) deleteDirectResponse(ctx context.Context, route *models.Route, domain *models.Domain) error {
	if route.Config.DirectResponse == nil {
		// Not a direct response route
		return nil
	}

	hrfName := kubernetes.HTTPRouteFilterName(route.K8sRouteName)
	cmName := route.K8sRouteName + "-dr-cm"

	// Delete HTTPRouteFilter
	if err := s.k8sRoutes.DeleteHTTPRouteFilter(ctx, domain.ProjectID, domain.Namespace, hrfName); err != nil {
		log.Printf("Warning: failed to delete HTTPRouteFilter %s: %v", hrfName, err)
	}

	// Delete ConfigMap
	if err := s.k8sRoutes.DeleteDirectResponseConfigMap(ctx, domain.ProjectID, domain.Namespace, cmName); err != nil {
		log.Printf("Warning: failed to delete ConfigMap %s: %v", cmName, err)
	}

	return nil
}

// deployBackends creates or updates Backend CRDs for external backends,
// or for ALL backends when failover is enabled (priority-based failover requires Backend CRDs)
func (s *RouteService) deployBackends(ctx context.Context, route *models.Route, domain *models.Domain) error {
	hasFailover := route.Config.HasFailover()

	for i, backend := range route.Config.Backends {
		// Create Backend CRD if:
		// 1. It's an external backend (always needs Backend CRD), OR
		// 2. Failover is enabled (all backends need Backend CRDs for priority), OR
		// 3. TLS is configured (K8s backends with TLS need Backend CRDs)
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

			backendConfig := &kubernetes.BackendConfig{
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
				backendConfig.TLS = &kubernetes.BackendTLSPolicyConfig{
					InsecureSkipVerify: backend.TLS.InsecureSkipVerify,
					SNI:                backend.TLS.SNI,
				}

				// Map CA certificate refs (only when not insecureSkipVerify)
				if !backend.TLS.InsecureSkipVerify && len(backend.TLS.CACertificateRefs) > 0 {
					backendConfig.TLS.CACertificateRefs = make([]kubernetes.BackendCertificateRefConfig, len(backend.TLS.CACertificateRefs))
					for j, ref := range backend.TLS.CACertificateRefs {
						backendConfig.TLS.CACertificateRefs[j] = kubernetes.BackendCertificateRefConfig{
							Kind:      ref.Kind,
							Name:      ref.Name,
							Namespace: ref.Namespace,
						}
					}
				}

				// Map client certificate ref for mTLS
				if backend.TLS.ClientCertificateRef != nil {
					backendConfig.TLS.ClientCertificateRef = &kubernetes.BackendSecretRefConfig{
						Name:      backend.TLS.ClientCertificateRef.Name,
						Namespace: backend.TLS.ClientCertificateRef.Namespace,
					}
				}
			}

			if err := s.k8sBackends.UpdateBackend(ctx, domain.ProjectID, backendConfig); err != nil {
				return fmt.Errorf("failed to create/update Backend CRD for %s: %w", backendName, err)
			}
		}
	}
	return nil
}

// deleteBackends deletes all Backend CRDs associated with a route
func (s *RouteService) deleteBackends(ctx context.Context, route *models.Route, domain *models.Domain) error {
	return s.k8sBackendReaper.DeleteBackendsByRoute(ctx, domain.ProjectID, domain.Namespace, route.ID.String())
}

// cleanupStaleBackends deletes Backend CRDs that are no longer in the route config.
// It lists all Backend CRDs for this route by label, compares with the current config,
// and only deletes ones that are no longer needed.
func (s *RouteService) cleanupStaleBackends(ctx context.Context, route *models.Route, domain *models.Domain) error {
	hasFailover := route.Config.HasFailover()

	// Build a set of expected backend names from the current config
	expectedNames := make(map[string]bool)
	for i, backend := range route.Config.Backends {
		// Include backend if it's external, failover is enabled, or TLS is configured
		if backend.Type == models.BackendTypeExternal || hasFailover || backend.TLS != nil {
			backendName := fmt.Sprintf("%s-backend-%d", route.K8sRouteName, i)
			expectedNames[backendName] = true
		}
	}

	// Delete only backends that are no longer expected
	return s.k8sBackendReaper.DeleteStaleBackendsByRoute(ctx, domain.ProjectID, domain.Namespace, route.ID.String(), expectedNames)
}

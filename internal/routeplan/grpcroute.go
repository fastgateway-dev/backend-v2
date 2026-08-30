package routeplan

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// BuildGRPCRouteConfig assembles the GRPCRouteConfig for a route.
//
// This replaces the former deploy/preview pair. Unlike the HTTPRoute pair
// (Task 7), no drift was found between them -- TestDifferentialGRPCRoute
// passed before this collapse across an expanded fixture set, and the diff
// between the two bodies was comment-only. This collapse is therefore a
// pure refactor: it changes no output.
//
// Pure: no receiver, no repository access, no clock, no environment.
func BuildGRPCRouteConfig(route *models.Route, domain *models.Domain) *kubernetes.GRPCRouteConfig {
	rules := make([]kubernetes.GRPCRouteRule, 0)

	for _, match := range route.Config.Matches {
		rule := kubernetes.GRPCRouteRule{}

		// Convert gRPC service/method matches
		if match.GRPCService != nil {
			rule.GRPCService = &kubernetes.GRPCMethodMatchConfig{
				Type:  match.GRPCService.Type,
				Value: match.GRPCService.Value,
			}
		}
		if match.GRPCMethod != nil {
			rule.GRPCMethod = &kubernetes.GRPCMethodMatchConfig{
				Type:  match.GRPCMethod.Type,
				Value: match.GRPCMethod.Value,
			}
		}

		// Convert header matches
		if len(match.Headers) > 0 {
			rule.Headers = make([]kubernetes.HeaderMatch, 0, len(match.Headers))
			for _, h := range match.Headers {
				rule.Headers = append(rule.Headers, kubernetes.HeaderMatch{
					Name:  h.Name,
					Type:  h.Type,
					Value: h.Value,
				})
			}
		}

		// Convert backend refs
		hasFailover := route.Config.HasFailover()
		for i, backend := range route.Config.Backends {
			if backend.Type == models.BackendTypeExternal || hasFailover || backend.TLS != nil {
				backendName := fmt.Sprintf("%s-backend-%d", route.K8sRouteName, i)
				rule.BackendRefs = append(rule.BackendRefs, kubernetes.BackendRef{
					Name:       backendName,
					Namespace:  domain.Namespace,
					Port:       backend.Port,
					Weight:     backend.Weight,
					IsExternal: true,
					Group:      "gateway.envoyproxy.io",
					Kind:       "Backend",
				})
			} else {
				rule.BackendRefs = append(rule.BackendRefs, kubernetes.BackendRef{
					Name:      backend.Service,
					Namespace: backend.Namespace,
					Port:      backend.Port,
					Weight:    backend.Weight,
				})
			}
		}

		rules = append(rules, rule)
	}

	// If no matches defined, add a single rule with just backends (match all)
	if len(rules) == 0 && len(route.Config.Backends) > 0 {
		rule := kubernetes.GRPCRouteRule{}
		hasFailover := route.Config.HasFailover()
		for i, backend := range route.Config.Backends {
			if backend.Type == models.BackendTypeExternal || hasFailover || backend.TLS != nil {
				backendName := fmt.Sprintf("%s-backend-%d", route.K8sRouteName, i)
				rule.BackendRefs = append(rule.BackendRefs, kubernetes.BackendRef{
					Name:       backendName,
					Namespace:  domain.Namespace,
					Port:       backend.Port,
					Weight:     backend.Weight,
					IsExternal: true,
					Group:      "gateway.envoyproxy.io",
					Kind:       "Backend",
				})
			} else {
				rule.BackendRefs = append(rule.BackendRefs, kubernetes.BackendRef{
					Name:      backend.Service,
					Namespace: backend.Namespace,
					Port:      backend.Port,
					Weight:    backend.Weight,
				})
			}
		}
		rules = append(rules, rule)
	}

	config := &kubernetes.GRPCRouteConfig{
		Name:        route.K8sRouteName,
		Namespace:   domain.Namespace,
		GatewayName: domain.K8sGatewayName,
		GatewayID:   domain.ID.String(),
		RouteID:     route.ID.String(),
		Hostname:    domain.Hostname,
		Rules:       rules,
	}

	// Add header modifiers
	if route.Config.RequestHeaderModifier != nil {
		config.RequestHeaderModifier = convertHeaderModifier(route.Config.RequestHeaderModifier)
	}
	if route.Config.ResponseHeaderModifier != nil {
		config.ResponseHeaderModifier = convertHeaderModifier(route.Config.ResponseHeaderModifier)
	}

	// Add mirrors
	if len(route.Config.Mirrors) > 0 {
		config.Mirrors = make([]kubernetes.MirrorRef, 0, len(route.Config.Mirrors))
		for _, m := range route.Config.Mirrors {
			config.Mirrors = append(config.Mirrors, kubernetes.MirrorRef{
				Name:      m.Service,
				Namespace: m.Namespace,
				Port:      m.Port,
			})
		}
	}

	return config
}

// BuildGRPCRouteConfigForYAML builds GRPCRouteConfig for YAML/preview
// generation. Since this collapse, this and (*RouteService).buildGRPCRouteConfig
// are identical one-line delegations to buildGRPCRouteConfigUnified -- this one
// exists so preview callers without a RouteService receiver can still reach
// it directly.
func BuildGRPCRouteConfigForYAML(route *models.Route, domain *models.Domain) *kubernetes.GRPCRouteConfig {
	return BuildGRPCRouteConfig(route, domain)
}

package kubernetes

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GRPCRouteConfig holds configuration for building a GRPCRoute K8s object
type GRPCRouteConfig struct {
	Name                   string
	Namespace              string
	GatewayName            string
	GatewayID              string
	RouteID                string
	Hostname               string
	Rules                  []GRPCRouteRule
	Mirrors                []MirrorRef
	RequestHeaderModifier  *HTTPHeaderModifier
	ResponseHeaderModifier *HTTPHeaderModifier
}

// GRPCRouteRule defines a single gRPC route matching rule
type GRPCRouteRule struct {
	GRPCService *GRPCMethodMatchConfig // optional
	GRPCMethod  *GRPCMethodMatchConfig // optional
	Headers     []HeaderMatch
	BackendRefs []BackendRef
}

// GRPCMethodMatchConfig holds the type and value for a gRPC service/method match
type GRPCMethodMatchConfig struct {
	Type  string // "Exact" or "RegularExpression"
	Value string
}

// BuildGRPCRouteObject builds a typed GRPCRoute K8s object from config
func BuildGRPCRouteObject(config *GRPCRouteConfig) *gatewayv1.GRPCRoute {
	if config == nil {
		return nil
	}

	namespace := gatewayv1.Namespace(config.Namespace)

	rules := make([]gatewayv1.GRPCRouteRule, 0, len(config.Rules))
	for _, rule := range config.Rules {
		grpcRule := gatewayv1.GRPCRouteRule{}

		// Build match
		match := gatewayv1.GRPCRouteMatch{}
		hasMatch := false

		// gRPC method match (service + method)
		if rule.GRPCService != nil || rule.GRPCMethod != nil {
			methodMatch := &gatewayv1.GRPCMethodMatch{}
			if rule.GRPCService != nil && rule.GRPCService.Value != "" {
				matchType := gatewayv1.GRPCMethodMatchType(rule.GRPCService.Type)
				if matchType == "" {
					matchType = gatewayv1.GRPCMethodMatchExact
				}
				methodMatch.Type = &matchType
				methodMatch.Service = &rule.GRPCService.Value
			}
			if rule.GRPCMethod != nil && rule.GRPCMethod.Value != "" {
				matchType := gatewayv1.GRPCMethodMatchType(rule.GRPCMethod.Type)
				if matchType == "" {
					matchType = gatewayv1.GRPCMethodMatchExact
				}
				// GRPCMethodMatch has a single Type for both service and method
				if methodMatch.Type == nil {
					methodMatch.Type = &matchType
				}
				methodMatch.Method = &rule.GRPCMethod.Value
			}
			match.Method = methodMatch
			hasMatch = true
		}

		// Header matches
		if len(rule.Headers) > 0 {
			headerMatches := make([]gatewayv1.GRPCHeaderMatch, 0, len(rule.Headers))
			for _, h := range rule.Headers {
				headerType := gatewayv1.GRPCHeaderMatchType(h.Type)
				headerMatches = append(headerMatches, gatewayv1.GRPCHeaderMatch{
					Type:  &headerType,
					Name:  gatewayv1.GRPCHeaderName(h.Name),
					Value: h.Value,
				})
			}
			match.Headers = headerMatches
			hasMatch = true
		}

		if hasMatch {
			grpcRule.Matches = []gatewayv1.GRPCRouteMatch{match}
		}

		// Build filters (request/response header modifiers + mirrors)
		filters := buildGRPCRouteFilters(config.RequestHeaderModifier, config.ResponseHeaderModifier)

		// Add mirror filters
		if len(config.Mirrors) > 0 {
			for _, mirror := range config.Mirrors {
				mirrorNs := gatewayv1.Namespace(mirror.Namespace)
				port := gatewayv1.PortNumber(mirror.Port)
				filters = append(filters, gatewayv1.GRPCRouteFilter{
					Type: gatewayv1.GRPCRouteFilterRequestMirror,
					RequestMirror: &gatewayv1.HTTPRequestMirrorFilter{
						BackendRef: gatewayv1.BackendObjectReference{
							Name:      gatewayv1.ObjectName(mirror.Name),
							Namespace: &mirrorNs,
							Port:      &port,
						},
					},
				})
			}
		}

		if len(filters) > 0 {
			grpcRule.Filters = filters
		}

		// Build backend refs
		if len(rule.BackendRefs) > 0 {
			backendRefs := make([]gatewayv1.GRPCBackendRef, 0, len(rule.BackendRefs))
			for _, backend := range rule.BackendRefs {
				port := gatewayv1.PortNumber(backend.Port)

				ref := gatewayv1.GRPCBackendRef{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: gatewayv1.ObjectName(backend.Name),
							Port: &port,
						},
					},
				}
				if backend.Weight > 0 {
					weight := int32(backend.Weight)
					ref.BackendRef.Weight = &weight
				}

				if backend.IsExternal {
					group := gatewayv1.Group(backend.Group)
					kind := gatewayv1.Kind(backend.Kind)
					ref.BackendRef.Group = &group
					ref.BackendRef.Kind = &kind
				} else if backend.Namespace != "" {
					ns := gatewayv1.Namespace(backend.Namespace)
					ref.BackendRef.Namespace = &ns
				}

				backendRefs = append(backendRefs, ref)
			}
			grpcRule.BackendRefs = backendRefs
		}

		rules = append(rules, grpcRule)
	}

	hostname := gatewayv1.Hostname(config.Hostname)

	grpcRoute := &gatewayv1.GRPCRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "GRPCRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Name,
			Namespace: config.Namespace,
			Labels:    ForRoute(config.GatewayID, config.RouteID),
		},
		Spec: gatewayv1.GRPCRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name:      gatewayv1.ObjectName(config.GatewayName),
						Namespace: &namespace,
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{hostname},
			Rules:     rules,
		},
	}

	return grpcRoute
}

// buildGRPCRouteFilters builds GRPCRouteFilter slice for header modification
func buildGRPCRouteFilters(reqMod, resMod *HTTPHeaderModifier) []gatewayv1.GRPCRouteFilter {
	var filters []gatewayv1.GRPCRouteFilter

	if reqMod != nil {
		headerFilter := buildHTTPHeaderFilter(reqMod)
		if headerFilter != nil {
			filters = append(filters, gatewayv1.GRPCRouteFilter{
				Type:                  gatewayv1.GRPCRouteFilterRequestHeaderModifier,
				RequestHeaderModifier: headerFilter,
			})
		}
	}

	if resMod != nil {
		headerFilter := buildHTTPHeaderFilter(resMod)
		if headerFilter != nil {
			filters = append(filters, gatewayv1.GRPCRouteFilter{
				Type:                   gatewayv1.GRPCRouteFilterResponseHeaderModifier,
				ResponseHeaderModifier: headerFilter,
			})
		}
	}

	return filters
}

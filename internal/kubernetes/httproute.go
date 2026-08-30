package kubernetes

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// HTTPRouteConfig represents HTTPRoute configuration
type HTTPRouteConfig struct {
	Name                   string
	Namespace              string
	GatewayName            string
	GatewayID              string // Domain UUID for labeling
	RouteID                string // Route UUID for labeling
	Hostname               string
	Rules                  []HTTPRouteRule
	Mirrors                []MirrorRef // Mirror destinations for request mirroring
	RequestHeaderModifier  *HTTPHeaderModifier
	ResponseHeaderModifier *HTTPHeaderModifier
	URLRewrite             *HTTPURLRewrite
	Redirect               *HTTPRedirectConfig // When set, this is a redirect route (no backends)
	HTTPRouteFilterName    string              // When set, this is a direct response route (uses ExtensionRef filter)
	// Note: CORS is now handled via SecurityPolicy CRD (see SecurityPolicyConfig)
}

// MirrorRef represents a mirror backend reference for HTTPRoute
type MirrorRef struct {
	Name      string
	Namespace string
	Port      int
}

// HTTPRedirectConfig represents HTTP redirect filter configuration
type HTTPRedirectConfig struct {
	Scheme     string           // "http" or "https"
	Hostname   string           // New hostname
	Port       *int             // New port
	StatusCode int              // 301 or 302
	Path       *HTTPPathRewrite // Path rewrite for redirect
}

// HTTPURLRewrite represents URL rewrite configuration
type HTTPURLRewrite struct {
	Hostname *string          // Rewrite Host header
	Path     *HTTPPathRewrite // Rewrite path
}

// HTTPPathRewrite represents path rewrite configuration
type HTTPPathRewrite struct {
	Type               string // ReplacePrefixMatch or ReplaceFullPath
	ReplacePrefixMatch string
	ReplaceFullPath    string
}

// HTTPRouteRule represents a rule in HTTPRoute
type HTTPRouteRule struct {
	PathType    string // Exact, PathPrefix, RegularExpression
	PathValue   string
	Headers     []HeaderMatch
	Method      string
	QueryParams []QueryParamMatch
	BackendRefs []BackendRef
}

// HTTPHeaderModifier represents header modification filters
type HTTPHeaderModifier struct {
	Set    []HTTPHeaderValue // Set header (overwrite if exists)
	Add    []HTTPHeaderValue // Add header (append if exists)
	Remove []string          // Remove header by name
}

// HTTPHeaderValue represents a header name-value pair for modification
type HTTPHeaderValue struct {
	Name  string
	Value string
}

// HeaderMatch represents a header matching rule for HTTPRoute
type HeaderMatch struct {
	Name  string
	Type  string // Exact, RegularExpression
	Value string
}

// QueryParamMatch represents a query param matching rule for HTTPRoute
type QueryParamMatch struct {
	Name  string
	Type  string // Exact, RegularExpression
	Value string
}

// BackendRef represents a backend reference
type BackendRef struct {
	Name      string
	Namespace string
	Port      int
	Weight    int
	// For external backends
	IsExternal bool   // If true, this references a Backend CRD instead of a Service
	Group      string // API group (empty for Service, "gateway.envoyproxy.io" for Backend)
	Kind       string // Kind (empty for Service, "Backend" for external)
}

// BuildHTTPRouteObject builds a typed Gateway API HTTPRoute object
// This is used for both Kubernetes deployment and YAML preview generation
func BuildHTTPRouteObject(config *HTTPRouteConfig) *gatewayv1.HTTPRoute {
	namespace := gatewayv1.Namespace(config.Namespace)

	// Build rules
	rules := make([]gatewayv1.HTTPRouteRule, 0, len(config.Rules))
	for _, rule := range config.Rules {
		httpRule := gatewayv1.HTTPRouteRule{}

		// Build match
		match := gatewayv1.HTTPRouteMatch{}
		hasMatch := false

		// Path match
		if rule.PathValue != "" {
			pathType := gatewayv1.PathMatchType(rule.PathType)
			match.Path = &gatewayv1.HTTPPathMatch{
				Type:  &pathType,
				Value: &rule.PathValue,
			}
			hasMatch = true
		}

		// Method match
		if rule.Method != "" {
			method := gatewayv1.HTTPMethod(rule.Method)
			match.Method = &method
			hasMatch = true
		}

		// Header matches
		if len(rule.Headers) > 0 {
			headerMatches := make([]gatewayv1.HTTPHeaderMatch, 0, len(rule.Headers))
			for _, h := range rule.Headers {
				headerType := gatewayv1.HeaderMatchType(h.Type)
				headerMatches = append(headerMatches, gatewayv1.HTTPHeaderMatch{
					Type:  &headerType,
					Name:  gatewayv1.HTTPHeaderName(h.Name),
					Value: h.Value,
				})
			}
			match.Headers = headerMatches
			hasMatch = true
		}

		// Query param matches
		if len(rule.QueryParams) > 0 {
			queryMatches := make([]gatewayv1.HTTPQueryParamMatch, 0, len(rule.QueryParams))
			for _, qp := range rule.QueryParams {
				queryType := gatewayv1.QueryParamMatchType(qp.Type)
				queryMatches = append(queryMatches, gatewayv1.HTTPQueryParamMatch{
					Type:  &queryType,
					Name:  gatewayv1.HTTPHeaderName(qp.Name),
					Value: qp.Value,
				})
			}
			match.QueryParams = queryMatches
			hasMatch = true
		}

		if hasMatch {
			httpRule.Matches = []gatewayv1.HTTPRouteMatch{match}
		}

		// Build filters (header modifiers, URL rewrite, redirect) - applied to all rules
		// Note: For direct response routes, we only include response header modifier (request modifier is not applicable)
		var filters []gatewayv1.HTTPRouteFilter
		if config.HTTPRouteFilterName != "" {
			// Direct response route - only response header modifier is applicable
			filters = buildHTTPRouteFilters(nil, config.ResponseHeaderModifier, nil)
		} else {
			filters = buildHTTPRouteFilters(config.RequestHeaderModifier, config.ResponseHeaderModifier, config.URLRewrite)
		}

		// If this is a redirect route, add the redirect filter and skip backend refs
		if config.Redirect != nil {
			redirectFilter := buildRedirectFilter(config.Redirect)
			if redirectFilter != nil {
				filters = append(filters, *redirectFilter)
			}
			// No backend refs for redirect routes
		} else if config.HTTPRouteFilterName != "" {
			// Direct response route - add ExtensionRef filter pointing to HTTPRouteFilter
			extensionRefFilter := gatewayv1.HTTPRouteFilter{
				Type: gatewayv1.HTTPRouteFilterExtensionRef,
				ExtensionRef: &gatewayv1.LocalObjectReference{
					Group: gatewayv1.Group("gateway.envoyproxy.io"),
					Kind:  gatewayv1.Kind("HTTPRouteFilter"),
					Name:  gatewayv1.ObjectName(config.HTTPRouteFilterName),
				},
			}
			filters = append(filters, extensionRefFilter)
			// No backend refs for direct response routes
		} else {
			// Build backend refs (only for non-redirect and non-direct-response routes)
			backendRefs := make([]gatewayv1.HTTPBackendRef, 0, len(rule.BackendRefs))
			for _, backend := range rule.BackendRefs {
				port := gatewayv1.PortNumber(backend.Port)
				backendRef := gatewayv1.HTTPBackendRef{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: gatewayv1.ObjectName(backend.Name),
							Port: &port,
						},
					},
				}
				// Set group and kind for external backends (Backend CRD)
				if backend.IsExternal {
					group := gatewayv1.Group(backend.Group)
					kind := gatewayv1.Kind(backend.Kind)
					backendRef.BackendRef.BackendObjectReference.Group = &group
					backendRef.BackendRef.BackendObjectReference.Kind = &kind
				}
				if backend.Namespace != "" {
					ns := gatewayv1.Namespace(backend.Namespace)
					backendRef.BackendRef.BackendObjectReference.Namespace = &ns
				}
				// Set weight explicitly only if specified (non-zero).
				// weight=0 is used for fallback backends to ensure they don't receive normal traffic.
				// If weight is 0 and it's not explicitly a fallback, omit it so K8s defaults to 1.
				if backend.Weight > 0 {
					weight := int32(backend.Weight)
					backendRef.BackendRef.Weight = &weight
				}
				backendRefs = append(backendRefs, backendRef)
			}
			httpRule.BackendRefs = backendRefs

			// Add mirror filters (only for backend routes)
			if len(config.Mirrors) > 0 {
				mirrorFilters := buildMirrorFilters(config.Mirrors)
				filters = append(filters, mirrorFilters...)
			}
		}

		if len(filters) > 0 {
			httpRule.Filters = filters
		}

		rules = append(rules, httpRule)
	}

	return &gatewayv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Name,
			Namespace: config.Namespace,
			Labels:    ForRoute(config.GatewayID, config.RouteID),
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name:      gatewayv1.ObjectName(config.GatewayName),
						Namespace: &namespace,
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(config.Hostname)},
			Rules:     rules,
		},
	}
}

// buildHTTPRouteFilters builds HTTPRoute filters for header modification and URL rewrite
// Note: CORS filters are handled separately via addCORSFiltersToUnstructured since they're an Envoy Gateway extension
func buildHTTPRouteFilters(reqMod, resMod *HTTPHeaderModifier, urlRewrite *HTTPURLRewrite) []gatewayv1.HTTPRouteFilter {
	var filters []gatewayv1.HTTPRouteFilter

	// Request header modifier
	if reqMod != nil && (len(reqMod.Set) > 0 || len(reqMod.Add) > 0 || len(reqMod.Remove) > 0) {
		filter := gatewayv1.HTTPRouteFilter{
			Type:                  gatewayv1.HTTPRouteFilterRequestHeaderModifier,
			RequestHeaderModifier: buildHTTPHeaderFilter(reqMod),
		}
		filters = append(filters, filter)
	}

	// Response header modifier
	if resMod != nil && (len(resMod.Set) > 0 || len(resMod.Add) > 0 || len(resMod.Remove) > 0) {
		filter := gatewayv1.HTTPRouteFilter{
			Type:                   gatewayv1.HTTPRouteFilterResponseHeaderModifier,
			ResponseHeaderModifier: buildHTTPHeaderFilter(resMod),
		}
		filters = append(filters, filter)
	}

	// URL rewrite
	if urlRewrite != nil && (urlRewrite.Hostname != nil || urlRewrite.Path != nil) {
		filter := gatewayv1.HTTPRouteFilter{
			Type:       gatewayv1.HTTPRouteFilterURLRewrite,
			URLRewrite: buildURLRewriteFilter(urlRewrite),
		}
		filters = append(filters, filter)
	}

	return filters
}

// buildMirrorFilters builds RequestMirror filters for HTTPRoute
func buildMirrorFilters(mirrors []MirrorRef) []gatewayv1.HTTPRouteFilter {
	var filters []gatewayv1.HTTPRouteFilter

	for _, mirror := range mirrors {
		port := gatewayv1.PortNumber(mirror.Port)
		mirrorFilter := gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterRequestMirror,
			RequestMirror: &gatewayv1.HTTPRequestMirrorFilter{
				BackendRef: gatewayv1.BackendObjectReference{
					Name: gatewayv1.ObjectName(mirror.Name),
					Port: &port,
				},
			},
		}

		// Add namespace if specified
		if mirror.Namespace != "" {
			ns := gatewayv1.Namespace(mirror.Namespace)
			mirrorFilter.RequestMirror.BackendRef.Namespace = &ns
		}

		filters = append(filters, mirrorFilter)
	}

	return filters
}

// buildRedirectFilter builds a Gateway API RequestRedirect filter
func buildRedirectFilter(redirect *HTTPRedirectConfig) *gatewayv1.HTTPRouteFilter {
	if redirect == nil {
		return nil
	}

	filter := &gatewayv1.HTTPRouteFilter{
		Type:            gatewayv1.HTTPRouteFilterRequestRedirect,
		RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{},
	}

	// Set scheme
	if redirect.Scheme != "" {
		filter.RequestRedirect.Scheme = &redirect.Scheme
	}

	// Set hostname
	if redirect.Hostname != "" {
		hostname := gatewayv1.PreciseHostname(redirect.Hostname)
		filter.RequestRedirect.Hostname = &hostname
	}

	// Set port
	if redirect.Port != nil {
		port := gatewayv1.PortNumber(*redirect.Port)
		filter.RequestRedirect.Port = &port
	}

	// Set status code
	if redirect.StatusCode > 0 {
		statusCode := redirect.StatusCode
		filter.RequestRedirect.StatusCode = &statusCode
	}

	// Set path rewrite
	if redirect.Path != nil {
		pathMod := &gatewayv1.HTTPPathModifier{}
		switch redirect.Path.Type {
		case "ReplacePrefixMatch":
			pathMod.Type = gatewayv1.PrefixMatchHTTPPathModifier
			pathMod.ReplacePrefixMatch = &redirect.Path.ReplacePrefixMatch
		case "ReplaceFullPath":
			pathMod.Type = gatewayv1.FullPathHTTPPathModifier
			pathMod.ReplaceFullPath = &redirect.Path.ReplaceFullPath
		}
		filter.RequestRedirect.Path = pathMod
	}

	return filter
}

// buildURLRewriteFilter builds an HTTPURLRewriteFilter from our HTTPURLRewrite
func buildURLRewriteFilter(rewrite *HTTPURLRewrite) *gatewayv1.HTTPURLRewriteFilter {
	filter := &gatewayv1.HTTPURLRewriteFilter{}

	// Hostname rewrite
	if rewrite.Hostname != nil && *rewrite.Hostname != "" {
		hostname := gatewayv1.PreciseHostname(*rewrite.Hostname)
		filter.Hostname = &hostname
	}

	// Path rewrite
	if rewrite.Path != nil {
		pathMod := &gatewayv1.HTTPPathModifier{}
		switch rewrite.Path.Type {
		case "ReplacePrefixMatch":
			pathMod.Type = gatewayv1.PrefixMatchHTTPPathModifier
			pathMod.ReplacePrefixMatch = &rewrite.Path.ReplacePrefixMatch
		case "ReplaceFullPath":
			pathMod.Type = gatewayv1.FullPathHTTPPathModifier
			pathMod.ReplaceFullPath = &rewrite.Path.ReplaceFullPath
		}
		filter.Path = pathMod
	}

	return filter
}

// buildHTTPHeaderFilter builds an HTTPHeaderFilter from our HTTPHeaderModifier
func buildHTTPHeaderFilter(mod *HTTPHeaderModifier) *gatewayv1.HTTPHeaderFilter {
	filter := &gatewayv1.HTTPHeaderFilter{}

	// Set headers
	if len(mod.Set) > 0 {
		set := make([]gatewayv1.HTTPHeader, 0, len(mod.Set))
		for _, h := range mod.Set {
			set = append(set, gatewayv1.HTTPHeader{
				Name:  gatewayv1.HTTPHeaderName(h.Name),
				Value: h.Value,
			})
		}
		filter.Set = set
	}

	// Add headers
	if len(mod.Add) > 0 {
		add := make([]gatewayv1.HTTPHeader, 0, len(mod.Add))
		for _, h := range mod.Add {
			add = append(add, gatewayv1.HTTPHeader{
				Name:  gatewayv1.HTTPHeaderName(h.Name),
				Value: h.Value,
			})
		}
		filter.Add = add
	}

	// Remove headers
	if len(mod.Remove) > 0 {
		filter.Remove = mod.Remove
	}

	return filter
}

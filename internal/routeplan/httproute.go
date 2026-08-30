package routeplan

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"sigs.k8s.io/yaml"
)

// BuildHTTPRouteConfig assembles the HTTPRouteConfig for a route.
//
// This replaces the former deploy/preview pair. They had drifted in three
// places, all the same shape: the preview path omitted the
// `DirectResponse == nil` exclusion the deploy path applied when setting
// HTTPRouteFilterName and when guarding the URLRewrite and
// RequestHeaderModifier filters. For a direct-response route that also
// carried a URL rewrite or a header modifier, the previewed HTTPRoute showed
// filters the deployed route did not have, and omitted the extensionRef
// filter it did have. The deploy behaviour is authoritative here.
//
// Pure: no receiver, no repository access, no clock, no environment.
func BuildHTTPRouteConfig(route *models.Route, domain *models.Domain) *kubernetes.HTTPRouteConfig {
	rules := make([]kubernetes.HTTPRouteRule, 0, len(route.Config.Matches))

	for _, match := range route.Config.Matches {
		rule := kubernetes.HTTPRouteRule{
			BackendRefs: make([]kubernetes.BackendRef, 0, len(route.Config.Backends)),
		}

		// Path matching
		if match.Path != nil {
			rule.PathType = convertPathTypeToGatewayAPI(string(match.Path.Type))
			rule.PathValue = match.Path.Value
		}

		// Header matching
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

		// Method matching
		if match.Method != "" {
			rule.Method = match.Method
		}

		// Query param matching
		if len(match.QueryParams) > 0 {
			rule.QueryParams = make([]kubernetes.QueryParamMatch, 0, len(match.QueryParams))
			for _, qp := range match.QueryParams {
				rule.QueryParams = append(rule.QueryParams, kubernetes.QueryParamMatch{
					Name:  qp.Name,
					Type:  qp.Type,
					Value: qp.Value,
				})
			}
		}

		// Backend refs (only for non-redirect and non-direct-response routes)
		if route.Config.Redirect == nil && route.Config.DirectResponse == nil {
			hasFailover := route.Config.HasFailover()
			for i, backend := range route.Config.Backends {
				// Use Backend CRD if external OR failover is enabled
				if backend.Type == models.BackendTypeExternal || hasFailover || backend.TLS != nil {
					// Reference Backend CRD
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
					// Kubernetes service backend - reference Service directly
					rule.BackendRefs = append(rule.BackendRefs, kubernetes.BackendRef{
						Name:      backend.Service,
						Namespace: backend.Namespace,
						Port:      backend.Port,
						Weight:    backend.Weight,
					})
				}
			}
		}

		rules = append(rules, rule)
	}

	config := &kubernetes.HTTPRouteConfig{
		Name:        route.K8sRouteName,
		Namespace:   domain.Namespace,
		GatewayName: domain.K8sGatewayName,
		GatewayID:   domain.ID.String(),
		RouteID:     route.ID.String(),
		Hostname:    domain.Hostname,
		Rules:       rules,
	}

	// Add header modifiers (request modifier only for non-direct-response routes)
	if route.Config.RequestHeaderModifier != nil && route.Config.DirectResponse == nil {
		config.RequestHeaderModifier = convertHeaderModifier(route.Config.RequestHeaderModifier)
	}
	if route.Config.ResponseHeaderModifier != nil {
		config.ResponseHeaderModifier = convertHeaderModifier(route.Config.ResponseHeaderModifier)
	}

	// Add URL rewrite (only for non-redirect and non-direct-response routes)
	if route.Config.URLRewrite != nil && route.Config.Redirect == nil && route.Config.DirectResponse == nil {
		config.URLRewrite = convertURLRewrite(route.Config.URLRewrite)
	}

	// Add redirect
	if route.Config.Redirect != nil {
		config.Redirect = convertRedirect(route.Config.Redirect)
	}

	// Add HTTPRouteFilter name for direct response routes
	if route.Config.DirectResponse != nil {
		config.HTTPRouteFilterName = kubernetes.HTTPRouteFilterName(route.K8sRouteName)
	}

	// Add mirror refs (only for backend routes, not redirect or direct response)
	if route.Config.Redirect == nil && route.Config.DirectResponse == nil && len(route.Config.Mirrors) > 0 {
		config.Mirrors = make([]kubernetes.MirrorRef, 0, len(route.Config.Mirrors))
		for _, mirror := range route.Config.Mirrors {
			config.Mirrors = append(config.Mirrors, kubernetes.MirrorRef{
				Name:      mirror.Service,
				Namespace: mirror.Namespace,
				Port:      mirror.Port,
			})
		}
	}

	// Note: CORS is now handled via SecurityPolicy (separate from HTTPRoute)

	return config
}

// convertRedirect converts models.RedirectConfig to HTTPRedirectConfig
func convertRedirect(redirect *models.RedirectConfig) *kubernetes.HTTPRedirectConfig {
	if redirect == nil {
		return nil
	}

	result := &kubernetes.HTTPRedirectConfig{
		Scheme:     redirect.Scheme,
		Hostname:   redirect.Hostname,
		Port:       redirect.Port,
		StatusCode: redirect.StatusCode,
	}

	if redirect.Path != nil {
		result.Path = &kubernetes.HTTPPathRewrite{
			Type:               redirect.Path.Type,
			ReplacePrefixMatch: redirect.Path.ReplacePrefixMatch,
			ReplaceFullPath:    redirect.Path.ReplaceFullPath,
		}
	}

	return result
}

// convertHeaderModifier converts models.HeaderModifier to HTTPHeaderModifier
func convertHeaderModifier(mod *models.HeaderModifier) *kubernetes.HTTPHeaderModifier {
	if mod == nil {
		return nil
	}

	result := &kubernetes.HTTPHeaderModifier{}

	if len(mod.Set) > 0 {
		result.Set = make([]kubernetes.HTTPHeaderValue, 0, len(mod.Set))
		for _, h := range mod.Set {
			result.Set = append(result.Set, kubernetes.HTTPHeaderValue{Name: h.Name, Value: h.Value})
		}
	}

	if len(mod.Add) > 0 {
		result.Add = make([]kubernetes.HTTPHeaderValue, 0, len(mod.Add))
		for _, h := range mod.Add {
			result.Add = append(result.Add, kubernetes.HTTPHeaderValue{Name: h.Name, Value: h.Value})
		}
	}

	if len(mod.Remove) > 0 {
		result.Remove = mod.Remove
	}

	return result
}

// convertURLRewrite converts models.URLRewrite to HTTPURLRewrite
func convertURLRewrite(rewrite *models.URLRewrite) *kubernetes.HTTPURLRewrite {
	if rewrite == nil {
		return nil
	}

	result := &kubernetes.HTTPURLRewrite{}

	if rewrite.Hostname != nil {
		result.Hostname = rewrite.Hostname
	}

	if rewrite.Path != nil {
		result.Path = &kubernetes.HTTPPathRewrite{
			Type:               rewrite.Path.Type,
			ReplacePrefixMatch: rewrite.Path.ReplacePrefixMatch,
			ReplaceFullPath:    rewrite.Path.ReplaceFullPath,
		}
	}

	return result
}

// GenerateHTTPRouteYAML generates HTTPRoute YAML using typed Gateway API structs
// This ensures the preview YAML matches exactly what will be deployed to Kubernetes
func GenerateHTTPRouteYAML(route *models.Route, domain *models.Domain) string {
	if route.Protocol == models.RouteProtocolGRPC {
		config := BuildGRPCRouteConfigForYAML(route, domain)
		grpcRoute := kubernetes.BuildGRPCRouteObject(config)
		yamlBytes, err := yaml.Marshal(grpcRoute)
		if err != nil {
			return fmt.Sprintf("# Error generating YAML: %v", err)
		}
		return string(yamlBytes)
	}

	// Build HTTPRouteConfig from route and domain
	config := BuildHTTPRouteConfigForYAML(route, domain)

	// Use the same typed struct builder as Kubernetes deployment
	httpRoute := kubernetes.BuildHTTPRouteObject(config)

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(httpRoute)
	if err != nil {
		return fmt.Sprintf("# Error generating YAML: %v", err)
	}

	return string(yamlBytes)
}

// BuildHTTPRouteConfigForYAML builds HTTPRouteConfig for YAML/preview
// generation. This and (*RouteService).buildHTTPRouteConfig are now
// identical one-line delegations to the same BuildHTTPRouteConfig -- this
// one exists so preview callers without a RouteService receiver can still
// reach it directly.
func BuildHTTPRouteConfigForYAML(route *models.Route, domain *models.Domain) *kubernetes.HTTPRouteConfig {
	return BuildHTTPRouteConfig(route, domain)
}

// BuildMTLSXFCCHeaderMatches builds a single XFCC header match for mTLS client routing.
// Multiple hashes/SANs are combined into one regex with alternation (OR logic)
// so that any one of the client's certificate identifiers can match.
func BuildMTLSXFCCHeaderMatches(client ClientAuthCategory) []kubernetes.HeaderMatch {
	if !client.EnableMTLS {
		return nil
	}

	// Collect all patterns — each hash or SAN is an OR alternative
	var patterns []string

	for _, hash := range client.MTLSHashes {
		patterns = append(patterns, fmt.Sprintf("Hash=%s", regexp.QuoteMeta(hash)))
	}

	for _, san := range client.MTLSSANs {
		patterns = append(patterns, fmt.Sprintf("%s=%s", san.Type, regexp.QuoteMeta(san.Value)))
	}

	if len(patterns) == 0 {
		return nil
	}

	// Single header match with alternation: .*(Hash=abc|DNS=example\.com).*
	// The .* anchors are needed because Envoy regex header matching requires
	// the regex to match the entire header value, not just a substring.
	return []kubernetes.HeaderMatch{{
		Name:  "x-forwarded-client-cert",
		Type:  "RegularExpression",
		Value: ".*(" + strings.Join(patterns, "|") + ").*",
	}}
}

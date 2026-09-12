package services

import (
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// buildAPIKeyEnvoyExtensionPolicyConfig builds EnvoyExtensionPolicy config for a per-client route.
//
// The guard below decides *whether* to build at all and stays here; assembly
// itself is delegated to routeplan.BuildEnvoyExtensionPolicyK8sConfig, shared
// with the base route path in route_deploy.go (Phase 2H). This site has no
// WAF policy in scope, so wafPolicy is passed as nil.
func (a *routeAssembler) buildAPIKeyEnvoyExtensionPolicyConfig(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory, policy *models.EnvoyExtensionPolicy) *unstructured.Unstructured {
	if policy == nil || policy.Config.IsEmpty() {
		return nil
	}

	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]

	config := routeplan.BuildEnvoyExtensionPolicyK8sConfig(route, domain, routeName, policy, nil, routeplan.WAFConfig{})

	return kubernetes.BuildEnvoyExtensionPolicy(config)
}

// buildAPIKeyGRPCRouteConfig builds GRPCRoute config for a client with API key/JWT auth
func (a *routeAssembler) buildAPIKeyGRPCRouteConfig(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory) *kubernetes.GRPCRouteConfig {
	baseConfig := a.buildGRPCRouteConfig(route, domain)

	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
	baseConfig.Name = routeName
	baseConfig.RouteID = route.ID.String()

	// Add header match on client ID for routing (for API key / JWT clients)
	if client.EnableAPIKey || client.EnableJWT {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, kubernetes.HeaderMatch{
				Name:  client.ClientIDHeaderName,
				Type:  "Exact",
				Value: client.ClientID.String(),
			})
		}
	}

	// Add XFCC header matches (for mTLS clients)
	xfccMatches := routeplan.BuildMTLSXFCCHeaderMatches(client)
	if len(xfccMatches) > 0 {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, xfccMatches...)
		}
	}

	// Add client identification headers for backend enrichment
	if baseConfig.RequestHeaderModifier == nil {
		baseConfig.RequestHeaderModifier = &kubernetes.HTTPHeaderModifier{}
	}
	baseConfig.RequestHeaderModifier.Add = append(baseConfig.RequestHeaderModifier.Add,
		kubernetes.HTTPHeaderValue{Name: "X-Client-ID", Value: client.ClientID.String()},
		kubernetes.HTTPHeaderValue{Name: "X-Client-Name", Value: client.ClientName},
	)

	return baseConfig
}

// buildAPIKeyHTTPRouteConfig builds HTTPRoute config for a client with API key auth
// Uses client ID header for routing (not API key value) to avoid exposing secrets in HTTPRoute
func (a *routeAssembler) buildAPIKeyHTTPRouteConfig(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory) *kubernetes.HTTPRouteConfig {
	// Get the base config
	baseConfig := a.buildHTTPRouteConfig(route, domain)

	// Modify name to include client ID prefix
	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
	baseConfig.Name = routeName
	baseConfig.RouteID = route.ID.String() // Keep original route ID for labeling

	// Add header match on CLIENT ID (for API key, JWT, and mTLS clients)
	if client.EnableAPIKey || client.EnableJWT || client.EnableMTLS {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, kubernetes.HeaderMatch{
				Name:  client.ClientIDHeaderName,
				Type:  "Exact",
				Value: client.ClientID.String(),
			})
		}
	}

	// Add XFCC header matches (for mTLS clients - additional cert verification)
	xfccMatches := routeplan.BuildMTLSXFCCHeaderMatches(client)
	if len(xfccMatches) > 0 {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, xfccMatches...)
		}
	}

	// Add client identification headers for backend enrichment
	if baseConfig.RequestHeaderModifier == nil {
		baseConfig.RequestHeaderModifier = &kubernetes.HTTPHeaderModifier{}
	}
	baseConfig.RequestHeaderModifier.Add = append(baseConfig.RequestHeaderModifier.Add,
		kubernetes.HTTPHeaderValue{Name: "X-Client-ID", Value: client.ClientID.String()},
		kubernetes.HTTPHeaderValue{Name: "X-Client-Name", Value: client.ClientName},
	)

	return baseConfig
}

// buildAPIKeyHTTPRouteConfigRedacted builds HTTPRoute config for display
// Note: With the new two-header approach, client ID is used for routing (not API key),
// so no redaction is needed - client IDs are not secrets
func (a *routeAssembler) buildAPIKeyHTTPRouteConfigRedacted(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory) *kubernetes.HTTPRouteConfig {
	// Get the base config
	baseConfig := a.buildHTTPRouteConfig(route, domain)

	// Modify name to include client ID prefix
	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
	baseConfig.Name = routeName
	baseConfig.RouteID = route.ID.String()

	// Add header match on CLIENT ID (for API key, JWT, and mTLS clients)
	if client.EnableAPIKey || client.EnableJWT || client.EnableMTLS {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, kubernetes.HeaderMatch{
				Name:  client.ClientIDHeaderName,
				Type:  "Exact",
				Value: client.ClientID.String(),
			})
		}
	}

	// Add XFCC header matches (for mTLS clients - additional cert verification)
	xfccMatches := routeplan.BuildMTLSXFCCHeaderMatches(client)
	if len(xfccMatches) > 0 {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, xfccMatches...)
		}
	}

	// Add client identification headers
	if baseConfig.RequestHeaderModifier == nil {
		baseConfig.RequestHeaderModifier = &kubernetes.HTTPHeaderModifier{}
	}
	baseConfig.RequestHeaderModifier.Add = append(baseConfig.RequestHeaderModifier.Add,
		kubernetes.HTTPHeaderValue{Name: "X-Client-ID", Value: client.ClientID.String()},
		kubernetes.HTTPHeaderValue{Name: "X-Client-Name", Value: client.ClientName},
	)

	return baseConfig
}

// buildAPIKeySecurityPolicyConfig builds SecurityPolicy for a client with API key and/or JWT auth
func (a *routeAssembler) buildAPIKeySecurityPolicyConfig(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory, requireIP bool, secPolicy *models.SecurityPolicy) *kubernetes.SecurityPolicyConfig {
	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]

	// Only CORS is copied from the route-level SecurityPolicy on this path.
	var cors *models.CORSConfig
	if secPolicy != nil {
		cors = secPolicy.Config.CORS
	}

	// The only impure step in this site: deriving the client's API key Secret
	// name needs a.k8sAPIKeys. It stays behind the same guard it has always
	// had, so a client without an API key never touches the interface -- which
	// is what keeps this function callable on a zero-value RouteService.
	var apiKeySecretName string
	if client.EnableAPIKey && client.APIKey != "" {
		apiKeySecretName = a.k8sAPIKeys.GetAPIKeySecretName(client.ClientID)
	}

	return routeplan.AssembleSecurityPolicyConfig(routeplan.SecurityPolicyAssembly{
		Route:            route,
		Domain:           domain,
		TargetName:       routeName,
		CORS:             cors,
		Client:           &client,
		RequireIP:        requireIP,
		APIKeySecretName: apiKeySecretName,
	})
}

// buildAuthorizationFromClientAttachments builds authorization config by collecting
// IP CIDRs from all active/approved client attachments with IP allowlisting enabled
// DEPRECATED: Use buildMergedAuthorizationConfig instead which includes direct IPs
func (a *routeAssembler) buildAuthorizationFromClientAttachments(routeID uuid.UUID) *kubernetes.AuthorizationPolicyConfig {
	// Get active attachments with IP allowlist enabled
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list active attachments for route %s: %v", routeID, err)
		return nil
	}

	// Also get approved (pending deploy) attachments
	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list approved attachments for route %s: %v", routeID, err)
	}

	// Merge active + approved attachments
	allAttachments := append(activeAttachments, approvedAttachments...)

	// Collect CIDRs from clients with IP allowlisting enabled
	var allCIDRs []string
	for _, attachment := range allAttachments {
		if !attachment.EnableIPAllowlist {
			continue
		}

		ips, err := a.clientIPRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			log.Printf("Failed to list IPs for client %s: %v", attachment.ClientID, err)
			continue
		}

		for _, ip := range ips {
			allCIDRs = append(allCIDRs, ip.CIDR)
		}
	}

	if len(allCIDRs) == 0 {
		return nil
	}

	return &kubernetes.AuthorizationPolicyConfig{
		DefaultAction: "Deny",
		Rules: []kubernetes.AuthorizationRulePolicyConfig{
			{
				Action:      "Allow",
				ClientCIDRs: allCIDRs,
			},
		},
	}
}

// buildClientIPAuthorizationConfig builds authorization config from base-route-only clients.
// This collects IPs, headers, and methods from active/approved client attachments
// that do NOT have API key/JWT/mTLS enabled (those go to per-client routes).
//
// SINCE Phase 2G (S4): propagates any error from its three collectors instead
// of treating a collector failure as "this client configured nothing" -- see
// each collector's own comment for why a swallowed error there is a fail-open.
func (a *routeAssembler) buildClientIPAuthorizationConfig(routeID uuid.UUID) (*kubernetes.AuthorizationPolicyConfig, error) {
	// Collect client IPs from attachments (normalize to ensure CIDR format)
	clientCIDRs, err := a.collectClientIPCIDRs(routeID)
	if err != nil {
		return nil, err
	}
	clientHeaders, err := a.collectClientHeaders(routeID)
	if err != nil {
		return nil, err
	}
	clientMethods, err := a.collectClientMethods(routeID)
	if err != nil {
		return nil, err
	}

	if len(clientCIDRs) == 0 && len(clientHeaders) == 0 && len(clientMethods) == 0 {
		return nil, nil
	}

	rule := kubernetes.AuthorizationRulePolicyConfig{
		Action: "Allow",
	}

	// Normalize and deduplicate CIDRs
	if len(clientCIDRs) > 0 {
		seen := make(map[string]bool)
		uniqueCIDRs := make([]string, 0, len(clientCIDRs))
		for _, cidr := range clientCIDRs {
			normalized := routeplan.NormalizeCIDR(cidr)
			if !seen[normalized] {
				seen[normalized] = true
				uniqueCIDRs = append(uniqueCIDRs, normalized)
			}
		}
		rule.ClientCIDRs = uniqueCIDRs
	}

	// Add headers
	if len(clientHeaders) > 0 {
		for _, h := range clientHeaders {
			rule.Headers = append(rule.Headers, kubernetes.HeaderMatchPolicyConfig{Name: h.Name, Values: h.Values})
		}
	}

	// Add methods
	if len(clientMethods) > 0 {
		rule.Methods = clientMethods
	}

	return &kubernetes.AuthorizationPolicyConfig{
		DefaultAction: "Deny",
		Rules:         []kubernetes.AuthorizationRulePolicyConfig{rule},
	}, nil
}

// buildHTTPRouteConfig builds kubernetes.HTTPRouteConfig from route and domain
func (a *routeAssembler) buildHTTPRouteConfig(route *models.Route, domain *models.Domain) *kubernetes.HTTPRouteConfig {
	return routeplan.BuildHTTPRouteConfig(route, domain)
}

// buildGRPCRouteConfig builds kubernetes.GRPCRouteConfig from route and domain
func (a *routeAssembler) buildGRPCRouteConfig(route *models.Route, domain *models.Domain) *kubernetes.GRPCRouteConfig {
	return routeplan.BuildGRPCRouteConfig(route, domain)
}

// buildSecurityPolicyConfig builds kubernetes.SecurityPolicyConfig from route, domain and security policy
// Note: This builds from DB only (CORS + stored authorization). For deploy, use deploySecurityPolicy()
// which also computes authorization from active client attachments.
func (a *routeAssembler) buildSecurityPolicyConfig(route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) *kubernetes.SecurityPolicyConfig {
	return routeplan.SecurityPolicyConfigFromDB(route, domain, policy)
}

// buildEnvoyExtensionPolicyConfig builds EnvoyExtensionPolicyK8sConfig from database model.
//
// The guard below decides *whether* to build at all and stays here; assembly
// itself is delegated to routeplan.BuildEnvoyExtensionPolicyK8sConfig, shared
// with the per-client route path in route_clients_apikey.go (Phase 2H).
func (a *routeAssembler) buildEnvoyExtensionPolicyConfig(route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy) *kubernetes.EnvoyExtensionPolicyK8sConfig {
	// Check if we have any extensions to deploy
	hasGenericExtensions := policy != nil && !policy.Config.IsEmpty()
	hasWaf := wafPolicy != nil && !wafPolicy.Config.IsEmpty()

	if !hasGenericExtensions && !hasWaf {
		return nil
	}

	return routeplan.BuildEnvoyExtensionPolicyK8sConfig(route, domain, route.K8sRouteName, policy, wafPolicy, a.wafConfig)
}

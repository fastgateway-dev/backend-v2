package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type routeAssembler struct {
	clientRepo           repository.ClientRepositoryInterface
	clientAttachmentRepo repository.ClientAttachmentRepositoryInterface
	clientIPRepo         repository.ClientIPRepositoryInterface
	clientHeaderRepo     repository.ClientHeaderRepositoryInterface
	k8sAPIKeys           APIKeySecretApplier
	wafConfig            routeplan.WAFConfig
	idgen                func() uuid.UUID
}

// GetEffectiveIPAllowlist returns the merged IP allowlist for a route from active client attachments
func (a *routeAssembler) GetEffectiveIPAllowlist(routeID uuid.UUID) ([]EffectiveIPEntry, error) {
	// Get active attachments with IP allowlist enabled
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active attachments: %w", err)
	}

	// Also include approved (pending deploy) attachments
	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("failed to list approved attachments: %w", err)
	}

	allAttachments := append(activeAttachments, approvedAttachments...)

	var entries []EffectiveIPEntry
	for _, attachment := range allAttachments {
		if !attachment.EnableIPAllowlist {
			continue
		}

		clientName := "Unknown"
		if attachment.Client != nil {
			clientName = attachment.Client.Name
		}

		ips, err := a.clientIPRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			continue
		}

		for _, ip := range ips {
			entries = append(entries, EffectiveIPEntry{
				CIDR:        ip.CIDR,
				ClientID:    attachment.ClientID.String(),
				ClientName:  clientName,
				Description: ip.Description,
			})
		}
	}

	if entries == nil {
		entries = []EffectiveIPEntry{}
	}

	return entries, nil
}

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

// categorizeClientAttachments groups attachments by auth type for deployment
// Returns:
// - ipOnlyClients: IP allowlisting only (goes to base route)
// - apiKeyOnlyClients: API key or JWT without IP (per-client route, no IP check)
// - bothClients: API key or JWT with IP (per-client route with IP check)
func (a *routeAssembler) categorizeClientAttachments(ctx context.Context, routeID uuid.UUID, domain *models.Domain) (ipOnlyClients, apiKeyOnlyClients, bothClients []routeplan.ClientAuthCategory, err error) {
	// Get active attachments
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to list active attachments: %w", err)
	}

	// Get approved (pending deploy) attachments
	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list approved attachments: %v", err)
	}

	// Merge
	allAttachments := append(activeAttachments, approvedAttachments...)

	for _, attachment := range allAttachments {
		// Skip if no auth method is enabled
		if !attachment.EnableIPAllowlist && !attachment.EnableAPIKey && !attachment.EnableJWT && !attachment.EnableMTLS && !attachment.EnableHeaderAuth {
			continue
		}

		// Get client details
		client, err := a.clientRepo.GetByID(attachment.ClientID)
		if err != nil {
			log.Printf("Failed to get client %s: %v", attachment.ClientID, err)
			continue
		}

		cat := routeplan.ClientAuthCategory{
			ClientID:     client.ID,
			ClientName:   client.Name,
			EnableIP:     attachment.EnableIPAllowlist,
			EnableAPIKey: attachment.EnableAPIKey,
			EnableJWT:    attachment.EnableJWT,
			EnableMTLS:   attachment.EnableMTLS,
		}

		// Collect IP CIDRs if IP allowlisting is enabled.
		//
		// SINCE Phase 2G (S5, route_clients_apikey.go:~222): clientIPRepo.ListByClientID
		// ends in gorm's Find, so any non-nil error is a genuine repository
		// failure, never absence -- it is now propagated instead of logged and
		// swallowed. BEFORE Phase 2G: a swallowed error here left cat.IPCIDRs
		// empty, silently turning an "API key AND source-IP allowlist" client
		// into "API key only".
		if attachment.EnableIPAllowlist {
			ips, err := a.clientIPRepo.ListByClientID(client.ID)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("list IP allowlist for client %s: %w", client.ID, err)
			}
			for _, ip := range ips {
				cat.IPCIDRs = append(cat.IPCIDRs, routeplan.NormalizeCIDR(ip.CIDR))
			}
		}

		// Get API key if API key auth is enabled
		// API key is stored encrypted in the database, decode it for deployment
		if attachment.EnableAPIKey && client.APIKeyEnabled {
			if client.APIKeyEncrypted == "" {
				log.Printf("Client %s has API key enabled but no encrypted key data", client.ID)
				// Don't skip - might have JWT enabled as well. This branch is a
				// legitimate "not configured" case (no encrypted key stored at
				// all), not a failure -- left as log-and-continue.
			} else {
				// Decode the API key from base64.
				//
				// SINCE Phase 2G (S5, controller Ruling R13(a)): a decode
				// failure is a corrupt/failed operation on data that IS
				// present, not a legitimately-absent config (unlike the empty
				// branch above) -- it is now propagated. BEFORE Phase 2G: the
				// error was logged and swallowed, leaving cat.APIKey empty
				// while the client could still reach a returned bucket via
				// another independently-valid auth method (e.g. JWT), so a
				// per-client route published matching only on the non-secret
				// X-Client-ID header with no API-key credential enforced at
				// all.
				decoded, err := base64.StdEncoding.DecodeString(client.APIKeyEncrypted)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("decode API key for client %s: %w", client.ID, err)
				}
				cat.APIKey = string(decoded)
			}

			// Set API key header name
			cat.APIKeyHeaderName = client.APIKeyHeaderName
			if cat.APIKeyHeaderName == "" {
				cat.APIKeyHeaderName = "x-api-key"
			}
		}

		// Set client ID header name (for routing) - needed for API key, JWT, and mTLS
		if attachment.EnableAPIKey || attachment.EnableJWT || attachment.EnableMTLS {
			cat.ClientIDHeaderName = client.ClientIDHeaderName
			if cat.ClientIDHeaderName == "" {
				cat.ClientIDHeaderName = "x-client-id"
			}
		}

		// Get JWT config if JWT auth is enabled
		if attachment.EnableJWT && client.JWTEnabled {
			if client.JWTIssuer == "" || client.JWTJWKSURL == "" {
				log.Printf("Client %s has JWT enabled but missing issuer or JWKS URL", client.ID)
				// Don't skip - might have API key enabled as well
			} else {
				cat.JWTIssuer = client.JWTIssuer
				cat.JWTJWKSURL = client.JWTJWKSURL
				cat.JWTAudiences = client.JWTAudiences
				cat.JWTRequiredClaims = client.JWTRequiredClaims
				cat.JWTClaimToHeaders = client.JWTClaimToHeaders
			}
		}

		// Get mTLS config if mTLS auth is enabled
		if attachment.EnableMTLS && client.MTLSEnabled {
			if len(client.MTLSSANs) == 0 && len(client.MTLSHashes) == 0 {
				log.Printf("Client %s has mTLS enabled but no SAN or hash configured", client.ID)
				// Don't skip - might have other auth enabled
			} else {
				cat.MTLSSANs = client.MTLSSANs
				cat.MTLSHashes = client.MTLSHashes
				cat.MTLSCAPem = client.MTLSCAPem
			}
		}

		// Get header auth config if enabled.
		//
		// SINCE Phase 2G (S5, route_clients_apikey.go:~290): clientHeaderRepo.ListByClientID
		// ends in gorm's Find, so any non-nil error is a genuine repository
		// failure, never absence -- it is now propagated. BEFORE Phase 2G: a
		// swallowed error here left cat.HeaderMatches empty, dropping the
		// header Authorization requirement for this client's per-client route.
		if attachment.EnableHeaderAuth {
			cat.EnableHeaderAuth = true
			headers, err := a.clientHeaderRepo.ListByClientID(client.ID)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("list headers for client %s: %w", client.ID, err)
			}
			for _, h := range headers {
				cat.HeaderMatches = append(cat.HeaderMatches, models.AuthorizationHeaderMatch{
					Name:   h.Name,
					Values: []string(h.Values),
				})
			}
		}

		// Get allowed methods from client (client-level, not per-attachment)
		if len(client.AllowedMethods) > 0 {
			cat.AllowedMethods = []string(client.AllowedMethods)
		}

		// Get rate limit config from attachment
		cat.RateLimitConfig = attachment.RateLimitConfig

		// Get ext auth config from attachment
		cat.ExtAuth = attachment.ExtAuth

		// Categorize based on auth type
		// hasPerClientAuth means API key, JWT, or mTLS is enabled
		// For mTLS, SANs/hashes are optional - x-client-id header is used for routing,
		// XFCC SAN/hash matching is an additional verification layer when configured
		hasPerClientAuth := (attachment.EnableAPIKey && client.APIKeyEnabled && cat.APIKey != "") ||
			(attachment.EnableJWT && client.JWTEnabled && cat.JWTIssuer != "") ||
			(attachment.EnableMTLS && client.MTLSEnabled)

		if attachment.EnableIPAllowlist && !hasPerClientAuth {
			// IP only - goes to base route
			ipOnlyClients = append(ipOnlyClients, cat)
		} else if hasPerClientAuth && !attachment.EnableIPAllowlist {
			// API key or JWT only - per-client route without IP check
			apiKeyOnlyClients = append(apiKeyOnlyClients, cat)
		} else if hasPerClientAuth && attachment.EnableIPAllowlist {
			// API key or JWT with IP - per-client route with IP check
			bothClients = append(bothClients, cat)
		}
		// Note: if attachment has auth enabled but client doesn't have valid config, it's skipped
	}

	return ipOnlyClients, apiKeyOnlyClients, bothClients, nil
}

// collectClientHeaders collects header matches from base-route-only clients
// (header auth enabled, but NOT API key/JWT/mTLS enabled)
//
// SINCE Phase 2G (S4): ListActiveByRouteID/ListApprovedByRouteID/
// clientHeaderRepo.ListByClientID all end in gorm's Find, so any non-nil
// error from them is a genuine repository failure, never absence -- it is now
// propagated instead of yielding nil/skipping the client. BEFORE Phase 2G: a
// swallowed error here silently dropped that client's Authorization header
// requirement, indistinguishable from a client with no header auth
// configured.
func (a *routeAssembler) collectClientHeaders(routeID uuid.UUID) ([]models.AuthorizationHeaderMatch, error) {
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list active client attachments for route %s: %w", routeID, err)
	}
	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list approved client attachments for route %s: %w", routeID, err)
	}
	allAttachments := append(activeAttachments, approvedAttachments...)

	var headers []models.AuthorizationHeaderMatch
	for _, attachment := range allAttachments {
		if !attachment.EnableHeaderAuth {
			continue
		}
		// Skip per-client route clients
		if attachment.EnableAPIKey || attachment.EnableJWT || attachment.EnableMTLS {
			continue
		}
		clientHeaders, err := a.clientHeaderRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			return nil, fmt.Errorf("list headers for client %s: %w", attachment.ClientID, err)
		}
		for _, h := range clientHeaders {
			headers = append(headers, models.AuthorizationHeaderMatch{Name: h.Name, Values: []string(h.Values)})
		}
	}
	return headers, nil
}

// collectClientIPCIDRs collects IP CIDRs from all active/approved client attachments
// with IP allowlisting enabled BUT NOT API key/JWT enabled.
// Clients with both IP and API key/JWT go to per-client routes only (AND logic).
// Used by buildClientIPAuthorizationConfig.
//
// SINCE Phase 2G (S4): ListActiveByRouteID/ListApprovedByRouteID/
// clientIPRepo.ListByClientID all end in gorm's Find, so any non-nil error
// from them is a genuine repository failure, never absence -- it is now
// propagated. BEFORE Phase 2G: all three were logged and swallowed, and a
// swallowed error here fed the all-empty check in
// buildClientIPAuthorizationConfig, which could drop the WHOLE Authorization
// block for the base route.
func (a *routeAssembler) collectClientIPCIDRs(routeID uuid.UUID) ([]string, error) {
	// Get active attachments with IP allowlist enabled
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list active client attachments for route %s: %w", routeID, err)
	}

	// Also get approved (pending deploy) attachments
	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list approved client attachments for route %s: %w", routeID, err)
	}

	// Merge active + approved attachments
	allAttachments := append(activeAttachments, approvedAttachments...)

	// Collect CIDRs from clients with IP allowlisting enabled (but NOT API key)
	// Clients with both IP and API key enabled require BOTH checks (AND logic),
	// so their IPs should NOT be in the base route - they go to per-client routes only
	var cidrs []string
	for _, attachment := range allAttachments {
		if !attachment.EnableIPAllowlist {
			continue
		}

		// Skip if API key or JWT is also enabled - those clients go to per-client routes only
		// Adding their IPs here would allow bypassing the API key/JWT requirement
		if attachment.EnableAPIKey || attachment.EnableJWT {
			continue
		}

		ips, err := a.clientIPRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			return nil, fmt.Errorf("list IPs for client %s: %w", attachment.ClientID, err)
		}

		for _, ip := range ips {
			cidrs = append(cidrs, ip.CIDR)
		}
	}

	return cidrs, nil
}

// collectClientMethods collects allowed methods from base-route-only clients
// Methods are now stored on the client entity, not the attachment.
// Only includes clients that are NOT per-client route clients (API key/JWT/mTLS).
//
// SINCE Phase 2G (S4): ListActiveByRouteID/ListApprovedByRouteID end in gorm's
// Find (any error is a genuine failure, propagated unconditionally).
// clientRepo.GetByID ends in First, so absence surfaces as
// gorm.ErrRecordNotFound and is distinguished from a real lookup failure: a
// client that has genuinely been deleted (or has no AllowedMethods
// configured) is legitimately skipped, but any OTHER error is propagated.
// BEFORE Phase 2G: `err != nil || len(...) == 0` conflated the two, so a
// GetByID failure silently dropped that client's verb restriction, leaving
// the rule with NO method restriction at all.
func (a *routeAssembler) collectClientMethods(routeID uuid.UUID) ([]string, error) {
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list active client attachments for route %s: %w", routeID, err)
	}
	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list approved client attachments for route %s: %w", routeID, err)
	}
	allAttachments := append(activeAttachments, approvedAttachments...)

	seen := make(map[string]bool)
	var methods []string
	for _, attachment := range allAttachments {
		// Skip per-client route clients
		if attachment.EnableAPIKey || attachment.EnableJWT || attachment.EnableMTLS {
			continue
		}
		// Get client to read allowed methods
		client, err := a.clientRepo.GetByID(attachment.ClientID)
		switch {
		case err == nil:
			// fall through to the AllowedMethods check below
		case errors.Is(err, gorm.ErrRecordNotFound):
			// Genuine absence: the client no longer exists.
			continue
		default:
			return nil, fmt.Errorf("get client %s: %w", attachment.ClientID, err)
		}
		if len(client.AllowedMethods) == 0 {
			continue
		}
		for _, m := range client.AllowedMethods {
			if !seen[m] {
				seen[m] = true
				methods = append(methods, m)
			}
		}
	}
	return methods, nil
}

// countClientAttachments counts active and approved client attachments for a route.
//
// SINCE Phase 2G (S3): ListActiveByRouteID/ListApprovedByRouteID both end in
// gorm's Find, which returns an empty slice with a nil error when there are
// no rows -- so any non-nil error here is a genuine repository failure, never
// absence, and is now propagated. BEFORE Phase 2G: either error was silently
// treated as "0 attachments", indistinguishable from a route with no clients
// at all. Its caller, deploySecurityPolicy, gates the entire
// DefaultTrafficPolicy block (including "deny") on clientCount > 0, so a
// swallowed error here used to deploy a defaultTrafficPolicy=deny route with
// NO deny rule at all.
func (a *routeAssembler) countClientAttachments(routeID uuid.UUID) (int, error) {
	// Get active attachments
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return 0, fmt.Errorf("list active client attachments for route %s: %w", routeID, err)
	}

	// Get approved attachments
	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return 0, fmt.Errorf("list approved client attachments for route %s: %w", routeID, err)
	}

	return len(activeAttachments) + len(approvedAttachments), nil
}

// hasAPIKeyClientAttachments checks if there are any API key client attachments for a route
func (a *routeAssembler) hasAPIKeyClientAttachments(routeID uuid.UUID) bool {
	// Get active attachments
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return false
	}

	// Get approved attachments
	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
	}

	// Check if any attachment has API key enabled
	for _, att := range append(activeAttachments, approvedAttachments...) {
		if att.EnableAPIKey {
			return true
		}
	}

	return false
}

// hasJWTClientAttachments checks if there are any JWT client attachments for a route
func (a *routeAssembler) hasJWTClientAttachments(routeID uuid.UUID) bool {
	// Get active attachments
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return false
	}

	// Get approved attachments
	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
	}

	// Check if any attachment has JWT enabled
	for _, att := range append(activeAttachments, approvedAttachments...) {
		if att.EnableJWT {
			return true
		}
	}

	return false
}

func (a *routeAssembler) hasMTLSClientAttachments(routeID uuid.UUID) bool {
	activeAttachments, err := a.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return false
	}

	approvedAttachments, err := a.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
	}

	for _, att := range append(activeAttachments, approvedAttachments...) {
		if att.EnableMTLS {
			return true
		}
	}

	return false
}

// newID returns a route ID, using the injected generator when present.
//
// This lives outside route_service.go on purpose: the design spec's exit
// criterion for that file is zero direct uuid.New() calls, so the one
// remaining call (the fallback for a nil idgen) is isolated here.
func (a *routeAssembler) newID() uuid.UUID {
	if a.idgen == nil {
		return uuid.New()
	}
	return a.idgen()
}

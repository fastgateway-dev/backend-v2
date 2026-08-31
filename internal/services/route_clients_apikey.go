package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// deployAPIKeyClients deploys HTTPRoutes and SecurityPolicies for API key authenticated clients
func (s *RouteService) deployAPIKeyClients(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Categorize client attachments
	_, apiKeyOnlyClients, bothClients, err := s.categorizeClientAttachments(ctx, route.ID, domain)
	if err != nil {
		return err
	}
	// Create K8s Secrets for mTLS client CAs and update CTP
	allClients := make([]routeplan.ClientAuthCategory, 0, len(apiKeyOnlyClients)+len(bothClients))
	allClients = append(allClients, apiKeyOnlyClients...)
	allClients = append(allClients, bothClients...)
	if s.domainService != nil {
		hasMTLSClients := false
		for _, c := range allClients {
			if c.EnableMTLS && c.MTLSCAPem != "" {
				// Create K8s Secret for this client's CA
				secretName := fmt.Sprintf("fastgateway-client-%s-mtls-ca", c.ClientID.String()[:8])
				if err := s.k8sService.CreateOrUpdateSecret(ctx, domain.ProjectID, kubernetes.FastGatewayNamespace, secretName, map[string][]byte{
					"ca.crt": []byte(c.MTLSCAPem),
				}); err != nil {
					log.Printf("Warning: failed to create client CA secret %s: %v", secretName, err)
				} else {
					hasMTLSClients = true
				}
			}
		}
		if hasMTLSClients && s.domainService.settingsRepo != nil {
			settings, err := s.domainService.settingsRepo.GetByDomainID(domain.ID)
			if err == nil && settings != nil {
				if err := s.domainService.applyEnvoyGatewayClientTrafficPolicy(ctx, domain, &settings.Config); err != nil {
					log.Printf("Warning: failed to update CTP for mTLS clients: %v", err)
				}
			}
		}
	}

	// Deploy API-key-only clients (no IP check)
	if len(apiKeyOnlyClients) > 0 {
		if err := s.deployAPIKeyRoutes(ctx, route, domain, apiKeyOnlyClients, false); err != nil {
			return err
		}
	}

	// Deploy both clients (API key + IP check - AND logic)
	if len(bothClients) > 0 {
		if err := s.deployAPIKeyRoutes(ctx, route, domain, bothClients, true); err != nil {
			return err
		}
	}

	return nil
}

// cleanupStaleAPIKeyRoutes deletes per-client API key HTTPRoutes, SecurityPolicies, and BackendTrafficPolicies
// that are no longer needed (e.g., client was detached or changed from API key to IP-only).
func (s *RouteService) cleanupStaleAPIKeyRoutes(ctx context.Context, route *models.Route, domain *models.Domain) error {
	if s.clientAttachmentRepo == nil {
		return nil
	}

	// Build set of expected client prefixes from current API key attachments
	expectedClientPrefixes := make(map[string]bool)

	// Get active attachments
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(route.ID)
	if err != nil {
		return fmt.Errorf("failed to list active attachments: %w", err)
	}

	// Get approved attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(route.ID)
	if err != nil {
		log.Printf("Failed to list approved attachments: %v", err)
	}

	// Collect client prefixes that should have per-client routes
	for _, att := range append(activeAttachments, approvedAttachments...) {
		if att.EnableAPIKey || att.EnableJWT || att.EnableMTLS {
			// Use first 8 chars of client ID as prefix (same as in buildAPIKeyHTTPRouteConfig)
			clientPrefix := att.ClientID.String()[:8]
			expectedClientPrefixes[clientPrefix] = true
		}
	}

	// Delete stale per-client resources
	return s.k8sService.DeleteStaleAPIKeyResources(ctx, domain.ProjectID, domain.Namespace, route.ID.String(), route.K8sRouteName, expectedClientPrefixes)
}

// buildAPIKeyGRPCRouteConfig builds GRPCRoute config for a client with API key/JWT auth
func (s *RouteService) buildAPIKeyGRPCRouteConfig(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory) *kubernetes.GRPCRouteConfig {
	baseConfig := s.buildGRPCRouteConfig(route, domain)

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

// buildAPIKeyHTTPRouteConfigRedacted builds HTTPRoute config for display
// Note: With the new two-header approach, client ID is used for routing (not API key),
// so no redaction is needed - client IDs are not secrets
func (s *RouteService) buildAPIKeyHTTPRouteConfigRedacted(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory) *kubernetes.HTTPRouteConfig {
	// Get the base config
	baseConfig := s.buildHTTPRouteConfig(route, domain)

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

// categorizeClientAttachments groups attachments by auth type for deployment
// Returns:
// - ipOnlyClients: IP allowlisting only (goes to base route)
// - apiKeyOnlyClients: API key or JWT without IP (per-client route, no IP check)
// - bothClients: API key or JWT with IP (per-client route with IP check)
func (s *RouteService) categorizeClientAttachments(ctx context.Context, routeID uuid.UUID, domain *models.Domain) (ipOnlyClients, apiKeyOnlyClients, bothClients []routeplan.ClientAuthCategory, err error) {
	if s.clientAttachmentRepo == nil || s.clientRepo == nil {
		return nil, nil, nil, nil
	}

	// Get active attachments
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to list active attachments: %w", err)
	}

	// Get approved (pending deploy) attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
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
		client, err := s.clientRepo.GetByID(attachment.ClientID)
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

		// Collect IP CIDRs if IP allowlisting is enabled
		if attachment.EnableIPAllowlist && s.clientIPRepo != nil {
			ips, err := s.clientIPRepo.ListByClientID(client.ID)
			if err == nil {
				for _, ip := range ips {
					cat.IPCIDRs = append(cat.IPCIDRs, routeplan.NormalizeCIDR(ip.CIDR))
				}
			}
		}

		// Get API key if API key auth is enabled
		// API key is stored encrypted in the database, decode it for deployment
		if attachment.EnableAPIKey && client.APIKeyEnabled {
			if client.APIKeyEncrypted == "" {
				log.Printf("Client %s has API key enabled but no encrypted key data", client.ID)
				// Don't skip - might have JWT enabled as well
			} else {
				// Decode the API key from base64
				decoded, err := base64.StdEncoding.DecodeString(client.APIKeyEncrypted)
				if err != nil {
					log.Printf("Failed to decode API key for client %s: %v", client.ID, err)
				} else {
					cat.APIKey = string(decoded)
				}
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

		// Get header auth config if enabled
		if attachment.EnableHeaderAuth && s.clientHeaderRepo != nil {
			cat.EnableHeaderAuth = true
			headers, err := s.clientHeaderRepo.ListByClientID(client.ID)
			if err == nil {
				for _, h := range headers {
					cat.HeaderMatches = append(cat.HeaderMatches, models.AuthorizationHeaderMatch{
						Name:   h.Name,
						Values: []string(h.Values),
					})
				}
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

// deployAPIKeyRoutes creates per-client routes (HTTPRoute or GRPCRoute) for API key and/or JWT authenticated clients
// (The function name is historical; it now handles both API key and JWT clients)
func (s *RouteService) deployAPIKeyRoutes(ctx context.Context, route *models.Route, domain *models.Domain, clients []routeplan.ClientAuthCategory, requireIP bool) error {
	// Get BackendTrafficPolicy for this route (if any) to apply to per-client routes
	var policy *models.BackendTrafficPolicy
	if s.backendTrafficPolicyRepo != nil {
		policy, _ = s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
	}

	// Get SecurityPolicy for this route (if any) to copy CORS config to per-client routes
	var secPolicy *models.SecurityPolicy
	if s.securityPolicyRepo != nil {
		secPolicy, _ = s.securityPolicyRepo.GetByRouteID(route.ID)
	}

	// Get EnvoyExtensionPolicy for this route (if any) to apply to per-client routes
	var extPolicy *models.EnvoyExtensionPolicy
	if s.envoyExtensionPolicyRepo != nil {
		extPolicy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
	}

	for i := range clients {
		client := &clients[i] // Use pointer to allow modification

		// Check if client has valid auth config (API key, JWT, or mTLS)
		hasValidAPIKey := client.EnableAPIKey && client.APIKey != ""
		hasValidJWT := client.EnableJWT && client.JWTIssuer != ""
		hasMTLS := client.EnableMTLS

		if !hasValidAPIKey && !hasValidJWT && !hasMTLS {
			continue
		}

		// Create/update K8s Secret for this client's API key (only if API key is enabled)
		if hasValidAPIKey {
			if err := s.k8sService.CreateAPIKeySecret(ctx, domain.ProjectID, client.ClientID, client.APIKey); err != nil {
				return fmt.Errorf("failed to create API key secret for client %s: %w", client.ClientName, err)
			}
		}

		// Create ext-auth Backend CRD if client has ext-auth configured
		if client.ExtAuth != nil {
			backendName := kubernetes.GenerateExtAuthBackendName(route.ID.String(), client.ClientID.String())
			var backendRef models.ExtAuthBackendRef
			if client.ExtAuth.Type == "http" && client.ExtAuth.HTTP != nil {
				backendRef = client.ExtAuth.HTTP.BackendRef
			} else if client.ExtAuth.Type == "grpc" && client.ExtAuth.GRPC != nil {
				backendRef = client.ExtAuth.GRPC.BackendRef
			}
			if backendRef.Name != "" {
				backendConfig := &kubernetes.ExtAuthBackendConfig{
					Name:      backendName,
					Namespace: domain.Namespace,
					GatewayID: domain.ID.String(),
					RouteID:   route.ID.String(),
					ClientID:  client.ClientID.String(),
					Service:   backendRef,
				}
				extAuthBackend := kubernetes.BuildExtAuthBackend(backendConfig)
				if err := s.k8sService.UpdateBackendUnstructured(ctx, domain.ProjectID, extAuthBackend); err != nil {
					return fmt.Errorf("failed to create/update ext-auth Backend for client %s: %w", client.ClientName, err)
				}
				client.ExtAuthBackendName = backendName
			}
		}

		// Build route config with header match (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			grpcRouteConfig := s.buildAPIKeyGRPCRouteConfig(route, domain, *client)
			if err := s.k8sService.CreateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
				if err := s.k8sService.UpdateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
					return fmt.Errorf("failed to create/update per-client GRPCRoute for client %s: %w", client.ClientName, err)
				}
			}
		} else {
			httpRouteConfig := s.buildAPIKeyHTTPRouteConfig(route, domain, *client)
			err := s.k8sService.CreateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig)
			if err != nil {
				err = s.k8sService.UpdateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig)
				if err != nil {
					return fmt.Errorf("failed to create/update per-client HTTPRoute for client %s: %w", client.ClientName, err)
				}
			}
		}

		// Build SecurityPolicy config (handles both API key and JWT)
		securityConfig := s.buildAPIKeySecurityPolicyConfig(route, domain, *client, requireIP, secPolicy)
		if err := s.k8sService.UpdateSecurityPolicy(ctx, domain.ProjectID, securityConfig); err != nil {
			return fmt.Errorf("failed to create/update per-client SecurityPolicy for client %s: %w", client.ClientName, err)
		}

		// Build and deploy BackendTrafficPolicy if configured (base policy or attachment rate limit)
		btpConfig := routeplan.BuildAPIKeyBackendTrafficPolicyConfig(route, domain, *client, policy)
		if btpConfig != nil {
			if err := s.k8sService.UpdateBackendTrafficPolicy(ctx, domain.ProjectID, btpConfig); err != nil {
				return fmt.Errorf("failed to create/update per-client BackendTrafficPolicy for client %s: %w", client.ClientName, err)
			}
		}

		// Build and deploy EnvoyExtensionPolicy if configured
		extConfig := s.buildAPIKeyEnvoyExtensionPolicyConfig(route, domain, *client, extPolicy)
		if extConfig != nil {
			if err := s.k8sService.UpdateEnvoyExtensionPolicy(ctx, domain.ProjectID, extConfig); err != nil {
				return fmt.Errorf("failed to create/update per-client EnvoyExtensionPolicy for client %s: %w", client.ClientName, err)
			}
		}
	}

	return nil
}

// buildAPIKeyHTTPRouteConfig builds HTTPRoute config for a client with API key auth
// Uses client ID header for routing (not API key value) to avoid exposing secrets in HTTPRoute
func (s *RouteService) buildAPIKeyHTTPRouteConfig(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory) *kubernetes.HTTPRouteConfig {
	// Get the base config
	baseConfig := s.buildHTTPRouteConfig(route, domain)

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

// buildAPIKeySecurityPolicyConfig builds SecurityPolicy for a client with API key and/or JWT auth
func (s *RouteService) buildAPIKeySecurityPolicyConfig(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory, requireIP bool, secPolicy *models.SecurityPolicy) *kubernetes.SecurityPolicyConfig {
	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]

	// Only CORS is copied from the route-level SecurityPolicy on this path.
	var cors *models.CORSConfig
	if secPolicy != nil {
		cors = secPolicy.Config.CORS
	}

	// The only impure step in this site: deriving the client's API key Secret
	// name needs s.k8sService. It stays behind the same guard it has always
	// had, so a client without an API key never touches the interface -- which
	// is what keeps this function callable on a zero-value RouteService.
	var apiKeySecretName string
	if client.EnableAPIKey && client.APIKey != "" {
		apiKeySecretName = s.k8sService.GetAPIKeySecretName(client.ClientID)
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

// buildAPIKeyEnvoyExtensionPolicyConfig builds EnvoyExtensionPolicy config for a per-client route
func (s *RouteService) buildAPIKeyEnvoyExtensionPolicyConfig(route *models.Route, domain *models.Domain, client routeplan.ClientAuthCategory, policy *models.EnvoyExtensionPolicy) *unstructured.Unstructured {
	if policy == nil || policy.Config.IsEmpty() {
		return nil
	}

	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]

	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(routeName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  routeplan.GetRouteKind(route.Protocol),
			Name:  routeName,
		},
	}

	// Copy Lua extension from base policy
	if policy.Config.Lua != nil {
		luaConfig := kubernetes.LuaExtensionPolicyConfig{
			Type:   policy.Config.Lua.Type,
			Inline: policy.Config.Lua.Inline,
		}
		if policy.Config.Lua.ValueRef != nil {
			luaConfig.ValueRef = &kubernetes.ValueRefPolicyConfig{
				Group:     policy.Config.Lua.ValueRef.Group,
				Kind:      policy.Config.Lua.ValueRef.Kind,
				Name:      policy.Config.Lua.ValueRef.Name,
				Namespace: policy.Config.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Copy Wasm extension from base policy
	if policy.Config.Wasm != nil {
		wasmConfig := kubernetes.WasmExtensionPolicyConfig{
			Name:   policy.Config.Wasm.Name,
			RootID: policy.Config.Wasm.RootID,
			Code: kubernetes.WasmCodeSourcePolicyConfig{
				Type: policy.Config.Wasm.Code.Type,
			},
			Config: policy.Config.Wasm.Config,
		}
		if policy.Config.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &kubernetes.WasmHTTPSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.HTTP.URL,
				SHA256: policy.Config.Wasm.Code.HTTP.SHA256,
			}
		}
		if policy.Config.Wasm.Code.Image != nil {
			imageConfig := &kubernetes.WasmImageSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.Image.URL,
				SHA256: policy.Config.Wasm.Code.Image.SHA256,
			}
			if policy.Config.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &kubernetes.ValueRefPolicyConfig{
					Group:     policy.Config.Wasm.Code.Image.PullSecret.Group,
					Kind:      policy.Config.Wasm.Code.Image.PullSecret.Kind,
					Name:      policy.Config.Wasm.Code.Image.PullSecret.Name,
					Namespace: policy.Config.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Copy ExtProc extension from base policy (shared Backend CRD, per-client EnvoyExtensionPolicy)
	if policy.Config.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, routeplan.BuildExtProcPolicyConfig(policy.Config.ExtProc))
	}

	return kubernetes.BuildEnvoyExtensionPolicy(config)
}

// deleteAPIKeyRoutes deletes per-client routes (HTTPRoute or GRPCRoute) for API key clients
func (s *RouteService) deleteAPIKeyRoutes(ctx context.Context, route *models.Route, domain *models.Domain) error {
	if s.clientAttachmentRepo == nil || s.clientRepo == nil {
		// Fallback: use label-based cleanup to delete all per-client resources
		// This handles cases where attachments are already deleted (e.g., client deleted before route)
		return s.deleteAllPerClientResources(ctx, route, domain)
	}

	// Get all attachments (active + approved + pending_detach) to clean up all possible API key routes
	activeAttachments, _ := s.clientAttachmentRepo.ListActiveByRouteID(route.ID)
	approvedAttachments, _ := s.clientAttachmentRepo.ListApprovedByRouteID(route.ID)

	allAttachments := append(activeAttachments, approvedAttachments...)

	// If no attachments found in DB, use label-based cleanup as fallback
	// This handles cases where attachments were already cascade-deleted (e.g., client deleted before route)
	if len(allAttachments) == 0 {
		return s.deleteAllPerClientResources(ctx, route, domain)
	}

	for _, attachment := range allAttachments {
		if !attachment.EnableAPIKey && !attachment.EnableJWT && !attachment.EnableMTLS {
			continue
		}

		routeName := route.K8sRouteName + "-ak-" + attachment.ClientID.String()[:8]

		// Delete BackendTrafficPolicy first
		btpName := kubernetes.BackendTrafficPolicyName(routeName)
		if err := s.k8sService.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName); err != nil {
			log.Printf("Failed to delete API key BackendTrafficPolicy %s: %v", btpName, err)
		}

		// Delete EnvoyExtensionPolicy
		eepName := kubernetes.EnvoyExtensionPolicyName(routeName)
		if err := s.k8sService.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName); err != nil {
			log.Printf("Failed to delete API key EnvoyExtensionPolicy %s: %v", eepName, err)
		}

		// Delete SecurityPolicy
		securityName := kubernetes.SecurityPolicyName(routeName)
		if err := s.k8sService.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, securityName); err != nil {
			log.Printf("Failed to delete API key SecurityPolicy %s: %v", securityName, err)
		}

		// Delete route (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			if err := s.k8sService.DeleteGRPCRoute(ctx, domain.ProjectID, domain.Namespace, routeName); err != nil {
				log.Printf("Failed to delete API key GRPCRoute %s: %v", routeName, err)
			}
		} else {
			if err := s.k8sService.DeleteHTTPRoute(ctx, domain.ProjectID, domain.Namespace, routeName); err != nil {
				log.Printf("Failed to delete API key HTTPRoute %s: %v", routeName, err)
			}
		}
	}

	return nil
}

// deleteAllPerClientResources uses label-based cleanup to delete all per-client k8s resources
// for a route. This is used as a fallback when attachment records are no longer in the database
// (e.g., cascade-deleted when client was deleted before route deletion).
func (s *RouteService) deleteAllPerClientResources(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Pass empty expectedClientPrefixes to delete ALL per-client resources for this route
	emptyExpected := map[string]bool{}
	if err := s.k8sService.DeleteStaleAPIKeyResources(ctx, domain.ProjectID, domain.Namespace, route.ID.String(), route.K8sRouteName, emptyExpected); err != nil {
		log.Printf("Failed to delete per-client resources by label for route %s: %v", route.K8sRouteName, err)
		return err
	}
	return nil
}

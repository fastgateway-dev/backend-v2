package services

import (
	"context"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

// deployAPIKeyClients deploys HTTPRoutes and SecurityPolicies for API key authenticated clients
func (d *routeDeploy) deployAPIKeyClients(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Categorize client attachments
	_, apiKeyOnlyClients, bothClients, err := d.assembler.categorizeClientAttachments(ctx, route.ID, domain)
	if err != nil {
		return err
	}
	// Create K8s Secrets for mTLS client CAs and update CTP
	allClients := make([]routeplan.ClientAuthCategory, 0, len(apiKeyOnlyClients)+len(bothClients))
	allClients = append(allClients, apiKeyOnlyClients...)
	allClients = append(allClients, bothClients...)
	hasMTLSClients := false
	for _, c := range allClients {
		if c.EnableMTLS && c.MTLSCAPem != "" {
			// Create K8s Secret for this client's CA
			secretName := fmt.Sprintf("fastgateway-client-%s-mtls-ca", c.ClientID.String()[:8])
			if err := d.k8sSecrets.CreateOrUpdateSecret(ctx, domain.ProjectID, kubernetes.FastGatewayNamespace, secretName, map[string][]byte{
				"ca.crt": []byte(c.MTLSCAPem),
			}); err != nil {
				log.Printf("Warning: failed to create client CA secret %s: %v", secretName, err)
			} else {
				hasMTLSClients = true
			}
		}
	}
	if hasMTLSClients {
		if err := d.domains.EnsureMTLSClientTrafficPolicy(ctx, domain); err != nil {
			log.Printf("Warning: failed to update CTP for mTLS clients: %v", err)
		}
	}

	// Deploy API-key-only clients (no IP check)
	if len(apiKeyOnlyClients) > 0 {
		if err := d.deployAPIKeyRoutes(ctx, route, domain, apiKeyOnlyClients, false); err != nil {
			return err
		}
	}

	// Deploy both clients (API key + IP check - AND logic)
	if len(bothClients) > 0 {
		if err := d.deployAPIKeyRoutes(ctx, route, domain, bothClients, true); err != nil {
			return err
		}
	}

	return nil
}

// cleanupStaleAPIKeyRoutes deletes per-client API key HTTPRoutes, SecurityPolicies, and BackendTrafficPolicies
// that are no longer needed (e.g., client was detached or changed from API key to IP-only).
func (d *routeDeploy) cleanupStaleAPIKeyRoutes(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Build set of expected client prefixes from current API key attachments
	expectedClientPrefixes := make(map[string]bool)

	// Get active attachments
	activeAttachments, err := d.clientAttachmentRepo.ListActiveByRouteID(route.ID)
	if err != nil {
		return fmt.Errorf("failed to list active attachments: %w", err)
	}

	// Get approved attachments
	approvedAttachments, err := d.clientAttachmentRepo.ListApprovedByRouteID(route.ID)
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
	return d.k8sAPIKeys.DeleteStaleAPIKeyResources(ctx, domain.ProjectID, domain.Namespace, route.ID.String(), route.K8sRouteName, expectedClientPrefixes)
}

// deployAPIKeyRoutes creates per-client routes (HTTPRoute or GRPCRoute) for API key and/or JWT authenticated clients
// (The function name is historical; it now handles both API key and JWT clients)
func (d *routeDeploy) deployAPIKeyRoutes(ctx context.Context, route *models.Route, domain *models.Domain, clients []routeplan.ClientAuthCategory, requireIP bool) error {
	// Get BackendTrafficPolicy for this route (if any) to apply to per-client routes
	var policy *models.BackendTrafficPolicy
	policy, _ = d.backendTrafficPolicyRepo.GetByRouteID(route.ID)

	// Get SecurityPolicy for this route (if any) to copy CORS config to per-client routes
	var secPolicy *models.SecurityPolicy
	secPolicy, _ = d.securityPolicyRepo.GetByRouteID(route.ID)

	// Get EnvoyExtensionPolicy for this route (if any) to apply to per-client routes
	var extPolicy *models.EnvoyExtensionPolicy
	extPolicy, _ = d.envoyExtensionPolicyRepo.GetByRouteID(route.ID)

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
			if err := d.k8sAPIKeys.CreateAPIKeySecret(ctx, domain.ProjectID, client.ClientID, client.APIKey); err != nil {
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
				if err := d.k8sBackends.UpdateBackendUnstructured(ctx, domain.ProjectID, extAuthBackend); err != nil {
					return fmt.Errorf("failed to create/update ext-auth Backend for client %s: %w", client.ClientName, err)
				}
				client.ExtAuthBackendName = backendName
			}
		}

		// Build route config with header match (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			grpcRouteConfig := d.assembler.buildAPIKeyGRPCRouteConfig(route, domain, *client)
			if err := d.k8sRoutes.CreateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
				if err := d.k8sRoutes.UpdateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
					return fmt.Errorf("failed to create/update per-client GRPCRoute for client %s: %w", client.ClientName, err)
				}
			}
		} else {
			httpRouteConfig := d.assembler.buildAPIKeyHTTPRouteConfig(route, domain, *client)
			err := d.k8sRoutes.CreateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig)
			if err != nil {
				err = d.k8sRoutes.UpdateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig)
				if err != nil {
					return fmt.Errorf("failed to create/update per-client HTTPRoute for client %s: %w", client.ClientName, err)
				}
			}
		}

		// Build SecurityPolicy config (handles both API key and JWT)
		securityConfig := d.assembler.buildAPIKeySecurityPolicyConfig(route, domain, *client, requireIP, secPolicy)
		if err := d.k8sPolicies.UpdateSecurityPolicy(ctx, domain.ProjectID, securityConfig); err != nil {
			return fmt.Errorf("failed to create/update per-client SecurityPolicy for client %s: %w", client.ClientName, err)
		}

		// Build and deploy BackendTrafficPolicy if configured (base policy or attachment rate limit)
		btpConfig := routeplan.BuildAPIKeyBackendTrafficPolicyConfig(route, domain, *client, policy)
		if btpConfig != nil {
			if err := d.k8sPolicies.UpdateBackendTrafficPolicy(ctx, domain.ProjectID, btpConfig); err != nil {
				return fmt.Errorf("failed to create/update per-client BackendTrafficPolicy for client %s: %w", client.ClientName, err)
			}
		}

		// Build and deploy EnvoyExtensionPolicy if configured
		extConfig := d.assembler.buildAPIKeyEnvoyExtensionPolicyConfig(route, domain, *client, extPolicy)
		if extConfig != nil {
			if err := d.k8sPolicies.UpdateEnvoyExtensionPolicy(ctx, domain.ProjectID, extConfig); err != nil {
				return fmt.Errorf("failed to create/update per-client EnvoyExtensionPolicy for client %s: %w", client.ClientName, err)
			}
		}
	}

	return nil
}

// deleteAPIKeyRoutes deletes per-client routes (HTTPRoute or GRPCRoute) for API key clients
func (d *routeDeploy) deleteAPIKeyRoutes(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get all attachments (active + approved + pending_detach) to clean up all possible API key routes
	activeAttachments, _ := d.clientAttachmentRepo.ListActiveByRouteID(route.ID)
	approvedAttachments, _ := d.clientAttachmentRepo.ListApprovedByRouteID(route.ID)

	allAttachments := append(activeAttachments, approvedAttachments...)

	// If no attachments found in DB, use label-based cleanup as fallback
	// This handles cases where attachments were already cascade-deleted (e.g., client deleted before route)
	if len(allAttachments) == 0 {
		return d.deleteAllPerClientResources(ctx, route, domain)
	}

	for _, attachment := range allAttachments {
		if !attachment.EnableAPIKey && !attachment.EnableJWT && !attachment.EnableMTLS {
			continue
		}

		routeName := route.K8sRouteName + "-ak-" + attachment.ClientID.String()[:8]

		// Delete BackendTrafficPolicy first
		btpName := kubernetes.BackendTrafficPolicyName(routeName)
		if err := d.k8sPolicies.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName); err != nil {
			log.Printf("Failed to delete API key BackendTrafficPolicy %s: %v", btpName, err)
		}

		// Delete EnvoyExtensionPolicy
		eepName := kubernetes.EnvoyExtensionPolicyName(routeName)
		if err := d.k8sPolicies.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName); err != nil {
			log.Printf("Failed to delete API key EnvoyExtensionPolicy %s: %v", eepName, err)
		}

		// Delete SecurityPolicy
		securityName := kubernetes.SecurityPolicyName(routeName)
		if err := d.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, securityName); err != nil {
			log.Printf("Failed to delete API key SecurityPolicy %s: %v", securityName, err)
		}

		// Delete route (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			if err := d.k8sRoutes.DeleteGRPCRoute(ctx, domain.ProjectID, domain.Namespace, routeName); err != nil {
				log.Printf("Failed to delete API key GRPCRoute %s: %v", routeName, err)
			}
		} else {
			if err := d.k8sRoutes.DeleteHTTPRoute(ctx, domain.ProjectID, domain.Namespace, routeName); err != nil {
				log.Printf("Failed to delete API key HTTPRoute %s: %v", routeName, err)
			}
		}
	}

	return nil
}

// deleteAllPerClientResources uses label-based cleanup to delete all per-client k8s resources
// for a route. This is used as a fallback when attachment records are no longer in the database
// (e.g., cascade-deleted when client was deleted before route deletion).
func (d *routeDeploy) deleteAllPerClientResources(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Pass empty expectedClientPrefixes to delete ALL per-client resources for this route
	emptyExpected := map[string]bool{}
	if err := d.k8sAPIKeys.DeleteStaleAPIKeyResources(ctx, domain.ProjectID, domain.Namespace, route.ID.String(), route.K8sRouteName, emptyExpected); err != nil {
		log.Printf("Failed to delete per-client resources by label for route %s: %v", route.K8sRouteName, err)
		return err
	}
	return nil
}

// updateClientAttachmentStatuses updates client attachment statuses after a successful deploy
// approved → active (for new/updated attachments)
// pending_detach approved attachments → removed (handled separately via approved status first)
func (d *routeDeploy) updateClientAttachmentStatuses(routeID uuid.UUID) {
	// Move approved attachments to active
	if err := d.clientAttachmentRepo.UpdateStatusByRouteID(routeID, models.AttachmentStatusApproved, models.AttachmentStatusActive); err != nil {
		log.Printf("Failed to update client attachment statuses (approved→active) for route %s: %v", routeID, err)
	}

	// Detach cleanup: OnApprovalComplete sets detached attachments directly to "removed",
	// so cleanupStaleAPIKeyRoutes correctly identifies their K8s resources as stale.
}

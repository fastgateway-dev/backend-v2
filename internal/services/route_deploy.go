package services

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

// Deploy deploys an approved route to Kubernetes
// This can only be called by the route owner team
func (s *RouteService) Deploy(id uuid.UUID, deployedBy uuid.UUID) (*models.Route, error) {
	route, err := s.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Check if route is in a deployable state
	if route.Status != models.RouteStatusApproved && route.Status != models.RouteStatusPendingDeploy {
		return nil, errors.New("route is not approved for deployment")
	}

	// Get the latest approved approval request to determine action
	// For pending_deploy (triggered by client IP changes), there may not be a new approval;
	// in that case, treat it as an update deploy
	approval, err := s.approvalRepo.GetLatestApprovedByEntityID(models.ApprovalEntityRoute, id)
	if err != nil && route.Status == models.RouteStatusPendingDeploy {
		// No new route approval but route needs redeployment (e.g., client IP changes)
		// Create a synthetic "update" action
		approval = &models.Approval{
			Action: models.ApprovalActionUpdate,
		}
	} else if err != nil {
		return nil, errors.New("no approved request found for this route")
	}

	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	// Safety net: ensure ReferenceGrants include this domain's namespace
	if domain.Namespace != kubernetes.FastGatewayNamespace {
		s.ensureReferenceGrantsForDomain(ctx, route, domain)
	}

	// Apply changes to Kubernetes based on the approval action
	switch approval.Action {
	case models.ApprovalActionCreate:
		// Create Backend CRDs (for external backends or when failover is enabled)
		if err := s.deployBackends(ctx, route, domain); err != nil {
			log.Printf("Failed to create Backend CRDs in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create Backend CRDs in Kubernetes: %w", err)
		}

		// Create HTTPRouteFilter and ConfigMap for direct response routes (must be created before HTTPRoute)
		if err := s.deployDirectResponse(ctx, route, domain); err != nil {
			log.Printf("Failed to create HTTPRouteFilter/ConfigMap in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create HTTPRouteFilter/ConfigMap in Kubernetes: %w", err)
		}

		// Create route in Kubernetes (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			grpcRouteConfig := s.buildGRPCRouteConfig(route, domain)
			if err := s.k8sRoutes.CreateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
				log.Printf("Failed to create GRPCRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to create GRPCRoute in Kubernetes: %w", err)
			}
		} else {
			httpRouteConfig := s.buildHTTPRouteConfig(route, domain)
			if err := s.k8sRoutes.CreateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig); err != nil {
				log.Printf("Failed to create HTTPRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to create HTTPRoute in Kubernetes: %w", err)
			}
		}

		// Create SecurityPolicy if configured (Envoy Gateway extension - includes CORS + client IP authorization)
		if err := s.deploySecurityPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to create SecurityPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create SecurityPolicy in Kubernetes: %w", err)
		}

		// Deploy per-client routes only in client mode
		if route.SecurityMode == models.SecurityModeClient {
			// Deploy API key HTTPRoutes for clients with API key auth
			if err := s.deployAPIKeyClients(ctx, route, domain); err != nil {
				log.Printf("Failed to deploy API key HTTPRoutes: %v", err)
				return nil, fmt.Errorf("failed to deploy API key HTTPRoutes: %w", err)
			}

			// Clean up stale API key routes (in case route was modified before first deploy)
			if err := s.cleanupStaleAPIKeyRoutes(ctx, route, domain); err != nil {
				log.Printf("Failed to clean up stale API key routes: %v", err)
				// Non-fatal
			}
		}

		// Create BackendTrafficPolicy if configured (Envoy Gateway extension)
		if err := s.deployBackendTrafficPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to create BackendTrafficPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create BackendTrafficPolicy in Kubernetes: %w", err)
		}

		// Create EnvoyExtensionPolicy if configured (Envoy Gateway extension - Lua/Wasm)
		if err := s.deployEnvoyExtensionPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to create EnvoyExtensionPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create EnvoyExtensionPolicy in Kubernetes: %w", err)
		}

		// Update client attachment statuses: approved → active
		s.updateClientAttachmentStatuses(route.ID)

		// route.Status moves to active after the switch, through the state
		// machine — see the transition below.

	case models.ApprovalActionUpdate:
		// Update Backend CRDs (for external backends or when failover is enabled)
		if err := s.deployBackends(ctx, route, domain); err != nil {
			log.Printf("Failed to update Backend CRDs in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update Backend CRDs in Kubernetes: %w", err)
		}
		// Clean up stale Backend CRDs that are no longer in the config
		if err := s.cleanupStaleBackends(ctx, route, domain); err != nil {
			log.Printf("Failed to clean up stale Backend CRDs: %v", err)
			// Non-fatal: stale backends won't affect routing
		}

		// Update HTTPRouteFilter and ConfigMap for direct response routes (must be updated before HTTPRoute)
		if err := s.deployDirectResponse(ctx, route, domain); err != nil {
			log.Printf("Failed to update HTTPRouteFilter/ConfigMap in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update HTTPRouteFilter/ConfigMap in Kubernetes: %w", err)
		}

		// Update route in Kubernetes (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			grpcRouteConfig := s.buildGRPCRouteConfig(route, domain)
			if err := s.k8sRoutes.UpdateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
				log.Printf("Failed to update GRPCRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to update GRPCRoute in Kubernetes: %w", err)
			}
		} else {
			httpRouteConfig := s.buildHTTPRouteConfig(route, domain)
			if err := s.k8sRoutes.UpdateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig); err != nil {
				log.Printf("Failed to update HTTPRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to update HTTPRoute in Kubernetes: %w", err)
			}
		}

		// Update SecurityPolicy if configured (Envoy Gateway extension - includes CORS + client IP authorization)
		if err := s.deploySecurityPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to update SecurityPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update SecurityPolicy in Kubernetes: %w", err)
		}

		// Deploy per-client routes only in client mode
		if route.SecurityMode == models.SecurityModeClient {
			// Deploy API key HTTPRoutes for clients with API key auth
			if err := s.deployAPIKeyClients(ctx, route, domain); err != nil {
				log.Printf("Failed to deploy API key HTTPRoutes: %v", err)
				return nil, fmt.Errorf("failed to deploy API key HTTPRoutes: %w", err)
			}

			// Clean up stale API key routes (detached clients or clients that changed from API key to IP-only)
			if err := s.cleanupStaleAPIKeyRoutes(ctx, route, domain); err != nil {
				log.Printf("Failed to clean up stale API key routes: %v", err)
				// Non-fatal: stale routes won't break new routing but may allow old API keys
			}
		}

		// Update BackendTrafficPolicy if configured (Envoy Gateway extension)
		if err := s.deployBackendTrafficPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to update BackendTrafficPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update BackendTrafficPolicy in Kubernetes: %w", err)
		}

		// Update EnvoyExtensionPolicy if configured (Envoy Gateway extension - Lua/Wasm)
		if err := s.deployEnvoyExtensionPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to update EnvoyExtensionPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update EnvoyExtensionPolicy in Kubernetes: %w", err)
		}

		// Update client attachment statuses: approved → active, pending_detach (approved) → removed
		s.updateClientAttachmentStatuses(route.ID)

		// route.Status moves to active after the switch, through the state
		// machine — see the transition below.

	case models.ApprovalActionDelete:
		// Delete API key HTTPRoutes and their SecurityPolicies
		if err := s.deleteAPIKeyRoutes(ctx, route, domain); err != nil {
			log.Printf("Failed to delete API key HTTPRoutes: %v", err)
			// Continue with other deletions
		}

		// Delete BackendTrafficPolicy from Kubernetes first
		if err := s.deleteBackendTrafficPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to delete BackendTrafficPolicy from Kubernetes: %v", err)
			// Continue with other deletions even if BackendTrafficPolicy deletion fails
		}

		// Delete EnvoyExtensionPolicy from Kubernetes
		if err := s.deleteEnvoyExtensionPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to delete EnvoyExtensionPolicy from Kubernetes: %v", err)
			// Continue with other deletions even if EnvoyExtensionPolicy deletion fails
		}

		// Delete SecurityPolicy from Kubernetes
		if err := s.deleteSecurityPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to delete SecurityPolicy from Kubernetes: %v", err)
			// Continue with HTTPRoute deletion even if SecurityPolicy deletion fails
		}

		// Delete route from Kubernetes (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			if err := s.k8sRoutes.DeleteGRPCRoute(ctx, domain.ProjectID, domain.Namespace, route.K8sRouteName); err != nil {
				log.Printf("Failed to delete GRPCRoute from Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to delete GRPCRoute from Kubernetes: %w", err)
			}
		} else {
			if err := s.k8sRoutes.DeleteHTTPRoute(ctx, domain.ProjectID, domain.Namespace, route.K8sRouteName); err != nil {
				log.Printf("Failed to delete HTTPRoute from Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to delete HTTPRoute from Kubernetes: %w", err)
			}
		}

		// Delete HTTPRouteFilter and ConfigMap for direct response routes (after HTTPRoute deletion)
		if err := s.deleteDirectResponse(ctx, route, domain); err != nil {
			log.Printf("Failed to delete HTTPRouteFilter/ConfigMap from Kubernetes: %v", err)
			// Continue with other deletions even if direct response resource deletion fails
		}

		// Delete Backend CRDs associated with this route
		if err := s.deleteBackends(ctx, route, domain); err != nil {
			log.Printf("Failed to delete Backend CRDs from Kubernetes: %v", err)
			// Continue with database deletion even if Backend CRD deletion fails
		}

		// Delete all approvals for this route (no FK cascade on entity_id)
		if err := s.approvalRepo.DeleteByEntityID(models.ApprovalEntityRoute, route.ID); err != nil {
			log.Printf("Failed to delete approvals for route %s: %v", route.ID, err)
		}

		// Delete client attachment approvals before route deletion cascade-deletes attachments
		attachments, listErr := s.clientAttachmentRepo.ListByRouteID(route.ID)
		if listErr != nil {
			log.Printf("Failed to list attachments for approval cleanup on route %s: %v", route.ID, listErr)
		}
		for _, att := range attachments {
			if err := s.approvalRepo.DeleteByEntityID(models.ApprovalEntityClientAttachment, att.ID); err != nil {
				log.Printf("Failed to delete approvals for attachment %s: %v", att.ID, err)
			}
		}

		// Delete route from database (cascade-deletes attachments, security policies, etc.)
		if err := s.routeRepo.Delete(route.ID); err != nil {
			return nil, err
		}
		return route, nil
	}

	// Only the create and update cases fall through to here; the delete case
	// returns above after removing the row. Both of them mean "the route is
	// now live in Kubernetes", which is exactly the active transition.
	//
	// This replaces the two assignments of active to route.Status that used to
	// sit inside the switch plus the unconditional routeRepo.Update that
	// followed it: routeStateMachine.To persists, so a second write here
	// would be redundant. Deploy's entry guard rejects anything that is not
	// approved or pending_deploy, so To is never on its no-op path and no
	// route field mutation can be dropped (Deploy mutates no other field).
	if err := s.state.To(SiteDeploy, route, models.RouteStatusActive,
		fmt.Sprintf("deploy succeeded (action %s)", approval.Action)); err != nil {
		return nil, err
	}

	// Create version snapshot after successful deploy.
	if err := s.routeVersions.CreateVersion(route, approval, deployedBy); err != nil {
		log.Printf("Failed to create route version: %v", err)
		// Non-fatal: deploy succeeded, version tracking is best-effort
	}

	return route, nil
}

// deploySecurityPolicy deploys SecurityPolicy to Kubernetes if configured
// This merges CORS config from the DB security policy with authorization
// computed from: (1) direct IP allowlist in security policy, and (2) client attachments
func (s *RouteService) deploySecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get SecurityPolicy from database
	var policy *models.SecurityPolicy
	p, err := s.securityPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		policy = p
	}

	// General mode: build SecurityPolicy directly from stored config
	if route.SecurityMode == models.SecurityModeGeneral {
		return s.deployGeneralSecurityPolicy(ctx, route, domain, policy)
	}

	// Client mode: existing logic below
	// Build authorization config from IP-only client attachments
	// (clients with IP allowlisting but NOT API key/JWT - those go to per-client routes)
	authConfig := s.buildClientIPAuthorizationConfig(route.ID)

	// Check if there are any client attachments
	clientCount := s.countClientAttachments(route.ID)

	// When clients are attached, apply DefaultTrafficPolicy to control non-client traffic
	if clientCount > 0 {
		// Check if there are API key/JWT clients (per-client routes handle their own auth)
		hasPerClientAuth := s.hasAPIKeyClientAttachments(route.ID) || s.hasJWTClientAttachments(route.ID) || s.hasMTLSClientAttachments(route.ID)

		switch route.Config.DefaultTrafficPolicy {
		case models.DefaultTrafficPolicyDeny:
			// Deny all non-client traffic, but preserve IP-only client allow rules
			// authConfig from buildClientIPAuthorizationConfig already has DefaultAction: "Deny"
			// with allow rules for IP-only clients. Only create deny-all if no IP-only clients.
			if authConfig == nil {
				authConfig = &kubernetes.AuthorizationPolicyConfig{
					DefaultAction: "Deny",
					Rules:         []kubernetes.AuthorizationRulePolicyConfig{},
				}
			}
			// Ensure default action is Deny (authConfig from IP-only clients already has this)
			authConfig.DefaultAction = "Deny"
		case models.DefaultTrafficPolicyRequireIPAllowlist:
			// Require requests to come from allowed IPs (defaultAllowedCIDRs)
			// Merge with IP-only client CIDRs so registered IP-only clients are also allowed
			var mergedRules []kubernetes.AuthorizationRulePolicyConfig

			// Add defaultAllowedCIDRs
			if len(route.Config.DefaultAllowedCIDRs) > 0 {
				cidrs := make([]string, 0, len(route.Config.DefaultAllowedCIDRs))
				for _, cidr := range route.Config.DefaultAllowedCIDRs {
					cidrs = append(cidrs, routeplan.NormalizeCIDR(cidr))
				}
				mergedRules = append(mergedRules, kubernetes.AuthorizationRulePolicyConfig{
					Action:      "Allow",
					ClientCIDRs: cidrs,
				})
			}

			// Merge IP-only client rules
			if authConfig != nil {
				mergedRules = append(mergedRules, authConfig.Rules...)
			}

			authConfig = &kubernetes.AuthorizationPolicyConfig{
				DefaultAction: "Deny",
				Rules:         mergedRules,
			}
		case models.DefaultTrafficPolicyAllowAll, "":
			// Allow all requests without client header (default behavior)
			// Keep merged auth if it exists (direct IPs + IP-only client IPs)
			// But if there are API key/JWT clients and no merged auth, create deny-all
			// to prevent unauthenticated access through the base HTTPRoute
			if hasPerClientAuth && authConfig == nil {
				authConfig = &kubernetes.AuthorizationPolicyConfig{
					DefaultAction: "Deny",
					Rules:         []kubernetes.AuthorizationRulePolicyConfig{},
				}
			}
		}
	}

	// Build base SecurityPolicy config
	config := routeplan.SecurityPolicyConfigForDeploy(route, domain, policy, authConfig)

	// Check if there's actually anything to deploy
	if config.CORS == nil && config.Authorization == nil {
		// No security features to deploy; delete existing SecurityPolicy if any
		return s.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, config.Name)
	}

	// Create or update SecurityPolicy in Kubernetes
	return s.k8sPolicies.UpdateSecurityPolicy(ctx, domain.ProjectID, config)
}

// deployGeneralSecurityPolicy deploys SecurityPolicy for general mode routes
// In general mode, all security features (CORS, IP, API key, JWT, OIDC, ExtAuth) come from the DB policy
func (s *RouteService) deployGeneralSecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) error {
	config := routeplan.SecurityPolicyConfigFromDB(route, domain, policy)
	if config == nil {
		policyName := kubernetes.SecurityPolicyName(route.K8sRouteName)
		// Also clean up ext-auth backend if it exists (legacy cleanup)
		extAuthBackendName := kubernetes.GenerateExtAuthBackendName(route.ID.String(), "")
		_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extAuthBackendName)
		return s.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, policyName)
	}

	// Note: ExtAuth uses direct K8s Service reference in SecurityPolicy, no Backend CRD needed
	// Clean up any legacy ext-auth Backend CRD that might exist
	if config.ExtAuth != nil {
		extAuthBackendName := kubernetes.GenerateExtAuthBackendName(route.ID.String(), "")
		_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extAuthBackendName)
	}

	return s.k8sPolicies.UpdateSecurityPolicy(ctx, domain.ProjectID, config)
}

// deleteSecurityPolicy deletes SecurityPolicy from Kubernetes
func (s *RouteService) deleteSecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Build the security policy name
	securityPolicyName := kubernetes.SecurityPolicyName(route.K8sRouteName)

	// Always delete from Kubernetes (client-mode routes create k8s SecurityPolicies
	// without a DB security_policies record, so we can't gate on DB lookup)
	if err := s.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, securityPolicyName); err != nil {
		log.Printf("Failed to delete SecurityPolicy %s from Kubernetes: %v", securityPolicyName, err)
	}

	// Delete from database if a record exists
	policy, err := s.securityPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		return s.securityPolicyRepo.Delete(policy.ID)
	}

	return nil
}

// deployBackendTrafficPolicy deploys BackendTrafficPolicy to Kubernetes if configured
func (s *RouteService) deployBackendTrafficPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get BackendTrafficPolicy from database
	policy, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
	if err != nil {
		// No BackendTrafficPolicy configured for this route
		return nil
	}

	// Build BackendTrafficPolicy config for Kubernetes
	btpConfig := routeplan.BuildBackendTrafficPolicyConfig(route, domain, policy)
	if btpConfig == nil {
		return nil
	}

	// Create or update BackendTrafficPolicy in Kubernetes
	return s.k8sPolicies.UpdateBackendTrafficPolicy(ctx, domain.ProjectID, btpConfig)
}

// deleteBackendTrafficPolicy deletes BackendTrafficPolicy from Kubernetes
func (s *RouteService) deleteBackendTrafficPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Check if BackendTrafficPolicy exists for this route
	policy, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
	if err != nil {
		// No BackendTrafficPolicy to delete
		return nil
	}

	// Build the backend traffic policy name
	btpName := kubernetes.BackendTrafficPolicyName(route.K8sRouteName)

	// Delete from Kubernetes
	if err := s.k8sPolicies.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName); err != nil {
		return err
	}

	// Delete from database
	return s.backendTrafficPolicyRepo.Delete(policy.ID)
}

// deployEnvoyExtensionPolicy deploys EnvoyExtensionPolicy to Kubernetes
func (s *RouteService) deployEnvoyExtensionPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get EnvoyExtensionPolicy from database (may be nil)
	var policy *models.EnvoyExtensionPolicy
	policy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)

	// Get WafPolicy from database (may be nil)
	var wafPolicy *models.WafPolicy
	wafPolicy, _ = s.wafPolicyRepo.GetByRouteID(route.ID)

	// Handle ext-proc Backend CRD lifecycle
	extProcBackendName := kubernetes.GenerateExtProcBackendName(route.ID.String())
	if policy != nil && policy.Config.ExtProc != nil {
		// Create/update ext-proc Backend CRD
		backendConfig := &kubernetes.ExtProcBackendConfig{
			Name:      extProcBackendName,
			Namespace: domain.Namespace,
			GatewayID: domain.ID.String(),
			RouteID:   route.ID.String(),
			Service: kubernetes.ExtProcBackendRefPolicyConfig{
				Name:      policy.Config.ExtProc.BackendRef.Name,
				Namespace: policy.Config.ExtProc.BackendRef.Namespace,
				Port:      policy.Config.ExtProc.BackendRef.Port,
			},
		}
		backend := kubernetes.BuildExtProcBackend(backendConfig)
		if backend != nil {
			if err := s.k8sBackends.UpdateBackendUnstructured(ctx, domain.ProjectID, backend); err != nil {
				return fmt.Errorf("failed to create/update ext-proc Backend: %w", err)
			}
		}
	} else {
		// Delete ext-proc Backend CRD if ext-proc was removed
		_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
	}

	// Build EnvoyExtensionPolicy config for Kubernetes (merged)
	extConfig := s.buildEnvoyExtensionPolicyConfig(route, domain, policy, wafPolicy)
	if extConfig == nil {
		// No extensions to deploy - delete any existing policy if present
		eepName := kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName)
		s.k8sPolicies.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName)
		return nil
	}

	// Build the unstructured object
	extPolicy := kubernetes.BuildEnvoyExtensionPolicy(extConfig)
	if extPolicy == nil {
		return nil
	}

	// Create or update EnvoyExtensionPolicy in Kubernetes
	return s.k8sPolicies.UpdateEnvoyExtensionPolicy(ctx, domain.ProjectID, extPolicy)
}

// deleteEnvoyExtensionPolicy deletes EnvoyExtensionPolicy from Kubernetes
func (s *RouteService) deleteEnvoyExtensionPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Check if EnvoyExtensionPolicy exists for this route
	var policy *models.EnvoyExtensionPolicy
	p, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		policy = p
	}

	// Check if WafPolicy exists for this route
	var wafPolicy *models.WafPolicy
	w, err := s.wafPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		wafPolicy = w
	}

	// If neither EnvoyExtensionPolicy nor WafPolicy exists, nothing to delete
	if policy == nil && wafPolicy == nil {
		return nil
	}

	// Delete ext-proc Backend CRD if it exists
	extProcBackendName := kubernetes.GenerateExtProcBackendName(route.ID.String())
	_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)

	// Build the envoy extension policy name
	eepName := kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName)

	// Delete from Kubernetes (the CRD contains both Lua/Wasm and WAF configurations)
	if err := s.k8sPolicies.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName); err != nil {
		return err
	}

	// Delete EnvoyExtensionPolicy from database (WAF is deleted by CASCADE on route deletion)
	if policy != nil {
		return s.envoyExtensionPolicyRepo.Delete(policy.ID)
	}

	return nil
}

// buildEnvoyExtensionPolicyConfig builds EnvoyExtensionPolicyK8sConfig from database model
func (s *RouteService) buildEnvoyExtensionPolicyConfig(route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy) *kubernetes.EnvoyExtensionPolicyK8sConfig {
	// Check if we have any extensions to deploy
	hasGenericExtensions := policy != nil && !policy.Config.IsEmpty()
	hasWaf := wafPolicy != nil && !wafPolicy.Config.IsEmpty()

	if !hasGenericExtensions && !hasWaf {
		return nil
	}

	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName),
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  routeplan.GetRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Add Lua extension configuration (only if policy exists)
	if hasGenericExtensions && policy.Config.Lua != nil {
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

	// Add Wasm extension configuration (only if policy exists)
	if hasGenericExtensions && policy.Config.Wasm != nil {
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

	// Add ExtProc extension configuration (only if policy exists)
	if hasGenericExtensions && policy.Config.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, routeplan.BuildExtProcPolicyConfig(policy.Config.ExtProc))
	}

	// Add WAF (coraza) WASM entry if WAF is configured
	if hasWaf {
		corazaConfig, err := routeplan.BuildCorazaDirectives(&wafPolicy.Config)
		if err == nil && corazaConfig != "" {
			wasmConfig := kubernetes.WasmExtensionPolicyConfig{
				Name:   "coraza-waf",
				RootID: "",
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "Image",
					Image: &kubernetes.WasmImageSourcePolicyConfig{
						URL: s.wafConfig.ImageURL(),
					},
				},
				Config: &corazaConfig,
			}
			config.Wasm = append(config.Wasm, wasmConfig)
		}
	}

	return config
}

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

// buildSecurityPolicyConfig builds kubernetes.SecurityPolicyConfig from route, domain and security policy
// Note: This builds from DB only (CORS + stored authorization). For deploy, use deploySecurityPolicy()
// which also computes authorization from active client attachments.
func (s *RouteService) buildSecurityPolicyConfig(route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) *kubernetes.SecurityPolicyConfig {
	return routeplan.SecurityPolicyConfigFromDB(route, domain, policy)
}

// buildHTTPRouteConfig builds kubernetes.HTTPRouteConfig from route and domain
func (s *RouteService) buildHTTPRouteConfig(route *models.Route, domain *models.Domain) *kubernetes.HTTPRouteConfig {
	return routeplan.BuildHTTPRouteConfig(route, domain)
}

// buildGRPCRouteConfig builds kubernetes.GRPCRouteConfig from route and domain
func (s *RouteService) buildGRPCRouteConfig(route *models.Route, domain *models.Domain) *kubernetes.GRPCRouteConfig {
	return routeplan.BuildGRPCRouteConfig(route, domain)
}

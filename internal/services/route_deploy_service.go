package services

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/routestate"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// referenceGrantEnsurer supplies ensureReferenceGrantsForDomain, called by
// Deploy but currently defined elsewhere.
type referenceGrantEnsurer interface {
	ensureReferenceGrantsForDomain(ctx context.Context, route *models.Route, domain *models.Domain)
}

type routeDeploy struct {
	routeRepo                repository.RouteRepositoryInterface
	approvalRepo             repository.UnifiedApprovalRepositoryInterface
	domainRepo               repository.DomainRepositoryInterface
	securityPolicyRepo       repository.SecurityPolicyRepositoryInterface
	backendTrafficPolicyRepo repository.BackendTrafficPolicyRepositoryInterface
	envoyExtensionPolicyRepo repository.EnvoyExtensionPolicyRepositoryInterface
	wafPolicyRepo            repository.WafPolicyRepositoryInterface
	clientAttachmentRepo     repository.ClientAttachmentRepositoryInterface

	k8sRoutes        RouteApplier
	k8sPolicies      PolicyApplier
	k8sBackends      BackendApplier
	k8sBackendReaper RouteBackendReaper
	k8sSecrets       SecretWriter
	k8sAPIKeys       APIKeySecretApplier
	domains          ClientTrafficPolicyEnsurer
	routeVersions    RouteVersionRecorder

	state *routestate.Machine

	assembler *routeAssembler
	write     referenceGrantEnsurer
}

// Deploy deploys an approved route to Kubernetes
// This can only be called by the route owner team
func (d *routeDeploy) Deploy(id uuid.UUID, deployedBy uuid.UUID) (*models.Route, error) {
	route, err := d.routeRepo.GetByID(id)
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
	approval, err := d.approvalRepo.GetLatestApprovedByEntityID(models.ApprovalEntityRoute, id)
	if err != nil && route.Status == models.RouteStatusPendingDeploy {
		// No new route approval but route needs redeployment (e.g., client IP changes)
		// Create a synthetic "update" action
		approval = &models.Approval{
			Action: models.ApprovalActionUpdate,
		}
	} else if err != nil {
		return nil, errors.New("no approved request found for this route")
	}

	domain, err := d.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	// Safety net: ensure ReferenceGrants include this domain's namespace
	if domain.Namespace != kubernetes.FastGatewayNamespace {
		d.write.ensureReferenceGrantsForDomain(ctx, route, domain)
	}

	// Apply changes to Kubernetes based on the approval action
	switch approval.Action {
	case models.ApprovalActionCreate:
		// Create Backend CRDs (for external backends or when failover is enabled)
		if err := d.deployBackends(ctx, route, domain); err != nil {
			log.Printf("Failed to create Backend CRDs in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create Backend CRDs in Kubernetes: %w", err)
		}

		// Create HTTPRouteFilter and ConfigMap for direct response routes (must be created before HTTPRoute)
		if err := d.deployDirectResponse(ctx, route, domain); err != nil {
			log.Printf("Failed to create HTTPRouteFilter/ConfigMap in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create HTTPRouteFilter/ConfigMap in Kubernetes: %w", err)
		}

		// Create route in Kubernetes (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			grpcRouteConfig := d.assembler.buildGRPCRouteConfig(route, domain)
			if err := d.k8sRoutes.CreateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
				log.Printf("Failed to create GRPCRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to create GRPCRoute in Kubernetes: %w", err)
			}
		} else {
			httpRouteConfig := d.assembler.buildHTTPRouteConfig(route, domain)
			if err := d.k8sRoutes.CreateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig); err != nil {
				log.Printf("Failed to create HTTPRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to create HTTPRoute in Kubernetes: %w", err)
			}
		}

		// Create SecurityPolicy if configured (Envoy Gateway extension - includes CORS + client IP authorization)
		if err := d.deploySecurityPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to create SecurityPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create SecurityPolicy in Kubernetes: %w", err)
		}

		// Deploy per-client routes only in client mode
		if route.SecurityMode == models.SecurityModeClient {
			// Deploy API key HTTPRoutes for clients with API key auth
			if err := d.deployAPIKeyClients(ctx, route, domain); err != nil {
				log.Printf("Failed to deploy API key HTTPRoutes: %v", err)
				return nil, fmt.Errorf("failed to deploy API key HTTPRoutes: %w", err)
			}

			// Clean up stale API key routes (in case route was modified before first deploy)
			if err := d.cleanupStaleAPIKeyRoutes(ctx, route, domain); err != nil {
				log.Printf("Failed to clean up stale API key routes: %v", err)
				// Non-fatal
			}
		}

		// Create BackendTrafficPolicy if configured (Envoy Gateway extension)
		if err := d.deployBackendTrafficPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to create BackendTrafficPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create BackendTrafficPolicy in Kubernetes: %w", err)
		}

		// Create EnvoyExtensionPolicy if configured (Envoy Gateway extension - Lua/Wasm)
		if err := d.deployEnvoyExtensionPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to create EnvoyExtensionPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to create EnvoyExtensionPolicy in Kubernetes: %w", err)
		}

		// Update client attachment statuses: approved → active
		d.updateClientAttachmentStatuses(route.ID)

		// route.Status moves to active after the switch, through the state
		// machine — see the transition below.

	case models.ApprovalActionUpdate:
		// Update Backend CRDs (for external backends or when failover is enabled)
		if err := d.deployBackends(ctx, route, domain); err != nil {
			log.Printf("Failed to update Backend CRDs in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update Backend CRDs in Kubernetes: %w", err)
		}
		// Clean up stale Backend CRDs that are no longer in the config
		if err := d.cleanupStaleBackends(ctx, route, domain); err != nil {
			log.Printf("Failed to clean up stale Backend CRDs: %v", err)
			// Non-fatal: stale backends won't affect routing
		}

		// Update HTTPRouteFilter and ConfigMap for direct response routes (must be updated before HTTPRoute)
		if err := d.deployDirectResponse(ctx, route, domain); err != nil {
			log.Printf("Failed to update HTTPRouteFilter/ConfigMap in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update HTTPRouteFilter/ConfigMap in Kubernetes: %w", err)
		}

		// Update route in Kubernetes (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			grpcRouteConfig := d.assembler.buildGRPCRouteConfig(route, domain)
			if err := d.k8sRoutes.UpdateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
				log.Printf("Failed to update GRPCRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to update GRPCRoute in Kubernetes: %w", err)
			}
		} else {
			httpRouteConfig := d.assembler.buildHTTPRouteConfig(route, domain)
			if err := d.k8sRoutes.UpdateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig); err != nil {
				log.Printf("Failed to update HTTPRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to update HTTPRoute in Kubernetes: %w", err)
			}
		}

		// Update SecurityPolicy if configured (Envoy Gateway extension - includes CORS + client IP authorization)
		if err := d.deploySecurityPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to update SecurityPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update SecurityPolicy in Kubernetes: %w", err)
		}

		// Deploy per-client routes only in client mode
		if route.SecurityMode == models.SecurityModeClient {
			// Deploy API key HTTPRoutes for clients with API key auth
			if err := d.deployAPIKeyClients(ctx, route, domain); err != nil {
				log.Printf("Failed to deploy API key HTTPRoutes: %v", err)
				return nil, fmt.Errorf("failed to deploy API key HTTPRoutes: %w", err)
			}

			// Clean up stale API key routes (detached clients or clients that changed from API key to IP-only)
			if err := d.cleanupStaleAPIKeyRoutes(ctx, route, domain); err != nil {
				log.Printf("Failed to clean up stale API key routes: %v", err)
				// Non-fatal: stale routes won't break new routing but may allow old API keys
			}
		}

		// Update BackendTrafficPolicy if configured (Envoy Gateway extension)
		if err := d.deployBackendTrafficPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to update BackendTrafficPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update BackendTrafficPolicy in Kubernetes: %w", err)
		}

		// Update EnvoyExtensionPolicy if configured (Envoy Gateway extension - Lua/Wasm)
		if err := d.deployEnvoyExtensionPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to update EnvoyExtensionPolicy in Kubernetes: %v", err)
			return nil, fmt.Errorf("failed to update EnvoyExtensionPolicy in Kubernetes: %w", err)
		}

		// Update client attachment statuses: approved → active, pending_detach (approved) → removed
		d.updateClientAttachmentStatuses(route.ID)

		// route.Status moves to active after the switch, through the state
		// machine — see the transition below.

	case models.ApprovalActionDelete:
		// Delete API key HTTPRoutes and their SecurityPolicies
		if err := d.deleteAPIKeyRoutes(ctx, route, domain); err != nil {
			log.Printf("Failed to delete API key HTTPRoutes: %v", err)
			// Continue with other deletions
		}

		// Delete BackendTrafficPolicy from Kubernetes first
		if err := d.deleteBackendTrafficPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to delete BackendTrafficPolicy from Kubernetes: %v", err)
			// Continue with other deletions even if BackendTrafficPolicy deletion fails
		}

		// Delete EnvoyExtensionPolicy from Kubernetes
		if err := d.deleteEnvoyExtensionPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to delete EnvoyExtensionPolicy from Kubernetes: %v", err)
			// Continue with other deletions even if EnvoyExtensionPolicy deletion fails
		}

		// Delete SecurityPolicy from Kubernetes
		if err := d.deleteSecurityPolicy(ctx, route, domain); err != nil {
			log.Printf("Failed to delete SecurityPolicy from Kubernetes: %v", err)
			// Continue with HTTPRoute deletion even if SecurityPolicy deletion fails
		}

		// Delete route from Kubernetes (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			if err := d.k8sRoutes.DeleteGRPCRoute(ctx, domain.ProjectID, domain.Namespace, route.K8sRouteName); err != nil {
				log.Printf("Failed to delete GRPCRoute from Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to delete GRPCRoute from Kubernetes: %w", err)
			}
		} else {
			if err := d.k8sRoutes.DeleteHTTPRoute(ctx, domain.ProjectID, domain.Namespace, route.K8sRouteName); err != nil {
				log.Printf("Failed to delete HTTPRoute from Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to delete HTTPRoute from Kubernetes: %w", err)
			}
		}

		// Delete HTTPRouteFilter and ConfigMap for direct response routes (after HTTPRoute deletion)
		if err := d.deleteDirectResponse(ctx, route, domain); err != nil {
			log.Printf("Failed to delete HTTPRouteFilter/ConfigMap from Kubernetes: %v", err)
			// Continue with other deletions even if direct response resource deletion fails
		}

		// Delete Backend CRDs associated with this route
		if err := d.deleteBackends(ctx, route, domain); err != nil {
			log.Printf("Failed to delete Backend CRDs from Kubernetes: %v", err)
			// Continue with database deletion even if Backend CRD deletion fails
		}

		// Delete all approvals for this route (no FK cascade on entity_id)
		if err := d.approvalRepo.DeleteByEntityID(models.ApprovalEntityRoute, route.ID); err != nil {
			log.Printf("Failed to delete approvals for route %s: %v", route.ID, err)
		}

		// Delete client attachment approvals before route deletion cascade-deletes attachments
		attachments, listErr := d.clientAttachmentRepo.ListByRouteID(route.ID)
		if listErr != nil {
			log.Printf("Failed to list attachments for approval cleanup on route %s: %v", route.ID, listErr)
		}
		for _, att := range attachments {
			if err := d.approvalRepo.DeleteByEntityID(models.ApprovalEntityClientAttachment, att.ID); err != nil {
				log.Printf("Failed to delete approvals for attachment %s: %v", att.ID, err)
			}
		}

		// Delete route from database (cascade-deletes attachments, security policies, etc.)
		if err := d.routeRepo.Delete(route.ID); err != nil {
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
	// followed it: routestate.Machine.To persists, so a second write here
	// would be redundant. Deploy's entry guard rejects anything that is not
	// approved or pending_deploy, so To is never on its no-op path and no
	// route field mutation can be dropped (Deploy mutates no other field).
	if err := d.state.To(models.SiteDeploy, route, models.RouteStatusActive,
		fmt.Sprintf("deploy succeeded (action %s)", approval.Action)); err != nil {
		return nil, err
	}

	// Create version snapshot after successful deploy.
	if err := d.routeVersions.CreateVersion(route, approval, deployedBy); err != nil {
		log.Printf("Failed to create route version: %v", err)
		// Non-fatal: deploy succeeded, version tracking is best-effort
	}

	return route, nil
}

// deployDirectResponse deploys HTTPRouteFilter and ConfigMap for direct response routes
func (d *routeDeploy) deployDirectResponse(ctx context.Context, route *models.Route, domain *models.Domain) error {
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
		if err := d.k8sRoutes.ApplyDirectResponseConfigMap(ctx, domain.ProjectID, cmConfig); err != nil {
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

	if err := d.k8sRoutes.ApplyHTTPRouteFilter(ctx, domain.ProjectID, hrfConfig); err != nil {
		return fmt.Errorf("failed to apply HTTPRouteFilter: %w", err)
	}

	return nil
}

// deleteDirectResponse deletes HTTPRouteFilter and ConfigMap for direct response routes
func (d *routeDeploy) deleteDirectResponse(ctx context.Context, route *models.Route, domain *models.Domain) error {
	if route.Config.DirectResponse == nil {
		// Not a direct response route
		return nil
	}

	hrfName := kubernetes.HTTPRouteFilterName(route.K8sRouteName)
	cmName := route.K8sRouteName + "-dr-cm"

	// Delete HTTPRouteFilter
	if err := d.k8sRoutes.DeleteHTTPRouteFilter(ctx, domain.ProjectID, domain.Namespace, hrfName); err != nil {
		log.Printf("Warning: failed to delete HTTPRouteFilter %s: %v", hrfName, err)
	}

	// Delete ConfigMap
	if err := d.k8sRoutes.DeleteDirectResponseConfigMap(ctx, domain.ProjectID, domain.Namespace, cmName); err != nil {
		log.Printf("Warning: failed to delete ConfigMap %s: %v", cmName, err)
	}

	return nil
}

// deployBackends creates or updates Backend CRDs for external backends,
// or for ALL backends when failover is enabled (priority-based failover requires Backend CRDs)
func (d *routeDeploy) deployBackends(ctx context.Context, route *models.Route, domain *models.Domain) error {
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

			if err := d.k8sBackends.UpdateBackend(ctx, domain.ProjectID, backendConfig); err != nil {
				return fmt.Errorf("failed to create/update Backend CRD for %s: %w", backendName, err)
			}
		}
	}
	return nil
}

// deleteBackends deletes all Backend CRDs associated with a route
func (d *routeDeploy) deleteBackends(ctx context.Context, route *models.Route, domain *models.Domain) error {
	return d.k8sBackendReaper.DeleteBackendsByRoute(ctx, domain.ProjectID, domain.Namespace, route.ID.String())
}

// cleanupStaleBackends deletes Backend CRDs that are no longer in the route config.
// It lists all Backend CRDs for this route by label, compares with the current config,
// and only deletes ones that are no longer needed.
func (d *routeDeploy) cleanupStaleBackends(ctx context.Context, route *models.Route, domain *models.Domain) error {
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
	return d.k8sBackendReaper.DeleteStaleBackendsByRoute(ctx, domain.ProjectID, domain.Namespace, route.ID.String(), expectedNames)
}

// deploySecurityPolicy deploys SecurityPolicy to Kubernetes if configured
// This merges CORS config from the DB security policy with authorization
// computed from: (1) direct IP allowlist in security policy, and (2) client attachments
func (d *routeDeploy) deploySecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get SecurityPolicy from database
	var policy *models.SecurityPolicy
	p, err := d.securityPolicyRepo.GetByRouteID(route.ID)
	switch {
	case err == nil:
		policy = p
	case errors.Is(err, gorm.ErrRecordNotFound):
		// No SecurityPolicy configured for this route. Legitimate: policy
		// stays nil and the caller's cleanup branch removes any stale
		// cluster object.
	default:
		// Any other error is a LOOKUP FAILURE, not an absence. Returning nil
		// policy here would send a general-mode route down
		// deployGeneralSecurityPolicy's config == nil branch, which DELETES
		// the live SecurityPolicy -- stripping OIDC/JWT/API-key/IP
		// authorization from a route that is still serving -- and then
		// report success.
		return fmt.Errorf("load security policy for route %s: %w", route.ID, err)
	}

	// General mode: build SecurityPolicy directly from stored config
	if route.SecurityMode == models.SecurityModeGeneral {
		return d.deployGeneralSecurityPolicy(ctx, route, domain, policy)
	}

	// Client mode: existing logic below
	// Build authorization config from IP-only client attachments
	// (clients with IP allowlisting but NOT API key/JWT - those go to per-client routes)
	authConfig, err := d.assembler.buildClientIPAuthorizationConfig(route.ID)
	if err != nil {
		return fmt.Errorf("build client IP authorization config for route %s: %w", route.ID, err)
	}

	// Check if there are any client attachments
	clientCount, err := d.assembler.countClientAttachments(route.ID)
	if err != nil {
		return fmt.Errorf("count client attachments for route %s: %w", route.ID, err)
	}

	// When clients are attached, apply DefaultTrafficPolicy to control non-client traffic
	if clientCount > 0 {
		// Check if there are API key/JWT clients (per-client routes handle their own auth)
		hasPerClientAuth := d.assembler.hasAPIKeyClientAttachments(route.ID) || d.assembler.hasJWTClientAttachments(route.ID) || d.assembler.hasMTLSClientAttachments(route.ID)

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
		return d.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, config.Name)
	}

	// Create or update SecurityPolicy in Kubernetes
	return d.k8sPolicies.UpdateSecurityPolicy(ctx, domain.ProjectID, config)
}

// deployGeneralSecurityPolicy deploys SecurityPolicy for general mode routes
// In general mode, all security features (CORS, IP, API key, JWT, OIDC, ExtAuth) come from the DB policy
func (d *routeDeploy) deployGeneralSecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) error {
	config := routeplan.SecurityPolicyConfigFromDB(route, domain, policy)
	if config == nil {
		policyName := kubernetes.SecurityPolicyName(route.K8sRouteName)
		// Also clean up ext-auth backend if it exists (legacy cleanup)
		extAuthBackendName := kubernetes.GenerateExtAuthBackendName(route.ID.String(), "")
		_ = d.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extAuthBackendName)
		return d.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, policyName)
	}

	// Note: ExtAuth uses direct K8s Service reference in SecurityPolicy, no Backend CRD needed
	// Clean up any legacy ext-auth Backend CRD that might exist
	if config.ExtAuth != nil {
		extAuthBackendName := kubernetes.GenerateExtAuthBackendName(route.ID.String(), "")
		_ = d.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extAuthBackendName)
	}

	return d.k8sPolicies.UpdateSecurityPolicy(ctx, domain.ProjectID, config)
}

// deleteSecurityPolicy deletes SecurityPolicy from Kubernetes
func (d *routeDeploy) deleteSecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Build the security policy name
	securityPolicyName := kubernetes.SecurityPolicyName(route.K8sRouteName)

	// Always delete from Kubernetes (client-mode routes create k8s SecurityPolicies
	// without a DB security_policies record, so we can't gate on DB lookup)
	if err := d.k8sPolicies.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, securityPolicyName); err != nil {
		log.Printf("Failed to delete SecurityPolicy %s from Kubernetes: %v", securityPolicyName, err)
	}

	// Delete from database if a record exists
	policy, err := d.securityPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		return d.securityPolicyRepo.Delete(policy.ID)
	}

	return nil
}

// deployBackendTrafficPolicy deploys BackendTrafficPolicy to Kubernetes if configured
func (d *routeDeploy) deployBackendTrafficPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get BackendTrafficPolicy from database
	policy, err := d.backendTrafficPolicyRepo.GetByRouteID(route.ID)
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
	return d.k8sPolicies.UpdateBackendTrafficPolicy(ctx, domain.ProjectID, btpConfig)
}

// deleteBackendTrafficPolicy deletes BackendTrafficPolicy from Kubernetes
func (d *routeDeploy) deleteBackendTrafficPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Check if BackendTrafficPolicy exists for this route
	policy, err := d.backendTrafficPolicyRepo.GetByRouteID(route.ID)
	if err != nil {
		// No BackendTrafficPolicy to delete
		return nil
	}

	// Build the backend traffic policy name
	btpName := kubernetes.BackendTrafficPolicyName(route.K8sRouteName)

	// Delete from Kubernetes
	if err := d.k8sPolicies.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName); err != nil {
		return err
	}

	// Delete from database
	return d.backendTrafficPolicyRepo.Delete(policy.ID)
}

// deployEnvoyExtensionPolicy deploys EnvoyExtensionPolicy to Kubernetes
func (d *routeDeploy) deployEnvoyExtensionPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get EnvoyExtensionPolicy from database (may be genuinely absent)
	var policy *models.EnvoyExtensionPolicy
	p, err := d.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
	switch {
	case err == nil:
		policy = p
	case errors.Is(err, gorm.ErrRecordNotFound):
		// No EnvoyExtensionPolicy configured for this route. Legitimate:
		// policy stays nil and the cleanup branch below removes any stale
		// cluster object.
	default:
		// Any other error is a LOOKUP FAILURE, not an absence. Returning a
		// nil policy here would send the route down the extConfig == nil
		// branch, which DELETES the live EnvoyExtensionPolicy -- stripping
		// the route's WAF and ext-proc configuration while it is still
		// serving -- and then report success.
		return fmt.Errorf("load envoy extension policy for route %s: %w", route.ID, err)
	}

	// Get WafPolicy from database (may be genuinely absent)
	var wafPolicy *models.WafPolicy
	wp, err := d.wafPolicyRepo.GetByRouteID(route.ID)
	switch {
	case err == nil:
		wafPolicy = wp
	case errors.Is(err, gorm.ErrRecordNotFound):
		// No WAF policy configured for this route. Same reasoning as above.
	default:
		// Same hazard as the EnvoyExtensionPolicy lookup: a nil wafPolicy on
		// failure deletes the live policy and reports success.
		return fmt.Errorf("load waf policy for route %s: %w", route.ID, err)
	}

	// Handle ext-proc Backend CRD lifecycle
	extProcBackendName := kubernetes.GenerateExtProcBackendName(route.ID.String())
	if policy != nil && policy.Config.ExtProc != nil {
		// Create/update ext-proc Backend CRD
		//
		// Deliberately NOT extracted to a shared builder (Phase 2H, spec §6).
		// The two ExtProcBackendConfig sites differ in owner identity -- this one
		// sets RouteID; the domain path sets DomainID with an empty RouteID -- and
		// object construction is already shared via kubernetes.BuildExtProcBackend.
		// A parameterised builder would encode two owner semantics in one
		// signature for no reduction in size.
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
			if err := d.k8sBackends.UpdateBackendUnstructured(ctx, domain.ProjectID, backend); err != nil {
				return fmt.Errorf("failed to create/update ext-proc Backend: %w", err)
			}
		}
	} else {
		// Delete ext-proc Backend CRD if ext-proc was removed
		_ = d.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
	}

	// Build EnvoyExtensionPolicy config for Kubernetes (merged)
	extConfig := d.assembler.buildEnvoyExtensionPolicyConfig(route, domain, policy, wafPolicy)
	if extConfig == nil {
		// No extensions to deploy - delete any existing policy if present
		eepName := kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName)
		// Return the delete error rather than discarding it, matching how the
		// SecurityPolicy cleanup branch returns its delete.
		return d.k8sPolicies.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName)
	}

	// Build the unstructured object
	extPolicy := kubernetes.BuildEnvoyExtensionPolicy(extConfig)
	if extPolicy == nil {
		return nil
	}

	// Create or update EnvoyExtensionPolicy in Kubernetes
	return d.k8sPolicies.UpdateEnvoyExtensionPolicy(ctx, domain.ProjectID, extPolicy)
}

// deleteEnvoyExtensionPolicy deletes EnvoyExtensionPolicy from Kubernetes
func (d *routeDeploy) deleteEnvoyExtensionPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Check if EnvoyExtensionPolicy exists for this route
	var policy *models.EnvoyExtensionPolicy
	p, err := d.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		policy = p
	}

	// Check if WafPolicy exists for this route
	var wafPolicy *models.WafPolicy
	w, err := d.wafPolicyRepo.GetByRouteID(route.ID)
	if err == nil {
		wafPolicy = w
	}

	// If neither EnvoyExtensionPolicy nor WafPolicy exists, nothing to delete
	if policy == nil && wafPolicy == nil {
		return nil
	}

	// Delete ext-proc Backend CRD if it exists
	extProcBackendName := kubernetes.GenerateExtProcBackendName(route.ID.String())
	_ = d.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)

	// Build the envoy extension policy name
	eepName := kubernetes.EnvoyExtensionPolicyName(route.K8sRouteName)

	// Delete from Kubernetes (the CRD contains both Lua/Wasm and WAF configurations)
	if err := d.k8sPolicies.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName); err != nil {
		return err
	}

	// Delete EnvoyExtensionPolicy from database (WAF is deleted by CASCADE on route deletion)
	if policy != nil {
		return d.envoyExtensionPolicyRepo.Delete(policy.ID)
	}

	return nil
}

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

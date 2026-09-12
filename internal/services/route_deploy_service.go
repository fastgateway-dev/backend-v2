package services

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routestate"
	"github.com/google/uuid"
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

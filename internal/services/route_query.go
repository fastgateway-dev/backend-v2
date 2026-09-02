package services

import (
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// GetByID gets a route by ID
func (s *RouteService) GetByID(id uuid.UUID) (*models.Route, error) {
	route, err := s.routeRepo.GetByIDWithApproval(id)
	if err != nil {
		return nil, err
	}
	s.populateRouteComputedFields(route)
	return route, nil
}

// populateRouteComputedFields populates computed fields (ClientCount, SecurityStatus) for a route
//
// NOT one of Task 4's six ripple call sites: countClientAttachments now
// returns (int, error), but this helper is void-returning and shared by
// GetByID and both route-list populators, so propagating the error here would
// require a signature change to all of those, which is out of this task's
// declared scope. That is an acceptable place to stop: ClientCount is a
// display-only computed field on the route DTO, not a security gate (unlike
// route_deploy.go's use of the same repository call, which does gate
// authorization and DOES propagate -- see deploySecurityPolicy). On error the
// count is logged and left at zero, same observable value as before Phase
// 2G, just no longer silent.
func (s *RouteService) populateRouteComputedFields(route *models.Route) {
	if route == nil {
		return
	}

	// Count client attachments
	count, err := s.countClientAttachments(route.ID)
	if err != nil {
		log.Printf("Failed to count client attachments for route %s: %v", route.ID, err)
	}
	route.ClientCount = count

	// Compute security status
	route.SecurityStatus = s.computeSecurityStatus(route)
}

// computeSecurityStatus computes the security status of a route
func (s *RouteService) computeSecurityStatus(route *models.Route) models.SecurityStatus {
	// General mode: check if any security feature is configured in the security policy
	if route.SecurityMode == models.SecurityModeGeneral {
		policy, err := s.securityPolicyRepo.GetByRouteID(route.ID)
		if err != nil || policy == nil {
			return models.SecurityStatusNone
		}
		// Any auth feature configured = protected
		if policy.Config.Authorization != nil || policy.Config.APIKeyAuth != nil ||
			policy.Config.JWT != nil || policy.Config.OIDC != nil {
			return models.SecurityStatusProtected
		}
		// Only CORS is not really "protected"
		if policy.Config.CORS != nil {
			return models.SecurityStatusNone
		}
		return models.SecurityStatusNone
	}

	// Client mode: existing logic
	if route.ClientCount == 0 {
		return models.SecurityStatusNone
	}

	// Clients are attached, check if default policy is secure
	switch route.Config.DefaultTrafficPolicy {
	case models.DefaultTrafficPolicyDeny:
		return models.SecurityStatusProtected
	case models.DefaultTrafficPolicyRequireIPAllowlist:
		if len(route.Config.DefaultAllowedCIDRs) > 0 {
			return models.SecurityStatusProtected
		}
		// No CIDRs configured but policy requires IP allowlist - still protected (denies all)
		return models.SecurityStatusProtected
	case models.DefaultTrafficPolicyAllowAll, "":
		// Clients attached but default allows all - warning
		return models.SecurityStatusWarning
	default:
		return models.SecurityStatusWarning
	}
}

// GetSecurityPolicy gets the security policy for a route
func (s *RouteService) GetSecurityPolicy(routeID uuid.UUID) (*models.SecurityPolicy, error) {
	policy, err := s.securityPolicyRepo.GetByRouteID(routeID)
	if err != nil {
		// Not found is not an error, just return nil
		return nil, nil
	}
	return policy, nil
}

// GetBackendTrafficPolicy gets the backend traffic policy for a route
func (s *RouteService) GetBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	policy, err := s.backendTrafficPolicyRepo.GetByRouteID(routeID)
	if err != nil {
		// Not found is not an error, just return nil
		return nil, nil
	}
	return policy, nil
}

// GetEnvoyExtensionPolicy gets the envoy extension policy for a route
func (s *RouteService) GetEnvoyExtensionPolicy(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	policy, err := s.envoyExtensionPolicyRepo.GetByRouteID(routeID)
	if err != nil {
		// Not found is not an error, just return nil
		return nil, nil
	}
	return policy, nil
}

// GetWafPolicy gets the WAF policy for a route
func (s *RouteService) GetWafPolicy(routeID uuid.UUID) (*models.WafPolicy, error) {
	return s.wafPolicyRepo.GetByRouteID(routeID)
}

// ListByDomainID lists routes for a domain
func (s *RouteService) ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error) {
	routes, total, err := s.routeRepo.ListByDomainID(domainID, page, limit, teamID, status, search, searchField, labels)
	if err != nil {
		return nil, 0, err
	}

	// Populate computed fields for each route
	for i := range routes {
		s.populateRouteComputedFields(&routes[i])
	}

	return routes, total, nil
}

// ListByProjectID returns routes across all domains in a project, optionally
// filtered by backend service+namespace. Pure pass-through to the repository
// followed by population of computed fields; permission and visibility checks
// are the caller's responsibility (the handler enforces them).
func (s *RouteService) ListByProjectID(projectID uuid.UUID, page, limit int, filters RouteListFilters) ([]models.Route, int64, error) {
	routes, total, err := s.routeRepo.ListByProjectID(projectID, page, limit, filters)
	if err != nil {
		return nil, 0, err
	}
	for i := range routes {
		s.populateRouteComputedFields(&routes[i])
	}
	return routes, total, nil
}

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
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

type routeQuery struct {
	routeRepo                repository.RouteRepositoryInterface
	securityPolicyRepo       repository.SecurityPolicyRepositoryInterface
	backendTrafficPolicyRepo repository.BackendTrafficPolicyRepositoryInterface
	envoyExtensionPolicyRepo repository.EnvoyExtensionPolicyRepositoryInterface
	wafPolicyRepo            repository.WafPolicyRepositoryInterface
	domainRepo               repository.DomainRepositoryInterface
	projectNamespaceRepo     repository.ProjectNamespaceRepositoryInterface
	wafConfig                routeplan.WAFConfig

	assembler *routeAssembler
}

// GetDomainName returns the domain name for a given domain ID (used for audit enrichment)
func (q *routeQuery) GetDomainName(domainID uuid.UUID) (string, error) {
	domain, err := q.domainRepo.GetByID(domainID)
	if err != nil {
		return "", err
	}
	return domain.Name, nil
}

// GetByID gets a route by ID
func (q *routeQuery) GetByID(id uuid.UUID) (*models.Route, error) {
	route, err := q.routeRepo.GetByIDWithApproval(id)
	if err != nil {
		return nil, err
	}
	q.populateRouteComputedFields(route)
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
func (q *routeQuery) populateRouteComputedFields(route *models.Route) {
	if route == nil {
		return
	}

	// Count client attachments
	count, err := q.assembler.countClientAttachments(route.ID)
	if err != nil {
		log.Printf("Failed to count client attachments for route %s: %v", route.ID, err)
	}
	route.ClientCount = count

	// Compute security status
	route.SecurityStatus = q.computeSecurityStatus(route)
}

// computeSecurityStatus computes the security status of a route
func (q *routeQuery) computeSecurityStatus(route *models.Route) models.SecurityStatus {
	// General mode: check if any security feature is configured in the security policy
	if route.SecurityMode == models.SecurityModeGeneral {
		policy, err := q.securityPolicyRepo.GetByRouteID(route.ID)
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
func (q *routeQuery) GetSecurityPolicy(routeID uuid.UUID) (*models.SecurityPolicy, error) {
	policy, err := q.securityPolicyRepo.GetByRouteID(routeID)
	if err != nil {
		// Not found is not an error, just return nil
		return nil, nil
	}
	return policy, nil
}

// GetBackendTrafficPolicy gets the backend traffic policy for a route
func (q *routeQuery) GetBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	policy, err := q.backendTrafficPolicyRepo.GetByRouteID(routeID)
	if err != nil {
		// Not found is not an error, just return nil
		return nil, nil
	}
	return policy, nil
}

// GetEnvoyExtensionPolicy gets the envoy extension policy for a route
func (q *routeQuery) GetEnvoyExtensionPolicy(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	policy, err := q.envoyExtensionPolicyRepo.GetByRouteID(routeID)
	if err != nil {
		// Not found is not an error, just return nil
		return nil, nil
	}
	return policy, nil
}

// GetWafPolicy gets the WAF policy for a route
func (q *routeQuery) GetWafPolicy(routeID uuid.UUID) (*models.WafPolicy, error) {
	return q.wafPolicyRepo.GetByRouteID(routeID)
}

// ListByDomainID lists routes for a domain
func (q *routeQuery) ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error) {
	routes, total, err := q.routeRepo.ListByDomainID(domainID, page, limit, teamID, status, search, searchField, labels)
	if err != nil {
		return nil, 0, err
	}

	// Populate computed fields for each route
	for i := range routes {
		q.populateRouteComputedFields(&routes[i])
	}

	return routes, total, nil
}

// ListByProjectID returns routes across all domains in a project, optionally
// filtered by backend service+namespace. Pure pass-through to the repository
// followed by population of computed fields; permission and visibility checks
// are the caller's responsibility (the handler enforces them).
func (q *routeQuery) ListByProjectID(projectID uuid.UUID, page, limit int, filters RouteListFilters) ([]models.Route, int64, error) {
	routes, total, err := q.routeRepo.ListByProjectID(projectID, page, limit, filters)
	if err != nil {
		return nil, 0, err
	}
	for i := range routes {
		q.populateRouteComputedFields(&routes[i])
	}
	return routes, total, nil
}

// validateBackendNamespaces validates that all backend and mirror namespaces are managed by the project
func (q *routeQuery) validateBackendNamespaces(projectID uuid.UUID, config *models.RouteConfig) error {
	// Validate primary backend namespaces
	for _, backend := range config.Backends {
		// Empty namespace or fastgateway-system namespace is always allowed
		if backend.Namespace == "" || backend.Namespace == kubernetes.FastGatewayNamespace {
			continue
		}

		// Check if namespace is managed by this project
		exists, err := q.projectNamespaceRepo.ExistsByProjectAndNamespace(projectID, backend.Namespace)
		if err != nil {
			return fmt.Errorf("failed to validate namespace '%s': %w", backend.Namespace, err)
		}
		if !exists {
			return fmt.Errorf("namespace '%s' is not managed by this project. Add it in Project Settings > Namespaces before using it as a backend", backend.Namespace)
		}
	}

	// Validate mirror backend namespaces (same rules as primary backends)
	for _, mirror := range config.Mirrors {
		// Empty namespace or fastgateway-system namespace is always allowed
		if mirror.Namespace == "" || mirror.Namespace == kubernetes.FastGatewayNamespace {
			continue
		}

		// Check if namespace is managed by this project
		exists, err := q.projectNamespaceRepo.ExistsByProjectAndNamespace(projectID, mirror.Namespace)
		if err != nil {
			return fmt.Errorf("failed to validate mirror namespace '%s': %w", mirror.Namespace, err)
		}
		if !exists {
			return fmt.Errorf("mirror namespace '%s' is not managed by this project. Add it in Project Settings > Namespaces before using it as a mirror target", mirror.Namespace)
		}
	}

	return nil
}

// validateMirrorTargets ensures mirror backends are different from primary backends
func (q *routeQuery) validateMirrorTargets(config *models.RouteConfig) error {
	if len(config.Mirrors) == 0 {
		return nil
	}

	// Mirror-only routes not allowed (must have primary backends, redirect, or direct response)
	if len(config.Backends) == 0 && config.Redirect == nil && config.DirectResponse == nil {
		return errors.New("routes with mirrors must have at least one primary backend")
	}

	// Build set of primary backend identifiers
	primaryBackends := make(map[string]bool)
	for _, backend := range config.Backends {
		if backend.Type == models.BackendTypeKubernetes {
			key := fmt.Sprintf("%s/%s:%d", backend.Namespace, backend.Service, backend.Port)
			primaryBackends[key] = true
		}
	}

	// Check mirrors don't duplicate primaries
	for _, mirror := range config.Mirrors {
		key := fmt.Sprintf("%s/%s:%d", mirror.Namespace, mirror.Service, mirror.Port)
		if primaryBackends[key] {
			return fmt.Errorf("mirror target '%s/%s:%d' cannot be the same as a primary backend", mirror.Namespace, mirror.Service, mirror.Port)
		}
	}

	return nil
}

// validateFailoverConfig validates failover configuration
func (q *routeQuery) validateFailoverConfig(config *models.RouteConfig) error {
	if !config.HasFailover() {
		return nil
	}

	// Count primary and fallback backends
	primaryCount := 0
	fallbackCount := 0
	for _, b := range config.Backends {
		if b.Fallback {
			fallbackCount++
		} else {
			primaryCount++
		}
	}

	// Must have at least one primary backend
	if primaryCount == 0 {
		return errors.New("failover requires at least one primary backend")
	}

	// Must have at least one fallback backend (implicit from HasFailover check, but be explicit)
	if fallbackCount == 0 {
		return errors.New("failover requires at least one fallback backend")
	}

	// Build set of primary backend identifiers
	primaryBackends := make(map[string]bool)
	for _, b := range config.Backends {
		if !b.Fallback {
			var key string
			if b.Type == models.BackendTypeKubernetes {
				key = fmt.Sprintf("k8s:%s/%s:%d", b.Namespace, b.Service, b.Port)
			} else if b.Type == models.BackendTypeExternal {
				key = fmt.Sprintf("ext:%s:%d", b.Address, b.Port)
			}
			if key != "" {
				primaryBackends[key] = true
			}
		}
	}

	// Check fallback backends are different from primary backends
	for _, b := range config.Backends {
		if b.Fallback {
			var key string
			if b.Type == models.BackendTypeKubernetes {
				key = fmt.Sprintf("k8s:%s/%s:%d", b.Namespace, b.Service, b.Port)
			} else if b.Type == models.BackendTypeExternal {
				key = fmt.Sprintf("ext:%s:%d", b.Address, b.Port)
			}
			if key != "" && primaryBackends[key] {
				if b.Type == models.BackendTypeKubernetes {
					return fmt.Errorf("fallback backend '%s/%s:%d' cannot be the same as a primary backend", b.Namespace, b.Service, b.Port)
				} else {
					return fmt.Errorf("fallback backend '%s:%d' cannot be the same as a primary backend", b.Address, b.Port)
				}
			}
		}
	}

	return nil
}

// validateBackendRequiredFields validates that all backends have required fields
func (q *routeQuery) validateBackendRequiredFields(config *models.RouteConfig) error {
	// Validate primary backends
	for i, backend := range config.Backends {
		if backend.Type == models.BackendTypeKubernetes {
			if backend.Namespace == "" {
				return fmt.Errorf("backend %d: namespace is required for Kubernetes backends", i+1)
			}
			if backend.Service == "" {
				return fmt.Errorf("backend %d: service is required for Kubernetes backends", i+1)
			}
			if backend.Port <= 0 {
				return fmt.Errorf("backend %d: port must be greater than 0 for Kubernetes backends", i+1)
			}
			// Validate TLS configuration (allowed for K8s backends too)
			if backend.TLS != nil {
				if err := backend.TLS.Validate(); err != nil {
					return fmt.Errorf("backend %d: %w", i+1, err)
				}
			}
		} else if backend.Type == models.BackendTypeExternal {
			if backend.Address == "" {
				return fmt.Errorf("backend %d: address is required for external backends", i+1)
			}
			if backend.Port <= 0 {
				return fmt.Errorf("backend %d: port must be greater than 0 for external backends", i+1)
			}
			// Validate TLS configuration
			if backend.TLS != nil {
				if err := backend.TLS.Validate(); err != nil {
					return fmt.Errorf("backend %d: %w", i+1, err)
				}
			}
		}
	}

	// Validate mirror backends
	for i, mirror := range config.Mirrors {
		if mirror.Namespace == "" {
			return fmt.Errorf("mirror %d: namespace is required", i+1)
		}
		if mirror.Service == "" {
			return fmt.Errorf("mirror %d: service is required", i+1)
		}
		if mirror.Port <= 0 {
			return fmt.Errorf("mirror %d: port must be greater than 0", i+1)
		}
	}

	return nil
}

// validateMatcherConflict checks if the given route config's matcher conflicts with
// any existing route in the same domain. excludeRouteID can be set to skip the route
// being updated. Returns an error naming the conflicting route if found.
func (q *routeQuery) validateMatcherConflict(domainID uuid.UUID, config *models.RouteConfig, excludeRouteID *uuid.UUID) error {
	if len(config.Matches) == 0 {
		return nil
	}
	newMatch := config.Matches[0]

	// Fetch all routes in the domain (no filters, high limit)
	existingRoutes, _, err := q.routeRepo.ListByDomainID(domainID, 1, 10000, nil, "", "", "", nil)
	if err != nil {
		return fmt.Errorf("failed to check matcher conflicts: %w", err)
	}

	for _, route := range existingRoutes {
		if excludeRouteID != nil && route.ID == *excludeRouteID {
			continue
		}
		if len(route.Config.Matches) == 0 {
			continue
		}
		if routeMatchersEqual(newMatch, route.Config.Matches[0]) {
			return fmt.Errorf("route matcher conflicts with existing route '%s'", route.Name)
		}
	}

	return nil
}

// CheckMatcherConflicts checks if the given matcher conflicts with any existing route
// in the domain. Returns all conflicting routes. excludeRouteID can be set to skip
// the route being updated.
func (q *routeQuery) CheckMatcherConflicts(domainID uuid.UUID, match models.RouteMatch, excludeRouteID *uuid.UUID) ([]ConflictResult, error) {
	existingRoutes, _, err := q.routeRepo.ListByDomainID(domainID, 1, 10000, nil, "", "", "", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to check matcher conflicts: %w", err)
	}

	var conflicts []ConflictResult
	for _, route := range existingRoutes {
		if excludeRouteID != nil && route.ID == *excludeRouteID {
			continue
		}
		if len(route.Config.Matches) == 0 {
			continue
		}
		if routeMatchersEqual(match, route.Config.Matches[0]) {
			conflicts = append(conflicts, ConflictResult{
				RouteID:   route.ID,
				RouteName: route.Name,
			})
		}
	}

	return conflicts, nil
}

// GenerateYAML generates the Kubernetes YAML for a route
func (q *routeQuery) GenerateYAML(id uuid.UUID) (string, error) {
	route, err := q.routeRepo.GetByID(id)
	if err != nil {
		return "", err
	}

	domain, err := q.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return "", err
	}

	return routeplan.GenerateHTTPRouteYAML(route, domain), nil
}

// GenerateYAMLs generates both HTTPRoute and SecurityPolicy YAML for a route
func (q *routeQuery) GenerateYAMLs(id uuid.UUID) (*RouteYAMLs, error) {
	route, err := q.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	domain, err := q.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, err
	}

	result := &RouteYAMLs{
		HTTPRouteYAML: routeplan.GenerateHTTPRouteYAML(route, domain),
	}

	// Generate SecurityPolicy YAML if exists
	// This includes CORS, OIDC, JWT, APIKeyAuth, Authorization from DB
	// plus client IP authorization from attachments
	policy, _ := q.securityPolicyRepo.GetByRouteID(id)

	// Compute authorization from IP-only client attachments
	clientAuthConfig, err := q.assembler.buildClientIPAuthorizationConfig(id)
	if err != nil {
		return nil, fmt.Errorf("build client IP authorization config for route %s: %w", id, err)
	}

	// Check if there are per-client auth clients that require deny-all on base route
	// This matches the deploy logic in deploySecurityPolicy
	hasPerClientClients := q.assembler.hasAPIKeyClientAttachments(id) || q.assembler.hasJWTClientAttachments(id) || q.assembler.hasMTLSClientAttachments(id)
	if hasPerClientClients && clientAuthConfig == nil {
		// Create a deny-all authorization (empty CIDR list with default deny)
		// This prevents unauthenticated access through the base HTTPRoute
		clientAuthConfig = &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules:         []kubernetes.AuthorizationRulePolicyConfig{},
		}
	}

	if policy != nil || clientAuthConfig != nil {
		// Use routeplan.SecurityPolicyConfigFromDB to get full config (CORS, OIDC, JWT, APIKeyAuth, Authorization)
		var mergedConfig *kubernetes.SecurityPolicyConfig
		if policy != nil {
			mergedConfig = routeplan.SecurityPolicyConfigFromDB(route, domain, policy)
		}

		// If no DB policy but we have client auth, create a minimal config.
		// This is identity-only (name + targetRef); it goes through the same
		// assembler so those fields cannot drift away from the deploy path.
		if mergedConfig == nil && clientAuthConfig != nil {
			mergedConfig = routeplan.AssembleSecurityPolicyConfig(routeplan.SecurityPolicyAssembly{
				Route:  route,
				Domain: domain,
			})
		}

		// Merge client IP authorization if present and no DB authorization exists
		// (client mode uses client IPs, general mode uses DB authorization)
		if mergedConfig != nil && clientAuthConfig != nil && mergedConfig.Authorization == nil {
			mergedConfig.Authorization = clientAuthConfig
		}

		if mergedConfig != nil {
			securityPolicy := kubernetes.BuildSecurityPolicy(mergedConfig)
			if securityPolicy != nil {
				yamlBytes, err := yaml.Marshal(securityPolicy.Object)
				if err == nil {
					result.SecurityPolicyYAML = string(yamlBytes)
				}
			}
		}
	}

	// Generate BackendTrafficPolicy YAML if exists
	btpPolicy, _ := q.backendTrafficPolicyRepo.GetByRouteID(id)
	if btpPolicy != nil {
		result.BackendTrafficPolicyYAML = routeplan.GenerateBackendTrafficPolicyYAMLFromDB(route, domain, btpPolicy)
	}

	// Generate EnvoyExtensionPolicy YAML if exists (with WAF support)
	var extPolicy *models.EnvoyExtensionPolicy
	var wafPolicy *models.WafPolicy
	extPolicy, _ = q.envoyExtensionPolicyRepo.GetByRouteID(id)
	wafPolicy, _ = q.wafPolicyRepo.GetByRouteID(id)
	if extPolicy != nil || wafPolicy != nil {
		result.EnvoyExtensionPolicyYAML = q.generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route, domain, extPolicy, wafPolicy)
	}

	// Generate Backend CRD YAML for external backends
	result.BackendYAML = routeplan.GenerateBackendYAMLs(route, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes
	if route.Config.DirectResponse != nil {
		result.HTTPRouteFilterYAML, result.ConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(route, domain)
	}

	// Generate per-client API key resources (with secrets redacted).
	//
	// SINCE Phase 2G Task 4 fix round 1 (F-1): propagates instead of
	// swallowing categorizeClientAttachments' error. BEFORE: a base64 decode
	// failure (now a deterministic error out of S5's categorizeClientAttachments
	// fix) silently rendered NO per-client API-key resources in the preview,
	// while Deploy of the same route hard-fails on the identical error --
	// preview and deploy disagreeing is this project's #1 known defect class.
	apiKeyClientResources, err := q.generateAPIKeyClientResourceYAMLs(route, domain)
	if err != nil {
		return nil, fmt.Errorf("generate per-client API key resources for route %s: %w", id, err)
	}
	if len(apiKeyClientResources) > 0 {
		result.APIKeyClientResources = apiKeyClientResources
	}

	return result, nil
}

// generateAPIKeyClientResourceYAMLs generates YAML for per-client API key resources
// with secrets redacted for display purposes
//
// SINCE Phase 2G Task 4 fix round 1 (F-1): categorizeClientAttachments' error
// is now propagated instead of swallowed into a nil (empty) result. Its only
// caller, GenerateYAMLs, already returns (*RouteYAMLs, error).
func (q *routeQuery) generateAPIKeyClientResourceYAMLs(route *models.Route, domain *models.Domain) ([]APIKeyClientResourceYAMLs, error) {
	ctx := context.Background()

	// Categorize client attachments
	_, apiKeyOnlyClients, bothClients, err := q.assembler.categorizeClientAttachments(ctx, route.ID, domain)
	if err != nil {
		return nil, err
	}

	// Combine API key clients
	allAPIKeyClients := append(apiKeyOnlyClients, bothClients...)
	if len(allAPIKeyClients) == 0 {
		return nil, nil
	}

	// Get SecurityPolicy for this route (if any) to copy CORS config to per-client routes
	var secPolicy *models.SecurityPolicy
	secPolicy, _ = q.securityPolicyRepo.GetByRouteID(route.ID)

	var results []APIKeyClientResourceYAMLs
	for _, client := range allAPIKeyClients {
		clientResource := APIKeyClientResourceYAMLs{
			ClientID:   client.ClientID.String(),
			ClientName: client.ClientName,
		}

		// Build route YAML (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			grpcRouteConfig := q.assembler.buildAPIKeyGRPCRouteConfig(route, domain, client)
			grpcRoute := kubernetes.BuildGRPCRouteObject(grpcRouteConfig)
			if grpcRoute != nil {
				yamlBytes, err := yaml.Marshal(grpcRoute)
				if err == nil {
					clientResource.HTTPRouteYAML = string(yamlBytes)
				}
			}
		} else {
			httpRouteConfig := q.assembler.buildAPIKeyHTTPRouteConfigRedacted(route, domain, client)
			httpRoute := kubernetes.BuildHTTPRouteObject(httpRouteConfig)
			if httpRoute != nil {
				yamlBytes, err := yaml.Marshal(httpRoute)
				if err == nil {
					clientResource.HTTPRouteYAML = string(yamlBytes)
				}
			}
		}

		// Build SecurityPolicy
		requireIP := client.EnableIP
		securityConfig := q.assembler.buildAPIKeySecurityPolicyConfig(route, domain, client, requireIP, secPolicy)
		securityPolicy := kubernetes.BuildSecurityPolicy(securityConfig)
		if securityPolicy != nil {
			yamlBytes, err := yaml.Marshal(securityPolicy.Object)
			if err == nil {
				clientResource.SecurityPolicyYAML = string(yamlBytes)
			}
		}

		// Build BackendTrafficPolicy if base BTP exists or client has rate limit
		{
			var btpPolicy *models.BackendTrafficPolicy
			btpPolicy, _ = q.backendTrafficPolicyRepo.GetByRouteID(route.ID)
			if btpPolicy != nil || client.RateLimitConfig != nil {
				routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
				clientResource.BackendTrafficPolicyYAML = routeplan.GenerateAPIKeyBackendTrafficPolicyYAML(route, domain, btpPolicy, routeName, client.RateLimitConfig)
			}
		}

		// Build EnvoyExtensionPolicy if base extension policy exists
		{
			var extPolicy *models.EnvoyExtensionPolicy
			extPolicy, _ = q.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
			if extPolicy != nil {
				routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
				clientResource.EnvoyExtensionPolicyYAML = routeplan.GenerateAPIKeyEnvoyExtensionPolicyYAML(route, domain, extPolicy, routeName)
			}
		}

		results = append(results, clientResource)
	}

	return results, nil
}

// PreviewCreate generates a preview of what the HTTPRoute YAML would look like for a new route
func (q *routeQuery) PreviewCreate(domainID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error) {
	// Validate route name format
	if !isValidK8sName(input.Name) {
		return nil, errors.New("route name must be lowercase alphanumeric with dashes only (e.g., 'user-api')")
	}

	// Verify domain exists
	domain, err := q.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Generate a temporary route ID for preview
	tempRouteID := q.assembler.newID()
	// Safe only because input.Name was already validated by isValidK8sName above
	// (see the comment on that function) - kubernetes.RouteK8sName sanitizes
	// differently than isValidK8sName rejects, so they only agree on validated input.
	k8sRouteName := kubernetes.RouteK8sName(input.Name, tempRouteID.String())

	protocol := input.Protocol
	if protocol == "" {
		protocol = models.RouteProtocolHTTP
	}

	// Create a temporary route object for YAML generation
	tempRoute := &models.Route{
		ID:           tempRouteID,
		DomainID:     domainID,
		TeamID:       input.TeamID,
		Name:         input.Name,
		Description:  input.Description,
		Protocol:     protocol,
		Config:       input.Config,
		K8sRouteName: k8sRouteName,
	}

	proposedYAML := routeplan.GenerateHTTPRouteYAML(tempRoute, domain)

	// Generate SecurityPolicy YAML if security features are configured
	// Note: For new routes, there are no client attachments yet, so clientCIDRs is nil
	proposedSecurityPolicyYAML := routeplan.GenerateSecurityPolicyYAML(tempRoute, domain, input.SecurityPolicy, nil)

	// Generate BackendTrafficPolicy YAML if configured
	proposedBackendTrafficPolicyYAML := routeplan.GenerateBackendTrafficPolicyYAML(tempRoute, domain, input.BackendTrafficPolicy)

	// Generate Backend CRD YAML for external backends
	proposedBackendYAML := routeplan.GenerateBackendYAMLs(tempRoute, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes
	var proposedHTTPRouteFilterYAML, proposedConfigMapYAML string
	if input.Config.DirectResponse != nil {
		proposedHTTPRouteFilterYAML, proposedConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(tempRoute, domain)
	}

	// Generate EnvoyExtensionPolicy YAML if configured (extension policy and/or WAF)
	proposedEnvoyExtensionPolicyYAML := routeplan.GenerateEnvoyExtensionPolicyYAMLWithWaf(tempRoute, domain, input.ExtensionPolicy, input.WafPolicy, q.wafConfig)

	return &PreviewCreateResult{
		ProposedYAML:                     proposedYAML,
		ProposedSecurityPolicyYAML:       proposedSecurityPolicyYAML,
		ProposedBackendTrafficPolicyYAML: proposedBackendTrafficPolicyYAML,
		ProposedEnvoyExtensionPolicyYAML: proposedEnvoyExtensionPolicyYAML,
		ProposedBackendYAML:              proposedBackendYAML,
		ProposedHTTPRouteFilterYAML:      proposedHTTPRouteFilterYAML,
		ProposedConfigMapYAML:            proposedConfigMapYAML,
	}, nil
}

// PreviewUpdate generates a preview comparing current and proposed HTTPRoute YAML
func (q *routeQuery) PreviewUpdate(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error) {
	// Get existing route
	route, err := q.routeRepo.GetByID(routeID)
	if err != nil {
		return nil, errors.New("route not found")
	}

	// Get domain
	domain, err := q.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Generate current YAML (with existing config)
	currentYAML := routeplan.GenerateHTTPRouteYAML(route, domain)

	// Get current SecurityPolicy from database (if any)
	var currentSecurityPolicyYAML string
	currentPolicy, _ := q.securityPolicyRepo.GetByRouteID(routeID)
	if currentPolicy != nil {
		currentSecurityPolicyYAML = routeplan.GenerateSecurityPolicyYAMLFromDB(route, domain, currentPolicy)
	}

	// Get current BackendTrafficPolicy from database (if any)
	var currentBackendTrafficPolicyYAML string
	currentBtpPolicy, _ := q.backendTrafficPolicyRepo.GetByRouteID(routeID)
	if currentBtpPolicy != nil {
		currentBackendTrafficPolicyYAML = routeplan.GenerateBackendTrafficPolicyYAMLFromDB(route, domain, currentBtpPolicy)
	}

	// Get current EnvoyExtensionPolicy and WafPolicy from database (if any)
	var currentEnvoyExtensionPolicyYAML string
	var currentExtPolicy *models.EnvoyExtensionPolicy
	var currentWafPolicy *models.WafPolicy
	currentExtPolicy, _ = q.envoyExtensionPolicyRepo.GetByRouteID(routeID)
	currentWafPolicy, _ = q.wafPolicyRepo.GetByRouteID(routeID)
	if currentExtPolicy != nil || currentWafPolicy != nil {
		currentEnvoyExtensionPolicyYAML = q.generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route, domain, currentExtPolicy, currentWafPolicy)
	}

	// Create a copy of the route with updated config for proposed YAML
	proposedRoute := &models.Route{
		ID:           route.ID,
		DomainID:     route.DomainID,
		TeamID:       route.TeamID,
		Name:         route.Name,
		Description:  input.Description,
		Protocol:     route.Protocol,
		Config:       input.Config,
		K8sRouteName: route.K8sRouteName,
	}
	if input.Description == "" {
		proposedRoute.Description = route.Description
	}

	proposedYAML := routeplan.GenerateHTTPRouteYAML(proposedRoute, domain)

	// Collect client CIDRs from existing attachments for preview
	clientCIDRs, err := q.assembler.collectClientIPCIDRs(routeID)
	if err != nil {
		return nil, fmt.Errorf("collect client IP CIDRs for route %s: %w", routeID, err)
	}

	// Generate proposed SecurityPolicy YAML if security features are configured
	// Include client CIDRs to show the full merged result
	proposedSecurityPolicyYAML := routeplan.GenerateSecurityPolicyYAML(proposedRoute, domain, input.SecurityPolicy, clientCIDRs)

	// Generate proposed BackendTrafficPolicy YAML if configured
	proposedBackendTrafficPolicyYAML := routeplan.GenerateBackendTrafficPolicyYAML(proposedRoute, domain, input.BackendTrafficPolicy)

	// Generate Backend CRD YAML for external backends (current and proposed)
	currentBackendYAML := routeplan.GenerateBackendYAMLs(route, domain)
	proposedBackendYAML := routeplan.GenerateBackendYAMLs(proposedRoute, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes (current and proposed)
	var currentHTTPRouteFilterYAML, currentConfigMapYAML string
	if route.Config.DirectResponse != nil {
		currentHTTPRouteFilterYAML, currentConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(route, domain)
	}
	var proposedHTTPRouteFilterYAML, proposedConfigMapYAML string
	if input.Config.DirectResponse != nil {
		proposedHTTPRouteFilterYAML, proposedConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(proposedRoute, domain)
	}

	// Generate proposed EnvoyExtensionPolicy YAML if configured (extension policy and/or WAF)
	proposedEnvoyExtensionPolicyYAML := routeplan.GenerateEnvoyExtensionPolicyYAMLWithWaf(proposedRoute, domain, input.ExtensionPolicy, input.WafPolicy, q.wafConfig)

	return &PreviewUpdateResult{
		CurrentYAML:                      currentYAML,
		ProposedYAML:                     proposedYAML,
		CurrentSecurityPolicyYAML:        currentSecurityPolicyYAML,
		ProposedSecurityPolicyYAML:       proposedSecurityPolicyYAML,
		CurrentBackendTrafficPolicyYAML:  currentBackendTrafficPolicyYAML,
		ProposedBackendTrafficPolicyYAML: proposedBackendTrafficPolicyYAML,
		CurrentEnvoyExtensionPolicyYAML:  currentEnvoyExtensionPolicyYAML,
		ProposedEnvoyExtensionPolicyYAML: proposedEnvoyExtensionPolicyYAML,
		CurrentBackendYAML:               currentBackendYAML,
		ProposedBackendYAML:              proposedBackendYAML,
		CurrentHTTPRouteFilterYAML:       currentHTTPRouteFilterYAML,
		ProposedHTTPRouteFilterYAML:      proposedHTTPRouteFilterYAML,
		CurrentConfigMapYAML:             currentConfigMapYAML,
		ProposedConfigMapYAML:            proposedConfigMapYAML,
	}, nil
}

// PreviewDelete generates a preview of what will be deleted
func (q *routeQuery) PreviewDelete(routeID uuid.UUID) (*PreviewDeleteResult, error) {
	// Get existing route
	route, err := q.routeRepo.GetByID(routeID)
	if err != nil {
		return nil, errors.New("route not found")
	}

	// Get domain
	domain, err := q.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Generate current YAML
	currentYAML := routeplan.GenerateHTTPRouteYAML(route, domain)

	// Get current SecurityPolicy from database (if any)
	var currentSecurityPolicyYAML string
	currentPolicy, _ := q.securityPolicyRepo.GetByRouteID(routeID)
	if currentPolicy != nil {
		currentSecurityPolicyYAML = routeplan.GenerateSecurityPolicyYAMLFromDB(route, domain, currentPolicy)
	}

	// Get current BackendTrafficPolicy from database (if any)
	var currentBackendTrafficPolicyYAML string
	currentBtpPolicy, _ := q.backendTrafficPolicyRepo.GetByRouteID(routeID)
	if currentBtpPolicy != nil {
		currentBackendTrafficPolicyYAML = routeplan.GenerateBackendTrafficPolicyYAMLFromDB(route, domain, currentBtpPolicy)
	}

	// Get current EnvoyExtensionPolicy and WafPolicy from database (if any)
	var currentEnvoyExtensionPolicyYAML string
	var currentExtPolicy *models.EnvoyExtensionPolicy
	var currentWafPolicy *models.WafPolicy
	currentExtPolicy, _ = q.envoyExtensionPolicyRepo.GetByRouteID(routeID)
	currentWafPolicy, _ = q.wafPolicyRepo.GetByRouteID(routeID)
	if currentExtPolicy != nil || currentWafPolicy != nil {
		currentEnvoyExtensionPolicyYAML = q.generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route, domain, currentExtPolicy, currentWafPolicy)
	}

	// Generate Backend CRD YAML for external backends
	currentBackendYAML := routeplan.GenerateBackendYAMLs(route, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes
	var currentHTTPRouteFilterYAML, currentConfigMapYAML string
	if route.Config.DirectResponse != nil {
		currentHTTPRouteFilterYAML, currentConfigMapYAML = routeplan.GenerateDirectResponseYAMLs(route, domain)
	}

	return &PreviewDeleteResult{
		CurrentYAML:                     currentYAML,
		CurrentSecurityPolicyYAML:       currentSecurityPolicyYAML,
		CurrentBackendTrafficPolicyYAML: currentBackendTrafficPolicyYAML,
		CurrentEnvoyExtensionPolicyYAML: currentEnvoyExtensionPolicyYAML,
		CurrentBackendYAML:              currentBackendYAML,
		CurrentHTTPRouteFilterYAML:      currentHTTPRouteFilterYAML,
		CurrentConfigMapYAML:            currentConfigMapYAML,
	}, nil
}

// generateEnvoyExtensionPolicyYAMLFromDBWithWaf generates EnvoyExtensionPolicy YAML from database models with WAF support
func (q *routeQuery) generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy) string {
	// Use buildEnvoyExtensionPolicyConfig which already handles WAF merging
	config := q.assembler.buildEnvoyExtensionPolicyConfig(route, domain, policy, wafPolicy)
	if config == nil {
		return ""
	}

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := kubernetes.BuildEnvoyExtensionPolicy(config)
	if extensionPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(extensionPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating EnvoyExtensionPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// getCorazaWasmImageURL returns the coraza-proxy-wasm image URL
// Configurable via WAF_IMAGE and WAF_TAG environment variables
func getCorazaWasmImageURL() string {
	image := os.Getenv("WAF_IMAGE")
	if image == "" {
		image = "ghcr.io/corazawaf/coraza-proxy-wasm"
	}
	tag := os.Getenv("WAF_TAG")
	if tag == "" {
		tag = "0.6.0"
	}
	return image + ":" + tag
}

// normalizeCIDR ensures a CIDR has a prefix. If it's a plain IP, adds /32 for IPv4 or /128 for IPv6.
func normalizeCIDR(cidr string) string {
	if strings.Contains(cidr, "/") {
		return cidr // Already has a prefix
	}
	ip := net.ParseIP(cidr)
	if ip == nil {
		return cidr // Invalid IP, return as-is (validation will catch it)
	}
	if ip.To4() != nil {
		return cidr + "/32" // IPv4
	}
	return cidr + "/128" // IPv6
}

// RouteService handles route business logic
type RouteService struct {
	routeRepo                repository.RouteRepositoryInterface
	approvalRepo             repository.UnifiedApprovalRepositoryInterface
	policyRepo               repository.ApprovalPolicyRepositoryInterface
	domainRepo               repository.DomainRepositoryInterface
	teamRepo                 repository.TeamRepositoryInterface
	projectNamespaceRepo     repository.ProjectNamespaceRepositoryInterface
	securityPolicyRepo       repository.SecurityPolicyRepositoryInterface
	backendTrafficPolicyRepo repository.BackendTrafficPolicyRepositoryInterface
	envoyExtensionPolicyRepo repository.EnvoyExtensionPolicyRepositoryInterface
	wafPolicyRepo            repository.WafPolicyRepositoryInterface
	clientAttachmentRepo     repository.ClientAttachmentRepositoryInterface
	clientIPRepo             repository.ClientIPRepositoryInterface
	clientHeaderRepo         repository.ClientHeaderRepositoryInterface
	clientRepo               repository.ClientRepositoryInterface
	projectRepo              repository.ProjectRepositoryInterface
	k8sService               KubernetesServiceInterface
	domainService            *DomainService
	routeVersionService      *RouteVersionService
}

// NewRouteService creates a new route service
func NewRouteService(
	routeRepo repository.RouteRepositoryInterface,
	approvalRepo repository.UnifiedApprovalRepositoryInterface,
	policyRepo repository.ApprovalPolicyRepositoryInterface,
	domainRepo repository.DomainRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
) *RouteService {
	return &RouteService{
		routeRepo:    routeRepo,
		approvalRepo: approvalRepo,
		policyRepo:   policyRepo,
		domainRepo:   domainRepo,
		teamRepo:     teamRepo,
	}
}

// SetKubernetesService sets the Kubernetes service (to avoid circular dependency)
func (s *RouteService) SetKubernetesService(k8sService KubernetesServiceInterface) {
	s.k8sService = k8sService
}

// SetApprovalPolicyRepository sets the approval policy repository (to avoid circular dependency)
func (s *RouteService) SetApprovalPolicyRepository(repo repository.ApprovalPolicyRepositoryInterface) {
	s.policyRepo = repo
}

// SetProjectNamespaceRepository sets the project namespace repository (to avoid circular dependency)
func (s *RouteService) SetProjectNamespaceRepository(repo repository.ProjectNamespaceRepositoryInterface) {
	s.projectNamespaceRepo = repo
}

// SetSecurityPolicyRepository sets the security policy repository (to avoid circular dependency)
func (s *RouteService) SetSecurityPolicyRepository(repo repository.SecurityPolicyRepositoryInterface) {
	s.securityPolicyRepo = repo
}

// SetBackendTrafficPolicyRepository sets the backend traffic policy repository (to avoid circular dependency)
func (s *RouteService) SetBackendTrafficPolicyRepository(repo repository.BackendTrafficPolicyRepositoryInterface) {
	s.backendTrafficPolicyRepo = repo
}

// SetEnvoyExtensionPolicyRepository sets the envoy extension policy repository (to avoid circular dependency)
func (s *RouteService) SetEnvoyExtensionPolicyRepository(repo repository.EnvoyExtensionPolicyRepositoryInterface) {
	s.envoyExtensionPolicyRepo = repo
}

// SetWafPolicyRepository sets the WAF policy repository (to avoid circular dependency)
func (s *RouteService) SetWafPolicyRepository(repo repository.WafPolicyRepositoryInterface) {
	s.wafPolicyRepo = repo
}

// SetClientAttachmentRepository sets the client attachment repository (for IP allowlisting during deploy)
func (s *RouteService) SetClientAttachmentRepository(repo repository.ClientAttachmentRepositoryInterface) {
	s.clientAttachmentRepo = repo
}

// SetClientIPRepository sets the client IP repository (for IP allowlisting during deploy)
func (s *RouteService) SetClientIPRepository(repo repository.ClientIPRepositoryInterface) {
	s.clientIPRepo = repo
}

// SetClientHeaderRepository sets the client header repository (for header auth during deploy)
func (s *RouteService) SetClientHeaderRepository(repo repository.ClientHeaderRepositoryInterface) {
	s.clientHeaderRepo = repo
}

// SetClientRepository sets the client repository (for API key access during deploy)
func (s *RouteService) SetClientRepository(repo repository.ClientRepositoryInterface) {
	s.clientRepo = repo
}

// SetDomainService sets the domain service for mTLS CA regeneration during deploy
func (s *RouteService) SetDomainService(ds *DomainService) {
	s.domainService = ds
}

// SetRouteVersionService sets the route version service for version tracking
func (s *RouteService) SetRouteVersionService(rvs *RouteVersionService) {
	s.routeVersionService = rvs
}

// SetProjectRepository sets the project repository for approval bypass
func (s *RouteService) SetProjectRepository(repo repository.ProjectRepositoryInterface) {
	s.projectRepo = repo
}

// buildRouteApprovalStages looks up the approval policy and builds approval stages for route approvals.
// It tries action-specific policy first, then falls back to default (action=nil).
func (s *RouteService) buildRouteApprovalStages(projectID uuid.UUID, submittedBy uuid.UUID, action string) []models.ApprovalStage {
	var stages []models.ApprovalStage
	if s.policyRepo != nil {
		var templates []models.PolicyStageTemplate

		// Step 1: Try action-specific policy
		policy, err := s.policyRepo.GetByProjectAndEntity(projectID, string(models.ApprovalEntityRoute), &action)
		if err != nil || policy == nil {
			// Step 2: Fall back to default policy (action = nil)
			policy, _ = s.policyRepo.GetByProjectAndEntity(projectID, string(models.ApprovalEntityRoute), nil)
		}

		if policy != nil {
			json.Unmarshal(policy.Stages, &templates)
		}

		for _, t := range templates {
			resolvedTeamID, _ := s.resolveTeamScope(t.TeamScope, projectID, submittedBy)
			stages = append(stages, models.ApprovalStage{
				StageOrder:         t.Order,
				RequiredPermission: t.RequiredPermission,
				RequiredTeamID:     resolvedTeamID,
				MinApprovers:       models.EffectiveMinApprovers(t.MinApprovers),
				Status:             models.ApprovalStatusPending,
			})
		}
	}
	if len(stages) == 0 {
		// Default fallback: single stage with route.approve permission
		stages = []models.ApprovalStage{{
			StageOrder:         1,
			RequiredPermission: string(models.PermRouteApprove),
			MinApprovers:       1,
			Status:             models.ApprovalStatusPending,
		}}
	}
	return stages
}

// resolveTeamScope resolves a team_scope string to a concrete team ID
func (s *RouteService) resolveTeamScope(scope string, projectID uuid.UUID, submittedBy uuid.UUID) (*uuid.UUID, error) {
	switch scope {
	case "any", "":
		return nil, nil
	case "submitter_team":
		ptrs, err := s.teamRepo.GetUserTeamsInProject(projectID, submittedBy)
		if err != nil || len(ptrs) == 0 {
			return nil, nil
		}
		teamID := ptrs[0].TeamID
		return &teamID, nil
	case "other_team":
		submitterPtrs, err := s.teamRepo.GetUserTeamsInProject(projectID, submittedBy)
		if err != nil {
			return nil, nil
		}
		submitterTeams := make(map[uuid.UUID]bool)
		for _, ptr := range submitterPtrs {
			submitterTeams[ptr.TeamID] = true
		}
		allPtrs, err := s.teamRepo.ListProjectTeams(projectID)
		if err != nil {
			return nil, nil
		}
		for _, ptr := range allPtrs {
			if !submitterTeams[ptr.TeamID] {
				teamID := ptr.TeamID
				return &teamID, nil
			}
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown team_scope: %s", scope)
	}
}

// validateBackendNamespaces validates that all backend and mirror namespaces are managed by the project
func (s *RouteService) validateBackendNamespaces(projectID uuid.UUID, config *models.RouteConfig) error {
	if s.projectNamespaceRepo == nil {
		// If repository is not set, skip validation (backwards compatibility)
		return nil
	}

	// Validate primary backend namespaces
	for _, backend := range config.Backends {
		// Empty namespace or fastgateway-system namespace is always allowed
		if backend.Namespace == "" || backend.Namespace == FastGatewayNamespace {
			continue
		}

		// Check if namespace is managed by this project
		exists, err := s.projectNamespaceRepo.ExistsByProjectAndNamespace(projectID, backend.Namespace)
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
		if mirror.Namespace == "" || mirror.Namespace == FastGatewayNamespace {
			continue
		}

		// Check if namespace is managed by this project
		exists, err := s.projectNamespaceRepo.ExistsByProjectAndNamespace(projectID, mirror.Namespace)
		if err != nil {
			return fmt.Errorf("failed to validate mirror namespace '%s': %w", mirror.Namespace, err)
		}
		if !exists {
			return fmt.Errorf("mirror namespace '%s' is not managed by this project. Add it in Project Settings > Namespaces before using it as a mirror target", mirror.Namespace)
		}
	}

	return nil
}

// ensureReferenceGrantsForDomain verifies backend namespace ReferenceGrants include
// the domain's namespace. This is a deploy-time safety net.
func (s *RouteService) ensureReferenceGrantsForDomain(ctx context.Context, route *models.Route, domain *models.Domain) {
	if len(route.Config.Backends) == 0 {
		return
	}
	for _, backend := range route.Config.Backends {
		ns := backend.Namespace
		if ns == "" || ns == domain.Namespace {
			continue
		}
		rgName := generateReferenceGrantName(domain.ProjectID, ns)
		exists, _ := s.k8sService.ReferenceGrantExists(ctx, domain.ProjectID, ns, rgName)
		if !exists {
			log.Printf("Deploy safety net: ReferenceGrant missing in %s for domain %s, skipping (will be created on next namespace sync)", ns, domain.Namespace)
		}
	}
}

// validateMirrorTargets ensures mirror backends are different from primary backends
func (s *RouteService) validateMirrorTargets(config *models.RouteConfig) error {
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
func (s *RouteService) validateFailoverConfig(config *models.RouteConfig) error {
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
func (s *RouteService) validateBackendRequiredFields(config *models.RouteConfig) error {
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

// getRouteKind returns the K8s Kind for SecurityPolicy/BTP targetRef based on protocol
func getRouteKind(protocol models.RouteProtocol) string {
	if protocol == models.RouteProtocolGRPC {
		return "GRPCRoute"
	}
	return "HTTPRoute"
}

// validateGRPCRouteConfig validates that gRPC routes don't use HTTP-only features
func validateGRPCRouteConfig(config *models.RouteConfig) error {
	// gRPC only supports backend route type for now
	if config.RouteType == models.RouteTypeRedirect {
		return errors.New("redirect is not supported for gRPC routes")
	}
	if config.RouteType == models.RouteTypeDirectResponse {
		return errors.New("direct response is not supported for gRPC routes")
	}
	if config.URLRewrite != nil {
		return errors.New("URL rewrite is not supported for gRPC routes")
	}
	if config.Redirect != nil {
		return errors.New("redirect config is not supported for gRPC routes")
	}
	if config.DirectResponse != nil {
		return errors.New("direct response config is not supported for gRPC routes")
	}
	// Validate matches don't use HTTP-only fields
	for i, match := range config.Matches {
		if match.Path != nil {
			return fmt.Errorf("match[%d]: path matching is not supported for gRPC routes, use grpcService/grpcMethod", i)
		}
		if match.Method != "" {
			return fmt.Errorf("match[%d]: HTTP method matching is not supported for gRPC routes", i)
		}
		if len(match.QueryParams) > 0 {
			return fmt.Errorf("match[%d]: query parameter matching is not supported for gRPC routes", i)
		}
		// Gateway API GRPCMethodMatch has a single Type for both service and method.
		// If both are specified, their types must match.
		if match.GRPCService != nil && match.GRPCMethod != nil &&
			match.GRPCService.Type != "" && match.GRPCMethod.Type != "" &&
			match.GRPCService.Type != match.GRPCMethod.Type {
			return fmt.Errorf("match[%d]: grpcService type (%s) and grpcMethod type (%s) must be the same, as Gateway API GRPCMethodMatch uses a single type for both", i, match.GRPCService.Type, match.GRPCMethod.Type)
		}
	}
	return nil
}

// validateHTTPRouteConfig validates that HTTP routes don't use gRPC-only features
func validateHTTPRouteConfig(config *models.RouteConfig) error {
	for i, match := range config.Matches {
		if match.GRPCService != nil {
			return fmt.Errorf("match[%d]: grpcService is only supported for gRPC routes", i)
		}
		if match.GRPCMethod != nil {
			return fmt.Errorf("match[%d]: grpcMethod is only supported for gRPC routes", i)
		}
	}
	return nil
}

// validateRouteConfig validates essential route configuration based on route type.
// This ensures routes have the minimum required configuration to function properly.
func validateRouteConfig(config *models.RouteConfig, protocol models.RouteProtocol) error {
	routeType := config.RouteType

	// Backend routes: require at least one backend
	if routeType == "" || routeType == models.RouteTypeBackend {
		if len(config.Backends) == 0 {
			return errors.New("at least one backend is required")
		}
	}

	// Backend and redirect routes: require path matching (HTTP only)
	// gRPC routes use grpcService/grpcMethod instead of path
	if routeType != models.RouteTypeDirectResponse && protocol != models.RouteProtocolGRPC {
		for i, match := range config.Matches {
			if match.Path == nil || match.Path.Value == "" {
				return fmt.Errorf("path matching is required for match rule %d", i+1)
			}
		}
		// Also validate if no matches at all (empty matches array)
		if len(config.Matches) == 0 {
			return errors.New("path matching is required")
		}
	}

	// Redirect routes: require redirect config
	if routeType == models.RouteTypeRedirect {
		if config.Redirect == nil {
			return errors.New("redirect configuration is required for redirect routes")
		}
	}

	return nil
}

// BackendTLSWarnings returns warnings for TLS configuration
func BackendTLSWarnings(config *models.RouteConfig) []string {
	var warnings []string
	for _, backend := range config.Backends {
		if backend.TLS != nil && backend.TLS.InsecureSkipVerify {
			warnings = append(warnings, "insecureSkipVerify skips backend certificate verification. Use only for development/testing or when you trust the network.")
			break // one warning is enough
		}
	}
	return warnings
}

// DirectResponsePercentWarnings returns warnings when a DirectResponse inline
// body contains a literal '%'. On Envoy Gateway v1.8+, '%...%' in the body is
// interpolated as an Envoy command operator. Existing routes with '%' in the
// inline body will produce different output after the cluster admin upgrades
// EG, so we surface a warning at submit time. Detection is intentionally
// simple: any '%' triggers a warning. Authors who genuinely want command
// operators can dismiss it; authors with literal '%' can escape as '%%' or
// switch to a ValueRef body.
func DirectResponsePercentWarnings(config *models.RouteConfig) []string {
	if config == nil || config.DirectResponse == nil || config.DirectResponse.Body == nil {
		return nil
	}
	b := config.DirectResponse.Body
	if b.Type != models.DirectResponseBodyTypeInline {
		return nil
	}
	if !strings.Contains(b.Inline, "%") {
		return nil
	}
	return []string{
		"DirectResponse inline body contains '%'. On Envoy Gateway v1.8+, '%...%' is interpolated as an Envoy command operator. Escape literal '%' as '%%' to keep current behavior, or use a ValueRef body for complex content.",
	}
}

// routeMatchersEqual checks if two RouteMatch structs are equal.
// Comparison is case-insensitive for path values and header values.
// Headers and QueryParams are compared order-independently.
func routeMatchersEqual(a, b models.RouteMatch) bool {
	if !pathMatchEqual(a.Path, b.Path) {
		return false
	}
	if !strings.EqualFold(a.Method, b.Method) {
		return false
	}
	if !headerMatchesEqual(a.Headers, b.Headers) {
		return false
	}
	if !queryParamMatchesEqual(a.QueryParams, b.QueryParams) {
		return false
	}
	if !grpcMatchEqual(a.GRPCService, b.GRPCService) {
		return false
	}
	if !grpcMatchEqual(a.GRPCMethod, b.GRPCMethod) {
		return false
	}
	return true
}

func pathMatchEqual(a, b *models.PathMatch) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Type, b.Type) && strings.EqualFold(a.Value, b.Value)
}

func grpcMatchEqual(a, b *models.GRPCMethodMatch) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Type, b.Type) && strings.EqualFold(a.Value, b.Value)
}

func headerMatchesEqual(a, b []models.HeaderMatch) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	type key struct{ name, typ, value string }
	set := make(map[key]bool, len(a))
	for _, h := range a {
		set[key{strings.ToLower(h.Name), strings.ToLower(h.Type), strings.ToLower(h.Value)}] = true
	}
	for _, h := range b {
		if !set[key{strings.ToLower(h.Name), strings.ToLower(h.Type), strings.ToLower(h.Value)}] {
			return false
		}
	}
	return true
}

func queryParamMatchesEqual(a, b []models.QueryParamMatch) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	type key struct{ name, typ, value string }
	set := make(map[key]bool, len(a))
	for _, q := range a {
		set[key{strings.ToLower(q.Name), strings.ToLower(q.Type), strings.ToLower(q.Value)}] = true
	}
	for _, q := range b {
		if !set[key{strings.ToLower(q.Name), strings.ToLower(q.Type), strings.ToLower(q.Value)}] {
			return false
		}
	}
	return true
}

// validateMatcherConflict checks if the given route config's matcher conflicts with
// any existing route in the same domain. excludeRouteID can be set to skip the route
// being updated. Returns an error naming the conflicting route if found.
func (s *RouteService) validateMatcherConflict(domainID uuid.UUID, config *models.RouteConfig, excludeRouteID *uuid.UUID) error {
	if len(config.Matches) == 0 {
		return nil
	}
	newMatch := config.Matches[0]

	// Fetch all routes in the domain (no filters, high limit)
	existingRoutes, _, err := s.routeRepo.ListByDomainID(domainID, 1, 10000, nil, "", "", "", nil)
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

// ConflictResult represents a route that conflicts with a given matcher.
type ConflictResult struct {
	RouteID   uuid.UUID `json:"routeId"`
	RouteName string    `json:"routeName"`
}

// CheckMatcherConflicts checks if the given matcher conflicts with any existing route
// in the domain. Returns all conflicting routes. excludeRouteID can be set to skip
// the route being updated.
func (s *RouteService) CheckMatcherConflicts(domainID uuid.UUID, match models.RouteMatch, excludeRouteID *uuid.UUID) ([]ConflictResult, error) {
	existingRoutes, _, err := s.routeRepo.ListByDomainID(domainID, 1, 10000, nil, "", "", "", nil)
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

// SecurityPolicyInput represents security policy configuration input
type SecurityPolicyInput struct {
	CORS          *models.CORSConfig    `json:"cors,omitempty"`
	Authorization *AuthorizationInput   `json:"authorization,omitempty"` // General mode: IP allowlisting
	APIKeyAuth    *APIKeyAuthInput      `json:"apiKeyAuth,omitempty"`    // General mode: API key auth
	JWT           *JWTInput             `json:"jwt,omitempty"`           // General mode: JWT validation
	OIDC          *OIDCInput            `json:"oidc,omitempty"`          // General mode: OIDC/SSO login
	ExtAuth       *models.ExtAuthConfig `json:"extAuth,omitempty"`       // Both modes: external authorization
}

// AuthorizationInput represents authorization input for general mode
type AuthorizationInput struct {
	AllowedCIDRs []string                          `json:"allowedCIDRs"`
	Headers      []models.AuthorizationHeaderMatch `json:"headers,omitempty"`
	Methods      []string                          `json:"methods,omitempty"`
}

// APIKeyAuthInput represents API key authentication input for general mode
type APIKeyAuthInput struct {
	SecretName string `json:"secretName"`
	HeaderName string `json:"headerName"`
}

// JWTInput represents JWT validation input for general mode
type JWTInput struct {
	Issuer         string                      `json:"issuer"`
	JWKSURL        string                      `json:"jwksUrl"`
	Audiences      []string                    `json:"audiences,omitempty"`
	ClaimToHeaders []models.SPJWTClaimToHeader `json:"claimToHeaders,omitempty"`
}

// OIDCInput represents OIDC/SSO login input for general mode
type OIDCInput struct {
	Issuer           string   `json:"issuer"`
	ClientID         string   `json:"clientId"`
	ClientSecretName string   `json:"clientSecretName"`
	RedirectURL      string   `json:"redirectURL"`
	LogoutPath       string   `json:"logoutPath"`
	Scopes           []string `json:"scopes,omitempty"`
	CookieDomain     string   `json:"cookieDomain,omitempty"`
}

// validateSecurityModeGeneral validates that general mode security input is valid
func validateSecurityModeGeneral(input *SecurityPolicyInput) error {
	if input == nil {
		return nil
	}

	if input.OIDC != nil {
		if input.OIDC.Issuer == "" {
			return errors.New("OIDC issuer is required")
		}
		if input.OIDC.ClientID == "" {
			return errors.New("OIDC clientId is required")
		}
		if input.OIDC.ClientSecretName == "" {
			return errors.New("OIDC clientSecretName is required")
		}
		if input.OIDC.RedirectURL == "" {
			return errors.New("OIDC redirectURL is required")
		}
		if !strings.HasPrefix(input.OIDC.RedirectURL, "https://") {
			return errors.New("OIDC redirectURL must use HTTPS")
		}
		if input.OIDC.LogoutPath == "" {
			return errors.New("OIDC logoutPath is required")
		}
	}

	if input.JWT != nil {
		if input.JWT.Issuer == "" {
			return errors.New("JWT issuer is required")
		}
		if input.JWT.JWKSURL == "" {
			return errors.New("JWT jwksUrl is required")
		}
	}

	if input.APIKeyAuth != nil {
		if input.APIKeyAuth.SecretName == "" {
			return errors.New("API key secretName is required")
		}
		if input.APIKeyAuth.HeaderName == "" {
			return errors.New("API key headerName is required")
		}
	}

	if input.Authorization != nil {
		hasCIDRs := len(input.Authorization.AllowedCIDRs) > 0
		hasHeaders := len(input.Authorization.Headers) > 0
		hasMethods := len(input.Authorization.Methods) > 0
		if !hasCIDRs && !hasHeaders && !hasMethods {
			return errors.New("authorization requires at least one of: allowedCIDRs, headers, or methods")
		}
		for _, cidr := range input.Authorization.AllowedCIDRs {
			normalized := normalizeCIDR(cidr)
			if _, _, err := net.ParseCIDR(normalized); err != nil {
				return fmt.Errorf("invalid CIDR or IP address: %s", cidr)
			}
		}
		for _, h := range input.Authorization.Headers {
			if strings.TrimSpace(h.Name) == "" {
				return errors.New("authorization header name must not be empty")
			}
			if len(h.Values) == 0 {
				return errors.New("authorization header must have at least one value")
			}
		}
		validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true}
		for _, m := range input.Authorization.Methods {
			if !validMethods[strings.ToUpper(m)] {
				return fmt.Errorf("invalid HTTP method: %s", m)
			}
		}
	}

	// Validate ExtAuth if present
	if input.ExtAuth != nil {
		if err := input.ExtAuth.Validate(); err != nil {
			return fmt.Errorf("extAuth: %w", err)
		}
	}

	return nil
}

// validateSecurityModeClient validates that client mode doesn't have general-only fields
func validateSecurityModeClient(input *SecurityPolicyInput) error {
	if input == nil {
		return nil
	}
	if input.Authorization != nil {
		return errors.New("IP allowlisting via authorization is not available in client security mode; use client attachments instead")
	}
	if input.APIKeyAuth != nil {
		return errors.New("API key auth is not available in client security mode; use client attachments instead")
	}
	if input.JWT != nil {
		return errors.New("JWT auth is not available in client security mode; use client attachments instead")
	}
	if input.OIDC != nil {
		return errors.New("OIDC is not available in client security mode; use general security mode")
	}
	// ExtAuth is allowed in client mode (per-attachment config takes precedence)
	if input.ExtAuth != nil {
		if err := input.ExtAuth.Validate(); err != nil {
			return fmt.Errorf("extAuth: %w", err)
		}
	}
	return nil
}

// BackendTrafficPolicyInput represents backend traffic policy configuration input
type BackendTrafficPolicyInput struct {
	Compression      []models.CompressionConfig    `json:"compression,omitempty"`
	Retry            *models.RetryConfig           `json:"retry,omitempty"`
	LoadBalancer     *models.LoadBalancerConfig    `json:"loadBalancer,omitempty"`
	CircuitBreaker   *models.CircuitBreakerConfig  `json:"circuitBreaker,omitempty"`
	HealthCheck      *models.HealthCheckConfig     `json:"healthCheck,omitempty"`
	FaultInjection   *models.FaultInjectionConfig  `json:"faultInjection,omitempty"`
	RateLimit        *models.RateLimitConfig       `json:"rateLimit,omitempty"`
	RequestBuffer    *models.RequestBufferConfig   `json:"requestBuffer,omitempty"`
	ResponseOverride []models.ResponseOverrideRule `json:"responseOverride,omitempty"`
	Timeout          *models.BTPTimeoutConfig      `json:"timeout,omitempty"`
}

// HasContent checks if the input has any features configured
func (i *BackendTrafficPolicyInput) HasContent() bool {
	return len(i.Compression) > 0 || i.Retry != nil || i.LoadBalancer != nil || i.CircuitBreaker != nil || i.HealthCheck != nil || i.FaultInjection != nil || i.RateLimit != nil || i.RequestBuffer != nil || len(i.ResponseOverride) > 0 || i.Timeout != nil
}

// EnvoyExtensionPolicyInput represents input for Envoy extension policy creation
type EnvoyExtensionPolicyInput struct {
	Lua     *models.LuaExtensionConfig     `json:"lua,omitempty"`
	Wasm    *models.WasmExtensionConfig    `json:"wasm,omitempty"`
	ExtProc *models.ExtProcExtensionConfig `json:"extProc,omitempty"`
}

// HasContent checks if the input has any extensions configured
func (i *EnvoyExtensionPolicyInput) HasContent() bool {
	return i.Lua != nil || i.Wasm != nil || i.ExtProc != nil
}

// WafPolicyInput represents WAF policy input for route operations
type WafPolicyInput struct {
	Mode             string   `json:"mode"`
	Rulesets         []string `json:"rulesets,omitempty"`
	AnomalyThreshold *int     `json:"anomalyThreshold,omitempty"`
	ParanoiaLevel    *int     `json:"paranoiaLevel,omitempty"`
	DisabledRuleIDs  []int    `json:"disabledRuleIDs,omitempty"`
	CustomDirectives []string `json:"customDirectives,omitempty"`
}

// CreateRouteInput represents input for creating a route
type CreateRouteInput struct {
	Name                 string                     `json:"name" binding:"required,min=1,max=63"`
	Description          string                     `json:"description"`
	Protocol             models.RouteProtocol       `json:"protocol"`
	SecurityMode         models.SecurityMode        `json:"securityMode"`
	TeamID               uuid.UUID                  `json:"teamId" binding:"required"`
	Config               models.RouteConfig         `json:"config" binding:"required"`
	SecurityPolicy       *SecurityPolicyInput       `json:"securityPolicy,omitempty"`       // Optional security policy (CORS, auth)
	BackendTrafficPolicy *BackendTrafficPolicyInput `json:"backendTrafficPolicy,omitempty"` // Optional backend traffic policy (compression)
	ExtensionPolicy      *EnvoyExtensionPolicyInput `json:"extensionPolicy,omitempty"`      // Optional extension policy (Lua, Wasm)
	WafPolicy            *WafPolicyInput            `json:"wafPolicy,omitempty"`            // Optional WAF policy
	ChangeDescription    string                     `json:"changeDescription,omitempty"`
	AIReview             json.RawMessage            `json:"aiReview,omitempty"`
	Labels               models.Labels              `json:"labels,omitempty"`
}

// UpdateRouteInput represents input for updating a route
type UpdateRouteInput struct {
	Description          string                     `json:"description"`
	Config               models.RouteConfig         `json:"config" binding:"required"`
	SecurityPolicy       *SecurityPolicyInput       `json:"securityPolicy,omitempty"`       // Optional security policy (CORS, auth)
	BackendTrafficPolicy *BackendTrafficPolicyInput `json:"backendTrafficPolicy,omitempty"` // Optional backend traffic policy (compression)
	ExtensionPolicy      *EnvoyExtensionPolicyInput `json:"extensionPolicy,omitempty"`      // Optional extension policy (Lua, Wasm)
	WafPolicy            *WafPolicyInput            `json:"wafPolicy,omitempty"`            // Optional WAF policy
	ChangeDescription    string                     `json:"changeDescription,omitempty"`
	AIReview             json.RawMessage            `json:"aiReview,omitempty"`
	Labels               models.Labels              `json:"labels,omitempty"`
}

// Create creates a new route (submits for approval)
func (s *RouteService) Create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID) (*models.Route, error) {
	// Validate route name - no spaces allowed
	if strings.Contains(input.Name, " ") {
		return nil, errors.New("route name cannot contain spaces")
	}

	// Validate route name format - must be lowercase alphanumeric with dashes
	if !isValidK8sName(input.Name) {
		return nil, errors.New("route name must be lowercase alphanumeric with dashes only (e.g., 'user-api')")
	}

	// Check if route name already exists in domain
	exists, err := s.routeRepo.ExistsByName(domainID, input.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("route name already exists in this domain")
	}

	// Verify domain exists
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Validate backend required fields (namespace, service, port)
	if err := s.validateBackendRequiredFields(&input.Config); err != nil {
		return nil, err
	}

	// Validate backend namespaces are managed by the project
	if err := s.validateBackendNamespaces(domain.ProjectID, &input.Config); err != nil {
		return nil, err
	}

	// Validate mirror targets (must be different from primary backends)
	if err := s.validateMirrorTargets(&input.Config); err != nil {
		return nil, err
	}

	// Validate failover configuration (must have at least one primary when fallback exists)
	if err := s.validateFailoverConfig(&input.Config); err != nil {
		return nil, err
	}

	// Validate retry configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.Retry != nil {
		if err := input.BackendTrafficPolicy.Retry.Validate(); err != nil {
			return nil, fmt.Errorf("invalid retry configuration: %w", err)
		}
	}

	// Validate load balancer configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.LoadBalancer != nil {
		if err := input.BackendTrafficPolicy.LoadBalancer.Validate(); err != nil {
			return nil, fmt.Errorf("invalid load balancer configuration: %w", err)
		}
	}

	// Validate circuit breaker configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.CircuitBreaker != nil {
		if err := input.BackendTrafficPolicy.CircuitBreaker.Validate(); err != nil {
			return nil, fmt.Errorf("invalid circuit breaker configuration: %w", err)
		}
	}

	// Validate health check configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HealthCheck != nil {
		if err := input.BackendTrafficPolicy.HealthCheck.Validate(); err != nil {
			return nil, fmt.Errorf("invalid health check configuration: %w", err)
		}
	}

	// Validate fault injection configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.FaultInjection != nil {
		if err := input.BackendTrafficPolicy.FaultInjection.Validate(); err != nil {
			return nil, fmt.Errorf("invalid fault injection configuration: %w", err)
		}
	}

	// Validate rate limit configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.RateLimit != nil {
		if err := input.BackendTrafficPolicy.RateLimit.Validate(); err != nil {
			return nil, fmt.Errorf("invalid rate limit configuration: %w", err)
		}
	}

	// Validate timeout configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.Timeout != nil {
		if err := input.BackendTrafficPolicy.Timeout.Validate(); err != nil {
			return nil, fmt.Errorf("invalid timeout configuration: %w", err)
		}
	}

	// Validate direct response configuration if provided
	if input.Config.RouteType == models.RouteTypeDirectResponse {
		if input.Config.DirectResponse == nil {
			return nil, errors.New("directResponse configuration is required for directResponse route type")
		}
		if err := input.Config.DirectResponse.Validate(); err != nil {
			return nil, fmt.Errorf("invalid direct response configuration: %w", err)
		}
		// Direct response routes cannot have backends
		if len(input.Config.Backends) > 0 {
			return nil, errors.New("directResponse routes cannot have backends")
		}
		// Direct response routes cannot have URL rewrite
		if input.Config.URLRewrite != nil {
			return nil, errors.New("directResponse routes cannot have URL rewrite")
		}
		// Direct response routes cannot have request header modifier
		if input.Config.RequestHeaderModifier != nil {
			return nil, errors.New("directResponse routes cannot have request header modifier")
		}
		// Direct response routes cannot have backend traffic policy
		if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
			return nil, errors.New("directResponse routes cannot have backend traffic policy")
		}
	}

	// Verify team exists
	_, err = s.teamRepo.GetByID(input.TeamID)
	if err != nil {
		return nil, errors.New("team not found")
	}

	protocol := input.Protocol
	if protocol == "" {
		protocol = models.RouteProtocolHTTP
	}

	// Default security mode
	securityMode := input.SecurityMode
	if securityMode == "" {
		securityMode = models.SecurityModeGeneral
	}

	// Validate security mode specific config
	if securityMode == models.SecurityModeGeneral {
		if err := validateSecurityModeGeneral(input.SecurityPolicy); err != nil {
			return nil, err
		}
	} else if securityMode == models.SecurityModeClient {
		if err := validateSecurityModeClient(input.SecurityPolicy); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("invalid security mode: %s (must be 'general' or 'client')", securityMode)
	}

	// Validate protocol-specific config
	if protocol == models.RouteProtocolGRPC {
		if err := validateGRPCRouteConfig(&input.Config); err != nil {
			return nil, err
		}
	} else {
		if err := validateHTTPRouteConfig(&input.Config); err != nil {
			return nil, err
		}
	}

	// Validate essential route configuration
	if err := validateRouteConfig(&input.Config, protocol); err != nil {
		return nil, err
	}

	// Check for matcher conflicts with existing routes in the domain
	if err := s.validateMatcherConflict(domainID, &input.Config, nil); err != nil {
		return nil, err
	}

	// Generate route UUID first so we can use it for K8s resource name
	routeID := uuid.New()

	// Generate K8s resource name: {route-name}-{first-8-chars-of-route-uuid}
	k8sRouteName := generateRouteK8sName(input.Name, routeID)

	// Validate labels
	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
	}

	route := &models.Route{
		DomainID:     domainID,
		TeamID:       input.TeamID,
		Name:         input.Name,
		Description:  input.Description,
		Protocol:     protocol,
		SecurityMode: securityMode,
		Config:       input.Config,
		Status:       models.RouteStatusPendingCreate,
		K8sRouteName: k8sRouteName,
		CreatedBy:    createdBy,
		Labels:       input.Labels,
	}
	route.ID = routeID // Set the pre-generated UUID

	if err := s.routeRepo.Create(route); err != nil {
		return nil, err
	}

	// Build config snapshot for unified approval
	var snapshotSP *models.SecurityPolicyConfig
	if input.SecurityPolicy != nil {
		snapshot := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if securityMode == models.SecurityModeGeneral {
			snapshot.Authorization = buildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			snapshot.APIKeyAuth = buildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			snapshot.JWT = buildJWTConfigFromInput(input.SecurityPolicy.JWT)
			snapshot.OIDC = buildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		snapshot.ExtAuth = input.SecurityPolicy.ExtAuth
		snapshotSP = &snapshot
	}

	var snapshotBTP *models.BackendTrafficPolicyConfig
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
		snapshotBTP = &models.BackendTrafficPolicyConfig{
			Compression:      input.BackendTrafficPolicy.Compression,
			Retry:            input.BackendTrafficPolicy.Retry,
			LoadBalancer:     input.BackendTrafficPolicy.LoadBalancer,
			CircuitBreaker:   input.BackendTrafficPolicy.CircuitBreaker,
			HealthCheck:      input.BackendTrafficPolicy.HealthCheck,
			FaultInjection:   input.BackendTrafficPolicy.FaultInjection,
			RateLimit:        input.BackendTrafficPolicy.RateLimit,
			RequestBuffer:    input.BackendTrafficPolicy.RequestBuffer,
			ResponseOverride: input.BackendTrafficPolicy.ResponseOverride,
			Timeout:          input.BackendTrafficPolicy.Timeout,
		}
	}

	var snapshotEEP *models.EnvoyExtensionPolicyConfig
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() {
		snapshotEEP = &models.EnvoyExtensionPolicyConfig{
			Lua:     input.ExtensionPolicy.Lua,
			Wasm:    input.ExtensionPolicy.Wasm,
			ExtProc: input.ExtensionPolicy.ExtProc,
		}
	}

	var snapshotWaf *models.WafPolicyConfig
	if input.WafPolicy != nil {
		wafCfg := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafCfg.Validate(); err == nil {
			snapshotWaf = &wafCfg
		}
	}

	// Check if approvals are disabled for this project
	if s.projectRepo != nil {
		project, err := s.projectRepo.GetByID(domain.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project approval settings: %w", err)
		}
		if !project.ApprovalEnabled {
			// Skip approval — set route directly to approved
			route.Status = models.RouteStatusApproved
			if err := s.routeRepo.Update(route); err != nil {
				return nil, err
			}
			return route, nil
		}
	}

	configSnapshot, _ := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          &input.Config,
		SecurityPolicy:       snapshotSP,
		BackendTrafficPolicy: snapshotBTP,
		EnvoyExtensionPolicy: snapshotEEP,
		WafPolicy:            snapshotWaf,
	})

	approval := &models.Approval{
		ProjectID:         domain.ProjectID,
		EntityType:        models.ApprovalEntityRoute,
		EntityID:          route.ID,
		Action:            models.ApprovalActionCreate,
		ConfigSnapshot:    configSnapshot,
		SubmittedBy:       createdBy,
		Status:            models.ApprovalStatusPending,
		ChangeDescription: input.ChangeDescription,
		AIReview:          input.AIReview,
		Stages:            s.buildRouteApprovalStages(domain.ProjectID, createdBy, "create"),
	}

	if err := s.approvalRepo.Create(approval); err != nil {
		return nil, err
	}

	// Create SecurityPolicy if provided
	if input.SecurityPolicy != nil && s.securityPolicyRepo != nil {
		spConfig := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if securityMode == models.SecurityModeGeneral {
			spConfig.Authorization = buildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			spConfig.APIKeyAuth = buildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			spConfig.JWT = buildJWTConfigFromInput(input.SecurityPolicy.JWT)
			spConfig.OIDC = buildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		spConfig.ExtAuth = input.SecurityPolicy.ExtAuth
		securityPolicy := &models.SecurityPolicy{
			RouteID:   route.ID,
			ProjectID: domain.ProjectID,
			Config:    spConfig,
		}
		if err := s.securityPolicyRepo.Create(securityPolicy); err != nil {
			return nil, fmt.Errorf("failed to create security policy: %w", err)
		}
	}

	// Create BackendTrafficPolicy if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() && s.backendTrafficPolicyRepo != nil {
		backendTrafficPolicy := &models.BackendTrafficPolicy{
			RouteID:   &route.ID,
			ProjectID: domain.ProjectID,
			Config: models.BackendTrafficPolicyConfig{
				Compression:      input.BackendTrafficPolicy.Compression,
				Retry:            input.BackendTrafficPolicy.Retry,
				LoadBalancer:     input.BackendTrafficPolicy.LoadBalancer,
				CircuitBreaker:   input.BackendTrafficPolicy.CircuitBreaker,
				HealthCheck:      input.BackendTrafficPolicy.HealthCheck,
				FaultInjection:   input.BackendTrafficPolicy.FaultInjection,
				RateLimit:        input.BackendTrafficPolicy.RateLimit,
				RequestBuffer:    input.BackendTrafficPolicy.RequestBuffer,
				ResponseOverride: input.BackendTrafficPolicy.ResponseOverride,
				Timeout:          input.BackendTrafficPolicy.Timeout,
			},
		}
		if err := s.backendTrafficPolicyRepo.Create(backendTrafficPolicy); err != nil {
			return nil, fmt.Errorf("failed to create backend traffic policy: %w", err)
		}
	}

	// Create EnvoyExtensionPolicy if provided
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() && s.envoyExtensionPolicyRepo != nil {
		extensionPolicy := &models.EnvoyExtensionPolicy{
			RouteID:   &route.ID,
			ProjectID: domain.ProjectID,
			Config: models.EnvoyExtensionPolicyConfig{
				Lua:     input.ExtensionPolicy.Lua,
				Wasm:    input.ExtensionPolicy.Wasm,
				ExtProc: input.ExtensionPolicy.ExtProc,
			},
		}
		if err := s.envoyExtensionPolicyRepo.Create(extensionPolicy); err != nil {
			return nil, fmt.Errorf("failed to create envoy extension policy: %w", err)
		}
	}

	// Create WAF policy if provided
	if input.WafPolicy != nil && s.wafPolicyRepo != nil {
		wafConfig := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafConfig.Validate(); err != nil {
			return nil, fmt.Errorf("invalid WAF policy config: %w", err)
		}

		wafPolicy := &models.WafPolicy{
			RouteID:   route.ID,
			ProjectID: domain.ProjectID,
			Config:    wafConfig,
		}
		if err := s.wafPolicyRepo.Create(wafPolicy); err != nil {
			return nil, fmt.Errorf("failed to create WAF policy: %w", err)
		}
	}

	route.PendingApproval = approval
	return route, nil
}

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
func (s *RouteService) populateRouteComputedFields(route *models.Route) {
	if route == nil {
		return
	}

	// Count client attachments
	route.ClientCount = s.countClientAttachments(route.ID)

	// Compute security status
	route.SecurityStatus = s.computeSecurityStatus(route)
}

// computeSecurityStatus computes the security status of a route
func (s *RouteService) computeSecurityStatus(route *models.Route) models.SecurityStatus {
	// General mode: check if any security feature is configured in the security policy
	if route.SecurityMode == models.SecurityModeGeneral {
		if s.securityPolicyRepo == nil {
			return models.SecurityStatusNone
		}
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
	if s.securityPolicyRepo == nil {
		return nil, nil
	}
	policy, err := s.securityPolicyRepo.GetByRouteID(routeID)
	if err != nil {
		// Not found is not an error, just return nil
		return nil, nil
	}
	return policy, nil
}

// GetBackendTrafficPolicy gets the backend traffic policy for a route
func (s *RouteService) GetBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	if s.backendTrafficPolicyRepo == nil {
		return nil, nil
	}
	policy, err := s.backendTrafficPolicyRepo.GetByRouteID(routeID)
	if err != nil {
		// Not found is not an error, just return nil
		return nil, nil
	}
	return policy, nil
}

// GetEnvoyExtensionPolicy gets the envoy extension policy for a route
func (s *RouteService) GetEnvoyExtensionPolicy(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	if s.envoyExtensionPolicyRepo == nil {
		return nil, nil
	}
	policy, err := s.envoyExtensionPolicyRepo.GetByRouteID(routeID)
	if err != nil {
		// Not found is not an error, just return nil
		return nil, nil
	}
	return policy, nil
}

// GetWafPolicy gets the WAF policy for a route
func (s *RouteService) GetWafPolicy(routeID uuid.UUID) (*models.WafPolicy, error) {
	if s.wafPolicyRepo == nil {
		return nil, nil
	}
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
func (s *RouteService) ListByProjectID(projectID uuid.UUID, page, limit int, filters repository.RouteListFilters) ([]models.Route, int64, error) {
	routes, total, err := s.routeRepo.ListByProjectID(projectID, page, limit, filters)
	if err != nil {
		return nil, 0, err
	}
	for i := range routes {
		s.populateRouteComputedFields(&routes[i])
	}
	return routes, total, nil
}

// Update updates a route (submits for approval)
func (s *RouteService) Update(id uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error) {
	route, err := s.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Get domain to validate namespaces
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Validate backend required fields (namespace, service, port)
	if err := s.validateBackendRequiredFields(&input.Config); err != nil {
		return nil, err
	}

	// Validate backend namespaces are managed by the project
	if err := s.validateBackendNamespaces(domain.ProjectID, &input.Config); err != nil {
		return nil, err
	}

	// Validate mirror targets (must be different from primary backends)
	if err := s.validateMirrorTargets(&input.Config); err != nil {
		return nil, err
	}

	// Validate failover configuration (must have at least one primary when fallback exists)
	if err := s.validateFailoverConfig(&input.Config); err != nil {
		return nil, err
	}

	// Validate retry configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.Retry != nil {
		if err := input.BackendTrafficPolicy.Retry.Validate(); err != nil {
			return nil, fmt.Errorf("invalid retry configuration: %w", err)
		}
	}

	// Validate load balancer configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.LoadBalancer != nil {
		if err := input.BackendTrafficPolicy.LoadBalancer.Validate(); err != nil {
			return nil, fmt.Errorf("invalid load balancer configuration: %w", err)
		}
	}

	// Validate circuit breaker configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.CircuitBreaker != nil {
		if err := input.BackendTrafficPolicy.CircuitBreaker.Validate(); err != nil {
			return nil, fmt.Errorf("invalid circuit breaker configuration: %w", err)
		}
	}

	// Validate health check configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HealthCheck != nil {
		if err := input.BackendTrafficPolicy.HealthCheck.Validate(); err != nil {
			return nil, fmt.Errorf("invalid health check configuration: %w", err)
		}
	}

	// Validate fault injection configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.FaultInjection != nil {
		if err := input.BackendTrafficPolicy.FaultInjection.Validate(); err != nil {
			return nil, fmt.Errorf("invalid fault injection configuration: %w", err)
		}
	}

	// Validate rate limit configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.RateLimit != nil {
		if err := input.BackendTrafficPolicy.RateLimit.Validate(); err != nil {
			return nil, fmt.Errorf("invalid rate limit configuration: %w", err)
		}
	}

	// Validate timeout configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.Timeout != nil {
		if err := input.BackendTrafficPolicy.Timeout.Validate(); err != nil {
			return nil, fmt.Errorf("invalid timeout configuration: %w", err)
		}
	}

	// Validate security mode specific config (use route's existing security mode)
	if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
		if err := validateSecurityModeGeneral(input.SecurityPolicy); err != nil {
			return nil, err
		}
	} else if route.SecurityMode == models.SecurityModeClient {
		if err := validateSecurityModeClient(input.SecurityPolicy); err != nil {
			return nil, err
		}
	}

	// Validate protocol-specific config
	if route.Protocol == models.RouteProtocolGRPC {
		if err := validateGRPCRouteConfig(&input.Config); err != nil {
			return nil, err
		}
	} else {
		if err := validateHTTPRouteConfig(&input.Config); err != nil {
			return nil, err
		}
	}

	// Validate essential route configuration
	if err := validateRouteConfig(&input.Config, route.Protocol); err != nil {
		return nil, err
	}

	// Check for matcher conflicts with existing routes in the domain
	if err := s.validateMatcherConflict(route.DomainID, &input.Config, &id); err != nil {
		return nil, err
	}

	// Validate direct response configuration if provided
	if input.Config.RouteType == models.RouteTypeDirectResponse {
		if input.Config.DirectResponse == nil {
			return nil, errors.New("directResponse configuration is required for directResponse route type")
		}
		if err := input.Config.DirectResponse.Validate(); err != nil {
			return nil, fmt.Errorf("invalid direct response configuration: %w", err)
		}
		// Direct response routes cannot have backends
		if len(input.Config.Backends) > 0 {
			return nil, errors.New("directResponse routes cannot have backends")
		}
		// Direct response routes cannot have URL rewrite
		if input.Config.URLRewrite != nil {
			return nil, errors.New("directResponse routes cannot have URL rewrite")
		}
		// Direct response routes cannot have request header modifier
		if input.Config.RequestHeaderModifier != nil {
			return nil, errors.New("directResponse routes cannot have request header modifier")
		}
		// Direct response routes cannot have backend traffic policy
		if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
			return nil, errors.New("directResponse routes cannot have backend traffic policy")
		}
	}

	// Check if there's already a pending approval
	existing, err := s.approvalRepo.GetPendingByEntityID(models.ApprovalEntityRoute, id)
	if err == nil && existing != nil {
		return nil, errors.New("there is already a pending approval for this route")
	}

	// Store previous config
	previousConfig := route.Config

	// Capture previous SecurityPolicy config (before update)
	var previousSecurityPolicy *models.SecurityPolicyConfig
	if s.securityPolicyRepo != nil {
		if existingSP, err := s.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
			spConfig := existingSP.Config
			previousSecurityPolicy = &spConfig
		}
	}

	// Capture previous BackendTrafficPolicy config (before update)
	var previousBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	if s.backendTrafficPolicyRepo != nil {
		if existingBTP, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
			btpConfig := existingBTP.Config
			previousBackendTrafficPolicy = &btpConfig
		}
	}

	// Capture previous EnvoyExtensionPolicy config (before update)
	var previousEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	if s.envoyExtensionPolicyRepo != nil {
		if existingEEP, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
			eepConfig := existingEEP.Config
			previousEnvoyExtensionPolicy = &eepConfig
		}
	}

	// Update route status
	route.Status = models.RouteStatusPendingUpdate
	if input.Description != "" {
		route.Description = input.Description
	}
	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
		route.Labels = input.Labels
	}

	if err := s.routeRepo.Update(route); err != nil {
		return nil, err
	}

	// Build config snapshot for unified approval
	var updateSnapshotSP *models.SecurityPolicyConfig
	if input.SecurityPolicy != nil {
		snapshot := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
			snapshot.Authorization = buildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			snapshot.APIKeyAuth = buildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			snapshot.JWT = buildJWTConfigFromInput(input.SecurityPolicy.JWT)
			snapshot.OIDC = buildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		snapshot.ExtAuth = input.SecurityPolicy.ExtAuth
		updateSnapshotSP = &snapshot
	}

	var updateSnapshotBTP *models.BackendTrafficPolicyConfig
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
		updateSnapshotBTP = &models.BackendTrafficPolicyConfig{
			Compression:      input.BackendTrafficPolicy.Compression,
			Retry:            input.BackendTrafficPolicy.Retry,
			LoadBalancer:     input.BackendTrafficPolicy.LoadBalancer,
			CircuitBreaker:   input.BackendTrafficPolicy.CircuitBreaker,
			HealthCheck:      input.BackendTrafficPolicy.HealthCheck,
			FaultInjection:   input.BackendTrafficPolicy.FaultInjection,
			RateLimit:        input.BackendTrafficPolicy.RateLimit,
			RequestBuffer:    input.BackendTrafficPolicy.RequestBuffer,
			ResponseOverride: input.BackendTrafficPolicy.ResponseOverride,
			Timeout:          input.BackendTrafficPolicy.Timeout,
		}
	}

	var updateSnapshotEEP *models.EnvoyExtensionPolicyConfig
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() {
		updateSnapshotEEP = &models.EnvoyExtensionPolicyConfig{
			Lua:  input.ExtensionPolicy.Lua,
			Wasm: input.ExtensionPolicy.Wasm,
		}
	}

	// Build WAF snapshot for proposed config
	var updateSnapshotWaf *models.WafPolicyConfig
	if input.WafPolicy != nil {
		wafCfg := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafCfg.Validate(); err == nil {
			updateSnapshotWaf = &wafCfg
		}
	}

	// Capture previous WAF policy for approval diff
	var previousWafPolicy *models.WafPolicyConfig
	if s.wafPolicyRepo != nil {
		if existingWaf, err := s.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
			prevWaf := existingWaf.Config
			previousWafPolicy = &prevWaf
		}
	}

	// Check if approvals are disabled for this project
	if s.projectRepo != nil {
		project, err := s.projectRepo.GetByID(domain.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project approval settings: %w", err)
		}
		if !project.ApprovalEnabled {
			// Skip approval — set route directly to pending_deploy
			route.Status = models.RouteStatusPendingDeploy
			if err := s.routeRepo.Update(route); err != nil {
				return nil, err
			}
			return route, nil
		}
	}

	configSnapshot, _ := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          &input.Config,
		SecurityPolicy:       updateSnapshotSP,
		BackendTrafficPolicy: updateSnapshotBTP,
		EnvoyExtensionPolicy: updateSnapshotEEP,
		WafPolicy:            updateSnapshotWaf,
	})

	// Build previous config snapshot
	var prevConfigSnapshot json.RawMessage
	prevConfigSnapshot, _ = json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          &previousConfig,
		SecurityPolicy:       previousSecurityPolicy,
		BackendTrafficPolicy: previousBackendTrafficPolicy,
		EnvoyExtensionPolicy: previousEnvoyExtensionPolicy,
		WafPolicy:            previousWafPolicy,
	})

	approval := &models.Approval{
		ProjectID:         domain.ProjectID,
		EntityType:        models.ApprovalEntityRoute,
		EntityID:          route.ID,
		Action:            models.ApprovalActionUpdate,
		ConfigSnapshot:    configSnapshot,
		PreviousConfig:    prevConfigSnapshot,
		SubmittedBy:       submittedBy,
		Status:            models.ApprovalStatusPending,
		ChangeDescription: input.ChangeDescription,
		AIReview:          input.AIReview,
		Stages:            s.buildRouteApprovalStages(domain.ProjectID, submittedBy, "update"),
	}

	if err := s.approvalRepo.Create(approval); err != nil {
		return nil, err
	}

	// Update or create SecurityPolicy if provided
	if input.SecurityPolicy != nil && s.securityPolicyRepo != nil {
		spConfig := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
			spConfig.Authorization = buildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			spConfig.APIKeyAuth = buildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			spConfig.JWT = buildJWTConfigFromInput(input.SecurityPolicy.JWT)
			spConfig.OIDC = buildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		spConfig.ExtAuth = input.SecurityPolicy.ExtAuth
		securityPolicy := &models.SecurityPolicy{
			RouteID:   route.ID,
			ProjectID: domain.ProjectID,
			Config:    spConfig,
		}
		if err := s.securityPolicyRepo.Upsert(securityPolicy); err != nil {
			return nil, fmt.Errorf("failed to update security policy: %w", err)
		}
	} else if input.SecurityPolicy == nil && s.securityPolicyRepo != nil {
		// If SecurityPolicy is explicitly nil, delete existing one
		_ = s.securityPolicyRepo.DeleteByRouteID(route.ID)
	}

	// Update or create BackendTrafficPolicy if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() && s.backendTrafficPolicyRepo != nil {
		backendTrafficPolicy := &models.BackendTrafficPolicy{
			RouteID:   &route.ID,
			ProjectID: domain.ProjectID,
			Config: models.BackendTrafficPolicyConfig{
				Compression:      input.BackendTrafficPolicy.Compression,
				Retry:            input.BackendTrafficPolicy.Retry,
				LoadBalancer:     input.BackendTrafficPolicy.LoadBalancer,
				CircuitBreaker:   input.BackendTrafficPolicy.CircuitBreaker,
				HealthCheck:      input.BackendTrafficPolicy.HealthCheck,
				FaultInjection:   input.BackendTrafficPolicy.FaultInjection,
				RateLimit:        input.BackendTrafficPolicy.RateLimit,
				RequestBuffer:    input.BackendTrafficPolicy.RequestBuffer,
				ResponseOverride: input.BackendTrafficPolicy.ResponseOverride,
				Timeout:          input.BackendTrafficPolicy.Timeout,
			},
		}
		if err := s.backendTrafficPolicyRepo.Upsert(backendTrafficPolicy); err != nil {
			return nil, fmt.Errorf("failed to update backend traffic policy: %w", err)
		}
	} else if (input.BackendTrafficPolicy == nil || !input.BackendTrafficPolicy.HasContent()) && s.backendTrafficPolicyRepo != nil {
		// If BackendTrafficPolicy is explicitly nil or has no content, delete existing one
		_ = s.backendTrafficPolicyRepo.DeleteByRouteID(route.ID)
	}

	// Update or create EnvoyExtensionPolicy if provided
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() && s.envoyExtensionPolicyRepo != nil {
		extensionPolicy := &models.EnvoyExtensionPolicy{
			RouteID:   &route.ID,
			ProjectID: domain.ProjectID,
			Config: models.EnvoyExtensionPolicyConfig{
				Lua:     input.ExtensionPolicy.Lua,
				Wasm:    input.ExtensionPolicy.Wasm,
				ExtProc: input.ExtensionPolicy.ExtProc,
			},
		}
		if err := s.envoyExtensionPolicyRepo.Upsert(extensionPolicy); err != nil {
			return nil, fmt.Errorf("failed to update envoy extension policy: %w", err)
		}
	} else if (input.ExtensionPolicy == nil || !input.ExtensionPolicy.HasContent()) && s.envoyExtensionPolicyRepo != nil {
		// If ExtensionPolicy is explicitly nil or has no content, delete existing one
		_ = s.envoyExtensionPolicyRepo.DeleteByRouteID(route.ID)
	}

	// Update WAF policy if provided
	if input.WafPolicy != nil && s.wafPolicyRepo != nil {
		wafConfig := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafConfig.Validate(); err != nil {
			return nil, fmt.Errorf("invalid WAF policy config: %w", err)
		}

		wafPolicy := &models.WafPolicy{
			RouteID:   route.ID,
			ProjectID: domain.ProjectID,
			Config:    wafConfig,
		}
		if err := s.wafPolicyRepo.Upsert(wafPolicy); err != nil {
			return nil, fmt.Errorf("failed to update WAF policy: %w", err)
		}
	}

	route.PendingApproval = approval
	return route, nil
}

// Delete requests deletion of a route (submits for approval)
func (s *RouteService) Delete(id uuid.UUID, submittedBy uuid.UUID) (*models.Route, error) {
	route, err := s.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Check if there's already a pending approval
	existing, err := s.approvalRepo.GetPendingByEntityID(models.ApprovalEntityRoute, id)
	if err == nil && existing != nil {
		return nil, errors.New("there is already a pending approval for this route")
	}

	// Get domain for project ID
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, err
	}

	// Update route status
	route.Status = models.RouteStatusPendingDelete

	if err := s.routeRepo.Update(route); err != nil {
		return nil, err
	}

	// Capture current policy configs for the previous config snapshot
	var deletePrevSP *models.SecurityPolicyConfig
	if s.securityPolicyRepo != nil {
		if existingSP, err := s.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
			spConfig := existingSP.Config
			deletePrevSP = &spConfig
		}
	}

	var deletePrevBTP *models.BackendTrafficPolicyConfig
	if s.backendTrafficPolicyRepo != nil {
		if existingBTP, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
			btpConfig := existingBTP.Config
			deletePrevBTP = &btpConfig
		}
	}

	var deletePrevEEP *models.EnvoyExtensionPolicyConfig
	if s.envoyExtensionPolicyRepo != nil {
		if existingEEP, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
			eepConfig := existingEEP.Config
			deletePrevEEP = &eepConfig
		}
	}

	var deletePrevWaf *models.WafPolicyConfig
	if s.wafPolicyRepo != nil {
		if existingWaf, err := s.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
			wafConfig := existingWaf.Config
			deletePrevWaf = &wafConfig
		}
	}

	// Check if approvals are disabled for this project
	if s.projectRepo != nil {
		project, err := s.projectRepo.GetByID(domain.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project approval settings: %w", err)
		}
		if !project.ApprovalEnabled {
			// Skip approval — set route directly to pending_deploy
			route.Status = models.RouteStatusPendingDeploy
			if err := s.routeRepo.Update(route); err != nil {
				return nil, err
			}
			return route, nil
		}
	}

	// Build config snapshot (current config being deleted)
	configSnapshot, _ := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          &route.Config,
		SecurityPolicy:       deletePrevSP,
		BackendTrafficPolicy: deletePrevBTP,
		EnvoyExtensionPolicy: deletePrevEEP,
		WafPolicy:            deletePrevWaf,
	})

	approval := &models.Approval{
		ProjectID:      domain.ProjectID,
		EntityType:     models.ApprovalEntityRoute,
		EntityID:       route.ID,
		Action:         models.ApprovalActionDelete,
		ConfigSnapshot: configSnapshot,
		SubmittedBy:    submittedBy,
		Status:         models.ApprovalStatusPending,
		Stages:         s.buildRouteApprovalStages(domain.ProjectID, submittedBy, "delete"),
	}

	if err := s.approvalRepo.Create(approval); err != nil {
		return nil, err
	}

	route.PendingApproval = approval
	return route, nil
}

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

	if s.k8sService == nil {
		return nil, errors.New("kubernetes service not configured")
	}

	ctx := context.Background()

	// Safety net: ensure ReferenceGrants include this domain's namespace
	if domain.Namespace != FastGatewayNamespace {
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
			if err := s.k8sService.CreateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
				log.Printf("Failed to create GRPCRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to create GRPCRoute in Kubernetes: %w", err)
			}
		} else {
			httpRouteConfig := s.buildHTTPRouteConfig(route, domain)
			if err := s.k8sService.CreateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig); err != nil {
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

		route.Status = models.RouteStatusActive

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
			if err := s.k8sService.UpdateGRPCRoute(ctx, domain.ProjectID, grpcRouteConfig); err != nil {
				log.Printf("Failed to update GRPCRoute in Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to update GRPCRoute in Kubernetes: %w", err)
			}
		} else {
			httpRouteConfig := s.buildHTTPRouteConfig(route, domain)
			if err := s.k8sService.UpdateHTTPRoute(ctx, domain.ProjectID, httpRouteConfig); err != nil {
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

		route.Status = models.RouteStatusActive

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
			if err := s.k8sService.DeleteGRPCRoute(ctx, domain.ProjectID, domain.Namespace, route.K8sRouteName); err != nil {
				log.Printf("Failed to delete GRPCRoute from Kubernetes: %v", err)
				return nil, fmt.Errorf("failed to delete GRPCRoute from Kubernetes: %w", err)
			}
		} else {
			if err := s.k8sService.DeleteHTTPRoute(ctx, domain.ProjectID, domain.Namespace, route.K8sRouteName); err != nil {
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
		if s.clientAttachmentRepo != nil {
			attachments, listErr := s.clientAttachmentRepo.ListByRouteID(route.ID)
			if listErr != nil {
				log.Printf("Failed to list attachments for approval cleanup on route %s: %v", route.ID, listErr)
			}
			for _, att := range attachments {
				if err := s.approvalRepo.DeleteByEntityID(models.ApprovalEntityClientAttachment, att.ID); err != nil {
					log.Printf("Failed to delete approvals for attachment %s: %v", att.ID, err)
				}
			}
		}

		// Delete route from database (cascade-deletes attachments, security policies, etc.)
		if err := s.routeRepo.Delete(route.ID); err != nil {
			return nil, err
		}
		return route, nil
	}

	if err := s.routeRepo.Update(route); err != nil {
		return nil, err
	}

	// Create version snapshot after successful deploy
	if s.routeVersionService != nil {
		if err := s.routeVersionService.CreateVersion(route, approval, deployedBy); err != nil {
			log.Printf("Failed to create route version: %v", err)
			// Non-fatal: deploy succeeded, version tracking is best-effort
		}
	}

	return route, nil
}

// deploySecurityPolicy deploys SecurityPolicy to Kubernetes if configured
// This merges CORS config from the DB security policy with authorization
// computed from: (1) direct IP allowlist in security policy, and (2) client attachments
func (s *RouteService) deploySecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get SecurityPolicy from database
	var policy *models.SecurityPolicy
	if s.securityPolicyRepo != nil {
		p, err := s.securityPolicyRepo.GetByRouteID(route.ID)
		if err == nil {
			policy = p
		}
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
				authConfig = &AuthorizationPolicyConfig{
					DefaultAction: "Deny",
					Rules:         []AuthorizationRulePolicyConfig{},
				}
			}
			// Ensure default action is Deny (authConfig from IP-only clients already has this)
			authConfig.DefaultAction = "Deny"
		case models.DefaultTrafficPolicyRequireIPAllowlist:
			// Require requests to come from allowed IPs (defaultAllowedCIDRs)
			// Merge with IP-only client CIDRs so registered IP-only clients are also allowed
			var mergedRules []AuthorizationRulePolicyConfig

			// Add defaultAllowedCIDRs
			if len(route.Config.DefaultAllowedCIDRs) > 0 {
				cidrs := make([]string, 0, len(route.Config.DefaultAllowedCIDRs))
				for _, cidr := range route.Config.DefaultAllowedCIDRs {
					cidrs = append(cidrs, normalizeCIDR(cidr))
				}
				mergedRules = append(mergedRules, AuthorizationRulePolicyConfig{
					Action:      "Allow",
					ClientCIDRs: cidrs,
				})
			}

			// Merge IP-only client rules
			if authConfig != nil {
				mergedRules = append(mergedRules, authConfig.Rules...)
			}

			authConfig = &AuthorizationPolicyConfig{
				DefaultAction: "Deny",
				Rules:         mergedRules,
			}
		case models.DefaultTrafficPolicyAllowAll, "":
			// Allow all requests without client header (default behavior)
			// Keep merged auth if it exists (direct IPs + IP-only client IPs)
			// But if there are API key/JWT clients and no merged auth, create deny-all
			// to prevent unauthenticated access through the base HTTPRoute
			if hasPerClientAuth && authConfig == nil {
				authConfig = &AuthorizationPolicyConfig{
					DefaultAction: "Deny",
					Rules:         []AuthorizationRulePolicyConfig{},
				}
			}
		}
	}

	// Build base SecurityPolicy config
	config := &SecurityPolicyConfig{
		Name:      route.K8sRouteName + "-security",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: SecurityPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Add CORS from DB security policy (applies to all traffic)
	if policy != nil && policy.Config.CORS != nil {
		config.CORS = &CORSPolicyConfig{
			AllowOrigins:     policy.Config.CORS.AllowOrigins,
			AllowMethods:     policy.Config.CORS.AllowMethods,
			AllowHeaders:     policy.Config.CORS.AllowHeaders,
			ExposeHeaders:    policy.Config.CORS.ExposeHeaders,
			MaxAge:           policy.Config.CORS.MaxAge,
			AllowCredentials: policy.Config.CORS.AllowCredentials,
		}
	}

	// Add authorization (merged direct IPs + IP-only client IPs, or DefaultTrafficPolicy override)
	config.Authorization = authConfig

	// Check if there's actually anything to deploy
	if config.CORS == nil && config.Authorization == nil {
		// No security features to deploy; delete existing SecurityPolicy if any
		return s.k8sService.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, config.Name)
	}

	// Create or update SecurityPolicy in Kubernetes
	return s.k8sService.UpdateSecurityPolicy(ctx, domain.ProjectID, config)
}

// deployGeneralSecurityPolicy deploys SecurityPolicy for general mode routes
// In general mode, all security features (CORS, IP, API key, JWT, OIDC, ExtAuth) come from the DB policy
func (s *RouteService) deployGeneralSecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) error {
	config := securityPolicyConfigFromDB(route, domain, policy)
	if config == nil {
		policyName := route.K8sRouteName + "-security"
		// Also clean up ext-auth backend if it exists (legacy cleanup)
		extAuthBackendName := GenerateExtAuthBackendName(route.ID.String(), "")
		_ = s.k8sService.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extAuthBackendName)
		return s.k8sService.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, policyName)
	}

	// Note: ExtAuth uses direct K8s Service reference in SecurityPolicy, no Backend CRD needed
	// Clean up any legacy ext-auth Backend CRD that might exist
	if config.ExtAuth != nil {
		extAuthBackendName := GenerateExtAuthBackendName(route.ID.String(), "")
		_ = s.k8sService.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extAuthBackendName)
	}

	return s.k8sService.UpdateSecurityPolicy(ctx, domain.ProjectID, config)
}

// deployAPIKeyClients deploys HTTPRoutes and SecurityPolicies for API key authenticated clients
func (s *RouteService) deployAPIKeyClients(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Categorize client attachments
	_, apiKeyOnlyClients, bothClients, err := s.categorizeClientAttachments(ctx, route.ID, domain)
	if err != nil {
		return err
	}
	// Create K8s Secrets for mTLS client CAs and update CTP
	allClients := make([]ClientAuthCategory, 0, len(apiKeyOnlyClients)+len(bothClients))
	allClients = append(allClients, apiKeyOnlyClients...)
	allClients = append(allClients, bothClients...)
	if s.domainService != nil {
		hasMTLSClients := false
		for _, c := range allClients {
			if c.EnableMTLS && c.MTLSCAPem != "" {
				// Create K8s Secret for this client's CA
				secretName := fmt.Sprintf("fastgateway-client-%s-mtls-ca", c.ClientID.String()[:8])
				if err := s.k8sService.CreateOrUpdateSecret(ctx, domain.ProjectID, FastGatewayNamespace, secretName, map[string][]byte{
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

// hasAPIKeyClientAttachments checks if there are any API key client attachments for a route
func (s *RouteService) hasAPIKeyClientAttachments(routeID uuid.UUID) bool {
	if s.clientAttachmentRepo == nil {
		return false
	}

	// Get active attachments
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return false
	}

	// Get approved attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
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
func (s *RouteService) hasJWTClientAttachments(routeID uuid.UUID) bool {
	if s.clientAttachmentRepo == nil {
		return false
	}

	// Get active attachments
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return false
	}

	// Get approved attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
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

func (s *RouteService) hasMTLSClientAttachments(routeID uuid.UUID) bool {
	if s.clientAttachmentRepo == nil {
		return false
	}

	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return false
	}

	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
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

// countClientAttachments counts active and approved client attachments for a route
func (s *RouteService) countClientAttachments(routeID uuid.UUID) int {
	if s.clientAttachmentRepo == nil {
		return 0
	}

	count := 0

	// Get active attachments
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err == nil {
		count += len(activeAttachments)
	}

	// Get approved attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err == nil {
		count += len(approvedAttachments)
	}

	return count
}

// buildClientIPAuthorizationConfig builds authorization config from base-route-only clients.
// This collects IPs, headers, and methods from active/approved client attachments
// that do NOT have API key/JWT/mTLS enabled (those go to per-client routes).
func (s *RouteService) buildClientIPAuthorizationConfig(routeID uuid.UUID) *AuthorizationPolicyConfig {
	// Collect client IPs from attachments (normalize to ensure CIDR format)
	clientCIDRs := s.collectClientIPCIDRs(routeID)
	clientHeaders := s.collectClientHeaders(routeID)
	clientMethods := s.collectClientMethods(routeID)

	if len(clientCIDRs) == 0 && len(clientHeaders) == 0 && len(clientMethods) == 0 {
		return nil
	}

	rule := AuthorizationRulePolicyConfig{
		Action: "Allow",
	}

	// Normalize and deduplicate CIDRs
	if len(clientCIDRs) > 0 {
		seen := make(map[string]bool)
		uniqueCIDRs := make([]string, 0, len(clientCIDRs))
		for _, cidr := range clientCIDRs {
			normalized := normalizeCIDR(cidr)
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
			rule.Headers = append(rule.Headers, HeaderMatchPolicyConfig{Name: h.Name, Values: h.Values})
		}
	}

	// Add methods
	if len(clientMethods) > 0 {
		rule.Methods = clientMethods
	}

	return &AuthorizationPolicyConfig{
		DefaultAction: "Deny",
		Rules:         []AuthorizationRulePolicyConfig{rule},
	}
}

// collectClientHeaders collects header matches from base-route-only clients
// (header auth enabled, but NOT API key/JWT/mTLS enabled)
func (s *RouteService) collectClientHeaders(routeID uuid.UUID) []models.AuthorizationHeaderMatch {
	if s.clientAttachmentRepo == nil || s.clientHeaderRepo == nil {
		return nil
	}

	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil
	}
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
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
		clientHeaders, err := s.clientHeaderRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			continue
		}
		for _, h := range clientHeaders {
			headers = append(headers, models.AuthorizationHeaderMatch{Name: h.Name, Values: []string(h.Values)})
		}
	}
	return headers
}

// collectClientMethods collects allowed methods from base-route-only clients
// Methods are now stored on the client entity, not the attachment.
// Only includes clients that are NOT per-client route clients (API key/JWT/mTLS).
func (s *RouteService) collectClientMethods(routeID uuid.UUID) []string {
	if s.clientAttachmentRepo == nil || s.clientRepo == nil {
		return nil
	}

	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil
	}
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
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
		client, err := s.clientRepo.GetByID(attachment.ClientID)
		if err != nil || len(client.AllowedMethods) == 0 {
			continue
		}
		for _, m := range client.AllowedMethods {
			if !seen[m] {
				seen[m] = true
				methods = append(methods, m)
			}
		}
	}
	return methods
}

// collectClientIPCIDRs collects IP CIDRs from all active/approved client attachments
// with IP allowlisting enabled BUT NOT API key/JWT enabled.
// Clients with both IP and API key/JWT go to per-client routes only (AND logic).
// Used by buildClientIPAuthorizationConfig.
func (s *RouteService) collectClientIPCIDRs(routeID uuid.UUID) []string {
	if s.clientAttachmentRepo == nil || s.clientIPRepo == nil {
		return nil
	}

	// Get active attachments with IP allowlist enabled
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list active attachments for route %s: %v", routeID, err)
		return nil
	}

	// Also get approved (pending deploy) attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list approved attachments for route %s: %v", routeID, err)
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

		ips, err := s.clientIPRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			log.Printf("Failed to list IPs for client %s: %v", attachment.ClientID, err)
			continue
		}

		for _, ip := range ips {
			cidrs = append(cidrs, ip.CIDR)
		}
	}

	return cidrs
}

// buildAuthorizationFromClientAttachments builds authorization config by collecting
// IP CIDRs from all active/approved client attachments with IP allowlisting enabled
// DEPRECATED: Use buildMergedAuthorizationConfig instead which includes direct IPs
func (s *RouteService) buildAuthorizationFromClientAttachments(routeID uuid.UUID) *AuthorizationPolicyConfig {
	if s.clientAttachmentRepo == nil || s.clientIPRepo == nil {
		return nil
	}

	// Get active attachments with IP allowlist enabled
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list active attachments for route %s: %v", routeID, err)
		return nil
	}

	// Also get approved (pending deploy) attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
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

		ips, err := s.clientIPRepo.ListByClientID(attachment.ClientID)
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

	return &AuthorizationPolicyConfig{
		DefaultAction: "Deny",
		Rules: []AuthorizationRulePolicyConfig{
			{
				Action:      "Allow",
				ClientCIDRs: allCIDRs,
			},
		},
	}
}

// updateClientAttachmentStatuses updates client attachment statuses after a successful deploy
// approved → active (for new/updated attachments)
// pending_detach approved attachments → removed (handled separately via approved status first)
func (s *RouteService) updateClientAttachmentStatuses(routeID uuid.UUID) {
	if s.clientAttachmentRepo == nil {
		return
	}

	// Move approved attachments to active
	if err := s.clientAttachmentRepo.UpdateStatusByRouteID(routeID, models.AttachmentStatusApproved, models.AttachmentStatusActive); err != nil {
		log.Printf("Failed to update client attachment statuses (approved→active) for route %s: %v", routeID, err)
	}

	// Detach cleanup: OnApprovalComplete sets detached attachments directly to "removed",
	// so cleanupStaleAPIKeyRoutes correctly identifies their K8s resources as stale.
}

// deleteSecurityPolicy deletes SecurityPolicy from Kubernetes
func (s *RouteService) deleteSecurityPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Build the security policy name
	securityPolicyName := route.K8sRouteName + "-security"

	// Always delete from Kubernetes (client-mode routes create k8s SecurityPolicies
	// without a DB security_policies record, so we can't gate on DB lookup)
	if err := s.k8sService.DeleteSecurityPolicy(ctx, domain.ProjectID, domain.Namespace, securityPolicyName); err != nil {
		log.Printf("Failed to delete SecurityPolicy %s from Kubernetes: %v", securityPolicyName, err)
	}

	// Delete from database if a record exists
	if s.securityPolicyRepo != nil {
		policy, err := s.securityPolicyRepo.GetByRouteID(route.ID)
		if err == nil {
			return s.securityPolicyRepo.Delete(policy.ID)
		}
	}

	return nil
}

// deployBackendTrafficPolicy deploys BackendTrafficPolicy to Kubernetes if configured
func (s *RouteService) deployBackendTrafficPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	if s.backendTrafficPolicyRepo == nil {
		// BackendTrafficPolicy repository not configured, skip
		return nil
	}

	// Get BackendTrafficPolicy from database
	policy, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
	if err != nil {
		// No BackendTrafficPolicy configured for this route
		return nil
	}

	// Build BackendTrafficPolicy config for Kubernetes
	btpConfig := s.buildBackendTrafficPolicyConfig(route, domain, policy)
	if btpConfig == nil {
		return nil
	}

	// Create or update BackendTrafficPolicy in Kubernetes
	return s.k8sService.UpdateBackendTrafficPolicy(ctx, domain.ProjectID, btpConfig)
}

// deleteBackendTrafficPolicy deletes BackendTrafficPolicy from Kubernetes
func (s *RouteService) deleteBackendTrafficPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	if s.backendTrafficPolicyRepo == nil {
		return nil
	}

	// Check if BackendTrafficPolicy exists for this route
	policy, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
	if err != nil {
		// No BackendTrafficPolicy to delete
		return nil
	}

	// Build the backend traffic policy name
	btpName := route.K8sRouteName + "-btp"

	// Delete from Kubernetes
	if err := s.k8sService.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName); err != nil {
		return err
	}

	// Delete from database
	return s.backendTrafficPolicyRepo.Delete(policy.ID)
}

// deployEnvoyExtensionPolicy deploys EnvoyExtensionPolicy to Kubernetes
func (s *RouteService) deployEnvoyExtensionPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Get EnvoyExtensionPolicy from database (may be nil)
	var policy *models.EnvoyExtensionPolicy
	if s.envoyExtensionPolicyRepo != nil {
		policy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
	}

	// Get WafPolicy from database (may be nil)
	var wafPolicy *models.WafPolicy
	if s.wafPolicyRepo != nil {
		wafPolicy, _ = s.wafPolicyRepo.GetByRouteID(route.ID)
	}

	// Handle ext-proc Backend CRD lifecycle
	extProcBackendName := GenerateExtProcBackendName(route.ID.String())
	if policy != nil && policy.Config.ExtProc != nil {
		// Create/update ext-proc Backend CRD
		backendConfig := &ExtProcBackendConfig{
			Name:      extProcBackendName,
			Namespace: domain.Namespace,
			GatewayID: domain.ID.String(),
			RouteID:   route.ID.String(),
			Service: ExtProcBackendRefPolicyConfig{
				Name:      policy.Config.ExtProc.BackendRef.Name,
				Namespace: policy.Config.ExtProc.BackendRef.Namespace,
				Port:      policy.Config.ExtProc.BackendRef.Port,
			},
		}
		backend := BuildExtProcBackend(backendConfig)
		if backend != nil {
			if err := s.k8sService.UpdateBackendUnstructured(ctx, domain.ProjectID, backend); err != nil {
				return fmt.Errorf("failed to create/update ext-proc Backend: %w", err)
			}
		}
	} else {
		// Delete ext-proc Backend CRD if ext-proc was removed
		_ = s.k8sService.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
	}

	// Build EnvoyExtensionPolicy config for Kubernetes (merged)
	extConfig := s.buildEnvoyExtensionPolicyConfig(route, domain, policy, wafPolicy)
	if extConfig == nil {
		// No extensions to deploy - delete any existing policy if present
		eepName := route.K8sRouteName + "-eep"
		s.k8sService.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName)
		return nil
	}

	// Build the unstructured object
	extPolicy := BuildEnvoyExtensionPolicy(extConfig)
	if extPolicy == nil {
		return nil
	}

	// Create or update EnvoyExtensionPolicy in Kubernetes
	return s.k8sService.UpdateEnvoyExtensionPolicy(ctx, domain.ProjectID, extPolicy)
}

// deleteEnvoyExtensionPolicy deletes EnvoyExtensionPolicy from Kubernetes
func (s *RouteService) deleteEnvoyExtensionPolicy(ctx context.Context, route *models.Route, domain *models.Domain) error {
	// Check if EnvoyExtensionPolicy exists for this route
	var policy *models.EnvoyExtensionPolicy
	if s.envoyExtensionPolicyRepo != nil {
		p, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
		if err == nil {
			policy = p
		}
	}

	// Check if WafPolicy exists for this route
	var wafPolicy *models.WafPolicy
	if s.wafPolicyRepo != nil {
		w, err := s.wafPolicyRepo.GetByRouteID(route.ID)
		if err == nil {
			wafPolicy = w
		}
	}

	// If neither EnvoyExtensionPolicy nor WafPolicy exists, nothing to delete
	if policy == nil && wafPolicy == nil {
		return nil
	}

	// Delete ext-proc Backend CRD if it exists
	extProcBackendName := GenerateExtProcBackendName(route.ID.String())
	_ = s.k8sService.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)

	// Build the envoy extension policy name
	eepName := route.K8sRouteName + "-eep"

	// Delete from Kubernetes (the CRD contains both Lua/Wasm and WAF configurations)
	if err := s.k8sService.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName); err != nil {
		return err
	}

	// Delete EnvoyExtensionPolicy from database (WAF is deleted by CASCADE on route deletion)
	if policy != nil && s.envoyExtensionPolicyRepo != nil {
		return s.envoyExtensionPolicyRepo.Delete(policy.ID)
	}

	return nil
}

// buildExtProcPolicyConfig converts a model ExtProcExtensionConfig to the K8s policy config
func buildExtProcPolicyConfig(extProc *models.ExtProcExtensionConfig) ExtProcPolicyConfig {
	if extProc == nil {
		return ExtProcPolicyConfig{}
	}
	cfg := ExtProcPolicyConfig{
		BackendRef: ExtProcBackendRefPolicyConfig{
			Name:      extProc.BackendRef.Name,
			Namespace: extProc.BackendRef.Namespace,
			Port:      extProc.BackendRef.Port,
		},
		FailOpen: extProc.FailOpen,
	}
	if extProc.ProcessingMode != nil {
		cfg.ProcessingMode = &ExtProcProcessingModeConfig{}
		if extProc.ProcessingMode.Request != nil {
			cfg.ProcessingMode.Request = &ExtProcBodyModeConfig{Body: extProc.ProcessingMode.Request.Body}
		}
		if extProc.ProcessingMode.Response != nil {
			cfg.ProcessingMode.Response = &ExtProcBodyModeConfig{Body: extProc.ProcessingMode.Response.Body}
		}
	}
	return cfg
}

// buildEnvoyExtensionPolicyConfig builds EnvoyExtensionPolicyK8sConfig from database model
func (s *RouteService) buildEnvoyExtensionPolicyConfig(route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy) *EnvoyExtensionPolicyK8sConfig {
	// Check if we have any extensions to deploy
	hasGenericExtensions := policy != nil && !policy.Config.IsEmpty()
	hasWaf := wafPolicy != nil && !wafPolicy.Config.IsEmpty()

	if !hasGenericExtensions && !hasWaf {
		return nil
	}

	config := &EnvoyExtensionPolicyK8sConfig{
		Name:      route.K8sRouteName + "-eep",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Add Lua extension configuration (only if policy exists)
	if hasGenericExtensions && policy.Config.Lua != nil {
		luaConfig := LuaExtensionPolicyConfig{
			Type:   policy.Config.Lua.Type,
			Inline: policy.Config.Lua.Inline,
		}
		if policy.Config.Lua.ValueRef != nil {
			luaConfig.ValueRef = &ValueRefPolicyConfig{
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
		wasmConfig := WasmExtensionPolicyConfig{
			Name:   policy.Config.Wasm.Name,
			RootID: policy.Config.Wasm.RootID,
			Code: WasmCodeSourcePolicyConfig{
				Type: policy.Config.Wasm.Code.Type,
			},
			Config: policy.Config.Wasm.Config,
		}
		if policy.Config.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &WasmHTTPSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.HTTP.URL,
				SHA256: policy.Config.Wasm.Code.HTTP.SHA256,
			}
		}
		if policy.Config.Wasm.Code.Image != nil {
			imageConfig := &WasmImageSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.Image.URL,
				SHA256: policy.Config.Wasm.Code.Image.SHA256,
			}
			if policy.Config.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &ValueRefPolicyConfig{
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
		config.ExtProc = append(config.ExtProc, buildExtProcPolicyConfig(policy.Config.ExtProc))
	}

	// Add WAF (coraza) WASM entry if WAF is configured
	if hasWaf {
		corazaConfig, err := BuildCorazaDirectives(&wafPolicy.Config)
		if err == nil && corazaConfig != "" {
			wasmConfig := WasmExtensionPolicyConfig{
				Name:   "coraza-waf",
				RootID: "",
				Code: WasmCodeSourcePolicyConfig{
					Type: "Image",
					Image: &WasmImageSourcePolicyConfig{
						URL: getCorazaWasmImageURL(),
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

	hrfName := route.K8sRouteName + "-hrf"
	cmName := route.K8sRouteName + "-dr-cm"

	// Check if we need a ConfigMap (body is provided)
	if route.Config.DirectResponse.Body != nil && route.Config.DirectResponse.Body.Inline != "" {
		// Create ConfigMap for the body
		cmConfig := &DirectResponseConfigMapConfig{
			Name:        cmName,
			Namespace:   domain.Namespace,
			GatewayID:   domain.ID.String(),
			RouteID:     route.ID.String(),
			BodyContent: route.Config.DirectResponse.Body.Inline,
		}
		if err := s.k8sService.ApplyDirectResponseConfigMap(ctx, domain.ProjectID, cmConfig); err != nil {
			return fmt.Errorf("failed to apply ConfigMap: %w", err)
		}
	}

	// Create HTTPRouteFilter
	hrfConfig := &HTTPRouteFilterConfig{
		Name:      hrfName,
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		DirectResponse: &DirectResponseFilterConfig{
			StatusCode:  route.Config.DirectResponse.StatusCode,
			ContentType: route.Config.DirectResponse.ContentType,
		},
	}

	// Set body configuration
	if route.Config.DirectResponse.Body != nil && route.Config.DirectResponse.Body.Inline != "" {
		// Use ValueRef to reference ConfigMap
		hrfConfig.DirectResponse.Body = &DirectResponseBodyFilterConfig{
			Type: "ValueRef",
			ValueRef: &DirectResponseValueRef{
				Group: "",
				Kind:  "ConfigMap",
				Name:  cmName,
			},
		}
	}

	if err := s.k8sService.ApplyHTTPRouteFilter(ctx, domain.ProjectID, hrfConfig); err != nil {
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

	hrfName := route.K8sRouteName + "-hrf"
	cmName := route.K8sRouteName + "-dr-cm"

	// Delete HTTPRouteFilter
	if err := s.k8sService.DeleteHTTPRouteFilter(ctx, domain.ProjectID, domain.Namespace, hrfName); err != nil {
		log.Printf("Warning: failed to delete HTTPRouteFilter %s: %v", hrfName, err)
	}

	// Delete ConfigMap
	if err := s.k8sService.DeleteDirectResponseConfigMap(ctx, domain.ProjectID, domain.Namespace, cmName); err != nil {
		log.Printf("Warning: failed to delete ConfigMap %s: %v", cmName, err)
	}

	return nil
}

// buildBackendTrafficPolicyConfig builds BackendTrafficPolicyConfig from route, domain and policy
func (s *RouteService) buildBackendTrafficPolicyConfig(route *models.Route, domain *models.Domain, policy *models.BackendTrafficPolicy) *BackendTrafficPolicyConfig {
	if policy == nil {
		return nil
	}

	// Check if any feature is configured
	if policy.Config.IsEmpty() {
		return nil
	}

	config := &BackendTrafficPolicyConfig{
		Name:      route.K8sRouteName + "-btp",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: BackendTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Add compression configuration
	if len(policy.Config.Compression) > 0 {
		config.Compression = make([]CompressionPolicyConfig, 0, len(policy.Config.Compression))
		for _, comp := range policy.Config.Compression {
			policyComp := CompressionPolicyConfig{
				Type: string(comp.Type),
			}
			switch comp.Type {
			case models.CompressionTypeGzip:
				policyComp.Gzip = &GzipPolicyConfig{}
			case models.CompressionTypeBrotli:
				policyComp.Brotli = &BrotliPolicyConfig{}
			case models.CompressionTypeZstd:
				policyComp.Zstd = &ZstdPolicyConfig{}
			}
			config.Compression = append(config.Compression, policyComp)
		}
	}

	// Add retry configuration
	if policy.Config.Retry != nil {
		config.Retry = mapRetryConfigToPolicy(policy.Config.Retry)
	}

	// Add load balancer configuration
	if policy.Config.LoadBalancer != nil {
		config.LoadBalancer = mapLoadBalancerConfigToPolicy(policy.Config.LoadBalancer)
	}

	// Add circuit breaker configuration
	if policy.Config.CircuitBreaker != nil {
		config.CircuitBreaker = mapCircuitBreakerConfigToPolicy(policy.Config.CircuitBreaker)
	}

	// Add health check configuration
	if policy.Config.HealthCheck != nil {
		config.HealthCheck = mapHealthCheckConfigToPolicy(policy.Config.HealthCheck)
	}

	// Add fault injection configuration
	if policy.Config.FaultInjection != nil {
		config.FaultInjection = mapFaultInjectionConfigToPolicy(policy.Config.FaultInjection)
	}

	// Add rate limit configuration
	if policy.Config.RateLimit != nil {
		config.RateLimit = mapRateLimitConfigToPolicy(policy.Config.RateLimit)
	}

	// Add request buffer configuration
	if policy.Config.RequestBuffer != nil {
		config.RequestBuffer = &RequestBufferPolicyConfig{
			Limit: policy.Config.RequestBuffer.Limit,
		}
	}

	// Add response override configuration
	if len(policy.Config.ResponseOverride) > 0 {
		config.ResponseOverride = mapResponseOverrideToPolicy(policy.Config.ResponseOverride)
	}

	// Add timeout configuration
	if policy.Config.Timeout != nil {
		config.Timeout = mapTimeoutConfigToPolicy(policy.Config.Timeout)
	}

	return config
}

// mapRateLimitConfigToPolicy converts model RateLimitConfig to k8s-side RateLimitPolicyConfig
func mapRateLimitConfigToPolicy(rl *models.RateLimitConfig) *RateLimitPolicyConfig {
	if rl == nil || rl.Global == nil {
		return nil
	}

	rules := make([]RateLimitRulePolicyConfig, 0, len(rl.Global.Rules))
	for _, rule := range rl.Global.Rules {
		policyRule := RateLimitRulePolicyConfig{
			Limit: RateLimitValuePolicyConfig{
				Requests: rule.Limit.Requests,
				Unit:     rule.Limit.Unit,
			},
		}
		if len(rule.ClientSelectors) > 0 {
			selectors := make([]RateLimitSelectorPolicyConfig, 0, len(rule.ClientSelectors))
			for _, sel := range rule.ClientSelectors {
				policySel := RateLimitSelectorPolicyConfig{}
				if len(sel.Headers) > 0 {
					headers := make([]RateLimitHeaderMatchPolicyConfig, 0, len(sel.Headers))
					for _, h := range sel.Headers {
						headers = append(headers, RateLimitHeaderMatchPolicyConfig{
							Name:   h.Name,
							Value:  h.Value,
							Type:   h.Type,
							Invert: h.Invert,
						})
					}
					policySel.Headers = headers
				}
				if sel.SourceCIDR != nil {
					policySel.SourceCIDR = &RateLimitSourceCIDRPolicyConfig{
						Value: sel.SourceCIDR.Value,
						Type:  sel.SourceCIDR.Type,
					}
				}
				if sel.Path != nil {
					policySel.Path = &RateLimitPathMatchPolicyConfig{
						Value: sel.Path.Value,
						Type:  sel.Path.Type,
					}
				}
				if len(sel.Methods) > 0 {
					policySel.Methods = sel.Methods
				}
				selectors = append(selectors, policySel)
			}
			policyRule.ClientSelectors = selectors
		}
		rules = append(rules, policyRule)
	}

	return &RateLimitPolicyConfig{
		Global: &GlobalRateLimitPolicyConfig{
			Rules: rules,
		},
	}
}

// mapCircuitBreakerConfigToPolicy converts model CircuitBreakerConfig to k8s-side CircuitBreakerPolicyConfig
func mapCircuitBreakerConfigToPolicy(cb *models.CircuitBreakerConfig) *CircuitBreakerPolicyConfig {
	if cb == nil {
		return nil
	}
	return &CircuitBreakerPolicyConfig{
		MaxConnections:           cb.MaxConnections,
		MaxPendingRequests:       cb.MaxPendingRequests,
		MaxParallelRequests:      cb.MaxParallelRequests,
		MaxParallelRetries:       cb.MaxParallelRetries,
		MaxRequestsPerConnection: cb.MaxRequestsPerConnection,
	}
}

// mapHealthCheckConfigToPolicy converts model HealthCheckConfig to k8s-side HealthCheckPolicyConfig
func mapHealthCheckConfigToPolicy(hc *models.HealthCheckConfig) *HealthCheckPolicyConfig {
	if hc == nil {
		return nil
	}
	result := &HealthCheckPolicyConfig{
		PanicThreshold: hc.PanicThreshold,
	}
	if hc.Active != nil {
		result.Active = &ActiveHealthCheckPolicyConfig{
			Timeout:            hc.Active.Timeout,
			Interval:           hc.Active.Interval,
			UnhealthyThreshold: hc.Active.UnhealthyThreshold,
			HealthyThreshold:   hc.Active.HealthyThreshold,
			Type:               hc.Active.Type,
		}
		// Always create the type-specific config based on Type, as the CRD requires it
		switch hc.Active.Type {
		case "HTTP":
			if hc.Active.HTTP != nil {
				result.Active.HTTP = &HTTPActiveHealthCheckPolicyConfig{
					Path:             hc.Active.HTTP.Path,
					Method:           hc.Active.HTTP.Method,
					ExpectedStatuses: hc.Active.HTTP.ExpectedStatuses,
				}
			} else {
				result.Active.HTTP = &HTTPActiveHealthCheckPolicyConfig{}
			}
		case "TCP":
			result.Active.TCP = &TCPActiveHealthCheckPolicyConfig{}
			if hc.Active.TCP != nil {
				if hc.Active.TCP.Send != nil && hc.Active.TCP.Send.Text != nil {
					result.Active.TCP.SendText = hc.Active.TCP.Send.Text
				}
				if hc.Active.TCP.Receive != nil && hc.Active.TCP.Receive.Text != nil {
					result.Active.TCP.ReceiveText = hc.Active.TCP.Receive.Text
				}
			}
		case "GRPC":
			if hc.Active.GRPC != nil {
				result.Active.GRPC = &GRPCActiveHealthCheckPolicyConfig{
					Service: hc.Active.GRPC.Service,
				}
			} else {
				result.Active.GRPC = &GRPCActiveHealthCheckPolicyConfig{}
			}
		}
	}
	if hc.Passive != nil {
		result.Passive = &PassiveHealthCheckPolicyConfig{
			ConsecutiveGatewayErrors:       hc.Passive.ConsecutiveGatewayErrors,
			Consecutive5xxErrors:           hc.Passive.Consecutive5xxErrors,
			ConsecutiveLocalOriginFailures: hc.Passive.ConsecutiveLocalOriginFailures,
			Interval:                       hc.Passive.Interval,
			BaseEjectionTime:               hc.Passive.BaseEjectionTime,
			MaxEjectionPercent:             hc.Passive.MaxEjectionPercent,
			SplitExternalLocalOriginErrors: hc.Passive.SplitExternalLocalOriginErrors,
		}
	}
	return result
}

// mapFaultInjectionConfigToPolicy converts model FaultInjectionConfig to k8s-side FaultInjectionPolicyConfig
func mapFaultInjectionConfigToPolicy(fi *models.FaultInjectionConfig) *FaultInjectionPolicyConfig {
	if fi == nil {
		return nil
	}
	result := &FaultInjectionPolicyConfig{}
	if fi.Delay != nil {
		result.Delay = &FaultInjectionDelayPolicyConfig{
			FixedDelay: fi.Delay.FixedDelay,
			Percentage: fi.Delay.Percentage,
		}
	}
	if fi.Abort != nil {
		result.Abort = &FaultInjectionAbortPolicyConfig{
			HTTPStatus: fi.Abort.HTTPStatus,
			GRPCStatus: fi.Abort.GRPCStatus,
			Percentage: fi.Abort.Percentage,
		}
	}
	return result
}

// mapLoadBalancerConfigToPolicy converts model LoadBalancerConfig to k8s-side LoadBalancerPolicyConfig
func mapLoadBalancerConfigToPolicy(lb *models.LoadBalancerConfig) *LoadBalancerPolicyConfig {
	if lb == nil {
		return nil
	}

	result := &LoadBalancerPolicyConfig{
		Type: string(lb.Type),
	}

	if lb.ConsistentHash != nil {
		result.ConsistentHash = &ConsistentHashPolicyConfig{
			Type: string(lb.ConsistentHash.Type),
		}
		if lb.ConsistentHash.Header != nil {
			result.ConsistentHash.Header = &ConsistentHashHeaderPolicyConfig{
				Name: lb.ConsistentHash.Header.Name,
			}
		}
		if lb.ConsistentHash.Cookie != nil {
			result.ConsistentHash.Cookie = &ConsistentHashCookiePolicyConfig{
				Name:       lb.ConsistentHash.Cookie.Name,
				TTL:        lb.ConsistentHash.Cookie.TTL,
				Attributes: lb.ConsistentHash.Cookie.Attributes,
			}
		}
	}

	return result
}

// mapRetryConfigToPolicy converts model RetryConfig to k8s-side RetryPolicyConfig
func mapRetryConfigToPolicy(retry *models.RetryConfig) *RetryPolicyConfig {
	if retry == nil {
		return nil
	}

	result := &RetryPolicyConfig{
		NumRetries: retry.NumRetries,
	}

	if retry.RetryOn != nil {
		result.RetryOn = &RetryOnPolicyConfig{
			HTTPStatusCodes: retry.RetryOn.HTTPStatusCodes,
			Triggers:        retry.RetryOn.Triggers,
		}
	}

	if retry.PerRetryPolicy != nil {
		result.PerRetry = &PerRetryPolicyConfig{
			Timeout: retry.PerRetryPolicy.Timeout,
		}
		if retry.PerRetryPolicy.BackOff != nil {
			result.PerRetry.BackOff = &BackOffPolicyConfig{
				BaseInterval: retry.PerRetryPolicy.BackOff.BaseInterval,
				MaxInterval:  retry.PerRetryPolicy.BackOff.MaxInterval,
			}
		}
	}

	return result
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

			backendConfig := &BackendConfig{
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
				backendConfig.TLS = &BackendTLSPolicyConfig{
					InsecureSkipVerify: backend.TLS.InsecureSkipVerify,
					SNI:                backend.TLS.SNI,
				}

				// Map CA certificate refs (only when not insecureSkipVerify)
				if !backend.TLS.InsecureSkipVerify && len(backend.TLS.CACertificateRefs) > 0 {
					backendConfig.TLS.CACertificateRefs = make([]BackendCertificateRefConfig, len(backend.TLS.CACertificateRefs))
					for j, ref := range backend.TLS.CACertificateRefs {
						backendConfig.TLS.CACertificateRefs[j] = BackendCertificateRefConfig{
							Kind:      ref.Kind,
							Name:      ref.Name,
							Namespace: ref.Namespace,
						}
					}
				}

				// Map client certificate ref for mTLS
				if backend.TLS.ClientCertificateRef != nil {
					backendConfig.TLS.ClientCertificateRef = &BackendSecretRefConfig{
						Name:      backend.TLS.ClientCertificateRef.Name,
						Namespace: backend.TLS.ClientCertificateRef.Namespace,
					}
				}
			}

			if err := s.k8sService.UpdateBackend(ctx, domain.ProjectID, backendConfig); err != nil {
				return fmt.Errorf("failed to create/update Backend CRD for %s: %w", backendName, err)
			}
		}
	}
	return nil
}

// deleteBackends deletes all Backend CRDs associated with a route
func (s *RouteService) deleteBackends(ctx context.Context, route *models.Route, domain *models.Domain) error {
	return s.k8sService.DeleteBackendsByRoute(ctx, domain.ProjectID, domain.Namespace, route.ID.String())
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
	return s.k8sService.DeleteStaleBackendsByRoute(ctx, domain.ProjectID, domain.Namespace, route.ID.String(), expectedNames)
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

// securityPolicyConfigFromDB converts DB security policy model to SecurityPolicyConfig for K8s CRD building.
// Shared by buildSecurityPolicyConfig, deployGeneralSecurityPolicy, and generateSecurityPolicyYAMLFromDB.
func securityPolicyConfigFromDB(route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) *SecurityPolicyConfig {
	if policy == nil || !policy.Config.HasAnyConfig() {
		return nil
	}

	config := &SecurityPolicyConfig{
		Name:      route.K8sRouteName + "-security",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: SecurityPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// CORS
	if policy.Config.CORS != nil {
		config.CORS = &CORSPolicyConfig{
			AllowOrigins:     policy.Config.CORS.AllowOrigins,
			AllowMethods:     policy.Config.CORS.AllowMethods,
			AllowHeaders:     policy.Config.CORS.AllowHeaders,
			ExposeHeaders:    policy.Config.CORS.ExposeHeaders,
			MaxAge:           policy.Config.CORS.MaxAge,
			AllowCredentials: policy.Config.CORS.AllowCredentials,
		}
	}

	// Authorization (IP allowlisting, headers, methods)
	if policy.Config.Authorization != nil {
		authRules := make([]AuthorizationRulePolicyConfig, 0, len(policy.Config.Authorization.Rules))
		for _, rule := range policy.Config.Authorization.Rules {
			policyRule := AuthorizationRulePolicyConfig{
				Action:      rule.Action,
				ClientCIDRs: rule.Principal.ClientCIDRs,
			}
			if len(rule.Principal.Headers) > 0 {
				for _, h := range rule.Principal.Headers {
					policyRule.Headers = append(policyRule.Headers, HeaderMatchPolicyConfig{
						Name:   h.Name,
						Values: h.Values,
					})
				}
			}
			if rule.Operation != nil && len(rule.Operation.Methods) > 0 {
				policyRule.Methods = rule.Operation.Methods
			}
			authRules = append(authRules, policyRule)
		}
		config.Authorization = &AuthorizationPolicyConfig{
			DefaultAction: policy.Config.Authorization.DefaultAction,
			Rules:         authRules,
		}
	}

	// API Key Auth
	if policy.Config.APIKeyAuth != nil {
		credRefs := make([]SecretRefConfig, 0, len(policy.Config.APIKeyAuth.CredentialRefs))
		for _, ref := range policy.Config.APIKeyAuth.CredentialRefs {
			credRefs = append(credRefs, SecretRefConfig{Name: ref.Name, Namespace: ref.Namespace})
		}
		extractFrom := make([]APIKeyExtractFromConfig, 0, len(policy.Config.APIKeyAuth.ExtractFrom))
		for _, ef := range policy.Config.APIKeyAuth.ExtractFrom {
			extractFrom = append(extractFrom, APIKeyExtractFromConfig{Headers: ef.Headers})
		}
		config.APIKeyAuth = &APIKeyAuthPolicyConfig{CredentialRefs: credRefs, ExtractFrom: extractFrom}
	}

	// JWT
	if policy.Config.JWT != nil {
		providers := make([]JWTProviderPolicyConfig, 0, len(policy.Config.JWT.Providers))
		for _, p := range policy.Config.JWT.Providers {
			provider := JWTProviderPolicyConfig{Name: p.Name, Issuer: p.Issuer, Audiences: p.Audiences}
			if p.RemoteJWKS != nil {
				provider.JWKSURL = p.RemoteJWKS.URI
			}
			for _, cth := range p.ClaimToHeaders {
				provider.ClaimToHeaders = append(provider.ClaimToHeaders, JWTClaimToHeaderPolicyConfig{Claim: cth.Claim, Header: cth.Header})
			}
			providers = append(providers, provider)
		}
		config.JWT = &JWTAuthPolicyConfig{Providers: providers}
	}

	// OIDC
	if policy.Config.OIDC != nil {
		config.OIDC = &OIDCPolicyConfig{
			ClientID:     policy.Config.OIDC.ClientID,
			RedirectURL:  policy.Config.OIDC.RedirectURL,
			LogoutPath:   policy.Config.OIDC.LogoutPath,
			Scopes:       policy.Config.OIDC.Scopes,
			CookieDomain: policy.Config.OIDC.CookieDomain,
		}
		if policy.Config.OIDC.Provider != nil {
			config.OIDC.Issuer = policy.Config.OIDC.Provider.Issuer
		}
		if policy.Config.OIDC.ClientSecret != nil {
			config.OIDC.ClientSecretName = policy.Config.OIDC.ClientSecret.Name
			config.OIDC.ClientSecretNS = policy.Config.OIDC.ClientSecret.Namespace
		}
		// Fallback to FastGatewayNamespace if clientSecret namespace is empty
		if config.OIDC.ClientSecretNS == "" {
			config.OIDC.ClientSecretNS = FastGatewayNamespace
		}
	}

	// ExtAuth
	if policy.Config.ExtAuth != nil {
		config.ExtAuth = policy.Config.ExtAuth
		// ExtAuthBackendName will be set by the deploy function
	}

	return config
}

// buildSecurityPolicyConfig builds SecurityPolicyConfig from route, domain and security policy
// Note: This builds from DB only (CORS + stored authorization). For deploy, use deploySecurityPolicy()
// which also computes authorization from active client attachments.
func (s *RouteService) buildSecurityPolicyConfig(route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) *SecurityPolicyConfig {
	return securityPolicyConfigFromDB(route, domain, policy)
}

// EffectiveIPEntry represents a single IP CIDR in the effective IP allowlist
type EffectiveIPEntry struct {
	CIDR        string `json:"cidr"`
	ClientID    string `json:"clientId"`
	ClientName  string `json:"clientName"`
	Description string `json:"description,omitempty"`
}

// GetEffectiveIPAllowlist returns the merged IP allowlist for a route from active client attachments
func (s *RouteService) GetEffectiveIPAllowlist(routeID uuid.UUID) ([]EffectiveIPEntry, error) {
	if s.clientAttachmentRepo == nil || s.clientIPRepo == nil {
		return []EffectiveIPEntry{}, nil
	}

	// Get active attachments with IP allowlist enabled
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active attachments: %w", err)
	}

	// Also include approved (pending deploy) attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
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

		ips, err := s.clientIPRepo.ListByClientID(attachment.ClientID)
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

// buildAuthorizationConfigFromInput converts AuthorizationInput to models.AuthorizationConfig
func buildAuthorizationConfigFromInput(input *AuthorizationInput) *models.AuthorizationConfig {
	if input == nil {
		return nil
	}

	hasCIDRs := len(input.AllowedCIDRs) > 0
	hasHeaders := len(input.Headers) > 0
	hasMethods := len(input.Methods) > 0
	if !hasCIDRs && !hasHeaders && !hasMethods {
		return nil
	}

	rule := models.AuthorizationRule{
		Action: "Allow",
	}

	if hasCIDRs {
		cidrs := make([]string, 0, len(input.AllowedCIDRs))
		for _, cidr := range input.AllowedCIDRs {
			cidrs = append(cidrs, normalizeCIDR(cidr))
		}
		rule.Principal.ClientCIDRs = cidrs
	}

	if hasHeaders {
		rule.Principal.Headers = input.Headers
	}

	if hasMethods {
		methods := make([]string, 0, len(input.Methods))
		for _, m := range input.Methods {
			methods = append(methods, strings.ToUpper(m))
		}
		rule.Operation = &models.AuthorizationOperation{Methods: methods}
	}

	return &models.AuthorizationConfig{
		DefaultAction: "Deny",
		Rules:         []models.AuthorizationRule{rule},
	}
}

// buildAPIKeyAuthConfigFromInput converts APIKeyAuthInput to models.APIKeyAuthConfig
func buildAPIKeyAuthConfigFromInput(input *APIKeyAuthInput) *models.APIKeyAuthConfig {
	if input == nil {
		return nil
	}
	return &models.APIKeyAuthConfig{
		CredentialRefs: []models.SecretRef{{
			Name:      input.SecretName,
			Namespace: FastGatewayNamespace,
		}},
		ExtractFrom: []models.APIKeyExtractFrom{{
			Headers: []string{input.HeaderName},
		}},
	}
}

// buildJWTConfigFromInput converts JWTInput to models.JWTConfig
func buildJWTConfigFromInput(input *JWTInput) *models.JWTConfig {
	if input == nil {
		return nil
	}
	return &models.JWTConfig{
		Providers: []models.JWTProvider{{
			Name:   "route-jwt",
			Issuer: input.Issuer,
			RemoteJWKS: &models.RemoteJWKS{
				URI: input.JWKSURL,
			},
			Audiences:      input.Audiences,
			ClaimToHeaders: input.ClaimToHeaders,
		}},
	}
}

// buildOIDCConfigFromInput converts OIDCInput to models.OIDCConfig
func buildOIDCConfigFromInput(input *OIDCInput) *models.OIDCConfig {
	if input == nil {
		return nil
	}
	return &models.OIDCConfig{
		Provider: &models.OIDCProvider{
			Issuer: input.Issuer,
		},
		ClientID: input.ClientID,
		ClientSecret: &models.SecretRef{
			Name:      input.ClientSecretName,
			Namespace: FastGatewayNamespace,
		},
		RedirectURL:  input.RedirectURL,
		LogoutPath:   input.LogoutPath,
		Scopes:       input.Scopes,
		CookieDomain: input.CookieDomain,
	}
}

// convertPathTypeToGatewayAPI converts frontend path types to Gateway API path types
func convertPathTypeToGatewayAPI(pathType string) string {
	switch pathType {
	case "Prefix":
		return "PathPrefix"
	case "Exact":
		return "Exact"
	case "RegularExpression":
		return "RegularExpression"
	default:
		return "PathPrefix" // Default to PathPrefix
	}
}

// buildHTTPRouteConfig builds HTTPRouteConfig from route and domain
func (s *RouteService) buildHTTPRouteConfig(route *models.Route, domain *models.Domain) *HTTPRouteConfig {
	rules := make([]HTTPRouteRule, 0, len(route.Config.Matches))

	for _, match := range route.Config.Matches {
		rule := HTTPRouteRule{
			BackendRefs: make([]BackendRef, 0, len(route.Config.Backends)),
		}

		// Path matching
		if match.Path != nil {
			rule.PathType = convertPathTypeToGatewayAPI(string(match.Path.Type))
			rule.PathValue = match.Path.Value
		}

		// Header matching
		if len(match.Headers) > 0 {
			rule.Headers = make([]HeaderMatch, 0, len(match.Headers))
			for _, h := range match.Headers {
				rule.Headers = append(rule.Headers, HeaderMatch{
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
			rule.QueryParams = make([]QueryParamMatch, 0, len(match.QueryParams))
			for _, qp := range match.QueryParams {
				rule.QueryParams = append(rule.QueryParams, QueryParamMatch{
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
					rule.BackendRefs = append(rule.BackendRefs, BackendRef{
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
					rule.BackendRefs = append(rule.BackendRefs, BackendRef{
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

	config := &HTTPRouteConfig{
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
		config.HTTPRouteFilterName = route.K8sRouteName + "-hrf"
	}

	// Add mirror refs (only for backend routes, not redirect or direct response)
	if route.Config.Redirect == nil && route.Config.DirectResponse == nil && len(route.Config.Mirrors) > 0 {
		config.Mirrors = make([]MirrorRef, 0, len(route.Config.Mirrors))
		for _, mirror := range route.Config.Mirrors {
			config.Mirrors = append(config.Mirrors, MirrorRef{
				Name:      mirror.Service,
				Namespace: mirror.Namespace,
				Port:      mirror.Port,
			})
		}
	}

	// Note: CORS is now handled via SecurityPolicy (separate from HTTPRoute)

	return config
}

// buildGRPCRouteConfig builds GRPCRouteConfig from route and domain
func (s *RouteService) buildGRPCRouteConfig(route *models.Route, domain *models.Domain) *GRPCRouteConfig {
	rules := make([]GRPCRouteRule, 0)

	for _, match := range route.Config.Matches {
		rule := GRPCRouteRule{}

		// Convert gRPC service/method matches
		if match.GRPCService != nil {
			rule.GRPCService = &GRPCMethodMatchConfig{
				Type:  match.GRPCService.Type,
				Value: match.GRPCService.Value,
			}
		}
		if match.GRPCMethod != nil {
			rule.GRPCMethod = &GRPCMethodMatchConfig{
				Type:  match.GRPCMethod.Type,
				Value: match.GRPCMethod.Value,
			}
		}

		// Convert header matches
		if len(match.Headers) > 0 {
			rule.Headers = make([]HeaderMatch, 0, len(match.Headers))
			for _, h := range match.Headers {
				rule.Headers = append(rule.Headers, HeaderMatch{
					Name:  h.Name,
					Type:  h.Type,
					Value: h.Value,
				})
			}
		}

		// Convert backend refs
		hasFailover := route.Config.HasFailover()
		for i, backend := range route.Config.Backends {
			if backend.Type == models.BackendTypeExternal || hasFailover || backend.TLS != nil {
				backendName := fmt.Sprintf("%s-backend-%d", route.K8sRouteName, i)
				rule.BackendRefs = append(rule.BackendRefs, BackendRef{
					Name:       backendName,
					Namespace:  domain.Namespace,
					Port:       backend.Port,
					Weight:     backend.Weight,
					IsExternal: true,
					Group:      "gateway.envoyproxy.io",
					Kind:       "Backend",
				})
			} else {
				rule.BackendRefs = append(rule.BackendRefs, BackendRef{
					Name:      backend.Service,
					Namespace: backend.Namespace,
					Port:      backend.Port,
					Weight:    backend.Weight,
				})
			}
		}

		rules = append(rules, rule)
	}

	// If no matches defined, add a single rule with just backends (match all)
	if len(rules) == 0 && len(route.Config.Backends) > 0 {
		rule := GRPCRouteRule{}
		hasFailover := route.Config.HasFailover()
		for i, backend := range route.Config.Backends {
			if backend.Type == models.BackendTypeExternal || hasFailover || backend.TLS != nil {
				backendName := fmt.Sprintf("%s-backend-%d", route.K8sRouteName, i)
				rule.BackendRefs = append(rule.BackendRefs, BackendRef{
					Name:       backendName,
					Namespace:  domain.Namespace,
					Port:       backend.Port,
					Weight:     backend.Weight,
					IsExternal: true,
					Group:      "gateway.envoyproxy.io",
					Kind:       "Backend",
				})
			} else {
				rule.BackendRefs = append(rule.BackendRefs, BackendRef{
					Name:      backend.Service,
					Namespace: backend.Namespace,
					Port:      backend.Port,
					Weight:    backend.Weight,
				})
			}
		}
		rules = append(rules, rule)
	}

	config := &GRPCRouteConfig{
		Name:        route.K8sRouteName,
		Namespace:   domain.Namespace,
		GatewayName: domain.K8sGatewayName,
		GatewayID:   domain.ID.String(),
		RouteID:     route.ID.String(),
		Hostname:    domain.Hostname,
		Rules:       rules,
	}

	// Add header modifiers
	if route.Config.RequestHeaderModifier != nil {
		config.RequestHeaderModifier = convertHeaderModifier(route.Config.RequestHeaderModifier)
	}
	if route.Config.ResponseHeaderModifier != nil {
		config.ResponseHeaderModifier = convertHeaderModifier(route.Config.ResponseHeaderModifier)
	}

	// Add mirrors
	if len(route.Config.Mirrors) > 0 {
		config.Mirrors = make([]MirrorRef, 0, len(route.Config.Mirrors))
		for _, m := range route.Config.Mirrors {
			config.Mirrors = append(config.Mirrors, MirrorRef{
				Name:      m.Service,
				Namespace: m.Namespace,
				Port:      m.Port,
			})
		}
	}

	return config
}

// buildAPIKeyGRPCRouteConfig builds GRPCRoute config for a client with API key/JWT auth
func (s *RouteService) buildAPIKeyGRPCRouteConfig(route *models.Route, domain *models.Domain, client ClientAuthCategory) *GRPCRouteConfig {
	baseConfig := s.buildGRPCRouteConfig(route, domain)

	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
	baseConfig.Name = routeName
	baseConfig.RouteID = route.ID.String()

	// Add header match on client ID for routing (for API key / JWT clients)
	if client.EnableAPIKey || client.EnableJWT {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, HeaderMatch{
				Name:  client.ClientIDHeaderName,
				Type:  "Exact",
				Value: client.ClientID.String(),
			})
		}
	}

	// Add XFCC header matches (for mTLS clients)
	xfccMatches := buildMTLSXFCCHeaderMatches(client)
	if len(xfccMatches) > 0 {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, xfccMatches...)
		}
	}

	// Add client identification headers for backend enrichment
	if baseConfig.RequestHeaderModifier == nil {
		baseConfig.RequestHeaderModifier = &HTTPHeaderModifier{}
	}
	baseConfig.RequestHeaderModifier.Add = append(baseConfig.RequestHeaderModifier.Add,
		HTTPHeaderValue{Name: "X-Client-ID", Value: client.ClientID.String()},
		HTTPHeaderValue{Name: "X-Client-Name", Value: client.ClientName},
	)

	return baseConfig
}

// convertRedirect converts models.RedirectConfig to HTTPRedirectConfig
func convertRedirect(redirect *models.RedirectConfig) *HTTPRedirectConfig {
	if redirect == nil {
		return nil
	}

	result := &HTTPRedirectConfig{
		Scheme:     redirect.Scheme,
		Hostname:   redirect.Hostname,
		Port:       redirect.Port,
		StatusCode: redirect.StatusCode,
	}

	if redirect.Path != nil {
		result.Path = &HTTPPathRewrite{
			Type:               redirect.Path.Type,
			ReplacePrefixMatch: redirect.Path.ReplacePrefixMatch,
			ReplaceFullPath:    redirect.Path.ReplaceFullPath,
		}
	}

	return result
}

// convertHeaderModifier converts models.HeaderModifier to HTTPHeaderModifier
func convertHeaderModifier(mod *models.HeaderModifier) *HTTPHeaderModifier {
	if mod == nil {
		return nil
	}

	result := &HTTPHeaderModifier{}

	if len(mod.Set) > 0 {
		result.Set = make([]HTTPHeaderValue, 0, len(mod.Set))
		for _, h := range mod.Set {
			result.Set = append(result.Set, HTTPHeaderValue{Name: h.Name, Value: h.Value})
		}
	}

	if len(mod.Add) > 0 {
		result.Add = make([]HTTPHeaderValue, 0, len(mod.Add))
		for _, h := range mod.Add {
			result.Add = append(result.Add, HTTPHeaderValue{Name: h.Name, Value: h.Value})
		}
	}

	if len(mod.Remove) > 0 {
		result.Remove = mod.Remove
	}

	return result
}

// convertURLRewrite converts models.URLRewrite to HTTPURLRewrite
func convertURLRewrite(rewrite *models.URLRewrite) *HTTPURLRewrite {
	if rewrite == nil {
		return nil
	}

	result := &HTTPURLRewrite{}

	if rewrite.Hostname != nil {
		result.Hostname = rewrite.Hostname
	}

	if rewrite.Path != nil {
		result.Path = &HTTPPathRewrite{
			Type:               rewrite.Path.Type,
			ReplacePrefixMatch: rewrite.Path.ReplacePrefixMatch,
			ReplaceFullPath:    rewrite.Path.ReplaceFullPath,
		}
	}

	return result
}

// GenerateYAML generates the Kubernetes YAML for a route
func (s *RouteService) GenerateYAML(id uuid.UUID) (string, error) {
	route, err := s.routeRepo.GetByID(id)
	if err != nil {
		return "", err
	}

	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return "", err
	}

	return generateHTTPRouteYAML(route, domain), nil
}

// RouteYAMLs represents both HTTPRoute and SecurityPolicy YAMLs
type RouteYAMLs struct {
	HTTPRouteYAML            string `json:"httpRouteYaml"`
	SecurityPolicyYAML       string `json:"securityPolicyYaml,omitempty"`
	BackendTrafficPolicyYAML string `json:"backendTrafficPolicyYaml,omitempty"`
	EnvoyExtensionPolicyYAML string `json:"envoyExtensionPolicyYaml,omitempty"`
	BackendYAML              string `json:"backendYaml,omitempty"`
	HTTPRouteFilterYAML      string `json:"httpRouteFilterYaml,omitempty"`
	ConfigMapYAML            string `json:"configMapYaml,omitempty"`
	// Per-client API key resources (with secrets redacted)
	APIKeyClientResources []APIKeyClientResourceYAMLs `json:"apiKeyClientResources,omitempty"`
}

// APIKeyClientResourceYAMLs represents the K8s resources for a single API key client
type APIKeyClientResourceYAMLs struct {
	ClientID                 string `json:"clientId"`
	ClientName               string `json:"clientName"`
	HTTPRouteYAML            string `json:"httpRouteYaml"`
	SecurityPolicyYAML       string `json:"securityPolicyYaml"`
	BackendTrafficPolicyYAML string `json:"backendTrafficPolicyYaml,omitempty"`
	EnvoyExtensionPolicyYAML string `json:"envoyExtensionPolicyYaml,omitempty"`
}

// GenerateYAMLs generates both HTTPRoute and SecurityPolicy YAML for a route
func (s *RouteService) GenerateYAMLs(id uuid.UUID) (*RouteYAMLs, error) {
	route, err := s.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, err
	}

	result := &RouteYAMLs{
		HTTPRouteYAML: generateHTTPRouteYAML(route, domain),
	}

	// Generate SecurityPolicy YAML if exists
	// This includes CORS, OIDC, JWT, APIKeyAuth, Authorization from DB
	// plus client IP authorization from attachments
	if s.securityPolicyRepo != nil {
		policy, _ := s.securityPolicyRepo.GetByRouteID(id)

		// Compute authorization from IP-only client attachments
		clientAuthConfig := s.buildClientIPAuthorizationConfig(id)

		// Check if there are per-client auth clients that require deny-all on base route
		// This matches the deploy logic in deploySecurityPolicy
		hasPerClientClients := s.hasAPIKeyClientAttachments(id) || s.hasJWTClientAttachments(id) || s.hasMTLSClientAttachments(id)
		if hasPerClientClients && clientAuthConfig == nil {
			// Create a deny-all authorization (empty CIDR list with default deny)
			// This prevents unauthenticated access through the base HTTPRoute
			clientAuthConfig = &AuthorizationPolicyConfig{
				DefaultAction: "Deny",
				Rules:         []AuthorizationRulePolicyConfig{},
			}
		}

		if policy != nil || clientAuthConfig != nil {
			// Use securityPolicyConfigFromDB to get full config (CORS, OIDC, JWT, APIKeyAuth, Authorization)
			var mergedConfig *SecurityPolicyConfig
			if policy != nil {
				mergedConfig = securityPolicyConfigFromDB(route, domain, policy)
			}

			// If no DB policy but we have client auth, create a minimal config
			if mergedConfig == nil && clientAuthConfig != nil {
				mergedConfig = &SecurityPolicyConfig{
					Name:      route.K8sRouteName + "-security",
					Namespace: domain.Namespace,
					GatewayID: domain.ID.String(),
					RouteID:   route.ID.String(),
					TargetRef: SecurityPolicyTargetRef{
						Group: "gateway.networking.k8s.io",
						Kind:  getRouteKind(route.Protocol),
						Name:  route.K8sRouteName,
					},
				}
			}

			// Merge client IP authorization if present and no DB authorization exists
			// (client mode uses client IPs, general mode uses DB authorization)
			if mergedConfig != nil && clientAuthConfig != nil && mergedConfig.Authorization == nil {
				mergedConfig.Authorization = clientAuthConfig
			}

			if mergedConfig != nil {
				securityPolicy := BuildSecurityPolicy(mergedConfig)
				if securityPolicy != nil {
					yamlBytes, err := yaml.Marshal(securityPolicy.Object)
					if err == nil {
						result.SecurityPolicyYAML = string(yamlBytes)
					}
				}
			}
		}
	}

	// Generate BackendTrafficPolicy YAML if exists
	if s.backendTrafficPolicyRepo != nil {
		btpPolicy, _ := s.backendTrafficPolicyRepo.GetByRouteID(id)
		if btpPolicy != nil {
			result.BackendTrafficPolicyYAML = generateBackendTrafficPolicyYAMLFromDB(route, domain, btpPolicy)
		}
	}

	// Generate EnvoyExtensionPolicy YAML if exists (with WAF support)
	var extPolicy *models.EnvoyExtensionPolicy
	var wafPolicy *models.WafPolicy
	if s.envoyExtensionPolicyRepo != nil {
		extPolicy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(id)
	}
	if s.wafPolicyRepo != nil {
		wafPolicy, _ = s.wafPolicyRepo.GetByRouteID(id)
	}
	if extPolicy != nil || wafPolicy != nil {
		result.EnvoyExtensionPolicyYAML = s.generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route, domain, extPolicy, wafPolicy)
	}

	// Generate Backend CRD YAML for external backends
	result.BackendYAML = generateBackendYAMLs(route, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes
	if route.Config.DirectResponse != nil {
		result.HTTPRouteFilterYAML, result.ConfigMapYAML = generateDirectResponseYAMLs(route, domain)
	}

	// Generate per-client API key resources (with secrets redacted)
	apiKeyClientResources := s.generateAPIKeyClientResourceYAMLs(route, domain)
	if len(apiKeyClientResources) > 0 {
		result.APIKeyClientResources = apiKeyClientResources
	}

	return result, nil
}

// generateAPIKeyClientResourceYAMLs generates YAML for per-client API key resources
// with secrets redacted for display purposes
func (s *RouteService) generateAPIKeyClientResourceYAMLs(route *models.Route, domain *models.Domain) []APIKeyClientResourceYAMLs {
	if s.clientAttachmentRepo == nil || s.clientRepo == nil {
		return nil
	}

	ctx := context.Background()

	// Categorize client attachments
	_, apiKeyOnlyClients, bothClients, err := s.categorizeClientAttachments(ctx, route.ID, domain)
	if err != nil {
		return nil
	}

	// Combine API key clients
	allAPIKeyClients := append(apiKeyOnlyClients, bothClients...)
	if len(allAPIKeyClients) == 0 {
		return nil
	}

	// Get SecurityPolicy for this route (if any) to copy CORS config to per-client routes
	var secPolicy *models.SecurityPolicy
	if s.securityPolicyRepo != nil {
		secPolicy, _ = s.securityPolicyRepo.GetByRouteID(route.ID)
	}

	var results []APIKeyClientResourceYAMLs
	for _, client := range allAPIKeyClients {
		clientResource := APIKeyClientResourceYAMLs{
			ClientID:   client.ClientID.String(),
			ClientName: client.ClientName,
		}

		// Build route YAML (HTTPRoute or GRPCRoute based on protocol)
		if route.Protocol == models.RouteProtocolGRPC {
			grpcRouteConfig := s.buildAPIKeyGRPCRouteConfig(route, domain, client)
			grpcRoute := BuildGRPCRouteObject(grpcRouteConfig)
			if grpcRoute != nil {
				yamlBytes, err := yaml.Marshal(grpcRoute)
				if err == nil {
					clientResource.HTTPRouteYAML = string(yamlBytes)
				}
			}
		} else {
			httpRouteConfig := s.buildAPIKeyHTTPRouteConfigRedacted(route, domain, client)
			httpRoute := BuildHTTPRouteObject(httpRouteConfig)
			if httpRoute != nil {
				yamlBytes, err := yaml.Marshal(httpRoute)
				if err == nil {
					clientResource.HTTPRouteYAML = string(yamlBytes)
				}
			}
		}

		// Build SecurityPolicy
		requireIP := client.EnableIP
		securityConfig := s.buildAPIKeySecurityPolicyConfig(route, domain, client, requireIP, secPolicy)
		securityPolicy := BuildSecurityPolicy(securityConfig)
		if securityPolicy != nil {
			yamlBytes, err := yaml.Marshal(securityPolicy.Object)
			if err == nil {
				clientResource.SecurityPolicyYAML = string(yamlBytes)
			}
		}

		// Build BackendTrafficPolicy if base BTP exists or client has rate limit
		{
			var btpPolicy *models.BackendTrafficPolicy
			if s.backendTrafficPolicyRepo != nil {
				btpPolicy, _ = s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
			}
			if btpPolicy != nil || client.RateLimitConfig != nil {
				routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
				clientResource.BackendTrafficPolicyYAML = generateAPIKeyBackendTrafficPolicyYAML(route, domain, btpPolicy, routeName, client.RateLimitConfig)
			}
		}

		// Build EnvoyExtensionPolicy if base extension policy exists
		{
			var extPolicy *models.EnvoyExtensionPolicy
			if s.envoyExtensionPolicyRepo != nil {
				extPolicy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
			}
			if extPolicy != nil {
				routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
				clientResource.EnvoyExtensionPolicyYAML = generateAPIKeyEnvoyExtensionPolicyYAML(route, domain, extPolicy, routeName)
			}
		}

		results = append(results, clientResource)
	}

	return results
}

// buildAPIKeyHTTPRouteConfigRedacted builds HTTPRoute config for display
// Note: With the new two-header approach, client ID is used for routing (not API key),
// so no redaction is needed - client IDs are not secrets
func (s *RouteService) buildAPIKeyHTTPRouteConfigRedacted(route *models.Route, domain *models.Domain, client ClientAuthCategory) *HTTPRouteConfig {
	// Get the base config
	baseConfig := s.buildHTTPRouteConfig(route, domain)

	// Modify name to include client ID prefix
	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
	baseConfig.Name = routeName
	baseConfig.RouteID = route.ID.String()

	// Add header match on CLIENT ID (for API key, JWT, and mTLS clients)
	if client.EnableAPIKey || client.EnableJWT || client.EnableMTLS {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, HeaderMatch{
				Name:  client.ClientIDHeaderName,
				Type:  "Exact",
				Value: client.ClientID.String(),
			})
		}
	}

	// Add XFCC header matches (for mTLS clients - additional cert verification)
	xfccMatches := buildMTLSXFCCHeaderMatches(client)
	if len(xfccMatches) > 0 {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, xfccMatches...)
		}
	}

	// Add client identification headers
	if baseConfig.RequestHeaderModifier == nil {
		baseConfig.RequestHeaderModifier = &HTTPHeaderModifier{}
	}
	baseConfig.RequestHeaderModifier.Add = append(baseConfig.RequestHeaderModifier.Add,
		HTTPHeaderValue{Name: "X-Client-ID", Value: client.ClientID.String()},
		HTTPHeaderValue{Name: "X-Client-Name", Value: client.ClientName},
	)

	return baseConfig
}

// generateAPIKeyBackendTrafficPolicyYAML generates BTP YAML for a per-client HTTPRoute
func generateAPIKeyBackendTrafficPolicyYAML(route *models.Route, domain *models.Domain, btpPolicy *models.BackendTrafficPolicy, routeName string, rateLimitConfig *models.RateLimitConfig) string {
	hasBasePolicy := btpPolicy != nil && !btpPolicy.Config.IsEmpty()
	hasRateLimit := rateLimitConfig != nil

	if !hasBasePolicy && !hasRateLimit {
		return ""
	}

	btpConfig := &BackendTrafficPolicyConfig{
		Name:      routeName + "-btp",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: BackendTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  routeName,
		},
	}

	// Copy base BTP config if present
	if hasBasePolicy {
		// Copy compression config from base BTP
		if len(btpPolicy.Config.Compression) > 0 {
			btpConfig.Compression = make([]CompressionPolicyConfig, 0, len(btpPolicy.Config.Compression))
			for _, comp := range btpPolicy.Config.Compression {
				policyComp := CompressionPolicyConfig{
					Type: string(comp.Type),
				}
				switch comp.Type {
				case models.CompressionTypeGzip:
					policyComp.Gzip = &GzipPolicyConfig{}
				case models.CompressionTypeBrotli:
					policyComp.Brotli = &BrotliPolicyConfig{}
				case models.CompressionTypeZstd:
					policyComp.Zstd = &ZstdPolicyConfig{}
				}
				btpConfig.Compression = append(btpConfig.Compression, policyComp)
			}
		}

		// Copy retry config from base BTP
		if btpPolicy.Config.Retry != nil {
			btpConfig.Retry = mapRetryConfigToPolicy(btpPolicy.Config.Retry)
		}

		// Copy load balancer config from base BTP
		if btpPolicy.Config.LoadBalancer != nil {
			btpConfig.LoadBalancer = mapLoadBalancerConfigToPolicy(btpPolicy.Config.LoadBalancer)
		}

		// Copy circuit breaker config from base BTP
		if btpPolicy.Config.CircuitBreaker != nil {
			btpConfig.CircuitBreaker = mapCircuitBreakerConfigToPolicy(btpPolicy.Config.CircuitBreaker)
		}

		// Copy health check config from base BTP
		if btpPolicy.Config.HealthCheck != nil {
			btpConfig.HealthCheck = mapHealthCheckConfigToPolicy(btpPolicy.Config.HealthCheck)
		}

		// Copy fault injection config from base BTP
		if btpPolicy.Config.FaultInjection != nil {
			btpConfig.FaultInjection = mapFaultInjectionConfigToPolicy(btpPolicy.Config.FaultInjection)
		}

		// Copy base route rate limit (may be overridden by per-client below)
		if btpPolicy.Config.RateLimit != nil {
			btpConfig.RateLimit = mapRateLimitConfigToPolicy(btpPolicy.Config.RateLimit)
		}

		// Copy request buffer config from base BTP
		if btpPolicy.Config.RequestBuffer != nil {
			btpConfig.RequestBuffer = &RequestBufferPolicyConfig{
				Limit: btpPolicy.Config.RequestBuffer.Limit,
			}
		}

		// Copy response override config from base BTP
		if len(btpPolicy.Config.ResponseOverride) > 0 {
			btpConfig.ResponseOverride = mapResponseOverrideToPolicy(btpPolicy.Config.ResponseOverride)
		}

		// Copy timeout config from base BTP
		if btpPolicy.Config.Timeout != nil {
			btpConfig.Timeout = mapTimeoutConfigToPolicy(btpPolicy.Config.Timeout)
		}
	}

	// Override with per-client rate limit from attachment if present
	if hasRateLimit {
		btpConfig.RateLimit = mapRateLimitConfigToPolicy(rateLimitConfig)
	}

	btp := BuildBackendTrafficPolicy(btpConfig)
	if btp == nil {
		return ""
	}

	yamlBytes, err := yaml.Marshal(btp.Object)
	if err != nil {
		return ""
	}

	return string(yamlBytes)
}

// generateAPIKeyEnvoyExtensionPolicyYAML generates EnvoyExtensionPolicy YAML for a per-client HTTPRoute
func generateAPIKeyEnvoyExtensionPolicyYAML(route *models.Route, domain *models.Domain, extPolicy *models.EnvoyExtensionPolicy, routeName string) string {
	if extPolicy == nil || extPolicy.Config.IsEmpty() {
		return ""
	}

	extConfig := &EnvoyExtensionPolicyK8sConfig{
		Name:      routeName + "-eep",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  routeName,
		},
	}

	// Copy Lua extension from base policy
	if extPolicy.Config.Lua != nil {
		luaConfig := LuaExtensionPolicyConfig{
			Type:   extPolicy.Config.Lua.Type,
			Inline: extPolicy.Config.Lua.Inline,
		}
		if extPolicy.Config.Lua.ValueRef != nil {
			luaConfig.ValueRef = &ValueRefPolicyConfig{
				Group:     extPolicy.Config.Lua.ValueRef.Group,
				Kind:      extPolicy.Config.Lua.ValueRef.Kind,
				Name:      extPolicy.Config.Lua.ValueRef.Name,
				Namespace: extPolicy.Config.Lua.ValueRef.Namespace,
			}
		}
		extConfig.Lua = append(extConfig.Lua, luaConfig)
	}

	// Copy Wasm extension from base policy
	if extPolicy.Config.Wasm != nil {
		wasmConfig := WasmExtensionPolicyConfig{
			Name:   extPolicy.Config.Wasm.Name,
			RootID: extPolicy.Config.Wasm.RootID,
			Code: WasmCodeSourcePolicyConfig{
				Type: extPolicy.Config.Wasm.Code.Type,
			},
			Config: extPolicy.Config.Wasm.Config,
		}
		if extPolicy.Config.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &WasmHTTPSourcePolicyConfig{
				URL:    extPolicy.Config.Wasm.Code.HTTP.URL,
				SHA256: extPolicy.Config.Wasm.Code.HTTP.SHA256,
			}
		}
		if extPolicy.Config.Wasm.Code.Image != nil {
			imageConfig := &WasmImageSourcePolicyConfig{
				URL:    extPolicy.Config.Wasm.Code.Image.URL,
				SHA256: extPolicy.Config.Wasm.Code.Image.SHA256,
			}
			if extPolicy.Config.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &ValueRefPolicyConfig{
					Group:     extPolicy.Config.Wasm.Code.Image.PullSecret.Group,
					Kind:      extPolicy.Config.Wasm.Code.Image.PullSecret.Kind,
					Name:      extPolicy.Config.Wasm.Code.Image.PullSecret.Name,
					Namespace: extPolicy.Config.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		extConfig.Wasm = append(extConfig.Wasm, wasmConfig)
	}

	// Add ExtProc extension
	if extPolicy.Config.ExtProc != nil {
		extConfig.ExtProc = append(extConfig.ExtProc, buildExtProcPolicyConfig(extPolicy.Config.ExtProc))
	}

	eep := BuildEnvoyExtensionPolicy(extConfig)
	if eep == nil {
		return ""
	}

	yamlBytes, err := yaml.Marshal(eep.Object)
	if err != nil {
		return ""
	}

	return string(yamlBytes)
}

// isValidK8sName checks if a name is valid for Kubernetes resources
// Must be lowercase alphanumeric with dashes, start with letter, end with alphanumeric
func isValidK8sName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}

	// Must start with a lowercase letter
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}

	// Must end with alphanumeric
	last := name[len(name)-1]
	if !((last >= 'a' && last <= 'z') || (last >= '0' && last <= '9')) {
		return false
	}

	// Check all characters
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}

	return true
}

// generateRouteK8sName generates a valid Kubernetes resource name for a route
// Format: {route-name}-{first-8-chars-of-route-uuid}
func generateRouteK8sName(routeName string, routeID uuid.UUID) string {
	// Get first 8 characters of UUID (without dashes)
	shortID := strings.ReplaceAll(routeID.String(), "-", "")[:8]

	// Combine route name and short UUID
	k8sName := fmt.Sprintf("%s-%s", routeName, shortID)
	k8sName = strings.ToLower(k8sName)

	// Truncate to 63 characters (K8s name limit)
	if len(k8sName) > 63 {
		// Truncate route name part, keep the UUID suffix
		maxRouteNameLen := 63 - 9 // 8 chars for UUID + 1 dash
		k8sName = fmt.Sprintf("%s-%s", routeName[:maxRouteNameLen], shortID)
		k8sName = strings.TrimRight(k8sName, "-")
	}

	return k8sName
}

// PreviewCreateResult represents the result of a create preview
type PreviewCreateResult struct {
	ProposedYAML                     string `json:"proposedYaml"`
	ProposedSecurityPolicyYAML       string `json:"proposedSecurityPolicyYaml,omitempty"`
	ProposedBackendTrafficPolicyYAML string `json:"proposedBackendTrafficPolicyYaml,omitempty"`
	ProposedEnvoyExtensionPolicyYAML string `json:"proposedEnvoyExtensionPolicyYaml,omitempty"`
	ProposedBackendYAML              string `json:"proposedBackendYaml,omitempty"`
	ProposedHTTPRouteFilterYAML      string `json:"proposedHttpRouteFilterYaml,omitempty"`
	ProposedConfigMapYAML            string `json:"proposedConfigMapYaml,omitempty"`
}

// PreviewUpdateResult represents the result of an update preview
type PreviewUpdateResult struct {
	CurrentYAML                      string `json:"currentYaml"`
	ProposedYAML                     string `json:"proposedYaml"`
	CurrentSecurityPolicyYAML        string `json:"currentSecurityPolicyYaml,omitempty"`
	ProposedSecurityPolicyYAML       string `json:"proposedSecurityPolicyYaml,omitempty"`
	CurrentBackendTrafficPolicyYAML  string `json:"currentBackendTrafficPolicyYaml,omitempty"`
	ProposedBackendTrafficPolicyYAML string `json:"proposedBackendTrafficPolicyYaml,omitempty"`
	CurrentEnvoyExtensionPolicyYAML  string `json:"currentEnvoyExtensionPolicyYaml,omitempty"`
	ProposedEnvoyExtensionPolicyYAML string `json:"proposedEnvoyExtensionPolicyYaml,omitempty"`
	CurrentBackendYAML               string `json:"currentBackendYaml,omitempty"`
	ProposedBackendYAML              string `json:"proposedBackendYaml,omitempty"`
	CurrentHTTPRouteFilterYAML       string `json:"currentHttpRouteFilterYaml,omitempty"`
	ProposedHTTPRouteFilterYAML      string `json:"proposedHttpRouteFilterYaml,omitempty"`
	CurrentConfigMapYAML             string `json:"currentConfigMapYaml,omitempty"`
	ProposedConfigMapYAML            string `json:"proposedConfigMapYaml,omitempty"`
}

// PreviewDeleteResult represents the result of a delete preview
type PreviewDeleteResult struct {
	CurrentYAML                     string `json:"currentYaml"`
	CurrentSecurityPolicyYAML       string `json:"currentSecurityPolicyYaml,omitempty"`
	CurrentBackendTrafficPolicyYAML string `json:"currentBackendTrafficPolicyYaml,omitempty"`
	CurrentEnvoyExtensionPolicyYAML string `json:"currentEnvoyExtensionPolicyYaml,omitempty"`
	CurrentBackendYAML              string `json:"currentBackendYaml,omitempty"`
	CurrentHTTPRouteFilterYAML      string `json:"currentHttpRouteFilterYaml,omitempty"`
	CurrentConfigMapYAML            string `json:"currentConfigMapYaml,omitempty"`
}

// PreviewCreate generates a preview of what the HTTPRoute YAML would look like for a new route
func (s *RouteService) PreviewCreate(domainID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error) {
	// Validate route name format
	if !isValidK8sName(input.Name) {
		return nil, errors.New("route name must be lowercase alphanumeric with dashes only (e.g., 'user-api')")
	}

	// Verify domain exists
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Generate a temporary route ID for preview
	tempRouteID := uuid.New()
	k8sRouteName := generateRouteK8sName(input.Name, tempRouteID)

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

	proposedYAML := generateHTTPRouteYAML(tempRoute, domain)

	// Generate SecurityPolicy YAML if security features are configured
	// Note: For new routes, there are no client attachments yet, so clientCIDRs is nil
	proposedSecurityPolicyYAML := generateSecurityPolicyYAML(tempRoute, domain, input.SecurityPolicy, nil)

	// Generate BackendTrafficPolicy YAML if configured
	proposedBackendTrafficPolicyYAML := generateBackendTrafficPolicyYAML(tempRoute, domain, input.BackendTrafficPolicy)

	// Generate Backend CRD YAML for external backends
	proposedBackendYAML := generateBackendYAMLs(tempRoute, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes
	var proposedHTTPRouteFilterYAML, proposedConfigMapYAML string
	if input.Config.DirectResponse != nil {
		proposedHTTPRouteFilterYAML, proposedConfigMapYAML = generateDirectResponseYAMLs(tempRoute, domain)
	}

	// Generate EnvoyExtensionPolicy YAML if configured (extension policy and/or WAF)
	proposedEnvoyExtensionPolicyYAML := generateEnvoyExtensionPolicyYAMLWithWaf(tempRoute, domain, input.ExtensionPolicy, input.WafPolicy)

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
func (s *RouteService) PreviewUpdate(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error) {
	// Get existing route
	route, err := s.routeRepo.GetByID(routeID)
	if err != nil {
		return nil, errors.New("route not found")
	}

	// Get domain
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Generate current YAML (with existing config)
	currentYAML := generateHTTPRouteYAML(route, domain)

	// Get current SecurityPolicy from database (if any)
	var currentSecurityPolicyYAML string
	if s.securityPolicyRepo != nil {
		currentPolicy, _ := s.securityPolicyRepo.GetByRouteID(routeID)
		if currentPolicy != nil {
			currentSecurityPolicyYAML = generateSecurityPolicyYAMLFromDB(route, domain, currentPolicy)
		}
	}

	// Get current BackendTrafficPolicy from database (if any)
	var currentBackendTrafficPolicyYAML string
	if s.backendTrafficPolicyRepo != nil {
		currentBtpPolicy, _ := s.backendTrafficPolicyRepo.GetByRouteID(routeID)
		if currentBtpPolicy != nil {
			currentBackendTrafficPolicyYAML = generateBackendTrafficPolicyYAMLFromDB(route, domain, currentBtpPolicy)
		}
	}

	// Get current EnvoyExtensionPolicy and WafPolicy from database (if any)
	var currentEnvoyExtensionPolicyYAML string
	var currentExtPolicy *models.EnvoyExtensionPolicy
	var currentWafPolicy *models.WafPolicy
	if s.envoyExtensionPolicyRepo != nil {
		currentExtPolicy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(routeID)
	}
	if s.wafPolicyRepo != nil {
		currentWafPolicy, _ = s.wafPolicyRepo.GetByRouteID(routeID)
	}
	if currentExtPolicy != nil || currentWafPolicy != nil {
		currentEnvoyExtensionPolicyYAML = s.generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route, domain, currentExtPolicy, currentWafPolicy)
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

	proposedYAML := generateHTTPRouteYAML(proposedRoute, domain)

	// Collect client CIDRs from existing attachments for preview
	clientCIDRs := s.collectClientIPCIDRs(routeID)

	// Generate proposed SecurityPolicy YAML if security features are configured
	// Include client CIDRs to show the full merged result
	proposedSecurityPolicyYAML := generateSecurityPolicyYAML(proposedRoute, domain, input.SecurityPolicy, clientCIDRs)

	// Generate proposed BackendTrafficPolicy YAML if configured
	proposedBackendTrafficPolicyYAML := generateBackendTrafficPolicyYAML(proposedRoute, domain, input.BackendTrafficPolicy)

	// Generate Backend CRD YAML for external backends (current and proposed)
	currentBackendYAML := generateBackendYAMLs(route, domain)
	proposedBackendYAML := generateBackendYAMLs(proposedRoute, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes (current and proposed)
	var currentHTTPRouteFilterYAML, currentConfigMapYAML string
	if route.Config.DirectResponse != nil {
		currentHTTPRouteFilterYAML, currentConfigMapYAML = generateDirectResponseYAMLs(route, domain)
	}
	var proposedHTTPRouteFilterYAML, proposedConfigMapYAML string
	if input.Config.DirectResponse != nil {
		proposedHTTPRouteFilterYAML, proposedConfigMapYAML = generateDirectResponseYAMLs(proposedRoute, domain)
	}

	// Generate proposed EnvoyExtensionPolicy YAML if configured (extension policy and/or WAF)
	proposedEnvoyExtensionPolicyYAML := generateEnvoyExtensionPolicyYAMLWithWaf(proposedRoute, domain, input.ExtensionPolicy, input.WafPolicy)

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
func (s *RouteService) PreviewDelete(routeID uuid.UUID) (*PreviewDeleteResult, error) {
	// Get existing route
	route, err := s.routeRepo.GetByID(routeID)
	if err != nil {
		return nil, errors.New("route not found")
	}

	// Get domain
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Generate current YAML
	currentYAML := generateHTTPRouteYAML(route, domain)

	// Get current SecurityPolicy from database (if any)
	var currentSecurityPolicyYAML string
	if s.securityPolicyRepo != nil {
		currentPolicy, _ := s.securityPolicyRepo.GetByRouteID(routeID)
		if currentPolicy != nil {
			currentSecurityPolicyYAML = generateSecurityPolicyYAMLFromDB(route, domain, currentPolicy)
		}
	}

	// Get current BackendTrafficPolicy from database (if any)
	var currentBackendTrafficPolicyYAML string
	if s.backendTrafficPolicyRepo != nil {
		currentBtpPolicy, _ := s.backendTrafficPolicyRepo.GetByRouteID(routeID)
		if currentBtpPolicy != nil {
			currentBackendTrafficPolicyYAML = generateBackendTrafficPolicyYAMLFromDB(route, domain, currentBtpPolicy)
		}
	}

	// Get current EnvoyExtensionPolicy and WafPolicy from database (if any)
	var currentEnvoyExtensionPolicyYAML string
	var currentExtPolicy *models.EnvoyExtensionPolicy
	var currentWafPolicy *models.WafPolicy
	if s.envoyExtensionPolicyRepo != nil {
		currentExtPolicy, _ = s.envoyExtensionPolicyRepo.GetByRouteID(routeID)
	}
	if s.wafPolicyRepo != nil {
		currentWafPolicy, _ = s.wafPolicyRepo.GetByRouteID(routeID)
	}
	if currentExtPolicy != nil || currentWafPolicy != nil {
		currentEnvoyExtensionPolicyYAML = s.generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route, domain, currentExtPolicy, currentWafPolicy)
	}

	// Generate Backend CRD YAML for external backends
	currentBackendYAML := generateBackendYAMLs(route, domain)

	// Generate HTTPRouteFilter and ConfigMap YAML for direct response routes
	var currentHTTPRouteFilterYAML, currentConfigMapYAML string
	if route.Config.DirectResponse != nil {
		currentHTTPRouteFilterYAML, currentConfigMapYAML = generateDirectResponseYAMLs(route, domain)
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

// generateHTTPRouteYAML generates HTTPRoute YAML using typed Gateway API structs
// This ensures the preview YAML matches exactly what will be deployed to Kubernetes
func generateHTTPRouteYAML(route *models.Route, domain *models.Domain) string {
	if route.Protocol == models.RouteProtocolGRPC {
		config := buildGRPCRouteConfigForYAML(route, domain)
		grpcRoute := BuildGRPCRouteObject(config)
		yamlBytes, err := yaml.Marshal(grpcRoute)
		if err != nil {
			return fmt.Sprintf("# Error generating YAML: %v", err)
		}
		return string(yamlBytes)
	}

	// Build HTTPRouteConfig from route and domain
	config := buildHTTPRouteConfigForYAML(route, domain)

	// Use the same typed struct builder as Kubernetes deployment
	httpRoute := BuildHTTPRouteObject(config)

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(httpRoute)
	if err != nil {
		return fmt.Sprintf("# Error generating YAML: %v", err)
	}

	return string(yamlBytes)
}

// generateBackendYAMLs generates Backend CRD YAML for external backends,
// or for ALL backends when failover is enabled
func generateBackendYAMLs(route *models.Route, domain *models.Domain) string {
	hasFailover := route.Config.HasFailover()
	var yamls []string
	for i, backend := range route.Config.Backends {
		// Generate Backend CRD if external, failover is enabled, or TLS is configured
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

			config := &BackendConfig{
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
				config.TLS = &BackendTLSPolicyConfig{
					InsecureSkipVerify: backend.TLS.InsecureSkipVerify,
					SNI:                backend.TLS.SNI,
				}

				// Map CA certificate refs (only when not insecureSkipVerify)
				if !backend.TLS.InsecureSkipVerify && len(backend.TLS.CACertificateRefs) > 0 {
					config.TLS.CACertificateRefs = make([]BackendCertificateRefConfig, len(backend.TLS.CACertificateRefs))
					for j, ref := range backend.TLS.CACertificateRefs {
						config.TLS.CACertificateRefs[j] = BackendCertificateRefConfig{
							Kind:      ref.Kind,
							Name:      ref.Name,
							Namespace: ref.Namespace,
						}
					}
				}

				// Map client certificate ref for mTLS
				if backend.TLS.ClientCertificateRef != nil {
					config.TLS.ClientCertificateRef = &BackendSecretRefConfig{
						Name:      backend.TLS.ClientCertificateRef.Name,
						Namespace: backend.TLS.ClientCertificateRef.Namespace,
					}
				}
			}

			obj := BuildBackend(config)
			yamlBytes, err := yaml.Marshal(obj.Object)
			if err == nil {
				yamls = append(yamls, string(yamlBytes))
			}
		}
	}
	if len(yamls) == 0 {
		return ""
	}
	return strings.Join(yamls, "---\n")
}

// generateDirectResponseYAMLs generates HTTPRouteFilter and ConfigMap YAML for direct response routes
func generateDirectResponseYAMLs(route *models.Route, domain *models.Domain) (string, string) {
	if route.Config.DirectResponse == nil {
		return "", ""
	}

	hrfName := route.K8sRouteName + "-hrf"
	cmName := route.K8sRouteName + "-dr-cm"

	var configMapYAML string
	// Generate ConfigMap YAML if body is provided
	if route.Config.DirectResponse.Body != nil && route.Config.DirectResponse.Body.Inline != "" {
		cmConfig := &DirectResponseConfigMapConfig{
			Name:        cmName,
			Namespace:   domain.Namespace,
			GatewayID:   domain.ID.String(),
			RouteID:     route.ID.String(),
			BodyContent: route.Config.DirectResponse.Body.Inline,
		}
		cmObj := BuildDirectResponseConfigMap(cmConfig)
		if cmObj != nil {
			yamlBytes, err := yaml.Marshal(cmObj.Object)
			if err == nil {
				configMapYAML = string(yamlBytes)
			}
		}
	}

	// Generate HTTPRouteFilter YAML
	hrfConfig := &HTTPRouteFilterConfig{
		Name:      hrfName,
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		DirectResponse: &DirectResponseFilterConfig{
			StatusCode:  route.Config.DirectResponse.StatusCode,
			ContentType: route.Config.DirectResponse.ContentType,
		},
	}

	// Set body configuration
	if route.Config.DirectResponse.Body != nil && route.Config.DirectResponse.Body.Inline != "" {
		hrfConfig.DirectResponse.Body = &DirectResponseBodyFilterConfig{
			Type: "ValueRef",
			ValueRef: &DirectResponseValueRef{
				Group: "",
				Kind:  "ConfigMap",
				Name:  cmName,
			},
		}
	}

	var httpRouteFilterYAML string
	hrfObj := BuildHTTPRouteFilter(hrfConfig)
	if hrfObj != nil {
		yamlBytes, err := yaml.Marshal(hrfObj.Object)
		if err == nil {
			httpRouteFilterYAML = string(yamlBytes)
		}
	}

	return httpRouteFilterYAML, configMapYAML
}

// generateSecurityPolicyYAML generates SecurityPolicy YAML for CORS and client IP allowlist
// clientCIDRs parameter allows including client IPs in the preview (for accurate representation)
func generateSecurityPolicyYAML(route *models.Route, domain *models.Domain, securityInput *SecurityPolicyInput, clientCIDRs []string) string {
	hasCORS := securityInput != nil && securityInput.CORS != nil
	hasClientIPs := len(clientCIDRs) > 0
	hasGeneralAuth := securityInput != nil && securityInput.Authorization != nil
	hasAPIKeyAuth := securityInput != nil && securityInput.APIKeyAuth != nil
	hasJWT := securityInput != nil && securityInput.JWT != nil
	hasOIDC := securityInput != nil && securityInput.OIDC != nil
	hasExtAuth := securityInput != nil && securityInput.ExtAuth != nil

	if !hasCORS && !hasClientIPs && !hasGeneralAuth && !hasAPIKeyAuth && !hasJWT && !hasOIDC && !hasExtAuth {
		return ""
	}

	// Build SecurityPolicyConfig
	config := &SecurityPolicyConfig{
		Name:      route.K8sRouteName + "-security",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: SecurityPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Convert CORS config
	if hasCORS {
		config.CORS = &CORSPolicyConfig{
			AllowOrigins:     securityInput.CORS.AllowOrigins,
			AllowMethods:     securityInput.CORS.AllowMethods,
			AllowHeaders:     securityInput.CORS.AllowHeaders,
			ExposeHeaders:    securityInput.CORS.ExposeHeaders,
			MaxAge:           securityInput.CORS.MaxAge,
			AllowCredentials: securityInput.CORS.AllowCredentials,
		}
	}

	// Build authorization config from client IPs (client mode) - mutually exclusive with general mode auth
	if hasClientIPs && !hasGeneralAuth {
		seen := make(map[string]bool)
		uniqueCIDRs := make([]string, 0, len(clientCIDRs))
		for _, cidr := range clientCIDRs {
			normalized := normalizeCIDR(cidr)
			if !seen[normalized] {
				seen[normalized] = true
				uniqueCIDRs = append(uniqueCIDRs, normalized)
			}
		}
		config.Authorization = &AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []AuthorizationRulePolicyConfig{{
				Action:      "Allow",
				ClientCIDRs: uniqueCIDRs,
			}},
		}
	}

	// General mode: authorization from input (takes precedence over client IPs)
	if hasGeneralAuth {
		rule := AuthorizationRulePolicyConfig{
			Action: "Allow",
		}
		if len(securityInput.Authorization.AllowedCIDRs) > 0 {
			cidrs := make([]string, 0, len(securityInput.Authorization.AllowedCIDRs))
			for _, cidr := range securityInput.Authorization.AllowedCIDRs {
				cidrs = append(cidrs, normalizeCIDR(cidr))
			}
			rule.ClientCIDRs = cidrs
		}
		if len(securityInput.Authorization.Headers) > 0 {
			for _, h := range securityInput.Authorization.Headers {
				rule.Headers = append(rule.Headers, HeaderMatchPolicyConfig{Name: h.Name, Values: h.Values})
			}
		}
		if len(securityInput.Authorization.Methods) > 0 {
			rule.Methods = securityInput.Authorization.Methods
		}
		config.Authorization = &AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules:         []AuthorizationRulePolicyConfig{rule},
		}
	}

	// General mode: API Key Auth from input
	if hasAPIKeyAuth {
		config.APIKeyAuth = &APIKeyAuthPolicyConfig{
			CredentialRefs: []SecretRefConfig{{
				Name:      securityInput.APIKeyAuth.SecretName,
				Namespace: FastGatewayNamespace,
			}},
			ExtractFrom: []APIKeyExtractFromConfig{{
				Headers: []string{securityInput.APIKeyAuth.HeaderName},
			}},
		}
	}

	// General mode: JWT from input
	if hasJWT {
		provider := JWTProviderPolicyConfig{
			Name:   "route-jwt",
			Issuer: securityInput.JWT.Issuer,
		}
		if securityInput.JWT.JWKSURL != "" {
			provider.JWKSURL = securityInput.JWT.JWKSURL
		}
		if len(securityInput.JWT.Audiences) > 0 {
			provider.Audiences = securityInput.JWT.Audiences
		}
		for _, cth := range securityInput.JWT.ClaimToHeaders {
			provider.ClaimToHeaders = append(provider.ClaimToHeaders, JWTClaimToHeaderPolicyConfig{
				Claim:  cth.Claim,
				Header: cth.Header,
			})
		}
		config.JWT = &JWTAuthPolicyConfig{
			Providers: []JWTProviderPolicyConfig{provider},
		}
	}

	// General mode: OIDC from input
	if hasOIDC {
		config.OIDC = &OIDCPolicyConfig{
			Issuer:           securityInput.OIDC.Issuer,
			ClientID:         securityInput.OIDC.ClientID,
			ClientSecretName: securityInput.OIDC.ClientSecretName,
			ClientSecretNS:   FastGatewayNamespace,
			RedirectURL:      securityInput.OIDC.RedirectURL,
			LogoutPath:       securityInput.OIDC.LogoutPath,
			Scopes:           securityInput.OIDC.Scopes,
			CookieDomain:     securityInput.OIDC.CookieDomain,
		}
	}

	// Both modes: ExtAuth from input
	if hasExtAuth {
		// Generate a backend name for the ext-auth service (route-level, no client ID)
		backendName := GenerateExtAuthBackendName(route.ID.String(), "")
		config.ExtAuth = securityInput.ExtAuth
		config.ExtAuthBackendName = backendName
	}

	// Build the SecurityPolicy object
	securityPolicy := BuildSecurityPolicy(config)
	if securityPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(securityPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating SecurityPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// generateSecurityPolicyYAMLFromDB generates SecurityPolicy YAML from database model
func generateSecurityPolicyYAMLFromDB(route *models.Route, domain *models.Domain, policy *models.SecurityPolicy) string {
	config := securityPolicyConfigFromDB(route, domain, policy)
	if config == nil {
		return ""
	}

	// Set ExtAuthBackendName if ExtAuth is configured
	if config.ExtAuth != nil {
		config.ExtAuthBackendName = GenerateExtAuthBackendName(route.ID.String(), "")
	}

	// Build the SecurityPolicy object
	securityPolicy := BuildSecurityPolicy(config)
	if securityPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(securityPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating SecurityPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// generateBackendTrafficPolicyYAML generates BackendTrafficPolicy YAML for compression, retry and other features
func generateBackendTrafficPolicyYAML(route *models.Route, domain *models.Domain, btpInput *BackendTrafficPolicyInput) string {
	if btpInput == nil || !btpInput.HasContent() {
		return ""
	}

	// Build BackendTrafficPolicyConfig
	config := &BackendTrafficPolicyConfig{
		Name:      route.K8sRouteName + "-btp",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: BackendTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Convert compression config
	if len(btpInput.Compression) > 0 {
		config.Compression = make([]CompressionPolicyConfig, 0, len(btpInput.Compression))
		for _, comp := range btpInput.Compression {
			policyComp := CompressionPolicyConfig{
				Type: string(comp.Type),
			}
			switch comp.Type {
			case models.CompressionTypeGzip:
				policyComp.Gzip = &GzipPolicyConfig{}
			case models.CompressionTypeBrotli:
				policyComp.Brotli = &BrotliPolicyConfig{}
			case models.CompressionTypeZstd:
				policyComp.Zstd = &ZstdPolicyConfig{}
			}
			config.Compression = append(config.Compression, policyComp)
		}
	}

	// Convert retry config
	if btpInput.Retry != nil {
		config.Retry = mapRetryConfigToPolicy(btpInput.Retry)
	}

	// Convert load balancer config
	if btpInput.LoadBalancer != nil {
		config.LoadBalancer = mapLoadBalancerConfigToPolicy(btpInput.LoadBalancer)
	}

	// Convert circuit breaker config
	if btpInput.CircuitBreaker != nil {
		config.CircuitBreaker = mapCircuitBreakerConfigToPolicy(btpInput.CircuitBreaker)
	}

	// Convert health check config
	if btpInput.HealthCheck != nil {
		config.HealthCheck = mapHealthCheckConfigToPolicy(btpInput.HealthCheck)
	}

	// Convert fault injection config
	if btpInput.FaultInjection != nil {
		config.FaultInjection = mapFaultInjectionConfigToPolicy(btpInput.FaultInjection)
	}

	// Convert rate limit config
	if btpInput.RateLimit != nil {
		config.RateLimit = mapRateLimitConfigToPolicy(btpInput.RateLimit)
	}

	// Convert request buffer config
	if btpInput.RequestBuffer != nil {
		config.RequestBuffer = &RequestBufferPolicyConfig{
			Limit: btpInput.RequestBuffer.Limit,
		}
	}

	// Convert response override config
	if len(btpInput.ResponseOverride) > 0 {
		config.ResponseOverride = mapResponseOverrideToPolicy(btpInput.ResponseOverride)
	}

	// Convert timeout config
	if btpInput.Timeout != nil {
		config.Timeout = mapTimeoutConfigToPolicy(btpInput.Timeout)
	}

	// Build the BackendTrafficPolicy object
	backendTrafficPolicy := BuildBackendTrafficPolicy(config)
	if backendTrafficPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(backendTrafficPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating BackendTrafficPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// generateBackendTrafficPolicyYAMLFromDB generates BackendTrafficPolicy YAML from database model
func generateBackendTrafficPolicyYAMLFromDB(route *models.Route, domain *models.Domain, policy *models.BackendTrafficPolicy) string {
	if policy == nil || policy.Config.IsEmpty() {
		return ""
	}

	// Build BackendTrafficPolicyConfig
	config := &BackendTrafficPolicyConfig{
		Name:      route.K8sRouteName + "-btp",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: BackendTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Convert compression config
	if len(policy.Config.Compression) > 0 {
		config.Compression = make([]CompressionPolicyConfig, 0, len(policy.Config.Compression))
		for _, comp := range policy.Config.Compression {
			policyComp := CompressionPolicyConfig{
				Type: string(comp.Type),
			}
			switch comp.Type {
			case models.CompressionTypeGzip:
				policyComp.Gzip = &GzipPolicyConfig{}
			case models.CompressionTypeBrotli:
				policyComp.Brotli = &BrotliPolicyConfig{}
			case models.CompressionTypeZstd:
				policyComp.Zstd = &ZstdPolicyConfig{}
			}
			config.Compression = append(config.Compression, policyComp)
		}
	}

	// Convert retry config
	if policy.Config.Retry != nil {
		config.Retry = mapRetryConfigToPolicy(policy.Config.Retry)
	}

	// Convert load balancer config
	if policy.Config.LoadBalancer != nil {
		config.LoadBalancer = mapLoadBalancerConfigToPolicy(policy.Config.LoadBalancer)
	}

	// Convert circuit breaker config
	if policy.Config.CircuitBreaker != nil {
		config.CircuitBreaker = mapCircuitBreakerConfigToPolicy(policy.Config.CircuitBreaker)
	}

	// Convert health check config
	if policy.Config.HealthCheck != nil {
		config.HealthCheck = mapHealthCheckConfigToPolicy(policy.Config.HealthCheck)
	}

	// Convert fault injection config
	if policy.Config.FaultInjection != nil {
		config.FaultInjection = mapFaultInjectionConfigToPolicy(policy.Config.FaultInjection)
	}

	// Convert rate limit config
	if policy.Config.RateLimit != nil {
		config.RateLimit = mapRateLimitConfigToPolicy(policy.Config.RateLimit)
	}

	// Convert request buffer config
	if policy.Config.RequestBuffer != nil {
		config.RequestBuffer = &RequestBufferPolicyConfig{
			Limit: policy.Config.RequestBuffer.Limit,
		}
	}

	// Convert response override config
	if len(policy.Config.ResponseOverride) > 0 {
		config.ResponseOverride = mapResponseOverrideToPolicy(policy.Config.ResponseOverride)
	}

	// Add timeout configuration
	if policy.Config.Timeout != nil {
		config.Timeout = mapTimeoutConfigToPolicy(policy.Config.Timeout)
	}

	// Build the BackendTrafficPolicy object
	backendTrafficPolicy := BuildBackendTrafficPolicy(config)
	if backendTrafficPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(backendTrafficPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating BackendTrafficPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// mapResponseOverrideToPolicy converts model ResponseOverrideRule to policy config
func mapResponseOverrideToPolicy(rules []models.ResponseOverrideRule) []ResponseOverridePolicyConfig {
	result := make([]ResponseOverridePolicyConfig, 0, len(rules))
	for _, rule := range rules {
		statusCodes := make([]StatusCodeMatchPolicyConfig, 0, len(rule.Match.StatusCodes))
		for _, sc := range rule.Match.StatusCodes {
			match := StatusCodeMatchPolicyConfig{Type: sc.Type, Value: sc.Value}
			if sc.Range != nil {
				match.Range = &StatusCodeRangePolicyConfig{Start: sc.Range.Start, End: sc.Range.End}
			}
			statusCodes = append(statusCodes, match)
		}

		body := ResponseOverrideBodyPolicyConfig{Type: rule.Response.Body.Type, Inline: rule.Response.Body.Inline}
		if rule.Response.Body.ValueRef != nil {
			body.ValueRef = &ValueRefPolicyConfig{
				Group:     rule.Response.Body.ValueRef.Group,
				Kind:      rule.Response.Body.ValueRef.Kind,
				Name:      rule.Response.Body.ValueRef.Name,
				Namespace: rule.Response.Body.ValueRef.Namespace,
			}
		}

		result = append(result, ResponseOverridePolicyConfig{
			Match:    ResponseOverrideMatchPolicyConfig{StatusCodes: statusCodes},
			Response: ResponseOverrideResponsePolicyConfig{ContentType: rule.Response.ContentType, Body: body},
		})
	}
	return result
}

// mapTimeoutConfigToPolicy converts model BTPTimeoutConfig to k8s-side BTPTimeoutPolicyConfig
func mapTimeoutConfigToPolicy(t *models.BTPTimeoutConfig) *BTPTimeoutPolicyConfig {
	if t == nil {
		return nil
	}
	result := &BTPTimeoutPolicyConfig{}
	if t.TCP != nil {
		result.TCP = &BTPTCPTimeoutPolicyConfig{
			ConnectTimeout: t.TCP.ConnectTimeout,
		}
	}
	if t.HTTP != nil {
		result.HTTP = &BTPHTTPTimeoutPolicyConfig{
			RequestTimeout:        t.HTTP.RequestTimeout,
			ConnectionIdleTimeout: t.HTTP.ConnectionIdleTimeout,
			MaxConnectionDuration: t.HTTP.MaxConnectionDuration,
			MaxStreamDuration:     t.HTTP.MaxStreamDuration,
		}
	}
	return result
}

// generateEnvoyExtensionPolicyYAML generates EnvoyExtensionPolicy YAML from input
func generateEnvoyExtensionPolicyYAML(route *models.Route, domain *models.Domain, extInput *EnvoyExtensionPolicyInput) string {
	if extInput == nil || !extInput.HasContent() {
		return ""
	}

	// Build EnvoyExtensionPolicyK8sConfig
	config := &EnvoyExtensionPolicyK8sConfig{
		Name:      route.K8sRouteName + "-eep",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Convert Lua extension
	if extInput.Lua != nil {
		luaConfig := LuaExtensionPolicyConfig{
			Type:   extInput.Lua.Type,
			Inline: extInput.Lua.Inline,
		}
		if extInput.Lua.ValueRef != nil {
			luaConfig.ValueRef = &ValueRefPolicyConfig{
				Group:     extInput.Lua.ValueRef.Group,
				Kind:      extInput.Lua.ValueRef.Kind,
				Name:      extInput.Lua.ValueRef.Name,
				Namespace: extInput.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Convert Wasm extension
	if extInput.Wasm != nil {
		wasmConfig := WasmExtensionPolicyConfig{
			Name:   extInput.Wasm.Name,
			RootID: extInput.Wasm.RootID,
			Code: WasmCodeSourcePolicyConfig{
				Type: extInput.Wasm.Code.Type,
			},
			Config: extInput.Wasm.Config,
		}
		if extInput.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &WasmHTTPSourcePolicyConfig{
				URL:    extInput.Wasm.Code.HTTP.URL,
				SHA256: extInput.Wasm.Code.HTTP.SHA256,
			}
		}
		if extInput.Wasm.Code.Image != nil {
			imageConfig := &WasmImageSourcePolicyConfig{
				URL:    extInput.Wasm.Code.Image.URL,
				SHA256: extInput.Wasm.Code.Image.SHA256,
			}
			if extInput.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &ValueRefPolicyConfig{
					Group:     extInput.Wasm.Code.Image.PullSecret.Group,
					Kind:      extInput.Wasm.Code.Image.PullSecret.Kind,
					Name:      extInput.Wasm.Code.Image.PullSecret.Name,
					Namespace: extInput.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Add ExtProc extension
	if extInput != nil && extInput.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, buildExtProcPolicyConfig(extInput.ExtProc))
	}

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := BuildEnvoyExtensionPolicy(config)
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

// generateEnvoyExtensionPolicyYAMLFromDB generates EnvoyExtensionPolicy YAML from database model
func generateEnvoyExtensionPolicyYAMLFromDB(route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy) string {
	if policy == nil || policy.Config.IsEmpty() {
		return ""
	}

	// Build EnvoyExtensionPolicyK8sConfig
	config := &EnvoyExtensionPolicyK8sConfig{
		Name:      route.K8sRouteName + "-eep",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Convert Lua extension
	if policy.Config.Lua != nil {
		luaConfig := LuaExtensionPolicyConfig{
			Type:   policy.Config.Lua.Type,
			Inline: policy.Config.Lua.Inline,
		}
		if policy.Config.Lua.ValueRef != nil {
			luaConfig.ValueRef = &ValueRefPolicyConfig{
				Group:     policy.Config.Lua.ValueRef.Group,
				Kind:      policy.Config.Lua.ValueRef.Kind,
				Name:      policy.Config.Lua.ValueRef.Name,
				Namespace: policy.Config.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Convert Wasm extension
	if policy.Config.Wasm != nil {
		wasmConfig := WasmExtensionPolicyConfig{
			Name:   policy.Config.Wasm.Name,
			RootID: policy.Config.Wasm.RootID,
			Code: WasmCodeSourcePolicyConfig{
				Type: policy.Config.Wasm.Code.Type,
			},
			Config: policy.Config.Wasm.Config,
		}
		if policy.Config.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &WasmHTTPSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.HTTP.URL,
				SHA256: policy.Config.Wasm.Code.HTTP.SHA256,
			}
		}
		if policy.Config.Wasm.Code.Image != nil {
			imageConfig := &WasmImageSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.Image.URL,
				SHA256: policy.Config.Wasm.Code.Image.SHA256,
			}
			if policy.Config.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &ValueRefPolicyConfig{
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

	// Add ExtProc extension
	if policy.Config.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, buildExtProcPolicyConfig(policy.Config.ExtProc))
	}

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := BuildEnvoyExtensionPolicy(config)
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

// generateEnvoyExtensionPolicyYAMLWithWaf generates EnvoyExtensionPolicy YAML from input with WAF support
func generateEnvoyExtensionPolicyYAMLWithWaf(route *models.Route, domain *models.Domain, extInput *EnvoyExtensionPolicyInput, wafInput *WafPolicyInput) string {
	// Check if we have any content
	hasExtensions := extInput != nil && extInput.HasContent()
	hasWaf := wafInput != nil && wafInput.Mode != ""

	if !hasExtensions && !hasWaf {
		return ""
	}

	// Build EnvoyExtensionPolicyK8sConfig
	config := &EnvoyExtensionPolicyK8sConfig{
		Name:      route.K8sRouteName + "-eep",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Convert Lua extension
	if hasExtensions && extInput.Lua != nil {
		luaConfig := LuaExtensionPolicyConfig{
			Type:   extInput.Lua.Type,
			Inline: extInput.Lua.Inline,
		}
		if extInput.Lua.ValueRef != nil {
			luaConfig.ValueRef = &ValueRefPolicyConfig{
				Group:     extInput.Lua.ValueRef.Group,
				Kind:      extInput.Lua.ValueRef.Kind,
				Name:      extInput.Lua.ValueRef.Name,
				Namespace: extInput.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Convert Wasm extension
	if hasExtensions && extInput.Wasm != nil {
		wasmConfig := WasmExtensionPolicyConfig{
			Name:   extInput.Wasm.Name,
			RootID: extInput.Wasm.RootID,
			Code: WasmCodeSourcePolicyConfig{
				Type: extInput.Wasm.Code.Type,
			},
			Config: extInput.Wasm.Config,
		}
		if extInput.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &WasmHTTPSourcePolicyConfig{
				URL:    extInput.Wasm.Code.HTTP.URL,
				SHA256: extInput.Wasm.Code.HTTP.SHA256,
			}
		}
		if extInput.Wasm.Code.Image != nil {
			imageConfig := &WasmImageSourcePolicyConfig{
				URL:    extInput.Wasm.Code.Image.URL,
				SHA256: extInput.Wasm.Code.Image.SHA256,
			}
			if extInput.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &ValueRefPolicyConfig{
					Group:     extInput.Wasm.Code.Image.PullSecret.Group,
					Kind:      extInput.Wasm.Code.Image.PullSecret.Kind,
					Name:      extInput.Wasm.Code.Image.PullSecret.Name,
					Namespace: extInput.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Add ExtProc extension
	if hasExtensions && extInput.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, buildExtProcPolicyConfig(extInput.ExtProc))
	}

	// Add WAF (coraza) WASM entry if WAF is configured
	if hasWaf {
		// Build a temporary WafPolicyConfig to use BuildCorazaDirectives
		wafConfig := &models.WafPolicyConfig{
			Mode:             wafInput.Mode,
			Rulesets:         wafInput.Rulesets,
			AnomalyThreshold: wafInput.AnomalyThreshold,
			ParanoiaLevel:    wafInput.ParanoiaLevel,
			DisabledRuleIDs:  wafInput.DisabledRuleIDs,
			CustomDirectives: wafInput.CustomDirectives,
		}
		corazaConfig, err := BuildCorazaDirectives(wafConfig)
		if err == nil && corazaConfig != "" {
			wasmConfig := WasmExtensionPolicyConfig{
				Name:   "coraza-waf",
				RootID: "",
				Code: WasmCodeSourcePolicyConfig{
					Type: "Image",
					Image: &WasmImageSourcePolicyConfig{
						URL: getCorazaWasmImageURL(),
					},
				},
				Config: &corazaConfig,
			}
			config.Wasm = append(config.Wasm, wasmConfig)
		}
	}

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := BuildEnvoyExtensionPolicy(config)
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

// generateEnvoyExtensionPolicyYAMLFromDBWithWaf generates EnvoyExtensionPolicy YAML from database models with WAF support
func (s *RouteService) generateEnvoyExtensionPolicyYAMLFromDBWithWaf(route *models.Route, domain *models.Domain, policy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy) string {
	// Use buildEnvoyExtensionPolicyConfig which already handles WAF merging
	config := s.buildEnvoyExtensionPolicyConfig(route, domain, policy, wafPolicy)
	if config == nil {
		return ""
	}

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := BuildEnvoyExtensionPolicy(config)
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

// generateEnvoyExtensionPolicyYAMLFromSnapshot generates EnvoyExtensionPolicy YAML from policy models
// This is a standalone function that can be called from approval_service.go for YAML diff generation
func generateEnvoyExtensionPolicyYAMLFromSnapshot(route *models.Route, domain *models.Domain, extPolicy *models.EnvoyExtensionPolicy, wafPolicy *models.WafPolicy) string {
	// Check if we have any extensions to deploy
	hasGenericExtensions := extPolicy != nil && !extPolicy.Config.IsEmpty()
	hasWaf := wafPolicy != nil && !wafPolicy.Config.IsEmpty()

	if !hasGenericExtensions && !hasWaf {
		return ""
	}

	config := &EnvoyExtensionPolicyK8sConfig{
		Name:      route.K8sRouteName + "-eep",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  route.K8sRouteName,
		},
	}

	// Add Lua extension configuration (only if policy exists)
	if hasGenericExtensions && extPolicy.Config.Lua != nil {
		luaConfig := LuaExtensionPolicyConfig{
			Type:   extPolicy.Config.Lua.Type,
			Inline: extPolicy.Config.Lua.Inline,
		}
		if extPolicy.Config.Lua.ValueRef != nil {
			luaConfig.ValueRef = &ValueRefPolicyConfig{
				Group:     extPolicy.Config.Lua.ValueRef.Group,
				Kind:      extPolicy.Config.Lua.ValueRef.Kind,
				Name:      extPolicy.Config.Lua.ValueRef.Name,
				Namespace: extPolicy.Config.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Add Wasm extension configuration (only if policy exists)
	if hasGenericExtensions && extPolicy.Config.Wasm != nil {
		wasmConfig := WasmExtensionPolicyConfig{
			Name:   extPolicy.Config.Wasm.Name,
			RootID: extPolicy.Config.Wasm.RootID,
			Code: WasmCodeSourcePolicyConfig{
				Type: extPolicy.Config.Wasm.Code.Type,
			},
			Config: extPolicy.Config.Wasm.Config,
		}
		if extPolicy.Config.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &WasmHTTPSourcePolicyConfig{
				URL:    extPolicy.Config.Wasm.Code.HTTP.URL,
				SHA256: extPolicy.Config.Wasm.Code.HTTP.SHA256,
			}
		}
		if extPolicy.Config.Wasm.Code.Image != nil {
			imageConfig := &WasmImageSourcePolicyConfig{
				URL:    extPolicy.Config.Wasm.Code.Image.URL,
				SHA256: extPolicy.Config.Wasm.Code.Image.SHA256,
			}
			if extPolicy.Config.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &ValueRefPolicyConfig{
					Group:     extPolicy.Config.Wasm.Code.Image.PullSecret.Group,
					Kind:      extPolicy.Config.Wasm.Code.Image.PullSecret.Kind,
					Name:      extPolicy.Config.Wasm.Code.Image.PullSecret.Name,
					Namespace: extPolicy.Config.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Add ExtProc extension configuration (only if policy exists)
	if hasGenericExtensions && extPolicy.Config.ExtProc != nil {
		config.ExtProc = append(config.ExtProc, buildExtProcPolicyConfig(extPolicy.Config.ExtProc))
	}

	// Add WAF (coraza) WASM entry if WAF is configured
	if hasWaf {
		corazaConfig, err := BuildCorazaDirectives(&wafPolicy.Config)
		if err == nil && corazaConfig != "" {
			wasmConfig := WasmExtensionPolicyConfig{
				Name:   "coraza-waf",
				RootID: "",
				Code: WasmCodeSourcePolicyConfig{
					Type: "Image",
					Image: &WasmImageSourcePolicyConfig{
						URL: getCorazaWasmImageURL(),
					},
				},
				Config: &corazaConfig,
			}
			config.Wasm = append(config.Wasm, wasmConfig)
		}
	}

	// Build the EnvoyExtensionPolicy object
	extensionPolicy := BuildEnvoyExtensionPolicy(config)
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

// buildHTTPRouteConfigForYAML builds HTTPRouteConfig for YAML generation
// This is similar to buildHTTPRouteConfig but can be called without RouteService
func buildHTTPRouteConfigForYAML(route *models.Route, domain *models.Domain) *HTTPRouteConfig {
	rules := make([]HTTPRouteRule, 0, len(route.Config.Matches))

	for _, match := range route.Config.Matches {
		rule := HTTPRouteRule{
			BackendRefs: make([]BackendRef, 0, len(route.Config.Backends)),
		}

		// Path matching
		if match.Path != nil {
			rule.PathType = convertPathTypeToGatewayAPI(string(match.Path.Type))
			rule.PathValue = match.Path.Value
		}

		// Header matching
		if len(match.Headers) > 0 {
			rule.Headers = make([]HeaderMatch, 0, len(match.Headers))
			for _, h := range match.Headers {
				rule.Headers = append(rule.Headers, HeaderMatch{
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
			rule.QueryParams = make([]QueryParamMatch, 0, len(match.QueryParams))
			for _, qp := range match.QueryParams {
				rule.QueryParams = append(rule.QueryParams, QueryParamMatch{
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
					rule.BackendRefs = append(rule.BackendRefs, BackendRef{
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
					rule.BackendRefs = append(rule.BackendRefs, BackendRef{
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

	config := &HTTPRouteConfig{
		Name:        route.K8sRouteName,
		Namespace:   domain.Namespace,
		GatewayName: domain.K8sGatewayName,
		GatewayID:   domain.ID.String(),
		RouteID:     route.ID.String(),
		Hostname:    domain.Hostname,
		Rules:       rules,
	}

	// Add header modifiers
	if route.Config.RequestHeaderModifier != nil {
		config.RequestHeaderModifier = convertHeaderModifier(route.Config.RequestHeaderModifier)
	}
	if route.Config.ResponseHeaderModifier != nil {
		config.ResponseHeaderModifier = convertHeaderModifier(route.Config.ResponseHeaderModifier)
	}

	// Add URL rewrite (only for non-redirect routes - redirect and rewrite are mutually exclusive)
	if route.Config.URLRewrite != nil && route.Config.Redirect == nil {
		config.URLRewrite = convertURLRewrite(route.Config.URLRewrite)
	}

	// Add redirect
	if route.Config.Redirect != nil {
		config.Redirect = convertRedirect(route.Config.Redirect)
	}

	// Add mirror refs (only for backend routes, not redirect or direct response)
	if route.Config.Redirect == nil && route.Config.DirectResponse == nil && len(route.Config.Mirrors) > 0 {
		config.Mirrors = make([]MirrorRef, 0, len(route.Config.Mirrors))
		for _, mirror := range route.Config.Mirrors {
			config.Mirrors = append(config.Mirrors, MirrorRef{
				Name:      mirror.Service,
				Namespace: mirror.Namespace,
				Port:      mirror.Port,
			})
		}
	}

	// Note: CORS is now handled via SecurityPolicy (separate from HTTPRoute)

	return config
}

// buildGRPCRouteConfigForYAML builds GRPCRouteConfig for YAML generation (standalone, no RouteService)
func buildGRPCRouteConfigForYAML(route *models.Route, domain *models.Domain) *GRPCRouteConfig {
	rules := make([]GRPCRouteRule, 0)

	for _, match := range route.Config.Matches {
		rule := GRPCRouteRule{}

		if match.GRPCService != nil {
			rule.GRPCService = &GRPCMethodMatchConfig{
				Type:  match.GRPCService.Type,
				Value: match.GRPCService.Value,
			}
		}
		if match.GRPCMethod != nil {
			rule.GRPCMethod = &GRPCMethodMatchConfig{
				Type:  match.GRPCMethod.Type,
				Value: match.GRPCMethod.Value,
			}
		}

		if len(match.Headers) > 0 {
			rule.Headers = make([]HeaderMatch, 0, len(match.Headers))
			for _, h := range match.Headers {
				rule.Headers = append(rule.Headers, HeaderMatch{
					Name:  h.Name,
					Type:  h.Type,
					Value: h.Value,
				})
			}
		}

		hasFailover := route.Config.HasFailover()
		for i, backend := range route.Config.Backends {
			if backend.Type == models.BackendTypeExternal || hasFailover || backend.TLS != nil {
				backendName := fmt.Sprintf("%s-backend-%d", route.K8sRouteName, i)
				rule.BackendRefs = append(rule.BackendRefs, BackendRef{
					Name:       backendName,
					Namespace:  domain.Namespace,
					Port:       backend.Port,
					Weight:     backend.Weight,
					IsExternal: true,
					Group:      "gateway.envoyproxy.io",
					Kind:       "Backend",
				})
			} else {
				rule.BackendRefs = append(rule.BackendRefs, BackendRef{
					Name:      backend.Service,
					Namespace: backend.Namespace,
					Port:      backend.Port,
					Weight:    backend.Weight,
				})
			}
		}

		rules = append(rules, rule)
	}

	if len(rules) == 0 && len(route.Config.Backends) > 0 {
		rule := GRPCRouteRule{}
		hasFailover := route.Config.HasFailover()
		for i, backend := range route.Config.Backends {
			if backend.Type == models.BackendTypeExternal || hasFailover || backend.TLS != nil {
				backendName := fmt.Sprintf("%s-backend-%d", route.K8sRouteName, i)
				rule.BackendRefs = append(rule.BackendRefs, BackendRef{
					Name:       backendName,
					Namespace:  domain.Namespace,
					Port:       backend.Port,
					Weight:     backend.Weight,
					IsExternal: true,
					Group:      "gateway.envoyproxy.io",
					Kind:       "Backend",
				})
			} else {
				rule.BackendRefs = append(rule.BackendRefs, BackendRef{
					Name:      backend.Service,
					Namespace: backend.Namespace,
					Port:      backend.Port,
					Weight:    backend.Weight,
				})
			}
		}
		rules = append(rules, rule)
	}

	config := &GRPCRouteConfig{
		Name:        route.K8sRouteName,
		Namespace:   domain.Namespace,
		GatewayName: domain.K8sGatewayName,
		GatewayID:   domain.ID.String(),
		RouteID:     route.ID.String(),
		Hostname:    domain.Hostname,
		Rules:       rules,
	}

	if route.Config.RequestHeaderModifier != nil {
		config.RequestHeaderModifier = convertHeaderModifier(route.Config.RequestHeaderModifier)
	}
	if route.Config.ResponseHeaderModifier != nil {
		config.ResponseHeaderModifier = convertHeaderModifier(route.Config.ResponseHeaderModifier)
	}

	if len(route.Config.Mirrors) > 0 {
		config.Mirrors = make([]MirrorRef, 0, len(route.Config.Mirrors))
		for _, m := range route.Config.Mirrors {
			config.Mirrors = append(config.Mirrors, MirrorRef{
				Name:      m.Service,
				Namespace: m.Namespace,
				Port:      m.Port,
			})
		}
	}

	return config
}

// ClientAuthCategory represents the auth type for a client attachment
type ClientAuthCategory struct {
	ClientID           uuid.UUID
	ClientName         string
	EnableIP           bool
	EnableAPIKey       bool
	EnableJWT          bool
	EnableMTLS         bool
	APIKey             string   // Plaintext API key from K8s Secret (only for API key clients)
	APIKeyHeaderName   string   // Header to extract API key from (e.g., "x-api-key")
	ClientIDHeaderName string   // Header for client identification/routing (e.g., "x-client-id")
	IPCIDRs            []string // Client's IP CIDRs (only for IP clients)
	// JWT fields
	JWTIssuer         string                    // JWT issuer (iss claim)
	JWTJWKSURL        string                    // URL to fetch JWKS
	JWTAudiences      []string                  // Allowed audiences (aud claim)
	JWTRequiredClaims []models.JWTRequiredClaim // Required claims for authorization
	JWTClaimToHeaders []models.JWTClaimToHeader // Map claims to headers
	// Header/Method fields
	EnableHeaderAuth bool
	HeaderMatches    []models.AuthorizationHeaderMatch // Client's headers for authorization
	AllowedMethods   []string                          // Client-level allowed methods
	// mTLS fields
	MTLSSANs   []models.MTLSSANEntry // Client SANs for XFCC matching
	MTLSHashes []string              // Client certificate hashes for XFCC matching
	MTLSCAPem  string                // Client CA PEM for creating K8s secret at deploy time
	// Rate limit config from attachment
	RateLimitConfig *models.RateLimitConfig
	// External auth config from attachment
	ExtAuth            *models.ExtAuthConfig
	ExtAuthBackendName string // Name of Backend CRD for ext-auth (set during deployment)
}

// buildMTLSXFCCHeaderMatches builds a single XFCC header match for mTLS client routing.
// Multiple hashes/SANs are combined into one regex with alternation (OR logic)
// so that any one of the client's certificate identifiers can match.
func buildMTLSXFCCHeaderMatches(client ClientAuthCategory) []HeaderMatch {
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
	return []HeaderMatch{{
		Name:  "x-forwarded-client-cert",
		Type:  "RegularExpression",
		Value: ".*(" + strings.Join(patterns, "|") + ").*",
	}}
}

// categorizeClientAttachments groups attachments by auth type for deployment
// Returns:
// - ipOnlyClients: IP allowlisting only (goes to base route)
// - apiKeyOnlyClients: API key or JWT without IP (per-client route, no IP check)
// - bothClients: API key or JWT with IP (per-client route with IP check)
func (s *RouteService) categorizeClientAttachments(ctx context.Context, routeID uuid.UUID, domain *models.Domain) (ipOnlyClients, apiKeyOnlyClients, bothClients []ClientAuthCategory, err error) {
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

		cat := ClientAuthCategory{
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
					cat.IPCIDRs = append(cat.IPCIDRs, normalizeCIDR(ip.CIDR))
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
func (s *RouteService) deployAPIKeyRoutes(ctx context.Context, route *models.Route, domain *models.Domain, clients []ClientAuthCategory, requireIP bool) error {
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
			backendName := GenerateExtAuthBackendName(route.ID.String(), client.ClientID.String())
			var backendRef models.ExtAuthBackendRef
			if client.ExtAuth.Type == "http" && client.ExtAuth.HTTP != nil {
				backendRef = client.ExtAuth.HTTP.BackendRef
			} else if client.ExtAuth.Type == "grpc" && client.ExtAuth.GRPC != nil {
				backendRef = client.ExtAuth.GRPC.BackendRef
			}
			if backendRef.Name != "" {
				backendConfig := &ExtAuthBackendConfig{
					Name:      backendName,
					Namespace: domain.Namespace,
					GatewayID: domain.ID.String(),
					RouteID:   route.ID.String(),
					ClientID:  client.ClientID.String(),
					Service:   backendRef,
				}
				extAuthBackend := BuildExtAuthBackend(backendConfig)
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
		btpConfig := s.buildAPIKeyBackendTrafficPolicyConfig(route, domain, *client, policy)
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
func (s *RouteService) buildAPIKeyHTTPRouteConfig(route *models.Route, domain *models.Domain, client ClientAuthCategory) *HTTPRouteConfig {
	// Get the base config
	baseConfig := s.buildHTTPRouteConfig(route, domain)

	// Modify name to include client ID prefix
	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]
	baseConfig.Name = routeName
	baseConfig.RouteID = route.ID.String() // Keep original route ID for labeling

	// Add header match on CLIENT ID (for API key, JWT, and mTLS clients)
	if client.EnableAPIKey || client.EnableJWT || client.EnableMTLS {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, HeaderMatch{
				Name:  client.ClientIDHeaderName,
				Type:  "Exact",
				Value: client.ClientID.String(),
			})
		}
	}

	// Add XFCC header matches (for mTLS clients - additional cert verification)
	xfccMatches := buildMTLSXFCCHeaderMatches(client)
	if len(xfccMatches) > 0 {
		for i := range baseConfig.Rules {
			baseConfig.Rules[i].Headers = append(baseConfig.Rules[i].Headers, xfccMatches...)
		}
	}

	// Add client identification headers for backend enrichment
	if baseConfig.RequestHeaderModifier == nil {
		baseConfig.RequestHeaderModifier = &HTTPHeaderModifier{}
	}
	baseConfig.RequestHeaderModifier.Add = append(baseConfig.RequestHeaderModifier.Add,
		HTTPHeaderValue{Name: "X-Client-ID", Value: client.ClientID.String()},
		HTTPHeaderValue{Name: "X-Client-Name", Value: client.ClientName},
	)

	return baseConfig
}

// buildAPIKeySecurityPolicyConfig builds SecurityPolicy for a client with API key and/or JWT auth
func (s *RouteService) buildAPIKeySecurityPolicyConfig(route *models.Route, domain *models.Domain, client ClientAuthCategory, requireIP bool, secPolicy *models.SecurityPolicy) *SecurityPolicyConfig {
	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]

	config := &SecurityPolicyConfig{
		Name:      routeName + "-security",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: SecurityPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  routeName,
		},
	}

	// Copy CORS config from route's SecurityPolicy (if any)
	if secPolicy != nil && secPolicy.Config.CORS != nil {
		config.CORS = &CORSPolicyConfig{
			AllowOrigins:     secPolicy.Config.CORS.AllowOrigins,
			AllowMethods:     secPolicy.Config.CORS.AllowMethods,
			AllowHeaders:     secPolicy.Config.CORS.AllowHeaders,
			ExposeHeaders:    secPolicy.Config.CORS.ExposeHeaders,
			MaxAge:           secPolicy.Config.CORS.MaxAge,
			AllowCredentials: secPolicy.Config.CORS.AllowCredentials,
		}
	}

	// Add API key auth config if enabled
	if client.EnableAPIKey && client.APIKey != "" {
		secretName := s.k8sService.GetAPIKeySecretName(client.ClientID)
		config.APIKeyAuth = &APIKeyAuthPolicyConfig{
			CredentialRefs: []SecretRefConfig{
				{Name: secretName, Namespace: FastGatewayNamespace},
			},
			ExtractFrom: []APIKeyExtractFromConfig{
				{Headers: []string{client.APIKeyHeaderName}},
			},
		}
	}

	// Add JWT auth config if enabled
	if client.EnableJWT && client.JWTIssuer != "" {
		providerName := "client-" + client.ClientID.String()[:8]
		provider := JWTProviderPolicyConfig{
			Name:      providerName,
			Issuer:    client.JWTIssuer,
			JWKSURL:   client.JWTJWKSURL,
			Audiences: client.JWTAudiences,
		}

		// Add claim-to-headers if configured
		if len(client.JWTClaimToHeaders) > 0 {
			for _, cth := range client.JWTClaimToHeaders {
				provider.ClaimToHeaders = append(provider.ClaimToHeaders, JWTClaimToHeaderPolicyConfig{
					Claim:  cth.Claim,
					Header: cth.Header,
				})
			}
		}

		config.JWT = &JWTAuthPolicyConfig{
			Providers: []JWTProviderPolicyConfig{provider},
		}

		// Add JWT claim authorization if required claims are set
		if len(client.JWTRequiredClaims) > 0 {
			jwtPrincipal := &JWTPrincipalPolicyConfig{
				Provider: providerName,
			}
			for _, claim := range client.JWTRequiredClaims {
				jwtPrincipal.Claims = append(jwtPrincipal.Claims, JWTClaimRulePolicyConfig{
					Name:      claim.Name,
					Values:    claim.Values,
					ValueType: claim.ValueType,
				})
			}

			// If authorization already exists (from IP), add JWT to it
			// Otherwise create new authorization
			if config.Authorization == nil {
				config.Authorization = &AuthorizationPolicyConfig{
					DefaultAction: "Deny",
					Rules:         []AuthorizationRulePolicyConfig{},
				}
			}

			// Add JWT claim rule
			rule := AuthorizationRulePolicyConfig{
				Action: "Allow",
				JWT:    jwtPrincipal,
			}

			// If IP is also required, add CIDRs to the same rule (AND logic)
			if requireIP && len(client.IPCIDRs) > 0 {
				rule.ClientCIDRs = client.IPCIDRs
			}

			config.Authorization.Rules = append(config.Authorization.Rules, rule)
		} else if requireIP && len(client.IPCIDRs) > 0 {
			// JWT without required claims but with IP check
			config.Authorization = &AuthorizationPolicyConfig{
				DefaultAction: "Deny",
				Rules: []AuthorizationRulePolicyConfig{
					{
						Action:      "Allow",
						ClientCIDRs: client.IPCIDRs,
					},
				},
			}
		}
	} else if requireIP && len(client.IPCIDRs) > 0 {
		// No JWT, just API key with IP check
		config.Authorization = &AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []AuthorizationRulePolicyConfig{
				{
					Action:      "Allow",
					ClientCIDRs: client.IPCIDRs,
				},
			},
		}
	}

	// Add header/method authorization to existing rules (AND logic with other principals)
	hasHeaderOrMethod := (client.EnableHeaderAuth && len(client.HeaderMatches) > 0) || len(client.AllowedMethods) > 0
	if hasHeaderOrMethod {
		// Ensure authorization config exists
		if config.Authorization == nil {
			config.Authorization = &AuthorizationPolicyConfig{
				DefaultAction: "Deny",
				Rules:         []AuthorizationRulePolicyConfig{{Action: "Allow"}},
			}
		}

		// Add headers and methods to each rule
		for i := range config.Authorization.Rules {
			if client.EnableHeaderAuth && len(client.HeaderMatches) > 0 {
				for _, h := range client.HeaderMatches {
					config.Authorization.Rules[i].Headers = append(config.Authorization.Rules[i].Headers, HeaderMatchPolicyConfig{
						Name:   h.Name,
						Values: h.Values,
					})
				}
			}
			if len(client.AllowedMethods) > 0 {
				config.Authorization.Rules[i].Methods = client.AllowedMethods
			}
		}
	}

	// Add ExtAuth config if client has ext-auth configured
	if client.ExtAuth != nil && client.ExtAuthBackendName != "" {
		config.ExtAuth = client.ExtAuth
		config.ExtAuthBackendName = client.ExtAuthBackendName
	}

	return config
}

// buildAPIKeyBackendTrafficPolicyConfig builds BackendTrafficPolicy for a client with API key auth
func (s *RouteService) buildAPIKeyBackendTrafficPolicyConfig(route *models.Route, domain *models.Domain, client ClientAuthCategory, policy *models.BackendTrafficPolicy) *BackendTrafficPolicyConfig {
	hasBasePolicy := policy != nil && !policy.Config.IsEmpty()
	hasRateLimit := client.RateLimitConfig != nil

	if !hasBasePolicy && !hasRateLimit {
		return nil
	}

	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]

	config := &BackendTrafficPolicyConfig{
		Name:      routeName + "-btp",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: BackendTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  routeName,
		},
	}

	// Copy base policy features if present
	if hasBasePolicy {
		// Add compression configuration
		if len(policy.Config.Compression) > 0 {
			config.Compression = make([]CompressionPolicyConfig, 0, len(policy.Config.Compression))
			for _, comp := range policy.Config.Compression {
				policyComp := CompressionPolicyConfig{
					Type: string(comp.Type),
				}
				switch comp.Type {
				case models.CompressionTypeGzip:
					policyComp.Gzip = &GzipPolicyConfig{}
				case models.CompressionTypeBrotli:
					policyComp.Brotli = &BrotliPolicyConfig{}
				case models.CompressionTypeZstd:
					policyComp.Zstd = &ZstdPolicyConfig{}
				}
				config.Compression = append(config.Compression, policyComp)
			}
		}

		// Add retry configuration
		if policy.Config.Retry != nil {
			config.Retry = mapRetryConfigToPolicy(policy.Config.Retry)
		}

		// Add load balancer configuration
		if policy.Config.LoadBalancer != nil {
			config.LoadBalancer = mapLoadBalancerConfigToPolicy(policy.Config.LoadBalancer)
		}

		// Add circuit breaker configuration
		if policy.Config.CircuitBreaker != nil {
			config.CircuitBreaker = mapCircuitBreakerConfigToPolicy(policy.Config.CircuitBreaker)
		}

		// Add health check configuration
		if policy.Config.HealthCheck != nil {
			config.HealthCheck = mapHealthCheckConfigToPolicy(policy.Config.HealthCheck)
		}

		// Add fault injection configuration
		if policy.Config.FaultInjection != nil {
			config.FaultInjection = mapFaultInjectionConfigToPolicy(policy.Config.FaultInjection)
		}

		// Add rate limit from base policy
		if policy.Config.RateLimit != nil {
			config.RateLimit = mapRateLimitConfigToPolicy(policy.Config.RateLimit)
		}

		// Add request buffer configuration
		if policy.Config.RequestBuffer != nil {
			config.RequestBuffer = &RequestBufferPolicyConfig{
				Limit: policy.Config.RequestBuffer.Limit,
			}
		}

		// Add response override configuration
		if len(policy.Config.ResponseOverride) > 0 {
			config.ResponseOverride = mapResponseOverrideToPolicy(policy.Config.ResponseOverride)
		}

		// Add timeout configuration
		if policy.Config.Timeout != nil {
			config.Timeout = mapTimeoutConfigToPolicy(policy.Config.Timeout)
		}
	}

	// Add rate limit from attachment (overrides base policy rate limit for per-client mode)
	if hasRateLimit {
		config.RateLimit = mapRateLimitConfigToPolicy(client.RateLimitConfig)
	}

	return config
}

// buildAPIKeyEnvoyExtensionPolicyConfig builds EnvoyExtensionPolicy config for a per-client route
func (s *RouteService) buildAPIKeyEnvoyExtensionPolicyConfig(route *models.Route, domain *models.Domain, client ClientAuthCategory, policy *models.EnvoyExtensionPolicy) *unstructured.Unstructured {
	if policy == nil || policy.Config.IsEmpty() {
		return nil
	}

	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]

	config := &EnvoyExtensionPolicyK8sConfig{
		Name:      routeName + "-eep",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   route.ID.String(),
		TargetRef: EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  getRouteKind(route.Protocol),
			Name:  routeName,
		},
	}

	// Copy Lua extension from base policy
	if policy.Config.Lua != nil {
		luaConfig := LuaExtensionPolicyConfig{
			Type:   policy.Config.Lua.Type,
			Inline: policy.Config.Lua.Inline,
		}
		if policy.Config.Lua.ValueRef != nil {
			luaConfig.ValueRef = &ValueRefPolicyConfig{
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
		wasmConfig := WasmExtensionPolicyConfig{
			Name:   policy.Config.Wasm.Name,
			RootID: policy.Config.Wasm.RootID,
			Code: WasmCodeSourcePolicyConfig{
				Type: policy.Config.Wasm.Code.Type,
			},
			Config: policy.Config.Wasm.Config,
		}
		if policy.Config.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &WasmHTTPSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.HTTP.URL,
				SHA256: policy.Config.Wasm.Code.HTTP.SHA256,
			}
		}
		if policy.Config.Wasm.Code.Image != nil {
			imageConfig := &WasmImageSourcePolicyConfig{
				URL:    policy.Config.Wasm.Code.Image.URL,
				SHA256: policy.Config.Wasm.Code.Image.SHA256,
			}
			if policy.Config.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &ValueRefPolicyConfig{
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
		config.ExtProc = append(config.ExtProc, buildExtProcPolicyConfig(policy.Config.ExtProc))
	}

	return BuildEnvoyExtensionPolicy(config)
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
		btpName := routeName + "-btp"
		if err := s.k8sService.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName); err != nil {
			log.Printf("Failed to delete API key BackendTrafficPolicy %s: %v", btpName, err)
		}

		// Delete EnvoyExtensionPolicy
		eepName := routeName + "-eep"
		if err := s.k8sService.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName); err != nil {
			log.Printf("Failed to delete API key EnvoyExtensionPolicy %s: %v", eepName, err)
		}

		// Delete SecurityPolicy
		securityName := routeName + "-security"
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

// GetDomainName returns the domain name for a given domain ID (used for audit enrichment)
func (s *RouteService) GetDomainName(domainID uuid.UUID) (string, error) {
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return "", err
	}
	return domain.Name, nil
}

// GetApprovalIDForEntity returns the most recent approval ID for an entity.
// Checks pending first, then latest approved.
func (s *RouteService) GetApprovalIDForEntity(entityType models.ApprovalEntityType, entityID uuid.UUID) (*uuid.UUID, error) {
	// Try pending first
	pending, err := s.approvalRepo.GetPendingByEntityID(entityType, entityID)
	if err == nil && pending != nil {
		return &pending.ID, nil
	}
	// Fall back to latest approved
	approved, err := s.approvalRepo.GetLatestApprovedByEntityID(entityType, entityID)
	if err == nil && approved != nil {
		return &approved.ID, nil
	}
	return nil, fmt.Errorf("no approval found")
}

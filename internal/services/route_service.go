package services

import (
	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

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
	wafConfig                routeplan.WAFConfig

	// approvals owns approval planning and traversal. Set via
	// SetApprovalEngine at construction. See internal/approval.
	approvals *approvalpkg.Engine

	// state is the sole writer of route.Status. See route_state.go: before
	// Phase 2D the field was assigned at 24 sites with no transition
	// validation at all.
	state *routeStateMachine

	// idgen mints route IDs. Injected so the preview path is deterministic under
	// test: the first 8 hex characters of the ID minted in PreviewCreate are
	// embedded in every previewed resource name. Nil means uuid.New (see
	// newID in route_service_idgen.go).
	idgen func() uuid.UUID
}

// NewRouteService creates a new route service
func NewRouteService(
	routeRepo repository.RouteRepositoryInterface,
	approvalRepo repository.UnifiedApprovalRepositoryInterface,
	policyRepo repository.ApprovalPolicyRepositoryInterface,
	domainRepo repository.DomainRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
	wafConfig routeplan.WAFConfig,
) *RouteService {
	svc := &RouteService{
		routeRepo:    routeRepo,
		approvalRepo: approvalRepo,
		policyRepo:   policyRepo,
		domainRepo:   domainRepo,
		teamRepo:     teamRepo,
		wafConfig:    wafConfig,
	}
	// routeRepo is already a constructor parameter, so the state machine
	// needs no setter of its own.
	svc.state = &routeStateMachine{repo: routeRepo}
	return svc
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

// SetApprovalEngine sets the approval engine (to avoid a circular dependency:
// the engine calls back into RouteService as a Completer).
func (s *RouteService) SetApprovalEngine(e *approvalpkg.Engine) {
	s.approvals = e
}

// SetProjectRepository sets the project repository for approval bypass
func (s *RouteService) SetProjectRepository(repo repository.ProjectRepositoryInterface) {
	s.projectRepo = repo
}

// GetDomainName returns the domain name for a given domain ID (used for audit enrichment)
func (s *RouteService) GetDomainName(domainID uuid.UUID) (string, error) {
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return "", err
	}
	return domain.Name, nil
}

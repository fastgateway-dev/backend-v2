package services

import (
	"context"
	"strings"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/routestate"
	"github.com/google/uuid"
)

// RouteListFilters is the service layer's name for the optional filters a
// project-scoped route listing accepts.
//
// It is an alias, not a copy: RouteService.ListByProjectID hands the value
// straight to the repository, so an alias keeps the two provably identical
// while giving handlers a services-package name to construct. Phase 2F Task 4
// -- route_handler.go imported internal/repository solely to spell this type,
// which is why it was the one handler importing the package without calling a
// repository method.
type RouteListFilters = repository.RouteListFilters

// RouteService handles route business logic
type RouteService struct {
	// state is the sole writer of route.Status. See internal/routestate:
	// before Phase 2D the field was assigned at 24 sites with no transition
	// validation at all.
	state *routestate.Machine

	// idgen mints route IDs. Injected so the preview path is deterministic under
	// test: the first 8 hex characters of the ID minted in PreviewCreate are
	// embedded in every previewed resource name. Nil means uuid.New (see
	// newID in route_assembler.go).
	idgen func() uuid.UUID

	assembler *routeAssembler

	query  *routeQuery
	deploy *routeDeploy
	write  *routeWrite
}

// ClientTrafficPolicyEnsurer re-applies a domain's Envoy Gateway
// ClientTrafficPolicy. RouteService calls it after writing the Kubernetes
// secrets that hold client mTLS CAs, so the policy references the current
// secret set.
//
// RouteService declares it; *DomainService satisfies it structurally through
// the exported EnsureMTLSClientTrafficPolicy method added for controller
// ruling R1. Before Phase 2E route_clients_apikey.go held a *DomainService and
// reached into its unexported settingsRepo field and unexported
// applyEnvoyGatewayClientTrafficPolicy method, which no interface could
// express.
type ClientTrafficPolicyEnsurer interface {
	EnsureMTLSClientTrafficPolicy(ctx context.Context, domain *models.Domain) error
}

// RouteVersionRecorder snapshots a route as a new numbered version after a
// successful deploy. CreateVersion is the only method RouteService calls on
// RouteVersionService (route_deploy.go).
//
// RouteService declares it; *RouteVersionService satisfies it structurally.
// The two services depend on each other -- RouteVersionService resubmits a
// historical config through RouteUpdater -- so main.go orders the two
// constructions with a RouteUpdaterFunc closure rather than a setter.
type RouteVersionRecorder interface {
	CreateVersion(route *models.Route, approval *models.Approval, deployedBy uuid.UUID) error
}

// RouteServiceDeps carries everything RouteService needs. Every field is
// required unless its comment says otherwise: before Phase 2E these arrived
// through fourteen setters, and thirty-seven nil-guards existed across the
// package to tolerate the ones that might not have been called.
type RouteServiceDeps struct {
	RouteRepo                repository.RouteRepositoryInterface
	ApprovalRepo             repository.UnifiedApprovalRepositoryInterface
	PolicyRepo               repository.ApprovalPolicyRepositoryInterface
	DomainRepo               repository.DomainRepositoryInterface
	TeamRepo                 repository.TeamRepositoryInterface
	ProjectNamespaceRepo     repository.ProjectNamespaceRepositoryInterface
	SecurityPolicyRepo       repository.SecurityPolicyRepositoryInterface
	BackendTrafficPolicyRepo repository.BackendTrafficPolicyRepositoryInterface
	EnvoyExtensionPolicyRepo repository.EnvoyExtensionPolicyRepositoryInterface
	WafPolicyRepo            repository.WafPolicyRepositoryInterface
	ClientAttachmentRepo     repository.ClientAttachmentRepositoryInterface
	ClientIPRepo             repository.ClientIPRepositoryInterface
	ClientHeaderRepo         repository.ClientHeaderRepositoryInterface
	ClientRepo               repository.ClientRepositoryInterface
	ProjectRepo              repository.ProjectRepositoryInterface
	WafConfig                routeplan.WAFConfig

	// Domains re-applies a domain's ClientTrafficPolicy after client mTLS CA
	// secrets change. See ClientTrafficPolicyEnsurer.
	Domains ClientTrafficPolicyEnsurer

	// RouteVersions records a version snapshot after every successful
	// deploy. See RouteVersionRecorder.
	RouteVersions RouteVersionRecorder

	// Approvals owns approval planning and traversal. The engine holds this
	// service back as a Completer, so main.go builds the engine first and
	// registers the completers afterwards.
	Approvals *approvalpkg.Engine

	// The seven cluster roles route deployment uses. They replace
	// SetKubernetesService, which handed over all 58 cluster-client methods.
	// All seven are required: Phase 2E Task 9 deleted the compound
	// "kubernetes service not configured" guard in Deploy, which covered six
	// of them and silently omitted K8sRefGrants even though
	// ensureReferenceGrantsForDomain dereferences it a few lines later.
	K8sRoutes        RouteApplier
	K8sPolicies      PolicyApplier
	K8sBackends      BackendApplier
	K8sBackendReaper RouteBackendReaper
	K8sSecrets       SecretWriter
	K8sAPIKeys       APIKeySecretApplier
	K8sRefGrants     ReferenceGrantChecker

	// IDGen mints route IDs. Optional: nil means uuid.New. Injected so the
	// preview path is deterministic under test - the first 8 hex characters
	// of the ID minted in PreviewCreate appear in every previewed resource
	// name. See route_assembler.go.
	IDGen func() uuid.UUID
}

// NewRouteService builds a fully-wired RouteService. It panics if a required
// dependency is missing: before Phase 2E these arrived through setters after
// construction, so a forgotten wiring line degraded silently at runtime
// instead of failing at start-up. Master design section 6.6.
func NewRouteService(deps RouteServiceDeps) *RouteService {
	var missing []string
	if deps.RouteRepo == nil {
		missing = append(missing, "RouteRepo")
	}
	if deps.ApprovalRepo == nil {
		missing = append(missing, "ApprovalRepo")
	}
	if deps.PolicyRepo == nil {
		missing = append(missing, "PolicyRepo")
	}
	if deps.DomainRepo == nil {
		missing = append(missing, "DomainRepo")
	}
	if deps.TeamRepo == nil {
		missing = append(missing, "TeamRepo")
	}
	if deps.ProjectNamespaceRepo == nil {
		missing = append(missing, "ProjectNamespaceRepo")
	}
	if deps.SecurityPolicyRepo == nil {
		missing = append(missing, "SecurityPolicyRepo")
	}
	if deps.BackendTrafficPolicyRepo == nil {
		missing = append(missing, "BackendTrafficPolicyRepo")
	}
	if deps.EnvoyExtensionPolicyRepo == nil {
		missing = append(missing, "EnvoyExtensionPolicyRepo")
	}
	if deps.WafPolicyRepo == nil {
		missing = append(missing, "WafPolicyRepo")
	}
	if deps.ClientAttachmentRepo == nil {
		missing = append(missing, "ClientAttachmentRepo")
	}
	if deps.ClientIPRepo == nil {
		missing = append(missing, "ClientIPRepo")
	}
	if deps.ClientHeaderRepo == nil {
		missing = append(missing, "ClientHeaderRepo")
	}
	if deps.ClientRepo == nil {
		missing = append(missing, "ClientRepo")
	}
	if deps.ProjectRepo == nil {
		missing = append(missing, "ProjectRepo")
	}
	if deps.Domains == nil {
		missing = append(missing, "Domains")
	}
	if deps.RouteVersions == nil {
		missing = append(missing, "RouteVersions")
	}
	if deps.Approvals == nil {
		missing = append(missing, "Approvals")
	}
	if deps.K8sRoutes == nil {
		missing = append(missing, "K8sRoutes")
	}
	if deps.K8sPolicies == nil {
		missing = append(missing, "K8sPolicies")
	}
	if deps.K8sBackends == nil {
		missing = append(missing, "K8sBackends")
	}
	if deps.K8sBackendReaper == nil {
		missing = append(missing, "K8sBackendReaper")
	}
	if deps.K8sSecrets == nil {
		missing = append(missing, "K8sSecrets")
	}
	if deps.K8sAPIKeys == nil {
		missing = append(missing, "K8sAPIKeys")
	}
	if deps.K8sRefGrants == nil {
		missing = append(missing, "K8sRefGrants")
	}
	if len(missing) > 0 {
		panic("services.NewRouteService: missing required dependency: " + strings.Join(missing, ", "))
	}

	svc := &RouteService{
		idgen: deps.IDGen,
	}
	// routeRepo is already a constructor parameter, so the state machine
	// needs no setter of its own.
	svc.state = routestate.New(deps.RouteRepo)
	svc.assembler = &routeAssembler{
		clientRepo:           deps.ClientRepo,
		clientAttachmentRepo: deps.ClientAttachmentRepo,
		clientIPRepo:         deps.ClientIPRepo,
		clientHeaderRepo:     deps.ClientHeaderRepo,
		k8sAPIKeys:           deps.K8sAPIKeys,
		wafConfig:            deps.WafConfig,
		idgen:                svc.idgen,
	}
	svc.query = &routeQuery{
		routeRepo:                deps.RouteRepo,
		securityPolicyRepo:       deps.SecurityPolicyRepo,
		backendTrafficPolicyRepo: deps.BackendTrafficPolicyRepo,
		envoyExtensionPolicyRepo: deps.EnvoyExtensionPolicyRepo,
		wafPolicyRepo:            deps.WafPolicyRepo,
		domainRepo:               deps.DomainRepo,
		projectNamespaceRepo:     deps.ProjectNamespaceRepo,
		wafConfig:                deps.WafConfig,
		assembler:                svc.assembler,
	}
	svc.write = &routeWrite{
		routeRepo:                deps.RouteRepo,
		domainRepo:               deps.DomainRepo,
		teamRepo:                 deps.TeamRepo,
		projectRepo:              deps.ProjectRepo,
		approvalRepo:             deps.ApprovalRepo,
		securityPolicyRepo:       deps.SecurityPolicyRepo,
		backendTrafficPolicyRepo: deps.BackendTrafficPolicyRepo,
		envoyExtensionPolicyRepo: deps.EnvoyExtensionPolicyRepo,
		wafPolicyRepo:            deps.WafPolicyRepo,
		k8sRefGrants:             deps.K8sRefGrants,
		approvals:                deps.Approvals,
		state:                    svc.state,
		assembler:                svc.assembler,
		query:                    svc.query,
	}
	svc.deploy = &routeDeploy{
		routeRepo:                deps.RouteRepo,
		approvalRepo:             deps.ApprovalRepo,
		domainRepo:               deps.DomainRepo,
		securityPolicyRepo:       deps.SecurityPolicyRepo,
		backendTrafficPolicyRepo: deps.BackendTrafficPolicyRepo,
		envoyExtensionPolicyRepo: deps.EnvoyExtensionPolicyRepo,
		wafPolicyRepo:            deps.WafPolicyRepo,
		clientAttachmentRepo:     deps.ClientAttachmentRepo,
		k8sRoutes:                deps.K8sRoutes,
		k8sPolicies:              deps.K8sPolicies,
		k8sBackends:              deps.K8sBackends,
		k8sBackendReaper:         deps.K8sBackendReaper,
		k8sSecrets:               deps.K8sSecrets,
		k8sAPIKeys:               deps.K8sAPIKeys,
		domains:                  deps.Domains,
		routeVersions:            deps.RouteVersions,
		state:                    svc.state,
		assembler:                svc.assembler,
		write:                    svc.write,
	}
	return svc
}

// Deploy deploys an approved route to Kubernetes
// This can only be called by the route owner team
func (s *RouteService) Deploy(id uuid.UUID, deployedBy uuid.UUID) (*models.Route, error) {
	return s.deploy.Deploy(id, deployedBy)
}

// GetDomainName returns the domain name for a given domain ID (used for audit enrichment)
func (s *RouteService) GetDomainName(domainID uuid.UUID) (string, error) {
	return s.query.GetDomainName(domainID)
}

// GetEffectiveIPAllowlist returns the merged IP allowlist for a route from active client attachments
func (s *RouteService) GetEffectiveIPAllowlist(routeID uuid.UUID) ([]EffectiveIPEntry, error) {
	return s.assembler.GetEffectiveIPAllowlist(routeID)
}

// GetByID gets a route by ID
func (s *RouteService) GetByID(id uuid.UUID) (*models.Route, error) {
	return s.query.GetByID(id)
}

// GetSecurityPolicy gets the security policy for a route
func (s *RouteService) GetSecurityPolicy(routeID uuid.UUID) (*models.SecurityPolicy, error) {
	return s.query.GetSecurityPolicy(routeID)
}

// GetBackendTrafficPolicy gets the backend traffic policy for a route
func (s *RouteService) GetBackendTrafficPolicy(routeID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	return s.query.GetBackendTrafficPolicy(routeID)
}

// GetEnvoyExtensionPolicy gets the envoy extension policy for a route
func (s *RouteService) GetEnvoyExtensionPolicy(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	return s.query.GetEnvoyExtensionPolicy(routeID)
}

// GetWafPolicy gets the WAF policy for a route
func (s *RouteService) GetWafPolicy(routeID uuid.UUID) (*models.WafPolicy, error) {
	return s.query.GetWafPolicy(routeID)
}

// ListByDomainID lists routes for a domain
func (s *RouteService) ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error) {
	return s.query.ListByDomainID(domainID, page, limit, teamID, status, search, searchField, labels)
}

// ListByProjectID returns routes across all domains in a project, optionally
// filtered by backend service+namespace.
func (s *RouteService) ListByProjectID(projectID uuid.UUID, page, limit int, filters RouteListFilters) ([]models.Route, int64, error) {
	return s.query.ListByProjectID(projectID, page, limit, filters)
}

// CheckMatcherConflicts checks if the given matcher conflicts with any existing route
// in the domain. Returns all conflicting routes. excludeRouteID can be set to skip
// the route being updated.
func (s *RouteService) CheckMatcherConflicts(domainID uuid.UUID, match models.RouteMatch, excludeRouteID *uuid.UUID) ([]ConflictResult, error) {
	return s.query.CheckMatcherConflicts(domainID, match, excludeRouteID)
}

// GenerateYAML generates the Kubernetes YAML for a route
func (s *RouteService) GenerateYAML(id uuid.UUID) (string, error) {
	return s.query.GenerateYAML(id)
}

// GenerateYAMLs generates both HTTPRoute and SecurityPolicy YAML for a route
func (s *RouteService) GenerateYAMLs(id uuid.UUID) (*RouteYAMLs, error) {
	return s.query.GenerateYAMLs(id)
}

// PreviewCreate generates a preview of what the HTTPRoute YAML would look like for a new route
func (s *RouteService) PreviewCreate(domainID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error) {
	return s.query.PreviewCreate(domainID, input)
}

// PreviewUpdate generates a preview comparing current and proposed HTTPRoute YAML
func (s *RouteService) PreviewUpdate(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error) {
	return s.query.PreviewUpdate(routeID, input)
}

// PreviewDelete generates a preview of what will be deleted
func (s *RouteService) PreviewDelete(routeID uuid.UUID) (*PreviewDeleteResult, error) {
	return s.query.PreviewDelete(routeID)
}

// Create creates a new route (submits for approval)
func (s *RouteService) Create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID) (*models.Route, error) {
	return s.write.Create(domainID, input, createdBy)
}

// Update updates a route (submits for approval)
func (s *RouteService) Update(id uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error) {
	return s.write.Update(id, input, submittedBy)
}

// Delete requests deletion of a route (submits for approval)
func (s *RouteService) Delete(id uuid.UUID, submittedBy uuid.UUID) (*models.Route, error) {
	return s.write.Delete(id, submittedBy)
}

// OnApproved moves the route to its post-approval state.
func (s *RouteService) OnApproved(a *models.Approval) error {
	return s.write.OnApproved(a)
}

// OnRejected reverts the route when its approval is rejected.
func (s *RouteService) OnRejected(a *models.Approval) error {
	return s.write.OnRejected(a)
}

// OnCancelled reverts the route when its approval is withdrawn.
func (s *RouteService) OnCancelled(a *models.Approval) error {
	return s.write.OnCancelled(a)
}

// CanCancel implements approval.CancelAuthorizer for routes.
func (s *RouteService) CanCancel(a *models.Approval, user *models.User) bool {
	return s.write.CanCancel(a, user)
}

// GetApprovalIDForEntity returns the most recent approval ID for an entity.
func (s *RouteService) GetApprovalIDForEntity(entityType models.ApprovalEntityType, entityID uuid.UUID) (*uuid.UUID, error) {
	return s.write.GetApprovalIDForEntity(entityType, entityID)
}

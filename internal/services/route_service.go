package services

import (
	"context"
	"strings"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/models"
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
	// The seven cluster roles route deployment uses. Before Phase 2E Task 7
	// they arrived as one interface naming all 58 methods of the cluster
	// client, of which RouteService calls twenty-six.
	k8sRoutes        RouteApplier
	k8sPolicies      PolicyApplier
	k8sBackends      BackendApplier
	k8sBackendReaper RouteBackendReaper
	k8sSecrets       SecretWriter
	k8sAPIKeys       APIKeySecretApplier
	k8sRefGrants     ReferenceGrantChecker
	domains          ClientTrafficPolicyEnsurer
	routeVersions    RouteVersionRecorder
	wafConfig        routeplan.WAFConfig

	// approvals owns approval planning and traversal. A required
	// constructor dependency since Phase 2E Task 6. See internal/approval.
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
	// name. See route_service_idgen.go.
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
		routeRepo:                deps.RouteRepo,
		approvalRepo:             deps.ApprovalRepo,
		policyRepo:               deps.PolicyRepo,
		domainRepo:               deps.DomainRepo,
		teamRepo:                 deps.TeamRepo,
		projectNamespaceRepo:     deps.ProjectNamespaceRepo,
		securityPolicyRepo:       deps.SecurityPolicyRepo,
		backendTrafficPolicyRepo: deps.BackendTrafficPolicyRepo,
		envoyExtensionPolicyRepo: deps.EnvoyExtensionPolicyRepo,
		wafPolicyRepo:            deps.WafPolicyRepo,
		clientAttachmentRepo:     deps.ClientAttachmentRepo,
		clientIPRepo:             deps.ClientIPRepo,
		clientHeaderRepo:         deps.ClientHeaderRepo,
		clientRepo:               deps.ClientRepo,
		projectRepo:              deps.ProjectRepo,
		wafConfig:                deps.WafConfig,
		domains:                  deps.Domains,
		routeVersions:            deps.RouteVersions,
		approvals:                deps.Approvals,
		idgen:                    deps.IDGen,
		k8sRoutes:                deps.K8sRoutes,
		k8sPolicies:              deps.K8sPolicies,
		k8sBackends:              deps.K8sBackends,
		k8sBackendReaper:         deps.K8sBackendReaper,
		k8sSecrets:               deps.K8sSecrets,
		k8sAPIKeys:               deps.K8sAPIKeys,
		k8sRefGrants:             deps.K8sRefGrants,
	}
	// routeRepo is already a constructor parameter, so the state machine
	// needs no setter of its own.
	svc.state = &routeStateMachine{repo: deps.RouteRepo}
	return svc
}

// GetDomainName returns the domain name for a given domain ID (used for audit enrichment)
func (s *RouteService) GetDomainName(domainID uuid.UUID) (string, error) {
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return "", err
	}
	return domain.Name, nil
}

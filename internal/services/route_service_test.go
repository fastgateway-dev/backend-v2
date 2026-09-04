package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// helpers -----------------------------------------------------------------

// fullRouteServiceDeps returns a RouteServiceDeps in which every required
// dependency is populated. Phase 2E Task 2 made all fifteen repositories
// constructor parameters, so nothing may be left nil.
//
// Several defaults carry stubbed behaviour rather than being bare mocks,
// because before Phase 2E the test helpers left those fields nil and the
// production nil-guards then skipped the work entirely. Each stub reproduces
// the answer the skipped branch used to produce -- see the constructor
// functions below. Phase 2E Task 9 has since deleted every one of those
// nil-guards, so the stubs now describe production behaviour rather than a
// degraded fallback.
func fullRouteServiceDeps(t *testing.T) services.RouteServiceDeps {
	t.Helper()
	return newRouteServiceDeps()
}

func newRouteServiceDeps() services.RouteServiceDeps {
	return services.RouteServiceDeps{
		RouteRepo:                new(mocks.MockRouteRepository),
		ApprovalRepo:             new(mocks.MockUnifiedApprovalRepository),
		PolicyRepo:               new(mocks.MockApprovalPolicyRepository),
		DomainRepo:               new(mocks.MockDomainRepository),
		TeamRepo:                 new(mocks.MockTeamRepository),
		ProjectNamespaceRepo:     newManagedNamespaceRepo(),
		SecurityPolicyRepo:       newEmptySecurityPolicyRepo(),
		BackendTrafficPolicyRepo: newEmptyBackendTrafficPolicyRepo(),
		EnvoyExtensionPolicyRepo: newEmptyEnvoyExtensionPolicyRepo(),
		WafPolicyRepo:            newEmptyWafPolicyRepo(),
		ClientAttachmentRepo:     newEmptyClientAttachmentRepo(),
		ClientIPRepo:             new(mocks.MockClientIPRepository),
		ClientHeaderRepo:         newEmptyClientHeaderRepo(),
		ClientRepo:               newNoClientsClientRepo(),
		ProjectRepo:              newApprovalsEnabledProjectRepo(),
		WafConfig:                routeplan.WAFConfig{},
		Domains:                  noopCTPEnsurer{},
		RouteVersions:            noopVersionRecorder{},
		Approvals:                newRouteApprovalEngine(),

		// Phase 2E Task 9 deleted route_deploy.go's compound
		// "kubernetes service not configured" guard and made all seven
		// cluster roles required, so every RouteService names them. These
		// are bare mocks with no expectations: a test that reaches
		// Kubernetes without saying so fails on an unexpected call, which
		// is what the six deleted guard-pinning tests used to get as a
		// silent error string instead. Tests that do deploy override all
		// seven -- see newTestRouteServiceWithK8s.
		K8sRoutes:        new(mocks.MockKubernetesService),
		K8sPolicies:      new(mocks.MockKubernetesService),
		K8sBackends:      new(mocks.MockKubernetesService),
		K8sBackendReaper: new(mocks.MockKubernetesService),
		K8sSecrets:       new(mocks.MockKubernetesService),
		K8sAPIKeys:       new(mocks.MockKubernetesService),
		K8sRefGrants:     new(mocks.MockKubernetesService),
	}
}

// noopCTPEnsurer answers "nothing to re-apply", reproducing the pre-2E
// behaviour of an unset domainService: route_clients_apikey.go's
// `if s.domains != nil` skipped the client mTLS CA secret and
// ClientTrafficPolicy block entirely when the field was nil, and every test
// here left it nil. Phase 2E Task 9 deleted that guard; the stub keeps these
// tests on the same observable path, since no test here attaches an mTLS
// client.
type noopCTPEnsurer struct{}

func (noopCTPEnsurer) EnsureMTLSClientTrafficPolicy(context.Context, *models.Domain) error {
	return nil
}

// noopVersionRecorder answers "version recorded", reproducing the pre-2E
// behaviour of an unset routeVersionService: route_deploy.go skipped the
// snapshot entirely when the field was nil, and every test here left it nil.
// The production call already treats a CreateVersion failure as non-fatal, so
// returning nil is exactly the observable pre-2E result. Phase 2E Task 9
// deleted the guard, so the snapshot call now always happens -- against this
// stub.
type noopVersionRecorder struct{}

func (noopVersionRecorder) CreateVersion(*models.Route, *models.Approval, uuid.UUID) error {
	return nil
}

// newRouteApprovalEngine builds the approval engine RouteService now takes as
// a required constructor parameter (Phase 2E Task 6). It is the same engine
// newTestRouteServiceWith used to attach through SetApprovalEngine, built
// before the service instead of after it. Callers that need the route
// completer registered do that once the service exists.
func newRouteApprovalEngine() *approvalpkg.Engine {
	// The engine always records stage reviews; the pre-2D nil guard that
	// skipped them is gone. approval.New panics on a nil dependency by
	// design, so every slot gets a mock.
	stageReviewRepo := new(mocks.MockApprovalStageReviewRepository)
	stageReviewRepo.On("ListByStageID", mock.Anything).
		Return([]models.ApprovalStageReview{}, nil).Maybe()
	stageReviewRepo.On("Create", mock.AnythingOfType("*models.ApprovalStageReview")).
		Return(nil).Maybe()
	stageReviewRepo.On("CountByStageAndDecision", mock.Anything, mock.Anything).
		Return(int64(1), nil).Maybe()
	return approvalpkg.New(
		new(mocks.MockUnifiedApprovalRepository),
		stageReviewRepo,
		new(mocks.MockApprovalPolicyRepository),
		new(mocks.MockTeamRepository),
		new(mocks.MockProjectRepository),
	)
}

// newManagedNamespaceRepo answers "every namespace is managed by this
// project", reproducing the pre-2E behaviour of an unset
// projectNamespaceRepo (validation skipped).
func newManagedNamespaceRepo() *mocks.MockProjectNamespaceRepository {
	nsRepo := new(mocks.MockProjectNamespaceRepository)
	nsRepo.On("ExistsByProjectAndNamespace", mock.Anything, mock.Anything).
		Return(true, nil).Maybe()
	return nsRepo
}

// newApprovalsEnabledProjectRepo answers "approvals are enabled", reproducing
// the pre-2E behaviour of an unset projectRepo (the bypass branch skipped, so
// submissions go through the approval engine).
func newApprovalsEnabledProjectRepo() *mocks.MockProjectRepository {
	projectRepo := new(mocks.MockProjectRepository)
	projectRepo.On("GetByID", mock.Anything).
		Return(&models.Project{ApprovalEnabled: true}, nil).Maybe()
	return projectRepo
}

// The four "empty" policy repos and the empty attachment repo answer "nothing
// configured", reproducing the pre-2E behaviour of the corresponding unset
// field (route_query.go:36/80/93/106/119, route_clients.go:94,
// route_write.go:622/631/640/735/920/928/936/944).
func newEmptySecurityPolicyRepo() *mocks.MockSecurityPolicyRepository {
	repo := new(mocks.MockSecurityPolicyRepository)
	repo.On("GetByRouteID", mock.Anything).Return(nil, nil).Maybe()
	repo.On("DeleteByRouteID", mock.Anything).Return(nil).Maybe()
	repo.On("Create", mock.Anything).Return(nil).Maybe()
	repo.On("Upsert", mock.Anything).Return(nil).Maybe()
	return repo
}

func newEmptyBackendTrafficPolicyRepo() *mocks.MockBackendTrafficPolicyRepository {
	repo := new(mocks.MockBackendTrafficPolicyRepository)
	repo.On("GetByRouteID", mock.Anything).Return(nil, nil).Maybe()
	repo.On("DeleteByRouteID", mock.Anything).Return(nil).Maybe()
	repo.On("Create", mock.Anything).Return(nil).Maybe()
	repo.On("Upsert", mock.Anything).Return(nil).Maybe()
	return repo
}

func newEmptyEnvoyExtensionPolicyRepo() *mocks.MockEnvoyExtensionPolicyRepository {
	repo := new(mocks.MockEnvoyExtensionPolicyRepository)
	repo.On("GetByRouteID", mock.Anything).Return(nil, nil).Maybe()
	repo.On("DeleteByRouteID", mock.Anything).Return(nil).Maybe()
	repo.On("Create", mock.Anything).Return(nil).Maybe()
	repo.On("Upsert", mock.Anything).Return(nil).Maybe()
	return repo
}

func newEmptyWafPolicyRepo() *mocks.MockWafPolicyRepository {
	repo := new(mocks.MockWafPolicyRepository)
	repo.On("GetByRouteID", mock.Anything).Return(nil, nil).Maybe()
	repo.On("DeleteByRouteID", mock.Anything).Return(nil).Maybe()
	repo.On("Create", mock.Anything).Return(nil).Maybe()
	repo.On("Upsert", mock.Anything).Return(nil).Maybe()
	return repo
}

// newEmptyClientHeaderRepo and newNoClientsClientRepo answer "this client
// contributes nothing to the base route", reproducing the pre-2E behaviour of
// an unset clientHeaderRepo / clientRepo (route_clients.go:168, 206).
func newEmptyClientHeaderRepo() *mocks.MockClientHeaderRepository {
	repo := new(mocks.MockClientHeaderRepository)
	repo.On("ListByClientID", mock.Anything).
		Return([]models.ClientHeader{}, nil).Maybe()
	return repo
}

func newNoClientsClientRepo() *mocks.MockClientRepository {
	repo := new(mocks.MockClientRepository)
	// gorm.ErrRecordNotFound makes every caller treat this as genuine
	// absence (client no longer exists) and skip-and-continue, which is
	// what the nil clientRepo used to produce: no client contributes to the
	// plan.
	//
	// Phase 2G Task 4 note: this stub used to return a plain errors.New(...)
	// sentinel. Since collectClientMethods (route_clients.go) now
	// distinguishes genuine absence (gorm.ErrRecordNotFound, via clientRepo's
	// First-backed GetByID) from a real repository failure -- propagating
	// only the latter -- a generic error here would incorrectly fail every
	// test using this default instead of reproducing "this client
	// contributes nothing." gorm.ErrRecordNotFound is the correct value for
	// what this stub has always meant.
	repo.On("GetByID", mock.Anything).Return(nil, gorm.ErrRecordNotFound).Maybe()
	return repo
}

func newEmptyClientAttachmentRepo() *mocks.MockClientAttachmentRepository {
	repo := new(mocks.MockClientAttachmentRepository)
	repo.On("ListActiveByRouteID", mock.Anything).
		Return([]models.ClientRouteAttachment{}, nil).Maybe()
	repo.On("ListApprovedByRouteID", mock.Anything).
		Return([]models.ClientRouteAttachment{}, nil).Maybe()
	return repo
}

func newTestRouteService() (
	*services.RouteService,
	*mocks.MockRouteRepository,
	*mocks.MockUnifiedApprovalRepository,
	*mocks.MockApprovalPolicyRepository,
	*mocks.MockDomainRepository,
	*mocks.MockTeamRepository,
) {
	return newTestRouteServiceWith(nil)
}

// newTestRouteServiceWith builds the service after letting the caller adjust
// the dependency struct. It replaces the per-test setter calls that Phase 2E
// Task 2 deleted. The five mocks it returns are the ones it created itself,
// so mutate must not replace RouteRepo, ApprovalRepo, PolicyRepo, DomainRepo
// or TeamRepo.
func newTestRouteServiceWith(mutate func(*services.RouteServiceDeps)) (
	*services.RouteService,
	*mocks.MockRouteRepository,
	*mocks.MockUnifiedApprovalRepository,
	*mocks.MockApprovalPolicyRepository,
	*mocks.MockDomainRepository,
	*mocks.MockTeamRepository,
) {
	deps := newRouteServiceDeps()
	routeRepo := deps.RouteRepo.(*mocks.MockRouteRepository)
	approvalRepo := deps.ApprovalRepo.(*mocks.MockUnifiedApprovalRepository)
	policyRepo := deps.PolicyRepo.(*mocks.MockApprovalPolicyRepository)
	domainRepo := deps.DomainRepo.(*mocks.MockDomainRepository)
	teamRepo := deps.TeamRepo.(*mocks.MockTeamRepository)

	// Phase 2D Task 7: submission goes through the approval engine, which
	// plans the stages and persists the approval. approval.New panics on a
	// nil dependency by design, so every slot gets a mock. The engine also
	// always records stage reviews; the pre-2D nil guard that skipped them
	// is gone. Phase 2E Task 6: the engine is a required constructor
	// parameter, so it is built here -- from the same repositories as before,
	// captured before mutate runs -- and the completer is registered once the
	// service exists.
	stageReviewRepo := new(mocks.MockApprovalStageReviewRepository)
	stageReviewRepo.On("ListByStageID", mock.Anything).
		Return([]models.ApprovalStageReview{}, nil).Maybe()
	stageReviewRepo.On("Create", mock.AnythingOfType("*models.ApprovalStageReview")).
		Return(nil).Maybe()
	stageReviewRepo.On("CountByStageAndDecision", mock.Anything, mock.Anything).
		Return(int64(1), nil).Maybe()
	engine := approvalpkg.New(approvalRepo, stageReviewRepo, policyRepo, teamRepo, new(mocks.MockProjectRepository))
	deps.Approvals = engine

	if mutate != nil {
		mutate(&deps)
	}
	svc := services.NewRouteService(deps)
	engine.Register(models.ApprovalEntityRoute, svc)

	return svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo
}

// newTestRouteServiceFull creates a RouteService and returns the extra policy
// and client repositories alongside the core five, so tests can set
// expectations on them. Since Phase 2E Task 2 every RouteService is "full" --
// the constructor requires all of them -- so this differs from
// newTestRouteService only in what it hands back.
func newTestRouteServiceFull() (
	*services.RouteService,
	*mocks.MockRouteRepository,
	*mocks.MockUnifiedApprovalRepository,
	*mocks.MockApprovalPolicyRepository,
	*mocks.MockDomainRepository,
	*mocks.MockTeamRepository,
	*mocks.MockSecurityPolicyRepository,
	*mocks.MockBackendTrafficPolicyRepository,
	*mocks.MockEnvoyExtensionPolicyRepository,
	*mocks.MockWafPolicyRepository,
	*mocks.MockClientAttachmentRepository,
	*mocks.MockClientIPRepository,
) {
	return newTestRouteServiceFullWith(nil)
}

// newTestRouteServiceFullWith is newTestRouteServiceFull with a hook for
// the caller to fill in extra dependencies -- the seven Kubernetes roles,
// in practice. Phase 2E Task 7 replaced SetKubernetesService with those
// constructor fields, so they can no longer be attached afterwards.
func newTestRouteServiceFullWith(extra func(*services.RouteServiceDeps)) (
	*services.RouteService,
	*mocks.MockRouteRepository,
	*mocks.MockUnifiedApprovalRepository,
	*mocks.MockApprovalPolicyRepository,
	*mocks.MockDomainRepository,
	*mocks.MockTeamRepository,
	*mocks.MockSecurityPolicyRepository,
	*mocks.MockBackendTrafficPolicyRepository,
	*mocks.MockEnvoyExtensionPolicyRepository,
	*mocks.MockWafPolicyRepository,
	*mocks.MockClientAttachmentRepository,
	*mocks.MockClientIPRepository,
) {
	var secRepo *mocks.MockSecurityPolicyRepository
	var btpRepo *mocks.MockBackendTrafficPolicyRepository
	var eepRepo *mocks.MockEnvoyExtensionPolicyRepository
	var wafRepo *mocks.MockWafPolicyRepository
	var caRepo *mocks.MockClientAttachmentRepository
	var cipRepo *mocks.MockClientIPRepository

	svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo := newTestRouteServiceWith(
		func(d *services.RouteServiceDeps) {
			// Bare mocks, not the permissive defaults: these six are
			// handed back so the caller can set its own expectations,
			// exactly as it did when this helper called the (now deleted)
			// setters.
			secRepo = new(mocks.MockSecurityPolicyRepository)
			btpRepo = new(mocks.MockBackendTrafficPolicyRepository)
			eepRepo = new(mocks.MockEnvoyExtensionPolicyRepository)
			wafRepo = new(mocks.MockWafPolicyRepository)
			caRepo = new(mocks.MockClientAttachmentRepository)
			cipRepo = new(mocks.MockClientIPRepository)
			d.SecurityPolicyRepo = secRepo
			d.BackendTrafficPolicyRepo = btpRepo
			d.EnvoyExtensionPolicyRepo = eepRepo
			d.WafPolicyRepo = wafRepo
			d.ClientAttachmentRepo = caRepo
			d.ClientIPRepo = cipRepo
			if extra != nil {
				extra(d)
			}
		})

	return svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo, secRepo, btpRepo, eepRepo, wafRepo, caRepo, cipRepo
}

// helper to make a basic valid route config for HTTP
func makeBasicHTTPRouteConfig() models.RouteConfig {
	return models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/api/users"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "user-svc", Namespace: "default", Port: 8080},
		},
	}
}

// =========================================================================
// GetByID
// =========================================================================

func TestRouteService_GetByID_Success(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	expected := &models.Route{
		ID:   routeID,
		Name: "user-api",
	}

	routeRepo.On("GetByIDWithApproval", routeID).Return(expected, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, routeID, result.ID)
	assert.Equal(t, "user-api", result.Name)
	routeRepo.AssertExpectations(t)
}

func TestRouteService_GetByID_NotFound(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	routeRepo.On("GetByIDWithApproval", routeID).Return(nil, errors.New("record not found"))

	result, err := svc.GetByID(routeID)

	assert.Nil(t, result)
	assert.Error(t, err)
	routeRepo.AssertExpectations(t)
}

// =========================================================================
// ListByDomainID
// =========================================================================

func TestRouteService_ListByDomainID_Success(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	domainID := uuid.New()
	routes := []models.Route{
		{ID: uuid.New(), Name: "route-a"},
		{ID: uuid.New(), Name: "route-b"},
	}

	routeRepo.On("ListByDomainID", domainID, 1, 10, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return(routes, int64(2), nil)

	result, total, err := svc.ListByDomainID(domainID, 1, 10, nil, "", "", "", nil)

	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, result, 2)
	routeRepo.AssertExpectations(t)
}

func TestRouteService_ListByDomainID_Empty(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	domainID := uuid.New()
	routeRepo.On("ListByDomainID", domainID, 1, 10, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	result, total, err := svc.ListByDomainID(domainID, 1, 10, nil, "", "", "", nil)

	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, result)
	routeRepo.AssertExpectations(t)
}

// =========================================================================
// Create - basic success
// =========================================================================

func TestRouteService_Create_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()
	createdBy := uuid.New()

	domain := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Hostname:  "example.com",
	}

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api/users"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "user-svc", Namespace: "default", Port: 8080},
			},
		},
	}

	// Route name does not exist
	routeRepo.On("ExistsByName", domainID, "user-api").Return(false, nil)
	// Domain lookup
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	// Team lookup
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID, Name: "platform"}, nil)
	// No matcher conflicts - list existing routes
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	// Create route in DB
	routeRepo.On("Create", mock.AnythingOfType("*models.Route")).Return(nil)
	// Approval policy lookup (returns nil = use default)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	// Create approval
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	result, err := svc.Create(domainID, input, createdBy)

	require.NoError(t, err)
	assert.Equal(t, "user-api", result.Name)
	assert.Equal(t, domainID, result.DomainID)
	assert.Equal(t, models.RouteStatusPendingCreate, result.Status)
	assert.NotNil(t, result.PendingApproval)
	routeRepo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
	teamRepo.AssertExpectations(t)
	approvalRepo.AssertExpectations(t)
}

// =========================================================================
// Create - route name already exists
// =========================================================================

func TestRouteService_Create_NameAlreadyExists(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	domainID := uuid.New()

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: uuid.New(),
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	routeRepo.On("ExistsByName", domainID, "user-api").Return(true, nil)

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route name already exists in this domain")
	routeRepo.AssertExpectations(t)
}

// =========================================================================
// Create - invalid route name (spaces)
// =========================================================================

func TestRouteService_Create_InvalidName_Spaces(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	input := &services.CreateRouteInput{
		Name:   "user api",
		TeamID: uuid.New(),
		Config: models.RouteConfig{
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(uuid.New(), input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route name cannot contain spaces")
}

// =========================================================================
// Create - domain not found
// =========================================================================

func TestRouteService_Create_DomainNotFound(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	domainID := uuid.New()

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: uuid.New(),
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	routeRepo.On("ExistsByName", domainID, "user-api").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "domain not found")
	routeRepo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
}

// =========================================================================
// Delete - submits for approval
// =========================================================================

func TestRouteService_Delete_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
		Status:   models.RouteStatusActive,
	}

	domain := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	result, err := svc.Delete(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDelete, result.Status)
	assert.NotNil(t, result.PendingApproval)
	routeRepo.AssertExpectations(t)
	approvalRepo.AssertExpectations(t)
}

func TestRouteService_Delete_AlreadyPendingApproval(t *testing.T) {
	svc, routeRepo, approvalRepo, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Name:   "user-api",
		Status: models.RouteStatusActive,
	}

	existingApproval := &models.Approval{
		ID:     uuid.New(),
		Status: models.ApprovalStatusPending,
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(existingApproval, nil)

	result, err := svc.Delete(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "there is already a pending approval for this route")
	routeRepo.AssertExpectations(t)
	approvalRepo.AssertExpectations(t)
}

func TestRouteService_Delete_RouteNotFound(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("record not found"))

	result, err := svc.Delete(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Error(t, err)
	routeRepo.AssertExpectations(t)
}

// =========================================================================
// GetSecurityPolicy / GetBackendTrafficPolicy - nil repo returns nil
// =========================================================================

func TestRouteService_GetSecurityPolicy_NilRepo(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	result, err := svc.GetSecurityPolicy(uuid.New())

	assert.Nil(t, result)
	assert.NoError(t, err)
}

func TestRouteService_GetBackendTrafficPolicy_NilRepo(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	result, err := svc.GetBackendTrafficPolicy(uuid.New())

	assert.Nil(t, result)
	assert.NoError(t, err)
}

// =========================================================================
// Update - success path (creates approval)
// =========================================================================

func TestRouteService_Update_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submittedBy := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		Status:       models.RouteStatusActive,
		SecurityMode: models.SecurityModeGeneral,
		Config:       makeBasicHTTPRouteConfig(),
		K8sRouteName: "user-api-12345678",
	}

	domain := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Hostname:  "example.com",
	}

	input := &services.UpdateRouteInput{
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api/v2/users"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "user-svc-v2", Namespace: "default", Port: 8080},
			},
		},
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	// Matcher conflict check
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	// No pending approval
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	// Update route
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// Build approval stages
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	// Create approval
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	result, err := svc.Update(routeID, input, submittedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingUpdate, result.Status)
	assert.NotNil(t, result.PendingApproval)
	assert.Equal(t, models.ApprovalActionUpdate, result.PendingApproval.Action)
	routeRepo.AssertExpectations(t)
	approvalRepo.AssertExpectations(t)
}

// =========================================================================
// Update - route not found
// =========================================================================

func TestRouteService_Update_RouteNotFound(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("record not found"))

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Update(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.Error(t, err)
	routeRepo.AssertExpectations(t)
}

// =========================================================================
// Update - domain not found
// =========================================================================

func TestRouteService_Update_DomainNotFound(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
		Status:   models.RouteStatusActive,
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Update(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "domain not found")
}

// =========================================================================
// Update - pending approval already exists
// =========================================================================

func TestRouteService_Update_AlreadyPendingApproval(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
		Status:   models.RouteStatusActive,
		Config:   makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}
	existingApproval := &models.Approval{ID: uuid.New(), Status: models.ApprovalStatusPending}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(existingApproval, nil)

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Update(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "there is already a pending approval for this route")
}

// =========================================================================
// Update - validation errors (no backends)
// =========================================================================

func TestRouteService_Update_NoBackends(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
		Status:   models.RouteStatusActive,
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	input := &services.UpdateRouteInput{
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{},
		},
	}

	result, err := svc.Update(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "at least one backend is required")
}

// =========================================================================
// Update - validation: backend missing namespace
// =========================================================================

func TestRouteService_Update_BackendMissingNamespace(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
		Status:   models.RouteStatusActive,
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	input := &services.UpdateRouteInput{
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "", Port: 80},
			},
		},
	}

	result, err := svc.Update(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "namespace is required")
}

// =========================================================================
// Update - with SecurityPolicy upsert
// =========================================================================

func TestRouteService_Update_WithSecurityPolicy(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		Status:       models.RouteStatusActive,
		SecurityMode: models.SecurityModeGeneral,
		Config:       makeBasicHTTPRouteConfig(),
		K8sRouteName: "user-api-12345678",
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID, Hostname: "example.com"}

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://example.com"},
			},
		},
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	// Previous config capture mocks
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	// Upsert the new security policy
	secRepo.On("Upsert", mock.AnythingOfType("*models.SecurityPolicy")).Return(nil)
	// BTP and EEP are nil, so they get deleted
	btpRepo.On("DeleteByRouteID", routeID).Return(nil)
	eepRepo.On("DeleteByRouteID", routeID).Return(nil)

	result, err := svc.Update(routeID, input, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingUpdate, result.Status)
	secRepo.AssertExpectations(t)
}

// =========================================================================
// Update - client mode rejects general-only security features
// =========================================================================

func TestRouteService_Update_ClientMode_RejectsGeneralSecurityFields(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		Status:       models.RouteStatusActive,
		SecurityMode: models.SecurityModeClient,
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			Authorization: &routeplan.AuthorizationInput{
				AllowedCIDRs: []string{"10.0.0.0/8"},
			},
		},
	}

	result, err := svc.Update(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not available in client security mode")
}

// =========================================================================
// GetSecurityPolicy - with repo set, policy found
// =========================================================================

func TestRouteService_GetSecurityPolicy_Found(t *testing.T) {
	svc, _, _, _, _, _, secRepo, _, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	expected := &models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{AllowOrigins: []string{"*"}},
		},
	}

	secRepo.On("GetByRouteID", routeID).Return(expected, nil)

	result, err := svc.GetSecurityPolicy(routeID)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, routeID, result.RouteID)
	assert.NotNil(t, result.Config.CORS)
	secRepo.AssertExpectations(t)
}

// =========================================================================
// GetSecurityPolicy - with repo set, not found returns nil
// =========================================================================

func TestRouteService_GetSecurityPolicy_NotFound(t *testing.T) {
	svc, _, _, _, _, _, secRepo, _, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.GetSecurityPolicy(routeID)

	assert.Nil(t, result)
	assert.NoError(t, err) // Not found is not an error
	secRepo.AssertExpectations(t)
}

// =========================================================================
// GetBackendTrafficPolicy - with repo set, policy found
// =========================================================================

func TestRouteService_GetBackendTrafficPolicy_Found(t *testing.T) {
	svc, _, _, _, _, _, _, btpRepo, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	expected := &models.BackendTrafficPolicy{
		ID:      uuid.New(),
		RouteID: &routeID,
		Config: models.BackendTrafficPolicyConfig{
			Retry: &models.RetryConfig{NumRetries: routeInt32Ptr(3)},
		},
	}

	btpRepo.On("GetByRouteID", routeID).Return(expected, nil)

	result, err := svc.GetBackendTrafficPolicy(routeID)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, &routeID, result.RouteID)
	btpRepo.AssertExpectations(t)
}

// =========================================================================
// GetBackendTrafficPolicy - with repo set, not found returns nil
// =========================================================================

func TestRouteService_GetBackendTrafficPolicy_NotFound(t *testing.T) {
	svc, _, _, _, _, _, _, btpRepo, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.GetBackendTrafficPolicy(routeID)

	assert.Nil(t, result)
	assert.NoError(t, err)
	btpRepo.AssertExpectations(t)
}

// =========================================================================
// GetEnvoyExtensionPolicy - nil repo
// =========================================================================

func TestRouteService_GetEnvoyExtensionPolicy_NilRepo(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	result, err := svc.GetEnvoyExtensionPolicy(uuid.New())

	assert.Nil(t, result)
	assert.NoError(t, err)
}

// =========================================================================
// GetEnvoyExtensionPolicy - found
// =========================================================================

func TestRouteService_GetEnvoyExtensionPolicy_Found(t *testing.T) {
	svc, _, _, _, _, _, _, _, eepRepo, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	expected := &models.EnvoyExtensionPolicy{
		ID:      uuid.New(),
		RouteID: &routeID,
	}

	eepRepo.On("GetByRouteID", routeID).Return(expected, nil)

	result, err := svc.GetEnvoyExtensionPolicy(routeID)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, &routeID, result.RouteID)
	eepRepo.AssertExpectations(t)
}

// =========================================================================
// GetEnvoyExtensionPolicy - not found returns nil
// =========================================================================

func TestRouteService_GetEnvoyExtensionPolicy_NotFound(t *testing.T) {
	svc, _, _, _, _, _, _, _, eepRepo, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.GetEnvoyExtensionPolicy(routeID)

	assert.Nil(t, result)
	assert.NoError(t, err)
	eepRepo.AssertExpectations(t)
}

// =========================================================================
// GetWafPolicy - nil repo
// =========================================================================

func TestRouteService_GetWafPolicy_NilRepo(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	result, err := svc.GetWafPolicy(uuid.New())

	assert.Nil(t, result)
	assert.NoError(t, err)
}

// =========================================================================
// GetWafPolicy - found
// =========================================================================

func TestRouteService_GetWafPolicy_Found(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, wafRepo, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	expected := &models.WafPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.WafPolicyConfig{
			Mode: "DetectionOnly",
		},
	}

	wafRepo.On("GetByRouteID", routeID).Return(expected, nil)

	result, err := svc.GetWafPolicy(routeID)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "DetectionOnly", result.Config.Mode)
	wafRepo.AssertExpectations(t)
}

// =========================================================================
// GetWafPolicy - returns error from repo (unlike others, waf returns err)
// =========================================================================

func TestRouteService_GetWafPolicy_RepoError(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, wafRepo, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("db error"))

	result, err := svc.GetWafPolicy(routeID)

	assert.Nil(t, result)
	assert.Error(t, err) // WafPolicy returns the error unlike the others
	wafRepo.AssertExpectations(t)
}

// =========================================================================
// GenerateYAMLs - success
// =========================================================================

func TestRouteService_GenerateYAMLs_Success(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{
		ID:       domainID,
		Hostname: "example.com",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	// Client attachment calls for IP authorization / API key / JWT / mTLS
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GenerateYAMLs(routeID)

	require.NoError(t, err)
	assert.NotEmpty(t, result.HTTPRouteYAML)
	assert.Contains(t, result.HTTPRouteYAML, "HTTPRoute")
	assert.Empty(t, result.SecurityPolicyYAML)
	assert.Empty(t, result.BackendTrafficPolicyYAML)
	routeRepo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
}

// =========================================================================
// GenerateYAMLs - route not found
// =========================================================================

func TestRouteService_GenerateYAMLs_RouteNotFound(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.GenerateYAMLs(routeID)

	assert.Nil(t, result)
	assert.Error(t, err)
}

// =========================================================================
// GenerateYAMLs - per-client API key resource generation propagates errors
// (Phase 2G Task 4 fix round 1, review finding F-1)
//
// generateAPIKeyClientResourceYAMLs (route_yaml.go) used to swallow
// categorizeClientAttachments's error into a nil (empty) result, so a route
// with a client whose encrypted API key fails to base64-decode used to
// render a preview with NO per-client API-key resources at all, while
// Deploy of the identical route hard-fails on the same decode error --
// preview and deploy silently disagreeing, this project's #1 known defect
// class. Fix round 1 propagates the error out of
// generateAPIKeyClientResourceYAMLs and its only caller, GenerateYAMLs (which
// already returned (*RouteYAMLs, error)), so the whole preview now fails
// instead.
// =========================================================================

func TestRouteService_GenerateYAMLs_APIKeyDecodeFailurePropagatesError(t *testing.T) {
	var clientRepo *mocks.MockClientRepository
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _ := newTestRouteServiceFullWith(func(d *services.RouteServiceDeps) {
		clientRepo = new(mocks.MockClientRepository)
		d.ClientRepo = clientRepo
	})

	routeID := uuid.New()
	domainID := uuid.New()
	clientID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{
		ID:       domainID,
		Hostname: "example.com",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	// One active attachment with API-key auth enabled, whose client's
	// encrypted key is corrupt (not valid base64) -- categorizeClientAttachments
	// (route_clients_apikey.go, fixed by S5) now propagates that decode
	// failure instead of silently continuing with an empty credential.
	attachments := []models.ClientRouteAttachment{
		{
			ID:           uuid.New(),
			ClientID:     clientID,
			RouteID:      routeID,
			Status:       models.AttachmentStatusActive,
			EnableAPIKey: true,
		},
	}
	caRepo.On("ListActiveByRouteID", routeID).Return(attachments, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	client := &models.Client{
		ID:              clientID,
		APIKeyEnabled:   true,
		APIKeyEncrypted: "%%% not valid base64 %%%",
	}
	clientRepo.On("GetByID", clientID).Return(client, nil)

	result, err := svc.GenerateYAMLs(routeID)

	require.Error(t, err,
		"FIX ROUND 1 (route_yaml.go:150-167, F-1): a base64 decode failure "+
			"inside categorizeClientAttachments must now fail the WHOLE preview "+
			"instead of silently rendering zero per-client API-key resources, "+
			"which used to make GenerateYAMLs disagree with Deploy (which "+
			"hard-fails on the identical error)")
	assert.Contains(t, err.Error(), "illegal base64")
	assert.Nil(t, result)
}

// =========================================================================
// GenerateYAMLs - with SecurityPolicy
// =========================================================================

func TestRouteService_GenerateYAMLs_WithSecurityPolicy(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{
		ID:       domainID,
		Hostname: "example.com",
	}

	secPolicy := &models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://example.com"},
				AllowMethods: []string{"GET", "POST"},
			},
		},
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(secPolicy, nil)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GenerateYAMLs(routeID)

	require.NoError(t, err)
	assert.NotEmpty(t, result.HTTPRouteYAML)
	assert.NotEmpty(t, result.SecurityPolicyYAML)
	assert.Contains(t, result.SecurityPolicyYAML, "SecurityPolicy")
}

// =========================================================================
// GetEffectiveIPAllowlist - nil repos returns empty
// =========================================================================

func TestRouteService_GetEffectiveIPs_NilRepos(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	result, err := svc.GetEffectiveIPAllowlist(uuid.New())

	require.NoError(t, err)
	assert.Empty(t, result)
}

// =========================================================================
// GetEffectiveIPAllowlist - no active attachments
// =========================================================================

func TestRouteService_GetEffectiveIPs_NoAttachments(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, cipRepo := newTestRouteServiceFull()
	_ = cipRepo // not called when no attachments

	routeID := uuid.New()
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	assert.Empty(t, result)
	caRepo.AssertExpectations(t)
}

// =========================================================================
// GetEffectiveIPAllowlist - with client IP attachments
// =========================================================================

func TestRouteService_GetEffectiveIPs_WithAttachments(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, cipRepo := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()

	clientAttachment := models.ClientRouteAttachment{
		ID:                uuid.New(),
		ClientID:          clientID,
		RouteID:           routeID,
		EnableIPAllowlist: true,
		Client:            &models.Client{ID: clientID, Name: "test-client"},
	}

	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{clientAttachment}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	cipRepo.On("ListByClientID", clientID).Return([]models.ClientIPAddress{
		{ID: uuid.New(), ClientID: clientID, CIDR: "10.0.0.0/8", Description: "Internal"},
		{ID: uuid.New(), ClientID: clientID, CIDR: "192.168.1.0/24", Description: "Office"},
	}, nil)

	result, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "10.0.0.0/8", result[0].CIDR)
	assert.Equal(t, "test-client", result[0].ClientName)
	assert.Equal(t, "192.168.1.0/24", result[1].CIDR)
	caRepo.AssertExpectations(t)
	cipRepo.AssertExpectations(t)
}

// =========================================================================
// GetEffectiveIPAllowlist - skips attachments without IP allowlist
// =========================================================================

func TestRouteService_GetEffectiveIPs_SkipsNonIPAttachments(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()

	clientAttachment := models.ClientRouteAttachment{
		ID:                uuid.New(),
		ClientID:          clientID,
		RouteID:           routeID,
		EnableIPAllowlist: false, // IP allowlist not enabled
		EnableAPIKey:      true,
	}

	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{clientAttachment}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	assert.Empty(t, result)
}

// =========================================================================
// GetEffectiveIPAllowlist - error listing active attachments
// =========================================================================

func TestRouteService_GetEffectiveIPs_ErrorListingActive(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment(nil), errors.New("db error"))

	result, err := svc.GetEffectiveIPAllowlist(routeID)

	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list active attachments")
}

// =========================================================================
// PreviewCreate - success
// =========================================================================

func TestRouteService_PreviewCreate_Success(t *testing.T) {
	svc, _, _, _, domainRepo, _ := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()

	domain := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Hostname:  "example.com",
	}

	domainRepo.On("GetByID", domainID).Return(domain, nil)

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: uuid.New(),
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.PreviewCreate(domainID, input)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.ProposedYAML)
	assert.Contains(t, result.ProposedYAML, "HTTPRoute")
	assert.Contains(t, result.ProposedYAML, "example.com")
	domainRepo.AssertExpectations(t)
}

// =========================================================================
// PreviewCreate - invalid route name
// =========================================================================

func TestRouteService_PreviewCreate_InvalidName(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	input := &services.CreateRouteInput{
		Name:   "INVALID NAME",
		TeamID: uuid.New(),
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.PreviewCreate(uuid.New(), input)

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "route name must be lowercase")
}

// =========================================================================
// PreviewCreate - domain not found
// =========================================================================

func TestRouteService_PreviewCreate_DomainNotFound(t *testing.T) {
	svc, _, _, _, domainRepo, _ := newTestRouteService()

	domainID := uuid.New()
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: uuid.New(),
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.PreviewCreate(domainID, input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "domain not found")
}

// =========================================================================
// PreviewUpdate - success
// =========================================================================

func TestRouteService_PreviewUpdate_Success(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, cipRepo := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{
		ID:       domainID,
		Hostname: "example.com",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	// collectClientIPCIDRs calls
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	_ = cipRepo

	input := &services.UpdateRouteInput{
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api/v2/users"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "user-svc-v2", Namespace: "default", Port: 8080},
			},
		},
	}

	result, err := svc.PreviewUpdate(routeID, input)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.CurrentYAML)
	assert.NotEmpty(t, result.ProposedYAML)
	assert.Contains(t, result.CurrentYAML, "user-svc")
	assert.Contains(t, result.ProposedYAML, "user-svc-v2")
}

// =========================================================================
// PreviewUpdate - route not found
// =========================================================================

func TestRouteService_PreviewUpdate_RouteNotFound(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("not found"))

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.PreviewUpdate(routeID, input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "route not found")
}

// =========================================================================
// PreviewUpdate - domain not found
// =========================================================================

func TestRouteService_PreviewUpdate_DomainNotFound(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, _, _, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.PreviewUpdate(routeID, input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "domain not found")
}

// =========================================================================
// PreviewDelete - success
// =========================================================================

func TestRouteService_PreviewDelete_Success(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{
		ID:       domainID,
		Hostname: "example.com",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.PreviewDelete(routeID)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.CurrentYAML)
	assert.Contains(t, result.CurrentYAML, "HTTPRoute")
	assert.Empty(t, result.CurrentSecurityPolicyYAML)
}

// =========================================================================
// PreviewDelete - route not found
// =========================================================================

func TestRouteService_PreviewDelete_RouteNotFound(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.PreviewDelete(routeID)

	assert.Nil(t, result)
	assert.EqualError(t, err, "route not found")
}

// =========================================================================
// PreviewDelete - domain not found
// =========================================================================

func TestRouteService_PreviewDelete_DomainNotFound(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, _, _, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))

	result, err := svc.PreviewDelete(routeID)

	assert.Nil(t, result)
	assert.EqualError(t, err, "domain not found")
}

// =========================================================================
// Deploy - route not found
// =========================================================================

func TestRouteService_Deploy_RouteNotFound(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Error(t, err)
}

// =========================================================================
// Deploy - route not approved
// =========================================================================

func TestRouteService_Deploy_NotApproved(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Status: models.RouteStatusPendingCreate, // Not approved
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route is not approved for deployment")
}

// =========================================================================
// Deploy - no approved request found
// =========================================================================

func TestRouteService_Deploy_NoApprovedRequest(t *testing.T) {
	svc, routeRepo, approvalRepo, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Status: models.RouteStatusApproved,
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "no approved request found for this route")
}

// =========================================================================
// Create - invalid route name format (uppercase)
// =========================================================================

func TestRouteService_Create_InvalidNameFormat(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	input := &services.CreateRouteInput{
		Name:   "UserAPI",
		TeamID: uuid.New(),
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Create(uuid.New(), input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "route name must be lowercase")
}

// =========================================================================
// Create - missing backends
// =========================================================================

func TestRouteService_Create_NoBackends(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	teamID := uuid.New()
	routeRepo.On("ExistsByName", domainID, "user-api").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: uuid.New()}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "at least one backend is required")
}

// =========================================================================
// Create - missing path matching
// =========================================================================

func TestRouteService_Create_NoPathMatching(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "user-api").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches:  []models.RouteMatch{},
			Backends: []models.RouteBackend{{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80}},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "path matching is required")
}

// =========================================================================
// Create - matcher conflict with existing route
// =========================================================================

func TestRouteService_Create_MatcherConflict(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	existingRoute := models.Route{
		ID:   uuid.New(),
		Name: "existing-route",
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api/users"}},
			},
		},
	}

	routeRepo.On("ExistsByName", domainID, "new-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{existingRoute}, int64(1), nil)

	input := &services.CreateRouteInput{
		Name:   "new-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api/users"}}, // Same matcher
			},
			Backends: []models.RouteBackend{{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80}},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "route matcher conflicts")
}

// =========================================================================
// CheckMatcherConflicts
// =========================================================================

func TestRouteService_CheckMatcherConflicts_Found(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	domainID := uuid.New()
	existingRoute := models.Route{
		ID:   uuid.New(),
		Name: "existing-route",
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api/users"}},
			},
		},
	}

	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{existingRoute}, int64(1), nil)

	match := models.RouteMatch{
		Path: &models.PathMatch{Type: "Prefix", Value: "/api/users"},
	}

	conflicts, err := svc.CheckMatcherConflicts(domainID, match, nil)

	assert.NoError(t, err)
	assert.Len(t, conflicts, 1)
	assert.Equal(t, existingRoute.ID, conflicts[0].RouteID)
	assert.Equal(t, "existing-route", conflicts[0].RouteName)
}

func TestRouteService_CheckMatcherConflicts_NoConflict(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	domainID := uuid.New()
	existingRoute := models.Route{
		ID:   uuid.New(),
		Name: "existing-route",
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api/users"}},
			},
		},
	}

	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{existingRoute}, int64(1), nil)

	match := models.RouteMatch{
		Path: &models.PathMatch{Type: "Prefix", Value: "/api/orders"},
	}

	conflicts, err := svc.CheckMatcherConflicts(domainID, match, nil)

	assert.NoError(t, err)
	assert.Len(t, conflicts, 0)
}

func TestRouteService_CheckMatcherConflicts_ExcludesSelf(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	domainID := uuid.New()
	routeID := uuid.New()
	existingRoute := models.Route{
		ID:   routeID,
		Name: "my-route",
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api/users"}},
			},
		},
	}

	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{existingRoute}, int64(1), nil)

	match := models.RouteMatch{
		Path: &models.PathMatch{Type: "Prefix", Value: "/api/users"},
	}

	conflicts, err := svc.CheckMatcherConflicts(domainID, match, &routeID)

	assert.NoError(t, err)
	assert.Len(t, conflicts, 0)
}

// =========================================================================
// GenerateYAML - success
// =========================================================================

func TestRouteService_GenerateYAML_Success(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{
		ID:       domainID,
		Hostname: "example.com",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	result, err := svc.GenerateYAML(routeID)

	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "HTTPRoute")
}

// =========================================================================
// GenerateYAML - route not found
// =========================================================================

func TestRouteService_GenerateYAML_RouteNotFound(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.GenerateYAML(routeID)

	assert.Empty(t, result)
	assert.Error(t, err)
}

// =========================================================================
// Update - with BackendTrafficPolicy
// =========================================================================

func TestRouteService_Update_WithBackendTrafficPolicy(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		Status:       models.RouteStatusActive,
		SecurityMode: models.SecurityModeGeneral,
		Config:       makeBasicHTTPRouteConfig(),
		K8sRouteName: "user-api-12345678",
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID, Hostname: "example.com"}

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			Retry: &models.RetryConfig{NumRetries: routeInt32Ptr(3)},
		},
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	secRepo.On("DeleteByRouteID", routeID).Return(nil)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("Upsert", mock.AnythingOfType("*models.BackendTrafficPolicy")).Return(nil)
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("DeleteByRouteID", routeID).Return(nil)
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.Update(routeID, input, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingUpdate, result.Status)
	btpRepo.AssertExpectations(t)
}

// =========================================================================
// Update - gRPC route rejects redirect
// =========================================================================

func TestRouteService_Update_GRPCRoute_RejectsRedirect(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "grpc-route",
		Status:   models.RouteStatusActive,
		Protocol: models.RouteProtocolGRPC,
		Config: models.RouteConfig{
			Backends: []models.RouteBackend{{Type: models.BackendTypeKubernetes, Service: "grpc-svc", Namespace: "default", Port: 9090}},
		},
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	input := &services.UpdateRouteInput{
		Config: models.RouteConfig{
			RouteType: models.RouteTypeRedirect,
			Backends:  []models.RouteBackend{{Type: models.BackendTypeKubernetes, Service: "grpc-svc", Namespace: "default", Port: 9090}},
		},
	}

	result, err := svc.Update(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "redirect is not supported for gRPC routes")
}

// =========================================================================
// Update - HTTP route rejects gRPC fields
// =========================================================================

func TestRouteService_Update_HTTPRoute_RejectsGRPCFields(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "http-route",
		Status:   models.RouteStatusActive,
		Protocol: models.RouteProtocolHTTP,
		Config:   makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	input := &services.UpdateRouteInput{
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{
					Path:        &models.PathMatch{Type: "Prefix", Value: "/api"},
					GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "myservice"},
				},
			},
			Backends: []models.RouteBackend{{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80}},
		},
	}

	result, err := svc.Update(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "grpcService is only supported for gRPC routes")
}

// =========================================================================
// Delete - with policy repos capturing previous configs
// =========================================================================

func TestRouteService_Delete_WithPolicies(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
		Status:   models.RouteStatusActive,
		Config:   makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		RouteID: routeID,
		Config:  models.SecurityPolicyConfig{CORS: &models.CORSConfig{AllowOrigins: []string{"*"}}},
	}, nil)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.Delete(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDelete, result.Status)

	// Verify the approval was created with config snapshot containing previous SP
	approvalRepo.AssertCalled(t, "Create", mock.AnythingOfType("*models.Approval"))
}

// =========================================================================
// PreviewCreate - with SecurityPolicy
// =========================================================================

func TestRouteService_PreviewCreate_WithSecurityPolicy(t *testing.T) {
	svc, _, _, _, domainRepo, _ := newTestRouteService()

	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, ProjectID: uuid.New(), Hostname: "example.com"}
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: uuid.New(),
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://example.com"},
			},
		},
	}

	result, err := svc.PreviewCreate(domainID, input)

	require.NoError(t, err)
	assert.NotEmpty(t, result.ProposedYAML)
	assert.NotEmpty(t, result.ProposedSecurityPolicyYAML)
}

// =========================================================================
// PreviewDelete - with BTP and SecurityPolicy
// =========================================================================

func TestRouteService_PreviewDelete_WithPolicies(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, Hostname: "example.com"}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		RouteID: routeID,
		Config:  models.SecurityPolicyConfig{CORS: &models.CORSConfig{AllowOrigins: []string{"*"}}},
	}, nil)
	btpRepo.On("GetByRouteID", routeID).Return(&models.BackendTrafficPolicy{
		RouteID: &routeID,
		Config:  models.BackendTrafficPolicyConfig{Retry: &models.RetryConfig{NumRetries: routeInt32Ptr(3)}},
	}, nil)
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.PreviewDelete(routeID)

	require.NoError(t, err)
	assert.NotEmpty(t, result.CurrentYAML)
	assert.NotEmpty(t, result.CurrentSecurityPolicyYAML)
	assert.NotEmpty(t, result.CurrentBackendTrafficPolicyYAML)
}

// =========================================================================
// Update - description update
// =========================================================================

func TestRouteService_Update_DescriptionUpdated(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		Description:  "old description",
		Status:       models.RouteStatusActive,
		Config:       makeBasicHTTPRouteConfig(),
		K8sRouteName: "user-api-12345678",
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	input := &services.UpdateRouteInput{
		Description: "new description",
		Config:      makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Update(routeID, input, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "new description", result.Description)
}

// =========================================================================
// Update - approval snapshot contains config
// =========================================================================

func TestRouteService_Update_ApprovalHasConfigSnapshot(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		Status:       models.RouteStatusActive,
		Config:       makeBasicHTTPRouteConfig(),
		K8sRouteName: "user-api-12345678",
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()

	var capturedApproval *models.Approval
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Run(func(args mock.Arguments) {
		capturedApproval = args.Get(0).(*models.Approval)
	}).Return(nil)

	newConfig := models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/api/v2"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "v2-svc", Namespace: "default", Port: 8080},
		},
	}

	input := &services.UpdateRouteInput{
		Config:            newConfig,
		ChangeDescription: "upgrade to v2",
	}

	result, err := svc.Update(routeID, input, uuid.New())

	require.NoError(t, err)
	assert.NotNil(t, result.PendingApproval)

	// Verify the captured approval has the right fields
	require.NotNil(t, capturedApproval)
	assert.Equal(t, models.ApprovalActionUpdate, capturedApproval.Action)
	assert.Equal(t, "upgrade to v2", capturedApproval.ChangeDescription)
	assert.NotEmpty(t, capturedApproval.ConfigSnapshot)
	assert.NotEmpty(t, capturedApproval.PreviousConfig)

	// Verify ConfigSnapshot contains the new config
	var snapshot models.RouteApprovalSnapshot
	err = json.Unmarshal(capturedApproval.ConfigSnapshot, &snapshot)
	require.NoError(t, err)
	assert.Equal(t, "/api/v2", snapshot.RouteConfig.Matches[0].Path.Value)

	// Verify PreviousConfig contains the old config
	var prevSnapshot models.RouteApprovalSnapshot
	err = json.Unmarshal(capturedApproval.PreviousConfig, &prevSnapshot)
	require.NoError(t, err)
	assert.Equal(t, "/api/users", prevSnapshot.RouteConfig.Matches[0].Path.Value)
}

// TestRouteService_Update_ApprovalSnapshot_PreservesExtProc pins the approval
// snapshot's EnvoyExtensionPolicy to include ExtProc on Update, the same way
// TestRouteService_Create_ApprovalSnapshot_PreservesExtProc pins it on
// Create. The input carries ExtProc alone -- no Lua, no Wasm -- so
// EnvoyExtensionPolicyInput.HasContent() (internal/routeplan/input.go) is
// true purely because of ExtProc, and the snapshot must still carry it.
//
// This guards against the drift once present in route_write.go's Update:
// updateSnapshotEEP was built with only Lua and Wasm, silently dropping
// ExtProc from the snapshot that becomes the approval's ConfigSnapshot
// (route_write.go ~line 748) -- the very payload ApprovalService.GetDiff
// renders as the proposed EnvoyExtensionPolicy YAML for whoever approves the
// change. An ext-proc change on a route update was therefore invisible to
// the approver, even though the entity actually persisted for deploy
// (envoyExtensionPolicyRepo.Upsert, route_write.go ~line 844) always
// included ExtProc correctly -- so this is an approval-review integrity
// defect, not a deploy-path one.
func TestRouteService_Update_ApprovalSnapshot_PreservesExtProc(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		Status:       models.RouteStatusActive,
		Config:       makeBasicHTTPRouteConfig(),
		K8sRouteName: "user-api-12345678",
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()

	var capturedApproval *models.Approval
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Run(func(args mock.Arguments) {
		capturedApproval = args.Get(0).(*models.Approval)
	}).Return(nil)

	extProc := &models.ExtProcExtensionConfig{
		BackendRef: models.ExtProcBackendRef{Name: "ext-proc-svc", Namespace: "default", Port: 9000},
	}

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
		ExtensionPolicy: &routeplan.EnvoyExtensionPolicyInput{
			ExtProc: extProc,
		},
		ChangeDescription: "add ext_proc",
	}

	result, err := svc.Update(routeID, input, uuid.New())

	require.NoError(t, err)
	require.NotNil(t, result.PendingApproval)
	require.NotNil(t, capturedApproval)

	var snapshot models.RouteApprovalSnapshot
	err = json.Unmarshal(capturedApproval.ConfigSnapshot, &snapshot)
	require.NoError(t, err)

	require.NotNil(t, snapshot.EnvoyExtensionPolicy, "approval snapshot dropped the EnvoyExtensionPolicy entirely")
	assert.Equal(t, extProc, snapshot.EnvoyExtensionPolicy.ExtProc,
		"approval snapshot dropped ExtProc: an ext-proc-only change on update would be invisible to whoever approves it")
}

// TestRouteService_Create_ApprovalSnapshot_PreservesExtProc is the mirror of
// TestRouteService_Update_ApprovalSnapshot_PreservesExtProc for Create.
// Create's snapshotEEP already includes ExtProc, so this passes immediately
// -- it exists to pin both paths symmetrically so the drift found in Update
// cannot silently reappear in either direction.
func TestRouteService_Create_ApprovalSnapshot_PreservesExtProc(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()
	createdBy := uuid.New()

	domain := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Hostname:  "example.com",
	}

	extProc := &models.ExtProcExtensionConfig{
		BackendRef: models.ExtProcBackendRef{Name: "ext-proc-svc", Namespace: "default", Port: 9000},
	}

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		ExtensionPolicy: &routeplan.EnvoyExtensionPolicyInput{
			ExtProc: extProc,
		},
	}

	routeRepo.On("ExistsByName", domainID, "user-api").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID, Name: "platform"}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	routeRepo.On("Create", mock.AnythingOfType("*models.Route")).Return(nil)
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()

	var capturedApproval *models.Approval
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Run(func(args mock.Arguments) {
		capturedApproval = args.Get(0).(*models.Approval)
	}).Return(nil)

	result, err := svc.Create(domainID, input, createdBy)

	require.NoError(t, err)
	require.NotNil(t, result.PendingApproval)
	require.NotNil(t, capturedApproval)

	var snapshot models.RouteApprovalSnapshot
	err = json.Unmarshal(capturedApproval.ConfigSnapshot, &snapshot)
	require.NoError(t, err)

	require.NotNil(t, snapshot.EnvoyExtensionPolicy)
	assert.Equal(t, extProc, snapshot.EnvoyExtensionPolicy.ExtProc)
}

// =========================================================================
// Deploy - route active but wrong status
// =========================================================================

func TestRouteService_Deploy_ActiveRouteNotDeployable(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Status: models.RouteStatusActive,
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route is not approved for deployment")
}

// =========================================================================
// ListByDomainID - error from repo
// =========================================================================

func TestRouteService_ListByDomainID_Error(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	domainID := uuid.New()
	routeRepo.On("ListByDomainID", domainID, 1, 10, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route(nil), int64(0), errors.New("db error"))

	result, total, err := svc.ListByDomainID(domainID, 1, 10, nil, "", "", "", nil)

	assert.Nil(t, result)
	assert.Equal(t, int64(0), total)
	assert.Error(t, err)
}

// =========================================================================
// Update - labels validation
// =========================================================================

func TestRouteService_Update_WithLabels(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		Status:       models.RouteStatusActive,
		Config:       makeBasicHTTPRouteConfig(),
		K8sRouteName: "user-api-12345678",
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
		Labels: models.Labels{"env": "production", "team": "platform"},
	}

	result, err := svc.Update(routeID, input, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "production", result.Labels["env"])
	assert.Equal(t, "platform", result.Labels["team"])
}

// =========================================================================
// Update - direct response route type validations
// =========================================================================

func TestRouteService_Update_DirectResponse_CannotHaveBackends(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "dr-route",
		Status:   models.RouteStatusActive,
		Config:   makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.UpdateRouteInput{
		Config: models.RouteConfig{
			RouteType: models.RouteTypeDirectResponse,
			DirectResponse: &models.DirectResponseConfig{
				StatusCode: 200,
				Body:       &models.DirectResponseBody{Type: "Inline", Inline: "OK"},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Update(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "directResponse routes cannot have backends")
}

// =========================================================================
// GenerateYAMLs - with BackendTrafficPolicy
// =========================================================================

func TestRouteService_GenerateYAMLs_WithBTP(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, Hostname: "example.com"}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(&models.BackendTrafficPolicy{
		RouteID: &routeID,
		Config:  models.BackendTrafficPolicyConfig{Retry: &models.RetryConfig{NumRetries: routeInt32Ptr(3)}},
	}, nil)
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GenerateYAMLs(routeID)

	require.NoError(t, err)
	assert.NotEmpty(t, result.HTTPRouteYAML)
	assert.NotEmpty(t, result.BackendTrafficPolicyYAML)
	assert.Contains(t, result.BackendTrafficPolicyYAML, "BackendTrafficPolicy")
}

// =========================================================================
// PreviewUpdate - with SecurityPolicy changes (current and proposed)
// =========================================================================

func TestRouteService_PreviewUpdate_WithSecurityPolicyChanges(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, Hostname: "example.com"}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		RouteID: routeID,
		Config:  models.SecurityPolicyConfig{CORS: &models.CORSConfig{AllowOrigins: []string{"*"}}},
	}, nil)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://new-origin.com"},
				AllowMethods: []string{"GET"},
			},
		},
	}

	result, err := svc.PreviewUpdate(routeID, input)

	require.NoError(t, err)
	assert.NotEmpty(t, result.CurrentSecurityPolicyYAML)
	assert.NotEmpty(t, result.ProposedSecurityPolicyYAML)
}

// =========================================================================
// Create - route name ending with dash (invalid K8s name)
// =========================================================================

func TestRouteService_Create_InvalidNameEndingWithDash(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	input := &services.CreateRouteInput{
		Name:   "user-api-",
		TeamID: uuid.New(),
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Create(uuid.New(), input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "route name must be lowercase")
}

// =========================================================================
// Create - security mode general validation with invalid OIDC
// =========================================================================

func TestRouteService_Create_GeneralMode_InvalidOIDC(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "oidc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:         "oidc-route",
		TeamID:       teamID,
		SecurityMode: models.SecurityModeGeneral,
		Config:       makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			OIDC: &routeplan.OIDCInput{
				Issuer: "", // Missing required field
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "OIDC issuer is required")
}

// =========================================================================
// Create - gRPC route rejects redirect
// =========================================================================

func TestRouteService_Create_GRPCRoute_RejectsRedirect(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "grpc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:     "grpc-route",
		TeamID:   teamID,
		Protocol: models.RouteProtocolGRPC,
		Config: models.RouteConfig{
			RouteType: models.RouteTypeRedirect,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "redirect is not supported for gRPC routes")
}

// =========================================================================
// Create - gRPC route rejects direct response
// =========================================================================

func TestRouteService_Create_GRPCRoute_RejectsDirectResponse(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "grpc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:     "grpc-route",
		TeamID:   teamID,
		Protocol: models.RouteProtocolGRPC,
		Config: models.RouteConfig{
			DirectResponse: &models.DirectResponseConfig{StatusCode: 200},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "direct response config is not supported for gRPC routes")
}

// =========================================================================
// Create - gRPC route rejects path matching
// =========================================================================

func TestRouteService_Create_GRPCRoute_RejectsPathMatching(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "grpc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:     "grpc-route",
		TeamID:   teamID,
		Protocol: models.RouteProtocolGRPC,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "path matching is not supported for gRPC routes")
}

// =========================================================================
// Create - gRPC route rejects URL rewrite
// =========================================================================

func TestRouteService_Create_GRPCRoute_RejectsURLRewrite(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "grpc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:     "grpc-route",
		TeamID:   teamID,
		Protocol: models.RouteProtocolGRPC,
		Config: models.RouteConfig{
			URLRewrite: &models.URLRewrite{Hostname: routeStringPtr("new.example.com")},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "URL rewrite is not supported for gRPC routes")
}

// =========================================================================
// Create - HTTP route rejects gRPC fields
// =========================================================================

func TestRouteService_Create_HTTPRoute_RejectsGRPCFields(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "http-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:   "http-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "hello.Greeter"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "grpcService is only supported for gRPC routes")
}

// =========================================================================
// Create - invalid security mode
// =========================================================================

func TestRouteService_Create_InvalidSecurityMode(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "test-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:         "test-route",
		TeamID:       teamID,
		SecurityMode: models.SecurityMode("invalid"),
		Config:       makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid security mode")
}

// =========================================================================
// Create - client mode rejects OIDC
// =========================================================================

func TestRouteService_Create_ClientMode_RejectsOIDC(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "client-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:         "client-route",
		TeamID:       teamID,
		SecurityMode: models.SecurityModeClient,
		Config:       makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			OIDC: &routeplan.OIDCInput{
				Issuer:           "https://issuer.com",
				ClientID:         "client-id",
				ClientSecretName: "secret",
				RedirectURL:      "https://example.com/callback",
				LogoutPath:       "/logout",
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "OIDC is not available in client security mode")
}

// =========================================================================
// Create - client mode rejects JWT
// =========================================================================

func TestRouteService_Create_ClientMode_RejectsJWT(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "client-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:         "client-route",
		TeamID:       teamID,
		SecurityMode: models.SecurityModeClient,
		Config:       makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			JWT: &routeplan.JWTInput{
				Issuer:  "https://issuer.com",
				JWKSURL: "https://issuer.com/.well-known/jwks.json",
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "JWT auth is not available in client security mode")
}

// =========================================================================
// Create - client mode rejects API key auth
// =========================================================================

func TestRouteService_Create_ClientMode_RejectsAPIKeyAuth(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "client-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:         "client-route",
		TeamID:       teamID,
		SecurityMode: models.SecurityModeClient,
		Config:       makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			APIKeyAuth: &routeplan.APIKeyAuthInput{
				SecretName: "my-secret",
				HeaderName: "x-api-key",
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "API key auth is not available in client security mode")
}

// =========================================================================
// Create - general mode validates JWT fields
// =========================================================================

func TestRouteService_Create_GeneralMode_InvalidJWT(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "jwt-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "jwt-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			JWT: &routeplan.JWTInput{
				Issuer: "", // Missing required field
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "JWT issuer is required")
}

// =========================================================================
// Create - general mode validates JWT jwksUrl
// =========================================================================

func TestRouteService_Create_GeneralMode_JWT_MissingJWKSURL(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "jwt-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "jwt-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			JWT: &routeplan.JWTInput{
				Issuer:  "https://issuer.com",
				JWKSURL: "", // Missing
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "JWT jwksUrl is required")
}

// =========================================================================
// Create - general mode validates API key fields
// =========================================================================

func TestRouteService_Create_GeneralMode_APIKey_MissingSecretName(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "apikey-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "apikey-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			APIKeyAuth: &routeplan.APIKeyAuthInput{
				SecretName: "",
				HeaderName: "x-api-key",
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "API key secretName is required")
}

// =========================================================================
// Create - general mode validates API key header name
// =========================================================================

func TestRouteService_Create_GeneralMode_APIKey_MissingHeaderName(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "apikey-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "apikey-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			APIKeyAuth: &routeplan.APIKeyAuthInput{
				SecretName: "my-secret",
				HeaderName: "",
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "API key headerName is required")
}

// =========================================================================
// Create - general mode validates OIDC HTTPS redirect URL
// =========================================================================

func TestRouteService_Create_GeneralMode_OIDC_NonHTTPS_RedirectURL(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "oidc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "oidc-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			OIDC: &routeplan.OIDCInput{
				Issuer:           "https://issuer.com",
				ClientID:         "client-id",
				ClientSecretName: "secret",
				RedirectURL:      "http://example.com/callback", // non-HTTPS
				LogoutPath:       "/logout",
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "OIDC redirectURL must use HTTPS")
}

// =========================================================================
// Create - general mode validates OIDC missing fields
// =========================================================================

func TestRouteService_Create_GeneralMode_OIDC_MissingClientID(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "oidc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "oidc-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			OIDC: &routeplan.OIDCInput{
				Issuer:   "https://issuer.com",
				ClientID: "", // Missing
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "OIDC clientId is required")
}

// =========================================================================
// Create - general mode validates OIDC missing client secret name
// =========================================================================

func TestRouteService_Create_GeneralMode_OIDC_MissingClientSecretName(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "oidc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "oidc-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			OIDC: &routeplan.OIDCInput{
				Issuer:           "https://issuer.com",
				ClientID:         "client-id",
				ClientSecretName: "", // Missing
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "OIDC clientSecretName is required")
}

// =========================================================================
// Create - general mode validates OIDC missing logout path
// =========================================================================

func TestRouteService_Create_GeneralMode_OIDC_MissingLogoutPath(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "oidc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "oidc-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			OIDC: &routeplan.OIDCInput{
				Issuer:           "https://issuer.com",
				ClientID:         "client-id",
				ClientSecretName: "secret",
				RedirectURL:      "https://example.com/callback",
				LogoutPath:       "", // Missing
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "OIDC logoutPath is required")
}

// =========================================================================
// Create - general mode validates IP allowlist invalid CIDR
// =========================================================================

func TestRouteService_Create_GeneralMode_InvalidCIDR(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "ip-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "ip-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			Authorization: &routeplan.AuthorizationInput{
				AllowedCIDRs: []string{"not-a-cidr"},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid CIDR or IP address")
}

// =========================================================================
// Create - general mode validates empty IP allowlist
// =========================================================================

func TestRouteService_Create_GeneralMode_EmptyCIDRList(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "ip-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "ip-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			Authorization: &routeplan.AuthorizationInput{
				AllowedCIDRs: []string{},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "authorization requires at least one of: allowedCIDRs, headers, or methods")
}

// =========================================================================
// Create - redirect route requires redirect config
// =========================================================================

func TestRouteService_Create_RedirectRoute_MissingRedirectConfig(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "redir-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "redir-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			RouteType: models.RouteTypeRedirect,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/old"}},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "redirect configuration is required for redirect routes")
}

// =========================================================================
// Create - direct response route cannot have backends
// =========================================================================

func TestRouteService_Create_DirectResponseRoute_CannotHaveBackends(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "dr-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:   "dr-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			RouteType: models.RouteTypeDirectResponse,
			DirectResponse: &models.DirectResponseConfig{
				StatusCode: 200,
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "directResponse routes cannot have backends")
}

// =========================================================================
// Create - direct response route cannot have URL rewrite
// =========================================================================

func TestRouteService_Create_DirectResponseRoute_CannotHaveURLRewrite(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "dr-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:   "dr-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			RouteType: models.RouteTypeDirectResponse,
			DirectResponse: &models.DirectResponseConfig{
				StatusCode: 200,
			},
			URLRewrite: &models.URLRewrite{Hostname: routeStringPtr("new.example.com")},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "directResponse routes cannot have URL rewrite")
}

// =========================================================================
// Create - direct response route cannot have request header modifier
// =========================================================================

func TestRouteService_Create_DirectResponseRoute_CannotHaveHeaderModifier(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "dr-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:   "dr-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			RouteType: models.RouteTypeDirectResponse,
			DirectResponse: &models.DirectResponseConfig{
				StatusCode: 200,
			},
			RequestHeaderModifier: &models.HeaderModifier{
				Set: []models.HeaderValue{{Name: "X-Custom", Value: "test"}},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "directResponse routes cannot have request header modifier")
}

// =========================================================================
// Create - team not found
// =========================================================================

func TestRouteService_Create_TeamNotFound(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "user-api").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(nil, errors.New("not found"))

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "team not found")
}

// =========================================================================
// Create - mirror target same as primary backend
// =========================================================================

func TestRouteService_Create_MirrorSameAsPrimary(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "mirror-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:   "mirror-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
			Mirrors: []models.MirrorBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot be the same as a primary backend")
}

// =========================================================================
// Create - mirror without primary backend
// =========================================================================

func TestRouteService_Create_MirrorWithoutPrimaryBackend(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "mirror-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:   "mirror-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{},
			Mirrors: []models.MirrorBackend{
				{Type: models.BackendTypeKubernetes, Service: "mirror-svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "must have at least one primary backend")
}

// =========================================================================
// Create - backend missing service name
// =========================================================================

func TestRouteService_Create_BackendMissingService(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "svc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)

	input := &services.CreateRouteInput{
		Name:   "svc-route",
		TeamID: uuid.New(),
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "service is required")
}

// =========================================================================
// Create - backend invalid port
// =========================================================================

func TestRouteService_Create_BackendInvalidPort(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "port-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)

	input := &services.CreateRouteInput{
		Name:   "port-route",
		TeamID: uuid.New(),
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 0},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "port must be greater than 0")
}

// =========================================================================
// Create - external backend missing address
// =========================================================================

func TestRouteService_Create_ExternalBackendMissingAddress(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "ext-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)

	input := &services.CreateRouteInput{
		Name:   "ext-route",
		TeamID: uuid.New(),
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeExternal, Address: "", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "address is required")
}

// =========================================================================
// Create - mirror backend missing fields
// =========================================================================

func TestRouteService_Create_MirrorMissingNamespace(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "mirror-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)

	input := &services.CreateRouteInput{
		Name:   "mirror-route",
		TeamID: uuid.New(),
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
			Mirrors: []models.MirrorBackend{
				{Service: "mirror-svc", Namespace: "", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "namespace is required")
}

// =========================================================================
// GenerateYAML - domain not found
// =========================================================================

func TestRouteService_GenerateYAML_DomainNotFound(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))

	result, err := svc.GenerateYAML(routeID)

	assert.Empty(t, result)
	assert.Error(t, err)
}

// =========================================================================
// GenerateYAMLs - domain not found
// =========================================================================

func TestRouteService_GenerateYAMLs_DomainNotFound(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))

	result, err := svc.GenerateYAMLs(routeID)

	assert.Nil(t, result)
	assert.Error(t, err)
}

// =========================================================================
// PreviewCreate - with backend traffic policy
// =========================================================================

func TestRouteService_PreviewCreate_WithBTP(t *testing.T) {
	svc, _, _, _, domainRepo, _ := newTestRouteService()

	domainID := uuid.New()

	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, Hostname: "example.com"}, nil)

	input := &services.CreateRouteInput{
		Name:   "btp-route",
		TeamID: uuid.New(),
		Config: makeBasicHTTPRouteConfig(),
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			Retry: &models.RetryConfig{NumRetries: routeInt32Ptr(3)},
		},
	}

	result, err := svc.PreviewCreate(domainID, input)

	require.NoError(t, err)
	assert.NotEmpty(t, result.ProposedYAML)
	assert.NotEmpty(t, result.ProposedBackendTrafficPolicyYAML)
}

// =========================================================================
// PreviewUpdate - with BTP changes
// =========================================================================

func TestRouteService_PreviewUpdate_WithBTPChanges(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, Hostname: "example.com"}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	input := &services.UpdateRouteInput{
		Config: makeBasicHTTPRouteConfig(),
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			Retry: &models.RetryConfig{NumRetries: routeInt32Ptr(5)},
		},
	}

	result, err := svc.PreviewUpdate(routeID, input)

	require.NoError(t, err)
	assert.NotEmpty(t, result.CurrentYAML)
	assert.NotEmpty(t, result.ProposedYAML)
	assert.NotEmpty(t, result.ProposedBackendTrafficPolicyYAML)
}

// =========================================================================
// PreviewDelete - with BTP
// =========================================================================

func TestRouteService_PreviewDelete_WithBTP(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "user-api",
		K8sRouteName: "user-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{ID: domainID, Hostname: "example.com"}

	btpPolicy := &models.BackendTrafficPolicy{
		ID:      uuid.New(),
		RouteID: &routeID,
		Config:  models.BackendTrafficPolicyConfig{Retry: &models.RetryConfig{NumRetries: routeInt32Ptr(3)}},
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(btpPolicy, nil)
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.PreviewDelete(routeID)

	require.NoError(t, err)
	assert.NotEmpty(t, result.CurrentYAML)
	assert.NotEmpty(t, result.CurrentBackendTrafficPolicyYAML)
}

// =========================================================================
// GetEffectiveIPAllowlist - approved attachments included
// =========================================================================

func TestRouteService_GetEffectiveIPs_IncludesApprovedAttachments(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, cipRepo := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()

	// No active attachments
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	// Approved attachment with IP allowlist
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          clientID,
			RouteID:           routeID,
			EnableIPAllowlist: true,
			Client:            &models.Client{Name: "approved-client"},
		},
	}, nil)
	cipRepo.On("ListByClientID", clientID).Return([]models.ClientIPAddress{
		{CIDR: "192.168.1.0/24", Description: "Office"},
	}, nil)

	entries, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "192.168.1.0/24", entries[0].CIDR)
	assert.Equal(t, "approved-client", entries[0].ClientName)
	assert.Equal(t, "Office", entries[0].Description)
}

// =========================================================================
// GetEffectiveIPAllowlist - error listing approved attachments
// =========================================================================

func TestRouteService_GetEffectiveIPs_ErrorListingApproved(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, cipRepo := newTestRouteServiceFull()

	routeID := uuid.New()

	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment(nil), errors.New("db error"))
	_ = cipRepo

	entries, err := svc.GetEffectiveIPAllowlist(routeID)

	assert.Nil(t, entries)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list approved attachments")
}

// =========================================================================
// Delete - non-active route (pending_create) can be deleted
// =========================================================================

func TestRouteService_Delete_PendingCreateRoute(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
		Status:   models.RouteStatusPendingCreate,
	}

	domain := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	result, err := svc.Delete(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDelete, result.Status)
}

// =========================================================================
// GenerateYAMLs - with SecurityPolicy includes CORS
// =========================================================================

func TestRouteService_GenerateYAMLs_WithCORS(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	domainID := uuid.New()

	route := &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "cors-api",
		K8sRouteName: "cors-api-12345678",
		Config:       makeBasicHTTPRouteConfig(),
	}
	domain := &models.Domain{
		ID:       domainID,
		Hostname: "example.com",
	}

	secPolicy := &models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://example.com"},
				AllowMethods: []string{"GET", "POST"},
			},
		},
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	secRepo.On("GetByRouteID", routeID).Return(secPolicy, nil)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GenerateYAMLs(routeID)

	require.NoError(t, err)
	assert.NotEmpty(t, result.HTTPRouteYAML)
	assert.NotEmpty(t, result.SecurityPolicyYAML)
	assert.Contains(t, result.SecurityPolicyYAML, "example.com")
}

// =========================================================================
// routeplan.BackendTrafficPolicyInput.HasContent
// =========================================================================

func TestRouteService_BTPInput_HasContent(t *testing.T) {
	tests := []struct {
		name     string
		input    routeplan.BackendTrafficPolicyInput
		expected bool
	}{
		{"empty", routeplan.BackendTrafficPolicyInput{}, false},
		{"retry", routeplan.BackendTrafficPolicyInput{Retry: &models.RetryConfig{NumRetries: routeInt32Ptr(3)}}, true},
		{"loadbalancer", routeplan.BackendTrafficPolicyInput{LoadBalancer: &models.LoadBalancerConfig{Type: "RoundRobin"}}, true},
		{"circuit breaker", routeplan.BackendTrafficPolicyInput{CircuitBreaker: &models.CircuitBreakerConfig{}}, true},
		{"health check", routeplan.BackendTrafficPolicyInput{HealthCheck: &models.HealthCheckConfig{}}, true},
		{"fault injection", routeplan.BackendTrafficPolicyInput{FaultInjection: &models.FaultInjectionConfig{}}, true},
		{"rate limit", routeplan.BackendTrafficPolicyInput{RateLimit: &models.RateLimitConfig{}}, true},
		{"timeout", routeplan.BackendTrafficPolicyInput{Timeout: &models.BTPTimeoutConfig{}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.input.HasContent())
		})
	}
}

// =========================================================================
// routeplan.EnvoyExtensionPolicyInput.HasContent
// =========================================================================

func TestRouteService_EEPInput_HasContent(t *testing.T) {
	tests := []struct {
		name     string
		input    routeplan.EnvoyExtensionPolicyInput
		expected bool
	}{
		{"empty", routeplan.EnvoyExtensionPolicyInput{}, false},
		{"lua", routeplan.EnvoyExtensionPolicyInput{Lua: &models.LuaExtensionConfig{}}, true},
		{"wasm", routeplan.EnvoyExtensionPolicyInput{Wasm: &models.WasmExtensionConfig{}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.input.HasContent())
		})
	}
}

// =========================================================================
// Create - failover requires primary backend
// =========================================================================

func TestRouteService_Create_FailoverNoPrimary(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "failover-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:   "failover-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "fallback-svc", Namespace: "default", Port: 80, Fallback: true},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "failover requires at least one primary backend")
}

// =========================================================================
// Create - failover: fallback same as primary
// =========================================================================

func TestRouteService_Create_FailoverFallbackSameAsPrimary(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "failover-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:   "failover-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80, Fallback: true},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot be the same as a primary backend")
}

// =========================================================================
// Create - TLS on Kubernetes backend is accepted when valid
// =========================================================================

func TestRouteService_Create_K8sBackendWithValidTLS(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()
	createdBy := uuid.New()

	routeRepo.On("ExistsByName", domainID, "tls-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	routeRepo.On("Create", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	input := &services.CreateRouteInput{
		Name:   "tls-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{
					Type:      models.BackendTypeKubernetes,
					Service:   "svc",
					Namespace: "default",
					Port:      80,
					TLS: &models.BackendTLSConfig{
						Mode: models.BackendTLSModeSimple,
						CACertificateRefs: []models.CertificateRef{
							{Kind: "Secret", Name: "ca-cert", Namespace: "default"},
						},
					},
				},
			},
		},
	}

	result, err := svc.Create(domainID, input, createdBy)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "tls-route", result.Name)
}

// =========================================================================
// gRPC route: mismatched service and method types
// =========================================================================

func TestRouteService_Create_GRPCRoute_MismatchedServiceMethodType(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "grpc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:     "grpc-route",
		TeamID:   teamID,
		Protocol: models.RouteProtocolGRPC,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{
					GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "hello.Greeter"},
					GRPCMethod:  &models.GRPCMethodMatch{Type: "RegularExpression", Value: "Say.*"},
				},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "grpcService type (Exact) and grpcMethod type (RegularExpression) must be the same")
}

// =========================================================================
// gRPC route: HTTP method matching not supported
// =========================================================================

func TestRouteService_Create_GRPCRoute_RejectsHTTPMethodMatching(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "grpc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:     "grpc-route",
		TeamID:   teamID,
		Protocol: models.RouteProtocolGRPC,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Method: "POST"},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "HTTP method matching is not supported for gRPC routes")
}

// =========================================================================
// gRPC route: query param matching not supported
// =========================================================================

func TestRouteService_Create_GRPCRoute_RejectsQueryParamMatching(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "grpc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	input := &services.CreateRouteInput{
		Name:     "grpc-route",
		TeamID:   teamID,
		Protocol: models.RouteProtocolGRPC,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{QueryParams: []models.QueryParamMatch{{Name: "q", Type: "Exact", Value: "test"}}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "query parameter matching is not supported for gRPC routes")
}

// =========================================================================
// Namespace validation with ProjectNamespaceRepository
// =========================================================================

func TestRouteService_Create_NamespaceNotManaged(t *testing.T) {
	nsRepo := new(mocks.MockProjectNamespaceRepository)
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.ProjectNamespaceRepo = nsRepo
	})

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "ns-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	nsRepo.On("ExistsByProjectAndNamespace", projectID, "unmanaged-ns").Return(false, nil)

	input := &services.CreateRouteInput{
		Name:   "ns-route",
		TeamID: teamID,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "unmanaged-ns", Port: 80},
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not managed by this project")
}

// =========================================================================
// OIDC missing redirect URL
// =========================================================================

func TestRouteService_Create_GeneralMode_OIDC_MissingRedirectURL(t *testing.T) {
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteService()

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()

	routeRepo.On("ExistsByName", domainID, "oidc-route").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)

	input := &services.CreateRouteInput{
		Name:   "oidc-route",
		TeamID: teamID,
		Config: makeBasicHTTPRouteConfig(),
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			OIDC: &routeplan.OIDCInput{
				Issuer:           "https://issuer.com",
				ClientID:         "client-id",
				ClientSecretName: "secret",
				RedirectURL:      "",
				LogoutPath:       "/logout",
			},
		},
	}

	result, err := svc.Create(domainID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "OIDC redirectURL is required")
}

// =========================================================================
// helper
// =========================================================================

func routeInt32Ptr(n int32) *int32 {
	return &n
}

func routeStringPtr(s string) *string {
	return &s
}

// =========================================================================
// Deploy - error paths
// =========================================================================

func TestRouteService_Deploy_RouteNotFound_Orchestration(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("record not found"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Error(t, err)
	routeRepo.AssertExpectations(t)
}

func TestRouteService_Deploy_NotApproved_Active(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Name:   "user-api",
		Status: models.RouteStatusActive,
	}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route is not approved for deployment")
	routeRepo.AssertExpectations(t)
}

func TestRouteService_Deploy_NotApproved_PendingCreate(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Name:   "user-api",
		Status: models.RouteStatusPendingCreate,
	}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route is not approved for deployment")
	routeRepo.AssertExpectations(t)
}

func TestRouteService_Deploy_NotApproved_Rejected(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Name:   "user-api",
		Status: models.RouteStatusRejected,
	}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route is not approved for deployment")
	routeRepo.AssertExpectations(t)
}

func TestRouteService_Deploy_NoApprovedRequest_Orchestration(t *testing.T) {
	svc, routeRepo, approvalRepo, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Name:   "user-api",
		Status: models.RouteStatusApproved,
	}
	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(nil, errors.New("not found"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "no approved request found for this route")
	routeRepo.AssertExpectations(t)
	approvalRepo.AssertExpectations(t)
}

func TestRouteService_Deploy_DomainNotFound(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _ := newTestRouteService()

	routeID := uuid.New()
	domainID := uuid.New()
	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "user-api",
		Status:   models.RouteStatusApproved,
	}
	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Error(t, err)
	routeRepo.AssertExpectations(t)
	approvalRepo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
}

func TestRouteService_Deploy_NotApproved_PendingUpdate(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Name:   "user-api",
		Status: models.RouteStatusPendingUpdate,
	}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route is not approved for deployment")
	routeRepo.AssertExpectations(t)
}

func TestRouteService_Deploy_NotApproved_PendingDelete(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Name:   "user-api",
		Status: models.RouteStatusPendingDelete,
	}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route is not approved for deployment")
	routeRepo.AssertExpectations(t)
}

// =========================================================================
// computeSecurityStatus - tested via GetByID (populates SecurityStatus)
// =========================================================================

func TestRouteService_ComputeSecurityStatus_GeneralMode_NoSecurityPolicy(t *testing.T) {
	// General mode with no stored security policy returns "none". Before
	// Phase 2E Task 9 this test reached that answer through the
	// `s.securityPolicyRepo == nil` guard; it now reaches it through the
	// repository answering "no policy", which is the production path.
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeGeneral,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, models.SecurityStatusNone, result.SecurityStatus)
	routeRepo.AssertExpectations(t)
}

func TestRouteService_ComputeSecurityStatus_GeneralMode_NoPolicyFound(t *testing.T) {
	svc, routeRepo, _, _, _, _, secRepo, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeGeneral,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, models.SecurityStatusNone, result.SecurityStatus)
	routeRepo.AssertExpectations(t)
}

func TestRouteService_ComputeSecurityStatus_GeneralMode_WithAuthorization_Protected(t *testing.T) {
	svc, routeRepo, _, _, _, _, secRepo, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeGeneral,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			Authorization: &models.AuthorizationConfig{
				DefaultAction: "Deny",
			},
		},
	}, nil)
	// countClientAttachments
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, models.SecurityStatusProtected, result.SecurityStatus)
	routeRepo.AssertExpectations(t)
}

func TestRouteService_ComputeSecurityStatus_GeneralMode_WithAPIKeyAuth_Protected(t *testing.T) {
	svc, routeRepo, _, _, _, _, secRepo, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeGeneral,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			APIKeyAuth: &models.APIKeyAuthConfig{
				CredentialRefs: []models.SecretRef{{Name: "my-secret", Namespace: "default"}},
			},
		},
	}, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, models.SecurityStatusProtected, result.SecurityStatus)
}

func TestRouteService_ComputeSecurityStatus_GeneralMode_WithJWT_Protected(t *testing.T) {
	svc, routeRepo, _, _, _, _, secRepo, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeGeneral,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			JWT: &models.JWTConfig{
				Providers: []models.JWTProvider{{
					Name:      "my-provider",
					Issuer:    "https://issuer.example.com",
					Audiences: []string{"api"},
				}},
			},
		},
	}, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, models.SecurityStatusProtected, result.SecurityStatus)
}

func TestRouteService_ComputeSecurityStatus_GeneralMode_WithOIDC_Protected(t *testing.T) {
	svc, routeRepo, _, _, _, _, secRepo, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeGeneral,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			OIDC: &models.OIDCConfig{
				ClientID: "client-id",
			},
		},
	}, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, models.SecurityStatusProtected, result.SecurityStatus)
}

func TestRouteService_ComputeSecurityStatus_GeneralMode_OnlyCORS_None(t *testing.T) {
	svc, routeRepo, _, _, _, _, secRepo, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeGeneral,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"*"},
			},
		},
	}, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, models.SecurityStatusNone, result.SecurityStatus)
}

func TestRouteService_ComputeSecurityStatus_ClientMode_NoClients_None(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	// ClientCount = 0, so SecurityStatus = none
	assert.Equal(t, 0, result.ClientCount)
	assert.Equal(t, models.SecurityStatusNone, result.SecurityStatus)
}

func TestRouteService_ComputeSecurityStatus_ClientMode_WithClients_DenyPolicy_Protected(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
		Config: models.RouteConfig{
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID, RouteID: routeID, EnableIPAllowlist: true, Status: models.AttachmentStatusActive},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, 1, result.ClientCount)
	assert.Equal(t, models.SecurityStatusProtected, result.SecurityStatus)
}

func TestRouteService_ComputeSecurityStatus_ClientMode_WithClients_AllowAll_Warning(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
		Config: models.RouteConfig{
			DefaultTrafficPolicy: models.DefaultTrafficPolicyAllowAll,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID, RouteID: routeID, EnableIPAllowlist: true, Status: models.AttachmentStatusActive},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, 1, result.ClientCount)
	assert.Equal(t, models.SecurityStatusWarning, result.SecurityStatus)
}

func TestRouteService_ComputeSecurityStatus_ClientMode_WithClients_RequireIPAllowlist_Protected(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
		Config: models.RouteConfig{
			DefaultTrafficPolicy: models.DefaultTrafficPolicyRequireIPAllowlist,
			DefaultAllowedCIDRs:  []string{"10.0.0.0/8"},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID, RouteID: routeID, EnableIPAllowlist: true, Status: models.AttachmentStatusActive},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, 1, result.ClientCount)
	assert.Equal(t, models.SecurityStatusProtected, result.SecurityStatus)
}

func TestRouteService_ComputeSecurityStatus_ClientMode_EmptyDefaultPolicy_Warning(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
		Config: models.RouteConfig{
			DefaultTrafficPolicy: "", // empty = allow_all
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, 1, result.ClientCount)
	assert.Equal(t, models.SecurityStatusWarning, result.SecurityStatus)
}

func TestRouteService_ComputeSecurityStatus_ClientMode_RequireIPAllowlist_NoCIDRs_Protected(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
		Config: models.RouteConfig{
			DefaultTrafficPolicy: models.DefaultTrafficPolicyRequireIPAllowlist,
			// No DefaultAllowedCIDRs - still protected (denies all)
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "svc", Namespace: "default", Port: 80},
			},
		},
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusActive},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, models.SecurityStatusProtected, result.SecurityStatus)
}

// =========================================================================
// countClientAttachments - tested via GetByID (populates ClientCount)
// =========================================================================

func TestRouteService_CountClientAttachments_NilRepo(t *testing.T) {
	svc, routeRepo, _, _, _, _ := newTestRouteService()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	// Without client attachment repo, count should be 0
	assert.Equal(t, 0, result.ClientCount)
}

func TestRouteService_CountClientAttachments_WithActiveAndApproved(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID1 := uuid.New()
	clientID2 := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID1, RouteID: routeID, Status: models.AttachmentStatusActive},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID2, RouteID: routeID, Status: models.AttachmentStatusApproved},
	}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, 2, result.ClientCount)
}

func TestRouteService_CountClientAttachments_Empty(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, 0, result.ClientCount)
}

func TestRouteService_CountClientAttachments_MultipleActive(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID1 := uuid.New()
	clientID2 := uuid.New()
	route := &models.Route{
		ID:           routeID,
		Name:         "test-route",
		SecurityMode: models.SecurityModeClient,
	}
	routeRepo.On("GetByIDWithApproval", routeID).Return(route, nil)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID1, RouteID: routeID, Status: models.AttachmentStatusActive},
		{ID: uuid.New(), ClientID: clientID2, RouteID: routeID, Status: models.AttachmentStatusActive},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	result, err := svc.GetByID(routeID)

	require.NoError(t, err)
	assert.Equal(t, 2, result.ClientCount)
}

// =========================================================================
// GetEffectiveIPAllowlist
// =========================================================================

func TestRouteService_GetEffectiveIPAllowlist_NilRepos(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	entries, err := svc.GetEffectiveIPAllowlist(uuid.New())

	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestRouteService_GetEffectiveIPAllowlist_NoAttachments(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	entries, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestRouteService_GetEffectiveIPAllowlist_WithIPAttachments(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, cipRepo := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()

	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          clientID,
			RouteID:           routeID,
			EnableIPAllowlist: true,
			Status:            models.AttachmentStatusActive,
			Client:            &models.Client{Name: "test-client"},
		},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	cipRepo.On("ListByClientID", clientID).Return([]models.ClientIPAddress{
		{ID: uuid.New(), ClientID: clientID, CIDR: "10.0.0.0/24", Description: "office"},
		{ID: uuid.New(), ClientID: clientID, CIDR: "192.168.1.0/24", Description: "vpn"},
	}, nil)

	entries, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, "10.0.0.0/24", entries[0].CIDR)
	assert.Equal(t, "test-client", entries[0].ClientName)
	assert.Equal(t, "office", entries[0].Description)
	assert.Equal(t, "192.168.1.0/24", entries[1].CIDR)
}

func TestRouteService_GetEffectiveIPAllowlist_SkipsNonIPAttachments(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()

	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          uuid.New(),
			RouteID:           routeID,
			EnableIPAllowlist: false, // IP allowlist not enabled
			EnableAPIKey:      true,
			Status:            models.AttachmentStatusActive,
		},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	entries, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestRouteService_GetEffectiveIPAllowlist_ListActiveError(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, errors.New("db error"))

	entries, err := svc.GetEffectiveIPAllowlist(routeID)

	assert.Nil(t, entries)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list active attachments")
}

func TestRouteService_GetEffectiveIPAllowlist_ApprovedAttachments(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, cipRepo := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()

	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          clientID,
			RouteID:           routeID,
			EnableIPAllowlist: true,
			Status:            models.AttachmentStatusApproved,
			Client:            &models.Client{Name: "approved-client"},
		},
	}, nil)
	cipRepo.On("ListByClientID", clientID).Return([]models.ClientIPAddress{
		{ID: uuid.New(), ClientID: clientID, CIDR: "172.16.0.0/16"},
	}, nil)

	entries, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "172.16.0.0/16", entries[0].CIDR)
	assert.Equal(t, "approved-client", entries[0].ClientName)
}

func TestRouteService_GetEffectiveIPAllowlist_MultipleClients(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, cipRepo := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID1 := uuid.New()
	clientID2 := uuid.New()

	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          clientID1,
			RouteID:           routeID,
			EnableIPAllowlist: true,
			Status:            models.AttachmentStatusActive,
			Client:            &models.Client{Name: "client-1"},
		},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          clientID2,
			RouteID:           routeID,
			EnableIPAllowlist: true,
			Status:            models.AttachmentStatusApproved,
			Client:            &models.Client{Name: "client-2"},
		},
	}, nil)
	cipRepo.On("ListByClientID", clientID1).Return([]models.ClientIPAddress{
		{ID: uuid.New(), ClientID: clientID1, CIDR: "10.0.0.0/24"},
	}, nil)
	cipRepo.On("ListByClientID", clientID2).Return([]models.ClientIPAddress{
		{ID: uuid.New(), ClientID: clientID2, CIDR: "172.16.0.0/16"},
	}, nil)

	entries, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, "10.0.0.0/24", entries[0].CIDR)
	assert.Equal(t, "client-1", entries[0].ClientName)
	assert.Equal(t, "172.16.0.0/16", entries[1].CIDR)
	assert.Equal(t, "client-2", entries[1].ClientName)
}

func TestRouteService_GetEffectiveIPAllowlist_ClientIPListError_Skips(t *testing.T) {
	svc, _, _, _, _, _, _, _, _, _, caRepo, cipRepo := newTestRouteServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()

	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          clientID,
			RouteID:           routeID,
			EnableIPAllowlist: true,
			Status:            models.AttachmentStatusActive,
			Client:            &models.Client{Name: "test-client"},
		},
	}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	cipRepo.On("ListByClientID", clientID).Return([]models.ClientIPAddress{}, errors.New("ip lookup error"))

	entries, err := svc.GetEffectiveIPAllowlist(routeID)

	require.NoError(t, err)
	// Client IPs errored out, so no entries
	assert.Empty(t, entries)
}

// =========================================================================
// GetSecurityPolicy / GetBackendTrafficPolicy / GetEnvoyExtensionPolicy with repos
// =========================================================================

func TestRouteService_GetSecurityPolicy_WithRepo_Success(t *testing.T) {
	svc, _, _, _, _, _, secRepo, _, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	expected := &models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{AllowOrigins: []string{"*"}},
		},
	}
	secRepo.On("GetByRouteID", routeID).Return(expected, nil)

	result, err := svc.GetSecurityPolicy(routeID)

	require.NoError(t, err)
	assert.Equal(t, expected.ID, result.ID)
	assert.NotNil(t, result.Config.CORS)
}

func TestRouteService_GetSecurityPolicy_WithRepo_NotFound(t *testing.T) {
	svc, _, _, _, _, _, secRepo, _, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.GetSecurityPolicy(routeID)

	assert.Nil(t, result)
	assert.NoError(t, err) // Not found is not an error
}

func TestRouteService_GetBackendTrafficPolicy_WithRepo_Success(t *testing.T) {
	svc, _, _, _, _, _, _, btpRepo, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	expected := &models.BackendTrafficPolicy{
		ID:      uuid.New(),
		RouteID: &routeID,
	}
	btpRepo.On("GetByRouteID", routeID).Return(expected, nil)

	result, err := svc.GetBackendTrafficPolicy(routeID)

	require.NoError(t, err)
	assert.Equal(t, expected.ID, result.ID)
}

func TestRouteService_GetBackendTrafficPolicy_WithRepo_NotFound(t *testing.T) {
	svc, _, _, _, _, _, _, btpRepo, _, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.GetBackendTrafficPolicy(routeID)

	assert.Nil(t, result)
	assert.NoError(t, err)
}

func TestRouteService_GetEnvoyExtensionPolicy_NilRepo_Orchestration(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	result, err := svc.GetEnvoyExtensionPolicy(uuid.New())

	assert.Nil(t, result)
	assert.NoError(t, err)
}

func TestRouteService_GetEnvoyExtensionPolicy_WithRepo_Success(t *testing.T) {
	svc, _, _, _, _, _, _, _, eepRepo, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	expected := &models.EnvoyExtensionPolicy{
		ID:      uuid.New(),
		RouteID: &routeID,
	}
	eepRepo.On("GetByRouteID", routeID).Return(expected, nil)

	result, err := svc.GetEnvoyExtensionPolicy(routeID)

	require.NoError(t, err)
	assert.Equal(t, expected.ID, result.ID)
}

func TestRouteService_GetEnvoyExtensionPolicy_WithRepo_NotFound(t *testing.T) {
	svc, _, _, _, _, _, _, _, eepRepo, _, _, _ := newTestRouteServiceFull()

	routeID := uuid.New()
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	result, err := svc.GetEnvoyExtensionPolicy(routeID)

	assert.Nil(t, result)
	assert.NoError(t, err)
}

// =========================================================================
// Create - validateBackendNamespaces paths
// =========================================================================

func TestRouteService_Create_BackendNamespaceNotManaged(t *testing.T) {
	nsRepo := new(mocks.MockProjectNamespaceRepository)
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.ProjectNamespaceRepo = nsRepo
	})

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()
	createdBy := uuid.New()

	domain := &models.Domain{ID: domainID, ProjectID: projectID}
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ExistsByName", domainID, "user-api").Return(false, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	// The backend namespace is not managed
	nsRepo.On("ExistsByProjectAndNamespace", projectID, "custom-ns").Return(false, nil)

	config := models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "my-svc", Namespace: "custom-ns", Port: 8080},
		},
	}

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: teamID,
		Config: config,
	}

	result, err := svc.Create(domainID, input, createdBy)

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "namespace 'custom-ns' is not managed by this project")
}

func TestRouteService_Create_BackendNamespaceManaged(t *testing.T) {
	nsRepo := new(mocks.MockProjectNamespaceRepository)
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo := newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.ProjectNamespaceRepo = nsRepo
	})

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()
	createdBy := uuid.New()

	domain := &models.Domain{ID: domainID, ProjectID: projectID}
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ExistsByName", domainID, "user-api").Return(false, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	// The backend namespace IS managed
	nsRepo.On("ExistsByProjectAndNamespace", projectID, "custom-ns").Return(true, nil)

	// No matcher conflicts
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).Return([]models.Route{}, int64(0), nil)
	routeRepo.On("Create", mock.AnythingOfType("*models.Route")).Return(nil)

	// Approval stages - default
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)
	// GetPendingByEntityID for enrichment
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, mock.AnythingOfType("uuid.UUID")).Return(&models.Approval{
		ID:     uuid.New(),
		Status: models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{{StageOrder: 1, RequiredPermission: "route.approve", Status: models.ApprovalStatusPending}},
	}, nil)

	config := models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "my-svc", Namespace: "custom-ns", Port: 8080},
		},
	}

	input := &services.CreateRouteInput{
		Name:   "user-api",
		TeamID: teamID,
		Config: config,
	}

	result, err := svc.Create(domainID, input, createdBy)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "user-api", result.Name)
}

func TestRouteService_Create_MirrorNamespaceNotManaged(t *testing.T) {
	nsRepo := new(mocks.MockProjectNamespaceRepository)
	svc, routeRepo, _, _, domainRepo, teamRepo := newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.ProjectNamespaceRepo = nsRepo
	})

	domainID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()
	createdBy := uuid.New()

	domain := &models.Domain{ID: domainID, ProjectID: projectID}
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("ExistsByName", domainID, "mirror-route").Return(false, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)

	// Primary backend NS is managed, but mirror NS is not
	nsRepo.On("ExistsByProjectAndNamespace", projectID, "primary-ns").Return(true, nil)
	nsRepo.On("ExistsByProjectAndNamespace", projectID, "mirror-ns").Return(false, nil)

	config := models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "primary-svc", Namespace: "primary-ns", Port: 8080},
		},
		Mirrors: []models.MirrorBackend{
			{Type: models.BackendTypeKubernetes, Service: "mirror-svc", Namespace: "mirror-ns", Port: 8080},
		},
	}

	input := &services.CreateRouteInput{
		Name:   "mirror-route",
		TeamID: teamID,
		Config: config,
	}

	result, err := svc.Create(domainID, input, createdBy)

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "mirror namespace 'mirror-ns' is not managed by this project")
}

// =========================================================================
// Create - buildRouteApprovalStages with custom policy
// =========================================================================

// =========================================================================
// Create - validation edge cases
// =========================================================================

func TestRouteService_Create_InvalidRouteName_Spaces(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	input := &services.CreateRouteInput{
		Name:   "my route",
		TeamID: uuid.New(),
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Create(uuid.New(), input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route name cannot contain spaces")
}

func TestRouteService_Create_InvalidRouteName_Format(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()

	input := &services.CreateRouteInput{
		Name:   "MyRoute!",
		TeamID: uuid.New(),
		Config: makeBasicHTTPRouteConfig(),
	}

	result, err := svc.Create(uuid.New(), input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "route name must be lowercase")
}

// =========================================================================
// Helper: newTestRouteServiceWithK8s
// =========================================================================

func newTestRouteServiceWithK8s() (
	*services.RouteService,
	*mocks.MockRouteRepository,
	*mocks.MockUnifiedApprovalRepository,
	*mocks.MockApprovalPolicyRepository,
	*mocks.MockDomainRepository,
	*mocks.MockTeamRepository,
	*mocks.MockSecurityPolicyRepository,
	*mocks.MockBackendTrafficPolicyRepository,
	*mocks.MockEnvoyExtensionPolicyRepository,
	*mocks.MockWafPolicyRepository,
	*mocks.MockClientAttachmentRepository,
	*mocks.MockClientIPRepository,
	*mocks.MockKubernetesService,
) {
	k8sMock := new(mocks.MockKubernetesService)
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo, secRepo, btpRepo, eepRepo, wafRepo, caRepo, cipRepo := newTestRouteServiceFullWith(
		func(d *services.RouteServiceDeps) {
			d.K8sRoutes = k8sMock
			d.K8sPolicies = k8sMock
			d.K8sBackends = k8sMock
			d.K8sBackendReaper = k8sMock
			d.K8sSecrets = k8sMock
			d.K8sAPIKeys = k8sMock
			d.K8sRefGrants = k8sMock
		})
	return svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo, secRepo, btpRepo, eepRepo, wafRepo, caRepo, cipRepo, k8sMock
}

// makeTestRoute creates a basic route for deploy/delete tests.
func makeTestRoute(routeID, domainID uuid.UUID) *models.Route {
	return &models.Route{
		ID:           routeID,
		DomainID:     domainID,
		Name:         "test-route",
		K8sRouteName: "test-route-abcd1234",
		Protocol:     models.RouteProtocolHTTP,
		SecurityMode: models.SecurityModeGeneral,
		Status:       models.RouteStatusApproved,
		Config:       makeBasicHTTPRouteConfig(),
	}
}

func makeTestDomain(domainID, projectID uuid.UUID) *models.Domain {
	return &models.Domain{
		ID:             domainID,
		ProjectID:      projectID,
		Name:           "test-domain",
		Hostname:       "example.com",
		Namespace:      "fastgateway-system",
		K8sGatewayName: "test-gateway",
	}
}

// setupDeployMocksForCreate sets up common mock expectations for a Deploy(create) flow.
func setupDeployMocksForCreate(
	routeRepo *mocks.MockRouteRepository,
	approvalRepo *mocks.MockUnifiedApprovalRepository,
	domainRepo *mocks.MockDomainRepository,
	secRepo *mocks.MockSecurityPolicyRepository,
	btpRepo *mocks.MockBackendTrafficPolicyRepository,
	eepRepo *mocks.MockEnvoyExtensionPolicyRepository,
	wafRepo *mocks.MockWafPolicyRepository,
	caRepo *mocks.MockClientAttachmentRepository,
	k8sMock *mocks.MockKubernetesService,
	route *models.Route,
	domain *models.Domain,
) {
	routeRepo.On("GetByID", route.ID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, route.ID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", route.DomainID).Return(domain, nil)

	// Policy repos return nothing (no policies)
	secRepo.On("GetByRouteID", route.ID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", route.ID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", route.ID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", route.ID).Return(nil, gorm.ErrRecordNotFound)

	// Client attachment repos return empty
	caRepo.On("ListActiveByRouteID", route.ID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", route.ID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", route.ID, mock.Anything, mock.Anything).Return(nil).Maybe()

	// K8s calls for SecurityPolicy delete (no SP to deploy, will delete)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, domain.ProjectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	// K8s calls for EEP delete (no EEP to deploy)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, domain.ProjectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, domain.ProjectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	// K8s calls for ext-auth backend cleanup (legacy cleanup in deployGeneralSecurityPolicy)
	k8sMock.On("DeleteBackend", mock.Anything, domain.ProjectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()

	// Update route status at end
	routeRepo.On("Update", mock.Anything).Return(nil)
}

// =========================================================================
// Deploy
// =========================================================================

func TestRouteService_Deploy_CreateHTTPRoute_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	setupDeployMocksForCreate(routeRepo, approvalRepo, domainRepo, secRepo, btpRepo, eepRepo, wafRepo, caRepo, k8sMock, route, domain)

	// K8s: create HTTPRoute
	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig"))
}

func TestRouteService_Deploy_CreateGRPCRoute_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Protocol = models.RouteProtocolGRPC
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "my.Service"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "grpc-svc", Namespace: "default", Port: 50051},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	setupDeployMocksForCreate(routeRepo, approvalRepo, domainRepo, secRepo, btpRepo, eepRepo, wafRepo, caRepo, k8sMock, route, domain)

	k8sMock.On("CreateGRPCRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GRPCRouteConfig")).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "CreateGRPCRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GRPCRouteConfig"))
	k8sMock.AssertNotCalled(t, "CreateHTTPRoute", mock.Anything, mock.Anything, mock.Anything)
}

func TestRouteService_Deploy_UpdateHTTPRoute_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Status = models.RouteStatusApproved
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig"))
}

func TestRouteService_Deploy_RouteNotFound_K8s(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, _, _, _ := newTestRouteServiceWithK8s()

	routeID := uuid.New()

	routeRepo.On("GetByID", routeID).Return(nil, errors.New("route not found"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route not found")
}

func TestRouteService_Deploy_RouteNotApproved(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, _, _, _ := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	route := makeTestRoute(routeID, uuid.New())
	route.Status = models.RouteStatusActive // already active, not approved

	routeRepo.On("GetByID", routeID).Return(route, nil)

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route is not approved for deployment")
}

func TestRouteService_Deploy_DomainNotFound_K8s(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, _, _, _, _, _, _, _ := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()

	route := makeTestRoute(routeID, domainID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("domain not found"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "domain not found")
}

func TestRouteService_Deploy_WithSecurityPolicy_General(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeGeneral
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	// Security policy with CORS
	sp := &models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://example.com"},
				AllowMethods: []string{"GET", "POST"},
			},
		},
	}
	secRepo.On("GetByRouteID", routeID).Return(sp, nil)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig"))
}

func TestRouteService_Deploy_WithBackendTrafficPolicy(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	// BTP with timeout
	btp := &models.BackendTrafficPolicy{
		ID:      uuid.New(),
		RouteID: &routeID,
		Config: models.BackendTrafficPolicyConfig{
			Timeout: &models.BTPTimeoutConfig{
				TCP: &models.BTPTCPTimeoutConfig{
					ConnectTimeout: "10s",
				},
			},
		},
	}
	btpRepo.On("GetByRouteID", routeID).Return(btp, nil)

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("UpdateBackendTrafficPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig")).Return(nil)
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateBackendTrafficPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig"))
}

func TestRouteService_Deploy_WithDirectResponse(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/health"}},
		},
		DirectResponse: &models.DirectResponseConfig{
			StatusCode:  200,
			ContentType: "application/json",
			Body:        &models.DirectResponseBody{Type: "Inline", Inline: `{"status":"ok"}`},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("ApplyDirectResponseConfigMap", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.DirectResponseConfigMapConfig")).Return(nil)
	k8sMock.On("ApplyHTTPRouteFilter", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteFilterConfig")).Return(nil)
	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "ApplyDirectResponseConfigMap", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.DirectResponseConfigMapConfig"))
	k8sMock.AssertCalled(t, "ApplyHTTPRouteFilter", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteFilterConfig"))
}

func TestRouteService_Deploy_WithExternalBackend(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/external"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeExternal, AddressType: "fqdn", Address: "api.example.com", Port: 443},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig")).Return(nil)
	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig"))
}

func TestRouteService_Deploy_PendingDeploy_NoApproval(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Status = models.RouteStatusPendingDeploy
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	// No approval found — for pending_deploy this is OK, treated as update
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(nil, errors.New("not found"))
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
}

func TestRouteService_Deploy_CreateHTTPRoute_K8sError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	// K8s create fails
	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(errors.New("k8s connection refused"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to create HTTPRoute in Kubernetes")
}

// =========================================================================
// Deploy - Delete action
// =========================================================================

func TestRouteService_Deploy_DeleteHTTPRoute_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Status = models.RouteStatusApproved
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionDelete}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	// Security policy exists in DB
	sp := &models.SecurityPolicy{ID: uuid.New(), RouteID: routeID, Config: models.SecurityPolicyConfig{
		CORS: &models.CORSConfig{AllowOrigins: []string{"*"}},
	}}
	secRepo.On("GetByRouteID", routeID).Return(sp, nil)
	secRepo.On("Delete", sp.ID).Return(nil)

	// BTP exists
	btp := &models.BackendTrafficPolicy{ID: uuid.New(), RouteID: &routeID, Config: models.BackendTrafficPolicyConfig{
		Timeout: &models.BTPTimeoutConfig{TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"}},
	}}
	btpRepo.On("GetByRouteID", routeID).Return(btp, nil)
	btpRepo.On("Delete", btp.ID).Return(nil)

	// EEP exists
	eep := &models.EnvoyExtensionPolicy{ID: uuid.New(), RouteID: &routeID, Config: models.EnvoyExtensionPolicyConfig{
		Lua: &models.LuaExtensionConfig{Type: "Inline", Inline: "print('hello')"},
	}}
	eepRepo.On("GetByRouteID", routeID).Return(eep, nil)
	eepRepo.On("Delete", eep.ID).Return(nil)

	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	// Client attachment: no clients (triggers label-based fallback cleanup)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	// K8s delete calls
	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil)
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-btp").Return(nil)
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.AnythingOfType("string")).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil)
	k8sMock.On("DeleteHTTPRoute", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName).Return(nil)
	k8sMock.On("DeleteBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String()).Return(nil)

	// Approval cleanup
	approvalRepo.On("DeleteByEntityID", models.ApprovalEntityRoute, routeID).Return(nil)

	routeRepo.On("Delete", routeID).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.NotNil(t, result)
	k8sMock.AssertCalled(t, "DeleteHTTPRoute", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName)
	k8sMock.AssertCalled(t, "DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security")
	k8sMock.AssertCalled(t, "DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-btp")
	k8sMock.AssertCalled(t, "DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep")
	k8sMock.AssertCalled(t, "DeleteBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String())
}

func TestRouteService_Deploy_DeleteHTTPRoute_WithAttachmentApprovals(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Status = models.RouteStatusApproved
	domain := makeTestDomain(domainID, projectID)

	att1ID := uuid.New()
	att2ID := uuid.New()

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionDelete}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	// No active/approved attachments (triggers label-based fallback)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	// But ListByRouteID returns attachments (for approval cleanup before cascade delete)
	caRepo.On("ListByRouteID", routeID).Return([]models.ClientRouteAttachment{
		{ID: att1ID, RouteID: routeID},
		{ID: att2ID, RouteID: routeID},
	}, nil)

	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil)
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteHTTPRoute", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName).Return(nil)
	k8sMock.On("DeleteBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String()).Return(nil)

	// Approval cleanup: route + both attachments
	approvalRepo.On("DeleteByEntityID", models.ApprovalEntityRoute, routeID).Return(nil)
	approvalRepo.On("DeleteByEntityID", models.ApprovalEntityClientAttachment, att1ID).Return(nil)
	approvalRepo.On("DeleteByEntityID", models.ApprovalEntityClientAttachment, att2ID).Return(nil)

	routeRepo.On("Delete", routeID).Return(nil)

	result, err := svc.Deploy(routeID, uuid.New())

	require.NoError(t, err)
	assert.NotNil(t, result)
	approvalRepo.AssertCalled(t, "DeleteByEntityID", models.ApprovalEntityRoute, routeID)
	approvalRepo.AssertCalled(t, "DeleteByEntityID", models.ApprovalEntityClientAttachment, att1ID)
	approvalRepo.AssertCalled(t, "DeleteByEntityID", models.ApprovalEntityClientAttachment, att2ID)
}

func TestRouteService_Deploy_DeleteGRPCRoute_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Protocol = models.RouteProtocolGRPC
	route.Status = models.RouteStatusApproved
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionDelete}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil)
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-btp").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteGRPCRoute", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName).Return(nil)
	k8sMock.On("DeleteBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String()).Return(nil)

	// Approval cleanup
	approvalRepo.On("DeleteByEntityID", models.ApprovalEntityRoute, routeID).Return(nil)

	routeRepo.On("Delete", routeID).Return(nil)

	result, err := svc.Deploy(routeID, uuid.New())

	require.NoError(t, err)
	assert.NotNil(t, result)
	k8sMock.AssertCalled(t, "DeleteGRPCRoute", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName)
	k8sMock.AssertNotCalled(t, "DeleteHTTPRoute", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRouteService_Deploy_Delete_WithDirectResponse(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/health"}},
		},
		DirectResponse: &models.DirectResponseConfig{
			StatusCode:  200,
			ContentType: "text/plain",
			Body:        &models.DirectResponseBody{Type: "Inline", Inline: "OK"},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionDelete}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil)
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteHTTPRoute", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName).Return(nil)
	k8sMock.On("DeleteHTTPRouteFilter", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-hrf").Return(nil)
	k8sMock.On("DeleteDirectResponseConfigMap", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-dr-cm").Return(nil)
	k8sMock.On("DeleteBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String()).Return(nil)

	// Approval cleanup
	approvalRepo.On("DeleteByEntityID", models.ApprovalEntityRoute, routeID).Return(nil)

	routeRepo.On("Delete", routeID).Return(nil)

	result, err := svc.Deploy(routeID, uuid.New())

	require.NoError(t, err)
	assert.NotNil(t, result)
	k8sMock.AssertCalled(t, "DeleteHTTPRouteFilter", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-hrf")
	k8sMock.AssertCalled(t, "DeleteDirectResponseConfigMap", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-dr-cm")
}

func TestRouteService_Deploy_Delete_HTTPRouteDeleteError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionDelete}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil)
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteHTTPRoute", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName).Return(errors.New("k8s error"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to delete HTTPRoute from Kubernetes")
}

// =========================================================================
// Delete (creates approval for deletion)
// =========================================================================

func TestRouteService_Delete_WithK8s_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, _, _, _ := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submittedBy := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "test-route",
		Status:   models.RouteStatusActive,
		Config:   makeBasicHTTPRouteConfig(),
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("Update", mock.Anything).Return(nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))

	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()

	approvalRepo.On("Create", mock.Anything).Return(nil)

	result, err := svc.Delete(routeID, submittedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDelete, result.Status)
	assert.NotNil(t, result.PendingApproval)
}

func TestRouteService_Delete_RouteNotFound_K8s(t *testing.T) {
	svc, routeRepo, _, _, _, _, _, _, _, _, _, _, _ := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("route not found"))

	result, err := svc.Delete(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route not found")
}

func TestRouteService_Delete_PendingApprovalExists_K8s(t *testing.T) {
	svc, routeRepo, approvalRepo, _, _, _, _, _, _, _, _, _, _ := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Name:   "test",
		Status: models.RouteStatusActive,
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{ID: uuid.New()}, nil)

	result, err := svc.Delete(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "there is already a pending approval for this route")
}

func TestRouteService_Delete_WithExistingPolicies(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, _, _, _ := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submittedBy := uuid.New()

	route := &models.Route{
		ID:       routeID,
		DomainID: domainID,
		Name:     "test-route",
		Status:   models.RouteStatusActive,
		Config:   makeBasicHTTPRouteConfig(),
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	routeRepo.On("Update", mock.Anything).Return(nil)

	// Policies exist
	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		Config: models.SecurityPolicyConfig{CORS: &models.CORSConfig{AllowOrigins: []string{"*"}}},
	}, nil)
	btpRepo.On("GetByRouteID", routeID).Return(&models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{Timeout: &models.BTPTimeoutConfig{TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"}}},
	}, nil)
	eepRepo.On("GetByRouteID", routeID).Return(&models.EnvoyExtensionPolicy{
		Config: models.EnvoyExtensionPolicyConfig{Lua: &models.LuaExtensionConfig{Type: "Inline", Inline: "print()"}},
	}, nil)
	wafRepo.On("GetByRouteID", routeID).Return(&models.WafPolicy{
		Config: models.WafPolicyConfig{Mode: "DetectionOnly"},
	}, nil)

	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.MatchedBy(func(a *models.Approval) bool {
		// Verify the config snapshot contains policy data
		return a.Action == models.ApprovalActionDelete && len(a.ConfigSnapshot) > 0
	})).Return(nil)

	result, err := svc.Delete(routeID, submittedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDelete, result.Status)

	// Verify config snapshot includes policies
	var snapshot struct {
		SecurityPolicy       json.RawMessage `json:"securityPolicy"`
		BackendTrafficPolicy json.RawMessage `json:"backendTrafficPolicy"`
		EnvoyExtensionPolicy json.RawMessage `json:"envoyExtensionPolicy"`
		WafPolicy            json.RawMessage `json:"wafPolicy"`
	}
	err = json.Unmarshal(result.PendingApproval.ConfigSnapshot, &snapshot)
	require.NoError(t, err)
	assert.NotEmpty(t, snapshot.SecurityPolicy)
	assert.NotEmpty(t, snapshot.BackendTrafficPolicy)
	assert.NotEmpty(t, snapshot.EnvoyExtensionPolicy)
	assert.NotEmpty(t, snapshot.WafPolicy)
}

func TestRouteService_Deploy_NoApprovedRequest_NotPendingDeploy(t *testing.T) {
	svc, routeRepo, approvalRepo, _, _, _, _, _, _, _, _, _, _ := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	route := makeTestRoute(routeID, uuid.New())
	route.Status = models.RouteStatusApproved

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(nil, errors.New("not found"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "no approved request found for this route")
}

func TestRouteService_Deploy_UpdateGRPCRoute_Success(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Protocol = models.RouteProtocolGRPC
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "my.Service"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "grpc-svc", Namespace: "default", Port: 50051},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("UpdateGRPCRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GRPCRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateGRPCRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GRPCRouteConfig"))
}

func TestRouteService_Deploy_DeployBackends_Error(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/ext"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeExternal, AddressType: "fqdn", Address: "api.example.com", Port: 443},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig")).Return(errors.New("backend CRD failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to create Backend CRDs in Kubernetes")
}

func TestRouteService_Deploy_SecurityPolicyDeploy_Error(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeGeneral
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{AllowOrigins: []string{"*"}},
		},
	}, nil)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(errors.New("SP apply failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to create SecurityPolicy in Kubernetes")
}

func TestRouteService_Deploy_BackendTrafficPolicy_Error(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	btpRepo.On("GetByRouteID", routeID).Return(&models.BackendTrafficPolicy{
		RouteID: &routeID,
		Config: models.BackendTrafficPolicyConfig{
			Timeout: &models.BTPTimeoutConfig{TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"}},
		},
	}, nil)

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("UpdateBackendTrafficPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig")).Return(errors.New("BTP apply failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to create BackendTrafficPolicy in Kubernetes")
}

func TestRouteService_Deploy_DirectResponse_Error(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/dr"}},
		},
		DirectResponse: &models.DirectResponseConfig{
			StatusCode:  200,
			ContentType: "text/plain",
			Body:        &models.DirectResponseBody{Type: "Inline", Inline: "hello"},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("ApplyDirectResponseConfigMap", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.DirectResponseConfigMapConfig")).Return(errors.New("configmap failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to create HTTPRouteFilter/ConfigMap in Kubernetes")
}

func TestRouteService_Deploy_EnvoyExtensionPolicy_Error(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	// EEP with Lua
	eepRepo.On("GetByRouteID", routeID).Return(&models.EnvoyExtensionPolicy{
		RouteID: &routeID,
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{Type: "Inline", Inline: "function envoy_on_request(handle) end"},
		},
	}, nil)

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("UpdateEnvoyExtensionPolicy", mock.Anything, projectID, mock.Anything).Return(errors.New("EEP apply failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to create EnvoyExtensionPolicy in Kubernetes")
}

func TestRouteService_Deploy_Update_WithDirectResponseAndBackends(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/dr"}},
		},
		DirectResponse: &models.DirectResponseConfig{
			StatusCode:  200,
			ContentType: "text/plain",
			Body:        &models.DirectResponseBody{Type: "Inline", Inline: "updated"},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("ApplyDirectResponseConfigMap", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.DirectResponseConfigMapConfig")).Return(nil)
	k8sMock.On("ApplyHTTPRouteFilter", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteFilterConfig")).Return(nil)
	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "ApplyDirectResponseConfigMap", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.DirectResponseConfigMapConfig"))
	k8sMock.AssertCalled(t, "ApplyHTTPRouteFilter", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteFilterConfig"))
}

func TestRouteService_Deploy_Update_WithExternalBackends(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/ext"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeExternal, AddressType: "fqdn", Address: "api.example.com", Port: 443},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig")).Return(nil)
	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig"))
	k8sMock.AssertCalled(t, "DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything)
}

func TestRouteService_Deploy_Update_WithSecurityPolicyAndBTP(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeGeneral
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{AllowOrigins: []string{"https://app.example.com"}},
		},
	}, nil)
	btpRepo.On("GetByRouteID", routeID).Return(&models.BackendTrafficPolicy{
		RouteID: &routeID,
		Config: models.BackendTrafficPolicyConfig{
			Timeout: &models.BTPTimeoutConfig{TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "15s"}},
		},
	}, nil)
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(nil)
	k8sMock.On("UpdateBackendTrafficPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig")).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig"))
	k8sMock.AssertCalled(t, "UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig"))
	k8sMock.AssertCalled(t, "UpdateBackendTrafficPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig"))
}

func TestRouteService_Deploy_Update_WithEnvoyExtensionPolicy(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	eepRepo.On("GetByRouteID", routeID).Return(&models.EnvoyExtensionPolicy{
		RouteID: &routeID,
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{Type: "Inline", Inline: "function envoy_on_request(handle) end"},
		},
	}, nil)

	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	k8sMock.On("UpdateEnvoyExtensionPolicy", mock.Anything, projectID, mock.Anything).Return(nil)
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateEnvoyExtensionPolicy", mock.Anything, projectID, mock.Anything)
}

func TestRouteService_Deploy_Update_DeployBackendsError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/ext"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeExternal, AddressType: "fqdn", Address: "api.example.com", Port: 443},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig")).Return(errors.New("backend update failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to update Backend CRDs in Kubernetes")
}

func TestRouteService_Deploy_Update_DirectResponseError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/dr"}},
		},
		DirectResponse: &models.DirectResponseConfig{
			StatusCode:  200,
			ContentType: "text/plain",
			Body:        &models.DirectResponseBody{Type: "Inline", Inline: "fail"},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	k8sMock.On("ApplyDirectResponseConfigMap", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.DirectResponseConfigMapConfig")).Return(errors.New("cm update failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to update HTTPRouteFilter/ConfigMap in Kubernetes")
}

func TestRouteService_Deploy_Update_UpdateHTTPRouteError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(errors.New("update failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to update HTTPRoute in Kubernetes")
}

func TestRouteService_Deploy_Update_UpdateGRPCRouteError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Protocol = models.RouteProtocolGRPC
	route.Config = models.RouteConfig{
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "grpc-svc", Namespace: "default", Port: 50051},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	k8sMock.On("UpdateGRPCRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GRPCRouteConfig")).Return(errors.New("grpc update failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to update GRPCRoute in Kubernetes")
}

func TestRouteService_Deploy_Update_SecurityPolicyError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeGeneral
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(&models.SecurityPolicy{
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{AllowOrigins: []string{"*"}},
		},
	}, nil)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(errors.New("SP update failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to update SecurityPolicy in Kubernetes")
}

func TestRouteService_Deploy_Update_BackendTrafficPolicyError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	btpRepo.On("GetByRouteID", routeID).Return(&models.BackendTrafficPolicy{
		RouteID: &routeID,
		Config: models.BackendTrafficPolicyConfig{
			Timeout: &models.BTPTimeoutConfig{TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"}},
		},
	}, nil)

	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("UpdateBackendTrafficPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig")).Return(errors.New("BTP update failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to update BackendTrafficPolicy in Kubernetes")
}

func TestRouteService_Deploy_Update_EnvoyExtensionPolicyError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionUpdate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	eepRepo.On("GetByRouteID", routeID).Return(&models.EnvoyExtensionPolicy{
		RouteID: &routeID,
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{Type: "Inline", Inline: "function envoy_on_request(handle) end"},
		},
	}, nil)

	k8sMock.On("DeleteStaleBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String(), mock.Anything).Return(nil)
	k8sMock.On("UpdateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("UpdateEnvoyExtensionPolicy", mock.Anything, projectID, mock.Anything).Return(errors.New("EEP update failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to update EnvoyExtensionPolicy in Kubernetes")
}

func TestRouteService_Deploy_Create_GRPCRouteError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Protocol = models.RouteProtocolGRPC
	route.Config = models.RouteConfig{
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "grpc-svc", Namespace: "default", Port: 50051},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	setupDeployMocksForCreate(routeRepo, approvalRepo, domainRepo, secRepo, btpRepo, eepRepo, wafRepo, caRepo, k8sMock, route, domain)

	k8sMock.On("CreateGRPCRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GRPCRouteConfig")).Return(errors.New("grpc create failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to create GRPCRoute in Kubernetes")
}

func TestRouteService_Deploy_Delete_GRPCRouteError(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Protocol = models.RouteProtocolGRPC
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionDelete}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil)
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteGRPCRoute", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName).Return(errors.New("grpc delete failed"))

	result, err := svc.Deploy(routeID, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to delete GRPCRoute from Kubernetes")
}

func TestRouteService_Deploy_Delete_WithExternalBackends(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/ext"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeExternal, AddressType: "fqdn", Address: "api.example.com", Port: 443},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionDelete}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	wafRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)

	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil)
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteHTTPRoute", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName).Return(nil)
	k8sMock.On("DeleteBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String()).Return(nil)

	// Approval cleanup
	approvalRepo.On("DeleteByEntityID", models.ApprovalEntityRoute, routeID).Return(nil)

	routeRepo.On("Delete", routeID).Return(nil)

	result, err := svc.Deploy(routeID, uuid.New())

	require.NoError(t, err)
	assert.NotNil(t, result)
	k8sMock.AssertCalled(t, "DeleteBackendsByRoute", mock.Anything, projectID, "fastgateway-system", routeID.String())
}

// =========================================================================
// Deploy — Client Mode SecurityPolicy Tests
// =========================================================================

// Test 1: Client mode, deny policy, no clients → should DeleteSecurityPolicy
func TestRouteService_Deploy_ClientMode_Deny_NoClients(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeClient
	route.Config.DefaultTrafficPolicy = models.DefaultTrafficPolicyDeny
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	// No security policy in DB
	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)

	// No client attachments at all
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	// K8s expectations
	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	// No CORS, no authorization → should delete security policy
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security")
}

// Test 2: Client mode, deny policy, has clients but no IP/API key/JWT/MTLS → deny-all
func TestRouteService_Deploy_ClientMode_Deny_WithClients(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()
	clientID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeClient
	route.Config.DefaultTrafficPolicy = models.DefaultTrafficPolicyDeny
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)

	// Has client attachments but none with IP/API key/JWT/MTLS enabled
	attachments := []models.ClientRouteAttachment{
		{
			ID:       uuid.New(),
			ClientID: clientID,
			RouteID:  routeID,
			Status:   models.AttachmentStatusActive,
		},
	}
	caRepo.On("ListActiveByRouteID", routeID).Return(attachments, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	// Should create deny-all SecurityPolicy
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig"))
}

// Test 3: Client mode, deny policy, has clients with IP allowlisting → deny + IP allow rules
func TestRouteService_Deploy_ClientMode_Deny_WithIPClients(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, cipRepo, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()
	clientID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeClient
	route.Config.DefaultTrafficPolicy = models.DefaultTrafficPolicyDeny
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)

	// Client with IP allowlisting (no API key/JWT)
	attachments := []models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          clientID,
			RouteID:           routeID,
			EnableIPAllowlist: true,
			Status:            models.AttachmentStatusActive,
		},
	}
	caRepo.On("ListActiveByRouteID", routeID).Return(attachments, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Client IPs
	cipRepo.On("ListByClientID", clientID).Return([]models.ClientIPAddress{
		{CIDR: "10.0.0.0/24"},
		{CIDR: "192.168.1.0/24"},
	}, nil)

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig"))
}

// Test 4: Client mode, require_ip_allowlist policy → merges defaultAllowedCIDRs + client IPs
func TestRouteService_Deploy_ClientMode_RequireIPAllowlist(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, cipRepo, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()
	clientID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeClient
	route.Config.DefaultTrafficPolicy = models.DefaultTrafficPolicyRequireIPAllowlist
	route.Config.DefaultAllowedCIDRs = []string{"172.16.0.0/16", "10.10.0.0/16"}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)

	// Client with IP allowlisting
	attachments := []models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          clientID,
			RouteID:           routeID,
			EnableIPAllowlist: true,
			Status:            models.AttachmentStatusActive,
		},
	}
	caRepo.On("ListActiveByRouteID", routeID).Return(attachments, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	cipRepo.On("ListByClientID", clientID).Return([]models.ClientIPAddress{
		{CIDR: "10.0.0.0/24"},
	}, nil)

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig"))
}

// Test 5: Client mode, allow_all, has per-client auth (API key) → deny-all base route
func TestRouteService_Deploy_ClientMode_AllowAll_WithPerClientAuth(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()
	clientID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeClient
	route.Config.DefaultTrafficPolicy = models.DefaultTrafficPolicyAllowAll
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)

	// Client with API key enabled (per-client auth)
	attachments := []models.ClientRouteAttachment{
		{
			ID:           uuid.New(),
			ClientID:     clientID,
			RouteID:      routeID,
			EnableAPIKey: true,
			Status:       models.AttachmentStatusActive,
		},
	}
	caRepo.On("ListActiveByRouteID", routeID).Return(attachments, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	// hasPerClientAuth=true, authConfig=nil → deny-all
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig"))
}

// Test 6: Client mode, allow_all, has IP-only clients, no per-client auth → SecurityPolicy with IP rules
func TestRouteService_Deploy_ClientMode_AllowAll_NoPerClientAuth(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, cipRepo, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()
	clientID := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeClient
	route.Config.DefaultTrafficPolicy = models.DefaultTrafficPolicyAllowAll
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)

	// Client with IP allowlisting only (no API key/JWT/MTLS)
	attachments := []models.ClientRouteAttachment{
		{
			ID:                uuid.New(),
			ClientID:          clientID,
			RouteID:           routeID,
			EnableIPAllowlist: true,
			Status:            models.AttachmentStatusActive,
		},
	}
	caRepo.On("ListActiveByRouteID", routeID).Return(attachments, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	cipRepo.On("ListByClientID", clientID).Return([]models.ClientIPAddress{
		{CIDR: "10.0.0.0/24"},
	}, nil)

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	// IP-only client → authConfig has IP rules; no per-client auth → allow_all doesn't override
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig"))
}

// Test 7: Client mode with CORS from DB SecurityPolicy
func TestRouteService_Deploy_ClientMode_WithCORS(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.SecurityMode = models.SecurityModeClient
	route.Config.DefaultTrafficPolicy = models.DefaultTrafficPolicyDeny
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	// Security policy with CORS in DB
	sp := &models.SecurityPolicy{
		ID:      uuid.New(),
		RouteID: routeID,
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://example.com"},
				AllowMethods: []string{"GET", "POST"},
				AllowHeaders: []string{"Content-Type"},
				MaxAge:       func() *int { v := 3600; return &v }(),
			},
		},
	}
	secRepo.On("GetByRouteID", routeID).Return(sp, nil)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)

	// No client attachments — but CORS is present → should deploy SecurityPolicy
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	// CORS present → should UpdateSecurityPolicy (even though no authorization)
	k8sMock.On("UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig")).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	k8sMock.On("DeleteStaleAPIKeyResources", mock.Anything, projectID, "fastgateway-system", routeID.String(), route.K8sRouteName, mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)
	k8sMock.AssertCalled(t, "UpdateSecurityPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.SecurityPolicyConfig"))
}

// =========================================================================
// Deploy — Failover Backend Tests
// =========================================================================

// Test 8: Route with failover backends (K8s) → all backends get Backend CRDs with FQDN
func TestRouteService_Deploy_WithFailoverBackends(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "primary-svc", Namespace: "production", Port: 8080},
			{Type: models.BackendTypeKubernetes, Service: "fallback-svc", Namespace: "production", Port: 8080, Fallback: true},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Both backends should get UpdateBackend calls (failover enabled → all get Backend CRDs)
	var capturedBackends []*kubernetes.BackendConfig
	k8sMock.On("UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig")).
		Run(func(args mock.Arguments) {
			bc := args.Get(2).(*kubernetes.BackendConfig)
			capturedBackends = append(capturedBackends, bc)
		}).Return(nil)
	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)

	// Verify both backends were created
	require.Len(t, capturedBackends, 2)

	// Primary backend: K8s FQDN format
	assert.Equal(t, "fqdn", capturedBackends[0].AddressType)
	assert.Equal(t, "primary-svc.production.svc.cluster.local", capturedBackends[0].Address)
	assert.Equal(t, int32(8080), capturedBackends[0].Port)
	assert.False(t, capturedBackends[0].Fallback)

	// Fallback backend: K8s FQDN format with fallback=true
	assert.Equal(t, "fqdn", capturedBackends[1].AddressType)
	assert.Equal(t, "fallback-svc.production.svc.cluster.local", capturedBackends[1].Address)
	assert.Equal(t, int32(8080), capturedBackends[1].Port)
	assert.True(t, capturedBackends[1].Fallback)
}

// Test 9: External backend with TLS (CA certs only)
func TestRouteService_Deploy_WithExternalBackend_TLS(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/secure"}},
		},
		Backends: []models.RouteBackend{
			{
				Type:        models.BackendTypeExternal,
				AddressType: "fqdn",
				Address:     "secure-api.example.com",
				Port:        443,
				TLS: &models.BackendTLSConfig{
					Mode: models.BackendTLSModeSimple,
					CACertificateRefs: []models.CertificateRef{
						{Kind: "Secret", Name: "ca-cert", Namespace: "fastgateway-system"},
					},
				},
			},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	var capturedBackend *kubernetes.BackendConfig
	k8sMock.On("UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig")).
		Run(func(args mock.Arguments) {
			capturedBackend = args.Get(2).(*kubernetes.BackendConfig)
		}).Return(nil)
	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)

	// Verify TLS config
	require.NotNil(t, capturedBackend)
	require.NotNil(t, capturedBackend.TLS)
	require.Len(t, capturedBackend.TLS.CACertificateRefs, 1)
	assert.Equal(t, "Secret", capturedBackend.TLS.CACertificateRefs[0].Kind)
	assert.Equal(t, "ca-cert", capturedBackend.TLS.CACertificateRefs[0].Name)
	assert.Equal(t, "fastgateway-system", capturedBackend.TLS.CACertificateRefs[0].Namespace)
	assert.Nil(t, capturedBackend.TLS.ClientCertificateRef)
}

// Test 10: External backend with TLS + mTLS (CA certs + client cert)
func TestRouteService_Deploy_WithExternalBackend_TLS_MTLS(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/mtls"}},
		},
		Backends: []models.RouteBackend{
			{
				Type:        models.BackendTypeExternal,
				AddressType: "fqdn",
				Address:     "mtls-api.example.com",
				Port:        443,
				TLS: &models.BackendTLSConfig{
					Mode: models.BackendTLSModeMTLS,
					CACertificateRefs: []models.CertificateRef{
						{Kind: "Secret", Name: "ca-cert", Namespace: "fastgateway-system"},
						{Kind: "ConfigMap", Name: "ca-bundle", Namespace: "fastgateway-system"},
					},
					ClientCertificateRef: &models.SecretRef{
						Name:      "client-cert",
						Namespace: "fastgateway-system",
					},
				},
			},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	var capturedBackend *kubernetes.BackendConfig
	k8sMock.On("UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig")).
		Run(func(args mock.Arguments) {
			capturedBackend = args.Get(2).(*kubernetes.BackendConfig)
		}).Return(nil)
	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)

	// Verify TLS + mTLS config
	require.NotNil(t, capturedBackend)
	require.NotNil(t, capturedBackend.TLS)

	// CA certs
	require.Len(t, capturedBackend.TLS.CACertificateRefs, 2)
	assert.Equal(t, "Secret", capturedBackend.TLS.CACertificateRefs[0].Kind)
	assert.Equal(t, "ca-cert", capturedBackend.TLS.CACertificateRefs[0].Name)
	assert.Equal(t, "ConfigMap", capturedBackend.TLS.CACertificateRefs[1].Kind)
	assert.Equal(t, "ca-bundle", capturedBackend.TLS.CACertificateRefs[1].Name)

	// Client cert (mTLS)
	require.NotNil(t, capturedBackend.TLS.ClientCertificateRef)
	assert.Equal(t, "client-cert", capturedBackend.TLS.ClientCertificateRef.Name)
	assert.Equal(t, "fastgateway-system", capturedBackend.TLS.ClientCertificateRef.Namespace)
}

// Test 11: K8s failover backend with empty namespace → defaults to "default"
func TestRouteService_Deploy_WithFailover_EmptyNamespace(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _, secRepo, btpRepo, eepRepo, wafRepo, caRepo, _, k8sMock := newTestRouteServiceWithK8s()

	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	deployedBy := uuid.New()

	route := makeTestRoute(routeID, domainID)
	route.Config = models.RouteConfig{
		Matches: []models.RouteMatch{
			{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
		},
		Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "primary-svc", Namespace: "", Port: 8080},
			{Type: models.BackendTypeKubernetes, Service: "fallback-svc", Namespace: "", Port: 8080, Fallback: true},
		},
	}
	domain := makeTestDomain(domainID, projectID)

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, routeID).
		Return(&models.Approval{Action: models.ApprovalActionCreate}, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	secRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	btpRepo.On("GetByRouteID", routeID).Return(nil, errors.New("not found"))
	eepRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	wafRepo.On("GetByRouteID", routeID).Return(nil, gorm.ErrRecordNotFound)
	caRepo.On("ListActiveByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("ListApprovedByRouteID", routeID).Return([]models.ClientRouteAttachment{}, nil)
	caRepo.On("UpdateStatusByRouteID", routeID, mock.Anything, mock.Anything).Return(nil).Maybe()

	var capturedBackends []*kubernetes.BackendConfig
	k8sMock.On("UpdateBackend", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.BackendConfig")).
		Run(func(args mock.Arguments) {
			bc := args.Get(2).(*kubernetes.BackendConfig)
			capturedBackends = append(capturedBackends, bc)
		}).Return(nil)
	k8sMock.On("CreateHTTPRoute", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.HTTPRouteConfig")).Return(nil)
	k8sMock.On("DeleteSecurityPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-security").Return(nil).Maybe()
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", route.K8sRouteName+"-eep").Return(nil).Maybe()
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", mock.Anything).Return(nil).Maybe()
	routeRepo.On("Update", mock.Anything).Return(nil)

	result, err := svc.Deploy(routeID, deployedBy)

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, result.Status)

	// Verify empty namespace defaults to "default"
	require.Len(t, capturedBackends, 2)
	assert.Equal(t, "primary-svc.default.svc.cluster.local", capturedBackends[0].Address)
	assert.Equal(t, "fallback-svc.default.svc.cluster.local", capturedBackends[1].Address)
}

func TestDirectResponsePercentWarnings(t *testing.T) {
	cases := []struct {
		name   string
		config *models.RouteConfig
		want   int // expected number of warning strings
	}{
		{
			name:   "nil config returns no warnings",
			config: nil,
			want:   0,
		},
		{
			name:   "nil DirectResponse returns no warnings",
			config: &models.RouteConfig{},
			want:   0,
		},
		{
			name: "DirectResponse without body returns no warnings",
			config: &models.RouteConfig{
				DirectResponse: &models.DirectResponseConfig{StatusCode: 200},
			},
			want: 0,
		},
		{
			name: "ValueRef body returns no warnings (only Inline bodies are inspected)",
			config: &models.RouteConfig{
				DirectResponse: &models.DirectResponseConfig{
					StatusCode: 200,
					Body: &models.DirectResponseBody{
						Type: models.DirectResponseBodyTypeValueRef,
					},
				},
			},
			want: 0,
		},
		{
			name: "Inline body without percent returns no warnings",
			config: &models.RouteConfig{
				DirectResponse: &models.DirectResponseConfig{
					StatusCode: 200,
					Body: &models.DirectResponseBody{
						Type:   models.DirectResponseBodyTypeInline,
						Inline: "Not Found",
					},
				},
			},
			want: 0,
		},
		{
			name: "Inline body with single percent returns one warning",
			config: &models.RouteConfig{
				DirectResponse: &models.DirectResponseConfig{
					StatusCode: 200,
					Body: &models.DirectResponseBody{
						Type:   models.DirectResponseBodyTypeInline,
						Inline: "100% off",
					},
				},
			},
			want: 1,
		},
		{
			name: "Inline body with escaped percent still returns one warning",
			config: &models.RouteConfig{
				DirectResponse: &models.DirectResponseConfig{
					StatusCode: 200,
					Body: &models.DirectResponseBody{
						Type:   models.DirectResponseBodyTypeInline,
						Inline: "100%% off",
					},
				},
			},
			want: 1,
		},
		{
			name: "Inline body with command-operator-shaped percent returns one warning",
			config: &models.RouteConfig{
				DirectResponse: &models.DirectResponseConfig{
					StatusCode: 200,
					Body: &models.DirectResponseBody{
						Type:   models.DirectResponseBodyTypeInline,
						Inline: "%DOWNSTREAM_REMOTE_ADDRESS%",
					},
				},
			},
			want: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := services.DirectResponsePercentWarnings(tc.config)
			assert.Len(t, got, tc.want)
			if tc.want > 0 {
				assert.Contains(t, got[0], "Envoy Gateway v1.8")
				assert.Contains(t, got[0], "%%")
			}
		})
	}
}

// =========================================================================
// Approvals-disabled fast paths (project.ApprovalEnabled == false).
//
// Added in fix round 1 of Task 10+11. Before it, NOTHING in the test tree
// stubbed a project with approvals off -- newTestRouteService never even
// calls SetProjectRepository -- so the three route_write.go fast paths, all
// migrated onto routeStateMachine.To in Task 10, ran unexercised. That same
// blind spot is what let a wrong from-set for the attach fast paths ship (see
// TestClientAttachmentService_AttachFromRoute_FastPath_*).
//
// The Create case is also the origin of the bug those tests caught: it leaves
// the route at APPROVED, not active.
// =========================================================================

// newApprovalsDisabledProjectRepo is the one-line stub these paths needed all
// along.
func newApprovalsDisabledProjectRepo() *mocks.MockProjectRepository {
	projectRepo := new(mocks.MockProjectRepository)
	projectRepo.On("GetByID", mock.Anything).
		Return(&models.Project{ApprovalEnabled: false}, nil).Maybe()
	return projectRepo
}

func TestRouteService_Create_ApprovalsDisabled_LeavesRouteApproved(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, teamRepo := newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.ProjectRepo = newApprovalsDisabledProjectRepo()
	})

	domainID, projectID, teamID, createdBy := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	routeRepo.On("ExistsByName", domainID, "user-api").Return(false, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID, Hostname: "example.com"}, nil)
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID, Name: "platform"}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	routeRepo.On("Create", mock.AnythingOfType("*models.Route")).Return(nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.Status == models.RouteStatusApproved
	})).Return(nil)

	result, err := svc.Create(domainID, &services.CreateRouteInput{
		Name: "user-api", TeamID: teamID, Config: makeBasicHTTPRouteConfig(),
	}, createdBy)

	require.NoError(t, err)
	// THE ROUTE IS `approved`, NOT `active`. This is the fact the attach
	// fast paths' {active} from-set contradicted.
	assert.Equal(t, models.RouteStatusApproved, result.Status)
	assert.Nil(t, result.PendingApproval, "no approval is submitted when approvals are disabled")
	approvalRepo.AssertNotCalled(t, "Create", mock.Anything)
	policyRepo.AssertNotCalled(t, "GetByProjectAndEntity", mock.Anything, mock.Anything, mock.Anything)
	routeRepo.AssertExpectations(t)
}

func TestRouteService_Update_ApprovalsDisabled_GoesToPendingDeploy(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _ := newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.ProjectRepo = newApprovalsDisabledProjectRepo()
	})

	routeID, domainID, projectID := uuid.New(), uuid.New(), uuid.New()

	routeRepo.On("GetByID", routeID).Return(&models.Route{
		ID: routeID, DomainID: domainID, Name: "user-api",
		Status: models.RouteStatusActive, SecurityMode: models.SecurityModeGeneral,
		Config: makeBasicHTTPRouteConfig(), K8sRouteName: "user-api-12345678",
	}, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID, Hostname: "example.com"}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)

	result, err := svc.Update(routeID, &services.UpdateRouteInput{
		Config:      makeBasicHTTPRouteConfig(),
		Description: "revised",
	}, uuid.New())

	require.NoError(t, err)
	// active -> pending_update (persisted) -> pending_deploy, both through To.
	assert.Equal(t, models.RouteStatusPendingDeploy, result.Status)
	assert.Equal(t, "revised", result.Description, "the caller's field edits must survive both transitions")
	approvalRepo.AssertNotCalled(t, "Create", mock.Anything)
	routeRepo.AssertExpectations(t)
}

// The no-op branch of To's contract, on the one site that needed an explicit
// persist: an orphaned pending_update route (Create/Update persists the status
// before calling approvals.Submit, so a failed submit leaves one) must still
// have its Description and Labels written.
func TestRouteService_Update_OrphanedPendingUpdate_StillPersistsFieldEdits(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID, domainID, projectID := uuid.New(), uuid.New(), uuid.New()

	routeRepo.On("GetByID", routeID).Return(&models.Route{
		ID: routeID, DomainID: domainID, Name: "user-api",
		Status: models.RouteStatusPendingUpdate, SecurityMode: models.SecurityModeGeneral,
		Config: makeBasicHTTPRouteConfig(), K8sRouteName: "user-api-12345678",
	}, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID, Hostname: "example.com"}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	// To is on its no-op path here (pending_update -> pending_update), so it
	// writes nothing; the explicit persist in Update is the only thing that
	// carries the description.
	persisted := false
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.Description == "revised after a failed submit"
	})).Run(func(mock.Arguments) { persisted = true }).Return(nil)

	_, err := svc.Update(routeID, &services.UpdateRouteInput{
		Config:      makeBasicHTTPRouteConfig(),
		Description: "revised after a failed submit",
	}, uuid.New())

	require.NoError(t, err)
	assert.True(t, persisted, "field edits must be persisted even when the status transition is a no-op")
}

func TestRouteService_Delete_ApprovalsDisabled_GoesToPendingDeploy(t *testing.T) {
	svc, routeRepo, approvalRepo, _, domainRepo, _ := newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.ProjectRepo = newApprovalsDisabledProjectRepo()
	})

	routeID, domainID, projectID := uuid.New(), uuid.New(), uuid.New()

	routeRepo.On("GetByID", routeID).Return(&models.Route{
		ID: routeID, DomainID: domainID, Name: "user-api", Status: models.RouteStatusActive,
	}, nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)

	result, err := svc.Delete(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDeploy, result.Status)
	approvalRepo.AssertNotCalled(t, "Create", mock.Anything)
	routeRepo.AssertExpectations(t)
}

// =========================================================================
// Orphaned pending_update / pending_delete routes (final fix wave, Fix 1)
//
// Update (route_write.go:665-675) and Delete (:911-916) both persist the new
// status BEFORE calling approvals.Submit. A failed Submit therefore leaves the
// route orphaned at pending_update / pending_delete with NO pending approval,
// which is exactly the precondition Update and Delete gate on
// (route_write.go:611-615 and :899-903, byte-identical). The other operation
// must still be reachable on such a route -- it was, pre-2D.
//
// Phase 2D makes Submit fail more often, not less: internal/approval/
// planning.go:120-159 now errors on repository failures, unknown scopes, and a
// submitter belonging to no team. An instance owner or project admin passes
// middleware/permissions.go:63 with no team membership at all, so an owner
// submitting under a submitter_team policy orphans deterministically.
// =========================================================================

// An orphaned pending_update route must stay DELETABLE.
func TestRouteService_Delete_OrphanedPendingUpdateRoute(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID, domainID, projectID := uuid.New(), uuid.New(), uuid.New()

	route := &models.Route{
		ID: routeID, DomainID: domainID, Name: "user-api",
		Status: models.RouteStatusPendingUpdate,
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	result, err := svc.Delete(routeID, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDelete, result.Status)
}

// An orphaned pending_delete route must stay REVISABLE.
func TestRouteService_Update_OrphanedPendingDeleteRoute(t *testing.T) {
	svc, routeRepo, approvalRepo, policyRepo, domainRepo, _ := newTestRouteService()

	routeID, domainID, projectID := uuid.New(), uuid.New(), uuid.New()

	route := &models.Route{
		ID: routeID, DomainID: domainID, Name: "user-api",
		Status: models.RouteStatusPendingDelete, SecurityMode: models.SecurityModeGeneral,
		Config: makeBasicHTTPRouteConfig(), K8sRouteName: "user-api-12345678",
	}

	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID, Hostname: "example.com"}, nil)
	routeRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{}, int64(0), nil)
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("not found"))
	// models.ErrPolicyNotFound (not a plain error): since Phase 2G,
	// PlanStages classifies any non-sentinel error as a lookup FAILURE, not
	// genuine absence, and returns it instead of falling back to the
	// single-stage default gate these tests expect.
	policyRepo.On("GetByProjectAndEntity", projectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)

	result, err := svc.Update(routeID, &services.UpdateRouteInput{
		Config:      makeBasicHTTPRouteConfig(),
		Description: "revised after a failed delete submit",
	}, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingUpdate, result.Status)
}

// =========================================================================
// Constructor contract (Phase 2E Task 2)
// =========================================================================

func TestNewRouteService_RequiresEveryDependency(t *testing.T) {
	// The fully-wired case must not panic.
	require.NotPanics(t, func() { services.NewRouteService(fullRouteServiceDeps(t)) })

	// Every required dependency must be named when it is missing. IDGen is
	// deliberately excluded: it is an optional determinism seam.
	cases := map[string]func(*services.RouteServiceDeps){
		"RouteRepo":                func(d *services.RouteServiceDeps) { d.RouteRepo = nil },
		"ApprovalRepo":             func(d *services.RouteServiceDeps) { d.ApprovalRepo = nil },
		"PolicyRepo":               func(d *services.RouteServiceDeps) { d.PolicyRepo = nil },
		"DomainRepo":               func(d *services.RouteServiceDeps) { d.DomainRepo = nil },
		"TeamRepo":                 func(d *services.RouteServiceDeps) { d.TeamRepo = nil },
		"ProjectNamespaceRepo":     func(d *services.RouteServiceDeps) { d.ProjectNamespaceRepo = nil },
		"SecurityPolicyRepo":       func(d *services.RouteServiceDeps) { d.SecurityPolicyRepo = nil },
		"BackendTrafficPolicyRepo": func(d *services.RouteServiceDeps) { d.BackendTrafficPolicyRepo = nil },
		"EnvoyExtensionPolicyRepo": func(d *services.RouteServiceDeps) { d.EnvoyExtensionPolicyRepo = nil },
		"WafPolicyRepo":            func(d *services.RouteServiceDeps) { d.WafPolicyRepo = nil },
		"ClientAttachmentRepo":     func(d *services.RouteServiceDeps) { d.ClientAttachmentRepo = nil },
		"ClientIPRepo":             func(d *services.RouteServiceDeps) { d.ClientIPRepo = nil },
		"ClientHeaderRepo":         func(d *services.RouteServiceDeps) { d.ClientHeaderRepo = nil },
		"ClientRepo":               func(d *services.RouteServiceDeps) { d.ClientRepo = nil },
		"ProjectRepo":              func(d *services.RouteServiceDeps) { d.ProjectRepo = nil },
		"Domains":                  func(d *services.RouteServiceDeps) { d.Domains = nil },
		"RouteVersions":            func(d *services.RouteServiceDeps) { d.RouteVersions = nil },
		"Approvals":                func(d *services.RouteServiceDeps) { d.Approvals = nil },
		// The seven cluster roles. Phase 2E Task 9 deleted route_deploy.go's
		// compound "kubernetes service not configured" guard, which covered
		// six of them and silently omitted K8sRefGrants even though
		// ensureReferenceGrantsForDomain dereferences it four lines later.
		"K8sRoutes":        func(d *services.RouteServiceDeps) { d.K8sRoutes = nil },
		"K8sPolicies":      func(d *services.RouteServiceDeps) { d.K8sPolicies = nil },
		"K8sBackends":      func(d *services.RouteServiceDeps) { d.K8sBackends = nil },
		"K8sBackendReaper": func(d *services.RouteServiceDeps) { d.K8sBackendReaper = nil },
		"K8sSecrets":       func(d *services.RouteServiceDeps) { d.K8sSecrets = nil },
		"K8sAPIKeys":       func(d *services.RouteServiceDeps) { d.K8sAPIKeys = nil },
		"K8sRefGrants":     func(d *services.RouteServiceDeps) { d.K8sRefGrants = nil },
	}
	for name, breakIt := range cases {
		t.Run("nil "+name, func(t *testing.T) {
			d := fullRouteServiceDeps(t)
			breakIt(&d)
			assert.PanicsWithValue(t,
				"services.NewRouteService: missing required dependency: "+name,
				func() { services.NewRouteService(d) })
		})
	}
}

func TestNewRouteService_IDGenIsOptional(t *testing.T) {
	d := fullRouteServiceDeps(t)
	d.IDGen = nil
	assert.NotPanics(t, func() { services.NewRouteService(d) },
		"IDGen is an optional determinism seam; nil means uuid.New")
}

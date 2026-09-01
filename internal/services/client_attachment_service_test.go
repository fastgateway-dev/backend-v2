package services_test

import (
	"encoding/json"
	"errors"
	"testing"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newCASApprovalEngine builds the approval engine that a
// ClientAttachmentService's AttachFrom*/RequestDetach (Submit) and
// ApproveStage/RejectStage paths delegate to. Phase 2D Task 8: those methods
// are pure delegation, so without an engine they nil-panic. Phase 2E Task 6:
// the engine is a required ClientAttachmentServiceDeps field, so it is built
// before the service and the completer is registered afterwards, instead of
// both arriving through SetApprovalEngine.
//
// approvalpkg.New panics on a nil dependency, so every slot the caller
// leaves nil is filled with a fresh mock. The stage-review store is always
// stubbed: the engine records every review (the pre-2D nil guard silently
// downgraded MinApprovers>1 to 1), and a single approval satisfies the
// default MinApprovers of 1.
func newCASApprovalEngine(
	approvalRepo *mocks.MockUnifiedApprovalRepository,
	policyRepo *mocks.MockApprovalPolicyRepository,
	teamRepo *mocks.MockTeamRepository,
	projectRepo *mocks.MockProjectRepository,
) *approvalpkg.Engine {
	if approvalRepo == nil {
		approvalRepo = new(mocks.MockUnifiedApprovalRepository)
	}
	if policyRepo == nil {
		policyRepo = new(mocks.MockApprovalPolicyRepository)
	}
	if teamRepo == nil {
		teamRepo = new(mocks.MockTeamRepository)
	}
	if projectRepo == nil {
		projectRepo = new(mocks.MockProjectRepository)
	}

	stageReviewRepo := new(mocks.MockApprovalStageReviewRepository)
	stageReviewRepo.On("ListByStageID", mock.Anything).
		Return([]models.ApprovalStageReview{}, nil).Maybe()
	stageReviewRepo.On("Create", mock.AnythingOfType("*models.ApprovalStageReview")).
		Return(nil).Maybe()
	stageReviewRepo.On("CountByStageAndDecision", mock.Anything, mock.Anything).
		Return(int64(1), nil).Maybe()

	return approvalpkg.New(approvalRepo, stageReviewRepo, policyRepo, teamRepo, projectRepo)
}

// newTestClientAttachmentService stands in for
// services.NewClientAttachmentService now that every dependency is required
// (Phase 2E Task 3). Every test below built its ClientAttachmentService
// positionally, passing nil for whatever the test did not need; this helper
// preserves that call shape by substituting an inert mock for any nil
// argument so construction does not panic. DomainSettingsRepo and
// StageReviewRepo, which used to arrive through SetDomainSettingsRepository
// and SetStageReviewRepository, always get a fresh mock here -- no test in
// this file exercises validateMTLSPairing or the stage-review path directly
// through this constructor, so an unset mock is never called.
//
// projectRepo is a special case, and NOT just an inert stand-in: the guard
// at client_attachment_service.go:285 (`if s.projectRepo != nil`) used a nil
// projectRepo to fall through to the approvals-REQUIRED path (the same
// result as a wired projectRepo whose project has ApprovalEnabled=true).
// FINDING (Phase 2E Task 3): every test in this file that passed nil for
// projectRepo relied on that fall-through, so the default stub created here
// reproduces it with a real, working mock instead of the skipped branch.
func newTestClientAttachmentService(
	attachmentRepo *mocks.MockClientAttachmentRepository,
	approvalRepo *mocks.MockUnifiedApprovalRepository,
	policyRepo *mocks.MockApprovalPolicyRepository,
	clientRepo *mocks.MockClientRepository,
	routeRepo *mocks.MockRouteRepository,
	domainRepo *mocks.MockDomainRepository,
	teamRepo *mocks.MockTeamRepository,
	projectRepo *mocks.MockProjectRepository,
) *services.ClientAttachmentService {
	return newTestClientAttachmentServiceWithEngine(nil,
		attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, teamRepo, projectRepo)
}

// newTestClientAttachmentServiceWithEngine is newTestClientAttachmentService
// for the tests that need to hold the engine themselves. Phase 2E Task 6 made
// the engine a required constructor parameter, so it can no longer be
// attached after the service is built; a nil engine here means "build one
// from this service's own repositories", which is what every call site that
// used to follow the constructor with wireCASApprovalEngine did.
//
// The service is registered as the client_attachment completer, exactly as
// wireCASApprovalEngine used to do.
func newTestClientAttachmentServiceWithEngine(
	engine *approvalpkg.Engine,
	attachmentRepo *mocks.MockClientAttachmentRepository,
	approvalRepo *mocks.MockUnifiedApprovalRepository,
	policyRepo *mocks.MockApprovalPolicyRepository,
	clientRepo *mocks.MockClientRepository,
	routeRepo *mocks.MockRouteRepository,
	domainRepo *mocks.MockDomainRepository,
	teamRepo *mocks.MockTeamRepository,
	projectRepo *mocks.MockProjectRepository,
) *services.ClientAttachmentService {
	if attachmentRepo == nil {
		attachmentRepo = new(mocks.MockClientAttachmentRepository)
	}
	if approvalRepo == nil {
		approvalRepo = new(mocks.MockUnifiedApprovalRepository)
	}
	if policyRepo == nil {
		policyRepo = new(mocks.MockApprovalPolicyRepository)
	}
	if clientRepo == nil {
		clientRepo = new(mocks.MockClientRepository)
	}
	if routeRepo == nil {
		routeRepo = new(mocks.MockRouteRepository)
	}
	if domainRepo == nil {
		domainRepo = new(mocks.MockDomainRepository)
	}
	if teamRepo == nil {
		teamRepo = new(mocks.MockTeamRepository)
	}
	if projectRepo == nil {
		projectRepo = new(mocks.MockProjectRepository)
		projectRepo.On("GetByID", mock.Anything).
			Return(&models.Project{ApprovalEnabled: true}, nil).Maybe()
	}
	if engine == nil {
		engine = newCASApprovalEngine(approvalRepo, policyRepo, teamRepo, projectRepo)
	}
	svc := services.NewClientAttachmentService(services.ClientAttachmentServiceDeps{
		AttachmentRepo:     attachmentRepo,
		ApprovalRepo:       approvalRepo,
		ClientRepo:         clientRepo,
		RouteRepo:          routeRepo,
		DomainRepo:         domainRepo,
		ProjectRepo:        projectRepo,
		DomainSettingsRepo: new(mocks.MockDomainSettingsRepository),
		Approvals:          engine,
	})
	engine.Register(models.ApprovalEntityClientAttachment, svc)
	return svc
}

// ---------------------------------------------------------------------------
// ListByClientID
// ---------------------------------------------------------------------------

func TestClientAttachmentService_ListByClientID_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)

	clientID := uuid.New()
	attachments := []models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID, Status: models.AttachmentStatusActive},
	}
	attachmentRepo.On("ListByClientID", clientID).Return(attachments, nil)

	result, err := svc.ListByClientID(clientID)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, models.AttachmentStatusActive, result[0].Status)
	attachmentRepo.AssertExpectations(t)
}

func TestClientAttachmentService_ListByClientID_WithPendingApproval(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)

	clientID := uuid.New()
	attachmentID := uuid.New()
	attachments := []models.ClientRouteAttachment{
		{ID: attachmentID, ClientID: clientID, Status: models.AttachmentStatusPendingAttach},
	}
	attachmentRepo.On("ListByClientID", clientID).Return(attachments, nil)

	pendingApproval := &models.Approval{ID: uuid.New(), Status: models.ApprovalStatusPending}
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityClientAttachment, attachmentID).Return(pendingApproval, nil)

	result, err := svc.ListByClientID(clientID)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.NotNil(t, result[0].PendingApproval)
	attachmentRepo.AssertExpectations(t)
	approvalRepo.AssertExpectations(t)
}

func TestClientAttachmentService_ListByClientID_Error(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

	clientID := uuid.New()
	attachmentRepo.On("ListByClientID", clientID).Return([]models.ClientRouteAttachment(nil), errors.New("db error"))

	_, err := svc.ListByClientID(clientID)

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ListByRouteID
// ---------------------------------------------------------------------------

func TestClientAttachmentService_ListByRouteID_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)

	routeID := uuid.New()
	attachments := []models.ClientRouteAttachment{
		{ID: uuid.New(), RouteID: routeID, Status: models.AttachmentStatusActive},
	}
	attachmentRepo.On("ListByRouteID", routeID).Return(attachments, nil)

	result, err := svc.ListByRouteID(routeID)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	attachmentRepo.AssertExpectations(t)
}

func TestClientAttachmentService_ListByRouteID_Error(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

	routeID := uuid.New()
	attachmentRepo.On("ListByRouteID", routeID).Return([]models.ClientRouteAttachment(nil), errors.New("db error"))

	_, err := svc.ListByRouteID(routeID)

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// GetAttachment
// ---------------------------------------------------------------------------

func TestClientAttachmentService_GetAttachment_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

	id := uuid.New()
	expected := &models.ClientRouteAttachment{ID: id, Status: models.AttachmentStatusActive}
	attachmentRepo.On("GetByID", id).Return(expected, nil)

	result, err := svc.GetAttachment(id)

	require.NoError(t, err)
	assert.Equal(t, id, result.ID)
	attachmentRepo.AssertExpectations(t)
}

func TestClientAttachmentService_GetAttachment_NotFound(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

	id := uuid.New()
	attachmentRepo.On("GetByID", id).Return(nil, errors.New("not found"))

	_, err := svc.GetAttachment(id)

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// AttachFromRoute
// ---------------------------------------------------------------------------

func TestClientAttachmentService_AttachFromRoute_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, policyRepo, teamRepo, nil),
		attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, teamRepo, nil)

	clientID := uuid.New()
	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submitterID := uuid.New()

	client := &models.Client{ID: clientID, APIKeyEnabled: true}
	route := &models.Route{ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	clientRepo.On("GetByID", clientID).Return(client, nil)
	routeRepo.On("GetByID", routeID).Return(route, nil)
	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	attachmentRepo.On("Create", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	// createApproval mocks
	stages := `[{"order":1,"required_permission":"client_attachment.approve","team_scope":"any"}]`
	policyRepo.On("GetByProjectAndEntity", projectID, "client_attachment", mock.Anything).
		Return(&models.ApprovalPolicy{Stages: []byte(stages)}, nil)
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	attachmentResult := &models.ClientRouteAttachment{ID: uuid.New(), ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusPendingAttach}
	attachmentRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(attachmentResult, nil)

	input := &services.AttachFromRouteInput{
		ClientID:     clientID,
		EnableAPIKey: true,
	}

	result, err := svc.AttachFromRoute(routeID, input, submitterID)

	require.NoError(t, err)
	assert.Equal(t, models.AttachmentStatusPendingAttach, result.Status)
}

func TestClientAttachmentService_AttachFromRoute_ClientNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientAttachmentService(nil, nil, nil, clientRepo, nil, nil, nil, nil)

	clientRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(nil, errors.New("not found"))

	input := &services.AttachFromRouteInput{ClientID: uuid.New(), EnableAPIKey: true}
	_, err := svc.AttachFromRoute(uuid.New(), input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
}

func TestClientAttachmentService_AttachFromRoute_RouteNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, nil, nil, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID}, nil)
	routeRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(nil, errors.New("not found"))

	input := &services.AttachFromRouteInput{ClientID: clientID, EnableAPIKey: true}
	_, err := svc.AttachFromRoute(uuid.New(), input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "route not found")
}

func TestClientAttachmentService_AttachFromRoute_GeneralMode(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, nil, nil, nil)

	clientID := uuid.New()
	routeID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID}, nil)
	routeRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, SecurityMode: models.SecurityModeGeneral}, nil)

	input := &services.AttachFromRouteInput{ClientID: clientID, EnableAPIKey: true}
	_, err := svc.AttachFromRoute(routeID, input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "general security mode")
}

func TestClientAttachmentService_AttachFromRoute_AlreadyAttached(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, clientRepo, routeRepo, nil, nil, nil)

	clientID := uuid.New()
	routeID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID, APIKeyEnabled: true}, nil)
	routeRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, SecurityMode: models.SecurityModeClient}, nil)
	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(&models.ClientRouteAttachment{
		Status: models.AttachmentStatusActive,
	}, nil)

	input := &services.AttachFromRouteInput{ClientID: clientID, EnableAPIKey: true}
	_, err := svc.AttachFromRoute(routeID, input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already attached")
}

// ---------------------------------------------------------------------------
// AttachFromClient
// ---------------------------------------------------------------------------

func TestClientAttachmentService_AttachFromClient_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, policyRepo, teamRepo, nil),
		attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, teamRepo, nil)

	clientID := uuid.New()
	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submitterID := uuid.New()

	client := &models.Client{ID: clientID, APIKeyEnabled: true}
	route := &models.Route{ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient}
	domain := &models.Domain{ID: domainID, ProjectID: projectID}

	clientRepo.On("GetByID", clientID).Return(client, nil)
	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))
	attachmentRepo.On("Create", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	// createApproval mocks
	stages := `[{"order":1,"required_permission":"client_attachment.approve","team_scope":"any"}]`
	policyRepo.On("GetByProjectAndEntity", projectID, "client_attachment", mock.Anything).
		Return(&models.ApprovalPolicy{Stages: []byte(stages)}, nil)
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	attachmentResult := &models.ClientRouteAttachment{ID: uuid.New(), ClientID: clientID, RouteID: routeID, Status: models.AttachmentStatusPendingAttach}
	attachmentRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(attachmentResult, nil)

	input := &services.AttachFromClientInput{
		RouteID:      routeID,
		ProjectID:    projectID,
		EnableAPIKey: true,
	}

	result, err := svc.AttachFromClient(clientID, input, submitterID)

	require.NoError(t, err)
	assert.Equal(t, models.AttachmentStatusPendingAttach, result.Status)
}

func TestClientAttachmentService_AttachFromClient_ClientNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientAttachmentService(nil, nil, nil, clientRepo, nil, nil, nil, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(nil, errors.New("not found"))

	input := &services.AttachFromClientInput{RouteID: uuid.New(), ProjectID: uuid.New(), EnableAPIKey: true}
	_, err := svc.AttachFromClient(clientID, input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
}

func TestClientAttachmentService_AttachFromClient_RouteNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, nil, nil, nil)

	clientID := uuid.New()
	routeID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID}, nil)
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("not found"))

	input := &services.AttachFromClientInput{RouteID: routeID, ProjectID: uuid.New(), EnableAPIKey: true}
	_, err := svc.AttachFromClient(clientID, input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "route not found")
}

func TestClientAttachmentService_AttachFromClient_GeneralMode(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, nil, nil, nil)

	clientID := uuid.New()
	routeID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID}, nil)
	routeRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, SecurityMode: models.SecurityModeGeneral}, nil)

	input := &services.AttachFromClientInput{RouteID: routeID, ProjectID: uuid.New(), EnableAPIKey: true}
	_, err := svc.AttachFromClient(clientID, input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "general security mode")
}

func TestClientAttachmentService_AttachFromClient_AlreadyAttached(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, clientRepo, routeRepo, domainRepo, nil, nil)

	clientID := uuid.New()
	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID, APIKeyEnabled: true}, nil)
	routeRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient}, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(&models.ClientRouteAttachment{
		Status: models.AttachmentStatusActive,
	}, nil)

	input := &services.AttachFromClientInput{RouteID: routeID, ProjectID: projectID, EnableAPIKey: true}
	_, err := svc.AttachFromClient(clientID, input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already attached")
}

func TestClientAttachmentService_AttachFromClient_WrongProject(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	svc := newTestClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, domainRepo, nil, nil)

	clientID := uuid.New()
	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	otherProjectID := uuid.New()

	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID}, nil)
	routeRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient}, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)

	input := &services.AttachFromClientInput{RouteID: routeID, ProjectID: otherProjectID, EnableAPIKey: true}
	_, err := svc.AttachFromClient(clientID, input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong to the specified project")
}

// ---------------------------------------------------------------------------
// RequestDetach
// ---------------------------------------------------------------------------

func TestClientAttachmentService_RequestDetach_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, policyRepo, teamRepo, nil),
		attachmentRepo, approvalRepo, policyRepo, nil, routeRepo, domainRepo, teamRepo, nil)

	attachmentID := uuid.New()
	routeID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submitterID := uuid.New()

	attachment := &models.ClientRouteAttachment{
		ID:      attachmentID,
		RouteID: routeID,
		Status:  models.AttachmentStatusActive,
	}

	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil).Times(2)
	routeRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, DomainID: domainID}, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)
	attachmentRepo.On("Update", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	// createApproval mocks
	stages := `[{"order":1,"required_permission":"client_attachment.approve","team_scope":"any"}]`
	policyRepo.On("GetByProjectAndEntity", projectID, "client_attachment", mock.Anything).
		Return(&models.ApprovalPolicy{Stages: []byte(stages)}, nil)
	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Return(nil)

	result, err := svc.RequestDetach(attachmentID, submitterID)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestClientAttachmentService_RequestDetach_NotActive(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

	attachmentID := uuid.New()
	attachment := &models.ClientRouteAttachment{
		ID:     attachmentID,
		Status: models.AttachmentStatusPendingAttach,
	}
	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)

	_, err := svc.RequestDetach(attachmentID, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "only active attachments")
}

func TestClientAttachmentService_RequestDetach_NotFound(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

	attachmentRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(nil, errors.New("not found"))

	_, err := svc.RequestDetach(uuid.New(), uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "attachment not found")
}

// ---------------------------------------------------------------------------
// ApproveStage (ClientAttachmentService)
// ---------------------------------------------------------------------------

func TestClientAttachmentService_ApproveStage_Success_SingleStage(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	routeRepo := new(mocks.MockRouteRepository)
	projectRepo := new(mocks.MockProjectRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, nil, teamRepo, projectRepo),
		attachmentRepo, approvalRepo, nil, nil, routeRepo, nil, teamRepo, projectRepo)

	approvalID := uuid.New()
	stageID := uuid.New()
	projectID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()
	attachmentID := uuid.New()
	routeID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    attachmentID,
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{
				ID:                 stageID,
				StageOrder:         1,
				RequiredPermission: "client_attachment.approve",
				Status:             models.ApprovalStatusPending,
			},
		},
	}
	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleOwner}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	approvalRepo.On("UpdateStage", mock.AnythingOfType("*models.ApprovalStage")).Return(nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)

	// OnApproved (was OnApprovalComplete)
	attachment := &models.ClientRouteAttachment{ID: attachmentID, RouteID: routeID, Status: models.AttachmentStatusPendingAttach}
	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)
	attachmentRepo.On("Update", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)
	route := &models.Route{ID: routeID, Status: models.RouteStatusActive}
	routeRepo.On("GetByID", routeID).Return(route, nil).Times(2)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)

	result, err := svc.ApproveStage(approvalID, stageID, reviewer)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestClientAttachmentService_ApproveStage_Success_MultiStage_FirstOnly(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	projectRepo := new(mocks.MockProjectRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, nil, teamRepo, projectRepo),
		nil, approvalRepo, nil, nil, nil, nil, teamRepo, projectRepo)

	approvalID := uuid.New()
	stage1ID := uuid.New()
	stage2ID := uuid.New()
	projectID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    uuid.New(),
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stage1ID, StageOrder: 1, RequiredPermission: "client_attachment.approve", Status: models.ApprovalStatusPending},
			{ID: stage2ID, StageOrder: 2, RequiredPermission: "client_attachment.approve", Status: models.ApprovalStatusPending},
		},
	}
	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleOwner}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	approvalRepo.On("UpdateStage", mock.AnythingOfType("*models.ApprovalStage")).Return(nil)
	// No Update on approval since not all stages are approved

	result, err := svc.ApproveStage(approvalID, stage1ID, reviewer)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestClientAttachmentService_ApproveStage_WrongPermission(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	projectRepo := new(mocks.MockProjectRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, nil, teamRepo, projectRepo),
		nil, approvalRepo, nil, nil, nil, nil, teamRepo, projectRepo)

	approvalID := uuid.New()
	stageID := uuid.New()
	projectID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    uuid.New(),
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, RequiredPermission: "client_attachment.approve", Status: models.ApprovalStatusPending},
		},
	}
	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleUser}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil)
	projectRepo.On("IsAdmin", projectID, reviewerID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, reviewerID, models.Permission("client_attachment.approve")).Return(false, nil)

	_, err := svc.ApproveStage(approvalID, stageID, reviewer)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "required permission")
}

func TestClientAttachmentService_ApproveStage_NotPending(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, nil, nil, nil),
		nil, approvalRepo, nil, nil, nil, nil, nil, nil)

	approvalID := uuid.New()
	approval := &models.Approval{
		ID:     approvalID,
		Status: models.ApprovalStatusApproved,
	}
	approvalRepo.On("GetByID", approvalID).Return(approval, nil)

	_, err := svc.ApproveStage(approvalID, uuid.New(), &models.User{ID: uuid.New()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not pending")
}

func TestClientAttachmentService_ApproveStage_SubmitterCannotApprove(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	// The engine resolves the completer before it authorises the stage, so
	// the approval needs its real EntityType; and the pre-2D
	// `if s.projectRepo != nil` guard around the self-approval lookup is
	// gone (an unwired project repo used to silently permit self-approval),
	// so the lookup now always happens. A failed lookup still denies, which
	// is what this test asserts.
	projectID := uuid.New()
	projectRepo := new(mocks.MockProjectRepository)
	projectRepo.On("GetByID", projectID).Return(nil, errors.New("no project"))
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, nil, nil, projectRepo),
		nil, approvalRepo, nil, nil, nil, nil, nil, projectRepo)

	userID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		SubmittedBy: userID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}
	approvalRepo.On("GetByID", approvalID).Return(approval, nil)

	_, err := svc.ApproveStage(approvalID, stageID, &models.User{ID: userID})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "submitter cannot approve")
}

// ---------------------------------------------------------------------------
// RejectStage (ClientAttachmentService)
// ---------------------------------------------------------------------------

func TestClientAttachmentService_RejectStage_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	projectRepo := new(mocks.MockProjectRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, nil, teamRepo, projectRepo),
		attachmentRepo, approvalRepo, nil, nil, nil, nil, teamRepo, projectRepo)

	approvalID := uuid.New()
	stageID := uuid.New()
	projectID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()
	attachmentID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    attachmentID,
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, RequiredPermission: "client_attachment.approve", Status: models.ApprovalStatusPending},
		},
	}
	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleOwner}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	approvalRepo.On("UpdateStage", mock.AnythingOfType("*models.ApprovalStage")).Return(nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)

	// RejectStage updates attachment status to rejected
	attachment := &models.ClientRouteAttachment{ID: attachmentID, Status: models.AttachmentStatusPendingAttach}
	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)
	attachmentRepo.On("Update", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	result, err := svc.RejectStage(approvalID, stageID, reviewer, "not acceptable")

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestClientAttachmentService_RejectStage_AlreadyRejected(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, nil, nil, nil),
		nil, approvalRepo, nil, nil, nil, nil, nil, nil)

	approvalID := uuid.New()
	approval := &models.Approval{
		ID:     approvalID,
		Status: models.ApprovalStatusRejected,
	}
	approvalRepo.On("GetByID", approvalID).Return(approval, nil)

	_, err := svc.RejectStage(approvalID, uuid.New(), &models.User{ID: uuid.New()}, "reason")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not pending")
}

// ---------------------------------------------------------------------------
// GetApproval
// ---------------------------------------------------------------------------

func TestClientAttachmentService_GetApproval_Success(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

	approvalID := uuid.New()
	expected := &models.Approval{ID: approvalID, Status: models.ApprovalStatusPending}
	approvalRepo.On("GetByID", approvalID).Return(expected, nil)

	result, err := svc.GetApproval(approvalID)

	require.NoError(t, err)
	assert.Equal(t, approvalID, result.ID)
}

func TestClientAttachmentService_GetApproval_NotFound(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

	approvalID := uuid.New()
	approvalRepo.On("GetByID", approvalID).Return(nil, errors.New("not found"))

	_, err := svc.GetApproval(approvalID)

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ListApprovalsByProjectID
// ---------------------------------------------------------------------------

func TestClientAttachmentService_ListApprovalsByProjectID_Success(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

	projectID := uuid.New()
	approvals := []models.Approval{
		{ID: uuid.New(), Status: models.ApprovalStatusPending},
	}
	approvalRepo.On("ListByProjectID", projectID, 1, 10, "pending", "client_attachment").Return(approvals, int64(1), nil)

	result, total, err := svc.ListApprovalsByProjectID(projectID, 1, 10, "pending")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, int64(1), total)
}

func TestClientAttachmentService_ListApprovalsByProjectID_Empty(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

	projectID := uuid.New()
	approvalRepo.On("ListByProjectID", projectID, 1, 10, "", "client_attachment").Return([]models.Approval{}, int64(0), nil)

	result, total, err := svc.ListApprovalsByProjectID(projectID, 1, 10, "")

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, int64(0), total)
}

// ---------------------------------------------------------------------------
// OnApproved (was OnApprovalComplete)
// ---------------------------------------------------------------------------

func TestClientAttachmentService_OnApproved_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, routeRepo, nil, nil, nil)

	attachmentID := uuid.New()
	routeID := uuid.New()

	attachment := &models.ClientRouteAttachment{
		ID:      attachmentID,
		RouteID: routeID,
		Status:  models.AttachmentStatusPendingAttach,
	}
	route := &models.Route{
		ID:     routeID,
		Status: models.RouteStatusActive,
	}

	approval := &models.Approval{
		EntityID: attachmentID,
	}

	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)
	attachmentRepo.On("Update", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)
	routeRepo.On("GetByID", routeID).Return(route, nil).Times(2)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)

	err := svc.OnApproved(approval)

	require.NoError(t, err)
	attachmentRepo.AssertExpectations(t)
}

func TestClientAttachmentService_OnApproved_RouteNotActive(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, routeRepo, nil, nil, nil)

	attachmentID := uuid.New()
	routeID := uuid.New()

	attachment := &models.ClientRouteAttachment{
		ID:      attachmentID,
		RouteID: routeID,
		Status:  models.AttachmentStatusPendingAttach,
	}
	route := &models.Route{
		ID:     routeID,
		Status: models.RouteStatusPendingCreate, // not active, so no status change
	}

	approval := &models.Approval{EntityID: attachmentID}

	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)
	attachmentRepo.On("Update", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)
	routeRepo.On("GetByID", routeID).Return(route, nil)

	err := svc.OnApproved(approval)

	require.NoError(t, err)
}

func TestClientAttachmentService_OnApproved_Detach(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, routeRepo, nil, nil, nil)

	attachmentID := uuid.New()
	routeID := uuid.New()

	attachment := &models.ClientRouteAttachment{
		ID:      attachmentID,
		RouteID: routeID,
		Status:  models.AttachmentStatusPendingDetach,
	}
	route := &models.Route{
		ID:     routeID,
		Status: models.RouteStatusActive,
	}

	approval := &models.Approval{
		EntityID: attachmentID,
		Action:   models.ApprovalActionDetach,
	}

	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)
	attachmentRepo.On("Update", mock.MatchedBy(func(a *models.ClientRouteAttachment) bool {
		return a.Status == models.AttachmentStatusRemoved
	})).Return(nil)
	routeRepo.On("GetByID", routeID).Return(route, nil).Times(2)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)

	err := svc.OnApproved(approval)

	require.NoError(t, err)
	attachmentRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// OnRejected (was OnApprovalRejected)
// ---------------------------------------------------------------------------

func TestClientAttachmentService_OnRejected_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := newTestClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

	attachmentID := uuid.New()
	attachment := &models.ClientRouteAttachment{ID: attachmentID, Status: models.AttachmentStatusPendingAttach}
	approval := &models.Approval{EntityID: attachmentID}

	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)
	attachmentRepo.On("Update", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	err := svc.OnRejected(approval)

	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// ListByRouteID with pending approvals
// ---------------------------------------------------------------------------

func TestClientAttachmentService_ListByRouteID_WithPendingApprovals(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := newTestClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)

	routeID := uuid.New()
	attachmentID := uuid.New()

	attachments := []models.ClientRouteAttachment{
		{ID: attachmentID, RouteID: routeID, Status: models.AttachmentStatusPendingAttach},
	}
	attachmentRepo.On("ListByRouteID", routeID).Return(attachments, nil)

	pendingApproval := &models.Approval{ID: uuid.New(), Status: models.ApprovalStatusPending}
	approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityClientAttachment, attachmentID).Return(pendingApproval, nil)

	result, err := svc.ListByRouteID(routeID)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.NotNil(t, result[0].PendingApproval)
}

// TestClientAttachmentService_SetDomainSettingsRepository is gone (Phase 2E
// Task 3). SetDomainSettingsRepository no longer exists: DomainSettingsRepo
// is now a required ClientAttachmentServiceDeps field, set once at
// construction, so there is no setter left to test.

// ---------------------------------------------------------------------------
// AttachFromRoute - resolveTeamScope paths
// ---------------------------------------------------------------------------

func newTestClientAttachmentServiceFull() (
	*services.ClientAttachmentService,
	*mocks.MockClientAttachmentRepository,
	*mocks.MockUnifiedApprovalRepository,
	*mocks.MockApprovalPolicyRepository,
	*mocks.MockClientRepository,
	*mocks.MockRouteRepository,
	*mocks.MockDomainRepository,
	*mocks.MockTeamRepository,
	*mocks.MockProjectRepository,
) {
	return newTestClientAttachmentServiceWithApprovals(true)
}

// newTestClientAttachmentServiceApprovalsDisabled is the sibling of
// newTestClientAttachmentServiceFull for a project with ApprovalEnabled=false,
// i.e. the attach/detach FAST PATHS.
//
// Those paths had no coverage at all before fix round 1 of Task 10+11 -- every
// fixture in this file stubbed ApprovalEnabled=true -- which is how a wrong
// derived from-set for route.Status reached the state machine unnoticed. It
// cannot be an override on the Full helper: that helper registers its own
// .Maybe() stub for the same method and arguments, and testify's
// findExpectedCall returns the FIRST match with Repeatability > -1, so a later
// On("GetByID", ...) would never be reached.
func newTestClientAttachmentServiceApprovalsDisabled() (
	*services.ClientAttachmentService,
	*mocks.MockClientAttachmentRepository,
	*mocks.MockUnifiedApprovalRepository,
	*mocks.MockApprovalPolicyRepository,
	*mocks.MockClientRepository,
	*mocks.MockRouteRepository,
	*mocks.MockDomainRepository,
	*mocks.MockTeamRepository,
	*mocks.MockProjectRepository,
) {
	return newTestClientAttachmentServiceWithApprovals(false)
}

func newTestClientAttachmentServiceWithApprovals(approvalEnabled bool) (
	*services.ClientAttachmentService,
	*mocks.MockClientAttachmentRepository,
	*mocks.MockUnifiedApprovalRepository,
	*mocks.MockApprovalPolicyRepository,
	*mocks.MockClientRepository,
	*mocks.MockRouteRepository,
	*mocks.MockDomainRepository,
	*mocks.MockTeamRepository,
	*mocks.MockProjectRepository,
) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)

	svc := newTestClientAttachmentServiceWithEngine(
		newCASApprovalEngine(approvalRepo, policyRepo, teamRepo, projectRepo),
		attachmentRepo, approvalRepo, policyRepo,
		clientRepo, routeRepo, domainRepo, teamRepo, projectRepo,
	)

	projectRepo.On("GetByID", mock.Anything).
		Return(&models.Project{ApprovalEnabled: approvalEnabled}, nil).Maybe()

	return svc, attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, teamRepo, projectRepo
}

// ---------------------------------------------------------------------------
// Attach/detach FAST PATHS (project.ApprovalEnabled == false).
//
// Added in fix round 1 of Task 10+11. The derived from-set for these three
// sites was {active}, argued from the approvals-ENABLED counterpart's
// `route.Status == active` guard. That argument was wrong: neither attach
// function reads route.Status at all, and in an approvals-disabled project
// RouteService.Create leaves the route at APPROVED, never active. So the
// ordinary flow create -> attach -> deploy hit
// `illegal transition "approved" -> "pending_deploy"`.
// ---------------------------------------------------------------------------

// attachFastPathRouteStatusCase drives AttachFromRoute's approvals-disabled
// branch against a route sitting at the given status.
func attachFastPathRouteStatusCase(t *testing.T, status models.RouteStatus, wantRouteWrite bool) {
	t.Helper()

	svc, attachmentRepo, _, _, clientRepo, routeRepo, domainRepo, _, _ :=
		newTestClientAttachmentServiceApprovalsDisabled()

	routeID, clientID, domainID := uuid.New(), uuid.New(), uuid.New()
	attachmentID, submittedBy := uuid.New(), uuid.New()

	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID, IPAddressCount: 1}, nil)
	routeRepo.On("GetByID", routeID).Return(&models.Route{
		ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient, Status: status,
	}, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: uuid.New()}, nil)

	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))
	attachmentRepo.On("Create", mock.AnythingOfType("*models.ClientRouteAttachment")).
		Run(func(args mock.Arguments) {
			args.Get(0).(*models.ClientRouteAttachment).ID = attachmentID
		}).Return(nil)
	attachmentRepo.On("Update", mock.MatchedBy(func(a *models.ClientRouteAttachment) bool {
		return a.Status == models.AttachmentStatusApproved
	})).Return(nil)
	attachmentRepo.On("GetByID", attachmentID).
		Return(&models.ClientRouteAttachment{ID: attachmentID, ClientID: clientID, RouteID: routeID}, nil)

	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Status == models.RouteStatusPendingDeploy
	})).Return(nil).Maybe()

	result, err := svc.AttachFromRoute(routeID, &services.AttachFromRouteInput{
		ClientID:          clientID,
		EnableIPAllowlist: true,
	}, submittedBy)

	require.NoError(t, err, "attaching a client to a %s route must not be rejected by the state machine", status)
	assert.NotNil(t, result)

	if wantRouteWrite {
		routeRepo.AssertCalled(t, "Update", mock.Anything)
	} else {
		routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	}
}

// The regression this fix round exists for: create a route in an
// approvals-disabled project (which leaves it at `approved`, route_write.go
// Create/approvals-disabled), then attach a client to it.
func TestClientAttachmentService_AttachFromRoute_FastPath_ApprovedRoute(t *testing.T) {
	attachFastPathRouteStatusCase(t, models.RouteStatusApproved, true)
}

func TestClientAttachmentService_AttachFromRoute_FastPath_ActiveRoute(t *testing.T) {
	attachFastPathRouteStatusCase(t, models.RouteStatusActive, true)
}

// Already at the target: To's no-op path. No route write at all, where the
// pre-2D code did a redundant same-value one.
func TestClientAttachmentService_AttachFromRoute_FastPath_PendingDeployRouteIsANoOp(t *testing.T) {
	attachFastPathRouteStatusCase(t, models.RouteStatusPendingDeploy, false)
}

// Reachable only through the runtime toggle (project_service.go UpdateProject
// flips ApprovalEnabled): the route was created, and rejected, while approvals
// were on; approvals are then turned off and a client is attached.
func TestClientAttachmentService_AttachFromRoute_FastPath_RejectedRoute(t *testing.T) {
	attachFastPathRouteStatusCase(t, models.RouteStatusRejected, true)
}

// Same toggle, or an orphan: Create persists pending_create before calling
// approvals.Submit, so a failed submit leaves the row there with no approval.
func TestClientAttachmentService_AttachFromRoute_FastPath_PendingCreateRoute(t *testing.T) {
	attachFastPathRouteStatusCase(t, models.RouteStatusPendingCreate, true)
}

// ---------------------------------------------------------------------------
// The same status-parameterised coverage for the OTHER two fast paths.
//
// Added in the final fix wave. Only AttachFromRoute got these in fix round 1;
// AttachFromClient (client_attachment_service.go:443) and RequestDetach
// (:521) were left uncovered on the grounds that their bodies are identical
// to AttachFromRoute's. That is precisely the reasoning that produced the
// Critical fix round 1 had to fix -- #9's derived from-set was copied from
// #8's sibling rather than derived from the code, and was wrong -- so it is
// not a basis for skipping the tests. Each site gets its own coverage.
//
// These are regression tests for the state machine, not for the attach logic:
// each drives the real approvals-disabled branch against a route sitting at
// the given status and asserts the transition to pending_deploy is not
// rejected.
// ---------------------------------------------------------------------------

// attachFromClientFastPathRouteStatusCase drives AttachFromClient's
// approvals-disabled branch against a route sitting at the given status.
func attachFromClientFastPathRouteStatusCase(t *testing.T, status models.RouteStatus, wantRouteWrite bool) {
	t.Helper()

	svc, attachmentRepo, _, _, clientRepo, routeRepo, domainRepo, _, _ :=
		newTestClientAttachmentServiceApprovalsDisabled()

	routeID, clientID, domainID := uuid.New(), uuid.New(), uuid.New()
	projectID := uuid.New()
	attachmentID, submittedBy := uuid.New(), uuid.New()

	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID, IPAddressCount: 1}, nil)
	routeRepo.On("GetByID", routeID).Return(&models.Route{
		ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient, Status: status,
	}, nil)
	// AttachFromClient additionally checks domain.ProjectID == input.ProjectID.
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: projectID}, nil)

	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))
	attachmentRepo.On("Create", mock.AnythingOfType("*models.ClientRouteAttachment")).
		Run(func(args mock.Arguments) {
			args.Get(0).(*models.ClientRouteAttachment).ID = attachmentID
		}).Return(nil)
	attachmentRepo.On("Update", mock.MatchedBy(func(a *models.ClientRouteAttachment) bool {
		return a.Status == models.AttachmentStatusApproved
	})).Return(nil)
	attachmentRepo.On("GetByID", attachmentID).
		Return(&models.ClientRouteAttachment{ID: attachmentID, ClientID: clientID, RouteID: routeID}, nil)

	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Status == models.RouteStatusPendingDeploy
	})).Return(nil).Maybe()

	result, err := svc.AttachFromClient(clientID, &services.AttachFromClientInput{
		RouteID:           routeID,
		ProjectID:         projectID,
		EnableIPAllowlist: true,
	}, submittedBy)

	require.NoError(t, err, "attaching a client to a %s route must not be rejected by the state machine", status)
	assert.NotNil(t, result)

	if wantRouteWrite {
		routeRepo.AssertCalled(t, "Update", mock.Anything)
	} else {
		routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	}
}

// The mirror of the regression fix round 1 existed for: Create in an
// approvals-disabled project leaves the route at `approved`, not active.
func TestClientAttachmentService_AttachFromClient_FastPath_ApprovedRoute(t *testing.T) {
	attachFromClientFastPathRouteStatusCase(t, models.RouteStatusApproved, true)
}

func TestClientAttachmentService_AttachFromClient_FastPath_ActiveRoute(t *testing.T) {
	attachFromClientFastPathRouteStatusCase(t, models.RouteStatusActive, true)
}

// Already at the target: To's no-op path, so no route write at all.
func TestClientAttachmentService_AttachFromClient_FastPath_PendingDeployRouteIsANoOp(t *testing.T) {
	attachFromClientFastPathRouteStatusCase(t, models.RouteStatusPendingDeploy, false)
}

// Reachable through the ApprovalEnabled runtime toggle
// (ProjectService.UpdateProject).
func TestClientAttachmentService_AttachFromClient_FastPath_RejectedRoute(t *testing.T) {
	attachFromClientFastPathRouteStatusCase(t, models.RouteStatusRejected, true)
}

// Same toggle, or an orphan left by a failed approvals.Submit inside Create.
func TestClientAttachmentService_AttachFromClient_FastPath_PendingCreateRoute(t *testing.T) {
	attachFromClientFastPathRouteStatusCase(t, models.RouteStatusPendingCreate, true)
}

// detachFastPathRouteStatusCase drives RequestDetach's approvals-disabled
// branch against a route sitting at the given status.
//
// RequestDetach's own derived from-set is {active, pending_update,
// pending_delete}: it is guarded on attachment.Status == active, an attachment
// only becomes active inside a successful Deploy, and Deploy sets
// route.Status = active in the same call, so the route was active at that
// moment and can only have moved on from there.
//
// PHASE 2E TASK 11. Until this task the table was keyed globally on
// (from, to), so the ATTACH paths' extra origins -- approved, rejected,
// pending_create -- were accepted here too. transitions.md recorded that as
// "Known residual gaps" item 3 and said per-site keying would fix it; the
// three cases below were written to FAIL when it did, rather than let the
// change land silently. It has landed: they now assert the rejection. See
// detachFastPathRejectedRouteStatusCase.
func detachFastPathRouteStatusCase(t *testing.T, status models.RouteStatus, wantRouteWrite bool) {
	t.Helper()

	svc, attachmentRepo, _, _, _, routeRepo, domainRepo, _, _ :=
		newTestClientAttachmentServiceApprovalsDisabled()

	routeID, clientID, domainID := uuid.New(), uuid.New(), uuid.New()
	attachmentID, submittedBy := uuid.New(), uuid.New()

	// Only an ACTIVE attachment can be detached (client_attachment_service.go
	// :479-481); that guard is on the attachment, not the route.
	attachmentRepo.On("GetByID", attachmentID).Return(&models.ClientRouteAttachment{
		ID: attachmentID, ClientID: clientID, RouteID: routeID,
		Status: models.AttachmentStatusActive,
	}, nil)
	routeRepo.On("GetByID", routeID).Return(&models.Route{
		ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient, Status: status,
	}, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: uuid.New()}, nil)

	attachmentRepo.On("Update", mock.MatchedBy(func(a *models.ClientRouteAttachment) bool {
		return a.Status == models.AttachmentStatusRemoved
	})).Return(nil)

	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Status == models.RouteStatusPendingDeploy
	})).Return(nil).Maybe()

	result, err := svc.RequestDetach(attachmentID, submittedBy)

	require.NoError(t, err, "detaching a client from a %s route must not be rejected by the state machine", status)
	assert.NotNil(t, result)

	if wantRouteWrite {
		routeRepo.AssertCalled(t, "Update", mock.Anything)
	} else {
		routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	}
	// The attachment is only marked removed once the route transition has
	// succeeded (the ORDER MATTERS note at :500-517).
	attachmentRepo.AssertCalled(t, "Update", mock.Anything)
}

// The mainline: RequestDetach's own derivation says the route is active here.
func TestClientAttachmentService_RequestDetach_FastPath_ActiveRoute(t *testing.T) {
	detachFastPathRouteStatusCase(t, models.RouteStatusActive, true)
}

// Already at the target: To's no-op path, so no route write at all.
func TestClientAttachmentService_RequestDetach_FastPath_PendingDeployRouteIsANoOp(t *testing.T) {
	detachFastPathRouteStatusCase(t, models.RouteStatusPendingDeploy, false)
}

// The two origins reachable from active, both inside RequestDetach's own
// derived from-set: an attachment stays active across a later Update or
// Delete submission, so a detach can land on an in-flight route.
func TestClientAttachmentService_RequestDetach_FastPath_PendingUpdateRoute(t *testing.T) {
	detachFastPathRouteStatusCase(t, models.RouteStatusPendingUpdate, true)
}

func TestClientAttachmentService_RequestDetach_FastPath_PendingDeleteRoute(t *testing.T) {
	detachFastPathRouteStatusCase(t, models.RouteStatusPendingDelete, true)
}

// detachFastPathRejectedRouteStatusCase is the negative half: a status that
// only the ATTACH fast paths can reach must be refused at the DETACH site,
// and the refusal must leave both records untouched.
//
// ORDER MATTERS (client_attachment_service.go RequestDetach): the route
// transition is attempted before the attachment is marked removed, precisely
// so a rejection cannot leave a persisted "removed" attachment against a route
// that was never queued for redeploy -- the client would keep working in
// Kubernetes while the database said it was detached. This asserts that.
func detachFastPathRejectedRouteStatusCase(t *testing.T, status models.RouteStatus) {
	t.Helper()

	svc, attachmentRepo, _, _, _, routeRepo, domainRepo, _, _ :=
		newTestClientAttachmentServiceApprovalsDisabled()

	routeID, clientID, domainID := uuid.New(), uuid.New(), uuid.New()
	attachmentID, submittedBy := uuid.New(), uuid.New()

	attachmentRepo.On("GetByID", attachmentID).Return(&models.ClientRouteAttachment{
		ID: attachmentID, ClientID: clientID, RouteID: routeID,
		Status: models.AttachmentStatusActive,
	}, nil)
	route := &models.Route{
		ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient, Status: status,
	}
	routeRepo.On("GetByID", routeID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: uuid.New()}, nil)

	_, err := svc.RequestDetach(attachmentID, submittedBy)

	require.Error(t, err, "an attachment cannot be active on a %s route, so the detach must be refused", status)
	assert.Contains(t, err.Error(), "SiteRequestDetach")
	assert.Equal(t, status, route.Status, "the route must not be mutated")
	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	attachmentRepo.AssertNotCalled(t, "Update", mock.Anything)
}

// PHASE 2E TASK 11: these three were legal only by the old global (from, to)
// keying. An attachment only becomes active inside a successful Deploy, which
// sets route.Status = active, so no active attachment can point at a route
// that is still approved, rejected or pending_create.
func TestClientAttachmentService_RequestDetach_FastPath_ApprovedRouteIsRejected(t *testing.T) {
	detachFastPathRejectedRouteStatusCase(t, models.RouteStatusApproved)
}

func TestClientAttachmentService_RequestDetach_FastPath_RejectedRouteIsRejected(t *testing.T) {
	detachFastPathRejectedRouteStatusCase(t, models.RouteStatusRejected)
}

func TestClientAttachmentService_RequestDetach_FastPath_PendingCreateRouteIsRejected(t *testing.T) {
	detachFastPathRejectedRouteStatusCase(t, models.RouteStatusPendingCreate)
}

func TestClientAttachmentService_AttachFromRoute_ClientNotFound2(t *testing.T) {
	svc, _, _, _, clientRepo, _, _, _, _ := newTestClientAttachmentServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(nil, errors.New("not found"))

	input := &services.AttachFromRouteInput{
		ClientID:          clientID,
		EnableIPAllowlist: true,
	}

	result, err := svc.AttachFromRoute(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "client not found")
}

func TestClientAttachmentService_AttachFromRoute_RouteNotFound2(t *testing.T) {
	svc, _, _, _, clientRepo, routeRepo, _, _, _ := newTestClientAttachmentServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	client := &models.Client{ID: clientID, IPAddressCount: 1}
	clientRepo.On("GetByID", clientID).Return(client, nil)
	routeRepo.On("GetByID", routeID).Return(nil, errors.New("not found"))

	input := &services.AttachFromRouteInput{
		ClientID:          clientID,
		EnableIPAllowlist: true,
	}

	result, err := svc.AttachFromRoute(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "route not found")
}

func TestClientAttachmentService_AttachFromRoute_GeneralModeRejected(t *testing.T) {
	svc, _, _, _, clientRepo, routeRepo, _, _, _ := newTestClientAttachmentServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	client := &models.Client{ID: clientID, IPAddressCount: 1}
	clientRepo.On("GetByID", clientID).Return(client, nil)

	route := &models.Route{ID: routeID, SecurityMode: models.SecurityModeGeneral}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	input := &services.AttachFromRouteInput{
		ClientID:          clientID,
		EnableIPAllowlist: true,
	}

	result, err := svc.AttachFromRoute(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "client attachments are not available for routes with general security mode")
}

func TestClientAttachmentService_AttachFromRoute_NoAuthMethodEnabled(t *testing.T) {
	svc, attachmentRepo, _, _, clientRepo, routeRepo, _, _, _ := newTestClientAttachmentServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	client := &models.Client{ID: clientID}
	clientRepo.On("GetByID", clientID).Return(client, nil)

	route := &models.Route{ID: routeID, SecurityMode: models.SecurityModeClient}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))

	input := &services.AttachFromRouteInput{
		ClientID: clientID,
		// No auth methods enabled
	}

	result, err := svc.AttachFromRoute(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "at least one authentication method must be enabled")
}

func TestClientAttachmentService_AttachFromRoute_ResolveTeamScope_Any(t *testing.T) {
	svc, attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, _, _ := newTestClientAttachmentServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submittedBy := uuid.New()

	client := &models.Client{ID: clientID, IPAddressCount: 1}
	clientRepo.On("GetByID", clientID).Return(client, nil)

	route := &models.Route{ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))
	attachmentRepo.On("Create", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	domain := &models.Domain{ID: domainID, ProjectID: projectID}
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	// Policy with "any" team scope
	stagesJSON, _ := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "client_attachment.approve", TeamScope: "any"},
	})
	policy := &models.ApprovalPolicy{Stages: stagesJSON}
	policyRepo.On("GetByProjectAndEntity", projectID, "client_attachment", mock.Anything).Return(policy, nil)

	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Run(func(args mock.Arguments) {
		approval := args.Get(0).(*models.Approval)
		assert.Len(t, approval.Stages, 1)
		assert.Nil(t, approval.Stages[0].RequiredTeamID) // "any" => nil
	}).Return(nil)

	attachmentID := uuid.New()
	enrichedAttachment := &models.ClientRouteAttachment{ID: attachmentID, ClientID: clientID, RouteID: routeID}
	attachmentRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(enrichedAttachment, nil)

	input := &services.AttachFromRouteInput{
		ClientID:          clientID,
		EnableIPAllowlist: true,
	}

	result, err := svc.AttachFromRoute(routeID, input, submittedBy)

	require.NoError(t, err)
	assert.NotNil(t, result)
	approvalRepo.AssertExpectations(t)
}

func TestClientAttachmentService_AttachFromRoute_ResolveTeamScope_SubmitterTeam(t *testing.T) {
	svc, attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, teamRepo, _ := newTestClientAttachmentServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submittedBy := uuid.New()
	submitterTeamID := uuid.New()

	client := &models.Client{ID: clientID, IPAddressCount: 1}
	clientRepo.On("GetByID", clientID).Return(client, nil)

	route := &models.Route{ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))
	attachmentRepo.On("Create", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	domain := &models.Domain{ID: domainID, ProjectID: projectID}
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	// Policy with "submitter_team" team scope
	stagesJSON, _ := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "client_attachment.approve", TeamScope: "submitter_team"},
	})
	policy := &models.ApprovalPolicy{Stages: stagesJSON}
	policyRepo.On("GetByProjectAndEntity", projectID, "client_attachment", mock.Anything).Return(policy, nil)

	// Submitter belongs to a team in this project
	teamRepo.On("GetUserTeamsInProject", projectID, submittedBy).Return([]models.ProjectTeamRole{
		{TeamID: submitterTeamID, ProjectID: projectID},
	}, nil)

	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Run(func(args mock.Arguments) {
		approval := args.Get(0).(*models.Approval)
		assert.Len(t, approval.Stages, 1)
		assert.Equal(t, &submitterTeamID, approval.Stages[0].RequiredTeamID)
	}).Return(nil)

	attachmentID := uuid.New()
	enrichedAttachment := &models.ClientRouteAttachment{ID: attachmentID, ClientID: clientID, RouteID: routeID}
	attachmentRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(enrichedAttachment, nil)

	input := &services.AttachFromRouteInput{
		ClientID:          clientID,
		EnableIPAllowlist: true,
	}

	result, err := svc.AttachFromRoute(routeID, input, submittedBy)

	require.NoError(t, err)
	assert.NotNil(t, result)
	approvalRepo.AssertExpectations(t)
}

func TestClientAttachmentService_AttachFromRoute_ResolveTeamScope_OtherTeam(t *testing.T) {
	svc, attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, teamRepo, _ := newTestClientAttachmentServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submittedBy := uuid.New()
	submitterTeamID := uuid.New()
	otherTeamID := uuid.New()

	client := &models.Client{ID: clientID, IPAddressCount: 1}
	clientRepo.On("GetByID", clientID).Return(client, nil)

	route := &models.Route{ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))
	attachmentRepo.On("Create", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	domain := &models.Domain{ID: domainID, ProjectID: projectID}
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	// Policy with "other_team" team scope
	stagesJSON, _ := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "client_attachment.approve", TeamScope: "other_team"},
	})
	policy := &models.ApprovalPolicy{Stages: stagesJSON}
	policyRepo.On("GetByProjectAndEntity", projectID, "client_attachment", mock.Anything).Return(policy, nil)

	// Submitter belongs to submitterTeamID
	teamRepo.On("GetUserTeamsInProject", projectID, submittedBy).Return([]models.ProjectTeamRole{
		{TeamID: submitterTeamID, ProjectID: projectID},
	}, nil)

	// Project has two teams
	teamRepo.On("ListProjectTeams", projectID).Return([]models.ProjectTeamRole{
		{TeamID: submitterTeamID, ProjectID: projectID},
		{TeamID: otherTeamID, ProjectID: projectID},
	}, nil)

	approvalRepo.On("Create", mock.AnythingOfType("*models.Approval")).Run(func(args mock.Arguments) {
		approval := args.Get(0).(*models.Approval)
		assert.Len(t, approval.Stages, 1)
		assert.Equal(t, &otherTeamID, approval.Stages[0].RequiredTeamID) // resolved to other team
	}).Return(nil)

	attachmentID := uuid.New()
	enrichedAttachment := &models.ClientRouteAttachment{ID: attachmentID, ClientID: clientID, RouteID: routeID}
	attachmentRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(enrichedAttachment, nil)

	input := &services.AttachFromRouteInput{
		ClientID:          clientID,
		EnableIPAllowlist: true,
	}

	result, err := svc.AttachFromRoute(routeID, input, submittedBy)

	require.NoError(t, err)
	assert.NotNil(t, result)
	approvalRepo.AssertExpectations(t)
}

func TestClientAttachmentService_AttachFromRoute_ResolveTeamScope_UnknownScope(t *testing.T) {
	svc, attachmentRepo, _, policyRepo, clientRepo, routeRepo, domainRepo, _, _ := newTestClientAttachmentServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()
	submittedBy := uuid.New()

	client := &models.Client{ID: clientID, IPAddressCount: 1}
	clientRepo.On("GetByID", clientID).Return(client, nil)

	route := &models.Route{ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))
	attachmentRepo.On("Create", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	domain := &models.Domain{ID: domainID, ProjectID: projectID}
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	// Policy with unknown team scope
	stagesJSON, _ := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "client_attachment.approve", TeamScope: "unknown_scope"},
	})
	policy := &models.ApprovalPolicy{Stages: stagesJSON}
	policyRepo.On("GetByProjectAndEntity", projectID, "client_attachment", mock.Anything).Return(policy, nil)

	input := &services.AttachFromRouteInput{
		ClientID:          clientID,
		EnableIPAllowlist: true,
	}

	result, err := svc.AttachFromRoute(routeID, input, submittedBy)

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "unknown team_scope")
}

func TestClientAttachmentService_AttachFromRoute_APIKeyNotConfigured(t *testing.T) {
	svc, attachmentRepo, _, _, clientRepo, routeRepo, _, _, _ := newTestClientAttachmentServiceFull()

	routeID := uuid.New()
	clientID := uuid.New()

	client := &models.Client{ID: clientID, APIKeyEnabled: false}
	clientRepo.On("GetByID", clientID).Return(client, nil)

	route := &models.Route{ID: routeID, SecurityMode: models.SecurityModeClient}
	routeRepo.On("GetByID", routeID).Return(route, nil)

	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))

	input := &services.AttachFromRouteInput{
		ClientID:     clientID,
		EnableAPIKey: true,
	}

	result, err := svc.AttachFromRoute(routeID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "client does not have an API key configured; generate one first")
}

// A rejected route transition must leave NO partial write behind.
//
// Until fix round 1 of Task 10+11 the fast path persisted
// attachment.Status = approved BEFORE attempting the route transition, so a
// rejection left an approved attachment pointing at an untouched route --
// a state the pre-2D code could not produce, because pre-2D the route write
// could not be rejected. The order is now reversed.
//
// An unknown route status is used as the trigger because, after the from-set
// correction above, every real status is either legal or a no-op here; it is
// the "no transitions defined from" arm of To rather than the illegal-pair
// arm, which is the same failure as far as ordering is concerned.
func TestClientAttachmentService_AttachFromRoute_FastPath_RejectedTransitionWritesNothing(t *testing.T) {
	svc, attachmentRepo, _, _, clientRepo, routeRepo, domainRepo, _, _ :=
		newTestClientAttachmentServiceApprovalsDisabled()

	routeID, clientID, domainID := uuid.New(), uuid.New(), uuid.New()

	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID, IPAddressCount: 1}, nil)
	routeRepo.On("GetByID", routeID).Return(&models.Route{
		ID: routeID, DomainID: domainID, SecurityMode: models.SecurityModeClient,
		Status: models.RouteStatus("something-unrecognised"),
	}, nil)
	domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, ProjectID: uuid.New()}, nil)
	attachmentRepo.On("GetByClientAndRoute", clientID, routeID).Return(nil, errors.New("not found"))
	attachmentRepo.On("Create", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	result, err := svc.AttachFromRoute(routeID, &services.AttachFromRouteInput{
		ClientID:          clientID,
		EnableIPAllowlist: true,
	}, uuid.New())

	require.Error(t, err)
	assert.Nil(t, result)
	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	// The decisive assertion: the attachment was NOT promoted to approved.
	attachmentRepo.AssertNotCalled(t, "Update", mock.Anything)
}

// ---------------------------------------------------------------------------
// NewClientAttachmentService
// ---------------------------------------------------------------------------

func fullClientAttachmentServiceDeps() services.ClientAttachmentServiceDeps {
	return services.ClientAttachmentServiceDeps{
		AttachmentRepo:     new(mocks.MockClientAttachmentRepository),
		ApprovalRepo:       new(mocks.MockUnifiedApprovalRepository),
		ClientRepo:         new(mocks.MockClientRepository),
		RouteRepo:          new(mocks.MockRouteRepository),
		DomainRepo:         new(mocks.MockDomainRepository),
		ProjectRepo:        new(mocks.MockProjectRepository),
		DomainSettingsRepo: new(mocks.MockDomainSettingsRepository),
		Approvals:          newCASApprovalEngine(nil, nil, nil, nil),
	}
}

func TestNewClientAttachmentService_RequiresEveryDependency(t *testing.T) {
	require.NotPanics(t, func() { services.NewClientAttachmentService(fullClientAttachmentServiceDeps()) })

	cases := map[string]func(*services.ClientAttachmentServiceDeps){
		"AttachmentRepo":     func(d *services.ClientAttachmentServiceDeps) { d.AttachmentRepo = nil },
		"ApprovalRepo":       func(d *services.ClientAttachmentServiceDeps) { d.ApprovalRepo = nil },
		"ClientRepo":         func(d *services.ClientAttachmentServiceDeps) { d.ClientRepo = nil },
		"RouteRepo":          func(d *services.ClientAttachmentServiceDeps) { d.RouteRepo = nil },
		"DomainRepo":         func(d *services.ClientAttachmentServiceDeps) { d.DomainRepo = nil },
		"ProjectRepo":        func(d *services.ClientAttachmentServiceDeps) { d.ProjectRepo = nil },
		"DomainSettingsRepo": func(d *services.ClientAttachmentServiceDeps) { d.DomainSettingsRepo = nil },
		"Approvals":          func(d *services.ClientAttachmentServiceDeps) { d.Approvals = nil },
	}
	for name, breakIt := range cases {
		t.Run("nil "+name, func(t *testing.T) {
			d := fullClientAttachmentServiceDeps()
			breakIt(&d)
			assert.PanicsWithValue(t,
				"services.NewClientAttachmentService: missing required dependency: "+name,
				func() { services.NewClientAttachmentService(d) })
		})
	}
}

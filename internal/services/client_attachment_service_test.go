package services_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ListByClientID
// ---------------------------------------------------------------------------

func TestClientAttachmentService_ListByClientID_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, teamRepo, nil)

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
	svc := services.NewClientAttachmentService(nil, nil, nil, clientRepo, nil, nil, nil, nil)

	clientRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(nil, errors.New("not found"))

	input := &services.AttachFromRouteInput{ClientID: uuid.New(), EnableAPIKey: true}
	_, err := svc.AttachFromRoute(uuid.New(), input, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
}

func TestClientAttachmentService_AttachFromRoute_RouteNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, clientRepo, routeRepo, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, teamRepo, nil)

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
	svc := services.NewClientAttachmentService(nil, nil, nil, clientRepo, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, clientRepo, routeRepo, domainRepo, nil, nil)

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
	svc := services.NewClientAttachmentService(nil, nil, nil, clientRepo, routeRepo, domainRepo, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, policyRepo, nil, routeRepo, domainRepo, teamRepo, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, routeRepo, nil, teamRepo, projectRepo)

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

	// OnApprovalComplete
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
	svc := services.NewClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, teamRepo, projectRepo)

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
	svc := services.NewClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, teamRepo, projectRepo)

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
	svc := services.NewClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

	userID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
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
	svc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, teamRepo, projectRepo)

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
	svc := services.NewClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

	approvalID := uuid.New()
	expected := &models.Approval{ID: approvalID, Status: models.ApprovalStatusPending}
	approvalRepo.On("GetByID", approvalID).Return(expected, nil)

	result, err := svc.GetApproval(approvalID)

	require.NoError(t, err)
	assert.Equal(t, approvalID, result.ID)
}

func TestClientAttachmentService_GetApproval_NotFound(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

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
	svc := services.NewClientAttachmentService(nil, approvalRepo, nil, nil, nil, nil, nil, nil)

	projectID := uuid.New()
	approvalRepo.On("ListByProjectID", projectID, 1, 10, "", "client_attachment").Return([]models.Approval{}, int64(0), nil)

	result, total, err := svc.ListApprovalsByProjectID(projectID, 1, 10, "")

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, int64(0), total)
}

// ---------------------------------------------------------------------------
// OnApprovalComplete
// ---------------------------------------------------------------------------

func TestClientAttachmentService_OnApprovalComplete_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, routeRepo, nil, nil, nil)

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

	err := svc.OnApprovalComplete(approval)

	require.NoError(t, err)
	attachmentRepo.AssertExpectations(t)
}

func TestClientAttachmentService_OnApprovalComplete_RouteNotActive(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, routeRepo, nil, nil, nil)

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

	err := svc.OnApprovalComplete(approval)

	require.NoError(t, err)
}

func TestClientAttachmentService_OnApprovalComplete_Detach(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, routeRepo, nil, nil, nil)

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

	err := svc.OnApprovalComplete(approval)

	require.NoError(t, err)
	attachmentRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// OnApprovalRejected
// ---------------------------------------------------------------------------

func TestClientAttachmentService_OnApprovalRejected_Success(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := services.NewClientAttachmentService(attachmentRepo, nil, nil, nil, nil, nil, nil, nil)

	attachmentID := uuid.New()
	attachment := &models.ClientRouteAttachment{ID: attachmentID, Status: models.AttachmentStatusPendingAttach}
	approval := &models.Approval{EntityID: attachmentID}

	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)
	attachmentRepo.On("Update", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	err := svc.OnApprovalRejected(approval)

	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// ListByRouteID with pending approvals
// ---------------------------------------------------------------------------

func TestClientAttachmentService_ListByRouteID_WithPendingApprovals(t *testing.T) {
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)

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

// ---------------------------------------------------------------------------
// SetDomainSettingsRepository
// ---------------------------------------------------------------------------

func TestClientAttachmentService_SetDomainSettingsRepository(t *testing.T) {
	svc := services.NewClientAttachmentService(nil, nil, nil, nil, nil, nil, nil, nil)

	mockDSRepo := new(mocks.MockDomainSettingsRepository)
	// Should not panic
	svc.SetDomainSettingsRepository(mockDSRepo)
}

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
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	clientRepo := new(mocks.MockClientRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)

	svc := services.NewClientAttachmentService(
		attachmentRepo, approvalRepo, policyRepo,
		clientRepo, routeRepo, domainRepo, teamRepo, projectRepo,
	)

	// Default: approvals enabled (bypass check returns project with ApprovalEnabled=true)
	projectRepo.On("GetByID", mock.Anything).Return(&models.Project{ApprovalEnabled: true}, nil).Maybe()

	return svc, attachmentRepo, approvalRepo, policyRepo, clientRepo, routeRepo, domainRepo, teamRepo, projectRepo
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

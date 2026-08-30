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
// GetByID
// ---------------------------------------------------------------------------

func TestApprovalService_GetByID_Success(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	id := uuid.New()
	expected := &models.Approval{ID: id, Status: models.ApprovalStatusPending}
	approvalRepo.On("GetByID", id).Return(expected, nil)

	result, err := svc.GetByID(id)

	require.NoError(t, err)
	assert.Equal(t, id, result.ID)
	assert.Equal(t, models.ApprovalStatusPending, result.Status)
	approvalRepo.AssertExpectations(t)
}

func TestApprovalService_GetByID_NotFound(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	id := uuid.New()
	approvalRepo.On("GetByID", id).Return(nil, errors.New("not found"))

	_, err := svc.GetByID(id)

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ListByProjectID
// ---------------------------------------------------------------------------

func TestApprovalService_ListByProjectID_NoRoutes(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, routeRepo, nil, domainRepo, nil, services.WAFConfig{})

	projectID := uuid.New()
	approvals := []models.Approval{
		{ID: uuid.New(), EntityType: models.ApprovalEntityClientAttachment, Status: models.ApprovalStatusPending},
	}
	approvalRepo.On("ListByProjectID", projectID, 1, 10, "pending", "").Return(approvals, int64(1), nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 10, "pending", "")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, int64(1), total)
	approvalRepo.AssertExpectations(t)
}

func TestApprovalService_ListByProjectID_WithRouteEnrichment(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, routeRepo, nil, domainRepo, nil, services.WAFConfig{})

	projectID := uuid.New()
	routeID := uuid.New()
	domainID := uuid.New()

	approvals := []models.Approval{
		{ID: uuid.New(), EntityType: models.ApprovalEntityRoute, EntityID: routeID, Status: models.ApprovalStatusPending},
	}
	approvalRepo.On("ListByProjectID", projectID, 1, 10, "", "").Return(approvals, int64(1), nil)
	routeRepo.On("GetByIDs", mock.AnythingOfType("[]uuid.UUID")).Return([]models.Route{
		{ID: routeID, Name: "my-route", DomainID: domainID},
	}, nil)
	domainRepo.On("GetByIDs", mock.AnythingOfType("[]uuid.UUID")).Return([]models.Domain{
		{ID: domainID, Hostname: "example.com"},
	}, nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 10, "", "")

	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "my-route", result[0].EntityName)
	assert.Equal(t, "example.com", result[0].DomainName)
}

func TestApprovalService_ListByProjectID_RepoError(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	projectID := uuid.New()
	approvalRepo.On("ListByProjectID", projectID, 1, 10, "", "").Return([]models.Approval(nil), int64(0), errors.New("db error"))

	_, _, err := svc.ListByProjectID(projectID, 1, 10, "", "")

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// CountPendingByProjectID
// ---------------------------------------------------------------------------

func TestApprovalService_CountPendingByProjectID(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	projectID := uuid.New()
	approvalRepo.On("CountPendingByProjectID", projectID).Return(int64(5), nil)

	count, err := svc.CountPendingByProjectID(projectID)

	require.NoError(t, err)
	assert.Equal(t, int64(5), count)
	approvalRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// ListPolicies
// ---------------------------------------------------------------------------

func TestApprovalService_ListPolicies(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalService(nil, policyRepo, nil, nil, nil, nil, nil, services.WAFConfig{})

	projectID := uuid.New()
	policies := []models.ApprovalPolicy{
		{ID: uuid.New(), ProjectID: projectID},
	}
	policyRepo.On("ListByProjectID", projectID).Return(policies, nil)

	result, err := svc.ListPolicies(projectID)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	policyRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// UpsertPolicy
// ---------------------------------------------------------------------------

func TestApprovalService_UpsertPolicy(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalService(nil, policyRepo, nil, nil, nil, nil, nil, services.WAFConfig{})

	policy := &models.ApprovalPolicy{
		ID:        uuid.New(),
		ProjectID: uuid.New(),
	}
	policyRepo.On("Upsert", policy).Return(nil)

	err := svc.UpsertPolicy(policy)

	require.NoError(t, err)
	policyRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// ApproveStage
// ---------------------------------------------------------------------------

func TestApprovalService_ApproveStage_SingleStage_Success(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	stageID := uuid.New()
	projectID := uuid.New()
	entityID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    entityID,
		Action:      models.ApprovalActionCreate,
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{
				ID:                 stageID,
				ApprovalID:         approvalID,
				StageOrder:         1,
				RequiredPermission: "route.approve",
				Status:             models.ApprovalStatusPending,
			},
		},
	}

	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleOwner}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	approvalRepo.On("UpdateStage", mock.AnythingOfType("*models.ApprovalStage")).Return(nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)
	// onApprovalComplete -> onRouteApprovalComplete
	routeRepo.On("GetByID", entityID).Return(&models.Route{
		ID:     entityID,
		Status: models.RouteStatusPendingCreate,
	}, nil)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)

	result, err := svc.ApproveStage(approvalID, stageID, reviewer)

	require.NoError(t, err)
	assert.NotNil(t, result)
	approvalRepo.AssertExpectations(t)
}

func TestApprovalService_ApproveStage_MultiStage_Success(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, nil, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	stage1ID := uuid.New()
	stage2ID := uuid.New()
	projectID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()

	// Approve stage 1 of 2 - should NOT trigger onApprovalComplete
	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    uuid.New(),
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stage1ID, StageOrder: 1, RequiredPermission: "route.approve", Status: models.ApprovalStatusPending},
			{ID: stage2ID, StageOrder: 2, RequiredPermission: "route.approve", Status: models.ApprovalStatusPending},
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

func TestApprovalService_ApproveStage_SubmitterCannotApprove(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

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
	reviewer := &models.User{ID: userID, Role: models.UserRoleUser}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil)

	_, err := svc.ApproveStage(approvalID, stageID, reviewer)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "submitter cannot approve")
}

func TestApprovalService_ApproveStage_NotPending(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

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

func TestApprovalService_ApproveStage_StageNotFound(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()
	approval := &models.Approval{
		ID:          approvalID,
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages:      []models.ApprovalStage{},
	}
	approvalRepo.On("GetByID", approvalID).Return(approval, nil)

	_, err := svc.ApproveStage(approvalID, uuid.New(), &models.User{ID: reviewerID})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stage not found")
}

func TestApprovalService_ApproveStage_WrongPermission(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, nil, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	stageID := uuid.New()
	projectID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, RequiredPermission: "route.approve", Status: models.ApprovalStatusPending},
		},
	}
	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleUser}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil)
	projectRepo.On("IsAdmin", projectID, reviewerID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, reviewerID, models.Permission("route.approve")).Return(false, nil)

	_, err := svc.ApproveStage(approvalID, stageID, reviewer)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "required permission")
}

// ---------------------------------------------------------------------------
// ApproveStage - onApprovalComplete for client_attachment entity type
// ---------------------------------------------------------------------------

func TestApprovalService_ApproveStage_ClientAttachment_Complete(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)
	routeRepo := new(mocks.MockRouteRepository)
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	// Create a client attachment service and wire it in
	casSvc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, routeRepo, nil, nil, nil)
	svc.SetClientAttachmentService(casSvc)

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
		Action:      models.ApprovalActionAttach,
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

	// OnApprovalComplete for client attachment
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

// ---------------------------------------------------------------------------
// RejectStage
// ---------------------------------------------------------------------------

func TestApprovalService_RejectStage_Success(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	stageID := uuid.New()
	projectID := uuid.New()
	entityID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    entityID,
		Action:      models.ApprovalActionCreate,
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, RequiredPermission: "route.approve", Status: models.ApprovalStatusPending},
		},
	}
	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleOwner}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	approvalRepo.On("UpdateStage", mock.AnythingOfType("*models.ApprovalStage")).Return(nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)
	// onApprovalRejected -> onRouteApprovalRejected
	routeRepo.On("GetByID", entityID).Return(&models.Route{ID: entityID, Status: models.RouteStatusPendingCreate}, nil)
	routeRepo.On("Update", mock.AnythingOfType("*models.Route")).Return(nil)

	result, err := svc.RejectStage(approvalID, stageID, reviewer, "not good")

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestApprovalService_RejectStage_EmptyComment(t *testing.T) {
	svc := services.NewApprovalService(nil, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	_, err := svc.RejectStage(uuid.New(), uuid.New(), &models.User{ID: uuid.New()}, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejection comment is required")
}

func TestApprovalService_RejectStage_AlreadyRejected(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

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
// RejectStage - onApprovalRejected for client_attachment entity type
// ---------------------------------------------------------------------------

func TestApprovalService_RejectStage_ClientAttachment(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	casSvc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)
	svc.SetClientAttachmentService(casSvc)

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
		Action:      models.ApprovalActionAttach,
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

	// onApprovalRejected -> clientAttachmentService.OnApprovalRejected
	attachment := &models.ClientRouteAttachment{ID: attachmentID, Status: models.AttachmentStatusPendingAttach}
	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)
	attachmentRepo.On("Update", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	result, err := svc.RejectStage(approvalID, stageID, reviewer, "rejected for reason")

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// onRouteApprovalRejected - update action restores active status
// ---------------------------------------------------------------------------

func TestApprovalService_RejectStage_Route_UpdateAction(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	stageID := uuid.New()
	projectID := uuid.New()
	entityID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    entityID,
		Action:      models.ApprovalActionUpdate,
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, RequiredPermission: "route.approve", Status: models.ApprovalStatusPending},
		},
	}
	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleOwner}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	approvalRepo.On("UpdateStage", mock.AnythingOfType("*models.ApprovalStage")).Return(nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)
	routeRepo.On("GetByID", entityID).Return(&models.Route{ID: entityID, Status: models.RouteStatusPendingDeploy}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.Status == models.RouteStatusActive
	})).Return(nil)

	result, err := svc.RejectStage(approvalID, stageID, reviewer, "reverting")

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// onRouteApprovalComplete - update action sets pending_deploy
// ---------------------------------------------------------------------------

func TestApprovalService_ApproveStage_Route_UpdateAction(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	stageID := uuid.New()
	projectID := uuid.New()
	entityID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()

	routeConfig := models.RouteConfig{
		Matches: []models.RouteMatch{{Path: &models.PathMatch{Value: "/updated"}}},
	}
	snapshot := models.RouteApprovalSnapshot{RouteConfig: &routeConfig}
	snapshotJSON, _ := json.Marshal(snapshot)

	approval := &models.Approval{
		ID:             approvalID,
		ProjectID:      projectID,
		EntityType:     models.ApprovalEntityRoute,
		EntityID:       entityID,
		Action:         models.ApprovalActionUpdate,
		ConfigSnapshot: snapshotJSON,
		SubmittedBy:    submitterID,
		Status:         models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, RequiredPermission: "route.approve", Status: models.ApprovalStatusPending},
		},
	}
	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleOwner}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	approvalRepo.On("UpdateStage", mock.AnythingOfType("*models.ApprovalStage")).Return(nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)
	routeRepo.On("GetByID", entityID).Return(&models.Route{ID: entityID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.Status == models.RouteStatusPendingDeploy
	})).Return(nil)

	result, err := svc.ApproveStage(approvalID, stageID, reviewer)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// onRouteApprovalComplete - delete action sets pending_deploy
// ---------------------------------------------------------------------------

func TestApprovalService_ApproveStage_Route_DeleteAction(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectRepo := new(mocks.MockProjectRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	stageID := uuid.New()
	projectID := uuid.New()
	entityID := uuid.New()
	submitterID := uuid.New()
	reviewerID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    entityID,
		Action:      models.ApprovalActionDelete,
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, RequiredPermission: "route.approve", Status: models.ApprovalStatusPending},
		},
	}
	reviewer := &models.User{ID: reviewerID, Role: models.UserRoleOwner}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	approvalRepo.On("UpdateStage", mock.AnythingOfType("*models.ApprovalStage")).Return(nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)
	routeRepo.On("GetByID", entityID).Return(&models.Route{ID: entityID, Status: models.RouteStatusActive}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.Status == models.RouteStatusPendingDeploy
	})).Return(nil)

	result, err := svc.ApproveStage(approvalID, stageID, reviewer)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// CancelApproval
// ---------------------------------------------------------------------------

func TestApprovalService_CancelApproval_Success_BySubmitter(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	routeRepo := new(mocks.MockRouteRepository)
	projectRepo := new(mocks.MockProjectRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	userID := uuid.New()
	entityID := uuid.New()
	projectID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    entityID,
		Action:      models.ApprovalActionCreate,
		SubmittedBy: userID,
		Status:      models.ApprovalStatusPending,
	}
	user := &models.User{ID: userID, Role: models.UserRoleUser}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	projectRepo.On("IsAdmin", projectID, userID).Return(false, nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)
	// onApprovalCancelled -> onRouteApprovalCancelled (create -> delete route)
	routeRepo.On("Delete", entityID).Return(nil)

	result, err := svc.CancelApproval(approvalID, user)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestApprovalService_CancelApproval_NotPending(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	approval := &models.Approval{
		ID:     approvalID,
		Status: models.ApprovalStatusRejected,
	}
	approvalRepo.On("GetByID", approvalID).Return(approval, nil)

	_, err := svc.CancelApproval(approvalID, &models.User{ID: uuid.New()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be cancelled")
}

func TestApprovalService_CancelApproval_NoPermission(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	projectRepo := new(mocks.MockProjectRepository)
	teamRepo := new(mocks.MockTeamRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewApprovalService(approvalRepo, nil, teamRepo, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	submitterID := uuid.New()
	otherUserID := uuid.New()
	projectID := uuid.New()
	entityID := uuid.New()
	teamID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    entityID,
		SubmittedBy: submitterID,
		Status:      models.ApprovalStatusPending,
	}
	user := &models.User{ID: otherUserID, Role: models.UserRoleUser}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil)
	projectRepo.On("IsAdmin", projectID, otherUserID).Return(false, nil)
	routeRepo.On("GetByID", entityID).Return(&models.Route{ID: entityID, TeamID: teamID}, nil)
	teamRepo.On("IsMember", teamID, otherUserID).Return(false, nil)

	_, err := svc.CancelApproval(approvalID, user)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "do not have permission")
}

// ---------------------------------------------------------------------------
// CancelApproval - onApprovalCancelled for update action (revert to active)
// ---------------------------------------------------------------------------

func TestApprovalService_CancelApproval_UpdateAction_RevertsToActive(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	routeRepo := new(mocks.MockRouteRepository)
	projectRepo := new(mocks.MockProjectRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, routeRepo, projectRepo, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	userID := uuid.New()
	entityID := uuid.New()
	projectID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    entityID,
		Action:      models.ApprovalActionUpdate,
		SubmittedBy: userID,
		Status:      models.ApprovalStatusPending,
	}
	user := &models.User{ID: userID, Role: models.UserRoleUser}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	projectRepo.On("IsAdmin", projectID, userID).Return(false, nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)
	routeRepo.On("GetByID", entityID).Return(&models.Route{ID: entityID, Status: models.RouteStatusPendingDeploy}, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.Status == models.RouteStatusActive
	})).Return(nil)

	result, err := svc.CancelApproval(approvalID, user)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// CancelApproval - onApprovalCancelled for client_attachment entity type
// ---------------------------------------------------------------------------

func TestApprovalService_CancelApproval_ClientAttachment(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	projectRepo := new(mocks.MockProjectRepository)
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, projectRepo, nil, nil, services.WAFConfig{})

	casSvc := services.NewClientAttachmentService(attachmentRepo, approvalRepo, nil, nil, nil, nil, nil, nil)
	svc.SetClientAttachmentService(casSvc)

	approvalID := uuid.New()
	userID := uuid.New()
	attachmentID := uuid.New()
	projectID := uuid.New()

	approval := &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    attachmentID,
		Action:      models.ApprovalActionAttach,
		SubmittedBy: userID,
		Status:      models.ApprovalStatusPending,
	}
	user := &models.User{ID: userID, Role: models.UserRoleUser}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil).Times(2)
	projectRepo.On("IsAdmin", projectID, userID).Return(false, nil)
	approvalRepo.On("Update", mock.AnythingOfType("*models.Approval")).Return(nil)

	// onApprovalCancelled -> clientAttachmentService.OnApprovalRejected
	attachment := &models.ClientRouteAttachment{ID: attachmentID, Status: models.AttachmentStatusPendingAttach}
	attachmentRepo.On("GetByID", attachmentID).Return(attachment, nil)
	attachmentRepo.On("Update", mock.AnythingOfType("*models.ClientRouteAttachment")).Return(nil)

	result, err := svc.CancelApproval(approvalID, user)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// GetDiff
// ---------------------------------------------------------------------------

func TestApprovalService_GetDiff_CreateAction(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, routeRepo, nil, domainRepo, nil, services.WAFConfig{})

	approvalID := uuid.New()
	entityID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	routeConfig := models.RouteConfig{
		Matches: []models.RouteMatch{{Path: &models.PathMatch{Value: "/test"}}},
	}
	snapshot := models.RouteApprovalSnapshot{RouteConfig: &routeConfig}
	snapshotJSON, _ := json.Marshal(snapshot)

	approval := &models.Approval{
		ID:             approvalID,
		EntityType:     models.ApprovalEntityRoute,
		EntityID:       entityID,
		Action:         models.ApprovalActionCreate,
		ConfigSnapshot: snapshotJSON,
		Status:         models.ApprovalStatusPending,
	}

	route := &models.Route{
		ID:           entityID,
		DomainID:     domainID,
		Name:         "test-route",
		K8sRouteName: "test-route",
	}

	domain := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Hostname:  "example.com",
		Namespace: "fastgateway-system",
	}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil)
	routeRepo.On("GetByID", entityID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	result, err := svc.GetDiff(approvalID)

	require.NoError(t, err)
	assert.Equal(t, "create", result.Action)
	assert.NotEmpty(t, result.ProposedYAML)
	assert.Empty(t, result.CurrentYAML)
}

func TestApprovalService_GetDiff_UpdateAction(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, routeRepo, nil, domainRepo, nil, services.WAFConfig{})

	approvalID := uuid.New()
	entityID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	currentConfig := models.RouteConfig{
		Matches: []models.RouteMatch{{Path: &models.PathMatch{Value: "/old"}}},
	}
	proposedConfig := models.RouteConfig{
		Matches: []models.RouteMatch{{Path: &models.PathMatch{Value: "/new"}}},
	}
	previousSnapshot := models.RouteApprovalSnapshot{RouteConfig: &currentConfig}
	proposedSnapshot := models.RouteApprovalSnapshot{RouteConfig: &proposedConfig}
	previousJSON, _ := json.Marshal(previousSnapshot)
	proposedJSON, _ := json.Marshal(proposedSnapshot)

	approval := &models.Approval{
		ID:             approvalID,
		EntityType:     models.ApprovalEntityRoute,
		EntityID:       entityID,
		Action:         models.ApprovalActionUpdate,
		ConfigSnapshot: proposedJSON,
		PreviousConfig: previousJSON,
		Status:         models.ApprovalStatusPending,
	}

	route := &models.Route{
		ID:           entityID,
		DomainID:     domainID,
		Name:         "test-route",
		K8sRouteName: "test-route",
	}

	domain := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Hostname:  "example.com",
		Namespace: "fastgateway-system",
	}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil)
	routeRepo.On("GetByID", entityID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	result, err := svc.GetDiff(approvalID)

	require.NoError(t, err)
	assert.Equal(t, "update", result.Action)
	assert.NotEmpty(t, result.CurrentYAML)
	assert.NotEmpty(t, result.ProposedYAML)
}

func TestApprovalService_GetDiff_NonRouteEntity(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	approval := &models.Approval{
		ID:         approvalID,
		EntityType: models.ApprovalEntityClientAttachment,
		Status:     models.ApprovalStatusPending,
	}
	approvalRepo.On("GetByID", approvalID).Return(approval, nil)

	_, err := svc.GetDiff(approvalID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "only available for route approvals")
}

func TestApprovalService_GetDiff_DeleteAction(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	routeRepo := new(mocks.MockRouteRepository)
	domainRepo := new(mocks.MockDomainRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, routeRepo, nil, domainRepo, nil, services.WAFConfig{})

	approvalID := uuid.New()
	entityID := uuid.New()
	domainID := uuid.New()
	projectID := uuid.New()

	approval := &models.Approval{
		ID:         approvalID,
		EntityType: models.ApprovalEntityRoute,
		EntityID:   entityID,
		Action:     models.ApprovalActionDelete,
		Status:     models.ApprovalStatusPending,
	}

	route := &models.Route{
		ID:           entityID,
		DomainID:     domainID,
		Name:         "test-route",
		K8sRouteName: "test-route",
	}

	domain := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Hostname:  "example.com",
		Namespace: "fastgateway-system",
	}

	approvalRepo.On("GetByID", approvalID).Return(approval, nil)
	routeRepo.On("GetByID", entityID).Return(route, nil)
	domainRepo.On("GetByID", domainID).Return(domain, nil)

	result, err := svc.GetDiff(approvalID)

	require.NoError(t, err)
	assert.Equal(t, "delete", result.Action)
	assert.NotEmpty(t, result.CurrentYAML)
	assert.Empty(t, result.ProposedYAML)
}

// ---------------------------------------------------------------------------
// UpdateAIReview
// ---------------------------------------------------------------------------

func TestApprovalService_UpdateAIReview_Success(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	aiReview := json.RawMessage(`{"summary":"looks good"}`)
	approval := &models.Approval{
		ID:       approvalID,
		AIReview: aiReview,
	}

	approvalRepo.On("SetAIReview", approvalID, aiReview).Return(nil)

	err := svc.UpdateAIReview(approval)

	require.NoError(t, err)
	approvalRepo.AssertExpectations(t)
}

func TestApprovalService_UpdateAIReview_Error(t *testing.T) {
	approvalRepo := new(mocks.MockUnifiedApprovalRepository)
	svc := services.NewApprovalService(approvalRepo, nil, nil, nil, nil, nil, nil, services.WAFConfig{})

	approvalID := uuid.New()
	approval := &models.Approval{ID: approvalID}

	approvalRepo.On("SetAIReview", approvalID, json.RawMessage(nil)).Return(errors.New("db error"))

	err := svc.UpdateAIReview(approval)

	require.Error(t, err)
}

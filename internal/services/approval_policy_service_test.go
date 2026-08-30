package services_test

import (
	"encoding/json"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestApprovalPolicyService_List(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	projectID := uuid.New()
	expected := []models.ApprovalPolicy{
		{ID: uuid.New(), ProjectID: projectID, EntityType: models.ApprovalEntityRoute},
	}
	policyRepo.On("ListByProjectID", projectID).Return(expected, nil)

	result, err := svc.List(projectID)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	policyRepo.AssertExpectations(t)
}

func TestApprovalPolicyService_Get(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	policyID := uuid.New()
	projectID := uuid.New()
	expected := &models.ApprovalPolicy{ID: policyID, ProjectID: projectID}
	policyRepo.On("GetByID", policyID).Return(expected, nil)

	result, err := svc.Get(projectID, policyID)

	require.NoError(t, err)
	assert.Equal(t, policyID, result.ID)
	policyRepo.AssertExpectations(t)
}

func TestApprovalPolicyService_Get_WrongProject(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	policyID := uuid.New()
	projectID := uuid.New()
	otherProjectID := uuid.New()
	expected := &models.ApprovalPolicy{ID: policyID, ProjectID: otherProjectID}
	policyRepo.On("GetByID", policyID).Return(expected, nil)

	_, err := svc.Get(projectID, policyID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestApprovalPolicyService_Create_Valid(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	projectID := uuid.New()
	input := services.ApprovalPolicyInput{
		EntityType: "route",
		Stages: []services.PolicyStageInput{
			{Order: 1, RequiredPermission: "route.approve", TeamScope: "any", MinApprovers: 1},
		},
	}

	policyRepo.On("GetByProjectAndEntity", projectID, "route", (*string)(nil)).Return(nil, assert.AnError)
	policyRepo.On("Create", mock.AnythingOfType("*models.ApprovalPolicy")).Return(nil)

	result, err := svc.Create(projectID, input)

	require.NoError(t, err)
	assert.Equal(t, projectID, result.ProjectID)
	policyRepo.AssertExpectations(t)
}

func TestApprovalPolicyService_Create_DuplicateEntityType(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	projectID := uuid.New()
	input := services.ApprovalPolicyInput{
		EntityType: "route",
		Stages: []services.PolicyStageInput{
			{Order: 1, RequiredPermission: "route.approve", TeamScope: "any", MinApprovers: 1},
		},
	}

	existing := &models.ApprovalPolicy{ID: uuid.New(), ProjectID: projectID}
	policyRepo.On("GetByProjectAndEntity", projectID, "route", (*string)(nil)).Return(existing, nil)

	_, err := svc.Create(projectID, input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestApprovalPolicyService_Create_InvalidPermission(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	projectID := uuid.New()
	input := services.ApprovalPolicyInput{
		EntityType: "route",
		Stages: []services.PolicyStageInput{
			{Order: 1, RequiredPermission: "invalid.perm", TeamScope: "any", MinApprovers: 1},
		},
	}

	_, err := svc.Create(projectID, input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid permission")
}

func TestApprovalPolicyService_Create_InvalidTeamScope(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	projectID := uuid.New()
	input := services.ApprovalPolicyInput{
		EntityType: "route",
		Stages: []services.PolicyStageInput{
			{Order: 1, RequiredPermission: "route.approve", TeamScope: "invalid_scope", MinApprovers: 1},
		},
	}

	_, err := svc.Create(projectID, input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid team scope")
}

func TestApprovalPolicyService_Create_NoStages(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	projectID := uuid.New()
	input := services.ApprovalPolicyInput{
		EntityType: "route",
		Stages:     []services.PolicyStageInput{},
	}

	_, err := svc.Create(projectID, input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least 1 stage")
}

func TestApprovalPolicyService_Update(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	policyID := uuid.New()
	projectID := uuid.New()
	existing := &models.ApprovalPolicy{
		ID:         policyID,
		ProjectID:  projectID,
		EntityType: models.ApprovalEntityRoute,
	}
	policyRepo.On("GetByID", policyID).Return(existing, nil)

	input := services.ApprovalPolicyInput{
		EntityType: "route",
		Stages: []services.PolicyStageInput{
			{Order: 1, RequiredPermission: "route.approve", TeamScope: "other_team", MinApprovers: 2},
		},
	}

	policyRepo.On("Update", mock.AnythingOfType("*models.ApprovalPolicy")).Return(nil)

	result, err := svc.Update(projectID, policyID, input)

	require.NoError(t, err)
	assert.Equal(t, policyID, result.ID)

	// Verify stages were updated
	var stages []models.PolicyStageTemplate
	json.Unmarshal(result.Stages, &stages)
	assert.Len(t, stages, 1)
	assert.Equal(t, "other_team", stages[0].TeamScope)
	assert.Equal(t, 2, stages[0].MinApprovers)
	policyRepo.AssertExpectations(t)
}

func TestApprovalPolicyService_Delete(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	policyID := uuid.New()
	projectID := uuid.New()
	existing := &models.ApprovalPolicy{ID: policyID, ProjectID: projectID}
	policyRepo.On("GetByID", policyID).Return(existing, nil)
	policyRepo.On("Delete", policyID).Return(nil)

	err := svc.Delete(projectID, policyID)

	require.NoError(t, err)
	policyRepo.AssertExpectations(t)
}

func TestApprovalPolicyService_MinApproversDefaultsTo1(t *testing.T) {
	policyRepo := new(mocks.MockApprovalPolicyRepository)
	svc := services.NewApprovalPolicyService(policyRepo)

	projectID := uuid.New()
	input := services.ApprovalPolicyInput{
		EntityType: "route",
		Stages: []services.PolicyStageInput{
			{Order: 1, RequiredPermission: "route.approve", TeamScope: "any", MinApprovers: 0},
		},
	}

	policyRepo.On("GetByProjectAndEntity", projectID, "route", (*string)(nil)).Return(nil, assert.AnError)
	policyRepo.On("Create", mock.AnythingOfType("*models.ApprovalPolicy")).Return(nil)

	result, err := svc.Create(projectID, input)

	require.NoError(t, err)
	var stages []models.PolicyStageTemplate
	json.Unmarshal(result.Stages, &stages)
	assert.Equal(t, 1, stages[0].MinApprovers)
}

package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPresetService_Create_Success(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	input := &services.CreatePresetInput{
		Name:        "my-preset",
		Description: "A custom preset",
		Permissions: []string{"route.view", "route.create"},
	}

	mockRepo.On("GetByProjectAndName", projectID, "my-preset").Return(nil, errors.New("not found"))
	mockRepo.On("Create", mock.AnythingOfType("*models.PermissionPreset")).Return(nil)

	preset, err := svc.Create(projectID, input)

	require.NoError(t, err)
	assert.Equal(t, "my-preset", preset.Name)
	assert.Equal(t, projectID, preset.ProjectID)
	assert.False(t, preset.IsBuiltin)
	mockRepo.AssertExpectations(t)
}

func TestPresetService_Create_DuplicateName(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	existing := &models.PermissionPreset{Name: "my-preset"}
	mockRepo.On("GetByProjectAndName", projectID, "my-preset").Return(existing, nil)

	input := &services.CreatePresetInput{
		Name:        "my-preset",
		Permissions: []string{"route.view"},
	}

	preset, err := svc.Create(projectID, input)

	assert.Nil(t, preset)
	assert.EqualError(t, err, "a preset with this name already exists")
	mockRepo.AssertExpectations(t)
}

func TestPresetService_Create_InvalidPermission(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	input := &services.CreatePresetInput{
		Name:        "bad-preset",
		Permissions: []string{"invalid.permission"},
	}

	preset, err := svc.Create(projectID, input)

	assert.Nil(t, preset)
	assert.Contains(t, err.Error(), "invalid permission")
}

func TestPresetService_GetByID(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	id := uuid.New()
	expected := &models.PermissionPreset{ID: id, Name: "test"}
	mockRepo.On("GetByID", id).Return(expected, nil)

	result, err := svc.GetByID(id)

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	mockRepo.AssertExpectations(t)
}

func TestPresetService_ListByProject(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	presets := []models.PermissionPreset{
		{ID: uuid.New(), Name: "preset1"},
		{ID: uuid.New(), Name: "preset2"},
	}
	mockRepo.On("ListByProject", projectID).Return(presets, nil)

	result, err := svc.ListByProject(projectID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	mockRepo.AssertExpectations(t)
}

func TestPresetService_Update_Success(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	presetID := uuid.New()
	existing := &models.PermissionPreset{
		ID:          presetID,
		ProjectID:   projectID,
		Name:        "old-name",
		Permissions: pq.StringArray{"route.view"},
		IsBuiltin:   false,
	}

	mockRepo.On("GetByID", presetID).Return(existing, nil)
	mockRepo.On("GetByProjectAndName", projectID, "new-name").Return(nil, errors.New("not found"))
	mockRepo.On("Update", mock.AnythingOfType("*models.PermissionPreset")).Return(nil)

	input := &services.UpdatePresetInput{Name: "new-name"}
	result, err := svc.Update(projectID, presetID, input)

	require.NoError(t, err)
	assert.Equal(t, "new-name", result.Name)
	mockRepo.AssertExpectations(t)
}

func TestPresetService_Update_BuiltinRestriction(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	presetID := uuid.New()
	existing := &models.PermissionPreset{
		ID:        presetID,
		ProjectID: projectID,
		Name:      "viewer",
		IsBuiltin: true,
	}

	mockRepo.On("GetByID", presetID).Return(existing, nil)

	input := &services.UpdatePresetInput{Name: "renamed"}
	result, err := svc.Update(projectID, presetID, input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "cannot modify name or permissions of built-in presets")
	mockRepo.AssertExpectations(t)
}

func TestPresetService_Delete_Success(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	presetID := uuid.New()
	existing := &models.PermissionPreset{
		ID:        presetID,
		ProjectID: projectID,
		IsBuiltin: false,
	}

	mockRepo.On("GetByID", presetID).Return(existing, nil)
	mockRepo.On("IsPresetInUse", presetID).Return(false, nil)
	mockRepo.On("Delete", presetID).Return(nil)

	err := svc.Delete(projectID, presetID)

	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func TestPresetService_Delete_InUse(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	presetID := uuid.New()
	existing := &models.PermissionPreset{
		ID:        presetID,
		ProjectID: projectID,
		IsBuiltin: false,
	}

	mockRepo.On("GetByID", presetID).Return(existing, nil)
	mockRepo.On("IsPresetInUse", presetID).Return(true, nil)

	err := svc.Delete(projectID, presetID)

	assert.EqualError(t, err, "cannot delete preset that is assigned to teams")
	mockRepo.AssertExpectations(t)
}

func TestPresetService_Delete_Builtin(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	presetID := uuid.New()
	existing := &models.PermissionPreset{
		ID:        presetID,
		ProjectID: projectID,
		IsBuiltin: true,
	}

	mockRepo.On("GetByID", presetID).Return(existing, nil)

	err := svc.Delete(projectID, presetID)

	assert.EqualError(t, err, "cannot delete built-in presets")
	mockRepo.AssertExpectations(t)
}

func TestPresetService_SeedBuiltinPresets(t *testing.T) {
	mockRepo := new(mocks.MockPresetRepository)
	svc := services.NewPresetService(mockRepo)

	projectID := uuid.New()
	mockRepo.On("SeedBuiltinPresets", projectID).Return(nil)

	err := svc.SeedBuiltinPresets(projectID)

	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

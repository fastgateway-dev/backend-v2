package services_test

import (
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

func TestAuditService_LogAction_WithUser(t *testing.T) {
	mockRepo := new(mocks.MockAuditLogRepository)
	svc := services.NewAuditService(mockRepo)

	projectID := uuid.New()
	user := &models.User{ID: uuid.New(), Username: "admin"}
	resourceID := uuid.New()

	mockRepo.On("Create", mock.AnythingOfType("*models.AuditLog")).Return(nil)

	err := svc.LogAction(&projectID, user, "create", "route", &resourceID, "my-route", models.AuditDetails{"key": "value"}, "127.0.0.1", "test-agent")

	require.NoError(t, err)
	mockRepo.AssertExpectations(t)

	call := mockRepo.Calls[0]
	log := call.Arguments.Get(0).(*models.AuditLog)
	assert.Equal(t, &projectID, log.ProjectID)
	assert.Equal(t, &user.ID, log.UserID)
	assert.Equal(t, "admin", log.Username)
	assert.Equal(t, "create", log.Action)
	assert.Equal(t, "route", log.ResourceType)
}

func TestAuditService_LogAction_NilUser(t *testing.T) {
	mockRepo := new(mocks.MockAuditLogRepository)
	svc := services.NewAuditService(mockRepo)

	mockRepo.On("Create", mock.AnythingOfType("*models.AuditLog")).Return(nil)

	err := svc.LogAction(nil, nil, "cleanup", "system", nil, "", nil, "", "")

	require.NoError(t, err)

	call := mockRepo.Calls[0]
	log := call.Arguments.Get(0).(*models.AuditLog)
	assert.Nil(t, log.UserID)
	assert.Equal(t, "system", log.Username)
	mockRepo.AssertExpectations(t)
}

func TestAuditService_ListByProjectID(t *testing.T) {
	mockRepo := new(mocks.MockAuditLogRepository)
	svc := services.NewAuditService(mockRepo)

	projectID := uuid.New()
	logs := []models.AuditLog{
		{ID: uuid.New(), Action: "create"},
		{ID: uuid.New(), Action: "delete"},
	}
	mockRepo.On("ListByProjectID", projectID, 1, 10, "", "", (*uuid.UUID)(nil)).Return(logs, int64(2), nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 10, "", "", nil)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(2), total)
	mockRepo.AssertExpectations(t)
}

func TestAuditService_CleanupOlderThan(t *testing.T) {
	mockRepo := new(mocks.MockAuditLogRepository)
	svc := services.NewAuditService(mockRepo)

	projectID := uuid.New()
	mockRepo.On("DeleteOlderThan", projectID, 30).Return(int64(5), nil)

	deleted, err := svc.CleanupOlderThan(projectID, 30)

	require.NoError(t, err)
	assert.Equal(t, int64(5), deleted)
	mockRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// ExportByProjectID
// ---------------------------------------------------------------------------

func TestAuditService_ExportByProjectID_Success(t *testing.T) {
	mockRepo := new(mocks.MockAuditLogRepository)
	svc := services.NewAuditService(mockRepo)

	projectID := uuid.New()
	logs := []models.AuditLog{
		{ID: uuid.New(), Action: "create", ResourceType: "route"},
		{ID: uuid.New(), Action: "delete", ResourceType: "route"},
		{ID: uuid.New(), Action: "update", ResourceType: "domain"},
	}
	mockRepo.On("ExportByProjectID", projectID, "", "", (*uuid.UUID)(nil)).Return(logs, nil)

	result, err := svc.ExportByProjectID(projectID, "", "", nil)

	require.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Equal(t, "create", result[0].Action)
	mockRepo.AssertExpectations(t)
}

func TestAuditService_ExportByProjectID_WithFilters(t *testing.T) {
	mockRepo := new(mocks.MockAuditLogRepository)
	svc := services.NewAuditService(mockRepo)

	projectID := uuid.New()
	userID := uuid.New()
	logs := []models.AuditLog{
		{ID: uuid.New(), Action: "create", ResourceType: "route"},
	}
	mockRepo.On("ExportByProjectID", projectID, "route", "create", &userID).Return(logs, nil)

	result, err := svc.ExportByProjectID(projectID, "route", "create", &userID)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	mockRepo.AssertExpectations(t)
}

func TestAuditService_ExportByProjectID_Empty(t *testing.T) {
	mockRepo := new(mocks.MockAuditLogRepository)
	svc := services.NewAuditService(mockRepo)

	projectID := uuid.New()
	mockRepo.On("ExportByProjectID", projectID, "", "", (*uuid.UUID)(nil)).Return([]models.AuditLog{}, nil)

	result, err := svc.ExportByProjectID(projectID, "", "", nil)

	require.NoError(t, err)
	assert.Empty(t, result)
	mockRepo.AssertExpectations(t)
}

func TestAuditService_ExportByProjectID_Error(t *testing.T) {
	mockRepo := new(mocks.MockAuditLogRepository)
	svc := services.NewAuditService(mockRepo)

	projectID := uuid.New()
	mockRepo.On("ExportByProjectID", projectID, "", "", (*uuid.UUID)(nil)).Return(nil, errors.New("db error"))

	result, err := svc.ExportByProjectID(projectID, "", "", nil)

	assert.Nil(t, result)
	assert.EqualError(t, err, "db error")
	mockRepo.AssertExpectations(t)
}

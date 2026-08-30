package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newTestProjectService() (*services.ProjectService, *mocks.MockProjectRepository) {
	mockRepo := new(mocks.MockProjectRepository)
	cfg := &config.Config{
		EncryptionKey: "test-encryption-key-32-bytes!!!!",
	}
	svc := services.NewProjectService(mockRepo, cfg)
	return svc, mockRepo
}

func TestProjectService_Create_APIToken_Success(t *testing.T) {
	svc, mockRepo := newTestProjectService()

	createdBy := uuid.New()
	input := &services.CreateProjectInput{
		Name:            "test-project",
		Description:     "A test project",
		ConnectionType:  services.ConnectionTypeAPIToken,
		K8sAPIURL:       "https://k8s.example.com",
		K8sToken:        "my-token",
		TLSVerification: services.TLSVerificationSkip,
	}

	mockRepo.On("Create", mock.AnythingOfType("*models.Project")).Return(nil)
	mockRepo.On("AddAdmin", mock.AnythingOfType("uuid.UUID"), createdBy).Return(nil)

	project, err := svc.Create(input, createdBy)

	require.NoError(t, err)
	assert.Equal(t, "test-project", project.Name)
	assert.Equal(t, services.ConnectionTypeAPIToken, project.ConnectionType)
	assert.True(t, project.K8sTLSSkipVerify)
	mockRepo.AssertExpectations(t)
}

func TestProjectService_Create_MissingAPIURL(t *testing.T) {
	svc, _ := newTestProjectService()

	input := &services.CreateProjectInput{
		Name:           "test-project",
		ConnectionType: services.ConnectionTypeAPIToken,
		K8sToken:       "my-token",
	}

	project, err := svc.Create(input, uuid.New())

	assert.Nil(t, project)
	assert.EqualError(t, err, "k8sApiUrl is required for api_token connection type")
}

func TestProjectService_Create_InvalidConnectionType(t *testing.T) {
	svc, _ := newTestProjectService()

	input := &services.CreateProjectInput{
		Name:           "test-project",
		ConnectionType: "invalid",
	}

	project, err := svc.Create(input, uuid.New())

	assert.Nil(t, project)
	assert.EqualError(t, err, "invalid connection type")
}

func TestProjectService_GetByID_Success(t *testing.T) {
	svc, mockRepo := newTestProjectService()

	id := uuid.New()
	expected := &models.Project{ID: id, Name: "test-project"}
	mockRepo.On("GetByIDWithCounts", id).Return(expected, nil)

	project, err := svc.GetByID(id)

	require.NoError(t, err)
	assert.Equal(t, expected, project)
	mockRepo.AssertExpectations(t)
}

func TestProjectService_GetByID_NotFound(t *testing.T) {
	svc, mockRepo := newTestProjectService()

	id := uuid.New()
	mockRepo.On("GetByIDWithCounts", id).Return(nil, gorm.ErrRecordNotFound)

	project, err := svc.GetByID(id)

	assert.Nil(t, project)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	mockRepo.AssertExpectations(t)
}

func TestProjectService_List(t *testing.T) {
	svc, mockRepo := newTestProjectService()

	userID := uuid.New()
	projects := []models.Project{
		{ID: uuid.New(), Name: "project1"},
		{ID: uuid.New(), Name: "project2"},
	}
	mockRepo.On("ListByUserAccess", userID, models.UserRoleUser, 1, 10, "", map[string]string(nil)).Return(projects, int64(2), nil)

	result, total, err := svc.List(userID, models.UserRoleUser, 1, 10, "", nil)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(2), total)
	mockRepo.AssertExpectations(t)
}

func TestProjectService_Delete(t *testing.T) {
	svc, mockRepo := newTestProjectService()

	id := uuid.New()
	mockRepo.On("Delete", id).Return(nil)

	err := svc.Delete(id)

	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func TestProjectService_Update_ApprovalSettings_Success(t *testing.T) {
	svc, mockRepo := newTestProjectService()

	projectID := uuid.New()
	existingProject := &models.Project{
		ID:              projectID,
		Name:            "test-project",
		ApprovalEnabled: true,
		ConnectionType:  "api_token",
	}
	mockRepo.On("GetByID", projectID).Return(existingProject, nil)
	mockRepo.On("Update", mock.AnythingOfType("*models.Project")).Return(nil)

	enabled := false
	input := &services.UpdateProjectInput{
		ApprovalEnabled: &enabled,
	}

	project, err := svc.Update(projectID, input)

	require.NoError(t, err)
	assert.False(t, project.ApprovalEnabled)
	mockRepo.AssertExpectations(t)
}

func TestProjectService_Update_NonApprovalFields(t *testing.T) {
	svc, mockRepo := newTestProjectService()

	projectID := uuid.New()
	existingProject := &models.Project{
		ID:             projectID,
		Name:           "old-name",
		ConnectionType: "api_token",
	}
	mockRepo.On("GetByID", projectID).Return(existingProject, nil)
	mockRepo.On("Update", mock.AnythingOfType("*models.Project")).Return(nil)

	input := &services.UpdateProjectInput{
		Name: "new-name",
	}

	project, err := svc.Update(projectID, input)

	require.NoError(t, err)
	assert.Equal(t, "new-name", project.Name)
	mockRepo.AssertExpectations(t)
}

func TestProjectService_Delete_Error(t *testing.T) {
	svc, mockRepo := newTestProjectService()

	id := uuid.New()
	mockRepo.On("Delete", id).Return(errors.New("db error"))

	err := svc.Delete(id)

	assert.EqualError(t, err, "db error")
	mockRepo.AssertExpectations(t)
}

func TestProjectService_Update_MetricsFields_BearerToken(t *testing.T) {
	svc, repo := newTestProjectService()

	projectID := uuid.New()
	existing := &models.Project{
		ID:   projectID,
		Name: "test",
	}
	repo.On("GetByID", projectID).Return(existing, nil)
	repo.On("Update", mock.MatchedBy(func(p *models.Project) bool {
		return p.MetricsEndpointURL == "http://prom:9090" &&
			p.MetricsAuthType == "bearer" &&
			p.MetricsTokenEncrypted != "" &&
			p.MetricsTokenEncrypted != "secret-token" // encrypted
	})).Return(nil)

	url := "http://prom:9090"
	authType := "bearer"
	token := "secret-token"

	_, err := svc.Update(projectID, &services.UpdateProjectInput{
		MetricsEndpointURL: &url,
		MetricsAuthType:    &authType,
		MetricsToken:       &token,
	})
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestProjectService_Update_MetricsFields_UnchangedTokenPreserved(t *testing.T) {
	svc, repo := newTestProjectService()

	projectID := uuid.New()
	existing := &models.Project{
		ID:                    projectID,
		Name:                  "test",
		MetricsEndpointURL:    "http://prom:9090",
		MetricsAuthType:       "bearer",
		MetricsTokenEncrypted: "already-encrypted-value",
	}
	repo.On("GetByID", projectID).Return(existing, nil)
	repo.On("Update", mock.MatchedBy(func(p *models.Project) bool {
		return p.MetricsTokenEncrypted == "already-encrypted-value"
	})).Return(nil)

	newURL := "http://prom2:9090"
	_, err := svc.Update(projectID, &services.UpdateProjectInput{
		MetricsEndpointURL: &newURL,
		// MetricsToken not provided → preserved
	})
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

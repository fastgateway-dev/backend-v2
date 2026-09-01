package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
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

// newDefaultApprovalPolicyRepoStub stands in for the ApprovalPolicyRepo
// dependency, which is now required (Phase 2E Task 3). Before Task 3, a nil
// approvalPolicyRepo made the `s.approvalPolicyRepo != nil` guard in
// project_service.go's Create (around line 210) skip SeedDefaults entirely,
// logging nothing. This stub reproduces the same "seeding succeeded/was a
// no-op" outcome through a real, no-op-returning call instead of the
// skipped branch.
func newDefaultApprovalPolicyRepoStub() *mocks.MockApprovalPolicyRepository {
	repo := new(mocks.MockApprovalPolicyRepository)
	repo.On("SeedDefaults", mock.Anything).Return(nil).Maybe()
	return repo
}

// newDefaultPresetRepoStub is the PresetRepo counterpart of
// newDefaultApprovalPolicyRepoStub: before Task 3, a nil presetRepo made the
// `s.presetRepo != nil` guard skip SeedBuiltinPresets entirely.
func newDefaultPresetRepoStub() *mocks.MockPresetRepository {
	repo := new(mocks.MockPresetRepository)
	repo.On("SeedBuiltinPresets", mock.Anything).Return(nil).Maybe()
	return repo
}

// newPassingPreflightStub is the K8sPreflight counterpart. K8sPreflight
// became a required constructor parameter in Phase 2E Task 9 (fix round 1,
// ruling R12), which deleted the `s.k8sPreflight != nil` half of the
// condition at project_service.go Create -- so cluster prerequisite
// validation now runs for every non-in_cluster connection type instead of
// being skipped whenever the field was unset. Answering "every prerequisite
// present, no error" reproduces exactly what skipping produced: Create
// carries on to projectRepo.Create. No test below asserts on this call.
func newPassingPreflightStub() *mocks.MockKubernetesService {
	k8s := new(mocks.MockKubernetesService)
	k8s.On("ValidatePrerequisites", mock.Anything, mock.Anything, mock.Anything).
		Return(&cluster.PrerequisiteCheck{
			NamespaceExists:    true,
			GatewayCRDExists:   true,
			HTTPRouteCRDExists: true,
		}, nil).Maybe()
	return k8s
}

func newTestProjectService() (*services.ProjectService, *mocks.MockProjectRepository) {
	mockRepo := new(mocks.MockProjectRepository)
	cfg := &config.Config{
		EncryptionKey: "test-encryption-key-32-bytes!!!!",
	}
	svc := services.NewProjectService(services.ProjectServiceDeps{
		ProjectRepo:        mockRepo,
		ApprovalPolicyRepo: newDefaultApprovalPolicyRepoStub(),
		PresetRepo:         newDefaultPresetRepoStub(),
		Config:             cfg,
		K8sPreflight:       newPassingPreflightStub(),
	})
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

// ---------------------------------------------------------------------------
// NewProjectService
// ---------------------------------------------------------------------------

func fullProjectServiceDeps() services.ProjectServiceDeps {
	return services.ProjectServiceDeps{
		ProjectRepo:        new(mocks.MockProjectRepository),
		ApprovalPolicyRepo: newDefaultApprovalPolicyRepoStub(),
		PresetRepo:         newDefaultPresetRepoStub(),
		Config:             &config.Config{EncryptionKey: "test-encryption-key-32-bytes!!!!"},
		K8sPreflight:       newPassingPreflightStub(),
	}
}

func TestNewProjectService_RequiresEveryDependency(t *testing.T) {
	require.NotPanics(t, func() { services.NewProjectService(fullProjectServiceDeps()) })

	cases := map[string]func(*services.ProjectServiceDeps){
		"ProjectRepo":        func(d *services.ProjectServiceDeps) { d.ProjectRepo = nil },
		"ApprovalPolicyRepo": func(d *services.ProjectServiceDeps) { d.ApprovalPolicyRepo = nil },
		"PresetRepo":         func(d *services.ProjectServiceDeps) { d.PresetRepo = nil },
		"Config":             func(d *services.ProjectServiceDeps) { d.Config = nil },
		// Required since Phase 2E Task 9 (fix round 1) deleted the
		// `s.k8sPreflight != nil` half of the preflight condition in
		// project_service.go Create.
		"K8sPreflight": func(d *services.ProjectServiceDeps) { d.K8sPreflight = nil },
	}
	for name, breakIt := range cases {
		t.Run("nil "+name, func(t *testing.T) {
			d := fullProjectServiceDeps()
			breakIt(&d)
			assert.PanicsWithValue(t,
				"services.NewProjectService: missing required dependency: "+name,
				func() { services.NewProjectService(d) })
		})
	}
}

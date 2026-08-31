package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newTestProjectNamespaceService() (*services.ProjectNamespaceService, *mocks.MockProjectNamespaceRepository, *mocks.MockProjectRepository) {
	mockNSRepo := new(mocks.MockProjectNamespaceRepository)
	mockProjectRepo := new(mocks.MockProjectRepository)
	// Pass nil for k8sService - tests that need it will be skipped or handle the nil
	svc := services.NewProjectNamespaceService(mockNSRepo, mockProjectRepo, nil, nil)
	return svc, mockNSRepo, mockProjectRepo
}

func TestProjectNamespaceService_GetByID_Success(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	nsID := uuid.New()
	projectID := uuid.New()
	expected := &models.ProjectNamespace{
		ID:        nsID,
		ProjectID: projectID,
		Namespace: "my-namespace",
	}
	mockNSRepo.On("GetByID", nsID).Return(expected, nil)

	result, err := svc.GetByID(nsID)

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	mockNSRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_GetByID_NotFound(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	nsID := uuid.New()
	mockNSRepo.On("GetByID", nsID).Return(nil, gorm.ErrRecordNotFound)

	result, err := svc.GetByID(nsID)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	mockNSRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_ListByProjectID_Success(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	projectID := uuid.New()
	namespaces := []models.ProjectNamespace{
		{ID: uuid.New(), ProjectID: projectID, Namespace: "ns-1"},
		{ID: uuid.New(), ProjectID: projectID, Namespace: "ns-2"},
	}
	mockNSRepo.On("ListByProjectID", projectID).Return(namespaces, nil)

	result, err := svc.ListByProjectID(projectID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "ns-1", result[0].Namespace)
	assert.Equal(t, "ns-2", result[1].Namespace)
	mockNSRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_ListByProjectID_Empty(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	projectID := uuid.New()
	mockNSRepo.On("ListByProjectID", projectID).Return([]models.ProjectNamespace{}, nil)

	result, err := svc.ListByProjectID(projectID)

	require.NoError(t, err)
	assert.Empty(t, result)
	mockNSRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_IsNamespaceManaged_GatewayNamespace(t *testing.T) {
	svc, _, _ := newTestProjectNamespaceService()

	projectID := uuid.New()

	// fastgateway-system is always managed
	managed, err := svc.IsNamespaceManaged(projectID, "fastgateway-system")
	require.NoError(t, err)
	assert.True(t, managed)
}

func TestProjectNamespaceService_IsNamespaceManaged_EmptyNamespace(t *testing.T) {
	svc, _, _ := newTestProjectNamespaceService()

	projectID := uuid.New()

	// Empty namespace is always managed
	managed, err := svc.IsNamespaceManaged(projectID, "")
	require.NoError(t, err)
	assert.True(t, managed)
}

func TestProjectNamespaceService_IsNamespaceManaged_ManagedNamespace(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	projectID := uuid.New()
	mockNSRepo.On("ExistsByProjectAndNamespace", projectID, "my-ns").Return(true, nil)

	managed, err := svc.IsNamespaceManaged(projectID, "my-ns")

	require.NoError(t, err)
	assert.True(t, managed)
	mockNSRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_IsNamespaceManaged_UnmanagedNamespace(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	projectID := uuid.New()
	mockNSRepo.On("ExistsByProjectAndNamespace", projectID, "other-ns").Return(false, nil)

	managed, err := svc.IsNamespaceManaged(projectID, "other-ns")

	require.NoError(t, err)
	assert.False(t, managed)
	mockNSRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_IsNamespaceManaged_Error(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	projectID := uuid.New()
	mockNSRepo.On("ExistsByProjectAndNamespace", projectID, "my-ns").Return(false, errors.New("db error"))

	managed, err := svc.IsNamespaceManaged(projectID, "my-ns")

	assert.False(t, managed)
	assert.EqualError(t, err, "db error")
	mockNSRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_Create_ProjectNotFound(t *testing.T) {
	svc, _, mockProjectRepo := newTestProjectNamespaceService()

	projectID := uuid.New()
	input := &services.CreateProjectNamespaceInput{Namespace: "my-ns"}

	mockProjectRepo.On("GetByID", projectID).Return(nil, gorm.ErrRecordNotFound)

	result, err := svc.Create(projectID, input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "project not found")
	mockProjectRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_Create_AlreadyExists(t *testing.T) {
	svc, mockNSRepo, mockProjectRepo := newTestProjectNamespaceService()

	projectID := uuid.New()
	project := &models.Project{ID: projectID, Name: "test-project"}
	input := &services.CreateProjectNamespaceInput{Namespace: "my-ns", Capabilities: []string{"deploy_gateway"}}

	mockProjectRepo.On("GetByID", projectID).Return(project, nil)
	mockNSRepo.On("ExistsByProjectAndNamespace", projectID, "my-ns").Return(true, nil)

	result, err := svc.Create(projectID, input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "namespace already added to this project")
	mockProjectRepo.AssertExpectations(t)
	mockNSRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_Delete_NotFound(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	nsID := uuid.New()
	mockNSRepo.On("GetByID", nsID).Return(nil, gorm.ErrRecordNotFound)

	err := svc.Delete(nsID)

	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	mockNSRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// GetByProjectAndNamespace
// ---------------------------------------------------------------------------

func TestProjectNamespaceService_GetByProjectAndNamespace_Success(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	projectID := uuid.New()
	expected := &models.ProjectNamespace{
		ID:        uuid.New(),
		ProjectID: projectID,
		Namespace: "my-namespace",
	}
	mockNSRepo.On("GetByProjectAndNamespace", projectID, "my-namespace").Return(expected, nil)

	result, err := svc.GetByProjectAndNamespace(projectID, "my-namespace")

	require.NoError(t, err)
	assert.Equal(t, "my-namespace", result.Namespace)
	mockNSRepo.AssertExpectations(t)
}

func TestProjectNamespaceService_GetByProjectAndNamespace_NotFound(t *testing.T) {
	svc, mockNSRepo, _ := newTestProjectNamespaceService()

	projectID := uuid.New()
	mockNSRepo.On("GetByProjectAndNamespace", projectID, "nonexistent").Return(nil, errors.New("not found"))

	result, err := svc.GetByProjectAndNamespace(projectID, "nonexistent")

	assert.Nil(t, result)
	assert.EqualError(t, err, "not found")
	mockNSRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Create - ExistsByProjectAndNamespace error
// ---------------------------------------------------------------------------

func TestProjectNamespaceService_Create_ExistsCheckError(t *testing.T) {
	svc, mockNSRepo, mockProjectRepo := newTestProjectNamespaceService()

	projectID := uuid.New()
	project := &models.Project{ID: projectID, Name: "test-project"}
	input := &services.CreateProjectNamespaceInput{Namespace: "my-ns", Capabilities: []string{"deploy_gateway"}}

	mockProjectRepo.On("GetByID", projectID).Return(project, nil)
	mockNSRepo.On("ExistsByProjectAndNamespace", projectID, "my-ns").Return(false, errors.New("db error"))

	result, err := svc.Create(projectID, input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "db error")
	mockProjectRepo.AssertExpectations(t)
	mockNSRepo.AssertExpectations(t)
}

// =====================================================================
// Capability tests
// =====================================================================

func newTestProjectNamespaceServiceWithK8s() (*services.ProjectNamespaceService, *mocks.MockProjectNamespaceRepository, *mocks.MockProjectRepository, *mocks.MockDomainRepository, *mocks.MockKubernetesService) {
	mockNSRepo := new(mocks.MockProjectNamespaceRepository)
	mockProjectRepo := new(mocks.MockProjectRepository)
	mockDomainRepo := new(mocks.MockDomainRepository)
	mockK8s := new(mocks.MockKubernetesService)
	svc := services.NewProjectNamespaceService(mockNSRepo, mockProjectRepo, mockDomainRepo, mockK8s)
	return svc, mockNSRepo, mockProjectRepo, mockDomainRepo, mockK8s
}

func TestProjectNamespaceService_Create_RejectsUnknownCapability(t *testing.T) {
	svc, mockNSRepo, mockProjectRepo, _, _ := newTestProjectNamespaceServiceWithK8s()
	projectID := uuid.New()
	mockProjectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID}, nil)

	_, err := svc.Create(projectID, &services.CreateProjectNamespaceInput{
		Namespace:    "ns",
		Capabilities: []string{"deploy_gateway", "bogus_capability"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown capability")
	mockProjectRepo.AssertExpectations(t)
	mockNSRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestProjectNamespaceService_Create_RequiresAtLeastOneCapability(t *testing.T) {
	svc, _, mockProjectRepo, _, _ := newTestProjectNamespaceServiceWithK8s()
	projectID := uuid.New()
	mockProjectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID}, nil)

	_, err := svc.Create(projectID, &services.CreateProjectNamespaceInput{
		Namespace:    "ns",
		Capabilities: nil,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one capability")
}

func TestProjectNamespaceService_Create_DeployOnlyDoesNotCreateRG(t *testing.T) {
	svc, mockNSRepo, mockProjectRepo, mockDomainRepo, mockK8s := newTestProjectNamespaceServiceWithK8s()
	projectID := uuid.New()
	mockProjectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID}, nil)
	mockNSRepo.On("ExistsByProjectAndNamespace", projectID, "team-a-gw").Return(false, nil)
	mockK8s.On("ListNamespaces", mock.Anything, projectID).Return([]string{"team-a-gw", "default"}, nil)
	mockNSRepo.On("Create", mock.AnythingOfType("*models.ProjectNamespace")).Return(nil)
	// No domains exist yet — getDomainNamespaces only used if RG is created. Not called here.
	_ = mockDomainRepo

	ns, err := svc.Create(projectID, &services.CreateProjectNamespaceInput{
		Namespace:    "team-a-gw",
		Capabilities: []string{"deploy_gateway"},
	})
	require.NoError(t, err)
	assert.False(t, ns.ReferenceGrantCreated)
	mockK8s.AssertNotCalled(t, "CreateReferenceGrant", mock.Anything, mock.Anything, mock.Anything)
}

func TestProjectNamespaceService_Create_BackendOnlySetsServiceKindOnly(t *testing.T) {
	svc, mockNSRepo, mockProjectRepo, mockDomainRepo, mockK8s := newTestProjectNamespaceServiceWithK8s()
	projectID := uuid.New()
	mockProjectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID}, nil)
	mockNSRepo.On("ExistsByProjectAndNamespace", projectID, "backends").Return(false, nil)
	mockK8s.On("ListNamespaces", mock.Anything, projectID).Return([]string{"backends"}, nil)
	mockNSRepo.On("Create", mock.AnythingOfType("*models.ProjectNamespace")).Return(nil)
	mockDomainRepo.On("ListByProjectID", projectID, 1, 10000, "", "", map[string]string(nil)).
		Return([]models.Domain{}, int64(0), nil)

	var capturedConfig *cluster.ReferenceGrantConfig
	mockK8s.On("CreateReferenceGrant", mock.Anything, projectID, mock.AnythingOfType("*cluster.ReferenceGrantConfig")).
		Run(func(args mock.Arguments) {
			capturedConfig = args.Get(2).(*cluster.ReferenceGrantConfig)
		}).Return(nil)
	mockNSRepo.On("Update", mock.AnythingOfType("*models.ProjectNamespace")).Return(nil)

	ns, err := svc.Create(projectID, &services.CreateProjectNamespaceInput{
		Namespace:    "backends",
		Capabilities: []string{"backend_service"},
	})
	require.NoError(t, err)
	assert.True(t, ns.ReferenceGrantCreated)
	require.NotNil(t, capturedConfig)
	assert.Equal(t, []string{"Service"}, capturedConfig.ToKinds)
}

func TestProjectNamespaceService_Create_BothCapsSetsBothKinds(t *testing.T) {
	svc, mockNSRepo, mockProjectRepo, mockDomainRepo, mockK8s := newTestProjectNamespaceServiceWithK8s()
	projectID := uuid.New()
	mockProjectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID}, nil)
	mockNSRepo.On("ExistsByProjectAndNamespace", projectID, "shared").Return(false, nil)
	mockK8s.On("ListNamespaces", mock.Anything, projectID).Return([]string{"shared"}, nil)
	mockNSRepo.On("Create", mock.AnythingOfType("*models.ProjectNamespace")).Return(nil)
	mockDomainRepo.On("ListByProjectID", projectID, 1, 10000, "", "", map[string]string(nil)).
		Return([]models.Domain{}, int64(0), nil)

	var capturedConfig *cluster.ReferenceGrantConfig
	mockK8s.On("CreateReferenceGrant", mock.Anything, projectID, mock.AnythingOfType("*cluster.ReferenceGrantConfig")).
		Run(func(args mock.Arguments) {
			capturedConfig = args.Get(2).(*cluster.ReferenceGrantConfig)
		}).Return(nil)
	mockNSRepo.On("Update", mock.AnythingOfType("*models.ProjectNamespace")).Return(nil)

	_, err := svc.Create(projectID, &services.CreateProjectNamespaceInput{
		Namespace:    "shared",
		Capabilities: []string{"backend_service", "tls_secret"},
	})
	require.NoError(t, err)
	require.NotNil(t, capturedConfig)
	assert.ElementsMatch(t, []string{"Service", "Secret"}, capturedConfig.ToKinds)
}

func TestProjectNamespaceService_Update_DropsRGWhenNoTargetCaps(t *testing.T) {
	svc, mockNSRepo, _, _, mockK8s := newTestProjectNamespaceServiceWithK8s()
	id := uuid.New()
	projectID := uuid.New()
	existing := &models.ProjectNamespace{
		ID:                    id,
		ProjectID:             projectID,
		Namespace:             "ns",
		Capabilities:          []string{"backend_service", "tls_secret"},
		ReferenceGrantCreated: true,
	}
	mockNSRepo.On("GetByID", id).Return(existing, nil)
	mockK8s.On("DeleteReferenceGrant", mock.Anything, projectID, "ns", mock.AnythingOfType("string")).Return(nil)
	mockNSRepo.On("Update", mock.AnythingOfType("*models.ProjectNamespace")).Return(nil)

	ns, err := svc.Update(id, &services.UpdateProjectNamespaceInput{
		Capabilities: []string{"deploy_gateway"},
	})
	require.NoError(t, err)
	assert.False(t, ns.ReferenceGrantCreated)
	assert.ElementsMatch(t, []string{"deploy_gateway"}, []string(ns.Capabilities))
}

func TestProjectNamespaceService_Update_RecreatesRGForNewKinds(t *testing.T) {
	svc, mockNSRepo, _, mockDomainRepo, mockK8s := newTestProjectNamespaceServiceWithK8s()
	id := uuid.New()
	projectID := uuid.New()
	existing := &models.ProjectNamespace{
		ID: id, ProjectID: projectID, Namespace: "ns",
		Capabilities:          []string{"backend_service"},
		ReferenceGrantCreated: true,
	}
	mockNSRepo.On("GetByID", id).Return(existing, nil)
	mockDomainRepo.On("ListByProjectID", projectID, 1, 10000, "", "", map[string]string(nil)).
		Return([]models.Domain{}, int64(0), nil)

	var capturedConfig *cluster.ReferenceGrantConfig
	mockK8s.On("RecreateReferenceGrant", mock.Anything, projectID, mock.AnythingOfType("*cluster.ReferenceGrantConfig")).
		Run(func(args mock.Arguments) {
			capturedConfig = args.Get(2).(*cluster.ReferenceGrantConfig)
		}).Return(nil)
	mockNSRepo.On("Update", mock.AnythingOfType("*models.ProjectNamespace")).Return(nil)

	_, err := svc.Update(id, &services.UpdateProjectNamespaceInput{
		Capabilities: []string{"backend_service", "tls_secret"},
	})
	require.NoError(t, err)
	require.NotNil(t, capturedConfig)
	assert.ElementsMatch(t, []string{"Service", "Secret"}, capturedConfig.ToKinds)
}

func TestProjectNamespaceService_Update_NotFound(t *testing.T) {
	svc, mockNSRepo, _, _, _ := newTestProjectNamespaceServiceWithK8s()
	nsID := uuid.New()
	mockNSRepo.On("GetByID", nsID).Return(nil, gorm.ErrRecordNotFound)

	_, err := svc.Update(nsID, &services.UpdateProjectNamespaceInput{Capabilities: []string{"deploy_gateway"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestProjectNamespaceService_Update_EmptyCapabilitiesRejected(t *testing.T) {
	svc, mockNSRepo, _, _, _ := newTestProjectNamespaceServiceWithK8s()
	nsID := uuid.New()
	mockNSRepo.On("GetByID", nsID).Return(&models.ProjectNamespace{ID: nsID, Capabilities: []string{"deploy_gateway"}}, nil)

	_, err := svc.Update(nsID, &services.UpdateProjectNamespaceInput{Capabilities: nil})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one capability")
	mockNSRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestProjectNamespaceService_Update_AddsCapsWhenNoPriorRG(t *testing.T) {
	svc, mockNSRepo, _, mockDomainRepo, mockK8s := newTestProjectNamespaceServiceWithK8s()
	nsID := uuid.New()
	projectID := uuid.New()
	existing := &models.ProjectNamespace{
		ID: nsID, ProjectID: projectID, Namespace: "ns",
		Capabilities:          []string{"deploy_gateway"}, // no targetable kinds initially
		ReferenceGrantCreated: false,
	}
	mockNSRepo.On("GetByID", nsID).Return(existing, nil)
	mockDomainRepo.On("ListByProjectID", projectID, 1, 10000, "", "", map[string]string(nil)).
		Return([]models.Domain{}, int64(0), nil)

	var capturedConfig *cluster.ReferenceGrantConfig
	mockK8s.On("RecreateReferenceGrant", mock.Anything, projectID, mock.AnythingOfType("*cluster.ReferenceGrantConfig")).
		Run(func(args mock.Arguments) {
			capturedConfig = args.Get(2).(*cluster.ReferenceGrantConfig)
		}).Return(nil)
	mockNSRepo.On("Update", mock.AnythingOfType("*models.ProjectNamespace")).Return(nil)

	ns, err := svc.Update(nsID, &services.UpdateProjectNamespaceInput{
		Capabilities: []string{"deploy_gateway", "backend_service"},
	})
	require.NoError(t, err)
	assert.True(t, ns.ReferenceGrantCreated)
	require.NotNil(t, capturedConfig)
	assert.Equal(t, []string{"Service"}, capturedConfig.ToKinds)
}

func TestProjectNamespaceService_EnsureReferenceGrant_CleansStaleRG(t *testing.T) {
	svc, mockNSRepo, _, _, mockK8s := newTestProjectNamespaceServiceWithK8s()
	nsID := uuid.New()
	projectID := uuid.New()
	stale := &models.ProjectNamespace{
		ID: nsID, ProjectID: projectID, Namespace: "ns",
		Capabilities:          []string{"deploy_gateway"}, // no target kinds
		ReferenceGrantCreated: true,                       // stale flag
	}
	mockNSRepo.On("GetByID", nsID).Return(stale, nil)
	mockK8s.On("DeleteReferenceGrant", mock.Anything, projectID, "ns", mock.AnythingOfType("string")).Return(nil)
	mockNSRepo.On("Update", mock.MatchedBy(func(n *models.ProjectNamespace) bool {
		return !n.ReferenceGrantCreated
	})).Return(nil)

	err := svc.EnsureReferenceGrant(nsID)
	require.NoError(t, err)
}

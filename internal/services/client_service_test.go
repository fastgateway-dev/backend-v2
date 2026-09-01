package services_test

import (
	"context"
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

// newTestClientService stands in for services.NewClientService now that
// every dependency is required (Phase 2E Task 3). Every test below built its
// ClientService positionally with just (clientRepo, clientIPRepo, teamRepo),
// passing nil for whatever it did not need; this helper preserves that call
// shape by substituting an inert mock for any nil argument, and for the
// three dependencies (ClientHeaderRepo, ClientAttachmentRepo, RouteRepo)
// that used to arrive through setters and have no positional slot here.
//
// ClientAttachmentRepo needs a working default, not just an inert one.
// client_service.go:374's R13 guard used to make cascadeToAttachedRoutes a
// silent no-op whenever clientAttachmentRepo/routeRepo/state were unset; that
// guard is deleted in Phase 2E Task 3 (NewClientService now requires both),
// so the cascade runs unconditionally, and every mutation method
// (AddIP/RemoveIP/GenerateAPIKey/RevokeAPIKey/ConfigureJWT/RemoveJWT/
// UpdateClientMTLS/AddHeader/RemoveHeader/SetAllowedMethods) now calls one of
// ClientAttachmentRepo's five ListActiveByClientIDWith*/ListByClientID
// query methods. FINDING (Phase 2E Task 3): none of the tests below expected
// this call, so the default stub returns an empty attachment list for all
// five, reproducing the same "nothing to cascade to" outcome the R13 guard
// used to produce by skipping.
func newTestClientService(
	clientRepo *mocks.MockClientRepository,
	clientIPRepo *mocks.MockClientIPRepository,
	teamRepo *mocks.MockTeamRepository,
) *services.ClientService {
	if clientRepo == nil {
		clientRepo = new(mocks.MockClientRepository)
	}
	if clientIPRepo == nil {
		clientIPRepo = new(mocks.MockClientIPRepository)
	}
	if teamRepo == nil {
		teamRepo = new(mocks.MockTeamRepository)
	}
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	noAttachments := []models.ClientRouteAttachment{}
	attachmentRepo.On("ListActiveByClientIDWithIPAllowlist", mock.Anything).Return(noAttachments, nil).Maybe()
	attachmentRepo.On("ListActiveByClientIDWithHeaderAuth", mock.Anything).Return(noAttachments, nil).Maybe()
	attachmentRepo.On("ListActiveByClientIDWithAPIKey", mock.Anything).Return(noAttachments, nil).Maybe()
	attachmentRepo.On("ListActiveByClientIDWithJWT", mock.Anything).Return(noAttachments, nil).Maybe()
	attachmentRepo.On("ListByClientID", mock.Anything).Return(noAttachments, nil).Maybe()

	// K8sSecrets/K8sAPIKeys became required constructor parameters in Phase
	// 2E Task 9 (fix round 1, ruling R12), which deleted the two conditions
	// that used to skip the Kubernetes cleanup whenever they were unset:
	// client_service.go:244 (Delete) and client_service.go:930
	// (UpdateClientMTLS's disable branch). Both blocks are best-effort --
	// every failure is logged or discarded and neither changes the method's
	// return value -- so a stub that accepts the calls and answers nil
	// reproduces exactly what the skipped path produced: no secret deleted,
	// no error, same result. No test below asserts on these calls.
	k8s := new(mocks.MockKubernetesService)
	k8s.On("DeleteAPIKeySecret", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	k8s.On("DeleteSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	// Same reason: the deleted conditions also gated this call, so no test
	// below expects it.
	teamRepo.On("ListTeamProjects", mock.Anything).
		Return([]models.ProjectTeamRole{}, nil).Maybe()

	return services.NewClientService(services.ClientServiceDeps{
		ClientRepo:           clientRepo,
		ClientIPRepo:         clientIPRepo,
		ClientHeaderRepo:     new(mocks.MockClientHeaderRepository),
		TeamRepo:             teamRepo,
		ClientAttachmentRepo: attachmentRepo,
		RouteRepo:            new(mocks.MockRouteRepository),
		K8sSecrets:           k8s,
		K8sAPIKeys:           k8s,
	})
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func TestClientService_Create_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, teamRepo)

	teamID := uuid.New()
	createdBy := uuid.New()
	input := &services.CreateClientInput{
		Name:        "test-client",
		Description: "A test client",
		TeamID:      teamID,
	}

	team := &models.Team{ID: teamID, Name: "my-team"}
	teamRepo.On("GetByID", teamID).Return(team, nil)
	clientRepo.On("ExistsByName", "test-client").Return(false, nil)
	clientRepo.On("Create", mock.AnythingOfType("*models.Client")).Return(nil)
	clientRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(&models.Client{
		ID:                 uuid.New(),
		Name:               "test-client",
		TeamID:             teamID,
		ClientIDHeaderName: "x-client-id",
	}, nil)

	result, err := svc.Create(input, createdBy)

	require.NoError(t, err)
	assert.Equal(t, "test-client", result.Name)
	assert.Equal(t, "x-client-id", result.ClientIDHeaderName)
	clientRepo.AssertExpectations(t)
	teamRepo.AssertExpectations(t)
}

func TestClientService_Create_DuplicateName(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, teamRepo)

	teamID := uuid.New()
	teamRepo.On("GetByID", teamID).Return(&models.Team{ID: teamID}, nil)
	clientRepo.On("ExistsByName", "dup-client").Return(true, nil)

	_, err := svc.Create(&services.CreateClientInput{
		Name:   "dup-client",
		TeamID: teamID,
	}, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client name already exists")
}

func TestClientService_Create_TeamNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, teamRepo)

	teamID := uuid.New()
	teamRepo.On("GetByID", teamID).Return(nil, errors.New("not found"))

	_, err := svc.Create(&services.CreateClientInput{
		Name:   "test",
		TeamID: teamID,
	}, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "team not found")
}

// ---------------------------------------------------------------------------
// GetByID
// ---------------------------------------------------------------------------

func TestClientService_GetByID(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	id := uuid.New()
	expected := &models.Client{ID: id, Name: "client-a"}
	clientRepo.On("GetByID", id).Return(expected, nil)

	result, err := svc.GetByID(id)

	require.NoError(t, err)
	assert.Equal(t, expected.Name, result.Name)
	clientRepo.AssertExpectations(t)
}

func TestClientService_GetByID_NotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	id := uuid.New()
	clientRepo.On("GetByID", id).Return(nil, errors.New("not found"))

	_, err := svc.GetByID(id)

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func TestClientService_Update_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	id := uuid.New()
	existing := &models.Client{ID: id, Name: "old-name"}
	clientRepo.On("GetByID", id).Return(existing, nil).Once()
	clientRepo.On("ExistsByNameExcluding", "new-name", id).Return(false, nil)
	clientRepo.On("Update", mock.AnythingOfType("*models.Client")).Return(nil)
	clientRepo.On("GetByID", id).Return(&models.Client{ID: id, Name: "new-name"}, nil).Once()

	result, err := svc.Update(id, &services.UpdateClientInput{Name: "new-name"})

	require.NoError(t, err)
	assert.Equal(t, "new-name", result.Name)
	clientRepo.AssertExpectations(t)
}

func TestClientService_Update_NotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	id := uuid.New()
	clientRepo.On("GetByID", id).Return(nil, errors.New("not found"))

	_, err := svc.Update(id, &services.UpdateClientInput{Name: "x"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestClientService_Delete_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	id := uuid.New()
	clientRepo.On("GetByID", id).Return(&models.Client{ID: id}, nil)
	clientRepo.On("Delete", id).Return(nil)

	err := svc.Delete(context.Background(), id)

	require.NoError(t, err)
	clientRepo.AssertExpectations(t)
}

func TestClientService_Delete_NotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	id := uuid.New()
	clientRepo.On("GetByID", id).Return(nil, errors.New("not found"))

	err := svc.Delete(context.Background(), id)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestClientService_List(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	clients := []models.Client{
		{ID: uuid.New(), Name: "a"},
		{ID: uuid.New(), Name: "b"},
	}
	clientRepo.On("List", 1, 10, (*uuid.UUID)(nil)).Return(clients, int64(2), nil)

	result, total, err := svc.List(1, 10, nil)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(2), total)
	clientRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// AddIP
// ---------------------------------------------------------------------------

func TestClientService_AddIP_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, nil)

	clientID := uuid.New()
	createdBy := uuid.New()

	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID}, nil)
	clientIPRepo.On("Create", mock.AnythingOfType("*models.ClientIPAddress")).Return(nil)
	clientIPRepo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(&models.ClientIPAddress{
		ClientID: clientID,
		CIDR:     "10.0.0.0/24",
	}, nil)

	result, err := svc.AddIP(clientID, &services.CreateClientIPInput{CIDR: "10.0.0.0/24"}, createdBy)

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0/24", result.CIDR)
	clientIPRepo.AssertExpectations(t)
}

func TestClientService_AddIP_InvalidCIDR(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID}, nil)

	_, err := svc.AddIP(clientID, &services.CreateClientIPInput{CIDR: "not-a-cidr"}, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid CIDR")
}

func TestClientService_AddIP_ClientNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(nil, errors.New("not found"))

	_, err := svc.AddIP(clientID, &services.CreateClientIPInput{CIDR: "10.0.0.0/24"}, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
}

// ---------------------------------------------------------------------------
// RemoveIP
// ---------------------------------------------------------------------------

func TestClientService_RemoveIP_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, nil)

	clientID := uuid.New()
	ipID := uuid.New()

	clientIPRepo.On("GetByID", ipID).Return(&models.ClientIPAddress{
		ID:       ipID,
		ClientID: clientID,
	}, nil)
	clientIPRepo.On("Delete", ipID).Return(nil)

	err := svc.RemoveIP(clientID, ipID)

	require.NoError(t, err)
	clientIPRepo.AssertExpectations(t)
}

func TestClientService_RemoveIP_WrongClient(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, nil)

	clientID := uuid.New()
	otherClientID := uuid.New()
	ipID := uuid.New()

	clientIPRepo.On("GetByID", ipID).Return(&models.ClientIPAddress{
		ID:       ipID,
		ClientID: otherClientID,
	}, nil)

	err := svc.RemoveIP(clientID, ipID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong to this client")
}

func TestClientService_RemoveIP_NotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, nil)

	clientID := uuid.New()
	ipID := uuid.New()
	clientIPRepo.On("GetByID", ipID).Return(nil, errors.New("not found"))

	err := svc.RemoveIP(clientID, ipID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "IP address not found")
}

// ---------------------------------------------------------------------------
// ListIPs
// ---------------------------------------------------------------------------

func TestClientService_ListIPs_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, nil)

	clientID := uuid.New()
	ips := []models.ClientIPAddress{
		{ID: uuid.New(), CIDR: "10.0.0.0/24"},
		{ID: uuid.New(), CIDR: "192.168.1.0/24"},
	}
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID}, nil)
	clientIPRepo.On("ListByClientID", clientID).Return(ips, nil)

	result, err := svc.ListIPs(clientID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	clientIPRepo.AssertExpectations(t)
}

func TestClientService_ListIPs_ClientNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	svc := newTestClientService(clientRepo, clientIPRepo, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(nil, errors.New("not found"))

	_, err := svc.ListIPs(clientID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
}

// ---------------------------------------------------------------------------
// Update (additional cases)
// ---------------------------------------------------------------------------

func TestClientService_Update_DuplicateName(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	id := uuid.New()
	existing := &models.Client{ID: id, Name: "old-name"}
	clientRepo.On("GetByID", id).Return(existing, nil)
	clientRepo.On("ExistsByNameExcluding", "dup-name", id).Return(true, nil)

	_, err := svc.Update(id, &services.UpdateClientInput{Name: "dup-name"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client name already exists")
}

func TestClientService_Update_SameNameNoCheck(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	id := uuid.New()
	existing := &models.Client{ID: id, Name: "same-name", Description: "old"}
	clientRepo.On("GetByID", id).Return(existing, nil).Once()
	clientRepo.On("Update", mock.AnythingOfType("*models.Client")).Return(nil)
	clientRepo.On("GetByID", id).Return(&models.Client{ID: id, Name: "same-name", Description: "new desc"}, nil).Once()

	result, err := svc.Update(id, &services.UpdateClientInput{Name: "same-name", Description: "new desc"})

	require.NoError(t, err)
	assert.Equal(t, "new desc", result.Description)
}

// ---------------------------------------------------------------------------
// GenerateAPIKey
// ---------------------------------------------------------------------------

func TestClientService_GenerateAPIKey_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	clientID := uuid.New()
	createdBy := uuid.New()
	client := &models.Client{ID: clientID, Name: "test-client"}

	clientRepo.On("GetByID", clientID).Return(client, nil)
	clientRepo.On("Update", mock.AnythingOfType("*models.Client")).Return(nil)

	result, err := svc.GenerateAPIKey(context.Background(), clientID, nil, createdBy)

	require.NoError(t, err)
	assert.NotEmpty(t, result.APIKey)
	assert.True(t, len(result.APIKey) > 8)
	assert.Equal(t, "x-api-key", result.HeaderName)
	assert.NotEmpty(t, result.Prefix)
}

func TestClientService_GenerateAPIKey_ClientNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(nil, errors.New("not found"))

	_, err := svc.GenerateAPIKey(context.Background(), clientID, nil, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
}

func TestClientService_GenerateAPIKey_CustomHeaderName(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	clientID := uuid.New()
	client := &models.Client{ID: clientID}

	clientRepo.On("GetByID", clientID).Return(client, nil)
	clientRepo.On("Update", mock.AnythingOfType("*models.Client")).Return(nil)

	input := &services.GenerateAPIKeyInput{HeaderName: "X-Custom-Key"}
	result, err := svc.GenerateAPIKey(context.Background(), clientID, input, uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "x-custom-key", result.HeaderName)
}

// ---------------------------------------------------------------------------
// GetAPIKeyForDeploy
// ---------------------------------------------------------------------------

func TestClientService_GetAPIKeyForDeploy_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	// The actual key encoded in base64
	apiKey := "fg_live_abcdef123456"
	encoded := "ZmdfbGl2ZV9hYmNkZWYxMjM0NTY=" // base64 of apiKey

	client := &models.Client{
		APIKeyEnabled:   true,
		APIKeyEncrypted: encoded,
	}

	result, err := svc.GetAPIKeyForDeploy(context.Background(), client)

	require.NoError(t, err)
	assert.Equal(t, apiKey, result)
}

func TestClientService_GetAPIKeyForDeploy_NotEnabled(t *testing.T) {
	svc := newTestClientService(nil, nil, nil)

	client := &models.Client{APIKeyEnabled: false}
	_, err := svc.GetAPIKeyForDeploy(context.Background(), client)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not have an API key enabled")
}

func TestClientService_GetAPIKeyForDeploy_NoData(t *testing.T) {
	svc := newTestClientService(nil, nil, nil)

	client := &models.Client{APIKeyEnabled: true, APIKeyEncrypted: ""}
	_, err := svc.GetAPIKeyForDeploy(context.Background(), client)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// Delete with K8s cleanup
// ---------------------------------------------------------------------------

func TestClientService_Delete_WithAPIKeyCleanup(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	id := uuid.New()
	projectID := uuid.New()

	client := &models.Client{ID: id, APIKeyEnabled: true}
	clientRepo.On("GetByID", id).Return(client, nil)

	// No k8sService set, so it won't try to delete secrets but will still list attachments
	attachmentRepo.On("ListByClientID", id).Return([]models.ClientRouteAttachment{
		{ID: uuid.New(), Route: &models.Route{Domain: models.Domain{ProjectID: projectID}}},
	}, nil)

	clientRepo.On("Delete", id).Return(nil)

	err := svc.Delete(context.Background(), id)

	require.NoError(t, err)
	clientRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// RevokeAPIKey
// ---------------------------------------------------------------------------

func TestClientService_RevokeAPIKey_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	clientID := uuid.New()
	client := &models.Client{ID: clientID, APIKeyEnabled: true, APIKeyHash: "hash"}

	clientRepo.On("GetByID", clientID).Return(client, nil)
	clientRepo.On("Update", mock.AnythingOfType("*models.Client")).Return(nil)

	err := svc.RevokeAPIKey(context.Background(), clientID)

	require.NoError(t, err)
}

func TestClientService_RevokeAPIKey_NoKey(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := newTestClientService(clientRepo, nil, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID, APIKeyEnabled: false}, nil)

	err := svc.RevokeAPIKey(context.Background(), clientID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not have an API key")
}

// ---------------------------------------------------------------------------
// NewClientService
// ---------------------------------------------------------------------------

func fullClientServiceDeps() services.ClientServiceDeps {
	return services.ClientServiceDeps{
		ClientRepo:           new(mocks.MockClientRepository),
		ClientIPRepo:         new(mocks.MockClientIPRepository),
		ClientHeaderRepo:     new(mocks.MockClientHeaderRepository),
		TeamRepo:             new(mocks.MockTeamRepository),
		ClientAttachmentRepo: new(mocks.MockClientAttachmentRepository),
		RouteRepo:            new(mocks.MockRouteRepository),
		K8sSecrets:           new(mocks.MockKubernetesService),
		K8sAPIKeys:           new(mocks.MockKubernetesService),
	}
}

func TestNewClientService_RequiresEveryDependency(t *testing.T) {
	require.NotPanics(t, func() { services.NewClientService(fullClientServiceDeps()) })

	cases := map[string]func(*services.ClientServiceDeps){
		"ClientRepo":           func(d *services.ClientServiceDeps) { d.ClientRepo = nil },
		"ClientIPRepo":         func(d *services.ClientServiceDeps) { d.ClientIPRepo = nil },
		"ClientHeaderRepo":     func(d *services.ClientServiceDeps) { d.ClientHeaderRepo = nil },
		"TeamRepo":             func(d *services.ClientServiceDeps) { d.TeamRepo = nil },
		"ClientAttachmentRepo": func(d *services.ClientServiceDeps) { d.ClientAttachmentRepo = nil },
		"RouteRepo":            func(d *services.ClientServiceDeps) { d.RouteRepo = nil },
		// Required since Phase 2E Task 9 (fix round 1) deleted the two
		// conditions that skipped Kubernetes secret cleanup when they were
		// unset -- client_service.go Delete and UpdateClientMTLS.
		"K8sSecrets": func(d *services.ClientServiceDeps) { d.K8sSecrets = nil },
		"K8sAPIKeys": func(d *services.ClientServiceDeps) { d.K8sAPIKeys = nil },
	}
	for name, breakIt := range cases {
		t.Run("nil "+name, func(t *testing.T) {
			d := fullClientServiceDeps()
			breakIt(&d)
			assert.PanicsWithValue(t,
				"services.NewClientService: missing required dependency: "+name,
				func() { services.NewClientService(d) })
		})
	}
}

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

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func TestClientService_Create_Success(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	teamRepo := new(mocks.MockTeamRepository)
	svc := services.NewClientService(clientRepo, clientIPRepo, teamRepo)

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
	svc := services.NewClientService(clientRepo, clientIPRepo, teamRepo)

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
	svc := services.NewClientService(clientRepo, clientIPRepo, teamRepo)

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
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

	id := uuid.New()
	clientRepo.On("GetByID", id).Return(&models.Client{ID: id}, nil)
	clientRepo.On("Delete", id).Return(nil)

	err := svc.Delete(context.Background(), id)

	require.NoError(t, err)
	clientRepo.AssertExpectations(t)
}

func TestClientService_Delete_NotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, clientIPRepo, nil)

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
	svc := services.NewClientService(clientRepo, clientIPRepo, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID}, nil)

	_, err := svc.AddIP(clientID, &services.CreateClientIPInput{CIDR: "not-a-cidr"}, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid CIDR")
}

func TestClientService_AddIP_ClientNotFound(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	clientIPRepo := new(mocks.MockClientIPRepository)
	svc := services.NewClientService(clientRepo, clientIPRepo, nil)

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
	svc := services.NewClientService(clientRepo, clientIPRepo, nil)

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
	svc := services.NewClientService(clientRepo, clientIPRepo, nil)

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
	svc := services.NewClientService(clientRepo, clientIPRepo, nil)

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
	svc := services.NewClientService(clientRepo, clientIPRepo, nil)

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
	svc := services.NewClientService(clientRepo, clientIPRepo, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(nil, errors.New("not found"))

	_, err := svc.GenerateAPIKey(context.Background(), clientID, nil, uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not found")
}

func TestClientService_GenerateAPIKey_CustomHeaderName(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)

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
	svc := services.NewClientService(nil, nil, nil)

	client := &models.Client{APIKeyEnabled: false}
	_, err := svc.GetAPIKeyForDeploy(context.Background(), client)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not have an API key enabled")
}

func TestClientService_GetAPIKeyForDeploy_NoData(t *testing.T) {
	svc := services.NewClientService(nil, nil, nil)

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
	svc := services.NewClientService(clientRepo, nil, nil)
	svc.SetClientAttachmentRepository(attachmentRepo)

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
	svc := services.NewClientService(clientRepo, nil, nil)

	clientID := uuid.New()
	client := &models.Client{ID: clientID, APIKeyEnabled: true, APIKeyHash: "hash"}

	clientRepo.On("GetByID", clientID).Return(client, nil)
	clientRepo.On("Update", mock.AnythingOfType("*models.Client")).Return(nil)

	err := svc.RevokeAPIKey(context.Background(), clientID)

	require.NoError(t, err)
}

func TestClientService_RevokeAPIKey_NoKey(t *testing.T) {
	clientRepo := new(mocks.MockClientRepository)
	svc := services.NewClientService(clientRepo, nil, nil)

	clientID := uuid.New()
	clientRepo.On("GetByID", clientID).Return(&models.Client{ID: clientID, APIKeyEnabled: false}, nil)

	err := svc.RevokeAPIKey(context.Background(), clientID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not have an API key")
}

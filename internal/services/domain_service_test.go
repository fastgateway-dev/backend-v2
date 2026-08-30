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

// helpers -----------------------------------------------------------------

func newTestDomainService() (
	*services.DomainService,
	*mocks.MockDomainRepository,
	*mocks.MockProjectRepository,
	*mocks.MockDomainTemplateRepository,
) {
	domainRepo := new(mocks.MockDomainRepository)
	projectRepo := new(mocks.MockProjectRepository)
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainService(domainRepo, projectRepo, dtRepo, nil)
	return svc, domainRepo, projectRepo, dtRepo
}

// =========================================================================
// GetByID
// =========================================================================

func TestDomainService_GetByID_Success(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	domainID := uuid.New()
	expected := &models.Domain{
		ID:       domainID,
		Name:     "my-domain",
		Hostname: "example.com",
	}

	domainRepo.On("GetByID", domainID).Return(expected, nil)

	result, err := svc.GetByID(domainID)

	require.NoError(t, err)
	assert.Equal(t, expected.ID, result.ID)
	assert.Equal(t, "my-domain", result.Name)
	domainRepo.AssertExpectations(t)
}

func TestDomainService_GetByID_NotFound(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	domainID := uuid.New()
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("record not found"))

	result, err := svc.GetByID(domainID)

	assert.Nil(t, result)
	assert.Error(t, err)
	domainRepo.AssertExpectations(t)
}

// =========================================================================
// ListByProjectID
// =========================================================================

func TestDomainService_ListByProjectID_Success(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	projectID := uuid.New()
	domains := []models.Domain{
		{ID: uuid.New(), Name: "d1", Hostname: "d1.example.com"},
		{ID: uuid.New(), Name: "d2", Hostname: "d2.example.com"},
	}

	domainRepo.On("ListByProjectID", projectID, 1, 10, "", "", map[string]string(nil)).
		Return(domains, int64(2), nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 10, "", "", nil)

	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, result, 2)
	domainRepo.AssertExpectations(t)
}

// =========================================================================
// Create
// =========================================================================

func TestDomainService_Create_HostnameAlreadyExists(t *testing.T) {
	svc, domainRepo, _, dtRepo := newTestDomainService()

	projectID := uuid.New()
	dtID := uuid.New()
	input := &services.CreateDomainInput{
		Name:             "test",
		Hostname:         "existing.example.com",
		DomainTemplateID: dtID.String(),
	}

	domainRepo.On("ExistsByHostname", projectID, "existing.example.com").Return(true, nil)

	result, err := svc.Create(projectID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "hostname already exists in this project")
	domainRepo.AssertExpectations(t)
	dtRepo.AssertExpectations(t)
}

func TestDomainService_Create_InvalidTemplateID(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	projectID := uuid.New()
	input := &services.CreateDomainInput{
		Name:             "test",
		Hostname:         "new.example.com",
		DomainTemplateID: "not-a-uuid",
	}

	domainRepo.On("ExistsByHostname", projectID, "new.example.com").Return(false, nil)

	result, err := svc.Create(projectID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "invalid domain template ID")
	domainRepo.AssertExpectations(t)
}

func TestDomainService_Create_TemplateNotFound(t *testing.T) {
	svc, domainRepo, _, dtRepo := newTestDomainService()

	projectID := uuid.New()
	dtID := uuid.New()
	input := &services.CreateDomainInput{
		Name:             "test",
		Hostname:         "new.example.com",
		DomainTemplateID: dtID.String(),
	}

	domainRepo.On("ExistsByHostname", projectID, "new.example.com").Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(nil, errors.New("not found"))

	result, err := svc.Create(projectID, input, uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "domain template not found")
	domainRepo.AssertExpectations(t)
	dtRepo.AssertExpectations(t)
}

func TestDomainService_Create_TemplateNotActive(t *testing.T) {
	svc, domainRepo, _, dtRepo := newTestDomainService()

	projectID := uuid.New()
	dtID := uuid.New()
	input := &services.CreateDomainInput{
		Name:             "test",
		Hostname:         "new.example.com",
		DomainTemplateID: dtID.String(),
	}

	dt := &models.DomainTemplate{
		ID:        dtID,
		ProjectID: projectID,
		Name:      "tpl",
		Status:    models.DomainTemplateStatusPending,
	}

	domainRepo.On("ExistsByHostname", projectID, "new.example.com").Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)

	result, err := svc.Create(projectID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "is not active")
	domainRepo.AssertExpectations(t)
	dtRepo.AssertExpectations(t)
}

func TestDomainService_Create_TLSRequiredButMissing(t *testing.T) {
	svc, domainRepo, _, dtRepo := newTestDomainService()

	projectID := uuid.New()
	dtID := uuid.New()
	input := &services.CreateDomainInput{
		Name:             "test",
		Hostname:         "new.example.com",
		DomainTemplateID: dtID.String(),
		// TLSSecretName intentionally empty
	}

	dt := &models.DomainTemplate{
		ID:        dtID,
		ProjectID: projectID,
		Name:      "tpl",
		Status:    models.DomainTemplateStatusActive,
		TLSMode:   models.TLSModeOnly, // requires TLS secret
	}

	domainRepo.On("ExistsByHostname", projectID, "new.example.com").Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)

	result, err := svc.Create(projectID, input, uuid.New())

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "TLS secret name is required")
	domainRepo.AssertExpectations(t)
	dtRepo.AssertExpectations(t)
}

// =========================================================================
// Update
// =========================================================================

func TestDomainService_Update_Success(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	domainID := uuid.New()
	existing := &models.Domain{
		ID:       domainID,
		Name:     "old-name",
		Hostname: "example.com",
	}

	domainRepo.On("GetByID", domainID).Return(existing, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)

	input := &services.UpdateDomainInput{
		Name: "new-name",
	}

	result, err := svc.Update(domainID, input)

	require.NoError(t, err)
	assert.Equal(t, "new-name", result.Name)
	domainRepo.AssertExpectations(t)
}

func TestDomainService_Update_NotFound(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	domainID := uuid.New()
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("record not found"))

	input := &services.UpdateDomainInput{Name: "new-name"}

	result, err := svc.Update(domainID, input)

	assert.Nil(t, result)
	assert.Error(t, err)
	domainRepo.AssertExpectations(t)
}

func TestDomainService_Update_TLSSecretName(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	domainID := uuid.New()
	existing := &models.Domain{
		ID:            domainID,
		Name:          "my-domain",
		TLSSecretName: "old-secret",
	}

	domainRepo.On("GetByID", domainID).Return(existing, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)

	input := &services.UpdateDomainInput{
		TLSSecretName: "new-secret",
	}

	result, err := svc.Update(domainID, input)

	require.NoError(t, err)
	assert.Equal(t, "new-secret", result.TLSSecretName)
	domainRepo.AssertExpectations(t)
}

// =========================================================================
// GetDomainSettings
// =========================================================================

func TestDomainService_GetDomainSettings_RepoNotConfigured(t *testing.T) {
	svc, _, _, _ := newTestDomainService()
	// settingsRepo is nil by default

	result, err := svc.GetDomainSettings(uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "domain settings repository not configured")
}

func TestDomainService_GetDomainSettings_Success(t *testing.T) {
	svc, _, _, _ := newTestDomainService()

	settingsRepo := new(mocks.MockDomainSettingsRepository)
	svc.SetDomainSettingsRepository(settingsRepo)

	domainID := uuid.New()
	expected := &models.DomainSettings{
		DomainID: domainID,
	}

	settingsRepo.On("GetByDomainID", domainID).Return(expected, nil)

	result, err := svc.GetDomainSettings(domainID)

	require.NoError(t, err)
	assert.Equal(t, domainID, result.DomainID)
	settingsRepo.AssertExpectations(t)
}

func TestDomainService_GetDomainSettings_NotFound(t *testing.T) {
	svc, _, _, _ := newTestDomainService()

	settingsRepo := new(mocks.MockDomainSettingsRepository)
	svc.SetDomainSettingsRepository(settingsRepo)

	domainID := uuid.New()
	settingsRepo.On("GetByDomainID", domainID).Return(nil, errors.New("not found"))

	result, err := svc.GetDomainSettings(domainID)

	assert.Nil(t, result)
	assert.Error(t, err)
}

// =========================================================================
// Update (additional)
// =========================================================================

func TestDomainService_Update_WithLabels(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	domainID := uuid.New()
	existing := &models.Domain{
		ID:       domainID,
		Name:     "my-domain",
		Hostname: "example.com",
	}

	domainRepo.On("GetByID", domainID).Return(existing, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)

	labels := models.Labels{"env": "prod"}
	input := &services.UpdateDomainInput{Labels: labels}

	result, err := svc.Update(domainID, input)

	require.NoError(t, err)
	assert.Equal(t, labels, result.Labels)
}

func TestDomainService_Update_RepoError(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	domainID := uuid.New()
	existing := &models.Domain{ID: domainID, Name: "my-domain"}

	domainRepo.On("GetByID", domainID).Return(existing, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(errors.New("db error"))

	_, err := svc.Update(domainID, &services.UpdateDomainInput{Name: "new"})

	require.Error(t, err)
}

// =========================================================================
// ListByProjectID (additional)
// =========================================================================

func TestDomainService_ListByProjectID_Empty(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	projectID := uuid.New()
	domainRepo.On("ListByProjectID", projectID, 1, 10, "", "", map[string]string(nil)).
		Return([]models.Domain{}, int64(0), nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 10, "", "", nil)

	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Len(t, result, 0)
}

func TestDomainService_ListByProjectID_Error(t *testing.T) {
	svc, domainRepo, _, _ := newTestDomainService()

	projectID := uuid.New()
	domainRepo.On("ListByProjectID", projectID, 1, 10, "", "", map[string]string(nil)).
		Return([]models.Domain(nil), int64(0), errors.New("db error"))

	_, _, err := svc.ListByProjectID(projectID, 1, 10, "", "", nil)

	require.Error(t, err)
}

// =========================================================================
// UpdateDomainSettings
// =========================================================================

func newTestDomainServiceWithK8s() (
	*services.DomainService,
	*mocks.MockDomainRepository,
	*mocks.MockDomainSettingsRepository,
	*mocks.MockKubernetesService,
) {
	domainRepo := new(mocks.MockDomainRepository)
	settingsRepo := new(mocks.MockDomainSettingsRepository)
	k8sMock := new(mocks.MockKubernetesService)
	svc := services.NewDomainService(domainRepo, nil, nil, k8sMock)
	svc.SetDomainSettingsRepository(settingsRepo)
	return svc, domainRepo, settingsRepo, k8sMock
}

func TestDomainService_UpdateDomainSettings_MTLSEnabledWithCAs_UpdatesCTPWithCARefs(t *testing.T) {
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithK8s()

	domainID := uuid.New()
	projectID := uuid.New()
	domain := &models.Domain{
		ID:             domainID,
		ProjectID:      projectID,
		K8sGatewayName: "test-gw",
		Namespace:      "fastgateway-system",
	}
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	settingsRepo.On("Upsert", mock.AnythingOfType("*models.DomainSettings")).Return(nil)

	// applyEnvoyGatewayClientTrafficPolicy calls CreateClientTrafficPolicy
	k8sMock.On("CreateClientTrafficPolicy", mock.Anything, projectID,
		mock.MatchedBy(func(config *services.ClientTrafficPolicyConfig) bool {
			if config.ClientValidation == nil {
				return false
			}
			// Should have 1 CA ref pointing to the domain CA secret directly (no merged secret)
			if len(config.ClientValidation.CACertificateRefs) != 1 {
				return false
			}
			return config.ClientValidation.CACertificateRefs[0].Name == "my-ca-secret"
		})).Return(nil)
	// BTP and extension policy are nil → delete path
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-btp").Return(nil)
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", "test-gw-eep-extproc").Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-eep").Return(nil)
	settingsRepo.On("GetByDomainID", domainID).Return(&models.DomainSettings{DomainID: domainID}, nil)

	input := &services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{
			Enabled:  true,
			Optional: false,
			CACerts: []models.MTLSCACert{
				{ID: "ca1", Name: "Root CA", SecretName: "my-ca-secret", SecretKey: "ca.crt"},
			},
		},
	}

	result, err := svc.UpdateDomainSettings(domainID, input)

	require.NoError(t, err)
	assert.NotNil(t, result)
	// Should NOT call GetSecretData or CreateOrUpdateSecret (no merging)
	k8sMock.AssertNotCalled(t, "GetSecretData", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	k8sMock.AssertNotCalled(t, "CreateOrUpdateSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	k8sMock.AssertExpectations(t)
}

func TestDomainService_UpdateDomainSettings_MTLSEnabledNoCAs_SkipsRegenerate(t *testing.T) {
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithK8s()

	domainID := uuid.New()
	projectID := uuid.New()
	domain := &models.Domain{
		ID:             domainID,
		ProjectID:      projectID,
		K8sGatewayName: "test-gw",
		Namespace:      "fastgateway-system",
	}
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	settingsRepo.On("Upsert", mock.AnythingOfType("*models.DomainSettings")).Return(nil)
	// Only applyEnvoyGatewayClientTrafficPolicy called (no regenerate since no CAs)
	k8sMock.On("CreateClientTrafficPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.ClientTrafficPolicyConfig")).Return(nil)
	// BTP and extension policy are nil → delete path
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-btp").Return(nil)
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", "test-gw-eep-extproc").Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-eep").Return(nil)
	settingsRepo.On("GetByDomainID", domainID).Return(&models.DomainSettings{DomainID: domainID}, nil)

	input := &services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{
			Enabled:  true,
			Optional: false,
			CACerts:  []models.MTLSCACert{}, // no CAs
		},
	}

	result, err := svc.UpdateDomainSettings(domainID, input)

	require.NoError(t, err)
	assert.NotNil(t, result)
	// Should NOT call CreateOrUpdateSecret (no regeneration needed)
	k8sMock.AssertNotCalled(t, "CreateOrUpdateSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	k8sMock.AssertNotCalled(t, "GetSecretData", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainService_UpdateDomainSettings_NoMTLS_SkipsRegenerate(t *testing.T) {
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithK8s()

	domainID := uuid.New()
	projectID := uuid.New()
	domain := &models.Domain{
		ID:             domainID,
		ProjectID:      projectID,
		K8sGatewayName: "test-gw",
		Namespace:      "fastgateway-system",
	}
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	settingsRepo.On("Upsert", mock.AnythingOfType("*models.DomainSettings")).Return(nil)
	k8sMock.On("CreateClientTrafficPolicy", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.ClientTrafficPolicyConfig")).Return(nil)
	// BTP and extension policy are nil → delete path
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-btp").Return(nil)
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", "test-gw-eep-extproc").Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-eep").Return(nil)
	settingsRepo.On("GetByDomainID", domainID).Return(&models.DomainSettings{DomainID: domainID}, nil)

	timeout := &models.TimeoutConfig{
		HTTP: &models.HTTPTimeoutConfig{
			RequestReceivedTimeout: strPtr("30s"),
		},
	}
	input := &services.UpdateDomainSettingsInput{
		Timeout: timeout,
	}

	result, err := svc.UpdateDomainSettings(domainID, input)

	require.NoError(t, err)
	assert.NotNil(t, result)
	// No mTLS → no regeneration
	k8sMock.AssertNotCalled(t, "CreateOrUpdateSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	k8sMock.AssertNotCalled(t, "GetSecretData", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainService_UpdateDomainSettings_RepoNotConfigured(t *testing.T) {
	svc, _, _, _ := newTestDomainService()
	// settingsRepo is nil by default

	_, err := svc.UpdateDomainSettings(uuid.New(), &services.UpdateDomainSettingsInput{})

	assert.EqualError(t, err, "domain settings repository not configured")
}

func TestDomainService_UpdateDomainSettings_DomainNotFound(t *testing.T) {
	svc, domainRepo, settingsRepo, _ := newTestDomainServiceWithK8s()

	domainID := uuid.New()
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))
	_ = settingsRepo // unused but part of helper

	_, err := svc.UpdateDomainSettings(domainID, &services.UpdateDomainSettingsInput{})

	assert.Contains(t, err.Error(), "domain not found")
}

func TestDomainService_UpdateDomainSettings_EmptyConfig_DeletesSettings(t *testing.T) {
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithK8s()

	domainID := uuid.New()
	projectID := uuid.New()
	domain := &models.Domain{
		ID:             domainID,
		ProjectID:      projectID,
		K8sGatewayName: "test-gw",
		Namespace:      "fastgateway-system",
	}
	domainRepo.On("GetByID", domainID).Return(domain, nil)
	k8sMock.On("DeleteClientTrafficPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-ctp").Return(nil)
	// All empty → delete BTP and extension policy too
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-btp").Return(nil)
	k8sMock.On("DeleteBackend", mock.Anything, projectID, "fastgateway-system", "test-gw-eep-extproc").Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-eep").Return(nil)
	settingsRepo.On("DeleteByDomainID", domainID).Return(nil)

	// Empty input → all nil fields → config.IsEmpty() == true
	input := &services.UpdateDomainSettingsInput{}

	result, err := svc.UpdateDomainSettings(domainID, input)

	require.NoError(t, err)
	assert.Nil(t, result)
	k8sMock.AssertCalled(t, "DeleteClientTrafficPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-ctp")
	settingsRepo.AssertCalled(t, "DeleteByDomainID", domainID)
}

// strPtr is a helper for string pointer
func strPtr(s string) *string {
	return &s
}

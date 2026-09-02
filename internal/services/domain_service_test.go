package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// helpers -----------------------------------------------------------------

// newDefaultClientAttachmentRepoStub stands in for the ClientAttachmentRepo
// dependency, which is now required (Phase 2E Task 3). Before Task 3, a nil
// clientAttachmentRepo made collectCASecretRefs (domain_service.go:812) skip
// adding client mTLS CA refs entirely. This stub reproduces the same "no
// client CA refs" outcome through a real, empty result instead of the
// skipped branch, so tests that never cared about client mTLS attachments
// keep seeing the same effective behaviour.
func newDefaultClientAttachmentRepoStub() *mocks.MockClientAttachmentRepository {
	repo := new(mocks.MockClientAttachmentRepository)
	repo.On("GetMTLSClientsForDomain", mock.Anything).Return([]models.Client{}, nil).Maybe()
	return repo
}

// newDefaultBtpRepoStub stands in for the BtpRepo dependency, which is now
// required (Phase 2E Task 3). Before Task 3, a nil btpRepo made every
// `s.btpRepo != nil` guard in domain_service.go (Delete:355,
// applyDomainBackendTrafficPolicy:1342/1350, GenerateYAMLs:915,
// PreviewSettingsChanges:1098) skip the DB read/write entirely. This stub
// reproduces the same "no domain-level BTP in the database" outcome through
// real, empty/no-op returns instead of the skipped branch.
func newDefaultBtpRepoStub() *mocks.MockBackendTrafficPolicyRepository {
	repo := new(mocks.MockBackendTrafficPolicyRepository)
	repo.On("GetByDomainID", mock.Anything).Return((*models.BackendTrafficPolicy)(nil), nil).Maybe()
	repo.On("DeleteByDomainID", mock.Anything).Return(nil).Maybe()
	repo.On("Upsert", mock.Anything).Return(nil).Maybe()
	return repo
}

// newDefaultExtPolicyRepoStub is the ExtPolicyRepo counterpart of
// newDefaultBtpRepoStub. Before Task 3, a nil extPolicyRepo made every
// `s.extPolicyRepo != nil` guard in domain_service.go (Delete:366,
// applyDomainEnvoyExtensionPolicy:1379/1388, GenerateYAMLs:933,
// PreviewSettingsChanges:1125) skip the DB read/write entirely.
func newDefaultExtPolicyRepoStub() *mocks.MockEnvoyExtensionPolicyRepository {
	repo := new(mocks.MockEnvoyExtensionPolicyRepository)
	repo.On("GetByDomainID", mock.Anything).Return((*models.EnvoyExtensionPolicy)(nil), nil).Maybe()
	repo.On("DeleteByDomainID", mock.Anything).Return(nil).Maybe()
	repo.On("Upsert", mock.Anything).Return(nil).Maybe()
	return repo
}

// noTemplateLookup answers "no template resolved", reproducing the pre-2E
// behaviour of an unset dtService: the guard at domain_service.go:869
// (`if s.dtService != nil && ...`) skipped the lookup entirely, so the
// generated Gateway carried no template annotations. Returning a nil template
// leaves the template-annotation resolution on exactly the same branch.
// Phase 2F Task 1 moved the builder itself to domainplan.BuildGatewayConfig
// (internal/domainplan/gateway.go); the lookup that feeds it now lives in
// DomainService.templateAnnotations.
// Phase 2E Task 9 deleted that nil half: DtService is required, so only the
// DomainTemplateID check remains.
type noTemplateLookup struct{}

func (noTemplateLookup) GetByID(uuid.UUID) (*models.DomainTemplate, error) { return nil, nil }

// disabledAIReviewer answers "AI is not configured", reproducing the pre-2E
// behaviour of an unset aiService: the guards at domain_service.go:1005 and
// :1152 (`s.aiService != nil && s.aiService.IsEnabled()`) skipped the review.
// IsEnabled returning false is the production way to say the same thing --
// NewAIService always returns a usable service, so nil-ness was only ever a
// wiring accident. Review therefore must never be called; it is left
// unimplemented so a regression panics loudly.
// Phase 2E Task 9 deleted the nil halves of both conditions: AiService is
// required, so IsEnabled is the only test left.
type disabledAIReviewer struct{}

func (disabledAIReviewer) IsEnabled() bool { return false }

func (disabledAIReviewer) Review(context.Context, uuid.UUID, ai.ReviewRequest) (*ai.ReviewResult, error) {
	panic("disabledAIReviewer.Review: IsEnabled() is false, Review must not be called")
}

func newTestDomainService() (
	*services.DomainService,
	*mocks.MockDomainRepository,
	*mocks.MockProjectRepository,
	*mocks.MockDomainTemplateRepository,
) {
	domainRepo := new(mocks.MockDomainRepository)
	projectRepo := new(mocks.MockProjectRepository)
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainService(services.DomainServiceDeps{
		DomainRepo:           domainRepo,
		ProjectRepo:          projectRepo,
		DomainTemplateRepo:   dtRepo,
		K8sGateways:          new(mocks.MockKubernetesService),
		K8sSecrets:           new(mocks.MockKubernetesService),
		K8sBackends:          new(mocks.MockKubernetesService),
		K8sPolicies:          new(mocks.MockKubernetesService),
		K8sRefGrants:         new(mocks.MockKubernetesService),
		SettingsRepo:         new(mocks.MockDomainSettingsRepository),
		ClientAttachmentRepo: newDefaultClientAttachmentRepoStub(),
		BtpRepo:              newDefaultBtpRepoStub(),
		ExtPolicyRepo:        newDefaultExtPolicyRepoStub(),
		ProjectNamespaceRepo: new(mocks.MockProjectNamespaceRepository),
		DtService:            noTemplateLookup{},
		AiService:            disabledAIReviewer{},
	})
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

// TestDomainService_GetDomainSettings_RepoNotConfigured is gone (Phase 2E
// Task 3). It pinned the nil-guard at domain_service.go:508-513
// ("domain settings repository not configured"), which fires only when
// SettingsRepo is unset. SettingsRepo is now a required
// DomainServiceDeps field checked by NewDomainService, so a DomainService
// can no longer be constructed with it unset through the public API this
// test file uses. Phase 2E Task 9 then deleted the guard itself, so
// GetDomainSettings now goes straight to the repository.

func TestDomainService_GetDomainSettings_Success(t *testing.T) {
	svc, _, settingsRepo, _ := newTestDomainServiceWithK8s()

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
	svc, _, settingsRepo, _ := newTestDomainServiceWithK8s()

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
	svc := services.NewDomainService(services.DomainServiceDeps{
		DomainRepo:           domainRepo,
		ProjectRepo:          new(mocks.MockProjectRepository),
		DomainTemplateRepo:   new(mocks.MockDomainTemplateRepository),
		K8sGateways:          k8sMock,
		K8sSecrets:           k8sMock,
		K8sBackends:          k8sMock,
		K8sPolicies:          k8sMock,
		K8sRefGrants:         k8sMock,
		SettingsRepo:         settingsRepo,
		ClientAttachmentRepo: newDefaultClientAttachmentRepoStub(),
		BtpRepo:              newDefaultBtpRepoStub(),
		ExtPolicyRepo:        newDefaultExtPolicyRepoStub(),
		ProjectNamespaceRepo: new(mocks.MockProjectNamespaceRepository),
		DtService:            noTemplateLookup{},
		AiService:            disabledAIReviewer{},
	})
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
		mock.MatchedBy(func(config *kubernetes.ClientTrafficPolicyConfig) bool {
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

	result, warnings, err := svc.UpdateDomainSettings(domainID, input)

	require.NoError(t, err)
	assert.NotNil(t, result)
	// Domain CAs are present -> no "no CA available" warning (test case 3,
	// mtls-warning-brief.md).
	assert.Empty(t, warnings)
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

	// SINCE this change: UpdateDomainSettings still ACCEPTS mtls.enabled=true
	// with zero domain-level CA certs (the withdrawn "at least one CA
	// required" rule stays withdrawn from the write path -- see
	// mtls-warning-brief.md test case 8). It now additionally surfaces a
	// warning for exactly this case; this test does not assert on the
	// warning (see TestDomainService_UpdateDomainSettings_MTLSNoCAAnywhere_
	// ReturnsWarning for that), only that acceptance is unchanged.
	result, _, err := svc.UpdateDomainSettings(domainID, input)

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

	result, warnings, err := svc.UpdateDomainSettings(domainID, input)

	require.NoError(t, err)
	assert.NotNil(t, result)
	// No mTLS → no regeneration, and no "no CA available" warning (test case
	// 4, mtls-warning-brief.md).
	assert.Empty(t, warnings)
	k8sMock.AssertNotCalled(t, "CreateOrUpdateSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	k8sMock.AssertNotCalled(t, "GetSecretData", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestDomainService_UpdateDomainSettings_RepoNotConfigured is gone (Phase 2E
// Task 3), for the same reason as TestDomainService_GetDomainSettings_
// RepoNotConfigured above: it pinned the nil-guard at
// domain_service.go ("domain settings repository not configured"), which
// became unreachable once SettingsRepo was a required DomainServiceDeps
// field. Phase 2E Task 9 deleted that guard.

func TestDomainService_UpdateDomainSettings_DomainNotFound(t *testing.T) {
	svc, domainRepo, settingsRepo, _ := newTestDomainServiceWithK8s()

	domainID := uuid.New()
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("not found"))
	_ = settingsRepo // unused but part of helper

	_, _, err := svc.UpdateDomainSettings(domainID, &services.UpdateDomainSettingsInput{})

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

	result, warnings, err := svc.UpdateDomainSettings(domainID, input)

	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Empty(t, warnings)
	k8sMock.AssertCalled(t, "DeleteClientTrafficPolicy", mock.Anything, projectID, "fastgateway-system", "test-gw-ctp")
	settingsRepo.AssertCalled(t, "DeleteByDomainID", domainID)
}

// ---------------------------------------------------------------------------
// UpdateDomainSettings -- mTLS no-CA-available warning
// (mtls-warning-brief.md, Change 1)
// ---------------------------------------------------------------------------

// newTestDomainServiceWithClientAttachments is the newTestDomainServiceWithK8s
// variant needed for test case 2 of the brief: "one active mTLS client
// attachment supplying a CA -> no warning". newDefaultClientAttachmentRepoStub
// always answers GetMTLSClientsForDomain with an empty slice, so this swaps
// in a stub that returns the given clients instead.
func newTestDomainServiceWithClientAttachments(clients []models.Client) (
	*services.DomainService,
	*mocks.MockDomainRepository,
	*mocks.MockDomainSettingsRepository,
	*mocks.MockKubernetesService,
) {
	domainRepo := new(mocks.MockDomainRepository)
	settingsRepo := new(mocks.MockDomainSettingsRepository)
	k8sMock := new(mocks.MockKubernetesService)
	attachmentRepo := new(mocks.MockClientAttachmentRepository)
	attachmentRepo.On("GetMTLSClientsForDomain", mock.Anything).Return(clients, nil).Maybe()
	svc := services.NewDomainService(services.DomainServiceDeps{
		DomainRepo:           domainRepo,
		ProjectRepo:          new(mocks.MockProjectRepository),
		DomainTemplateRepo:   new(mocks.MockDomainTemplateRepository),
		K8sGateways:          k8sMock,
		K8sSecrets:           k8sMock,
		K8sBackends:          k8sMock,
		K8sPolicies:          k8sMock,
		K8sRefGrants:         k8sMock,
		SettingsRepo:         settingsRepo,
		ClientAttachmentRepo: attachmentRepo,
		BtpRepo:              newDefaultBtpRepoStub(),
		ExtPolicyRepo:        newDefaultExtPolicyRepoStub(),
		ProjectNamespaceRepo: new(mocks.MockProjectNamespaceRepository),
		DtService:            noTemplateLookup{},
		AiService:            disabledAIReviewer{},
	})
	return svc, domainRepo, settingsRepo, k8sMock
}

// mtlsWarningTestDomain returns a domain fixture for the warning tests below,
// shaped like the one used throughout the existing UpdateDomainSettings
// tests in this file.
func mtlsWarningTestDomain() *models.Domain {
	return &models.Domain{
		ID:             uuid.New(),
		ProjectID:      uuid.New(),
		K8sGatewayName: "test-gw",
		Namespace:      "fastgateway-system",
	}
}

// setupMTLSWarningTestMocks wires the mocks every warning test below needs:
// domain lookup, settings upsert/reload, CTP apply, and the BTP/extension-
// policy delete path (both are nil in every warning test's input, so
// UpdateDomainSettings takes the "delete" branch for each).
func setupMTLSWarningTestMocks(
	domainRepo *mocks.MockDomainRepository,
	settingsRepo *mocks.MockDomainSettingsRepository,
	k8sMock *mocks.MockKubernetesService,
	domain *models.Domain,
) {
	domainRepo.On("GetByID", domain.ID).Return(domain, nil)
	settingsRepo.On("Upsert", mock.AnythingOfType("*models.DomainSettings")).Return(nil)
	k8sMock.On("CreateClientTrafficPolicy", mock.Anything, domain.ProjectID, mock.AnythingOfType("*kubernetes.ClientTrafficPolicyConfig")).Return(nil)
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, domain.ProjectID, domain.Namespace, domain.K8sGatewayName+"-btp").Return(nil)
	k8sMock.On("DeleteBackend", mock.Anything, domain.ProjectID, domain.Namespace, domain.K8sGatewayName+"-eep-extproc").Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, domain.ProjectID, domain.Namespace, domain.K8sGatewayName+"-eep").Return(nil)
	settingsRepo.On("GetByDomainID", domain.ID).Return(&models.DomainSettings{DomainID: domain.ID}, nil)
}

const mtlsNoCAWarningText = "mTLS is enabled but no CA certificates are available for this domain (none configured directly, and no active mTLS clients attached). The domain will reject client connections until a CA is added or an mTLS client is attached."

// Test case 1 (mtls-warning-brief.md): mTLS enabled, no domain CAs, no mTLS
// client attachments -> warning returned.
func TestDomainService_UpdateDomainSettings_MTLSNoCAAnywhere_ReturnsWarning(t *testing.T) {
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithK8s()
	domain := mtlsWarningTestDomain()
	setupMTLSWarningTestMocks(domainRepo, settingsRepo, k8sMock, domain)

	input := &services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{Enabled: true},
	}

	result, warnings, err := svc.UpdateDomainSettings(domain.ID, input)

	require.NoError(t, err)
	assert.NotNil(t, result)
	require.Len(t, warnings, 1)
	assert.Equal(t, mtlsNoCAWarningText, warnings[0])
}

// Test case 2 (mtls-warning-brief.md): mTLS enabled, no domain CAs, one
// active mTLS client attachment supplying a CA -> NO warning. This is the
// case that must not produce noise, and the whole reason the warning is
// computed from the RESOLVED ref list (collectCASecretRefs' merged output)
// rather than from input.MTLS.CACerts alone.
func TestDomainService_UpdateDomainSettings_MTLSNoCAButClientAttachmentSuppliesOne_NoWarning(t *testing.T) {
	client := models.Client{ID: uuid.New(), MTLSCASecret: "client-supplied-ca-secret"}
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithClientAttachments([]models.Client{client})
	domain := mtlsWarningTestDomain()
	setupMTLSWarningTestMocks(domainRepo, settingsRepo, k8sMock, domain)

	input := &services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{Enabled: true},
	}

	result, warnings, err := svc.UpdateDomainSettings(domain.ID, input)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, warnings)
}

// Test case 5-7 wiring (mtls-warning-brief.md, Change 2): an invalid mTLS
// shape (bad SAN type here) is rejected by UpdateDomainSettings before any
// repository or Kubernetes call, via the newly-wired ValidateShape(). The
// per-rule shape checks themselves are pinned directly against
// DomainMTLSConfig.ValidateShape in internal/models/domain_settings_test.go;
// this test only pins the wiring.
func TestDomainService_UpdateDomainSettings_InvalidMTLSShape_RejectedBeforeAnyWrite(t *testing.T) {
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithK8s()
	domain := mtlsWarningTestDomain()
	domainRepo.On("GetByID", domain.ID).Return(domain, nil)

	input := &services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{
			Enabled:      true,
			SANWhitelist: []models.MTLSSANEntry{{Type: "EMAIL", Value: "test@example.com"}},
		},
	}

	result, warnings, err := svc.UpdateDomainSettings(domain.ID, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mTLS config")
	assert.Nil(t, result)
	assert.Nil(t, warnings)
	settingsRepo.AssertNotCalled(t, "Upsert", mock.Anything)
	k8sMock.AssertNotCalled(t, "CreateClientTrafficPolicy", mock.Anything, mock.Anything, mock.Anything)
}

// strPtr is a helper for string pointer
func strPtr(s string) *string {
	return &s
}

// ---------------------------------------------------------------------------
// NewDomainService
// ---------------------------------------------------------------------------

func fullDomainServiceDeps() services.DomainServiceDeps {
	return services.DomainServiceDeps{
		DomainRepo:           new(mocks.MockDomainRepository),
		ProjectRepo:          new(mocks.MockProjectRepository),
		DomainTemplateRepo:   new(mocks.MockDomainTemplateRepository),
		K8sGateways:          new(mocks.MockKubernetesService),
		K8sSecrets:           new(mocks.MockKubernetesService),
		K8sBackends:          new(mocks.MockKubernetesService),
		K8sPolicies:          new(mocks.MockKubernetesService),
		K8sRefGrants:         new(mocks.MockKubernetesService),
		SettingsRepo:         new(mocks.MockDomainSettingsRepository),
		ClientAttachmentRepo: newDefaultClientAttachmentRepoStub(),
		BtpRepo:              newDefaultBtpRepoStub(),
		ExtPolicyRepo:        newDefaultExtPolicyRepoStub(),
		ProjectNamespaceRepo: new(mocks.MockProjectNamespaceRepository),
		DtService:            noTemplateLookup{},
		AiService:            disabledAIReviewer{},
	}
}

func TestNewDomainService_RequiresEveryDependency(t *testing.T) {
	require.NotPanics(t, func() { services.NewDomainService(fullDomainServiceDeps()) })

	cases := map[string]func(*services.DomainServiceDeps){
		"DomainRepo":           func(d *services.DomainServiceDeps) { d.DomainRepo = nil },
		"ProjectRepo":          func(d *services.DomainServiceDeps) { d.ProjectRepo = nil },
		"DomainTemplateRepo":   func(d *services.DomainServiceDeps) { d.DomainTemplateRepo = nil },
		"K8sGateways":          func(d *services.DomainServiceDeps) { d.K8sGateways = nil },
		"K8sSecrets":           func(d *services.DomainServiceDeps) { d.K8sSecrets = nil },
		"K8sBackends":          func(d *services.DomainServiceDeps) { d.K8sBackends = nil },
		"K8sPolicies":          func(d *services.DomainServiceDeps) { d.K8sPolicies = nil },
		"K8sRefGrants":         func(d *services.DomainServiceDeps) { d.K8sRefGrants = nil },
		"SettingsRepo":         func(d *services.DomainServiceDeps) { d.SettingsRepo = nil },
		"ClientAttachmentRepo": func(d *services.DomainServiceDeps) { d.ClientAttachmentRepo = nil },
		"BtpRepo":              func(d *services.DomainServiceDeps) { d.BtpRepo = nil },
		"ExtPolicyRepo":        func(d *services.DomainServiceDeps) { d.ExtPolicyRepo = nil },
		"ProjectNamespaceRepo": func(d *services.DomainServiceDeps) { d.ProjectNamespaceRepo = nil },
		"DtService":            func(d *services.DomainServiceDeps) { d.DtService = nil },
		"AiService":            func(d *services.DomainServiceDeps) { d.AiService = nil },
	}
	for name, breakIt := range cases {
		t.Run("nil "+name, func(t *testing.T) {
			d := fullDomainServiceDeps()
			breakIt(&d)
			assert.PanicsWithValue(t,
				"services.NewDomainService: missing required dependency: "+name,
				func() { services.NewDomainService(d) })
		})
	}
}

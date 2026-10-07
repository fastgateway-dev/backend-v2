package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/domainplan"
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
	*mocks.MockKubernetesService,
	*mocks.MockProjectNamespaceRepository,
	*mocks.MockManagedCertificateRepository,
) {
	domainRepo := new(mocks.MockDomainRepository)
	projectRepo := new(mocks.MockProjectRepository)
	dtRepo := new(mocks.MockDomainTemplateRepository)
	k8sMock := new(mocks.MockKubernetesService)
	projectNamespaceRepo := new(mocks.MockProjectNamespaceRepository)
	managedCertRepo := new(mocks.MockManagedCertificateRepository)
	svc := services.NewDomainService(services.DomainServiceDeps{
		DomainRepo:         domainRepo,
		ProjectRepo:        projectRepo,
		DomainTemplateRepo: dtRepo,
		// All five K8s roles share one mock instance here (as
		// newTestDomainServiceWithK8s already does below) so that a single
		// applyGateway call -- UpdateGateway plus the syncReferenceGrants
		// fan-out it now triggers (Update/AttachCertificate/
		// DetachCertificate) -- can be set up against one object.
		K8sGateways:          k8sMock,
		K8sSecrets:           k8sMock,
		K8sBackends:          k8sMock,
		K8sPolicies:          k8sMock,
		K8sRefGrants:         k8sMock,
		SettingsRepo:         new(mocks.MockDomainSettingsRepository),
		ClientAttachmentRepo: newDefaultClientAttachmentRepoStub(),
		BtpRepo:              newDefaultBtpRepoStub(),
		ExtPolicyRepo:        newDefaultExtPolicyRepoStub(),
		ProjectNamespaceRepo: projectNamespaceRepo,
		DtService:            noTemplateLookup{},
		AiService:            disabledAIReviewer{},
		ManagedCertLookup:    managedCertRepo,
	})
	return svc, domainRepo, projectRepo, dtRepo, k8sMock, projectNamespaceRepo, managedCertRepo
}

// expectSuccessfulApplyGateway wires the mocks applyGateway's shared path
// needs on a happy path: UpdateGateway succeeds, and the syncReferenceGrants
// call it now makes on every apply (Update/AttachCertificate/
// DetachCertificate -- previously Update never called it at all) sees an
// otherwise-empty project: no other domains and no project namespaces, so it
// reconciles down to a single no-op DeleteReferenceGrant for the shared
// secrets ReferenceGrant in fastgateway-system.
func expectSuccessfulApplyGateway(
	domainRepo *mocks.MockDomainRepository,
	projectNamespaceRepo *mocks.MockProjectNamespaceRepository,
	k8sMock *mocks.MockKubernetesService,
	projectID uuid.UUID,
) {
	k8sMock.On("UpdateGateway", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).Return(nil)
	domainRepo.On("ListByProjectID", projectID, 1, 10000, "", "", mock.Anything).
		Return([]models.Domain{}, int64(0), nil)
	projectNamespaceRepo.On("ListByProjectID", projectID).Return([]models.ProjectNamespace{}, nil)
	k8sMock.On("DeleteReferenceGrant", mock.Anything, projectID, kubernetes.FastGatewayNamespace, mock.Anything).Return(nil)
}

// =========================================================================
// GetByID
// =========================================================================

func TestDomainService_GetByID_Success(t *testing.T) {
	svc, domainRepo, _, _, _, _, _ := newTestDomainService()

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
	svc, domainRepo, _, _, _, _, _ := newTestDomainService()

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
	svc, domainRepo, _, _, _, _, _ := newTestDomainService()

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
	svc, domainRepo, _, dtRepo, _, _, _ := newTestDomainService()

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
	svc, domainRepo, _, _, _, _, _ := newTestDomainService()

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
	svc, domainRepo, _, dtRepo, _, _, _ := newTestDomainService()

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
	svc, domainRepo, _, dtRepo, _, _, _ := newTestDomainService()

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
	svc, domainRepo, _, dtRepo, _, _, _ := newTestDomainService()

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
	svc, domainRepo, _, _, k8sMock, projectNamespaceRepo, _ := newTestDomainService()

	domainID := uuid.New()
	projectID := uuid.New()
	existing := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Name:      "old-name",
		Hostname:  "example.com",
	}

	domainRepo.On("GetByID", domainID).Return(existing, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)
	expectSuccessfulApplyGateway(domainRepo, projectNamespaceRepo, k8sMock, projectID)

	input := &services.UpdateDomainInput{
		Name: "new-name",
	}

	result, err := svc.Update(domainID, input)

	require.NoError(t, err)
	assert.Equal(t, "new-name", result.Name)
	k8sMock.AssertCalled(t, "UpdateGateway", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GatewayConfig"))
}

func TestDomainService_Update_NotFound(t *testing.T) {
	svc, domainRepo, _, _, _, _, _ := newTestDomainService()

	domainID := uuid.New()
	domainRepo.On("GetByID", domainID).Return(nil, errors.New("record not found"))

	input := &services.UpdateDomainInput{Name: "new-name"}

	result, err := svc.Update(domainID, input)

	assert.Nil(t, result)
	assert.Error(t, err)
	domainRepo.AssertExpectations(t)
}

// TestDomainService_Update_TLSSecretName is the fill-the-TODO regression:
// domain_service.go:371 used to read "// TODO: Update Kubernetes resources",
// making a TLSSecretName change a DB-only no-op against the live Gateway.
// It now goes through applyGateway, which must call UpdateGateway.
func TestDomainService_Update_TLSSecretName(t *testing.T) {
	svc, domainRepo, _, _, k8sMock, projectNamespaceRepo, _ := newTestDomainService()

	domainID := uuid.New()
	projectID := uuid.New()
	existing := &models.Domain{
		ID:            domainID,
		ProjectID:     projectID,
		Name:          "my-domain",
		TLSSecretName: "old-secret",
	}

	domainRepo.On("GetByID", domainID).Return(existing, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)
	expectSuccessfulApplyGateway(domainRepo, projectNamespaceRepo, k8sMock, projectID)

	input := &services.UpdateDomainInput{
		TLSSecretName: "new-secret",
	}

	result, err := svc.Update(domainID, input)

	require.NoError(t, err)
	assert.Equal(t, "new-secret", result.TLSSecretName)
	k8sMock.AssertCalled(t, "UpdateGateway", mock.Anything, projectID, mock.MatchedBy(func(cfg *kubernetes.GatewayConfig) bool {
		return cfg.TLSSecretName == "new-secret"
	}))
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
	svc, domainRepo, _, _, k8sMock, projectNamespaceRepo, _ := newTestDomainService()

	domainID := uuid.New()
	projectID := uuid.New()
	existing := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Name:      "my-domain",
		Hostname:  "example.com",
	}

	domainRepo.On("GetByID", domainID).Return(existing, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)
	expectSuccessfulApplyGateway(domainRepo, projectNamespaceRepo, k8sMock, projectID)

	labels := models.Labels{"env": "prod"}
	input := &services.UpdateDomainInput{Labels: labels}

	result, err := svc.Update(domainID, input)

	require.NoError(t, err)
	assert.Equal(t, labels, result.Labels)
}

func TestDomainService_Update_RepoError(t *testing.T) {
	svc, domainRepo, _, _, _, _, _ := newTestDomainService()

	domainID := uuid.New()
	existing := &models.Domain{ID: domainID, Name: "my-domain"}

	domainRepo.On("GetByID", domainID).Return(existing, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(errors.New("db error"))

	_, err := svc.Update(domainID, &services.UpdateDomainInput{Name: "new"})

	require.Error(t, err)
}

// TestDomainService_Update_GatewayApplyError pins the Phase 3b gap: Update's
// applyGateway call can now fail against a real cluster (e.g. a transient
// tenant-cluster outage), and that failure must be distinguishable from an
// input/validation error so the handler can map it to 500 instead of 400.
// Update must return an error that unwraps to services.ErrGatewayApply.
func TestDomainService_Update_GatewayApplyError(t *testing.T) {
	svc, domainRepo, _, _, k8sMock, _, _ := newTestDomainService()

	domainID := uuid.New()
	projectID := uuid.New()
	existing := &models.Domain{
		ID:        domainID,
		ProjectID: projectID,
		Name:      "my-domain",
		Hostname:  "example.com",
	}

	domainRepo.On("GetByID", domainID).Return(existing, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)
	k8sMock.On("UpdateGateway", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).
		Return(errors.New("cluster unreachable"))

	result, err := svc.Update(domainID, &services.UpdateDomainInput{Name: "new-name"})

	require.Error(t, err)
	assert.True(t, errors.Is(err, services.ErrGatewayApply))
	// applyGateway still returns the domain alongside the error (Update's
	// existing "return domain, err" path), so the handler can still log it.
	require.NotNil(t, result)
}

// =========================================================================
// AttachCertificate / DetachCertificate
// =========================================================================

// readyServerCert returns a managed certificate fixture that passes every
// AttachCertificate validation gate: same project, usage=server, status=ready.
func readyServerCert(id, projectID uuid.UUID) *models.ManagedCertificate {
	return &models.ManagedCertificate{
		ID:        id,
		ProjectID: projectID,
		Usage:     models.ManagedCertUsageServer,
		Status:    models.ManagedCertStatusReady,
	}
}

func TestDomainService_AttachCertificate_Success(t *testing.T) {
	svc, domainRepo, _, _, k8sMock, projectNamespaceRepo, certRepo := newTestDomainService()

	domainID := uuid.New()
	certID := uuid.New()
	projectID := uuid.New()
	domain := &models.Domain{ID: domainID, ProjectID: projectID, Name: "my-domain"}
	cert := readyServerCert(certID, projectID)

	domainRepo.On("GetByID", domainID).Return(domain, nil)
	certRepo.On("GetByID", certID).Return(cert, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)
	expectSuccessfulApplyGateway(domainRepo, projectNamespaceRepo, k8sMock, projectID)

	result, err := svc.AttachCertificate(domainID, certID, projectID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ManagedCertificateID)
	assert.Equal(t, certID, *result.ManagedCertificateID)
	k8sMock.AssertCalled(t, "UpdateGateway", mock.Anything, projectID, mock.Anything)
	k8sMock.AssertNumberOfCalls(t, "UpdateGateway", 1)
}

func TestDomainService_AttachCertificate_DomainWrongProject_NotFound(t *testing.T) {
	svc, domainRepo, _, _, k8sMock, _, certRepo := newTestDomainService()

	domainID := uuid.New()
	certID := uuid.New()
	domainProjectID := uuid.New()
	callerProjectID := uuid.New()
	domain := &models.Domain{ID: domainID, ProjectID: domainProjectID, Name: "my-domain"}

	domainRepo.On("GetByID", domainID).Return(domain, nil)

	result, err := svc.AttachCertificate(domainID, certID, callerProjectID)

	require.Error(t, err)
	assert.ErrorIs(t, err, services.ErrDomainNotFound)
	assert.Nil(t, result)
	certRepo.AssertNotCalled(t, "GetByID", mock.Anything)
	domainRepo.AssertNotCalled(t, "Update", mock.Anything)
	k8sMock.AssertNotCalled(t, "UpdateGateway", mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainService_AttachCertificate_CertWrongProject_NotFound(t *testing.T) {
	svc, domainRepo, _, _, k8sMock, _, certRepo := newTestDomainService()

	domainID := uuid.New()
	certID := uuid.New()
	projectID := uuid.New()
	otherProjectID := uuid.New()
	domain := &models.Domain{ID: domainID, ProjectID: projectID, Name: "my-domain"}
	cert := readyServerCert(certID, otherProjectID)

	domainRepo.On("GetByID", domainID).Return(domain, nil)
	certRepo.On("GetByID", certID).Return(cert, nil)

	result, err := svc.AttachCertificate(domainID, certID, projectID)

	require.Error(t, err)
	assert.ErrorIs(t, err, services.ErrCertificateNotFound)
	assert.Nil(t, result)
	domainRepo.AssertNotCalled(t, "Update", mock.Anything)
	assert.Nil(t, domain.ManagedCertificateID)
	k8sMock.AssertNotCalled(t, "UpdateGateway", mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainService_AttachCertificate_ClientUsage_Rejected(t *testing.T) {
	svc, domainRepo, _, _, k8sMock, _, certRepo := newTestDomainService()

	domainID := uuid.New()
	certID := uuid.New()
	projectID := uuid.New()
	domain := &models.Domain{ID: domainID, ProjectID: projectID, Name: "my-domain"}
	cert := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Usage:     models.ManagedCertUsageClient,
		Status:    models.ManagedCertStatusReady,
	}

	domainRepo.On("GetByID", domainID).Return(domain, nil)
	certRepo.On("GetByID", certID).Return(cert, nil)

	result, err := svc.AttachCertificate(domainID, certID, projectID)

	require.Error(t, err)
	assert.ErrorIs(t, err, services.ErrCertificateWrongUsage)
	assert.Nil(t, result)
	domainRepo.AssertNotCalled(t, "Update", mock.Anything)
	assert.Nil(t, domain.ManagedCertificateID)
	k8sMock.AssertNotCalled(t, "UpdateGateway", mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainService_AttachCertificate_NotReady_Rejected(t *testing.T) {
	svc, domainRepo, _, _, k8sMock, _, certRepo := newTestDomainService()

	domainID := uuid.New()
	certID := uuid.New()
	projectID := uuid.New()
	domain := &models.Domain{ID: domainID, ProjectID: projectID, Name: "my-domain"}
	cert := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Usage:     models.ManagedCertUsageServer,
		Status:    models.ManagedCertStatusIssuing,
	}

	domainRepo.On("GetByID", domainID).Return(domain, nil)
	certRepo.On("GetByID", certID).Return(cert, nil)

	result, err := svc.AttachCertificate(domainID, certID, projectID)

	require.Error(t, err)
	assert.ErrorIs(t, err, services.ErrCertificateNotReady)
	assert.Nil(t, result)
	domainRepo.AssertNotCalled(t, "Update", mock.Anything)
	assert.Nil(t, domain.ManagedCertificateID)
	k8sMock.AssertNotCalled(t, "UpdateGateway", mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainService_DetachCertificate_Success(t *testing.T) {
	svc, domainRepo, _, _, k8sMock, projectNamespaceRepo, _ := newTestDomainService()

	domainID := uuid.New()
	certID := uuid.New()
	projectID := uuid.New()
	domain := &models.Domain{
		ID:                   domainID,
		ProjectID:            projectID,
		Name:                 "my-domain",
		ManagedCertificateID: &certID,
		TLSSecretName:        "legacy-secret",
	}

	domainRepo.On("GetByID", domainID).Return(domain, nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)
	expectSuccessfulApplyGateway(domainRepo, projectNamespaceRepo, k8sMock, projectID)

	result, err := svc.DetachCertificate(domainID, projectID)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.ManagedCertificateID)
	k8sMock.AssertCalled(t, "UpdateGateway", mock.Anything, projectID, mock.MatchedBy(func(cfg *kubernetes.GatewayConfig) bool {
		return cfg.TLSSecretName == "legacy-secret"
	}))
}

func TestDomainService_DetachCertificate_WrongProject_NotFound(t *testing.T) {
	svc, domainRepo, _, _, k8sMock, _, _ := newTestDomainService()

	domainID := uuid.New()
	domainProjectID := uuid.New()
	callerProjectID := uuid.New()
	domain := &models.Domain{ID: domainID, ProjectID: domainProjectID, Name: "my-domain"}

	domainRepo.On("GetByID", domainID).Return(domain, nil)

	result, err := svc.DetachCertificate(domainID, callerProjectID)

	require.Error(t, err)
	assert.ErrorIs(t, err, services.ErrDomainNotFound)
	assert.Nil(t, result)
	domainRepo.AssertNotCalled(t, "Update", mock.Anything)
	k8sMock.AssertNotCalled(t, "UpdateGateway", mock.Anything, mock.Anything, mock.Anything)
}

// =========================================================================
// ListByProjectID (additional)
// =========================================================================

func TestDomainService_ListByProjectID_Empty(t *testing.T) {
	svc, domainRepo, _, _, _, _, _ := newTestDomainService()

	projectID := uuid.New()
	domainRepo.On("ListByProjectID", projectID, 1, 10, "", "", map[string]string(nil)).
		Return([]models.Domain{}, int64(0), nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 10, "", "", nil)

	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Len(t, result, 0)
}

func TestDomainService_ListByProjectID_Error(t *testing.T) {
	svc, domainRepo, _, _, _, _, _ := newTestDomainService()

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
		ManagedCertLookup:    new(mocks.MockManagedCertificateRepository),
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
		ManagedCertLookup:    new(mocks.MockManagedCertificateRepository),
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

const mtlsNoCAWarningText = "mTLS is enabled but no CA certificates are available for this domain (none configured directly, and no active mTLS clients attached). The effect depends on the Envoy Gateway version. On 1.8.0 and later the gateway fails closed: requests get an HTTP 500 at the gateway (not a rejected TLS handshake) until a CA is added or an mTLS client is attached. On versions before 1.8.0 the ClientTrafficPolicy is not reconciled and the domain keeps serving traffic with NO client authentication -- it fails OPEN, so do not rely on mTLS on those versions until a CA is added or an mTLS client is attached. Because ClientTrafficPolicy is Gateway-scoped, this likely affects every route behind this domain's Gateway, not only this domain's routes."

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
		ManagedCertLookup:    new(mocks.MockManagedCertificateRepository),
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
		"ManagedCertLookup":    func(d *services.DomainServiceDeps) { d.ManagedCertLookup = nil },
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

// CHARACTERIZATION. domain_service.go:297 -- the path that actually deploys
// the Gateway -- assembled its own config, bypassing the pure builder, and no
// golden covered it. This pins that domainplan.BuildGatewayConfig, called
// directly here with a hand-built models.Domain, produces the same
// GatewayConfig the old inline literal did, for the deploy path's field
// shape. It does NOT exercise DomainService.Create and so cannot catch
// `domain` failing to carry a value at the actual call site: replacing the
// deploy-path call with domainplan.BuildGatewayConfig(&models.Domain{}, nil)
// -- a Gateway with no name, namespace, hostname or TLS -- leaves
// internal/services green. That gap is closed by code review only.
func TestDomainService_DeployGatewayConfig_MatchesDomainplanBuilder(t *testing.T) {
	tmplID := uuid.New()
	domain := &models.Domain{
		K8sGatewayName:     "eg",
		Namespace:          "gateway-ns",
		K8sGatewayClass:    "example-public",
		Hostname:           "example.com",
		TLSMode:            "tls_only",
		HTTPPort:           80,
		HTTPSPort:          443,
		TLSSecretName:      "wildcard-tls",
		TLSSecretNamespace: "shared-certs",
		TLSPolicy:          models.TLSPolicyTerminate,
		DomainTemplateID:   &tmplID,
	}
	annotations := map[string]string{"a": "1"}

	want := &kubernetes.GatewayConfig{
		Name:               domain.K8sGatewayName,
		Namespace:          domain.Namespace,
		GatewayClassName:   domain.K8sGatewayClass,
		Hostname:           domain.Hostname,
		TLSMode:            domain.TLSMode,
		HTTPPort:           domain.HTTPPort,
		HTTPSPort:          domain.HTTPSPort,
		TLSSecretName:      domain.TLSSecretName,
		TLSSecretNamespace: domain.TLSSecretNamespace,
		TLSPolicy:          string(domain.TLSPolicy),
		Annotations:        annotations,
	}

	require.Equal(t, want, domainplan.BuildGatewayConfig(domain, annotations))
}

// TestDomainService_Create_SelectingManagedCertAttachesIt verifies Option A:
// when a domain's TLS secret IS a ready server managed certificate's secret
// (cert-<id>), Create sets the domain's ManagedCertificateID so the
// certificate's in-use delete guard applies -- no separate attach step.
func TestDomainService_Create_SelectingManagedCertAttachesIt(t *testing.T) {
	svc, domainRepo, _, dtRepo, k8sMock, _, managedCertRepo := newTestDomainService()

	projectID := uuid.New()
	dtID := uuid.New()
	certID := uuid.New()

	input := &services.CreateDomainInput{
		Name:             "test",
		Hostname:         "new.example.com",
		DomainTemplateID: dtID.String(),
		Namespace:        kubernetes.FastGatewayNamespace,
		TLSSecretName:    "cert-" + certID.String(),
	}
	dt := &models.DomainTemplate{
		ID: dtID, ProjectID: projectID, Name: "tpl",
		Status:  models.DomainTemplateStatusActive,
		TLSMode: models.TLSModeOnly,
	}

	domainRepo.On("ExistsByHostname", projectID, "new.example.com").Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)
	managedCertRepo.On("GetByID", certID).Return(&models.ManagedCertificate{
		ID: certID, ProjectID: projectID,
		Usage: models.ManagedCertUsageServer, Status: models.ManagedCertStatusReady,
	}, nil)
	domainRepo.On("Create", mock.MatchedBy(func(d *models.Domain) bool {
		return d.ManagedCertificateID != nil && *d.ManagedCertificateID == certID
	})).Return(nil)
	k8sMock.On("CreateGateway", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).Return(nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)

	result, err := svc.Create(projectID, input, uuid.New())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ManagedCertificateID)
	assert.Equal(t, certID, *result.ManagedCertificateID)
	managedCertRepo.AssertExpectations(t)
}

// TestDomainService_Create_ByoSecretDoesNotAttach verifies a plain BYO secret
// name leaves ManagedCertificateID nil (no managed-cert lookup/attach).
func TestDomainService_Create_ByoSecretDoesNotAttach(t *testing.T) {
	svc, domainRepo, _, dtRepo, k8sMock, _, _ := newTestDomainService()

	projectID := uuid.New()
	dtID := uuid.New()

	input := &services.CreateDomainInput{
		Name:             "test",
		Hostname:         "new.example.com",
		DomainTemplateID: dtID.String(),
		Namespace:        kubernetes.FastGatewayNamespace,
		TLSSecretName:    "my-wildcard-tls",
	}
	dt := &models.DomainTemplate{
		ID: dtID, ProjectID: projectID, Name: "tpl",
		Status:  models.DomainTemplateStatusActive,
		TLSMode: models.TLSModeOnly,
	}

	domainRepo.On("ExistsByHostname", projectID, "new.example.com").Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)
	domainRepo.On("Create", mock.MatchedBy(func(d *models.Domain) bool {
		return d.ManagedCertificateID == nil
	})).Return(nil)
	k8sMock.On("CreateGateway", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).Return(nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)

	result, err := svc.Create(projectID, input, uuid.New())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.ManagedCertificateID)
}

// =========================================================================
// Create: DNS auto-enable (Task 10)
// =========================================================================

// mockDNSEnabler is a minimal stand-in for DNSRecordService's Enable/Delete
// methods (services.DNSRecordManager). It is deliberately not a full
// testify mock -- Create's DNS-enable step and Delete's DNS-teardown step
// are both best-effort and only need to record whether (and with what) they
// were called.
type mockDNSEnabler struct {
	enableCalled bool
	domainID     uuid.UUID
	projectID    uuid.UUID
	createdBy    uuid.UUID
	in           services.DNSRecordInput
	err          error

	deleteCalled   bool
	deleteDomains  []uuid.UUID
	deleteProjects []uuid.UUID
	deleteErr      error

	checkCalled       bool
	checkCollisionErr error
}

func (m *mockDNSEnabler) CheckCollision(hostname string, hostedZoneID, excludeDomainID uuid.UUID) error {
	m.checkCalled = true
	return m.checkCollisionErr
}

func (m *mockDNSEnabler) Enable(domainID, projectID, createdBy uuid.UUID, in services.DNSRecordInput) (*models.DomainDNSRecord, error) {
	m.enableCalled = true
	m.domainID = domainID
	m.projectID = projectID
	m.createdBy = createdBy
	m.in = in
	if m.err != nil {
		return nil, m.err
	}
	return &models.DomainDNSRecord{DomainID: domainID}, nil
}

func (m *mockDNSEnabler) Delete(domainID, projectID uuid.UUID) error {
	m.deleteCalled = true
	m.deleteDomains = append(m.deleteDomains, domainID)
	m.deleteProjects = append(m.deleteProjects, projectID)
	return m.deleteErr
}

func TestCreateDomain_WithDNS_EnablesRecord(t *testing.T) {
	svc, domainRepo, _, dtRepo, k8sMock, _, _ := newTestDomainService()
	dnsMock := &mockDNSEnabler{}
	svc.SetDNSRecords(dnsMock)

	projectID := uuid.New()
	dtID := uuid.New()
	userID := uuid.New()
	cred := uuid.New()

	dt := &models.DomainTemplate{
		ID: dtID, ProjectID: projectID, Name: "tpl",
		Status:  models.DomainTemplateStatusActive,
		TLSMode: models.TLSModeNone,
	}

	domainRepo.On("ExistsByHostname", projectID, "app.example.com").Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)
	domainRepo.On("Create", mock.AnythingOfType("*models.Domain")).Return(nil)
	k8sMock.On("CreateGateway", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).Return(nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)

	result, err := svc.Create(projectID, &services.CreateDomainInput{
		Name:             "d",
		Hostname:         "app.example.com",
		DomainTemplateID: dtID.String(),
		Namespace:        kubernetes.FastGatewayNamespace,
		DNS: &services.DomainDNSInput{
			Enabled:      true,
			HostedZoneID: cred.String(),
		},
	}, userID)

	require.NoError(t, err)
	require.NotNil(t, result)
	if !dnsMock.enableCalled {
		t.Fatal("expected DNS record Enable to be called")
	}
	assert.Equal(t, result.ID, dnsMock.domainID)
	assert.Equal(t, projectID, dnsMock.projectID)
	assert.Equal(t, userID, dnsMock.createdBy)
	require.NotNil(t, dnsMock.in.HostedZoneID)
	assert.Equal(t, cred, *dnsMock.in.HostedZoneID)
}

// TestCreateDomain_WithDNS_Disabled verifies that an unset or disabled DNS
// block never calls Enable, even when dnsRecords is wired.
func TestCreateDomain_WithDNS_Disabled(t *testing.T) {
	svc, domainRepo, _, dtRepo, k8sMock, _, _ := newTestDomainService()
	dnsMock := &mockDNSEnabler{}
	svc.SetDNSRecords(dnsMock)

	projectID := uuid.New()
	dtID := uuid.New()

	dt := &models.DomainTemplate{
		ID: dtID, ProjectID: projectID, Name: "tpl",
		Status:  models.DomainTemplateStatusActive,
		TLSMode: models.TLSModeNone,
	}

	domainRepo.On("ExistsByHostname", projectID, "app2.example.com").Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)
	domainRepo.On("Create", mock.AnythingOfType("*models.Domain")).Return(nil)
	k8sMock.On("CreateGateway", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).Return(nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)

	_, err := svc.Create(projectID, &services.CreateDomainInput{
		Name:             "d",
		Hostname:         "app2.example.com",
		DomainTemplateID: dtID.String(),
		Namespace:        kubernetes.FastGatewayNamespace,
	}, uuid.New())

	require.NoError(t, err)
	if dnsMock.enableCalled {
		t.Fatal("expected DNS record Enable NOT to be called when DNS is unset")
	}
}

// TestCreateDomain_WithDNS_EnableFailureIsBestEffort verifies that a DNS
// Enable failure does not fail domain creation (Create still returns the
// domain with no error).
func TestCreateDomain_WithDNS_EnableFailureIsBestEffort(t *testing.T) {
	svc, domainRepo, _, dtRepo, k8sMock, _, _ := newTestDomainService()
	dnsMock := &mockDNSEnabler{err: errors.New("boom")}
	svc.SetDNSRecords(dnsMock)

	projectID := uuid.New()
	dtID := uuid.New()

	dt := &models.DomainTemplate{
		ID: dtID, ProjectID: projectID, Name: "tpl",
		Status:  models.DomainTemplateStatusActive,
		TLSMode: models.TLSModeNone,
	}

	domainRepo.On("ExistsByHostname", projectID, "app3.example.com").Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)
	domainRepo.On("Create", mock.AnythingOfType("*models.Domain")).Return(nil)
	k8sMock.On("CreateGateway", mock.Anything, projectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).Return(nil)
	domainRepo.On("Update", mock.AnythingOfType("*models.Domain")).Return(nil)

	result, err := svc.Create(projectID, &services.CreateDomainInput{
		Name:             "d",
		Hostname:         "app3.example.com",
		DomainTemplateID: dtID.String(),
		Namespace:        kubernetes.FastGatewayNamespace,
		DNS: &services.DomainDNSInput{
			Enabled:      true,
			HostedZoneID: uuid.New().String(),
		},
	}, uuid.New())

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.DomainStatusActive, result.Status)
	if !dnsMock.enableCalled {
		t.Fatal("expected DNS record Enable to be attempted")
	}
}

// =========================================================================
// Delete: DNS record teardown (final review Fix 1)
// =========================================================================

// expectDomainTeardownMocks wires every K8s/DB teardown call Delete makes
// before reaching the DNS-teardown step, so tests below only need to assert
// on the DNS side effect.
func expectDomainTeardownMocks(domainRepo *mocks.MockDomainRepository, settingsRepo *mocks.MockDomainSettingsRepository, k8sMock *mocks.MockKubernetesService, domain *models.Domain) {
	domainRepo.On("GetByID", domain.ID).Return(domain, nil)
	k8sMock.On("DeleteBackendTrafficPolicy", mock.Anything, domain.ProjectID, domain.Namespace, domain.K8sGatewayName+"-btp").Return(nil)
	k8sMock.On("DeleteBackend", mock.Anything, domain.ProjectID, domain.Namespace, mock.Anything).Return(nil)
	k8sMock.On("DeleteEnvoyExtensionPolicy", mock.Anything, domain.ProjectID, domain.Namespace, domain.K8sGatewayName+"-eep").Return(nil)
	k8sMock.On("DeleteClientTrafficPolicy", mock.Anything, domain.ProjectID, domain.Namespace, domain.K8sGatewayName+"-ctp").Return(nil)
	settingsRepo.On("DeleteByDomainID", domain.ID).Return(nil)
	k8sMock.On("DeleteGateway", mock.Anything, domain.ProjectID, domain.Namespace, domain.K8sGatewayName).Return(nil)
	domainRepo.On("Delete", domain.ID).Return(nil)
}

// TestDomainService_Delete_TearsDownDNSRecord is the regression test for the
// critical review finding: deleting a domain must remove its managed DNS
// record (via DNSRecordManager.Delete), not just let the DomainDNSRecord DB
// row cascade away while the live provider record is orphaned forever.
func TestDomainService_Delete_TearsDownDNSRecord(t *testing.T) {
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithK8s()
	dnsMock := &mockDNSEnabler{}
	svc.SetDNSRecords(dnsMock)

	domain := &models.Domain{
		ID:             uuid.New(),
		ProjectID:      uuid.New(),
		K8sGatewayName: "test-gw",
		Namespace:      kubernetes.FastGatewayNamespace,
	}
	expectDomainTeardownMocks(domainRepo, settingsRepo, k8sMock, domain)

	err := svc.Delete(domain.ID)

	require.NoError(t, err)
	if !dnsMock.deleteCalled {
		t.Fatal("expected DNSRecordManager.Delete to be called for the deleted domain")
	}
	require.Len(t, dnsMock.deleteDomains, 1)
	assert.Equal(t, domain.ID, dnsMock.deleteDomains[0])
	require.Len(t, dnsMock.deleteProjects, 1)
	assert.Equal(t, domain.ProjectID, dnsMock.deleteProjects[0])
}

// TestDomainService_Delete_DNSTeardownFailureIsBestEffort verifies a DNS
// teardown failure does not block domain deletion, matching the best-effort
// style of the other teardowns in Delete (e.g. DeleteGateway/
// DeleteClientTrafficPolicy already log-and-continue on error).
func TestDomainService_Delete_DNSTeardownFailureIsBestEffort(t *testing.T) {
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithK8s()
	dnsMock := &mockDNSEnabler{deleteErr: errors.New("k8s boom")}
	svc.SetDNSRecords(dnsMock)

	domain := &models.Domain{
		ID:             uuid.New(),
		ProjectID:      uuid.New(),
		K8sGatewayName: "test-gw",
		Namespace:      kubernetes.FastGatewayNamespace,
	}
	expectDomainTeardownMocks(domainRepo, settingsRepo, k8sMock, domain)

	err := svc.Delete(domain.ID)

	require.NoError(t, err)
	if !dnsMock.deleteCalled {
		t.Fatal("expected DNSRecordManager.Delete to be attempted even though it fails")
	}
	domainRepo.AssertExpectations(t)
}

// TestDomainService_Delete_NoDNSRecordsWired verifies Delete still succeeds
// when dnsRecords was never wired (outside a cluster) -- the nil-guard around
// the teardown call must make this a no-op, not a panic.
func TestDomainService_Delete_NoDNSRecordsWired(t *testing.T) {
	svc, domainRepo, settingsRepo, k8sMock := newTestDomainServiceWithK8s()

	domain := &models.Domain{
		ID:             uuid.New(),
		ProjectID:      uuid.New(),
		K8sGatewayName: "test-gw",
		Namespace:      kubernetes.FastGatewayNamespace,
	}
	expectDomainTeardownMocks(domainRepo, settingsRepo, k8sMock, domain)

	err := svc.Delete(domain.ID)

	require.NoError(t, err)
	domainRepo.AssertExpectations(t)
}

// TestCreateDomain_WithDNS_CollisionFailsCreate verifies the up-front pre-flight:
// a DNS collision rejects the whole create before anything is persisted.
func TestCreateDomain_WithDNS_CollisionFailsCreate(t *testing.T) {
	svc, domainRepo, _, _, _, _, _ := newTestDomainService()
	dnsMock := &mockDNSEnabler{checkCollisionErr: services.ErrHostnameClaimed}
	svc.SetDNSRecords(dnsMock)

	projectID := uuid.New()
	zoneID := uuid.New()

	_, err := svc.Create(projectID, &services.CreateDomainInput{
		Name:             "d",
		Hostname:         "app.example.com",
		DomainTemplateID: uuid.New().String(),
		Namespace:        kubernetes.FastGatewayNamespace,
		DNS:              &services.DomainDNSInput{Enabled: true, HostedZoneID: zoneID.String()},
	}, uuid.New())

	require.ErrorIs(t, err, services.ErrHostnameClaimed)
	assert.True(t, dnsMock.checkCalled, "CheckCollision should run")
	domainRepo.AssertNotCalled(t, "Create", mock.Anything)
	domainRepo.AssertNotCalled(t, "ExistsByHostname", mock.Anything, mock.Anything)
}

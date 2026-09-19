package services_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// fakeCertApprovalSubmitter is a minimal stand-in for *approvalpkg.Engine
// that satisfies services.CertApprovalSubmitter, so Create can be tested
// without a real Engine and its five dependencies.
type fakeCertApprovalSubmitter struct {
	submitCalled bool
	lastSpec     approvalpkg.Spec
	approval     *models.Approval
	err          error
}

func (f *fakeCertApprovalSubmitter) Submit(spec approvalpkg.Spec) (*models.Approval, error) {
	f.submitCalled = true
	f.lastSpec = spec
	if f.err != nil {
		return nil, f.err
	}
	if f.approval != nil {
		return f.approval, nil
	}
	return &models.Approval{ID: uuid.New(), ProjectID: spec.ProjectID, EntityType: spec.EntityType, EntityID: spec.EntityID, Action: spec.Action, Status: models.ApprovalStatusPending}, nil
}

func newTestManagedCertificateService(
	repo *mocks.MockManagedCertificateRepository,
	issuerRepo *mocks.MockCertificateIssuerRepository,
	grantRepo *mocks.MockIssuerProjectGrantRepository,
	projectRepo *mocks.MockProjectRepository,
	applier *mocks.MockCertInfraApplier,
	submitter services.CertApprovalSubmitter,
	distRepo *mocks.MockCertificateDistributionRepository,
) *services.ManagedCertificateService {
	return services.NewManagedCertificateService(services.ManagedCertificateServiceDeps{
		Repo:         repo,
		IssuerRepo:   issuerRepo,
		GrantRepo:    grantRepo,
		ProjectRepo:  projectRepo,
		ControlPlane: applier,
		Approvals:    submitter,
		Config:       &config.Config{},
		DistRepo:     distRepo,
	})
}

func TestManagedCertificateService_Create_SubmitsApproval(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	projectID := uuid.New()
	issuerID := uuid.New()
	createdBy := uuid.New()

	grantRepo.On("Exists", issuerID, projectID).Return(true, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-abc"},
	}, nil)
	projectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, ApprovalEnabled: true}, nil)

	repo.On("Create", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		c := args.Get(0).(*models.ManagedCertificate)
		c.ID = uuid.New()
	}).Return(nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Return(nil)

	cert, approval, err := svc.Create(projectID, &services.CreateCertificateInput{
		Name:     "example",
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageServer,
		DNSNames: []string{"example.com"},
	}, createdBy)

	require.NoError(t, err)
	require.NotNil(t, cert)
	require.NotNil(t, approval)
	assert.Equal(t, models.ManagedCertStatusPending, cert.Status)
	assert.True(t, submitter.submitCalled)
	assert.Equal(t, models.ApprovalEntityCertificate, submitter.lastSpec.EntityType)
	assert.Equal(t, models.ApprovalActionCreate, submitter.lastSpec.Action)
	assert.Equal(t, cert.ID, submitter.lastSpec.EntityID)
	repo.AssertExpectations(t)
	grantRepo.AssertExpectations(t)
	issuerRepo.AssertExpectations(t)
	projectRepo.AssertExpectations(t)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
}

func TestManagedCertificateService_Create_RejectedWhenIssuerNotGranted(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	projectID := uuid.New()
	issuerID := uuid.New()

	grantRepo.On("Exists", issuerID, projectID).Return(false, nil)

	cert, approval, err := svc.Create(projectID, &services.CreateCertificateInput{
		Name:     "example",
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageServer,
		DNSNames: []string{"example.com"},
	}, uuid.New())

	require.Error(t, err)
	assert.Nil(t, cert)
	assert.Nil(t, approval)
	repo.AssertNotCalled(t, "Create", mock.Anything)
	assert.False(t, submitter.submitCalled)
}

func TestManagedCertificateService_OnApproved_IssuesLeafCertificate(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	issuerID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:       certID,
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageServer,
		Status:   models.ManagedCertStatusPending,
		Config: models.ManagedCertConfig{
			DNSNames:        []string{"example.com"},
			CertificateName: "cert-" + certID.String(),
			SecretName:      "cert-" + certID.String(),
			KeyAlgorithm:    "RSA",
			KeySize:         2048,
			DurationDays:    90,
		},
	}

	repo.On("GetByID", certID).Return(cert, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-abc"},
	}, nil)
	applier.On("Namespace").Return("fastgateway-system")

	var appliedObj *unstructured.Unstructured
	applier.On("ApplyNamespaced", mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Run(func(args mock.Arguments) {
		appliedObj = args.Get(2).(*unstructured.Unstructured)
	}).Return(nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		updated := args.Get(0).(*models.ManagedCertificate)
		assert.Equal(t, models.ManagedCertStatusIssuing, updated.Status)
	}).Return(nil)

	approval := &models.Approval{
		ID:         uuid.New(),
		EntityType: models.ApprovalEntityCertificate,
		Action:     models.ApprovalActionCreate,
		EntityID:   certID,
	}

	err := svc.OnApproved(approval)
	require.NoError(t, err)

	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 1)
	repo.AssertExpectations(t)

	// Guard against swapped DNSNames<->Subject, wrong-issuer, or
	// dropped-namespace bugs: assert on the actual object handed to
	// ApplyNamespaced, not just that it was called.
	require.NotNil(t, appliedObj)
	assert.Equal(t, "fastgateway-system", appliedObj.GetNamespace())
	spec, ok := appliedObj.Object["spec"].(map[string]interface{})
	require.True(t, ok, "spec must be a map")
	assert.Equal(t, []interface{}{"example.com"}, spec["dnsNames"])
	assert.Equal(t, cert.Config.SecretName, spec["secretName"])
	issuerRef, ok := spec["issuerRef"].(map[string]interface{})
	require.True(t, ok, "issuerRef must be a map")
	assert.Equal(t, "iss-abc", issuerRef["name"])
}

func TestManagedCertificateService_OnApproved_ApplyFailureSetsErrorStatus(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	issuerID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:       certID,
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageServer,
		Status:   models.ManagedCertStatusPending,
		Config: models.ManagedCertConfig{
			DNSNames:        []string{"example.com"},
			CertificateName: "cert-" + certID.String(),
			SecretName:      "cert-" + certID.String(),
		},
	}

	repo.On("GetByID", certID).Return(cert, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-abc"},
	}, nil)
	applier.On("Namespace").Return("fastgateway-system")
	applier.On("ApplyNamespaced", mock.Anything, kubernetes.CertManagerCertificateGVR, mock.AnythingOfType("*unstructured.Unstructured")).Return(errors.New("apply failed"))
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		updated := args.Get(0).(*models.ManagedCertificate)
		assert.Equal(t, models.ManagedCertStatusError, updated.Status)
		assert.Equal(t, "apply failed", updated.StatusMessage)
	}).Return(nil)

	approval := &models.Approval{
		ID:         uuid.New(),
		EntityType: models.ApprovalEntityCertificate,
		Action:     models.ApprovalActionCreate,
		EntityID:   certID,
	}

	err := svc.OnApproved(approval)
	require.Error(t, err)
	repo.AssertExpectations(t)
}

func TestManagedCertificateService_Create_ServerUsageRequiresDNSNames(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	_, _, err := svc.Create(uuid.New(), &services.CreateCertificateInput{
		Name:     "example",
		IssuerID: uuid.New(),
		Usage:    models.ManagedCertUsageServer,
	}, uuid.New())

	require.Error(t, err)
	repo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestManagedCertificateService_Create_ClientUsageRequiresSubject(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	_, _, err := svc.Create(uuid.New(), &services.CreateCertificateInput{
		Name:     "example",
		IssuerID: uuid.New(),
		Usage:    models.ManagedCertUsageClient,
	}, uuid.New())

	require.Error(t, err)
	repo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestManagedCertificateService_Create_FastPathWhenApprovalDisabled(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	projectID := uuid.New()
	issuerID := uuid.New()

	grantRepo.On("Exists", issuerID, projectID).Return(true, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-abc"},
	}, nil)
	projectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, ApprovalEnabled: false}, nil)

	var createdID uuid.UUID
	repo.On("Create", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		c := args.Get(0).(*models.ManagedCertificate)
		c.ID = uuid.New()
		createdID = c.ID
	}).Return(nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Return(nil)
	repo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(func(id uuid.UUID) *models.ManagedCertificate {
		return &models.ManagedCertificate{
			ID:       id,
			IssuerID: issuerID,
			Usage:    models.ManagedCertUsageServer,
			Config: models.ManagedCertConfig{
				DNSNames:        []string{"example.com"},
				CertificateName: "cert-" + id.String(),
				SecretName:      "cert-" + id.String(),
			},
		}
	}, nil)
	applier.On("Namespace").Return("fastgateway-system")
	applier.On("ApplyNamespaced", mock.Anything, kubernetes.CertManagerCertificateGVR, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)

	cert, approval, err := svc.Create(projectID, &services.CreateCertificateInput{
		Name:     "example",
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageServer,
		DNSNames: []string{"example.com"},
	}, uuid.New())

	require.NoError(t, err)
	require.NotNil(t, cert)
	assert.Nil(t, approval)
	assert.False(t, submitter.submitCalled)
	assert.NotEqual(t, uuid.Nil, createdID)
	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 1)
}

// TestManagedCertificateService_OnCancelled_CreateDeletesRow mirrors
// routeWrite.OnCancelled's create case: a cancelled create was never
// issued, so the pending row is deleted outright rather than reverted to
// some other status.
func TestManagedCertificateService_OnCancelled_CreateDeletesRow(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	repo.On("Delete", certID).Return(nil)

	approval := &models.Approval{
		ID:         uuid.New(),
		EntityType: models.ApprovalEntityCertificate,
		Action:     models.ApprovalActionCreate,
		EntityID:   certID,
	}

	err := svc.OnCancelled(approval)
	require.NoError(t, err)

	repo.AssertCalled(t, "Delete", certID)
	repo.AssertNotCalled(t, "GetByID", mock.Anything)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
}

// TestManagedCertificateService_OnRejected_CreateSetsErrorStatus mirrors
// routeWrite.OnRejected's create case in spirit: a rejected create never
// reached the cluster, so the row is marked errored (this implementation's
// choice, unlike the route's "rejected" status, since ManagedCertStatus has
// no dedicated rejected value) and no cluster apply happens.
func TestManagedCertificateService_OnRejected_CreateSetsErrorStatus(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:     certID,
		Status: models.ManagedCertStatusPending,
	}
	repo.On("GetByID", certID).Return(cert, nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		updated := args.Get(0).(*models.ManagedCertificate)
		assert.Equal(t, models.ManagedCertStatusError, updated.Status)
		assert.NotEmpty(t, updated.StatusMessage)
	}).Return(nil)

	approval := &models.Approval{
		ID:         uuid.New(),
		EntityType: models.ApprovalEntityCertificate,
		Action:     models.ApprovalActionCreate,
		EntityID:   certID,
	}

	err := svc.OnRejected(approval)
	require.NoError(t, err)

	repo.AssertExpectations(t)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
	issuerRepo.AssertNotCalled(t, "GetByID", mock.Anything)
}

// readyConditionCertificate builds a minimal unstructured cert-manager
// Certificate object with a single status.conditions entry of type=Ready,
// for exercising Status's condition-parsing logic.
func readyConditionCertificate(status, message string, extra map[string]interface{}) *unstructured.Unstructured {
	statusFields := map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":    "Ready",
				"status":  status,
				"message": message,
			},
		},
	}
	for k, v := range extra {
		statusFields[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata":   map[string]interface{}{"name": "cert-x", "namespace": "fastgateway-system"},
		"status":     statusFields,
	}}
}

func TestManagedCertificateService_Status_ReadyTrue(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:     certID,
		Status: models.ManagedCertStatusIssuing,
		Config: models.ManagedCertConfig{CertificateName: "cert-x"},
	}
	repo.On("GetByID", certID).Return(cert, nil)
	obj := readyConditionCertificate("True", "", map[string]interface{}{
		"notAfter":    "2027-01-01T00:00:00Z",
		"fingerprint": "AA:BB:CC",
	})
	applier.On("Get", mock.Anything, kubernetes.CertManagerCertificateGVR, "cert-x", true).Return(obj, nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		updated := args.Get(0).(*models.ManagedCertificate)
		assert.Equal(t, models.ManagedCertStatusReady, updated.Status)
		assert.Equal(t, "AA:BB:CC", updated.Fingerprint)
		require.NotNil(t, updated.NotAfter)
		assert.True(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Equal(*updated.NotAfter))
	}).Return(nil)

	result, err := svc.Status(certID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.ManagedCertStatusReady, result.Status)
	assert.Empty(t, result.Message)
	require.NotNil(t, result.NotAfter)
	assert.Equal(t, "2027-01-01T00:00:00Z", *result.NotAfter)
	repo.AssertExpectations(t)
}

// TestManagedCertificateService_Status_ReadyTrueInvalidNotAfterDoesNotClobber
// covers the guard: if status.notAfter is missing or fails RFC3339 parsing,
// the model's existing NotAfter must be left untouched rather than being
// zeroed out on Update.
func TestManagedCertificateService_Status_ReadyTrueInvalidNotAfterDoesNotClobber(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	existingNotAfter := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cert := &models.ManagedCertificate{
		ID:       certID,
		Status:   models.ManagedCertStatusIssuing,
		Config:   models.ManagedCertConfig{CertificateName: "cert-x"},
		NotAfter: &existingNotAfter,
	}
	repo.On("GetByID", certID).Return(cert, nil)
	obj := readyConditionCertificate("True", "", map[string]interface{}{
		"notAfter":    "not-a-valid-timestamp",
		"fingerprint": "AA:BB:CC",
	})
	applier.On("Get", mock.Anything, kubernetes.CertManagerCertificateGVR, "cert-x", true).Return(obj, nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		updated := args.Get(0).(*models.ManagedCertificate)
		require.NotNil(t, updated.NotAfter)
		assert.True(t, existingNotAfter.Equal(*updated.NotAfter))
	}).Return(nil)

	result, err := svc.Status(certID)
	require.NoError(t, err)
	require.NotNil(t, result)
	repo.AssertExpectations(t)
}

func TestManagedCertificateService_Status_ReadyFalseSurfacesMessage(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:     certID,
		Status: models.ManagedCertStatusIssuing,
		Config: models.ManagedCertConfig{CertificateName: "cert-x"},
	}
	repo.On("GetByID", certID).Return(cert, nil)
	obj := readyConditionCertificate("False", "failed to issue certificate: rate limited", nil)
	applier.On("Get", mock.Anything, kubernetes.CertManagerCertificateGVR, "cert-x", true).Return(obj, nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		updated := args.Get(0).(*models.ManagedCertificate)
		assert.Equal(t, models.ManagedCertStatusError, updated.Status)
		assert.Equal(t, "failed to issue certificate: rate limited", updated.StatusMessage)
	}).Return(nil)

	result, err := svc.Status(certID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.ManagedCertStatusError, result.Status)
	assert.Equal(t, "failed to issue certificate: rate limited", result.Message)
	assert.Nil(t, result.NotAfter)
	repo.AssertExpectations(t)
}

func TestManagedCertificateService_Status_NoReadyConditionDefaultsToIssuing(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:     certID,
		Status: models.ManagedCertStatusIssuing,
		Config: models.ManagedCertConfig{CertificateName: "cert-x"},
	}
	repo.On("GetByID", certID).Return(cert, nil)
	// No status.conditions at all -- freshly-applied Certificate before
	// cert-manager's controller has reconciled it.
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata":   map[string]interface{}{"name": "cert-x", "namespace": "fastgateway-system"},
		"status":     map[string]interface{}{},
	}}
	applier.On("Get", mock.Anything, kubernetes.CertManagerCertificateGVR, "cert-x", true).Return(obj, nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		updated := args.Get(0).(*models.ManagedCertificate)
		assert.Equal(t, models.ManagedCertStatusIssuing, updated.Status)
	}).Return(nil)

	result, err := svc.Status(certID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.ManagedCertStatusIssuing, result.Status)
	assert.Nil(t, result.NotAfter)
	repo.AssertExpectations(t)
}

func TestManagedCertificateService_IssuersForProject_ReturnsOnlyGranted(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	projectID := uuid.New()
	grantedIssuer := models.CertificateIssuer{ID: uuid.New(), Name: "granted"}
	ungrantedIssuer := models.CertificateIssuer{ID: uuid.New(), Name: "ungranted"}

	issuerRepo.On("List").Return([]models.CertificateIssuer{grantedIssuer, ungrantedIssuer}, nil)
	grantRepo.On("Exists", grantedIssuer.ID, projectID).Return(true, nil)
	grantRepo.On("Exists", ungrantedIssuer.ID, projectID).Return(false, nil)

	out, err := svc.IssuersForProject(projectID)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, grantedIssuer.ID, out[0].ID)
}

func TestManagedCertificateService_DistributionStatus_ReturnsRow(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	syncedAt := time.Now()
	dist := &models.CertificateDistribution{
		ID:                    uuid.New(),
		ManagedCertificateID:  certID,
		Status:                models.CertDistStatusSynced,
		LastPushedFingerprint: "sha256:abc",
		LastSyncedAt:          &syncedAt,
	}
	distRepo.On("GetByCertificateID", certID).Return(dist, nil)

	result, err := svc.DistributionStatus(certID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.CertDistStatusSynced, result.Status)
	assert.Equal(t, "sha256:abc", result.LastPushedFingerprint)
	distRepo.AssertExpectations(t)
}

func TestManagedCertificateService_DistributionStatus_NoRow_ReturnsPendingPlaceholder(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	distRepo.On("GetByCertificateID", certID).Return(nil, gorm.ErrRecordNotFound)

	result, err := svc.DistributionStatus(certID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, certID, result.ManagedCertificateID)
	assert.Equal(t, models.CertDistStatusPending, result.Status)
	assert.Equal(t, "distribution not yet started", result.Message)
	distRepo.AssertExpectations(t)
}

func TestManagedCertificateService_DistributionStatus_OtherError_Propagates(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	boom := errors.New("db exploded")
	distRepo.On("GetByCertificateID", certID).Return(nil, boom)

	result, err := svc.DistributionStatus(certID)
	require.Error(t, err)
	require.Nil(t, result)
	distRepo.AssertExpectations(t)
}

func TestManagedCertificateService_Resync_ExistingRow_PreservesFingerprintAndSyncedAt(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	projectID := uuid.New()
	distID := uuid.New()
	syncedAt := time.Now().Add(-time.Hour)

	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Status: models.ManagedCertStatusReady}
	repo.On("GetByID", certID).Return(cert, nil)

	existing := &models.CertificateDistribution{
		ID:                    distID,
		ManagedCertificateID:  certID,
		ProjectID:             projectID,
		Status:                models.CertDistStatusSynced,
		LastPushedFingerprint: "sha256:preserved",
		LastSyncedAt:          &syncedAt,
	}
	distRepo.On("GetByCertificateID", certID).Return(existing, nil)

	distRepo.On("Upsert", mock.AnythingOfType("*models.CertificateDistribution")).Run(func(args mock.Arguments) {
		updated := args.Get(0).(*models.CertificateDistribution)
		assert.Equal(t, distID, updated.ID)
		assert.Equal(t, certID, updated.ManagedCertificateID)
		assert.Equal(t, projectID, updated.ProjectID)
		assert.Equal(t, models.CertDistStatusPending, updated.Status)
		assert.Equal(t, "sha256:preserved", updated.LastPushedFingerprint)
		require.NotNil(t, updated.LastSyncedAt)
		assert.True(t, syncedAt.Equal(*updated.LastSyncedAt))
	}).Return(nil)

	err := svc.Resync(certID)
	require.NoError(t, err)
	repo.AssertExpectations(t)
	distRepo.AssertExpectations(t)
}

func TestManagedCertificateService_Resync_NoRow_CreatesPendingRow(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	projectID := uuid.New()

	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Status: models.ManagedCertStatusReady}
	repo.On("GetByID", certID).Return(cert, nil)
	distRepo.On("GetByCertificateID", certID).Return(nil, gorm.ErrRecordNotFound)

	distRepo.On("Upsert", mock.AnythingOfType("*models.CertificateDistribution")).Run(func(args mock.Arguments) {
		created := args.Get(0).(*models.CertificateDistribution)
		assert.Equal(t, certID, created.ManagedCertificateID)
		assert.Equal(t, projectID, created.ProjectID)
		assert.Equal(t, models.CertDistStatusPending, created.Status)
		assert.Empty(t, created.LastPushedFingerprint)
		assert.Nil(t, created.LastSyncedAt)
	}).Return(nil)

	err := svc.Resync(certID)
	require.NoError(t, err)
	repo.AssertExpectations(t)
	distRepo.AssertExpectations(t)
}

func TestManagedCertificateService_Resync_CertNotFound_ReturnsError(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	repo.On("GetByID", certID).Return(nil, gorm.ErrRecordNotFound)

	err := svc.Resync(certID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	distRepo.AssertNotCalled(t, "GetByCertificateID", mock.Anything)
	distRepo.AssertNotCalled(t, "Upsert", mock.Anything)
	repo.AssertExpectations(t)
}

func TestManagedCertificateService_Resync_DistRepoOtherError_Propagates(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo)

	certID := uuid.New()
	projectID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Status: models.ManagedCertStatusReady}
	repo.On("GetByID", certID).Return(cert, nil)

	boom := errors.New("db exploded")
	distRepo.On("GetByCertificateID", certID).Return(nil, boom)

	err := svc.Resync(certID)
	require.Error(t, err)
	assert.False(t, errors.Is(err, gorm.ErrRecordNotFound))
	distRepo.AssertNotCalled(t, "Upsert", mock.Anything)
	repo.AssertExpectations(t)
	distRepo.AssertExpectations(t)
}

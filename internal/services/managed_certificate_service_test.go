package services_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
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

// newTestManagedCertificateService builds a service for tests that don't
// care about the export-grant path. It supplies an unexercised
// MockCertificateExportGrantRepository so ExportGrantRepo's nil-check is
// satisfied without every one of this file's call sites having to thread an
// extra parameter through. Tests that DO exercise OnApproved's export
// branch use newTestManagedCertificateServiceWithExportGrant instead, so
// they can set expectations on that mock.
func newTestManagedCertificateService(
	repo *mocks.MockManagedCertificateRepository,
	issuerRepo *mocks.MockCertificateIssuerRepository,
	grantRepo *mocks.MockIssuerProjectGrantRepository,
	projectRepo *mocks.MockProjectRepository,
	applier *mocks.MockCertInfraApplier,
	submitter services.CertApprovalSubmitter,
	distRepo *mocks.MockCertificateDistributionRepository,
	domainRepo *mocks.MockDomainRepository,
	tenantSecrets *mocks.MockTenantSecretDeleter,
) *services.ManagedCertificateService {
	return newTestManagedCertificateServiceWithExportGrant(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets,
		new(mocks.MockCertificateExportGrantRepository),
	)
}

func newTestManagedCertificateServiceWithExportGrant(
	repo *mocks.MockManagedCertificateRepository,
	issuerRepo *mocks.MockCertificateIssuerRepository,
	grantRepo *mocks.MockIssuerProjectGrantRepository,
	projectRepo *mocks.MockProjectRepository,
	applier *mocks.MockCertInfraApplier,
	submitter services.CertApprovalSubmitter,
	distRepo *mocks.MockCertificateDistributionRepository,
	domainRepo *mocks.MockDomainRepository,
	tenantSecrets *mocks.MockTenantSecretDeleter,
	exportGrantRepo *mocks.MockCertificateExportGrantRepository,
) *services.ManagedCertificateService {
	// The Client referential guard (Task 10) is exercised by its own
	// dedicated tests via newTestManagedCertificateServiceWithClientRepo.
	// Every other test that goes through this helper defaults ClientRepo to
	// "no client references this cert" so Delete's existing domain-guard
	// tests still reach the domain guard / delete rather than panicking on
	// an unstubbed mock call.
	clientRepo := new(mocks.MockClientRepository)
	clientRepo.On("GetByManagedCertificateID", mock.Anything).Return(nil, gorm.ErrRecordNotFound)
	return newTestManagedCertificateServiceWithClientRepo(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets,
		exportGrantRepo, clientRepo,
	)
}

// newTestManagedCertificateServiceWithClientRepo is the fullest-featured
// constructor: every other helper in this file delegates to it, supplying a
// default "no client references this cert" ClientRepo mock. Tests
// exercising the Task 10 Client referential guard in Delete call this one
// directly so they can control ClientRepo's behavior instead.
func newTestManagedCertificateServiceWithClientRepo(
	repo *mocks.MockManagedCertificateRepository,
	issuerRepo *mocks.MockCertificateIssuerRepository,
	grantRepo *mocks.MockIssuerProjectGrantRepository,
	projectRepo *mocks.MockProjectRepository,
	applier *mocks.MockCertInfraApplier,
	submitter services.CertApprovalSubmitter,
	distRepo *mocks.MockCertificateDistributionRepository,
	domainRepo *mocks.MockDomainRepository,
	tenantSecrets *mocks.MockTenantSecretDeleter,
	exportGrantRepo *mocks.MockCertificateExportGrantRepository,
	clientRepo *mocks.MockClientRepository,
) *services.ManagedCertificateService {
	return services.NewManagedCertificateService(services.ManagedCertificateServiceDeps{
		Repo:            repo,
		IssuerRepo:      issuerRepo,
		GrantRepo:       grantRepo,
		ProjectRepo:     projectRepo,
		ControlPlane:    applier,
		Approvals:       submitter,
		Config:          &config.Config{},
		DistRepo:        distRepo,
		DomainRepo:      domainRepo,
		TenantSecrets:   tenantSecrets,
		ExportGrantRepo: exportGrantRepo,
		ClientRepo:      clientRepo,
	})
}

func TestManagedCertificateService_Create_SubmitsApproval(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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

// TestManagedCertificateService_OnApproved_ExportAction_CreatesGrantOnly
// verifies the export-action Completer branch: OnApproved must create a
// CertificateExportGrant bound to the certificate and the requesting user
// (a.SubmittedBy), and must NOT touch the certificate row or the cluster --
// export approvals never issue or mutate a certificate.
func TestManagedCertificateService_OnApproved_ExportAction_CreatesGrantOnly(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo,
	)

	certID := uuid.New()
	requester := uuid.New()

	var created *models.CertificateExportGrant
	exportGrantRepo.On("Create", mock.AnythingOfType("*models.CertificateExportGrant")).Run(func(args mock.Arguments) {
		created = args.Get(0).(*models.CertificateExportGrant)
	}).Return(nil)

	approval := &models.Approval{
		ID:          uuid.New(),
		EntityType:  models.ApprovalEntityCertificate,
		Action:      models.ApprovalActionExport,
		EntityID:    certID,
		SubmittedBy: requester,
	}

	before := time.Now()
	err := svc.OnApproved(approval)
	require.NoError(t, err)

	require.NotNil(t, created)
	assert.Equal(t, certID, created.ManagedCertificateID)
	assert.Equal(t, requester, created.GrantedTo)
	assert.True(t, created.ExpiresAt.After(before), "grant must expire in the future")
	assert.WithinDuration(t, before.Add(15*time.Minute), created.ExpiresAt, 5*time.Second)

	exportGrantRepo.AssertExpectations(t)
	repo.AssertNotCalled(t, "GetByID", mock.Anything)
	repo.AssertNotCalled(t, "Update", mock.Anything)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
}

// TestManagedCertificateService_OnApproved_CreateAction_DoesNotTouchExportGrants
// guards the boundary the other way: a create-action approval must go on
// issuing the leaf certificate as before, and must never create an export
// grant.
func TestManagedCertificateService_OnApproved_CreateAction_DoesNotTouchExportGrants(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo,
	)

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
	applier.On("ApplyNamespaced", mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Return(nil)

	approval := &models.Approval{
		ID:         uuid.New(),
		EntityType: models.ApprovalEntityCertificate,
		Action:     models.ApprovalActionCreate,
		EntityID:   certID,
	}

	err := svc.OnApproved(approval)
	require.NoError(t, err)

	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 1)
	exportGrantRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestManagedCertificateService_OnApproved_ApplyFailureSetsErrorStatus(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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

// TestManagedCertificateService_Delete_InUseReturnsError covers the
// referential guard: Delete must check for referencing domains BEFORE
// deleting anything, and return ErrCertificateInUse rather than touching the
// row or the cluster when the certificate is still attached to a domain.
func TestManagedCertificateService_Delete_InUseReturnsError(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:     certID,
		Config: models.ManagedCertConfig{CertificateName: "cert-x", SecretName: "cert-x"},
	}
	repo.On("GetByID", certID).Return(cert, nil)
	domainRepo.On("ListByManagedCertificateID", certID).Return([]models.Domain{{ID: uuid.New()}}, nil)

	err := svc.Delete(certID)

	require.Error(t, err)
	assert.True(t, errors.Is(err, services.ErrCertificateInUse))
	repo.AssertNotCalled(t, "Delete", mock.Anything)
	applier.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tenantSecrets.AssertNotCalled(t, "DeleteSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
}

// TestManagedCertificateService_Delete_NoReferences_CleansUpCluster covers
// the happy path: no referencing domains, so the row is deleted and
// best-effort cluster cleanup (leaf Certificate CRD + tenant TLS Secret) is
// invoked with the right GVR/name/namespace. One of the cleanup calls
// returns an error, which must not fail the overall Delete -- the row is
// already gone and the user's action succeeded.
func TestManagedCertificateService_Delete_NoReferences_CleansUpCluster(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	certID := uuid.New()
	projectID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Config: models.ManagedCertConfig{
			CertificateName: "cert-" + certID.String(),
			SecretName:      "cert-" + certID.String(),
		},
	}
	repo.On("GetByID", certID).Return(cert, nil)
	domainRepo.On("ListByManagedCertificateID", certID).Return(nil, nil)
	repo.On("Delete", certID).Return(nil)
	applier.On("Delete", mock.Anything, kubernetes.CertManagerCertificateGVR, "cert-"+certID.String(), true).Return(errors.New("crd not found"))
	tenantSecrets.On("DeleteSecret", mock.Anything, projectID, kubernetes.FastGatewayNamespace, "cert-"+certID.String()).Return(nil)

	err := svc.Delete(certID)

	require.NoError(t, err)
	repo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
	applier.AssertExpectations(t)
	tenantSecrets.AssertExpectations(t)
}

// TestManagedCertificateService_Delete_ClientInUseReturnsError covers the
// Task 10 Client referential guard: a Client's ManagedCertificateID is a
// 1:1 FK with ON DELETE RESTRICT (Task 2), but Delete should still return
// the friendly ErrCertificateInUse sentinel (-> 409 via the handler) rather
// than surfacing a raw DB constraint violation. No domain references the
// cert here, so the guard under test is the client one, not the existing
// domain guard.
func TestManagedCertificateService_Delete_ClientInUseReturnsError(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	clientRepo := new(mocks.MockClientRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithClientRepo(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets,
		exportGrantRepo, clientRepo,
	)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:     certID,
		Config: models.ManagedCertConfig{CertificateName: "cert-x", SecretName: "cert-x"},
	}
	repo.On("GetByID", certID).Return(cert, nil)
	domainRepo.On("ListByManagedCertificateID", certID).Return(nil, nil)
	clientRepo.On("GetByManagedCertificateID", certID).Return(&models.Client{ID: uuid.New(), ManagedCertificateID: &certID}, nil)

	err := svc.Delete(certID)

	require.Error(t, err)
	assert.True(t, errors.Is(err, services.ErrCertificateInUse))
	repo.AssertNotCalled(t, "Delete", mock.Anything)
	applier.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tenantSecrets.AssertNotCalled(t, "DeleteSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
	clientRepo.AssertExpectations(t)
}

// TestManagedCertificateService_Delete_NoClientNoDomain_Proceeds is the
// counterpart to the in-use tests: when neither a domain nor a client
// references the cert, Delete proceeds to remove the row (existing
// behavior, preserved once the new Client guard is added).
func TestManagedCertificateService_Delete_NoClientNoDomain_Proceeds(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	clientRepo := new(mocks.MockClientRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithClientRepo(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets,
		exportGrantRepo, clientRepo,
	)

	certID := uuid.New()
	projectID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Config: models.ManagedCertConfig{
			CertificateName: "cert-" + certID.String(),
			SecretName:      "cert-" + certID.String(),
		},
	}
	repo.On("GetByID", certID).Return(cert, nil)
	domainRepo.On("ListByManagedCertificateID", certID).Return(nil, nil)
	clientRepo.On("GetByManagedCertificateID", certID).Return(nil, gorm.ErrRecordNotFound)
	repo.On("Delete", certID).Return(nil)
	applier.On("Delete", mock.Anything, kubernetes.CertManagerCertificateGVR, "cert-"+certID.String(), true).Return(nil)
	tenantSecrets.On("DeleteSecret", mock.Anything, projectID, kubernetes.FastGatewayNamespace, "cert-"+certID.String()).Return(nil)

	err := svc.Delete(certID)

	require.NoError(t, err)
	repo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
	clientRepo.AssertExpectations(t)
	applier.AssertExpectations(t)
	tenantSecrets.AssertExpectations(t)
}

// TestManagedCertificateService_Delete_ClientRepoErrorPropagates covers the
// third branch of the Task 10 guard: a real error from
// ClientRepo.GetByManagedCertificateID (i.e. not gorm.ErrRecordNotFound)
// must propagate as-is, and Delete must not proceed to remove the row.
func TestManagedCertificateService_Delete_ClientRepoErrorPropagates(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	clientRepo := new(mocks.MockClientRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithClientRepo(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets,
		exportGrantRepo, clientRepo,
	)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:     certID,
		Config: models.ManagedCertConfig{CertificateName: "cert-x", SecretName: "cert-x"},
	}
	dbErr := errors.New("connection reset")
	repo.On("GetByID", certID).Return(cert, nil)
	domainRepo.On("ListByManagedCertificateID", certID).Return(nil, nil)
	clientRepo.On("GetByManagedCertificateID", certID).Return(nil, dbErr)

	err := svc.Delete(certID)

	require.Error(t, err)
	assert.False(t, errors.Is(err, services.ErrCertificateInUse))
	assert.True(t, errors.Is(err, dbErr))
	repo.AssertNotCalled(t, "Delete", mock.Anything)
	applier.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tenantSecrets.AssertNotCalled(t, "DeleteSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
	clientRepo.AssertExpectations(t)
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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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

// TestManagedCertificateService_OnRejected_ExportAction_NoOp guards against
// stranding an approval: the approval engine persists Status=rejected
// BEFORE calling the completer, with no wrapping transaction, so an error
// here would leave the approval durably terminal with no way to retry. An
// export approval mints nothing until OnApproved runs, so rejecting it must
// be a pure no-op -- no repo/applier/export-grant calls at all.
func TestManagedCertificateService_OnRejected_ExportAction_NoOp(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo,
	)

	approval := &models.Approval{
		ID:          uuid.New(),
		EntityType:  models.ApprovalEntityCertificate,
		Action:      models.ApprovalActionExport,
		EntityID:    uuid.New(),
		SubmittedBy: uuid.New(),
	}

	err := svc.OnRejected(approval)
	require.NoError(t, err)

	repo.AssertNotCalled(t, "GetByID", mock.Anything)
	repo.AssertNotCalled(t, "Update", mock.Anything)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
	exportGrantRepo.AssertNotCalled(t, "Create", mock.Anything)
}

// TestManagedCertificateService_OnCancelled_ExportAction_NoOp mirrors
// TestManagedCertificateService_OnRejected_ExportAction_NoOp for the cancel
// path: cancelling an export approval before OnApproved has run has nothing
// to undo, so it must be a pure no-op too.
func TestManagedCertificateService_OnCancelled_ExportAction_NoOp(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo,
	)

	approval := &models.Approval{
		ID:          uuid.New(),
		EntityType:  models.ApprovalEntityCertificate,
		Action:      models.ApprovalActionExport,
		EntityID:    uuid.New(),
		SubmittedBy: uuid.New(),
	}

	err := svc.OnCancelled(approval)
	require.NoError(t, err)

	repo.AssertNotCalled(t, "GetByID", mock.Anything)
	repo.AssertNotCalled(t, "Delete", mock.Anything)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
	exportGrantRepo.AssertNotCalled(t, "Create", mock.Anything)
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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:          certID,
		Status:      models.ManagedCertStatusIssuing,
		Config:      models.ManagedCertConfig{CertificateName: "cert-x"},
		Fingerprint: "existingbarehexfingerprint",
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
		// Status() must NOT overwrite Fingerprint with cert-manager's
		// colon-hex status.fingerprint -- the distributor (bare-hex, via
		// SetIssuedMeta) is the sole writer of this column.
		assert.Equal(t, "existingbarehexfingerprint", updated.Fingerprint)
		assert.NotEqual(t, "AA:BB:CC", updated.Fingerprint)
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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

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

// --- ListProjectCertificatesEnriched tests ---

func TestManagedCertificateService_ListProjectCertificatesEnriched_JoinsAndNoN1(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo)

	viewerID := uuid.New()
	projectID := uuid.New()
	issuerID := uuid.New()
	certAID := uuid.New()
	certBID := uuid.New()

	certA := models.ManagedCertificate{ID: certAID, ProjectID: projectID, Name: "cert-a", IssuerID: issuerID, Status: models.ManagedCertStatusReady}
	certB := models.ManagedCertificate{ID: certBID, ProjectID: projectID, Name: "cert-b", IssuerID: issuerID, Status: models.ManagedCertStatusReady}
	certs := []models.ManagedCertificate{certA, certB}

	dist := models.CertificateDistribution{ManagedCertificateID: certAID, ProjectID: projectID, Status: models.CertDistStatusSynced}
	domain := models.Domain{ID: uuid.New(), ProjectID: projectID, Hostname: "a.example.com", ManagedCertificateID: &certAID}
	issuer := models.CertificateIssuer{ID: issuerID, Name: "Issuer A", Type: models.IssuerTypeSelfSignedCA}

	filter := repository.CertificateListFilter{Status: string(models.ManagedCertStatusReady)}

	repo.On("ListByProjectFiltered", projectID, 1, 20, filter).Return(certs, int64(2), nil)

	// The batch repos must be called with exactly the page's cert IDs -- ONE
	// call each, proving no N+1 per-cert lookups. Only certAID has a usable
	// export grant for viewerID, so ExportAvailable must be true for cert A
	// and false for cert B.
	distRepo.On("ListByCertificateIDs", []uuid.UUID{certAID, certBID}).Return([]models.CertificateDistribution{dist}, nil).Once()
	domainRepo.On("ListByManagedCertificateIDs", []uuid.UUID{certAID, certBID}).Return([]models.Domain{domain}, nil).Once()
	issuerRepo.On("List").Return([]models.CertificateIssuer{issuer}, nil).Once()
	exportGrantRepo.On("ListUsableGrantCertIDs", viewerID, []uuid.UUID{certAID, certBID}).Return([]uuid.UUID{certAID}, nil).Once()

	enriched, total, err := svc.ListProjectCertificatesEnriched(projectID, 1, 20, filter, viewerID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, enriched, 2)

	assert.Equal(t, certAID, enriched[0].Certificate.ID)
	require.NotNil(t, enriched[0].Distribution)
	assert.Equal(t, models.CertDistStatusSynced, enriched[0].Distribution.Status)
	require.Len(t, enriched[0].Domains, 1)
	assert.Equal(t, "a.example.com", enriched[0].Domains[0].Hostname)
	assert.Equal(t, "Issuer A", enriched[0].IssuerName)
	assert.Equal(t, "self_signed_ca", enriched[0].IssuerType)
	assert.True(t, enriched[0].ExportAvailable)

	assert.Equal(t, certBID, enriched[1].Certificate.ID)
	assert.Nil(t, enriched[1].Distribution)
	assert.Empty(t, enriched[1].Domains)
	assert.False(t, enriched[1].ExportAvailable)

	repo.AssertExpectations(t)
	distRepo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
	issuerRepo.AssertExpectations(t)
	exportGrantRepo.AssertExpectations(t)
}

func TestManagedCertificateService_ListProjectCertificatesEnriched_EmptyPage_NoBatchCalls(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo)

	viewerID := uuid.New()
	projectID := uuid.New()
	filter := repository.CertificateListFilter{}
	repo.On("ListByProjectFiltered", projectID, 1, 20, filter).Return([]models.ManagedCertificate{}, int64(0), nil)
	distRepo.On("ListByCertificateIDs", []uuid.UUID{}).Return([]models.CertificateDistribution{}, nil).Once()
	domainRepo.On("ListByManagedCertificateIDs", []uuid.UUID{}).Return([]models.Domain{}, nil).Once()
	issuerRepo.On("List").Return([]models.CertificateIssuer{}, nil).Once()
	exportGrantRepo.On("ListUsableGrantCertIDs", viewerID, []uuid.UUID{}).Return([]uuid.UUID{}, nil).Once()

	enriched, total, err := svc.ListProjectCertificatesEnriched(projectID, 1, 20, filter, viewerID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, enriched)

	repo.AssertExpectations(t)
}

func TestManagedCertificateService_ListProjectCertificatesEnriched_RepoError_Propagates(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	projectID := uuid.New()
	filter := repository.CertificateListFilter{}
	boom := errors.New("db exploded")
	repo.On("ListByProjectFiltered", projectID, 1, 20, filter).Return(nil, int64(0), boom)

	_, _, err := svc.ListProjectCertificatesEnriched(projectID, 1, 20, filter, uuid.New())
	require.Error(t, err)
	distRepo.AssertNotCalled(t, "ListByCertificateIDs", mock.Anything)
	domainRepo.AssertNotCalled(t, "ListByManagedCertificateIDs", mock.Anything)
	issuerRepo.AssertNotCalled(t, "List")
}

// --- ListFleetCertificates tests ---

// Verifies ListFleetCertificates lists across projects (via repo.ListFleet)
// and reuses the same enrich helper as ListProjectCertificatesEnriched --
// each batch repo called exactly once with the page's cert IDs, no per-cert
// N+1 lookups.
func TestManagedCertificateService_ListFleetCertificates_JoinsAndNoN1(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo)

	viewerID := uuid.New()
	projectA := uuid.New()
	projectB := uuid.New()
	issuerID := uuid.New()
	certAID := uuid.New()
	certBID := uuid.New()

	// certA and certB belong to DIFFERENT projects -- proving this is a
	// cross-project (fleet) listing, not a project-scoped one.
	certA := models.ManagedCertificate{ID: certAID, ProjectID: projectA, Name: "cert-a", IssuerID: issuerID, Status: models.ManagedCertStatusReady}
	certB := models.ManagedCertificate{ID: certBID, ProjectID: projectB, Name: "cert-b", IssuerID: issuerID, Status: models.ManagedCertStatusReady}
	certs := []models.ManagedCertificate{certA, certB}

	dist := models.CertificateDistribution{ManagedCertificateID: certAID, ProjectID: projectA, Status: models.CertDistStatusSynced}
	domain := models.Domain{ID: uuid.New(), ProjectID: projectA, Hostname: "a.example.com", ManagedCertificateID: &certAID}
	issuer := models.CertificateIssuer{ID: issuerID, Name: "Issuer A", Type: models.IssuerTypeSelfSignedCA}

	filter := repository.CertificateListFilter{Status: string(models.ManagedCertStatusReady)}

	repo.On("ListFleet", 1, 20, filter).Return(certs, int64(2), nil)

	distRepo.On("ListByCertificateIDs", []uuid.UUID{certAID, certBID}).Return([]models.CertificateDistribution{dist}, nil).Once()
	domainRepo.On("ListByManagedCertificateIDs", []uuid.UUID{certAID, certBID}).Return([]models.Domain{domain}, nil).Once()
	issuerRepo.On("List").Return([]models.CertificateIssuer{issuer}, nil).Once()
	exportGrantRepo.On("ListUsableGrantCertIDs", viewerID, []uuid.UUID{certAID, certBID}).Return([]uuid.UUID{certAID}, nil).Once()

	enriched, total, err := svc.ListFleetCertificates(1, 20, filter, viewerID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, enriched, 2)

	assert.Equal(t, certAID, enriched[0].Certificate.ID)
	assert.Equal(t, projectA, enriched[0].Certificate.ProjectID)
	require.NotNil(t, enriched[0].Distribution)
	assert.Equal(t, models.CertDistStatusSynced, enriched[0].Distribution.Status)
	require.Len(t, enriched[0].Domains, 1)
	assert.Equal(t, "a.example.com", enriched[0].Domains[0].Hostname)
	assert.Equal(t, "Issuer A", enriched[0].IssuerName)
	assert.True(t, enriched[0].ExportAvailable)

	assert.Equal(t, certBID, enriched[1].Certificate.ID)
	assert.Equal(t, projectB, enriched[1].Certificate.ProjectID)
	assert.Nil(t, enriched[1].Distribution)
	assert.Empty(t, enriched[1].Domains)
	assert.False(t, enriched[1].ExportAvailable)

	repo.AssertExpectations(t)
	distRepo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
	issuerRepo.AssertExpectations(t)
	exportGrantRepo.AssertExpectations(t)
}

// Verifies the projectId filter passes through to repo.ListFleet
// unmodified -- unlike ListProjectCertificatesEnriched, the fleet method
// does NOT ignore f.ProjectID, since there is no path-derived project scope
// to fall back on.
func TestManagedCertificateService_ListFleetCertificates_ProjectIDFilterPassesThrough(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo)

	viewerID := uuid.New()
	projectID := uuid.New()
	filter := repository.CertificateListFilter{ProjectID: &projectID}

	repo.On("ListFleet", 1, 20, filter).Return([]models.ManagedCertificate{}, int64(0), nil)
	distRepo.On("ListByCertificateIDs", []uuid.UUID{}).Return([]models.CertificateDistribution{}, nil).Once()
	domainRepo.On("ListByManagedCertificateIDs", []uuid.UUID{}).Return([]models.Domain{}, nil).Once()
	issuerRepo.On("List").Return([]models.CertificateIssuer{}, nil).Once()
	exportGrantRepo.On("ListUsableGrantCertIDs", viewerID, []uuid.UUID{}).Return([]uuid.UUID{}, nil).Once()

	enriched, total, err := svc.ListFleetCertificates(1, 20, filter, viewerID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, enriched)

	repo.AssertExpectations(t)
}

// Verifies a repo.ListFleet error propagates without calling any of the
// batch enrichment repos.
func TestManagedCertificateService_ListFleetCertificates_RepoError_Propagates(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	filter := repository.CertificateListFilter{}
	boom := errors.New("db exploded")
	repo.On("ListFleet", 1, 20, filter).Return(nil, int64(0), boom)

	_, _, err := svc.ListFleetCertificates(1, 20, filter, uuid.New())
	require.Error(t, err)
	distRepo.AssertNotCalled(t, "ListByCertificateIDs", mock.Anything)
	domainRepo.AssertNotCalled(t, "ListByManagedCertificateIDs", mock.Anything)
	issuerRepo.AssertNotCalled(t, "List")
}

// Verifies HasUsableExportGrant delegates directly to the export grant
// repository's read-only HasUsableGrant.
func TestManagedCertificateService_HasUsableExportGrant_Delegates(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo)

	certID := uuid.New()
	userID := uuid.New()
	exportGrantRepo.On("HasUsableGrant", certID, userID).Return(true, nil).Once()

	ok, err := svc.HasUsableExportGrant(certID, userID)
	require.NoError(t, err)
	assert.True(t, ok)

	exportGrantRepo.AssertExpectations(t)
}

// =============================================================================
// Task 5: usage=client + keyMode (managed | csr) create/issue/status
// =============================================================================

// generateTestCSR builds a PEM-encoded PKCS#10 CertificateRequest for
// commonName, used to exercise the csr key-mode path without a real key
// management system.
func generateTestCSR(t *testing.T, commonName string) []byte {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.CertificateRequest{
		Subject: pkix.Name{CommonName: commonName},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &template, priv)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

// generateTestLeafCert builds a self-signed leaf certificate PEM with the
// given NotAfter, truncated to whole seconds so it round-trips exactly
// through PEM/DER for comparison. Mirrors certdist_test.go's
// generateTestCert -- used here to stand in for the signed leaf PEM
// cert-manager would place on a CertificateRequest's status.certificate.
func generateTestLeafCert(t *testing.T, notAfter time.Time) []byte {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "client-a"},
		NotBefore:    time.Now().Add(-time.Hour).Truncate(time.Second),
		NotAfter:     notAfter.Truncate(time.Second),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// TestManagedCertificateService_Create_ClientUsageWithACMEIssuer_ReturnsErrClientRequiresPrivateCA
// covers ValidateCertificateKind's client/private-CA rule: Create must
// consult the issuer's Type (loaded via issuerRepo.GetByID) and reject
// before doing ANY persistence or cluster work.
func TestManagedCertificateService_Create_ClientUsageWithACMEIssuer_ReturnsErrClientRequiresPrivateCA(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	projectID := uuid.New()
	issuerID := uuid.New()

	grantRepo.On("Exists", issuerID, projectID).Return(true, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Type:   models.IssuerTypeACME,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-acme"},
	}, nil)

	cert, approval, err := svc.Create(projectID, &services.CreateCertificateInput{
		Name:     "client-a",
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageClient,
		Subject:  "CN=client-a",
	}, uuid.New())

	require.Error(t, err)
	assert.True(t, errors.Is(err, services.ErrClientRequiresPrivateCA))
	assert.Nil(t, cert)
	assert.Nil(t, approval)
	issuerRepo.AssertExpectations(t)
	grantRepo.AssertExpectations(t)
	repo.AssertNotCalled(t, "Create", mock.Anything)
	projectRepo.AssertNotCalled(t, "GetByID", mock.Anything)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
}

// TestManagedCertificateService_Create_ServerUsageWithCSRKeyMode_RejectsBeforeServerCheck
// covers the usage=server + keyMode=csr combination named in the task
// brief. ValidateCertificateKind's checks run in order: the
// keyMode==csr-requires-client rule is evaluated before the
// server-requires-managed rule, so this combination actually surfaces
// ErrCSRRequiresClient, not ErrServerRequiresManagedKey -- asserted here
// against the real ValidateCertificateKind return value rather than assumed.
func TestManagedCertificateService_Create_ServerUsageWithCSRKeyMode_RejectsBeforeServerCheck(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	projectID := uuid.New()
	issuerID := uuid.New()

	grantRepo.On("Exists", issuerID, projectID).Return(true, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Type:   models.IssuerTypeSelfSignedCA,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-ca"},
	}, nil)

	cert, approval, err := svc.Create(projectID, &services.CreateCertificateInput{
		Name:     "server-a",
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageServer,
		DNSNames: []string{"example.com"},
		KeyMode:  models.ManagedCertKeyModeCSR,
	}, uuid.New())

	require.Error(t, err)
	assert.True(t, errors.Is(err, services.ErrCSRRequiresClient))
	assert.Nil(t, cert)
	assert.Nil(t, approval)
	repo.AssertNotCalled(t, "Create", mock.Anything)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
}

// TestManagedCertificateService_Create_CSRKeyModeWithoutCSR_ReturnsErrCSRRequired
// covers Create's own guard (beyond ValidateCertificateKind): csr mode
// requires an actual CSR payload.
func TestManagedCertificateService_Create_CSRKeyModeWithoutCSR_ReturnsErrCSRRequired(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	projectID := uuid.New()
	issuerID := uuid.New()

	grantRepo.On("Exists", issuerID, projectID).Return(true, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Type:   models.IssuerTypeSelfSignedCA,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-ca"},
	}, nil)

	cert, approval, err := svc.Create(projectID, &services.CreateCertificateInput{
		Name:     "client-a",
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageClient,
		Subject:  "CN=client-a",
		KeyMode:  models.ManagedCertKeyModeCSR,
	}, uuid.New())

	require.Error(t, err)
	assert.True(t, errors.Is(err, services.ErrCSRRequired))
	assert.Nil(t, cert)
	assert.Nil(t, approval)
	repo.AssertNotCalled(t, "Create", mock.Anything)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
}

// TestManagedCertificateService_Create_CSRKeyModeMalformedCSR_ReturnsParseError
// covers Create's CSR-parses-as-a-real-CSR guard.
func TestManagedCertificateService_Create_CSRKeyModeMalformedCSR_ReturnsParseError(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	projectID := uuid.New()
	issuerID := uuid.New()

	grantRepo.On("Exists", issuerID, projectID).Return(true, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Type:   models.IssuerTypeSelfSignedCA,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-ca"},
	}, nil)

	cert, approval, err := svc.Create(projectID, &services.CreateCertificateInput{
		Name:     "client-a",
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageClient,
		Subject:  "CN=client-a",
		KeyMode:  models.ManagedCertKeyModeCSR,
		CSR:      "not-a-valid-pem-csr",
	}, uuid.New())

	require.Error(t, err)
	assert.False(t, errors.Is(err, services.ErrCSRRequired), "malformed CSR must be a parse error, not the missing-CSR sentinel")
	assert.Nil(t, cert)
	assert.Nil(t, approval)
	repo.AssertNotCalled(t, "Create", mock.Anything)
	applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
}

// TestManagedCertificateService_Create_ManagedClientCert_PersistsKeyModeAndAppliesClientCertificate
// covers usage=client + keyMode=managed against a self-signed-CA issuer
// (the valid managed-client combination): Create must persist
// Config.KeyMode=managed and Config.URISANs, and OnApproved (via the
// approvals-disabled fast path) must apply a Certificate (not a
// CertificateRequest) whose spec carries client-auth usages and the
// URISANs as uris.
func TestManagedCertificateService_Create_ManagedClientCert_PersistsKeyModeAndAppliesClientCertificate(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	projectID := uuid.New()
	issuerID := uuid.New()
	uriSANs := []string{"spiffe://example.org/client-a"}

	grantRepo.On("Exists", issuerID, projectID).Return(true, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Type:   models.IssuerTypeSelfSignedCA,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-ca"},
	}, nil)
	projectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, ApprovalEnabled: false}, nil)

	repo.On("Create", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		c := args.Get(0).(*models.ManagedCertificate)
		c.ID = uuid.New()
	}).Return(nil)

	// Capture every Update call's Config -- the FIRST one is Create's own
	// persisted state (what we assert here); a later one (from OnApproved's
	// fast path) reflects the canned repo.GetByID cert below, not Create's
	// real computation.
	var updateConfigs []models.ManagedCertConfig
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		c := args.Get(0).(*models.ManagedCertificate)
		updateConfigs = append(updateConfigs, c.Config)
	}).Return(nil)

	repo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(func(id uuid.UUID) *models.ManagedCertificate {
		return &models.ManagedCertificate{
			ID:       id,
			IssuerID: issuerID,
			Usage:    models.ManagedCertUsageClient,
			Config: models.ManagedCertConfig{
				Subject:         "CN=client-a",
				CertificateName: "cert-" + id.String(),
				SecretName:      "cert-" + id.String(),
				KeyAlgorithm:    "RSA",
				KeySize:         2048,
				DurationDays:    90,
				KeyMode:         models.ManagedCertKeyModeManaged,
				URISANs:         uriSANs,
			},
		}
	}, nil)
	applier.On("Namespace").Return("fastgateway-system")

	var appliedGVR schema.GroupVersionResource
	var appliedObj *unstructured.Unstructured
	applier.On("ApplyNamespaced", mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Run(func(args mock.Arguments) {
		appliedGVR = args.Get(1).(schema.GroupVersionResource)
		appliedObj = args.Get(2).(*unstructured.Unstructured)
	}).Return(nil)

	cert, approval, err := svc.Create(projectID, &services.CreateCertificateInput{
		Name:     "client-a",
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageClient,
		Subject:  "CN=client-a",
		KeyMode:  models.ManagedCertKeyModeManaged,
		URISANs:  uriSANs,
	}, uuid.New())

	require.NoError(t, err)
	require.NotNil(t, cert)
	assert.Nil(t, approval)
	assert.False(t, submitter.submitCalled)

	require.NotEmpty(t, updateConfigs)
	assert.Equal(t, models.ManagedCertKeyModeManaged, updateConfigs[0].KeyMode)
	assert.Equal(t, uriSANs, updateConfigs[0].URISANs)

	assert.Equal(t, kubernetes.CertManagerCertificateGVR, appliedGVR)
	require.NotNil(t, appliedObj)
	assert.Equal(t, "Certificate", appliedObj.Object["kind"])
	spec, ok := appliedObj.Object["spec"].(map[string]interface{})
	require.True(t, ok, "spec must be a map")
	assert.Equal(t, []interface{}{"client auth", "digital signature", "key encipherment"}, spec["usages"])
	assert.Equal(t, []interface{}{"spiffe://example.org/client-a"}, spec["uris"])
	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 1)
}

// TestManagedCertificateService_Create_CSRClientCert_OnApprovedAppliesCertificateRequestNotCertificate
// covers usage=client + keyMode=csr against a self-signed-CA issuer:
// OnApproved must apply a CertificateRequest (CertManagerCertificateRequestGVR),
// never a Certificate, carrying the caller's CSR base64-encoded as
// spec.request -- and Create must persist Config.CSRPEM so OnApproved (a
// fresh row load) can read it back.
func TestManagedCertificateService_Create_CSRClientCert_OnApprovedAppliesCertificateRequestNotCertificate(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	projectID := uuid.New()
	issuerID := uuid.New()
	csrPEM := generateTestCSR(t, "client-a")

	grantRepo.On("Exists", issuerID, projectID).Return(true, nil)
	issuerRepo.On("GetByID", issuerID).Return(&models.CertificateIssuer{
		ID:     issuerID,
		Type:   models.IssuerTypeSelfSignedCA,
		Config: models.IssuerConfig{ClusterIssuerName: "iss-ca"},
	}, nil)
	projectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, ApprovalEnabled: false}, nil)

	repo.On("Create", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		c := args.Get(0).(*models.ManagedCertificate)
		c.ID = uuid.New()
	}).Return(nil)

	var updateConfigs []models.ManagedCertConfig
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		c := args.Get(0).(*models.ManagedCertificate)
		updateConfigs = append(updateConfigs, c.Config)
	}).Return(nil)

	repo.On("GetByID", mock.AnythingOfType("uuid.UUID")).Return(func(id uuid.UUID) *models.ManagedCertificate {
		return &models.ManagedCertificate{
			ID:       id,
			IssuerID: issuerID,
			Usage:    models.ManagedCertUsageClient,
			Config: models.ManagedCertConfig{
				Subject:         "CN=client-a",
				CertificateName: "cert-" + id.String(),
				DurationDays:    90,
				KeyMode:         models.ManagedCertKeyModeCSR,
				CSRPEM:          string(csrPEM),
			},
		}
	}, nil)
	applier.On("Namespace").Return("fastgateway-system")

	var appliedGVR schema.GroupVersionResource
	var appliedObj *unstructured.Unstructured
	applier.On("ApplyNamespaced", mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Run(func(args mock.Arguments) {
		appliedGVR = args.Get(1).(schema.GroupVersionResource)
		appliedObj = args.Get(2).(*unstructured.Unstructured)
	}).Return(nil)

	cert, approval, err := svc.Create(projectID, &services.CreateCertificateInput{
		Name:     "client-a",
		IssuerID: issuerID,
		Usage:    models.ManagedCertUsageClient,
		Subject:  "CN=client-a",
		KeyMode:  models.ManagedCertKeyModeCSR,
		CSR:      string(csrPEM),
	}, uuid.New())

	require.NoError(t, err)
	require.NotNil(t, cert)
	assert.Nil(t, approval)

	require.NotEmpty(t, updateConfigs)
	assert.Equal(t, models.ManagedCertKeyModeCSR, updateConfigs[0].KeyMode)
	assert.Equal(t, string(csrPEM), updateConfigs[0].CSRPEM)

	assert.Equal(t, kubernetes.CertManagerCertificateRequestGVR, appliedGVR)
	assert.NotEqual(t, kubernetes.CertManagerCertificateGVR, appliedGVR)
	require.NotNil(t, appliedObj)
	assert.Equal(t, "CertificateRequest", appliedObj.Object["kind"])
	spec, ok := appliedObj.Object["spec"].(map[string]interface{})
	require.True(t, ok, "spec must be a map")
	reqB64, ok := spec["request"].(string)
	require.True(t, ok, "spec.request must be a base64 string")
	assert.NotEmpty(t, reqB64)
	decoded, err := base64.StdEncoding.DecodeString(reqB64)
	require.NoError(t, err)
	assert.Equal(t, csrPEM, decoded)
	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 1)
}

// TestManagedCertificateService_Status_CSRKeyMode_ReadyTrue_ReadsCertificateRequest
// covers Status's csr branch: for a csr-mode certificate, Status must read
// the CertificateRequest (not the Certificate) and, once its Ready
// condition is True, derive NotAfter from status.certificate.
func TestManagedCertificateService_Status_CSRKeyMode_ReadyTrue_ReadsCertificateRequest(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	certID := uuid.New()
	notAfter := time.Now().Add(72 * time.Hour)
	leafPEM := generateTestLeafCert(t, notAfter)

	cert := &models.ManagedCertificate{
		ID:     certID,
		Status: models.ManagedCertStatusIssuing,
		Config: models.ManagedCertConfig{
			CertificateName: "cert-x",
			KeyMode:         models.ManagedCertKeyModeCSR,
		},
	}
	repo.On("GetByID", certID).Return(cert, nil)

	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "CertificateRequest",
		"metadata":   map[string]interface{}{"name": "cert-x", "namespace": "fastgateway-system"},
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True", "message": ""},
			},
			"certificate": base64.StdEncoding.EncodeToString(leafPEM),
		},
	}}
	applier.On("Get", mock.Anything, kubernetes.CertManagerCertificateRequestGVR, "cert-x", true).Return(obj, nil)
	repo.On("Update", mock.AnythingOfType("*models.ManagedCertificate")).Run(func(args mock.Arguments) {
		updated := args.Get(0).(*models.ManagedCertificate)
		assert.Equal(t, models.ManagedCertStatusReady, updated.Status)
		require.NotNil(t, updated.NotAfter)
		assert.WithinDuration(t, notAfter.Truncate(time.Second), *updated.NotAfter, time.Second)
	}).Return(nil)

	result, err := svc.Status(certID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.ManagedCertStatusReady, result.Status)
	require.NotNil(t, result.NotAfter)
	applier.AssertExpectations(t)
	applier.AssertNotCalled(t, "Get", mock.Anything, kubernetes.CertManagerCertificateGVR, mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}

// --- RequestExport / ExportBundle tests (Task 7) ---

// coreSecretObject builds a minimal unstructured core Secret with the given
// plaintext values base64-encoded under .data, matching the shape
// controlPlane.Get returns for a real Secret.
func coreSecretObject(name string, plaintext map[string]string) *unstructured.Unstructured {
	data := make(map[string]interface{}, len(plaintext))
	for k, v := range plaintext {
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]interface{}{"name": name, "namespace": "fastgateway-system"},
		"data":       data,
	}}
}

// TestManagedCertificateService_RequestExport_CSRMode_ReturnsErrNotApplicable
// verifies a csr-key-mode certificate can never be exported -- the caller
// already holds the private key, so there is nothing server-side to grant
// access to. The project/approval path must never even be reached.
func TestManagedCertificateService_RequestExport_CSRMode_ReturnsErrNotApplicable(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:     certID,
		Config: models.ManagedCertConfig{KeyMode: models.ManagedCertKeyModeCSR},
	}
	repo.On("GetByID", certID).Return(cert, nil)

	approval, err := svc.RequestExport(certID, uuid.New())

	require.ErrorIs(t, err, services.ErrExportNotApplicable)
	assert.Nil(t, approval)
	assert.False(t, submitter.submitCalled)
	projectRepo.AssertNotCalled(t, "GetByID", mock.Anything)
}

// TestManagedCertificateService_RequestExport_NotReady_ReturnsErrNotReady
// verifies RequestExport refuses to open (or fast-path mint) an export grant
// for a managed-key certificate that hasn't finished issuing yet -- the leaf
// Secret cert-manager would eventually push doesn't exist yet, so a grant
// minted now would just 500 on download. The project/approval path must
// never be reached, mirroring the csr-mode guard above.
func TestManagedCertificateService_RequestExport_NotReady_ReturnsErrNotReady(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:     certID,
		Status: models.ManagedCertStatusIssuing,
		Config: models.ManagedCertConfig{KeyMode: models.ManagedCertKeyModeManaged},
	}
	repo.On("GetByID", certID).Return(cert, nil)

	approval, err := svc.RequestExport(certID, uuid.New())

	require.ErrorIs(t, err, services.ErrCertificateNotReadyForExport)
	assert.Nil(t, approval)
	assert.False(t, submitter.submitCalled)
	projectRepo.AssertNotCalled(t, "GetByID", mock.Anything)
}

// TestManagedCertificateService_RequestExport_ManagedMode_SubmitsExportApproval
// verifies the approval-gated path: a managed-key certificate in a project
// with approvals enabled must submit an ApprovalActionExport approval
// carrying the certificate's project, id, and the requesting user.
func TestManagedCertificateService_RequestExport_ManagedMode_SubmitsExportApproval(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateService(repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets)

	certID := uuid.New()
	projectID := uuid.New()
	requestedBy := uuid.New()
	cert := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Status:    models.ManagedCertStatusReady,
		Config:    models.ManagedCertConfig{KeyMode: models.ManagedCertKeyModeManaged},
	}
	repo.On("GetByID", certID).Return(cert, nil)
	projectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, ApprovalEnabled: true}, nil)

	approval, err := svc.RequestExport(certID, requestedBy)

	require.NoError(t, err)
	require.NotNil(t, approval)
	assert.True(t, submitter.submitCalled)
	assert.Equal(t, models.ApprovalEntityCertificate, submitter.lastSpec.EntityType)
	assert.Equal(t, models.ApprovalActionExport, submitter.lastSpec.Action)
	assert.Equal(t, certID, submitter.lastSpec.EntityID)
	assert.Equal(t, projectID, submitter.lastSpec.ProjectID)
	assert.Equal(t, requestedBy, submitter.lastSpec.SubmittedBy)
	repo.AssertExpectations(t)
	projectRepo.AssertExpectations(t)
}

// TestManagedCertificateService_RequestExport_ManagedMode_FastPath_MintsGrantImmediately
// mirrors Create's fast path: when the project has approvals disabled,
// RequestExport must mint the export grant synchronously (via OnApproved)
// rather than going through the approval engine, and return a nil approval
// -- exactly like Create's fast path returns a nil *models.Approval.
func TestManagedCertificateService_RequestExport_ManagedMode_FastPath_MintsGrantImmediately(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo,
	)

	certID := uuid.New()
	projectID := uuid.New()
	requestedBy := uuid.New()
	cert := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Status:    models.ManagedCertStatusReady,
		Config:    models.ManagedCertConfig{KeyMode: models.ManagedCertKeyModeManaged},
	}
	repo.On("GetByID", certID).Return(cert, nil)
	projectRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, ApprovalEnabled: false}, nil)

	var created *models.CertificateExportGrant
	exportGrantRepo.On("Create", mock.AnythingOfType("*models.CertificateExportGrant")).Run(func(args mock.Arguments) {
		created = args.Get(0).(*models.CertificateExportGrant)
	}).Return(nil)

	approval, err := svc.RequestExport(certID, requestedBy)

	require.NoError(t, err)
	assert.Nil(t, approval)
	assert.False(t, submitter.submitCalled)
	require.NotNil(t, created)
	assert.Equal(t, certID, created.ManagedCertificateID)
	assert.Equal(t, requestedBy, created.GrantedTo)
	exportGrantRepo.AssertExpectations(t)
}

// TestManagedCertificateService_ExportBundle_HappyPath_ReturnsThreePEMs
// verifies the full read path: a consumed grant, the leaf Secret's
// tls.crt/tls.key, and the issuer CA Secret's ca.crt, all base64-decoded
// exactly the way certdist's ReadLeafSecret does.
func TestManagedCertificateService_ExportBundle_HappyPath_ReturnsThreePEMs(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo,
	)

	certID := uuid.New()
	issuerID := uuid.New()
	userID := uuid.New()

	cert := &models.ManagedCertificate{
		ID:       certID,
		IssuerID: issuerID,
		Config:   models.ManagedCertConfig{SecretName: "cert-" + certID.String()},
	}
	issuer := &models.CertificateIssuer{
		ID:     issuerID,
		Config: models.IssuerConfig{CASecretName: "issuer-ca-secret"},
	}

	grant := &models.CertificateExportGrant{ID: uuid.New(), ManagedCertificateID: certID, GrantedTo: userID}
	exportGrantRepo.On("ConsumeForCert", certID, userID).Return(grant, nil)
	repo.On("GetByID", certID).Return(cert, nil)
	issuerRepo.On("GetByID", issuerID).Return(issuer, nil)

	leafSecret := coreSecretObject(cert.Config.SecretName, map[string]string{
		"tls.crt": "leaf-cert-pem-bytes",
		"tls.key": "-----BEGIN PRIVATE KEY-----\nleaf-key-pem-bytes\n-----END PRIVATE KEY-----",
	})
	caSecret := coreSecretObject(issuer.Config.CASecretName, map[string]string{
		"ca.crt": "ca-chain-pem-bytes",
	})
	applier.On("Get", mock.Anything, kubernetes.CoreSecretGVR, cert.Config.SecretName, true).Return(leafSecret, nil)
	applier.On("Get", mock.Anything, kubernetes.CoreSecretGVR, issuer.Config.CASecretName, true).Return(caSecret, nil)

	leafPEM, keyPEM, caPEM, err := svc.ExportBundle(certID, userID)

	require.NoError(t, err)
	assert.Equal(t, "leaf-cert-pem-bytes", string(leafPEM))
	assert.Contains(t, string(keyPEM), "BEGIN PRIVATE KEY")
	assert.Equal(t, "ca-chain-pem-bytes", string(caPEM))
	exportGrantRepo.AssertExpectations(t)
	repo.AssertExpectations(t)
	issuerRepo.AssertExpectations(t)
	applier.AssertExpectations(t)
}

// TestManagedCertificateService_ExportBundle_CAFallsBackToTLSCrt verifies
// the CA-secret fallback: a self-signed-CA issuer's CA Secret has no ca.crt
// key at all (it's a plain tls.crt/tls.key pair), so ExportBundle must fall
// back to tls.crt for the CA chain instead of erroring.
func TestManagedCertificateService_ExportBundle_CAFallsBackToTLSCrt(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo,
	)

	certID := uuid.New()
	issuerID := uuid.New()
	userID := uuid.New()

	cert := &models.ManagedCertificate{
		ID:       certID,
		IssuerID: issuerID,
		Config:   models.ManagedCertConfig{SecretName: "cert-" + certID.String()},
	}
	issuer := &models.CertificateIssuer{
		ID:     issuerID,
		Config: models.IssuerConfig{CASecretName: "issuer-ca-secret"},
	}

	exportGrantRepo.On("ConsumeForCert", certID, userID).Return(&models.CertificateExportGrant{}, nil)
	repo.On("GetByID", certID).Return(cert, nil)
	issuerRepo.On("GetByID", issuerID).Return(issuer, nil)

	leafSecret := coreSecretObject(cert.Config.SecretName, map[string]string{
		"tls.crt": "leaf-cert-pem-bytes",
		"tls.key": "leaf-key-pem-bytes",
	})
	caSecretNoCACrt := coreSecretObject(issuer.Config.CASecretName, map[string]string{
		"tls.crt": "self-signed-ca-pem-bytes",
		"tls.key": "self-signed-ca-key-pem-bytes",
	})
	applier.On("Get", mock.Anything, kubernetes.CoreSecretGVR, cert.Config.SecretName, true).Return(leafSecret, nil)
	applier.On("Get", mock.Anything, kubernetes.CoreSecretGVR, issuer.Config.CASecretName, true).Return(caSecretNoCACrt, nil)

	_, _, caPEM, err := svc.ExportBundle(certID, userID)

	require.NoError(t, err)
	assert.Equal(t, "self-signed-ca-pem-bytes", string(caPEM))
}

// TestManagedCertificateService_ExportBundle_GrantUnavailable_PropagatesAndSkipsSecretRead
// verifies single-use enforcement: when ConsumeForCert reports the grant is
// unavailable (already consumed, expired, or never existed for this user),
// ExportBundle must propagate that error immediately and must NEVER read
// the certificate row or any Secret -- a reused/invalid request must not
// touch key material at all.
func TestManagedCertificateService_ExportBundle_GrantUnavailable_PropagatesAndSkipsSecretRead(t *testing.T) {
	repo := new(mocks.MockManagedCertificateRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	projectRepo := new(mocks.MockProjectRepository)
	applier := new(mocks.MockCertInfraApplier)
	distRepo := new(mocks.MockCertificateDistributionRepository)
	domainRepo := new(mocks.MockDomainRepository)
	tenantSecrets := new(mocks.MockTenantSecretDeleter)
	exportGrantRepo := new(mocks.MockCertificateExportGrantRepository)
	submitter := &fakeCertApprovalSubmitter{}

	svc := newTestManagedCertificateServiceWithExportGrant(
		repo, issuerRepo, grantRepo, projectRepo, applier, submitter, distRepo, domainRepo, tenantSecrets, exportGrantRepo,
	)

	certID := uuid.New()
	userID := uuid.New()
	exportGrantRepo.On("ConsumeForCert", certID, userID).Return(nil, repository.ErrExportGrantUnavailable)

	leafPEM, keyPEM, caPEM, err := svc.ExportBundle(certID, userID)

	require.ErrorIs(t, err, repository.ErrExportGrantUnavailable)
	assert.Nil(t, leafPEM)
	assert.Nil(t, keyPEM)
	assert.Nil(t, caPEM)
	repo.AssertNotCalled(t, "GetByID", mock.Anything)
	issuerRepo.AssertNotCalled(t, "GetByID", mock.Anything)
	applier.AssertNotCalled(t, "Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	exportGrantRepo.AssertExpectations(t)
}

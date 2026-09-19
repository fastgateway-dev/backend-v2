package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/crypto"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestCertificateIssuerService_CreateSelfSignedCA_AppliesCRDs(t *testing.T) {
	repo := new(mocks.MockCertificateIssuerRepository)
	applier := new(mocks.MockCertInfraApplier)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	dnsSvc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: new(mocks.MockDNSProviderCredentialRepository), Config: cfg, IssuerRepo: repo})
	svc := services.NewCertificateIssuerService(services.CertificateIssuerServiceDeps{
		Repo: repo, DNSCreds: dnsSvc, ControlPlane: applier, Config: cfg, ManagedCertRepo: new(mocks.MockManagedCertificateRepository),
	})

	applier.On("Namespace").Return("fastgateway-system")
	applier.On("ApplyClusterScoped", mock.Anything, mock.AnythingOfType("schema.GroupVersionResource"), mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	applier.On("ApplyNamespaced", mock.Anything, mock.AnythingOfType("schema.GroupVersionResource"), mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	repo.On("Create", mock.AnythingOfType("*models.CertificateIssuer")).Return(nil)
	repo.On("Update", mock.AnythingOfType("*models.CertificateIssuer")).Return(nil)

	out, err := svc.Create(&services.CreateIssuerInput{
		Type: "self_signed_ca", Name: "root", CommonName: "FGW Root",
		KeyAlgorithm: "RSA", KeySize: 4096, DurationDays: 3650,
	}, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, models.IssuerTypeSelfSignedCA, out.Type)
	// self-signed CA applies: SelfSigned ClusterIssuer + CA ClusterIssuer (cluster-scoped) and CA Certificate (namespaced)
	applier.AssertNumberOfCalls(t, "ApplyClusterScoped", 2)
	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 1)
	repo.AssertExpectations(t)

	_ = context.Background()
	_ = unstructured.Unstructured{}
	_ = schema.GroupVersionResource{}
}

func TestCertificateIssuerService_CreateACME_AppliesSolverAndIssuer(t *testing.T) {
	repo := new(mocks.MockCertificateIssuerRepository)
	dnsRepo := new(mocks.MockDNSProviderCredentialRepository)
	applier := new(mocks.MockCertInfraApplier)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	dnsSvc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: dnsRepo, Config: cfg, IssuerRepo: repo})
	svc := services.NewCertificateIssuerService(services.CertificateIssuerServiceDeps{
		Repo: repo, DNSCreds: dnsSvc, ControlPlane: applier, Config: cfg, ManagedCertRepo: new(mocks.MockManagedCertificateRepository),
	})

	credID := uuid.New()
	encToken, err := crypto.Encrypt("plain-api-token", cfg.EncryptionKey)
	require.NoError(t, err)
	dnsRepo.On("GetByID", credID).Return(&models.DNSProviderCredential{
		ID: credID, ProviderType: "cloudflare",
		Credentials: models.DNSCredentialData{"apiToken": encToken},
	}, nil)

	applier.On("Namespace").Return("fastgateway-system")
	applier.On("ApplyNamespaced", mock.Anything, kubernetes.SecretGVR, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	applier.On("ApplyClusterScoped", mock.Anything, kubernetes.CertManagerClusterIssuerGVR, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	repo.On("Create", mock.AnythingOfType("*models.CertificateIssuer")).Return(nil)
	repo.On("Update", mock.AnythingOfType("*models.CertificateIssuer")).Return(nil)

	out, err := svc.Create(&services.CreateIssuerInput{
		Type: "acme", Name: "letsencrypt-zerossl", Server: "https://acme.zerossl.com/v2/DV90",
		Email: "ops@example.com", EABKeyID: "eab-key-id", EABHMACKey: "eab-hmac-key",
		DNSCredentialID: &credID,
	}, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, models.IssuerTypeACME, out.Type)
	assert.Equal(t, models.IssuerStatusReady, out.Status)

	// solver Secret + EAB Secret, both namespaced; ACME ClusterIssuer, cluster-scoped
	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 2)
	applier.AssertNumberOfCalls(t, "ApplyClusterScoped", 1)
	assert.Equal(t, "eab-key-id", out.Config.EABKeyID)
	assert.NotEmpty(t, out.Config.EABSecretName)
	assert.NotEmpty(t, out.Config.SolverSecretName)
	repo.AssertExpectations(t)
	dnsRepo.AssertExpectations(t)
}

// TestCertificateIssuerService_CreateACME_ApplyFailure_ConfigPopulatedForCleanup
// guards the Task 8 fix: Config must be persisted BEFORE any ControlPlane
// apply call, so that when a later apply fails (here, the ClusterIssuer
// apply), the errored issuer row still carries every secret name Delete
// needs to clean up whatever was already created in-cluster (the solver
// Secret here) -- otherwise those secrets, which hold plaintext DNS-provider
// tokens, would be silently orphaned.
func TestCertificateIssuerService_CreateACME_ApplyFailure_ConfigPopulatedForCleanup(t *testing.T) {
	repo := new(mocks.MockCertificateIssuerRepository)
	dnsRepo := new(mocks.MockDNSProviderCredentialRepository)
	applier := new(mocks.MockCertInfraApplier)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	dnsSvc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: dnsRepo, Config: cfg, IssuerRepo: repo})
	svc := services.NewCertificateIssuerService(services.CertificateIssuerServiceDeps{
		Repo: repo, DNSCreds: dnsSvc, ControlPlane: applier, Config: cfg, ManagedCertRepo: new(mocks.MockManagedCertificateRepository),
	})

	credID := uuid.New()
	encToken, err := crypto.Encrypt("plain-api-token", cfg.EncryptionKey)
	require.NoError(t, err)
	dnsRepo.On("GetByID", credID).Return(&models.DNSProviderCredential{
		ID: credID, ProviderType: "cloudflare",
		Credentials: models.DNSCredentialData{"apiToken": encToken},
	}, nil)

	applier.On("Namespace").Return("fastgateway-system")
	// Solver + EAB secret applies (ApplyNamespaced) succeed; the ClusterIssuer
	// apply (ApplyClusterScoped) -- the last step -- fails.
	applier.On("ApplyNamespaced", mock.Anything, kubernetes.SecretGVR, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	applier.On("ApplyClusterScoped", mock.Anything, kubernetes.CertManagerClusterIssuerGVR, mock.AnythingOfType("*unstructured.Unstructured")).Return(errors.New("apply failed: admission webhook denied"))
	repo.On("Create", mock.AnythingOfType("*models.CertificateIssuer")).Return(nil)
	repo.On("Update", mock.AnythingOfType("*models.CertificateIssuer")).Return(nil)

	out, err := svc.Create(&services.CreateIssuerInput{
		Type: "acme", Name: "letsencrypt-zerossl", Server: "https://acme.zerossl.com/v2/DV90",
		Email: "ops@example.com", EABKeyID: "eab-key-id", EABHMACKey: "eab-hmac-key",
		DNSCredentialID: &credID,
	}, uuid.New())
	require.NoError(t, err) // markError returns a non-error result, mirroring domain_service's convention
	require.NotNil(t, out)

	assert.Equal(t, models.IssuerStatusError, out.Status)
	assert.NotEmpty(t, out.StatusMessage)
	// Config must already carry every name Delete needs to clean up the
	// secrets that were actually applied before the failure.
	assert.NotEmpty(t, out.Config.SolverSecretName)
	assert.NotEmpty(t, out.Config.EABSecretName)
	assert.NotEmpty(t, out.Config.ClusterIssuerName)
	assert.NotEmpty(t, out.Config.AccountSecretName)
	assert.Equal(t, "eab-key-id", out.Config.EABKeyID)
	require.NotNil(t, out.Config.DNSCredentialID)
	assert.Equal(t, credID, *out.Config.DNSCredentialID)

	repo.AssertExpectations(t)
	dnsRepo.AssertExpectations(t)
}

func TestCertificateIssuerService_DeleteACME_RemovesSecrets(t *testing.T) {
	repo := new(mocks.MockCertificateIssuerRepository)
	dnsRepo := new(mocks.MockDNSProviderCredentialRepository)
	applier := new(mocks.MockCertInfraApplier)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	dnsSvc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: dnsRepo, Config: cfg, IssuerRepo: repo})
	managedCertRepo := new(mocks.MockManagedCertificateRepository)
	svc := services.NewCertificateIssuerService(services.CertificateIssuerServiceDeps{
		Repo: repo, DNSCreds: dnsSvc, ControlPlane: applier, Config: cfg, ManagedCertRepo: managedCertRepo,
	})

	id := uuid.New()
	iss := &models.CertificateIssuer{
		ID: id, Type: models.IssuerTypeACME, Status: models.IssuerStatusReady,
		Config: models.IssuerConfig{
			ClusterIssuerName: "iss-" + id.String(),
			SolverSecretName:  "iss-" + id.String() + "-solver",
			AccountSecretName: "iss-" + id.String() + "-account",
			EABSecretName:     "iss-" + id.String() + "-eab",
		},
	}
	repo.On("GetByID", id).Return(iss, nil)
	managedCertRepo.On("CountByIssuer", id).Return(int64(0), nil)
	applier.On("Delete", mock.Anything, kubernetes.CertManagerClusterIssuerGVR, iss.Config.ClusterIssuerName, false).Return(nil)
	applier.On("Delete", mock.Anything, kubernetes.SecretGVR, iss.Config.SolverSecretName, true).Return(nil)
	applier.On("Delete", mock.Anything, kubernetes.SecretGVR, iss.Config.AccountSecretName, true).Return(nil)
	applier.On("Delete", mock.Anything, kubernetes.SecretGVR, iss.Config.EABSecretName, true).Return(nil)
	repo.On("Delete", id).Return(nil)

	err := svc.Delete(id)
	require.NoError(t, err)

	applier.AssertNumberOfCalls(t, "Delete", 4)
	applier.AssertCalled(t, "Delete", mock.Anything, kubernetes.CertManagerClusterIssuerGVR, iss.Config.ClusterIssuerName, false)
	applier.AssertCalled(t, "Delete", mock.Anything, kubernetes.SecretGVR, iss.Config.SolverSecretName, true)
	applier.AssertCalled(t, "Delete", mock.Anything, kubernetes.SecretGVR, iss.Config.AccountSecretName, true)
	applier.AssertCalled(t, "Delete", mock.Anything, kubernetes.SecretGVR, iss.Config.EABSecretName, true)
	repo.AssertExpectations(t)
}

func TestCertificateIssuerService_Delete_BlockedWhenCertReferences(t *testing.T) {
	repo := new(mocks.MockCertificateIssuerRepository)
	dnsRepo := new(mocks.MockDNSProviderCredentialRepository)
	applier := new(mocks.MockCertInfraApplier)
	managedCertRepo := new(mocks.MockManagedCertificateRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	dnsSvc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: dnsRepo, Config: cfg, IssuerRepo: repo})
	svc := services.NewCertificateIssuerService(services.CertificateIssuerServiceDeps{
		Repo: repo, DNSCreds: dnsSvc, ControlPlane: applier, Config: cfg, ManagedCertRepo: managedCertRepo,
	})

	id := uuid.New()
	iss := &models.CertificateIssuer{ID: id, Type: models.IssuerTypeACME, Status: models.IssuerStatusReady}
	repo.On("GetByID", id).Return(iss, nil)
	managedCertRepo.On("CountByIssuer", id).Return(int64(1), nil)

	err := svc.Delete(id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in use")
	applier.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertNotCalled(t, "Delete", mock.Anything)
}

func TestDNSCredentialService_Delete_BlockedWhenReferenced(t *testing.T) {
	dnsRepo := new(mocks.MockDNSProviderCredentialRepository)
	issRepo := new(mocks.MockCertificateIssuerRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: dnsRepo, Config: cfg, IssuerRepo: issRepo})

	id := uuid.New()
	issRepo.On("CountByDNSCredential", id).Return(int64(2), nil)
	err := svc.Delete(id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in use")
	dnsRepo.AssertNotCalled(t, "Delete", mock.Anything)
}

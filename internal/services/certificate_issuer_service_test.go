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
	applier.On("ApplyNamespaced", mock.Anything, mock.AnythingOfType("schema.GroupVersionResource"), mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	repo.On("Create", mock.AnythingOfType("*models.CertificateIssuer")).Return(nil)
	repo.On("Update", mock.AnythingOfType("*models.CertificateIssuer")).Return(nil)

	out, err := svc.Create(&services.CreateIssuerInput{
		Type: "self_signed_ca", Name: "root", CommonName: "FGW Root",
		KeyAlgorithm: "RSA", KeySize: 4096, DurationDays: 3650,
	}, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, models.IssuerTypeSelfSignedCA, out.Type)
	// self-signed CA applies: SelfSigned Issuer + CA Issuer + CA Certificate, all namespaced.
	// Both issuer applies must target CertManagerIssuerGVR (the namespaced "issuers"
	// resource), never CertManagerClusterIssuerGVR -- otherwise a ClusterIssuer is
	// created and the original cluster-scoped-issuer bug persists.
	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 3)
	issuerGVRCalls := 0
	for _, call := range applier.Calls {
		if call.Method == "ApplyNamespaced" && call.Arguments.Get(1) == kubernetes.CertManagerIssuerGVR {
			issuerGVRCalls++
		}
	}
	assert.Equal(t, 2, issuerGVRCalls, "expected SelfSignedIssuer + CAIssuer both applied via CertManagerIssuerGVR")
	applier.AssertNotCalled(t, "ApplyClusterScoped", mock.Anything, mock.Anything, mock.Anything)
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
	applier.On("ApplyNamespaced", mock.Anything, kubernetes.CertManagerIssuerGVR, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
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

	// solver Secret + EAB Secret + ACME Issuer, all namespaced -- the ACME
	// Issuer must be applied via CertManagerIssuerGVR (never
	// CertManagerClusterIssuerGVR/ApplyClusterScoped).
	applier.AssertNumberOfCalls(t, "ApplyNamespaced", 3)
	applier.AssertCalled(t, "ApplyNamespaced", mock.Anything, kubernetes.CertManagerIssuerGVR, mock.AnythingOfType("*unstructured.Unstructured"))
	applier.AssertNotCalled(t, "ApplyClusterScoped", mock.Anything, mock.Anything, mock.Anything)
	assert.Equal(t, "eab-key-id", out.Config.EABKeyID)
	assert.NotEmpty(t, out.Config.EABSecretName)
	assert.NotEmpty(t, out.Config.SolverSecretName)
	repo.AssertExpectations(t)
	dnsRepo.AssertExpectations(t)
}

// TestCertificateIssuerService_CreateACME_ApplyFailure_ConfigPopulatedForCleanup
// guards the Task 8 fix: Config must be persisted BEFORE any ControlPlane
// apply call, so that when a later apply fails (here, the Issuer
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
	// Solver + EAB secret applies (ApplyNamespaced) succeed; the namespaced
	// Issuer apply (also ApplyNamespaced, CertManagerIssuerGVR) -- the last
	// step -- fails.
	applier.On("ApplyNamespaced", mock.Anything, kubernetes.SecretGVR, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	applier.On("ApplyNamespaced", mock.Anything, kubernetes.CertManagerIssuerGVR, mock.AnythingOfType("*unstructured.Unstructured")).Return(errors.New("apply failed: admission webhook denied"))
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
	assert.NotEmpty(t, out.Config.IssuerName)
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
			IssuerName:        "iss-" + id.String(),
			SolverSecretName:  "iss-" + id.String() + "-solver",
			AccountSecretName: "iss-" + id.String() + "-account",
			EABSecretName:     "iss-" + id.String() + "-eab",
		},
	}
	repo.On("GetByID", id).Return(iss, nil)
	managedCertRepo.On("CountByIssuer", id).Return(int64(0), nil)
	// Delete must target the namespaced Issuer GVR with namespaced=true --
	// CertManagerClusterIssuerGVR/namespaced=false would leave the namespaced
	// Issuer orphaned in-cluster.
	applier.On("Delete", mock.Anything, kubernetes.CertManagerIssuerGVR, iss.Config.IssuerName, true).Return(nil)
	applier.On("Delete", mock.Anything, kubernetes.SecretGVR, iss.Config.SolverSecretName, true).Return(nil)
	applier.On("Delete", mock.Anything, kubernetes.SecretGVR, iss.Config.AccountSecretName, true).Return(nil)
	applier.On("Delete", mock.Anything, kubernetes.SecretGVR, iss.Config.EABSecretName, true).Return(nil)
	repo.On("Delete", id).Return(nil)

	err := svc.Delete(id)
	require.NoError(t, err)

	applier.AssertNumberOfCalls(t, "Delete", 4)
	applier.AssertCalled(t, "Delete", mock.Anything, kubernetes.CertManagerIssuerGVR, iss.Config.IssuerName, true)
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

package services_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/crypto"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestDNSCredentialService_Create_EncryptsCredentials(t *testing.T) {
	repo := new(mocks.MockDNSProviderCredentialRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: repo, Config: cfg, IssuerRepo: issuerRepo, ZoneRepo: new(mocks.MockDNSHostedZoneRepository)})

	var saved *models.DNSProviderCredential
	repo.On("Create", mock_anything(&saved)).Return(nil)

	in := &services.CreateDNSCredentialInput{
		Name: "cf-prod", ProviderType: "cloudflare",
		Credentials: map[string]string{"apiToken": "plain-token"},
	}
	out, err := svc.Create(in, uuid.New())
	require.NoError(t, err)

	// stored value must be ciphertext, not the plaintext
	assert.NotEqual(t, "plain-token", out.Credentials["apiToken"])
	dec, err := crypto.Decrypt(out.Credentials["apiToken"], cfg.EncryptionKey)
	require.NoError(t, err)
	assert.Equal(t, "plain-token", dec)

	repo.AssertExpectations(t)
}

func TestDNSCredentialService_Update_ReEncryptsChangedCredentials(t *testing.T) {
	repo := new(mocks.MockDNSProviderCredentialRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: repo, Config: cfg, IssuerRepo: issuerRepo, ZoneRepo: new(mocks.MockDNSHostedZoneRepository)})

	id := uuid.New()
	existing := &models.DNSProviderCredential{
		ID:           id,
		Name:         "cf-prod",
		ProviderType: "cloudflare",
		Credentials:  models.DNSCredentialData{"apiToken": "old-ciphertext"},
	}
	repo.On("GetByID", id).Return(existing, nil)

	var saved *models.DNSProviderCredential
	repo.On("Update", mock_anything(&saved)).Return(nil)

	in := &services.UpdateDNSCredentialInput{
		Credentials: map[string]string{"apiToken": "new-plain-token"},
	}
	out, err := svc.Update(id, in)
	require.NoError(t, err)

	// the new value must be re-encrypted, never stored or returned in plaintext
	assert.NotEqual(t, "new-plain-token", out.Credentials["apiToken"])
	assert.NotEqual(t, "old-ciphertext", out.Credentials["apiToken"])
	dec, err := crypto.Decrypt(out.Credentials["apiToken"], cfg.EncryptionKey)
	require.NoError(t, err)
	assert.Equal(t, "new-plain-token", dec)

	repo.AssertExpectations(t)
}

func TestNewDNSCredentialService_PanicsOnNilRepo(t *testing.T) {
	assert.Panics(t, func() {
		services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Config: &config.Config{}})
	})
}

func TestCreateDNSCredential_Route53ValidatesFields(t *testing.T) {
	repo := new(mocks.MockDNSProviderCredentialRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: repo, Config: cfg, IssuerRepo: issuerRepo, ZoneRepo: new(mocks.MockDNSHostedZoneRepository)})

	in := &services.CreateDNSCredentialInput{
		Name: "aws", ProviderType: "route53",
		Credentials: map[string]string{"accessKeyId": "AK"}, // missing secretAccessKey
	}
	_, err := svc.Create(in, uuid.New())
	if err == nil {
		t.Fatal("expected validation error for missing secretAccessKey")
	}
}

func TestCreateDNSCredential_Route53AcceptsCompleteFields(t *testing.T) {
	repo := new(mocks.MockDNSProviderCredentialRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: repo, Config: cfg, IssuerRepo: issuerRepo, ZoneRepo: new(mocks.MockDNSHostedZoneRepository)})

	var saved *models.DNSProviderCredential
	repo.On("Create", mock_anything(&saved)).Return(nil)

	in := &services.CreateDNSCredentialInput{
		Name: "aws", ProviderType: "route53",
		Credentials: map[string]string{"accessKeyId": "AK", "secretAccessKey": "SK"},
	}
	_, err := svc.Create(in, uuid.New())
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestCreateDNSCredential_UnsupportedProvider(t *testing.T) {
	repo := new(mocks.MockDNSProviderCredentialRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: repo, Config: cfg, IssuerRepo: issuerRepo, ZoneRepo: new(mocks.MockDNSHostedZoneRepository)})

	in := &services.CreateDNSCredentialInput{Name: "x", ProviderType: "bind", Credentials: map[string]string{}}
	_, err := svc.Create(in, uuid.New())
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

// TestDNSCredentialService_Delete_RejectsWhenInUseByHostedZone is the
// regression test for the direct-provider redesign's in-use guard: deleting
// a credential that one or more registered hosted zones still depend on
// must be refused, not silently succeed.
func TestDNSCredentialService_Delete_RejectsWhenInUseByHostedZone(t *testing.T) {
	repo := new(mocks.MockDNSProviderCredentialRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	zoneRepo := new(mocks.MockDNSHostedZoneRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{
		Repo: repo, Config: cfg, IssuerRepo: issuerRepo, ZoneRepo: zoneRepo,
	})

	id := uuid.New()
	issuerRepo.On("CountByDNSCredential", id).Return(int64(0), nil)
	zoneRepo.On("CountByCredential", id).Return(int64(2), nil)

	err := svc.Delete(id)

	require.Error(t, err)
	assert.ErrorIs(t, err, services.ErrDNSCredentialInUse)
	repo.AssertNotCalled(t, "Delete", mock.Anything)
}

// TestDNSCredentialService_Delete_RejectsWhenInUseByIssuer verifies the
// ACME-issuer in-use guard is still enforced, unchanged by the hosted-zone
// rewrite.
func TestDNSCredentialService_Delete_RejectsWhenInUseByIssuer(t *testing.T) {
	repo := new(mocks.MockDNSProviderCredentialRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	zoneRepo := new(mocks.MockDNSHostedZoneRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{
		Repo: repo, Config: cfg, IssuerRepo: issuerRepo, ZoneRepo: zoneRepo,
	})

	id := uuid.New()
	issuerRepo.On("CountByDNSCredential", id).Return(int64(1), nil)

	err := svc.Delete(id)

	require.Error(t, err)
	assert.ErrorIs(t, err, services.ErrDNSCredentialInUseByIssuer)
	repo.AssertNotCalled(t, "Delete", mock.Anything)
	zoneRepo.AssertNotCalled(t, "CountByCredential", mock.Anything)
}

// TestDNSCredentialService_Delete_AllowsWhenUnused verifies Delete proceeds
// to the repository when neither guard finds a reference.
func TestDNSCredentialService_Delete_AllowsWhenUnused(t *testing.T) {
	repo := new(mocks.MockDNSProviderCredentialRepository)
	issuerRepo := new(mocks.MockCertificateIssuerRepository)
	zoneRepo := new(mocks.MockDNSHostedZoneRepository)
	cfg := &config.Config{EncryptionKey: "test-encryption-key-32-bytes-xx!"}
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{
		Repo: repo, Config: cfg, IssuerRepo: issuerRepo, ZoneRepo: zoneRepo,
	})

	id := uuid.New()
	issuerRepo.On("CountByDNSCredential", id).Return(int64(0), nil)
	zoneRepo.On("CountByCredential", id).Return(int64(0), nil)
	repo.On("Delete", id).Return(nil)

	err := svc.Delete(id)

	require.NoError(t, err)
	repo.AssertExpectations(t)
}

// mock_anything captures the *models.DNSProviderCredential passed to Create.
func mock_anything(dst **models.DNSProviderCredential) interface{} {
	return mock.MatchedBy(func(c *models.DNSProviderCredential) bool { *dst = c; return true })
}

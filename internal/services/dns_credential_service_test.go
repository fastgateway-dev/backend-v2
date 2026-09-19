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
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: repo, Config: cfg, IssuerRepo: issuerRepo})

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
	svc := services.NewDNSCredentialService(services.DNSCredentialServiceDeps{Repo: repo, Config: cfg, IssuerRepo: issuerRepo})

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

// mock_anything captures the *models.DNSProviderCredential passed to Create.
func mock_anything(dst **models.DNSProviderCredential) interface{} {
	return mock.MatchedBy(func(c *models.DNSProviderCredential) bool { *dst = c; return true })
}

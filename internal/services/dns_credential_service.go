package services

import (
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/crypto"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// DNSCredentialService manages platform-global (owner-managed) DNS provider
// credentials used by ACME DNS-01 issuers. Every credential value is
// encrypted before it is persisted and decrypted only on demand (see
// DecryptedCredentials) -- it is never returned in plaintext through the API.
type DNSCredentialService struct {
	repo       repository.DNSProviderCredentialRepositoryInterface
	config     *config.Config
	issuerRepo repository.CertificateIssuerRepositoryInterface
}

// DNSCredentialServiceDeps are DNSCredentialService's required dependencies.
// NewDNSCredentialService panics if any of them is nil, following the house
// pattern for services with required constructor dependencies. IssuerRepo
// lets Delete refuse to remove a credential that's still referenced by an
// ACME issuer (see CertificateIssuerService, Task 6).
type DNSCredentialServiceDeps struct {
	Repo       repository.DNSProviderCredentialRepositoryInterface
	Config     *config.Config
	IssuerRepo repository.CertificateIssuerRepositoryInterface
}

func NewDNSCredentialService(deps DNSCredentialServiceDeps) *DNSCredentialService {
	var missing []string
	if deps.Repo == nil {
		missing = append(missing, "Repo")
	}
	if deps.Config == nil {
		missing = append(missing, "Config")
	}
	if deps.IssuerRepo == nil {
		missing = append(missing, "IssuerRepo")
	}
	if len(missing) > 0 {
		panic("services.NewDNSCredentialService: missing required dependency: " + strings.Join(missing, ", "))
	}
	return &DNSCredentialService{repo: deps.Repo, config: deps.Config, issuerRepo: deps.IssuerRepo}
}

// CreateDNSCredentialInput is the request body for creating a DNS provider
// credential. Credentials is provider-specific plaintext (e.g. for
// cloudflare: {"apiToken": "..."}); the service encrypts every value before
// persisting it.
type CreateDNSCredentialInput struct {
	Name         string            `json:"name" binding:"required"`
	ProviderType string            `json:"providerType" binding:"required"`
	Credentials  map[string]string `json:"credentials" binding:"required"`
}

// UpdateDNSCredentialInput is the request body for updating a DNS provider
// credential's name and/or credential values. Credentials is optional: when
// nil, the existing encrypted credentials are left untouched.
type UpdateDNSCredentialInput struct {
	Name        string            `json:"name"`
	Credentials map[string]string `json:"credentials"`
}

var supportedDNSProviders = map[string]bool{"cloudflare": true}

func (s *DNSCredentialService) Create(input *CreateDNSCredentialInput, createdBy uuid.UUID) (*models.DNSProviderCredential, error) {
	if !supportedDNSProviders[input.ProviderType] {
		return nil, errors.New("unsupported DNS provider: " + input.ProviderType)
	}
	enc := make(models.DNSCredentialData, len(input.Credentials))
	for k, v := range input.Credentials {
		ct, err := crypto.Encrypt(v, s.config.EncryptionKey)
		if err != nil {
			return nil, err
		}
		enc[k] = ct
	}
	c := &models.DNSProviderCredential{
		Name: input.Name, ProviderType: input.ProviderType,
		Credentials: enc, CreatedBy: createdBy,
	}
	if err := s.repo.Create(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *DNSCredentialService) List() ([]models.DNSProviderCredential, error) { return s.repo.List() }
func (s *DNSCredentialService) GetByID(id uuid.UUID) (*models.DNSProviderCredential, error) {
	return s.repo.GetByID(id)
}

func (s *DNSCredentialService) Update(id uuid.UUID, input *UpdateDNSCredentialInput) (*models.DNSProviderCredential, error) {
	c, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if input.Name != "" {
		c.Name = input.Name
	}
	if input.Credentials != nil {
		enc := make(models.DNSCredentialData, len(input.Credentials))
		for k, v := range input.Credentials {
			ct, err := crypto.Encrypt(v, s.config.EncryptionKey)
			if err != nil {
				return nil, err
			}
			enc[k] = ct
		}
		c.Credentials = enc
	}
	if err := s.repo.Update(c); err != nil {
		return nil, err
	}
	return c, nil
}

// Delete removes a DNS provider credential, but refuses when it is still
// referenced by one or more ACME issuers (IssuerConfig.dnsCredentialId) --
// deleting it out from under them would leave those issuers unable to
// re-solve DNS-01 challenges.
func (s *DNSCredentialService) Delete(id uuid.UUID) error {
	n, err := s.issuerRepo.CountByDNSCredential(id)
	if err != nil {
		return err
	}
	if n > 0 {
		return errors.New("DNS credential is in use by an ACME issuer")
	}
	return s.repo.Delete(id)
}

// DecryptedCredentials returns the plaintext credential map (service-internal;
// used by the issuer service to build the DNS-01 solver Secret).
func (s *DNSCredentialService) DecryptedCredentials(id uuid.UUID) (string, map[string]string, error) {
	c, err := s.repo.GetByID(id)
	if err != nil {
		return "", nil, err
	}
	out := make(map[string]string, len(c.Credentials))
	for k, v := range c.Credentials {
		pt, err := crypto.Decrypt(v, s.config.EncryptionKey)
		if err != nil {
			return "", nil, err
		}
		out[k] = pt
	}
	return c.ProviderType, out, nil
}

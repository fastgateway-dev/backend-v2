package services

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/dnsprovider"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

// DNSCredentialReader is the narrow role DNSInfraService needs from
// DNSCredentialService: decrypt a stored DNS provider credential by id so it
// can be rendered into the external-dns Secret. Satisfied by
// *DNSCredentialService.
type DNSCredentialReader interface {
	DecryptedCredentials(id uuid.UUID) (string, map[string]string, error)
}

// SystemSettingsStore is the narrow role DNSInfraService needs from
// SystemSettingsService: read and persist which DNS provider credential is
// currently active. Satisfied by *SystemSettingsService.
type SystemSettingsStore interface {
	GetActiveDNSCredentialID() (*uuid.UUID, error)
	SetActiveDNSCredentialID(id *uuid.UUID) error
}

// Compile-time role satisfaction checks.
var _ DNSCredentialReader = (*DNSCredentialService)(nil)
var _ SystemSettingsStore = (*SystemSettingsService)(nil)

// DNSInfraService sets which DNS provider credential is active for
// external-dns and renders+applies the Secret external-dns reads for it. It
// reuses CertInfraApplier (the same narrow control-plane role the
// certificate-infra services use) to write to the backend's own cluster.
type DNSInfraService struct {
	creds        DNSCredentialReader
	settings     SystemSettingsStore
	controlPlane CertInfraApplier
}

// DNSInfraServiceDeps are DNSInfraService's required dependencies.
// NewDNSInfraService panics if any of them is nil, following the house
// pattern for services with required constructor dependencies.
type DNSInfraServiceDeps struct {
	Creds        DNSCredentialReader
	Settings     SystemSettingsStore
	ControlPlane CertInfraApplier
}

func NewDNSInfraService(deps DNSInfraServiceDeps) *DNSInfraService {
	if deps.Creds == nil || deps.Settings == nil || deps.ControlPlane == nil {
		panic("services.NewDNSInfraService: missing required dependency")
	}
	return &DNSInfraService{creds: deps.Creds, settings: deps.Settings, controlPlane: deps.ControlPlane}
}

// GetActiveCredentialID returns the id of the DNS provider credential
// currently active for external-dns, delegating to the system-settings
// singleton.
func (s *DNSInfraService) GetActiveCredentialID() (*uuid.UUID, error) {
	return s.settings.GetActiveDNSCredentialID()
}

// SetActiveCredential validates that the credential exists and can be
// decrypted, renders it into the external-dns Secret via its provider, and
// applies that Secret to the control cluster. Only once the apply succeeds
// does it persist the credential as active.
func (s *DNSInfraService) SetActiveCredential(id uuid.UUID) error {
	providerType, creds, err := s.creds.DecryptedCredentials(id)
	if err != nil {
		return errors.New("credential not found or undecryptable")
	}
	prov, ok := dnsprovider.Get(providerType)
	if !ok {
		return errors.New("unsupported DNS provider: " + providerType)
	}
	secret := kubernetes.ExternalDNSSecret(kubernetes.ExternalDNSSecretName, prov.RenderSecret(creds))
	if err := s.controlPlane.ApplyNamespaced(context.Background(), kubernetes.SecretGVR, secret); err != nil {
		return err
	}
	return s.settings.SetActiveDNSCredentialID(&id)
}

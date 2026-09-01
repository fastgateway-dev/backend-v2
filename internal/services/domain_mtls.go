// DomainService: domain mTLS CA management.
//
// Adding and removing the CA certificates a domain trusts for client mTLS, the
// PEM validation that guards them, the collection of CA secret references that
// the ClientTrafficPolicy needs, and the entry point RouteService calls to
// rebuild that policy after client mTLS secrets change. Phase 2F Task 3.

package services

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// EnsureMTLSClientTrafficPolicy re-applies the Envoy Gateway
// ClientTrafficPolicy for domain from its stored domain settings.
//
// RouteService calls this after creating or replacing the Kubernetes secrets
// holding client mTLS CAs, so the policy references the current secret set.
// It exists because route_clients_apikey.go used to reach into this service's
// unexported settingsRepo field and unexported
// applyEnvoyGatewayClientTrafficPolicy method -- neither expressible as an
// interface. Controller ruling R1, Phase 2E Task 5.
//
// It reports success when the domain has no stored settings: without settings
// there is no policy to rebuild. That is exactly what the reach-through did.
func (s *DomainService) EnsureMTLSClientTrafficPolicy(ctx context.Context, domain *models.Domain) error {
	settings, err := s.settingsRepo.GetByDomainID(domain.ID)
	if err != nil || settings == nil {
		// No stored settings for this domain: nothing to re-apply.
		return nil
	}
	return s.applyEnvoyGatewayClientTrafficPolicy(ctx, domain, &settings.Config)
}

// collectCASecretRefs builds the list of CA secret refs from domain config and active client mTLS attachments.
// Used by applyEnvoyGatewayClientTrafficPolicy, GenerateYAMLs, and PreviewSettingsChanges.
func (s *DomainService) collectCASecretRefs(domain *models.Domain, config *models.DomainSettingsConfig) []kubernetes.SecretRefPolicyConfig {
	if config.MTLS == nil || !config.MTLS.Enabled {
		return nil
	}

	var refs []kubernetes.SecretRefPolicyConfig

	// Domain CAs
	for _, ca := range config.MTLS.CACerts {
		refs = append(refs, kubernetes.SecretRefPolicyConfig{
			Group: "",
			Kind:  "Secret",
			Name:  ca.SecretName,
		})
	}

	// Client CAs from active mTLS attachments
	mtlsClients, err := s.clientAttachmentRepo.GetMTLSClientsForDomain(domain.ID)
	if err != nil {
		log.Printf("Warning: failed to get mTLS clients for domain %s: %v", domain.ID, err)
	} else {
		for _, client := range mtlsClients {
			if client.MTLSCASecret != "" {
				refs = append(refs, kubernetes.SecretRefPolicyConfig{
					Group: "",
					Kind:  "Secret",
					Name:  client.MTLSCASecret,
				})
			}
		}
	}

	return refs
}

// AddDomainMTLSCAInput represents input for adding a domain CA certificate
type AddDomainMTLSCAInput struct {
	Name  string `json:"name" binding:"required"`
	CAPem string `json:"caPem" binding:"required"`
}

// validateCAPEM validates a PEM-encoded CA certificate
func validateCAPEM(pemData string) error {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return errors.New("failed to decode PEM data")
	}
	if block.Type != "CERTIFICATE" {
		return fmt.Errorf("expected CERTIFICATE, got %s", block.Type)
	}
	_, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("invalid certificate: %w", err)
	}
	return nil
}

// AddDomainMTLSCA adds a CA certificate for domain mTLS
func (s *DomainService) AddDomainMTLSCA(ctx context.Context, domainID uuid.UUID, input *AddDomainMTLSCAInput) (*models.DomainSettings, error) {
	// Validate PEM
	if err := validateCAPEM(input.CAPem); err != nil {
		return nil, fmt.Errorf("invalid CA certificate: %w", err)
	}

	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, fmt.Errorf("domain not found: %w", err)
	}

	// Get or create settings
	settings, err := s.settingsRepo.GetByDomainID(domainID)
	if err != nil {
		settings = &models.DomainSettings{
			DomainID:  domainID,
			ProjectID: domain.ProjectID,
			Config:    models.DomainSettingsConfig{},
		}
	}

	if settings.Config.MTLS == nil {
		settings.Config.MTLS = &models.DomainMTLSConfig{}
	}

	// Generate unique ID and secret name
	caID := uuid.New().String()[:8]
	secretName := fmt.Sprintf("fastgateway-%s-mtls-ca-%s", domainID.String()[:8], caID)

	// Create K8s secret with CA
	err = s.k8sSecrets.CreateOrUpdateSecret(ctx, domain.ProjectID, domain.Namespace, secretName, map[string][]byte{
		"ca.crt": []byte(input.CAPem),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create CA secret: %w", err)
	}

	// Add to config
	settings.Config.MTLS.CACerts = append(settings.Config.MTLS.CACerts, models.MTLSCACert{
		ID:         caID,
		Name:       input.Name,
		SecretName: secretName,
		SecretKey:  "ca.crt",
	})

	// Save settings
	if err := s.settingsRepo.Upsert(settings); err != nil {
		return nil, fmt.Errorf("failed to save settings: %w", err)
	}

	// Update CTP if mTLS is enabled (new CA secret ref will be included)
	if settings.Config.MTLS.Enabled {
		if err := s.applyEnvoyGatewayClientTrafficPolicy(ctx, domain, &settings.Config); err != nil {
			return nil, err
		}
	}

	return settings, nil
}

// RemoveDomainMTLSCA removes a CA certificate from domain mTLS
func (s *DomainService) RemoveDomainMTLSCA(ctx context.Context, domainID uuid.UUID, caID string) (*models.DomainSettings, error) {
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, fmt.Errorf("domain not found: %w", err)
	}

	settings, err := s.settingsRepo.GetByDomainID(domainID)
	if err != nil {
		return nil, fmt.Errorf("settings not found: %w", err)
	}

	if settings.Config.MTLS == nil {
		return nil, errors.New("mTLS not configured")
	}

	// Find and remove CA
	var removedCA *models.MTLSCACert
	newCAs := make([]models.MTLSCACert, 0, len(settings.Config.MTLS.CACerts))
	for _, ca := range settings.Config.MTLS.CACerts {
		if ca.ID == caID {
			caCopy := ca
			removedCA = &caCopy
		} else {
			newCAs = append(newCAs, ca)
		}
	}

	if removedCA == nil {
		return nil, errors.New("CA not found")
	}

	// If removing the last CA, auto-disable mTLS
	if len(newCAs) == 0 && settings.Config.MTLS.Enabled {
		settings.Config.MTLS.Enabled = false
	}

	// Delete the K8s secret
	if err := s.k8sSecrets.DeleteSecret(ctx, domain.ProjectID, domain.Namespace, removedCA.SecretName); err != nil {
		log.Printf("Warning: failed to delete CA secret: %v", err)
	}

	// Update config
	settings.Config.MTLS.CACerts = newCAs

	// Save settings
	if err := s.settingsRepo.Upsert(settings); err != nil {
		return nil, fmt.Errorf("failed to save settings: %w", err)
	}

	// Update CTP (re-apply to reflect removed CA or disabled mTLS)
	if err := s.applyEnvoyGatewayClientTrafficPolicy(ctx, domain, &settings.Config); err != nil {
		return nil, err
	}

	return settings, nil
}

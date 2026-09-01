// DomainService: gateway-agnostic domain settings.
//
// Reading and writing a domain's settings record, and translating the
// ClientTrafficPolicy half of those settings onto the cluster. The
// BackendTrafficPolicy and EnvoyExtensionPolicy halves are applied by
// domain_deploy.go. Phase 2F Task 3.

package services

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/domainplan"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// UpdateDomainSettingsInput represents input for updating domain settings
type UpdateDomainSettingsInput struct {
	ClientConnection     *models.ClientConnectionConfig     `json:"clientConnection,omitempty"`
	Timeout              *models.TimeoutConfig              `json:"timeout,omitempty"`
	HTTP3                *models.HTTP3Config                `json:"http3,omitempty"`
	TLS                  *models.TLSSettingsConfig          `json:"tls,omitempty"`
	ClientIPDetection    *models.ClientIPDetectionConfig    `json:"clientIPDetection,omitempty"`
	MTLS                 *models.DomainMTLSConfig           `json:"mtls,omitempty"`
	BackendTrafficPolicy *models.BackendTrafficPolicyConfig `json:"backendTrafficPolicy,omitempty"`
	ExtensionPolicy      *models.EnvoyExtensionPolicyConfig `json:"extensionPolicy,omitempty"`
}

// GetDomainSettings gets the settings for a domain
func (s *DomainService) GetDomainSettings(domainID uuid.UUID) (*models.DomainSettings, error) {
	return s.settingsRepo.GetByDomainID(domainID)
}

// UpdateDomainSettings updates the settings for a domain
// This is the gateway-agnostic API - internally translates to gateway-specific resources
func (s *DomainService) UpdateDomainSettings(domainID uuid.UUID, input *UpdateDomainSettingsInput) (*models.DomainSettings, error) {
	// Get domain
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, fmt.Errorf("domain not found: %w", err)
	}

	// Validate CTP input
	if input.ClientConnection != nil {
		if err := input.ClientConnection.Validate(); err != nil {
			return nil, fmt.Errorf("invalid client connection config: %w", err)
		}
	}
	if input.Timeout != nil {
		if err := input.Timeout.Validate(); err != nil {
			return nil, fmt.Errorf("invalid timeout config: %w", err)
		}
	}
	if input.TLS != nil {
		if err := input.TLS.Validate(); err != nil {
			return nil, fmt.Errorf("invalid TLS config: %w", err)
		}
	}
	if input.ClientIPDetection != nil {
		if err := input.ClientIPDetection.Validate(); err != nil {
			return nil, fmt.Errorf("invalid clientIPDetection: %w", err)
		}
	}

	// Validate BTP input
	if input.BackendTrafficPolicy != nil {
		// Reject features not applicable at domain level
		if input.BackendTrafficPolicy.HealthCheck != nil {
			return nil, errors.New("healthCheck is not supported at domain level")
		}
		if input.BackendTrafficPolicy.RateLimit != nil {
			return nil, errors.New("rateLimit is not supported at domain level")
		}
		if input.BackendTrafficPolicy.FaultInjection != nil {
			return nil, errors.New("faultInjection is not supported at domain level")
		}
		// Validate individual sub-configs
		if input.BackendTrafficPolicy.Retry != nil {
			if err := input.BackendTrafficPolicy.Retry.Validate(); err != nil {
				return nil, fmt.Errorf("invalid retry config: %w", err)
			}
		}
		if input.BackendTrafficPolicy.LoadBalancer != nil {
			if err := input.BackendTrafficPolicy.LoadBalancer.Validate(); err != nil {
				return nil, fmt.Errorf("invalid loadBalancer config: %w", err)
			}
		}
		if input.BackendTrafficPolicy.CircuitBreaker != nil {
			if err := input.BackendTrafficPolicy.CircuitBreaker.Validate(); err != nil {
				return nil, fmt.Errorf("invalid circuitBreaker config: %w", err)
			}
		}
		if input.BackendTrafficPolicy.RequestBuffer != nil {
			if err := input.BackendTrafficPolicy.RequestBuffer.Validate(); err != nil {
				return nil, fmt.Errorf("invalid requestBuffer config: %w", err)
			}
		}
		if len(input.BackendTrafficPolicy.ResponseOverride) > 0 {
			for i, rule := range input.BackendTrafficPolicy.ResponseOverride {
				if err := rule.Validate(); err != nil {
					return nil, fmt.Errorf("invalid responseOverride[%d]: %w", i, err)
				}
			}
		}
		if input.BackendTrafficPolicy.Timeout != nil {
			if err := input.BackendTrafficPolicy.Timeout.Validate(); err != nil {
				return nil, fmt.Errorf("invalid BTP timeout config: %w", err)
			}
		}
	}

	// Validate extension policy input
	if input.ExtensionPolicy != nil {
		if err := input.ExtensionPolicy.Validate(); err != nil {
			return nil, fmt.Errorf("invalid extension policy config: %w", err)
		}
	}

	// Build CTP settings config
	ctpConfig := models.DomainSettingsConfig{
		ClientConnection:  input.ClientConnection,
		Timeout:           input.Timeout,
		HTTP3:             input.HTTP3,
		TLS:               input.TLS,
		ClientIPDetection: input.ClientIPDetection,
		MTLS:              input.MTLS,
	}

	ctx := context.Background()

	// Determine which configs are empty
	ctpEmpty := ctpConfig.IsEmpty()
	btpEmpty := input.BackendTrafficPolicy == nil || input.BackendTrafficPolicy.IsEmpty()
	extEmpty := input.ExtensionPolicy == nil || input.ExtensionPolicy.IsEmpty()

	// If ALL empty: delete all K8s resources and DB records
	if ctpEmpty && btpEmpty && extEmpty {
		ctpName := domain.K8sGatewayName + "-ctp"
		if err := s.k8sGateways.DeleteClientTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, ctpName); err != nil {
			log.Printf("Failed to delete ClientTrafficPolicy from Kubernetes: %v", err)
		}
		if err := s.settingsRepo.DeleteByDomainID(domainID); err != nil {
			log.Printf("Failed to delete domain settings from database: %v", err)
		}
		// Delete BTP
		if err := s.applyDomainBackendTrafficPolicy(ctx, domain, nil); err != nil {
			log.Printf("Failed to delete domain BTP: %v", err)
		}
		// Delete extension policy
		if err := s.applyDomainEnvoyExtensionPolicy(ctx, domain, nil); err != nil {
			log.Printf("Failed to delete domain extension policy: %v", err)
		}
		return nil, nil
	}

	// Handle CTP independently
	if ctpEmpty {
		ctpName := domain.K8sGatewayName + "-ctp"
		if err := s.k8sGateways.DeleteClientTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, ctpName); err != nil {
			log.Printf("Failed to delete ClientTrafficPolicy from Kubernetes: %v", err)
		}
		if err := s.settingsRepo.DeleteByDomainID(domainID); err != nil {
			log.Printf("Failed to delete domain settings from database: %v", err)
		}
	} else {
		settings := &models.DomainSettings{
			DomainID:  domainID,
			ProjectID: domain.ProjectID,
			Config:    ctpConfig,
		}
		if err := s.settingsRepo.Upsert(settings); err != nil {
			return nil, fmt.Errorf("failed to save domain settings: %w", err)
		}
		if err := s.applyEnvoyGatewayClientTrafficPolicy(ctx, domain, &ctpConfig); err != nil {
			return nil, err
		}
	}

	// Handle BTP independently
	if err := s.applyDomainBackendTrafficPolicy(ctx, domain, input.BackendTrafficPolicy); err != nil {
		return nil, err
	}

	// Handle extension policy independently
	if err := s.applyDomainEnvoyExtensionPolicy(ctx, domain, input.ExtensionPolicy); err != nil {
		return nil, err
	}

	// Reload and return the settings (may be nil if CTP was empty but BTP/ext were set)
	settings, err := s.settingsRepo.GetByDomainID(domainID)
	if err != nil {
		// CTP was deleted but BTP/extension saved successfully — not an error
		return nil, nil
	}
	return settings, nil
}

// applyEnvoyGatewayClientTrafficPolicy translates domain settings to Envoy Gateway ClientTrafficPolicy CRD
func (s *DomainService) applyEnvoyGatewayClientTrafficPolicy(ctx context.Context, domain *models.Domain, config *models.DomainSettingsConfig) error {
	caSecretRefs := s.collectCASecretRefs(domain, config)
	ctpConfig := domainplan.BuildClientTrafficPolicyConfig(domain, config, caSecretRefs)

	if err := s.k8sGateways.CreateClientTrafficPolicy(ctx, domain.ProjectID, ctpConfig); err != nil {
		return fmt.Errorf("failed to apply ClientTrafficPolicy to Kubernetes: %w", err)
	}
	return nil
}

// GetDomainBackendTrafficPolicy returns the domain-level BackendTrafficPolicy
// record for a domain, or a not-found error if the domain has none.
//
// Phase 2F Task 4: DomainHandler.GetDomainSettings read this straight off
// repository.BackendTrafficPolicyRepositoryInterface. The repository owns the
// row; the handler should not. The caller's existing shape -- treat any error
// as "no policy to report" -- is unchanged, so the error is returned rather
// than swallowed here.
func (s *DomainService) GetDomainBackendTrafficPolicy(domainID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	return s.btpRepo.GetByDomainID(domainID)
}

// GetDomainEnvoyExtensionPolicy returns the domain-level EnvoyExtensionPolicy
// record for a domain, or a not-found error if the domain has none. The
// companion of GetDomainBackendTrafficPolicy; see its comment.
func (s *DomainService) GetDomainEnvoyExtensionPolicy(domainID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	return s.extPolicyRepo.GetByDomainID(domainID)
}

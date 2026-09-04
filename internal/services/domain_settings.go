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
//
// The second return value carries operator-facing warnings about the
// settings just written -- currently just the mTLS no-CA-available warning
// (mtls-warning-brief.md, Change 1). It is nil whenever there is nothing to
// warn about, following the house pattern in route_validation.go
// (BackendTLSWarnings, DirectResponsePercentWarnings).
func (s *DomainService) UpdateDomainSettings(domainID uuid.UUID, input *UpdateDomainSettingsInput) (*models.DomainSettings, []string, error) {
	// Get domain
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, nil, fmt.Errorf("domain not found: %w", err)
	}

	// Validate CTP input
	if input.ClientConnection != nil {
		if err := input.ClientConnection.Validate(); err != nil {
			return nil, nil, fmt.Errorf("invalid client connection config: %w", err)
		}
	}
	if input.Timeout != nil {
		if err := input.Timeout.Validate(); err != nil {
			return nil, nil, fmt.Errorf("invalid timeout config: %w", err)
		}
	}
	if input.TLS != nil {
		if err := input.TLS.Validate(); err != nil {
			return nil, nil, fmt.Errorf("invalid TLS config: %w", err)
		}
	}
	if input.ClientIPDetection != nil {
		if err := input.ClientIPDetection.Validate(); err != nil {
			return nil, nil, fmt.Errorf("invalid clientIPDetection: %w", err)
		}
	}
	// ValidateShape, not Validate: Validate's "at least one CA certificate is
	// required when mTLS is enabled" rule was explicitly withdrawn for this
	// write path (mtls-warning-brief.md, Change 2) -- a client attachment can
	// supply the CA instead, via DomainService.collectCASecretRefs. Only the
	// two unconditionally-correct shape checks (SAN entries, hash whitelist)
	// run here.
	if input.MTLS != nil {
		if err := input.MTLS.ValidateShape(); err != nil {
			return nil, nil, fmt.Errorf("invalid mTLS config: %w", err)
		}
	}

	// Validate BTP input
	if input.BackendTrafficPolicy != nil {
		// Reject features not applicable at domain level
		if input.BackendTrafficPolicy.HealthCheck != nil {
			return nil, nil, errors.New("healthCheck is not supported at domain level")
		}
		if input.BackendTrafficPolicy.RateLimit != nil {
			return nil, nil, errors.New("rateLimit is not supported at domain level")
		}
		if input.BackendTrafficPolicy.FaultInjection != nil {
			return nil, nil, errors.New("faultInjection is not supported at domain level")
		}
		// Validate individual sub-configs
		if input.BackendTrafficPolicy.Retry != nil {
			if err := input.BackendTrafficPolicy.Retry.Validate(); err != nil {
				return nil, nil, fmt.Errorf("invalid retry config: %w", err)
			}
		}
		if input.BackendTrafficPolicy.LoadBalancer != nil {
			if err := input.BackendTrafficPolicy.LoadBalancer.Validate(); err != nil {
				return nil, nil, fmt.Errorf("invalid loadBalancer config: %w", err)
			}
		}
		if input.BackendTrafficPolicy.CircuitBreaker != nil {
			if err := input.BackendTrafficPolicy.CircuitBreaker.Validate(); err != nil {
				return nil, nil, fmt.Errorf("invalid circuitBreaker config: %w", err)
			}
		}
		if input.BackendTrafficPolicy.RequestBuffer != nil {
			if err := input.BackendTrafficPolicy.RequestBuffer.Validate(); err != nil {
				return nil, nil, fmt.Errorf("invalid requestBuffer config: %w", err)
			}
		}
		if len(input.BackendTrafficPolicy.ResponseOverride) > 0 {
			for i, rule := range input.BackendTrafficPolicy.ResponseOverride {
				if err := rule.Validate(); err != nil {
					return nil, nil, fmt.Errorf("invalid responseOverride[%d]: %w", i, err)
				}
			}
		}
		if input.BackendTrafficPolicy.Timeout != nil {
			if err := input.BackendTrafficPolicy.Timeout.Validate(); err != nil {
				return nil, nil, fmt.Errorf("invalid BTP timeout config: %w", err)
			}
		}
	}

	// Validate extension policy input
	if input.ExtensionPolicy != nil {
		if err := input.ExtensionPolicy.Validate(); err != nil {
			return nil, nil, fmt.Errorf("invalid extension policy config: %w", err)
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
		return nil, nil, nil
	}

	var warnings []string

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
			return nil, nil, fmt.Errorf("failed to save domain settings: %w", err)
		}
		w, err := s.applyEnvoyGatewayClientTrafficPolicy(ctx, domain, &ctpConfig)
		if err != nil {
			return nil, nil, err
		}
		warnings = w
	}

	// Handle BTP independently
	if err := s.applyDomainBackendTrafficPolicy(ctx, domain, input.BackendTrafficPolicy); err != nil {
		return nil, nil, err
	}

	// Handle extension policy independently
	if err := s.applyDomainEnvoyExtensionPolicy(ctx, domain, input.ExtensionPolicy); err != nil {
		return nil, nil, err
	}

	// Reload and return the settings (may be nil if CTP was empty but BTP/ext were set)
	settings, err := s.settingsRepo.GetByDomainID(domainID)
	if err != nil {
		// CTP was deleted but BTP/extension saved successfully — not an error
		return nil, nil, nil
	}
	return settings, warnings, nil
}

// mtlsNoCAWarning is Change 1's operator-facing warning (mtls-warning-brief.md):
// mTLS is enabled but the RESOLVED CA secret ref list (domain-level CACerts
// merged with active mTLS client attachments, via collectCASecretRefs) is
// empty. Computed from the resolved list rather than input.MTLS.CACerts
// alone, because a client attachment supplying the CA is a normal
// configuration that must not produce this warning -- see
// collectCASecretRefs and DomainMTLSConfig.Validate's doc comment.
//
// The consequence sentence was corrected by the Phase 2I e2e test
// (e2e/suites/domain/mtls_no_ca_test.go): the original text asserted the
// domain "will reject client connections," an untested claim about Envoy
// Gateway's runtime behavior. CI has since run that test against Envoy
// Gateway 1.8.4 and observed the opposite of a rejected handshake -- the
// gateway completes TLS and returns HTTP 500 for the life of the window.
// The security property still holds (unauthenticated traffic never reaches
// the backend), so only the described mechanism changed, not the warning's
// purpose. The Gateway-wide blast radius sentence is an inference, not a
// measurement: ClientTrafficPolicy attaches to the Gateway, not the route,
// so it necessarily affects every route behind it, but the e2e test only
// ever exercised one route, so this is worded as "likely affects" rather
// than as an observed fact.
const mtlsNoCAWarning = "mTLS is enabled but no CA certificates are available for this domain (none configured directly, and no active mTLS clients attached). Requests will fail with an HTTP 500 at the gateway (measured on Envoy Gateway 1.8.4), not a rejected TLS handshake, until a CA is added or an mTLS client is attached. Because ClientTrafficPolicy is Gateway-scoped, this likely affects every route behind this domain's Gateway, not only this domain's routes."

// applyEnvoyGatewayClientTrafficPolicy translates domain settings to Envoy
// Gateway ClientTrafficPolicy CRD.
//
// The returned []string carries operator-facing warnings about the policy
// just applied (currently just mtlsNoCAWarning); it is nil on any error path
// and whenever there is nothing to warn about.
func (s *DomainService) applyEnvoyGatewayClientTrafficPolicy(ctx context.Context, domain *models.Domain, config *models.DomainSettingsConfig) ([]string, error) {
	caSecretRefs, err := s.collectCASecretRefs(domain, config)
	if err != nil {
		return nil, fmt.Errorf("collect CA secret refs for domain %s: %w", domain.ID, err)
	}
	ctpConfig := domainplan.BuildClientTrafficPolicyConfig(domain, config, caSecretRefs)

	if err := s.k8sGateways.CreateClientTrafficPolicy(ctx, domain.ProjectID, ctpConfig); err != nil {
		return nil, fmt.Errorf("failed to apply ClientTrafficPolicy to Kubernetes: %w", err)
	}

	var warnings []string
	if config.MTLS != nil && config.MTLS.Enabled && len(caSecretRefs) == 0 {
		warnings = append(warnings, mtlsNoCAWarning)
	}
	return warnings, nil
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

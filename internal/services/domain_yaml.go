// DomainService: YAML rendering and change previews.
//
// Rendering a domain's current manifests, and the create/settings previews that
// show what a proposed change would produce (with optional AI review). The
// manifest builders themselves live in internal/domainplan. Phase 2F Task 3.

package services

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/domainplan"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

// DomainYAMLs contains the generated YAML manifests for a domain
type DomainYAMLs struct {
	GatewayYaml              string `json:"gatewayYaml"`
	ClientTrafficPolicyYaml  string `json:"clientTrafficPolicyYaml,omitempty"`
	BackendTrafficPolicyYaml string `json:"backendTrafficPolicyYaml,omitempty"`
	EnvoyExtensionPolicyYaml string `json:"envoyExtensionPolicyYaml,omitempty"`
}

// DomainCreatePreviewInput represents input for previewing a domain creation
type DomainCreatePreviewInput struct {
	CreateDomainInput
	Description     string `json:"description,omitempty"`
	IncludeAIReview bool   `json:"includeAIReview,omitempty"`
}

// DomainCreatePreviewResult contains the preview YAML and optional AI review for domain creation
type DomainCreatePreviewResult struct {
	ProposedGatewayYaml string           `json:"proposedGatewayYaml"`
	AIReview            *ai.ReviewResult `json:"aiReview,omitempty"`
}

// templateAnnotations resolves a domain's template annotations for the Gateway
// manifest. Returns nil when the domain has no template.
//
// NOTE: a failed template lookup is deliberately swallowed here -- the Gateway
// is then built with no annotations, exactly as it was before Phase 2F moved
// the builder into internal/domainplan. Preserved verbatim; see the Phase 2F
// report for the finding.
func (s *DomainService) templateAnnotations(domain *models.Domain) map[string]string {
	if domain.DomainTemplateID != nil {
		dt, err := s.dtService.GetByID(*domain.DomainTemplateID)
		if err == nil && dt != nil {
			return map[string]string(dt.Annotations)
		}
	}
	return nil
}

// GenerateYAMLs generates the Kubernetes YAML manifests for a domain
func (s *DomainService) GenerateYAMLs(domainID uuid.UUID) (*DomainYAMLs, error) {
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, fmt.Errorf("domain not found: %w", err)
	}

	result := &DomainYAMLs{}

	// Build Gateway YAML
	gatewayObj := kubernetes.BuildGatewayObject(domainplan.BuildGatewayConfig(domain, s.templateAnnotations(domain)))
	if gatewayObj != nil {
		gatewayYaml, err := yaml.Marshal(gatewayObj.Object)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Gateway: %w", err)
		}
		result.GatewayYaml = string(gatewayYaml)
	}

	// Build ClientTrafficPolicy YAML (if settings exist)
	settings, err := s.settingsRepo.GetByDomainID(domainID)
	if err == nil && settings != nil && !settings.Config.IsEmpty() {
		caRefs := s.collectCASecretRefs(domain, &settings.Config)
		ctpConfig := domainplan.BuildClientTrafficPolicyConfig(domain, &settings.Config, caRefs)
		ctpObj := kubernetes.BuildClientTrafficPolicy(ctpConfig)
		if ctpObj != nil {
			ctpYaml, err := yaml.Marshal(ctpObj.Object)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal ClientTrafficPolicy: %w", err)
			}
			result.ClientTrafficPolicyYaml = string(ctpYaml)
		}
	}

	// Build BackendTrafficPolicy YAML (if domain-level BTP exists)
	btpPolicy, err := s.btpRepo.GetByDomainID(domainID)
	if err == nil && btpPolicy != nil && !btpPolicy.Config.IsEmpty() {
		btpK8sConfig := domainplan.BuildBackendTrafficPolicyConfig(domain, &btpPolicy.Config)
		if btpK8sConfig != nil {
			btpObj := kubernetes.BuildBackendTrafficPolicy(btpK8sConfig)
			if btpObj != nil {
				btpYaml, err := yaml.Marshal(btpObj.Object)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal BackendTrafficPolicy: %w", err)
				}
				result.BackendTrafficPolicyYaml = string(btpYaml)
			}
		}
	}

	// Build EnvoyExtensionPolicy YAML (if domain-level extension policy exists)
	extPolicy, err := s.extPolicyRepo.GetByDomainID(domainID)
	if err == nil && extPolicy != nil && !extPolicy.Config.IsEmpty() {
		extK8sConfig := domainplan.BuildEnvoyExtensionPolicyConfig(domain, &extPolicy.Config)
		if extK8sConfig != nil {
			extObj := kubernetes.BuildEnvoyExtensionPolicy(extK8sConfig)
			if extObj != nil {
				extYaml, err := yaml.Marshal(extObj.Object)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal EnvoyExtensionPolicy: %w", err)
				}
				result.EnvoyExtensionPolicyYaml = string(extYaml)
			}
		}
	}

	return result, nil
}

// PreviewCreate generates a preview of the Gateway YAML that would be created, with optional AI review
func (s *DomainService) PreviewCreate(projectID uuid.UUID, input *DomainCreatePreviewInput, userID uuid.UUID) (*DomainCreatePreviewResult, error) {
	// Parse and validate domain template
	domainTemplateID, err := uuid.Parse(input.DomainTemplateID)
	if err != nil {
		return nil, errors.New("invalid domain template ID")
	}

	dt, err := s.domainTemplateRepo.GetByID(domainTemplateID)
	if err != nil {
		return nil, errors.New("domain template not found")
	}
	if dt.ProjectID != projectID {
		return nil, errors.New("domain template does not belong to this project")
	}
	if dt.Status != models.DomainTemplateStatusActive {
		return nil, fmt.Errorf("domain template '%s' is not active (status: %s)", dt.Name, dt.Status)
	}

	// Default namespace for preview
	previewNamespace := input.Namespace
	if previewNamespace == "" {
		previewNamespace = kubernetes.FastGatewayNamespace
	}

	// Build Gateway config from input + template
	k8sGatewayName := generateK8sName(input.Hostname)
	gatewayConfig := &kubernetes.GatewayConfig{
		Name:             k8sGatewayName,
		Namespace:        previewNamespace,
		GatewayClassName: dt.K8sGatewayClassName,
		Hostname:         input.Hostname,
		TLSMode:          string(dt.TLSMode),
		HTTPPort:         dt.HTTPPort,
		HTTPSPort:        dt.HTTPSPort,
		TLSSecretName:    input.TLSSecretName,
		TLSPolicy:        string(dt.TLSPolicy),
		Annotations:      map[string]string(dt.Annotations),
	}

	result := &DomainCreatePreviewResult{}

	gatewayObj := kubernetes.BuildGatewayObject(gatewayConfig)
	if gatewayObj != nil {
		gwYaml, err := yaml.Marshal(gatewayObj.Object)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Gateway: %w", err)
		}
		result.ProposedGatewayYaml = string(gwYaml)
	}

	// AI review only when explicitly requested
	if s.aiService.IsEnabled() && input.IncludeAIReview {
		ctx := context.Background()
		description := "New domain Gateway creation"
		if input.Description != "" {
			description = input.Description
		}
		reviewReq := ai.ReviewRequest{
			Action:      "create",
			Description: description,
			ProposedYaml: &ai.YamlSet{
				Backend: result.ProposedGatewayYaml,
			},
		}
		reviewResult, err := s.aiService.Review(ctx, userID, reviewReq)
		if err != nil {
			log.Printf("AI review failed (non-fatal): %v", err)
		} else {
			result.AIReview = reviewResult
		}
	}

	return result, nil
}

// DomainSettingsPreviewInput extends UpdateDomainSettingsInput with a description for AI review
type DomainSettingsPreviewInput struct {
	UpdateDomainSettingsInput
	Description     string `json:"description,omitempty"`
	IncludeAIReview bool   `json:"includeAIReview,omitempty"`
}

// DomainSettingsPreviewResult contains diff and AI review for proposed settings changes
type DomainSettingsPreviewResult struct {
	CurrentGatewayYaml               string           `json:"currentGatewayYaml"`
	CurrentClientTrafficPolicyYaml   string           `json:"currentClientTrafficPolicyYaml"`
	ProposedClientTrafficPolicyYaml  string           `json:"proposedClientTrafficPolicyYaml"`
	CurrentBackendTrafficPolicyYaml  string           `json:"currentBackendTrafficPolicyYaml,omitempty"`
	ProposedBackendTrafficPolicyYaml string           `json:"proposedBackendTrafficPolicyYaml,omitempty"`
	CurrentEnvoyExtensionPolicyYaml  string           `json:"currentEnvoyExtensionPolicyYaml,omitempty"`
	ProposedEnvoyExtensionPolicyYaml string           `json:"proposedEnvoyExtensionPolicyYaml,omitempty"`
	AIReview                         *ai.ReviewResult `json:"aiReview,omitempty"`
}

// PreviewSettingsChanges generates a diff and AI review for proposed domain settings changes
func (s *DomainService) PreviewSettingsChanges(domainID uuid.UUID, input *DomainSettingsPreviewInput, userID uuid.UUID) (*DomainSettingsPreviewResult, error) {
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, fmt.Errorf("domain not found: %w", err)
	}

	result := &DomainSettingsPreviewResult{}

	// Build Gateway YAML (context, doesn't change on settings edit)
	gatewayObj := kubernetes.BuildGatewayObject(domainplan.BuildGatewayConfig(domain, s.templateAnnotations(domain)))
	if gatewayObj != nil {
		gwYaml, _ := yaml.Marshal(gatewayObj.Object)
		result.CurrentGatewayYaml = string(gwYaml)
	}

	// Build current CTP YAML
	settings, err := s.settingsRepo.GetByDomainID(domainID)
	if err == nil && settings != nil && !settings.Config.IsEmpty() {
		caRefs := s.collectCASecretRefs(domain, &settings.Config)
		ctpConfig := domainplan.BuildClientTrafficPolicyConfig(domain, &settings.Config, caRefs)
		ctpObj := kubernetes.BuildClientTrafficPolicy(ctpConfig)
		if ctpObj != nil {
			ctpYaml, _ := yaml.Marshal(ctpObj.Object)
			result.CurrentClientTrafficPolicyYaml = string(ctpYaml)
		}
	}

	// Build proposed CTP YAML
	proposedConfig := models.DomainSettingsConfig{
		ClientConnection:  input.ClientConnection,
		Timeout:           input.Timeout,
		HTTP3:             input.HTTP3,
		TLS:               input.TLS,
		ClientIPDetection: input.ClientIPDetection,
		MTLS:              input.MTLS,
	}
	if !proposedConfig.IsEmpty() {
		caRefs := s.collectCASecretRefs(domain, &proposedConfig)
		ctpConfig := domainplan.BuildClientTrafficPolicyConfig(domain, &proposedConfig, caRefs)
		ctpObj := kubernetes.BuildClientTrafficPolicy(ctpConfig)
		if ctpObj != nil {
			ctpYaml, _ := yaml.Marshal(ctpObj.Object)
			result.ProposedClientTrafficPolicyYaml = string(ctpYaml)
		}
	}

	// Build current BTP YAML
	btpPolicy, err := s.btpRepo.GetByDomainID(domainID)
	if err == nil && btpPolicy != nil && !btpPolicy.Config.IsEmpty() {
		btpK8sConfig := domainplan.BuildBackendTrafficPolicyConfig(domain, &btpPolicy.Config)
		if btpK8sConfig != nil {
			btpObj := kubernetes.BuildBackendTrafficPolicy(btpK8sConfig)
			if btpObj != nil {
				btpYaml, _ := yaml.Marshal(btpObj.Object)
				result.CurrentBackendTrafficPolicyYaml = string(btpYaml)
			}
		}
	}

	// Build proposed BTP YAML
	if input.BackendTrafficPolicy != nil && !input.BackendTrafficPolicy.IsEmpty() {
		btpK8sConfig := domainplan.BuildBackendTrafficPolicyConfig(domain, input.BackendTrafficPolicy)
		if btpK8sConfig != nil {
			btpObj := kubernetes.BuildBackendTrafficPolicy(btpK8sConfig)
			if btpObj != nil {
				btpYaml, _ := yaml.Marshal(btpObj.Object)
				result.ProposedBackendTrafficPolicyYaml = string(btpYaml)
			}
		}
	}

	// Build current EnvoyExtensionPolicy YAML
	extPolicy, err := s.extPolicyRepo.GetByDomainID(domainID)
	if err == nil && extPolicy != nil && !extPolicy.Config.IsEmpty() {
		extK8sConfig := domainplan.BuildEnvoyExtensionPolicyConfig(domain, &extPolicy.Config)
		if extK8sConfig != nil {
			extObj := kubernetes.BuildEnvoyExtensionPolicy(extK8sConfig)
			if extObj != nil {
				extYaml, _ := yaml.Marshal(extObj.Object)
				result.CurrentEnvoyExtensionPolicyYaml = string(extYaml)
			}
		}
	}

	// Build proposed EnvoyExtensionPolicy YAML
	if input.ExtensionPolicy != nil && !input.ExtensionPolicy.IsEmpty() {
		extK8sConfig := domainplan.BuildEnvoyExtensionPolicyConfig(domain, input.ExtensionPolicy)
		if extK8sConfig != nil {
			extObj := kubernetes.BuildEnvoyExtensionPolicy(extK8sConfig)
			if extObj != nil {
				extYaml, _ := yaml.Marshal(extObj.Object)
				result.ProposedEnvoyExtensionPolicyYaml = string(extYaml)
			}
		}
	}

	// AI review only when explicitly requested via flag
	if input.IncludeAIReview && s.aiService.IsEnabled() {
		ctx := context.Background()
		description := "Domain settings (ClientTrafficPolicy) update"
		if input.Description != "" {
			description = input.Description
		}
		reviewReq := ai.ReviewRequest{
			Action:      "update",
			Description: description,
			CurrentYaml: &ai.YamlSet{
				Backend: result.CurrentGatewayYaml + "\n---\n" + result.CurrentClientTrafficPolicyYaml,
			},
			ProposedYaml: &ai.YamlSet{
				Backend: result.CurrentGatewayYaml + "\n---\n" + result.ProposedClientTrafficPolicyYaml,
			},
		}
		reviewResult, err := s.aiService.Review(ctx, userID, reviewReq)
		if err != nil {
			log.Printf("AI review failed (non-fatal): %v", err)
		} else {
			result.AIReview = reviewResult
		}
	}

	return result, nil
}

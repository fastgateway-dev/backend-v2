package services

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

// DomainService handles domain business logic
type DomainService struct {
	domainRepo           repository.DomainRepositoryInterface
	projectRepo          repository.ProjectRepositoryInterface
	domainTemplateRepo   repository.DomainTemplateRepositoryInterface
	k8sService           KubernetesServiceInterface
	settingsRepo         repository.DomainSettingsRepositoryInterface
	clientAttachmentRepo repository.ClientAttachmentRepositoryInterface
	btpRepo              repository.BackendTrafficPolicyRepositoryInterface
	extPolicyRepo        repository.EnvoyExtensionPolicyRepositoryInterface
	dtService            *DomainTemplateService
	aiService            *AIService
	projectNamespaceRepo repository.ProjectNamespaceRepositoryInterface
}

// SetDomainSettingsRepository sets the domain settings repository
func (s *DomainService) SetDomainSettingsRepository(repo repository.DomainSettingsRepositoryInterface) {
	s.settingsRepo = repo
}

// SetClientAttachmentRepository sets the client attachment repository
func (s *DomainService) SetClientAttachmentRepository(repo repository.ClientAttachmentRepositoryInterface) {
	s.clientAttachmentRepo = repo
}

// SetDomainTemplateService sets the domain template service
func (s *DomainService) SetDomainTemplateService(dts *DomainTemplateService) {
	s.dtService = dts
}

// SetAIService sets the AI service for review functionality
func (s *DomainService) SetAIService(as *AIService) {
	s.aiService = as
}

// SetBackendTrafficPolicyRepository sets the backend traffic policy repository
func (s *DomainService) SetBackendTrafficPolicyRepository(repo repository.BackendTrafficPolicyRepositoryInterface) {
	s.btpRepo = repo
}

// SetEnvoyExtensionPolicyRepository sets the envoy extension policy repository
func (s *DomainService) SetEnvoyExtensionPolicyRepository(repo repository.EnvoyExtensionPolicyRepositoryInterface) {
	s.extPolicyRepo = repo
}

// SetProjectNamespaceRepository sets the project namespace repository
func (s *DomainService) SetProjectNamespaceRepository(repo repository.ProjectNamespaceRepositoryInterface) {
	s.projectNamespaceRepo = repo
}

// NewDomainService creates a new domain service
func NewDomainService(domainRepo repository.DomainRepositoryInterface, projectRepo repository.ProjectRepositoryInterface, domainTemplateRepo repository.DomainTemplateRepositoryInterface, k8sService KubernetesServiceInterface) *DomainService {
	return &DomainService{
		domainRepo:         domainRepo,
		projectRepo:        projectRepo,
		domainTemplateRepo: domainTemplateRepo,
		k8sService:         k8sService,
	}
}

// FastGatewayNamespace is the namespace where all Gateway objects are deployed
const FastGatewayNamespace = "fastgateway-system"

// CreateDomainInput represents input for creating a domain
type CreateDomainInput struct {
	Name               string        `json:"name" binding:"required"`
	Hostname           string        `json:"hostname" binding:"required"`
	DomainTemplateID   string        `json:"domainTemplateId" binding:"required"`
	TLSSecretName      string        `json:"tlsSecretName"`
	TLSSecretNamespace string        `json:"tlsSecretNamespace"`
	Namespace          string        `json:"namespace"`
	Labels             models.Labels `json:"labels,omitempty"`
}

// UpdateDomainInput represents input for updating a domain
type UpdateDomainInput struct {
	Name               string        `json:"name"`
	TLSSecretName      string        `json:"tlsSecretName"`
	TLSSecretNamespace string        `json:"tlsSecretNamespace"`
	Labels             models.Labels `json:"labels,omitempty"`
}

// ListTLSSecretsResponse is the API response for listing TLS secrets
type ListTLSSecretsResponse struct {
	Namespace           string          `json:"namespace"`
	Secrets             []TLSSecretInfo `json:"secrets"`
	AvailableNamespaces []string        `json:"availableNamespaces"`
}

// Create creates a new domain
func (s *DomainService) Create(projectID uuid.UUID, input *CreateDomainInput, createdBy uuid.UUID) (*models.Domain, error) {
	// Check if hostname already exists in project
	exists, err := s.domainRepo.ExistsByHostname(projectID, input.Hostname)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("hostname already exists in this project")
	}

	// Parse domain template ID
	domainTemplateID, err := uuid.Parse(input.DomainTemplateID)
	if err != nil {
		return nil, errors.New("invalid domain template ID")
	}

	// Get domain template and validate it's active
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

	// Validate TLS secret is required when TLS is enabled
	tlsMode := dt.TLSMode
	if (tlsMode == models.TLSModeOnly || tlsMode == models.TLSModeBoth) && input.TLSSecretName == "" {
		return nil, errors.New("TLS secret name is required when TLS is enabled in the domain template")
	}

	// Validate TLS secret namespace is managed by the project
	if input.TLSSecretNamespace != "" && input.TLSSecretNamespace != FastGatewayNamespace {
		if s.projectNamespaceRepo != nil {
			managed, err := s.projectNamespaceRepo.ExistsByProjectAndNamespace(projectID, input.TLSSecretNamespace)
			if err != nil {
				return nil, fmt.Errorf("failed to validate TLS secret namespace: %w", err)
			}
			if !managed {
				return nil, fmt.Errorf("namespace '%s' is not managed by this project. Add it in Project Settings > Namespaces", input.TLSSecretNamespace)
			}
		}
	}

	// Validate labels
	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
	}

	// Default and validate domain namespace
	if input.Namespace == "" {
		input.Namespace = FastGatewayNamespace
	}
	if input.Namespace != FastGatewayNamespace {
		if s.projectNamespaceRepo == nil {
			return nil, errors.New("namespace management not configured")
		}
		ns, err := s.projectNamespaceRepo.GetByProjectAndNamespace(projectID, input.Namespace)
		if err != nil {
			return nil, fmt.Errorf("namespace '%s' is not registered for this project", input.Namespace)
		}
		if !ns.HasCapability(models.NamespaceCapabilityDeployGateway) {
			return nil, fmt.Errorf("namespace '%s' is not enabled for deployment (missing capability '%s')", input.Namespace, models.NamespaceCapabilityDeployGateway)
		}
	}

	// Generate K8s resource names
	k8sGatewayName := generateK8sName(input.Hostname)

	// Inherit settings from domain template
	k8sGatewayClass := dt.K8sGatewayClassName

	domain := &models.Domain{
		ProjectID:          projectID,
		DomainTemplateID:   &domainTemplateID,
		Name:               input.Name,
		Hostname:           input.Hostname,
		HTTPPort:           dt.HTTPPort,
		HTTPSPort:          dt.HTTPSPort,
		TLSMode:            string(tlsMode),
		Namespace:          input.Namespace,
		TLSSecretName:      input.TLSSecretName,
		TLSSecretNamespace: input.TLSSecretNamespace,
		TLSPolicy:          dt.TLSPolicy,
		K8sGatewayName:     k8sGatewayName,
		K8sGatewayClass:    k8sGatewayClass,
		Status:             models.DomainStatusPending,
		CreatedBy:          createdBy,
		Labels:             input.Labels,
	}

	if err := s.domainRepo.Create(domain); err != nil {
		return nil, err
	}

	// Create Gateway in Kubernetes
	ctx := context.Background()
	gatewayConfig := &GatewayConfig{
		Name:               k8sGatewayName,
		Namespace:          input.Namespace,
		GatewayClassName:   k8sGatewayClass,
		Hostname:           input.Hostname,
		TLSMode:            string(tlsMode),
		HTTPPort:           dt.HTTPPort,
		HTTPSPort:          dt.HTTPSPort,
		TLSSecretName:      input.TLSSecretName,
		TLSSecretNamespace: input.TLSSecretNamespace,
		TLSPolicy:          string(dt.TLSPolicy),
		Annotations:        dt.Annotations,
	}

	if err := s.k8sService.CreateGateway(ctx, projectID, gatewayConfig); err != nil {
		log.Printf("Failed to create Gateway in Kubernetes: %v", err)
		domain.Status = models.DomainStatusError
		domain.StatusMessage = fmt.Sprintf("Failed to create Gateway: %v", err)
		_ = s.domainRepo.Update(domain)
		return domain, nil // Return domain but with error status
	}

	// Update status to active
	domain.Status = models.DomainStatusActive
	domain.StatusMessage = "Gateway created successfully"
	_ = s.domainRepo.Update(domain)

	// Sync ReferenceGrants if domain is in a non-default namespace
	if input.Namespace != FastGatewayNamespace {
		s.syncReferenceGrants(projectID)
	}

	return domain, nil
}

// GetByID gets a domain by ID
func (s *DomainService) GetByID(id uuid.UUID) (*models.Domain, error) {
	return s.domainRepo.GetByID(id)
}

// ListByProjectID lists domains in a project
func (s *DomainService) ListByProjectID(projectID uuid.UUID, page, limit int, search string, status string, labels map[string]string) ([]models.Domain, int64, error) {
	return s.domainRepo.ListByProjectID(projectID, page, limit, search, status, labels)
}

// Update updates a domain
func (s *DomainService) Update(id uuid.UUID, input *UpdateDomainInput) (*models.Domain, error) {
	domain, err := s.domainRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if input.Name != "" {
		domain.Name = input.Name
	}

	if input.TLSSecretName != "" {
		domain.TLSSecretName = input.TLSSecretName
	}

	if input.TLSSecretNamespace != "" {
		if input.TLSSecretNamespace != FastGatewayNamespace {
			if s.projectNamespaceRepo != nil {
				managed, err := s.projectNamespaceRepo.ExistsByProjectAndNamespace(domain.ProjectID, input.TLSSecretNamespace)
				if err != nil {
					return nil, fmt.Errorf("failed to validate TLS secret namespace: %w", err)
				}
				if !managed {
					return nil, fmt.Errorf("namespace '%s' is not managed by this project. Add it in Project Settings > Namespaces", input.TLSSecretNamespace)
				}
			}
		}
		domain.TLSSecretNamespace = input.TLSSecretNamespace
	}

	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
		domain.Labels = input.Labels
	}

	// Note: TLS policy and port cannot be updated - they are inherited from the template

	if err := s.domainRepo.Update(domain); err != nil {
		return nil, err
	}

	// TODO: Update Kubernetes resources

	return domain, nil
}

// Delete deletes a domain
func (s *DomainService) Delete(id uuid.UUID) error {
	domain, err := s.domainRepo.GetByID(id)
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Delete domain-level BackendTrafficPolicy from K8s and DB
	btpName := domain.K8sGatewayName + "-btp"
	if err := s.k8sService.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName); err != nil {
		log.Printf("Failed to delete domain BackendTrafficPolicy from Kubernetes: %v", err)
	}
	if s.btpRepo != nil {
		_ = s.btpRepo.DeleteByDomainID(id)
	}

	// Delete domain-level EnvoyExtensionPolicy from K8s and DB
	eepName := domain.K8sGatewayName + "-eep"
	extProcBackendName := GenerateExtProcBackendNameForDomain(domain.K8sGatewayName)
	_ = s.k8sService.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
	if err := s.k8sService.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName); err != nil {
		log.Printf("Failed to delete domain EnvoyExtensionPolicy from Kubernetes: %v", err)
	}
	if s.extPolicyRepo != nil {
		_ = s.extPolicyRepo.DeleteByDomainID(id)
	}

	// Delete gateway-specific resources from Kubernetes
	ctpName := domain.K8sGatewayName + "-ctp"
	if err := s.k8sService.DeleteClientTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, ctpName); err != nil {
		log.Printf("Failed to delete ClientTrafficPolicy from Kubernetes: %v", err)
		// Continue with deletion even if K8s deletion fails
	}

	// Delete domain settings from database if exists
	if s.settingsRepo != nil {
		_ = s.settingsRepo.DeleteByDomainID(id)
	}

	// Delete Gateway from Kubernetes
	if err := s.k8sService.DeleteGateway(ctx, domain.ProjectID, domain.Namespace, domain.K8sGatewayName); err != nil {
		log.Printf("Failed to delete Gateway from Kubernetes: %v", err)
		// Continue with database deletion even if K8s deletion fails
	}

	// Sync ReferenceGrants after deleting K8s resources
	projectID := domain.ProjectID
	domainNamespace := domain.Namespace

	if err := s.domainRepo.Delete(id); err != nil {
		return err
	}

	// After DB deletion, sync ReferenceGrants to potentially remove this namespace
	if domainNamespace != FastGatewayNamespace {
		s.syncReferenceGrants(projectID)
	}

	return nil
}

// getProjectDomainNamespaces returns the unique namespaces used by domains in a project.
// Always includes FastGatewayNamespace.
func (s *DomainService) getProjectDomainNamespaces(projectID uuid.UUID) ([]string, error) {
	domains, _, err := s.domainRepo.ListByProjectID(projectID, 1, 10000, "", "", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list domains for namespace collection: %w", err)
	}

	seen := map[string]bool{FastGatewayNamespace: true}
	namespaces := []string{FastGatewayNamespace}
	for _, d := range domains {
		if !seen[d.Namespace] {
			namespaces = append(namespaces, d.Namespace)
			seen[d.Namespace] = true
		}
	}
	return namespaces, nil
}

// syncReferenceGrants updates all ReferenceGrants in backend namespaces to allow
// references from all domain namespaces. Also creates a ReferenceGrant in
// fastgateway-system for cross-namespace secret access when non-default domain
// namespaces exist.
func (s *DomainService) syncReferenceGrants(projectID uuid.UUID) {
	ctx := context.Background()

	domainNamespaces, err := s.getProjectDomainNamespaces(projectID)
	if err != nil {
		log.Printf("Failed to get domain namespaces for ReferenceGrant sync: %v", err)
		return
	}

	// Get all backend namespaces
	if s.projectNamespaceRepo == nil {
		return
	}
	backendNamespaces, err := s.projectNamespaceRepo.ListByProjectID(projectID)
	if err != nil {
		log.Printf("Failed to list backend namespaces for ReferenceGrant sync: %v", err)
		return
	}

	// Update ReferenceGrant in each backend namespace, honoring capabilities.
	for _, bns := range backendNamespaces {
		toKinds := models.ReferenceGrantKindsForCapabilities(bns.Capabilities)
		rgName := generateReferenceGrantName(projectID, bns.Namespace)
		if len(toKinds) == 0 {
			// Namespace has no target capabilities — make sure no stale RG lingers.
			_ = s.k8sService.DeleteReferenceGrant(ctx, projectID, bns.Namespace, rgName)
			continue
		}
		rgConfig := &ReferenceGrantConfig{
			Name:           rgName,
			FromNamespaces: domainNamespaces,
			ToNamespace:    bns.Namespace,
			ToKinds:        toKinds,
		}
		if err := s.k8sService.RecreateReferenceGrant(ctx, projectID, rgConfig); err != nil {
			log.Printf("Failed to sync ReferenceGrant in %s: %v", bns.Namespace, err)
		}
	}

	// Handle cross-namespace secret access: if there are domain namespaces other
	// than fastgateway-system, create a ReferenceGrant IN fastgateway-system
	// allowing those namespaces to reference secrets there.
	nonDefaultNamespaces := make([]string, 0)
	for _, ns := range domainNamespaces {
		if ns != FastGatewayNamespace {
			nonDefaultNamespaces = append(nonDefaultNamespaces, ns)
		}
	}

	shortID := projectID.String()[:8]
	secretsRGName := fmt.Sprintf("fastgateway-%s-secrets", shortID)

	if len(nonDefaultNamespaces) > 0 {
		rgConfig := &ReferenceGrantConfig{
			Name:           secretsRGName,
			FromNamespaces: nonDefaultNamespaces,
			ToNamespace:    FastGatewayNamespace,
			ToKinds:        []string{"Secret"},
		}
		if err := s.k8sService.RecreateReferenceGrant(ctx, projectID, rgConfig); err != nil {
			log.Printf("Failed to sync secrets ReferenceGrant in fastgateway-system: %v", err)
		}
	} else {
		// No non-default namespaces, clean up the secrets ReferenceGrant if it exists
		_ = s.k8sService.DeleteReferenceGrant(ctx, projectID, FastGatewayNamespace, secretsRGName)
	}
}

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
	if s.settingsRepo == nil {
		return nil, errors.New("domain settings repository not configured")
	}
	return s.settingsRepo.GetByDomainID(domainID)
}

// UpdateDomainSettings updates the settings for a domain
// This is the gateway-agnostic API - internally translates to gateway-specific resources
func (s *DomainService) UpdateDomainSettings(domainID uuid.UUID, input *UpdateDomainSettingsInput) (*models.DomainSettings, error) {
	if s.settingsRepo == nil {
		return nil, errors.New("domain settings repository not configured")
	}

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
		if err := s.k8sService.DeleteClientTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, ctpName); err != nil {
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
		if err := s.k8sService.DeleteClientTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, ctpName); err != nil {
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

// buildCTPConfig builds a ClientTrafficPolicyConfig from domain and settings config
func (s *DomainService) buildCTPConfig(domain *models.Domain, config *models.DomainSettingsConfig, caSecretRefs []SecretRefPolicyConfig) *ClientTrafficPolicyConfig {
	ctpName := domain.K8sGatewayName + "-ctp"
	ctpConfig := &ClientTrafficPolicyConfig{
		Name:      ctpName,
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		TargetRef: ClientTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  "Gateway",
			Name:  domain.K8sGatewayName,
		},
	}

	if config.ClientConnection != nil {
		if config.ClientConnection.TCPKeepalive != nil {
			ctpConfig.TCPKeepalive = &TCPKeepalivePolicyConfig{
				Probes:   config.ClientConnection.TCPKeepalive.Probes,
				IdleTime: config.ClientConnection.TCPKeepalive.IdleTime,
				Interval: config.ClientConnection.TCPKeepalive.Interval,
			}
		}
		if config.ClientConnection.ProxyProtocol != nil && config.ClientConnection.ProxyProtocol.Enabled {
			ctpConfig.EnableProxyProtocol = true
		}
		if config.ClientConnection.ConnectionLimit != nil || config.ClientConnection.BufferLimit != nil {
			ctpConfig.Connection = &ConnectionPolicyConfig{}
			if config.ClientConnection.BufferLimit != nil {
				ctpConfig.Connection.BufferLimit = config.ClientConnection.BufferLimit
			}
			if config.ClientConnection.ConnectionLimit != nil {
				ctpConfig.Connection.MaxConnections = config.ClientConnection.ConnectionLimit.MaxConnections
				ctpConfig.Connection.CloseDelay = config.ClientConnection.ConnectionLimit.CloseDelay
				ctpConfig.Connection.MaxConnectionDuration = config.ClientConnection.ConnectionLimit.MaxConnectionDuration
				ctpConfig.Connection.MaxRequestsPerConnection = config.ClientConnection.ConnectionLimit.MaxRequestsPerConnection
			}
		}
	}

	if config.Timeout != nil && config.Timeout.HTTP != nil {
		ctpConfig.Timeout = &TimeoutPolicyConfig{
			HTTP: &HTTPTimeoutPolicyConfig{
				RequestReceivedTimeout: config.Timeout.HTTP.RequestReceivedTimeout,
				IdleTimeout:            config.Timeout.HTTP.IdleTimeout,
			},
		}
	}

	if config.HTTP3 != nil && config.HTTP3.Enabled {
		ctpConfig.HTTP3 = &HTTP3PolicyConfig{Enabled: true}
	}

	if config.TLS != nil && !config.TLS.IsEmpty() {
		ctpConfig.TLS = &TLSPolicyConfig{
			MinVersion:          config.TLS.MinVersion,
			MaxVersion:          config.TLS.MaxVersion,
			Ciphers:             config.TLS.Ciphers,
			ECDHCurves:          config.TLS.ECDHCurves,
			SignatureAlgorithms: config.TLS.SignatureAlgorithms,
		}
	}

	if config.ClientIPDetection != nil {
		ctpConfig.ClientIPDetection = &ClientIPDetectionPolicyConfig{}
		if config.ClientIPDetection.XForwardedFor != nil {
			ctpConfig.ClientIPDetection.XForwardedFor = &XForwardedForPolicyConfig{
				NumTrustedHops: config.ClientIPDetection.XForwardedFor.NumTrustedHops,
			}
		}
		if config.ClientIPDetection.CustomHeader != nil {
			ctpConfig.ClientIPDetection.CustomHeader = &CustomHeaderPolicyConfig{
				Name:       config.ClientIPDetection.CustomHeader.Name,
				FailClosed: config.ClientIPDetection.CustomHeader.FailClosed,
			}
		}
	}

	// mTLS client validation
	if config.MTLS != nil && config.MTLS.Enabled && len(caSecretRefs) > 0 {
		ctpConfig.ClientValidation = &ClientValidationPolicyConfig{
			Optional:          config.MTLS.Optional,
			CACertificateRefs: caSecretRefs,
		}
		for _, san := range config.MTLS.SANWhitelist {
			ctpConfig.ClientValidation.SANMatchers = append(ctpConfig.ClientValidation.SANMatchers, SANMatcherPolicyConfig{
				Type:  san.Type,
				Match: san.Value,
			})
		}
		ctpConfig.ClientValidation.CertificateHashes = config.MTLS.HashWhitelist
		ctpConfig.Headers = &HeadersPolicyConfig{
			XForwardedClientCert: &XFCCPolicyConfig{
				Mode:             "AppendForward",
				CertDetailsToAdd: []string{"Subject", "Cert", "DNS", "URI"},
			},
		}
	}

	return ctpConfig
}

// applyEnvoyGatewayClientTrafficPolicy translates domain settings to Envoy Gateway ClientTrafficPolicy CRD
func (s *DomainService) applyEnvoyGatewayClientTrafficPolicy(ctx context.Context, domain *models.Domain, config *models.DomainSettingsConfig) error {
	caSecretRefs := s.collectCASecretRefs(domain, config)
	ctpConfig := s.buildCTPConfig(domain, config, caSecretRefs)

	if err := s.k8sService.CreateClientTrafficPolicy(ctx, domain.ProjectID, ctpConfig); err != nil {
		return fmt.Errorf("failed to apply ClientTrafficPolicy to Kubernetes: %w", err)
	}
	return nil
}

// collectCASecretRefs builds the list of CA secret refs from domain config and active client mTLS attachments.
// Used by applyEnvoyGatewayClientTrafficPolicy, GenerateYAMLs, and PreviewSettingsChanges.
func (s *DomainService) collectCASecretRefs(domain *models.Domain, config *models.DomainSettingsConfig) []SecretRefPolicyConfig {
	if config.MTLS == nil || !config.MTLS.Enabled {
		return nil
	}

	var refs []SecretRefPolicyConfig

	// Domain CAs
	for _, ca := range config.MTLS.CACerts {
		refs = append(refs, SecretRefPolicyConfig{
			Group: "",
			Kind:  "Secret",
			Name:  ca.SecretName,
		})
	}

	// Client CAs from active mTLS attachments
	if s.clientAttachmentRepo != nil {
		mtlsClients, err := s.clientAttachmentRepo.GetMTLSClientsForDomain(domain.ID)
		if err != nil {
			log.Printf("Warning: failed to get mTLS clients for domain %s: %v", domain.ID, err)
		} else {
			for _, client := range mtlsClients {
				if client.MTLSCASecret != "" {
					refs = append(refs, SecretRefPolicyConfig{
						Group: "",
						Kind:  "Secret",
						Name:  client.MTLSCASecret,
					})
				}
			}
		}
	}

	return refs
}

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

// buildGatewayConfig builds a GatewayConfig from a domain, including template annotations
func (s *DomainService) buildGatewayConfig(domain *models.Domain) *GatewayConfig {
	config := &GatewayConfig{
		Name:             domain.K8sGatewayName,
		Namespace:        domain.Namespace,
		GatewayClassName: domain.K8sGatewayClass,
		Hostname:         domain.Hostname,
		TLSMode:          domain.TLSMode,
		HTTPPort:         domain.HTTPPort,
		HTTPSPort:        domain.HTTPSPort,
		TLSSecretName:    domain.TLSSecretName,
		TLSPolicy:        string(domain.TLSPolicy),
	}
	// Include annotations from domain template
	if s.dtService != nil && domain.DomainTemplateID != nil {
		dt, err := s.dtService.GetByID(*domain.DomainTemplateID)
		if err == nil && dt != nil {
			config.Annotations = map[string]string(dt.Annotations)
		}
	}
	return config
}

// GenerateYAMLs generates the Kubernetes YAML manifests for a domain
func (s *DomainService) GenerateYAMLs(domainID uuid.UUID) (*DomainYAMLs, error) {
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, fmt.Errorf("domain not found: %w", err)
	}

	result := &DomainYAMLs{}

	// Build Gateway YAML
	gatewayObj := BuildGatewayObject(s.buildGatewayConfig(domain))
	if gatewayObj != nil {
		gatewayYaml, err := yaml.Marshal(gatewayObj.Object)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Gateway: %w", err)
		}
		result.GatewayYaml = string(gatewayYaml)
	}

	// Build ClientTrafficPolicy YAML (if settings exist)
	if s.settingsRepo != nil {
		settings, err := s.settingsRepo.GetByDomainID(domainID)
		if err == nil && settings != nil && !settings.Config.IsEmpty() {
			caRefs := s.collectCASecretRefs(domain, &settings.Config)
			ctpConfig := s.buildCTPConfig(domain, &settings.Config, caRefs)
			ctpObj := BuildClientTrafficPolicy(ctpConfig)
			if ctpObj != nil {
				ctpYaml, err := yaml.Marshal(ctpObj.Object)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal ClientTrafficPolicy: %w", err)
				}
				result.ClientTrafficPolicyYaml = string(ctpYaml)
			}
		}
	}

	// Build BackendTrafficPolicy YAML (if domain-level BTP exists)
	if s.btpRepo != nil {
		btpPolicy, err := s.btpRepo.GetByDomainID(domainID)
		if err == nil && btpPolicy != nil && !btpPolicy.Config.IsEmpty() {
			btpK8sConfig := s.buildDomainBTPConfig(domain, &btpPolicy.Config)
			if btpK8sConfig != nil {
				btpObj := BuildBackendTrafficPolicy(btpK8sConfig)
				if btpObj != nil {
					btpYaml, err := yaml.Marshal(btpObj.Object)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal BackendTrafficPolicy: %w", err)
					}
					result.BackendTrafficPolicyYaml = string(btpYaml)
				}
			}
		}
	}

	// Build EnvoyExtensionPolicy YAML (if domain-level extension policy exists)
	if s.extPolicyRepo != nil {
		extPolicy, err := s.extPolicyRepo.GetByDomainID(domainID)
		if err == nil && extPolicy != nil && !extPolicy.Config.IsEmpty() {
			extK8sConfig := s.buildDomainExtensionPolicyConfig(domain, &extPolicy.Config)
			if extK8sConfig != nil {
				extObj := BuildEnvoyExtensionPolicy(extK8sConfig)
				if extObj != nil {
					extYaml, err := yaml.Marshal(extObj.Object)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal EnvoyExtensionPolicy: %w", err)
					}
					result.EnvoyExtensionPolicyYaml = string(extYaml)
				}
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
		previewNamespace = FastGatewayNamespace
	}

	// Build Gateway config from input + template
	k8sGatewayName := generateK8sName(input.Hostname)
	gatewayConfig := &GatewayConfig{
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

	gatewayObj := BuildGatewayObject(gatewayConfig)
	if gatewayObj != nil {
		gwYaml, err := yaml.Marshal(gatewayObj.Object)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Gateway: %w", err)
		}
		result.ProposedGatewayYaml = string(gwYaml)
	}

	// AI review only when explicitly requested
	if s.aiService != nil && s.aiService.IsEnabled() && input.IncludeAIReview {
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
	gatewayObj := BuildGatewayObject(s.buildGatewayConfig(domain))
	if gatewayObj != nil {
		gwYaml, _ := yaml.Marshal(gatewayObj.Object)
		result.CurrentGatewayYaml = string(gwYaml)
	}

	// Build current CTP YAML
	if s.settingsRepo != nil {
		settings, err := s.settingsRepo.GetByDomainID(domainID)
		if err == nil && settings != nil && !settings.Config.IsEmpty() {
			caRefs := s.collectCASecretRefs(domain, &settings.Config)
			ctpConfig := s.buildCTPConfig(domain, &settings.Config, caRefs)
			ctpObj := BuildClientTrafficPolicy(ctpConfig)
			if ctpObj != nil {
				ctpYaml, _ := yaml.Marshal(ctpObj.Object)
				result.CurrentClientTrafficPolicyYaml = string(ctpYaml)
			}
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
		ctpConfig := s.buildCTPConfig(domain, &proposedConfig, caRefs)
		ctpObj := BuildClientTrafficPolicy(ctpConfig)
		if ctpObj != nil {
			ctpYaml, _ := yaml.Marshal(ctpObj.Object)
			result.ProposedClientTrafficPolicyYaml = string(ctpYaml)
		}
	}

	// Build current BTP YAML
	if s.btpRepo != nil {
		btpPolicy, err := s.btpRepo.GetByDomainID(domainID)
		if err == nil && btpPolicy != nil && !btpPolicy.Config.IsEmpty() {
			btpK8sConfig := s.buildDomainBTPConfig(domain, &btpPolicy.Config)
			if btpK8sConfig != nil {
				btpObj := BuildBackendTrafficPolicy(btpK8sConfig)
				if btpObj != nil {
					btpYaml, _ := yaml.Marshal(btpObj.Object)
					result.CurrentBackendTrafficPolicyYaml = string(btpYaml)
				}
			}
		}
	}

	// Build proposed BTP YAML
	if input.BackendTrafficPolicy != nil && !input.BackendTrafficPolicy.IsEmpty() {
		btpK8sConfig := s.buildDomainBTPConfig(domain, input.BackendTrafficPolicy)
		if btpK8sConfig != nil {
			btpObj := BuildBackendTrafficPolicy(btpK8sConfig)
			if btpObj != nil {
				btpYaml, _ := yaml.Marshal(btpObj.Object)
				result.ProposedBackendTrafficPolicyYaml = string(btpYaml)
			}
		}
	}

	// Build current EnvoyExtensionPolicy YAML
	if s.extPolicyRepo != nil {
		extPolicy, err := s.extPolicyRepo.GetByDomainID(domainID)
		if err == nil && extPolicy != nil && !extPolicy.Config.IsEmpty() {
			extK8sConfig := s.buildDomainExtensionPolicyConfig(domain, &extPolicy.Config)
			if extK8sConfig != nil {
				extObj := BuildEnvoyExtensionPolicy(extK8sConfig)
				if extObj != nil {
					extYaml, _ := yaml.Marshal(extObj.Object)
					result.CurrentEnvoyExtensionPolicyYaml = string(extYaml)
				}
			}
		}
	}

	// Build proposed EnvoyExtensionPolicy YAML
	if input.ExtensionPolicy != nil && !input.ExtensionPolicy.IsEmpty() {
		extK8sConfig := s.buildDomainExtensionPolicyConfig(domain, input.ExtensionPolicy)
		if extK8sConfig != nil {
			extObj := BuildEnvoyExtensionPolicy(extK8sConfig)
			if extObj != nil {
				extYaml, _ := yaml.Marshal(extObj.Object)
				result.ProposedEnvoyExtensionPolicyYaml = string(extYaml)
			}
		}
	}

	// AI review only when explicitly requested via flag
	if input.IncludeAIReview && s.aiService != nil && s.aiService.IsEnabled() {
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

// buildDomainBTPConfig builds a BackendTrafficPolicyConfig for a domain-level BTP targeting the Gateway
func (s *DomainService) buildDomainBTPConfig(domain *models.Domain, btpConfig *models.BackendTrafficPolicyConfig) *BackendTrafficPolicyConfig {
	if btpConfig == nil || btpConfig.IsEmpty() {
		return nil
	}

	config := &BackendTrafficPolicyConfig{
		Name:      domain.K8sGatewayName + "-btp",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   "",
		DomainID:  domain.ID.String(),
		TargetRef: BackendTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  "Gateway",
			Name:  domain.K8sGatewayName,
		},
	}

	// Add compression configuration
	if len(btpConfig.Compression) > 0 {
		config.Compression = make([]CompressionPolicyConfig, 0, len(btpConfig.Compression))
		for _, comp := range btpConfig.Compression {
			policyComp := CompressionPolicyConfig{
				Type: string(comp.Type),
			}
			switch comp.Type {
			case models.CompressionTypeGzip:
				policyComp.Gzip = &GzipPolicyConfig{}
			case models.CompressionTypeBrotli:
				policyComp.Brotli = &BrotliPolicyConfig{}
			case models.CompressionTypeZstd:
				policyComp.Zstd = &ZstdPolicyConfig{}
			}
			config.Compression = append(config.Compression, policyComp)
		}
	}

	if btpConfig.Retry != nil {
		config.Retry = mapRetryConfigToPolicy(btpConfig.Retry)
	}
	if btpConfig.LoadBalancer != nil {
		config.LoadBalancer = mapLoadBalancerConfigToPolicy(btpConfig.LoadBalancer)
	}
	if btpConfig.CircuitBreaker != nil {
		config.CircuitBreaker = mapCircuitBreakerConfigToPolicy(btpConfig.CircuitBreaker)
	}
	if btpConfig.RequestBuffer != nil {
		config.RequestBuffer = &RequestBufferPolicyConfig{
			Limit: btpConfig.RequestBuffer.Limit,
		}
	}
	if len(btpConfig.ResponseOverride) > 0 {
		config.ResponseOverride = mapResponseOverrideToPolicy(btpConfig.ResponseOverride)
	}
	if btpConfig.Timeout != nil {
		config.Timeout = mapTimeoutConfigToPolicy(btpConfig.Timeout)
	}

	return config
}

// buildDomainExtensionPolicyConfig builds an EnvoyExtensionPolicyK8sConfig for a domain-level extension policy targeting the Gateway
func (s *DomainService) buildDomainExtensionPolicyConfig(domain *models.Domain, extConfig *models.EnvoyExtensionPolicyConfig) *EnvoyExtensionPolicyK8sConfig {
	if extConfig == nil || extConfig.IsEmpty() {
		return nil
	}

	config := &EnvoyExtensionPolicyK8sConfig{
		Name:      domain.K8sGatewayName + "-eep",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   "",
		DomainID:  domain.ID.String(),
		TargetRef: EnvoyExtensionPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  "Gateway",
			Name:  domain.K8sGatewayName,
		},
	}

	// Add Lua extension configuration
	if extConfig.Lua != nil {
		luaConfig := LuaExtensionPolicyConfig{
			Type:   extConfig.Lua.Type,
			Inline: extConfig.Lua.Inline,
		}
		if extConfig.Lua.ValueRef != nil {
			luaConfig.ValueRef = &ValueRefPolicyConfig{
				Group:     extConfig.Lua.ValueRef.Group,
				Kind:      extConfig.Lua.ValueRef.Kind,
				Name:      extConfig.Lua.ValueRef.Name,
				Namespace: extConfig.Lua.ValueRef.Namespace,
			}
		}
		config.Lua = append(config.Lua, luaConfig)
	}

	// Add Wasm extension configuration
	if extConfig.Wasm != nil {
		wasmConfig := WasmExtensionPolicyConfig{
			Name:   extConfig.Wasm.Name,
			RootID: extConfig.Wasm.RootID,
			Code: WasmCodeSourcePolicyConfig{
				Type: extConfig.Wasm.Code.Type,
			},
			Config: extConfig.Wasm.Config,
		}
		if extConfig.Wasm.Code.HTTP != nil {
			wasmConfig.Code.HTTP = &WasmHTTPSourcePolicyConfig{
				URL:    extConfig.Wasm.Code.HTTP.URL,
				SHA256: extConfig.Wasm.Code.HTTP.SHA256,
			}
		}
		if extConfig.Wasm.Code.Image != nil {
			imageConfig := &WasmImageSourcePolicyConfig{
				URL:    extConfig.Wasm.Code.Image.URL,
				SHA256: extConfig.Wasm.Code.Image.SHA256,
			}
			if extConfig.Wasm.Code.Image.PullSecret != nil {
				imageConfig.PullSecret = &ValueRefPolicyConfig{
					Group:     extConfig.Wasm.Code.Image.PullSecret.Group,
					Kind:      extConfig.Wasm.Code.Image.PullSecret.Kind,
					Name:      extConfig.Wasm.Code.Image.PullSecret.Name,
					Namespace: extConfig.Wasm.Code.Image.PullSecret.Namespace,
				}
			}
			wasmConfig.Code.Image = imageConfig
		}
		config.Wasm = append(config.Wasm, wasmConfig)
	}

	// Add ExtProc extension configuration
	if extConfig.ExtProc != nil {
		cfg := ExtProcPolicyConfig{
			BackendRef: ExtProcBackendRefPolicyConfig{
				Name:      extConfig.ExtProc.BackendRef.Name,
				Namespace: extConfig.ExtProc.BackendRef.Namespace,
				Port:      extConfig.ExtProc.BackendRef.Port,
			},
			FailOpen: extConfig.ExtProc.FailOpen,
		}
		if extConfig.ExtProc.ProcessingMode != nil {
			cfg.ProcessingMode = &ExtProcProcessingModeConfig{}
			if extConfig.ExtProc.ProcessingMode.Request != nil {
				cfg.ProcessingMode.Request = &ExtProcBodyModeConfig{Body: extConfig.ExtProc.ProcessingMode.Request.Body}
			}
			if extConfig.ExtProc.ProcessingMode.Response != nil {
				cfg.ProcessingMode.Response = &ExtProcBodyModeConfig{Body: extConfig.ExtProc.ProcessingMode.Response.Body}
			}
		}
		config.ExtProc = append(config.ExtProc, cfg)
	}

	return config
}

// applyDomainBackendTrafficPolicy saves and deploys or deletes the domain-level BTP
func (s *DomainService) applyDomainBackendTrafficPolicy(ctx context.Context, domain *models.Domain, btpConfig *models.BackendTrafficPolicyConfig) error {
	btpName := domain.K8sGatewayName + "-btp"

	if btpConfig == nil || btpConfig.IsEmpty() {
		// Delete from DB and K8s
		if s.btpRepo != nil {
			_ = s.btpRepo.DeleteByDomainID(domain.ID)
		}
		_ = s.k8sService.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName)
		return nil
	}

	// Save to DB
	if s.btpRepo != nil {
		policy := &models.BackendTrafficPolicy{
			DomainID:  &domain.ID,
			ProjectID: domain.ProjectID,
			Config:    *btpConfig,
		}
		if err := s.btpRepo.Upsert(policy); err != nil {
			return fmt.Errorf("failed to save domain BTP: %w", err)
		}
	}

	// Build K8s config and deploy
	k8sConfig := s.buildDomainBTPConfig(domain, btpConfig)
	if k8sConfig == nil {
		return nil
	}
	if err := s.k8sService.UpdateBackendTrafficPolicy(ctx, domain.ProjectID, k8sConfig); err != nil {
		return fmt.Errorf("failed to apply domain BackendTrafficPolicy to Kubernetes: %w", err)
	}
	return nil
}

// applyDomainEnvoyExtensionPolicy saves and deploys or deletes the domain-level extension policy
func (s *DomainService) applyDomainEnvoyExtensionPolicy(ctx context.Context, domain *models.Domain, extConfig *models.EnvoyExtensionPolicyConfig) error {
	eepName := domain.K8sGatewayName + "-eep"
	extProcBackendName := GenerateExtProcBackendNameForDomain(domain.K8sGatewayName)

	if extConfig == nil || extConfig.IsEmpty() {
		// Delete from DB and K8s
		if s.extPolicyRepo != nil {
			_ = s.extPolicyRepo.DeleteByDomainID(domain.ID)
		}
		_ = s.k8sService.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
		_ = s.k8sService.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName)
		return nil
	}

	// Save to DB
	if s.extPolicyRepo != nil {
		policy := &models.EnvoyExtensionPolicy{
			DomainID:  &domain.ID,
			ProjectID: domain.ProjectID,
			Config:    *extConfig,
		}
		if err := s.extPolicyRepo.Upsert(policy); err != nil {
			return fmt.Errorf("failed to save domain extension policy: %w", err)
		}
	}

	// Handle ext-proc Backend CRD lifecycle
	if extConfig.ExtProc != nil {
		backendConfig := &ExtProcBackendConfig{
			Name:      extProcBackendName,
			Namespace: domain.Namespace,
			GatewayID: domain.ID.String(),
			RouteID:   "",
			DomainID:  domain.ID.String(),
			Service: ExtProcBackendRefPolicyConfig{
				Name:      extConfig.ExtProc.BackendRef.Name,
				Namespace: extConfig.ExtProc.BackendRef.Namespace,
				Port:      extConfig.ExtProc.BackendRef.Port,
			},
		}
		backend := BuildExtProcBackend(backendConfig)
		if backend != nil {
			if err := s.k8sService.UpdateBackendUnstructured(ctx, domain.ProjectID, backend); err != nil {
				return fmt.Errorf("failed to create/update domain ext-proc Backend: %w", err)
			}
		}
	} else {
		_ = s.k8sService.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
	}

	// Build K8s config and deploy
	k8sConfig := s.buildDomainExtensionPolicyConfig(domain, extConfig)
	if k8sConfig == nil {
		return nil
	}
	extPolicy := BuildEnvoyExtensionPolicy(k8sConfig)
	if extPolicy == nil {
		return nil
	}
	if err := s.k8sService.UpdateEnvoyExtensionPolicy(ctx, domain.ProjectID, extPolicy); err != nil {
		return fmt.Errorf("failed to apply domain EnvoyExtensionPolicy to Kubernetes: %w", err)
	}
	return nil
}

// generateK8sName generates a valid Kubernetes resource name from a hostname
func generateK8sName(hostname string) string {
	// Replace dots with dashes and ensure it's lowercase
	name := strings.ReplaceAll(hostname, ".", "-")
	name = strings.ToLower(name)

	// Ensure it starts with a letter
	if len(name) > 0 && (name[0] < 'a' || name[0] > 'z') {
		name = "gw-" + name
	}

	// Truncate to 63 characters (K8s name limit)
	if len(name) > 63 {
		name = name[:63]
	}

	return name
}

// =============================================================================
// mTLS CA Management
// =============================================================================

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
	if s.settingsRepo == nil {
		return nil, errors.New("domain settings repository not configured")
	}

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
	err = s.k8sService.CreateOrUpdateSecret(ctx, domain.ProjectID, domain.Namespace, secretName, map[string][]byte{
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
	if s.settingsRepo == nil {
		return nil, errors.New("domain settings repository not configured")
	}

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
	if err := s.k8sService.DeleteSecret(ctx, domain.ProjectID, domain.Namespace, removedCA.SecretName); err != nil {
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

// ListTLSSecrets lists TLS secrets available for the given project and namespace
func (s *DomainService) ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) (*ListTLSSecretsResponse, error) {
	// Default to fastgateway-system
	if namespace == "" {
		namespace = FastGatewayNamespace
	}

	// Validate namespace is either fastgateway-system or managed by the project
	if namespace != FastGatewayNamespace {
		if s.projectNamespaceRepo == nil {
			return nil, errors.New("namespace management not configured")
		}
		managed, err := s.projectNamespaceRepo.ExistsByProjectAndNamespace(projectID, namespace)
		if err != nil {
			return nil, fmt.Errorf("failed to check namespace: %w", err)
		}
		if !managed {
			return nil, fmt.Errorf("namespace '%s' is not managed by this project", namespace)
		}
	}

	// Query K8s for TLS secrets
	secrets, err := s.k8sService.ListTLSSecrets(ctx, projectID, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to list TLS secrets: %w", err)
	}

	// Build available namespaces list — only those with the tls_secret capability.
	availableNamespaces := []string{FastGatewayNamespace}
	if s.projectNamespaceRepo != nil {
		projectNamespaces, err := s.projectNamespaceRepo.ListByCapability(projectID, models.NamespaceCapabilityTLSSecret)
		if err == nil {
			for _, ns := range projectNamespaces {
				if ns.Namespace != FastGatewayNamespace {
					availableNamespaces = append(availableNamespaces, ns.Namespace)
				}
			}
		}
	}

	return &ListTLSSecretsResponse{
		Namespace:           namespace,
		Secrets:             secrets,
		AvailableNamespaces: availableNamespaces,
	}, nil
}

// ListAvailableNamespaces returns namespaces eligible for domain deployment.
// Sourced from project_namespaces with the deploy_gateway capability, plus
// fastgateway-system always.
func (s *DomainService) ListAvailableNamespaces(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	out := []string{FastGatewayNamespace}
	if s.projectNamespaceRepo == nil {
		return out, nil
	}
	rows, err := s.projectNamespaceRepo.ListByCapability(projectID, models.NamespaceCapabilityDeployGateway)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{FastGatewayNamespace: true}
	for _, r := range rows {
		if !seen[r.Namespace] {
			out = append(out, r.Namespace)
			seen[r.Namespace] = true
		}
	}
	return out, nil
}

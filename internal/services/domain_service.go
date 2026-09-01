// DomainService: construction and domain CRUD.
//
// This file holds the service struct, its dependency set and constructor, the
// create/read/update/delete lifecycle of a domain, and the namespace/TLS-secret
// discovery endpoints that back the create form. Settings, mTLS CA management,
// cluster application of the domain-level policies, and YAML/preview generation
// live in domain_settings.go, domain_mtls.go, domain_deploy.go and
// domain_yaml.go respectively. Phase 2F Task 3.

package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// DomainService handles domain business logic
type DomainService struct {
	domainRepo           repository.DomainRepositoryInterface
	projectRepo          repository.ProjectRepositoryInterface
	domainTemplateRepo   repository.DomainTemplateRepositoryInterface
	k8sGateways          GatewayApplier
	k8sSecrets           Secrets
	k8sBackends          BackendApplier
	k8sPolicies          TrafficPolicyApplier
	k8sRefGrants         ReferenceGrantResetter
	settingsRepo         repository.DomainSettingsRepositoryInterface
	clientAttachmentRepo repository.ClientAttachmentRepositoryInterface
	btpRepo              repository.BackendTrafficPolicyRepositoryInterface
	extPolicyRepo        repository.EnvoyExtensionPolicyRepositoryInterface
	dtService            DomainTemplateLookup
	aiService            AIReviewer
	projectNamespaceRepo repository.ProjectNamespaceRepositoryInterface
}

// DomainTemplateLookup is the only thing DomainService needs from
// DomainTemplateService: resolving a domain's template so its annotations can
// be copied onto the generated Gateway. *DomainTemplateService satisfies it
// structurally.
type DomainTemplateLookup interface {
	GetByID(id uuid.UUID) (*models.DomainTemplate, error)
}

// AIReviewer is the only thing DomainService needs from AIService.
//
// It is a required dependency, not an optional one: NewAIService always
// returns a usable service, and "no AI provider configured" is reported by
// IsEnabled returning false -- which every call site already checks. The
// pre-2E "s.aiService != nil" half of those conditions was a wiring guard,
// never a feature flag. *AIService satisfies this interface structurally.
type AIReviewer interface {
	IsEnabled() bool
	Review(ctx context.Context, userID uuid.UUID, req ai.ReviewRequest) (*ai.ReviewResult, error)
}

// DomainServiceDeps carries everything DomainService needs. Every field is
// required unless its comment says otherwise: before Phase 2E five of these
// arrived through setters, and eight nil-guards existed across this file to
// tolerate the ones that might not have been called.
type DomainServiceDeps struct {
	DomainRepo         repository.DomainRepositoryInterface
	ProjectRepo        repository.ProjectRepositoryInterface
	DomainTemplateRepo repository.DomainTemplateRepositoryInterface

	// The five cluster roles this service uses. Before Phase 2E Task 7 they
	// were a single K8sService field naming all 58 methods of the cluster
	// client, of which DomainService calls fifteen.
	K8sGateways          GatewayApplier
	K8sSecrets           Secrets
	K8sBackends          BackendApplier
	K8sPolicies          TrafficPolicyApplier
	K8sRefGrants         ReferenceGrantResetter
	SettingsRepo         repository.DomainSettingsRepositoryInterface
	ClientAttachmentRepo repository.ClientAttachmentRepositoryInterface
	BtpRepo              repository.BackendTrafficPolicyRepositoryInterface
	ExtPolicyRepo        repository.EnvoyExtensionPolicyRepositoryInterface
	ProjectNamespaceRepo repository.ProjectNamespaceRepositoryInterface

	// DtService resolves a domain's template. See DomainTemplateLookup.
	DtService DomainTemplateLookup

	// AiService performs optional AI review of a proposed change. Required:
	// whether AI is actually available is AIReviewer.IsEnabled's answer, not
	// this field's nil-ness. See AIReviewer.
	AiService AIReviewer
}

// NewDomainService builds a fully-wired DomainService. It panics if a
// required dependency is missing: before Phase 2E these arrived through
// setters after construction, so a forgotten wiring line degraded silently
// at runtime instead of failing at start-up. Master design section 6.6.
func NewDomainService(deps DomainServiceDeps) *DomainService {
	var missing []string
	if deps.DomainRepo == nil {
		missing = append(missing, "DomainRepo")
	}
	if deps.ProjectRepo == nil {
		missing = append(missing, "ProjectRepo")
	}
	if deps.DomainTemplateRepo == nil {
		missing = append(missing, "DomainTemplateRepo")
	}
	if deps.K8sGateways == nil {
		missing = append(missing, "K8sGateways")
	}
	if deps.K8sSecrets == nil {
		missing = append(missing, "K8sSecrets")
	}
	if deps.K8sBackends == nil {
		missing = append(missing, "K8sBackends")
	}
	if deps.K8sPolicies == nil {
		missing = append(missing, "K8sPolicies")
	}
	if deps.K8sRefGrants == nil {
		missing = append(missing, "K8sRefGrants")
	}
	if deps.SettingsRepo == nil {
		missing = append(missing, "SettingsRepo")
	}
	if deps.ClientAttachmentRepo == nil {
		missing = append(missing, "ClientAttachmentRepo")
	}
	if deps.BtpRepo == nil {
		missing = append(missing, "BtpRepo")
	}
	if deps.ExtPolicyRepo == nil {
		missing = append(missing, "ExtPolicyRepo")
	}
	if deps.ProjectNamespaceRepo == nil {
		missing = append(missing, "ProjectNamespaceRepo")
	}
	if deps.DtService == nil {
		missing = append(missing, "DtService")
	}
	if deps.AiService == nil {
		missing = append(missing, "AiService")
	}
	if len(missing) > 0 {
		panic("services.NewDomainService: missing required dependency: " + strings.Join(missing, ", "))
	}

	return &DomainService{
		domainRepo:           deps.DomainRepo,
		projectRepo:          deps.ProjectRepo,
		domainTemplateRepo:   deps.DomainTemplateRepo,
		k8sGateways:          deps.K8sGateways,
		k8sSecrets:           deps.K8sSecrets,
		k8sBackends:          deps.K8sBackends,
		k8sPolicies:          deps.K8sPolicies,
		k8sRefGrants:         deps.K8sRefGrants,
		settingsRepo:         deps.SettingsRepo,
		clientAttachmentRepo: deps.ClientAttachmentRepo,
		btpRepo:              deps.BtpRepo,
		extPolicyRepo:        deps.ExtPolicyRepo,
		projectNamespaceRepo: deps.ProjectNamespaceRepo,
		dtService:            deps.DtService,
		aiService:            deps.AiService,
	}
}

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
	Namespace           string                  `json:"namespace"`
	Secrets             []cluster.TLSSecretInfo `json:"secrets"`
	AvailableNamespaces []string                `json:"availableNamespaces"`
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
	if input.TLSSecretNamespace != "" && input.TLSSecretNamespace != kubernetes.FastGatewayNamespace {
		managed, err := s.projectNamespaceRepo.ExistsByProjectAndNamespace(projectID, input.TLSSecretNamespace)
		if err != nil {
			return nil, fmt.Errorf("failed to validate TLS secret namespace: %w", err)
		}
		if !managed {
			return nil, fmt.Errorf("namespace '%s' is not managed by this project. Add it in Project Settings > Namespaces", input.TLSSecretNamespace)
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
		input.Namespace = kubernetes.FastGatewayNamespace
	}
	if input.Namespace != kubernetes.FastGatewayNamespace {
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
	gatewayConfig := &kubernetes.GatewayConfig{
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

	if err := s.k8sGateways.CreateGateway(ctx, projectID, gatewayConfig); err != nil {
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
	if input.Namespace != kubernetes.FastGatewayNamespace {
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
		if input.TLSSecretNamespace != kubernetes.FastGatewayNamespace {
			managed, err := s.projectNamespaceRepo.ExistsByProjectAndNamespace(domain.ProjectID, input.TLSSecretNamespace)
			if err != nil {
				return nil, fmt.Errorf("failed to validate TLS secret namespace: %w", err)
			}
			if !managed {
				return nil, fmt.Errorf("namespace '%s' is not managed by this project. Add it in Project Settings > Namespaces", input.TLSSecretNamespace)
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
	if err := s.k8sPolicies.DeleteBackendTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, btpName); err != nil {
		log.Printf("Failed to delete domain BackendTrafficPolicy from Kubernetes: %v", err)
	}
	_ = s.btpRepo.DeleteByDomainID(id)

	// Delete domain-level EnvoyExtensionPolicy from K8s and DB
	eepName := domain.K8sGatewayName + "-eep"
	extProcBackendName := kubernetes.GenerateExtProcBackendNameForDomain(domain.K8sGatewayName)
	_ = s.k8sBackends.DeleteBackend(ctx, domain.ProjectID, domain.Namespace, extProcBackendName)
	if err := s.k8sPolicies.DeleteEnvoyExtensionPolicy(ctx, domain.ProjectID, domain.Namespace, eepName); err != nil {
		log.Printf("Failed to delete domain EnvoyExtensionPolicy from Kubernetes: %v", err)
	}
	_ = s.extPolicyRepo.DeleteByDomainID(id)

	// Delete gateway-specific resources from Kubernetes
	ctpName := domain.K8sGatewayName + "-ctp"
	if err := s.k8sGateways.DeleteClientTrafficPolicy(ctx, domain.ProjectID, domain.Namespace, ctpName); err != nil {
		log.Printf("Failed to delete ClientTrafficPolicy from Kubernetes: %v", err)
		// Continue with deletion even if K8s deletion fails
	}

	// Delete domain settings from database if exists
	_ = s.settingsRepo.DeleteByDomainID(id)

	// Delete Gateway from Kubernetes
	if err := s.k8sGateways.DeleteGateway(ctx, domain.ProjectID, domain.Namespace, domain.K8sGatewayName); err != nil {
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
	if domainNamespace != kubernetes.FastGatewayNamespace {
		s.syncReferenceGrants(projectID)
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

// ListTLSSecrets lists TLS secrets available for the given project and namespace
func (s *DomainService) ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) (*ListTLSSecretsResponse, error) {
	// Default to fastgateway-system
	if namespace == "" {
		namespace = kubernetes.FastGatewayNamespace
	}

	// Validate namespace is either fastgateway-system or managed by the project
	if namespace != kubernetes.FastGatewayNamespace {
		managed, err := s.projectNamespaceRepo.ExistsByProjectAndNamespace(projectID, namespace)
		if err != nil {
			return nil, fmt.Errorf("failed to check namespace: %w", err)
		}
		if !managed {
			return nil, fmt.Errorf("namespace '%s' is not managed by this project", namespace)
		}
	}

	// Query K8s for TLS secrets
	secrets, err := s.k8sSecrets.ListTLSSecrets(ctx, projectID, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to list TLS secrets: %w", err)
	}

	// Build available namespaces list — only those with the tls_secret capability.
	availableNamespaces := []string{kubernetes.FastGatewayNamespace}
	projectNamespaces, err := s.projectNamespaceRepo.ListByCapability(projectID, models.NamespaceCapabilityTLSSecret)
	if err == nil {
		for _, ns := range projectNamespaces {
			if ns.Namespace != kubernetes.FastGatewayNamespace {
				availableNamespaces = append(availableNamespaces, ns.Namespace)
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
	out := []string{kubernetes.FastGatewayNamespace}
	rows, err := s.projectNamespaceRepo.ListByCapability(projectID, models.NamespaceCapabilityDeployGateway)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{kubernetes.FastGatewayNamespace: true}
	for _, r := range rows {
		if !seen[r.Namespace] {
			out = append(out, r.Namespace)
			seen[r.Namespace] = true
		}
	}
	return out, nil
}

package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

// DomainTemplateService handles domain template business logic
type DomainTemplateService struct {
	dtRepo      repository.DomainTemplateRepositoryInterface
	projectRepo repository.ProjectRepositoryInterface
	domainRepo  repository.DomainRepositoryInterface
	k8sService  GatewayClassApplier
	aiService   *AIService
}

// NewDomainTemplateService creates a new domain template service
func NewDomainTemplateService(
	dtRepo repository.DomainTemplateRepositoryInterface,
	projectRepo repository.ProjectRepositoryInterface,
	domainRepo repository.DomainRepositoryInterface,
	k8sService GatewayClassApplier,
	aiService *AIService,
) *DomainTemplateService {
	return &DomainTemplateService{
		dtRepo:      dtRepo,
		projectRepo: projectRepo,
		domainRepo:  domainRepo,
		k8sService:  k8sService,
		aiService:   aiService,
	}
}

// ListDomainsByTemplateID returns the domains built from a template.
//
// Phase 2F Task 4: DomainTemplateHandler.ListDomains read this straight off
// repository.DomainRepositoryInterface. "Which domains use this template" is a
// question about the template, so the template service answers it; the
// domainRepo field exists only to serve this method.
func (s *DomainTemplateService) ListDomainsByTemplateID(templateID uuid.UUID) ([]models.Domain, error) {
	return s.domainRepo.ListByTemplateID(templateID)
}

// validateScalingConfig validates a scaling configuration
func validateScalingConfig(sc *models.ScalingConfig) error {
	if sc == nil {
		return nil
	}
	if sc.Type != "fixed" && sc.Type != "hpa" {
		return errors.New("scaling type must be 'fixed' or 'hpa'")
	}
	if sc.Type == "fixed" {
		if sc.Replicas == nil || *sc.Replicas < 1 {
			return errors.New("fixed scaling requires replicas >= 1")
		}
	}
	if sc.Type == "hpa" {
		if sc.MinReplicas == nil || *sc.MinReplicas < 1 {
			return errors.New("HPA scaling requires minReplicas >= 1")
		}
		if sc.MaxReplicas == nil || *sc.MaxReplicas < 1 {
			return errors.New("HPA scaling requires maxReplicas >= 1")
		}
		if *sc.MaxReplicas < *sc.MinReplicas {
			return errors.New("HPA maxReplicas must be >= minReplicas")
		}
	}
	return nil
}

// CreateDomainTemplateInput represents input for creating a domain template
type CreateDomainTemplateInput struct {
	Name           string `json:"name" binding:"required"`
	Description    string `json:"description"`
	ControllerName string `json:"controllerName"`
	ExposureType   string `json:"exposureType" binding:"required"`
	TLSMode        string `json:"tlsMode" binding:"required"`

	// Advanced settings
	HTTPPort              int                              `json:"httpPort"`
	HTTPSPort             int                              `json:"httpsPort"`
	TLSPolicy             string                           `json:"tlsPolicy"`
	ExternalTrafficPolicy string                           `json:"externalTrafficPolicy"`
	LoadBalancerClass     string                           `json:"loadBalancerClass"`
	Annotations           map[string]string                `json:"annotations"`
	PodAnnotations        map[string]string                `json:"podAnnotations"`
	ContainerResources    *models.ContainerResourcesConfig `json:"containerResources"`
	ScalingConfig         *models.ScalingConfig            `json:"scalingConfig"`
	MergeGateways         bool                             `json:"mergeGateways"`

	// Telemetry settings
	TelemetryAccessLog *models.TelemetryAccessLogConfig `json:"telemetryAccessLog,omitempty"`
	TelemetryTracing   *models.TelemetryTracingConfig   `json:"telemetryTracing,omitempty"`
	TelemetryMetrics   *models.TelemetryMetricsConfig   `json:"telemetryMetrics,omitempty"`

	// Pod scheduling settings
	PodPlacement       *models.PodPlacementConfig       `json:"podPlacement,omitempty"`
	PDBConfig          *models.PDBConfig                `json:"pdbConfig,omitempty"`
	DeploymentStrategy *models.DeploymentStrategyConfig `json:"deploymentStrategy,omitempty"`
}

// UpdateDomainTemplateInput represents input for updating a domain template
type UpdateDomainTemplateInput struct {
	Description           string                           `json:"description"`
	ExternalTrafficPolicy string                           `json:"externalTrafficPolicy"`
	LoadBalancerClass     string                           `json:"loadBalancerClass"`
	Annotations           map[string]string                `json:"annotations"`
	PodAnnotations        map[string]string                `json:"podAnnotations"`
	ContainerResources    *models.ContainerResourcesConfig `json:"containerResources"`
	ScalingConfig         *models.ScalingConfig            `json:"scalingConfig"`

	// Telemetry settings
	TelemetryAccessLog *models.TelemetryAccessLogConfig `json:"telemetryAccessLog,omitempty"`
	TelemetryTracing   *models.TelemetryTracingConfig   `json:"telemetryTracing,omitempty"`
	TelemetryMetrics   *models.TelemetryMetricsConfig   `json:"telemetryMetrics,omitempty"`

	// Pod scheduling settings
	PodPlacement       *models.PodPlacementConfig       `json:"podPlacement,omitempty"`
	PDBConfig          *models.PDBConfig                `json:"pdbConfig,omitempty"`
	DeploymentStrategy *models.DeploymentStrategyConfig `json:"deploymentStrategy,omitempty"`

	// Clear-flags: when true, set the corresponding stored field to NULL.
	// Frontend sends these alongside (or instead of) the value when the user
	// disables a feature, since JSON null vs absent are indistinguishable in Go.
	ClearTelemetryAccessLog bool `json:"clearTelemetryAccessLog,omitempty"`
	ClearTelemetryTracing   bool `json:"clearTelemetryTracing,omitempty"`
	ClearTelemetryMetrics   bool `json:"clearTelemetryMetrics,omitempty"`
	ClearPodPlacement       bool `json:"clearPodPlacement,omitempty"`
	ClearPDBConfig          bool `json:"clearPdbConfig,omitempty"`
	ClearDeploymentStrategy bool `json:"clearDeploymentStrategy,omitempty"`
}

// Create creates a new domain template
func (s *DomainTemplateService) Create(projectID uuid.UUID, input *CreateDomainTemplateInput, createdBy uuid.UUID) (*models.DomainTemplate, error) {
	// Validate exposure type
	exposureType := models.ExposureType(input.ExposureType)
	if exposureType != models.ExposureTypeLoadBalancer && exposureType != models.ExposureTypeClusterIP {
		return nil, errors.New("exposure type must be 'LoadBalancer' or 'ClusterIP'")
	}

	// Validate TLS mode
	tlsMode := models.TLSMode(input.TLSMode)
	if tlsMode != models.TLSModeOnly && tlsMode != models.TLSModeNone && tlsMode != models.TLSModeBoth {
		return nil, errors.New("TLS mode must be 'tls_only', 'no_tls', or 'both'")
	}

	// Set default controller name if not provided
	controllerName := input.ControllerName
	if controllerName == "" {
		controllerName = kubernetes.EnvoyGatewayControllerName
	}

	// Validate controller name (only Envoy Gateway supported initially)
	if controllerName != kubernetes.EnvoyGatewayControllerName {
		return nil, errors.New("only Envoy Gateway controller is currently supported")
	}

	// Validate name is a valid K8s name
	if !isValidK8sName(input.Name) {
		return nil, errors.New("name must be lowercase, contain only letters, numbers, and dashes, and start with a letter")
	}

	// Check if name already exists in project
	exists, err := s.dtRepo.ExistsByName(projectID, input.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("domain template name already exists in this project")
	}

	// Set default TLS policy (only relevant when TLS is enabled)
	tlsPolicy := models.TLSPolicy(input.TLSPolicy)
	if tlsPolicy == "" {
		tlsPolicy = models.TLSPolicyTerminate
	}
	if tlsMode != models.TLSModeNone {
		if tlsPolicy != models.TLSPolicyTerminate && tlsPolicy != models.TLSPolicyPassthrough {
			return nil, errors.New("TLS policy must be 'terminate' or 'passthrough'")
		}
	}

	// Validate scaling config
	if err := validateScalingConfig(input.ScalingConfig); err != nil {
		return nil, err
	}

	// Set default ports
	httpPort := input.HTTPPort
	if httpPort == 0 {
		httpPort = 80
	}
	httpsPort := input.HTTPSPort
	if httpsPort == 0 {
		httpsPort = 443
	}

	// Validate ports
	if httpPort < 1 || httpPort > 65535 {
		return nil, errors.New("HTTP port must be between 1 and 65535")
	}
	if httpsPort < 1 || httpsPort > 65535 {
		return nil, errors.New("HTTPS port must be between 1 and 65535")
	}

	// Validate external traffic policy (only for LoadBalancer)
	var externalTrafficPolicy models.ExternalTrafficPolicy
	if exposureType == models.ExposureTypeLoadBalancer && input.ExternalTrafficPolicy != "" {
		externalTrafficPolicy = models.ExternalTrafficPolicy(input.ExternalTrafficPolicy)
		if externalTrafficPolicy != models.ExternalTrafficPolicyCluster && externalTrafficPolicy != models.ExternalTrafficPolicyLocal {
			return nil, errors.New("external traffic policy must be 'Cluster' or 'Local'")
		}
	}

	ctx := context.Background()

	// Generate K8s resource names (lowercase for K8s compatibility)
	exposureTypeLower := strings.ToLower(input.ExposureType)
	k8sGatewayClassName := fmt.Sprintf("%s-%s", input.Name, exposureTypeLower)
	k8sEnvoyProxyName := fmt.Sprintf("%s-%s-config", input.Name, exposureTypeLower)

	dt := &models.DomainTemplate{
		ProjectID:             projectID,
		Name:                  input.Name,
		Description:           input.Description,
		ControllerName:        controllerName,
		ExposureType:          exposureType,
		TLSMode:               tlsMode,
		HTTPPort:              httpPort,
		HTTPSPort:             httpsPort,
		TLSPolicy:             tlsPolicy,
		ExternalTrafficPolicy: externalTrafficPolicy,
		LoadBalancerClass:     input.LoadBalancerClass,
		Annotations:           models.Annotations(input.Annotations),
		PodAnnotations:        models.Annotations(input.PodAnnotations),
		ContainerResources:    input.ContainerResources,
		ScalingConfig:         input.ScalingConfig,
		MergeGateways:         input.MergeGateways,
		Status:                models.DomainTemplateStatusPending,
		K8sGatewayClassName:   k8sGatewayClassName,
		K8sEnvoyProxyName:     k8sEnvoyProxyName,
		CreatedBy:             createdBy,
		TelemetryAccessLog:    input.TelemetryAccessLog,
		TelemetryTracing:      input.TelemetryTracing,
		TelemetryMetrics:      input.TelemetryMetrics,
		PodPlacement:          input.PodPlacement,
		PDBConfig:             input.PDBConfig,
		DeploymentStrategy:    input.DeploymentStrategy,
	}

	// Normalize and validate telemetry fields before persistence
	dt.TelemetryMetrics = NormalizeEmptyTelemetryMetrics(dt.TelemetryMetrics)
	if err := ValidateDomainTemplateTelemetry(dt); err != nil {
		return nil, err
	}

	// Normalize and validate pod-scheduling fields before persistence
	dt.PodPlacement = NormalizeEmptyPodPlacement(dt.PodPlacement)
	if err := ValidateDomainTemplatePodScheduling(dt); err != nil {
		return nil, err
	}

	if err := s.dtRepo.Create(dt); err != nil {
		return nil, err
	}

	// Create EnvoyProxy in Kubernetes — use the shared builder so all fields
	// (telemetry, pod scheduling, PDB, deployment strategy) are included.
	epConfig := s.buildEnvoyProxyConfig(dt)

	if err := s.k8sService.CreateEnvoyProxy(ctx, projectID, epConfig); err != nil {
		log.Printf("Failed to create EnvoyProxy in Kubernetes: %v", err)
		dt.Status = models.DomainTemplateStatusError
		dt.StatusMessage = fmt.Sprintf("Failed to create EnvoyProxy: %v", err)
		_ = s.dtRepo.Update(dt)
		return dt, nil
	}

	// Create GatewayClass in Kubernetes with reference to EnvoyProxy
	gcConfig := &kubernetes.GatewayClassConfig{
		Name:              k8sGatewayClassName,
		ControllerName:    controllerName,
		ParametersRefName: k8sEnvoyProxyName,
	}

	if err := s.k8sService.CreateGatewayClass(ctx, projectID, gcConfig); err != nil {
		log.Printf("Failed to create GatewayClass in Kubernetes: %v", err)
		// Clean up EnvoyProxy
		_ = s.k8sService.DeleteEnvoyProxy(ctx, projectID, kubernetes.EnvoyGatewayNamespace, k8sEnvoyProxyName)
		dt.Status = models.DomainTemplateStatusError
		dt.StatusMessage = fmt.Sprintf("Failed to create GatewayClass: %v", err)
		_ = s.dtRepo.Update(dt)
		return dt, nil
	}

	// Update status to active
	dt.Status = models.DomainTemplateStatusActive
	dt.StatusMessage = "GatewayClass and EnvoyProxy created successfully"
	_ = s.dtRepo.Update(dt)

	return dt, nil
}

// GetByID gets a domain template by ID
func (s *DomainTemplateService) GetByID(id uuid.UUID) (*models.DomainTemplate, error) {
	return s.dtRepo.GetByID(id)
}

// GetByName gets a domain template by name in a project
func (s *DomainTemplateService) GetByName(projectID uuid.UUID, name string) (*models.DomainTemplate, error) {
	return s.dtRepo.GetByName(projectID, name)
}

// ListByProjectID lists domain templates in a project
func (s *DomainTemplateService) ListByProjectID(projectID uuid.UUID, page, limit int) ([]models.DomainTemplate, int64, error) {
	return s.dtRepo.ListByProjectID(projectID, page, limit)
}

// Update updates a domain template
func (s *DomainTemplateService) Update(id uuid.UUID, input *UpdateDomainTemplateInput) (*models.DomainTemplate, error) {
	dt, err := s.dtRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if input.Description != "" {
		dt.Description = input.Description
	}

	if input.ExternalTrafficPolicy != "" {
		externalTrafficPolicy := models.ExternalTrafficPolicy(input.ExternalTrafficPolicy)
		if externalTrafficPolicy != models.ExternalTrafficPolicyCluster && externalTrafficPolicy != models.ExternalTrafficPolicyLocal {
			return nil, errors.New("external traffic policy must be 'Cluster' or 'Local'")
		}
		dt.ExternalTrafficPolicy = externalTrafficPolicy
	}

	if input.LoadBalancerClass != "" {
		dt.LoadBalancerClass = input.LoadBalancerClass
	}

	if input.Annotations != nil {
		dt.Annotations = models.Annotations(input.Annotations)
	}

	if input.PodAnnotations != nil {
		dt.PodAnnotations = models.Annotations(input.PodAnnotations)
	}

	if input.ContainerResources != nil {
		dt.ContainerResources = input.ContainerResources
	}

	if input.ScalingConfig != nil {
		if err := validateScalingConfig(input.ScalingConfig); err != nil {
			return nil, err
		}
		dt.ScalingConfig = input.ScalingConfig
	}

	if input.ClearTelemetryAccessLog {
		dt.TelemetryAccessLog = nil
	} else if input.TelemetryAccessLog != nil {
		dt.TelemetryAccessLog = input.TelemetryAccessLog
	}

	if input.ClearTelemetryTracing {
		dt.TelemetryTracing = nil
	} else if input.TelemetryTracing != nil {
		dt.TelemetryTracing = input.TelemetryTracing
	}

	if input.ClearTelemetryMetrics {
		dt.TelemetryMetrics = nil
	} else if input.TelemetryMetrics != nil {
		dt.TelemetryMetrics = input.TelemetryMetrics
	}

	if input.ClearPodPlacement {
		dt.PodPlacement = nil
	} else if input.PodPlacement != nil {
		dt.PodPlacement = input.PodPlacement
	}

	if input.ClearPDBConfig {
		dt.PDBConfig = nil
	} else if input.PDBConfig != nil {
		dt.PDBConfig = input.PDBConfig
	}

	if input.ClearDeploymentStrategy {
		dt.DeploymentStrategy = nil
	} else if input.DeploymentStrategy != nil {
		dt.DeploymentStrategy = input.DeploymentStrategy
	}

	// Normalize and validate telemetry fields before persistence
	dt.TelemetryMetrics = NormalizeEmptyTelemetryMetrics(dt.TelemetryMetrics)
	if err := ValidateDomainTemplateTelemetry(dt); err != nil {
		return nil, err
	}

	// Normalize and validate pod-scheduling fields before persistence
	dt.PodPlacement = NormalizeEmptyPodPlacement(dt.PodPlacement)
	if err := ValidateDomainTemplatePodScheduling(dt); err != nil {
		return nil, err
	}

	// Update EnvoyProxy in Kubernetes if any infra setting changed.
	// Includes telemetry + pod-scheduling fields and their clear-flags so toggling
	// them off in the UI also propagates to the cluster.
	needsK8sUpdate := input.Annotations != nil || input.PodAnnotations != nil ||
		input.ContainerResources != nil || input.ScalingConfig != nil ||
		input.ExternalTrafficPolicy != "" || input.LoadBalancerClass != "" ||
		input.TelemetryAccessLog != nil || input.TelemetryTracing != nil || input.TelemetryMetrics != nil ||
		input.PodPlacement != nil || input.PDBConfig != nil || input.DeploymentStrategy != nil ||
		input.ClearTelemetryAccessLog || input.ClearTelemetryTracing || input.ClearTelemetryMetrics ||
		input.ClearPodPlacement || input.ClearPDBConfig || input.ClearDeploymentStrategy
	if needsK8sUpdate {
		ctx := context.Background()
		epConfig := s.buildEnvoyProxyConfig(dt)
		if err := s.k8sService.UpdateEnvoyProxy(ctx, dt.ProjectID, epConfig); err != nil {
			log.Printf("Failed to update EnvoyProxy in Kubernetes: %v", err)
			// Continue with database update even if K8s update fails
		}
	}

	if err := s.dtRepo.Update(dt); err != nil {
		return nil, err
	}

	return dt, nil
}

// Delete deletes a domain template
func (s *DomainTemplateService) Delete(id uuid.UUID) error {
	dt, err := s.dtRepo.GetByID(id)
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Delete GatewayClass from Kubernetes
	if err := s.k8sService.DeleteGatewayClass(ctx, dt.ProjectID, dt.K8sGatewayClassName); err != nil {
		log.Printf("Failed to delete GatewayClass from Kubernetes: %v", err)
		// Continue with deletion even if K8s deletion fails
	}

	// Delete EnvoyProxy from Kubernetes
	if dt.K8sEnvoyProxyName != "" {
		if err := s.k8sService.DeleteEnvoyProxy(ctx, dt.ProjectID, kubernetes.EnvoyGatewayNamespace, dt.K8sEnvoyProxyName); err != nil {
			log.Printf("Failed to delete EnvoyProxy from Kubernetes: %v", err)
			// Continue with deletion even if K8s deletion fails
		}
	}

	return s.dtRepo.Delete(id)
}

// NormalizeEmptyTelemetryMetrics returns nil if cfg has all default zero values,
// otherwise returns cfg unchanged. Used to keep DB rows clean per spec
// "empty-state semantics".
func NormalizeEmptyTelemetryMetrics(cfg *models.TelemetryMetricsConfig) *models.TelemetryMetricsConfig {
	if cfg == nil {
		return nil
	}
	if cfg.Prometheus == nil &&
		!cfg.EnableVirtualHostStats &&
		!cfg.EnablePerEndpointStats &&
		len(cfg.Sinks) == 0 {
		return nil
	}
	return cfg
}

// ValidateDomainTemplateTelemetry runs all three telemetry validators against
// the domain template's stored config.
func ValidateDomainTemplateTelemetry(dt *models.DomainTemplate) error {
	if err := ValidateAccessLog(dt.TelemetryAccessLog); err != nil {
		return err
	}
	if err := ValidateTracing(dt.TelemetryTracing); err != nil {
		return err
	}
	if err := ValidateMetrics(dt.TelemetryMetrics); err != nil {
		return err
	}
	return nil
}

// NormalizeEmptyPodPlacement returns nil if cfg has all default zero values,
// otherwise returns cfg unchanged. Mirrors the spec "empty-state semantics".
func NormalizeEmptyPodPlacement(cfg *models.PodPlacementConfig) *models.PodPlacementConfig {
	if cfg == nil {
		return nil
	}
	if len(cfg.NodeSelector) == 0 &&
		len(cfg.Tolerations) == 0 &&
		len(cfg.TopologySpreadConstraints) == 0 &&
		cfg.PriorityClassName == "" {
		return nil
	}
	return cfg
}

// ValidateDomainTemplatePodScheduling runs the three pod-scheduling validators against
// the domain template's stored config.
func ValidateDomainTemplatePodScheduling(dt *models.DomainTemplate) error {
	if err := ValidatePodPlacement(dt.PodPlacement); err != nil {
		return err
	}
	if err := ValidatePDB(dt.PDBConfig); err != nil {
		return err
	}
	if err := ValidateStrategy(dt.DeploymentStrategy); err != nil {
		return err
	}
	return nil
}

// buildEnvoyProxyConfig builds an EnvoyProxyConfig from a DomainTemplate
func (s *DomainTemplateService) buildEnvoyProxyConfig(dt *models.DomainTemplate) *kubernetes.EnvoyProxyConfig {
	return &kubernetes.EnvoyProxyConfig{
		Name:                  dt.K8sEnvoyProxyName,
		Namespace:             kubernetes.EnvoyGatewayNamespace,
		ServiceType:           string(dt.ExposureType),
		Annotations:           map[string]string(dt.Annotations),
		ExternalTrafficPolicy: string(dt.ExternalTrafficPolicy),
		LoadBalancerClass:     dt.LoadBalancerClass,
		PodAnnotations:        map[string]string(dt.PodAnnotations),
		ContainerResources:    dt.ContainerResources,
		ScalingConfig:         dt.ScalingConfig,
		MergeGateways:         dt.MergeGateways,
		TelemetryAccessLog:    dt.TelemetryAccessLog,
		TelemetryTracing:      dt.TelemetryTracing,
		TelemetryMetrics:      dt.TelemetryMetrics,
		GatewayClassName:      dt.K8sGatewayClassName,
		PodPlacement:          dt.PodPlacement,
		PDBConfig:             dt.PDBConfig,
		DeploymentStrategy:    dt.DeploymentStrategy,
	}
}

// DomainTemplateManifests contains the generated K8s YAML for a domain template
type DomainTemplateManifests struct {
	GatewayClassYaml string `json:"gatewayClassYaml"`
	EnvoyProxyYaml   string `json:"envoyProxyYaml"`
}

// GetManifests returns the generated K8s manifests for a domain template
func (s *DomainTemplateService) GetManifests(id uuid.UUID) (*DomainTemplateManifests, error) {
	dt, err := s.dtRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	gcConfig := &kubernetes.GatewayClassConfig{
		Name:              dt.K8sGatewayClassName,
		ControllerName:    dt.ControllerName,
		ParametersRefName: dt.K8sEnvoyProxyName,
	}
	gcObj := kubernetes.BuildGatewayClassObject(gcConfig)
	gcYaml, err := yaml.Marshal(gcObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GatewayClass: %w", err)
	}

	epConfig := s.buildEnvoyProxyConfig(dt)
	epObj := kubernetes.BuildEnvoyProxyObject(epConfig)
	epYaml, err := yaml.Marshal(epObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal EnvoyProxy: %w", err)
	}

	return &DomainTemplateManifests{
		GatewayClassYaml: string(gcYaml),
		EnvoyProxyYaml:   string(epYaml),
	}, nil
}

// DomainTemplatePreviewResult contains the diff and AI review for proposed changes
type DomainTemplatePreviewResult struct {
	CurrentEnvoyProxyYaml  string           `json:"currentEnvoyProxyYaml"`
	ProposedEnvoyProxyYaml string           `json:"proposedEnvoyProxyYaml"`
	AIReview               *ai.ReviewResult `json:"aiReview,omitempty"`
}

// PreviewChangesOptions contains options for the preview changes request
type PreviewChangesOptions struct {
	IncludeAIReview   bool   `json:"includeAIReview"`
	ChangeDescription string `json:"changeDescription"`
}

// PreviewChanges returns a diff and optional AI review for proposed domain template changes
func (s *DomainTemplateService) PreviewChanges(id uuid.UUID, input *UpdateDomainTemplateInput, userID uuid.UUID, opts *PreviewChangesOptions) (*DomainTemplatePreviewResult, error) {
	dt, err := s.dtRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Generate current YAML
	currentConfig := s.buildEnvoyProxyConfig(dt)
	currentObj := kubernetes.BuildEnvoyProxyObject(currentConfig)
	currentYaml, err := yaml.Marshal(currentObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal current EnvoyProxy: %w", err)
	}

	// Apply proposed changes to a copy
	proposed := *dt
	if input.Description != "" {
		proposed.Description = input.Description
	}
	if input.ExternalTrafficPolicy != "" {
		proposed.ExternalTrafficPolicy = models.ExternalTrafficPolicy(input.ExternalTrafficPolicy)
	}
	if input.LoadBalancerClass != "" {
		proposed.LoadBalancerClass = input.LoadBalancerClass
	}
	if input.Annotations != nil {
		proposed.Annotations = models.Annotations(input.Annotations)
	}
	if input.PodAnnotations != nil {
		proposed.PodAnnotations = models.Annotations(input.PodAnnotations)
	}
	if input.ContainerResources != nil {
		proposed.ContainerResources = input.ContainerResources
	}
	if input.ScalingConfig != nil {
		proposed.ScalingConfig = input.ScalingConfig
	}

	// Generate proposed YAML
	proposedConfig := s.buildEnvoyProxyConfig(&proposed)
	proposedObj := kubernetes.BuildEnvoyProxyObject(proposedConfig)
	proposedYaml, err := yaml.Marshal(proposedObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal proposed EnvoyProxy: %w", err)
	}

	result := &DomainTemplatePreviewResult{
		CurrentEnvoyProxyYaml:  string(currentYaml),
		ProposedEnvoyProxyYaml: string(proposedYaml),
	}

	// AI review only when explicitly requested.
	//
	// aiService is a genuinely OPTIONAL dependency here, unlike every other
	// service in this package: NewDomainTemplateService is still a positional
	// constructor with no nil checks at all (domain_template_service.go:27),
	// so a caller really can pass nil. Phase 2E Task 9 kept this guard for
	// that reason and deleted the equivalent ones in domain_service.go, whose
	// AiService is a checked DomainServiceDeps field.
	if s.aiService != nil && opts != nil && opts.IncludeAIReview {
		ctx := context.Background()
		description := "Domain template infrastructure settings update"
		if opts.ChangeDescription != "" {
			description = opts.ChangeDescription
		}
		reviewReq := ai.ReviewRequest{
			Action:      "update",
			Description: description,
			CurrentYaml: &ai.YamlSet{
				Backend: string(currentYaml),
			},
			ProposedYaml: &ai.YamlSet{
				Backend: string(proposedYaml),
			},
		}
		reviewResult, err := s.aiService.Review(ctx, userID, reviewReq)
		if err != nil {
			log.Printf("AI review failed for domain template preview: %v", err)
		} else {
			result.AIReview = reviewResult
		}
	}

	return result, nil
}

// DomainTemplateCreatePreviewResult contains the generated manifests for a new domain template
type DomainTemplateCreatePreviewResult struct {
	GatewayClassYaml string           `json:"gatewayClassYaml"`
	EnvoyProxyYaml   string           `json:"envoyProxyYaml"`
	GatewayYaml      string           `json:"gatewayYaml"`
	AIReview         *ai.ReviewResult `json:"aiReview,omitempty"`
}

// PreviewCreate generates K8s manifests for a proposed domain template without persisting
func (s *DomainTemplateService) PreviewCreate(projectID uuid.UUID, input *CreateDomainTemplateInput, userID uuid.UUID, opts *PreviewChangesOptions) (*DomainTemplateCreatePreviewResult, error) {
	// Validate exposure type
	exposureType := models.ExposureType(input.ExposureType)
	if exposureType != models.ExposureTypeLoadBalancer && exposureType != models.ExposureTypeClusterIP {
		return nil, errors.New("exposure type must be 'LoadBalancer' or 'ClusterIP'")
	}

	// Validate TLS mode
	tlsMode := models.TLSMode(input.TLSMode)
	if tlsMode != models.TLSModeOnly && tlsMode != models.TLSModeNone && tlsMode != models.TLSModeBoth {
		return nil, errors.New("TLS mode must be 'tls_only', 'no_tls', or 'both'")
	}

	// Set default controller name
	controllerName := input.ControllerName
	if controllerName == "" {
		controllerName = kubernetes.EnvoyGatewayControllerName
	}
	if controllerName != kubernetes.EnvoyGatewayControllerName {
		return nil, errors.New("only Envoy Gateway controller is currently supported")
	}

	// Validate name
	if !isValidK8sName(input.Name) {
		return nil, errors.New("name must be lowercase, contain only letters, numbers, and dashes, and start with a letter")
	}

	// Validate scaling config
	if err := validateScalingConfig(input.ScalingConfig); err != nil {
		return nil, err
	}

	// Set default ports
	httpPort := input.HTTPPort
	if httpPort == 0 {
		httpPort = 80
	}
	httpsPort := input.HTTPSPort
	if httpsPort == 0 {
		httpsPort = 443
	}
	if httpPort < 1 || httpPort > 65535 {
		return nil, errors.New("HTTP port must be between 1 and 65535")
	}
	if httpsPort < 1 || httpsPort > 65535 {
		return nil, errors.New("HTTPS port must be between 1 and 65535")
	}

	// Validate TLS policy
	tlsPolicy := models.TLSPolicy(input.TLSPolicy)
	if tlsPolicy == "" {
		tlsPolicy = models.TLSPolicyTerminate
	}
	if tlsMode != models.TLSModeNone {
		if tlsPolicy != models.TLSPolicyTerminate && tlsPolicy != models.TLSPolicyPassthrough {
			return nil, errors.New("TLS policy must be 'terminate' or 'passthrough'")
		}
	}

	// Validate external traffic policy
	var externalTrafficPolicy models.ExternalTrafficPolicy
	if exposureType == models.ExposureTypeLoadBalancer && input.ExternalTrafficPolicy != "" {
		externalTrafficPolicy = models.ExternalTrafficPolicy(input.ExternalTrafficPolicy)
		if externalTrafficPolicy != models.ExternalTrafficPolicyCluster && externalTrafficPolicy != models.ExternalTrafficPolicyLocal {
			return nil, errors.New("external traffic policy must be 'Cluster' or 'Local'")
		}
	}

	// Generate K8s resource names (same logic as Create)
	exposureTypeLower := strings.ToLower(input.ExposureType)
	k8sGatewayClassName := fmt.Sprintf("%s-%s", input.Name, exposureTypeLower)
	k8sEnvoyProxyName := fmt.Sprintf("%s-%s-config", input.Name, exposureTypeLower)

	// Build GatewayClass manifest
	gcConfig := &kubernetes.GatewayClassConfig{
		Name:              k8sGatewayClassName,
		ControllerName:    controllerName,
		ParametersRefName: k8sEnvoyProxyName,
	}
	gcObj := kubernetes.BuildGatewayClassObject(gcConfig)
	gcYaml, err := yaml.Marshal(gcObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GatewayClass: %w", err)
	}

	// Build EnvoyProxy manifest. Normalize the optional blocks so empty payloads
	// don't render meaningless YAML (matches what Create persists).
	normalizedMetrics := NormalizeEmptyTelemetryMetrics(input.TelemetryMetrics)
	normalizedPodPlacement := NormalizeEmptyPodPlacement(input.PodPlacement)
	serviceType := string(exposureType)
	epConfig := &kubernetes.EnvoyProxyConfig{
		Name:                  k8sEnvoyProxyName,
		Namespace:             kubernetes.EnvoyGatewayNamespace,
		ServiceType:           serviceType,
		Annotations:           input.Annotations,
		ExternalTrafficPolicy: string(externalTrafficPolicy),
		LoadBalancerClass:     input.LoadBalancerClass,
		PodAnnotations:        input.PodAnnotations,
		ContainerResources:    input.ContainerResources,
		ScalingConfig:         input.ScalingConfig,
		MergeGateways:         input.MergeGateways,
		// Telemetry — render spec.telemetry on EnvoyProxy CRD
		TelemetryAccessLog: input.TelemetryAccessLog,
		TelemetryTracing:   input.TelemetryTracing,
		TelemetryMetrics:   normalizedMetrics,
		// Pod scheduling + lifecycle — render envoyDeployment.{pod,strategy} and envoyPDB.
		// GatewayClassName is required so topology-spread labelSelector auto-fill works.
		GatewayClassName:   k8sGatewayClassName,
		PodPlacement:       normalizedPodPlacement,
		PDBConfig:          input.PDBConfig,
		DeploymentStrategy: input.DeploymentStrategy,
	}
	epObj := kubernetes.BuildEnvoyProxyObject(epConfig)
	epYaml, err := yaml.Marshal(epObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal EnvoyProxy: %w", err)
	}

	// Build example Gateway manifest to show TLS configuration impact
	gwConfig := &kubernetes.GatewayConfig{
		Name:             "example-domain",
		Namespace:        kubernetes.EnvoyGatewayNamespace,
		GatewayClassName: k8sGatewayClassName,
		Hostname:         "example.com",
		TLSMode:          string(tlsMode),
		HTTPPort:         httpPort,
		HTTPSPort:        httpsPort,
		TLSSecretName:    "example-tls-cert",
		TLSPolicy:        string(tlsPolicy),
	}
	gwObj := kubernetes.BuildGatewayObject(gwConfig)
	gwYaml, err := yaml.Marshal(gwObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Gateway example: %w", err)
	}

	result := &DomainTemplateCreatePreviewResult{
		GatewayClassYaml: string(gcYaml),
		EnvoyProxyYaml:   string(epYaml),
		GatewayYaml:      string(gwYaml),
	}

	// Optional AI review. aiService is genuinely optional here -- see the
	// note on the matching condition in updateInfrastructureSettings above.
	if s.aiService != nil && opts != nil && opts.IncludeAIReview {
		ctx := context.Background()
		description := "New domain template infrastructure configuration"
		if opts.ChangeDescription != "" {
			description = opts.ChangeDescription
		}
		reviewReq := ai.ReviewRequest{
			Action:      "create",
			Description: description,
			ProposedYaml: &ai.YamlSet{
				Backend: string(epYaml),
			},
		}
		reviewResult, err := s.aiService.Review(ctx, userID, reviewReq)
		if err != nil {
			log.Printf("AI review failed for domain template preview-create: %v", err)
		} else {
			result.AIReview = reviewResult
		}
	}

	return result, nil
}

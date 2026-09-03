package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/templateplan"
	"github.com/google/uuid"
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
	epConfig := templateplan.BuildEnvoyProxyConfig(dt)

	if err := s.k8sService.CreateEnvoyProxy(ctx, projectID, epConfig); err != nil {
		log.Printf("Failed to create EnvoyProxy in Kubernetes: %v", err)
		dt.Status = models.DomainTemplateStatusError
		dt.StatusMessage = fmt.Sprintf("Failed to create EnvoyProxy: %v", err)
		_ = s.dtRepo.Update(dt)
		return dt, nil
	}

	// Create GatewayClass in Kubernetes with reference to EnvoyProxy
	gcConfig := templateplan.BuildGatewayClassConfig(dt)

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
		epConfig := templateplan.BuildEnvoyProxyConfig(dt)
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

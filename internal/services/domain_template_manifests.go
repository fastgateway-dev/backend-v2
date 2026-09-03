package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/domainplan"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/templateplan"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

// templateFromCreateInput projects an unsaved CreateDomainTemplateInput into
// the models.DomainTemplate shape the manifest builders consume, so preview
// and persistence share one assembler.
//
// This is where normalization now lives. Before Phase 2H,
// NormalizeEmptyTelemetryMetrics and NormalizeEmptyPodPlacement were called
// from three independent sites -- Create, Update and PreviewCreate -- which
// agreed only by coincidence. Adding a normalized field to two of the three
// would have made preview silently disagree with what deploys.
//
// httpPort, httpsPort and tlsPolicy arrive as parameters rather than being
// read off input directly -- like controllerName, exposureType and
// externalTrafficPolicy already do -- because the caller has already
// defaulted and validated them (e.g. an unset HTTPPort becomes 80, an unset
// TLSPolicy becomes "terminate"). Reading input.HTTPPort/HTTPSPort/TLSPolicy
// here instead would silently drop those defaults and produce a projected
// template that disagrees with what Create persists for the same input.
func templateFromCreateInput(
	input *CreateDomainTemplateInput,
	controllerName, k8sGatewayClassName, k8sEnvoyProxyName string,
	exposureType models.ExposureType,
	externalTrafficPolicy models.ExternalTrafficPolicy,
	httpPort, httpsPort int,
	tlsPolicy models.TLSPolicy,
) *models.DomainTemplate {
	return &models.DomainTemplate{
		Name:                  input.Name,
		Description:           input.Description,
		ControllerName:        controllerName,
		ExposureType:          exposureType,
		TLSMode:               models.TLSMode(input.TLSMode),
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
		TelemetryAccessLog:    input.TelemetryAccessLog,
		TelemetryTracing:      input.TelemetryTracing,
		TelemetryMetrics:      NormalizeEmptyTelemetryMetrics(input.TelemetryMetrics),
		PodPlacement:          NormalizeEmptyPodPlacement(input.PodPlacement),
		PDBConfig:             input.PDBConfig,
		DeploymentStrategy:    input.DeploymentStrategy,
		K8sGatewayClassName:   k8sGatewayClassName,
		K8sEnvoyProxyName:     k8sEnvoyProxyName,
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

	gcConfig := templateplan.BuildGatewayClassConfig(dt)
	gcObj := kubernetes.BuildGatewayClassObject(gcConfig)
	gcYaml, err := yaml.Marshal(gcObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GatewayClass: %w", err)
	}

	epConfig := templateplan.BuildEnvoyProxyConfig(dt)
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
	currentConfig := templateplan.BuildEnvoyProxyConfig(dt)
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
	proposedConfig := templateplan.BuildEnvoyProxyConfig(&proposed)
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

	// Project the unsaved input into the models.DomainTemplate shape both
	// manifest builders consume, so preview shares one assembler with Create,
	// Update and GetManifests.
	projected := templateFromCreateInput(
		input,
		controllerName, k8sGatewayClassName, k8sEnvoyProxyName,
		exposureType, externalTrafficPolicy,
		httpPort, httpsPort, tlsPolicy,
	)

	// Build GatewayClass manifest
	gcConfig := templateplan.BuildGatewayClassConfig(projected)
	gcObj := kubernetes.BuildGatewayClassObject(gcConfig)
	gcYaml, err := yaml.Marshal(gcObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GatewayClass: %w", err)
	}

	// Build EnvoyProxy manifest. templateFromCreateInput normalizes the
	// optional blocks so empty payloads don't render meaningless YAML
	// (matches what Create persists).
	epConfig := templateplan.BuildEnvoyProxyConfig(projected)
	epObj := kubernetes.BuildEnvoyProxyObject(epConfig)
	epYaml, err := yaml.Marshal(epObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal EnvoyProxy: %w", err)
	}

	// Build example Gateway manifest to show TLS configuration impact.
	//
	// The example exists to show operators the impact of their TLS settings.
	// Building it with the same assembler real Gateways use means the
	// example cannot drift from what actually deploys.
	exampleDomain := &models.Domain{
		K8sGatewayName:  "example-domain",
		Namespace:       kubernetes.EnvoyGatewayNamespace,
		K8sGatewayClass: k8sGatewayClassName,
		Hostname:        "example.com",
		TLSMode:         string(tlsMode),
		HTTPPort:        httpPort,
		HTTPSPort:       httpsPort,
		TLSSecretName:   "example-tls-cert",
		TLSPolicy:       tlsPolicy,
	}
	gwConfig := domainplan.BuildGatewayConfig(exampleDomain, nil)
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

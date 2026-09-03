package templateplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// BuildEnvoyProxyConfig builds the EnvoyProxy config for a domain template.
// Before Phase 2H this was DomainTemplateService.buildEnvoyProxyConfig, a
// private method called from Create, Update, GetManifests, PreviewChanges
// (twice) and PreviewCreate (via templateFromCreateInput's projection).
func BuildEnvoyProxyConfig(dt *models.DomainTemplate) *kubernetes.EnvoyProxyConfig {
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

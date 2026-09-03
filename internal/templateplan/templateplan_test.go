package templateplan

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/stretchr/testify/require"
)

// CHARACTERIZATION. The expected literal below is copied VERBATIM from
// internal/services/domain_template_service.go:568 as it stood at 826340b.
// If this assertion fails, the extraction changed behaviour.
func TestBuildGatewayClassConfig_MatchesOriginalLiteral(t *testing.T) {
	dt := &models.DomainTemplate{
		K8sGatewayClassName: "example-public",
		ControllerName:      "gateway.envoyproxy.io/gatewayclass-controller",
		K8sEnvoyProxyName:   "example-public-config",
	}

	want := &kubernetes.GatewayClassConfig{
		Name:              dt.K8sGatewayClassName,
		ControllerName:    dt.ControllerName,
		ParametersRefName: dt.K8sEnvoyProxyName,
	}

	require.Equal(t, want, BuildGatewayClassConfig(dt))
}

// CHARACTERIZATION. The expected literal is copied VERBATIM from
// DomainTemplateService.buildEnvoyProxyConfig (domain_template_service.go:534)
// as it stood at 826340b.
func TestBuildEnvoyProxyConfig_MatchesOriginalLiteral(t *testing.T) {
	dt := &models.DomainTemplate{
		K8sEnvoyProxyName:     "example-public-config",
		K8sGatewayClassName:   "example-public",
		ExposureType:          models.ExposureTypeLoadBalancer,
		ExternalTrafficPolicy: models.ExternalTrafficPolicyLocal,
		LoadBalancerClass:     "service.k8s.aws/nlb",
		Annotations:           models.Annotations{"a": "1"},
		PodAnnotations:        models.Annotations{"b": "2"},
		MergeGateways:         true,
	}

	want := &kubernetes.EnvoyProxyConfig{
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

	require.Equal(t, want, BuildEnvoyProxyConfig(dt))
}

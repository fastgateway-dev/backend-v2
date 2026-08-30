package kubernetes

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// EnvoyGatewayControllerName is the controller name for Envoy Gateway
const EnvoyGatewayControllerName = "gateway.envoyproxy.io/gatewayclass-controller"

// EnvoyGatewayNamespace is the namespace where EnvoyProxy configs are created
const EnvoyGatewayNamespace = "envoy-gateway-system"

// GatewayClassConfig represents GatewayClass configuration
type GatewayClassConfig struct {
	Name              string
	ControllerName    string
	ParametersRefName string // Reference to EnvoyProxy config
}

// EnvoyProxyConfig represents EnvoyProxy configuration
type EnvoyProxyConfig struct {
	Name                  string
	Namespace             string
	ServiceType           string // LoadBalancer or ClusterIP
	Annotations           map[string]string
	ExternalTrafficPolicy string                           // Cluster or Local (only for LoadBalancer)
	LoadBalancerClass     string                           // optional (only for LoadBalancer)
	PodAnnotations        map[string]string                // envoyDeployment.pod.annotations
	ContainerResources    *models.ContainerResourcesConfig // envoyDeployment.container.resources
	ScalingConfig         *models.ScalingConfig            // envoyDeployment.replicas or envoyHpa
	MergeGateways         bool                             // spec.mergeGateways

	// Telemetry — see spec.telemetry on EnvoyProxy CRD.
	TelemetryAccessLog *models.TelemetryAccessLogConfig
	TelemetryTracing   *models.TelemetryTracingConfig
	TelemetryMetrics   *models.TelemetryMetricsConfig

	// GatewayClassName is the name of the GatewayClass that points at this EnvoyProxy
	// via parametersRef. Used by BuildPodPlacement to auto-fill the topology-spread
	// labelSelector to match data-plane pod labels EG applies.
	GatewayClassName string

	// PodPlacement, PDBConfig, DeploymentStrategy — see spec.provider.kubernetes on EnvoyProxy CRD.
	PodPlacement       *models.PodPlacementConfig
	PDBConfig          *models.PDBConfig
	DeploymentStrategy *models.DeploymentStrategyConfig
}

// BuildGatewayClassObject builds a GatewayClass unstructured object from the given config.
func BuildGatewayClassObject(config *GatewayClassConfig) *unstructured.Unstructured {
	spec := map[string]interface{}{
		"controllerName": config.ControllerName,
	}

	// Add parametersRef if EnvoyProxy name is provided
	if config.ParametersRefName != "" {
		spec["parametersRef"] = map[string]interface{}{
			"group":     "gateway.envoyproxy.io",
			"kind":      "EnvoyProxy",
			"name":      config.ParametersRefName,
			"namespace": EnvoyGatewayNamespace,
		}
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "GatewayClass",
			"metadata": map[string]interface{}{
				"name": config.Name,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
				},
			},
			"spec": spec,
		},
	}
}

// BuildEnvoyProxyObject builds an EnvoyProxy unstructured object from the given config.
func BuildEnvoyProxyObject(config *EnvoyProxyConfig) *unstructured.Unstructured {
	// Build kubernetes provider spec
	envoyService := map[string]interface{}{
		"type": config.ServiceType,
	}

	// Add annotations if provided
	if len(config.Annotations) > 0 {
		annotations := make(map[string]interface{})
		for k, v := range config.Annotations {
			annotations[k] = v
		}
		envoyService["annotations"] = annotations
	}

	// Add externalTrafficPolicy for LoadBalancer (if specified)
	if config.ServiceType == "LoadBalancer" && config.ExternalTrafficPolicy != "" {
		envoyService["externalTrafficPolicy"] = config.ExternalTrafficPolicy
	}

	// Add loadBalancerClass for LoadBalancer (if specified)
	if config.ServiceType == "LoadBalancer" && config.LoadBalancerClass != "" {
		envoyService["loadBalancerClass"] = config.LoadBalancerClass
	}

	// Build envoyDeployment spec if any deployment fields are set
	var envoyDeployment map[string]interface{}
	hasDeploymentConfig := len(config.PodAnnotations) > 0 || config.ContainerResources != nil ||
		(config.ScalingConfig != nil && config.ScalingConfig.Type == "fixed") ||
		config.PodPlacement != nil || config.DeploymentStrategy != nil

	if hasDeploymentConfig {
		envoyDeployment = make(map[string]interface{})

		podSubMap := map[string]interface{}{}
		if len(config.PodAnnotations) > 0 {
			podAnnotations := make(map[string]interface{})
			for k, v := range config.PodAnnotations {
				podAnnotations[k] = v
			}
			podSubMap["annotations"] = podAnnotations
		}
		if pp := BuildPodPlacement(config.PodPlacement, config.GatewayClassName); pp != nil {
			for k, v := range pp {
				podSubMap[k] = v
			}
		}
		if len(podSubMap) > 0 {
			envoyDeployment["pod"] = podSubMap
		}

		if config.ContainerResources != nil {
			resources := make(map[string]interface{})
			if config.ContainerResources.Requests != nil {
				requests := make(map[string]interface{})
				if config.ContainerResources.Requests.CPU != "" {
					requests["cpu"] = config.ContainerResources.Requests.CPU
				}
				if config.ContainerResources.Requests.Memory != "" {
					requests["memory"] = config.ContainerResources.Requests.Memory
				}
				if len(requests) > 0 {
					resources["requests"] = requests
				}
			}
			if config.ContainerResources.Limits != nil {
				limits := make(map[string]interface{})
				if config.ContainerResources.Limits.CPU != "" {
					limits["cpu"] = config.ContainerResources.Limits.CPU
				}
				if config.ContainerResources.Limits.Memory != "" {
					limits["memory"] = config.ContainerResources.Limits.Memory
				}
				if len(limits) > 0 {
					resources["limits"] = limits
				}
			}
			if len(resources) > 0 {
				envoyDeployment["container"] = map[string]interface{}{
					"resources": resources,
				}
			}
		}

		if config.ScalingConfig != nil && config.ScalingConfig.Type == "fixed" && config.ScalingConfig.Replicas != nil {
			envoyDeployment["replicas"] = *config.ScalingConfig.Replicas
		}

		if strat := BuildStrategy(config.DeploymentStrategy); strat != nil {
			envoyDeployment["strategy"] = strat
		}
	}

	// Build envoyHpa spec
	var envoyHpa map[string]interface{}
	if config.ScalingConfig != nil && config.ScalingConfig.Type == "hpa" {
		envoyHpa = make(map[string]interface{})
		if config.ScalingConfig.MinReplicas != nil {
			envoyHpa["minReplicas"] = *config.ScalingConfig.MinReplicas
		}
		if config.ScalingConfig.MaxReplicas != nil {
			envoyHpa["maxReplicas"] = *config.ScalingConfig.MaxReplicas
		}
	}

	// Build kubernetes provider spec
	k8sSpec := map[string]interface{}{
		"envoyService": envoyService,
	}
	if envoyDeployment != nil {
		k8sSpec["envoyDeployment"] = envoyDeployment
	}
	if envoyHpa != nil {
		k8sSpec["envoyHpa"] = envoyHpa
	}
	if pdb := BuildPDB(config.PDBConfig); pdb != nil {
		k8sSpec["envoyPDB"] = pdb
	}

	spec := map[string]interface{}{
		"provider": map[string]interface{}{
			"type":       "Kubernetes",
			"kubernetes": k8sSpec,
		},
	}

	if config.MergeGateways {
		spec["mergeGateways"] = true
	}

	telemetry := map[string]interface{}{}
	if al := BuildAccessLog(config.TelemetryAccessLog); al != nil {
		telemetry["accessLog"] = al
	}
	if tr := BuildTracing(config.TelemetryTracing); tr != nil {
		telemetry["tracing"] = tr
	}
	if me := BuildMetrics(config.TelemetryMetrics); me != nil {
		telemetry["metrics"] = me
	}
	if len(telemetry) > 0 {
		spec["telemetry"] = telemetry
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "EnvoyProxy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
				},
			},
			"spec": spec,
		},
	}
}

package templateplan

import "github.com/fastgateway-dev/backend-v2/internal/models"

// templateManifestFixture is one domain-template manifest input, snapshotted
// through the builder it names. Build closes over its own inputs so each
// fixture is a self-contained description of one branch through one builder.
type templateManifestFixture struct {
	Name  string
	Build func() any
}

// fixtureTemplate is the shared domain template for every fixture. Callers
// mutate the returned value to build variants -- each call returns a fresh
// copy, so mutating one fixture never bleeds into another.
func fixtureTemplate() *models.DomainTemplate {
	return &models.DomainTemplate{
		Name:                "example-public",
		ControllerName:      "gateway.envoyproxy.io/gatewayclass-controller",
		ExposureType:        models.ExposureTypeClusterIP,
		K8sGatewayClassName: "example-public",
		K8sEnvoyProxyName:   "example-public-config",
	}
}

func i32Ptr(i int32) *int32 { return &i }

// ─── BuildGatewayClassConfig fixtures ───────────────────────────────────────

func gatewayClassFixtures() []templateManifestFixture {
	return []templateManifestFixture{
		// GatewayClassConfig alone: only the three fields it ever maps.
		{
			Name: "gatewayclass-bare",
			Build: func() any {
				return BuildGatewayClassConfig(fixtureTemplate())
			},
		},
	}
}

// ─── BuildEnvoyProxyConfig fixtures ─────────────────────────────────────────

func envoyProxyFixtures() []templateManifestFixture {
	return []templateManifestFixture{
		// The control case: nothing but the identity fields every domain
		// template carries. Every zero-valued mapping is visible here.
		{
			Name: "envoyproxy-bare",
			Build: func() any {
				return BuildEnvoyProxyConfig(fixtureTemplate())
			},
		},
		// LoadBalancer exposure with ExternalTrafficPolicy: Local, plus a
		// LoadBalancerClass and both annotation maps -- the fields that only
		// mean something in LoadBalancer mode.
		{
			Name: "envoyproxy-loadbalancer-local",
			Build: func() any {
				dt := fixtureTemplate()
				dt.ExposureType = models.ExposureTypeLoadBalancer
				dt.ExternalTrafficPolicy = models.ExternalTrafficPolicyLocal
				dt.LoadBalancerClass = "service.k8s.aws/nlb"
				dt.Annotations = models.Annotations{"service.beta.kubernetes.io/aws-load-balancer-type": "nlb"}
				dt.PodAnnotations = models.Annotations{"prometheus.io/scrape": "true"}
				return BuildEnvoyProxyConfig(dt)
			},
		},
		// ScalingConfig set (HPA form).
		{
			Name: "envoyproxy-scaling-config",
			Build: func() any {
				dt := fixtureTemplate()
				dt.ScalingConfig = &models.ScalingConfig{
					Type:        "hpa",
					MinReplicas: i32Ptr(2),
					MaxReplicas: i32Ptr(10),
				}
				return BuildEnvoyProxyConfig(dt)
			},
		},
		// TelemetryAccessLog + TelemetryTracing + TelemetryMetrics all set at
		// once, to pin the whole telemetry block passing through untouched.
		{
			Name: "envoyproxy-telemetry",
			Build: func() any {
				dt := fixtureTemplate()
				dt.TelemetryAccessLog = &models.TelemetryAccessLogConfig{
					Format: models.TelemetryAccessLogFormat{Type: "text", Text: "%START_TIME%"},
					Sink: models.TelemetryAccessLogSink{
						Type: "file",
						File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"},
					},
				}
				dt.TelemetryTracing = &models.TelemetryTracingConfig{
					SamplingRate: 10.0,
					Provider: models.TelemetryServiceRef{
						Namespace: "observability",
						Service:   "otel-collector",
						Port:      4317,
					},
				}
				dt.TelemetryMetrics = &models.TelemetryMetricsConfig{
					Prometheus:             &models.TelemetryPrometheusConfig{Disable: false},
					EnableVirtualHostStats: true,
					EnablePerEndpointStats: true,
				}
				return BuildEnvoyProxyConfig(dt)
			},
		},
		// PodPlacement + PDBConfig + DeploymentStrategy all set at once, to pin
		// the whole pod-scheduling block passing through untouched.
		{
			Name: "envoyproxy-pod-scheduling",
			Build: func() any {
				dt := fixtureTemplate()
				dt.PodPlacement = &models.PodPlacementConfig{
					NodeSelector:      map[string]string{"kubernetes.io/os": "linux"},
					PriorityClassName: "system-cluster-critical",
					Tolerations: []models.TolerationConfig{
						{Key: "dedicated", Operator: "Equal", Value: "gateway", Effect: "NoSchedule"},
					},
				}
				dt.PDBConfig = &models.PDBConfig{Kind: "minAvailable", Amount: "1"}
				dt.DeploymentStrategy = &models.DeploymentStrategyConfig{
					Type: "RollingUpdate",
					RollingUpdate: &models.RollingUpdateConfig{
						MaxSurge:       "1",
						MaxUnavailable: "0",
					},
				}
				return BuildEnvoyProxyConfig(dt)
			},
		},
		// MergeGateways: true, on its own.
		{
			Name: "envoyproxy-merge-gateways",
			Build: func() any {
				dt := fixtureTemplate()
				dt.MergeGateways = true
				return BuildEnvoyProxyConfig(dt)
			},
		},
		// Every field BuildEnvoyProxyConfig maps, set to a distinct non-zero
		// value at once. If a future edit drops one of the seventeen mappings
		// this golden is the one that catches it.
		{
			Name: "envoyproxy-all-mapped-fields",
			Build: func() any {
				dt := fixtureTemplate()
				dt.ExposureType = models.ExposureTypeLoadBalancer
				dt.ExternalTrafficPolicy = models.ExternalTrafficPolicyLocal
				dt.LoadBalancerClass = "service.k8s.aws/nlb"
				dt.Annotations = models.Annotations{"a": "1"}
				dt.PodAnnotations = models.Annotations{"b": "2"}
				dt.ContainerResources = &models.ContainerResourcesConfig{
					Requests: &models.ResourceValues{CPU: "100m", Memory: "128Mi"},
					Limits:   &models.ResourceValues{CPU: "500m", Memory: "512Mi"},
				}
				dt.ScalingConfig = &models.ScalingConfig{Type: "fixed", Replicas: i32Ptr(3)}
				dt.MergeGateways = true
				dt.TelemetryAccessLog = &models.TelemetryAccessLogConfig{
					Format: models.TelemetryAccessLogFormat{Type: "json", JSON: map[string]string{"level": "info"}},
					Sink:   models.TelemetryAccessLogSink{Type: "file", File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"}},
				}
				dt.TelemetryTracing = &models.TelemetryTracingConfig{
					SamplingRate: 5.0,
					Provider:     models.TelemetryServiceRef{Namespace: "observability", Service: "otel-collector", Port: 4317},
				}
				dt.TelemetryMetrics = &models.TelemetryMetricsConfig{EnableVirtualHostStats: true}
				dt.PodPlacement = &models.PodPlacementConfig{PriorityClassName: "system-cluster-critical"}
				dt.PDBConfig = &models.PDBConfig{Kind: "minAvailable", Amount: "1"}
				dt.DeploymentStrategy = &models.DeploymentStrategyConfig{Type: "Recreate"}
				return BuildEnvoyProxyConfig(dt)
			},
		},
	}
}

// templateFixtures returns every golden fixture across both builders.
func templateFixtures() []templateManifestFixture {
	var fixtures []templateManifestFixture
	fixtures = append(fixtures, gatewayClassFixtures()...)
	fixtures = append(fixtures, envoyProxyFixtures()...)
	return fixtures
}

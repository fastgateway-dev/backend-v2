package domainplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
)

// BuildBackendTrafficPolicyConfig builds a kubernetes.BackendTrafficPolicyConfig for a domain-level BTP targeting the Gateway
func BuildBackendTrafficPolicyConfig(domain *models.Domain, btpConfig *models.BackendTrafficPolicyConfig) *kubernetes.BackendTrafficPolicyConfig {
	if btpConfig == nil || btpConfig.IsEmpty() {
		return nil
	}

	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      domain.K8sGatewayName + "-btp",
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		RouteID:   "",
		DomainID:  domain.ID.String(),
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  "Gateway",
			Name:  domain.K8sGatewayName,
		},
	}

	// Add compression configuration
	if len(btpConfig.Compression) > 0 {
		config.Compression = make([]kubernetes.CompressionPolicyConfig, 0, len(btpConfig.Compression))
		for _, comp := range btpConfig.Compression {
			policyComp := kubernetes.CompressionPolicyConfig{
				Type: string(comp.Type),
			}
			switch comp.Type {
			case models.CompressionTypeGzip:
				policyComp.Gzip = &kubernetes.GzipPolicyConfig{}
			case models.CompressionTypeBrotli:
				policyComp.Brotli = &kubernetes.BrotliPolicyConfig{}
			case models.CompressionTypeZstd:
				policyComp.Zstd = &kubernetes.ZstdPolicyConfig{}
			}
			config.Compression = append(config.Compression, policyComp)
		}
	}

	if btpConfig.Retry != nil {
		config.Retry = routeplan.MapRetryConfigToPolicy(btpConfig.Retry)
	}
	if btpConfig.LoadBalancer != nil {
		config.LoadBalancer = routeplan.MapLoadBalancerConfigToPolicy(btpConfig.LoadBalancer)
	}
	if btpConfig.CircuitBreaker != nil {
		config.CircuitBreaker = routeplan.MapCircuitBreakerConfigToPolicy(btpConfig.CircuitBreaker)
	}
	if btpConfig.RequestBuffer != nil {
		config.RequestBuffer = &kubernetes.RequestBufferPolicyConfig{
			Limit: btpConfig.RequestBuffer.Limit,
		}
	}
	if len(btpConfig.ResponseOverride) > 0 {
		config.ResponseOverride = routeplan.MapResponseOverrideToPolicy(btpConfig.ResponseOverride)
	}
	if btpConfig.Timeout != nil {
		config.Timeout = routeplan.MapTimeoutConfigToPolicy(btpConfig.Timeout)
	}

	return config
}

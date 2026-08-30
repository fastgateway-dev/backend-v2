package routeplan

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"sigs.k8s.io/yaml"
)

// buildBackendTrafficPolicyConfigFromInput is the single unified assembler
// behind every BackendTrafficPolicyConfig construction site (Task 10 collapses
// what used to be five independently-written copies of this same field
// mapping into this one function). Callers resolve everything site-specific
// before calling in:
//   - routeK8sName is the TargetRef/Name value. It is route.K8sRouteName for
//     the base-route sites, but a synthetic per-client name for the
//     per-client sites (they target a different Kubernetes object).
//   - RateLimit on the input must already reflect any per-client override --
//     this function applies no precedence logic of its own, it only copies
//     the ten fields across.
func buildBackendTrafficPolicyConfigFromInput(routeK8sName string, protocol models.RouteProtocol, routeID, namespace, gatewayID string, input *BackendTrafficPolicyInput) *kubernetes.BackendTrafficPolicyConfig {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      kubernetes.BackendTrafficPolicyName(routeK8sName),
		Namespace: namespace,
		GatewayID: gatewayID,
		RouteID:   routeID,
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  GetRouteKind(protocol),
			Name:  routeK8sName,
		},
	}

	// Add compression configuration
	if len(input.Compression) > 0 {
		config.Compression = make([]kubernetes.CompressionPolicyConfig, 0, len(input.Compression))
		for _, comp := range input.Compression {
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

	// Add retry configuration
	if input.Retry != nil {
		config.Retry = MapRetryConfigToPolicy(input.Retry)
	}

	// Add load balancer configuration
	if input.LoadBalancer != nil {
		config.LoadBalancer = MapLoadBalancerConfigToPolicy(input.LoadBalancer)
	}

	// Add circuit breaker configuration
	if input.CircuitBreaker != nil {
		config.CircuitBreaker = MapCircuitBreakerConfigToPolicy(input.CircuitBreaker)
	}

	// Add health check configuration
	if input.HealthCheck != nil {
		config.HealthCheck = MapHealthCheckConfigToPolicy(input.HealthCheck)
	}

	// Add fault injection configuration
	if input.FaultInjection != nil {
		config.FaultInjection = MapFaultInjectionConfigToPolicy(input.FaultInjection)
	}

	// Add rate limit configuration
	if input.RateLimit != nil {
		config.RateLimit = MapRateLimitConfigToPolicy(input.RateLimit)
	}

	// Add request buffer configuration
	if input.RequestBuffer != nil {
		config.RequestBuffer = &kubernetes.RequestBufferPolicyConfig{
			Limit: input.RequestBuffer.Limit,
		}
	}

	// Add response override configuration
	if len(input.ResponseOverride) > 0 {
		config.ResponseOverride = MapResponseOverrideToPolicy(input.ResponseOverride)
	}

	// Add timeout configuration
	if input.Timeout != nil {
		config.Timeout = MapTimeoutConfigToPolicy(input.Timeout)
	}

	return config
}

// BuildBackendTrafficPolicyConfig is the deploy-path assembler (formerly a
// (*RouteService) method -- it never used its receiver, so Task 10 makes it
// a plain function). Deploy is authoritative where the four pre-collapse
// bodies disagreed.
func BuildBackendTrafficPolicyConfig(route *models.Route, domain *models.Domain, policy *models.BackendTrafficPolicy) *kubernetes.BackendTrafficPolicyConfig {
	if policy == nil {
		return nil
	}

	// Check if any feature is configured
	if policy.Config.IsEmpty() {
		return nil
	}

	return buildBackendTrafficPolicyConfigFromInput(route.K8sRouteName, route.Protocol, route.ID.String(), domain.Namespace, domain.ID.String(), MapBackendTrafficPolicyConfigToInput(&policy.Config))
}

// GenerateAPIKeyBackendTrafficPolicyYAML generates BTP YAML for a per-client HTTPRoute
// GenerateAPIKeyBackendTrafficPolicyYAML is the per-client pre-persist YAML
// site -- not one of the four named in the original collapse plan, but
// discovered during Task 10 implementation: it duplicates
// BuildAPIKeyBackendTrafficPolicyConfig's exact same three divergences
// (existence gate, per-client naming, rate-limit override precedence) in
// YAML-returning form, so it reuses the same unified assembler. Note its one
// genuine (and preserved) quirk versus the other three YAML-returning sites:
// on a marshal error it returns "" rather than a "# Error ..." comment.
func GenerateAPIKeyBackendTrafficPolicyYAML(route *models.Route, domain *models.Domain, btpPolicy *models.BackendTrafficPolicy, routeName string, rateLimitConfig *models.RateLimitConfig) string {
	hasBasePolicy := btpPolicy != nil && !btpPolicy.Config.IsEmpty()
	hasRateLimit := rateLimitConfig != nil

	if !hasBasePolicy && !hasRateLimit {
		return ""
	}

	input := &BackendTrafficPolicyInput{}
	if hasBasePolicy {
		input = MapBackendTrafficPolicyConfigToInput(&btpPolicy.Config)
	}
	// Override with per-client rate limit from attachment if present
	if hasRateLimit {
		input.RateLimit = rateLimitConfig
	}

	btpConfig := buildBackendTrafficPolicyConfigFromInput(routeName, route.Protocol, route.ID.String(), domain.Namespace, domain.ID.String(), input)

	btp := kubernetes.BuildBackendTrafficPolicy(btpConfig)
	if btp == nil {
		return ""
	}

	yamlBytes, err := yaml.Marshal(btp.Object)
	if err != nil {
		return ""
	}

	return string(yamlBytes)
}

// GenerateBackendTrafficPolicyYAML generates BackendTrafficPolicy YAML for compression, retry and other features
func GenerateBackendTrafficPolicyYAML(route *models.Route, domain *models.Domain, btpInput *BackendTrafficPolicyInput) string {
	if btpInput == nil || !btpInput.HasContent() {
		return ""
	}

	config := buildBackendTrafficPolicyConfigFromInput(route.K8sRouteName, route.Protocol, route.ID.String(), domain.Namespace, domain.ID.String(), btpInput)

	// Build the BackendTrafficPolicy object
	backendTrafficPolicy := kubernetes.BuildBackendTrafficPolicy(config)
	if backendTrafficPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(backendTrafficPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating BackendTrafficPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// GenerateBackendTrafficPolicyYAMLFromDB generates BackendTrafficPolicy YAML from database model
func GenerateBackendTrafficPolicyYAMLFromDB(route *models.Route, domain *models.Domain, policy *models.BackendTrafficPolicy) string {
	config := BuildBackendTrafficPolicyConfig(route, domain, policy)
	if config == nil {
		return ""
	}

	// Build the BackendTrafficPolicy object
	backendTrafficPolicy := kubernetes.BuildBackendTrafficPolicy(config)
	if backendTrafficPolicy == nil {
		return ""
	}

	// Marshal to YAML
	yamlBytes, err := yaml.Marshal(backendTrafficPolicy.Object)
	if err != nil {
		return fmt.Sprintf("# Error generating BackendTrafficPolicy YAML: %v", err)
	}

	return string(yamlBytes)
}

// BuildAPIKeyBackendTrafficPolicyConfig is the per-client deploy-path
// assembler (formerly a (*RouteService) method -- it never used its
// receiver). Task 10 collapses its field-copy logic into the shared
// buildBackendTrafficPolicyConfigFromInput, but preserves its three
// deliberate divergences from the base-route assemblers exactly:
//  1. Existence gate: a config is produced when client.RateLimitConfig is
//     set even with no base policy at all (a client may have only a
//     rate-limit attachment).
//  2. Naming: both Name and TargetRef.Name use the synthetic per-client name
//     (route.K8sRouteName + "-ak-" + first 8 chars of the client ID), because
//     this targets a different Kubernetes object than the base-route sites.
//  3. Rate-limit precedence: the base policy's rate limit is applied first,
//     then unconditionally overwritten by client.RateLimitConfig when present.
func BuildAPIKeyBackendTrafficPolicyConfig(route *models.Route, domain *models.Domain, client ClientAuthCategory, policy *models.BackendTrafficPolicy) *kubernetes.BackendTrafficPolicyConfig {
	hasBasePolicy := policy != nil && !policy.Config.IsEmpty()
	hasRateLimit := client.RateLimitConfig != nil

	if !hasBasePolicy && !hasRateLimit {
		return nil
	}

	routeName := route.K8sRouteName + "-ak-" + client.ClientID.String()[:8]

	input := &BackendTrafficPolicyInput{}
	if hasBasePolicy {
		input = MapBackendTrafficPolicyConfigToInput(&policy.Config)
	}
	// Rate limit from attachment overrides base policy rate limit for per-client mode.
	if hasRateLimit {
		input.RateLimit = client.RateLimitConfig
	}

	return buildBackendTrafficPolicyConfigFromInput(routeName, route.Protocol, route.ID.String(), domain.Namespace, domain.ID.String(), input)
}

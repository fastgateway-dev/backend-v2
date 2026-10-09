package routeplan

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"sigs.k8s.io/yaml"
)

// buildBackendTrafficPolicyConfigFromInput is the single unified assembler
// behind every BackendTrafficPolicyConfig construction site, replacing what
// used to be five independently-written copies of this same field mapping.
// Having one copy of the mapping is what keeps the deploy, preview and
// per-client YAML paths from drifting from each other again. Callers resolve
// everything site-specific before calling in:
//   - routeK8sName is the TargetRef/Name value. It is route.K8sRouteName for
//     the base-route sites, but a synthetic per-client name for the
//     per-client sites (they target a different Kubernetes object).
//   - RateLimit on the input must already reflect any per-client override --
//     this function applies no precedence logic of its own, it only copies
//     the ten fields across.
func buildBackendTrafficPolicyConfigFromInput(routeK8sName string, protocol models.RouteProtocol, routeID, namespace, gatewayID string, input *BackendTrafficPolicyInput) *kubernetes.BackendTrafficPolicyConfig {
	// L4 routes get only the subset of the policy that applies to their
	// transport, so an HTTP-only field that slipped past write-time validation
	// can never reach the emitted CRD.
	input = gateBackendTrafficPolicyForProtocol(protocol, input)

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

// gateBackendTrafficPolicyForProtocol returns the part of input that is valid
// for the route protocol. HTTP and gRPC pass through untouched. For TCP it
// keeps the connection-oriented circuit-breaker counters (maxConnections,
// maxRequestsPerConnection), the load balancer, passive and active-TCP health
// checks and the TCP timeouts. For UDP it keeps the load balancer only
// (circuit breaking, health checks and timeouts do not apply to datagrams).
// Consistent-hash load balancing on L4 can only hash the source IP, so any
// other hash source is rewritten to SourceIP. The input is never mutated.
func gateBackendTrafficPolicyForProtocol(protocol models.RouteProtocol, input *BackendTrafficPolicyInput) *BackendTrafficPolicyInput {
	if input == nil || (protocol != models.RouteProtocolTCP && protocol != models.RouteProtocolUDP) {
		return input
	}

	gated := &BackendTrafficPolicyInput{LoadBalancer: l4LoadBalancer(input.LoadBalancer)}
	if protocol == models.RouteProtocolUDP {
		return gated
	}

	if cb := input.CircuitBreaker; cb != nil {
		if kept := (models.CircuitBreakerConfig{MaxConnections: cb.MaxConnections, MaxRequestsPerConnection: cb.MaxRequestsPerConnection}); kept.MaxConnections != nil || kept.MaxRequestsPerConnection != nil {
			gated.CircuitBreaker = &kept
		}
	}

	if hc := input.HealthCheck; hc != nil {
		kept := models.HealthCheckConfig{Passive: hc.Passive, PanicThreshold: hc.PanicThreshold}
		if hc.Active != nil && hc.Active.Type == "TCP" {
			kept.Active = hc.Active
		}
		if kept.Active != nil || kept.Passive != nil || kept.PanicThreshold != nil {
			gated.HealthCheck = &kept
		}
	}

	if t := input.Timeout; t != nil && t.TCP != nil {
		gated.Timeout = &models.BTPTimeoutConfig{TCP: t.TCP}
	}

	return gated
}

// l4LoadBalancer copies lb, rewriting a header/cookie consistent hash (which
// has no meaning without HTTP) to a source-IP hash.
func l4LoadBalancer(lb *models.LoadBalancerConfig) *models.LoadBalancerConfig {
	if lb == nil {
		return nil
	}
	out := *lb
	if lb.Type == models.LoadBalancerTypeConsistentHash {
		out.ConsistentHash = &models.ConsistentHashConfig{Type: models.ConsistentHashTypeSourceIP}
	}
	return &out
}

// BuildBackendTrafficPolicyConfig is the deploy-path assembler (formerly a
// (*RouteService) method; it never used its receiver, so it is now a plain
// function). Deploy is authoritative where the four pre-collapse bodies
// disagreed.
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
// site. It duplicates BuildAPIKeyBackendTrafficPolicyConfig's exact same
// three divergences from the base-route assemblers (existence gate,
// per-client naming, rate-limit override precedence) in YAML-returning form,
// so it reuses the same unified assembler rather than re-deriving them. Note
// its one genuine (and preserved) quirk versus the other three
// YAML-returning sites: on a marshal error it returns "" rather than a
// "# Error ..." comment.
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
// assembler (formerly a (*RouteService) method; it never used its
// receiver). Its field-copy logic now goes through the shared
// buildBackendTrafficPolicyConfigFromInput, but it preserves its three
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

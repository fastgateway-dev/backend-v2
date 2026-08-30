package kubernetes

// BackendTrafficPolicyConfig represents Envoy Gateway BackendTrafficPolicy configuration
type BackendTrafficPolicyConfig struct {
	Name             string                         // Name of the BackendTrafficPolicy (usually {routeName}-btp)
	Namespace        string                         // Namespace where the BackendTrafficPolicy will be created
	GatewayID        string                         // Domain/Gateway ID for labeling
	RouteID          string                         // Route ID for labeling
	DomainID         string                         // Domain ID for labeling (domain-level policies)
	TargetRef        BackendTrafficPolicyTargetRef  // Reference to the target HTTPRoute
	Compression      []CompressionPolicyConfig      // Compression configuration
	Retry            *RetryPolicyConfig             // Retry configuration
	LoadBalancer     *LoadBalancerPolicyConfig      // Load balancer configuration
	CircuitBreaker   *CircuitBreakerPolicyConfig    // Circuit breaker configuration
	HealthCheck      *HealthCheckPolicyConfig       // Health check configuration
	FaultInjection   *FaultInjectionPolicyConfig    // Fault injection configuration
	RateLimit        *RateLimitPolicyConfig         // Rate limit configuration
	RequestBuffer    *RequestBufferPolicyConfig     // Request buffering configuration
	ResponseOverride []ResponseOverridePolicyConfig // Response override configuration
	Timeout          *BTPTimeoutPolicyConfig        // Timeout configuration
}

// LoadBalancerPolicyConfig represents load balancer configuration for BackendTrafficPolicy
type LoadBalancerPolicyConfig struct {
	Type           string
	ConsistentHash *ConsistentHashPolicyConfig
}

// ConsistentHashPolicyConfig represents consistent hash configuration
type ConsistentHashPolicyConfig struct {
	Type   string
	Header *ConsistentHashHeaderPolicyConfig
	Cookie *ConsistentHashCookiePolicyConfig
}

// ConsistentHashHeaderPolicyConfig represents header-based consistent hashing
type ConsistentHashHeaderPolicyConfig struct {
	Name string
}

// ConsistentHashCookiePolicyConfig represents cookie-based consistent hashing
type ConsistentHashCookiePolicyConfig struct {
	Name       string
	TTL        *string
	Attributes map[string]string
}

// CircuitBreakerPolicyConfig represents circuit breaker configuration for BackendTrafficPolicy
type CircuitBreakerPolicyConfig struct {
	MaxConnections           *int64
	MaxPendingRequests       *int64
	MaxParallelRequests      *int64
	MaxParallelRetries       *int64
	MaxRequestsPerConnection *int64
}

// HealthCheckPolicyConfig represents health check configuration for BackendTrafficPolicy
type HealthCheckPolicyConfig struct {
	Active         *ActiveHealthCheckPolicyConfig
	Passive        *PassiveHealthCheckPolicyConfig
	PanicThreshold *uint32
}

// ActiveHealthCheckPolicyConfig represents active health check configuration
type ActiveHealthCheckPolicyConfig struct {
	Timeout            *string
	Interval           *string
	UnhealthyThreshold *uint32
	HealthyThreshold   *uint32
	Type               string
	HTTP               *HTTPActiveHealthCheckPolicyConfig
	TCP                *TCPActiveHealthCheckPolicyConfig
	GRPC               *GRPCActiveHealthCheckPolicyConfig
}

// HTTPActiveHealthCheckPolicyConfig represents HTTP active health checker
type HTTPActiveHealthCheckPolicyConfig struct {
	Path             string
	Method           *string
	ExpectedStatuses []int
}

// TCPActiveHealthCheckPolicyConfig represents TCP active health checker
type TCPActiveHealthCheckPolicyConfig struct {
	SendText    *string
	ReceiveText *string
}

// GRPCActiveHealthCheckPolicyConfig represents gRPC active health checker
type GRPCActiveHealthCheckPolicyConfig struct {
	Service *string
}

// PassiveHealthCheckPolicyConfig represents passive health check (outlier detection)
type PassiveHealthCheckPolicyConfig struct {
	ConsecutiveGatewayErrors       *uint32
	Consecutive5xxErrors           *uint32
	ConsecutiveLocalOriginFailures *uint32
	Interval                       *string
	BaseEjectionTime               *string
	MaxEjectionPercent             *int32
	SplitExternalLocalOriginErrors *bool
}

// FaultInjectionPolicyConfig represents fault injection configuration for BackendTrafficPolicy
type FaultInjectionPolicyConfig struct {
	Delay *FaultInjectionDelayPolicyConfig
	Abort *FaultInjectionAbortPolicyConfig
}

// FaultInjectionDelayPolicyConfig represents delay fault injection
type FaultInjectionDelayPolicyConfig struct {
	FixedDelay string
	Percentage *float32
}

// FaultInjectionAbortPolicyConfig represents abort fault injection
type FaultInjectionAbortPolicyConfig struct {
	HTTPStatus *int
	GRPCStatus *int
	Percentage *float32
}

// RateLimitPolicyConfig represents rate limit configuration for BackendTrafficPolicy
type RateLimitPolicyConfig struct {
	Global *GlobalRateLimitPolicyConfig
}

// GlobalRateLimitPolicyConfig represents global rate limit policy
type GlobalRateLimitPolicyConfig struct {
	Rules []RateLimitRulePolicyConfig
}

// RateLimitRulePolicyConfig represents a single rate limit rule
type RateLimitRulePolicyConfig struct {
	Limit           RateLimitValuePolicyConfig
	ClientSelectors []RateLimitSelectorPolicyConfig
}

// RateLimitValuePolicyConfig represents rate limit value
type RateLimitValuePolicyConfig struct {
	Requests int
	Unit     string
}

// RateLimitSelectorPolicyConfig represents client selector for rate limiting
type RateLimitSelectorPolicyConfig struct {
	Headers    []RateLimitHeaderMatchPolicyConfig
	SourceCIDR *RateLimitSourceCIDRPolicyConfig
	Path       *RateLimitPathMatchPolicyConfig
	Methods    []string
}

// RateLimitHeaderMatchPolicyConfig represents header match for rate limiting
type RateLimitHeaderMatchPolicyConfig struct {
	Name   string
	Value  string
	Type   string
	Invert bool
}

// RateLimitSourceCIDRPolicyConfig represents source CIDR match
type RateLimitSourceCIDRPolicyConfig struct {
	Value string
	Type  string
}

// RateLimitPathMatchPolicyConfig represents path match for rate limiting
type RateLimitPathMatchPolicyConfig struct {
	Value string
	Type  string
}

// RequestBufferPolicyConfig represents request buffering configuration
type RequestBufferPolicyConfig struct {
	Limit string
}

// BTPTimeoutPolicyConfig represents timeout configuration for BackendTrafficPolicy
type BTPTimeoutPolicyConfig struct {
	TCP  *BTPTCPTimeoutPolicyConfig
	HTTP *BTPHTTPTimeoutPolicyConfig
}

// BTPTCPTimeoutPolicyConfig represents TCP-level timeout settings for BackendTrafficPolicy
type BTPTCPTimeoutPolicyConfig struct {
	ConnectTimeout string
}

// BTPHTTPTimeoutPolicyConfig represents HTTP-level timeout settings for BackendTrafficPolicy
type BTPHTTPTimeoutPolicyConfig struct {
	RequestTimeout        string
	ConnectionIdleTimeout string
	MaxConnectionDuration string
	MaxStreamDuration     string
}

// ResponseOverridePolicyConfig represents response override configuration
type ResponseOverridePolicyConfig struct {
	Match    ResponseOverrideMatchPolicyConfig
	Response ResponseOverrideResponsePolicyConfig
}

// ResponseOverrideMatchPolicyConfig represents match conditions
type ResponseOverrideMatchPolicyConfig struct {
	StatusCodes []StatusCodeMatchPolicyConfig
}

// StatusCodeMatchPolicyConfig represents status code match
type StatusCodeMatchPolicyConfig struct {
	Type  string
	Value *int
	Range *StatusCodeRangePolicyConfig
}

// StatusCodeRangePolicyConfig represents status code range
type StatusCodeRangePolicyConfig struct {
	Start int
	End   int
}

// ResponseOverrideResponsePolicyConfig represents response override response
type ResponseOverrideResponsePolicyConfig struct {
	ContentType string
	Body        ResponseOverrideBodyPolicyConfig
}

// ResponseOverrideBodyPolicyConfig represents response body configuration
type ResponseOverrideBodyPolicyConfig struct {
	Type     string
	Inline   string
	ValueRef *ValueRefPolicyConfig
}

// ValueRefPolicyConfig represents a ConfigMap or Secret reference
type ValueRefPolicyConfig struct {
	Group     string
	Kind      string
	Name      string
	Namespace string
}

// BackendTrafficPolicyTargetRef represents the target reference for BackendTrafficPolicy
type BackendTrafficPolicyTargetRef struct {
	Group string
	Kind  string
	Name  string
}

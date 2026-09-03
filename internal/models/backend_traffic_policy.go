package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// BackendTrafficPolicy represents backend traffic configuration for routes or domains
// This maps to Envoy Gateway's BackendTrafficPolicy CRD
type BackendTrafficPolicy struct {
	ID        uuid.UUID                  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	RouteID   *uuid.UUID                 `gorm:"type:uuid;uniqueIndex" json:"routeId,omitempty"` // For per-route policy
	DomainID  *uuid.UUID                 `gorm:"type:uuid;index" json:"domainId,omitempty"`      // For per-domain policy (future)
	ProjectID uuid.UUID                  `gorm:"type:uuid;not null;index" json:"projectId"`
	Config    BackendTrafficPolicyConfig `gorm:"type:jsonb" json:"config"`
	CreatedAt time.Time                  `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt time.Time                  `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Route   *Route   `gorm:"foreignKey:RouteID" json:"route,omitempty"`
	Domain  *Domain  `gorm:"foreignKey:DomainID" json:"domain,omitempty"`
	Project *Project `gorm:"foreignKey:ProjectID" json:"project,omitempty"`
}

// TableName returns the table name for BackendTrafficPolicy
func (BackendTrafficPolicy) TableName() string {
	return "backend_traffic_policies"
}

// BackendTrafficPolicyConfig holds all backend traffic policy configurations
type BackendTrafficPolicyConfig struct {
	Compression      []CompressionConfig    `json:"compression,omitempty"`
	Retry            *RetryConfig           `json:"retry,omitempty"`
	LoadBalancer     *LoadBalancerConfig    `json:"loadBalancer,omitempty"`
	CircuitBreaker   *CircuitBreakerConfig  `json:"circuitBreaker,omitempty"`
	HealthCheck      *HealthCheckConfig     `json:"healthCheck,omitempty"`
	FaultInjection   *FaultInjectionConfig  `json:"faultInjection,omitempty"`
	RateLimit        *RateLimitConfig       `json:"rateLimit,omitempty"`
	RequestBuffer    *RequestBufferConfig   `json:"requestBuffer,omitempty"`
	ResponseOverride []ResponseOverrideRule `json:"responseOverride,omitempty"`
	Timeout          *BTPTimeoutConfig      `json:"timeout,omitempty"`
}

// Value implements the driver.Valuer interface for BackendTrafficPolicyConfig
func (c BackendTrafficPolicyConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for BackendTrafficPolicyConfig
func (c *BackendTrafficPolicyConfig) Scan(value interface{}) error {
	if value == nil {
		*c = BackendTrafficPolicyConfig{}
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(bytes, c)
}

// IsEmpty checks if the config has any features configured
func (c *BackendTrafficPolicyConfig) IsEmpty() bool {
	return len(c.Compression) == 0 && c.Retry == nil && c.LoadBalancer == nil && c.CircuitBreaker == nil && c.HealthCheck == nil && c.FaultInjection == nil && c.RateLimit == nil && c.RequestBuffer == nil && len(c.ResponseOverride) == 0 && c.Timeout == nil
}

// CompressionType represents the type of compression algorithm
type CompressionType string

const (
	CompressionTypeGzip   CompressionType = "Gzip"
	CompressionTypeBrotli CompressionType = "Brotli"
	CompressionTypeZstd   CompressionType = "Zstd"
)

// CompressionConfig represents a single compression configuration
type CompressionConfig struct {
	Type   CompressionType `json:"type"`             // Gzip, Brotli, Zstd
	Gzip   *GzipConfig     `json:"gzip,omitempty"`   // Gzip-specific config
	Brotli *BrotliConfig   `json:"brotli,omitempty"` // Brotli-specific config
	Zstd   *ZstdConfig     `json:"zstd,omitempty"`   // Zstd-specific config
}

// GzipConfig represents Gzip compression configuration
// Empty for now, can be extended with compression level, window bits, etc.
type GzipConfig struct{}

// BrotliConfig represents Brotli compression configuration
// Empty for now, can be extended with quality, window bits, etc.
type BrotliConfig struct{}

// ZstdConfig represents Zstd compression configuration
// Empty for now, can be extended with compression level, etc.
type ZstdConfig struct{}

// RetryConfig represents retry configuration for BackendTrafficPolicy
type RetryConfig struct {
	NumRetries     *int32          `json:"numRetries,omitempty"`
	RetryOn        *RetryOn        `json:"retryOn,omitempty"`
	PerRetryPolicy *PerRetryPolicy `json:"perRetryPolicy,omitempty"`
}

// RetryOn specifies conditions that trigger a retry
type RetryOn struct {
	HTTPStatusCodes []int    `json:"httpStatusCodes,omitempty"`
	Triggers        []string `json:"triggers,omitempty"`
}

// PerRetryPolicy defines per-attempt retry behavior
type PerRetryPolicy struct {
	BackOff *BackOffPolicy `json:"backOff,omitempty"`
	Timeout *string        `json:"timeout,omitempty"` // e.g. "1s", "250ms"
}

// BackOffPolicy defines backoff timing between retries
type BackOffPolicy struct {
	BaseInterval *string `json:"baseInterval,omitempty"` // e.g. "25ms", "100ms"
	MaxInterval  *string `json:"maxInterval,omitempty"`  // e.g. "250ms", "10s"
}

// validRetryTriggers is the set of known Envoy Gateway retry trigger conditions
// Based on the BackendTrafficPolicy CRD enum validation
var validRetryTriggers = map[string]bool{
	"5xx":                    true,
	"gateway-error":          true,
	"reset":                  true,
	"reset-before-request":   true,
	"connect-failure":        true,
	"retriable-4xx":          true,
	"refused-stream":         true,
	"retriable-status-codes": true,
	"cancelled":              true,
	"deadline-exceeded":      true,
	"internal":               true,
	"resource-exhausted":     true,
	"unavailable":            true,
}

// LoadBalancerType represents the type of load balancing algorithm
type LoadBalancerType string

const (
	LoadBalancerTypeRoundRobin     LoadBalancerType = "RoundRobin"
	LoadBalancerTypeRandom         LoadBalancerType = "Random"
	LoadBalancerTypeLeastRequest   LoadBalancerType = "LeastRequest"
	LoadBalancerTypeConsistentHash LoadBalancerType = "ConsistentHash"
)

// ConsistentHashType represents the type of consistent hashing
type ConsistentHashType string

const (
	ConsistentHashTypeSourceIP ConsistentHashType = "SourceIP"
	ConsistentHashTypeHeader   ConsistentHashType = "Header"
	ConsistentHashTypeCookie   ConsistentHashType = "Cookie"
)

// LoadBalancerConfig represents load balancer configuration
type LoadBalancerConfig struct {
	Type           LoadBalancerType      `json:"type"`
	ConsistentHash *ConsistentHashConfig `json:"consistentHash,omitempty"`
}

// ConsistentHashConfig represents consistent hash configuration
type ConsistentHashConfig struct {
	Type   ConsistentHashType    `json:"type"`
	Header *ConsistentHashHeader `json:"header,omitempty"`
	Cookie *ConsistentHashCookie `json:"cookie,omitempty"`
}

// ConsistentHashHeader represents header-based consistent hashing
type ConsistentHashHeader struct {
	Name string `json:"name"`
}

// ConsistentHashCookie represents cookie-based consistent hashing
type ConsistentHashCookie struct {
	Name       string            `json:"name"`
	TTL        *string           `json:"ttl,omitempty"`        // e.g. "60s"
	Attributes map[string]string `json:"attributes,omitempty"` // e.g. {"SameSite": "Strict"}
}

// CircuitBreakerConfig represents circuit breaker configuration
type CircuitBreakerConfig struct {
	MaxConnections           *int64 `json:"maxConnections,omitempty"`
	MaxPendingRequests       *int64 `json:"maxPendingRequests,omitempty"`
	MaxParallelRequests      *int64 `json:"maxParallelRequests,omitempty"`
	MaxParallelRetries       *int64 `json:"maxParallelRetries,omitempty"`
	MaxRequestsPerConnection *int64 `json:"maxRequestsPerConnection,omitempty"`
}

// HealthCheckConfig represents health check configuration
type HealthCheckConfig struct {
	Active         *ActiveHealthCheckConfig  `json:"active,omitempty"`
	Passive        *PassiveHealthCheckConfig `json:"passive,omitempty"`
	PanicThreshold *uint32                   `json:"panicThreshold,omitempty"`
}

// ActiveHealthCheckConfig represents active health check configuration
type ActiveHealthCheckConfig struct {
	Timeout            *string                      `json:"timeout,omitempty"`
	Interval           *string                      `json:"interval,omitempty"`
	UnhealthyThreshold *uint32                      `json:"unhealthyThreshold,omitempty"`
	HealthyThreshold   *uint32                      `json:"healthyThreshold,omitempty"`
	Type               string                       `json:"type"`
	HTTP               *HTTPActiveHealthCheckConfig `json:"http,omitempty"`
	TCP                *TCPActiveHealthCheckConfig  `json:"tcp,omitempty"`
	GRPC               *GRPCActiveHealthCheckConfig `json:"grpc,omitempty"`
}

// HTTPActiveHealthCheckConfig represents HTTP active health checker
type HTTPActiveHealthCheckConfig struct {
	Path             string  `json:"path"`
	Method           *string `json:"method,omitempty"`
	ExpectedStatuses []int   `json:"expectedStatuses,omitempty"`
}

// TCPActiveHealthCheckConfig represents TCP active health checker
type TCPActiveHealthCheckConfig struct {
	Send    *HealthCheckPayload `json:"send,omitempty"`
	Receive *HealthCheckPayload `json:"receive,omitempty"`
}

// GRPCActiveHealthCheckConfig represents gRPC active health checker
type GRPCActiveHealthCheckConfig struct {
	Service *string `json:"service,omitempty"`
}

// HealthCheckPayload represents a health check payload
type HealthCheckPayload struct {
	Type string  `json:"type"`
	Text *string `json:"text,omitempty"`
}

// PassiveHealthCheckConfig represents passive health check (outlier detection)
type PassiveHealthCheckConfig struct {
	ConsecutiveGatewayErrors       *uint32 `json:"consecutiveGatewayErrors,omitempty"`
	Consecutive5xxErrors           *uint32 `json:"consecutive5xxErrors,omitempty"`
	ConsecutiveLocalOriginFailures *uint32 `json:"consecutiveLocalOriginFailures,omitempty"`
	Interval                       *string `json:"interval,omitempty"`
	BaseEjectionTime               *string `json:"baseEjectionTime,omitempty"`
	MaxEjectionPercent             *int32  `json:"maxEjectionPercent,omitempty"`
	SplitExternalLocalOriginErrors *bool   `json:"splitExternalLocalOriginErrors,omitempty"`
}

// FaultInjectionConfig represents fault injection configuration
type FaultInjectionConfig struct {
	Delay *FaultInjectionDelayConfig `json:"delay,omitempty"`
	Abort *FaultInjectionAbortConfig `json:"abort,omitempty"`
}

// FaultInjectionDelayConfig represents delay fault injection
type FaultInjectionDelayConfig struct {
	FixedDelay string   `json:"fixedDelay"`
	Percentage *float32 `json:"percentage,omitempty"` // 0-100
}

// FaultInjectionAbortConfig represents abort fault injection
type FaultInjectionAbortConfig struct {
	HTTPStatus *int     `json:"httpStatus,omitempty"`
	GRPCStatus *int     `json:"grpcStatus,omitempty"`
	Percentage *float32 `json:"percentage,omitempty"` // 0-100
}

// RateLimitConfig represents rate limit configuration
type RateLimitConfig struct {
	Global *GlobalRateLimitConfig `json:"global,omitempty"`
}

// GlobalRateLimitConfig represents global rate limit configuration
type GlobalRateLimitConfig struct {
	Rules []RateLimitRule `json:"rules"`
}

// RateLimitRule represents a single rate limit rule
type RateLimitRule struct {
	Limit           RateLimitValue      `json:"limit"`
	ClientSelectors []RateLimitSelector `json:"clientSelectors,omitempty"`
}

// RateLimitValue represents the rate limit value (requests per time unit)
type RateLimitValue struct {
	Requests int    `json:"requests"`
	Unit     string `json:"unit"` // Second, Minute, Hour, Day
}

// RateLimitSelector represents client selection criteria for rate limiting
type RateLimitSelector struct {
	Headers    []RateLimitHeaderMatch `json:"headers,omitempty"`
	SourceCIDR *RateLimitSourceCIDR   `json:"sourceCIDR,omitempty"`
	Path       *RateLimitPathMatch    `json:"path,omitempty"`
	Methods    []string               `json:"methods,omitempty"`
}

// RateLimitHeaderMatch represents a header match for rate limiting
type RateLimitHeaderMatch struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Type   string `json:"type,omitempty"` // "Exact" or "Distinct"
	Invert bool   `json:"invert,omitempty"`
}

// RateLimitSourceCIDR represents a source CIDR match for rate limiting
type RateLimitSourceCIDR struct {
	Value string `json:"value"`
	Type  string `json:"type,omitempty"` // "Exact" or "Distinct"
}

// RateLimitPathMatch represents a path match for rate limiting
type RateLimitPathMatch struct {
	Value string `json:"value"`
	Type  string `json:"type"` // "Exact", "PathPrefix", "RegularExpression"
}

// Value implements the driver.Valuer interface for RateLimitConfig
func (rl RateLimitConfig) Value() (driver.Value, error) {
	return json.Marshal(rl)
}

// Scan implements the sql.Scanner interface for RateLimitConfig
func (rl *RateLimitConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, rl)
}

// RequestBufferConfig represents request buffering configuration
type RequestBufferConfig struct {
	Limit string `json:"limit"` // e.g., "4Ki", "1Mi" - max buffer size
}

// ResponseOverrideRule represents a single response override rule
type ResponseOverrideRule struct {
	Match    ResponseOverrideMatch    `json:"match"`
	Response ResponseOverrideResponse `json:"response"`
}

// ResponseOverrideMatch represents match conditions for response override
type ResponseOverrideMatch struct {
	StatusCodes []StatusCodeMatch `json:"statusCodes"`
}

// StatusCodeMatch represents a status code match condition
type StatusCodeMatch struct {
	Type  string           `json:"type"` // "Value" or "Range"
	Value *int             `json:"value,omitempty"`
	Range *StatusCodeRange `json:"range,omitempty"`
}

// StatusCodeRange represents a range of status codes
type StatusCodeRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ResponseOverrideResponse represents the response to return
type ResponseOverrideResponse struct {
	ContentType string               `json:"contentType"`
	Body        ResponseOverrideBody `json:"body"`
}

// ResponseOverrideBody represents the response body configuration
type ResponseOverrideBody struct {
	Type     string    `json:"type"` // "Inline" or "ValueRef"
	Inline   string    `json:"inline,omitempty"`
	ValueRef *ValueRef `json:"valueRef,omitempty"`
}

// ValueRef represents a reference to a ConfigMap or Secret
type ValueRef struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"` // "ConfigMap" or "Secret"
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// BTPTimeoutConfig represents BTP timeout configuration
type BTPTimeoutConfig struct {
	TCP  *BTPTCPTimeoutConfig  `json:"tcp,omitempty"`
	HTTP *BTPHTTPTimeoutConfig `json:"http,omitempty"`
}

// BTPTCPTimeoutConfig represents TCP-level timeout settings
type BTPTCPTimeoutConfig struct {
	ConnectTimeout string `json:"connectTimeout,omitempty"`
}

// BTPHTTPTimeoutConfig represents HTTP-level timeout settings
type BTPHTTPTimeoutConfig struct {
	RequestTimeout        string `json:"requestTimeout,omitempty"`
	ConnectionIdleTimeout string `json:"connectionIdleTimeout,omitempty"`
	MaxConnectionDuration string `json:"maxConnectionDuration,omitempty"`
	MaxStreamDuration     string `json:"maxStreamDuration,omitempty"`
}

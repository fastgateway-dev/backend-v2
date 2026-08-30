package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
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

// Validate validates the load balancer configuration
func (lb *LoadBalancerConfig) Validate() error {
	if lb == nil {
		return nil
	}

	validTypes := map[LoadBalancerType]bool{
		LoadBalancerTypeRoundRobin:     true,
		LoadBalancerTypeRandom:         true,
		LoadBalancerTypeLeastRequest:   true,
		LoadBalancerTypeConsistentHash: true,
	}
	if !validTypes[lb.Type] {
		return fmt.Errorf("invalid load balancer type %q, must be one of: RoundRobin, Random, LeastRequest, ConsistentHash", lb.Type)
	}

	if lb.Type == LoadBalancerTypeConsistentHash {
		if lb.ConsistentHash == nil {
			return fmt.Errorf("consistentHash configuration is required when type is ConsistentHash")
		}

		validHashTypes := map[ConsistentHashType]bool{
			ConsistentHashTypeSourceIP: true,
			ConsistentHashTypeHeader:   true,
			ConsistentHashTypeCookie:   true,
		}
		if !validHashTypes[lb.ConsistentHash.Type] {
			return fmt.Errorf("invalid consistent hash type %q, must be one of: SourceIP, Header, Cookie", lb.ConsistentHash.Type)
		}

		switch lb.ConsistentHash.Type {
		case ConsistentHashTypeHeader:
			if lb.ConsistentHash.Header == nil || lb.ConsistentHash.Header.Name == "" {
				return fmt.Errorf("header name is required when consistent hash type is Header")
			}
		case ConsistentHashTypeCookie:
			if lb.ConsistentHash.Cookie == nil || lb.ConsistentHash.Cookie.Name == "" {
				return fmt.Errorf("cookie name is required when consistent hash type is Cookie")
			}
			if lb.ConsistentHash.Cookie.TTL != nil {
				d, err := time.ParseDuration(*lb.ConsistentHash.Cookie.TTL)
				if err != nil {
					return fmt.Errorf("invalid cookie TTL %q: %w", *lb.ConsistentHash.Cookie.TTL, err)
				}
				if d < 0 {
					return fmt.Errorf("cookie TTL must be non-negative, got %s", *lb.ConsistentHash.Cookie.TTL)
				}
			}
		}
	} else if lb.ConsistentHash != nil {
		return fmt.Errorf("consistentHash configuration should only be set when type is ConsistentHash")
	}

	return nil
}

// CircuitBreakerConfig represents circuit breaker configuration
type CircuitBreakerConfig struct {
	MaxConnections           *int64 `json:"maxConnections,omitempty"`
	MaxPendingRequests       *int64 `json:"maxPendingRequests,omitempty"`
	MaxParallelRequests      *int64 `json:"maxParallelRequests,omitempty"`
	MaxParallelRetries       *int64 `json:"maxParallelRetries,omitempty"`
	MaxRequestsPerConnection *int64 `json:"maxRequestsPerConnection,omitempty"`
}

// Validate validates the circuit breaker configuration
func (cb *CircuitBreakerConfig) Validate() error {
	if cb == nil {
		return nil
	}
	if cb.MaxConnections != nil && *cb.MaxConnections < 0 {
		return fmt.Errorf("maxConnections must be >= 0, got %d", *cb.MaxConnections)
	}
	if cb.MaxPendingRequests != nil && *cb.MaxPendingRequests < 0 {
		return fmt.Errorf("maxPendingRequests must be >= 0, got %d", *cb.MaxPendingRequests)
	}
	if cb.MaxParallelRequests != nil && *cb.MaxParallelRequests < 0 {
		return fmt.Errorf("maxParallelRequests must be >= 0, got %d", *cb.MaxParallelRequests)
	}
	if cb.MaxParallelRetries != nil && *cb.MaxParallelRetries < 0 {
		return fmt.Errorf("maxParallelRetries must be >= 0, got %d", *cb.MaxParallelRetries)
	}
	if cb.MaxRequestsPerConnection != nil && *cb.MaxRequestsPerConnection < 0 {
		return fmt.Errorf("maxRequestsPerConnection must be >= 0, got %d", *cb.MaxRequestsPerConnection)
	}
	return nil
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

// Validate validates the health check configuration
func (hc *HealthCheckConfig) Validate() error {
	if hc == nil {
		return nil
	}
	if hc.Active == nil && hc.Passive == nil {
		return fmt.Errorf("at least one of active or passive health check must be configured")
	}
	if hc.Active != nil {
		if err := hc.Active.Validate(); err != nil {
			return fmt.Errorf("active health check: %w", err)
		}
	}
	if hc.Passive != nil {
		if err := hc.Passive.Validate(); err != nil {
			return fmt.Errorf("passive health check: %w", err)
		}
	}
	return nil
}

// Validate validates the active health check configuration
func (a *ActiveHealthCheckConfig) Validate() error {
	if a.Type == "" {
		return fmt.Errorf("type is required")
	}
	validTypes := map[string]bool{"HTTP": true, "TCP": true, "GRPC": true}
	if !validTypes[a.Type] {
		return fmt.Errorf("type must be HTTP, TCP, or GRPC, got %q", a.Type)
	}
	if a.Type == "HTTP" {
		if a.HTTP == nil || a.HTTP.Path == "" {
			return fmt.Errorf("HTTP health check requires a path")
		}
		if a.HTTP.Method != nil {
			validMethods := map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "DELETE": true, "OPTIONS": true, "PATCH": true}
			if !validMethods[*a.HTTP.Method] {
				return fmt.Errorf("invalid HTTP method: %q", *a.HTTP.Method)
			}
		}
		for _, code := range a.HTTP.ExpectedStatuses {
			if code < 100 || code > 599 {
				return fmt.Errorf("expected status must be 100-599, got %d", code)
			}
		}
	}
	if a.Timeout != nil {
		if _, err := time.ParseDuration(*a.Timeout); err != nil {
			return fmt.Errorf("invalid timeout duration: %w", err)
		}
	}
	if a.Interval != nil {
		if _, err := time.ParseDuration(*a.Interval); err != nil {
			return fmt.Errorf("invalid interval duration: %w", err)
		}
	}
	return nil
}

// Validate validates the passive health check configuration
func (p *PassiveHealthCheckConfig) Validate() error {
	if p.Interval != nil {
		if _, err := time.ParseDuration(*p.Interval); err != nil {
			return fmt.Errorf("invalid interval duration: %w", err)
		}
	}
	if p.BaseEjectionTime != nil {
		if _, err := time.ParseDuration(*p.BaseEjectionTime); err != nil {
			return fmt.Errorf("invalid base ejection time duration: %w", err)
		}
	}
	return nil
}

// Validate validates the retry configuration
func (r *RetryConfig) Validate() error {
	if r == nil {
		return nil
	}

	if r.NumRetries != nil && *r.NumRetries < 0 {
		return fmt.Errorf("numRetries must be >= 0, got %d", *r.NumRetries)
	}

	if r.RetryOn != nil {
		for _, code := range r.RetryOn.HTTPStatusCodes {
			if code < 100 || code > 599 {
				return fmt.Errorf("invalid HTTP status code %d, must be between 100 and 599", code)
			}
		}
		for _, trigger := range r.RetryOn.Triggers {
			if !validRetryTriggers[trigger] {
				return fmt.Errorf("invalid retry trigger %q", trigger)
			}
		}
	}

	if r.PerRetryPolicy != nil {
		if r.PerRetryPolicy.Timeout != nil {
			d, err := time.ParseDuration(*r.PerRetryPolicy.Timeout)
			if err != nil {
				return fmt.Errorf("invalid per-retry timeout %q: %w", *r.PerRetryPolicy.Timeout, err)
			}
			if d <= 0 {
				return fmt.Errorf("per-retry timeout must be positive, got %s", *r.PerRetryPolicy.Timeout)
			}
		}

		if r.PerRetryPolicy.BackOff != nil {
			var baseDuration, maxDuration time.Duration

			if r.PerRetryPolicy.BackOff.BaseInterval != nil {
				d, err := time.ParseDuration(*r.PerRetryPolicy.BackOff.BaseInterval)
				if err != nil {
					return fmt.Errorf("invalid backoff baseInterval %q: %w", *r.PerRetryPolicy.BackOff.BaseInterval, err)
				}
				if d <= 0 {
					return fmt.Errorf("backoff baseInterval must be positive, got %s", *r.PerRetryPolicy.BackOff.BaseInterval)
				}
				baseDuration = d
			}

			if r.PerRetryPolicy.BackOff.MaxInterval != nil {
				d, err := time.ParseDuration(*r.PerRetryPolicy.BackOff.MaxInterval)
				if err != nil {
					return fmt.Errorf("invalid backoff maxInterval %q: %w", *r.PerRetryPolicy.BackOff.MaxInterval, err)
				}
				if d <= 0 {
					return fmt.Errorf("backoff maxInterval must be positive, got %s", *r.PerRetryPolicy.BackOff.MaxInterval)
				}
				maxDuration = d
			}

			if baseDuration > 0 && maxDuration > 0 && maxDuration < baseDuration {
				return fmt.Errorf("backoff maxInterval (%s) must be >= baseInterval (%s)",
					*r.PerRetryPolicy.BackOff.MaxInterval, *r.PerRetryPolicy.BackOff.BaseInterval)
			}
		}
	}

	return nil
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

// Validate validates the rate limit configuration
func (rl *RateLimitConfig) Validate() error {
	if rl == nil {
		return nil
	}
	if rl.Global == nil {
		return fmt.Errorf("global rate limit configuration is required")
	}
	if len(rl.Global.Rules) == 0 {
		return fmt.Errorf("at least one rate limit rule is required")
	}
	validUnits := map[string]bool{"Second": true, "Minute": true, "Hour": true, "Day": true}
	for i, rule := range rl.Global.Rules {
		if rule.Limit.Requests <= 0 {
			return fmt.Errorf("rule[%d]: requests must be > 0", i)
		}
		if !validUnits[rule.Limit.Unit] {
			return fmt.Errorf("rule[%d]: unit must be one of: Second, Minute, Hour, Day, got %q", i, rule.Limit.Unit)
		}
		for j, sel := range rule.ClientSelectors {
			for k, h := range sel.Headers {
				if h.Name == "" {
					return fmt.Errorf("rule[%d].clientSelectors[%d].headers[%d]: name is required", i, j, k)
				}
				if h.Type != "" && h.Type != "Exact" && h.Type != "Distinct" {
					return fmt.Errorf("rule[%d].clientSelectors[%d].headers[%d]: type must be 'Exact' or 'Distinct', got %q", i, j, k, h.Type)
				}
			}
			if sel.SourceCIDR != nil {
				if sel.SourceCIDR.Value == "" {
					return fmt.Errorf("rule[%d].clientSelectors[%d].sourceCIDR: value is required", i, j)
				}
				// Validate CIDR format
				_, _, err := net.ParseCIDR(sel.SourceCIDR.Value)
				if err != nil {
					// Try parsing as plain IP (will be treated as /32 or /128)
					if net.ParseIP(sel.SourceCIDR.Value) == nil {
						return fmt.Errorf("rule[%d].clientSelectors[%d].sourceCIDR: invalid CIDR or IP format %q", i, j, sel.SourceCIDR.Value)
					}
				}
				if sel.SourceCIDR.Type != "" && sel.SourceCIDR.Type != "Exact" && sel.SourceCIDR.Type != "Distinct" {
					return fmt.Errorf("rule[%d].clientSelectors[%d].sourceCIDR: type must be 'Exact' or 'Distinct', got %q", i, j, sel.SourceCIDR.Type)
				}
			}
			if sel.Path != nil {
				if sel.Path.Value == "" {
					return fmt.Errorf("rule[%d].clientSelectors[%d].path: value is required", i, j)
				}
				validPathTypes := map[string]bool{"Exact": true, "PathPrefix": true, "RegularExpression": true}
				if !validPathTypes[sel.Path.Type] {
					return fmt.Errorf("rule[%d].clientSelectors[%d].path: type must be 'Exact', 'PathPrefix', or 'RegularExpression', got %q", i, j, sel.Path.Type)
				}
			}
		}
	}
	return nil
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

// Validate validates the fault injection configuration
func (fi *FaultInjectionConfig) Validate() error {
	if fi == nil {
		return nil
	}
	if fi.Delay == nil && fi.Abort == nil {
		return fmt.Errorf("at least one of delay or abort must be configured")
	}
	if fi.Delay != nil {
		if err := fi.Delay.Validate(); err != nil {
			return fmt.Errorf("delay: %w", err)
		}
	}
	if fi.Abort != nil {
		if err := fi.Abort.Validate(); err != nil {
			return fmt.Errorf("abort: %w", err)
		}
	}
	return nil
}

// Validate validates the delay fault injection configuration
func (d *FaultInjectionDelayConfig) Validate() error {
	if d.FixedDelay == "" {
		return fmt.Errorf("fixedDelay is required")
	}
	if _, err := time.ParseDuration(d.FixedDelay); err != nil {
		return fmt.Errorf("invalid fixedDelay duration: %w", err)
	}
	if d.Percentage != nil {
		if *d.Percentage < 0 || *d.Percentage > 100 {
			return fmt.Errorf("percentage must be between 0 and 100, got %f", *d.Percentage)
		}
	}
	return nil
}

// Validate validates the abort fault injection configuration
func (a *FaultInjectionAbortConfig) Validate() error {
	if a.HTTPStatus == nil && a.GRPCStatus == nil {
		return fmt.Errorf("either httpStatus or grpcStatus is required")
	}
	if a.HTTPStatus != nil && a.GRPCStatus != nil {
		return fmt.Errorf("cannot specify both httpStatus and grpcStatus")
	}
	if a.HTTPStatus != nil {
		if *a.HTTPStatus < 100 || *a.HTTPStatus > 599 {
			return fmt.Errorf("httpStatus must be between 100 and 599, got %d", *a.HTTPStatus)
		}
	}
	if a.GRPCStatus != nil {
		// Valid gRPC status codes are 0-16
		if *a.GRPCStatus < 0 || *a.GRPCStatus > 16 {
			return fmt.Errorf("grpcStatus must be between 0 and 16, got %d", *a.GRPCStatus)
		}
	}
	if a.Percentage != nil {
		if *a.Percentage < 0 || *a.Percentage > 100 {
			return fmt.Errorf("percentage must be between 0 and 100, got %f", *a.Percentage)
		}
	}
	return nil
}

// RequestBufferConfig represents request buffering configuration
type RequestBufferConfig struct {
	Limit string `json:"limit"` // e.g., "4Ki", "1Mi" - max buffer size
}

// Validate validates the request buffer config
func (r *RequestBufferConfig) Validate() error {
	if r.Limit == "" {
		return errors.New("requestBuffer.limit is required")
	}
	// Basic SI unit validation: number optionally followed by Ki, Mi, Gi, etc.
	matched := regexp.MustCompile(`^\d+(\.\d+)?(Ki?|Mi?|Gi?|Ti?)?$`).MatchString(r.Limit)
	if !matched {
		return fmt.Errorf("requestBuffer.limit must be a valid size (e.g., 4Ki, 1Mi), got %q", r.Limit)
	}
	return nil
}

// ResponseOverrideRule represents a single response override rule
type ResponseOverrideRule struct {
	Match    ResponseOverrideMatch    `json:"match"`
	Response ResponseOverrideResponse `json:"response"`
}

// Validate validates the response override rule
func (r *ResponseOverrideRule) Validate() error {
	if len(r.Match.StatusCodes) == 0 {
		return errors.New("responseOverride.match.statusCodes is required")
	}
	for i, sc := range r.Match.StatusCodes {
		if err := sc.Validate(); err != nil {
			return fmt.Errorf("responseOverride.match.statusCodes[%d]: %w", i, err)
		}
	}
	if r.Response.ContentType == "" {
		return errors.New("responseOverride.response.contentType is required")
	}
	if r.Response.Body.Type == "" {
		return errors.New("responseOverride.response.body.type is required")
	}
	if r.Response.Body.Type == "Inline" && r.Response.Body.Inline == "" {
		return errors.New("responseOverride.response.body.inline is required when type is Inline")
	}
	if r.Response.Body.Type == "ValueRef" && r.Response.Body.ValueRef == nil {
		return errors.New("responseOverride.response.body.valueRef is required when type is ValueRef")
	}
	return nil
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

// Validate validates the status code match
func (s *StatusCodeMatch) Validate() error {
	if s.Type != "Value" && s.Type != "Range" {
		return fmt.Errorf("type must be 'Value' or 'Range', got %q", s.Type)
	}
	if s.Type == "Value" {
		if s.Value == nil {
			return errors.New("value is required when type is Value")
		}
		if *s.Value < 100 || *s.Value > 599 {
			return fmt.Errorf("value must be between 100-599, got %d", *s.Value)
		}
	}
	if s.Type == "Range" {
		if s.Range == nil {
			return errors.New("range is required when type is Range")
		}
		if s.Range.Start < 100 || s.Range.Start > 599 {
			return fmt.Errorf("range.start must be between 100-599, got %d", s.Range.Start)
		}
		if s.Range.End < 100 || s.Range.End > 599 {
			return fmt.Errorf("range.end must be between 100-599, got %d", s.Range.End)
		}
		if s.Range.Start > s.Range.End {
			return fmt.Errorf("range.start (%d) must be <= range.end (%d)", s.Range.Start, s.Range.End)
		}
	}
	return nil
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

// Validate validates the timeout configuration
func (t *BTPTimeoutConfig) Validate() error {
	if t == nil {
		return nil
	}
	if t.TCP != nil && t.TCP.ConnectTimeout != "" {
		if _, err := time.ParseDuration(t.TCP.ConnectTimeout); err != nil {
			return fmt.Errorf("invalid tcp.connectTimeout duration: %w", err)
		}
	}
	if t.HTTP != nil {
		if t.HTTP.RequestTimeout != "" {
			if _, err := time.ParseDuration(t.HTTP.RequestTimeout); err != nil {
				return fmt.Errorf("invalid http.requestTimeout duration: %w", err)
			}
		}
		if t.HTTP.ConnectionIdleTimeout != "" {
			if _, err := time.ParseDuration(t.HTTP.ConnectionIdleTimeout); err != nil {
				return fmt.Errorf("invalid http.connectionIdleTimeout duration: %w", err)
			}
		}
		if t.HTTP.MaxConnectionDuration != "" {
			if _, err := time.ParseDuration(t.HTTP.MaxConnectionDuration); err != nil {
				return fmt.Errorf("invalid http.maxConnectionDuration duration: %w", err)
			}
		}
		if t.HTTP.MaxStreamDuration != "" {
			if _, err := time.ParseDuration(t.HTTP.MaxStreamDuration); err != nil {
				return fmt.Errorf("invalid http.maxStreamDuration duration: %w", err)
			}
		}
	}
	return nil
}

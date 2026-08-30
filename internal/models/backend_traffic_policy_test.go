package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBackendTrafficPolicyConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  BackendTrafficPolicyConfig
		want bool
	}{
		{
			name: "empty config",
			cfg:  BackendTrafficPolicyConfig{},
			want: true,
		},
		{
			name: "with retry",
			cfg:  BackendTrafficPolicyConfig{Retry: &RetryConfig{}},
			want: false,
		},
		{
			name: "with compression",
			cfg:  BackendTrafficPolicyConfig{Compression: []CompressionConfig{{Type: CompressionTypeGzip}}},
			want: false,
		},
		{
			name: "with load balancer",
			cfg:  BackendTrafficPolicyConfig{LoadBalancer: &LoadBalancerConfig{}},
			want: false,
		},
		{
			name: "with circuit breaker",
			cfg:  BackendTrafficPolicyConfig{CircuitBreaker: &CircuitBreakerConfig{}},
			want: false,
		},
		{
			name: "with health check",
			cfg:  BackendTrafficPolicyConfig{HealthCheck: &HealthCheckConfig{}},
			want: false,
		},
		{
			name: "with fault injection",
			cfg:  BackendTrafficPolicyConfig{FaultInjection: &FaultInjectionConfig{}},
			want: false,
		},
		{
			name: "with rate limit",
			cfg:  BackendTrafficPolicyConfig{RateLimit: &RateLimitConfig{}},
			want: false,
		},
		{
			name: "with request buffer",
			cfg:  BackendTrafficPolicyConfig{RequestBuffer: &RequestBufferConfig{}},
			want: false,
		},
		{
			name: "with response override",
			cfg:  BackendTrafficPolicyConfig{ResponseOverride: []ResponseOverrideRule{{}}},
			want: false,
		},
		{
			name: "with timeout",
			cfg:  BackendTrafficPolicyConfig{Timeout: &BTPTimeoutConfig{}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestLoadBalancerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *LoadBalancerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: false,
		},
		{
			name:    "round robin",
			cfg:     &LoadBalancerConfig{Type: LoadBalancerTypeRoundRobin},
			wantErr: false,
		},
		{
			name:    "random",
			cfg:     &LoadBalancerConfig{Type: LoadBalancerTypeRandom},
			wantErr: false,
		},
		{
			name:    "least request",
			cfg:     &LoadBalancerConfig{Type: LoadBalancerTypeLeastRequest},
			wantErr: false,
		},
		{
			name:    "invalid type",
			cfg:     &LoadBalancerConfig{Type: "BadType"},
			wantErr: true,
			errMsg:  "invalid load balancer type",
		},
		{
			name:    "consistent hash without config",
			cfg:     &LoadBalancerConfig{Type: LoadBalancerTypeConsistentHash},
			wantErr: true,
			errMsg:  "consistentHash configuration is required",
		},
		{
			name: "consistent hash with source IP",
			cfg: &LoadBalancerConfig{
				Type:           LoadBalancerTypeConsistentHash,
				ConsistentHash: &ConsistentHashConfig{Type: ConsistentHashTypeSourceIP},
			},
			wantErr: false,
		},
		{
			name: "consistent hash header without name",
			cfg: &LoadBalancerConfig{
				Type:           LoadBalancerTypeConsistentHash,
				ConsistentHash: &ConsistentHashConfig{Type: ConsistentHashTypeHeader},
			},
			wantErr: true,
			errMsg:  "header name is required",
		},
		{
			name: "consistent hash header with name",
			cfg: &LoadBalancerConfig{
				Type: LoadBalancerTypeConsistentHash,
				ConsistentHash: &ConsistentHashConfig{
					Type:   ConsistentHashTypeHeader,
					Header: &ConsistentHashHeader{Name: "x-user"},
				},
			},
			wantErr: false,
		},
		{
			name: "consistent hash cookie without name",
			cfg: &LoadBalancerConfig{
				Type:           LoadBalancerTypeConsistentHash,
				ConsistentHash: &ConsistentHashConfig{Type: ConsistentHashTypeCookie},
			},
			wantErr: true,
			errMsg:  "cookie name is required",
		},
		{
			name: "consistent hash cookie with valid TTL",
			cfg: &LoadBalancerConfig{
				Type: LoadBalancerTypeConsistentHash,
				ConsistentHash: &ConsistentHashConfig{
					Type:   ConsistentHashTypeCookie,
					Cookie: &ConsistentHashCookie{Name: "session", TTL: strPtr("60s")},
				},
			},
			wantErr: false,
		},
		{
			name: "consistent hash cookie with negative TTL",
			cfg: &LoadBalancerConfig{
				Type: LoadBalancerTypeConsistentHash,
				ConsistentHash: &ConsistentHashConfig{
					Type:   ConsistentHashTypeCookie,
					Cookie: &ConsistentHashCookie{Name: "session", TTL: strPtr("-1s")},
				},
			},
			wantErr: true,
			errMsg:  "cookie TTL must be non-negative",
		},
		{
			name: "consistent hash cookie with invalid TTL",
			cfg: &LoadBalancerConfig{
				Type: LoadBalancerTypeConsistentHash,
				ConsistentHash: &ConsistentHashConfig{
					Type:   ConsistentHashTypeCookie,
					Cookie: &ConsistentHashCookie{Name: "session", TTL: strPtr("bad")},
				},
			},
			wantErr: true,
			errMsg:  "invalid cookie TTL",
		},
		{
			name: "non-consistent-hash with consistentHash config set",
			cfg: &LoadBalancerConfig{
				Type:           LoadBalancerTypeRoundRobin,
				ConsistentHash: &ConsistentHashConfig{Type: ConsistentHashTypeSourceIP},
			},
			wantErr: true,
			errMsg:  "consistentHash configuration should only be set",
		},
		{
			name: "invalid consistent hash type",
			cfg: &LoadBalancerConfig{
				Type:           LoadBalancerTypeConsistentHash,
				ConsistentHash: &ConsistentHashConfig{Type: "Invalid"},
			},
			wantErr: true,
			errMsg:  "invalid consistent hash type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCircuitBreakerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *CircuitBreakerConfig
		wantErr bool
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "empty", cfg: &CircuitBreakerConfig{}, wantErr: false},
		{name: "valid values", cfg: &CircuitBreakerConfig{MaxConnections: int64Ptr(100)}, wantErr: false},
		{name: "negative maxConnections", cfg: &CircuitBreakerConfig{MaxConnections: int64Ptr(-1)}, wantErr: true},
		{name: "negative maxPendingRequests", cfg: &CircuitBreakerConfig{MaxPendingRequests: int64Ptr(-1)}, wantErr: true},
		{name: "negative maxParallelRequests", cfg: &CircuitBreakerConfig{MaxParallelRequests: int64Ptr(-1)}, wantErr: true},
		{name: "negative maxParallelRetries", cfg: &CircuitBreakerConfig{MaxParallelRetries: int64Ptr(-1)}, wantErr: true},
		{name: "negative maxRequestsPerConnection", cfg: &CircuitBreakerConfig{MaxRequestsPerConnection: int64Ptr(-1)}, wantErr: true},
		{name: "zero values allowed", cfg: &CircuitBreakerConfig{MaxConnections: int64Ptr(0)}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRetryConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *RetryConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "empty", cfg: &RetryConfig{}, wantErr: false},
		{
			name:    "negative numRetries",
			cfg:     &RetryConfig{NumRetries: int32Ptr(-1)},
			wantErr: true,
			errMsg:  "numRetries must be >= 0",
		},
		{
			name: "invalid HTTP status code in retryOn",
			cfg: &RetryConfig{
				RetryOn: &RetryOn{HTTPStatusCodes: []int{999}},
			},
			wantErr: true,
			errMsg:  "invalid HTTP status code",
		},
		{
			name: "invalid retry trigger",
			cfg: &RetryConfig{
				RetryOn: &RetryOn{Triggers: []string{"bad-trigger"}},
			},
			wantErr: true,
			errMsg:  "invalid retry trigger",
		},
		{
			name: "valid retry trigger",
			cfg: &RetryConfig{
				RetryOn: &RetryOn{Triggers: []string{"5xx", "gateway-error"}},
			},
			wantErr: false,
		},
		{
			name: "invalid per-retry timeout",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{Timeout: strPtr("bad")},
			},
			wantErr: true,
			errMsg:  "invalid per-retry timeout",
		},
		{
			name: "non-positive per-retry timeout",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{Timeout: strPtr("0s")},
			},
			wantErr: true,
			errMsg:  "per-retry timeout must be positive",
		},
		{
			name: "valid backoff",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{
					BackOff: &BackOffPolicy{
						BaseInterval: strPtr("100ms"),
						MaxInterval:  strPtr("1s"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "max < base interval",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{
					BackOff: &BackOffPolicy{
						BaseInterval: strPtr("1s"),
						MaxInterval:  strPtr("100ms"),
					},
				},
			},
			wantErr: true,
			errMsg:  "maxInterval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFaultInjectionConfig_Validate(t *testing.T) {
	pct50 := float32(50)
	pct150 := float32(150)
	http500 := 500
	grpc5 := 5

	tests := []struct {
		name    string
		cfg     *FaultInjectionConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{
			name:    "neither delay nor abort",
			cfg:     &FaultInjectionConfig{},
			wantErr: true,
			errMsg:  "at least one of delay or abort",
		},
		{
			name: "valid delay",
			cfg: &FaultInjectionConfig{
				Delay: &FaultInjectionDelayConfig{FixedDelay: "1s", Percentage: &pct50},
			},
			wantErr: false,
		},
		{
			name: "delay missing fixedDelay",
			cfg: &FaultInjectionConfig{
				Delay: &FaultInjectionDelayConfig{},
			},
			wantErr: true,
			errMsg:  "fixedDelay is required",
		},
		{
			name: "delay invalid percentage",
			cfg: &FaultInjectionConfig{
				Delay: &FaultInjectionDelayConfig{FixedDelay: "1s", Percentage: &pct150},
			},
			wantErr: true,
			errMsg:  "percentage must be between 0 and 100",
		},
		{
			name: "valid abort with httpStatus",
			cfg: &FaultInjectionConfig{
				Abort: &FaultInjectionAbortConfig{HTTPStatus: &http500},
			},
			wantErr: false,
		},
		{
			name: "valid abort with grpcStatus",
			cfg: &FaultInjectionConfig{
				Abort: &FaultInjectionAbortConfig{GRPCStatus: &grpc5},
			},
			wantErr: false,
		},
		{
			name: "abort with both statuses",
			cfg: &FaultInjectionConfig{
				Abort: &FaultInjectionAbortConfig{HTTPStatus: &http500, GRPCStatus: &grpc5},
			},
			wantErr: true,
			errMsg:  "cannot specify both",
		},
		{
			name: "abort with neither status",
			cfg: &FaultInjectionConfig{
				Abort: &FaultInjectionAbortConfig{},
			},
			wantErr: true,
			errMsg:  "either httpStatus or grpcStatus is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRateLimitConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *RateLimitConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{
			name:    "no global",
			cfg:     &RateLimitConfig{},
			wantErr: true,
			errMsg:  "global rate limit configuration is required",
		},
		{
			name:    "empty rules",
			cfg:     &RateLimitConfig{Global: &GlobalRateLimitConfig{}},
			wantErr: true,
			errMsg:  "at least one rate limit rule is required",
		},
		{
			name: "valid rule",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{Limit: RateLimitValue{Requests: 100, Unit: "Minute"}}},
				},
			},
			wantErr: false,
		},
		{
			name: "zero requests",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{Limit: RateLimitValue{Requests: 0, Unit: "Minute"}}},
				},
			},
			wantErr: true,
			errMsg:  "requests must be > 0",
		},
		{
			name: "invalid unit",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{Limit: RateLimitValue{Requests: 10, Unit: "Week"}}},
				},
			},
			wantErr: true,
			errMsg:  "unit must be one of",
		},
		{
			name: "invalid source CIDR",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							SourceCIDR: &RateLimitSourceCIDR{Value: "not-a-cidr"},
						}},
					}},
				},
			},
			wantErr: true,
			errMsg:  "invalid CIDR or IP format",
		},
		{
			name: "valid IP as source CIDR",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							SourceCIDR: &RateLimitSourceCIDR{Value: "10.0.0.1"},
						}},
					}},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHealthCheckConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *HealthCheckConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{
			name:    "neither active nor passive",
			cfg:     &HealthCheckConfig{},
			wantErr: true,
			errMsg:  "at least one of active or passive",
		},
		{
			name: "valid active HTTP",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{
					Type: "HTTP",
					HTTP: &HTTPActiveHealthCheckConfig{Path: "/health"},
				},
			},
			wantErr: false,
		},
		{
			name: "active with invalid type",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{Type: "Bad"},
			},
			wantErr: true,
			errMsg:  "type must be HTTP, TCP, or GRPC",
		},
		{
			name: "active HTTP without path",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{Type: "HTTP"},
			},
			wantErr: true,
			errMsg:  "HTTP health check requires a path",
		},
		{
			name: "valid passive",
			cfg: &HealthCheckConfig{
				Passive: &PassiveHealthCheckConfig{
					Interval: strPtr("10s"),
				},
			},
			wantErr: false,
		},
		{
			name: "passive with invalid interval",
			cfg: &HealthCheckConfig{
				Passive: &PassiveHealthCheckConfig{
					Interval: strPtr("bad"),
				},
			},
			wantErr: true,
			errMsg:  "invalid interval duration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRequestBufferConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     RequestBufferConfig
		wantErr bool
	}{
		{name: "valid Ki", cfg: RequestBufferConfig{Limit: "4Ki"}, wantErr: false},
		{name: "valid Mi", cfg: RequestBufferConfig{Limit: "1Mi"}, wantErr: false},
		{name: "valid plain number", cfg: RequestBufferConfig{Limit: "1024"}, wantErr: false},
		{name: "empty", cfg: RequestBufferConfig{Limit: ""}, wantErr: true},
		{name: "invalid format", cfg: RequestBufferConfig{Limit: "abc"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBTPTimeoutConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *BTPTimeoutConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "empty", cfg: &BTPTimeoutConfig{}, wantErr: false},
		{
			name: "valid TCP timeout",
			cfg: &BTPTimeoutConfig{
				TCP: &BTPTCPTimeoutConfig{ConnectTimeout: "5s"},
			},
			wantErr: false,
		},
		{
			name: "invalid TCP timeout",
			cfg: &BTPTimeoutConfig{
				TCP: &BTPTCPTimeoutConfig{ConnectTimeout: "bad"},
			},
			wantErr: true,
			errMsg:  "invalid tcp.connectTimeout",
		},
		{
			name: "valid HTTP timeouts",
			cfg: &BTPTimeoutConfig{
				HTTP: &BTPHTTPTimeoutConfig{
					RequestTimeout:        "30s",
					ConnectionIdleTimeout: "60s",
					MaxConnectionDuration: "1h",
					MaxStreamDuration:     "5m",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid HTTP request timeout",
			cfg: &BTPTimeoutConfig{
				HTTP: &BTPHTTPTimeoutConfig{RequestTimeout: "bad"},
			},
			wantErr: true,
			errMsg:  "invalid http.requestTimeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResponseOverrideRule_Validate(t *testing.T) {
	val200 := 200
	tests := []struct {
		name    string
		rule    ResponseOverrideRule
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no status codes",
			rule:    ResponseOverrideRule{},
			wantErr: true,
			errMsg:  "statusCodes is required",
		},
		{
			name: "valid Value type",
			rule: ResponseOverrideRule{
				Match: ResponseOverrideMatch{
					StatusCodes: []StatusCodeMatch{{Type: "Value", Value: &val200}},
				},
				Response: ResponseOverrideResponse{
					ContentType: "text/plain",
					Body:        ResponseOverrideBody{Type: "Inline", Inline: "ok"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing content type",
			rule: ResponseOverrideRule{
				Match: ResponseOverrideMatch{
					StatusCodes: []StatusCodeMatch{{Type: "Value", Value: &val200}},
				},
				Response: ResponseOverrideResponse{
					Body: ResponseOverrideBody{Type: "Inline", Inline: "ok"},
				},
			},
			wantErr: true,
			errMsg:  "contentType is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStatusCodeMatch_Validate(t *testing.T) {
	val200 := 200
	val50 := 50
	tests := []struct {
		name    string
		scm     StatusCodeMatch
		wantErr bool
	}{
		{name: "valid value", scm: StatusCodeMatch{Type: "Value", Value: &val200}, wantErr: false},
		{name: "value out of range", scm: StatusCodeMatch{Type: "Value", Value: &val50}, wantErr: true},
		{name: "value nil", scm: StatusCodeMatch{Type: "Value"}, wantErr: true},
		{name: "valid range", scm: StatusCodeMatch{Type: "Range", Range: &StatusCodeRange{Start: 400, End: 499}}, wantErr: false},
		{name: "range start > end", scm: StatusCodeMatch{Type: "Range", Range: &StatusCodeRange{Start: 500, End: 400}}, wantErr: true},
		{name: "invalid type", scm: StatusCodeMatch{Type: "Bad"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.scm.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBackendTrafficPolicyConfig_ScanValue(t *testing.T) {
	cfg := BackendTrafficPolicyConfig{
		Retry: &RetryConfig{NumRetries: int32Ptr(3)},
	}

	// Test Value
	val, err := cfg.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Test Scan with valid bytes
	var scanned BackendTrafficPolicyConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, int32(3), *scanned.Retry.NumRetries)

	// Test Scan nil
	var nilScanned BackendTrafficPolicyConfig
	err = nilScanned.Scan(nil)
	assert.NoError(t, err)
	assert.True(t, nilScanned.IsEmpty())

	// Test Scan wrong type
	err = nilScanned.Scan(123)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type assertion")
}

func TestRetryConfig_Validate_BackoffEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *RetryConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "invalid backoff baseInterval format",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{
					BackOff: &BackOffPolicy{BaseInterval: strPtr("bad")},
				},
			},
			wantErr: true,
			errMsg:  "invalid backoff baseInterval",
		},
		{
			name: "non-positive backoff baseInterval",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{
					BackOff: &BackOffPolicy{BaseInterval: strPtr("0s")},
				},
			},
			wantErr: true,
			errMsg:  "backoff baseInterval must be positive",
		},
		{
			name: "invalid backoff maxInterval format",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{
					BackOff: &BackOffPolicy{MaxInterval: strPtr("bad")},
				},
			},
			wantErr: true,
			errMsg:  "invalid backoff maxInterval",
		},
		{
			name: "non-positive backoff maxInterval",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{
					BackOff: &BackOffPolicy{MaxInterval: strPtr("-1s")},
				},
			},
			wantErr: true,
			errMsg:  "backoff maxInterval must be positive",
		},
		{
			name: "valid per-retry timeout",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{Timeout: strPtr("1s")},
			},
			wantErr: false,
		},
		{
			name: "valid retryOn with HTTP codes",
			cfg: &RetryConfig{
				RetryOn: &RetryOn{HTTPStatusCodes: []int{502, 503}},
			},
			wantErr: false,
		},
		{
			name: "low HTTP status code in retryOn",
			cfg: &RetryConfig{
				RetryOn: &RetryOn{HTTPStatusCodes: []int{50}},
			},
			wantErr: true,
			errMsg:  "invalid HTTP status code",
		},
		{
			name:    "zero numRetries is valid",
			cfg:     &RetryConfig{NumRetries: int32Ptr(0)},
			wantErr: false,
		},
		{
			name: "only base interval set (no max)",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{
					BackOff: &BackOffPolicy{BaseInterval: strPtr("100ms")},
				},
			},
			wantErr: false,
		},
		{
			name: "only max interval set (no base)",
			cfg: &RetryConfig{
				PerRetryPolicy: &PerRetryPolicy{
					BackOff: &BackOffPolicy{MaxInterval: strPtr("1s")},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFaultInjectionConfig_Validate_EdgeCases(t *testing.T) {
	pctNeg := float32(-1)
	pct100 := float32(100)
	pct0 := float32(0)
	http99 := 99
	http600 := 600
	grpc17 := 17
	grpcNeg := -1

	tests := []struct {
		name    string
		cfg     *FaultInjectionConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "delay invalid fixedDelay format",
			cfg: &FaultInjectionConfig{
				Delay: &FaultInjectionDelayConfig{FixedDelay: "bad"},
			},
			wantErr: true,
			errMsg:  "invalid fixedDelay duration",
		},
		{
			name: "delay negative percentage",
			cfg: &FaultInjectionConfig{
				Delay: &FaultInjectionDelayConfig{FixedDelay: "1s", Percentage: &pctNeg},
			},
			wantErr: true,
			errMsg:  "percentage must be between 0 and 100",
		},
		{
			name: "delay percentage at 100",
			cfg: &FaultInjectionConfig{
				Delay: &FaultInjectionDelayConfig{FixedDelay: "1s", Percentage: &pct100},
			},
			wantErr: false,
		},
		{
			name: "delay percentage at 0",
			cfg: &FaultInjectionConfig{
				Delay: &FaultInjectionDelayConfig{FixedDelay: "1s", Percentage: &pct0},
			},
			wantErr: false,
		},
		{
			name: "abort httpStatus too low",
			cfg: &FaultInjectionConfig{
				Abort: &FaultInjectionAbortConfig{HTTPStatus: &http99},
			},
			wantErr: true,
			errMsg:  "httpStatus must be between 100 and 599",
		},
		{
			name: "abort httpStatus too high",
			cfg: &FaultInjectionConfig{
				Abort: &FaultInjectionAbortConfig{HTTPStatus: &http600},
			},
			wantErr: true,
			errMsg:  "httpStatus must be between 100 and 599",
		},
		{
			name: "abort grpcStatus too high",
			cfg: &FaultInjectionConfig{
				Abort: &FaultInjectionAbortConfig{GRPCStatus: &grpc17},
			},
			wantErr: true,
			errMsg:  "grpcStatus must be between 0 and 16",
		},
		{
			name: "abort grpcStatus negative",
			cfg: &FaultInjectionConfig{
				Abort: &FaultInjectionAbortConfig{GRPCStatus: &grpcNeg},
			},
			wantErr: true,
			errMsg:  "grpcStatus must be between 0 and 16",
		},
		{
			name: "abort with invalid percentage",
			cfg: &FaultInjectionConfig{
				Abort: &FaultInjectionAbortConfig{HTTPStatus: intPtr2(500), Percentage: &pctNeg},
			},
			wantErr: true,
			errMsg:  "percentage must be between 0 and 100",
		},
		{
			name: "both delay and abort valid",
			cfg: &FaultInjectionConfig{
				Delay: &FaultInjectionDelayConfig{FixedDelay: "100ms"},
				Abort: &FaultInjectionAbortConfig{HTTPStatus: intPtr2(503)},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func intPtr2(i int) *int { return &i }

func TestHealthCheckConfig_Validate_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *HealthCheckConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "active type empty",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{},
			},
			wantErr: true,
			errMsg:  "type is required",
		},
		{
			name: "active TCP type",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{Type: "TCP"},
			},
			wantErr: false,
		},
		{
			name: "active GRPC type",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{Type: "GRPC"},
			},
			wantErr: false,
		},
		{
			name: "active HTTP invalid method",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{
					Type: "HTTP",
					HTTP: &HTTPActiveHealthCheckConfig{
						Path:   "/health",
						Method: strPtr("INVALID"),
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid HTTP method",
		},
		{
			name: "active HTTP valid method",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{
					Type: "HTTP",
					HTTP: &HTTPActiveHealthCheckConfig{
						Path:   "/health",
						Method: strPtr("GET"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "active HTTP expected status out of range",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{
					Type: "HTTP",
					HTTP: &HTTPActiveHealthCheckConfig{
						Path:             "/health",
						ExpectedStatuses: []int{200, 700},
					},
				},
			},
			wantErr: true,
			errMsg:  "expected status must be 100-599",
		},
		{
			name: "active with invalid timeout",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{
					Type:    "TCP",
					Timeout: strPtr("bad"),
				},
			},
			wantErr: true,
			errMsg:  "invalid timeout duration",
		},
		{
			name: "active with invalid interval",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{
					Type:     "TCP",
					Interval: strPtr("bad"),
				},
			},
			wantErr: true,
			errMsg:  "invalid interval duration",
		},
		{
			name: "active with valid timeout and interval",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{
					Type:     "TCP",
					Timeout:  strPtr("5s"),
					Interval: strPtr("10s"),
				},
			},
			wantErr: false,
		},
		{
			name: "passive with invalid base ejection time",
			cfg: &HealthCheckConfig{
				Passive: &PassiveHealthCheckConfig{
					BaseEjectionTime: strPtr("bad"),
				},
			},
			wantErr: true,
			errMsg:  "invalid base ejection time",
		},
		{
			name: "passive with valid config",
			cfg: &HealthCheckConfig{
				Passive: &PassiveHealthCheckConfig{
					Interval:         strPtr("10s"),
					BaseEjectionTime: strPtr("30s"),
				},
			},
			wantErr: false,
		},
		{
			name: "both active and passive",
			cfg: &HealthCheckConfig{
				Active: &ActiveHealthCheckConfig{
					Type: "TCP",
				},
				Passive: &PassiveHealthCheckConfig{
					Interval: strPtr("10s"),
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRateLimitConfig_Validate_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *RateLimitConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "header missing name",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							Headers: []RateLimitHeaderMatch{{Name: "", Type: "Exact"}},
						}},
					}},
				},
			},
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name: "header invalid type",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							Headers: []RateLimitHeaderMatch{{Name: "X-User", Type: "Prefix"}},
						}},
					}},
				},
			},
			wantErr: true,
			errMsg:  "type must be 'Exact' or 'Distinct'",
		},
		{
			name: "header valid Distinct type",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							Headers: []RateLimitHeaderMatch{{Name: "X-User", Type: "Distinct"}},
						}},
					}},
				},
			},
			wantErr: false,
		},
		{
			name: "sourceCIDR empty value",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							SourceCIDR: &RateLimitSourceCIDR{Value: ""},
						}},
					}},
				},
			},
			wantErr: true,
			errMsg:  "value is required",
		},
		{
			name: "sourceCIDR invalid type",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							SourceCIDR: &RateLimitSourceCIDR{Value: "10.0.0.0/8", Type: "Prefix"},
						}},
					}},
				},
			},
			wantErr: true,
			errMsg:  "type must be 'Exact' or 'Distinct'",
		},
		{
			name: "valid CIDR format",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							SourceCIDR: &RateLimitSourceCIDR{Value: "10.0.0.0/8", Type: "Exact"},
						}},
					}},
				},
			},
			wantErr: false,
		},
		{
			name: "path empty value",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							Path: &RateLimitPathMatch{Value: "", Type: "Exact"},
						}},
					}},
				},
			},
			wantErr: true,
			errMsg:  "value is required",
		},
		{
			name: "path invalid type",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							Path: &RateLimitPathMatch{Value: "/api", Type: "Invalid"},
						}},
					}},
				},
			},
			wantErr: true,
			errMsg:  "type must be 'Exact', 'PathPrefix', or 'RegularExpression'",
		},
		{
			name: "path valid PathPrefix",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							Path: &RateLimitPathMatch{Value: "/api", Type: "PathPrefix"},
						}},
					}},
				},
			},
			wantErr: false,
		},
		{
			name: "path valid RegularExpression",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{{
						Limit: RateLimitValue{Requests: 10, Unit: "Second"},
						ClientSelectors: []RateLimitSelector{{
							Path: &RateLimitPathMatch{Value: "/api/.*", Type: "RegularExpression"},
						}},
					}},
				},
			},
			wantErr: false,
		},
		{
			name: "all valid units",
			cfg: &RateLimitConfig{
				Global: &GlobalRateLimitConfig{
					Rules: []RateLimitRule{
						{Limit: RateLimitValue{Requests: 1, Unit: "Second"}},
						{Limit: RateLimitValue{Requests: 1, Unit: "Hour"}},
						{Limit: RateLimitValue{Requests: 1, Unit: "Day"}},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRateLimitConfig_ScanValue(t *testing.T) {
	cfg := RateLimitConfig{
		Global: &GlobalRateLimitConfig{
			Rules: []RateLimitRule{{Limit: RateLimitValue{Requests: 10, Unit: "Second"}}},
		},
	}

	val, err := cfg.Value()
	assert.NoError(t, err)

	var scanned RateLimitConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, 10, scanned.Global.Rules[0].Limit.Requests)

	// Scan nil
	err = scanned.Scan(nil)
	assert.NoError(t, err)

	// Scan wrong type
	err = scanned.Scan(123)
	assert.Error(t, err)
}

func TestBTPTimeoutConfig_Validate_MoreEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *BTPTimeoutConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "invalid HTTP connectionIdleTimeout",
			cfg: &BTPTimeoutConfig{
				HTTP: &BTPHTTPTimeoutConfig{ConnectionIdleTimeout: "bad"},
			},
			wantErr: true,
			errMsg:  "invalid http.connectionIdleTimeout",
		},
		{
			name: "invalid HTTP maxConnectionDuration",
			cfg: &BTPTimeoutConfig{
				HTTP: &BTPHTTPTimeoutConfig{MaxConnectionDuration: "bad"},
			},
			wantErr: true,
			errMsg:  "invalid http.maxConnectionDuration",
		},
		{
			name: "invalid HTTP maxStreamDuration",
			cfg: &BTPTimeoutConfig{
				HTTP: &BTPHTTPTimeoutConfig{MaxStreamDuration: "bad"},
			},
			wantErr: true,
			errMsg:  "invalid http.maxStreamDuration",
		},
		{
			name: "TCP with empty connect timeout is valid",
			cfg: &BTPTimeoutConfig{
				TCP: &BTPTCPTimeoutConfig{ConnectTimeout: ""},
			},
			wantErr: false,
		},
		{
			name: "HTTP with empty durations is valid",
			cfg: &BTPTimeoutConfig{
				HTTP: &BTPHTTPTimeoutConfig{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResponseOverrideRule_Validate_EdgeCases(t *testing.T) {
	val200 := 200
	tests := []struct {
		name    string
		rule    ResponseOverrideRule
		wantErr bool
		errMsg  string
	}{
		{
			name: "missing body type",
			rule: ResponseOverrideRule{
				Match: ResponseOverrideMatch{
					StatusCodes: []StatusCodeMatch{{Type: "Value", Value: &val200}},
				},
				Response: ResponseOverrideResponse{
					ContentType: "text/plain",
					Body:        ResponseOverrideBody{Type: ""},
				},
			},
			wantErr: true,
			errMsg:  "body.type is required",
		},
		{
			name: "Inline type with empty body",
			rule: ResponseOverrideRule{
				Match: ResponseOverrideMatch{
					StatusCodes: []StatusCodeMatch{{Type: "Value", Value: &val200}},
				},
				Response: ResponseOverrideResponse{
					ContentType: "text/plain",
					Body:        ResponseOverrideBody{Type: "Inline", Inline: ""},
				},
			},
			wantErr: true,
			errMsg:  "body.inline is required when type is Inline",
		},
		{
			name: "ValueRef type without valueRef",
			rule: ResponseOverrideRule{
				Match: ResponseOverrideMatch{
					StatusCodes: []StatusCodeMatch{{Type: "Value", Value: &val200}},
				},
				Response: ResponseOverrideResponse{
					ContentType: "text/plain",
					Body:        ResponseOverrideBody{Type: "ValueRef"},
				},
			},
			wantErr: true,
			errMsg:  "body.valueRef is required when type is ValueRef",
		},
		{
			name: "valid Range status code match",
			rule: ResponseOverrideRule{
				Match: ResponseOverrideMatch{
					StatusCodes: []StatusCodeMatch{{Type: "Range", Range: &StatusCodeRange{Start: 500, End: 599}}},
				},
				Response: ResponseOverrideResponse{
					ContentType: "text/plain",
					Body:        ResponseOverrideBody{Type: "Inline", Inline: "error"},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStatusCodeMatch_Validate_EdgeCases(t *testing.T) {
	val100 := 100
	val599 := 599
	tests := []struct {
		name    string
		scm     StatusCodeMatch
		wantErr bool
		errMsg  string
	}{
		{
			name:    "value at lower boundary",
			scm:     StatusCodeMatch{Type: "Value", Value: &val100},
			wantErr: false,
		},
		{
			name:    "value at upper boundary",
			scm:     StatusCodeMatch{Type: "Value", Value: &val599},
			wantErr: false,
		},
		{
			name:    "range nil",
			scm:     StatusCodeMatch{Type: "Range"},
			wantErr: true,
			errMsg:  "range is required",
		},
		{
			name:    "range start too low",
			scm:     StatusCodeMatch{Type: "Range", Range: &StatusCodeRange{Start: 50, End: 200}},
			wantErr: true,
			errMsg:  "range.start must be between 100-599",
		},
		{
			name:    "range end too high",
			scm:     StatusCodeMatch{Type: "Range", Range: &StatusCodeRange{Start: 200, End: 700}},
			wantErr: true,
			errMsg:  "range.end must be between 100-599",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.scm.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Helper functions
func strPtr(s string) *string { return &s }
func int32Ptr(i int32) *int32 { return &i }
func int64Ptr(i int64) *int64 { return &i }

func init() {
	// Ensure time package is used (for backoff validation tests that use ParseDuration indirectly)
	_ = time.Second
}

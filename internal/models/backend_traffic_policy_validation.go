package models

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"time"
)

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

package routeplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// MapRateLimitConfigToPolicy converts model RateLimitConfig to k8s-side RateLimitPolicyConfig
func MapRateLimitConfigToPolicy(rl *models.RateLimitConfig) *kubernetes.RateLimitPolicyConfig {
	if rl == nil || rl.Global == nil {
		return nil
	}

	rules := make([]kubernetes.RateLimitRulePolicyConfig, 0, len(rl.Global.Rules))
	for _, rule := range rl.Global.Rules {
		policyRule := kubernetes.RateLimitRulePolicyConfig{
			Limit: kubernetes.RateLimitValuePolicyConfig{
				Requests: rule.Limit.Requests,
				Unit:     rule.Limit.Unit,
			},
		}
		if len(rule.ClientSelectors) > 0 {
			selectors := make([]kubernetes.RateLimitSelectorPolicyConfig, 0, len(rule.ClientSelectors))
			for _, sel := range rule.ClientSelectors {
				policySel := kubernetes.RateLimitSelectorPolicyConfig{}
				if len(sel.Headers) > 0 {
					headers := make([]kubernetes.RateLimitHeaderMatchPolicyConfig, 0, len(sel.Headers))
					for _, h := range sel.Headers {
						headers = append(headers, kubernetes.RateLimitHeaderMatchPolicyConfig{
							Name:   h.Name,
							Value:  h.Value,
							Type:   h.Type,
							Invert: h.Invert,
						})
					}
					policySel.Headers = headers
				}
				if sel.SourceCIDR != nil {
					policySel.SourceCIDR = &kubernetes.RateLimitSourceCIDRPolicyConfig{
						Value: sel.SourceCIDR.Value,
						Type:  sel.SourceCIDR.Type,
					}
				}
				if sel.Path != nil {
					policySel.Path = &kubernetes.RateLimitPathMatchPolicyConfig{
						Value: sel.Path.Value,
						Type:  sel.Path.Type,
					}
				}
				if len(sel.Methods) > 0 {
					policySel.Methods = sel.Methods
				}
				selectors = append(selectors, policySel)
			}
			policyRule.ClientSelectors = selectors
		}
		rules = append(rules, policyRule)
	}

	return &kubernetes.RateLimitPolicyConfig{
		Global: &kubernetes.GlobalRateLimitPolicyConfig{
			Rules: rules,
		},
	}
}

// MapCircuitBreakerConfigToPolicy converts model CircuitBreakerConfig to k8s-side CircuitBreakerPolicyConfig
func MapCircuitBreakerConfigToPolicy(cb *models.CircuitBreakerConfig) *kubernetes.CircuitBreakerPolicyConfig {
	if cb == nil {
		return nil
	}
	return &kubernetes.CircuitBreakerPolicyConfig{
		MaxConnections:           cb.MaxConnections,
		MaxPendingRequests:       cb.MaxPendingRequests,
		MaxParallelRequests:      cb.MaxParallelRequests,
		MaxParallelRetries:       cb.MaxParallelRetries,
		MaxRequestsPerConnection: cb.MaxRequestsPerConnection,
	}
}

// MapHealthCheckConfigToPolicy converts model HealthCheckConfig to k8s-side HealthCheckPolicyConfig
func MapHealthCheckConfigToPolicy(hc *models.HealthCheckConfig) *kubernetes.HealthCheckPolicyConfig {
	if hc == nil {
		return nil
	}
	result := &kubernetes.HealthCheckPolicyConfig{
		PanicThreshold: hc.PanicThreshold,
	}
	if hc.Active != nil {
		result.Active = &kubernetes.ActiveHealthCheckPolicyConfig{
			Timeout:            hc.Active.Timeout,
			Interval:           hc.Active.Interval,
			UnhealthyThreshold: hc.Active.UnhealthyThreshold,
			HealthyThreshold:   hc.Active.HealthyThreshold,
			Type:               hc.Active.Type,
		}
		// Always create the type-specific config based on Type, as the CRD requires it
		switch hc.Active.Type {
		case "HTTP":
			if hc.Active.HTTP != nil {
				result.Active.HTTP = &kubernetes.HTTPActiveHealthCheckPolicyConfig{
					Path:             hc.Active.HTTP.Path,
					Method:           hc.Active.HTTP.Method,
					ExpectedStatuses: hc.Active.HTTP.ExpectedStatuses,
				}
			} else {
				result.Active.HTTP = &kubernetes.HTTPActiveHealthCheckPolicyConfig{}
			}
		case "TCP":
			result.Active.TCP = &kubernetes.TCPActiveHealthCheckPolicyConfig{}
			if hc.Active.TCP != nil {
				if hc.Active.TCP.Send != nil && hc.Active.TCP.Send.Text != nil {
					result.Active.TCP.SendText = hc.Active.TCP.Send.Text
				}
				if hc.Active.TCP.Receive != nil && hc.Active.TCP.Receive.Text != nil {
					result.Active.TCP.ReceiveText = hc.Active.TCP.Receive.Text
				}
			}
		case "GRPC":
			if hc.Active.GRPC != nil {
				result.Active.GRPC = &kubernetes.GRPCActiveHealthCheckPolicyConfig{
					Service: hc.Active.GRPC.Service,
				}
			} else {
				result.Active.GRPC = &kubernetes.GRPCActiveHealthCheckPolicyConfig{}
			}
		}
	}
	if hc.Passive != nil {
		result.Passive = &kubernetes.PassiveHealthCheckPolicyConfig{
			ConsecutiveGatewayErrors:       hc.Passive.ConsecutiveGatewayErrors,
			Consecutive5xxErrors:           hc.Passive.Consecutive5xxErrors,
			ConsecutiveLocalOriginFailures: hc.Passive.ConsecutiveLocalOriginFailures,
			Interval:                       hc.Passive.Interval,
			BaseEjectionTime:               hc.Passive.BaseEjectionTime,
			MaxEjectionPercent:             hc.Passive.MaxEjectionPercent,
			SplitExternalLocalOriginErrors: hc.Passive.SplitExternalLocalOriginErrors,
		}
	}
	return result
}

// MapFaultInjectionConfigToPolicy converts model FaultInjectionConfig to k8s-side FaultInjectionPolicyConfig
func MapFaultInjectionConfigToPolicy(fi *models.FaultInjectionConfig) *kubernetes.FaultInjectionPolicyConfig {
	if fi == nil {
		return nil
	}
	result := &kubernetes.FaultInjectionPolicyConfig{}
	if fi.Delay != nil {
		result.Delay = &kubernetes.FaultInjectionDelayPolicyConfig{
			FixedDelay: fi.Delay.FixedDelay,
			Percentage: fi.Delay.Percentage,
		}
	}
	if fi.Abort != nil {
		result.Abort = &kubernetes.FaultInjectionAbortPolicyConfig{
			HTTPStatus: fi.Abort.HTTPStatus,
			GRPCStatus: fi.Abort.GRPCStatus,
			Percentage: fi.Abort.Percentage,
		}
	}
	return result
}

// MapLoadBalancerConfigToPolicy converts model LoadBalancerConfig to k8s-side LoadBalancerPolicyConfig
func MapLoadBalancerConfigToPolicy(lb *models.LoadBalancerConfig) *kubernetes.LoadBalancerPolicyConfig {
	if lb == nil {
		return nil
	}

	result := &kubernetes.LoadBalancerPolicyConfig{
		Type: string(lb.Type),
	}

	if lb.ConsistentHash != nil {
		result.ConsistentHash = &kubernetes.ConsistentHashPolicyConfig{
			Type: string(lb.ConsistentHash.Type),
		}
		if lb.ConsistentHash.Header != nil {
			result.ConsistentHash.Header = &kubernetes.ConsistentHashHeaderPolicyConfig{
				Name: lb.ConsistentHash.Header.Name,
			}
		}
		if lb.ConsistentHash.Cookie != nil {
			result.ConsistentHash.Cookie = &kubernetes.ConsistentHashCookiePolicyConfig{
				Name:       lb.ConsistentHash.Cookie.Name,
				TTL:        lb.ConsistentHash.Cookie.TTL,
				Attributes: lb.ConsistentHash.Cookie.Attributes,
			}
		}
	}

	return result
}

// MapRetryConfigToPolicy converts model RetryConfig to k8s-side RetryPolicyConfig
func MapRetryConfigToPolicy(retry *models.RetryConfig) *kubernetes.RetryPolicyConfig {
	if retry == nil {
		return nil
	}

	result := &kubernetes.RetryPolicyConfig{
		NumRetries: retry.NumRetries,
	}

	if retry.RetryOn != nil {
		result.RetryOn = &kubernetes.RetryOnPolicyConfig{
			HTTPStatusCodes: retry.RetryOn.HTTPStatusCodes,
			Triggers:        retry.RetryOn.Triggers,
		}
	}

	if retry.PerRetryPolicy != nil {
		result.PerRetry = &kubernetes.PerRetryPolicyConfig{
			Timeout: retry.PerRetryPolicy.Timeout,
		}
		if retry.PerRetryPolicy.BackOff != nil {
			result.PerRetry.BackOff = &kubernetes.BackOffPolicyConfig{
				BaseInterval: retry.PerRetryPolicy.BackOff.BaseInterval,
				MaxInterval:  retry.PerRetryPolicy.BackOff.MaxInterval,
			}
		}
	}

	return result
}

// MapResponseOverrideToPolicy converts model ResponseOverrideRule to policy config
func MapResponseOverrideToPolicy(rules []models.ResponseOverrideRule) []kubernetes.ResponseOverridePolicyConfig {
	result := make([]kubernetes.ResponseOverridePolicyConfig, 0, len(rules))
	for _, rule := range rules {
		statusCodes := make([]kubernetes.StatusCodeMatchPolicyConfig, 0, len(rule.Match.StatusCodes))
		for _, sc := range rule.Match.StatusCodes {
			match := kubernetes.StatusCodeMatchPolicyConfig{Type: sc.Type, Value: sc.Value}
			if sc.Range != nil {
				match.Range = &kubernetes.StatusCodeRangePolicyConfig{Start: sc.Range.Start, End: sc.Range.End}
			}
			statusCodes = append(statusCodes, match)
		}

		body := kubernetes.ResponseOverrideBodyPolicyConfig{Type: rule.Response.Body.Type, Inline: rule.Response.Body.Inline}
		if rule.Response.Body.ValueRef != nil {
			body.ValueRef = &kubernetes.ValueRefPolicyConfig{
				Group:     rule.Response.Body.ValueRef.Group,
				Kind:      rule.Response.Body.ValueRef.Kind,
				Name:      rule.Response.Body.ValueRef.Name,
				Namespace: rule.Response.Body.ValueRef.Namespace,
			}
		}

		result = append(result, kubernetes.ResponseOverridePolicyConfig{
			Match:    kubernetes.ResponseOverrideMatchPolicyConfig{StatusCodes: statusCodes},
			Response: kubernetes.ResponseOverrideResponsePolicyConfig{ContentType: rule.Response.ContentType, Body: body},
		})
	}
	return result
}

// MapTimeoutConfigToPolicy converts model BTPTimeoutConfig to k8s-side BTPTimeoutPolicyConfig
func MapTimeoutConfigToPolicy(t *models.BTPTimeoutConfig) *kubernetes.BTPTimeoutPolicyConfig {
	if t == nil {
		return nil
	}
	result := &kubernetes.BTPTimeoutPolicyConfig{}
	if t.TCP != nil {
		result.TCP = &kubernetes.BTPTCPTimeoutPolicyConfig{
			ConnectTimeout: t.TCP.ConnectTimeout,
		}
	}
	if t.HTTP != nil {
		result.HTTP = &kubernetes.BTPHTTPTimeoutPolicyConfig{
			RequestTimeout:        t.HTTP.RequestTimeout,
			ConnectionIdleTimeout: t.HTTP.ConnectionIdleTimeout,
			MaxConnectionDuration: t.HTTP.MaxConnectionDuration,
			MaxStreamDuration:     t.HTTP.MaxStreamDuration,
		}
	}
	return result
}

// MapBackendTrafficPolicyConfigToInput converts a stored BackendTrafficPolicyConfig back to BackendTrafficPolicyInput
func MapBackendTrafficPolicyConfigToInput(cfg *models.BackendTrafficPolicyConfig) *BackendTrafficPolicyInput {
	return &BackendTrafficPolicyInput{
		Compression:      cfg.Compression,
		Retry:            cfg.Retry,
		LoadBalancer:     cfg.LoadBalancer,
		CircuitBreaker:   cfg.CircuitBreaker,
		HealthCheck:      cfg.HealthCheck,
		FaultInjection:   cfg.FaultInjection,
		RateLimit:        cfg.RateLimit,
		RequestBuffer:    cfg.RequestBuffer,
		ResponseOverride: cfg.ResponseOverride,
		Timeout:          cfg.Timeout,
	}
}

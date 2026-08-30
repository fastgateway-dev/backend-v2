package kubernetes

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// BuildBackendTrafficPolicy builds an Envoy Gateway BackendTrafficPolicy from config
func BuildBackendTrafficPolicy(config *BackendTrafficPolicyConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	spec := map[string]interface{}{
		"targetRef": map[string]interface{}{
			"group": config.TargetRef.Group,
			"kind":  config.TargetRef.Kind,
			"name":  config.TargetRef.Name,
		},
	}

	// Add compression configuration if present
	if len(config.Compression) > 0 {
		compressorArray := make([]interface{}, 0, len(config.Compression))
		for _, comp := range config.Compression {
			compEntry := map[string]interface{}{
				"type": comp.Type,
			}
			// Add type-specific config (empty objects for now)
			switch comp.Type {
			case "Gzip":
				compEntry["gzip"] = map[string]interface{}{}
			case "Brotli":
				compEntry["brotli"] = map[string]interface{}{}
			case "Zstd":
				compEntry["zstd"] = map[string]interface{}{}
			}
			compressorArray = append(compressorArray, compEntry)
		}
		spec["compressor"] = compressorArray
	}

	// Add retry configuration if present
	if config.Retry != nil {
		retry := map[string]interface{}{}

		if config.Retry.NumRetries != nil {
			retry["numRetries"] = *config.Retry.NumRetries
		}

		if config.Retry.RetryOn != nil {
			retryOn := map[string]interface{}{}
			if len(config.Retry.RetryOn.HTTPStatusCodes) > 0 {
				codes := make([]interface{}, len(config.Retry.RetryOn.HTTPStatusCodes))
				for i, code := range config.Retry.RetryOn.HTTPStatusCodes {
					codes[i] = int64(code)
				}
				retryOn["httpStatusCodes"] = codes
			}
			if len(config.Retry.RetryOn.Triggers) > 0 {
				triggers := make([]interface{}, len(config.Retry.RetryOn.Triggers))
				for i, t := range config.Retry.RetryOn.Triggers {
					triggers[i] = t
				}
				retryOn["triggers"] = triggers
			}
			if len(retryOn) > 0 {
				retry["retryOn"] = retryOn
			}
		}

		if config.Retry.PerRetry != nil {
			perRetry := map[string]interface{}{}
			if config.Retry.PerRetry.Timeout != nil {
				perRetry["timeout"] = *config.Retry.PerRetry.Timeout
			}
			if config.Retry.PerRetry.BackOff != nil {
				backOff := map[string]interface{}{}
				if config.Retry.PerRetry.BackOff.BaseInterval != nil {
					backOff["baseInterval"] = *config.Retry.PerRetry.BackOff.BaseInterval
				}
				if config.Retry.PerRetry.BackOff.MaxInterval != nil {
					backOff["maxInterval"] = *config.Retry.PerRetry.BackOff.MaxInterval
				}
				if len(backOff) > 0 {
					perRetry["backOff"] = backOff
				}
			}
			if len(perRetry) > 0 {
				retry["perRetry"] = perRetry
			}
		}

		if len(retry) > 0 {
			spec["retry"] = retry
		}
	}

	// Add load balancer configuration if present
	if config.LoadBalancer != nil {
		lb := map[string]interface{}{
			"type": config.LoadBalancer.Type,
		}
		if config.LoadBalancer.ConsistentHash != nil {
			ch := map[string]interface{}{
				"type": config.LoadBalancer.ConsistentHash.Type,
			}
			if config.LoadBalancer.ConsistentHash.Header != nil {
				ch["header"] = map[string]interface{}{
					"name": config.LoadBalancer.ConsistentHash.Header.Name,
				}
			}
			if config.LoadBalancer.ConsistentHash.Cookie != nil {
				cookie := map[string]interface{}{
					"name": config.LoadBalancer.ConsistentHash.Cookie.Name,
				}
				if config.LoadBalancer.ConsistentHash.Cookie.TTL != nil {
					cookie["ttl"] = *config.LoadBalancer.ConsistentHash.Cookie.TTL
				}
				if len(config.LoadBalancer.ConsistentHash.Cookie.Attributes) > 0 {
					attrs := make(map[string]interface{})
					for k, v := range config.LoadBalancer.ConsistentHash.Cookie.Attributes {
						attrs[k] = v
					}
					cookie["attributes"] = attrs
				}
				ch["cookie"] = cookie
			}
			lb["consistentHash"] = ch
		}
		spec["loadBalancer"] = lb
	}

	// Add circuit breaker configuration if present
	if config.CircuitBreaker != nil {
		cb := map[string]interface{}{}
		if config.CircuitBreaker.MaxConnections != nil {
			cb["maxConnections"] = *config.CircuitBreaker.MaxConnections
		}
		if config.CircuitBreaker.MaxPendingRequests != nil {
			cb["maxPendingRequests"] = *config.CircuitBreaker.MaxPendingRequests
		}
		if config.CircuitBreaker.MaxParallelRequests != nil {
			cb["maxParallelRequests"] = *config.CircuitBreaker.MaxParallelRequests
		}
		if config.CircuitBreaker.MaxParallelRetries != nil {
			cb["maxParallelRetries"] = *config.CircuitBreaker.MaxParallelRetries
		}
		if config.CircuitBreaker.MaxRequestsPerConnection != nil {
			cb["maxRequestsPerConnection"] = *config.CircuitBreaker.MaxRequestsPerConnection
		}
		if len(cb) > 0 {
			spec["circuitBreaker"] = cb
		}
	}

	// Add health check configuration if present
	if config.HealthCheck != nil {
		hc := map[string]interface{}{}

		if config.HealthCheck.Active != nil {
			active := map[string]interface{}{
				"type": config.HealthCheck.Active.Type,
			}
			if config.HealthCheck.Active.Timeout != nil {
				active["timeout"] = *config.HealthCheck.Active.Timeout
			}
			if config.HealthCheck.Active.Interval != nil {
				active["interval"] = *config.HealthCheck.Active.Interval
			}
			if config.HealthCheck.Active.UnhealthyThreshold != nil {
				active["unhealthyThreshold"] = *config.HealthCheck.Active.UnhealthyThreshold
			}
			if config.HealthCheck.Active.HealthyThreshold != nil {
				active["healthyThreshold"] = *config.HealthCheck.Active.HealthyThreshold
			}
			// CRD requires the type-specific field to be present based on the type value
			switch config.HealthCheck.Active.Type {
			case "HTTP":
				http := map[string]interface{}{}
				if config.HealthCheck.Active.HTTP != nil {
					http["path"] = config.HealthCheck.Active.HTTP.Path
					if config.HealthCheck.Active.HTTP.Method != nil {
						http["method"] = *config.HealthCheck.Active.HTTP.Method
					}
					if len(config.HealthCheck.Active.HTTP.ExpectedStatuses) > 0 {
						statuses := make([]interface{}, len(config.HealthCheck.Active.HTTP.ExpectedStatuses))
						for i, s := range config.HealthCheck.Active.HTTP.ExpectedStatuses {
							statuses[i] = int64(s)
						}
						http["expectedStatuses"] = statuses
					}
				}
				active["http"] = http
			case "TCP":
				tcp := map[string]interface{}{}
				if config.HealthCheck.Active.TCP != nil {
					if config.HealthCheck.Active.TCP.SendText != nil {
						tcp["send"] = map[string]interface{}{
							"type": "Text",
							"text": *config.HealthCheck.Active.TCP.SendText,
						}
					}
					if config.HealthCheck.Active.TCP.ReceiveText != nil {
						tcp["receive"] = map[string]interface{}{
							"type": "Text",
							"text": *config.HealthCheck.Active.TCP.ReceiveText,
						}
					}
				}
				active["tcp"] = tcp
			case "GRPC":
				grpc := map[string]interface{}{}
				if config.HealthCheck.Active.GRPC != nil {
					if config.HealthCheck.Active.GRPC.Service != nil {
						grpc["service"] = *config.HealthCheck.Active.GRPC.Service
					}
				}
				active["grpc"] = grpc
			}
			hc["active"] = active
		}

		if config.HealthCheck.Passive != nil {
			passive := map[string]interface{}{}
			if config.HealthCheck.Passive.ConsecutiveGatewayErrors != nil {
				passive["consecutiveGatewayErrors"] = *config.HealthCheck.Passive.ConsecutiveGatewayErrors
			}
			if config.HealthCheck.Passive.Consecutive5xxErrors != nil {
				passive["consecutive5XxErrors"] = *config.HealthCheck.Passive.Consecutive5xxErrors
			}
			if config.HealthCheck.Passive.ConsecutiveLocalOriginFailures != nil {
				passive["consecutiveLocalOriginFailures"] = *config.HealthCheck.Passive.ConsecutiveLocalOriginFailures
			}
			if config.HealthCheck.Passive.Interval != nil {
				passive["interval"] = *config.HealthCheck.Passive.Interval
			}
			if config.HealthCheck.Passive.BaseEjectionTime != nil {
				passive["baseEjectionTime"] = *config.HealthCheck.Passive.BaseEjectionTime
			}
			if config.HealthCheck.Passive.MaxEjectionPercent != nil {
				passive["maxEjectionPercent"] = *config.HealthCheck.Passive.MaxEjectionPercent
			}
			if config.HealthCheck.Passive.SplitExternalLocalOriginErrors != nil {
				passive["splitExternalLocalOriginErrors"] = *config.HealthCheck.Passive.SplitExternalLocalOriginErrors
			}
			if len(passive) > 0 {
				hc["passive"] = passive
			}
		}

		if config.HealthCheck.PanicThreshold != nil {
			hc["panicThreshold"] = *config.HealthCheck.PanicThreshold
		}

		if len(hc) > 0 {
			spec["healthCheck"] = hc
		}
	}

	// Add fault injection configuration if present
	if config.FaultInjection != nil {
		fi := map[string]interface{}{}

		if config.FaultInjection.Delay != nil {
			delay := map[string]interface{}{
				"fixedDelay": config.FaultInjection.Delay.FixedDelay,
			}
			if config.FaultInjection.Delay.Percentage != nil {
				delay["percentage"] = *config.FaultInjection.Delay.Percentage
			}
			fi["delay"] = delay
		}

		if config.FaultInjection.Abort != nil {
			abort := map[string]interface{}{}
			if config.FaultInjection.Abort.HTTPStatus != nil {
				abort["httpStatus"] = *config.FaultInjection.Abort.HTTPStatus
			}
			if config.FaultInjection.Abort.GRPCStatus != nil {
				abort["grpcStatus"] = *config.FaultInjection.Abort.GRPCStatus
			}
			if config.FaultInjection.Abort.Percentage != nil {
				abort["percentage"] = *config.FaultInjection.Abort.Percentage
			}
			fi["abort"] = abort
		}

		if len(fi) > 0 {
			spec["faultInjection"] = fi
		}
	}

	// Add rate limit configuration if present
	if config.RateLimit != nil && config.RateLimit.Global != nil {
		rules := make([]interface{}, 0, len(config.RateLimit.Global.Rules))
		for _, rule := range config.RateLimit.Global.Rules {
			ruleMap := map[string]interface{}{
				"limit": map[string]interface{}{
					"requests": int64(rule.Limit.Requests),
					"unit":     rule.Limit.Unit,
				},
			}
			if len(rule.ClientSelectors) > 0 {
				selectors := make([]interface{}, 0, len(rule.ClientSelectors))
				for _, sel := range rule.ClientSelectors {
					selMap := map[string]interface{}{}
					if len(sel.Headers) > 0 {
						headers := make([]interface{}, 0, len(sel.Headers))
						for _, h := range sel.Headers {
							hMap := map[string]interface{}{
								"name": h.Name,
							}
							if h.Value != "" {
								hMap["value"] = h.Value
							}
							if h.Type != "" {
								hMap["type"] = h.Type
							}
							if h.Invert {
								hMap["invert"] = true
							}
							headers = append(headers, hMap)
						}
						selMap["headers"] = headers
					}
					if sel.SourceCIDR != nil {
						cidr := map[string]interface{}{
							"value": sel.SourceCIDR.Value,
						}
						if sel.SourceCIDR.Type != "" {
							cidr["type"] = sel.SourceCIDR.Type
						}
						selMap["sourceCIDR"] = cidr
					}
					if sel.Path != nil {
						selMap["path"] = map[string]interface{}{
							"value": sel.Path.Value,
							"type":  sel.Path.Type,
						}
					}
					if len(sel.Methods) > 0 {
						methods := make([]interface{}, len(sel.Methods))
						for i, m := range sel.Methods {
							methods[i] = m
						}
						selMap["methods"] = methods
					}
					selectors = append(selectors, selMap)
				}
				ruleMap["clientSelectors"] = selectors
			}
			rules = append(rules, ruleMap)
		}
		spec["rateLimit"] = map[string]interface{}{
			"global": map[string]interface{}{
				"rules": rules,
			},
		}
	}

	// Add request buffer configuration if present
	if config.RequestBuffer != nil {
		spec["requestBuffer"] = map[string]interface{}{
			"limit": config.RequestBuffer.Limit,
		}
	}

	// Add response override configuration if present
	if len(config.ResponseOverride) > 0 {
		rules := make([]interface{}, 0, len(config.ResponseOverride))
		for _, rule := range config.ResponseOverride {
			statusCodes := make([]interface{}, 0, len(rule.Match.StatusCodes))
			for _, sc := range rule.Match.StatusCodes {
				scMap := map[string]interface{}{
					"type": sc.Type,
				}
				if sc.Value != nil {
					scMap["value"] = *sc.Value
				}
				if sc.Range != nil {
					scMap["range"] = map[string]interface{}{
						"start": sc.Range.Start,
						"end":   sc.Range.End,
					}
				}
				statusCodes = append(statusCodes, scMap)
			}

			body := map[string]interface{}{
				"type": rule.Response.Body.Type,
			}
			if rule.Response.Body.Inline != "" {
				body["inline"] = rule.Response.Body.Inline
			}
			if rule.Response.Body.ValueRef != nil {
				valueRef := map[string]interface{}{
					"kind": rule.Response.Body.ValueRef.Kind,
					"name": rule.Response.Body.ValueRef.Name,
				}
				if rule.Response.Body.ValueRef.Group != "" {
					valueRef["group"] = rule.Response.Body.ValueRef.Group
				}
				if rule.Response.Body.ValueRef.Namespace != "" {
					valueRef["namespace"] = rule.Response.Body.ValueRef.Namespace
				}
				body["valueRef"] = valueRef
			}

			ruleMap := map[string]interface{}{
				"match": map[string]interface{}{
					"statusCodes": statusCodes,
				},
				"response": map[string]interface{}{
					"contentType": rule.Response.ContentType,
					"body":        body,
				},
			}
			rules = append(rules, ruleMap)
		}
		spec["responseOverride"] = rules
	}

	// Add timeout configuration if present
	if config.Timeout != nil {
		timeout := map[string]interface{}{}
		if config.Timeout.TCP != nil {
			tcp := map[string]interface{}{}
			if config.Timeout.TCP.ConnectTimeout != "" {
				tcp["connectTimeout"] = config.Timeout.TCP.ConnectTimeout
			}
			if len(tcp) > 0 {
				timeout["tcp"] = tcp
			}
		}
		if config.Timeout.HTTP != nil {
			http := map[string]interface{}{}
			if config.Timeout.HTTP.RequestTimeout != "" {
				http["requestTimeout"] = config.Timeout.HTTP.RequestTimeout
			}
			if config.Timeout.HTTP.ConnectionIdleTimeout != "" {
				http["connectionIdleTimeout"] = config.Timeout.HTTP.ConnectionIdleTimeout
			}
			if config.Timeout.HTTP.MaxConnectionDuration != "" {
				http["maxConnectionDuration"] = config.Timeout.HTTP.MaxConnectionDuration
			}
			if config.Timeout.HTTP.MaxStreamDuration != "" {
				http["maxStreamDuration"] = config.Timeout.HTTP.MaxStreamDuration
			}
			if len(http) > 0 {
				timeout["http"] = http
			}
		}
		if len(timeout) > 0 {
			spec["timeout"] = timeout
		}
	}

	// Check if any feature is configured
	hasFeature := false
	if _, ok := spec["compressor"]; ok {
		hasFeature = true
	}
	if _, ok := spec["retry"]; ok {
		hasFeature = true
	}
	if _, ok := spec["loadBalancer"]; ok {
		hasFeature = true
	}
	if _, ok := spec["circuitBreaker"]; ok {
		hasFeature = true
	}
	if _, ok := spec["healthCheck"]; ok {
		hasFeature = true
	}
	if _, ok := spec["faultInjection"]; ok {
		hasFeature = true
	}
	if _, ok := spec["rateLimit"]; ok {
		hasFeature = true
	}
	if _, ok := spec["requestBuffer"]; ok {
		hasFeature = true
	}
	if _, ok := spec["responseOverride"]; ok {
		hasFeature = true
	}
	if _, ok := spec["timeout"]; ok {
		hasFeature = true
	}
	if !hasFeature {
		return nil
	}

	labels := map[string]interface{}{
		"app.kubernetes.io/managed-by": "fastgateway",
		"fastgateway.dev/gateway-id":   config.GatewayID,
	}
	if config.RouteID != "" {
		labels["fastgateway.dev/route-id"] = config.RouteID
	}
	if config.DomainID != "" {
		labels["fastgateway.dev/domain-id"] = config.DomainID
	}

	backendTrafficPolicy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "BackendTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    labels,
			},
			"spec": spec,
		},
	}

	return backendTrafficPolicy
}

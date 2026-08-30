package kubernetes

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// BuildClientTrafficPolicy builds an Envoy Gateway ClientTrafficPolicy from config
func BuildClientTrafficPolicy(config *ClientTrafficPolicyConfig) *unstructured.Unstructured {
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

	hasConfig := false

	// Add TCP keepalive configuration if present
	if config.TCPKeepalive != nil {
		tcpKeepalive := map[string]interface{}{}
		if config.TCPKeepalive.Probes != nil {
			tcpKeepalive["probes"] = *config.TCPKeepalive.Probes
		}
		if config.TCPKeepalive.IdleTime != nil && *config.TCPKeepalive.IdleTime != "" {
			tcpKeepalive["idleTime"] = *config.TCPKeepalive.IdleTime
		}
		if config.TCPKeepalive.Interval != nil && *config.TCPKeepalive.Interval != "" {
			tcpKeepalive["interval"] = *config.TCPKeepalive.Interval
		}
		if len(tcpKeepalive) > 0 {
			spec["tcpKeepalive"] = tcpKeepalive
			hasConfig = true
		}
	}

	// Add PROXY protocol configuration if enabled
	if config.EnableProxyProtocol {
		spec["enableProxyProtocol"] = true
		hasConfig = true
	}

	// Add connection configuration if present
	if config.Connection != nil {
		connection := map[string]interface{}{}
		if config.Connection.BufferLimit != nil && *config.Connection.BufferLimit != "" {
			connection["bufferLimit"] = *config.Connection.BufferLimit
		}
		// Connection limit settings go under connectionLimit
		connectionLimit := map[string]interface{}{}
		if config.Connection.MaxConnections != nil {
			connectionLimit["value"] = *config.Connection.MaxConnections
		}
		if config.Connection.CloseDelay != nil && *config.Connection.CloseDelay != "" {
			connectionLimit["closeDelay"] = *config.Connection.CloseDelay
		}
		if config.Connection.MaxConnectionDuration != nil && *config.Connection.MaxConnectionDuration != "" {
			connectionLimit["maxConnectionDuration"] = *config.Connection.MaxConnectionDuration
		}
		if config.Connection.MaxRequestsPerConnection != nil {
			connectionLimit["maxRequestsPerConnection"] = *config.Connection.MaxRequestsPerConnection
		}
		if len(connectionLimit) > 0 {
			connection["connectionLimit"] = connectionLimit
		}
		if len(connection) > 0 {
			spec["connection"] = connection
			hasConfig = true
		}
	}

	// Add timeout configuration if present
	if config.Timeout != nil && config.Timeout.HTTP != nil {
		timeout := map[string]interface{}{}
		httpTimeout := map[string]interface{}{}
		if config.Timeout.HTTP.RequestReceivedTimeout != nil && *config.Timeout.HTTP.RequestReceivedTimeout != "" {
			httpTimeout["requestReceivedTimeout"] = *config.Timeout.HTTP.RequestReceivedTimeout
		}
		if config.Timeout.HTTP.IdleTimeout != nil && *config.Timeout.HTTP.IdleTimeout != "" {
			httpTimeout["idleTimeout"] = *config.Timeout.HTTP.IdleTimeout
		}
		if len(httpTimeout) > 0 {
			timeout["http"] = httpTimeout
			spec["timeout"] = timeout
			hasConfig = true
		}
	}

	// Add HTTP/3 configuration if enabled
	if config.HTTP3 != nil && config.HTTP3.Enabled {
		spec["http3"] = map[string]interface{}{}
		hasConfig = true
	}

	// Add client IP detection configuration if present
	if config.ClientIPDetection != nil {
		clientIPDetection := map[string]interface{}{}
		if config.ClientIPDetection.XForwardedFor != nil {
			clientIPDetection["xForwardedFor"] = map[string]interface{}{
				"numTrustedHops": config.ClientIPDetection.XForwardedFor.NumTrustedHops,
			}
			hasConfig = true
		}
		if config.ClientIPDetection.CustomHeader != nil {
			customHeader := map[string]interface{}{
				"name": config.ClientIPDetection.CustomHeader.Name,
			}
			if config.ClientIPDetection.CustomHeader.FailClosed {
				customHeader["failClosed"] = true
			}
			clientIPDetection["customHeader"] = customHeader
			hasConfig = true
		}
		if len(clientIPDetection) > 0 {
			spec["clientIPDetection"] = clientIPDetection
		}
	}

	// Add TLS configuration if present
	if config.TLS != nil {
		tls := map[string]interface{}{}
		if config.TLS.MinVersion != nil && *config.TLS.MinVersion != "" {
			tls["minVersion"] = convertTLSVersionToK8s(*config.TLS.MinVersion)
		}
		if config.TLS.MaxVersion != nil && *config.TLS.MaxVersion != "" {
			tls["maxVersion"] = convertTLSVersionToK8s(*config.TLS.MaxVersion)
		}
		if len(config.TLS.Ciphers) > 0 {
			// stringSliceToInterfaceSlice, like every other builder in
			// this file: an unstructured.Unstructured may only hold
			// JSON-native values. A raw []string makes
			// runtime.DeepCopyJSONValue panic with "cannot deep copy
			// []string", taking down any path that copies the object --
			// an informer cache, client-go's converter, or a
			// read-modify-write retry that re-stamps it.
			tls["ciphers"] = stringSliceToInterfaceSlice(config.TLS.Ciphers)
		}
		if len(config.TLS.ECDHCurves) > 0 {
			tls["ecdhCurves"] = stringSliceToInterfaceSlice(config.TLS.ECDHCurves)
		}
		if len(config.TLS.SignatureAlgorithms) > 0 {
			tls["signatureAlgorithms"] = stringSliceToInterfaceSlice(config.TLS.SignatureAlgorithms)
		}
		if len(tls) > 0 {
			spec["tls"] = tls
			hasConfig = true
		}
	}

	// Add mTLS client validation configuration if present
	if config.ClientValidation != nil {
		// Get or create TLS spec section
		tlsSpec, ok := spec["tls"].(map[string]interface{})
		if !ok {
			tlsSpec = map[string]interface{}{}
		}

		clientValidation := map[string]interface{}{
			"optional": config.ClientValidation.Optional,
		}

		// Add CA certificate references
		if len(config.ClientValidation.CACertificateRefs) > 0 {
			caRefs := make([]interface{}, len(config.ClientValidation.CACertificateRefs))
			for i, ref := range config.ClientValidation.CACertificateRefs {
				caRefs[i] = map[string]interface{}{
					"group": ref.Group,
					"kind":  ref.Kind,
					"name":  ref.Name,
				}
			}
			clientValidation["caCertificateRefs"] = caRefs
		}

		// Add SAN matchers (subjectAltNames in CRD)
		if len(config.ClientValidation.SANMatchers) > 0 {
			sanMatchers := make([]interface{}, len(config.ClientValidation.SANMatchers))
			for i, san := range config.ClientValidation.SANMatchers {
				sanMatchers[i] = map[string]interface{}{
					"type": san.Type,
					"match": map[string]interface{}{
						"exact": san.Match,
					},
				}
			}
			clientValidation["subjectAltNames"] = sanMatchers
		}

		// Add certificate hashes
		if len(config.ClientValidation.CertificateHashes) > 0 {
			clientValidation["certificateHashes"] = stringSliceToInterfaceSlice(config.ClientValidation.CertificateHashes)
		}

		tlsSpec["clientValidation"] = clientValidation
		spec["tls"] = tlsSpec
		hasConfig = true
	}

	// Add headers configuration (XFCC) if present
	if config.Headers != nil && config.Headers.XForwardedClientCert != nil {
		xfcc := config.Headers.XForwardedClientCert
		headers := map[string]interface{}{
			"xForwardedClientCert": map[string]interface{}{
				"mode":             xfcc.Mode,
				"certDetailsToAdd": stringSliceToInterfaceSlice(xfcc.CertDetailsToAdd),
			},
		}
		spec["headers"] = headers
		hasConfig = true
	}

	// Only create if there's actual config beyond targetRef
	if !hasConfig {
		return nil
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "ClientTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    ForGatewayInterface(config.GatewayID),
			},
			"spec": spec,
		},
	}
}

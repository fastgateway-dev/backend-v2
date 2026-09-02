package domainplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// BuildClientTrafficPolicyConfig builds a ClientTrafficPolicyConfig from domain and settings config
func BuildClientTrafficPolicyConfig(domain *models.Domain, config *models.DomainSettingsConfig, caSecretRefs []kubernetes.SecretRefPolicyConfig) *kubernetes.ClientTrafficPolicyConfig {
	ctpName := domain.K8sGatewayName + "-ctp"
	ctpConfig := &kubernetes.ClientTrafficPolicyConfig{
		Name:      ctpName,
		Namespace: domain.Namespace,
		GatewayID: domain.ID.String(),
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{
			Group: "gateway.networking.k8s.io",
			Kind:  "Gateway",
			Name:  domain.K8sGatewayName,
		},
	}

	if config.ClientConnection != nil {
		if config.ClientConnection.TCPKeepalive != nil {
			ctpConfig.TCPKeepalive = &kubernetes.TCPKeepalivePolicyConfig{
				Probes:   config.ClientConnection.TCPKeepalive.Probes,
				IdleTime: config.ClientConnection.TCPKeepalive.IdleTime,
				Interval: config.ClientConnection.TCPKeepalive.Interval,
			}
		}
		if config.ClientConnection.ProxyProtocol != nil && config.ClientConnection.ProxyProtocol.Enabled {
			ctpConfig.EnableProxyProtocol = true
		}
		if config.ClientConnection.ConnectionLimit != nil || config.ClientConnection.BufferLimit != nil {
			ctpConfig.Connection = &kubernetes.ConnectionPolicyConfig{}
			if config.ClientConnection.BufferLimit != nil {
				ctpConfig.Connection.BufferLimit = config.ClientConnection.BufferLimit
			}
			if config.ClientConnection.ConnectionLimit != nil {
				ctpConfig.Connection.MaxConnections = config.ClientConnection.ConnectionLimit.MaxConnections
				ctpConfig.Connection.CloseDelay = config.ClientConnection.ConnectionLimit.CloseDelay
				ctpConfig.Connection.MaxConnectionDuration = config.ClientConnection.ConnectionLimit.MaxConnectionDuration
				ctpConfig.Connection.MaxRequestsPerConnection = config.ClientConnection.ConnectionLimit.MaxRequestsPerConnection
			}
		}
	}

	if config.Timeout != nil && config.Timeout.HTTP != nil {
		ctpConfig.Timeout = &kubernetes.TimeoutPolicyConfig{
			HTTP: &kubernetes.HTTPTimeoutPolicyConfig{
				RequestReceivedTimeout: config.Timeout.HTTP.RequestReceivedTimeout,
				IdleTimeout:            config.Timeout.HTTP.IdleTimeout,
			},
		}
	}

	if config.HTTP3 != nil && config.HTTP3.Enabled {
		ctpConfig.HTTP3 = &kubernetes.HTTP3PolicyConfig{Enabled: true}
	}

	if config.TLS != nil && !config.TLS.IsEmpty() {
		ctpConfig.TLS = &kubernetes.TLSPolicyConfig{
			MinVersion:          config.TLS.MinVersion,
			MaxVersion:          config.TLS.MaxVersion,
			Ciphers:             config.TLS.Ciphers,
			ECDHCurves:          config.TLS.ECDHCurves,
			SignatureAlgorithms: config.TLS.SignatureAlgorithms,
		}
	}

	if config.ClientIPDetection != nil {
		ctpConfig.ClientIPDetection = &kubernetes.ClientIPDetectionPolicyConfig{}
		if config.ClientIPDetection.XForwardedFor != nil {
			ctpConfig.ClientIPDetection.XForwardedFor = &kubernetes.XForwardedForPolicyConfig{
				NumTrustedHops: config.ClientIPDetection.XForwardedFor.NumTrustedHops,
			}
		}
		if config.ClientIPDetection.CustomHeader != nil {
			ctpConfig.ClientIPDetection.CustomHeader = &kubernetes.CustomHeaderPolicyConfig{
				Name:       config.ClientIPDetection.CustomHeader.Name,
				FailClosed: config.ClientIPDetection.CustomHeader.FailClosed,
			}
		}
	}

	// mTLS client validation.
	//
	// Deliberately NOT gated on len(caSecretRefs) > 0. Before Phase 2G it
	// was, which meant an mTLS-enabled domain whose CA refs came back empty
	// rendered byte-identically to a domain with no security settings at
	// all -- silently unauthenticated, Gateway-wide. Emitting the block with
	// an empty ref list makes Envoy reject instead.
	if config.MTLS != nil && config.MTLS.Enabled {
		ctpConfig.ClientValidation = &kubernetes.ClientValidationPolicyConfig{
			Optional:          config.MTLS.Optional,
			CACertificateRefs: caSecretRefs,
		}
		for _, san := range config.MTLS.SANWhitelist {
			ctpConfig.ClientValidation.SANMatchers = append(ctpConfig.ClientValidation.SANMatchers, kubernetes.SANMatcherPolicyConfig{
				Type:  san.Type,
				Match: san.Value,
			})
		}
		ctpConfig.ClientValidation.CertificateHashes = config.MTLS.HashWhitelist
		ctpConfig.Headers = &kubernetes.HeadersPolicyConfig{
			XForwardedClientCert: &kubernetes.XFCCPolicyConfig{
				Mode:             "AppendForward",
				CertDetailsToAdd: []string{"Subject", "Cert", "DNS", "URI"},
			},
		}
	}

	return ctpConfig
}

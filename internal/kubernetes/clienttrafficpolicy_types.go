package kubernetes

// ClientTrafficPolicyConfig represents Envoy Gateway ClientTrafficPolicy configuration
// This targets Gateway objects for downstream client settings
type ClientTrafficPolicyConfig struct {
	Name                string                         // Name of the ClientTrafficPolicy (usually {gatewayName}-ctp)
	Namespace           string                         // Namespace where the ClientTrafficPolicy will be created
	GatewayID           string                         // Domain/Gateway ID for labeling
	TargetRef           ClientTrafficPolicyTargetRef   // Reference to the target Gateway
	TCPKeepalive        *TCPKeepalivePolicyConfig      // TCP keepalive configuration
	EnableProxyProtocol bool                           // Enable PROXY protocol on listener
	Connection          *ConnectionPolicyConfig        // Connection settings
	ClientIPDetection   *ClientIPDetectionPolicyConfig // Client IP detection settings
	Timeout             *TimeoutPolicyConfig           // Timeout settings
	HTTP3               *HTTP3PolicyConfig             // HTTP/3 settings
	TLS                 *TLSPolicyConfig               // TLS settings
	ClientValidation    *ClientValidationPolicyConfig  // mTLS client validation settings
	Headers             *HeadersPolicyConfig           // Headers settings (XFCC)
}

// ClientTrafficPolicyTargetRef represents the target reference for ClientTrafficPolicy
type ClientTrafficPolicyTargetRef struct {
	Group string
	Kind  string
	Name  string
}

// TCPKeepalivePolicyConfig represents TCP keepalive settings for ClientTrafficPolicy
type TCPKeepalivePolicyConfig struct {
	Probes   *int32  // Max keepalive probes before considering dead
	IdleTime *string // Duration before probes start (e.g. "60s")
	Interval *string // Duration between probes (e.g. "10s")
}

// ConnectionPolicyConfig represents connection settings for ClientTrafficPolicy
type ConnectionPolicyConfig struct {
	BufferLimit              *string // Buffer limit (e.g., "32Ki")
	MaxConnections           *int32  // Max concurrent connections
	CloseDelay               *string // Delay before closing rejected connections
	MaxConnectionDuration    *string // Max connection duration
	MaxRequestsPerConnection *int32  // Max requests per connection
}

// TimeoutPolicyConfig represents timeout settings for ClientTrafficPolicy
type TimeoutPolicyConfig struct {
	HTTP *HTTPTimeoutPolicyConfig
}

// HTTPTimeoutPolicyConfig represents HTTP timeout settings
type HTTPTimeoutPolicyConfig struct {
	RequestReceivedTimeout *string // Time to receive complete request headers
	IdleTimeout            *string // Idle connection timeout
}

// HTTP3PolicyConfig represents HTTP/3 settings
type HTTP3PolicyConfig struct {
	Enabled bool
}

// TLSPolicyConfig represents TLS settings for ClientTrafficPolicy
type TLSPolicyConfig struct {
	MinVersion          *string
	MaxVersion          *string
	Ciphers             []string
	ECDHCurves          []string
	SignatureAlgorithms []string
}

// ClientValidationPolicyConfig represents mTLS client validation for ClientTrafficPolicy
type ClientValidationPolicyConfig struct {
	CACertificateRefs []SecretRefPolicyConfig  `json:"caCertificateRefs,omitempty"`
	Optional          bool                     `json:"optional"`
	SANMatchers       []SANMatcherPolicyConfig `json:"sanMatchers,omitempty"`
	CertificateHashes []string                 `json:"certificateHashes,omitempty"`
}

// SecretRefPolicyConfig represents a reference to a K8s Secret
type SecretRefPolicyConfig struct {
	Group string `json:"group"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

// SANMatcherPolicyConfig represents a SAN matcher for mTLS
type SANMatcherPolicyConfig struct {
	Type  string `json:"type"`  // "DNS" or "URI"
	Match string `json:"match"` // Exact match value
}

// XFCCPolicyConfig represents X-Forwarded-Client-Cert header configuration
type XFCCPolicyConfig struct {
	Mode             string   `json:"mode"`             // "Sanitize", "ForwardOnly", "AppendForward"
	CertDetailsToAdd []string `json:"certDetailsToAdd"` // "Hash", "DNS", "URI", "Subject", "Cert", "Chain"
}

// HeadersPolicyConfig represents headers configuration for ClientTrafficPolicy
type HeadersPolicyConfig struct {
	XForwardedClientCert *XFCCPolicyConfig `json:"xForwardedClientCert,omitempty"`
}

// ClientIPDetectionPolicyConfig represents client IP detection settings for ClientTrafficPolicy
type ClientIPDetectionPolicyConfig struct {
	XForwardedFor *XForwardedForPolicyConfig `json:"xForwardedFor,omitempty"`
	CustomHeader  *CustomHeaderPolicyConfig  `json:"customHeader,omitempty"`
}

// XForwardedForPolicyConfig extracts client IP from X-Forwarded-For header
type XForwardedForPolicyConfig struct {
	NumTrustedHops int `json:"numTrustedHops"`
}

// CustomHeaderPolicyConfig extracts client IP from a custom header
type CustomHeaderPolicyConfig struct {
	Name       string `json:"name"`
	FailClosed bool   `json:"failClosed"`
}

// CompressionPolicyConfig represents a compression configuration for BackendTrafficPolicy
type CompressionPolicyConfig struct {
	Type   string // Gzip, Brotli, Zstd
	Gzip   *GzipPolicyConfig
	Brotli *BrotliPolicyConfig
	Zstd   *ZstdPolicyConfig
}

// GzipPolicyConfig represents Gzip-specific compression configuration
type GzipPolicyConfig struct{}

// BrotliPolicyConfig represents Brotli-specific compression configuration
type BrotliPolicyConfig struct{}

// ZstdPolicyConfig represents Zstd-specific compression configuration
type ZstdPolicyConfig struct{}

// RetryPolicyConfig represents retry configuration for BackendTrafficPolicy
type RetryPolicyConfig struct {
	NumRetries *int32
	RetryOn    *RetryOnPolicyConfig
	PerRetry   *PerRetryPolicyConfig
}

// RetryOnPolicyConfig specifies conditions that trigger a retry
type RetryOnPolicyConfig struct {
	HTTPStatusCodes []int
	Triggers        []string
}

// PerRetryPolicyConfig defines per-attempt retry behavior
type PerRetryPolicyConfig struct {
	BackOff *BackOffPolicyConfig
	Timeout *string
}

// BackOffPolicyConfig defines backoff timing between retries
type BackOffPolicyConfig struct {
	BaseInterval *string
	MaxInterval  *string
}

// convertTLSVersionToK8s converts API TLS version format to K8s CRD format
// API accepts: "TLS1.0", "TLS1.1", "TLS1.2", "TLS1.3", "TLSv1.0", "TLSv1.1", "TLSv1.2", "TLSv1.3", "Auto", ""
// K8s CRD expects: "Auto", "1.0", "1.1", "1.2", "1.3"
func convertTLSVersionToK8s(version string) string {
	switch version {
	case "TLS1.0", "TLSv1.0":
		return "1.0"
	case "TLS1.1", "TLSv1.1":
		return "1.1"
	case "TLS1.2", "TLSv1.2":
		return "1.2"
	case "TLS1.3", "TLSv1.3":
		return "1.3"
	case "Auto", "":
		return "Auto"
	default:
		// If already in K8s format or unknown, return as-is
		return version
	}
}

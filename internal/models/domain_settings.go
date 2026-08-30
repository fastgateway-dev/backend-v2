package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// httpHeaderNameRegex validates HTTP header names per RFC 7230
// Header names must be tokens: alphanumeric plus !#$%&'*+-.^_`|~
var httpHeaderNameRegex = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+\-.^_` + "`" + `|~]+$`)

// DomainSettings represents domain-level settings configuration
// This is a generic abstraction that gets translated to gateway-specific resources
// (e.g., Envoy Gateway's ClientTrafficPolicy CRD)
type DomainSettings struct {
	ID        uuid.UUID            `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	DomainID  uuid.UUID            `gorm:"type:uuid;not null;uniqueIndex" json:"domainId"`
	ProjectID uuid.UUID            `gorm:"type:uuid;not null;index" json:"projectId"`
	Config    DomainSettingsConfig `gorm:"type:jsonb" json:"settings"`
	CreatedAt time.Time            `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt time.Time            `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Domain  *Domain  `gorm:"foreignKey:DomainID" json:"domain,omitempty"`
	Project *Project `gorm:"foreignKey:ProjectID" json:"project,omitempty"`
}

// TableName returns the table name for DomainSettings
// Note: Uses existing migration table name for compatibility
func (DomainSettings) TableName() string {
	return "client_traffic_policies"
}

// DomainSettingsConfig holds all domain-level settings
// This is a gateway-agnostic configuration that gets translated to specific CRDs
type DomainSettingsConfig struct {
	ClientConnection  *ClientConnectionConfig  `json:"clientConnection,omitempty"`
	ClientIPDetection *ClientIPDetectionConfig `json:"clientIPDetection,omitempty"`
	Timeout           *TimeoutConfig           `json:"timeout,omitempty"`
	HTTP3             *HTTP3Config             `json:"http3,omitempty"`
	TLS               *TLSSettingsConfig       `json:"tls,omitempty"`
	MTLS              *DomainMTLSConfig        `json:"mtls,omitempty"`
}

// Value implements the driver.Valuer interface for DomainSettingsConfig
func (c DomainSettingsConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for DomainSettingsConfig
func (c *DomainSettingsConfig) Scan(value interface{}) error {
	if value == nil {
		*c = DomainSettingsConfig{}
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(bytes, c)
}

// IsEmpty returns true if no settings are configured
func (c *DomainSettingsConfig) IsEmpty() bool {
	if c == nil {
		return true
	}
	connEmpty := c.ClientConnection == nil || c.ClientConnection.IsEmpty()
	ipDetectEmpty := c.ClientIPDetection == nil || c.ClientIPDetection.IsEmpty()
	timeoutEmpty := c.Timeout == nil || c.Timeout.IsEmpty()
	http3Empty := c.HTTP3 == nil || !c.HTTP3.Enabled
	tlsEmpty := c.TLS == nil || c.TLS.IsEmpty()
	mtlsEmpty := c.MTLS == nil || !c.MTLS.Enabled
	return connEmpty && ipDetectEmpty && timeoutEmpty && http3Empty && tlsEmpty && mtlsEmpty
}

// ClientIPDetectionConfig configures how client IP is detected from HTTP headers
// At most one of XForwardedFor or CustomHeader can be set (mutually exclusive)
type ClientIPDetectionConfig struct {
	XForwardedFor *XForwardedForConfig `json:"xForwardedFor,omitempty"`
	CustomHeader  *CustomHeaderConfig  `json:"customHeader,omitempty"`
}

// XForwardedForConfig extracts client IP from X-Forwarded-For header
type XForwardedForConfig struct {
	NumTrustedHops int `json:"numTrustedHops"` // Required, 1-10
}

// CustomHeaderConfig extracts client IP from a custom header
type CustomHeaderConfig struct {
	Name       string `json:"name"`       // Required, e.g. "CF-Connecting-IP"
	FailClosed bool   `json:"failClosed"` // Reject if header missing
}

// IsEmpty returns true if no client IP detection is configured
func (c *ClientIPDetectionConfig) IsEmpty() bool {
	if c == nil {
		return true
	}
	return c.XForwardedFor == nil && c.CustomHeader == nil
}

// Validate validates the client IP detection configuration
func (c *ClientIPDetectionConfig) Validate() error {
	if c == nil {
		return nil
	}

	// Check mutual exclusivity
	if c.XForwardedFor != nil && c.CustomHeader != nil {
		return fmt.Errorf("xForwardedFor and customHeader are mutually exclusive")
	}

	// Validate XForwardedFor
	if c.XForwardedFor != nil {
		if c.XForwardedFor.NumTrustedHops < 1 || c.XForwardedFor.NumTrustedHops > 10 {
			return fmt.Errorf("numTrustedHops must be between 1 and 10")
		}
	}

	// Validate CustomHeader
	if c.CustomHeader != nil {
		if c.CustomHeader.Name == "" {
			return fmt.Errorf("customHeader.name is required")
		}
		if !httpHeaderNameRegex.MatchString(c.CustomHeader.Name) {
			return fmt.Errorf("customHeader.name must be a valid HTTP header name (alphanumeric and hyphens)")
		}
	}

	return nil
}

// ClientConnectionConfig represents client connection settings
type ClientConnectionConfig struct {
	TCPKeepalive    *TCPKeepaliveConfig    `json:"tcpKeepalive,omitempty"`
	ProxyProtocol   *ProxyProtocolConfig   `json:"proxyProtocol,omitempty"`
	ConnectionLimit *ConnectionLimitConfig `json:"connectionLimit,omitempty"`
	BufferLimit     *string                `json:"bufferLimit,omitempty"` // e.g., "32Ki", "1Mi"
}

// IsEmpty returns true if no client connection settings are configured
func (c *ClientConnectionConfig) IsEmpty() bool {
	if c == nil {
		return true
	}
	return c.TCPKeepalive == nil &&
		(c.ProxyProtocol == nil || !c.ProxyProtocol.Enabled) &&
		(c.ConnectionLimit == nil || c.ConnectionLimit.IsEmpty()) &&
		(c.BufferLimit == nil || *c.BufferLimit == "")
}

// Validate validates the client connection configuration
func (c *ClientConnectionConfig) Validate() error {
	if c == nil {
		return nil
	}
	if err := c.TCPKeepalive.Validate(); err != nil {
		return err
	}
	if err := c.ProxyProtocol.Validate(); err != nil {
		return err
	}
	return c.ConnectionLimit.Validate()
}

// ConnectionLimitConfig represents connection limit settings
type ConnectionLimitConfig struct {
	MaxConnections           *int32  `json:"maxConnections,omitempty"`           // Max concurrent connections
	CloseDelay               *string `json:"closeDelay,omitempty"`               // Delay before closing rejected connections (e.g., "5s")
	MaxConnectionDuration    *string `json:"maxConnectionDuration,omitempty"`    // Max time a connection can stay open (e.g., "1h")
	MaxRequestsPerConnection *int32  `json:"maxRequestsPerConnection,omitempty"` // Max requests per connection
}

// IsEmpty returns true if no connection limit settings are configured
func (c *ConnectionLimitConfig) IsEmpty() bool {
	if c == nil {
		return true
	}
	return c.MaxConnections == nil &&
		(c.CloseDelay == nil || *c.CloseDelay == "") &&
		(c.MaxConnectionDuration == nil || *c.MaxConnectionDuration == "") &&
		c.MaxRequestsPerConnection == nil
}

// Validate validates the connection limit configuration
func (c *ConnectionLimitConfig) Validate() error {
	if c == nil {
		return nil
	}

	if c.MaxConnections != nil && *c.MaxConnections < 0 {
		return fmt.Errorf("maxConnections must be non-negative")
	}

	if c.MaxRequestsPerConnection != nil && *c.MaxRequestsPerConnection < 0 {
		return fmt.Errorf("maxRequestsPerConnection must be non-negative")
	}

	if c.CloseDelay != nil && *c.CloseDelay != "" {
		if _, err := time.ParseDuration(*c.CloseDelay); err != nil {
			return fmt.Errorf("invalid closeDelay duration: %w", err)
		}
	}

	if c.MaxConnectionDuration != nil && *c.MaxConnectionDuration != "" {
		if _, err := time.ParseDuration(*c.MaxConnectionDuration); err != nil {
			return fmt.Errorf("invalid maxConnectionDuration: %w", err)
		}
	}

	return nil
}

// ProxyProtocolConfig represents PROXY protocol settings
// When enabled, the listener expects PROXY protocol headers from upstream load balancers
type ProxyProtocolConfig struct {
	Enabled bool `json:"enabled"`
}

// Validate validates the PROXY protocol configuration
func (p *ProxyProtocolConfig) Validate() error {
	return nil
}

// TCPKeepaliveConfig represents TCP keepalive settings
type TCPKeepaliveConfig struct {
	Probes   *int32  `json:"probes,omitempty"`   // Max keepalive probes before considering dead
	IdleTime *string `json:"idleTime,omitempty"` // Duration before probes start (e.g. "60s")
	Interval *string `json:"interval,omitempty"` // Duration between probes (e.g. "10s")
}

// Validate validates the TCP keepalive configuration
func (t *TCPKeepaliveConfig) Validate() error {
	if t == nil {
		return nil
	}

	if t.Probes != nil && *t.Probes < 0 {
		return fmt.Errorf("probes must be non-negative")
	}

	if t.IdleTime != nil && *t.IdleTime != "" {
		if _, err := time.ParseDuration(*t.IdleTime); err != nil {
			return fmt.Errorf("invalid idleTime duration: %w", err)
		}
	}

	if t.Interval != nil && *t.Interval != "" {
		if _, err := time.ParseDuration(*t.Interval); err != nil {
			return fmt.Errorf("invalid interval duration: %w", err)
		}
	}

	return nil
}

// TimeoutConfig represents timeout settings for client connections
type TimeoutConfig struct {
	HTTP *HTTPTimeoutConfig `json:"http,omitempty"`
}

// IsEmpty returns true if no timeout settings are configured
func (t *TimeoutConfig) IsEmpty() bool {
	if t == nil {
		return true
	}
	return t.HTTP == nil || t.HTTP.IsEmpty()
}

// Validate validates the timeout configuration
func (t *TimeoutConfig) Validate() error {
	if t == nil {
		return nil
	}
	return t.HTTP.Validate()
}

// HTTPTimeoutConfig represents HTTP-specific timeout settings
type HTTPTimeoutConfig struct {
	RequestReceivedTimeout *string `json:"requestReceivedTimeout,omitempty"` // Time to receive complete request headers (e.g., "30s")
	IdleTimeout            *string `json:"idleTimeout,omitempty"`            // Idle connection timeout (e.g., "60s")
}

// IsEmpty returns true if no HTTP timeout settings are configured
func (h *HTTPTimeoutConfig) IsEmpty() bool {
	if h == nil {
		return true
	}
	return (h.RequestReceivedTimeout == nil || *h.RequestReceivedTimeout == "") &&
		(h.IdleTimeout == nil || *h.IdleTimeout == "")
}

// Validate validates the HTTP timeout configuration
func (h *HTTPTimeoutConfig) Validate() error {
	if h == nil {
		return nil
	}

	if h.RequestReceivedTimeout != nil && *h.RequestReceivedTimeout != "" {
		if _, err := time.ParseDuration(*h.RequestReceivedTimeout); err != nil {
			return fmt.Errorf("invalid requestReceivedTimeout duration: %w", err)
		}
	}

	if h.IdleTimeout != nil && *h.IdleTimeout != "" {
		if _, err := time.ParseDuration(*h.IdleTimeout); err != nil {
			return fmt.Errorf("invalid idleTimeout duration: %w", err)
		}
	}

	return nil
}

// HTTP3Config represents HTTP/3 (QUIC) settings
type HTTP3Config struct {
	Enabled bool `json:"enabled"`
}

// TLSSettingsConfig represents TLS settings for client connections
type TLSSettingsConfig struct {
	MinVersion          *string  `json:"minVersion,omitempty"`          // Minimum TLS version (e.g., "TLS1.2", "TLS1.3")
	MaxVersion          *string  `json:"maxVersion,omitempty"`          // Maximum TLS version (e.g., "TLS1.2", "TLS1.3")
	Ciphers             []string `json:"ciphers,omitempty"`             // Cipher suites for TLS 1.0-1.2
	ECDHCurves          []string `json:"ecdhCurves,omitempty"`          // ECDH curves
	SignatureAlgorithms []string `json:"signatureAlgorithms,omitempty"` // Signature algorithms
}

// IsEmpty returns true if no TLS settings are configured
func (t *TLSSettingsConfig) IsEmpty() bool {
	if t == nil {
		return true
	}
	return (t.MinVersion == nil || *t.MinVersion == "") &&
		(t.MaxVersion == nil || *t.MaxVersion == "") &&
		len(t.Ciphers) == 0 &&
		len(t.ECDHCurves) == 0 &&
		len(t.SignatureAlgorithms) == 0
}

// Validate validates the TLS settings configuration
func (t *TLSSettingsConfig) Validate() error {
	if t == nil {
		return nil
	}

	validVersions := map[string]bool{
		"TLS1.0": true, "TLSv1.0": true,
		"TLS1.1": true, "TLSv1.1": true,
		"TLS1.2": true, "TLSv1.2": true,
		"TLS1.3": true, "TLSv1.3": true,
		"Auto": true, "": true,
	}

	if t.MinVersion != nil && *t.MinVersion != "" {
		if !validVersions[*t.MinVersion] {
			return fmt.Errorf("invalid minVersion: %s", *t.MinVersion)
		}
	}

	if t.MaxVersion != nil && *t.MaxVersion != "" {
		if !validVersions[*t.MaxVersion] {
			return fmt.Errorf("invalid maxVersion: %s", *t.MaxVersion)
		}
	}

	return nil
}

// TLS Security Profiles for preset configurations
const (
	TLSProfileModern       = "modern"       // TLS 1.3 only
	TLSProfileIntermediate = "intermediate" // TLS 1.2+ (default)
	TLSProfileCompatible   = "compatible"   // TLS 1.0+ (legacy)
	TLSProfileCustom       = "custom"       // Manual configuration
)

// GetTLSProfileConfig returns the TLS configuration for a given profile
func GetTLSProfileConfig(profile string) *TLSSettingsConfig {
	switch profile {
	case TLSProfileModern:
		minVer := "TLS1.3"
		maxVer := "TLS1.3"
		return &TLSSettingsConfig{
			MinVersion: &minVer,
			MaxVersion: &maxVer,
		}
	case TLSProfileIntermediate:
		minVer := "TLS1.2"
		maxVer := "TLS1.3"
		return &TLSSettingsConfig{
			MinVersion: &minVer,
			MaxVersion: &maxVer,
			Ciphers: []string{
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
				"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
				"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
				"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
			},
		}
	case TLSProfileCompatible:
		minVer := "TLS1.0"
		maxVer := "TLS1.3"
		return &TLSSettingsConfig{
			MinVersion: &minVer,
			MaxVersion: &maxVer,
		}
	default:
		return nil
	}
}

// MTLSCACert represents a CA certificate reference for mTLS
type MTLSCACert struct {
	ID         string `json:"id"`         // Unique ID for this CA entry
	Name       string `json:"name"`       // Display name
	SecretName string `json:"secretName"` // K8s Secret name
	SecretKey  string `json:"secretKey"`  // Key in secret (default: "ca.crt")
}

// MTLSSANEntry represents a SAN whitelist entry
type MTLSSANEntry struct {
	Type  string `json:"type"`  // "DNS" or "URI"
	Value string `json:"value"` // Exact match value
}

// DomainMTLSConfig represents domain-level mTLS configuration
type DomainMTLSConfig struct {
	Enabled       bool           `json:"enabled"`
	Optional      bool           `json:"optional"`                // true = cert optional, false = required
	CACerts       []MTLSCACert   `json:"caCerts,omitempty"`       // Domain-level CA certificates
	SANWhitelist  []MTLSSANEntry `json:"sanWhitelist,omitempty"`  // General mode SAN whitelist
	HashWhitelist []string       `json:"hashWhitelist,omitempty"` // General mode hash whitelist
}

// Validate validates the domain mTLS configuration
func (m *DomainMTLSConfig) Validate() error {
	if m == nil || !m.Enabled {
		return nil
	}

	// At least one CA required when enabled
	if len(m.CACerts) == 0 {
		return fmt.Errorf("at least one CA certificate is required when mTLS is enabled")
	}

	// Validate SAN entries
	for i, san := range m.SANWhitelist {
		if san.Type != "DNS" && san.Type != "URI" {
			return fmt.Errorf("sanWhitelist[%d]: type must be 'DNS' or 'URI', got '%s'", i, san.Type)
		}
		if san.Value == "" {
			return fmt.Errorf("sanWhitelist[%d]: value cannot be empty", i)
		}
	}

	// Validate hash format (hex-encoded SHA256 = 64 chars)
	for i, hash := range m.HashWhitelist {
		if len(hash) != 64 {
			return fmt.Errorf("hashWhitelist[%d]: must be 64 hex characters (SHA256), got %d", i, len(hash))
		}
	}

	return nil
}

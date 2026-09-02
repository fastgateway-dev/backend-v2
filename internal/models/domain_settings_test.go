package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDomainSettingsConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  *DomainSettingsConfig
		want bool
	}{
		{name: "nil", cfg: nil, want: true},
		{name: "zero value", cfg: &DomainSettingsConfig{}, want: true},
		{
			name: "with client connection (empty)",
			cfg:  &DomainSettingsConfig{ClientConnection: &ClientConnectionConfig{}},
			want: true,
		},
		{
			name: "with client connection (non-empty)",
			cfg: &DomainSettingsConfig{
				ClientConnection: &ClientConnectionConfig{
					TCPKeepalive: &TCPKeepaliveConfig{},
				},
			},
			want: false,
		},
		{
			name: "with client IP detection (empty)",
			cfg:  &DomainSettingsConfig{ClientIPDetection: &ClientIPDetectionConfig{}},
			want: true,
		},
		{
			name: "with client IP detection (non-empty)",
			cfg: &DomainSettingsConfig{
				ClientIPDetection: &ClientIPDetectionConfig{
					XForwardedFor: &XForwardedForConfig{NumTrustedHops: 1},
				},
			},
			want: false,
		},
		{
			name: "with timeout (empty)",
			cfg:  &DomainSettingsConfig{Timeout: &TimeoutConfig{}},
			want: true,
		},
		{
			name: "with HTTP3 disabled",
			cfg:  &DomainSettingsConfig{HTTP3: &HTTP3Config{Enabled: false}},
			want: true,
		},
		{
			name: "with HTTP3 enabled",
			cfg:  &DomainSettingsConfig{HTTP3: &HTTP3Config{Enabled: true}},
			want: false,
		},
		{
			name: "with TLS (empty)",
			cfg:  &DomainSettingsConfig{TLS: &TLSSettingsConfig{}},
			want: true,
		},
		{
			name: "with TLS (non-empty)",
			cfg: &DomainSettingsConfig{
				TLS: &TLSSettingsConfig{Ciphers: []string{"TLS_AES_128_GCM_SHA256"}},
			},
			want: false,
		},
		{
			name: "with MTLS disabled",
			cfg:  &DomainSettingsConfig{MTLS: &DomainMTLSConfig{Enabled: false}},
			want: true,
		},
		{
			name: "with MTLS enabled",
			cfg:  &DomainSettingsConfig{MTLS: &DomainMTLSConfig{Enabled: true}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestClientIPDetectionConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ClientIPDetectionConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "empty", cfg: &ClientIPDetectionConfig{}, wantErr: false},
		{
			name: "valid XForwardedFor",
			cfg: &ClientIPDetectionConfig{
				XForwardedFor: &XForwardedForConfig{NumTrustedHops: 2},
			},
			wantErr: false,
		},
		{
			name: "XForwardedFor hops too low",
			cfg: &ClientIPDetectionConfig{
				XForwardedFor: &XForwardedForConfig{NumTrustedHops: 0},
			},
			wantErr: true,
			errMsg:  "numTrustedHops must be between 1 and 10",
		},
		{
			name: "XForwardedFor hops too high",
			cfg: &ClientIPDetectionConfig{
				XForwardedFor: &XForwardedForConfig{NumTrustedHops: 11},
			},
			wantErr: true,
			errMsg:  "numTrustedHops must be between 1 and 10",
		},
		{
			name: "valid CustomHeader",
			cfg: &ClientIPDetectionConfig{
				CustomHeader: &CustomHeaderConfig{Name: "CF-Connecting-IP"},
			},
			wantErr: false,
		},
		{
			name: "CustomHeader empty name",
			cfg: &ClientIPDetectionConfig{
				CustomHeader: &CustomHeaderConfig{Name: ""},
			},
			wantErr: true,
			errMsg:  "customHeader.name is required",
		},
		{
			name: "CustomHeader invalid name",
			cfg: &ClientIPDetectionConfig{
				CustomHeader: &CustomHeaderConfig{Name: "invalid header name"},
			},
			wantErr: true,
			errMsg:  "customHeader.name must be a valid HTTP header name",
		},
		{
			name: "mutually exclusive",
			cfg: &ClientIPDetectionConfig{
				XForwardedFor: &XForwardedForConfig{NumTrustedHops: 1},
				CustomHeader:  &CustomHeaderConfig{Name: "X-Real-IP"},
			},
			wantErr: true,
			errMsg:  "mutually exclusive",
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

func TestTLSSettingsConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *TLSSettingsConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "empty", cfg: &TLSSettingsConfig{}, wantErr: false},
		{
			name:    "valid min version",
			cfg:     &TLSSettingsConfig{MinVersion: strPtr("TLS1.2")},
			wantErr: false,
		},
		{
			name:    "valid max version",
			cfg:     &TLSSettingsConfig{MaxVersion: strPtr("TLS1.3")},
			wantErr: false,
		},
		{
			name:    "invalid min version",
			cfg:     &TLSSettingsConfig{MinVersion: strPtr("SSL3.0")},
			wantErr: true,
			errMsg:  "invalid minVersion",
		},
		{
			name:    "invalid max version",
			cfg:     &TLSSettingsConfig{MaxVersion: strPtr("BadVersion")},
			wantErr: true,
			errMsg:  "invalid maxVersion",
		},
		{
			name:    "auto version",
			cfg:     &TLSSettingsConfig{MinVersion: strPtr("Auto")},
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

func TestTLSSettingsConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  *TLSSettingsConfig
		want bool
	}{
		{name: "nil", cfg: nil, want: true},
		{name: "empty", cfg: &TLSSettingsConfig{}, want: true},
		{name: "empty string min version", cfg: &TLSSettingsConfig{MinVersion: strPtr("")}, want: true},
		{name: "with min version", cfg: &TLSSettingsConfig{MinVersion: strPtr("TLS1.2")}, want: false},
		{name: "with ciphers", cfg: &TLSSettingsConfig{Ciphers: []string{"TLS_AES"}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestDomainMTLSConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *DomainMTLSConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "disabled", cfg: &DomainMTLSConfig{Enabled: false}, wantErr: false},
		{
			name:    "enabled without CAs",
			cfg:     &DomainMTLSConfig{Enabled: true},
			wantErr: true,
			errMsg:  "at least one CA certificate",
		},
		{
			name: "enabled with CA",
			cfg: &DomainMTLSConfig{
				Enabled: true,
				CACerts: []MTLSCACert{{ID: "1", Name: "ca", SecretName: "ca-secret"}},
			},
			wantErr: false,
		},
		{
			name: "invalid SAN type",
			cfg: &DomainMTLSConfig{
				Enabled:      true,
				CACerts:      []MTLSCACert{{ID: "1", Name: "ca", SecretName: "ca-secret"}},
				SANWhitelist: []MTLSSANEntry{{Type: "Invalid", Value: "test"}},
			},
			wantErr: true,
			errMsg:  "type must be 'DNS' or 'URI'",
		},
		{
			name: "empty SAN value",
			cfg: &DomainMTLSConfig{
				Enabled:      true,
				CACerts:      []MTLSCACert{{ID: "1", Name: "ca", SecretName: "ca-secret"}},
				SANWhitelist: []MTLSSANEntry{{Type: "DNS", Value: ""}},
			},
			wantErr: true,
			errMsg:  "value cannot be empty",
		},
		{
			name: "invalid hash length",
			cfg: &DomainMTLSConfig{
				Enabled:       true,
				CACerts:       []MTLSCACert{{ID: "1", Name: "ca", SecretName: "ca-secret"}},
				HashWhitelist: []string{"short"},
			},
			wantErr: true,
			errMsg:  "must be 64 hex characters",
		},
		{
			name: "valid hash",
			cfg: &DomainMTLSConfig{
				Enabled:       true,
				CACerts:       []MTLSCACert{{ID: "1", Name: "ca", SecretName: "ca-secret"}},
				HashWhitelist: []string{"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"},
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

// TestDomainMTLSConfig_ValidateShape pins the two rules split out of
// Validate() (mtls-warning-brief.md, Change 2): SAN entry shape and hash
// whitelist shape. Unlike Validate(), ValidateShape() must NOT require a CA
// certificate to be present -- a domain can legitimately have
// mtls.enabled=true and zero domain-level CACerts because a client
// attachment can supply the CA instead (DomainService.collectCASecretRefs).
// This is the method wired into UpdateDomainSettings; Validate() (with its
// CA requirement) is not.
func TestDomainMTLSConfig_ValidateShape(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *DomainMTLSConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "disabled", cfg: &DomainMTLSConfig{Enabled: false}, wantErr: false},
		{
			name:    "enabled with no CAs and no SAN/hash entries -- shape checks do not require a CA",
			cfg:     &DomainMTLSConfig{Enabled: true},
			wantErr: false,
		},
		{
			name: "SAN type EMAIL rejected",
			cfg: &DomainMTLSConfig{
				Enabled:      true,
				SANWhitelist: []MTLSSANEntry{{Type: "EMAIL", Value: "test@example.com"}},
			},
			wantErr: true,
			errMsg:  "type must be 'DNS' or 'URI'",
		},
		{
			name: "SAN type DNS accepted",
			cfg: &DomainMTLSConfig{
				Enabled:      true,
				SANWhitelist: []MTLSSANEntry{{Type: "DNS", Value: "example.com"}},
			},
			wantErr: false,
		},
		{
			name: "SAN type URI accepted",
			cfg: &DomainMTLSConfig{
				Enabled:      true,
				SANWhitelist: []MTLSSANEntry{{Type: "URI", Value: "spiffe://example.com/service"}},
			},
			wantErr: false,
		},
		{
			name: "empty SAN value rejected",
			cfg: &DomainMTLSConfig{
				Enabled:      true,
				SANWhitelist: []MTLSSANEntry{{Type: "DNS", Value: ""}},
			},
			wantErr: true,
			errMsg:  "value cannot be empty",
		},
		{
			name: "hash of 63 chars rejected",
			cfg: &DomainMTLSConfig{
				Enabled:       true,
				HashWhitelist: []string{"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b"},
			},
			wantErr: true,
			errMsg:  "must be 64 hex characters",
		},
		{
			name: "hash of 64 hex chars accepted",
			cfg: &DomainMTLSConfig{
				Enabled:       true,
				HashWhitelist: []string{"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"},
			},
			wantErr: false,
		},
		{
			name: "hash of 64 chars but non-hex characters rejected",
			cfg: &DomainMTLSConfig{
				Enabled:       true,
				HashWhitelist: []string{"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
			},
			wantErr: true,
			errMsg:  "hex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.ValidateShape()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConnectionLimitConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ConnectionLimitConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "empty", cfg: &ConnectionLimitConfig{}, wantErr: false},
		{
			name:    "negative maxConnections",
			cfg:     &ConnectionLimitConfig{MaxConnections: int32Ptr(-1)},
			wantErr: true,
			errMsg:  "maxConnections must be non-negative",
		},
		{
			name:    "valid maxConnections",
			cfg:     &ConnectionLimitConfig{MaxConnections: int32Ptr(100)},
			wantErr: false,
		},
		{
			name:    "invalid closeDelay",
			cfg:     &ConnectionLimitConfig{CloseDelay: strPtr("bad")},
			wantErr: true,
			errMsg:  "invalid closeDelay duration",
		},
		{
			name:    "valid closeDelay",
			cfg:     &ConnectionLimitConfig{CloseDelay: strPtr("5s")},
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

func TestTCPKeepaliveConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *TCPKeepaliveConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "empty", cfg: &TCPKeepaliveConfig{}, wantErr: false},
		{
			name:    "negative probes",
			cfg:     &TCPKeepaliveConfig{Probes: int32Ptr(-1)},
			wantErr: true,
			errMsg:  "probes must be non-negative",
		},
		{
			name:    "invalid idleTime",
			cfg:     &TCPKeepaliveConfig{IdleTime: strPtr("bad")},
			wantErr: true,
			errMsg:  "invalid idleTime duration",
		},
		{
			name:    "valid config",
			cfg:     &TCPKeepaliveConfig{Probes: int32Ptr(3), IdleTime: strPtr("60s"), Interval: strPtr("10s")},
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

func TestGetTLSProfileConfig(t *testing.T) {
	tests := []struct {
		name       string
		profile    string
		wantNil    bool
		wantMinVer string
	}{
		{name: "modern", profile: "modern", wantNil: false, wantMinVer: "TLS1.3"},
		{name: "intermediate", profile: "intermediate", wantNil: false, wantMinVer: "TLS1.2"},
		{name: "compatible", profile: "compatible", wantNil: false, wantMinVer: "TLS1.0"},
		{name: "unknown", profile: "unknown", wantNil: true},
		{name: "custom", profile: "custom", wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetTLSProfileConfig(tt.profile)
			if tt.wantNil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, tt.wantMinVer, *result.MinVersion)
			}
		})
	}
}

func TestHTTPTimeoutConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  *HTTPTimeoutConfig
		want bool
	}{
		{name: "nil", cfg: nil, want: true},
		{name: "empty", cfg: &HTTPTimeoutConfig{}, want: true},
		{name: "empty strings", cfg: &HTTPTimeoutConfig{RequestReceivedTimeout: strPtr(""), IdleTimeout: strPtr("")}, want: true},
		{name: "with value", cfg: &HTTPTimeoutConfig{RequestReceivedTimeout: strPtr("30s")}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestDomainSettingsConfig_ScanValue(t *testing.T) {
	cfg := DomainSettingsConfig{
		HTTP3: &HTTP3Config{Enabled: true},
	}

	val, err := cfg.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	var scanned DomainSettingsConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.True(t, scanned.HTTP3.Enabled)

	// Scan nil
	var nilScanned DomainSettingsConfig
	err = nilScanned.Scan(nil)
	assert.NoError(t, err)
	assert.True(t, nilScanned.IsEmpty())

	// Scan wrong type
	err = nilScanned.Scan(123)
	assert.Error(t, err)
}

func TestClientConnectionConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ClientConnectionConfig
		wantErr bool
		errMsg  string
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "empty", cfg: &ClientConnectionConfig{}, wantErr: false},
		{
			name: "valid tcp keepalive",
			cfg: &ClientConnectionConfig{
				TCPKeepalive: &TCPKeepaliveConfig{Probes: int32Ptr(3)},
			},
			wantErr: false,
		},
		{
			name: "invalid tcp keepalive",
			cfg: &ClientConnectionConfig{
				TCPKeepalive: &TCPKeepaliveConfig{Probes: int32Ptr(-1)},
			},
			wantErr: true,
			errMsg:  "probes must be non-negative",
		},
		{
			name: "invalid connection limit",
			cfg: &ClientConnectionConfig{
				ConnectionLimit: &ConnectionLimitConfig{MaxConnections: int32Ptr(-1)},
			},
			wantErr: true,
			errMsg:  "maxConnections must be non-negative",
		},
		{
			name: "valid proxy protocol",
			cfg: &ClientConnectionConfig{
				ProxyProtocol: &ProxyProtocolConfig{Enabled: true},
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

func TestDomainSettingsConfig_Validate(t *testing.T) {
	// Test TimeoutConfig.Validate via DomainSettingsConfig
	cfg := &TimeoutConfig{
		HTTP: &HTTPTimeoutConfig{RequestReceivedTimeout: strPtr("bad")},
	}
	err := cfg.Validate()
	assert.Error(t, err)

	// Valid timeout
	cfg = &TimeoutConfig{
		HTTP: &HTTPTimeoutConfig{RequestReceivedTimeout: strPtr("30s")},
	}
	err = cfg.Validate()
	assert.NoError(t, err)

	// Nil timeout
	var nilT *TimeoutConfig
	err = nilT.Validate()
	assert.NoError(t, err)
}

func TestClientConnectionConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ClientConnectionConfig
		want bool
	}{
		{name: "nil", cfg: nil, want: true},
		{name: "empty", cfg: &ClientConnectionConfig{}, want: true},
		{name: "with tcp keepalive", cfg: &ClientConnectionConfig{TCPKeepalive: &TCPKeepaliveConfig{}}, want: false},
		{name: "with proxy protocol enabled", cfg: &ClientConnectionConfig{ProxyProtocol: &ProxyProtocolConfig{Enabled: true}}, want: false},
		{name: "with proxy protocol disabled", cfg: &ClientConnectionConfig{ProxyProtocol: &ProxyProtocolConfig{Enabled: false}}, want: true},
		{name: "with buffer limit", cfg: &ClientConnectionConfig{BufferLimit: strPtr("32Ki")}, want: false},
		{name: "with empty buffer limit", cfg: &ClientConnectionConfig{BufferLimit: strPtr("")}, want: true},
		{name: "with connection limit empty", cfg: &ClientConnectionConfig{ConnectionLimit: &ConnectionLimitConfig{}}, want: true},
		{name: "with connection limit non-empty", cfg: &ClientConnectionConfig{ConnectionLimit: &ConnectionLimitConfig{MaxConnections: int32Ptr(100)}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestConnectionLimitConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ConnectionLimitConfig
		want bool
	}{
		{name: "nil", cfg: nil, want: true},
		{name: "empty", cfg: &ConnectionLimitConfig{}, want: true},
		{name: "with maxConnections", cfg: &ConnectionLimitConfig{MaxConnections: int32Ptr(10)}, want: false},
		{name: "with closeDelay empty", cfg: &ConnectionLimitConfig{CloseDelay: strPtr("")}, want: true},
		{name: "with closeDelay set", cfg: &ConnectionLimitConfig{CloseDelay: strPtr("5s")}, want: false},
		{name: "with maxConnectionDuration empty", cfg: &ConnectionLimitConfig{MaxConnectionDuration: strPtr("")}, want: true},
		{name: "with maxConnectionDuration set", cfg: &ConnectionLimitConfig{MaxConnectionDuration: strPtr("1h")}, want: false},
		{name: "with maxRequestsPerConnection", cfg: &ConnectionLimitConfig{MaxRequestsPerConnection: int32Ptr(100)}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestConnectionLimitConfig_Validate_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ConnectionLimitConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "negative maxRequestsPerConnection",
			cfg:     &ConnectionLimitConfig{MaxRequestsPerConnection: int32Ptr(-1)},
			wantErr: true,
			errMsg:  "maxRequestsPerConnection must be non-negative",
		},
		{
			name:    "invalid maxConnectionDuration",
			cfg:     &ConnectionLimitConfig{MaxConnectionDuration: strPtr("bad")},
			wantErr: true,
			errMsg:  "invalid maxConnectionDuration",
		},
		{
			name:    "valid maxConnectionDuration",
			cfg:     &ConnectionLimitConfig{MaxConnectionDuration: strPtr("1h")},
			wantErr: false,
		},
		{
			name:    "empty closeDelay is valid",
			cfg:     &ConnectionLimitConfig{CloseDelay: strPtr("")},
			wantErr: false,
		},
		{
			name:    "zero maxConnections is valid",
			cfg:     &ConnectionLimitConfig{MaxConnections: int32Ptr(0)},
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

func TestTCPKeepaliveConfig_Validate_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *TCPKeepaliveConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "invalid interval",
			cfg:     &TCPKeepaliveConfig{Interval: strPtr("bad")},
			wantErr: true,
			errMsg:  "invalid interval duration",
		},
		{
			name:    "empty interval is valid",
			cfg:     &TCPKeepaliveConfig{Interval: strPtr("")},
			wantErr: false,
		},
		{
			name:    "empty idleTime is valid",
			cfg:     &TCPKeepaliveConfig{IdleTime: strPtr("")},
			wantErr: false,
		},
		{
			name:    "zero probes is valid",
			cfg:     &TCPKeepaliveConfig{Probes: int32Ptr(0)},
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

func TestDomainMTLSConfig_Validate_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *DomainMTLSConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid DNS SAN",
			cfg: &DomainMTLSConfig{
				Enabled:      true,
				CACerts:      []MTLSCACert{{ID: "1", Name: "ca", SecretName: "ca-secret"}},
				SANWhitelist: []MTLSSANEntry{{Type: "DNS", Value: "example.com"}},
			},
			wantErr: false,
		},
		{
			name: "valid URI SAN",
			cfg: &DomainMTLSConfig{
				Enabled:      true,
				CACerts:      []MTLSCACert{{ID: "1", Name: "ca", SecretName: "ca-secret"}},
				SANWhitelist: []MTLSSANEntry{{Type: "URI", Value: "spiffe://example.com/service"}},
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

func TestClientIPDetectionConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ClientIPDetectionConfig
		want bool
	}{
		{name: "nil", cfg: nil, want: true},
		{name: "empty", cfg: &ClientIPDetectionConfig{}, want: true},
		{name: "with XForwardedFor", cfg: &ClientIPDetectionConfig{XForwardedFor: &XForwardedForConfig{NumTrustedHops: 1}}, want: false},
		{name: "with CustomHeader", cfg: &ClientIPDetectionConfig{CustomHeader: &CustomHeaderConfig{Name: "X-Real-IP"}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestTimeoutConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  *TimeoutConfig
		want bool
	}{
		{name: "nil", cfg: nil, want: true},
		{name: "empty", cfg: &TimeoutConfig{}, want: true},
		{name: "with nil HTTP", cfg: &TimeoutConfig{HTTP: nil}, want: true},
		{name: "with empty HTTP", cfg: &TimeoutConfig{HTTP: &HTTPTimeoutConfig{}}, want: true},
		{name: "with HTTP value", cfg: &TimeoutConfig{HTTP: &HTTPTimeoutConfig{RequestReceivedTimeout: strPtr("30s")}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestHTTPTimeoutConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *HTTPTimeoutConfig
		wantErr bool
	}{
		{name: "nil", cfg: nil, wantErr: false},
		{name: "valid", cfg: &HTTPTimeoutConfig{RequestReceivedTimeout: strPtr("30s")}, wantErr: false},
		{name: "invalid duration", cfg: &HTTPTimeoutConfig{RequestReceivedTimeout: strPtr("bad")}, wantErr: true},
		{name: "invalid idle timeout", cfg: &HTTPTimeoutConfig{IdleTimeout: strPtr("bad")}, wantErr: true},
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

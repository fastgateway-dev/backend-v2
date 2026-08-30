package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecurityPolicyConfig_HasAnyConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  SecurityPolicyConfig
		want bool
	}{
		{name: "empty", cfg: SecurityPolicyConfig{}, want: false},
		{name: "with CORS", cfg: SecurityPolicyConfig{CORS: &CORSConfig{}}, want: true},
		{name: "with Authorization", cfg: SecurityPolicyConfig{Authorization: &AuthorizationConfig{}}, want: true},
		{name: "with APIKeyAuth", cfg: SecurityPolicyConfig{APIKeyAuth: &APIKeyAuthConfig{}}, want: true},
		{name: "with BasicAuth", cfg: SecurityPolicyConfig{BasicAuth: &BasicAuthConfig{}}, want: true},
		{name: "with JWT", cfg: SecurityPolicyConfig{JWT: &JWTConfig{}}, want: true},
		{name: "with OIDC", cfg: SecurityPolicyConfig{OIDC: &OIDCConfig{}}, want: true},
		{name: "with ExtAuth", cfg: SecurityPolicyConfig{ExtAuth: &ExtAuthConfig{}}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.HasAnyConfig())
		})
	}
}

func TestExtAuthConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ExtAuthConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "invalid type",
			cfg:     ExtAuthConfig{Type: "bad"},
			wantErr: true,
			errMsg:  "type must be 'http' or 'grpc'",
		},
		{
			name:    "http type without config",
			cfg:     ExtAuthConfig{Type: "http"},
			wantErr: true,
			errMsg:  "http config is required",
		},
		{
			name:    "grpc type without config",
			cfg:     ExtAuthConfig{Type: "grpc"},
			wantErr: true,
			errMsg:  "grpc config is required",
		},
		{
			name: "valid http",
			cfg: ExtAuthConfig{
				Type: "http",
				HTTP: &ExtAuthHTTPConfig{
					BackendRef: ExtAuthBackendRef{Name: "auth-svc", Port: 8080},
					Path:       "/auth",
				},
			},
			wantErr: false,
		},
		{
			name: "valid grpc",
			cfg: ExtAuthConfig{
				Type: "grpc",
				GRPC: &ExtAuthGRPCConfig{
					BackendRef: ExtAuthBackendRef{Name: "auth-svc", Port: 9090},
				},
			},
			wantErr: false,
		},
		{
			name: "http path without leading slash",
			cfg: ExtAuthConfig{
				Type: "http",
				HTTP: &ExtAuthHTTPConfig{
					BackendRef: ExtAuthBackendRef{Name: "auth-svc", Port: 8080},
					Path:       "auth",
				},
			},
			wantErr: true,
			errMsg:  "path must start with '/'",
		},
		{
			name: "with request body exceeding limit",
			cfg: ExtAuthConfig{
				Type: "http",
				HTTP: &ExtAuthHTTPConfig{
					BackendRef: ExtAuthBackendRef{Name: "auth-svc", Port: 8080},
					Path:       "/auth",
				},
				WithRequestBody: &ExtAuthRequestBody{MaxBytes: 11 * 1024 * 1024},
			},
			wantErr: true,
			errMsg:  "maxBytes cannot exceed 10MB",
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

func TestSecurityPolicyConfig_ScanValue(t *testing.T) {
	cfg := SecurityPolicyConfig{
		CORS: &CORSConfig{AllowOrigins: []string{"*"}},
	}

	// Test Value
	val, err := cfg.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Test Scan with valid bytes
	var scanned SecurityPolicyConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.NotNil(t, scanned.CORS)
	assert.Equal(t, []string{"*"}, scanned.CORS.AllowOrigins)

	// Test Scan nil
	var nilScanned SecurityPolicyConfig
	err = nilScanned.Scan(nil)
	assert.NoError(t, err)
	assert.False(t, nilScanned.HasAnyConfig())

	// Test Scan wrong type
	err = nilScanned.Scan(123)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type assertion")
}

func TestExtAuthConfig_ScanValue(t *testing.T) {
	cfg := ExtAuthConfig{
		Type: "http",
		HTTP: &ExtAuthHTTPConfig{
			BackendRef: ExtAuthBackendRef{Name: "auth-svc", Port: 8080},
			Path:       "/auth",
		},
	}

	// Test Value
	val, err := cfg.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Test Scan with valid bytes
	var scanned ExtAuthConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, "http", scanned.Type)

	// Test Scan nil
	err = scanned.Scan(nil)
	assert.NoError(t, err)

	// Test Scan wrong type
	err = scanned.Scan(123)
	assert.Error(t, err)
}

func TestExtAuthHTTPConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ExtAuthHTTPConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid",
			cfg: ExtAuthHTTPConfig{
				BackendRef: ExtAuthBackendRef{Name: "svc", Port: 8080},
				Path:       "/auth",
			},
			wantErr: false,
		},
		{
			name: "missing path",
			cfg: ExtAuthHTTPConfig{
				BackendRef: ExtAuthBackendRef{Name: "svc", Port: 8080},
				Path:       "",
			},
			wantErr: true,
			errMsg:  "path is required",
		},
		{
			name: "missing backend name",
			cfg: ExtAuthHTTPConfig{
				BackendRef: ExtAuthBackendRef{Name: "", Port: 8080},
				Path:       "/auth",
			},
			wantErr: true,
			errMsg:  "name is required",
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

func TestExtAuthGRPCConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ExtAuthGRPCConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid",
			cfg:     ExtAuthGRPCConfig{BackendRef: ExtAuthBackendRef{Name: "svc", Port: 9090}},
			wantErr: false,
		},
		{
			name:    "missing backend name",
			cfg:     ExtAuthGRPCConfig{BackendRef: ExtAuthBackendRef{Port: 9090}},
			wantErr: true,
			errMsg:  "name is required",
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

func TestExtAuthBackendRef_Validate(t *testing.T) {
	tests := []struct {
		name    string
		ref     ExtAuthBackendRef
		wantErr bool
		errMsg  string
	}{
		{name: "valid", ref: ExtAuthBackendRef{Name: "svc", Port: 8080}, wantErr: false},
		{name: "missing name", ref: ExtAuthBackendRef{Port: 8080}, wantErr: true, errMsg: "name is required"},
		{name: "port 0", ref: ExtAuthBackendRef{Name: "svc", Port: 0}, wantErr: true, errMsg: "port must be between"},
		{name: "port too high", ref: ExtAuthBackendRef{Name: "svc", Port: 65536}, wantErr: true, errMsg: "port must be between"},
		{name: "port 1", ref: ExtAuthBackendRef{Name: "svc", Port: 1}, wantErr: false},
		{name: "port 65535", ref: ExtAuthBackendRef{Name: "svc", Port: 65535}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

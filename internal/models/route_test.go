package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRouteConfig_HasFailover(t *testing.T) {
	tests := []struct {
		name string
		cfg  RouteConfig
		want bool
	}{
		{
			name: "no backends",
			cfg:  RouteConfig{},
			want: false,
		},
		{
			name: "backends without fallback",
			cfg: RouteConfig{
				Backends: []RouteBackend{
					{Service: "svc1", Port: 80},
					{Service: "svc2", Port: 80},
				},
			},
			want: false,
		},
		{
			name: "one backend with fallback",
			cfg: RouteConfig{
				Backends: []RouteBackend{
					{Service: "svc1", Port: 80},
					{Service: "svc2", Port: 80, Fallback: true},
				},
			},
			want: true,
		},
		{
			name: "all backends with fallback",
			cfg: RouteConfig{
				Backends: []RouteBackend{
					{Service: "svc1", Port: 80, Fallback: true},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.HasFailover())
		})
	}
}

func TestDirectResponseConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     DirectResponseConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid minimal",
			cfg:     DirectResponseConfig{StatusCode: 200},
			wantErr: false,
		},
		{
			name:    "status code too low",
			cfg:     DirectResponseConfig{StatusCode: 99},
			wantErr: true,
			errMsg:  "statusCode must be between 100 and 599",
		},
		{
			name:    "status code too high",
			cfg:     DirectResponseConfig{StatusCode: 600},
			wantErr: true,
			errMsg:  "statusCode must be between 100 and 599",
		},
		{
			name: "valid inline body",
			cfg: DirectResponseConfig{
				StatusCode: 200,
				Body:       &DirectResponseBody{Type: DirectResponseBodyTypeInline, Inline: "hello"},
			},
			wantErr: false,
		},
		{
			name: "invalid body type",
			cfg: DirectResponseConfig{
				StatusCode: 200,
				Body:       &DirectResponseBody{Type: "Invalid"},
			},
			wantErr: true,
			errMsg:  "body type must be",
		},
		{
			name: "inline body too large",
			cfg: DirectResponseConfig{
				StatusCode: 200,
				Body:       &DirectResponseBody{Type: DirectResponseBodyTypeInline, Inline: string(make([]byte, 4097))},
			},
			wantErr: true,
			errMsg:  "inline body exceeds maximum size",
		},
		{
			name: "inline body at max size",
			cfg: DirectResponseConfig{
				StatusCode: 200,
				Body:       &DirectResponseBody{Type: DirectResponseBodyTypeInline, Inline: string(make([]byte, 4096))},
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

func TestRouteConfig_ScanValue(t *testing.T) {
	cfg := RouteConfig{
		RouteType: RouteTypeBackend,
		Matches:   []RouteMatch{{Path: &PathMatch{Type: "Prefix", Value: "/"}}},
		Backends:  []RouteBackend{{Type: BackendTypeKubernetes, Service: "svc", Port: 80}},
	}

	val, err := cfg.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	var scanned RouteConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, RouteTypeBackend, scanned.RouteType)
	assert.Len(t, scanned.Backends, 1)

	// Scan nil
	var nilScanned RouteConfig
	err = nilScanned.Scan(nil)
	assert.NoError(t, err)

	// Scan wrong type
	err = nilScanned.Scan(123)
	assert.Error(t, err)
}

func TestDirectResponseConfig_Validate_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     DirectResponseConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "status code 100 (lower boundary)",
			cfg:     DirectResponseConfig{StatusCode: 100},
			wantErr: false,
		},
		{
			name:    "status code 599 (upper boundary)",
			cfg:     DirectResponseConfig{StatusCode: 599},
			wantErr: false,
		},
		{
			name:    "status code 0",
			cfg:     DirectResponseConfig{StatusCode: 0},
			wantErr: true,
			errMsg:  "statusCode must be between 100 and 599",
		},
		{
			name: "ValueRef body type valid",
			cfg: DirectResponseConfig{
				StatusCode: 200,
				Body:       &DirectResponseBody{Type: DirectResponseBodyTypeValueRef},
			},
			wantErr: false,
		},
		{
			name:    "no body is valid",
			cfg:     DirectResponseConfig{StatusCode: 204},
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

func TestBackendTLSConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *BackendTLSConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: false,
		},
		{
			name:    "missing mode",
			cfg:     &BackendTLSConfig{},
			wantErr: true,
			errMsg:  "mode is required when tls is configured",
		},
		{
			name: "invalid mode",
			cfg: &BackendTLSConfig{
				Mode: "invalid",
			},
			wantErr: true,
			errMsg:  "mode must be 'simple' or 'mtls', got 'invalid'",
		},
		{
			name: "simple mode: valid with CA refs",
			cfg: &BackendTLSConfig{
				Mode:              BackendTLSModeSimple,
				CACertificateRefs: []CertificateRef{{Kind: "Secret", Name: "my-ca"}},
			},
			wantErr: false,
		},
		{
			name: "simple mode: missing CA refs",
			cfg: &BackendTLSConfig{
				Mode: BackendTLSModeSimple,
			},
			wantErr: true,
			errMsg:  "caCertificateRefs is required when insecureSkipVerify is false",
		},
		{
			name: "simple mode: invalid CA ref kind",
			cfg: &BackendTLSConfig{
				Mode:              BackendTLSModeSimple,
				CACertificateRefs: []CertificateRef{{Kind: "Invalid", Name: "my-ca"}},
			},
			wantErr: true,
			errMsg:  "kind must be 'Secret' or 'ConfigMap'",
		},
		{
			name: "simple mode: CA ref missing name",
			cfg: &BackendTLSConfig{
				Mode:              BackendTLSModeSimple,
				CACertificateRefs: []CertificateRef{{Kind: "Secret", Name: ""}},
			},
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name: "simple mode: clientCertificateRef rejected",
			cfg: &BackendTLSConfig{
				Mode:                 BackendTLSModeSimple,
				CACertificateRefs:    []CertificateRef{{Kind: "Secret", Name: "ca"}},
				ClientCertificateRef: &SecretRef{Name: "client-cert"},
			},
			wantErr: true,
			errMsg:  "clientCertificateRef is not allowed in simple mode",
		},
		{
			name: "simple mode: insecureSkipVerify valid",
			cfg: &BackendTLSConfig{
				Mode:               BackendTLSModeSimple,
				InsecureSkipVerify: true,
			},
			wantErr: false,
		},
		{
			name: "simple mode: insecureSkipVerify rejects CA refs",
			cfg: &BackendTLSConfig{
				Mode:               BackendTLSModeSimple,
				InsecureSkipVerify: true,
				CACertificateRefs:  []CertificateRef{{Kind: "Secret", Name: "ca"}},
			},
			wantErr: true,
			errMsg:  "caCertificateRefs must not be set when insecureSkipVerify is true",
		},
		{
			name: "mtls mode: valid with CA refs and client cert",
			cfg: &BackendTLSConfig{
				Mode:                 BackendTLSModeMTLS,
				CACertificateRefs:    []CertificateRef{{Kind: "Secret", Name: "ca"}},
				ClientCertificateRef: &SecretRef{Name: "client-cert"},
			},
			wantErr: false,
		},
		{
			name: "mtls mode: missing client cert",
			cfg: &BackendTLSConfig{
				Mode:              BackendTLSModeMTLS,
				CACertificateRefs: []CertificateRef{{Kind: "Secret", Name: "ca"}},
			},
			wantErr: true,
			errMsg:  "clientCertificateRef is required for mtls mode",
		},
		{
			name: "mtls mode: client cert missing name",
			cfg: &BackendTLSConfig{
				Mode:                 BackendTLSModeMTLS,
				CACertificateRefs:    []CertificateRef{{Kind: "Secret", Name: "ca"}},
				ClientCertificateRef: &SecretRef{Name: ""},
			},
			wantErr: true,
			errMsg:  "clientCertificateRef.name is required",
		},
		{
			name: "mtls mode: missing CA refs",
			cfg: &BackendTLSConfig{
				Mode:                 BackendTLSModeMTLS,
				ClientCertificateRef: &SecretRef{Name: "client-cert"},
			},
			wantErr: true,
			errMsg:  "caCertificateRefs is required when insecureSkipVerify is false",
		},
		{
			name: "mtls mode: insecureSkipVerify valid with client cert",
			cfg: &BackendTLSConfig{
				Mode:                 BackendTLSModeMTLS,
				InsecureSkipVerify:   true,
				ClientCertificateRef: &SecretRef{Name: "client-cert"},
			},
			wantErr: false,
		},
		{
			name: "mtls mode: insecureSkipVerify rejects CA refs",
			cfg: &BackendTLSConfig{
				Mode:                 BackendTLSModeMTLS,
				InsecureSkipVerify:   true,
				CACertificateRefs:    []CertificateRef{{Kind: "Secret", Name: "ca"}},
				ClientCertificateRef: &SecretRef{Name: "client-cert"},
			},
			wantErr: true,
			errMsg:  "caCertificateRefs must not be set when insecureSkipVerify is true",
		},
		{
			name: "simple mode: with sni override",
			cfg: &BackendTLSConfig{
				Mode:              BackendTLSModeSimple,
				SNI:               "custom.backend.svc",
				CACertificateRefs: []CertificateRef{{Kind: "Secret", Name: "ca"}},
			},
			wantErr: false,
		},
		{
			name: "valid ConfigMap CA ref",
			cfg: &BackendTLSConfig{
				Mode:              BackendTLSModeSimple,
				CACertificateRefs: []CertificateRef{{Kind: "ConfigMap", Name: "my-ca"}},
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

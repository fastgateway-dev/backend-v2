package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvoyExtensionPolicyConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  EnvoyExtensionPolicyConfig
		want bool
	}{
		{name: "empty", cfg: EnvoyExtensionPolicyConfig{}, want: true},
		{name: "with lua", cfg: EnvoyExtensionPolicyConfig{Lua: &LuaExtensionConfig{}}, want: false},
		{name: "with wasm", cfg: EnvoyExtensionPolicyConfig{Wasm: &WasmExtensionConfig{}}, want: false},
		{name: "with both", cfg: EnvoyExtensionPolicyConfig{Lua: &LuaExtensionConfig{}, Wasm: &WasmExtensionConfig{}}, want: false},
		{name: "with extProc", cfg: EnvoyExtensionPolicyConfig{ExtProc: &ExtProcExtensionConfig{BackendRef: ExtProcBackendRef{Name: "test", Namespace: "default", Port: 9002}}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestEnvoyExtensionPolicyConfig_HasContent(t *testing.T) {
	assert.False(t, (&EnvoyExtensionPolicyConfig{}).HasContent())
	assert.True(t, (&EnvoyExtensionPolicyConfig{Lua: &LuaExtensionConfig{}}).HasContent())
	assert.True(t, (&EnvoyExtensionPolicyConfig{ExtProc: &ExtProcExtensionConfig{}}).HasContent())
}

func TestEnvoyExtensionPolicyConfig_ScanValue(t *testing.T) {
	cfg := EnvoyExtensionPolicyConfig{
		Lua: &LuaExtensionConfig{Type: "Inline", Inline: "print('hello')"},
	}

	val, err := cfg.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	var scanned EnvoyExtensionPolicyConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.NotNil(t, scanned.Lua)
	assert.Equal(t, "Inline", scanned.Lua.Type)

	// Scan nil
	var nilScanned EnvoyExtensionPolicyConfig
	err = nilScanned.Scan(nil)
	assert.NoError(t, err)
	assert.True(t, nilScanned.IsEmpty())

	// Scan wrong type
	err = nilScanned.Scan(123)
	assert.Error(t, err)

	// Scan/Value with ExtProc
	extProcCfg := EnvoyExtensionPolicyConfig{
		ExtProc: &ExtProcExtensionConfig{
			BackendRef: ExtProcBackendRef{Name: "grpc-ext-proc", Namespace: "default", Port: 9002},
			ProcessingMode: &ExtProcProcessingMode{
				Request:  &ExtProcBodyMode{Body: "Buffered"},
				Response: &ExtProcBodyMode{Body: "Streamed"},
			},
			FailOpen: true,
		},
	}
	epVal, err := extProcCfg.Value()
	assert.NoError(t, err)

	var epScanned EnvoyExtensionPolicyConfig
	err = epScanned.Scan(epVal.([]byte))
	assert.NoError(t, err)
	assert.NotNil(t, epScanned.ExtProc)
	assert.Equal(t, "grpc-ext-proc", epScanned.ExtProc.BackendRef.Name)
	assert.Equal(t, "Buffered", epScanned.ExtProc.ProcessingMode.Request.Body)
	assert.Equal(t, "Streamed", epScanned.ExtProc.ProcessingMode.Response.Body)
	assert.True(t, epScanned.ExtProc.FailOpen)
}

func TestWasmHTTPSource_Validate_EdgeCases(t *testing.T) {
	validSHA := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	tests := []struct {
		name    string
		src     WasmHTTPSource
		wantErr bool
		errMsg  string
	}{
		{
			name:    "missing URL",
			src:     WasmHTTPSource{SHA256: validSHA},
			wantErr: true,
			errMsg:  "url is required",
		},
		{
			name:    "invalid URL",
			src:     WasmHTTPSource{URL: "not a url", SHA256: validSHA},
			wantErr: true,
			errMsg:  "url is not valid",
		},
		{
			name:    "invalid SHA256",
			src:     WasmHTTPSource{URL: "https://example.com/mod.wasm", SHA256: "invalid"},
			wantErr: true,
			errMsg:  "sha256 must be a 64-character hex string",
		},
		{
			name:    "valid",
			src:     WasmHTTPSource{URL: "https://example.com/mod.wasm", SHA256: validSHA},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.src.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestWasmImageSource_Validate(t *testing.T) {
	tests := []struct {
		name    string
		src     WasmImageSource
		wantErr bool
		errMsg  string
	}{
		{name: "missing URL", src: WasmImageSource{}, wantErr: true, errMsg: "url is required"},
		{name: "valid without SHA", src: WasmImageSource{URL: "oci://reg/mod:v1"}, wantErr: false},
		{
			name: "valid with SHA",
			src: WasmImageSource{
				URL:    "oci://reg/mod:v1",
				SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
			wantErr: false,
		},
		{
			name:    "invalid SHA",
			src:     WasmImageSource{URL: "oci://reg/mod:v1", SHA256: "short"},
			wantErr: true,
			errMsg:  "sha256 must be a 64-character hex string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.src.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEnvoyExtensionPolicyConfig_Validate(t *testing.T) {
	validSHA := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	tests := []struct {
		name    string
		cfg     EnvoyExtensionPolicyConfig
		wantErr bool
		errMsg  string
	}{
		{name: "empty is valid", cfg: EnvoyExtensionPolicyConfig{}, wantErr: false},
		{
			name: "valid config with extProc",
			cfg: EnvoyExtensionPolicyConfig{
				ExtProc: &ExtProcExtensionConfig{
					BackendRef: ExtProcBackendRef{Name: "grpc-ext-proc", Namespace: "default", Port: 9002},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid config with bad extProc",
			cfg: EnvoyExtensionPolicyConfig{
				ExtProc: &ExtProcExtensionConfig{
					BackendRef: ExtProcBackendRef{Port: 9002},
				},
			},
			wantErr: true,
			errMsg:  "ext-proc",
		},
		{
			name: "valid lua inline",
			cfg: EnvoyExtensionPolicyConfig{
				Lua: &LuaExtensionConfig{Type: "Inline", Inline: "print('hello')"},
			},
			wantErr: false,
		},
		{
			name: "lua invalid type",
			cfg: EnvoyExtensionPolicyConfig{
				Lua: &LuaExtensionConfig{Type: "Bad"},
			},
			wantErr: true,
			errMsg:  "lua: type must be",
		},
		{
			name: "lua inline empty script",
			cfg: EnvoyExtensionPolicyConfig{
				Lua: &LuaExtensionConfig{Type: "Inline"},
			},
			wantErr: true,
			errMsg:  "inline script is required",
		},
		{
			name: "lua valueRef valid",
			cfg: EnvoyExtensionPolicyConfig{
				Lua: &LuaExtensionConfig{
					Type:     "ValueRef",
					ValueRef: &ValueRef{Name: "my-cm", Kind: "ConfigMap"},
				},
			},
			wantErr: false,
		},
		{
			name: "lua valueRef missing",
			cfg: EnvoyExtensionPolicyConfig{
				Lua: &LuaExtensionConfig{Type: "ValueRef"},
			},
			wantErr: true,
			errMsg:  "valueRef is required",
		},
		{
			name: "lua valueRef missing name",
			cfg: EnvoyExtensionPolicyConfig{
				Lua: &LuaExtensionConfig{
					Type:     "ValueRef",
					ValueRef: &ValueRef{Kind: "ConfigMap"},
				},
			},
			wantErr: true,
			errMsg:  "valueRef.name is required",
		},
		{
			name: "lua valueRef invalid kind",
			cfg: EnvoyExtensionPolicyConfig{
				Lua: &LuaExtensionConfig{
					Type:     "ValueRef",
					ValueRef: &ValueRef{Name: "my-cm", Kind: "Bad"},
				},
			},
			wantErr: true,
			errMsg:  "valueRef.kind must be",
		},
		{
			name: "valid wasm HTTP",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Name: "my-wasm",
					Code: WasmCodeSource{
						Type: "HTTP",
						HTTP: &WasmHTTPSource{
							URL:    "https://example.com/module.wasm",
							SHA256: validSHA,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "wasm missing name",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Code: WasmCodeSource{Type: "Image", Image: &WasmImageSource{URL: "oci://image"}},
				},
			},
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name: "wasm invalid name format",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Name: "Invalid_Name",
					Code: WasmCodeSource{Type: "Image", Image: &WasmImageSource{URL: "oci://image"}},
				},
			},
			wantErr: true,
			errMsg:  "name must be lowercase",
		},
		{
			name: "wasm invalid code type",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Name: "my-wasm",
					Code: WasmCodeSource{Type: "Bad"},
				},
			},
			wantErr: true,
			errMsg:  "type must be 'HTTP' or 'Image'",
		},
		{
			name: "wasm HTTP missing source",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Name: "my-wasm",
					Code: WasmCodeSource{Type: "HTTP"},
				},
			},
			wantErr: true,
			errMsg:  "http is required",
		},
		{
			name: "wasm HTTP missing sha256",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Name: "my-wasm",
					Code: WasmCodeSource{
						Type: "HTTP",
						HTTP: &WasmHTTPSource{URL: "https://example.com/mod.wasm"},
					},
				},
			},
			wantErr: true,
			errMsg:  "sha256 is required",
		},
		{
			name: "wasm Image valid",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Name: "my-wasm",
					Code: WasmCodeSource{
						Type:  "Image",
						Image: &WasmImageSource{URL: "oci://my-registry/module:latest"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "wasm Image missing source",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Name: "my-wasm",
					Code: WasmCodeSource{Type: "Image"},
				},
			},
			wantErr: true,
			errMsg:  "image is required",
		},
		{
			name: "wasm Image missing URL",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Name: "my-wasm",
					Code: WasmCodeSource{
						Type:  "Image",
						Image: &WasmImageSource{},
					},
				},
			},
			wantErr: true,
			errMsg:  "url is required",
		},
		{
			name: "wasm Image invalid sha256",
			cfg: EnvoyExtensionPolicyConfig{
				Wasm: &WasmExtensionConfig{
					Name: "my-wasm",
					Code: WasmCodeSource{
						Type:  "Image",
						Image: &WasmImageSource{URL: "oci://image", SHA256: "short"},
					},
				},
			},
			wantErr: true,
			errMsg:  "sha256 must be a 64-character hex string",
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

func TestExtProcExtensionConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ExtProcExtensionConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid minimal config",
			config: ExtProcExtensionConfig{
				BackendRef: ExtProcBackendRef{Name: "grpc-ext-proc", Namespace: "default", Port: 9002},
			},
			wantErr: false,
		},
		{
			name: "valid full config",
			config: ExtProcExtensionConfig{
				BackendRef: ExtProcBackendRef{Name: "grpc-ext-proc", Namespace: "default", Port: 9002},
				ProcessingMode: &ExtProcProcessingMode{
					Request:  &ExtProcBodyMode{Body: "Buffered"},
					Response: &ExtProcBodyMode{Body: "Streamed"},
				},
				FailOpen: true,
			},
			wantErr: false,
		},
		{
			name:    "missing backend name",
			config:  ExtProcExtensionConfig{BackendRef: ExtProcBackendRef{Namespace: "default", Port: 9002}},
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name:    "missing backend namespace",
			config:  ExtProcExtensionConfig{BackendRef: ExtProcBackendRef{Name: "grpc-ext-proc", Port: 9002}},
			wantErr: true,
			errMsg:  "namespace is required",
		},
		{
			name:    "invalid port zero",
			config:  ExtProcExtensionConfig{BackendRef: ExtProcBackendRef{Name: "grpc-ext-proc", Namespace: "default", Port: 0}},
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name:    "invalid port too high",
			config:  ExtProcExtensionConfig{BackendRef: ExtProcBackendRef{Name: "grpc-ext-proc", Namespace: "default", Port: 70000}},
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name: "invalid body mode",
			config: ExtProcExtensionConfig{
				BackendRef:     ExtProcBackendRef{Name: "grpc-ext-proc", Namespace: "default", Port: 9002},
				ProcessingMode: &ExtProcProcessingMode{Request: &ExtProcBodyMode{Body: "Invalid"}},
			},
			wantErr: true,
			errMsg:  "body must be one of: None, Buffered, Streamed",
		},
		{
			name: "valid body mode None",
			config: ExtProcExtensionConfig{
				BackendRef: ExtProcBackendRef{Name: "grpc-ext-proc", Namespace: "default", Port: 9002},
				ProcessingMode: &ExtProcProcessingMode{
					Request:  &ExtProcBodyMode{Body: "None"},
					Response: &ExtProcBodyMode{Body: "None"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid body mode empty string",
			config: ExtProcExtensionConfig{
				BackendRef:     ExtProcBackendRef{Name: "grpc-ext-proc", Namespace: "default", Port: 9002},
				ProcessingMode: &ExtProcProcessingMode{Request: &ExtProcBodyMode{Body: ""}},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

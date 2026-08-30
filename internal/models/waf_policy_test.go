package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWafPolicyConfig_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		cfg  WafPolicyConfig
		want bool
	}{
		{name: "empty mode", cfg: WafPolicyConfig{}, want: true},
		{name: "with mode", cfg: WafPolicyConfig{Mode: "block"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.IsEmpty())
		})
	}
}

func TestWafPolicyConfig_HasContent(t *testing.T) {
	assert.False(t, (&WafPolicyConfig{}).HasContent())
	assert.True(t, (&WafPolicyConfig{Mode: "block"}).HasContent())
}

func TestWafPolicyConfig_ScanValue(t *testing.T) {
	cfg := WafPolicyConfig{Mode: "block", Rulesets: []string{"owasp-crs"}}

	val, err := cfg.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	var scanned WafPolicyConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, "block", scanned.Mode)
	assert.Equal(t, []string{"owasp-crs"}, scanned.Rulesets)

	// Scan nil
	var nilScanned WafPolicyConfig
	err = nilScanned.Scan(nil)
	assert.NoError(t, err)
	assert.True(t, nilScanned.IsEmpty())

	// Scan wrong type
	err = nilScanned.Scan(123)
	assert.Error(t, err)
}

func TestWafPolicyConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     WafPolicyConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty mode",
			cfg:     WafPolicyConfig{},
			wantErr: true,
			errMsg:  "mode is required",
		},
		{
			name:    "invalid mode",
			cfg:     WafPolicyConfig{Mode: "invalid"},
			wantErr: true,
			errMsg:  "mode must be 'block' or 'detect'",
		},
		{
			name:    "valid block mode",
			cfg:     WafPolicyConfig{Mode: "block"},
			wantErr: false,
		},
		{
			name:    "valid detect mode",
			cfg:     WafPolicyConfig{Mode: "detect"},
			wantErr: false,
		},
		{
			name:    "valid with rulesets",
			cfg:     WafPolicyConfig{Mode: "block", Rulesets: []string{"owasp-crs"}},
			wantErr: false,
		},
		{
			name:    "valid paranoia level",
			cfg:     WafPolicyConfig{Mode: "block", ParanoiaLevel: intPtr(2)},
			wantErr: false,
		},
		{
			name:    "paranoia level too low",
			cfg:     WafPolicyConfig{Mode: "block", ParanoiaLevel: intPtr(0)},
			wantErr: true,
			errMsg:  "paranoiaLevel must be between 1 and 4",
		},
		{
			name:    "paranoia level too high",
			cfg:     WafPolicyConfig{Mode: "block", ParanoiaLevel: intPtr(5)},
			wantErr: true,
			errMsg:  "paranoiaLevel must be between 1 and 4",
		},
		{
			name:    "valid anomaly threshold",
			cfg:     WafPolicyConfig{Mode: "block", AnomalyThreshold: intPtr(5)},
			wantErr: false,
		},
		{
			name:    "anomaly threshold too low",
			cfg:     WafPolicyConfig{Mode: "block", AnomalyThreshold: intPtr(0)},
			wantErr: true,
			errMsg:  "anomalyThreshold must be at least 1",
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

func intPtr(i int) *int { return &i }

package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateLabels(t *testing.T) {
	tests := []struct {
		name    string
		labels  Labels
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil labels",
			labels:  nil,
			wantErr: false,
		},
		{
			name:    "empty labels",
			labels:  Labels{},
			wantErr: false,
		},
		{
			name:    "single valid label",
			labels:  Labels{"app": "web"},
			wantErr: false,
		},
		{
			name:    "valid label with empty value",
			labels:  Labels{"app": ""},
			wantErr: false,
		},
		{
			name:    "valid label with dots, dashes, underscores",
			labels:  Labels{"app.kubernetes.io-name": "my-app_v1"},
			wantErr: false,
		},
		{
			name:    "key with slash is invalid",
			labels:  Labels{"app.kubernetes.io/name": "value"},
			wantErr: true,
			errMsg:  "invalid label key",
		},
		{
			name:    "max labels (10)",
			labels:  Labels{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5", "f": "6", "g": "7", "h": "8", "i": "9", "j": "10"},
			wantErr: false,
		},
		{
			name:    "too many labels (11)",
			labels:  Labels{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5", "f": "6", "g": "7", "h": "8", "i": "9", "j": "10", "k": "11"},
			wantErr: true,
			errMsg:  "too many labels",
		},
		{
			name:    "empty key",
			labels:  Labels{"": "value"},
			wantErr: true,
			errMsg:  "label key cannot be empty",
		},
		{
			name:    "key starts with dash",
			labels:  Labels{"-invalid": "value"},
			wantErr: true,
			errMsg:  "invalid label key",
		},
		{
			name:    "key ends with dash",
			labels:  Labels{"invalid-": "value"},
			wantErr: true,
			errMsg:  "invalid label key",
		},
		{
			name:    "key with spaces",
			labels:  Labels{"invalid key": "value"},
			wantErr: true,
			errMsg:  "invalid label key",
		},
		{
			name:    "invalid value starting with dash",
			labels:  Labels{"app": "-invalid"},
			wantErr: true,
			errMsg:  "invalid label value",
		},
		{
			name:    "single char key",
			labels:  Labels{"a": "b"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLabels(tt.labels)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLabels_Scan(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    Labels
		wantErr bool
	}{
		{
			name:  "nil value",
			input: nil,
			want:  Labels{},
		},
		{
			name:  "byte slice",
			input: []byte(`{"app":"web"}`),
			want:  Labels{"app": "web"},
		},
		{
			name:  "string value",
			input: `{"env":"prod"}`,
			want:  Labels{"env": "prod"},
		},
		{
			name:    "unsupported type",
			input:   123,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l Labels
			err := l.Scan(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, l)
			}
		})
	}
}

func TestLabels_Value(t *testing.T) {
	t.Run("nil labels", func(t *testing.T) {
		var l Labels
		v, err := l.Value()
		assert.NoError(t, err)
		assert.Equal(t, "{}", v)
	})

	t.Run("non-nil labels", func(t *testing.T) {
		l := Labels{"app": "web"}
		v, err := l.Value()
		assert.NoError(t, err)
		assert.Contains(t, v.(string), `"app":"web"`)
	})
}

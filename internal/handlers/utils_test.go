package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseLabelsFilter_Empty(t *testing.T) {
	result := parseLabelsFilter("")
	assert.Nil(t, result)
}

func TestParseLabelsFilter_SingleLabel(t *testing.T) {
	result := parseLabelsFilter("env=prod")
	assert.Equal(t, map[string]string{"env": "prod"}, result)
}

func TestParseLabelsFilter_MultipleLabels(t *testing.T) {
	result := parseLabelsFilter("env=prod,team=backend")
	assert.Equal(t, map[string]string{"env": "prod", "team": "backend"}, result)
}

func TestParseLabelsFilter_WithSpaces(t *testing.T) {
	result := parseLabelsFilter("env = prod , team = backend")
	assert.Equal(t, map[string]string{"env": "prod", "team": "backend"}, result)
}

func TestParseLabelsFilter_InvalidFormat(t *testing.T) {
	// No equals sign - should return nil
	result := parseLabelsFilter("invalid")
	assert.Nil(t, result)
}

func TestParseLabelsFilter_EmptyKey(t *testing.T) {
	// Empty key should be skipped
	result := parseLabelsFilter("=value")
	assert.Nil(t, result)
}

func TestParseLabelsFilter_MixedValid(t *testing.T) {
	result := parseLabelsFilter("env=prod,invalid,team=backend")
	assert.Equal(t, map[string]string{"env": "prod", "team": "backend"}, result)
}

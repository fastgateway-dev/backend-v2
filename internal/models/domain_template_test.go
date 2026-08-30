package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnnotations_ScanValue(t *testing.T) {
	// Value nil
	var nilAnnotations Annotations
	val, err := nilAnnotations.Value()
	assert.NoError(t, err)
	assert.Equal(t, "{}", val)

	// Value non-nil
	annotations := Annotations{"key": "value"}
	val, err = annotations.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Scan bytes
	var scanned Annotations
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, "value", scanned["key"])

	// Scan string
	var scanned2 Annotations
	err = scanned2.Scan(`{"k2":"v2"}`)
	assert.NoError(t, err)
	assert.Equal(t, "v2", scanned2["k2"])

	// Scan nil
	var scanned3 Annotations
	err = scanned3.Scan(nil)
	assert.NoError(t, err)
	assert.NotNil(t, scanned3)

	// Scan empty JSON object
	var scanned4 Annotations
	err = scanned4.Scan([]byte(`{}`))
	assert.NoError(t, err)
	assert.NotNil(t, scanned4)

	// Scan empty bytes
	var scanned5 Annotations
	err = scanned5.Scan([]byte(``))
	assert.NoError(t, err)
	assert.NotNil(t, scanned5)

	// Scan wrong type
	var scanned6 Annotations
	err = scanned6.Scan(123)
	assert.Error(t, err)
}

func TestContainerResourcesConfig_ScanValue(t *testing.T) {
	cfg := ContainerResourcesConfig{
		Requests: &ResourceValues{CPU: "100m", Memory: "128Mi"},
		Limits:   &ResourceValues{CPU: "500m", Memory: "512Mi"},
	}

	val, err := cfg.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Scan bytes
	var scanned ContainerResourcesConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, "100m", scanned.Requests.CPU)

	// Scan string
	var scanned2 ContainerResourcesConfig
	err = scanned2.Scan(`{"requests":{"cpu":"200m"}}`)
	assert.NoError(t, err)
	assert.Equal(t, "200m", scanned2.Requests.CPU)

	// Scan nil
	var scanned3 ContainerResourcesConfig
	err = scanned3.Scan(nil)
	assert.NoError(t, err)

	// Scan wrong type
	err = scanned3.Scan(123)
	assert.Error(t, err)
}

func TestScalingConfig_ScanValue(t *testing.T) {
	replicas := int32(3)
	cfg := ScalingConfig{Type: "fixed", Replicas: &replicas}

	val, err := cfg.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Scan bytes
	var scanned ScalingConfig
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, "fixed", scanned.Type)
	assert.Equal(t, int32(3), *scanned.Replicas)

	// Scan string
	var scanned2 ScalingConfig
	err = scanned2.Scan(`{"type":"hpa","minReplicas":1,"maxReplicas":5}`)
	assert.NoError(t, err)
	assert.Equal(t, "hpa", scanned2.Type)

	// Scan nil
	var scanned3 ScalingConfig
	err = scanned3.Scan(nil)
	assert.NoError(t, err)

	// Scan wrong type
	err = scanned3.Scan(123)
	assert.Error(t, err)
}

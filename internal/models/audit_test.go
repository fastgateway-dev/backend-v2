package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAuditDetails_ScanValue(t *testing.T) {
	// Value nil
	var nilDetails AuditDetails
	val, err := nilDetails.Value()
	assert.NoError(t, err)
	assert.Nil(t, val)

	// Value non-nil
	details := AuditDetails{"action": "create", "count": float64(5)}
	val, err = details.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	// Scan bytes
	var scanned AuditDetails
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, "create", scanned["action"])

	// Scan nil
	var scanned2 AuditDetails
	err = scanned2.Scan(nil)
	assert.NoError(t, err)
	assert.Nil(t, scanned2)

	// Scan wrong type
	var scanned3 AuditDetails
	err = scanned3.Scan(123)
	assert.Error(t, err)
}

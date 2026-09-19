package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDNSCredentialData_ValueScanRoundTrip(t *testing.T) {
	in := DNSCredentialData{"apiToken": "cf-secret-123"}
	v, err := in.Value()
	require.NoError(t, err)

	var out DNSCredentialData
	require.NoError(t, out.Scan(v))
	assert.Equal(t, "cf-secret-123", out["apiToken"])
}

func TestDNSCredentialData_ScanNil(t *testing.T) {
	var out DNSCredentialData
	require.NoError(t, out.Scan(nil))
	assert.NotNil(t, out)
	assert.Len(t, out, 0)
}

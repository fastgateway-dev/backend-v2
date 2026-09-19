package models

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssuerConfig_ValueScanRoundTrip(t *testing.T) {
	id := uuid.New()
	in := IssuerConfig{Server: "https://acme.example/dir", Email: "a@b.com", DNSCredentialID: &id, ClusterIssuerName: "iss-acme-1"}
	v, err := in.Value()
	require.NoError(t, err)
	var out IssuerConfig
	require.NoError(t, out.Scan(v))
	assert.Equal(t, "iss-acme-1", out.ClusterIssuerName)
	require.NotNil(t, out.DNSCredentialID)
	assert.Equal(t, id, *out.DNSCredentialID)
}

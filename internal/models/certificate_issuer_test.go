package models

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssuerConfig_ValueScanRoundTrip(t *testing.T) {
	id := uuid.New()
	in := IssuerConfig{Server: "https://acme.example/dir", Email: "a@b.com", DNSCredentialID: &id, IssuerName: "iss-acme-1"}
	v, err := in.Value()
	require.NoError(t, err)
	var out IssuerConfig
	require.NoError(t, out.Scan(v))
	assert.Equal(t, "iss-acme-1", out.IssuerName)
	require.NotNil(t, out.DNSCredentialID)
	assert.Equal(t, id, *out.DNSCredentialID)
}

func TestIssuerConfig_IssuerName_RoundTrip(t *testing.T) {
	in := IssuerConfig{IssuerName: "iss-abc", CASecretName: "ca-abc"}
	v, err := in.Value()
	require.NoError(t, err)
	var out IssuerConfig
	require.NoError(t, out.Scan(v))
	assert.Equal(t, "iss-abc", out.IssuerName)
	assert.Equal(t, "ca-abc", out.CASecretName)
}

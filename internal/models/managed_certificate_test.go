package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedCertConfig_ValueScanRoundTrip(t *testing.T) {
	in := ManagedCertConfig{
		DNSNames:        []string{"a.example.com", "b.example.com"},
		SecretName:      "fgw-cert-x",
		CertificateName: "cert-x",
		KeyAlgorithm:    "RSA",
		KeySize:         2048,
		DurationDays:    90,
	}
	v, err := in.Value()
	require.NoError(t, err)
	var out ManagedCertConfig
	require.NoError(t, out.Scan(v))
	assert.Equal(t, []string{"a.example.com", "b.example.com"}, out.DNSNames)
	assert.Equal(t, "fgw-cert-x", out.SecretName)
}

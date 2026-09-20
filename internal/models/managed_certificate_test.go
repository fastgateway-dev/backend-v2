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

// TestManagedCertConfig_ValueScanRoundTrip_CSRMode guards against a field
// silently never reaching the database: ManagedCertConfig is persisted as a
// jsonb column via Value() (json.Marshal) / Scan() (json.Unmarshal) below --
// NOT via any API response path -- so a field tagged json:"-" (as CSRPEM
// once was) is dropped here too, and OnApproved's later repo.GetByID would
// read it back empty. This test exercises the real Value()->Scan() round
// trip (no mocks) for every csr-mode field: KeyMode, URISANs, and CSRPEM.
func TestManagedCertConfig_ValueScanRoundTrip_CSRMode(t *testing.T) {
	in := ManagedCertConfig{
		DNSNames: []string{"a.example.com"},
		KeyMode:  ManagedCertKeyModeCSR,
		URISANs:  []string{"spiffe://x/y"},
		CSRPEM:   "-----BEGIN CERTIFICATE REQUEST-----\nsome-test-pem-bytes\n-----END CERTIFICATE REQUEST-----\n",
	}
	v, err := in.Value()
	require.NoError(t, err)
	var out ManagedCertConfig
	require.NoError(t, out.Scan(v))
	assert.Equal(t, ManagedCertKeyModeCSR, out.KeyMode)
	assert.Equal(t, []string{"spiffe://x/y"}, out.URISANs)
	assert.Equal(t, []string{"a.example.com"}, out.DNSNames)
	assert.Equal(t, in.CSRPEM, out.CSRPEM)
}

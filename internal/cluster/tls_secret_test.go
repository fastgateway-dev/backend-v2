package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCertFingerprint_Deterministic(t *testing.T) {
	crt := []byte("---BEGIN CERTIFICATE-----fake-cert-bytes-----END CERTIFICATE---")

	fp1 := CertFingerprint(crt)
	fp2 := CertFingerprint(crt)

	assert.Equal(t, fp1, fp2, "same input must produce the same fingerprint")
	assert.NotEmpty(t, fp1)
}

func TestCertFingerprint_DifferentInputsDiffer(t *testing.T) {
	fp1 := CertFingerprint([]byte("cert-a"))
	fp2 := CertFingerprint([]byte("cert-b"))

	assert.NotEqual(t, fp1, fp2)
}

func TestCertFingerprint_IsHexSHA256(t *testing.T) {
	fp := CertFingerprint([]byte("hello"))

	// sha256 hex digest is 64 chars
	assert.Len(t, fp, 64)
	// known sha256("hello") hex digest
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", fp)
}

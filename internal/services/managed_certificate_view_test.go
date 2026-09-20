package services

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func TestBuildEnrichedCertificates(t *testing.T) {
	// Setup test data
	projectID := uuid.New()

	// Issuer I1 (in the issuers list)
	issuerI1ID := uuid.New()
	issuerI1 := models.CertificateIssuer{
		ID:   issuerI1ID,
		Name: "Issuer I1",
		Type: models.IssuerTypeSelfSignedCA,
	}

	// Issuer I2 (NOT in the issuers list) - for CertC
	issuerI2ID := uuid.New()

	// CertA: has a dist row + 2 referencing domains
	certAID := uuid.New()
	certA := models.ManagedCertificate{
		ID:        certAID,
		ProjectID: projectID,
		Name:      "cert-a",
		IssuerID:  issuerI1ID,
		Usage:     models.ManagedCertUsageServer,
	}

	// CertB: has a dist row, no domains
	certBID := uuid.New()
	certB := models.ManagedCertificate{
		ID:        certBID,
		ProjectID: projectID,
		Name:      "cert-b",
		IssuerID:  issuerI1ID,
		Usage:     models.ManagedCertUsageServer,
	}

	// CertC: neither dist nor domains, references issuer NOT in the list
	certCID := uuid.New()
	certC := models.ManagedCertificate{
		ID:        certCID,
		ProjectID: projectID,
		Name:      "cert-c",
		IssuerID:  issuerI2ID, // Issuer NOT in the list
		Usage:     models.ManagedCertUsageServer,
	}

	certs := []models.ManagedCertificate{certA, certB, certC}

	// Distributions
	distA := models.CertificateDistribution{
		ID:                   uuid.New(),
		ManagedCertificateID: certAID,
		ProjectID:            projectID,
		Status:               models.CertDistStatusSynced,
	}
	distB := models.CertificateDistribution{
		ID:                   uuid.New(),
		ManagedCertificateID: certBID,
		ProjectID:            projectID,
		Status:               models.CertDistStatusPending,
	}
	// CertC has NO distribution
	dists := []models.CertificateDistribution{distA, distB}

	// Domains
	domainA1ID := uuid.New()
	domainA1 := models.Domain{
		ID:                   domainA1ID,
		ProjectID:            projectID,
		Name:                 "domain-a1",
		Hostname:             "a1.example.com",
		ManagedCertificateID: &certAID, // References CertA
	}

	domainA2ID := uuid.New()
	domainA2 := models.Domain{
		ID:                   domainA2ID,
		ProjectID:            projectID,
		Name:                 "domain-a2",
		Hostname:             "a2.example.com",
		ManagedCertificateID: &certAID, // References CertA
	}

	// Orphaned domain (no cert reference)
	domainOrphanID := uuid.New()
	domainOrphan := models.Domain{
		ID:                   domainOrphanID,
		ProjectID:            projectID,
		Name:                 "domain-orphan",
		Hostname:             "orphan.example.com",
		ManagedCertificateID: nil, // No cert reference
	}

	// CertB has no referencing domains
	// CertC has no referencing domains

	domains := []models.Domain{domainA1, domainA2, domainOrphan}

	// Issuers
	issuers := []models.CertificateIssuer{issuerI1}
	// issuerI2 is NOT in the list

	// Execute
	enriched := buildEnrichedCertificates(certs, dists, domains, issuers)

	// Assertions

	require.Len(t, enriched, 3, "Should have 3 enriched certificates")

	// Check order is preserved (A, B, C)
	assert.Equal(t, certAID, enriched[0].Certificate.ID, "First cert should be CertA")
	assert.Equal(t, certBID, enriched[1].Certificate.ID, "Second cert should be CertB")
	assert.Equal(t, certCID, enriched[2].Certificate.ID, "Third cert should be CertC")

	// ===== CertA assertions =====
	enrichedA := enriched[0]
	assert.NotNil(t, enrichedA.Distribution, "CertA should have a distribution")
	assert.Equal(t, certAID, enrichedA.Distribution.ManagedCertificateID, "CertA's distribution should reference CertA")
	assert.Len(t, enrichedA.Domains, 2, "CertA should have 2 referencing domains")
	assert.Equal(t, domainA1ID, enrichedA.Domains[0].ID, "CertA's first domain should be domainA1")
	assert.Equal(t, domainA2ID, enrichedA.Domains[1].ID, "CertA's second domain should be domainA2")
	assert.Equal(t, "Issuer I1", enrichedA.IssuerName, "CertA's issuer name should be resolved")
	assert.Equal(t, "self_signed_ca", enrichedA.IssuerType, "CertA's issuer type should be resolved")

	// ===== CertB assertions =====
	enrichedB := enriched[1]
	assert.NotNil(t, enrichedB.Distribution, "CertB should have a distribution")
	assert.Equal(t, certBID, enrichedB.Distribution.ManagedCertificateID, "CertB's distribution should reference CertB")
	assert.Empty(t, enrichedB.Domains, "CertB should have no referencing domains")
	assert.Equal(t, "Issuer I1", enrichedB.IssuerName, "CertB's issuer name should be resolved")
	assert.Equal(t, "self_signed_ca", enrichedB.IssuerType, "CertB's issuer type should be resolved")

	// ===== CertC assertions =====
	enrichedC := enriched[2]
	assert.Nil(t, enrichedC.Distribution, "CertC should NOT have a distribution")
	assert.Empty(t, enrichedC.Domains, "CertC should have no referencing domains")
	assert.Equal(t, "", enrichedC.IssuerName, "CertC's issuer should be missing (defensive), empty name")
	assert.Equal(t, "", enrichedC.IssuerType, "CertC's issuer should be missing (defensive), empty type")

	// ===== No loop-aliasing bug check =====
	// Verify A and B's Distribution point to DIFFERENT rows (not both the last element)
	assert.NotEqual(t,
		enrichedA.Distribution.ID,
		enrichedB.Distribution.ID,
		"CertA and CertB should have different distribution IDs")
	assert.Equal(t, distA.ID, enrichedA.Distribution.ID, "CertA's distribution ID should match distA")
	assert.Equal(t, distB.ID, enrichedB.Distribution.ID, "CertB's distribution ID should match distB")
}

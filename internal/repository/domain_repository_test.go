package repository_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies ListByManagedCertificateID returns exactly the domains whose
// managed_certificate_id matches the given certificate, and an empty slice
// (not an error) when no domain references the certificate.
func TestDomainRepository_ListByManagedCertificateID(t *testing.T) {
	db := requirePostgres(t)
	domainRepo := repository.NewDomainRepository(db)
	mcRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, userID := seedProject(t, db)

	issuerID := seedIssuer(t, db, userID)
	certID := seedManagedCertificate(t, mcRepo, projectID, issuerID, userID, "cert-for-domain", models.ManagedCertStatusReady)

	withCert := &models.Domain{
		ProjectID:            projectID,
		Name:                 "domain-with-cert-" + certID.String(),
		Hostname:             "with-cert-" + certID.String() + ".test.example.com",
		CreatedBy:            userID,
		ManagedCertificateID: &certID,
	}
	require.NoError(t, domainRepo.Create(withCert))

	withoutCert := &models.Domain{
		ProjectID: projectID,
		Name:      "domain-without-cert-" + certID.String(),
		Hostname:  "without-cert-" + certID.String() + ".test.example.com",
		CreatedBy: userID,
	}
	require.NoError(t, domainRepo.Create(withoutCert))

	// Domains reference the managed certificate with ON DELETE RESTRICT, so
	// they must be deleted before the certificate row. t.Cleanup runs LIFO
	// (last registered, first run), so the certificate-delete cleanup is
	// registered first and the domain-delete cleanup is registered after it
	// — that makes the domain rows get deleted first, then the certificate.
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id = ?`, certID).Error
	})
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domains WHERE id IN (?, ?)`, withCert.ID, withoutCert.ID).Error
	})

	got, err := domainRepo.ListByManagedCertificateID(certID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, withCert.ID, got[0].ID)

	empty, err := domainRepo.ListByManagedCertificateID(uuid.New())
	require.NoError(t, err)
	assert.Empty(t, empty)
}

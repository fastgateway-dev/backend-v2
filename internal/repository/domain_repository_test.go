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

// Verifies ListByManagedCertificateIDs returns exactly the domains whose
// managed_certificate_id is in the given set (batch sibling of
// ListByManagedCertificateID, used to avoid N+1 when enriching a page of
// certificates), and that an empty input returns an empty, nil-error result.
func TestDomainRepository_ListByManagedCertificateIDs(t *testing.T) {
	db := requirePostgres(t)
	domainRepo := repository.NewDomainRepository(db)
	mcRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, userID := seedProject(t, db)

	issuerID := seedIssuer(t, db, userID)
	certA := seedManagedCertificate(t, mcRepo, projectID, issuerID, userID, "cert-a-batch", models.ManagedCertStatusReady)
	certB := seedManagedCertificate(t, mcRepo, projectID, issuerID, userID, "cert-b-batch", models.ManagedCertStatusReady)

	domainA := &models.Domain{
		ProjectID:            projectID,
		Name:                 "domain-batch-a-" + certA.String(),
		Hostname:             "batch-a-" + certA.String() + ".test.example.com",
		CreatedBy:            userID,
		ManagedCertificateID: &certA,
	}
	require.NoError(t, domainRepo.Create(domainA))

	// Domains reference the managed certificate with ON DELETE RESTRICT, so
	// they must be deleted before the certificate rows. t.Cleanup runs LIFO,
	// so registering the certificate-delete cleanup first and the
	// domain-delete cleanup after it makes the domain row get deleted first.
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id IN (?, ?)`, certA, certB).Error
	})
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domains WHERE id = ?`, domainA.ID).Error
	})

	got, err := domainRepo.ListByManagedCertificateIDs([]uuid.UUID{certA, certB})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, domainA.ID, got[0].ID)

	empty, err := domainRepo.ListByManagedCertificateIDs(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

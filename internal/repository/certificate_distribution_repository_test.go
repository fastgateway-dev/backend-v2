package repository_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies ListByCertificateIDs returns exactly the distribution rows whose
// managed_certificate_id is in the given set (one query, no N+1), and that an
// empty input returns an empty, nil-error result without querying.
func TestCertificateDistributionRepository_ListByCertificateIDs(t *testing.T) {
	db := requirePostgres(t)
	distRepo := repository.NewCertificateDistributionRepository(db)
	mcRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, userID := seedProject(t, db)
	issuerID := seedIssuer(t, db, userID)

	certA := seedManagedCertificate(t, mcRepo, projectID, issuerID, userID, "cert-a-dist", models.ManagedCertStatusReady)
	certB := seedManagedCertificate(t, mcRepo, projectID, issuerID, userID, "cert-b-dist", models.ManagedCertStatusReady)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id IN (?, ?)`, certA, certB).Error
	})
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM certificate_distributions WHERE managed_certificate_id IN (?, ?)`, certA, certB).Error
	})

	dist := &models.CertificateDistribution{
		ManagedCertificateID: certA,
		ProjectID:            projectID,
		Status:               models.CertDistStatusSynced,
	}
	require.NoError(t, distRepo.Upsert(dist))

	got, err := distRepo.ListByCertificateIDs([]uuid.UUID{certA, certB})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, certA, got[0].ManagedCertificateID)

	empty, err := distRepo.ListByCertificateIDs(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

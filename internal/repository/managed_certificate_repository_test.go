package repository_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedIssuer inserts a minimal certificate_issuers row (global, not project
// scoped) and registers cleanup. Returns the issuer ID.
func seedIssuer(t *testing.T, db *gorm.DB, createdByUserID uuid.UUID) uuid.UUID {
	t.Helper()
	issuerID := uuid.New()
	require.NoError(t, db.Exec(`
		INSERT INTO certificate_issuers (id, name, type, status, created_by, created_at, updated_at)
		VALUES (?, ?, 'ca', 'ready', ?, NOW(), NOW())`,
		issuerID, "test-issuer-"+issuerID.String(), createdByUserID).Error)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM certificate_issuers WHERE id = ?`, issuerID).Error
	})

	return issuerID
}

// seedManagedCertificate inserts a ManagedCertificate row via the repository
// under test and registers cleanup. Returns the certificate ID.
func seedManagedCertificate(t *testing.T, repo *repository.ManagedCertificateRepository, projectID, issuerID, createdByUserID uuid.UUID, name string, status models.ManagedCertStatus) uuid.UUID {
	t.Helper()
	cert := &models.ManagedCertificate{
		ProjectID: projectID,
		Name:      name,
		IssuerID:  issuerID,
		Usage:     models.ManagedCertUsageServer,
		Status:    status,
		CreatedBy: createdByUserID,
	}
	require.NoError(t, repo.Create(cert))
	return cert.ID
}

// Verifies ListByStatuses returns matching certificates across projects
// (it is not scoped to a single project) and excludes non-matching statuses.
func TestManagedCertificateRepository_ListByStatuses_CrossProjectFiltersByStatus(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewManagedCertificateRepository(db)

	projectA, _, _, userA := seedProject(t, db)
	projectB, _, _, userB := seedProject(t, db)

	issuerA := seedIssuer(t, db, userA)
	issuerB := seedIssuer(t, db, userB)

	pendingA := seedManagedCertificate(t, repo, projectA, issuerA, userA, "pending-a", models.ManagedCertStatusPending)
	issuingB := seedManagedCertificate(t, repo, projectB, issuerB, userB, "issuing-b", models.ManagedCertStatusIssuing)
	seedManagedCertificate(t, repo, projectA, issuerA, userA, "ready-a", models.ManagedCertStatusReady)
	seedManagedCertificate(t, repo, projectB, issuerB, userB, "error-b", models.ManagedCertStatusError)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM managed_certificates WHERE project_id IN (?, ?)`, projectA, projectB).Error
	})

	got, err := repo.ListByStatuses([]models.ManagedCertStatus{
		models.ManagedCertStatusPending,
		models.ManagedCertStatusIssuing,
	})
	require.NoError(t, err)

	gotIDs := make(map[uuid.UUID]bool, len(got))
	for _, c := range got {
		gotIDs[c.ID] = true
	}

	assert.True(t, gotIDs[pendingA], "expected pending cert from project A")
	assert.True(t, gotIDs[issuingB], "expected issuing cert from project B (cross-project)")

	for _, c := range got {
		assert.Containsf(t, []models.ManagedCertStatus{models.ManagedCertStatusPending, models.ManagedCertStatusIssuing}, c.Status,
			"unexpected status %q returned for cert %s", c.Status, c.ID)
	}
}

func TestManagedCertificateRepository_ListByStatuses_EmptyStatusesReturnsEmpty(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewManagedCertificateRepository(db)

	projectA, _, _, userA := seedProject(t, db)
	issuerA := seedIssuer(t, db, userA)
	seedManagedCertificate(t, repo, projectA, issuerA, userA, "pending-a", models.ManagedCertStatusPending)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM managed_certificates WHERE project_id = ?`, projectA).Error
	})

	got, err := repo.ListByStatuses([]models.ManagedCertStatus{})
	require.NoError(t, err)
	assert.Empty(t, got)
}

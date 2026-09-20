package repository_test

import (
	"testing"
	"time"

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

// setCertUsageAndNotAfter directly updates the usage and not_after columns
// for a certificate. seedManagedCertificate always creates "server" usage
// certs with no expiry, so tests that need other values patch them in with
// a raw UPDATE rather than extending the shared seed helper's signature.
func setCertUsageAndNotAfter(t *testing.T, db *gorm.DB, certID uuid.UUID, usage models.ManagedCertUsage, notAfter *time.Time) {
	t.Helper()
	require.NoError(t, db.Exec(`UPDATE managed_certificates SET usage = ?, not_after = ? WHERE id = ?`,
		usage, notAfter, certID).Error)
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

// Verifies ListByProjectFiltered narrows by status, issuer, usage and
// ExpiresBefore independently, honors pagination, and never returns a
// certificate belonging to a different project regardless of filters.
func TestManagedCertificateRepository_ListByProjectFiltered(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewManagedCertificateRepository(db)

	projectA, _, _, userA := seedProject(t, db)
	projectB, _, _, userB := seedProject(t, db)

	issuerA1 := seedIssuer(t, db, userA)
	issuerA2 := seedIssuer(t, db, userA)
	issuerB := seedIssuer(t, db, userB)

	now := time.Now().UTC()
	soon := now.Add(5 * 24 * time.Hour)
	far := now.Add(60 * 24 * time.Hour)

	cert1 := seedManagedCertificate(t, repo, projectA, issuerA1, userA, "cert1-ready-server", models.ManagedCertStatusReady)
	setCertUsageAndNotAfter(t, db, cert1, models.ManagedCertUsageServer, &far)

	cert2 := seedManagedCertificate(t, repo, projectA, issuerA2, userA, "cert2-pending-client", models.ManagedCertStatusPending)
	setCertUsageAndNotAfter(t, db, cert2, models.ManagedCertUsageClient, &soon)

	cert3 := seedManagedCertificate(t, repo, projectA, issuerA1, userA, "cert3-ready-server", models.ManagedCertStatusReady)
	setCertUsageAndNotAfter(t, db, cert3, models.ManagedCertUsageServer, &far)

	// Cert in a different project: must never be returned for projectA,
	// no matter which filter is applied.
	otherProjectCert := seedManagedCertificate(t, repo, projectB, issuerB, userB, "other-project-ready", models.ManagedCertStatusReady)
	setCertUsageAndNotAfter(t, db, otherProjectCert, models.ManagedCertUsageServer, &far)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM managed_certificates WHERE project_id IN (?, ?)`, projectA, projectB).Error
	})

	// No filter: all three certs in projectA, none from projectB.
	all, total, err := repo.ListByProjectFiltered(projectA, 1, 10, repository.CertificateListFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Len(t, all, 3)
	for _, c := range all {
		assert.NotEqual(t, otherProjectCert, c.ID, "must not leak certs from other projects")
	}

	// Status filter.
	readyOnly, total, err := repo.ListByProjectFiltered(projectA, 1, 10, repository.CertificateListFilter{Status: string(models.ManagedCertStatusReady)})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	readyIDs := certIDSet(readyOnly)
	assert.True(t, readyIDs[cert1])
	assert.True(t, readyIDs[cert3])
	assert.False(t, readyIDs[cert2])

	// Usage filter.
	clientOnly, total, err := repo.ListByProjectFiltered(projectA, 1, 10, repository.CertificateListFilter{Usage: string(models.ManagedCertUsageClient)})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, clientOnly, 1)
	assert.Equal(t, cert2, clientOnly[0].ID)

	// IssuerID filter.
	issuerA2Only, total, err := repo.ListByProjectFiltered(projectA, 1, 10, repository.CertificateListFilter{IssuerID: &issuerA2})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, issuerA2Only, 1)
	assert.Equal(t, cert2, issuerA2Only[0].ID)

	// ExpiresBefore filter: only cert2 (soon) expires before now+10d.
	cutoff := now.Add(10 * 24 * time.Hour)
	expiringSoon, total, err := repo.ListByProjectFiltered(projectA, 1, 10, repository.CertificateListFilter{ExpiresBefore: &cutoff})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, expiringSoon, 1)
	assert.Equal(t, cert2, expiringSoon[0].ID)

	// Pagination: page 1 of 2 returns 2 rows, page 2 returns the remaining 1,
	// total stays 3 throughout.
	page1, total, err := repo.ListByProjectFiltered(projectA, 1, 2, repository.CertificateListFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Len(t, page1, 2)

	page2, total, err := repo.ListByProjectFiltered(projectA, 2, 2, repository.CertificateListFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Len(t, page2, 1)
}

// Verifies ListFleet returns certificates across multiple projects and that
// the ProjectID/status/usage/issuer/ExpiresBefore filters narrow correctly.
func TestManagedCertificateRepository_ListFleet(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewManagedCertificateRepository(db)

	projectA, _, _, userA := seedProject(t, db)
	projectB, _, _, userB := seedProject(t, db)

	issuerA := seedIssuer(t, db, userA)
	issuerB := seedIssuer(t, db, userB)

	now := time.Now().UTC()
	soon := now.Add(5 * 24 * time.Hour)
	far := now.Add(60 * 24 * time.Hour)

	certA := seedManagedCertificate(t, repo, projectA, issuerA, userA, "fleet-a-ready-server", models.ManagedCertStatusReady)
	setCertUsageAndNotAfter(t, db, certA, models.ManagedCertUsageServer, &far)

	certB := seedManagedCertificate(t, repo, projectB, issuerB, userB, "fleet-b-pending-client", models.ManagedCertStatusPending)
	setCertUsageAndNotAfter(t, db, certB, models.ManagedCertUsageClient, &soon)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM managed_certificates WHERE project_id IN (?, ?)`, projectA, projectB).Error
	})

	// No filter: certs from both projects come back.
	all, total, err := repo.ListFleet(1, 10, repository.CertificateListFilter{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(2))
	allIDs := certIDSet(all)
	assert.True(t, allIDs[certA], "expected cert from project A in fleet listing")
	assert.True(t, allIDs[certB], "expected cert from project B in fleet listing (cross-project)")

	// ProjectID filter narrows to a single project.
	onlyA, total, err := repo.ListFleet(1, 10, repository.CertificateListFilter{ProjectID: &projectA})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, onlyA, 1)
	assert.Equal(t, certA, onlyA[0].ID)

	// Status filter across projects.
	pendingOnly, total, err := repo.ListFleet(1, 10, repository.CertificateListFilter{Status: string(models.ManagedCertStatusPending)})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, pendingOnly, 1)
	assert.Equal(t, certB, pendingOnly[0].ID)

	// Usage filter across projects.
	clientOnly, total, err := repo.ListFleet(1, 10, repository.CertificateListFilter{Usage: string(models.ManagedCertUsageClient)})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, clientOnly, 1)
	assert.Equal(t, certB, clientOnly[0].ID)

	// IssuerID filter across projects.
	issuerAOnly, total, err := repo.ListFleet(1, 10, repository.CertificateListFilter{IssuerID: &issuerA})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, issuerAOnly, 1)
	assert.Equal(t, certA, issuerAOnly[0].ID)

	// ExpiresBefore filter across projects: only certB (soon) qualifies.
	cutoff := now.Add(10 * 24 * time.Hour)
	expiringSoon, total, err := repo.ListFleet(1, 10, repository.CertificateListFilter{ExpiresBefore: &cutoff})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, expiringSoon, 1)
	assert.Equal(t, certB, expiringSoon[0].ID)
}

// certIDSet builds a lookup set of certificate IDs for membership assertions.
func certIDSet(certs []models.ManagedCertificate) map[uuid.UUID]bool {
	set := make(map[uuid.UUID]bool, len(certs))
	for _, c := range certs {
		set[c.ID] = true
	}
	return set
}

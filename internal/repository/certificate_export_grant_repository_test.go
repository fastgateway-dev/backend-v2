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

// seedExportGrant inserts a certificate_export_grants row via the repository
// under test and registers cleanup. Returns the grant ID.
func seedExportGrant(t *testing.T, repo *repository.CertificateExportGrantRepository, certID, userID uuid.UUID, expiresAt time.Time) uuid.UUID {
	t.Helper()
	grant := &models.CertificateExportGrant{
		ManagedCertificateID: certID,
		GrantedTo:            userID,
		ExpiresAt:            expiresAt,
	}
	require.NoError(t, repo.Create(grant))
	return grant.ID
}

// Verifies Create persists a grant and ConsumeForCert returns it exactly
// once, marking it consumed on the row -- a second consume for the same
// (cert, user) pair must fail with ErrExportGrantUnavailable since the
// grant is single-use.
func TestCertificateExportGrantRepository_ConsumeForCert_SingleUse(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)
	certRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, userID := seedProject(t, db)
	issuerID := seedIssuer(t, db, userID)
	certID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "export-cert", models.ManagedCertStatusReady)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM certificate_export_grants WHERE managed_certificate_id = ?`, certID).Error
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id = ?`, certID).Error
	})

	grantID := seedExportGrant(t, repo, certID, userID, time.Now().Add(15*time.Minute))

	got, err := repo.ConsumeForCert(certID, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, grantID, got.ID)
	assert.Equal(t, certID, got.ManagedCertificateID)
	assert.Equal(t, userID, got.GrantedTo)
	require.NotNil(t, got.ConsumedAt)

	// Second consume of the same grant must fail: it is already consumed.
	_, err = repo.ConsumeForCert(certID, userID)
	assert.ErrorIs(t, err, repository.ErrExportGrantUnavailable)
}

// An expired grant (ExpiresAt in the past) must never be consumable, even
// though it was never used.
func TestCertificateExportGrantRepository_ConsumeForCert_ExpiredGrantUnavailable(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)
	certRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, userID := seedProject(t, db)
	issuerID := seedIssuer(t, db, userID)
	certID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "expired-export-cert", models.ManagedCertStatusReady)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM certificate_export_grants WHERE managed_certificate_id = ?`, certID).Error
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id = ?`, certID).Error
	})

	seedExportGrant(t, repo, certID, userID, time.Now().Add(-1*time.Minute))

	_, err := repo.ConsumeForCert(certID, userID)
	assert.ErrorIs(t, err, repository.ErrExportGrantUnavailable)
}

// A grant issued to a different user must never be consumable by the
// requester -- ConsumeForCert is bound to the caller, mirroring the
// controller ruling that grants are user-bound rather than a bearer token.
func TestCertificateExportGrantRepository_ConsumeForCert_DifferentUserUnavailable(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)
	certRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, ownerUserID := seedProject(t, db)
	_, _, _, otherUserID := seedProject(t, db)
	issuerID := seedIssuer(t, db, ownerUserID)
	certID := seedManagedCertificate(t, certRepo, projectID, issuerID, ownerUserID, "other-user-export-cert", models.ManagedCertStatusReady)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM certificate_export_grants WHERE managed_certificate_id = ?`, certID).Error
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id = ?`, certID).Error
	})

	seedExportGrant(t, repo, certID, ownerUserID, time.Now().Add(15*time.Minute))

	_, err := repo.ConsumeForCert(certID, otherUserID)
	assert.ErrorIs(t, err, repository.ErrExportGrantUnavailable)

	// The grant is still intact for its actual owner.
	got, err := repo.ConsumeForCert(certID, ownerUserID)
	require.NoError(t, err)
	require.NotNil(t, got)
}

// An unknown certificate/user pair (no grant row at all) must also report
// ErrExportGrantUnavailable rather than gorm.ErrRecordNotFound leaking out.
func TestCertificateExportGrantRepository_ConsumeForCert_NoGrantUnavailable(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)

	_, err := repo.ConsumeForCert(uuid.New(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, repository.ErrExportGrantUnavailable)
	assert.NotErrorIs(t, err, gorm.ErrRecordNotFound)
}

// HasUsableGrant must report true for a usable (unconsumed, unexpired) grant
// -- and, unlike ConsumeForCert, must leave it unconsumed so a later
// ConsumeForCert can still spend it.
func TestCertificateExportGrantRepository_HasUsableGrant_UsableGrant(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)
	certRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, userID := seedProject(t, db)
	issuerID := seedIssuer(t, db, userID)
	certID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "has-usable-grant-cert", models.ManagedCertStatusReady)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM certificate_export_grants WHERE managed_certificate_id = ?`, certID).Error
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id = ?`, certID).Error
	})

	seedExportGrant(t, repo, certID, userID, time.Now().Add(15*time.Minute))

	ok, err := repo.HasUsableGrant(certID, userID)
	require.NoError(t, err)
	assert.True(t, ok)

	// Read-only: the grant must still be consumable afterwards.
	got, err := repo.ConsumeForCert(certID, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
}

// HasUsableGrant must report false once the grant has been consumed.
func TestCertificateExportGrantRepository_HasUsableGrant_ConsumedGrantFalse(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)
	certRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, userID := seedProject(t, db)
	issuerID := seedIssuer(t, db, userID)
	certID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "consumed-grant-cert", models.ManagedCertStatusReady)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM certificate_export_grants WHERE managed_certificate_id = ?`, certID).Error
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id = ?`, certID).Error
	})

	seedExportGrant(t, repo, certID, userID, time.Now().Add(15*time.Minute))
	_, err := repo.ConsumeForCert(certID, userID)
	require.NoError(t, err)

	ok, err := repo.HasUsableGrant(certID, userID)
	require.NoError(t, err)
	assert.False(t, ok)
}

// HasUsableGrant must report false for an expired grant, even though it was
// never consumed.
func TestCertificateExportGrantRepository_HasUsableGrant_ExpiredGrantFalse(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)
	certRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, userID := seedProject(t, db)
	issuerID := seedIssuer(t, db, userID)
	certID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "expired-has-usable-cert", models.ManagedCertStatusReady)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM certificate_export_grants WHERE managed_certificate_id = ?`, certID).Error
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id = ?`, certID).Error
	})

	seedExportGrant(t, repo, certID, userID, time.Now().Add(-1*time.Minute))

	ok, err := repo.HasUsableGrant(certID, userID)
	require.NoError(t, err)
	assert.False(t, ok)
}

// HasUsableGrant must report false for another user's grant -- it is
// per-user, mirroring ConsumeForCert's user-bound semantics.
func TestCertificateExportGrantRepository_HasUsableGrant_OtherUserFalse(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)
	certRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, ownerUserID := seedProject(t, db)
	_, _, _, otherUserID := seedProject(t, db)
	issuerID := seedIssuer(t, db, ownerUserID)
	certID := seedManagedCertificate(t, certRepo, projectID, issuerID, ownerUserID, "has-usable-other-user-cert", models.ManagedCertStatusReady)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM certificate_export_grants WHERE managed_certificate_id = ?`, certID).Error
		_ = db.Exec(`DELETE FROM managed_certificates WHERE id = ?`, certID).Error
	})

	seedExportGrant(t, repo, certID, ownerUserID, time.Now().Add(15*time.Minute))

	ok, err := repo.HasUsableGrant(certID, otherUserID)
	require.NoError(t, err)
	assert.False(t, ok)
}

// ListUsableGrantCertIDs must return, in ONE query, only the cert IDs from
// certIDs for which userID has a usable grant -- excluding certs with no
// grant, a consumed grant, an expired grant, or a grant belonging to a
// different user.
func TestCertificateExportGrantRepository_ListUsableGrantCertIDs_BatchFiltersCorrectly(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)
	certRepo := repository.NewManagedCertificateRepository(db)

	projectID, _, _, userID := seedProject(t, db)
	_, _, _, otherUserID := seedProject(t, db)
	issuerID := seedIssuer(t, db, userID)

	usableCertID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "batch-usable-cert", models.ManagedCertStatusReady)
	consumedCertID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "batch-consumed-cert", models.ManagedCertStatusReady)
	expiredCertID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "batch-expired-cert", models.ManagedCertStatusReady)
	otherUserCertID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "batch-other-user-cert", models.ManagedCertStatusReady)
	noGrantCertID := seedManagedCertificate(t, certRepo, projectID, issuerID, userID, "batch-no-grant-cert", models.ManagedCertStatusReady)

	allCertIDs := []uuid.UUID{usableCertID, consumedCertID, expiredCertID, otherUserCertID, noGrantCertID}

	t.Cleanup(func() {
		for _, id := range allCertIDs {
			_ = db.Exec(`DELETE FROM certificate_export_grants WHERE managed_certificate_id = ?`, id).Error
			_ = db.Exec(`DELETE FROM managed_certificates WHERE id = ?`, id).Error
		}
	})

	seedExportGrant(t, repo, usableCertID, userID, time.Now().Add(15*time.Minute))
	seedExportGrant(t, repo, consumedCertID, userID, time.Now().Add(15*time.Minute))
	_, err := repo.ConsumeForCert(consumedCertID, userID)
	require.NoError(t, err)
	seedExportGrant(t, repo, expiredCertID, userID, time.Now().Add(-1*time.Minute))
	seedExportGrant(t, repo, otherUserCertID, otherUserID, time.Now().Add(15*time.Minute))
	// noGrantCertID has no grant row at all.

	got, err := repo.ListUsableGrantCertIDs(userID, allCertIDs)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{usableCertID}, got)
}

// Empty input must return an empty result without querying the database.
func TestCertificateExportGrantRepository_ListUsableGrantCertIDs_EmptyInput(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewCertificateExportGrantRepository(db)

	got, err := repo.ListUsableGrantCertIDs(uuid.New(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = repo.ListUsableGrantCertIDs(uuid.New(), []uuid.UUID{})
	require.NoError(t, err)
	assert.Empty(t, got)
}

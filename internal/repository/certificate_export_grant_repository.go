package repository

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// ErrExportGrantUnavailable reports that the caller has no usable
// certificate export grant -- none exists, it was already consumed, it has
// expired, or it belongs to a different user. ConsumeForCert collapses all
// of these into one error so the handler can map it to a single 403/404
// without leaking which case applied.
var ErrExportGrantUnavailable = errors.New("no usable certificate export grant")

type CertificateExportGrantRepository struct{ db *gorm.DB }

func NewCertificateExportGrantRepository(db *gorm.DB) *CertificateExportGrantRepository {
	return &CertificateExportGrantRepository{db: db}
}

func (r *CertificateExportGrantRepository) Create(g *models.CertificateExportGrant) error {
	return r.db.Create(g).Error
}

// ConsumeForCert atomically finds the caller's own unconsumed, unexpired
// export grant for certID and marks it consumed, returning it. Single-use is
// the whole point, so the find-and-mark happens inside one transaction with
// a row lock (SELECT ... FOR UPDATE): two concurrent requests racing on the
// same grant must not both succeed. Returns ErrExportGrantUnavailable when no
// such grant exists, was already consumed, has expired, or belongs to a
// different user.
func (r *CertificateExportGrantRepository) ConsumeForCert(certID, userID uuid.UUID) (*models.CertificateExportGrant, error) {
	var grant models.CertificateExportGrant
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("managed_certificate_id = ? AND granted_to = ? AND consumed_at IS NULL AND expires_at > ?", certID, userID, time.Now()).
			Order("created_at DESC").
			First(&grant).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrExportGrantUnavailable
			}
			return err
		}

		now := time.Now()
		grant.ConsumedAt = &now
		return tx.Save(&grant).Error
	})
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

// HasUsableGrant reports whether userID has an unconsumed, unexpired export
// grant for certID. Read-only: unlike ConsumeForCert, it never marks
// anything consumed -- it exists so a caller can show "Download" vs
// "Request export" without spending the single-use grant just by asking.
// Implemented as an existence check (LIMIT 1) rather than a full COUNT.
func (r *CertificateExportGrantRepository) HasUsableGrant(certID, userID uuid.UUID) (bool, error) {
	var ids []uuid.UUID
	err := r.db.Model(&models.CertificateExportGrant{}).
		Where("managed_certificate_id = ? AND granted_to = ? AND consumed_at IS NULL AND expires_at > ?", certID, userID, time.Now()).
		Limit(1).
		Pluck("id", &ids).Error
	if err != nil {
		return false, err
	}
	return len(ids) > 0, nil
}

// ListUsableGrantCertIDs returns the subset of certIDs for which userID has
// an unconsumed, unexpired export grant, in ONE query -- callers enriching a
// whole page of certificates must not issue one HasUsableGrant call per
// cert. A certificate can have more than one qualifying grant row, so the
// query selects DISTINCT managed_certificate_id. Empty input returns an
// empty result without touching the database.
func (r *CertificateExportGrantRepository) ListUsableGrantCertIDs(userID uuid.UUID, certIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(certIDs) == 0 {
		return nil, nil
	}

	var out []uuid.UUID
	err := r.db.Model(&models.CertificateExportGrant{}).
		Distinct().
		Where("granted_to = ? AND consumed_at IS NULL AND expires_at > ? AND managed_certificate_id IN ?", userID, time.Now(), certIDs).
		Pluck("managed_certificate_id", &out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

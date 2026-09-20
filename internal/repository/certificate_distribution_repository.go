package repository

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

type CertificateDistributionRepository struct{ db *gorm.DB }

func NewCertificateDistributionRepository(db *gorm.DB) *CertificateDistributionRepository {
	return &CertificateDistributionRepository{db: db}
}

func (r *CertificateDistributionRepository) Upsert(cd *models.CertificateDistribution) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "managed_certificate_id"}},
		UpdateAll: true,
	}).Create(cd).Error
}

func (r *CertificateDistributionRepository) GetByCertificateID(certID uuid.UUID) (*models.CertificateDistribution, error) {
	var cd models.CertificateDistribution
	if err := r.db.Where("managed_certificate_id = ?", certID).First(&cd).Error; err != nil {
		return nil, err
	}
	return &cd, nil
}

func (r *CertificateDistributionRepository) DeleteByCertificateID(certID uuid.UUID) error {
	return r.db.Delete(&models.CertificateDistribution{}, "managed_certificate_id = ?", certID).Error
}

// ListByCertificateIDs returns all distribution rows whose
// managed_certificate_id is in certIDs, in a single query. Used to avoid an
// N+1 when enriching a page of certificates. Returns nil, nil for an empty
// input without querying.
func (r *CertificateDistributionRepository) ListByCertificateIDs(certIDs []uuid.UUID) ([]models.CertificateDistribution, error) {
	if len(certIDs) == 0 {
		return nil, nil
	}
	var out []models.CertificateDistribution
	if err := r.db.Where("managed_certificate_id IN ?", certIDs).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

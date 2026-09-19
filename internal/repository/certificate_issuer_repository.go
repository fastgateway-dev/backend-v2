package repository

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

type CertificateIssuerRepository struct{ db *gorm.DB }

func NewCertificateIssuerRepository(db *gorm.DB) *CertificateIssuerRepository {
	return &CertificateIssuerRepository{db: db}
}

func (r *CertificateIssuerRepository) Create(c *models.CertificateIssuer) error {
	return r.db.Create(c).Error
}

func (r *CertificateIssuerRepository) GetByID(id uuid.UUID) (*models.CertificateIssuer, error) {
	var c models.CertificateIssuer
	if err := r.db.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *CertificateIssuerRepository) List() ([]models.CertificateIssuer, error) {
	var out []models.CertificateIssuer
	if err := r.db.Order("created_at DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *CertificateIssuerRepository) Update(c *models.CertificateIssuer) error {
	return r.db.Save(c).Error
}

func (r *CertificateIssuerRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.CertificateIssuer{}, "id = ?", id).Error
}

func (r *CertificateIssuerRepository) CountByDNSCredential(dnsCredentialID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.CertificateIssuer{}).
		Where("config->>'dnsCredentialId' = ?", dnsCredentialID.String()).
		Count(&n).Error
	return n, err
}

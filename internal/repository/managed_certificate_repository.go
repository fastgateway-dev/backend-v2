package repository

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

type ManagedCertificateRepository struct{ db *gorm.DB }

func NewManagedCertificateRepository(db *gorm.DB) *ManagedCertificateRepository {
	return &ManagedCertificateRepository{db: db}
}

func (r *ManagedCertificateRepository) Create(c *models.ManagedCertificate) error {
	return r.db.Create(c).Error
}

func (r *ManagedCertificateRepository) GetByID(id uuid.UUID) (*models.ManagedCertificate, error) {
	var c models.ManagedCertificate
	if err := r.db.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ManagedCertificateRepository) Update(c *models.ManagedCertificate) error {
	return r.db.Save(c).Error
}

func (r *ManagedCertificateRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.ManagedCertificate{}, "id = ?", id).Error
}

func (r *ManagedCertificateRepository) ListByProject(projectID uuid.UUID, page, limit int, status string) ([]models.ManagedCertificate, int64, error) {
	var out []models.ManagedCertificate
	var total int64

	query := r.db.Where("project_id = ?", projectID)
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Model(&models.ManagedCertificate{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	if err := query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&out).Error; err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

func (r *ManagedCertificateRepository) CountByIssuer(issuerID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.ManagedCertificate{}).Where("issuer_id = ?", issuerID).Count(&n).Error
	return n, err
}

func (r *ManagedCertificateRepository) CountByIssuerAndProject(issuerID, projectID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.ManagedCertificate{}).
		Where("issuer_id = ? AND project_id = ?", issuerID, projectID).Count(&n).Error
	return n, err
}

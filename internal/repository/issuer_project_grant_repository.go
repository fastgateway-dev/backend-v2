package repository

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

type IssuerProjectGrantRepository struct{ db *gorm.DB }

func NewIssuerProjectGrantRepository(db *gorm.DB) *IssuerProjectGrantRepository {
	return &IssuerProjectGrantRepository{db: db}
}

func (r *IssuerProjectGrantRepository) Create(g *models.IssuerProjectGrant) error {
	return r.db.Create(g).Error
}

func (r *IssuerProjectGrantRepository) ListByIssuer(issuerID uuid.UUID) ([]models.IssuerProjectGrant, error) {
	var out []models.IssuerProjectGrant
	if err := r.db.Where("issuer_id = ?", issuerID).Order("created_at DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *IssuerProjectGrantRepository) Delete(issuerID, projectID uuid.UUID) error {
	return r.db.Where("issuer_id = ? AND project_id = ?", issuerID, projectID).Delete(&models.IssuerProjectGrant{}).Error
}

func (r *IssuerProjectGrantRepository) Exists(issuerID, projectID uuid.UUID) (bool, error) {
	var n int64
	err := r.db.Model(&models.IssuerProjectGrant{}).
		Where("issuer_id = ? AND project_id = ?", issuerID, projectID).
		Count(&n).Error
	return n > 0, err
}

func (r *IssuerProjectGrantRepository) ListProjectIDsForIssuer(issuerID uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := r.db.Model(&models.IssuerProjectGrant{}).
		Where("issuer_id = ?", issuerID).
		Pluck("project_id", &out).Error
	return out, err
}

package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DomainTemplateRepository handles domain template database operations
type DomainTemplateRepository struct {
	db *gorm.DB
}

// NewDomainTemplateRepository creates a new domain template repository
func NewDomainTemplateRepository(db *gorm.DB) *DomainTemplateRepository {
	return &DomainTemplateRepository{db: db}
}

// Create creates a new domain template
func (r *DomainTemplateRepository) Create(dt *models.DomainTemplate) error {
	return r.db.Create(dt).Error
}

// GetByID gets a domain template by ID
func (r *DomainTemplateRepository) GetByID(id uuid.UUID) (*models.DomainTemplate, error) {
	var dt models.DomainTemplate
	err := r.db.Where("id = ?", id).First(&dt).Error
	if err != nil {
		return nil, err
	}
	return &dt, nil
}

// GetByName gets a domain template by name in a project
func (r *DomainTemplateRepository) GetByName(projectID uuid.UUID, name string) (*models.DomainTemplate, error) {
	var dt models.DomainTemplate
	err := r.db.Where("project_id = ? AND name = ?", projectID, name).First(&dt).Error
	if err != nil {
		return nil, err
	}
	return &dt, nil
}

// ListByProjectID lists domain templates in a project with pagination
func (r *DomainTemplateRepository) ListByProjectID(projectID uuid.UUID, page, limit int) ([]models.DomainTemplate, int64, error) {
	var domainTemplates []models.DomainTemplate
	var total int64

	query := r.db.Model(&models.DomainTemplate{}).Where("project_id = ?", projectID)

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	err = query.Offset(offset).Limit(limit).Order("name ASC").Find(&domainTemplates).Error
	if err != nil {
		return nil, 0, err
	}

	return domainTemplates, total, nil
}

// ListByExposureType lists domain templates by exposure type in a project
func (r *DomainTemplateRepository) ListByExposureType(projectID uuid.UUID, exposureType models.ExposureType) ([]models.DomainTemplate, error) {
	var domainTemplates []models.DomainTemplate
	err := r.db.Where("project_id = ? AND exposure_type = ?", projectID, exposureType).
		Order("name ASC").Find(&domainTemplates).Error
	if err != nil {
		return nil, err
	}
	return domainTemplates, nil
}

// Update updates a domain template
func (r *DomainTemplateRepository) Update(dt *models.DomainTemplate) error {
	return r.db.Save(dt).Error
}

// Delete deletes a domain template
func (r *DomainTemplateRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.DomainTemplate{}, "id = ?", id).Error
}

// ExistsByName checks if a domain template with the given name exists in the project
func (r *DomainTemplateRepository) ExistsByName(projectID uuid.UUID, name string) (bool, error) {
	var count int64
	err := r.db.Model(&models.DomainTemplate{}).
		Where("project_id = ? AND name = ?", projectID, name).
		Count(&count).Error
	return count > 0, err
}

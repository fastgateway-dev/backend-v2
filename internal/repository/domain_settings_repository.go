package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DomainSettingsRepository handles domain settings database operations
type DomainSettingsRepository struct {
	db *gorm.DB
}

// NewDomainSettingsRepository creates a new domain settings repository
func NewDomainSettingsRepository(db *gorm.DB) *DomainSettingsRepository {
	return &DomainSettingsRepository{db: db}
}

// Create creates new domain settings
func (r *DomainSettingsRepository) Create(settings *models.DomainSettings) error {
	return r.db.Create(settings).Error
}

// GetByID gets domain settings by ID
func (r *DomainSettingsRepository) GetByID(id uuid.UUID) (*models.DomainSettings, error) {
	var settings models.DomainSettings
	err := r.db.Where("id = ?", id).First(&settings).Error
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// GetByDomainID gets domain settings by domain ID
func (r *DomainSettingsRepository) GetByDomainID(domainID uuid.UUID) (*models.DomainSettings, error) {
	var settings models.DomainSettings
	err := r.db.Where("domain_id = ?", domainID).First(&settings).Error
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// ListByProjectID lists domain settings for a project
func (r *DomainSettingsRepository) ListByProjectID(projectID uuid.UUID) ([]models.DomainSettings, error) {
	var settings []models.DomainSettings
	err := r.db.Where("project_id = ?", projectID).Find(&settings).Error
	return settings, err
}

// Update updates domain settings
func (r *DomainSettingsRepository) Update(settings *models.DomainSettings) error {
	return r.db.Save(settings).Error
}

// Delete deletes domain settings by ID
func (r *DomainSettingsRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.DomainSettings{}, "id = ?", id).Error
}

// DeleteByDomainID deletes domain settings by domain ID
func (r *DomainSettingsRepository) DeleteByDomainID(domainID uuid.UUID) error {
	return r.db.Delete(&models.DomainSettings{}, "domain_id = ?", domainID).Error
}

// ExistsByDomainID checks if domain settings exist for a domain
func (r *DomainSettingsRepository) ExistsByDomainID(domainID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.DomainSettings{}).
		Where("domain_id = ?", domainID).
		Count(&count).Error
	return count > 0, err
}

// Upsert creates or updates domain settings for a domain
func (r *DomainSettingsRepository) Upsert(settings *models.DomainSettings) error {
	var existing models.DomainSettings
	err := r.db.Where("domain_id = ?", settings.DomainID).First(&existing).Error
	if err == nil {
		// Update existing
		settings.ID = existing.ID
		settings.CreatedAt = existing.CreatedAt
		return r.db.Save(settings).Error
	}
	// Create new
	return r.db.Create(settings).Error
}

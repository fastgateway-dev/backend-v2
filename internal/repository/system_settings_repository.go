package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"gorm.io/gorm"
)

// SystemSettingsRepository handles system_settings DB operations
type SystemSettingsRepository struct {
	db *gorm.DB
}

// NewSystemSettingsRepository creates a new system settings repository
func NewSystemSettingsRepository(db *gorm.DB) *SystemSettingsRepository {
	return &SystemSettingsRepository{db: db}
}

// Get retrieves the singleton system settings row
func (r *SystemSettingsRepository) Get() (*models.SystemSettings, error) {
	var settings models.SystemSettings
	err := r.db.First(&settings).Error
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// Update saves the system settings
func (r *SystemSettingsRepository) Update(settings *models.SystemSettings) error {
	return r.db.Save(settings).Error
}

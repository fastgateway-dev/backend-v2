package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"gorm.io/gorm"
)

type SSOConfigRepository struct {
	db *gorm.DB
}

func NewSSOConfigRepository(db *gorm.DB) *SSOConfigRepository {
	return &SSOConfigRepository{db: db}
}

func (r *SSOConfigRepository) Get() (*models.SSOConfig, error) {
	var config models.SSOConfig
	err := r.db.First(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func (r *SSOConfigRepository) Update(config *models.SSOConfig) error {
	return r.db.Save(config).Error
}

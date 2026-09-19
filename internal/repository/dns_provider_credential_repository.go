package repository

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

type DNSProviderCredentialRepository struct{ db *gorm.DB }

func NewDNSProviderCredentialRepository(db *gorm.DB) *DNSProviderCredentialRepository {
	return &DNSProviderCredentialRepository{db: db}
}

func (r *DNSProviderCredentialRepository) Create(c *models.DNSProviderCredential) error {
	return r.db.Create(c).Error
}

func (r *DNSProviderCredentialRepository) GetByID(id uuid.UUID) (*models.DNSProviderCredential, error) {
	var c models.DNSProviderCredential
	if err := r.db.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *DNSProviderCredentialRepository) List() ([]models.DNSProviderCredential, error) {
	var out []models.DNSProviderCredential
	if err := r.db.Order("created_at DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *DNSProviderCredentialRepository) Update(c *models.DNSProviderCredential) error {
	return r.db.Save(c).Error
}

func (r *DNSProviderCredentialRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.DNSProviderCredential{}, "id = ?", id).Error
}

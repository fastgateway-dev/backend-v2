package repository

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

type DNSHostedZoneRepository struct{ db *gorm.DB }

func NewDNSHostedZoneRepository(db *gorm.DB) *DNSHostedZoneRepository {
	return &DNSHostedZoneRepository{db: db}
}

func (r *DNSHostedZoneRepository) Create(z *models.DNSHostedZone) error {
	return r.db.Create(z).Error
}

func (r *DNSHostedZoneRepository) GetByID(id uuid.UUID) (*models.DNSHostedZone, error) {
	var z models.DNSHostedZone
	if err := r.db.Where("id = ?", id).First(&z).Error; err != nil {
		return nil, err
	}
	return &z, nil
}

func (r *DNSHostedZoneRepository) List() ([]models.DNSHostedZone, error) {
	var zs []models.DNSHostedZone
	if err := r.db.Order("name").Find(&zs).Error; err != nil {
		return nil, err
	}
	return zs, nil
}

func (r *DNSHostedZoneRepository) Update(z *models.DNSHostedZone) error {
	return r.db.Save(z).Error
}

func (r *DNSHostedZoneRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.DNSHostedZone{}, "id = ?", id).Error
}

func (r *DNSHostedZoneRepository) CountByCredential(credID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.DNSHostedZone{}).Where("provider_credential_id = ?", credID).Count(&n).Error
	return n, err
}

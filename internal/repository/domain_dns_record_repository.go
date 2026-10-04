package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type DomainDNSRecordRepository struct{ db *gorm.DB }

func NewDomainDNSRecordRepository(db *gorm.DB) *DomainDNSRecordRepository {
	return &DomainDNSRecordRepository{db: db}
}

func (r *DomainDNSRecordRepository) Create(rec *models.DomainDNSRecord) error {
	return r.db.Create(rec).Error
}

func (r *DomainDNSRecordRepository) GetByDomainID(domainID uuid.UUID) (*models.DomainDNSRecord, error) {
	var rec models.DomainDNSRecord
	if err := r.db.Where("domain_id = ?", domainID).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *DomainDNSRecordRepository) Update(rec *models.DomainDNSRecord) error {
	return r.db.Save(rec).Error
}

func (r *DomainDNSRecordRepository) DeleteByDomainID(domainID uuid.UUID) error {
	return r.db.Where("domain_id = ?", domainID).Delete(&models.DomainDNSRecord{}).Error
}

func (r *DomainDNSRecordRepository) CountByCredential(credID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.DomainDNSRecord{}).Where("provider_credential_id = ?", credID).Count(&n).Error
	return n, err
}

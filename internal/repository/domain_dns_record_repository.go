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

// ListByProjectID returns every managed DNS record whose domain belongs to the
// given project, each joined with its domain hostname (the record name) and the
// name of the hosted zone it lives in, ordered by hostname. A record whose
// hosted zone row is missing still appears (LEFT JOIN) with an empty ZoneName.
func (r *DomainDNSRecordRepository) ListByProjectID(projectID uuid.UUID) ([]models.DNSRecordListItem, error) {
	var items []models.DNSRecordListItem
	err := r.db.
		Table("domain_dns_records AS rec").
		Select("rec.*, d.hostname AS domain_hostname, z.name AS zone_name").
		Joins("JOIN domains d ON d.id = rec.domain_id").
		Joins("LEFT JOIN dns_hosted_zones z ON z.id = rec.hosted_zone_id").
		Where("d.project_id = ?", projectID).
		Order("d.hostname ASC").
		Scan(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (r *DomainDNSRecordRepository) Update(rec *models.DomainDNSRecord) error {
	return r.db.Save(rec).Error
}

func (r *DomainDNSRecordRepository) DeleteByDomainID(domainID uuid.UUID) error {
	return r.db.Where("domain_id = ?", domainID).Delete(&models.DomainDNSRecord{}).Error
}

func (r *DomainDNSRecordRepository) CountByZone(zoneID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.DomainDNSRecord{}).Where("hosted_zone_id = ?", zoneID).Count(&n).Error
	return n, err
}

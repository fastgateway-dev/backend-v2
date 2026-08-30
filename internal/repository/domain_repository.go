package repository

import (
	"encoding/json"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DomainRepository handles domain database operations
type DomainRepository struct {
	db *gorm.DB
}

// NewDomainRepository creates a new domain repository
func NewDomainRepository(db *gorm.DB) *DomainRepository {
	return &DomainRepository{db: db}
}

// Create creates a new domain
func (r *DomainRepository) Create(domain *models.Domain) error {
	return r.db.Create(domain).Error
}

// GetByID gets a domain by ID
func (r *DomainRepository) GetByID(id uuid.UUID) (*models.Domain, error) {
	var domain models.Domain
	err := r.db.Where("id = ?", id).First(&domain).Error
	if err != nil {
		return nil, err
	}

	// Get route count
	var routeCount int64
	r.db.Model(&models.Route{}).Where("domain_id = ?", id).Count(&routeCount)
	domain.RouteCount = int(routeCount)

	return &domain, nil
}

// GetByIDs gets multiple domains by their IDs
func (r *DomainRepository) GetByIDs(ids []uuid.UUID) ([]models.Domain, error) {
	var domains []models.Domain
	if len(ids) == 0 {
		return domains, nil
	}
	err := r.db.Where("id IN ?", ids).Find(&domains).Error
	return domains, err
}

// ListByProjectID lists domains in a project with pagination
// search filters by hostname (case-insensitive partial match)
// status filters by domain status (pending, active, error)
func (r *DomainRepository) ListByProjectID(projectID uuid.UUID, page, limit int, search string, status string, labels map[string]string) ([]models.Domain, int64, error) {
	var domains []models.Domain
	var total int64

	query := r.db.Model(&models.Domain{}).Where("project_id = ?", projectID)

	// Apply search filter (hostname)
	if search != "" {
		searchPattern := "%" + search + "%"
		query = query.Where("LOWER(hostname) LIKE LOWER(?)", searchPattern)
	}

	// Apply status filter
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if len(labels) > 0 {
		labelsJSON, err := json.Marshal(labels)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal labels filter: %w", err)
		}
		query = query.Where("labels @> ?", string(labelsJSON))
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	err = query.Offset(offset).Limit(limit).Order("hostname ASC").Find(&domains).Error
	if err != nil {
		return nil, 0, err
	}

	// Get route counts
	for i := range domains {
		var routeCount int64
		r.db.Model(&models.Route{}).Where("domain_id = ?", domains[i].ID).Count(&routeCount)
		domains[i].RouteCount = int(routeCount)
	}

	return domains, total, nil
}

// Update updates a domain
func (r *DomainRepository) Update(domain *models.Domain) error {
	return r.db.Save(domain).Error
}

// Delete deletes a domain
func (r *DomainRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.Domain{}, "id = ?", id).Error
}

// ExistsByHostname checks if a domain with the given hostname exists in the project
func (r *DomainRepository) ExistsByHostname(projectID uuid.UUID, hostname string) (bool, error) {
	var count int64
	err := r.db.Model(&models.Domain{}).
		Where("project_id = ? AND hostname = ?", projectID, hostname).
		Count(&count).Error
	return count > 0, err
}

// ListByTemplateID lists domains using a specific domain template
func (r *DomainRepository) ListByTemplateID(templateID uuid.UUID) ([]models.Domain, error) {
	var domains []models.Domain
	err := r.db.Where("domain_template_id = ?", templateID).Find(&domains).Error
	return domains, err
}

// CountByProjectID returns the number of domains in a project
func (r *DomainRepository) CountByProjectID(projectID uuid.UUID) (int, error) {
	var count int64
	if err := r.db.Model(&models.Domain{}).Where("project_id = ?", projectID).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

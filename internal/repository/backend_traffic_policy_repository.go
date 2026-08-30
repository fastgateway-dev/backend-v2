package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BackendTrafficPolicyRepository handles backend traffic policy database operations
type BackendTrafficPolicyRepository struct {
	db *gorm.DB
}

// NewBackendTrafficPolicyRepository creates a new backend traffic policy repository
func NewBackendTrafficPolicyRepository(db *gorm.DB) *BackendTrafficPolicyRepository {
	return &BackendTrafficPolicyRepository{db: db}
}

// Create creates a new backend traffic policy
func (r *BackendTrafficPolicyRepository) Create(policy *models.BackendTrafficPolicy) error {
	return r.db.Create(policy).Error
}

// GetByID gets a backend traffic policy by ID
func (r *BackendTrafficPolicyRepository) GetByID(id uuid.UUID) (*models.BackendTrafficPolicy, error) {
	var policy models.BackendTrafficPolicy
	err := r.db.Where("id = ?", id).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetByRouteID gets a backend traffic policy by route ID
func (r *BackendTrafficPolicyRepository) GetByRouteID(routeID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	var policy models.BackendTrafficPolicy
	err := r.db.Where("route_id = ?", routeID).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetByDomainID gets a backend traffic policy by domain ID (for future per-domain support)
func (r *BackendTrafficPolicyRepository) GetByDomainID(domainID uuid.UUID) (*models.BackendTrafficPolicy, error) {
	var policy models.BackendTrafficPolicy
	err := r.db.Where("domain_id = ?", domainID).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// ListByProjectID lists backend traffic policies for a project
func (r *BackendTrafficPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.BackendTrafficPolicy, error) {
	var policies []models.BackendTrafficPolicy
	err := r.db.Where("project_id = ?", projectID).Find(&policies).Error
	return policies, err
}

// Update updates a backend traffic policy
func (r *BackendTrafficPolicyRepository) Update(policy *models.BackendTrafficPolicy) error {
	return r.db.Save(policy).Error
}

// Delete deletes a backend traffic policy by ID
func (r *BackendTrafficPolicyRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.BackendTrafficPolicy{}, "id = ?", id).Error
}

// DeleteByRouteID deletes a backend traffic policy by route ID
func (r *BackendTrafficPolicyRepository) DeleteByRouteID(routeID uuid.UUID) error {
	return r.db.Delete(&models.BackendTrafficPolicy{}, "route_id = ?", routeID).Error
}

// DeleteByDomainID deletes a backend traffic policy by domain ID
func (r *BackendTrafficPolicyRepository) DeleteByDomainID(domainID uuid.UUID) error {
	return r.db.Delete(&models.BackendTrafficPolicy{}, "domain_id = ?", domainID).Error
}

// ExistsByRouteID checks if a backend traffic policy exists for a route
func (r *BackendTrafficPolicyRepository) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.BackendTrafficPolicy{}).
		Where("route_id = ?", routeID).
		Count(&count).Error
	return count > 0, err
}

// ExistsByDomainID checks if a backend traffic policy exists for a domain
func (r *BackendTrafficPolicyRepository) ExistsByDomainID(domainID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.BackendTrafficPolicy{}).
		Where("domain_id = ?", domainID).
		Count(&count).Error
	return count > 0, err
}

// Upsert creates or updates a backend traffic policy for a route
func (r *BackendTrafficPolicyRepository) Upsert(policy *models.BackendTrafficPolicy) error {
	if policy.RouteID != nil {
		var existing models.BackendTrafficPolicy
		err := r.db.Where("route_id = ?", policy.RouteID).First(&existing).Error
		if err == nil {
			// Update existing
			policy.ID = existing.ID
			policy.CreatedAt = existing.CreatedAt
			return r.db.Save(policy).Error
		}
	} else if policy.DomainID != nil {
		var existing models.BackendTrafficPolicy
		err := r.db.Where("domain_id = ?", policy.DomainID).First(&existing).Error
		if err == nil {
			// Update existing
			policy.ID = existing.ID
			policy.CreatedAt = existing.CreatedAt
			return r.db.Save(policy).Error
		}
	}
	// Create new
	return r.db.Create(policy).Error
}

package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EnvoyExtensionPolicyRepository handles envoy extension policy database operations
type EnvoyExtensionPolicyRepository struct {
	db *gorm.DB
}

// NewEnvoyExtensionPolicyRepository creates a new envoy extension policy repository
func NewEnvoyExtensionPolicyRepository(db *gorm.DB) *EnvoyExtensionPolicyRepository {
	return &EnvoyExtensionPolicyRepository{db: db}
}

// Create creates a new envoy extension policy
func (r *EnvoyExtensionPolicyRepository) Create(policy *models.EnvoyExtensionPolicy) error {
	return r.db.Create(policy).Error
}

// GetByID gets an envoy extension policy by ID
func (r *EnvoyExtensionPolicyRepository) GetByID(id uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	var policy models.EnvoyExtensionPolicy
	err := r.db.Where("id = ?", id).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetByRouteID gets an envoy extension policy by route ID
func (r *EnvoyExtensionPolicyRepository) GetByRouteID(routeID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	var policy models.EnvoyExtensionPolicy
	err := r.db.Where("route_id = ?", routeID).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetByDomainID gets an envoy extension policy by domain ID
func (r *EnvoyExtensionPolicyRepository) GetByDomainID(domainID uuid.UUID) (*models.EnvoyExtensionPolicy, error) {
	var policy models.EnvoyExtensionPolicy
	err := r.db.Where("domain_id = ?", domainID).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// ListByProjectID lists envoy extension policies for a project
func (r *EnvoyExtensionPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.EnvoyExtensionPolicy, error) {
	var policies []models.EnvoyExtensionPolicy
	err := r.db.Where("project_id = ?", projectID).Find(&policies).Error
	return policies, err
}

// Update updates an envoy extension policy
func (r *EnvoyExtensionPolicyRepository) Update(policy *models.EnvoyExtensionPolicy) error {
	return r.db.Save(policy).Error
}

// Delete deletes an envoy extension policy by ID
func (r *EnvoyExtensionPolicyRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.EnvoyExtensionPolicy{}, "id = ?", id).Error
}

// DeleteByRouteID deletes an envoy extension policy by route ID
func (r *EnvoyExtensionPolicyRepository) DeleteByRouteID(routeID uuid.UUID) error {
	return r.db.Delete(&models.EnvoyExtensionPolicy{}, "route_id = ?", routeID).Error
}

// DeleteByDomainID deletes an envoy extension policy by domain ID
func (r *EnvoyExtensionPolicyRepository) DeleteByDomainID(domainID uuid.UUID) error {
	return r.db.Delete(&models.EnvoyExtensionPolicy{}, "domain_id = ?", domainID).Error
}

// ExistsByRouteID checks if an envoy extension policy exists for a route
func (r *EnvoyExtensionPolicyRepository) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.EnvoyExtensionPolicy{}).
		Where("route_id = ?", routeID).
		Count(&count).Error
	return count > 0, err
}

// ExistsByDomainID checks if an envoy extension policy exists for a domain
func (r *EnvoyExtensionPolicyRepository) ExistsByDomainID(domainID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.EnvoyExtensionPolicy{}).
		Where("domain_id = ?", domainID).
		Count(&count).Error
	return count > 0, err
}

// Upsert creates or updates an envoy extension policy for a route or domain
func (r *EnvoyExtensionPolicyRepository) Upsert(policy *models.EnvoyExtensionPolicy) error {
	var existing models.EnvoyExtensionPolicy
	var err error

	if policy.RouteID != nil {
		err = r.db.Where("route_id = ?", *policy.RouteID).First(&existing).Error
	} else if policy.DomainID != nil {
		err = r.db.Where("domain_id = ?", *policy.DomainID).First(&existing).Error
	} else {
		return r.db.Create(policy).Error
	}

	if err == nil {
		// Update existing
		policy.ID = existing.ID
		policy.CreatedAt = existing.CreatedAt
		return r.db.Save(policy).Error
	}
	// Create new
	return r.db.Create(policy).Error
}

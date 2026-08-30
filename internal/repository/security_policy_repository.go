package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SecurityPolicyRepository handles security policy database operations
type SecurityPolicyRepository struct {
	db *gorm.DB
}

// NewSecurityPolicyRepository creates a new security policy repository
func NewSecurityPolicyRepository(db *gorm.DB) *SecurityPolicyRepository {
	return &SecurityPolicyRepository{db: db}
}

// Create creates a new security policy
func (r *SecurityPolicyRepository) Create(policy *models.SecurityPolicy) error {
	return r.db.Create(policy).Error
}

// GetByID gets a security policy by ID
func (r *SecurityPolicyRepository) GetByID(id uuid.UUID) (*models.SecurityPolicy, error) {
	var policy models.SecurityPolicy
	err := r.db.Where("id = ?", id).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetByRouteID gets a security policy by route ID
func (r *SecurityPolicyRepository) GetByRouteID(routeID uuid.UUID) (*models.SecurityPolicy, error) {
	var policy models.SecurityPolicy
	err := r.db.Where("route_id = ?", routeID).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// ListByProjectID lists security policies for a project
func (r *SecurityPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.SecurityPolicy, error) {
	var policies []models.SecurityPolicy
	err := r.db.Where("project_id = ?", projectID).Find(&policies).Error
	return policies, err
}

// Update updates a security policy
func (r *SecurityPolicyRepository) Update(policy *models.SecurityPolicy) error {
	return r.db.Save(policy).Error
}

// Delete deletes a security policy by ID
func (r *SecurityPolicyRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.SecurityPolicy{}, "id = ?", id).Error
}

// DeleteByRouteID deletes a security policy by route ID
func (r *SecurityPolicyRepository) DeleteByRouteID(routeID uuid.UUID) error {
	return r.db.Delete(&models.SecurityPolicy{}, "route_id = ?", routeID).Error
}

// ExistsByRouteID checks if a security policy exists for a route
func (r *SecurityPolicyRepository) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.SecurityPolicy{}).
		Where("route_id = ?", routeID).
		Count(&count).Error
	return count > 0, err
}

// Upsert creates or updates a security policy for a route
func (r *SecurityPolicyRepository) Upsert(policy *models.SecurityPolicy) error {
	var existing models.SecurityPolicy
	err := r.db.Where("route_id = ?", policy.RouteID).First(&existing).Error
	if err == nil {
		// Update existing
		policy.ID = existing.ID
		policy.CreatedAt = existing.CreatedAt
		return r.db.Save(policy).Error
	}
	// Create new
	return r.db.Create(policy).Error
}

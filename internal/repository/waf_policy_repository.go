package repository

import (
	"errors"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// WafPolicyRepository handles WAF policy database operations
type WafPolicyRepository struct {
	db *gorm.DB
}

// NewWafPolicyRepository creates a new WAF policy repository
func NewWafPolicyRepository(db *gorm.DB) *WafPolicyRepository {
	return &WafPolicyRepository{db: db}
}

// Create creates a new WAF policy
func (r *WafPolicyRepository) Create(policy *models.WafPolicy) error {
	return r.db.Create(policy).Error
}

// GetByID gets a WAF policy by ID
func (r *WafPolicyRepository) GetByID(id uuid.UUID) (*models.WafPolicy, error) {
	var policy models.WafPolicy
	err := r.db.Where("id = ?", id).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetByRouteID gets a WAF policy by route ID
func (r *WafPolicyRepository) GetByRouteID(routeID uuid.UUID) (*models.WafPolicy, error) {
	var policy models.WafPolicy
	err := r.db.Where("route_id = ?", routeID).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// ListByProjectID lists WAF policies for a project
func (r *WafPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.WafPolicy, error) {
	var policies []models.WafPolicy
	err := r.db.Where("project_id = ?", projectID).Find(&policies).Error
	return policies, err
}

// Update updates a WAF policy
func (r *WafPolicyRepository) Update(policy *models.WafPolicy) error {
	return r.db.Save(policy).Error
}

// Delete deletes a WAF policy by ID
func (r *WafPolicyRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.WafPolicy{}, "id = ?", id).Error
}

// DeleteByRouteID deletes a WAF policy by route ID
func (r *WafPolicyRepository) DeleteByRouteID(routeID uuid.UUID) error {
	return r.db.Delete(&models.WafPolicy{}, "route_id = ?", routeID).Error
}

// ExistsByRouteID checks if a WAF policy exists for a route
func (r *WafPolicyRepository) ExistsByRouteID(routeID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.WafPolicy{}).
		Where("route_id = ?", routeID).
		Count(&count).Error
	return count > 0, err
}

// Upsert creates or updates a WAF policy for a route
func (r *WafPolicyRepository) Upsert(policy *models.WafPolicy) error {
	var existing models.WafPolicy
	err := r.db.Where("route_id = ?", policy.RouteID).First(&existing).Error
	if err == nil {
		// Update existing
		policy.ID = existing.ID
		policy.CreatedAt = existing.CreatedAt
		return r.db.Save(policy).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err // Return actual database errors
	}
	// Create new
	return r.db.Create(policy).Error
}

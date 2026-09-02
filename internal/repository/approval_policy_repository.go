package repository

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ApprovalPolicyRepository handles approval policy database operations
type ApprovalPolicyRepository struct {
	db *gorm.DB
}

// NewApprovalPolicyRepository creates a new approval policy repository
func NewApprovalPolicyRepository(db *gorm.DB) *ApprovalPolicyRepository {
	return &ApprovalPolicyRepository{db: db}
}

// GetByProjectAndEntity returns the policy for a project, entity type, and
// optional action.
//
// It translates gorm.ErrRecordNotFound to models.ErrPolicyNotFound so callers
// through the approval.PolicyStore port -- which must not import gorm --
// can distinguish genuine absence from a lookup failure. See
// internal/approval/ports.go's PolicyStore doc comment for the contract this
// satisfies.
func (r *ApprovalPolicyRepository) GetByProjectAndEntity(projectID uuid.UUID, entityType string, action *string) (*models.ApprovalPolicy, error) {
	var policy models.ApprovalPolicy
	query := r.db.Where("project_id = ? AND entity_type = ?", projectID, entityType)
	if action != nil {
		query = query.Where("action = ?", *action)
	} else {
		query = query.Where("action IS NULL")
	}
	err := query.First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, models.ErrPolicyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// ListByProjectID lists all policies for a project
func (r *ApprovalPolicyRepository) ListByProjectID(projectID uuid.UUID) ([]models.ApprovalPolicy, error) {
	var policies []models.ApprovalPolicy
	err := r.db.Where("project_id = ?", projectID).Order("entity_type ASC, action ASC").Find(&policies).Error
	return policies, err
}

// Upsert creates or updates a policy
func (r *ApprovalPolicyRepository) Upsert(policy *models.ApprovalPolicy) error {
	// Try to find existing
	var existing models.ApprovalPolicy
	query := r.db.Where("project_id = ? AND entity_type = ?", policy.ProjectID, policy.EntityType)
	if policy.Action != nil {
		query = query.Where("action = ?", *policy.Action)
	} else {
		query = query.Where("action IS NULL")
	}

	err := query.First(&existing).Error
	if err == nil {
		// Update existing
		existing.Stages = policy.Stages
		existing.UpdatedAt = time.Now()
		return r.db.Save(&existing).Error
	}
	// Create new
	return r.db.Create(policy).Error
}

// GetByID returns a policy by its ID
func (r *ApprovalPolicyRepository) GetByID(id uuid.UUID) (*models.ApprovalPolicy, error) {
	var policy models.ApprovalPolicy
	err := r.db.Where("id = ?", id).First(&policy).Error
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

// Create creates a new policy
func (r *ApprovalPolicyRepository) Create(policy *models.ApprovalPolicy) error {
	return r.db.Create(policy).Error
}

// Update updates an existing policy
func (r *ApprovalPolicyRepository) Update(policy *models.ApprovalPolicy) error {
	policy.UpdatedAt = time.Now()
	return r.db.Save(policy).Error
}

// Delete removes a policy by its ID
func (r *ApprovalPolicyRepository) Delete(id uuid.UUID) error {
	return r.db.Where("id = ?", id).Delete(&models.ApprovalPolicy{}).Error
}

// SeedDefaults creates default approval policies for a new project
func (r *ApprovalPolicyRepository) SeedDefaults(projectID uuid.UUID) error {
	routeStages, _ := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "route.approve", TeamScope: "any"},
	})
	attachmentStages, _ := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "client.approve", TeamScope: "other_team"},
		{Order: 2, RequiredPermission: "client.approve", TeamScope: "any"},
	})

	policies := []models.ApprovalPolicy{
		{
			ProjectID:  projectID,
			EntityType: models.ApprovalEntityRoute,
			Stages:     routeStages,
		},
		{
			ProjectID:  projectID,
			EntityType: models.ApprovalEntityClientAttachment,
			Stages:     attachmentStages,
		},
	}

	for _, p := range policies {
		if err := r.db.Create(&p).Error; err != nil {
			return err
		}
	}
	return nil
}

package repository

import (
	"encoding/json"
	"errors"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UnifiedApprovalRepository handles unified approval database operations
type UnifiedApprovalRepository struct {
	db *gorm.DB
}

// NewUnifiedApprovalRepository creates a new unified approval repository
func NewUnifiedApprovalRepository(db *gorm.DB) *UnifiedApprovalRepository {
	return &UnifiedApprovalRepository{db: db}
}

// Create creates a new approval with its stages
func (r *UnifiedApprovalRepository) Create(approval *models.Approval) error {
	return r.db.Create(approval).Error
}

// GetByID gets an approval by ID with stages preloaded
func (r *UnifiedApprovalRepository) GetByID(id uuid.UUID) (*models.Approval, error) {
	var approval models.Approval
	err := r.db.Preload("Submitter").
		Preload("Stages", func(db *gorm.DB) *gorm.DB {
			return db.Order("stage_order ASC")
		}).
		Preload("Stages.Reviewer").
		Where("id = ?", id).First(&approval).Error
	if err != nil {
		return nil, err
	}
	return &approval, nil
}

// Update updates an approval
func (r *UnifiedApprovalRepository) Update(approval *models.Approval) error {
	return r.db.Save(approval).Error
}

// SetAIReview atomically sets ai_review only if it is currently NULL.
func (r *UnifiedApprovalRepository) SetAIReview(id uuid.UUID, aiReview json.RawMessage) error {
	result := r.db.Model(&models.Approval{}).
		Where("id = ? AND ai_review IS NULL", id).
		Update("ai_review", aiReview)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("AI review already exists for this approval")
	}
	return nil
}

// ListByProjectID lists approvals for a project with pagination and filters
func (r *UnifiedApprovalRepository) ListByProjectID(projectID uuid.UUID, page, limit int, status, entityType string) ([]models.Approval, int64, error) {
	var approvals []models.Approval
	var total int64

	query := r.db.Model(&models.Approval{}).Where("project_id = ?", projectID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if entityType != "" {
		query = query.Where("entity_type = ?", entityType)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	findQuery := r.db.Preload("Submitter").
		Preload("Stages", func(db *gorm.DB) *gorm.DB {
			return db.Order("stage_order ASC")
		}).
		Preload("Stages.Reviewer").
		Where("project_id = ?", projectID)
	if status != "" {
		findQuery = findQuery.Where("status = ?", status)
	}
	if entityType != "" {
		findQuery = findQuery.Where("entity_type = ?", entityType)
	}

	err := findQuery.Offset((page - 1) * limit).Limit(limit).
		Order("created_at DESC").
		Find(&approvals).Error

	return approvals, total, err
}

// CountPendingByProjectID counts pending approvals for a project
func (r *UnifiedApprovalRepository) CountPendingByProjectID(projectID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.Model(&models.Approval{}).
		Where("project_id = ? AND status = ?", projectID, models.ApprovalStatusPending).
		Count(&count).Error
	return count, err
}

// GetPendingByEntityID gets the pending approval for an entity
func (r *UnifiedApprovalRepository) GetPendingByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) (*models.Approval, error) {
	var approval models.Approval
	err := r.db.Preload("Submitter").
		Preload("Stages", func(db *gorm.DB) *gorm.DB {
			return db.Order("stage_order ASC")
		}).
		Preload("Stages.Reviewer").
		Where("entity_type = ? AND entity_id = ? AND status = ?", entityType, entityID, models.ApprovalStatusPending).
		First(&approval).Error
	if err != nil {
		return nil, err
	}
	return &approval, nil
}

// GetLatestApprovedByEntityID gets the most recent approved approval for an entity
func (r *UnifiedApprovalRepository) GetLatestApprovedByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) (*models.Approval, error) {
	var approval models.Approval
	err := r.db.Preload("Stages", func(db *gorm.DB) *gorm.DB {
		return db.Order("stage_order ASC")
	}).
		Where("entity_type = ? AND entity_id = ? AND status = ?", entityType, entityID, models.ApprovalStatusApproved).
		Order("created_at DESC").
		First(&approval).Error
	if err != nil {
		return nil, err
	}
	return &approval, nil
}

// DeleteByEntityID deletes all approvals (and their cascade-deleted stages) for a given entity.
// This is used to clean up orphaned approvals when the entity (route/attachment) is deleted.
func (r *UnifiedApprovalRepository) DeleteByEntityID(entityType models.ApprovalEntityType, entityID uuid.UUID) error {
	return r.db.Where("entity_type = ? AND entity_id = ?", entityType, entityID).Delete(&models.Approval{}).Error
}

// CreateStage creates a new approval stage
func (r *UnifiedApprovalRepository) CreateStage(stage *models.ApprovalStage) error {
	return r.db.Create(stage).Error
}

// UpdateStage updates an approval stage
func (r *UnifiedApprovalRepository) UpdateStage(stage *models.ApprovalStage) error {
	return r.db.Save(stage).Error
}

// GetStageByID gets a stage by ID
func (r *UnifiedApprovalRepository) GetStageByID(id uuid.UUID) (*models.ApprovalStage, error) {
	var stage models.ApprovalStage
	err := r.db.Preload("Reviewer").Where("id = ?", id).First(&stage).Error
	if err != nil {
		return nil, err
	}
	return &stage, nil
}

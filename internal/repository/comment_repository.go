package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CommentRepository handles approval comment database operations
type CommentRepository struct {
	db *gorm.DB
}

// NewCommentRepository creates a new comment repository
func NewCommentRepository(db *gorm.DB) *CommentRepository {
	return &CommentRepository{db: db}
}

// Create creates a new approval comment
func (r *CommentRepository) Create(comment *models.ApprovalComment) error {
	return r.db.Create(comment).Error
}

// ListByApprovalID lists comments for an approval ordered by created_at ASC
func (r *CommentRepository) ListByApprovalID(approvalID uuid.UUID) ([]models.ApprovalComment, error) {
	var comments []models.ApprovalComment
	err := r.db.Preload("User").
		Where("approval_id = ?", approvalID).
		Order("created_at ASC").
		Find(&comments).Error
	return comments, err
}

// CountByApprovalID counts comments for an approval
func (r *CommentRepository) CountByApprovalID(approvalID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.Model(&models.ApprovalComment{}).
		Where("approval_id = ?", approvalID).
		Count(&count).Error
	return count, err
}

package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ApprovalStageReviewRepository struct {
	db *gorm.DB
}

func NewApprovalStageReviewRepository(db *gorm.DB) *ApprovalStageReviewRepository {
	return &ApprovalStageReviewRepository{db: db}
}

func (r *ApprovalStageReviewRepository) Create(review *models.ApprovalStageReview) error {
	return r.db.Create(review).Error
}

func (r *ApprovalStageReviewRepository) CountByStageAndDecision(stageID uuid.UUID, decision string) (int64, error) {
	var count int64
	err := r.db.Model(&models.ApprovalStageReview{}).
		Where("stage_id = ? AND decision = ?", stageID, decision).
		Count(&count).Error
	return count, err
}

func (r *ApprovalStageReviewRepository) ListByStageID(stageID uuid.UUID) ([]models.ApprovalStageReview, error) {
	var reviews []models.ApprovalStageReview
	err := r.db.Where("stage_id = ?", stageID).Preload("Reviewer").Order("created_at ASC").Find(&reviews).Error
	return reviews, err
}

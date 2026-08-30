package repository

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AuditLogRepository handles audit log database operations
type AuditLogRepository struct {
	db *gorm.DB
}

// NewAuditLogRepository creates a new audit log repository
func NewAuditLogRepository(db *gorm.DB) *AuditLogRepository {
	return &AuditLogRepository{db: db}
}

// Create creates a new audit log entry
func (r *AuditLogRepository) Create(log *models.AuditLog) error {
	return r.db.Create(log).Error
}

// ListByProjectID lists audit logs for a project with pagination and filters
func (r *AuditLogRepository) ListByProjectID(projectID uuid.UUID, page, limit int, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, int64, error) {
	var logs []models.AuditLog
	var total int64

	query := r.db.Model(&models.AuditLog{}).Where("project_id = ?", projectID)

	if resourceType != "" {
		query = query.Where("resource_type = ?", resourceType)
	}

	if action != "" {
		query = query.Where("action = ?", action)
	}

	if userID != nil {
		query = query.Where("user_id = ?", *userID)
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	err = query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}

	return logs, total, nil
}

// ExportByProjectID returns all matching audit logs for a project (no pagination)
func (r *AuditLogRepository) ExportByProjectID(projectID uuid.UUID, resourceType, action string, userID *uuid.UUID) ([]models.AuditLog, error) {
	var logs []models.AuditLog

	query := r.db.Model(&models.AuditLog{}).Where("project_id = ?", projectID)

	if resourceType != "" {
		query = query.Where("resource_type = ?", resourceType)
	}

	if action != "" {
		query = query.Where("action = ?", action)
	}

	if userID != nil {
		query = query.Where("user_id = ?", *userID)
	}

	err := query.Order("created_at DESC").Find(&logs).Error
	if err != nil {
		return nil, err
	}

	return logs, nil
}

// DeleteOlderThan deletes audit logs older than the specified number of days for a project
func (r *AuditLogRepository) DeleteOlderThan(projectID uuid.UUID, days int) (int64, error) {
	result := r.db.Where("project_id = ? AND created_at < NOW() - INTERVAL '"+fmt.Sprintf("%d", days)+" days'", projectID).Delete(&models.AuditLog{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

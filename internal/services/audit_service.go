package services

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// AuditService handles audit logging
type AuditService struct {
	auditRepo repository.AuditLogRepositoryInterface
}

// NewAuditService creates a new audit service
func NewAuditService(auditRepo repository.AuditLogRepositoryInterface) *AuditService {
	return &AuditService{
		auditRepo: auditRepo,
	}
}

// LogAction logs an action
func (s *AuditService) LogAction(
	projectID *uuid.UUID,
	user *models.User,
	action string,
	resourceType string,
	resourceID *uuid.UUID,
	resourceName string,
	details models.AuditDetails,
	ipAddress string,
	userAgent string,
) error {
	var userID *uuid.UUID
	username := "system"

	if user != nil {
		userID = &user.ID
		username = user.Username
	}

	log := &models.AuditLog{
		ProjectID:    projectID,
		UserID:       userID,
		Username:     username,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ResourceName: resourceName,
		Details:      details,
		IPAddress:    ipAddress,
		UserAgent:    userAgent,
	}

	return s.auditRepo.Create(log)
}

// ListByProjectID lists audit logs for a project
func (s *AuditService) ListByProjectID(
	projectID uuid.UUID,
	page, limit int,
	resourceType, action string,
	userID *uuid.UUID,
) ([]models.AuditLog, int64, error) {
	return s.auditRepo.ListByProjectID(projectID, page, limit, resourceType, action, userID)
}

// ExportByProjectID returns all matching audit logs for a project (no pagination)
func (s *AuditService) ExportByProjectID(
	projectID uuid.UUID,
	resourceType, action string,
	userID *uuid.UUID,
) ([]models.AuditLog, error) {
	return s.auditRepo.ExportByProjectID(projectID, resourceType, action, userID)
}

// CleanupOlderThan deletes audit logs older than the specified number of days
func (s *AuditService) CleanupOlderThan(projectID uuid.UUID, days int) (int64, error) {
	return s.auditRepo.DeleteOlderThan(projectID, days)
}

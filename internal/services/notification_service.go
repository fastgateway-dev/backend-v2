package services

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// NotificationService handles notification operations
type NotificationService struct {
	notificationRepo repository.NotificationRepositoryInterface
}

// NewNotificationService creates a new notification service
func NewNotificationService(notificationRepo repository.NotificationRepositoryInterface) *NotificationService {
	return &NotificationService{notificationRepo: notificationRepo}
}

// List lists notifications for a user
func (s *NotificationService) List(userID uuid.UUID, unreadOnly bool, page, limit int) ([]models.Notification, int64, error) {
	return s.notificationRepo.ListByUserID(userID, unreadOnly, page, limit)
}

// CountUnread counts unread notifications for a user
func (s *NotificationService) CountUnread(userID uuid.UUID) (int64, error) {
	return s.notificationRepo.CountUnread(userID)
}

// MarkAsRead marks a notification as read
func (s *NotificationService) MarkAsRead(notificationID uuid.UUID, userID uuid.UUID) error {
	return s.notificationRepo.MarkAsRead(notificationID, userID)
}

// MarkAllAsRead marks all notifications as read for a user
func (s *NotificationService) MarkAllAsRead(userID uuid.UUID) error {
	return s.notificationRepo.MarkAllAsRead(userID)
}

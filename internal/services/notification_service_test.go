package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationService_List(t *testing.T) {
	mockRepo := new(mocks.MockNotificationRepository)
	svc := services.NewNotificationService(mockRepo)

	userID := uuid.New()
	notifications := []models.Notification{
		{ID: uuid.New(), UserID: userID, Title: "Notification 1"},
		{ID: uuid.New(), UserID: userID, Title: "Notification 2"},
	}
	mockRepo.On("ListByUserID", userID, false, 1, 10).Return(notifications, int64(2), nil)

	result, total, err := svc.List(userID, false, 1, 10)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(2), total)
	mockRepo.AssertExpectations(t)
}

func TestNotificationService_CountUnread(t *testing.T) {
	mockRepo := new(mocks.MockNotificationRepository)
	svc := services.NewNotificationService(mockRepo)

	userID := uuid.New()
	mockRepo.On("CountUnread", userID).Return(int64(5), nil)

	count, err := svc.CountUnread(userID)

	require.NoError(t, err)
	assert.Equal(t, int64(5), count)
	mockRepo.AssertExpectations(t)
}

func TestNotificationService_MarkAsRead(t *testing.T) {
	mockRepo := new(mocks.MockNotificationRepository)
	svc := services.NewNotificationService(mockRepo)

	notifID := uuid.New()
	userID := uuid.New()
	mockRepo.On("MarkAsRead", notifID, userID).Return(nil)

	err := svc.MarkAsRead(notifID, userID)

	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func TestNotificationService_MarkAllAsRead(t *testing.T) {
	mockRepo := new(mocks.MockNotificationRepository)
	svc := services.NewNotificationService(mockRepo)

	userID := uuid.New()
	mockRepo.On("MarkAllAsRead", userID).Return(nil)

	err := svc.MarkAllAsRead(userID)

	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func TestNotificationService_MarkAsRead_Error(t *testing.T) {
	mockRepo := new(mocks.MockNotificationRepository)
	svc := services.NewNotificationService(mockRepo)

	notifID := uuid.New()
	userID := uuid.New()
	mockRepo.On("MarkAsRead", notifID, userID).Return(errors.New("db error"))

	err := svc.MarkAsRead(notifID, userID)

	assert.EqualError(t, err, "db error")
	mockRepo.AssertExpectations(t)
}

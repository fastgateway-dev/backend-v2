package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCommentService_Create_Success(t *testing.T) {
	mockCommentRepo := new(mocks.MockCommentRepository)
	mockNotifRepo := new(mocks.MockNotificationRepository)
	mockApprovalRepo := new(mocks.MockUnifiedApprovalRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	svc := services.NewCommentService(mockCommentRepo, mockNotifRepo, mockApprovalRepo, mockTeamRepo)

	approvalID := uuid.New()
	projectID := uuid.New()
	user := &models.User{ID: uuid.New(), Username: "commenter"}
	approval := &models.Approval{ID: approvalID, ProjectID: projectID}

	mockApprovalRepo.On("GetByID", approvalID).Return(approval, nil)
	mockCommentRepo.On("Create", mock.AnythingOfType("*models.ApprovalComment")).Return(nil)
	// No mentions in body, so processMentions will call ListProjectTeams but find no matches
	mockTeamRepo.On("ListProjectTeams", projectID).Return([]models.ProjectTeamRole{}, nil).Maybe()

	comment, err := svc.Create(approvalID, user, "looks good")

	require.NoError(t, err)
	assert.Equal(t, approvalID, comment.ApprovalID)
	assert.Equal(t, user.ID, comment.UserID)
	assert.Equal(t, "looks good", comment.Body)
	assert.Equal(t, "commenter", comment.User.Username)
	mockCommentRepo.AssertExpectations(t)
	mockApprovalRepo.AssertExpectations(t)
}

func TestCommentService_Create_ApprovalNotFound(t *testing.T) {
	mockCommentRepo := new(mocks.MockCommentRepository)
	mockNotifRepo := new(mocks.MockNotificationRepository)
	mockApprovalRepo := new(mocks.MockUnifiedApprovalRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	svc := services.NewCommentService(mockCommentRepo, mockNotifRepo, mockApprovalRepo, mockTeamRepo)

	approvalID := uuid.New()
	user := &models.User{ID: uuid.New(), Username: "commenter"}

	mockApprovalRepo.On("GetByID", approvalID).Return(nil, gorm.ErrRecordNotFound)

	comment, err := svc.Create(approvalID, user, "test")

	assert.Nil(t, comment)
	assert.EqualError(t, err, "approval not found")
	mockApprovalRepo.AssertExpectations(t)
}

func TestCommentService_Create_WithMention(t *testing.T) {
	mockCommentRepo := new(mocks.MockCommentRepository)
	mockNotifRepo := new(mocks.MockNotificationRepository)
	mockApprovalRepo := new(mocks.MockUnifiedApprovalRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	svc := services.NewCommentService(mockCommentRepo, mockNotifRepo, mockApprovalRepo, mockTeamRepo)

	approvalID := uuid.New()
	projectID := uuid.New()
	teamID := uuid.New()
	mentionedUserID := uuid.New()
	user := &models.User{ID: uuid.New(), Username: "commenter"}
	approval := &models.Approval{ID: approvalID, ProjectID: projectID}

	mockApprovalRepo.On("GetByID", approvalID).Return(approval, nil)
	mockCommentRepo.On("Create", mock.AnythingOfType("*models.ApprovalComment")).Return(nil)
	mockTeamRepo.On("ListProjectTeams", projectID).Return([]models.ProjectTeamRole{
		{TeamID: teamID},
	}, nil)
	mockTeamRepo.On("ListMembers", teamID).Return([]models.User{
		{ID: mentionedUserID, Username: "alice"},
	}, nil)
	mockNotifRepo.On("Create", mock.AnythingOfType("*models.Notification")).Return(nil)

	comment, err := svc.Create(approvalID, user, "hey @alice check this")

	require.NoError(t, err)
	assert.NotNil(t, comment)
	mockNotifRepo.AssertCalled(t, "Create", mock.AnythingOfType("*models.Notification"))
}

func TestCommentService_ListByApprovalID(t *testing.T) {
	mockCommentRepo := new(mocks.MockCommentRepository)
	svc := services.NewCommentService(mockCommentRepo, nil, nil, nil)

	approvalID := uuid.New()
	comments := []models.ApprovalComment{
		{ID: uuid.New(), Body: "comment 1"},
		{ID: uuid.New(), Body: "comment 2"},
	}
	mockCommentRepo.On("ListByApprovalID", approvalID).Return(comments, nil)

	result, err := svc.ListByApprovalID(approvalID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	mockCommentRepo.AssertExpectations(t)
}

func TestCommentService_CountByApprovalID(t *testing.T) {
	mockCommentRepo := new(mocks.MockCommentRepository)
	svc := services.NewCommentService(mockCommentRepo, nil, nil, nil)

	approvalID := uuid.New()
	mockCommentRepo.On("CountByApprovalID", approvalID).Return(int64(3), nil)

	count, err := svc.CountByApprovalID(approvalID)

	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
	mockCommentRepo.AssertExpectations(t)
}

func TestCommentService_CountByApprovalID_Error(t *testing.T) {
	mockCommentRepo := new(mocks.MockCommentRepository)
	svc := services.NewCommentService(mockCommentRepo, nil, nil, nil)

	approvalID := uuid.New()
	mockCommentRepo.On("CountByApprovalID", approvalID).Return(int64(0), errors.New("db error"))

	count, err := svc.CountByApprovalID(approvalID)

	assert.EqualError(t, err, "db error")
	assert.Equal(t, int64(0), count)
	mockCommentRepo.AssertExpectations(t)
}

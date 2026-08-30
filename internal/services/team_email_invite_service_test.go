package services_test

import (
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

func newTestTeamEmailInviteService() (*services.TeamEmailInviteService, *mocks.MockTeamEmailInviteRepository, *mocks.MockUserRepository, *mocks.MockTeamRepository) {
	mockInviteRepo := new(mocks.MockTeamEmailInviteRepository)
	mockUserRepo := new(mocks.MockUserRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	svc := services.NewTeamEmailInviteService(mockInviteRepo, mockUserRepo, mockTeamRepo)
	return svc, mockInviteRepo, mockUserRepo, mockTeamRepo
}

func TestTeamEmailInviteService_AddMemberByEmail_UserExists(t *testing.T) {
	svc, _, mockUserRepo, mockTeamRepo := newTestTeamEmailInviteService()

	teamID := uuid.New()
	userID := uuid.New()
	invitedBy := uuid.New()
	user := &models.User{ID: userID, Email: "existing@example.com"}

	mockUserRepo.On("GetByEmail", "existing@example.com").Return(user, nil)
	mockTeamRepo.On("IsMember", teamID, userID).Return(false, nil)
	mockTeamRepo.On("AddMember", teamID, userID).Return(nil)

	result, err := svc.AddMemberByEmail(teamID, "existing@example.com", invitedBy)

	require.NoError(t, err)
	assert.Equal(t, "added", result.Type)
	assert.Equal(t, user, result.User)
	assert.Nil(t, result.Invite)
	mockUserRepo.AssertExpectations(t)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamEmailInviteService_AddMemberByEmail_UserExists_AlreadyMember(t *testing.T) {
	svc, _, mockUserRepo, mockTeamRepo := newTestTeamEmailInviteService()

	teamID := uuid.New()
	userID := uuid.New()
	user := &models.User{ID: userID}

	mockUserRepo.On("GetByEmail", "existing@example.com").Return(user, nil)
	mockTeamRepo.On("IsMember", teamID, userID).Return(true, nil)

	result, err := svc.AddMemberByEmail(teamID, "existing@example.com", uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "user is already a member of this team")
}

func TestTeamEmailInviteService_AddMemberByEmail_InviteCreated(t *testing.T) {
	svc, mockInviteRepo, mockUserRepo, _ := newTestTeamEmailInviteService()

	teamID := uuid.New()
	invitedBy := uuid.New()

	mockUserRepo.On("GetByEmail", "new@example.com").Return(nil, gorm.ErrRecordNotFound)
	mockInviteRepo.On("GetByEmail", "new@example.com").Return([]models.TeamEmailInvite{}, nil)
	mockInviteRepo.On("Create", mock.AnythingOfType("*models.TeamEmailInvite")).Return(nil)

	result, err := svc.AddMemberByEmail(teamID, "new@example.com", invitedBy)

	require.NoError(t, err)
	assert.Equal(t, "invited", result.Type)
	assert.NotNil(t, result.Invite)
	assert.Equal(t, teamID, result.Invite.TeamID)
	assert.Equal(t, "new@example.com", result.Invite.Email)
	mockUserRepo.AssertExpectations(t)
	mockInviteRepo.AssertExpectations(t)
}

func TestTeamEmailInviteService_AddMemberByEmail_DuplicateInvite(t *testing.T) {
	svc, mockInviteRepo, mockUserRepo, _ := newTestTeamEmailInviteService()

	teamID := uuid.New()

	mockUserRepo.On("GetByEmail", "new@example.com").Return(nil, gorm.ErrRecordNotFound)
	mockInviteRepo.On("GetByEmail", "new@example.com").Return([]models.TeamEmailInvite{
		{TeamID: teamID, Email: "new@example.com"},
	}, nil)

	result, err := svc.AddMemberByEmail(teamID, "new@example.com", uuid.New())

	assert.Nil(t, result)
	assert.EqualError(t, err, "an invite for this email already exists for this team")
}

func TestTeamEmailInviteService_ListInvites(t *testing.T) {
	svc, mockInviteRepo, _, _ := newTestTeamEmailInviteService()

	teamID := uuid.New()
	invites := []models.TeamEmailInvite{
		{ID: uuid.New(), TeamID: teamID, Email: "a@example.com"},
		{ID: uuid.New(), TeamID: teamID, Email: "b@example.com"},
	}
	mockInviteRepo.On("ListByTeam", teamID).Return(invites, nil)

	result, err := svc.ListInvites(teamID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	mockInviteRepo.AssertExpectations(t)
}

func TestTeamEmailInviteService_DeleteInvite(t *testing.T) {
	svc, mockInviteRepo, _, _ := newTestTeamEmailInviteService()

	inviteID := uuid.New()
	mockInviteRepo.On("Delete", inviteID).Return(nil)

	err := svc.DeleteInvite(inviteID)

	require.NoError(t, err)
	mockInviteRepo.AssertExpectations(t)
}

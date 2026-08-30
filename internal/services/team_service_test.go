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

func newTestTeamService() (*services.TeamService, *mocks.MockTeamRepository, *mocks.MockUserRepository, *mocks.MockPresetRepository) {
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockUserRepo := new(mocks.MockUserRepository)
	mockPresetRepo := new(mocks.MockPresetRepository)
	svc := services.NewTeamService(mockTeamRepo, mockUserRepo, mockPresetRepo)
	return svc, mockTeamRepo, mockUserRepo, mockPresetRepo
}

func TestTeamService_Create_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	mockTeamRepo.On("Create", mock.AnythingOfType("*models.Team")).Return(nil)

	input := &services.CreateTeamInput{Name: "dev-team", Description: "Development team"}
	team, err := svc.Create(input)

	require.NoError(t, err)
	assert.Equal(t, "dev-team", team.Name)
	assert.Equal(t, "Development team", team.Description)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_Create_Error(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	mockTeamRepo.On("Create", mock.AnythingOfType("*models.Team")).Return(errors.New("duplicate name"))

	input := &services.CreateTeamInput{Name: "dev-team"}
	team, err := svc.Create(input)

	assert.Nil(t, team)
	assert.EqualError(t, err, "duplicate name")
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_GetByID_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	id := uuid.New()
	expected := &models.Team{ID: id, Name: "dev-team"}
	mockTeamRepo.On("GetByID", id).Return(expected, nil)

	team, err := svc.GetByID(id)

	require.NoError(t, err)
	assert.Equal(t, expected, team)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_GetByID_NotFound(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	id := uuid.New()
	mockTeamRepo.On("GetByID", id).Return(nil, gorm.ErrRecordNotFound)

	team, err := svc.GetByID(id)

	assert.Nil(t, team)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_List(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	teams := []models.Team{
		{ID: uuid.New(), Name: "team1"},
		{ID: uuid.New(), Name: "team2"},
	}
	mockTeamRepo.On("List").Return(teams, nil)

	result, err := svc.List()

	require.NoError(t, err)
	assert.Len(t, result, 2)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_AddMember_Success(t *testing.T) {
	svc, mockTeamRepo, mockUserRepo, _ := newTestTeamService()

	teamID := uuid.New()
	userID := uuid.New()
	user := &models.User{ID: userID, Username: "testuser"}

	mockUserRepo.On("GetByID", userID).Return(user, nil)
	mockTeamRepo.On("IsMember", teamID, userID).Return(false, nil)
	mockTeamRepo.On("AddMember", teamID, userID).Return(nil)

	err := svc.AddMember(teamID, userID)

	require.NoError(t, err)
	mockTeamRepo.AssertExpectations(t)
	mockUserRepo.AssertExpectations(t)
}

func TestTeamService_AddMember_UserNotFound(t *testing.T) {
	svc, _, mockUserRepo, _ := newTestTeamService()

	teamID := uuid.New()
	userID := uuid.New()

	mockUserRepo.On("GetByID", userID).Return(nil, gorm.ErrRecordNotFound)

	err := svc.AddMember(teamID, userID)

	assert.EqualError(t, err, "user not found")
	mockUserRepo.AssertExpectations(t)
}

func TestTeamService_AddMember_AlreadyMember(t *testing.T) {
	svc, mockTeamRepo, mockUserRepo, _ := newTestTeamService()

	teamID := uuid.New()
	userID := uuid.New()
	user := &models.User{ID: userID}

	mockUserRepo.On("GetByID", userID).Return(user, nil)
	mockTeamRepo.On("IsMember", teamID, userID).Return(true, nil)

	err := svc.AddMember(teamID, userID)

	assert.EqualError(t, err, "user is already a member of this team")
	mockTeamRepo.AssertExpectations(t)
	mockUserRepo.AssertExpectations(t)
}

func TestTeamService_RemoveMember(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	teamID := uuid.New()
	userID := uuid.New()
	mockTeamRepo.On("RemoveMember", teamID, userID).Return(nil)

	err := svc.RemoveMember(teamID, userID)

	require.NoError(t, err)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_Delete(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	id := uuid.New()
	mockTeamRepo.On("Delete", id).Return(nil)

	err := svc.Delete(id)

	require.NoError(t, err)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_Update_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	id := uuid.New()
	existing := &models.Team{ID: id, Name: "old-name", Description: "old-desc"}
	mockTeamRepo.On("GetByID", id).Return(existing, nil)
	mockTeamRepo.On("Update", mock.AnythingOfType("*models.Team")).Return(nil)

	input := &services.UpdateTeamInput{Name: "new-name", Description: "new-desc"}
	team, err := svc.Update(id, input)

	require.NoError(t, err)
	assert.Equal(t, "new-name", team.Name)
	assert.Equal(t, "new-desc", team.Description)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_ListMembers_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	teamID := uuid.New()
	members := []models.User{
		{ID: uuid.New(), Username: "user1", Email: "user1@test.com"},
		{ID: uuid.New(), Username: "user2", Email: "user2@test.com"},
	}
	mockTeamRepo.On("ListMembers", teamID).Return(members, nil)

	result, err := svc.ListMembers(teamID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "user1", result[0].Username)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_ListMembers_Error(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	teamID := uuid.New()
	mockTeamRepo.On("ListMembers", teamID).Return([]models.User(nil), errors.New("db error"))

	result, err := svc.ListMembers(teamID)

	assert.Nil(t, result)
	assert.EqualError(t, err, "db error")
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_AssignTeamToProject_Success(t *testing.T) {
	svc, mockTeamRepo, _, mockPresetRepo := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	presetID := uuid.New()
	team := &models.Team{ID: teamID, Name: "dev-team"}
	preset := &models.PermissionPreset{ID: presetID, ProjectID: projectID, Name: "editor"}

	input := &services.AssignTeamInput{
		TeamID:    teamID,
		PresetIDs: []uuid.UUID{presetID},
	}

	mockTeamRepo.On("GetByID", teamID).Return(team, nil)
	// First call: check if already assigned (returns not found)
	// Second call: get newly created role
	ptr := &models.ProjectTeamRole{
		ID:        uuid.New(),
		ProjectID: projectID,
		TeamID:    teamID,
	}
	mockTeamRepo.On("GetProjectTeamRole", projectID, teamID).Return(nil, gorm.ErrRecordNotFound).Once()
	mockPresetRepo.On("GetByID", presetID).Return(preset, nil)
	mockTeamRepo.On("AssignTeamToProject", projectID, teamID, []uuid.UUID{presetID}).Return(nil)
	mockTeamRepo.On("GetProjectTeamRole", projectID, teamID).Return(ptr, nil).Once()

	result, err := svc.AssignTeamToProject(projectID, input)

	require.NoError(t, err)
	assert.Equal(t, projectID, result.ProjectID)
	assert.Equal(t, teamID, result.TeamID)
	assert.Equal(t, "dev-team", result.Team.Name)
}

func TestTeamService_AssignTeamToProject_TeamNotFound(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	input := &services.AssignTeamInput{
		TeamID:    teamID,
		PresetIDs: []uuid.UUID{uuid.New()},
	}

	mockTeamRepo.On("GetByID", teamID).Return(nil, gorm.ErrRecordNotFound)

	result, err := svc.AssignTeamToProject(projectID, input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "team not found")
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_AssignTeamToProject_AlreadyAssigned(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	team := &models.Team{ID: teamID, Name: "dev-team"}
	input := &services.AssignTeamInput{
		TeamID:    teamID,
		PresetIDs: []uuid.UUID{uuid.New()},
	}

	mockTeamRepo.On("GetByID", teamID).Return(team, nil)
	existing := &models.ProjectTeamRole{ID: uuid.New(), ProjectID: projectID, TeamID: teamID}
	mockTeamRepo.On("GetProjectTeamRole", projectID, teamID).Return(existing, nil)

	result, err := svc.AssignTeamToProject(projectID, input)

	assert.Nil(t, result)
	assert.EqualError(t, err, "team is already assigned to this project")
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_AssignTeamToProject_PresetNotFound(t *testing.T) {
	svc, mockTeamRepo, _, mockPresetRepo := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	presetID := uuid.New()
	team := &models.Team{ID: teamID, Name: "dev-team"}

	input := &services.AssignTeamInput{
		TeamID:    teamID,
		PresetIDs: []uuid.UUID{presetID},
	}

	mockTeamRepo.On("GetByID", teamID).Return(team, nil)
	mockTeamRepo.On("GetProjectTeamRole", projectID, teamID).Return(nil, gorm.ErrRecordNotFound)
	mockPresetRepo.On("GetByID", presetID).Return(nil, gorm.ErrRecordNotFound)

	result, err := svc.AssignTeamToProject(projectID, input)

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "preset not found")
	mockPresetRepo.AssertExpectations(t)
}

func TestTeamService_UpdateTeamPresets_Success(t *testing.T) {
	svc, mockTeamRepo, _, mockPresetRepo := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	presetID := uuid.New()
	preset := &models.PermissionPreset{ID: presetID, ProjectID: projectID, Name: "viewer"}

	input := &services.UpdateTeamPresetsInput{
		PresetIDs: []uuid.UUID{presetID},
	}

	mockPresetRepo.On("GetByID", presetID).Return(preset, nil)
	mockTeamRepo.On("UpdateTeamPresets", projectID, teamID, []uuid.UUID{presetID}).Return(nil)
	ptr := &models.ProjectTeamRole{
		ID:        uuid.New(),
		ProjectID: projectID,
		TeamID:    teamID,
	}
	mockTeamRepo.On("GetProjectTeamRole", projectID, teamID).Return(ptr, nil)

	result, err := svc.UpdateTeamPresets(projectID, teamID, input)

	require.NoError(t, err)
	assert.Equal(t, projectID, result.ProjectID)
	mockTeamRepo.AssertExpectations(t)
	mockPresetRepo.AssertExpectations(t)
}

func TestTeamService_UpdateTeamPresets_PresetWrongProject(t *testing.T) {
	svc, _, _, mockPresetRepo := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	presetID := uuid.New()
	otherProjectID := uuid.New()
	preset := &models.PermissionPreset{ID: presetID, ProjectID: otherProjectID, Name: "viewer"}

	input := &services.UpdateTeamPresetsInput{
		PresetIDs: []uuid.UUID{presetID},
	}

	mockPresetRepo.On("GetByID", presetID).Return(preset, nil)

	result, err := svc.UpdateTeamPresets(projectID, teamID, input)

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "does not belong to this project")
	mockPresetRepo.AssertExpectations(t)
}

func TestTeamService_RemoveTeamFromProject_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	mockTeamRepo.On("RemoveTeamFromProject", projectID, teamID).Return(nil)

	err := svc.RemoveTeamFromProject(projectID, teamID)

	require.NoError(t, err)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_RemoveTeamFromProject_Error(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	mockTeamRepo.On("RemoveTeamFromProject", projectID, teamID).Return(errors.New("not found"))

	err := svc.RemoveTeamFromProject(projectID, teamID)

	assert.EqualError(t, err, "not found")
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_ListProjectTeams_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	roles := []models.ProjectTeamRole{
		{ID: uuid.New(), ProjectID: projectID, TeamID: uuid.New()},
		{ID: uuid.New(), ProjectID: projectID, TeamID: uuid.New()},
	}
	mockTeamRepo.On("ListProjectTeams", projectID).Return(roles, nil)

	result, err := svc.ListProjectTeams(projectID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_ListProjectTeams_Empty(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	mockTeamRepo.On("ListProjectTeams", projectID).Return([]models.ProjectTeamRole{}, nil)

	result, err := svc.ListProjectTeams(projectID)

	require.NoError(t, err)
	assert.Empty(t, result)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_GetProjectTeamRole_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	expected := &models.ProjectTeamRole{
		ID:        uuid.New(),
		ProjectID: projectID,
		TeamID:    teamID,
	}
	mockTeamRepo.On("GetProjectTeamRole", projectID, teamID).Return(expected, nil)

	result, err := svc.GetProjectTeamRole(projectID, teamID)

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_GetProjectTeamRole_NotFound(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()
	mockTeamRepo.On("GetProjectTeamRole", projectID, teamID).Return(nil, gorm.ErrRecordNotFound)

	result, err := svc.GetProjectTeamRole(projectID, teamID)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_GetMyTeams_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	userID := uuid.New()
	teams := []models.Team{
		{ID: uuid.New(), Name: "team-a"},
		{ID: uuid.New(), Name: "team-b"},
	}
	mockTeamRepo.On("GetTeamsByUserID", userID).Return(teams, nil)

	result, err := svc.GetUserTeams(userID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "team-a", result[0].Name)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_GetMyTeams_Empty(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	userID := uuid.New()
	mockTeamRepo.On("GetTeamsByUserID", userID).Return([]models.Team{}, nil)

	result, err := svc.GetUserTeams(userID)

	require.NoError(t, err)
	assert.Empty(t, result)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_GetMyTeamsInProject_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	userID := uuid.New()
	roles := []models.ProjectTeamRole{
		{ID: uuid.New(), ProjectID: projectID, TeamID: uuid.New()},
	}
	mockTeamRepo.On("GetUserTeamsInProject", projectID, userID).Return(roles, nil)

	result, err := svc.GetUserTeamsInProject(projectID, userID)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_ListProjectMembers_Success(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	teamID1 := uuid.New()
	teamID2 := uuid.New()

	roles := []models.ProjectTeamRole{
		{ID: uuid.New(), ProjectID: projectID, TeamID: teamID1},
		{ID: uuid.New(), ProjectID: projectID, TeamID: teamID2},
	}
	user1 := models.User{ID: uuid.New(), Username: "alice", Email: "alice@test.com"}
	user2 := models.User{ID: uuid.New(), Username: "bob", Email: "bob@test.com"}

	mockTeamRepo.On("ListProjectTeams", projectID).Return(roles, nil)
	mockTeamRepo.On("ListMembers", teamID1).Return([]models.User{user1}, nil)
	mockTeamRepo.On("ListMembers", teamID2).Return([]models.User{user2, user1}, nil) // user1 in both teams

	result, err := svc.ListProjectMembers(projectID, "")

	require.NoError(t, err)
	// Should deduplicate - user1 appears in both teams
	assert.Len(t, result, 2)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_ListProjectMembers_WithSearch(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	teamID := uuid.New()

	roles := []models.ProjectTeamRole{
		{ID: uuid.New(), ProjectID: projectID, TeamID: teamID},
	}
	user1 := models.User{ID: uuid.New(), Username: "alice", Email: "alice@test.com"}
	user2 := models.User{ID: uuid.New(), Username: "bob", Email: "bob@test.com"}

	mockTeamRepo.On("ListProjectTeams", projectID).Return(roles, nil)
	mockTeamRepo.On("ListMembers", teamID).Return([]models.User{user1, user2}, nil)

	result, err := svc.ListProjectMembers(projectID, "alice")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "alice", result[0].Username)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamService_ListProjectMembers_NoTeams(t *testing.T) {
	svc, mockTeamRepo, _, _ := newTestTeamService()

	projectID := uuid.New()
	mockTeamRepo.On("ListProjectTeams", projectID).Return([]models.ProjectTeamRole{}, nil)

	result, err := svc.ListProjectMembers(projectID, "")

	require.NoError(t, err)
	assert.Empty(t, result)
	mockTeamRepo.AssertExpectations(t)
}

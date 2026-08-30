package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// TeamService handles team business logic
type TeamService struct {
	teamRepo   repository.TeamRepositoryInterface
	userRepo   repository.UserRepositoryInterface
	presetRepo repository.PresetRepositoryInterface
}

// NewTeamService creates a new team service
func NewTeamService(teamRepo repository.TeamRepositoryInterface, userRepo repository.UserRepositoryInterface, presetRepo repository.PresetRepositoryInterface) *TeamService {
	return &TeamService{
		teamRepo:   teamRepo,
		userRepo:   userRepo,
		presetRepo: presetRepo,
	}
}

// CreateTeamInput represents input for creating a global team
type CreateTeamInput struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// UpdateTeamInput represents input for updating a team
type UpdateTeamInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// AssignTeamInput represents input for assigning a team to a project with presets
type AssignTeamInput struct {
	TeamID    uuid.UUID   `json:"teamId" binding:"required"`
	PresetIDs []uuid.UUID `json:"presetIds" binding:"required,min=1"`
}

// UpdateTeamPresetsInput represents input for updating a team's presets in a project
type UpdateTeamPresetsInput struct {
	PresetIDs []uuid.UUID `json:"presetIds" binding:"required,min=1"`
}

// Create creates a new global team
func (s *TeamService) Create(input *CreateTeamInput) (*models.Team, error) {
	team := &models.Team{
		Name:        input.Name,
		Description: input.Description,
	}

	if err := s.teamRepo.Create(team); err != nil {
		return nil, err
	}

	return team, nil
}

// GetByID gets a team by ID
func (s *TeamService) GetByID(id uuid.UUID) (*models.Team, error) {
	return s.teamRepo.GetByID(id)
}

// List lists all global teams
func (s *TeamService) List() ([]models.Team, error) {
	return s.teamRepo.List()
}

// Update updates a team
func (s *TeamService) Update(id uuid.UUID, input *UpdateTeamInput) (*models.Team, error) {
	team, err := s.teamRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if input.Name != "" {
		team.Name = input.Name
	}

	if input.Description != "" {
		team.Description = input.Description
	}

	if err := s.teamRepo.Update(team); err != nil {
		return nil, err
	}

	return team, nil
}

// Delete deletes a team
func (s *TeamService) Delete(id uuid.UUID) error {
	return s.teamRepo.Delete(id)
}

// AddMember adds a member to a team
func (s *TeamService) AddMember(teamID, userID uuid.UUID) error {
	// Verify user exists
	_, err := s.userRepo.GetByID(userID)
	if err != nil {
		return errors.New("user not found")
	}

	// Check if already a member
	isMember, err := s.teamRepo.IsMember(teamID, userID)
	if err != nil {
		return err
	}
	if isMember {
		return errors.New("user is already a member of this team")
	}

	return s.teamRepo.AddMember(teamID, userID)
}

// RemoveMember removes a member from a team
func (s *TeamService) RemoveMember(teamID, userID uuid.UUID) error {
	return s.teamRepo.RemoveMember(teamID, userID)
}

// ListMembers lists members of a team
func (s *TeamService) ListMembers(teamID uuid.UUID) ([]models.User, error) {
	return s.teamRepo.ListMembers(teamID)
}

// ===== Project Team Preset Methods =====

// AssignTeamToProject assigns a team to a project with presets
func (s *TeamService) AssignTeamToProject(projectID uuid.UUID, input *AssignTeamInput) (*models.ProjectTeamRole, error) {
	// Verify team exists
	team, err := s.teamRepo.GetByID(input.TeamID)
	if err != nil {
		return nil, errors.New("team not found")
	}

	// Check if already assigned
	existing, _ := s.teamRepo.GetProjectTeamRole(projectID, input.TeamID)
	if existing != nil {
		return nil, errors.New("team is already assigned to this project")
	}

	// Verify all presets exist and belong to this project
	for _, presetID := range input.PresetIDs {
		preset, err := s.presetRepo.GetByID(presetID)
		if err != nil {
			return nil, fmt.Errorf("preset not found: %s", presetID)
		}
		if preset.ProjectID != projectID {
			return nil, fmt.Errorf("preset %s does not belong to this project", preset.Name)
		}
	}

	if err := s.teamRepo.AssignTeamToProject(projectID, input.TeamID, input.PresetIDs); err != nil {
		return nil, err
	}

	ptr, err := s.teamRepo.GetProjectTeamRole(projectID, input.TeamID)
	if err != nil {
		return nil, err
	}

	ptr.Team = *team
	return ptr, nil
}

// UpdateTeamPresets updates a team's presets in a project
func (s *TeamService) UpdateTeamPresets(projectID, teamID uuid.UUID, input *UpdateTeamPresetsInput) (*models.ProjectTeamRole, error) {
	// Verify all presets exist and belong to this project
	for _, presetID := range input.PresetIDs {
		preset, err := s.presetRepo.GetByID(presetID)
		if err != nil {
			return nil, fmt.Errorf("preset not found: %s", presetID)
		}
		if preset.ProjectID != projectID {
			return nil, fmt.Errorf("preset %s does not belong to this project", preset.Name)
		}
	}

	if err := s.teamRepo.UpdateTeamPresets(projectID, teamID, input.PresetIDs); err != nil {
		return nil, err
	}

	return s.teamRepo.GetProjectTeamRole(projectID, teamID)
}

// RemoveTeamFromProject removes a team from a project
func (s *TeamService) RemoveTeamFromProject(projectID, teamID uuid.UUID) error {
	return s.teamRepo.RemoveTeamFromProject(projectID, teamID)
}

// ListProjectTeams lists all teams assigned to a project
func (s *TeamService) ListProjectTeams(projectID uuid.UUID) ([]models.ProjectTeamRole, error) {
	return s.teamRepo.ListProjectTeams(projectID)
}

// GetProjectTeamRole gets a team's role in a project
func (s *TeamService) GetProjectTeamRole(projectID, teamID uuid.UUID) (*models.ProjectTeamRole, error) {
	return s.teamRepo.GetProjectTeamRole(projectID, teamID)
}

// GetUserTeamsInProject gets all teams a user belongs to in a project
func (s *TeamService) GetUserTeamsInProject(projectID, userID uuid.UUID) ([]models.ProjectTeamRole, error) {
	return s.teamRepo.GetUserTeamsInProject(projectID, userID)
}

// GetUserTeams returns all teams a user is a member of (global)
func (s *TeamService) GetUserTeams(userID uuid.UUID) ([]models.Team, error) {
	return s.teamRepo.GetTeamsByUserID(userID)
}

// HasPermissionInProject checks if a user has a specific permission in a project through team membership
func (s *TeamService) HasPermissionInProject(projectID, userID uuid.UUID, perm models.Permission) (bool, error) {
	return s.teamRepo.HasPermissionInProject(projectID, userID, perm)
}

// ListTeamProjects lists all projects a team is assigned to
func (s *TeamService) ListTeamProjects(teamID uuid.UUID) ([]models.ProjectTeamRole, error) {
	return s.teamRepo.ListTeamProjects(teamID)
}

// ListProjectMembers returns all unique users who are members of teams assigned to a project
func (s *TeamService) ListProjectMembers(projectID uuid.UUID, search string) ([]models.User, error) {
	teamRoles, err := s.teamRepo.ListProjectTeams(projectID)
	if err != nil {
		return nil, err
	}

	userMap := make(map[uuid.UUID]models.User)
	for _, tr := range teamRoles {
		members, err := s.teamRepo.ListMembers(tr.TeamID)
		if err != nil {
			continue
		}
		for _, m := range members {
			userMap[m.ID] = m
		}
	}

	var users []models.User
	for _, u := range userMap {
		if search == "" || containsIgnoreCase(u.Username, search) || containsIgnoreCase(u.Email, search) {
			users = append(users, u)
		}
	}
	return users, nil
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

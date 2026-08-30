package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TeamRepository handles team database operations
type TeamRepository struct {
	db *gorm.DB
}

// NewTeamRepository creates a new team repository
func NewTeamRepository(db *gorm.DB) *TeamRepository {
	return &TeamRepository{db: db}
}

// Create creates a new global team
func (r *TeamRepository) Create(team *models.Team) error {
	return r.db.Create(team).Error
}

// GetByID gets a team by ID
func (r *TeamRepository) GetByID(id uuid.UUID) (*models.Team, error) {
	var team models.Team
	err := r.db.Where("id = ?", id).First(&team).Error
	if err != nil {
		return nil, err
	}

	// Get member count
	var memberCount int64
	r.db.Model(&models.TeamMember{}).Where("team_id = ?", id).Count(&memberCount)
	team.MemberCount = int(memberCount)

	return &team, nil
}

// List lists all global teams
func (r *TeamRepository) List() ([]models.Team, error) {
	var teams []models.Team
	err := r.db.Order("name ASC").Find(&teams).Error
	if err != nil {
		return nil, err
	}

	// Get member counts
	for i := range teams {
		var memberCount int64
		r.db.Model(&models.TeamMember{}).Where("team_id = ?", teams[i].ID).Count(&memberCount)
		teams[i].MemberCount = int(memberCount)
	}

	return teams, nil
}

// Update updates a team
func (r *TeamRepository) Update(team *models.Team) error {
	return r.db.Save(team).Error
}

// Delete deletes a team
func (r *TeamRepository) Delete(id uuid.UUID) error {
	// Delete team members first
	if err := r.db.Delete(&models.TeamMember{}, "team_id = ?", id).Error; err != nil {
		return err
	}
	// Delete project team roles
	if err := r.db.Delete(&models.ProjectTeamRole{}, "team_id = ?", id).Error; err != nil {
		return err
	}
	return r.db.Delete(&models.Team{}, "id = ?", id).Error
}

// AddMember adds a member to a team
func (r *TeamRepository) AddMember(teamID, userID uuid.UUID) error {
	member := &models.TeamMember{
		TeamID: teamID,
		UserID: userID,
	}
	return r.db.Create(member).Error
}

// RemoveMember removes a member from a team
func (r *TeamRepository) RemoveMember(teamID, userID uuid.UUID) error {
	return r.db.Delete(&models.TeamMember{}, "team_id = ? AND user_id = ?", teamID, userID).Error
}

// ListMembers lists members of a team
func (r *TeamRepository) ListMembers(teamID uuid.UUID) ([]models.User, error) {
	var users []models.User
	err := r.db.Table("users").
		Joins("JOIN team_members ON users.id = team_members.user_id").
		Where("team_members.team_id = ?", teamID).
		Find(&users).Error
	return users, err
}

// IsMember checks if a user is a member of a team
func (r *TeamRepository) IsMember(teamID, userID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.TeamMember{}).
		Where("team_id = ? AND user_id = ?", teamID, userID).
		Count(&count).Error
	return count > 0, err
}

// GetTeamsByUserID returns all teams a user is a member of
func (r *TeamRepository) GetTeamsByUserID(userID uuid.UUID) ([]models.Team, error) {
	var teams []models.Team
	err := r.db.
		Joins("JOIN team_members ON team_members.team_id = teams.id").
		Where("team_members.user_id = ?", userID).
		Find(&teams).Error
	if err != nil {
		return nil, err
	}
	// Compute member count
	for i := range teams {
		var count int64
		r.db.Model(&models.TeamMember{}).Where("team_id = ?", teams[i].ID).Count(&count)
		teams[i].MemberCount = int(count)
	}
	return teams, nil
}

// ===== Project Team Role Methods =====

// AssignTeamToProject assigns a team to a project with presets
func (r *TeamRepository) AssignTeamToProject(projectID, teamID uuid.UUID, presetIDs []uuid.UUID) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Create the project team role
		ptr := &models.ProjectTeamRole{
			ProjectID: projectID,
			TeamID:    teamID,
		}
		if err := tx.Create(ptr).Error; err != nil {
			return err
		}

		// Create preset assignments
		for _, presetID := range presetIDs {
			ptp := &models.ProjectTeamPreset{
				ProjectTeamRoleID: ptr.ID,
				PresetID:          presetID,
			}
			if err := tx.Create(ptp).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateTeamPresets updates a team's preset assignments in a project
func (r *TeamRepository) UpdateTeamPresets(projectID, teamID uuid.UUID, presetIDs []uuid.UUID) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Get the project team role
		var ptr models.ProjectTeamRole
		if err := tx.Where("project_id = ? AND team_id = ?", projectID, teamID).First(&ptr).Error; err != nil {
			return err
		}

		// Delete existing preset assignments
		if err := tx.Delete(&models.ProjectTeamPreset{}, "project_team_role_id = ?", ptr.ID).Error; err != nil {
			return err
		}

		// Create new preset assignments
		for _, presetID := range presetIDs {
			ptp := &models.ProjectTeamPreset{
				ProjectTeamRoleID: ptr.ID,
				PresetID:          presetID,
			}
			if err := tx.Create(ptp).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveTeamFromProject removes a team from a project
func (r *TeamRepository) RemoveTeamFromProject(projectID, teamID uuid.UUID) error {
	return r.db.Delete(&models.ProjectTeamRole{}, "project_id = ? AND team_id = ?", projectID, teamID).Error
}

// ListProjectTeams lists all teams assigned to a project with their presets
func (r *TeamRepository) ListProjectTeams(projectID uuid.UUID) ([]models.ProjectTeamRole, error) {
	var ptrs []models.ProjectTeamRole
	err := r.db.Preload("Team").
		Preload("Presets.Preset").
		Where("project_id = ?", projectID).
		Find(&ptrs).Error
	if err != nil {
		return nil, err
	}

	// Get member counts and compute effective permissions
	for i := range ptrs {
		var memberCount int64
		r.db.Model(&models.TeamMember{}).Where("team_id = ?", ptrs[i].TeamID).Count(&memberCount)
		ptrs[i].Team.MemberCount = int(memberCount)

		// Compute effective permissions
		effectivePerms := ptrs[i].GetEffectivePermissions()
		ptrs[i].EffectivePermissions = make([]string, len(effectivePerms))
		for j, p := range effectivePerms {
			ptrs[i].EffectivePermissions[j] = string(p)
		}
	}

	return ptrs, err
}

// GetProjectTeamRole gets a specific team's role in a project with presets
func (r *TeamRepository) GetProjectTeamRole(projectID, teamID uuid.UUID) (*models.ProjectTeamRole, error) {
	var ptr models.ProjectTeamRole
	err := r.db.Preload("Team").
		Preload("Presets.Preset").
		Where("project_id = ? AND team_id = ?", projectID, teamID).
		First(&ptr).Error
	if err != nil {
		return nil, err
	}

	// Compute effective permissions
	effectivePerms := ptr.GetEffectivePermissions()
	ptr.EffectivePermissions = make([]string, len(effectivePerms))
	for i, p := range effectivePerms {
		ptr.EffectivePermissions[i] = string(p)
	}

	return &ptr, nil
}

// GetUserTeamsInProject gets all teams a user belongs to in a project (via project_team_roles)
func (r *TeamRepository) GetUserTeamsInProject(projectID, userID uuid.UUID) ([]models.ProjectTeamRole, error) {
	var ptrs []models.ProjectTeamRole
	err := r.db.Preload("Team").
		Preload("Presets.Preset").
		Joins("JOIN team_members ON project_team_roles.team_id = team_members.team_id").
		Where("project_team_roles.project_id = ? AND team_members.user_id = ?", projectID, userID).
		Find(&ptrs).Error
	return ptrs, err
}

// HasPermissionInProject checks if a user has a specific permission in a project through team membership
func (r *TeamRepository) HasPermissionInProject(projectID, userID uuid.UUID, perm models.Permission) (bool, error) {
	var count int64
	err := r.db.Model(&models.ProjectTeamPreset{}).
		Joins("JOIN project_team_roles ON project_team_presets.project_team_role_id = project_team_roles.id").
		Joins("JOIN team_members ON project_team_roles.team_id = team_members.team_id").
		Joins("JOIN permission_presets ON project_team_presets.preset_id = permission_presets.id").
		Where("project_team_roles.project_id = ? AND team_members.user_id = ? AND ? = ANY(permission_presets.permissions)",
			projectID, userID, string(perm)).
		Count(&count).Error
	return count > 0, err
}

// HasPermissionInAnyProject checks if a user has a specific permission in ANY project through team membership
func (r *TeamRepository) HasPermissionInAnyProject(userID uuid.UUID, perm models.Permission) (bool, error) {
	var count int64
	err := r.db.Model(&models.ProjectTeamPreset{}).
		Joins("JOIN project_team_roles ON project_team_presets.project_team_role_id = project_team_roles.id").
		Joins("JOIN team_members ON project_team_roles.team_id = team_members.team_id").
		Joins("JOIN permission_presets ON project_team_presets.preset_id = permission_presets.id").
		Where("team_members.user_id = ? AND ? = ANY(permission_presets.permissions)",
			userID, string(perm)).
		Count(&count).Error
	return count > 0, err
}

// HasAnyRoleInProject checks if a user has any role in a project through team membership
func (r *TeamRepository) HasAnyRoleInProject(projectID, userID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.ProjectTeamRole{}).
		Joins("JOIN team_members ON project_team_roles.team_id = team_members.team_id").
		Where("project_team_roles.project_id = ? AND team_members.user_id = ?", projectID, userID).
		Count(&count).Error
	return count > 0, err
}

// GetUserPermissionsInProject gets the merged permission set for a user in a project (union of all preset permissions)
func (r *TeamRepository) GetUserPermissionsInProject(projectID, userID uuid.UUID) ([]string, error) {
	ptrs, err := r.GetUserTeamsInProject(projectID, userID)
	if err != nil {
		return nil, err
	}

	// Merge all permissions from all presets into a set
	permSet := make(map[string]bool)
	for _, ptr := range ptrs {
		for _, ptp := range ptr.Presets {
			for _, p := range ptp.Preset.Permissions {
				permSet[p] = true
			}
		}
	}

	perms := make([]string, 0, len(permSet))
	for p := range permSet {
		perms = append(perms, p)
	}
	return perms, nil
}

// ListTeamProjects lists all projects a team is assigned to
func (r *TeamRepository) ListTeamProjects(teamID uuid.UUID) ([]models.ProjectTeamRole, error) {
	var ptrs []models.ProjectTeamRole
	err := r.db.Preload("Project").
		Where("team_id = ?", teamID).
		Find(&ptrs).Error
	return ptrs, err
}

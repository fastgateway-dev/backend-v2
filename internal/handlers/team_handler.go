package handlers

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TeamHandler handles global team endpoints
type TeamHandler struct {
	teamService        services.TeamServiceInterface
	teamRepo           repository.TeamRepositoryInterface
	auditService       services.AuditServiceInterface
	emailInviteService services.TeamEmailInviteServiceInterface
}

// SetEmailInviteService sets the email invite service on the handler
func (h *TeamHandler) SetEmailInviteService(s services.TeamEmailInviteServiceInterface) {
	h.emailInviteService = s
}

// NewTeamHandler creates a new team handler
func NewTeamHandler(teamService services.TeamServiceInterface, teamRepo repository.TeamRepositoryInterface, auditService services.AuditServiceInterface) *TeamHandler {
	return &TeamHandler{
		teamService:  teamService,
		teamRepo:     teamRepo,
		auditService: auditService,
	}
}

// ===== Global Team Endpoints =====

// List lists all global teams
// Access: Owner OR users with project.teams permission in any project
func (h *TeamHandler) List(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	// Owner can always list teams
	if !middleware.IsOwner(user) {
		// Check if user has project.teams permission in any project
		hasPermission, err := h.teamRepo.HasPermissionInAnyProject(user.ID, models.PermProjectTeams)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !hasPermission {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: requires owner role or project.teams permission"})
			return
		}
	}

	teams, err := h.teamService.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, teams)
}

// Create creates a new global team
func (h *TeamHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var input services.CreateTeamInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	team, err := h.teamService.Create(&input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log (no project context for global teams)
	h.auditService.LogAction(
		nil,
		user,
		"create",
		"team",
		&team.ID,
		team.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, team)
}

// Get gets a team by ID
func (h *TeamHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	team, err := h.teamService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		return
	}

	c.JSON(http.StatusOK, team)
}

// Update updates a global team
func (h *TeamHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	id, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	var input services.UpdateTeamInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	team, err := h.teamService.Update(id, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"update",
		"team",
		&team.ID,
		team.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, team)
}

// Delete deletes a global team
func (h *TeamHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	id, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	team, err := h.teamService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not found"})
		return
	}

	if err := h.teamService.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"delete",
		"team",
		&id,
		team.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// ===== Team Member Endpoints =====

// ListMembers lists team members
func (h *TeamHandler) ListMembers(c *gin.Context) {
	id, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	members, err := h.teamService.ListMembers(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, members)
}

// AddMemberRequest represents the request to add a member
type AddMemberRequest struct {
	UserID uuid.UUID `json:"userId" binding:"required"`
}

// AddMember adds a member to a team
func (h *TeamHandler) AddMember(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	teamID, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	var req AddMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.teamService.AddMember(teamID, req.UserID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"add_member",
		"team",
		&teamID,
		"",
		map[string]interface{}{"memberUserId": req.UserID},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusCreated)
}

// RemoveMember removes a member from a team
func (h *TeamHandler) RemoveMember(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	teamID, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	if err := h.teamService.RemoveMember(teamID, userID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"remove_member",
		"team",
		&teamID,
		"",
		map[string]interface{}{"memberUserId": userID},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// ===== Project Team Assignment Endpoints =====

// ListProjectTeams lists teams assigned to a project
func (h *TeamHandler) ListProjectTeams(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	teams, err := h.teamService.ListProjectTeams(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, teams)
}

// AssignTeamToProject assigns a team to a project with a role
func (h *TeamHandler) AssignTeamToProject(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var input services.AssignTeamInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ptr, err := h.teamService.AssignTeamToProject(projectID, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"assign_team",
		"project",
		&input.TeamID,
		"",
		map[string]interface{}{"presetIds": input.PresetIDs},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, ptr)
}

// GetProjectTeamRole gets a team's role in a project
func (h *TeamHandler) GetProjectTeamRole(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	teamID, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	ptr, err := h.teamService.GetProjectTeamRole(projectID, teamID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Team not assigned to this project"})
		return
	}

	c.JSON(http.StatusOK, ptr)
}

// UpdateTeamPresets updates a team's presets in a project
func (h *TeamHandler) UpdateTeamPresets(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	teamID, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	var input services.UpdateTeamPresetsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ptr, err := h.teamService.UpdateTeamPresets(projectID, teamID, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"update_team_presets",
		"project",
		&teamID,
		"",
		map[string]interface{}{"presetIds": input.PresetIDs},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, ptr)
}

// RemoveTeamFromProject removes a team from a project
func (h *TeamHandler) RemoveTeamFromProject(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	teamID, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	if err := h.teamService.RemoveTeamFromProject(projectID, teamID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"remove_team",
		"project",
		&teamID,
		"",
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// ListTeamProjects lists all projects a team is assigned to
func (h *TeamHandler) ListTeamProjects(c *gin.Context) {
	teamID, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
		return
	}

	projects, err := h.teamService.ListTeamProjects(teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, projects)
}

// ListMyTeams lists all teams the current user is a member of (global, not project-scoped)
func (h *TeamHandler) ListMyTeams(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	// Owner sees all teams
	if middleware.IsOwner(user) {
		teams, err := h.teamService.List()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, teams)
		return
	}

	teams, err := h.teamService.GetUserTeams(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, teams)
}

// ListMyTeamsInProject lists teams the current user is a member of in a project
func (h *TeamHandler) ListMyTeamsInProject(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	teams, err := h.teamService.GetUserTeamsInProject(projectID, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, teams)
}

// ===== Email Invite Endpoints =====

// AddMemberByEmail adds a member by email (creates invite if user doesn't exist)
func (h *TeamHandler) AddMemberByEmail(c *gin.Context) {
	teamID, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team ID"})
		return
	}

	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user := middleware.GetCurrentUser(c)
	result, err := h.emailInviteService.AddMemberByEmail(teamID, req.Email, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// ListInvites lists pending email invites for a team
func (h *TeamHandler) ListInvites(c *gin.Context) {
	teamID, err := uuid.Parse(c.Param("teamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team ID"})
		return
	}

	invites, err := h.emailInviteService.ListInvites(teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, invites)
}

// DeleteInvite deletes a pending email invite
func (h *TeamHandler) DeleteInvite(c *gin.Context) {
	inviteID, err := uuid.Parse(c.Param("inviteId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid invite ID"})
		return
	}

	if err := h.emailInviteService.DeleteInvite(inviteID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// ListProjectMembers lists all unique users in a project's teams
func (h *TeamHandler) ListProjectMembers(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	search := c.Query("search")
	members, err := h.teamService.ListProjectMembers(projectID, search)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, members)
}

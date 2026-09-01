package middleware

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PermissionChecker provides permission checking functions
type PermissionChecker struct {
	projectRepo repository.ProjectRepositoryInterface
	teamRepo    repository.TeamRepositoryInterface
}

// NewPermissionChecker creates a new permission checker
func NewPermissionChecker(
	projectRepo repository.ProjectRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
) *PermissionChecker {
	return &PermissionChecker{
		projectRepo: projectRepo,
		teamRepo:    teamRepo,
	}
}

// ProjectPermissions represents a user's permissions for a project
type ProjectPermissions struct {
	CanManageDomainTemplates bool     `json:"canManageDomainTemplates"`
	CanManageDomains         bool     `json:"canManageDomains"`
	CanManageTeams           bool     `json:"canManageTeams"`
	CanCreateRoutes          bool     `json:"canCreateRoutes"`
	CanApproveRoutes         bool     `json:"canApproveRoutes"`
	CanViewAudit             bool     `json:"canViewAudit"`
	Permissions              []string `json:"permissions"`
	IsOwner                  bool     `json:"isOwner"`
	IsProjectAdmin           bool     `json:"isProjectAdmin"`
}

// IsOwner checks if a user is a system owner
func IsOwner(user *models.User) bool {
	return user.Role == models.UserRoleOwner
}

// IsProjectAdmin checks if a user is a project admin
func (p *PermissionChecker) IsProjectAdmin(projectID, userID uuid.UUID) bool {
	isAdmin, err := p.projectRepo.IsAdmin(projectID, userID)
	if err != nil {
		return false
	}
	return isAdmin
}

// IsTeamMember checks if a user is a member of a team
func (p *PermissionChecker) IsTeamMember(teamID, userID uuid.UUID) (bool, error) {
	return p.teamRepo.IsMember(teamID, userID)
}

// CanAccessTeamResource reports whether user may act on a resource owned by
// team teamID: system owners always may, everyone else must be a member of
// that team.
//
// Phase 2F Task 4: this is the check fifteen ClientHandler call sites and two
// ClientAttachmentHandler call sites were spelling out inline against
// teamRepo.IsMember. A lookup error denies, exactly as the inline form did by
// discarding the error and testing the zero-value false.
func (p *PermissionChecker) CanAccessTeamResource(teamID uuid.UUID, user *models.User) bool {
	if IsOwner(user) {
		return true
	}
	isMember, err := p.teamRepo.IsMember(teamID, user.ID)
	if err != nil {
		return false
	}
	return isMember
}

// HasTeamPermission reports whether user is a system owner or holds perm
// through team membership in the project.
//
// This is deliberately NOT HasPermission: it has no project-admin bypass. The
// two ClientAttachmentHandler call sites it replaces tested ownership and then
// teamRepo.HasPermissionInProject directly, so a project admin who did not
// hold the permission through a team was denied. Granting them here would
// widen access, which Phase 2F Task 4 is not licensed to do.
func (p *PermissionChecker) HasTeamPermission(projectID uuid.UUID, user *models.User, perm models.Permission) bool {
	if IsOwner(user) {
		return true
	}
	has, err := p.teamRepo.HasPermissionInProject(projectID, user.ID, perm)
	if err != nil {
		return false
	}
	return has
}

// HasPermissionInAnyProject reports whether a user holds perm through team
// membership in any project.
//
// Unlike the checks above it returns the lookup error rather than folding it
// into a denial, because its one caller (TeamHandler.List) answers a failed
// lookup with 500, not 403. Phase 2F Task 4.
func (p *PermissionChecker) HasPermissionInAnyProject(userID uuid.UUID, perm models.Permission) (bool, error) {
	return p.teamRepo.HasPermissionInAnyProject(userID, perm)
}

// HasPermission checks if a user has a specific permission in a project
func (p *PermissionChecker) HasPermission(projectID uuid.UUID, user *models.User, perm models.Permission) bool {
	if IsOwner(user) {
		return true
	}
	if p.IsProjectAdmin(projectID, user.ID) {
		return true
	}
	has, err := p.teamRepo.HasPermissionInProject(projectID, user.ID, perm)
	if err != nil {
		return false
	}
	return has
}

// HasProjectAccess checks if a user has any access to a project
func (p *PermissionChecker) HasProjectAccess(projectID uuid.UUID, user *models.User) bool {
	// Owner has access to all projects
	if IsOwner(user) {
		return true
	}

	// Check if project admin
	if p.IsProjectAdmin(projectID, user.ID) {
		return true
	}

	// Check team membership
	teams, err := p.teamRepo.GetUserTeamsInProject(projectID, user.ID)
	if err != nil {
		return false
	}
	return len(teams) > 0
}

// CanManageDomainTemplates checks if user can manage domain templates
// Only Owner or Project Admin
func (p *PermissionChecker) CanManageDomainTemplates(projectID uuid.UUID, user *models.User) bool {
	if IsOwner(user) {
		return true
	}
	return p.IsProjectAdmin(projectID, user.ID)
}

// CanManageDomains checks if user can create/update/delete domains
// Owner, Project Admin, or user with domain.delete permission
func (p *PermissionChecker) CanManageDomains(projectID uuid.UUID, user *models.User) bool {
	if IsOwner(user) || p.IsProjectAdmin(projectID, user.ID) {
		return true
	}
	return p.HasPermission(projectID, user, models.PermDomainDelete)
}

// CanViewDomains checks if user can view domains
// Any team member, Owner, or Project Admin
func (p *PermissionChecker) CanViewDomains(projectID uuid.UUID, user *models.User) bool {
	return p.HasProjectAccess(projectID, user)
}

// CanManageTeams checks if user can manage teams
// Owner, Project Admin, or user with project.teams permission
func (p *PermissionChecker) CanManageTeams(projectID uuid.UUID, user *models.User) bool {
	if IsOwner(user) || p.IsProjectAdmin(projectID, user.ID) {
		return true
	}
	return p.HasPermission(projectID, user, models.PermProjectTeams)
}

// CanCreateRoutes checks if user can create/update/delete routes
// Owner, Project Admin, or user with route.create permission
func (p *PermissionChecker) CanCreateRoutes(projectID uuid.UUID, user *models.User) bool {
	return p.HasPermission(projectID, user, models.PermRouteCreate)
}

// CanApproveRoutes checks if user can approve/reject routes
// Owner, Project Admin, or user with route.approve permission
func (p *PermissionChecker) CanApproveRoutes(projectID uuid.UUID, user *models.User) bool {
	return p.HasPermission(projectID, user, models.PermRouteApprove)
}

// CanViewRoutes checks if user can view routes
// Any team member, Owner, or Project Admin
func (p *PermissionChecker) CanViewRoutes(projectID uuid.UUID, user *models.User) bool {
	return p.HasProjectAccess(projectID, user)
}

// CanViewAudit checks if user can view audit logs
// Owner, Project Admin, or user with audit.view permission
func (p *PermissionChecker) CanViewAudit(projectID uuid.UUID, user *models.User) bool {
	if IsOwner(user) || p.IsProjectAdmin(projectID, user.ID) {
		return true
	}
	return p.HasPermission(projectID, user, models.PermAuditView)
}

// GetProjectPermissions returns all permissions for a user in a project
func (p *PermissionChecker) GetProjectPermissions(projectID uuid.UUID, user *models.User) *ProjectPermissions {
	isOwner := IsOwner(user)
	isProjectAdmin := p.IsProjectAdmin(projectID, user.ID)

	// Get merged permissions
	var perms []string
	if isOwner || isProjectAdmin {
		// Owner and project admin get all permissions
		for _, perm := range models.AllPermissions {
			perms = append(perms, string(perm))
		}
	} else {
		perms, _ = p.teamRepo.GetUserPermissionsInProject(projectID, user.ID)
	}

	// Build permission set for fast lookup
	permSet := make(map[string]bool)
	for _, perm := range perms {
		permSet[perm] = true
	}

	canManageAll := isOwner || isProjectAdmin
	canApprove := canManageAll || permSet[string(models.PermRouteApprove)]
	canCreate := canManageAll || permSet[string(models.PermRouteCreate)]
	canManageTeams := canManageAll || permSet[string(models.PermProjectTeams)]
	canManageDomains := canManageAll || permSet[string(models.PermDomainDelete)]
	canViewAudit := canManageAll || permSet[string(models.PermAuditView)]

	return &ProjectPermissions{
		CanManageDomainTemplates: canManageAll, // Domain templates remain owner/admin only
		CanManageDomains:         canManageDomains,
		CanManageTeams:           canManageTeams,
		CanCreateRoutes:          canCreate,
		CanApproveRoutes:         canApprove,
		CanViewAudit:             canViewAudit,
		Permissions:              perms,
		IsOwner:                  isOwner,
		IsProjectAdmin:           isProjectAdmin,
	}
}

// Middleware functions for common permission checks

// RequireProjectAccess middleware checks if user has any access to the project
func (p *PermissionChecker) RequireProjectAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCurrentUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			c.Abort()
			return
		}

		projectIDStr := c.Param("projectId")
		projectID, err := uuid.Parse(projectIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
			c.Abort()
			return
		}

		if !p.HasProjectAccess(projectID, user) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: you are not a member of this project"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireDomainTemplateReadAccess middleware checks if user can read domain templates
// Allows users who can manage domains (needed for domain creation template selection)
func (p *PermissionChecker) RequireDomainTemplateReadAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCurrentUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			c.Abort()
			return
		}

		projectIDStr := c.Param("projectId")
		projectID, err := uuid.Parse(projectIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
			c.Abort()
			return
		}

		if !p.CanManageDomainTemplates(projectID, user) && !p.CanManageDomains(projectID, user) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: insufficient permissions to view domain templates"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireDomainTemplateAccess middleware checks if user can manage domain templates
func (p *PermissionChecker) RequireDomainTemplateAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCurrentUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			c.Abort()
			return
		}

		projectIDStr := c.Param("projectId")
		projectID, err := uuid.Parse(projectIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
			c.Abort()
			return
		}

		if !p.CanManageDomainTemplates(projectID, user) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage domain templates"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireTeamAccess middleware checks if user can manage teams
func (p *PermissionChecker) RequireTeamAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCurrentUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			c.Abort()
			return
		}

		projectIDStr := c.Param("projectId")
		projectID, err := uuid.Parse(projectIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
			c.Abort()
			return
		}

		if !p.CanManageTeams(projectID, user) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage teams"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireApprovalAccess middleware checks if user can view/manage approvals
// Any project member can view approvals; approve/reject is checked in service layer
func (p *PermissionChecker) RequireApprovalAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCurrentUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			c.Abort()
			return
		}

		projectIDStr := c.Param("projectId")
		projectID, err := uuid.Parse(projectIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
			c.Abort()
			return
		}

		if !p.HasProjectAccess(projectID, user) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: you are not a member of this project"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAdminAccess middleware checks if user is Owner or Project Admin
func (p *PermissionChecker) RequireAdminAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCurrentUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			c.Abort()
			return
		}

		projectIDStr := c.Param("projectId")
		projectID, err := uuid.Parse(projectIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
			c.Abort()
			return
		}

		if !IsOwner(user) && !p.IsProjectAdmin(projectID, user.ID) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can perform this action"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAuditAccess middleware checks if user can view audit logs
func (p *PermissionChecker) RequireAuditAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := GetCurrentUser(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			c.Abort()
			return
		}

		projectIDStr := c.Param("projectId")
		projectID, err := uuid.Parse(projectIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
			c.Abort()
			return
		}

		if !p.CanViewAudit(projectID, user) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: insufficient permissions to view audit logs"})
			c.Abort()
			return
		}

		c.Next()
	}
}

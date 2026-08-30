package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func newTestPermissionChecker() (*PermissionChecker, *mocks.MockProjectRepository, *mocks.MockTeamRepository) {
	projectRepo := new(mocks.MockProjectRepository)
	teamRepo := new(mocks.MockTeamRepository)
	checker := NewPermissionChecker(projectRepo, teamRepo)
	return checker, projectRepo, teamRepo
}

func newTestUser(role models.UserRole) *models.User {
	return &models.User{
		ID:       uuid.New(),
		Username: "testuser",
		Email:    "test@example.com",
		Role:     role,
		IsActive: true,
	}
}

// --- HasPermission tests ---

func TestHasPermission_OwnerAlwaysTrue(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	result := checker.HasPermission(projectID, user, models.PermRouteCreate)
	assert.True(t, result)
}

func TestHasPermission_ProjectAdminTrue(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(true, nil)

	result := checker.HasPermission(projectID, user, models.PermRouteCreate)
	assert.True(t, result)
	projectRepo.AssertExpectations(t)
}

func TestHasPermission_TeamMemberWithPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermRouteCreate).Return(true, nil)

	result := checker.HasPermission(projectID, user, models.PermRouteCreate)
	assert.True(t, result)
	projectRepo.AssertExpectations(t)
	teamRepo.AssertExpectations(t)
}

func TestHasPermission_TeamMemberWithoutPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermRouteCreate).Return(false, nil)

	result := checker.HasPermission(projectID, user, models.PermRouteCreate)
	assert.False(t, result)
	projectRepo.AssertExpectations(t)
	teamRepo.AssertExpectations(t)
}

// --- HasProjectAccess tests ---

func TestHasProjectAccess_Owner(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	result := checker.HasProjectAccess(projectID, user)
	assert.True(t, result)
}

func TestHasProjectAccess_ProjectAdmin(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(true, nil)

	result := checker.HasProjectAccess(projectID, user)
	assert.True(t, result)
	projectRepo.AssertExpectations(t)
}

func TestHasProjectAccess_TeamMember(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("GetUserTeamsInProject", projectID, user.ID).Return([]models.ProjectTeamRole{
		{ID: uuid.New(), ProjectID: projectID, TeamID: uuid.New()},
	}, nil)

	result := checker.HasProjectAccess(projectID, user)
	assert.True(t, result)
	projectRepo.AssertExpectations(t)
	teamRepo.AssertExpectations(t)
}

func TestHasProjectAccess_NoAccess(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("GetUserTeamsInProject", projectID, user.ID).Return([]models.ProjectTeamRole{}, nil)

	result := checker.HasProjectAccess(projectID, user)
	assert.False(t, result)
	projectRepo.AssertExpectations(t)
	teamRepo.AssertExpectations(t)
}

// --- IsProjectAdmin tests ---

func TestIsProjectAdmin_True(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	projectID := uuid.New()
	userID := uuid.New()

	projectRepo.On("IsAdmin", projectID, userID).Return(true, nil)

	result := checker.IsProjectAdmin(projectID, userID)
	assert.True(t, result)
	projectRepo.AssertExpectations(t)
}

func TestIsProjectAdmin_False(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	projectID := uuid.New()
	userID := uuid.New()

	projectRepo.On("IsAdmin", projectID, userID).Return(false, nil)

	result := checker.IsProjectAdmin(projectID, userID)
	assert.False(t, result)
	projectRepo.AssertExpectations(t)
}

// --- RequireProjectAccess middleware tests ---

func TestRequireProjectAccess_InvalidProjectID(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)

	user := newTestUser(models.UserRoleUser)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireProjectAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	c.Request = httptest.NewRequest(http.MethodGet, "/projects/invalid-uuid/test", nil)
	r.ServeHTTP(w, c.Request)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRequireProjectAccess_NoAccess(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("GetUserTeamsInProject", projectID, user.ID).Return([]models.ProjectTeamRole{}, nil)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireProjectAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	projectRepo.AssertExpectations(t)
	teamRepo.AssertExpectations(t)
}

func TestRequireProjectAccess_OwnerPasses(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireProjectAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireProjectAccess_NoUser(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		// Do NOT set user
		checker.RequireProjectAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- CanManageDomainTemplates tests ---

func TestCanManageDomainTemplates_Owner(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	assert.True(t, checker.CanManageDomainTemplates(projectID, user))
}

func TestCanManageDomainTemplates_ProjectAdmin(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(true, nil)

	assert.True(t, checker.CanManageDomainTemplates(projectID, user))
	projectRepo.AssertExpectations(t)
}

func TestCanManageDomainTemplates_RegularUser(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)

	assert.False(t, checker.CanManageDomainTemplates(projectID, user))
	projectRepo.AssertExpectations(t)
}

// --- CanManageDomains tests ---

func TestCanManageDomains_Owner(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	assert.True(t, checker.CanManageDomains(projectID, user))
}

func TestCanManageDomains_ProjectAdmin(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(true, nil)

	assert.True(t, checker.CanManageDomains(projectID, user))
	projectRepo.AssertExpectations(t)
}

func TestCanManageDomains_WithPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermDomainDelete).Return(true, nil)

	assert.True(t, checker.CanManageDomains(projectID, user))
	projectRepo.AssertExpectations(t)
	teamRepo.AssertExpectations(t)
}

func TestCanManageDomains_NoPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermDomainDelete).Return(false, nil)

	assert.False(t, checker.CanManageDomains(projectID, user))
}

// --- CanViewDomains tests ---

func TestCanViewDomains_Owner(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	assert.True(t, checker.CanViewDomains(projectID, user))
}

func TestCanViewDomains_TeamMember(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("GetUserTeamsInProject", projectID, user.ID).Return([]models.ProjectTeamRole{
		{ID: uuid.New(), ProjectID: projectID, TeamID: uuid.New()},
	}, nil)

	assert.True(t, checker.CanViewDomains(projectID, user))
}

func TestCanViewDomains_NoAccess(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("GetUserTeamsInProject", projectID, user.ID).Return([]models.ProjectTeamRole{}, nil)

	assert.False(t, checker.CanViewDomains(projectID, user))
}

// --- CanManageTeams tests ---

func TestCanManageTeams_Owner(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	assert.True(t, checker.CanManageTeams(projectID, user))
}

func TestCanManageTeams_ProjectAdmin(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(true, nil)

	assert.True(t, checker.CanManageTeams(projectID, user))
}

func TestCanManageTeams_WithPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermProjectTeams).Return(true, nil)

	assert.True(t, checker.CanManageTeams(projectID, user))
}

func TestCanManageTeams_NoPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermProjectTeams).Return(false, nil)

	assert.False(t, checker.CanManageTeams(projectID, user))
}

// --- CanCreateRoutes tests ---

func TestCanCreateRoutes_Owner(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	assert.True(t, checker.CanCreateRoutes(projectID, user))
}

func TestCanCreateRoutes_WithPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermRouteCreate).Return(true, nil)

	assert.True(t, checker.CanCreateRoutes(projectID, user))
}

func TestCanCreateRoutes_NoPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermRouteCreate).Return(false, nil)

	assert.False(t, checker.CanCreateRoutes(projectID, user))
}

// --- CanApproveRoutes tests ---

func TestCanApproveRoutes_Owner(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	assert.True(t, checker.CanApproveRoutes(projectID, user))
}

func TestCanApproveRoutes_WithPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermRouteApprove).Return(true, nil)

	assert.True(t, checker.CanApproveRoutes(projectID, user))
}

func TestCanApproveRoutes_NoPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermRouteApprove).Return(false, nil)

	assert.False(t, checker.CanApproveRoutes(projectID, user))
}

// --- CanViewRoutes tests ---

func TestCanViewRoutes_Owner(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	assert.True(t, checker.CanViewRoutes(projectID, user))
}

func TestCanViewRoutes_NoAccess(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("GetUserTeamsInProject", projectID, user.ID).Return([]models.ProjectTeamRole{}, nil)

	assert.False(t, checker.CanViewRoutes(projectID, user))
}

// --- CanViewAudit tests ---

func TestCanViewAudit_Owner(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	assert.True(t, checker.CanViewAudit(projectID, user))
}

func TestCanViewAudit_ProjectAdmin(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(true, nil)

	assert.True(t, checker.CanViewAudit(projectID, user))
}

func TestCanViewAudit_WithPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermAuditView).Return(true, nil)

	assert.True(t, checker.CanViewAudit(projectID, user))
}

func TestCanViewAudit_NoPermission(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermAuditView).Return(false, nil)

	assert.False(t, checker.CanViewAudit(projectID, user))
}

// --- GetProjectPermissions tests ---

func TestGetProjectPermissions_Owner(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	// Owner still calls IsProjectAdmin (returns false for owner user)
	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)

	perms := checker.GetProjectPermissions(projectID, user)
	assert.True(t, perms.IsOwner)
	assert.False(t, perms.IsProjectAdmin)
	assert.True(t, perms.CanManageDomainTemplates)
	assert.True(t, perms.CanManageDomains)
	assert.True(t, perms.CanManageTeams)
	assert.True(t, perms.CanCreateRoutes)
	assert.True(t, perms.CanApproveRoutes)
	assert.True(t, perms.CanViewAudit)
	assert.Equal(t, len(models.AllPermissions), len(perms.Permissions))
}

func TestGetProjectPermissions_ProjectAdmin(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(true, nil)

	perms := checker.GetProjectPermissions(projectID, user)
	assert.False(t, perms.IsOwner)
	assert.True(t, perms.IsProjectAdmin)
	assert.True(t, perms.CanManageDomainTemplates)
	assert.True(t, perms.CanManageDomains)
	assert.True(t, perms.CanManageTeams)
	assert.True(t, perms.CanCreateRoutes)
	assert.True(t, perms.CanApproveRoutes)
	assert.True(t, perms.CanViewAudit)
	assert.Equal(t, len(models.AllPermissions), len(perms.Permissions))
}

func TestGetProjectPermissions_RegularUser_SomePerms(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("GetUserPermissionsInProject", projectID, user.ID).Return(
		[]string{string(models.PermRouteCreate), string(models.PermAuditView)}, nil,
	)

	perms := checker.GetProjectPermissions(projectID, user)
	assert.False(t, perms.IsOwner)
	assert.False(t, perms.IsProjectAdmin)
	assert.False(t, perms.CanManageDomainTemplates)
	assert.False(t, perms.CanManageDomains)
	assert.False(t, perms.CanManageTeams)
	assert.True(t, perms.CanCreateRoutes)
	assert.False(t, perms.CanApproveRoutes)
	assert.True(t, perms.CanViewAudit)
	assert.Equal(t, 2, len(perms.Permissions))
}

func TestGetProjectPermissions_RegularUser_NoPerms(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("GetUserPermissionsInProject", projectID, user.ID).Return([]string{}, nil)

	perms := checker.GetProjectPermissions(projectID, user)
	assert.False(t, perms.IsOwner)
	assert.False(t, perms.IsProjectAdmin)
	assert.False(t, perms.CanManageDomainTemplates)
	assert.False(t, perms.CanManageDomains)
	assert.False(t, perms.CanManageTeams)
	assert.False(t, perms.CanCreateRoutes)
	assert.False(t, perms.CanApproveRoutes)
	assert.False(t, perms.CanViewAudit)
}

// --- RequireDomainTemplateAccess middleware tests ---

func TestRequireDomainTemplateAccess_NoUser(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		checker.RequireDomainTemplateAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireDomainTemplateAccess_InvalidProjectID(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireDomainTemplateAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/invalid/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRequireDomainTemplateAccess_OwnerPasses(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireDomainTemplateAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireDomainTemplateAccess_RegularUserDenied(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireDomainTemplateAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- RequireDomainTemplateReadAccess middleware tests ---

func TestRequireDomainTemplateReadAccess_NoUser(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		checker.RequireDomainTemplateReadAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireDomainTemplateReadAccess_InvalidProjectID(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireDomainTemplateReadAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/invalid/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRequireDomainTemplateReadAccess_OwnerPasses(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireDomainTemplateReadAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireDomainTemplateReadAccess_UserWithDomainManage(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	// Not admin, not template manager, but has domain.delete permission
	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermDomainDelete).Return(true, nil)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireDomainTemplateReadAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireDomainTemplateReadAccess_Denied(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermDomainDelete).Return(false, nil)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireDomainTemplateReadAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- RequireTeamAccess middleware tests ---

func TestRequireTeamAccess_NoUser(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		checker.RequireTeamAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireTeamAccess_InvalidProjectID(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireTeamAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/invalid/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRequireTeamAccess_OwnerPasses(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireTeamAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireTeamAccess_Denied(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermProjectTeams).Return(false, nil)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireTeamAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- RequireApprovalAccess middleware tests ---

func TestRequireApprovalAccess_NoUser(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		checker.RequireApprovalAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireApprovalAccess_InvalidProjectID(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireApprovalAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/invalid/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRequireApprovalAccess_OwnerPasses(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireApprovalAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireApprovalAccess_Denied(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("GetUserTeamsInProject", projectID, user.ID).Return([]models.ProjectTeamRole{}, nil)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireApprovalAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- RequireAdminAccess middleware tests ---

func TestRequireAdminAccess_NoUser(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		checker.RequireAdminAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAdminAccess_InvalidProjectID(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireAdminAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/invalid/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRequireAdminAccess_OwnerPasses(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireAdminAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAdminAccess_ProjectAdminPasses(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(true, nil)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireAdminAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAdminAccess_RegularUserDenied(t *testing.T) {
	checker, projectRepo, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireAdminAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- RequireAuditAccess middleware tests ---

func TestRequireAuditAccess_NoUser(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		checker.RequireAuditAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuditAccess_InvalidProjectID(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireAuditAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/invalid/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRequireAuditAccess_OwnerPasses(t *testing.T) {
	checker, _, _ := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleOwner)
	projectID := uuid.New()

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireAuditAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAuditAccess_Denied(t *testing.T) {
	checker, projectRepo, teamRepo := newTestPermissionChecker()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	user := newTestUser(models.UserRoleUser)
	projectID := uuid.New()

	projectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)
	teamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermAuditView).Return(false, nil)

	r.GET("/projects/:projectId/test", func(c *gin.Context) {
		c.Set("user", user)
		checker.RequireAuditAccess()(c)
	}, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- IsTeamMember tests ---

func TestIsTeamMember_True(t *testing.T) {
	checker, _, teamRepo := newTestPermissionChecker()
	teamID := uuid.New()
	userID := uuid.New()

	teamRepo.On("IsMember", teamID, userID).Return(true, nil)

	result, err := checker.IsTeamMember(teamID, userID)
	assert.NoError(t, err)
	assert.True(t, result)
}

func TestIsTeamMember_False(t *testing.T) {
	checker, _, teamRepo := newTestPermissionChecker()
	teamID := uuid.New()
	userID := uuid.New()

	teamRepo.On("IsMember", teamID, userID).Return(false, nil)

	result, err := checker.IsTeamMember(teamID, userID)
	assert.NoError(t, err)
	assert.False(t, result)
}

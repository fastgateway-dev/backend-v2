package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type fixedTokenExpiry struct{}

func (fixedTokenExpiry) GetJWTExpiry() time.Duration          { return time.Hour }
func (fixedTokenExpiry) GetRefreshTokenExpiry() time.Duration { return 2 * time.Hour }

type noForcedSSO struct{}

func (noForcedSSO) ShouldForceSSO(string, models.UserRole) bool { return false }

// projectRouteFixture is the real router (setupRouter) with real
// authentication and permission middleware over mocked repositories, so the
// tests exercise the route wiring itself. Handlers the tests never reach are
// zero-valued, as in TestRouteSpecParity.
type projectRouteFixture struct {
	router         *gin.Engine
	token          string
	projectID      uuid.UUID
	projectRepo    *mocks.MockProjectRepository
	teamRepo       *mocks.MockTeamRepository
	projectService *mocks.MockProjectService
}

func newProjectRouteFixture(t *testing.T) *projectRouteFixture {
	t.Helper()
	user := &models.User{ID: uuid.New(), Username: "member", Role: models.UserRoleUser, IsActive: true}

	userRepo := new(mocks.MockUserRepository)
	userRepo.On("GetByID", user.ID).Return(user, nil).Maybe()
	authService := services.NewAuthService(services.AuthServiceDeps{
		UserRepo:     userRepo,
		APITokenRepo: new(mocks.MockAPITokenRepository),
		Config:       &config.Config{JWTSecret: "project-route-authz-test-secret"},
		SSO:          noForcedSSO{},
		Settings:     fixedTokenExpiry{},
	})
	token, _, err := authService.GenerateTokensForUser(user)
	require.NoError(t, err)

	projectRepo := new(mocks.MockProjectRepository)
	teamRepo := new(mocks.MockTeamRepository)
	projectService := new(mocks.MockProjectService)
	audit := new(mocks.MockAuditService)
	audit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	deps := RouterDeps{
		AuthMiddleware:            middleware.NewAuthMiddleware(authService),
		PermChecker:               middleware.NewPermissionChecker(projectRepo, teamRepo),
		AuthHandler:               &handlers.AuthHandler{},
		SSOHandler:                &handlers.SSOHandler{},
		DocsHandler:               &handlers.DocsHandler{},
		UserHandler:               &handlers.UserHandler{},
		SystemSettingsHandler:     &handlers.SystemSettingsHandler{},
		TeamHandler:               &handlers.TeamHandler{},
		ClientHandler:             &handlers.ClientHandler{},
		ClientAttachmentHandler:   &handlers.ClientAttachmentHandler{},
		AIHandler:                 &handlers.AIHandler{},
		ProjectHandler:            handlers.NewProjectHandler(projectService, audit, nil, nil),
		MetricsHandler:            &handlers.MetricsHandler{},
		ProjectVersionHandler:     &handlers.ProjectVersionHandler{},
		PermissionHandler:         &handlers.PermissionHandler{},
		PresetHandler:             &handlers.PresetHandler{},
		DomainTemplateHandler:     &handlers.DomainTemplateHandler{},
		ProjectNamespaceHandler:   &handlers.ProjectNamespaceHandler{},
		DomainHandler:             &handlers.DomainHandler{},
		TopologyHandler:           &handlers.TopologyHandler{},
		OpenAPIImportHandler:      &handlers.OpenAPIImportHandler{},
		RouteHandler:              &handlers.RouteHandler{},
		RouteVersionHandler:       &handlers.RouteVersionHandler{},
		ApprovalHandler:           &handlers.ApprovalHandler{},
		CommentHandler:            &handlers.CommentHandler{},
		ApprovalPolicyHandler:     &handlers.ApprovalPolicyHandler{},
		K8sHandler:                &handlers.KubernetesHandler{},
		AuditHandler:              &handlers.AuditHandler{},
		NotificationHandler:       &handlers.NotificationHandler{},
		DNSCredentialHandler:      &handlers.DNSCredentialHandler{},
		CertificateIssuerHandler:  &handlers.CertificateIssuerHandler{},
		IssuerGrantHandler:        &handlers.IssuerGrantHandler{},
		ManagedCertificateHandler: &handlers.ManagedCertificateHandler{},
		ClientCertificateHandler:  &handlers.ClientCertificateHandler{},
		DNSRecordHandler:          &handlers.DNSRecordHandler{},
	}
	return &projectRouteFixture{
		router: setupRouter(deps), token: token, projectID: uuid.New(),
		projectRepo: projectRepo, teamRepo: teamRepo, projectService: projectService,
	}
}

// asNonMember: not a project admin and in no team of the project.
func (f *projectRouteFixture) asNonMember() {
	f.projectRepo.On("IsAdmin", f.projectID, mock.Anything).Return(false, nil)
	f.teamRepo.On("GetUserTeamsInProject", f.projectID, mock.Anything).Return([]models.ProjectTeamRole{}, nil)
}

// asTeamMember: in a team of the project, but not a project admin.
func (f *projectRouteFixture) asTeamMember() {
	f.projectRepo.On("IsAdmin", f.projectID, mock.Anything).Return(false, nil)
	f.teamRepo.On("GetUserTeamsInProject", f.projectID, mock.Anything).
		Return([]models.ProjectTeamRole{{ProjectID: f.projectID, TeamID: uuid.New()}}, nil)
}

func (f *projectRouteFixture) asProjectAdmin() {
	f.projectRepo.On("IsAdmin", f.projectID, mock.Anything).Return(true, nil)
}

func (f *projectRouteFixture) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/projects/"+f.projectID.String()+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

type projectRoute struct{ method, path string }

var projectReadRoutes = []projectRoute{
	{http.MethodGet, ""},
	{http.MethodGet, "/capabilities"},
	{http.MethodGet, "/versions"},
	{http.MethodPost, "/versions/refresh"},
	{http.MethodGet, "/admins"},
}

var projectAdminRoutes = []projectRoute{
	{http.MethodPatch, ""},
	{http.MethodDelete, ""},
	{http.MethodPost, "/test-connection"},
	{http.MethodPost, "/metrics/test-connection"},
}

// A logged-in user with no role in the project gets 403 on every project root
// route, and no handler runs (the project service mock has no expectations).
func TestProjectCoreRoutes_NonMemberIsForbidden(t *testing.T) {
	f := newProjectRouteFixture(t)
	f.asNonMember()

	for _, r := range append(append([]projectRoute{}, projectReadRoutes...), projectAdminRoutes...) {
		w := f.do(r.method, r.path, `{"approvalEnabled":false,"selfApprovalAllowed":true}`)
		assert.Equal(t, http.StatusForbidden, w.Code, "%s /projects/:id%s", r.method, r.path)
	}
	f.projectService.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
	f.projectService.AssertNotCalled(t, "Delete", mock.Anything)
	f.projectService.AssertNotCalled(t, "GetByID", mock.Anything)
}

// A team member can read the project but cannot change it, delete it, or
// trigger calls that use its stored credentials.
func TestProjectCoreRoutes_TeamMemberCanReadButNotChange(t *testing.T) {
	f := newProjectRouteFixture(t)
	f.asTeamMember()
	f.projectService.On("GetByID", f.projectID).Return(&models.Project{ID: f.projectID, Name: "p"}, nil)

	assert.Equal(t, http.StatusOK, f.do(http.MethodGet, "", "").Code)
	for _, r := range projectAdminRoutes {
		w := f.do(r.method, r.path, `{"name":"renamed"}`)
		assert.Equal(t, http.StatusForbidden, w.Code, "%s /projects/:id%s", r.method, r.path)
	}
	f.projectService.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
	f.projectService.AssertNotCalled(t, "Delete", mock.Anything)
}

func TestProjectCoreRoutes_ProjectAdminCanUpdate(t *testing.T) {
	f := newProjectRouteFixture(t)
	f.asProjectAdmin()
	f.projectService.On("Update", f.projectID, mock.AnythingOfType("*services.UpdateProjectInput")).
		Return(&models.Project{ID: f.projectID, Name: "renamed"}, nil)

	w := f.do(http.MethodPatch, "", `{"name":"renamed"}`)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	f.projectService.AssertExpectations(t)
}

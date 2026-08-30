package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestProjectNamespaceHandler_List_Success(t *testing.T) {
	mockNS := new(mocks.MockProjectNamespaceService)
	mockAudit := new(mocks.MockAuditService)
	mockProjectRepo := new(mocks.MockProjectRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	permChecker := middleware.NewPermissionChecker(mockProjectRepo, mockTeamRepo)
	h := handlers.NewProjectNamespaceHandler(mockNS, mockAudit, permChecker)

	projectID := uuid.New()
	namespaces := []models.ProjectNamespace{
		{ID: uuid.New(), ProjectID: projectID, Namespace: "ns1"},
		{ID: uuid.New(), ProjectID: projectID, Namespace: "ns2"},
	}
	mockNS.On("ListByProjectID", projectID).Return(namespaces, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/namespaces", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockNS.AssertExpectations(t)
}

func TestProjectNamespaceHandler_Create_Success(t *testing.T) {
	mockNS := new(mocks.MockProjectNamespaceService)
	mockAudit := new(mocks.MockAuditService)
	mockProjectRepo := new(mocks.MockProjectRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	permChecker := middleware.NewPermissionChecker(mockProjectRepo, mockTeamRepo)
	h := handlers.NewProjectNamespaceHandler(mockNS, mockAudit, permChecker)

	user := testUser() // owner role, bypasses permission check
	projectID := uuid.New()
	ns := &models.ProjectNamespace{ID: uuid.New(), ProjectID: projectID, Namespace: "new-ns"}
	mockNS.On("Create", projectID, mock.AnythingOfType("*services.CreateProjectNamespaceInput")).Return(ns, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"namespace":    "new-ns",
		"capabilities": []string{"deploy_gateway", "backend_service", "tls_secret"},
	})
	router := gin.New()
	router.POST("/projects/:projectId/namespaces", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/namespaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockNS.AssertExpectations(t)
}

func TestProjectNamespaceHandler_List_InvalidProjectID(t *testing.T) {
	mockNS := new(mocks.MockProjectNamespaceService)
	mockAudit := new(mocks.MockAuditService)
	mockProjectRepo := new(mocks.MockProjectRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	permChecker := middleware.NewPermissionChecker(mockProjectRepo, mockTeamRepo)
	h := handlers.NewProjectNamespaceHandler(mockNS, mockAudit, permChecker)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/invalid/namespaces", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "invalid"}}

	h.List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestProjectNamespaceHandler_Update_Success(t *testing.T) {
	mockNS := new(mocks.MockProjectNamespaceService)
	mockAudit := new(mocks.MockAuditService)
	mockProjectRepo := new(mocks.MockProjectRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	permChecker := middleware.NewPermissionChecker(mockProjectRepo, mockTeamRepo)
	h := handlers.NewProjectNamespaceHandler(mockNS, mockAudit, permChecker)

	user := testUser() // owner role
	projectID := uuid.New()
	nsID := uuid.New()
	updated := &models.ProjectNamespace{ID: nsID, ProjectID: projectID, Namespace: "ns", Capabilities: []string{"deploy_gateway"}}
	mockNS.On("Update", nsID, mock.AnythingOfType("*services.UpdateProjectNamespaceInput")).Return(updated, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"capabilities": []string{"deploy_gateway"},
	})

	router := gin.New()
	router.PATCH("/projects/:projectId/namespaces/:namespaceId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PATCH", "/projects/"+projectID.String()+"/namespaces/"+nsID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockNS.AssertExpectations(t)
}

func TestProjectNamespaceHandler_Update_NonAdmin_Forbidden(t *testing.T) {
	mockNS := new(mocks.MockProjectNamespaceService)
	mockAudit := new(mocks.MockAuditService)
	mockProjectRepo := new(mocks.MockProjectRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	permChecker := middleware.NewPermissionChecker(mockProjectRepo, mockTeamRepo)
	h := handlers.NewProjectNamespaceHandler(mockNS, mockAudit, permChecker)

	regularUser := &models.User{ID: uuid.New(), Username: "dev", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()
	nsID := uuid.New()

	// Project-admin lookup denies access; falls through to team perm check.
	mockProjectRepo.On("IsAdmin", projectID, regularUser.ID).Return(false, nil)
	mockTeamRepo.On("HasPermissionInProject", projectID, regularUser.ID, models.PermDomainDelete).Return(false, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"capabilities": []string{"deploy_gateway"},
	})

	router := gin.New()
	router.PATCH("/projects/:projectId/namespaces/:namespaceId", func(c *gin.Context) {
		c.Set("user", regularUser)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PATCH", "/projects/"+projectID.String()+"/namespaces/"+nsID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockNS.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

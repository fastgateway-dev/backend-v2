package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestProjectHandler_List_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()
	projects := []models.Project{
		{ID: uuid.New(), Name: "proj1"},
		{ID: uuid.New(), Name: "proj2"},
	}
	mockProject.On("List", user.ID, user.Role, 1, 20, "", map[string]string(nil)).Return(projects, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockProject.AssertExpectations(t)
}

func TestProjectHandler_Create_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()
	project := &models.Project{ID: uuid.New(), Name: "new-project"}
	mockProject.On("Create", mock.AnythingOfType("*services.CreateProjectInput"), user.ID).Return(project, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"name": "new-project"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/projects", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockProject.AssertExpectations(t)
}

func TestProjectHandler_Get_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	projectID := uuid.New()
	project := &models.Project{ID: projectID, Name: "proj1"}
	mockProject.On("GetByID", projectID).Return(project, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String(), nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockProject.AssertExpectations(t)
}

func TestProjectHandler_Get_InvalidID(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/invalid", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "invalid"}}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestProjectHandler_Delete_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()
	projectID := uuid.New()
	project := &models.Project{ID: projectID, Name: "proj-to-delete"}
	mockProject.On("GetByID", projectID).Return(project, nil)
	mockProject.On("Delete", projectID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockProject.AssertExpectations(t)
}

func TestProjectHandler_Update_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()
	projectID := uuid.New()
	project := &models.Project{ID: projectID, Name: "updated-project"}
	mockProject.On("Update", projectID, mock.AnythingOfType("*services.UpdateProjectInput")).Return(project, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"name": "updated-project"})
	router := gin.New()
	router.PUT("/projects/:projectId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockProject.AssertExpectations(t)
}

func TestProjectHandler_TestConnection_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	projectID := uuid.New()
	mockProject.On("TestConnection", projectID).Return(true, "Connected", "v1.28.0", nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/projects/"+projectID.String()+"/test-connection", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.TestConnection(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, true, resp["success"])
	assert.Equal(t, "v1.28.0", resp["kubernetesVersion"])
	mockProject.AssertExpectations(t)
}

func TestProjectHandler_ListAdmins_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	projectID := uuid.New()
	admins := []models.User{
		{ID: uuid.New(), Username: "admin1"},
		{ID: uuid.New(), Username: "admin2"},
	}
	mockProject.On("ListAdmins", projectID).Return(admins, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/admins", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.ListAdmins(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockProject.AssertExpectations(t)
}

func TestProjectHandler_AddAdmin_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()
	projectID := uuid.New()
	adminUserID := uuid.New()
	mockProject.On("AddAdmin", projectID, adminUserID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"userId": adminUserID.String()})
	router := gin.New()
	router.POST("/projects/:projectId/admins", func(c *gin.Context) {
		c.Set("user", user)
		h.AddAdmin(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/admins", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockProject.AssertExpectations(t)
}

func TestProjectHandler_Delete_NotFound(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()
	projectID := uuid.New()
	mockProject.On("GetByID", projectID).Return(nil, errors.New("not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/projects/"+projectID.String(), nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	c.Set("user", user)

	h.Delete(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestProjectHandler_RemoveAdmin_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()
	projectID := uuid.New()
	userID := uuid.New()
	mockProject.On("RemoveAdmin", projectID, userID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/admins/:userId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveAdmin(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/admins/"+userID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockProject.AssertExpectations(t)
}

func TestProjectHandler_RemoveAdmin_InvalidProjectID(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()

	router := gin.New()
	router.DELETE("/projects/:projectId/admins/:userId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveAdmin(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/bad-id/admins/"+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestProjectHandler_RemoveAdmin_InvalidUserID(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()
	projectID := uuid.New()

	router := gin.New()
	router.DELETE("/projects/:projectId/admins/:userId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveAdmin(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/admins/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestProjectHandler_RemoveAdmin_ServiceError(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	user := testUser()
	projectID := uuid.New()
	userID := uuid.New()
	mockProject.On("RemoveAdmin", projectID, userID).Return(errors.New("not found"))

	router := gin.New()
	router.DELETE("/projects/:projectId/admins/:userId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveAdmin(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/admins/"+userID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestProjectHandler_GetCapabilities_Success(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	projectID := uuid.New()
	mockK8s.On("IsRateLimitAvailable", mock.Anything, projectID).Return(true, nil)

	router := gin.New()
	router.GET("/projects/:projectId/capabilities", func(c *gin.Context) {
		h.GetCapabilities(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/capabilities", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, true, resp["rateLimitAvailable"])
}

func TestProjectHandler_GetCapabilities_InvalidID(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	router := gin.New()
	router.GET("/projects/:projectId/capabilities", func(c *gin.Context) {
		h.GetCapabilities(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/bad-id/capabilities", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestProjectHandler_GetCapabilities_ServiceError(t *testing.T) {
	mockProject := new(mocks.MockProjectService)
	mockAudit := new(mocks.MockAuditService)
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewProjectHandler(mockProject, mockAudit, mockK8s)

	projectID := uuid.New()
	mockK8s.On("IsRateLimitAvailable", mock.Anything, projectID).Return(false, errors.New("k8s error"))

	router := gin.New()
	router.GET("/projects/:projectId/capabilities", func(c *gin.Context) {
		h.GetCapabilities(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/capabilities", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

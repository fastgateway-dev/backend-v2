package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func routePermChecker() *middleware.PermissionChecker {
	return middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
}

func TestRouteHandler_List_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	domainID := uuid.New()
	routes := []models.Route{
		{ID: uuid.New(), Name: "route1"},
		{ID: uuid.New(), Name: "route2"},
	}
	mockRoute.On("ListByDomainID", domainID, 1, 20, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).Return(routes, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/"+domainID.String()+"/routes", nil)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_List_InvalidDomainID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/bad-id/routes", nil)
	c.Params = gin.Params{{Key: "domainId", Value: "bad-id"}}

	h.List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_Get_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser() // Owner role bypasses team check
	projectID := uuid.New()
	routeID := uuid.New()
	route := &models.Route{ID: routeID, Name: "route1", TeamID: uuid.New()}
	mockRoute.On("GetByID", routeID).Return(route, nil)
	mockRoute.On("GetSecurityPolicy", routeID).Return(nil, errors.New("not found"))
	mockRoute.On("GetBackendTrafficPolicy", routeID).Return(nil, errors.New("not found"))
	mockRoute.On("GetEnvoyExtensionPolicy", routeID).Return(nil, errors.New("not found"))
	mockRoute.On("GetWafPolicy", routeID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.GET("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Get(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Get_NonMember_Forbidden(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	mockTeam := new(mocks.MockTeamRepository)
	mockProject := new(mocks.MockProjectRepository)
	pc := middleware.NewPermissionChecker(mockProject, mockTeam)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	nonMember := &models.User{ID: uuid.New(), Username: "dev1", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()
	routeID := uuid.New()
	otherTeamID := uuid.New()
	route := &models.Route{ID: routeID, Name: "other-team-route", TeamID: otherTeamID}

	mockRoute.On("GetByID", routeID).Return(route, nil)
	mockProject.On("IsAdmin", projectID, nonMember.ID).Return(false, nil)
	mockTeam.On("IsMember", otherTeamID, nonMember.ID).Return(false, nil)

	router := gin.New()
	router.GET("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", nonMember)
		h.Get(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockRoute.AssertExpectations(t)
	mockTeam.AssertExpectations(t)
}

func TestRouteHandler_Get_InvalidID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	router := gin.New()
	router.GET("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Get(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+uuid.New().String()+"/domains/"+uuid.New().String()+"/routes/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_Get_NotFound(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	mockRoute.On("GetByID", routeID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.GET("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Get(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Create_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	teamID := uuid.New()
	route := &models.Route{ID: uuid.New(), Name: "new-route", TeamID: teamID}
	mockRoute.On("Create", domainID, mock.AnythingOfType("*services.CreateRouteInput"), user.ID).Return(route, nil)
	mockRoute.On("GetDomainName", mock.AnythingOfType("uuid.UUID")).Return("test-domain", nil)
	mockRoute.On("GetApprovalIDForEntity", models.ApprovalEntityRoute, route.ID).Return(nil, errors.New("not found"))
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":   "new-route",
		"teamId": teamID.String(),
		"config": map[string]interface{}{},
	})

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/routes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Update_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	route := &models.Route{ID: routeID, Name: "updated-route"}
	mockRoute.On("Update", routeID, mock.AnythingOfType("*services.UpdateRouteInput"), user.ID).Return(route, nil)
	mockRoute.On("GetDomainName", mock.AnythingOfType("uuid.UUID")).Return("test-domain", nil)
	mockRoute.On("GetApprovalIDForEntity", models.ApprovalEntityRoute, route.ID).Return(nil, errors.New("not found"))
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"config": map[string]interface{}{},
	})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Delete_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	route := &models.Route{ID: routeID, Name: "to-delete"}
	mockRoute.On("Delete", routeID, user.ID).Return(route, nil)
	mockRoute.On("GetDomainName", mock.AnythingOfType("uuid.UUID")).Return("test-domain", nil)
	mockRoute.On("GetApprovalIDForEntity", models.ApprovalEntityRoute, route.ID).Return(nil, errors.New("not found"))
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Deploy_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	teamID := uuid.New()
	route := &models.Route{ID: routeID, Name: "deploy-route", TeamID: teamID}
	mockRoute.On("GetByID", routeID).Return(route, nil)
	mockRoute.On("Deploy", routeID, user.ID).Return(route, nil)
	mockRoute.On("GetDomainName", mock.AnythingOfType("uuid.UUID")).Return("test-domain", nil)
	mockRoute.On("GetApprovalIDForEntity", models.ApprovalEntityRoute, route.ID).Return(nil, errors.New("not found"))
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/deploy", func(c *gin.Context) {
		c.Set("user", user)
		h.Deploy(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String()+"/deploy", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_GetYAML_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	mockRoute.On("GenerateYAML", routeID).Return("apiVersion: v1\nkind: HTTPRoute", nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/yaml", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.GetYAML(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "HTTPRoute")
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_GetYAMLs_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	yamls := &services.RouteYAMLs{HTTPRouteYAML: "apiVersion: v1"}
	mockRoute.On("GenerateYAMLs", routeID).Return(yamls, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/yamls", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.GetYAMLs(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_GetYAMLs_InvalidID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/bad-id/yamls", nil)
	c.Params = gin.Params{{Key: "routeId", Value: "bad-id"}}

	h.GetYAMLs(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_PreviewCreate_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	domainID := uuid.New()
	teamID := uuid.New()
	result := &services.PreviewCreateResult{ProposedYAML: "apiVersion: v1"}
	mockRoute.On("PreviewCreate", domainID, mock.AnythingOfType("*services.CreateRouteInput")).Return(result, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":   "preview-route",
		"teamId": teamID.String(),
		"config": map[string]interface{}{},
	})

	router := gin.New()
	router.POST("/domains/:domainId/routes/preview", func(c *gin.Context) {
		h.PreviewCreate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/domains/"+domainID.String()+"/routes/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Create_InvalidProjectID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/domains/"+uuid.New().String()+"/routes", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_Create_InvalidDomainID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+uuid.New().String()+"/domains/bad-id/routes", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_Create_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	mockRoute.On("Create", domainID, mock.AnythingOfType("*services.CreateRouteInput"), user.ID).Return(nil, errors.New("duplicate route"))

	body, _ := json.Marshal(map[string]interface{}{
		"name":   "new-route",
		"teamId": uuid.New().String(),
		"config": map[string]interface{}{},
	})

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/routes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Update_InvalidRouteID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+uuid.New().String()+"/domains/"+uuid.New().String()+"/routes/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_Update_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	mockRoute.On("Update", routeID, mock.AnythingOfType("*services.UpdateRouteInput"), user.ID).Return(nil, errors.New("validation error"))

	body, _ := json.Marshal(map[string]interface{}{
		"config": map[string]interface{}{},
	})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Delete_InvalidRouteID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+uuid.New().String()+"/domains/"+uuid.New().String()+"/routes/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_Delete_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	mockRoute.On("Delete", routeID, user.ID).Return(nil, errors.New("route is deployed"))

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Deploy_InvalidProjectID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/deploy", func(c *gin.Context) {
		c.Set("user", user)
		h.Deploy(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/domains/"+uuid.New().String()+"/routes/"+uuid.New().String()+"/deploy", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_Deploy_InvalidRouteID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/deploy", func(c *gin.Context) {
		c.Set("user", user)
		h.Deploy(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+uuid.New().String()+"/domains/"+uuid.New().String()+"/routes/bad-id/deploy", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_Deploy_RouteNotFound(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	mockRoute.On("GetByID", routeID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/deploy", func(c *gin.Context) {
		c.Set("user", user)
		h.Deploy(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String()+"/deploy", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Deploy_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	teamID := uuid.New()
	route := &models.Route{ID: routeID, Name: "deploy-route", TeamID: teamID}
	mockRoute.On("GetByID", routeID).Return(route, nil)
	mockRoute.On("Deploy", routeID, user.ID).Return(nil, errors.New("not approved"))

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/deploy", func(c *gin.Context) {
		c.Set("user", user)
		h.Deploy(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String()+"/deploy", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_GetYAML_InvalidID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/bad-id/yaml", nil)
	c.Params = gin.Params{{Key: "routeId", Value: "bad-id"}}

	h.GetYAML(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_GetYAML_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	mockRoute.On("GenerateYAML", routeID).Return("", errors.New("route not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/yaml", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.GetYAML(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_GetYAMLs_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	mockRoute.On("GenerateYAMLs", routeID).Return(nil, errors.New("route not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/yamls", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.GetYAMLs(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_PreviewCreate_InvalidDomainID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	router := gin.New()
	router.POST("/domains/:domainId/routes/preview", func(c *gin.Context) {
		h.PreviewCreate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/domains/bad-id/routes/preview", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_PreviewUpdate_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	result := &services.PreviewUpdateResult{CurrentYAML: "old yaml", ProposedYAML: "new yaml"}
	mockRoute.On("PreviewUpdate", routeID, mock.AnythingOfType("*services.UpdateRouteInput")).Return(result, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"config": map[string]interface{}{},
	})

	router := gin.New()
	router.POST("/routes/:routeId/preview-update", func(c *gin.Context) {
		h.PreviewUpdate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/routes/"+routeID.String()+"/preview-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_PreviewUpdate_InvalidRouteID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	router := gin.New()
	router.POST("/routes/:routeId/preview-update", func(c *gin.Context) {
		h.PreviewUpdate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/routes/bad-id/preview-update", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_PreviewDelete_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	result := &services.PreviewDeleteResult{CurrentYAML: "to-delete yaml"}
	mockRoute.On("PreviewDelete", routeID).Return(result, nil)

	router := gin.New()
	router.POST("/routes/:routeId/preview-delete", func(c *gin.Context) {
		h.PreviewDelete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/routes/"+routeID.String()+"/preview-delete", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_PreviewDelete_InvalidRouteID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	router := gin.New()
	router.POST("/routes/:routeId/preview-delete", func(c *gin.Context) {
		h.PreviewDelete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/routes/bad-id/preview-delete", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_GetEffectiveIPs_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	entries := []services.EffectiveIPEntry{
		{CIDR: "10.0.0.0/8", ClientName: "client1"},
	}
	mockRoute.On("GetEffectiveIPAllowlist", routeID).Return(entries, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/effective-ips", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.GetEffectiveIPs(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_GetEffectiveIPs_InvalidID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/bad-id/effective-ips", nil)
	c.Params = gin.Params{{Key: "routeId", Value: "bad-id"}}

	h.GetEffectiveIPs(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_GetEffectiveIPs_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	mockRoute.On("GetEffectiveIPAllowlist", routeID).Return([]services.EffectiveIPEntry{}, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/effective-ips", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.GetEffectiveIPs(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_PreviewCreate_BadBody(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	domainID := uuid.New()

	body := []byte("{invalid")

	router := gin.New()
	router.POST("/domains/:domainId/routes/preview", func(c *gin.Context) {
		h.PreviewCreate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/domains/"+domainID.String()+"/routes/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_PreviewCreate_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	domainID := uuid.New()
	teamID := uuid.New()
	mockRoute.On("PreviewCreate", domainID, mock.AnythingOfType("*services.CreateRouteInput")).Return(nil, errors.New("validation error"))

	body, _ := json.Marshal(map[string]interface{}{
		"name":   "preview-route",
		"teamId": teamID.String(),
		"config": map[string]interface{}{},
	})

	router := gin.New()
	router.POST("/domains/:domainId/routes/preview", func(c *gin.Context) {
		h.PreviewCreate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/domains/"+domainID.String()+"/routes/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_PreviewUpdate_BadBody(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()

	body := []byte("{invalid")

	router := gin.New()
	router.POST("/routes/:routeId/preview-update", func(c *gin.Context) {
		h.PreviewUpdate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/routes/"+routeID.String()+"/preview-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_PreviewUpdate_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	mockRoute.On("PreviewUpdate", routeID, mock.AnythingOfType("*services.UpdateRouteInput")).Return(nil, errors.New("route not found"))

	body, _ := json.Marshal(map[string]interface{}{
		"config": map[string]interface{}{},
	})

	router := gin.New()
	router.POST("/routes/:routeId/preview-update", func(c *gin.Context) {
		h.PreviewUpdate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/routes/"+routeID.String()+"/preview-update", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_PreviewDelete_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	routeID := uuid.New()
	mockRoute.On("PreviewDelete", routeID).Return(nil, errors.New("route not found"))

	router := gin.New()
	router.POST("/routes/:routeId/preview-delete", func(c *gin.Context) {
		h.PreviewDelete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/routes/"+routeID.String()+"/preview-delete", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_Create_BadBody(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	body := []byte("{invalid")

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/routes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_Update_BadBody(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	pc := routePermChecker()
	h := handlers.NewRouteHandler(mockRoute, mockAudit, pc)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()

	body := []byte("{invalid")

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/routes/:routeId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_List_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	domainID := uuid.New()
	mockRoute.On("ListByDomainID", domainID, 1, 20, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).Return([]models.Route{}, int64(0), errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/"+domainID.String()+"/routes", nil)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_ListByProject_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	projectID := uuid.New()
	routes := []models.Route{
		{ID: uuid.New(), Name: "route-a"},
		{ID: uuid.New(), Name: "route-b"},
	}
	expectedFilters := repository.RouteListFilters{
		BackendService:   "payments-api",
		BackendNamespace: "payments",
		IncludeMirrors:   true,
		Statuses:         []string{"active", "pending_create"},
	}
	mockRoute.On("ListByProjectID", projectID, 1, 50, expectedFilters).
		Return(routes, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	c.Request, _ = http.NewRequest("GET",
		"/projects/"+projectID.String()+"/routes?backend_service=payments-api&backend_namespace=payments&include_mirrors=true&status=active,pending_create",
		nil)

	h.ListByProject(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp["data"], 2)
	pagination := resp["pagination"].(map[string]any)
	assert.Equal(t, float64(2), pagination["total"])
	mockRoute.AssertExpectations(t)
}

func TestRouteHandler_ListByProject_InvalidProjectID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "projectId", Value: "not-a-uuid"}}
	c.Request, _ = http.NewRequest("GET", "/projects/not-a-uuid/routes", nil)

	h.ListByProject(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_ListByProject_ServiceError(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	projectID := uuid.New()
	mockRoute.On("ListByProjectID", projectID, 1, 50, mock.Anything).
		Return(nil, int64(0), errors.New("db down"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/routes", nil)

	h.ListByProject(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRouteHandler_ListByProject_PartialBackendFilter(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	projectID := uuid.New()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	c.Request, _ = http.NewRequest("GET",
		"/projects/"+projectID.String()+"/routes?backend_service=foo", nil)
	h.ListByProject(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	c2.Request, _ = http.NewRequest("GET",
		"/projects/"+projectID.String()+"/routes?backend_namespace=foo", nil)
	h.ListByProject(c2)
	assert.Equal(t, http.StatusBadRequest, w2.Code)

	mockRoute.AssertNotCalled(t, "ListByProjectID", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRouteHandler_ListByProject_InvalidTeamID(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, nil)

	projectID := uuid.New()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	c.Request, _ = http.NewRequest("GET",
		"/projects/"+projectID.String()+"/routes?team_id=not-a-uuid", nil)
	h.ListByProject(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

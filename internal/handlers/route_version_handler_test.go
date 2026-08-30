package handlers_test

import (
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

func TestRouteVersionHandler_List_Success(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	routeID := uuid.New()
	versions := []models.RouteVersion{
		{ID: uuid.New(), RouteID: routeID, Version: 1},
		{ID: uuid.New(), RouteID: routeID, Version: 2},
	}
	mockRV.On("ListVersions", routeID, 1, 20).Return(versions, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/versions", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockRV.AssertExpectations(t)
}

func TestRouteVersionHandler_List_InvalidRouteID(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/bad-id/versions", nil)
	c.Params = gin.Params{{Key: "routeId", Value: "bad-id"}}

	h.List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteVersionHandler_Get_Success(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	routeID := uuid.New()
	rv := &models.RouteVersion{ID: uuid.New(), RouteID: routeID, Version: 1}
	mockRV.On("GetVersion", routeID, 1).Return(rv, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/versions/1", nil)
	c.Params = gin.Params{
		{Key: "routeId", Value: routeID.String()},
		{Key: "version", Value: "1"},
	}

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRV.AssertExpectations(t)
}

func TestRouteVersionHandler_Get_InvalidVersion(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	routeID := uuid.New()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/versions/abc", nil)
	c.Params = gin.Params{
		{Key: "routeId", Value: routeID.String()},
		{Key: "version", Value: "abc"},
	}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteVersionHandler_Get_InvalidRouteID(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/bad-id/versions/1", nil)
	c.Params = gin.Params{
		{Key: "routeId", Value: "bad-id"},
		{Key: "version", Value: "1"},
	}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteVersionHandler_Get_ServiceError(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	routeID := uuid.New()
	mockRV.On("GetVersion", routeID, 99).Return(nil, errors.New("not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/versions/99", nil)
	c.Params = gin.Params{
		{Key: "routeId", Value: routeID.String()},
		{Key: "version", Value: "99"},
	}

	h.Get(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockRV.AssertExpectations(t)
}

func TestRouteVersionHandler_List_ServiceError(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	routeID := uuid.New()
	mockRV.On("ListVersions", routeID, 1, 20).Return([]models.RouteVersion{}, int64(0), errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/versions", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockRV.AssertExpectations(t)
}

func TestRouteVersionHandler_Rollback_Success(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	route := &models.Route{ID: routeID, Name: "test-route"}
	mockRV.On("Rollback", routeID, 1, user.ID).Return(route, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/routes/:routeId/rollback/:version", func(c *gin.Context) {
		c.Set("user", user)
		h.Rollback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/routes/"+routeID.String()+"/rollback/1", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockRV.AssertExpectations(t)
}

func TestRouteVersionHandler_Rollback_InvalidProjectID(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/routes/:routeId/rollback/:version", func(c *gin.Context) {
		c.Set("user", user)
		h.Rollback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/routes/"+uuid.New().String()+"/rollback/1", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteVersionHandler_Rollback_InvalidRouteID(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/routes/:routeId/rollback/:version", func(c *gin.Context) {
		c.Set("user", user)
		h.Rollback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+uuid.New().String()+"/routes/bad-id/rollback/1", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteVersionHandler_Rollback_InvalidVersion(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/routes/:routeId/rollback/:version", func(c *gin.Context) {
		c.Set("user", user)
		h.Rollback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+uuid.New().String()+"/routes/"+uuid.New().String()+"/rollback/abc", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteVersionHandler_Rollback_NotFoundError(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	user := testUser()
	routeID := uuid.New()
	mockRV.On("Rollback", routeID, 99, user.ID).Return(nil, errors.New("version not found"))

	router := gin.New()
	router.POST("/projects/:projectId/routes/:routeId/rollback/:version", func(c *gin.Context) {
		c.Set("user", user)
		h.Rollback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+uuid.New().String()+"/routes/"+routeID.String()+"/rollback/99", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockRV.AssertExpectations(t)
}

func TestRouteVersionHandler_Rollback_InternalError(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	user := testUser()
	routeID := uuid.New()
	mockRV.On("Rollback", routeID, 1, user.ID).Return(nil, errors.New("failed to unmarshal version"))

	router := gin.New()
	router.POST("/projects/:projectId/routes/:routeId/rollback/:version", func(c *gin.Context) {
		c.Set("user", user)
		h.Rollback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+uuid.New().String()+"/routes/"+routeID.String()+"/rollback/1", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockRV.AssertExpectations(t)
}

func TestRouteVersionHandler_Rollback_BadRequestError(t *testing.T) {
	mockRV := new(mocks.MockRouteVersionService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteVersionHandler(mockRV, mockAudit)

	user := testUser()
	routeID := uuid.New()
	mockRV.On("Rollback", routeID, 1, user.ID).Return(nil, errors.New("same version"))

	router := gin.New()
	router.POST("/projects/:projectId/routes/:routeId/rollback/:version", func(c *gin.Context) {
		c.Set("user", user)
		h.Rollback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+uuid.New().String()+"/routes/"+routeID.String()+"/rollback/1", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRV.AssertExpectations(t)
}

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMetricsHandler_TestConnection_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := &mocks.MockMetricsService{}
	h := NewMetricsHandler(mockSvc)

	projectID := uuid.New()
	mockSvc.On("TestConnection", mock.Anything, projectID).Return(
		&services.TestConnectionResult{OK: true}, nil,
	)

	router := gin.New()
	router.POST("/projects/:projectId/metrics/test-connection", h.TestConnection)

	req := httptest.NewRequest(http.MethodPost, "/projects/"+projectID.String()+"/metrics/test-connection", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body services.TestConnectionResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.True(t, body.OK)
}

func TestMetricsHandler_GetRouteMetrics_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := &mocks.MockMetricsService{}
	h := NewMetricsHandler(mockSvc)

	projectID := uuid.New()
	routeID := uuid.New()
	mockSvc.On("GetRouteMetrics", mock.Anything, projectID, routeID, "1h").Return(
		&services.RouteMetricsResult{TotalRequests: 42}, nil,
	)

	router := gin.New()
	router.GET("/projects/:projectId/routes/:routeId/metrics", h.GetRouteMetrics)

	req := httptest.NewRequest(http.MethodGet,
		"/projects/"+projectID.String()+"/routes/"+routeID.String()+"/metrics?range=1h", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body services.RouteMetricsResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(42), body.TotalRequests)
}

func TestMetricsHandler_GetRouteMetrics_ServiceError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := &mocks.MockMetricsService{}
	h := NewMetricsHandler(mockSvc)

	projectID := uuid.New()
	routeID := uuid.New()
	mockSvc.On("GetRouteMetrics", mock.Anything, projectID, routeID, "1h").Return(
		nil, errors.New("prom http 401: unauthorized"),
	)

	router := gin.New()
	router.GET("/projects/:projectId/routes/:routeId/metrics", h.GetRouteMetrics)

	req := httptest.NewRequest(http.MethodGet,
		"/projects/"+projectID.String()+"/routes/"+routeID.String()+"/metrics?range=1h", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "metrics_unavailable", body["error"])
	assert.Contains(t, body["message"], "401")
}

func TestMetricsHandler_GetRouteMetrics_InvalidRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := &mocks.MockMetricsService{}
	h := NewMetricsHandler(mockSvc)

	projectID := uuid.New()
	routeID := uuid.New()
	mockSvc.On("GetRouteMetrics", mock.Anything, projectID, routeID, "bogus").Return(
		nil, errors.New(`invalid range: "bogus"`),
	)

	router := gin.New()
	router.GET("/projects/:projectId/routes/:routeId/metrics", h.GetRouteMetrics)

	req := httptest.NewRequest(http.MethodGet,
		"/projects/"+projectID.String()+"/routes/"+routeID.String()+"/metrics?range=bogus", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMetricsHandler_GetDomainMetrics_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := &mocks.MockMetricsService{}
	h := NewMetricsHandler(mockSvc)

	projectID := uuid.New()
	domainID := uuid.New()
	mockSvc.On("GetDomainMetrics", mock.Anything, projectID, domainID, "1h").Return(
		&services.DomainMetricsResult{TotalRequests: 999}, nil,
	)

	router := gin.New()
	router.GET("/projects/:projectId/domains/:domainId/metrics", h.GetDomainMetrics)

	req := httptest.NewRequest(http.MethodGet,
		"/projects/"+projectID.String()+"/domains/"+domainID.String()+"/metrics?range=1h", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

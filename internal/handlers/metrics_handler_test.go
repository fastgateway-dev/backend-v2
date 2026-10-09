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

func TestMetricsHandler_GetRouteMetrics_L4Route_Is400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := &mocks.MockMetricsService{}
	h := NewMetricsHandler(mockSvc)

	projectID := uuid.New()
	routeID := uuid.New()
	mockSvc.On("GetRouteMetrics", mock.Anything, projectID, routeID, "1h").Return(
		nil, services.ErrMetricsNotAvailableForL4,
	)

	router := gin.New()
	router.GET("/projects/:projectId/routes/:routeId/metrics", h.GetRouteMetrics)

	req := httptest.NewRequest(http.MethodGet,
		"/projects/"+projectID.String()+"/routes/"+routeID.String()+"/metrics?range=1h", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "metrics_unavailable", body["error"])
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

func TestMetricsHandler_GetStreamMetrics_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := &mocks.MockMetricsService{}
	h := NewMetricsHandler(mockSvc)

	projectID, streamID := uuid.New(), uuid.New()
	mockSvc.On("StreamL4Metrics", mock.Anything, projectID.String(), streamID.String()).Return(
		services.L4Metrics{
			StreamID:          streamID,
			ActiveConnections: 7,
			BytesOut:          4096,
			Listeners:         []services.L4ListenerMetrics{{Protocol: "tcp", Port: 5432, ActiveConnections: 7, BytesOut: 4096}},
		}, nil,
	)

	router := gin.New()
	router.GET("/projects/:projectId/streams/:streamId/metrics", h.GetStreamMetrics)

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/streams/"+streamID.String()+"/metrics", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(7), body["activeConnections"])
	assert.Equal(t, float64(4096), body["bytesOut"])
	assert.Contains(t, body, "connectionRate")
	assert.Contains(t, body, "bytesIn")
	assert.NotContains(t, body, "latency")
	listeners, ok := body["listeners"].([]any)
	require.True(t, ok)
	assert.Len(t, listeners, 1)
}

func TestMetricsHandler_GetStreamMetrics_InvalidIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewMetricsHandler(&mocks.MockMetricsService{})
	router := gin.New()
	router.GET("/projects/:projectId/streams/:streamId/metrics", h.GetStreamMetrics)

	for _, path := range []string{
		"/projects/nope/streams/" + uuid.NewString() + "/metrics",
		"/projects/" + uuid.NewString() + "/streams/nope/metrics",
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}

func TestMetricsHandler_GetStreamMetrics_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := &mocks.MockMetricsService{}
	h := NewMetricsHandler(mockSvc)
	projectID, streamID := uuid.New(), uuid.New()
	mockSvc.On("StreamL4Metrics", mock.Anything, projectID.String(), streamID.String()).Return(
		services.L4Metrics{}, services.ErrStreamNotFound,
	)

	router := gin.New()
	router.GET("/projects/:projectId/streams/:streamId/metrics", h.GetStreamMetrics)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/projects/"+projectID.String()+"/streams/"+streamID.String()+"/metrics", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

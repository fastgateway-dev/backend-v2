package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func setupVersionRouter(svc *mocks.MockProjectVersionService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handlers.NewProjectVersionHandler(svc)
	r.GET("/projects/:projectId/versions", h.Get)
	r.POST("/projects/:projectId/versions/refresh", h.Refresh)
	return r
}

func TestProjectVersionHandler_Get_OK(t *testing.T) {
	pid := uuid.New()
	svc := new(mocks.MockProjectVersionService)
	svc.On("Get", mock.Anything, pid, false).Return(&services.VersionInfo{
		Status:         services.VersionStatusSupported,
		EnvoyGateway:   services.ProbeResult{Version: "1.7.0", Detected: true},
		GatewayAPI:     services.ProbeResult{Version: "1.4.1", Detected: true},
		SupportedPairs: services.SupportedVersionPairs,
		CheckedAt:      time.Now(),
		CacheExpiresAt: time.Now().Add(5 * time.Minute),
	}, nil)
	r := setupVersionRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/projects/"+pid.String()+"/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp services.VersionInfo
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, services.VersionStatusSupported, resp.Status)
	assert.Equal(t, "1.7.0", resp.EnvoyGateway.Version)
}

func TestProjectVersionHandler_Get_InvalidUUID(t *testing.T) {
	svc := new(mocks.MockProjectVersionService)
	r := setupVersionRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/projects/not-a-uuid/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestProjectVersionHandler_Refresh_InvalidatesAndReturnsFresh(t *testing.T) {
	pid := uuid.New()
	svc := new(mocks.MockProjectVersionService)
	svc.On("Invalidate", pid).Return().Once()
	svc.On("Get", mock.Anything, pid, true).Return(&services.VersionInfo{
		Status: services.VersionStatusUntested,
	}, nil).Once()
	r := setupVersionRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/projects/"+pid.String()+"/versions/refresh", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	svc.AssertExpectations(t)
}

func TestProjectVersionHandler_Get_NeverReturns500ForDetectionFailure(t *testing.T) {
	pid := uuid.New()
	svc := new(mocks.MockProjectVersionService)
	errMsg := "forbidden: cannot list deployments"
	svc.On("Get", mock.Anything, pid, false).Return(&services.VersionInfo{
		Status:       services.VersionStatusUnknown,
		EnvoyGateway: services.ProbeResult{Detected: false, Error: &errMsg},
		GatewayAPI:   services.ProbeResult{Detected: false, Error: &errMsg},
	}, nil)
	r := setupVersionRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/projects/"+pid.String()+"/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

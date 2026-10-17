package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// streamVersionsRouter mounts the version endpoints under the stream the way
// cmd/server/main.go does: each RouteVersionHandler method behind
// RouteHandler.ForStreamRoute.
func streamVersionsRouter(rh *handlers.RouteHandler, vh *handlers.RouteVersionHandler, user *models.User) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user", user) })
	g := router.Group("/projects/:projectId/streams/:streamId/routes")
	g.GET("/:routeId/versions", rh.ForStreamRoute(vh.List))
	g.GET("/:routeId/versions/:version", rh.ForStreamRoute(vh.Get))
	g.POST("/:routeId/versions/:version/rollback", rh.ForStreamRoute(vh.Rollback))
	return router
}

func TestRouteHandler_StreamVersions_DelegateForARouteOfTheStream(t *testing.T) {
	mockRoute, mockRV, mockAudit := new(mocks.MockRouteService), new(mocks.MockRouteVersionService), new(mocks.MockAuditService)
	rh := handlers.NewRouteHandler(mockRoute, mockAudit, routePermChecker())
	vh := handlers.NewRouteVersionHandler(mockRV, mockAudit)
	user := testUser()
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(&models.Route{ID: routeID, StreamID: &streamID}, nil)
	mockRV.On("ListVersions", routeID, 1, 20).Return([]models.RouteVersion{{ID: uuid.New(), RouteID: routeID, Version: 1}}, int64(1), nil)
	mockRV.On("GetVersion", routeID, 1).Return(&models.RouteVersion{ID: uuid.New(), RouteID: routeID, Version: 1}, nil)
	mockRV.On("Rollback", routeID, 1, user.ID).Return(&models.Route{ID: routeID, Name: "pg"}, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, "rollback", "route", mock.Anything, "pg", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	router := streamVersionsRouter(rh, vh, user)
	base := fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID)

	w := doJSON(router, "GET", base+"/versions", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var list map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	assert.Len(t, list["data"], 1)

	assert.Equal(t, http.StatusOK, doJSON(router, "GET", base+"/versions/1", nil).Code)
	assert.Equal(t, http.StatusOK, doJSON(router, "POST", base+"/versions/1/rollback", nil).Code)
	mockRV.AssertExpectations(t)
}

func TestRouteHandler_StreamVersions_RouteOfOtherStreamIsNotFound(t *testing.T) {
	mockRoute, mockRV := new(mocks.MockRouteService), new(mocks.MockRouteVersionService)
	rh := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	vh := handlers.NewRouteVersionHandler(mockRV, new(mocks.MockAuditService))
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(nil, services.ErrRouteNotFound)
	router := streamVersionsRouter(rh, vh, testUser())
	base := fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID)

	assert.Equal(t, http.StatusNotFound, doJSON(router, "GET", base+"/versions", nil).Code)
	assert.Equal(t, http.StatusNotFound, doJSON(router, "GET", base+"/versions/1", nil).Code)
	assert.Equal(t, http.StatusNotFound, doJSON(router, "POST", base+"/versions/1/rollback", nil).Code)
	mockRV.AssertNotCalled(t, "ListVersions", mock.Anything, mock.Anything, mock.Anything)
	mockRV.AssertNotCalled(t, "GetVersion", mock.Anything, mock.Anything)
	mockRV.AssertNotCalled(t, "Rollback", mock.Anything, mock.Anything, mock.Anything)
}

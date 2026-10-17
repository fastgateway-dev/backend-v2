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

func routeYAMLsRouter(h *handlers.RouteHandler) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user", testUser()) })
	router.GET("/projects/:projectId/streams/:streamId/routes/:routeId/yamls", h.GetYAMLsForStream)
	return router
}

func TestRouteHandler_GetYAMLsForStream_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(&models.Route{ID: routeID, StreamID: &streamID}, nil)
	mockRoute.On("GenerateYAMLs", routeID).Return(&services.RouteYAMLs{
		HTTPRouteYAML: "kind: TCPRoute\n", BackendTrafficPolicyYAML: "kind: BackendTrafficPolicy\n",
	}, nil)

	w := doJSON(routeYAMLsRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/routes/%s/yamls", projectID, streamID, routeID), nil)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "kind: TCPRoute\n", body["httpRouteYaml"])
	assert.Equal(t, "kind: BackendTrafficPolicy\n", body["backendTrafficPolicyYaml"])
}

func TestRouteHandler_GetYAMLsForStream_RouteOfOtherStreamIsNotFound(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(nil, services.ErrRouteNotFound)

	w := doJSON(routeYAMLsRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/routes/%s/yamls", projectID, streamID, routeID), nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockRoute.AssertNotCalled(t, "GenerateYAMLs", mock.Anything)
}

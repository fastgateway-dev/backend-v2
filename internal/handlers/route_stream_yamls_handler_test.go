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

func TestRouteHandler_GetStreamYAMLs(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  *services.StreamYAMLs
		err  error
		code int
	}{
		{"ok", &services.StreamYAMLs{GatewayYAML: "kind: Gateway\n", Routes: []services.StreamRouteYAMLs{}}, nil, http.StatusOK},
		{"other project's stream", nil, services.ErrStreamNotFound, http.StatusNotFound},
		{"read failure", nil, fmt.Errorf("failed to list active routes for stream: %w", assert.AnError), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockRoute := new(mocks.MockRouteService)
			h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
			projectID, streamID := uuid.New(), uuid.New()
			mockRoute.On("GenerateStreamYAMLs", projectID, streamID).Return(tc.res, tc.err)
			router := gin.New()
			router.GET("/projects/:projectId/streams/:streamId/yamls", h.GetStreamYAMLs)

			w := doJSON(router, "GET", fmt.Sprintf("/projects/%s/streams/%s/yamls", projectID, streamID), nil)

			require.Equal(t, tc.code, w.Code, w.Body.String())
			if tc.code == http.StatusOK {
				var body map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				assert.Equal(t, "kind: Gateway\n", body["gatewayYaml"])
				assert.Equal(t, []any{}, body["routes"])
			}
		})
	}
}

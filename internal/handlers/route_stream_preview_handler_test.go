package handlers_test

import (
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
)

// streamPreviewRouter mounts the three stream-scoped preview endpoints the way
// cmd/server/main.go does.
func streamPreviewRouter(h *handlers.RouteHandler) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user", testUser()) })
	g := router.Group("/projects/:projectId/streams/:streamId/routes")
	g.POST("/preview", h.PreviewCreateForStream)
	g.POST("/:routeId/preview", h.PreviewUpdateForStream)
	g.GET("/:routeId/preview-delete", h.PreviewDeleteForStream)
	return router
}

func l4PreviewBody() map[string]any {
	return map[string]any{"name": "pg", "teamId": uuid.New().String(), "protocol": "tcp", "config": map[string]any{"listenerPort": 9050}}
}

func TestRouteHandler_PreviewCreateForStream_StatusMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"ok", nil, http.StatusOK},
		{"collision", fmt.Errorf("%w: TCP/9050", services.ErrPortCollision), http.StatusConflict},
		{"reserved", fmt.Errorf("%w: 19000", services.ErrReservedPort), http.StatusBadRequest},
		{"out of range", fmt.Errorf("%w: 9050 not in 10000-11000", services.ErrPortOutOfRange), http.StatusBadRequest},
		{"stream of another project", services.ErrStreamNotFound, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockRoute := new(mocks.MockRouteService)
			h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
			projectID, streamID := uuid.New(), uuid.New()
			var res *services.PreviewCreateResult
			if tc.err == nil {
				res = &services.PreviewCreateResult{ProposedYAML: "kind: TCPRoute\n"}
			}
			mockRoute.On("PreviewCreateForStream", projectID, streamID, mock.AnythingOfType("*services.CreateRouteInput")).Return(res, tc.err)

			w := doJSON(streamPreviewRouter(h), "POST", fmt.Sprintf("/projects/%s/streams/%s/routes/preview", projectID, streamID), l4PreviewBody())

			assert.Equal(t, tc.code, w.Code, w.Body.String())
		})
	}
}

func TestRouteHandler_PreviewCreateForStream_BadBodyIs400(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	w := doJSON(streamPreviewRouter(h), "POST", fmt.Sprintf("/projects/%s/streams/%s/routes/preview", uuid.New(), uuid.New()), map[string]any{"name": ""})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockRoute.AssertNotCalled(t, "PreviewCreateForStream", mock.Anything, mock.Anything, mock.Anything)
}

func TestRouteHandler_PreviewUpdateForStream(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"ok", nil, http.StatusOK},
		{"collision on the new port", fmt.Errorf("%w: TCP/6432", services.ErrPortCollision), http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockRoute := new(mocks.MockRouteService)
			h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
			projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
			mockRoute.On("GetForStream", projectID, streamID, routeID).Return(&models.Route{ID: routeID, StreamID: &streamID}, nil)
			var res *services.PreviewUpdateResult
			if tc.err == nil {
				res = &services.PreviewUpdateResult{CurrentYAML: "a", ProposedYAML: "b"}
			}
			mockRoute.On("PreviewUpdateForStream", routeID, mock.AnythingOfType("*services.UpdateRouteInput")).Return(res, tc.err)

			w := doJSON(streamPreviewRouter(h), "POST", fmt.Sprintf("/projects/%s/streams/%s/routes/%s/preview", projectID, streamID, routeID),
				map[string]any{"config": map[string]any{"listenerPort": 6432}})

			assert.Equal(t, tc.code, w.Code, w.Body.String())
		})
	}
}

// A route of another stream is 404 on every stream-scoped preview, before
// any preview runs.
func TestRouteHandler_StreamPreviews_RouteOfOtherStreamIsNotFound(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(nil, services.ErrRouteNotFound)
	router := streamPreviewRouter(h)
	base := fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID)

	assert.Equal(t, http.StatusNotFound, doJSON(router, "POST", base+"/preview", map[string]any{"config": map[string]any{}}).Code)
	assert.Equal(t, http.StatusNotFound, doJSON(router, "GET", base+"/preview-delete", nil).Code)
	mockRoute.AssertNotCalled(t, "PreviewUpdateForStream", mock.Anything, mock.Anything)
	mockRoute.AssertNotCalled(t, "PreviewDelete", mock.Anything)
}

func TestRouteHandler_PreviewDeleteForStream_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	mockRoute.On("GetForStream", projectID, streamID, routeID).Return(&models.Route{ID: routeID, StreamID: &streamID}, nil)
	mockRoute.On("PreviewDelete", routeID).Return(&services.PreviewDeleteResult{CurrentYAML: "kind: TCPRoute\n"}, nil)

	w := doJSON(streamPreviewRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/routes/%s/preview-delete", projectID, streamID, routeID), nil)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

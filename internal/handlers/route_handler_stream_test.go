package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// streamRouteRouter mounts the stream-scoped route handlers the way
// cmd/server/main.go does.
func streamRouteRouter(h *handlers.RouteHandler, user *models.User) *gin.Engine {
	router := gin.New()
	auth := func(c *gin.Context) { c.Set("user", user) }
	g := router.Group("/projects/:projectId/streams/:streamId/routes", auth)
	g.GET("", h.ListByStream)
	g.POST("", h.CreateForStream)
	g.GET("/:routeId", h.GetForStream)
	g.PUT("/:routeId", h.UpdateForStream)
	g.DELETE("/:routeId", h.DeleteForStream)
	g.POST("/:routeId/deploy", h.DeployForStream)
	g.GET("/:routeId/yaml", h.GetYAMLForStream)
	return router
}

func doJSON(router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestRouteHandler_CreateForStream_Success(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewRouteHandler(mockRoute, mockAudit, routePermChecker())
	user := testUser()
	projectID, streamID, teamID := uuid.New(), uuid.New(), uuid.New()
	route := &models.Route{ID: uuid.New(), Name: "pg", StreamID: &streamID, Protocol: models.RouteProtocolTCP}

	var got *services.CreateRouteInput
	mockRoute.On("CreateForStream", projectID, streamID, mock.AnythingOfType("*services.CreateRouteInput"), user.ID).
		Run(func(args mock.Arguments) { got = args.Get(2).(*services.CreateRouteInput) }).Return(route, nil)
	mockRoute.On("GetApprovalIDForEntity", models.ApprovalEntityRoute, route.ID).Return(nil, errors.New("none"))
	mockAudit.On("LogAction", mock.Anything, mock.Anything, "create", "route", mock.Anything, "pg", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	w := doJSON(streamRouteRouter(h, user), "POST", fmt.Sprintf("/projects/%s/streams/%s/routes", projectID, streamID), map[string]any{
		"name": "pg", "teamId": teamID.String(), "protocol": "tcp",
		"config": map[string]any{"listenerPort": 5432},
	})

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NotNil(t, got)
	assert.Equal(t, models.RouteProtocolTCP, got.Protocol)
	assert.Equal(t, 5432, got.Config.ListenerPort)
	mockRoute.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestRouteHandler_CreateForStream_ErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"collision", fmt.Errorf("%w: TCP/5432", services.ErrPortCollision), http.StatusConflict},
		{"stream not found", services.ErrStreamNotFound, http.StatusNotFound},
		{"owner ambiguous", services.ErrRouteOwnerAmbiguous, http.StatusBadRequest},
		{"not l4 protocol", services.ErrRouteProtocolNotL4, http.StatusBadRequest},
		{"reserved port", fmt.Errorf("%w: 19000", services.ErrReservedPort), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockRoute := new(mocks.MockRouteService)
			h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
			user := testUser()
			projectID, streamID := uuid.New(), uuid.New()
			mockRoute.On("CreateForStream", projectID, streamID, mock.Anything, user.ID).Return(nil, tc.err)

			w := doJSON(streamRouteRouter(h, user), "POST", fmt.Sprintf("/projects/%s/streams/%s/routes", projectID, streamID),
				map[string]any{"name": "pg", "teamId": uuid.New().String(), "config": map[string]any{}})

			assert.Equal(t, tc.code, w.Code)
		})
	}
}

func TestRouteHandler_CreateForStream_BadIDsAndBody(t *testing.T) {
	h := handlers.NewRouteHandler(new(mocks.MockRouteService), new(mocks.MockAuditService), routePermChecker())
	router := streamRouteRouter(h, testUser())

	w := doJSON(router, "POST", fmt.Sprintf("/projects/%s/streams/not-a-uuid/routes", uuid.New()), map[string]any{})
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = doJSON(router, "POST", fmt.Sprintf("/projects/%s/streams/%s/routes", uuid.New(), uuid.New()), map[string]any{"name": ""})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRouteHandler_ListByStream(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID := uuid.New(), uuid.New()
	routes := []models.Route{{ID: uuid.New(), Name: "pg"}, {ID: uuid.New(), Name: "redis"}}
	mockRoute.On("ListByStreamID", projectID, streamID, 1, 20, (*uuid.UUID)(nil), "").Return(routes, int64(2), nil)

	w := doJSON(streamRouteRouter(h, testUser()), "GET", fmt.Sprintf("/projects/%s/streams/%s/routes", projectID, streamID), nil)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data       []models.Route `json:"data"`
		Pagination struct{ Total int64 }
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 2)
	assert.Equal(t, int64(2), resp.Pagination.Total)
}

func TestRouteHandler_ListByStream_StreamNotFound(t *testing.T) {
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewRouteHandler(mockRoute, new(mocks.MockAuditService), routePermChecker())
	projectID, streamID := uuid.New(), uuid.New()
	mockRoute.On("ListByStreamID", projectID, streamID, 1, 20, (*uuid.UUID)(nil), "").Return(nil, int64(0), services.ErrStreamNotFound)

	w := doJSON(streamRouteRouter(h, testUser()), "GET", fmt.Sprintf("/projects/%s/streams/%s/routes", projectID, streamID), nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRouteHandler_StreamRoute_ScopedOperations(t *testing.T) {
	projectID, streamID, routeID := uuid.New(), uuid.New(), uuid.New()
	base := fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID)
	route := &models.Route{ID: routeID, Name: "pg", StreamID: &streamID, Protocol: models.RouteProtocolTCP, TeamID: uuid.New()}

	newH := func() (*handlers.RouteHandler, *mocks.MockRouteService, *mocks.MockAuditService) {
		mr, ma := new(mocks.MockRouteService), new(mocks.MockAuditService)
		return handlers.NewRouteHandler(mr, ma, routePermChecker()), mr, ma
	}
	logged := func(ma *mocks.MockAuditService, action string) {
		ma.On("LogAction", mock.Anything, mock.Anything, action, "route", mock.Anything, "pg", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	}

	t.Run("update", func(t *testing.T) {
		h, mr, ma := newH()
		mr.On("GetForStream", projectID, streamID, routeID).Return(route, nil)
		mr.On("Update", routeID, mock.AnythingOfType("*services.UpdateRouteInput"), mock.Anything).Return(route, nil)
		mr.On("GetApprovalIDForEntity", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("none"))
		logged(ma, "update")
		w := doJSON(streamRouteRouter(h, testUser()), "PUT", base, map[string]any{"config": map[string]any{"listenerPort": 6432}})
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		mr.AssertExpectations(t)
	})

	t.Run("delete", func(t *testing.T) {
		h, mr, ma := newH()
		mr.On("GetForStream", projectID, streamID, routeID).Return(route, nil)
		mr.On("Delete", routeID, mock.Anything).Return(route, nil)
		mr.On("GetApprovalIDForEntity", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("none"))
		logged(ma, "request_delete")
		w := doJSON(streamRouteRouter(h, testUser()), "DELETE", base, nil)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		mr.AssertExpectations(t)
	})

	t.Run("deploy", func(t *testing.T) {
		h, mr, ma := newH()
		mr.On("GetForStream", projectID, streamID, routeID).Return(route, nil)
		mr.On("GetByID", routeID).Return(route, nil)
		mr.On("Deploy", routeID, mock.Anything).Return(route, nil)
		mr.On("GetApprovalIDForEntity", models.ApprovalEntityRoute, routeID).Return(nil, errors.New("none"))
		logged(ma, "deploy")
		w := doJSON(streamRouteRouter(h, testUser()), "POST", base+"/deploy", nil)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		mr.AssertExpectations(t)
	})

	t.Run("yaml", func(t *testing.T) {
		h, mr, _ := newH()
		mr.On("GetForStream", projectID, streamID, routeID).Return(route, nil)
		mr.On("GenerateYAML", routeID).Return("kind: TCPRoute", nil)
		w := doJSON(streamRouteRouter(h, testUser()), "GET", base+"/yaml", nil)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "TCPRoute")
	})

	t.Run("get", func(t *testing.T) {
		h, mr, _ := newH()
		mr.On("GetForStream", projectID, streamID, routeID).Return(route, nil)
		mr.On("GetByID", routeID).Return(route, nil)
		mr.On("GetSecurityPolicy", routeID).Return(nil, errors.New("none"))
		mr.On("GetBackendTrafficPolicy", routeID).Return(nil, errors.New("none"))
		mr.On("GetEnvoyExtensionPolicy", routeID).Return(nil, errors.New("none"))
		mr.On("GetWafPolicy", routeID).Return(nil, errors.New("none"))
		w := doJSON(streamRouteRouter(h, testUser()), "GET", base, nil)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})

	// A route that is not in this stream/project is 404 for every operation,
	// before the write is attempted.
	t.Run("route outside the stream is 404 and never written", func(t *testing.T) {
		h, mr, _ := newH()
		mr.On("GetForStream", projectID, streamID, routeID).Return(nil, services.ErrRouteNotFound)
		router := streamRouteRouter(h, testUser())
		for _, rq := range []struct{ method, path string }{
			{"GET", base}, {"PUT", base}, {"DELETE", base}, {"POST", base + "/deploy"}, {"GET", base + "/yaml"},
		} {
			w := doJSON(router, rq.method, rq.path, map[string]any{"config": map[string]any{}})
			assert.Equal(t, http.StatusNotFound, w.Code, "%s %s", rq.method, rq.path)
		}
		mr.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
		mr.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything)
		mr.AssertNotCalled(t, "Deploy", mock.Anything, mock.Anything)
	})
}

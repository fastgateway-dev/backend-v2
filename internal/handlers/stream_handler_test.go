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

// streamRouter wires the stream handler onto a gin router with the owner user
// pre-set in the context (owners pass CanManageDomains).
func streamRouter(h *handlers.StreamHandler, user *models.User) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user", user); c.Next() })
	g := r.Group("/projects/:projectId/streams")
	g.GET("", h.List)
	g.POST("", h.Create)
	g.GET("/:streamId", h.Get)
	g.PATCH("/:streamId", h.Update)
	g.DELETE("/:streamId", h.Delete)
	return r
}

// validStreamPorts is a request-body claim set the handler accepts.
var validStreamPorts = []map[string]any{{"transport": "TCP", "port": 5432}}

func newStreamHandler() (*handlers.StreamHandler, *mocks.MockStreamService, *mocks.MockAuditService) {
	svc := new(mocks.MockStreamService)
	audit := new(mocks.MockAuditService)
	audit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	return handlers.NewStreamHandler(svc, new(mocks.MockStreamViewService), audit, domainPermChecker()), svc, audit
}

func doStream(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestStreamHandler_Create_Success(t *testing.T) {
	h, svc, _ := newStreamHandler()
	user := testUser()
	projectID, tmplID := uuid.New(), uuid.New()
	created := &models.Stream{ID: uuid.New(), ProjectID: projectID, Name: "db", Namespace: "ns", GatewayTemplateID: tmplID, K8sGatewayName: "str-db"}
	svc.On("Create", projectID, services.CreateStreamInput{Name: "db", Namespace: "ns", GatewayTemplateID: tmplID,
		Ports: models.StreamPorts{{Transport: "TCP", Port: 5432}}}, user).Return(created, nil)

	w := doStream(streamRouter(h, user), "POST", "/projects/"+projectID.String()+"/streams",
		map[string]any{"name": "db", "namespace": "ns", "gatewayTemplateId": tmplID.String(), "ports": validStreamPorts})

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var got models.Stream
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "str-db", got.K8sGatewayName)
	svc.AssertExpectations(t)
}

func TestStreamHandler_Create_NonStreamTemplate_400(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID, tmplID := uuid.New(), uuid.New()
	svc.On("Create", projectID, mock.Anything, mock.Anything).Return(nil, services.ErrTemplateNotStreamEnabled)

	w := doStream(streamRouter(h, testUser()), "POST", "/projects/"+projectID.String()+"/streams",
		map[string]any{"name": "db", "namespace": "ns", "gatewayTemplateId": tmplID.String(), "ports": validStreamPorts})

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStreamHandler_Create_StreamsUnsupported_400(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID := uuid.New()
	svc.On("Create", projectID, mock.Anything, mock.Anything).Return(nil, services.ErrStreamsUnsupported)

	w := doStream(streamRouter(h, testUser()), "POST", "/projects/"+projectID.String()+"/streams",
		map[string]any{"name": "db", "namespace": "ns", "gatewayTemplateId": uuid.New().String(), "ports": validStreamPorts})

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStreamHandler_Create_TemplateNotFound_404(t *testing.T) {
	for _, sentinel := range []error{services.ErrStreamTemplateNotFound, services.ErrStreamTemplateWrongProject} {
		h, svc, _ := newStreamHandler()
		projectID := uuid.New()
		svc.On("Create", projectID, mock.Anything, mock.Anything).Return(nil, sentinel)

		w := doStream(streamRouter(h, testUser()), "POST", "/projects/"+projectID.String()+"/streams",
			map[string]any{"name": "db", "namespace": "ns", "gatewayTemplateId": uuid.New().String(), "ports": validStreamPorts})

		assert.Equal(t, http.StatusNotFound, w.Code, sentinel.Error())
	}
}

func TestStreamHandler_Create_DuplicateName_409(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID := uuid.New()
	svc.On("Create", projectID, mock.Anything, mock.Anything).Return(nil, services.ErrStreamNameTaken)

	w := doStream(streamRouter(h, testUser()), "POST", "/projects/"+projectID.String()+"/streams",
		map[string]any{"name": "db", "namespace": "ns", "gatewayTemplateId": uuid.New().String(), "ports": validStreamPorts})

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestStreamHandler_Create_NamespaceRejected_400(t *testing.T) {
	for _, sentinel := range []error{services.ErrStreamNamespaceNotRegistered, services.ErrStreamNamespaceNotDeployable} {
		h, svc, _ := newStreamHandler()
		projectID := uuid.New()
		svc.On("Create", projectID, mock.Anything, mock.Anything).Return(nil, sentinel)

		w := doStream(streamRouter(h, testUser()), "POST", "/projects/"+projectID.String()+"/streams",
			map[string]any{"name": "db", "namespace": "kube-system", "gatewayTemplateId": uuid.New().String(), "ports": validStreamPorts})

		assert.Equal(t, http.StatusBadRequest, w.Code, sentinel.Error())
	}
}

func TestStreamHandler_Update_DuplicateName_409(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID, streamID := uuid.New(), uuid.New()
	newName := "taken"
	svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Name: "db"}, nil)
	svc.On("Update", streamID, services.UpdateStreamInput{Name: &newName}).Return(nil, services.ErrStreamNameTaken)

	w := doStream(streamRouter(h, testUser()), "PATCH", "/projects/"+projectID.String()+"/streams/"+streamID.String(), map[string]any{"name": newName})

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestStreamHandler_Create_BadName_400(t *testing.T) {
	for _, name := range []string{"", "-", "---", "-db", "db-", "DB", "my_stream", "a--b", "has space", "dot.name",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		h, svc, _ := newStreamHandler()
		projectID := uuid.New()

		w := doStream(streamRouter(h, testUser()), "POST", "/projects/"+projectID.String()+"/streams",
			map[string]any{"name": name, "namespace": "ns", "gatewayTemplateId": uuid.New().String(), "ports": validStreamPorts})

		assert.Equal(t, http.StatusBadRequest, w.Code, "name %q", name)
		svc.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	}
}

func TestStreamHandler_Create_MissingTemplateID_400(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID := uuid.New()
	w := doStream(streamRouter(h, testUser()), "POST", "/projects/"+projectID.String()+"/streams",
		map[string]any{"name": "db", "namespace": "ns", "ports": validStreamPorts})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
}

func TestStreamHandler_Delete_WithRoutes_409(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID, streamID := uuid.New(), uuid.New()
	svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Name: "db"}, nil)
	svc.On("Delete", streamID).Return(services.ErrStreamHasRoutes)

	w := doStream(streamRouter(h, testUser()), "DELETE", "/projects/"+projectID.String()+"/streams/"+streamID.String(), nil)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestStreamHandler_Delete_Success_204(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID, streamID := uuid.New(), uuid.New()
	svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Name: "db"}, nil)
	svc.On("Delete", streamID).Return(nil)

	w := doStream(streamRouter(h, testUser()), "DELETE", "/projects/"+projectID.String()+"/streams/"+streamID.String(), nil)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestStreamHandler_Get_NotFound_404(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID, streamID := uuid.New(), uuid.New()
	svc.On("Get", streamID).Return(nil, services.ErrStreamNotFound)

	w := doStream(streamRouter(h, testUser()), "GET", "/projects/"+projectID.String()+"/streams/"+streamID.String(), nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStreamHandler_Get_OtherProject_404(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID, streamID := uuid.New(), uuid.New()
	svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: uuid.New()}, nil)

	w := doStream(streamRouter(h, testUser()), "GET", "/projects/"+projectID.String()+"/streams/"+streamID.String(), nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStreamHandler_List_Success(t *testing.T) {
	views := new(mocks.MockStreamViewService)
	h := handlers.NewStreamHandler(new(mocks.MockStreamService), views, new(mocks.MockAuditService), domainPermChecker())
	projectID := uuid.New()
	views.On("ListWithSummary", projectID).Return([]services.StreamListItem{
		{Stream: models.Stream{ID: uuid.New(), Name: "a", Namespace: "ns"}, Mode: "shared", GatewayTemplateName: "shared-envoy",
			RouteCount: 1, Ports: []services.StreamPort{{Transport: "TCP", Port: 5432}}},
		{Stream: models.Stream{ID: uuid.New(), Name: "b", Namespace: "ns"}, Mode: "dedicated", GatewayTemplateName: "edge-l4",
			Ports: []services.StreamPort{}},
	}, nil)

	w := doStream(streamRouter(h, testUser()), "GET", "/projects/"+projectID.String()+"/streams", nil)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 2)
	first := resp.Data[0]
	assert.Equal(t, "a", first["name"], "existing Stream fields stay at the top level")
	assert.Equal(t, "ns", first["namespace"])
	assert.Equal(t, "shared", first["mode"])
	assert.Equal(t, "shared-envoy", first["gatewayTemplateName"])
	assert.Equal(t, float64(1), first["routeCount"])
	assert.Equal(t, []any{map[string]any{"transport": "TCP", "port": float64(5432)}}, first["ports"])
	assert.Equal(t, []any{}, resp.Data[1]["ports"], "no routes is an empty list, never null")
}

func TestStreamHandler_List_SummaryFailureIs500(t *testing.T) {
	views := new(mocks.MockStreamViewService)
	h := handlers.NewStreamHandler(new(mocks.MockStreamService), views, new(mocks.MockAuditService), domainPermChecker())
	projectID := uuid.New()
	views.On("ListWithSummary", projectID).Return(nil, errors.New("failed to load gateway template"))

	w := doStream(streamRouter(h, testUser()), "GET", "/projects/"+projectID.String()+"/streams", nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestStreamHandler_Update_Success(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID, streamID := uuid.New(), uuid.New()
	newName := "renamed"
	svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Name: "db"}, nil)
	svc.On("Update", streamID, services.UpdateStreamInput{Name: &newName}).Return(&models.Stream{ID: streamID, ProjectID: projectID, Name: newName}, nil)

	w := doStream(streamRouter(h, testUser()), "PATCH", "/projects/"+projectID.String()+"/streams/"+streamID.String(), map[string]any{"name": newName})

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestStreamHandler_Update_BadName_400(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID, streamID := uuid.New(), uuid.New()
	svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Name: "db"}, nil)

	w := doStream(streamRouter(h, testUser()), "PATCH", "/projects/"+projectID.String()+"/streams/"+streamID.String(), map[string]any{"name": "Bad_Name"})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

func TestCreateStream_PortsRequired(t *testing.T) {
	for name, body := range map[string]map[string]any{
		"missing": {"name": "db", "namespace": "ns", "gatewayTemplateId": uuid.New().String()},
		"empty":   {"name": "db", "namespace": "ns", "gatewayTemplateId": uuid.New().String(), "ports": []any{}},
	} {
		t.Run(name, func(t *testing.T) {
			h, svc, _ := newStreamHandler()
			projectID := uuid.New()
			w := doStream(streamRouter(h, testUser()), "POST", "/projects/"+projectID.String()+"/streams", body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			svc.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestUpdateStream_PassesPortsThrough(t *testing.T) {
	h, svc, _ := newStreamHandler()
	projectID, streamID := uuid.New(), uuid.New()
	next := models.StreamPorts{{Transport: "tcp", Port: 5432}, {Transport: "UDP", Port: 53}}
	svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Name: "db"}, nil)
	svc.On("Update", streamID, services.UpdateStreamInput{Ports: &next}).
		Return(&models.Stream{ID: streamID, ProjectID: projectID, Name: "db", Ports: next.Normalize()}, nil)

	w := doStream(streamRouter(h, testUser()), "PATCH", "/projects/"+projectID.String()+"/streams/"+streamID.String(),
		map[string]any{"ports": []map[string]any{{"transport": "tcp", "port": 5432}, {"transport": "UDP", "port": 53}}})

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got models.Stream
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, next.Normalize(), got.Ports)
	svc.AssertExpectations(t)
}

func TestUpdateStream_ErrorMapping(t *testing.T) {
	cases := map[string]struct {
		err  error
		code int
	}{
		"port in use":       {services.ErrStreamPortInUse, http.StatusConflict},
		"port collision":    {services.ErrPortCollision, http.StatusConflict},
		"no ports":          {services.ErrNoStreamPorts, http.StatusBadRequest},
		"invalid port":      {services.ErrInvalidStreamPort, http.StatusBadRequest},
		"reserved port":     {services.ErrReservedPort, http.StatusBadRequest},
		"port out of range": {services.ErrPortOutOfRange, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, svc, _ := newStreamHandler()
			projectID, streamID := uuid.New(), uuid.New()
			svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Name: "db"}, nil)
			svc.On("Update", streamID, mock.Anything).Return(nil, fmt.Errorf("wrapped: %w", tc.err))

			w := doStream(streamRouter(h, testUser()), "PATCH", "/projects/"+projectID.String()+"/streams/"+streamID.String(),
				map[string]any{"ports": validStreamPorts})
			assert.Equal(t, tc.code, w.Code, w.Body.String())
		})
	}
}

func TestCreateStream_PortErrorMapping(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		code int
	}{
		"port collision": {services.ErrPortCollision, http.StatusConflict},
		"no ports":       {services.ErrNoStreamPorts, http.StatusBadRequest},
		"invalid port":   {services.ErrInvalidStreamPort, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			h, svc, _ := newStreamHandler()
			projectID := uuid.New()
			svc.On("Create", projectID, mock.Anything, mock.Anything).Return(nil, tc.err)
			w := doStream(streamRouter(h, testUser()), "POST", "/projects/"+projectID.String()+"/streams",
				map[string]any{"name": "db", "namespace": "ns", "gatewayTemplateId": uuid.New().String(), "ports": validStreamPorts})
			assert.Equal(t, tc.code, w.Code)
		})
	}
}

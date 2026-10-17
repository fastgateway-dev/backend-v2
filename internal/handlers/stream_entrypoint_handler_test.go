package handlers_test

import (
	"encoding/json"
	"errors"
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

// entrypointRouter mounts the two entry point endpoints the way
// cmd/server/main.go does.
func entrypointRouter(h *handlers.StreamHandler) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user", testUser()); c.Next() })
	r.GET("/projects/:projectId/streams/:streamId/entrypoint", h.GetEntrypoint)
	r.GET("/projects/:projectId/domain-templates/:domainTemplateId/entrypoint", h.GetTemplateEntrypoint)
	return r
}

func newEntrypointHandler() (*handlers.StreamHandler, *mocks.MockStreamService, *mocks.MockStreamViewService) {
	svc, views := new(mocks.MockStreamService), new(mocks.MockStreamViewService)
	return handlers.NewStreamHandler(svc, views, new(mocks.MockAuditService), domainPermChecker()), svc, views
}

func TestStreamHandler_GetEntrypoint_Success(t *testing.T) {
	h, svc, views := newEntrypointHandler()
	projectID, streamID := uuid.New(), uuid.New()
	stream := &models.Stream{ID: streamID, ProjectID: projectID, Name: "test-stream"}
	svc.On("Get", streamID).Return(stream, nil)
	views.On("StreamEntrypoint", mock.Anything, stream).Return(&services.StreamEntrypoint{
		Mode: services.EntrypointModeShared, Address: "10.0.0.5",
		SharedWith: services.EntrypointSharedWith{Streams: []services.EntrypointStreamRef{}, Domains: []services.EntrypointDomainRef{}},
		Ports:      []services.EntrypointPort{}, Reserved: []services.EntrypointReservedPort{},
	}, nil)

	w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/entrypoint", projectID, streamID), nil)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "shared", body["mode"])
	assert.Equal(t, "10.0.0.5", body["address"])
	for _, key := range []string{"templateId", "templateName", "gatewayClass", "portRange", "sharedWith", "ports", "reserved"} {
		assert.Contains(t, body, key)
	}
}

func TestStreamHandler_GetEntrypoint_OtherProjectStreamIsNotFound(t *testing.T) {
	h, svc, views := newEntrypointHandler()
	streamID := uuid.New()
	svc.On("Get", streamID).Return(&models.Stream{ID: streamID, ProjectID: uuid.New()}, nil)

	w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/entrypoint", uuid.New(), streamID), nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	views.AssertNotCalled(t, "StreamEntrypoint", mock.Anything, mock.Anything)
}

func TestStreamHandler_GetEntrypoint_BuilderFailureIs500(t *testing.T) {
	h, svc, views := newEntrypointHandler()
	projectID, streamID := uuid.New(), uuid.New()
	stream := &models.Stream{ID: streamID, ProjectID: projectID}
	svc.On("Get", streamID).Return(stream, nil)
	views.On("StreamEntrypoint", mock.Anything, stream).Return(nil, errors.New("failed to load gateway template: record not found"))

	w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/streams/%s/entrypoint", projectID, streamID), nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestStreamHandler_GetTemplateEntrypoint_ErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"ok", nil, http.StatusOK},
		{"missing template", services.ErrStreamTemplateNotFound, http.StatusNotFound},
		{"other project's template", services.ErrStreamTemplateWrongProject, http.StatusNotFound},
		{"lookup failure", errors.New("db down"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, views := newEntrypointHandler()
			projectID, templateID := uuid.New(), uuid.New()
			var ep *services.StreamEntrypoint
			if tc.err == nil {
				ep = &services.StreamEntrypoint{Mode: services.EntrypointModeDedicated}
			}
			views.On("TemplateEntrypoint", mock.Anything, projectID, templateID).Return(ep, tc.err)

			w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/domain-templates/%s/entrypoint", projectID, templateID), nil)

			assert.Equal(t, tc.code, w.Code, w.Body.String())
		})
	}
}

func TestStreamHandler_GetTemplateEntrypoint_BadTemplateID(t *testing.T) {
	h, _, _ := newEntrypointHandler()
	w := doStream(entrypointRouter(h), "GET", fmt.Sprintf("/projects/%s/domain-templates/not-a-uuid/entrypoint", uuid.New()), nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

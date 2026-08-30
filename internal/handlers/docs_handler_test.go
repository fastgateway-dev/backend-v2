package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestDocsHandler_GetOpenAPISpec(t *testing.T) {
	spec := []byte("openapi: 3.0.0\ninfo:\n  title: FastGateway API")
	h := handlers.NewDocsHandler(spec)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/docs/openapi.yaml", nil)

	h.GetOpenAPISpec(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/yaml")
	assert.Equal(t, string(spec), w.Body.String())
}

func TestDocsHandler_GetOpenAPISpec_Empty(t *testing.T) {
	h := handlers.NewDocsHandler([]byte{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/docs/openapi.yaml", nil)

	h.GetOpenAPISpec(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

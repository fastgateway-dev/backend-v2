package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// DocsHandler serves API documentation assets
type DocsHandler struct {
	openapiSpec []byte
}

// NewDocsHandler creates a new docs handler with embedded OpenAPI spec
func NewDocsHandler(openapiSpec []byte) *DocsHandler {
	return &DocsHandler{
		openapiSpec: openapiSpec,
	}
}

// GetOpenAPISpec returns the bundled OpenAPI specification
func (h *DocsHandler) GetOpenAPISpec(c *gin.Context) {
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", h.openapiSpec)
}

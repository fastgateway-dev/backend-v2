package handlers

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
)

// maxOpenAPISpecBytes caps incoming OpenAPI request bodies. The full spec
// is materialised into a parse tree, so an unbounded body is a DoS vector.
const maxOpenAPISpecBytes = 5 * 1024 * 1024

// OpenAPIImportHandler exposes the OpenAPI import endpoint.
type OpenAPIImportHandler struct {
	svc *services.OpenAPIImportService
}

// NewOpenAPIImportHandler constructs the handler.
func NewOpenAPIImportHandler(svc *services.OpenAPIImportService) *OpenAPIImportHandler {
	return &OpenAPIImportHandler{svc: svc}
}

// Import handles POST /projects/:projectId/domains/:domainId/import/openapi.
// Auth + project/domain access middleware is applied at the route group level
// in main.go (same as other domain-scoped endpoints).
func (h *OpenAPIImportHandler) Import(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxOpenAPISpecBytes)

	var req services.OpenAPIImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		status := http.StatusBadRequest
		errCode := "invalid_request"
		if err.Error() == "http: request body too large" {
			status = http.StatusRequestEntityTooLarge
			errCode = "spec_too_large"
		}
		c.JSON(status, gin.H{
			"error":   errCode,
			"message": err.Error(),
		})
		return
	}

	// Backend validation: exactly one of (service+namespace) or address
	hasService := req.DefaultBackend.Service != ""
	hasAddress := req.DefaultBackend.Address != ""
	if hasService == hasAddress {
		// Both true (conflicting) or both false (missing)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "defaultBackend must specify exactly one of (service+namespace) or address",
		})
		return
	}
	if hasService && req.DefaultBackend.Namespace == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "defaultBackend.namespace is required when service is set",
		})
		return
	}

	resp, err := h.svc.Parse(req.Spec, req.DefaultBackend)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "openapi_parse_failed",
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, resp)
}

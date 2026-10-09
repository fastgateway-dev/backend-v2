package handlers

import (
	"errors"
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateStreamRequest is the JSON body for POST /projects/:projectId/streams.
type CreateStreamRequest struct {
	Name              string    `json:"name" binding:"required"`
	Namespace         string    `json:"namespace" binding:"required"`
	GatewayTemplateID uuid.UUID `json:"gatewayTemplateId" binding:"required"`
}

// UpdateStreamRequest is the JSON body for PATCH /projects/:projectId/streams/:streamId.
// The Gateway Template is immutable, so only the name can change.
type UpdateStreamRequest struct {
	Name *string `json:"name"`
}

// StreamHandler handles L4 stream endpoints.
type StreamHandler struct {
	streamService StreamServiceInterface
	auditService  AuditServiceInterface
	permChecker   *middleware.PermissionChecker
}

// NewStreamHandler creates a new stream handler.
func NewStreamHandler(streamService StreamServiceInterface, auditService AuditServiceInterface, permChecker *middleware.PermissionChecker) *StreamHandler {
	return &StreamHandler{streamService: streamService, auditService: auditService, permChecker: permChecker}
}

// streamError maps service errors to HTTP responses. Unknown errors are 500.
func streamError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrTemplateNotStreamEnabled), errors.Is(err, services.ErrInvalidStreamName), errors.Is(err, services.ErrStreamTemplateImmutable):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrStreamHasRoutes):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrStreamNotFound), errors.Is(err, services.ErrStreamTemplateNotFound), errors.Is(err, services.ErrStreamTemplateWrongProject):
		// Cross-project template references are reported as not-found.
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// loadStream resolves :streamId and verifies it belongs to :projectId, writing
// the error response itself. A stream in another project is reported as not
// found so IDs cannot be probed across projects.
func (h *StreamHandler) loadStream(c *gin.Context, projectID uuid.UUID) (*models.Stream, bool) {
	id, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid stream ID"})
		return nil, false
	}
	stream, err := h.streamService.Get(id)
	if err != nil {
		streamError(c, err)
		return nil, false
	}
	if stream.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": services.ErrStreamNotFound.Error()})
		return nil, false
	}
	return stream, true
}

// List lists streams in a project.
func (h *StreamHandler) List(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	streams, err := h.streamService.List(projectID)
	if err != nil {
		streamError(c, err)
		return
	}
	if streams == nil {
		streams = []models.Stream{}
	}
	c.JSON(http.StatusOK, gin.H{"data": streams})
}

// Create creates a stream.
func (h *StreamHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can create streams"})
		return
	}

	var req CreateStreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := services.ValidateStreamName(req.Name); err != nil {
		streamError(c, err)
		return
	}

	stream, err := h.streamService.Create(projectID, services.CreateStreamInput{
		Name:              req.Name,
		Namespace:         req.Namespace,
		GatewayTemplateID: req.GatewayTemplateID,
	}, user)
	if err != nil {
		streamError(c, err)
		return
	}

	h.auditService.LogAction(&projectID, user, "create", "stream", &stream.ID, stream.Name,
		middleware.AuditDetails(c), c.ClientIP(), c.Request.UserAgent())

	c.JSON(http.StatusCreated, stream)
}

// Get gets a stream by ID.
func (h *StreamHandler) Get(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	stream, ok := h.loadStream(c, projectID)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, stream)
}

// Update renames a stream.
func (h *StreamHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can update streams"})
		return
	}
	existing, ok := h.loadStream(c, projectID)
	if !ok {
		return
	}

	var req UpdateStreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name != nil {
		if err := services.ValidateStreamName(*req.Name); err != nil {
			streamError(c, err)
			return
		}
	}

	stream, err := h.streamService.Update(existing.ID, services.UpdateStreamInput{Name: req.Name})
	if err != nil {
		streamError(c, err)
		return
	}

	h.auditService.LogAction(&projectID, user, "update", "stream", &stream.ID, stream.Name,
		middleware.AuditDetails(c), c.ClientIP(), c.Request.UserAgent())

	c.JSON(http.StatusOK, stream)
}

// Delete deletes a stream; 409 while it still has routes.
func (h *StreamHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can delete streams"})
		return
	}
	stream, ok := h.loadStream(c, projectID)
	if !ok {
		return
	}

	if err := h.streamService.Delete(stream.ID); err != nil {
		streamError(c, err)
		return
	}

	h.auditService.LogAction(&projectID, user, "delete", "stream", &stream.ID, stream.Name,
		middleware.AuditDetails(c), c.ClientIP(), c.Request.UserAgent())

	c.Status(http.StatusNoContent)
}

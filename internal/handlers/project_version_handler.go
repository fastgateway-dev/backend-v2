package handlers

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ProjectVersionHandler exposes detected EG and Gateway API versions for a project.
type ProjectVersionHandler struct {
	svc services.ProjectVersionServiceInterface
}

func NewProjectVersionHandler(svc services.ProjectVersionServiceInterface) *ProjectVersionHandler {
	return &ProjectVersionHandler{svc: svc}
}

// Get returns the cached or freshly-detected version info for the project.
func (h *ProjectVersionHandler) Get(c *gin.Context) {
	pid, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project id"})
		return
	}
	info, err := h.svc.Get(c.Request.Context(), pid, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, info)
}

// Refresh invalidates the cache and re-detects.
func (h *ProjectVersionHandler) Refresh(c *gin.Context) {
	pid, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project id"})
		return
	}
	h.svc.Invalidate(pid)
	info, err := h.svc.Get(c.Request.Context(), pid, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, info)
}

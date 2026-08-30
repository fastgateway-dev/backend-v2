package handlers

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PermissionHandler handles permission endpoints
type PermissionHandler struct {
	permChecker *middleware.PermissionChecker
}

// NewPermissionHandler creates a new permission handler
func NewPermissionHandler(permChecker *middleware.PermissionChecker) *PermissionHandler {
	return &PermissionHandler{
		permChecker: permChecker,
	}
}

// GetPermissions returns the current user's permissions for a project
func (h *PermissionHandler) GetPermissions(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	permissions := h.permChecker.GetProjectPermissions(projectID, user)
	c.JSON(http.StatusOK, permissions)
}

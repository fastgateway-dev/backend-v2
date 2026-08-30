package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RouteVersionHandler handles route version history endpoints
type RouteVersionHandler struct {
	routeVersionService services.RouteVersionServiceInterface
	auditService        services.AuditServiceInterface
}

// NewRouteVersionHandler creates a new route version handler
func NewRouteVersionHandler(routeVersionService services.RouteVersionServiceInterface, auditService services.AuditServiceInterface) *RouteVersionHandler {
	return &RouteVersionHandler{
		routeVersionService: routeVersionService,
		auditService:        auditService,
	}
}

// List returns paginated version history for a route
func (h *RouteVersionHandler) List(c *gin.Context) {
	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	versions, total, err := h.routeVersionService.ListVersions(routeID, page, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list versions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  versions,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// Get returns a specific version of a route
func (h *RouteVersionHandler) Get(c *gin.Context) {
	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	version, err := strconv.Atoi(c.Param("version"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid version number"})
		return
	}

	rv, err := h.routeVersionService.GetVersion(routeID, version)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Version not found"})
		return
	}

	c.JSON(http.StatusOK, rv)
}

// Rollback initiates a rollback to a previous version
func (h *RouteVersionHandler) Rollback(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	version, err := strconv.Atoi(c.Param("version"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid version number"})
		return
	}

	route, err := h.routeVersionService.Rollback(routeID, version, user.ID)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": errMsg})
		} else if strings.Contains(errMsg, "failed to unmarshal") || strings.Contains(errMsg, "failed to get version") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": errMsg})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
		}
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"rollback",
		"route",
		&route.ID,
		route.Name,
		map[string]interface{}{"targetVersion": version},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, route)
}

package handlers

import (
	"net/http"
	"strconv"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Stream-scoped route endpoints: the L4 (tcp/udp) counterpart of the
// domain-scoped /projects/:projectId/domains/:domainId/routes group.
//
//	/projects/:projectId/streams/:streamId/routes
//
// An L4 route is owned by a Stream, not a Domain, so these are mounted under
// the stream. They reuse the same RouteService write path, approval flow and
// handlers as HTTP routes: create resolves the stream (and checks it belongs
// to the project) and submits the usual route approval; update, delete, get,
// deploy and yaml first confirm the route belongs to the stream (and the
// stream to the project) and then delegate to the shared handlers, which only
// need :projectId and :routeId.

// streamRouteIDs parses :projectId and :streamId, writing the 400 itself.
func streamRouteIDs(c *gin.Context) (projectID, streamID uuid.UUID, ok bool) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return uuid.Nil, uuid.Nil, false
	}
	streamID, err = uuid.Parse(c.Param("streamId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid stream ID"})
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, streamID, true
}

// requireStreamRoute verifies that :routeId is an L4 route of :streamId and
// that the stream belongs to :projectId, writing a 400/404 itself. A route or
// stream outside the caller's scope is reported as not found, so IDs cannot be
// probed across streams or projects.
func (h *RouteHandler) requireStreamRoute(c *gin.Context) bool {
	projectID, streamID, ok := streamRouteIDs(c)
	if !ok {
		return false
	}
	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return false
	}
	if _, err := h.routeService.GetForStream(projectID, streamID, routeID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Route not found"})
		return false
	}
	return true
}

// ListByStream lists the L4 routes of a stream.
func (h *RouteHandler) ListByStream(c *gin.Context) {
	projectID, streamID, ok := streamRouteIDs(c)
	if !ok {
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	var teamID *uuid.UUID
	if teamIDStr := c.Query("teamId"); teamIDStr != "" {
		if id, err := uuid.Parse(teamIDStr); err == nil {
			teamID = &id
		}
	}

	routes, total, err := h.routeService.ListByStreamID(projectID, streamID, page, limit, teamID, c.Query("status"))
	if err != nil {
		c.JSON(routeWriteErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	if routes == nil {
		routes = []models.Route{}
	}

	c.JSON(http.StatusOK, gin.H{
		"data": routes,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// CreateForStream creates an L4 route under a stream (submits for approval).
func (h *RouteHandler) CreateForStream(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, streamID, ok := streamRouteIDs(c)
	if !ok {
		return
	}

	// Same permission as creating a domain route: Owner, Project Admin or Editor.
	if !h.permChecker.CanCreateRoutes(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only editors can create routes"})
		return
	}

	var input services.CreateRouteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	route, err := h.routeService.CreateForStream(projectID, streamID, &input, user.ID)
	if err != nil {
		c.JSON(routeWriteErrorStatus(err), gin.H{"error": err.Error()})
		return
	}

	details := middleware.AuditDetails(c)
	details["approvalEntityType"] = "route"
	details["streamId"] = streamID.String()
	if aid, err := h.routeService.GetApprovalIDForEntity(models.ApprovalEntityRoute, route.ID); err == nil {
		details["approvalId"] = aid
	}
	h.auditService.LogAction(&projectID, user, "create", "route", &route.ID, route.Name, details,
		c.ClientIP(), c.Request.UserAgent())

	c.JSON(http.StatusCreated, RouteResponse{Route: route})
}

// GetForStream gets an L4 route of a stream.
func (h *RouteHandler) GetForStream(c *gin.Context) {
	if h.requireStreamRoute(c) {
		h.Get(c)
	}
}

// UpdateForStream updates an L4 route of a stream (submits for approval).
func (h *RouteHandler) UpdateForStream(c *gin.Context) {
	if h.requireStreamRoute(c) {
		h.Update(c)
	}
}

// DeleteForStream requests deletion of an L4 route of a stream.
func (h *RouteHandler) DeleteForStream(c *gin.Context) {
	if h.requireStreamRoute(c) {
		h.Delete(c)
	}
}

// DeployForStream deploys an approved L4 route of a stream.
func (h *RouteHandler) DeployForStream(c *gin.Context) {
	if h.requireStreamRoute(c) {
		h.Deploy(c)
	}
}

// GetYAMLForStream gets the TCPRoute/UDPRoute YAML of an L4 route of a stream.
func (h *RouteHandler) GetYAMLForStream(c *gin.Context) {
	if h.requireStreamRoute(c) {
		h.GetYAML(c)
	}
}

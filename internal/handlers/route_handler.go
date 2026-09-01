package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RouteHandler handles route endpoints
type RouteHandler struct {
	routeService services.RouteHandlerService
	auditService services.AuditServiceInterface
	permChecker  *middleware.PermissionChecker
}

// NewRouteHandler creates a new route handler
func NewRouteHandler(routeService services.RouteHandlerService, auditService services.AuditServiceInterface, permChecker *middleware.PermissionChecker) *RouteHandler {
	return &RouteHandler{
		routeService: routeService,
		auditService: auditService,
		permChecker:  permChecker,
	}
}

// List lists routes for a domain
func (h *RouteHandler) List(c *gin.Context) {
	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
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
	status := c.Query("status")
	search := c.Query("search")
	searchField := c.Query("searchField") // "all", "name", "path", "owner"

	var teamID *uuid.UUID
	if teamIDStr := c.Query("teamId"); teamIDStr != "" {
		id, err := uuid.Parse(teamIDStr)
		if err == nil {
			teamID = &id
		}
	}
	labels := parseLabelsFilter(c.Query("labels"))

	routes, total, err := h.routeService.ListByDomainID(domainID, page, limit, teamID, status, search, searchField, labels)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
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

// Create creates a new route
func (h *RouteHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: Owner, Project Admin, or Editor can create routes
	if !h.permChecker.CanCreateRoutes(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only editors can create routes"})
		return
	}

	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	var input services.CreateRouteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	route, err := h.routeService.Create(domainID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	details := middleware.AuditDetails(c)
	details["approvalEntityType"] = "route"
	if dn, err := h.routeService.GetDomainName(domainID); err == nil {
		details["domainName"] = dn
	}
	if aid, err := h.routeService.GetApprovalIDForEntity(models.ApprovalEntityRoute, route.ID); err == nil {
		details["approvalId"] = aid
	}
	h.auditService.LogAction(
		&projectID,
		user,
		"create",
		"route",
		&route.ID,
		route.Name,
		details,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	warnings := services.BackendTLSWarnings(&input.Config)
	warnings = append(warnings, services.DirectResponsePercentWarnings(&input.Config)...)
	resp := RouteResponse{Route: route, Warnings: warnings}
	c.JSON(http.StatusCreated, resp)
}

// RouteResponse wraps a route with optional warnings for the caller
type RouteResponse struct {
	*models.Route
	Warnings []string `json:"warnings,omitempty"`
}

// RouteWithPolicies represents a route with its associated policies
type RouteWithPolicies struct {
	*models.Route
	SecurityPolicy       *models.SecurityPolicy       `json:"securityPolicy,omitempty"`
	BackendTrafficPolicy *models.BackendTrafficPolicy `json:"backendTrafficPolicy,omitempty"`
	ExtensionPolicy      *models.EnvoyExtensionPolicy `json:"extensionPolicy,omitempty"`
	WafPolicy            *models.WafPolicy            `json:"wafPolicy,omitempty"`
}

// Get gets a route by ID
func (h *RouteHandler) Get(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	route, err := h.routeService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Route not found"})
		return
	}

	if !middleware.IsOwner(user) && !h.permChecker.IsProjectAdmin(projectID, user.ID) {
		isMember, _ := h.permChecker.IsTeamMember(route.TeamID, user.ID)
		if !isMember {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: not a member of the route's owner team"})
			return
		}
	}

	// Get security policy if exists
	securityPolicy, _ := h.routeService.GetSecurityPolicy(id)

	// Get backend traffic policy if exists
	backendTrafficPolicy, _ := h.routeService.GetBackendTrafficPolicy(id)

	// Get envoy extension policy if exists
	extensionPolicy, _ := h.routeService.GetEnvoyExtensionPolicy(id)

	// Get WAF policy if exists
	wafPolicy, _ := h.routeService.GetWafPolicy(id)

	response := RouteWithPolicies{
		Route:                route,
		SecurityPolicy:       securityPolicy,
		BackendTrafficPolicy: backendTrafficPolicy,
		ExtensionPolicy:      extensionPolicy,
		WafPolicy:            wafPolicy,
	}

	c.JSON(http.StatusOK, response)
}

// Update updates a route
func (h *RouteHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: Owner, Project Admin, or Editor can update routes
	if !h.permChecker.CanCreateRoutes(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only editors can update routes"})
		return
	}

	id, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	var input services.UpdateRouteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	route, err := h.routeService.Update(id, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	details := middleware.AuditDetails(c)
	details["approvalEntityType"] = "route"
	if did, err := uuid.Parse(c.Param("domainId")); err == nil {
		if dn, err := h.routeService.GetDomainName(did); err == nil {
			details["domainName"] = dn
		}
	}
	if aid, err := h.routeService.GetApprovalIDForEntity(models.ApprovalEntityRoute, route.ID); err == nil {
		details["approvalId"] = aid
	}
	h.auditService.LogAction(
		&projectID,
		user,
		"update",
		"route",
		&route.ID,
		route.Name,
		details,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	warnings := services.BackendTLSWarnings(&input.Config)
	warnings = append(warnings, services.DirectResponsePercentWarnings(&input.Config)...)
	resp := RouteResponse{Route: route, Warnings: warnings}
	c.JSON(http.StatusOK, resp)
}

// Delete requests deletion of a route
func (h *RouteHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: Owner, Project Admin, or Editor can delete routes
	if !h.permChecker.CanCreateRoutes(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only editors can delete routes"})
		return
	}

	id, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	route, err := h.routeService.Delete(id, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	details := middleware.AuditDetails(c)
	details["approvalEntityType"] = "route"
	if did, err := uuid.Parse(c.Param("domainId")); err == nil {
		if dn, err := h.routeService.GetDomainName(did); err == nil {
			details["domainName"] = dn
		}
	}
	if aid, err := h.routeService.GetApprovalIDForEntity(models.ApprovalEntityRoute, route.ID); err == nil {
		details["approvalId"] = aid
	}
	h.auditService.LogAction(
		&projectID,
		user,
		"request_delete",
		"route",
		&route.ID,
		route.Name,
		details,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, route)
}

// GetYAML gets the Kubernetes YAML for a route
func (h *RouteHandler) GetYAML(c *gin.Context) {
	id, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	yaml, err := h.routeService.GenerateYAML(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/yaml")
	c.String(http.StatusOK, yaml)
}

// GetYAMLs gets both HTTPRoute and SecurityPolicy YAML for a route
func (h *RouteHandler) GetYAMLs(c *gin.Context) {
	id, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	yamls, err := h.routeService.GenerateYAMLs(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, yamls)
}

// PreviewCreate generates a preview of the HTTPRoute YAML for a new route
func (h *RouteHandler) PreviewCreate(c *gin.Context) {
	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	var input services.CreateRouteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.routeService.PreviewCreate(domainID, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// CheckConflicts checks if a route matcher conflicts with existing routes in the domain
func (h *RouteHandler) CheckConflicts(c *gin.Context) {
	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	var input struct {
		Match          models.RouteMatch `json:"match"`
		ExcludeRouteID *uuid.UUID        `json:"excludeRouteId,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	conflicts, err := h.routeService.CheckMatcherConflicts(domainID, input.Match, input.ExcludeRouteID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"conflicts": conflicts})
}

// PreviewUpdate generates a preview comparing current and proposed HTTPRoute YAML
func (h *RouteHandler) PreviewUpdate(c *gin.Context) {
	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	var input services.UpdateRouteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.routeService.PreviewUpdate(routeID, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// PreviewDelete generates a preview of what will be deleted
func (h *RouteHandler) PreviewDelete(c *gin.Context) {
	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	result, err := h.routeService.PreviewDelete(routeID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// Deploy deploys an approved route to Kubernetes
// Only the route owner team can deploy
func (h *RouteHandler) Deploy(c *gin.Context) {
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

	// Get the route to check ownership
	route, err := h.routeService.GetByID(routeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Route not found"})
		return
	}

	// Check permission: Only route owner team members, Owner, or Project Admin can deploy
	isOwnerOrAdmin := middleware.IsOwner(user) || h.permChecker.IsProjectAdmin(projectID, user.ID)
	if !isOwnerOrAdmin {
		// Check if user is a member of the route's owner team
		isMember, err := h.permChecker.IsTeamMember(route.TeamID, user.ID)
		if err != nil || !isMember {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only route owner team members can deploy"})
			return
		}
	}

	deployedRoute, err := h.routeService.Deploy(routeID, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	details := middleware.AuditDetails(c)
	details["approvalEntityType"] = "route"
	if did, err := uuid.Parse(c.Param("domainId")); err == nil {
		if dn, err := h.routeService.GetDomainName(did); err == nil {
			details["domainName"] = dn
		}
	}
	if aid, err := h.routeService.GetApprovalIDForEntity(models.ApprovalEntityRoute, deployedRoute.ID); err == nil {
		details["approvalId"] = aid
	}
	h.auditService.LogAction(
		&projectID,
		user,
		"deploy",
		"route",
		&deployedRoute.ID,
		deployedRoute.Name,
		details,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, deployedRoute)
}

// GetEffectiveIPs returns the effective IP allowlist for a route from active client attachments
func (h *RouteHandler) GetEffectiveIPs(c *gin.Context) {
	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	entries, err := h.routeService.GetEffectiveIPAllowlist(routeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, entries)
}

// ListByProject returns routes across all domains in a project, with optional
// filters for backend service+namespace, statuses, team, and domain.
//
// GET /api/v1/projects/:projectId/routes
//
//	?backend_service=payments-api
//	&backend_namespace=payments
//	&include_mirrors=true|false
//	&status=active,pending_create
//	&team_id=<uuid>
//	&domain_id=<uuid>
//	&page=1&limit=50
func (h *RouteHandler) ListByProject(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	filters := services.RouteListFilters{
		BackendService:   c.Query("backend_service"),
		BackendNamespace: c.Query("backend_namespace"),
		IncludeMirrors:   c.Query("include_mirrors") == "true",
	}

	if statusCSV := c.Query("status"); statusCSV != "" {
		for _, s := range strings.Split(statusCSV, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				filters.Statuses = append(filters.Statuses, s)
			}
		}
	}

	if teamIDStr := c.Query("team_id"); teamIDStr != "" {
		id, err := uuid.Parse(teamIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team_id"})
			return
		}
		filters.TeamID = &id
	}
	if domainIDStr := c.Query("domain_id"); domainIDStr != "" {
		id, err := uuid.Parse(domainIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain_id"})
			return
		}
		filters.DomainID = &id
	}

	// Reject partial backend filter (one without the other) to avoid
	// accidentally matching every route in the project.
	if (filters.BackendService == "") != (filters.BackendNamespace == "") {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "backend_service and backend_namespace must be provided together",
		})
		return
	}

	routes, total, err := h.routeService.ListByProjectID(projectID, page, limit, filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
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

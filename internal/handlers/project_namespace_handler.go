package handlers

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ProjectNamespaceHandler handles project namespace endpoints
type ProjectNamespaceHandler struct {
	nsService    services.ProjectNamespaceServiceInterface
	auditService services.AuditServiceInterface
	permChecker  *middleware.PermissionChecker
}

// NewProjectNamespaceHandler creates a new project namespace handler
func NewProjectNamespaceHandler(nsService services.ProjectNamespaceServiceInterface, auditService services.AuditServiceInterface, permChecker *middleware.PermissionChecker) *ProjectNamespaceHandler {
	return &ProjectNamespaceHandler{
		nsService:    nsService,
		auditService: auditService,
		permChecker:  permChecker,
	}
}

// List lists all namespaces for a project
func (h *ProjectNamespaceHandler) List(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	namespaces, err := h.nsService.ListByProjectID(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": namespaces,
	})
}

// Create adds a namespace to a project
func (h *ProjectNamespaceHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: only Owner or Project Admin can manage namespaces
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage namespaces"})
		return
	}

	var input services.CreateProjectNamespaceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ns, err := h.nsService.Create(projectID, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"create",
		"project_namespace",
		&ns.ID,
		ns.Namespace,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, ns)
}

// Update changes the capabilities of an existing project namespace
func (h *ProjectNamespaceHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage namespaces"})
		return
	}

	id, err := uuid.Parse(c.Param("namespaceId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid namespace ID"})
		return
	}

	var input services.UpdateProjectNamespaceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ns, err := h.nsService.Update(id, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"update",
		"project_namespace",
		&ns.ID,
		ns.Namespace,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, ns)
}

// Get gets a project namespace by ID
func (h *ProjectNamespaceHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("namespaceId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid namespace ID"})
		return
	}

	ns, err := h.nsService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Namespace not found"})
		return
	}

	c.JSON(http.StatusOK, ns)
}

// Delete removes a namespace from a project
func (h *ProjectNamespaceHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: only Owner or Project Admin can manage namespaces
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage namespaces"})
		return
	}

	id, err := uuid.Parse(c.Param("namespaceId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid namespace ID"})
		return
	}

	ns, err := h.nsService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Namespace not found"})
		return
	}

	if err := h.nsService.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"delete",
		"project_namespace",
		&id,
		ns.Namespace,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// EnsureReferenceGrant recreates the ReferenceGrant if it was deleted externally
func (h *ProjectNamespaceHandler) EnsureReferenceGrant(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: only Owner or Project Admin can manage namespaces
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage namespaces"})
		return
	}

	id, err := uuid.Parse(c.Param("namespaceId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid namespace ID"})
		return
	}

	if err := h.nsService.EnsureReferenceGrant(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ns, err := h.nsService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Namespace not found"})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"ensure_reference_grant",
		"project_namespace",
		&id,
		ns.Namespace,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, ns)
}

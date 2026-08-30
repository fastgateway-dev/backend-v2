package handlers

import (
	"net/http"
	"strconv"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ProjectHandler handles project endpoints
type ProjectHandler struct {
	projectService services.ProjectServiceInterface
	auditService   services.AuditServiceInterface
	k8sService     services.KubernetesServiceInterface
}

// NewProjectHandler creates a new project handler
func NewProjectHandler(projectService services.ProjectServiceInterface, auditService services.AuditServiceInterface, k8sService services.KubernetesServiceInterface) *ProjectHandler {
	return &ProjectHandler{
		projectService: projectService,
		auditService:   auditService,
		k8sService:     k8sService,
	}
}

// List lists projects
func (h *ProjectHandler) List(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	search := c.Query("search")
	labels := parseLabelsFilter(c.Query("labels"))

	projects, total, err := h.projectService.List(user.ID, user.Role, page, limit, search, labels)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": projects,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// Create creates a new project
func (h *ProjectHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	var input services.CreateProjectInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	project, err := h.projectService.Create(&input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&project.ID,
		user,
		"create",
		"project",
		&project.ID,
		project.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, project)
}

// Get gets a project by ID
func (h *ProjectHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	project, err := h.projectService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	c.JSON(http.StatusOK, project)
}

// Update updates a project
func (h *ProjectHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	id, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var input services.UpdateProjectInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	project, err := h.projectService.Update(id, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&project.ID,
		user,
		"update",
		"project",
		&project.ID,
		project.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, project)
}

// Delete deletes a project
func (h *ProjectHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	id, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	project, err := h.projectService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	if err := h.projectService.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&id,
		user,
		"delete",
		"project",
		&id,
		project.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// TestConnection tests the Kubernetes connection
func (h *ProjectHandler) TestConnection(c *gin.Context) {
	id, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	success, message, version, err := h.projectService.TestConnection(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": message,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":           success,
		"message":           message,
		"kubernetesVersion": version,
	})
}

// ListAdmins lists project admins
func (h *ProjectHandler) ListAdmins(c *gin.Context) {
	id, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	admins, err := h.projectService.ListAdmins(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, admins)
}

// AddAdminRequest represents the request to add an admin
type AddAdminRequest struct {
	UserID uuid.UUID `json:"userId" binding:"required"`
}

// AddAdmin adds an admin to a project
func (h *ProjectHandler) AddAdmin(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	id, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var req AddAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.projectService.AddAdmin(id, req.UserID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&id,
		user,
		"add_admin",
		"project",
		&id,
		"",
		map[string]interface{}{"adminUserId": req.UserID},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusCreated)
}

// RemoveAdmin removes an admin from a project
func (h *ProjectHandler) RemoveAdmin(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	if err := h.projectService.RemoveAdmin(projectID, userID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"remove_admin",
		"project",
		&projectID,
		"",
		map[string]interface{}{"adminUserId": userID},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// GetCapabilities returns project capabilities (e.g., rate limit availability)
func (h *ProjectHandler) GetCapabilities(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project ID"})
		return
	}

	rateLimitAvailable, err := h.k8sService.IsRateLimitAvailable(c.Request.Context(), projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"rateLimitAvailable": rateLimitAvailable,
	})
}

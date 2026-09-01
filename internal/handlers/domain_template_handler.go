package handlers

import (
	"net/http"
	"strconv"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DomainTemplateHandler handles domain template endpoints
type DomainTemplateHandler struct {
	dtService    services.DomainTemplateServiceInterface
	auditService services.AuditServiceInterface
	domainLister services.TemplateDomainLister
}

// NewDomainTemplateHandler creates a new domain template handler
func NewDomainTemplateHandler(
	dtService services.DomainTemplateServiceInterface,
	auditService services.AuditServiceInterface,
	domainLister services.TemplateDomainLister,
) *DomainTemplateHandler {
	return &DomainTemplateHandler{
		dtService:    dtService,
		auditService: auditService,
		domainLister: domainLister,
	}
}

// List lists domain templates in a project
func (h *DomainTemplateHandler) List(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	domainTemplates, total, err := h.dtService.ListByProjectID(projectID, page, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": domainTemplates,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// Create creates a new domain template
func (h *DomainTemplateHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var input services.CreateDomainTemplateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dt, err := h.dtService.Create(projectID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"create",
		"domaintemplate",
		&dt.ID,
		dt.Name,
		map[string]interface{}{
			"exposureType": dt.ExposureType,
			"tlsMode":      dt.TLSMode,
			"httpPort":     dt.HTTPPort,
			"httpsPort":    dt.HTTPSPort,
		},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, dt)
}

// Get gets a domain template by ID
func (h *DomainTemplateHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("domainTemplateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain template ID"})
		return
	}

	dt, err := h.dtService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Domain template not found"})
		return
	}

	c.JSON(http.StatusOK, dt)
}

// GetManifests returns the generated K8s manifests for a domain template
func (h *DomainTemplateHandler) GetManifests(c *gin.Context) {
	id, err := uuid.Parse(c.Param("domainTemplateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain template ID"})
		return
	}

	manifests, err := h.dtService.GetManifests(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, manifests)
}

// ListDomains lists domains using this domain template
func (h *DomainTemplateHandler) ListDomains(c *gin.Context) {
	id, err := uuid.Parse(c.Param("domainTemplateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain template ID"})
		return
	}

	domains, err := h.domainLister.ListDomainsByTemplateID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": domains})
}

// previewChangesRequest wraps the update input with optional AI review fields
type previewChangesRequest struct {
	services.UpdateDomainTemplateInput
	IncludeAIReview   bool   `json:"includeAIReview"`
	ChangeDescription string `json:"changeDescription"`
}

// PreviewChanges returns a preview of what an update would look like
func (h *DomainTemplateHandler) PreviewChanges(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	id, err := uuid.Parse(c.Param("domainTemplateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain template ID"})
		return
	}

	var req previewChangesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	opts := &services.PreviewChangesOptions{
		IncludeAIReview:   req.IncludeAIReview,
		ChangeDescription: req.ChangeDescription,
	}

	result, err := h.dtService.PreviewChanges(id, &req.UpdateDomainTemplateInput, user.ID, opts)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// previewCreateRequest wraps the create input with optional AI review fields
type previewCreateRequest struct {
	services.CreateDomainTemplateInput
	IncludeAIReview   bool   `json:"includeAIReview"`
	ChangeDescription string `json:"changeDescription"`
}

// PreviewCreate returns a preview of what a new domain template would generate
func (h *DomainTemplateHandler) PreviewCreate(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var req previewCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	opts := &services.PreviewChangesOptions{
		IncludeAIReview:   req.IncludeAIReview,
		ChangeDescription: req.ChangeDescription,
	}

	result, err := h.dtService.PreviewCreate(projectID, &req.CreateDomainTemplateInput, user.ID, opts)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// Update updates a domain template
func (h *DomainTemplateHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("domainTemplateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain template ID"})
		return
	}

	var input services.UpdateDomainTemplateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dt, err := h.dtService.Update(id, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"update",
		"domaintemplate",
		&dt.ID,
		dt.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, dt)
}

// Delete deletes a domain template
func (h *DomainTemplateHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("domainTemplateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain template ID"})
		return
	}

	dt, err := h.dtService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Domain template not found"})
		return
	}

	if err := h.dtService.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"delete",
		"domaintemplate",
		&id,
		dt.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DomainSettingsResponse includes CTP settings plus BTP and extension policy
type DomainSettingsResponse struct {
	ID                   *uuid.UUID                         `json:"id,omitempty"`
	DomainID             *uuid.UUID                         `json:"domainId,omitempty"`
	ProjectID            *uuid.UUID                         `json:"projectId,omitempty"`
	Settings             *models.DomainSettingsConfig       `json:"settings,omitempty"`
	BackendTrafficPolicy *models.BackendTrafficPolicyConfig `json:"backendTrafficPolicy,omitempty"`
	ExtensionPolicy      *models.EnvoyExtensionPolicyConfig `json:"extensionPolicy,omitempty"`
	CreatedAt            *time.Time                         `json:"createdAt,omitempty"`
	UpdatedAt            *time.Time                         `json:"updatedAt,omitempty"`
}

// DomainHandler handles domain endpoints
type DomainHandler struct {
	domainService services.DomainServiceInterface
	auditService  services.AuditServiceInterface
	permChecker   *middleware.PermissionChecker
	policyReader  services.DomainPolicyReader
}

// NewDomainHandler creates a new domain handler
func NewDomainHandler(domainService services.DomainServiceInterface, auditService services.AuditServiceInterface, permChecker *middleware.PermissionChecker, policyReader services.DomainPolicyReader) *DomainHandler {
	return &DomainHandler{
		domainService: domainService,
		auditService:  auditService,
		permChecker:   permChecker,
		policyReader:  policyReader,
	}
}

// List lists domains in a project
func (h *DomainHandler) List(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	search := c.Query("search")
	status := c.Query("status")
	labels := parseLabelsFilter(c.Query("labels"))

	domains, total, err := h.domainService.ListByProjectID(projectID, page, limit, search, status, labels)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": domains,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// Create creates a new domain
func (h *DomainHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: only Owner or Project Admin can create domains
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can create domains"})
		return
	}

	var input services.CreateDomainInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	domain, err := h.domainService.Create(projectID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"create",
		"domain",
		&domain.ID,
		domain.Hostname,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, domain)
}

// ListTLSSecrets returns available TLS secrets for the project
func (h *DomainHandler) ListTLSSecrets(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	namespace := c.DefaultQuery("namespace", "")

	result, err := h.domainService.ListTLSSecrets(c.Request.Context(), projectID, namespace)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "not managed by this project") || strings.Contains(errMsg, "namespace management not configured") {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": errMsg})
		}
		return
	}

	c.JSON(http.StatusOK, result)
}

// Get gets a domain by ID
func (h *DomainHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	domain, err := h.domainService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Domain not found"})
		return
	}

	c.JSON(http.StatusOK, domain)
}

// Update updates a domain
func (h *DomainHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: only Owner or Project Admin can update domains
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can update domains"})
		return
	}

	id, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	var input services.UpdateDomainInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	domain, err := h.domainService.Update(id, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"update",
		"domain",
		&domain.ID,
		domain.Hostname,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, domain)
}

// Delete deletes a domain
func (h *DomainHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: only Owner or Project Admin can delete domains
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can delete domains"})
		return
	}

	id, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	domain, err := h.domainService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Domain not found"})
		return
	}

	if err := h.domainService.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"delete",
		"domain",
		&id,
		domain.Hostname,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// GetDomainSettings gets the settings for a domain
func (h *DomainHandler) GetDomainSettings(c *gin.Context) {
	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	settings, err := h.domainService.GetDomainSettings(domainID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	response := &DomainSettingsResponse{}
	if settings != nil {
		response.ID = &settings.ID
		response.DomainID = &settings.DomainID
		response.ProjectID = &settings.ProjectID
		response.Settings = &settings.Config
		response.CreatedAt = &settings.CreatedAt
		response.UpdatedAt = &settings.UpdatedAt
	}

	// Fetch BTP and extension policy. The nil guard is not wiring tolerance:
	// handler tests construct the handler without a policy reader when the
	// endpoint under test does not exercise these two reads.
	if h.policyReader != nil {
		btpPolicy, err := h.policyReader.GetDomainBackendTrafficPolicy(domainID)
		if err == nil && btpPolicy != nil {
			response.BackendTrafficPolicy = &btpPolicy.Config
		}

		extPolicy, err := h.policyReader.GetDomainEnvoyExtensionPolicy(domainID)
		if err == nil && extPolicy != nil {
			response.ExtensionPolicy = &extPolicy.Config
		}
	}

	c.JSON(http.StatusOK, response)
}

// UpdateDomainSettings updates the settings for a domain
func (h *DomainHandler) UpdateDomainSettings(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: only Owner or Project Admin can update domain settings
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage domain settings"})
		return
	}

	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	var input services.UpdateDomainSettingsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	settings, err := h.domainService.UpdateDomainSettings(domainID, &input)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"update",
		"domain_settings",
		&domainID,
		"",
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, settings)
}

// GetYAMLs returns generated K8s YAML manifests for a domain
func (h *DomainHandler) GetYAMLs(c *gin.Context) {
	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	yamls, err := h.domainService.GenerateYAMLs(domainID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, yamls)
}

// PreviewCreate returns a preview of the Gateway YAML for a proposed domain creation
func (h *DomainHandler) PreviewCreate(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var input services.DomainCreatePreviewInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user := middleware.GetCurrentUser(c)

	result, err := h.domainService.PreviewCreate(projectID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// PreviewSettingsChanges returns a diff and AI review for proposed settings changes
func (h *DomainHandler) PreviewSettingsChanges(c *gin.Context) {
	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	var input services.DomainSettingsPreviewInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user := middleware.GetCurrentUser(c)

	result, err := h.domainService.PreviewSettingsChanges(domainID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// ListAvailableNamespaces returns namespaces eligible for domain deployment
func (h *DomainHandler) ListAvailableNamespaces(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	ctx := c.Request.Context()
	namespaces, err := h.domainService.ListAvailableNamespaces(ctx, projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"namespaces": namespaces})
}

// AddDomainMTLSCA adds a CA certificate for domain mTLS
// POST /projects/:projectId/domains/:domainId/settings/mtls/ca
func (h *DomainHandler) AddDomainMTLSCA(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: only Owner or Project Admin can manage domain settings
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage domain settings"})
		return
	}

	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	var input services.AddDomainMTLSCAInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	settings, err := h.domainService.AddDomainMTLSCA(c.Request.Context(), domainID, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"add",
		"domain_mtls_ca",
		&domainID,
		"",
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, settings)
}

// RemoveDomainMTLSCA removes a CA certificate from domain mTLS
// DELETE /projects/:projectId/domains/:domainId/settings/mtls/ca/:caId
func (h *DomainHandler) RemoveDomainMTLSCA(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Check permission: only Owner or Project Admin can manage domain settings
	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage domain settings"})
		return
	}

	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	caID := c.Param("caId")
	if caID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CA ID is required"})
		return
	}

	settings, err := h.domainService.RemoveDomainMTLSCA(c.Request.Context(), domainID, caID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"remove",
		"domain_mtls_ca",
		&domainID,
		"",
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, settings)
}

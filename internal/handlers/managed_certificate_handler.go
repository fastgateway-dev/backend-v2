package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// ManagedCertificateHandler exposes project-scoped management of managed
// certificates. Responses only ever carry certificate metadata --
// managedCertificateResponse deliberately omits models.ManagedCertificate's
// Config (DNS names are surfaced separately; the cert-manager Secret/
// Certificate resource names it also holds are internal plumbing) and
// CreatedBy, and no endpoint here ever returns key or certificate material:
// that lives in a cert-manager-managed Kubernetes Secret, never in this row.
type ManagedCertificateHandler struct {
	service      ManagedCertificateServiceInterface
	permChecker  *middleware.PermissionChecker
	auditService AuditServiceInterface
}

// NewManagedCertificateHandler creates a new managed certificate handler.
func NewManagedCertificateHandler(service ManagedCertificateServiceInterface, permChecker *middleware.PermissionChecker, auditService AuditServiceInterface) *ManagedCertificateHandler {
	return &ManagedCertificateHandler{
		service:      service,
		permChecker:  permChecker,
		auditService: auditService,
	}
}

// managedCertificateResponse carries only certificate metadata -- never key
// or certificate material.
type managedCertificateResponse struct {
	ID            uuid.UUID                `json:"id"`
	ProjectID     uuid.UUID                `json:"projectId"`
	Name          string                   `json:"name"`
	IssuerID      uuid.UUID                `json:"issuerId"`
	Usage         models.ManagedCertUsage  `json:"usage"`
	DNSNames      []string                 `json:"dnsNames,omitempty"`
	Status        models.ManagedCertStatus `json:"status"`
	StatusMessage string                   `json:"statusMessage,omitempty"`
	Fingerprint   string                   `json:"fingerprint,omitempty"`
	NotAfter      *time.Time               `json:"notAfter,omitempty"`
	CreatedAt     time.Time                `json:"createdAt"`
}

func toManagedCertificateResponse(c *models.ManagedCertificate) managedCertificateResponse {
	return managedCertificateResponse{
		ID:            c.ID,
		ProjectID:     c.ProjectID,
		Name:          c.Name,
		IssuerID:      c.IssuerID,
		Usage:         c.Usage,
		DNSNames:      c.Config.DNSNames,
		Status:        c.Status,
		StatusMessage: c.StatusMessage,
		Fingerprint:   c.Fingerprint,
		NotAfter:      c.NotAfter,
		CreatedAt:     c.CreatedAt,
	}
}

// List lists managed certificates for a project.
func (h *ManagedCertificateHandler) List(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
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

	certs, total, err := h.service.ListByProject(projectID, page, limit, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]managedCertificateResponse, 0, len(certs))
	for i := range certs {
		resp = append(resp, toManagedCertificateResponse(&certs[i]))
	}

	c.JSON(http.StatusOK, gin.H{
		"data": resp,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// Create creates a new managed certificate. The response's approvalId is
// nil when the project has approvals disabled and the certificate was
// issued via the fast path.
func (h *ManagedCertificateHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	if !h.permChecker.CanCreateCertificates(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only editors can create certificates"})
		return
	}

	var input services.CreateCertificateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cert, approval, err := h.service.Create(projectID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	details := middleware.AuditDetails(c)
	details["approvalEntityType"] = "certificate"
	var approvalID *uuid.UUID
	statusCode := http.StatusCreated
	if approval != nil {
		id := approval.ID
		approvalID = &id
		details["approvalId"] = approval.ID
		statusCode = http.StatusAccepted
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"create",
		"certificate",
		&cert.ID,
		cert.Name,
		details,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(statusCode, gin.H{
		"certificate": toManagedCertificateResponse(cert),
		"approvalId":  approvalID,
	})
}

// Get returns a single managed certificate.
func (h *ManagedCertificateHandler) Get(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// A certificate belonging to a different project must 404, not just be
	// denied -- 404 (rather than 403) avoids confirming that a certificate
	// with this ID exists at all in another project the caller has no
	// business knowing about.
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	c.JSON(http.StatusOK, toManagedCertificateResponse(cert))
}

// Delete deletes a managed certificate.
func (h *ManagedCertificateHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	if !h.permChecker.CanManageCertificates(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can delete certificates"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// See Get's comment: a cross-project delete must 404, not proceed.
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	if err := h.service.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"delete",
		"certificate",
		&id,
		cert.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// Status reports the certificate's live cert-manager issuance status.
func (h *ManagedCertificateHandler) Status(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	// Status only takes an id, so the ownership check needs its own lookup
	// before calling it -- see Get's comment on why this 404s rather than
	// proceeding or 403ing.
	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	status, err := h.service.Status(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, status)
}

// IssuersForProject lists the certificate issuers granted to a project, for
// populating a certificate-create form's issuer picker.
func (h *ManagedCertificateHandler) IssuersForProject(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	issuers, err := h.service.IssuersForProject(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": issuers})
}

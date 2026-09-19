package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// CertificateIssuerHandler exposes owner-only management of platform-global
// certificate issuers (self-signed CA + ACME). The model's own json tags
// already omit key material (private keys and ACME account/EAB secrets live
// in cert-manager-managed Kubernetes Secrets, never in this row), so handlers
// can serialize *models.CertificateIssuer directly, unlike DNSCredentialHandler.
type CertificateIssuerHandler struct {
	service CertificateIssuerServiceInterface
}

func NewCertificateIssuerHandler(service CertificateIssuerServiceInterface) *CertificateIssuerHandler {
	return &CertificateIssuerHandler{service: service}
}

func (h *CertificateIssuerHandler) List(c *gin.Context) {
	items, err := h.service.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *CertificateIssuerHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	var input services.CreateIssuerInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	issuer, err := h.service.Create(&input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, issuer)
}

func (h *CertificateIssuerHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("issuerId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issuer ID"})
		return
	}
	issuer, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate issuer not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, issuer)
}

func (h *CertificateIssuerHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("issuerId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issuer ID"})
		return
	}
	if err := h.service.Delete(id); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// Status returns just the issuer's status and status message, for polling
// while a Create is still converging (e.g. waiting on an ACME account
// registration).
func (h *CertificateIssuerHandler) Status(c *gin.Context) {
	id, err := uuid.Parse(c.Param("issuerId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issuer ID"})
		return
	}
	issuer, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate issuer not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": issuer.Status, "statusMessage": issuer.StatusMessage})
}

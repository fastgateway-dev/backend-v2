package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
)

// DNSActiveCredentialHandler exposes owner-only read/write access to which
// DNS provider credential is currently active for external-dns. There is
// exactly one active credential platform-wide at a time -- see
// DNSInfraService.GetActiveCredentialID/SetActiveCredential.
type DNSActiveCredentialHandler struct {
	service      DNSActiveCredentialServiceInterface
	auditService AuditServiceInterface
}

// NewDNSActiveCredentialHandler creates a new active-DNS-credential handler.
// auditService is security-relevant here: Set re-renders and applies the
// cluster-wide external-dns Secret, so a successful change is audit-logged
// the same way DNSRecordHandler's Enable/Update/Delete already are.
func NewDNSActiveCredentialHandler(service DNSActiveCredentialServiceInterface, auditService AuditServiceInterface) *DNSActiveCredentialHandler {
	return &DNSActiveCredentialHandler{service: service, auditService: auditService}
}

// dnsActiveCredentialResponse carries only the active credential's id, or
// null when no credential is active yet.
type dnsActiveCredentialResponse struct {
	CredentialID *uuid.UUID `json:"credentialId"`
}

// Get returns the id of the currently active DNS provider credential, or
// null if none is active.
func (h *DNSActiveCredentialHandler) Get(c *gin.Context) {
	id, err := h.service.GetActiveCredentialID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, dnsActiveCredentialResponse{CredentialID: id})
}

// setActiveDNSCredentialRequest is the Set request body.
type setActiveDNSCredentialRequest struct {
	CredentialID string `json:"credentialId" binding:"required"`
}

// Set changes the active DNS provider credential: validates it, renders it
// into the external-dns Secret, applies that Secret to the control cluster,
// and only then persists it as active (see
// DNSInfraService.SetActiveCredential). Every failure mode here (credential
// not found/undecryptable, unsupported provider, apply failure) is a
// caller-fixable input problem, so it is mapped to 400, consistent with
// DNSCredentialHandler.Create/Update.
func (h *DNSActiveCredentialHandler) Set(c *gin.Context) {
	var req setActiveDNSCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id, err := uuid.Parse(req.CredentialID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid credentialId"})
		return
	}

	if err := h.service.SetActiveCredential(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user := middleware.GetCurrentUser(c)
	h.auditService.LogAction(
		nil,
		user,
		"update",
		"dns_active_credential",
		&id,
		id.String(),
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, dnsActiveCredentialResponse{CredentialID: &id})
}

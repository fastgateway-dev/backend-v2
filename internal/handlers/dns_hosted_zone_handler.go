package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// DNSHostedZoneHandler exposes owner-only management of DNS hosted zones
// (domain apexes registered under a DNS provider credential). The
// models.DNSHostedZone response is serialized directly -- its
// ProviderZoneID field is already json:"-", so no provider-internal id
// leaks to the response.
type DNSHostedZoneHandler struct {
	service DNSHostedZoneServiceInterface
}

func NewDNSHostedZoneHandler(service DNSHostedZoneServiceInterface) *DNSHostedZoneHandler {
	return &DNSHostedZoneHandler{service: service}
}

func (h *DNSHostedZoneHandler) List(c *gin.Context) {
	zones, err := h.service.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": zones})
}

// createDNSHostedZoneRequest is the request body for Create.
type createDNSHostedZoneRequest struct {
	Name                 string `json:"name" binding:"required"`
	ProviderCredentialID string `json:"providerCredentialId" binding:"required"`
}

// Create registers a new hosted zone. A failed live-provider lookup still
// returns 201 with the zone in an error status (see
// DNSHostedZoneService.Create) -- only a hard failure (bad credential id,
// unsupported provider, persistence failure) is a 400.
func (h *DNSHostedZoneHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	var req createDNSHostedZoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	credID, err := uuid.Parse(req.ProviderCredentialID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid providerCredentialId"})
		return
	}
	zone, err := h.service.Create(req.Name, credID, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, zone)
}

func (h *DNSHostedZoneHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("hostedZoneId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid hosted zone ID"})
		return
	}
	zone, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, services.ErrHostedZoneNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "DNS hosted zone not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, zone)
}

// Delete removes a hosted zone. ErrDNSHostedZoneInUse is a caller-fixable
// conflict (409, mirroring DNSCredentialHandler.Delete); not-found is 404;
// anything else is an unexpected failure (500).
func (h *DNSHostedZoneHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("hostedZoneId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid hosted zone ID"})
		return
	}
	if err := h.service.Delete(id); err != nil {
		if errors.Is(err, services.ErrDNSHostedZoneInUse) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, services.ErrHostedZoneNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "DNS hosted zone not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

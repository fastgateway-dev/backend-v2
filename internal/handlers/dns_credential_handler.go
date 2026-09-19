package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// DNSCredentialHandler exposes owner-only management of platform-global DNS
// provider credentials. Responses never include credential material -- see
// dnsCredentialResponse.
type DNSCredentialHandler struct {
	service DNSCredentialServiceInterface
}

func NewDNSCredentialHandler(service DNSCredentialServiceInterface) *DNSCredentialHandler {
	return &DNSCredentialHandler{service: service}
}

// dnsCredentialResponse never includes credential material.
type dnsCredentialResponse struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	ProviderType string    `json:"providerType"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func toDNSCredentialResponse(c *models.DNSProviderCredential) dnsCredentialResponse {
	return dnsCredentialResponse{
		ID: c.ID, Name: c.Name, ProviderType: c.ProviderType,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func (h *DNSCredentialHandler) List(c *gin.Context) {
	items, err := h.service.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := make([]dnsCredentialResponse, 0, len(items))
	for i := range items {
		resp = append(resp, toDNSCredentialResponse(&items[i]))
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

func (h *DNSCredentialHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	var input services.CreateDNSCredentialInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cred, err := h.service.Create(&input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, toDNSCredentialResponse(cred))
}

func (h *DNSCredentialHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("dnsCredentialId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid credential ID"})
		return
	}
	cred, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "DNS credential not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toDNSCredentialResponse(cred))
}

func (h *DNSCredentialHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("dnsCredentialId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid credential ID"})
		return
	}
	var input services.UpdateDNSCredentialInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cred, err := h.service.Update(id, &input)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "DNS credential not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toDNSCredentialResponse(cred))
}

func (h *DNSCredentialHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("dnsCredentialId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid credential ID"})
		return
	}
	if err := h.service.Delete(id); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

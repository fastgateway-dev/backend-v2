package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
)

// IssuerGrantHandler exposes owner-only management of which projects a
// platform-global certificate issuer is granted to. It is registered as
// sub-routes of /certificates/issuers/:issuerId (see setupRouter), so it is
// pure DB and has no control-plane dependency, unlike CertificateIssuerHandler.
type IssuerGrantHandler struct {
	service IssuerGrantServiceInterface
}

func NewIssuerGrantHandler(service IssuerGrantServiceInterface) *IssuerGrantHandler {
	return &IssuerGrantHandler{service: service}
}

// grantRequest is the body of a Grant request.
type grantRequest struct {
	ProjectID uuid.UUID `json:"projectId" binding:"required"`
}

func (h *IssuerGrantHandler) List(c *gin.Context) {
	issuerID, err := uuid.Parse(c.Param("issuerId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issuer ID"})
		return
	}
	grants, err := h.service.ListGrants(issuerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": grants})
}

func (h *IssuerGrantHandler) Grant(c *gin.Context) {
	issuerID, err := uuid.Parse(c.Param("issuerId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issuer ID"})
		return
	}
	var input grantRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	user := middleware.GetCurrentUser(c)
	if err := h.service.Grant(issuerID, input.ProjectID, user.ID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func (h *IssuerGrantHandler) Revoke(c *gin.Context) {
	issuerID, err := uuid.Parse(c.Param("issuerId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issuer ID"})
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	if err := h.service.Revoke(issuerID, projectID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

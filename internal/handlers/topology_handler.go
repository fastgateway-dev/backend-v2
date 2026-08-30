package handlers

import (
	"errors"
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TopologyHandler exposes the read-only topology endpoints.
type TopologyHandler struct {
	svc services.TopologyServiceInterface
}

// NewTopologyHandler constructs a TopologyHandler.
func NewTopologyHandler(svc services.TopologyServiceInterface) *TopologyHandler {
	return &TopologyHandler{svc: svc}
}

// GetProjectTopology handles GET /projects/:projectId/topology
func (h *TopologyHandler) GetProjectTopology(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	resp, err := h.svc.GetProjectTopology(c.Request.Context(), projectID)
	if err != nil {
		if errors.Is(err, services.ErrTopologyNotFound) {
			// Use a generic body to avoid leaking which resource (or which
			// project) the missing/mismatched ID belongs to.
			c.JSON(http.StatusNotFound, gin.H{"error": "topology resource not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GetDomainTopology handles GET /projects/:projectId/domains/:domainId/topology
func (h *TopologyHandler) GetDomainTopology(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}
	resp, err := h.svc.GetDomainTopology(c.Request.Context(), projectID, domainID)
	if err != nil {
		if errors.Is(err, services.ErrTopologyNotFound) {
			// Use a generic body to avoid leaking which resource (or which
			// project) the missing/mismatched ID belongs to.
			c.JSON(http.StatusNotFound, gin.H{"error": "topology resource not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

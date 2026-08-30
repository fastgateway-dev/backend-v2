package handlers

import (
	"net/http"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MetricsHandler serves observability endpoints.
type MetricsHandler struct {
	metricsService services.MetricsServiceInterface
}

// NewMetricsHandler constructs a MetricsHandler.
func NewMetricsHandler(svc services.MetricsServiceInterface) *MetricsHandler {
	return &MetricsHandler{metricsService: svc}
}

// TestConnection verifies the project's metrics endpoint is reachable.
// POST /projects/:projectId/metrics/test-connection
func (h *MetricsHandler) TestConnection(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	res, err := h.metricsService.TestConnection(c.Request.Context(), projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// GetRouteMetrics returns Tier A panels for a single route.
// GET /projects/:projectId/routes/:routeId/metrics?range=1h
func (h *MetricsHandler) GetRouteMetrics(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}
	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	rangeSpec := c.DefaultQuery("range", "1h")

	res, err := h.metricsService.GetRouteMetrics(c.Request.Context(), projectID, routeID, rangeSpec)
	if err != nil {
		writeMetricsError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// GetDomainMetrics returns Tier A panels plus top-5 tables for a domain.
// GET /projects/:projectId/domains/:domainId/metrics?range=1h
func (h *MetricsHandler) GetDomainMetrics(c *gin.Context) {
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

	rangeSpec := c.DefaultQuery("range", "1h")

	res, err := h.metricsService.GetDomainMetrics(c.Request.Context(), projectID, domainID, rangeSpec)
	if err != nil {
		writeMetricsError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// writeMetricsError maps service errors to HTTP status + uniform body.
func writeMetricsError(c *gin.Context, err error) {
	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "not found"), strings.Contains(lower, "record not found"):
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "not_found",
			"message": msg,
		})
	case strings.Contains(lower, "invalid range"), strings.Contains(lower, "not configured"):
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "metrics_unavailable",
			"message": msg,
		})
	case strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "timeout"):
		c.JSON(http.StatusGatewayTimeout, gin.H{
			"error":   "metrics_unavailable",
			"message": msg,
		})
	default:
		c.JSON(http.StatusBadGateway, gin.H{
			"error":   "metrics_unavailable",
			"message": msg,
		})
	}
}

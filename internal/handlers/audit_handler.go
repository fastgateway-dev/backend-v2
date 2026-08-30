package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AuditHandler handles audit log endpoints
type AuditHandler struct {
	auditService services.AuditServiceInterface
}

// NewAuditHandler creates a new audit handler
func NewAuditHandler(auditService services.AuditServiceInterface) *AuditHandler {
	return &AuditHandler{
		auditService: auditService,
	}
}

// List lists audit logs for a project
func (h *AuditHandler) List(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	resourceType := c.Query("resourceType")
	action := c.Query("action")

	var userID *uuid.UUID
	if userIDStr := c.Query("userId"); userIDStr != "" {
		id, err := uuid.Parse(userIDStr)
		if err == nil {
			userID = &id
		}
	}

	logs, total, err := h.auditService.ListByProjectID(projectID, page, limit, resourceType, action, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": logs,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// Export exports audit logs as CSV
func (h *AuditHandler) Export(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	resourceType := c.Query("resourceType")
	action := c.Query("action")

	var userID *uuid.UUID
	if userIDStr := c.Query("userId"); userIDStr != "" {
		id, err := uuid.Parse(userIDStr)
		if err == nil {
			userID = &id
		}
	}

	logs, err := h.auditService.ExportByProjectID(projectID, resourceType, action, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=audit-log.csv")

	writer := csv.NewWriter(c.Writer)
	defer writer.Flush()

	// Write header
	writer.Write([]string{"Timestamp", "Username", "Action", "Resource Type", "Resource Name", "Resource ID", "IP Address", "Details"})

	for _, log := range logs {
		resourceID := ""
		if log.ResourceID != nil {
			resourceID = log.ResourceID.String()
		}

		details := ""
		if log.Details != nil {
			detailBytes, err := log.Details.Value()
			if err == nil && detailBytes != nil {
				if b, ok := detailBytes.([]byte); ok {
					details = string(b)
				}
			}
		}

		writer.Write([]string{
			log.CreatedAt.Format("2006-01-02T15:04:05Z"),
			log.Username,
			log.Action,
			log.ResourceType,
			log.ResourceName,
			resourceID,
			log.IPAddress,
			details,
		})
	}
}

// Cleanup deletes audit logs older than the specified number of days
func (h *AuditHandler) Cleanup(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var req struct {
		Days int `json:"days" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body, 'days' field is required"})
		return
	}

	if req.Days < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "days must be at least 1"})
		return
	}

	deleted, err := h.auditService.CleanupOlderThan(projectID, req.Days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"deleted": deleted,
		"message": fmt.Sprintf("Deleted %d audit logs older than %d days", deleted, req.Days),
	})
}

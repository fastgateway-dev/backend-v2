package handlers

import (
	"net/http"
	"strconv"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ApprovalHandler handles approval endpoints
type ApprovalHandler struct {
	approvalService services.ApprovalServiceInterface
	auditService    services.AuditServiceInterface
}

// NewApprovalHandler creates a new approval handler
func NewApprovalHandler(approvalService services.ApprovalServiceInterface, auditService services.AuditServiceInterface) *ApprovalHandler {
	return &ApprovalHandler{
		approvalService: approvalService,
		auditService:    auditService,
	}
}

// List lists approval requests for a project
func (h *ApprovalHandler) List(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	status := c.DefaultQuery("status", "pending")
	entityType := c.DefaultQuery("entityType", "")

	approvals, total, err := h.approvalService.ListByProjectID(projectID, page, limit, status, entityType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": approvals,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// Get gets an approval request by ID
func (h *ApprovalHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	approval, err := h.approvalService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Approval request not found"})
		return
	}

	c.JSON(http.StatusOK, approval)
}

// GetDiff gets the YAML diff for an approval request
func (h *ApprovalHandler) GetDiff(c *gin.Context) {
	id, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	diff, err := h.approvalService.GetDiff(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, diff)
}

// Approve approves a specific stage of an approval request
func (h *ApprovalHandler) Approve(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	stageID, err := uuid.Parse(c.Param("stageId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid stage ID"})
		return
	}

	approval, err := h.approvalService.ApproveStage(id, stageID, user)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	entityID := approval.EntityID
	h.auditService.LogAction(
		&projectID,
		user,
		"approve_stage",
		"approval",
		&entityID,
		"",
		map[string]interface{}{"approvalId": approval.ID, "action": approval.Action, "stageId": stageID},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, approval)
}

// RejectRequest represents the reject request body
type RejectRequest struct {
	Comment string `json:"comment" binding:"required"`
}

// Reject rejects a specific stage of an approval request
func (h *ApprovalHandler) Reject(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	stageID, err := uuid.Parse(c.Param("stageId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid stage ID"})
		return
	}

	var req RejectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	approval, err := h.approvalService.RejectStage(id, stageID, user, req.Comment)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	entityID := approval.EntityID
	h.auditService.LogAction(
		&projectID,
		user,
		"reject_stage",
		"approval",
		&entityID,
		"",
		map[string]interface{}{
			"approvalId": approval.ID,
			"action":     approval.Action,
			"stageId":    stageID,
			"comment":    req.Comment,
		},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, approval)
}

// Cancel cancels an approval request
func (h *ApprovalHandler) Cancel(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	approval, err := h.approvalService.CancelApproval(id, user)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	entityID := approval.EntityID
	h.auditService.LogAction(
		&projectID,
		user,
		"cancel_approval",
		"approval",
		&entityID,
		"",
		map[string]interface{}{"approvalId": approval.ID, "action": approval.Action},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, approval)
}

package handlers

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CommentHandler handles approval comment HTTP requests
type CommentHandler struct {
	commentService services.CommentServiceInterface
}

// NewCommentHandler creates a new comment handler
func NewCommentHandler(commentService services.CommentServiceInterface) *CommentHandler {
	return &CommentHandler{commentService: commentService}
}

// Create creates a new comment on an approval
func (h *CommentHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	approvalID, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	var req struct {
		Body string `json:"body" binding:"required,max=10000"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Comment body is required"})
		return
	}

	comment, err := h.commentService.Create(approvalID, user, req.Body)
	if err != nil {
		if err.Error() == "approval not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, comment)
}

// List lists comments for an approval
func (h *CommentHandler) List(c *gin.Context) {
	approvalID, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	comments, err := h.commentService.ListByApprovalID(approvalID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	count, _ := h.commentService.CountByApprovalID(approvalID)

	c.JSON(http.StatusOK, gin.H{
		"data":  comments,
		"total": count,
	})
}

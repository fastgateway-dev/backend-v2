package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AIHandler handles AI-related endpoints
type AIHandler struct {
	aiService       services.AIServiceInterface
	approvalService services.ApprovalServiceInterface
	domainService   services.DomainServiceInterface
}

// NewAIHandler creates a new AI handler
func NewAIHandler(aiService services.AIServiceInterface, approvalService services.ApprovalServiceInterface, domainService services.DomainServiceInterface) *AIHandler {
	return &AIHandler{
		aiService:       aiService,
		approvalService: approvalService,
		domainService:   domainService,
	}
}

// GetStatus returns the AI service status
func (h *AIHandler) GetStatus(c *gin.Context) {
	status := h.aiService.GetStatus()
	c.JSON(http.StatusOK, status)
}

// GenerateRequest represents the request body for route generation
type GenerateRequest struct {
	Mode       string `json:"mode" binding:"required,oneof=natural_language manifest_import"`
	Input      string `json:"input" binding:"required"`
	FormatHint string `json:"formatHint,omitempty" binding:"omitempty,oneof=ingress istio kong"`
}

// Generate handles AI route generation with SSE streaming
func (h *AIHandler) Generate(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	if !h.aiService.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI service is not available"})
		return
	}

	domainID, err := uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return
	}

	var req GenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get domain context
	domain, err := h.domainService.GetByID(domainID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Domain not found"})
		return
	}

	// Build AI request
	aiReq := ai.GenerateRequest{
		Mode:       ai.GenerateMode(req.Mode),
		Input:      req.Input,
		FormatHint: req.FormatHint,
		Domain: &ai.DomainContext{
			ID:       domain.ID.String(),
			Name:     domain.Name,
			Hostname: domain.Hostname,
		},
	}

	// Start generation
	chunks, err := h.aiService.Generate(c.Request.Context(), user.ID, aiReq)
	if err != nil {
		if err.Error() == "rate limit exceeded, please try again later" {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	c.Stream(func(w io.Writer) bool {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return false
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

// AIReviewRequest is the request body for AI review
type AIReviewRequest struct {
	Action       string      `json:"action" binding:"required,oneof=create update delete"`
	Description  string      `json:"description"`
	ProposedYaml *ai.YamlSet `json:"proposedYaml"`
	CurrentYaml  *ai.YamlSet `json:"currentYaml"`
}

// Review handles AI review requests
func (h *AIHandler) Review(c *gin.Context) {
	if !h.aiService.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI service is not available"})
		return
	}

	var req AIReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.ProposedYaml == nil && req.CurrentYaml == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one of proposedYaml or currentYaml is required"})
		return
	}

	user := middleware.GetCurrentUser(c)

	reviewReq := ai.ReviewRequest{
		Action:       req.Action,
		Description:  req.Description,
		ProposedYaml: req.ProposedYaml,
		CurrentYaml:  req.CurrentYaml,
	}

	result, err := h.aiService.Review(c.Request.Context(), user.ID, reviewReq)
	if err != nil {
		if err.Error() == "rate limit exceeded, please try again later" {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// ReviewApproval triggers AI review for an existing approval
func (h *AIHandler) ReviewApproval(c *gin.Context) {
	if !h.aiService.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI service is not available"})
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	approvalID, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	user := middleware.GetCurrentUser(c)

	approval, err := h.approvalService.GetByID(approvalID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Approval not found"})
		return
	}

	// Verify approval belongs to the project in the URL
	if approval.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Approval not found"})
		return
	}

	if approval.AIReview != nil && len(approval.AIReview) > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "AI review already exists for this approval"})
		return
	}

	diff, err := h.approvalService.GetDiff(approvalID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get approval diff"})
		return
	}

	result, err := h.aiService.ReviewApproval(c.Request.Context(), user.ID, approval, diff)
	if err != nil {
		if err.Error() == "rate limit exceeded, please try again later" {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI review failed"})
		return
	}

	if err := h.approvalService.UpdateAIReview(approval); err != nil {
		if err.Error() == "AI review already exists for this approval" {
			c.JSON(http.StatusConflict, gin.H{"error": "AI review already exists for this approval"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save AI review"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// ChatRequestBody is the request body for AI chat
type ChatRequestBody struct {
	Message string           `json:"message" binding:"required,max=10000"`
	Context *ai.ChatContext  `json:"context"`
	History []ai.ChatMessage `json:"history" binding:"max=50"`
}

// Chat handles AI chat with SSE streaming
func (h *AIHandler) Chat(c *gin.Context) {
	user := middleware.GetCurrentUser(c)

	if !h.aiService.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI service is not available"})
		return
	}

	var req ChatRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	chatReq := ai.ChatRequest{
		Message: req.Message,
		Context: req.Context,
		History: req.History,
	}

	chunks, err := h.aiService.Chat(c.Request.Context(), user.ID, chatReq)
	if err != nil {
		if err.Error() == "rate limit exceeded, please try again later" {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	c.Stream(func(w io.Writer) bool {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return false
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

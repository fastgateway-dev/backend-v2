package handlers

import (
	"net/http"
	"strconv"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ClientHandler handles client endpoints
type ClientHandler struct {
	clientService services.ClientServiceInterface
	auditService  services.AuditServiceInterface
	perms         *middleware.PermissionChecker
}

// NewClientHandler creates a new client handler
func NewClientHandler(
	clientService services.ClientServiceInterface,
	auditService services.AuditServiceInterface,
	perms *middleware.PermissionChecker,
) *ClientHandler {
	return &ClientHandler{
		clientService: clientService,
		auditService:  auditService,
		perms:         perms,
	}
}

// List lists clients
// Owner sees all, others see clients belonging to their teams
func (h *ClientHandler) List(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	// Optional team filter
	var teamID *uuid.UUID
	if teamIDStr := c.Query("teamId"); teamIDStr != "" {
		parsed, err := uuid.Parse(teamIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team ID"})
			return
		}
		teamID = &parsed
	}

	clients, total, err := h.clientService.List(page, limit, teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": clients,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// Create creates a new client
func (h *ClientHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	var input services.CreateClientInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Permission: owner or member of the specified team
	if !h.perms.CanAccessTeamResource(input.TeamID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: you must be a member of the specified team"})
		return
	}

	client, err := h.clientService.Create(&input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log (no project context for global clients)
	h.auditService.LogAction(
		nil,
		user,
		"create",
		"client",
		&client.ID,
		client.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, client)
}

// Get gets a client by ID
func (h *ClientHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("clientId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid client ID"})
		return
	}

	client, err := h.clientService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	c.JSON(http.StatusOK, client)
}

// Update updates a client
func (h *ClientHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	id, err := uuid.Parse(c.Param("clientId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid client ID"})
		return
	}

	// Check permission: owner or team member
	client, err := h.clientService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	if !h.perms.CanAccessTeamResource(client.TeamID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	var input services.UpdateClientInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := h.clientService.Update(id, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"update",
		"client",
		&id,
		updated.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, updated)
}

// Delete deletes a client
func (h *ClientHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	id, err := uuid.Parse(c.Param("clientId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid client ID"})
		return
	}

	// Check permission: owner or team member
	client, err := h.clientService.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	if !h.perms.CanAccessTeamResource(client.TeamID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	if err := h.clientService.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"delete",
		"client",
		&id,
		client.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// SetAllowedMethods sets the allowed HTTP methods for a client
func (h *ClientHandler) SetAllowedMethods(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	clientID, err := uuid.Parse(c.Param("clientId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid client ID"})
		return
	}

	// Check permission: owner or team member
	client, err := h.clientService.GetByID(clientID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	if !h.perms.CanAccessTeamResource(client.TeamID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	var input struct {
		Methods []string `json:"methods"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	updated, err := h.clientService.SetAllowedMethods(clientID, input.Methods)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"set_allowed_methods",
		"client",
		&clientID,
		client.Name,
		map[string]interface{}{"methods": input.Methods},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, updated)
}

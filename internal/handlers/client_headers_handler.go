package handlers

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListHeaders lists headers for a client
func (h *ClientHandler) ListHeaders(c *gin.Context) {
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

	headers, err := h.clientService.ListHeaders(clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, headers)
}

// AddHeader adds a header to a client
func (h *ClientHandler) AddHeader(c *gin.Context) {
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

	var input services.CreateClientHeaderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	header, err := h.clientService.AddHeader(clientID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"add_header",
		"client",
		&clientID,
		client.Name,
		map[string]interface{}{"headerName": input.Name, "values": input.Values},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, header)
}

// RemoveHeader removes a header from a client
func (h *ClientHandler) RemoveHeader(c *gin.Context) {
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

	headerID, err := uuid.Parse(c.Param("headerId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid header ID"})
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

	if err := h.clientService.RemoveHeader(clientID, headerID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"remove_header",
		"client",
		&clientID,
		client.Name,
		map[string]interface{}{"headerId": headerID},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

package handlers

import (
	"net/http"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GenerateAPIKey generates a new API key for a client
func (h *ClientHandler) GenerateAPIKey(c *gin.Context) {
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

	var input services.GenerateAPIKeyInput
	// Input is optional, so we don't fail if body is empty
	_ = c.ShouldBindJSON(&input)

	response, err := h.clientService.GenerateAPIKey(c.Request.Context(), clientID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"generate_api_key",
		"client",
		&clientID,
		client.Name,
		map[string]interface{}{"headerName": response.HeaderName},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, response)
}

// RevokeAPIKey revokes the API key for a client
func (h *ClientHandler) RevokeAPIKey(c *gin.Context) {
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

	if err := h.clientService.RevokeAPIKey(c.Request.Context(), clientID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"revoke_api_key",
		"client",
		&clientID,
		client.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// ConfigureJWT configures JWT authentication for a client
func (h *ClientHandler) ConfigureJWT(c *gin.Context) {
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

	var input services.ConfigureJWTInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.clientService.ConfigureJWT(c.Request.Context(), clientID, &input, user.ID)
	if err != nil {
		if strings.HasPrefix(err.Error(), "internal:") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"configure_jwt",
		"client",
		&clientID,
		client.Name,
		map[string]interface{}{"issuer": input.Issuer},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, response)
}

// UpdateJWT updates JWT authentication for a client
func (h *ClientHandler) UpdateJWT(c *gin.Context) {
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

	// Check if JWT is already configured
	if !client.JWTEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "JWT is not configured for this client"})
		return
	}

	var input services.ConfigureJWTInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.clientService.ConfigureJWT(c.Request.Context(), clientID, &input, user.ID)
	if err != nil {
		if strings.HasPrefix(err.Error(), "internal:") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"update_jwt",
		"client",
		&clientID,
		client.Name,
		map[string]interface{}{"issuer": input.Issuer},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, response)
}

// RemoveJWT removes JWT authentication from a client
func (h *ClientHandler) RemoveJWT(c *gin.Context) {
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

	if err := h.clientService.RemoveJWT(c.Request.Context(), clientID); err != nil {
		if strings.HasPrefix(err.Error(), "internal:") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"remove_jwt",
		"client",
		&clientID,
		client.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// UpdateClientMTLS updates client mTLS configuration
// PUT /clients/:clientId/mtls
func (h *ClientHandler) UpdateClientMTLS(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	clientID, err := uuid.Parse(c.Param("clientId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid client ID"})
		return
	}

	// Check team membership
	existingClient, err := h.clientService.GetByID(clientID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	isMember, _ := h.perms.IsTeamMember(existingClient.TeamID, user.ID)
	if !isMember {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: you must be a member of the client's team"})
		return
	}

	var input services.UpdateClientMTLSInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, err := h.clientService.UpdateClientMTLS(c.Request.Context(), clientID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	action := "update_mtls"
	if !input.Enabled {
		action = "disable_mtls"
	}
	h.auditService.LogAction(
		nil,
		user,
		action,
		"client",
		&clientID,
		client.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, client)
}

// DeleteClientMTLS disables client mTLS
// DELETE /clients/:clientId/mtls
func (h *ClientHandler) DeleteClientMTLS(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	clientID, err := uuid.Parse(c.Param("clientId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid client ID"})
		return
	}

	// Check team membership
	existingClient, err := h.clientService.GetByID(clientID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	isMember, _ := h.perms.IsTeamMember(existingClient.TeamID, user.ID)
	if !isMember {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: you must be a member of the client's team"})
		return
	}

	client, err := h.clientService.UpdateClientMTLS(c.Request.Context(), clientID, &services.UpdateClientMTLSInput{
		Enabled: false,
	}, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"remove_mtls",
		"client",
		&clientID,
		client.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

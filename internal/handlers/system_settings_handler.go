package handlers

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
)

// SystemSettingsHandler handles system settings endpoints
type SystemSettingsHandler struct {
	settingsService services.SystemSettingsServiceInterface
}

// NewSystemSettingsHandler creates a new system settings handler
func NewSystemSettingsHandler(settingsService services.SystemSettingsServiceInterface) *SystemSettingsHandler {
	return &SystemSettingsHandler{
		settingsService: settingsService,
	}
}

// Get returns the current system settings with effective values
func (h *SystemSettingsHandler) Get(c *gin.Context) {
	response, err := h.settingsService.GetResponse()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load system settings"})
		return
	}

	c.JSON(http.StatusOK, response)
}

// Update updates the system settings
func (h *SystemSettingsHandler) Update(c *gin.Context) {
	var input services.SystemSettingsInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.settingsService.Update(input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

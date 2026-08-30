package handlers

import (
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PresetHandler handles HTTP requests for permission presets
type PresetHandler struct {
	presetService services.PresetServiceInterface
	auditService  services.AuditServiceInterface
}

// NewPresetHandler creates a new preset handler
func NewPresetHandler(presetService services.PresetServiceInterface, auditService services.AuditServiceInterface) *PresetHandler {
	return &PresetHandler{
		presetService: presetService,
		auditService:  auditService,
	}
}

// List lists all presets for a project
func (h *PresetHandler) List(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	presets, err := h.presetService.ListByProject(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list presets"})
		return
	}

	c.JSON(http.StatusOK, presets)
}

// Get gets a preset by ID
func (h *PresetHandler) Get(c *gin.Context) {
	presetID, err := uuid.Parse(c.Param("presetId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid preset ID"})
		return
	}

	preset, err := h.presetService.GetByID(presetID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Preset not found"})
		return
	}

	c.JSON(http.StatusOK, preset)
}

// Create creates a new preset
func (h *PresetHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	var input services.CreatePresetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	preset, err := h.presetService.Create(projectID, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"create",
		"preset",
		&preset.ID,
		preset.Name,
		map[string]interface{}{"permissions": preset.Permissions},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, preset)
}

// Update updates a preset
func (h *PresetHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	presetID, err := uuid.Parse(c.Param("presetId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid preset ID"})
		return
	}

	var input services.UpdatePresetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	preset, err := h.presetService.Update(projectID, presetID, &input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"update",
		"preset",
		&preset.ID,
		preset.Name,
		map[string]interface{}{"permissions": preset.Permissions},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, preset)
}

// Delete deletes a preset
func (h *PresetHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	presetID, err := uuid.Parse(c.Param("presetId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid preset ID"})
		return
	}

	// Get preset name for audit log before deleting
	preset, err := h.presetService.GetByID(presetID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Preset not found"})
		return
	}
	presetName := preset.Name

	if err := h.presetService.Delete(projectID, presetID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"delete",
		"preset",
		&presetID,
		presetName,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

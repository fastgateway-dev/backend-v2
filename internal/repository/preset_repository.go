package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PresetRepository handles permission preset database operations
type PresetRepository struct {
	db *gorm.DB
}

// NewPresetRepository creates a new preset repository
func NewPresetRepository(db *gorm.DB) *PresetRepository {
	return &PresetRepository{db: db}
}

// Create creates a new permission preset
func (r *PresetRepository) Create(preset *models.PermissionPreset) error {
	return r.db.Create(preset).Error
}

// GetByID gets a preset by ID
func (r *PresetRepository) GetByID(id uuid.UUID) (*models.PermissionPreset, error) {
	var preset models.PermissionPreset
	err := r.db.Where("id = ?", id).First(&preset).Error
	if err != nil {
		return nil, err
	}
	return &preset, nil
}

// GetByProjectAndName gets a preset by project ID and name
func (r *PresetRepository) GetByProjectAndName(projectID uuid.UUID, name string) (*models.PermissionPreset, error) {
	var preset models.PermissionPreset
	err := r.db.Where("project_id = ? AND name = ?", projectID, name).First(&preset).Error
	if err != nil {
		return nil, err
	}
	return &preset, nil
}

// ListByProject lists all presets for a project
func (r *PresetRepository) ListByProject(projectID uuid.UUID) ([]models.PermissionPreset, error) {
	var presets []models.PermissionPreset
	err := r.db.Where("project_id = ?", projectID).
		Order("is_builtin DESC, name ASC").
		Find(&presets).Error
	return presets, err
}

// Update updates a preset
func (r *PresetRepository) Update(preset *models.PermissionPreset) error {
	return r.db.Save(preset).Error
}

// Delete deletes a preset
func (r *PresetRepository) Delete(id uuid.UUID) error {
	// Junction table entries will be deleted by CASCADE
	return r.db.Delete(&models.PermissionPreset{}, "id = ?", id).Error
}

// IsPresetInUse checks if a preset is assigned to any team
func (r *PresetRepository) IsPresetInUse(presetID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.ProjectTeamPreset{}).
		Where("preset_id = ?", presetID).
		Count(&count).Error
	return count > 0, err
}

// SeedBuiltinPresets seeds the built-in presets for a new project
func (r *PresetRepository) SeedBuiltinPresets(projectID uuid.UUID) error {
	presets := []models.PermissionPreset{
		{
			ProjectID:   projectID,
			Name:        "Viewer",
			Description: "Read-only access to routes, clients, and domains",
			Permissions: []string{"route.view", "client.view", "domain.view"},
			IsBuiltin:   true,
		},
		{
			ProjectID:   projectID,
			Name:        "Editor",
			Description: "Can create and edit routes, clients, and domains",
			Permissions: []string{
				"route.view", "route.create", "route.edit", "route.delete", "route.deploy",
				"client.view", "client.create", "client.edit", "client.manage_ip", "client.manage_apikey", "client.manage_jwt", "client.attach", "client.detach",
				"domain.view", "domain.create", "domain.edit",
			},
			IsBuiltin: true,
		},
		{
			ProjectID:   projectID,
			Name:        "Approver",
			Description: "Can approve or reject route and client changes",
			Permissions: []string{"route.view", "route.approve", "client.view", "client.approve", "domain.view"},
			IsBuiltin:   true,
		},
		{
			ProjectID:   projectID,
			Name:        "Admin",
			Description: "Full project administration access",
			Permissions: []string{
				"route.view", "route.create", "route.edit", "route.delete", "route.deploy", "route.approve",
				"client.view", "client.create", "client.edit", "client.delete", "client.manage_ip", "client.manage_apikey", "client.manage_jwt", "client.attach", "client.detach", "client.approve",
				"domain.view", "domain.create", "domain.edit", "domain.delete",
				"project.settings", "project.teams", "project.approval_policy",
				"audit.view",
			},
			IsBuiltin: true,
		},
	}

	for _, preset := range presets {
		if err := r.db.Create(&preset).Error; err != nil {
			return err
		}
	}
	return nil
}

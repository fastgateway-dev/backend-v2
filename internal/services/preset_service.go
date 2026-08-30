package services

import (
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// PresetService handles permission preset business logic
type PresetService struct {
	presetRepo repository.PresetRepositoryInterface
}

// NewPresetService creates a new preset service
func NewPresetService(presetRepo repository.PresetRepositoryInterface) *PresetService {
	return &PresetService{
		presetRepo: presetRepo,
	}
}

// CreatePresetInput represents input for creating a preset
type CreatePresetInput struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions" binding:"required,min=1"`
}

// UpdatePresetInput represents input for updating a preset
type UpdatePresetInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// Create creates a new permission preset
func (s *PresetService) Create(projectID uuid.UUID, input *CreatePresetInput) (*models.PermissionPreset, error) {
	// Validate permissions
	for _, p := range input.Permissions {
		if !models.IsValidPermission(p) {
			return nil, fmt.Errorf("invalid permission: %s", p)
		}
	}

	// Check for duplicate name
	existing, _ := s.presetRepo.GetByProjectAndName(projectID, input.Name)
	if existing != nil {
		return nil, errors.New("a preset with this name already exists")
	}

	preset := &models.PermissionPreset{
		ProjectID:   projectID,
		Name:        input.Name,
		Description: input.Description,
		Permissions: pq.StringArray(input.Permissions),
		IsBuiltin:   false,
	}

	if err := s.presetRepo.Create(preset); err != nil {
		return nil, err
	}

	return preset, nil
}

// GetByID gets a preset by ID
func (s *PresetService) GetByID(id uuid.UUID) (*models.PermissionPreset, error) {
	return s.presetRepo.GetByID(id)
}

// ListByProject lists all presets for a project
func (s *PresetService) ListByProject(projectID uuid.UUID) ([]models.PermissionPreset, error) {
	return s.presetRepo.ListByProject(projectID)
}

// Update updates a preset
func (s *PresetService) Update(projectID, id uuid.UUID, input *UpdatePresetInput) (*models.PermissionPreset, error) {
	preset, err := s.presetRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Verify preset belongs to this project
	if preset.ProjectID != projectID {
		return nil, errors.New("preset not found")
	}

	// Cannot modify built-in presets (except description)
	if preset.IsBuiltin && (input.Name != "" || len(input.Permissions) > 0) {
		return nil, errors.New("cannot modify name or permissions of built-in presets")
	}

	if input.Name != "" {
		// Check for duplicate name
		existing, _ := s.presetRepo.GetByProjectAndName(preset.ProjectID, input.Name)
		if existing != nil && existing.ID != preset.ID {
			return nil, errors.New("a preset with this name already exists")
		}
		preset.Name = input.Name
	}

	if input.Description != "" {
		preset.Description = input.Description
	}

	if len(input.Permissions) > 0 {
		// Validate permissions
		for _, p := range input.Permissions {
			if !models.IsValidPermission(p) {
				return nil, fmt.Errorf("invalid permission: %s", p)
			}
		}
		preset.Permissions = pq.StringArray(input.Permissions)
	}

	if err := s.presetRepo.Update(preset); err != nil {
		return nil, err
	}

	return preset, nil
}

// Delete deletes a preset
func (s *PresetService) Delete(projectID, id uuid.UUID) error {
	preset, err := s.presetRepo.GetByID(id)
	if err != nil {
		return err
	}

	// Verify preset belongs to this project
	if preset.ProjectID != projectID {
		return errors.New("preset not found")
	}

	if preset.IsBuiltin {
		return errors.New("cannot delete built-in presets")
	}

	// Check if preset is in use
	inUse, err := s.presetRepo.IsPresetInUse(id)
	if err != nil {
		return err
	}
	if inUse {
		return errors.New("cannot delete preset that is assigned to teams")
	}

	return s.presetRepo.Delete(id)
}

// SeedBuiltinPresets seeds the built-in presets for a new project
func (s *PresetService) SeedBuiltinPresets(projectID uuid.UUID) error {
	return s.presetRepo.SeedBuiltinPresets(projectID)
}

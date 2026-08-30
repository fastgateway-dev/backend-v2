package services

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// ApprovalPolicyInput is the request body for creating/updating a policy
type ApprovalPolicyInput struct {
	EntityType string             `json:"entityType" binding:"required"`
	Action     *string            `json:"action"`
	Stages     []PolicyStageInput `json:"stages" binding:"required"`
}

// PolicyStageInput is one stage in a policy input
type PolicyStageInput struct {
	Order              int    `json:"order"`
	RequiredPermission string `json:"requiredPermission" binding:"required"`
	TeamScope          string `json:"teamScope" binding:"required"`
	MinApprovers       int    `json:"minApprovers"`
}

// ApprovalPolicyService handles approval policy CRUD
type ApprovalPolicyService struct {
	policyRepo repository.ApprovalPolicyRepositoryInterface
}

// NewApprovalPolicyService creates a new approval policy service
func NewApprovalPolicyService(policyRepo repository.ApprovalPolicyRepositoryInterface) *ApprovalPolicyService {
	return &ApprovalPolicyService{policyRepo: policyRepo}
}

// List returns all policies for a project
func (s *ApprovalPolicyService) List(projectID uuid.UUID) ([]models.ApprovalPolicy, error) {
	return s.policyRepo.ListByProjectID(projectID)
}

// Get returns a single policy, validating project ownership
func (s *ApprovalPolicyService) Get(projectID uuid.UUID, policyID uuid.UUID) (*models.ApprovalPolicy, error) {
	policy, err := s.policyRepo.GetByID(policyID)
	if err != nil {
		return nil, err
	}
	if policy.ProjectID != projectID {
		return nil, errors.New("policy not found")
	}
	return policy, nil
}

// Create creates a new approval policy after validation
func (s *ApprovalPolicyService) Create(projectID uuid.UUID, input ApprovalPolicyInput) (*models.ApprovalPolicy, error) {
	if err := s.validateInput(input); err != nil {
		return nil, err
	}

	// Check for duplicate (entity_type, action)
	existing, err := s.policyRepo.GetByProjectAndEntity(projectID, input.EntityType, input.Action)
	if err == nil && existing != nil {
		return nil, errors.New("policy for this entity type and action already exists")
	}

	stages := s.buildStageTemplates(input.Stages)
	stagesJSON, err := json.Marshal(stages)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal stages: %w", err)
	}

	policy := &models.ApprovalPolicy{
		ProjectID:  projectID,
		EntityType: models.ApprovalEntityType(input.EntityType),
		Action:     input.Action,
		Stages:     stagesJSON,
	}

	if err := s.policyRepo.Create(policy); err != nil {
		return nil, err
	}
	return policy, nil
}

// Update updates an existing approval policy
func (s *ApprovalPolicyService) Update(projectID uuid.UUID, policyID uuid.UUID, input ApprovalPolicyInput) (*models.ApprovalPolicy, error) {
	if err := s.validateInput(input); err != nil {
		return nil, err
	}

	policy, err := s.policyRepo.GetByID(policyID)
	if err != nil {
		return nil, err
	}
	if policy.ProjectID != projectID {
		return nil, errors.New("policy not found")
	}

	stages := s.buildStageTemplates(input.Stages)
	stagesJSON, err := json.Marshal(stages)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal stages: %w", err)
	}

	policy.Stages = stagesJSON
	if err := s.policyRepo.Update(policy); err != nil {
		return nil, err
	}
	return policy, nil
}

// Delete removes an approval policy
func (s *ApprovalPolicyService) Delete(projectID uuid.UUID, policyID uuid.UUID) error {
	policy, err := s.policyRepo.GetByID(policyID)
	if err != nil {
		return err
	}
	if policy.ProjectID != projectID {
		return errors.New("policy not found")
	}
	return s.policyRepo.Delete(policyID)
}

var validTeamScopes = map[string]bool{
	"any":            true,
	"other_team":     true,
	"submitter_team": true,
}

func (s *ApprovalPolicyService) validateInput(input ApprovalPolicyInput) error {
	if len(input.Stages) == 0 {
		return errors.New("at least 1 stage is required")
	}

	validEntityTypes := map[string]bool{
		string(models.ApprovalEntityRoute):            true,
		string(models.ApprovalEntityClientAttachment): true,
	}
	if !validEntityTypes[input.EntityType] {
		return fmt.Errorf("invalid entity type: %s", input.EntityType)
	}

	// Build valid permission set
	validPermissions := make(map[string]bool)
	for _, p := range models.AllPermissions {
		validPermissions[string(p)] = true
	}

	for _, stage := range input.Stages {
		if !validPermissions[stage.RequiredPermission] {
			return fmt.Errorf("invalid permission: %s", stage.RequiredPermission)
		}
		if !validTeamScopes[stage.TeamScope] {
			return fmt.Errorf("invalid team scope: %s", stage.TeamScope)
		}
	}
	return nil
}

func (s *ApprovalPolicyService) buildStageTemplates(inputs []PolicyStageInput) []models.PolicyStageTemplate {
	stages := make([]models.PolicyStageTemplate, len(inputs))
	for i, input := range inputs {
		minApprovers := models.EffectiveMinApprovers(input.MinApprovers)
		stages[i] = models.PolicyStageTemplate{
			Order:              input.Order,
			RequiredPermission: input.RequiredPermission,
			TeamScope:          input.TeamScope,
			MinApprovers:       minApprovers,
		}
	}
	return stages
}

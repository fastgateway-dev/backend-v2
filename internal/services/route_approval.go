package services

import (
	"encoding/json"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// buildRouteApprovalStages looks up the approval policy and builds approval stages for route approvals.
// It tries action-specific policy first, then falls back to default (action=nil).
func (s *RouteService) buildRouteApprovalStages(projectID uuid.UUID, submittedBy uuid.UUID, action string) []models.ApprovalStage {
	var stages []models.ApprovalStage
	if s.policyRepo != nil {
		var templates []models.PolicyStageTemplate

		// Step 1: Try action-specific policy
		policy, err := s.policyRepo.GetByProjectAndEntity(projectID, string(models.ApprovalEntityRoute), &action)
		if err != nil || policy == nil {
			// Step 2: Fall back to default policy (action = nil)
			policy, _ = s.policyRepo.GetByProjectAndEntity(projectID, string(models.ApprovalEntityRoute), nil)
		}

		if policy != nil {
			json.Unmarshal(policy.Stages, &templates)
		}

		for _, t := range templates {
			resolvedTeamID, _ := s.resolveTeamScope(t.TeamScope, projectID, submittedBy)
			stages = append(stages, models.ApprovalStage{
				StageOrder:         t.Order,
				RequiredPermission: t.RequiredPermission,
				RequiredTeamID:     resolvedTeamID,
				MinApprovers:       models.EffectiveMinApprovers(t.MinApprovers),
				Status:             models.ApprovalStatusPending,
			})
		}
	}
	if len(stages) == 0 {
		// Default fallback: single stage with route.approve permission
		stages = []models.ApprovalStage{{
			StageOrder:         1,
			RequiredPermission: string(models.PermRouteApprove),
			MinApprovers:       1,
			Status:             models.ApprovalStatusPending,
		}}
	}
	return stages
}

// resolveTeamScope resolves a team_scope string to a concrete team ID
func (s *RouteService) resolveTeamScope(scope string, projectID uuid.UUID, submittedBy uuid.UUID) (*uuid.UUID, error) {
	switch scope {
	case "any", "":
		return nil, nil
	case "submitter_team":
		ptrs, err := s.teamRepo.GetUserTeamsInProject(projectID, submittedBy)
		if err != nil || len(ptrs) == 0 {
			return nil, nil
		}
		teamID := ptrs[0].TeamID
		return &teamID, nil
	case "other_team":
		submitterPtrs, err := s.teamRepo.GetUserTeamsInProject(projectID, submittedBy)
		if err != nil {
			return nil, nil
		}
		submitterTeams := make(map[uuid.UUID]bool)
		for _, ptr := range submitterPtrs {
			submitterTeams[ptr.TeamID] = true
		}
		allPtrs, err := s.teamRepo.ListProjectTeams(projectID)
		if err != nil {
			return nil, nil
		}
		for _, ptr := range allPtrs {
			if !submitterTeams[ptr.TeamID] {
				teamID := ptr.TeamID
				return &teamID, nil
			}
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown team_scope: %s", scope)
	}
}

// GetApprovalIDForEntity returns the most recent approval ID for an entity.
// Checks pending first, then latest approved.
func (s *RouteService) GetApprovalIDForEntity(entityType models.ApprovalEntityType, entityID uuid.UUID) (*uuid.UUID, error) {
	// Try pending first
	pending, err := s.approvalRepo.GetPendingByEntityID(entityType, entityID)
	if err == nil && pending != nil {
		return &pending.ID, nil
	}
	// Fall back to latest approved
	approved, err := s.approvalRepo.GetLatestApprovedByEntityID(entityType, entityID)
	if err == nil && approved != nil {
		return &approved.ID, nil
	}
	return nil, fmt.Errorf("no approval found")
}

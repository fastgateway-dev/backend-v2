package approval

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// PlanStages builds the approval stages for a submission. It tries the
// action-specific policy first, then the project default.
//
// It returns an error rather than degrading: before Phase 2D a corrupt
// policy or a failed team lookup silently produced a weaker gate than the
// policy specified.
func (e *Engine) PlanStages(
	projectID, submittedBy uuid.UUID,
	entity models.ApprovalEntityType,
	action models.ApprovalAction,
) ([]models.ApprovalStage, error) {
	// The policy lookup is unconditional. It used to be wrapped in
	// `if e.policies != nil`, which meant an unwired policy store skipped
	// the lookup and fell through to the route default stage -- silently
	// replacing a project's real multi-stage policy with a weaker
	// single-stage gate.
	//
	// Removing that guard is safe only because New rejects a nil
	// PolicyStore, so an unwired store can never reach this function: it
	// panics at construction instead. That is what enforces the rule, and
	// it is pinned by TestNew_PanicsOnNilDependency/nil_policies -- no test
	// down here could detect the difference, because both the guarded and
	// unguarded forms behave identically for every non-nil store.
	var templates []models.PolicyStageTemplate

	// Policy lookup: action-specific first, falling back to the project
	// default. Since Phase 2G this distinguishes a genuine miss
	// (models.ErrPolicyNotFound) from a lookup FAILURE.
	//
	// Before Phase 2G both discarded their error outright ("err != nil ||
	// policy == nil" on the first call, "_" on the second), so a database
	// blip on either lookup was indistinguishable from the project simply
	// having no policy configured: both silently produced the same
	// single-stage route.approve fallback (or, for client_attachment, the
	// same "no policy found" error a genuine miss also produces) instead of
	// surfacing the failure. See
	// TestPlanStages_LookupErrorIsReturnedNotSwallowed (formerly
	// TestPlanStages_LookupErrorSilentlyYieldsDefaultGate) for the before/after.
	//
	// The action-specific -> project-default fallback CHAIN itself is
	// unchanged; only the error handling around it is.
	actionStr := string(action)
	policy, err := e.policies.GetByProjectAndEntity(projectID, string(entity), &actionStr)
	if errors.Is(err, models.ErrPolicyNotFound) {
		policy, err = e.policies.GetByProjectAndEntity(projectID, string(entity), nil)
		if errors.Is(err, models.ErrPolicyNotFound) {
			policy, err = nil, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("look up approval policy for project %s (%s): %w",
			projectID, entity, err)
	}

	if policy != nil {
		if err := json.Unmarshal(policy.Stages, &templates); err != nil {
			return nil, fmt.Errorf("approval policy for project %s (%s) is not valid JSON: %w",
				projectID, entity, err)
		}
	}

	var stages []models.ApprovalStage
	for _, t := range templates {
		teamID, err := resolveTeamScope(e.teams, t.TeamScope, projectID, submittedBy)
		if err != nil {
			return nil, fmt.Errorf("approval stage %d: %w", t.Order, err)
		}
		stages = append(stages, models.ApprovalStage{
			StageOrder:         t.Order,
			RequiredPermission: t.RequiredPermission,
			RequiredTeamID:     teamID,
			MinApprovers:       models.EffectiveMinApprovers(t.MinApprovers),
			Status:             models.ApprovalStatusPending,
		})
	}

	if len(stages) == 0 {
		return noPolicyFallback(entity, policy)
	}

	return stages, nil
}

// noPolicyFallback decides what happens when policy lookup produced no
// stage templates -- either no policy exists for this project/entity/action,
// or the policy that was found has an empty stage list.
//
// route and client_attachment deliberately diverge here. This is not an
// inconsistency to "clean up":
//
//   - route falls back to a single stage requiring route.approve. This
//     matches RouteService.buildRouteApprovalStages's pre-2D behaviour,
//     which treats an absent policy as "no policy configured", not a
//     failure.
//
//   - client_attachment returns an error instead of synthesising a stage.
//     ApprovalPolicyRepository.SeedDefaults seeds every project with a
//     TWO-stage client_attachment policy -- client.approve/other_team, then
//     client.approve/any -- specifically so a submitter's own team cannot be
//     the (only) approver of its own attachment change. A synthesised
//     single-stage client.approve fallback would silently replace that
//     two-stage cross-team gate with a strictly weaker one the moment a
//     project's policy went missing or was misconfigured down to zero
//     stages -- the exact silent-gate-widening defect this phase exists to
//     eliminate. ClientAttachmentService.createApproval never fell back
//     either: it returned "no approval policy found for client_attachment"
//     when no policy existed, or "approval policy has no stages defined"
//     when one existed but was empty. This function preserves both of those
//     error paths.
func noPolicyFallback(entity models.ApprovalEntityType, policy *models.ApprovalPolicy) ([]models.ApprovalStage, error) {
	if entity == models.ApprovalEntityClientAttachment {
		if policy == nil {
			return nil, errors.New("no approval policy found for client_attachment")
		}
		return nil, errors.New("approval policy has no stages defined")
	}

	return []models.ApprovalStage{{
		StageOrder:         1,
		RequiredPermission: string(models.PermRouteApprove),
		MinApprovers:       1,
		Status:             models.ApprovalStatusPending,
	}}, nil
}

// resolveTeamScope resolves a policy's team_scope to a required team.
//
// Returning (nil, nil) means "no team restriction". Only two paths do so
// deliberately: an explicit "any"/"" scope, and an "other_team" scope in a
// project with no other team. Every failure returns an error, because a
// silent failure here widens an approval gate.
func resolveTeamScope(teams TeamLookup, scope string, projectID, submittedBy uuid.UUID) (*uuid.UUID, error) {
	switch scope {
	case "any", "":
		return nil, nil

	case "submitter_team":
		ptrs, err := teams.GetUserTeamsInProject(projectID, submittedBy)
		if err != nil {
			return nil, fmt.Errorf("resolve submitter_team: %w", err)
		}
		if len(ptrs) == 0 {
			return nil, errors.New("resolve submitter_team: submitter is not a member of any team in this project")
		}
		teamID := ptrs[0].TeamID
		return &teamID, nil

	case "other_team":
		submitterPtrs, err := teams.GetUserTeamsInProject(projectID, submittedBy)
		if err != nil {
			return nil, fmt.Errorf("resolve other_team: look up submitter teams: %w", err)
		}
		submitterTeams := make(map[uuid.UUID]bool, len(submitterPtrs))
		for _, ptr := range submitterPtrs {
			submitterTeams[ptr.TeamID] = true
		}
		allPtrs, err := teams.ListProjectTeams(projectID)
		if err != nil {
			return nil, fmt.Errorf("resolve other_team: list project teams: %w", err)
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

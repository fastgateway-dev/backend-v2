package approval

// CHARACTERIZATION (Phase 2G Task 2, INVERTED by Task 3). Pins T7 from the
// phase brief: PolicyStore could not tell absence from failure.
//
// internal/approval/planning.go used to read (originally at :38-41):
//
//	policy, err := e.policies.GetByProjectAndEntity(projectID, string(entity), &actionStr)
//	if err != nil || policy == nil {
//	    policy, _ = e.policies.GetByProjectAndEntity(projectID, string(entity), nil)
//	}
//
// There were TWO discards here, not one:
//
//   - the action-specific lookup could not tell a DB failure from a genuine
//     miss, so an error there silently downgraded to the project default
//     lookup -- exactly like a real "no policy for this action" would.
//   - the default lookup discarded its own error outright ("_"), so a
//     failure on the DEFAULT lookup also yielded policy == nil, which
//     noPolicyFallback turned into a synthesised single-stage gate in place
//     of the project's real (possibly multi-stage, possibly cross-team)
//     policy.
//
// BEFORE Phase 2G, a database blip during PlanStages was completely
// indistinguishable from a project that simply has no approval policy
// configured: both produced a working, no-error single-stage route.approve
// gate. SINCE Phase 2G, models.ErrPolicyNotFound (returned by
// ApprovalPolicyRepository.GetByProjectAndEntity for a genuine
// gorm.ErrRecordNotFound, per the PolicyStore contract in ports.go)
// distinguishes the two: PlanStages now returns the lookup error on failure,
// while genuine absence still falls back to the same single-stage gate as
// before -- these tests are the fixture that proves that split.
//
// This file uses the stubPolicies and stubTeams types already defined in
// planning_test.go (same package, package approval) rather than inventing new
// ones, per the phase brief. stubPolicies' "not found" case was updated
// (planning_test.go) to return models.ErrPolicyNotFound instead of a plain
// error, per the PolicyStore contract -- see the addendum to the Task 3
// brief.

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Case 1: a lookup ERROR (not absence) on both the action-specific and
// default policy lookups is no longer swallowed into the same single-stage
// fallback a genuinely unconfigured project would get.
//
// BEFORE Phase 2G: no error was returned; a real connection failure looked
// identical to "no policy is configured for this project."
// SINCE Phase 2G: PlanStages returns the lookup error instead of degrading
// to the fallback -- a database blip on the policy lookup is now a hard
// failure, not a silently-widened approval gate.
func TestPlanStages_LookupErrorIsReturnedNotSwallowed(t *testing.T) {
	e := &Engine{policies: stubPolicies{err: errors.New("connection refused")}, teams: &stubTeams{}}

	stages, err := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityRoute, models.ApprovalActionCreate)

	require.Error(t, err,
		"SINCE Phase 2G: a database failure on BOTH the action-specific and "+
			"default policy lookups must be returned, not swallowed into the "+
			"single-stage fallback (was swallowed before Phase 2G).")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, stages)
}

// Case 2: genuine absence (no policy row exists for either the
// action-specific or the default lookup) still produces the same
// single-stage gate with no error -- this is not a bug, it is the existing,
// legitimate "no policy configured" behaviour, and Task 3's fix to Case 1
// preserves it unchanged.
//
// BEFORE Phase 2G, this was indistinguishable from Case 1 (a lookup FAILURE)
// purely by looking at PlanStages's return value -- both produced this same
// no-error single-stage gate, which was exactly the defect: nothing told you
// whether the project truly had no policy, or the lookup just failed. SINCE
// Phase 2G, Case 1 now returns an error while this case still does not --
// this test and Case 1 together are the fixture that proves the split.
//
// This is the "stubPolicies landmine" test from the Task 3 brief's addendum:
// stubPolicies' map-miss path must return models.ErrPolicyNotFound (not a
// plain error) for this genuine-absence case to still be classified as
// absence rather than failure. See planning_test.go.
//
// See also TestPlanStages_NoPolicyFallsBackToSingleStage in planning_test.go,
// which pins the same fallback outcome from the other pre-2D angle (route
// service's "no policy configured" case, not this phase's fail-open framing).
func TestPlanStages_GenuineAbsenceYieldsIndistinguishableDefaultGate(t *testing.T) {
	e := &Engine{
		policies: stubPolicies{byAction: map[string]*models.ApprovalPolicy{}},
		teams:    &stubTeams{},
	}

	stages, err := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityRoute, models.ApprovalActionCreate)

	require.NoError(t, err)
	require.Len(t, stages, 1,
		"genuine absence still produces the single-stage default gate, "+
			"unaffected by Task 3's fix to the lookup-FAILURE case (Case 1) above")
	assert.Equal(t, string(models.PermRouteApprove), stages[0].RequiredPermission)
}

// Case 3: a policy that IS found (for the specific action) is applied
// unaffected -- the discard at the old :39/:40 never fired on the happy
// path, and Task 3's fix does not disturb it.
func TestPlanStages_FoundPolicyIsAppliedUnaffected(t *testing.T) {
	tmpl, err := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "route.approve", TeamScope: "any", MinApprovers: 2},
	})
	require.NoError(t, err)

	e := &Engine{
		policies: stubPolicies{byAction: map[string]*models.ApprovalPolicy{
			"create": {Stages: tmpl},
		}},
		teams: &stubTeams{},
	}

	stages, planErr := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityRoute, models.ApprovalActionCreate)

	require.NoError(t, planErr)
	require.Len(t, stages, 1)
	assert.Equal(t, 2, stages[0].MinApprovers,
		"the real action-specific policy's MinApprovers must reach the caller, "+
			"not the synthetic fallback's hardcoded 1")
}

// Case 4: the action-specific -> project-default fallback CHAIN itself (the
// legitimate call at the old :40, as opposed to swallowing its error)
// survives Task 3's fix unchanged. An action-specific policy that is simply
// absent -- no row for this project/entity/action, but the project DOES
// have a default policy -- must still find and apply that default, not
// degrade to the single-stage synthetic fallback.
func TestPlanStages_ActionSpecificMissingFallsBackToProjectDefault(t *testing.T) {
	tmpl, err := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "route.approve", TeamScope: "any", MinApprovers: 2},
	})
	require.NoError(t, err)

	e := &Engine{
		policies: stubPolicies{byAction: map[string]*models.ApprovalPolicy{
			// Only the project default ("" key) exists; there is no
			// "create"-specific policy row.
			"": {Stages: tmpl},
		}},
		teams: &stubTeams{},
	}

	stages, planErr := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityRoute, models.ApprovalActionCreate)

	require.NoError(t, planErr)
	require.Len(t, stages, 1)
	assert.Equal(t, 2, stages[0].MinApprovers,
		"must come from the project's default policy (MinApprovers 2), not the "+
			"single-stage synthetic fallback (which always uses MinApprovers 1) -- "+
			"this chain survives Task 3's fix to the error-handling above it")
}

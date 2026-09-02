package approval

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTeams struct {
	userTeams    []models.ProjectTeamRole
	userTeamsErr error
	allTeams     []models.ProjectTeamRole
	allTeamsErr  error

	// Traversal-side behaviour (see traversal_test.go). hasPermErr /
	// isMemberErr let a test drive the lookup-failure paths;
	// permCalls records every permission consulted, which is how the
	// "empty RequiredPermission is still checked" test proves the call
	// happened at all.
	hasPerm     bool
	hasPermErr  error
	permCalls   []models.Permission
	isMember    bool
	isMemberErr error
}

func (s *stubTeams) GetUserTeamsInProject(uuid.UUID, uuid.UUID) ([]models.ProjectTeamRole, error) {
	return s.userTeams, s.userTeamsErr
}
func (s *stubTeams) ListProjectTeams(uuid.UUID) ([]models.ProjectTeamRole, error) {
	return s.allTeams, s.allTeamsErr
}
func (s *stubTeams) IsMember(uuid.UUID, uuid.UUID) (bool, error) {
	return s.isMember, s.isMemberErr
}
func (s *stubTeams) HasPermissionInProject(_, _ uuid.UUID, perm models.Permission) (bool, error) {
	s.permCalls = append(s.permCalls, perm)
	return s.hasPerm, s.hasPermErr
}

type stubPolicies struct {
	byAction map[string]*models.ApprovalPolicy
	err      error
}

func (s stubPolicies) GetByProjectAndEntity(_ uuid.UUID, _ string, action *string) (*models.ApprovalPolicy, error) {
	if s.err != nil {
		return nil, s.err
	}
	key := ""
	if action != nil {
		key = *action
	}
	p, ok := s.byAction[key]
	if !ok {
		// PolicyStore's contract (internal/approval/ports.go) requires
		// models.ErrPolicyNotFound for a genuine absence -- a plain
		// errors.New("not found") would be misclassified as a FAILURE by
		// PlanStages since Phase 2G. Not gorm.ErrRecordNotFound: this stub
		// implements the port, not the repository, and internal/approval
		// must never import gorm.
		return nil, models.ErrPolicyNotFound
	}
	return p, nil
}

func TestResolveTeamScope_AnyAndEmptyMeanNoRestriction(t *testing.T) {
	for _, scope := range []string{"any", ""} {
		got, err := resolveTeamScope(&stubTeams{}, scope, uuid.New(), uuid.New())
		require.NoError(t, err, "scope %q", scope)
		assert.Nil(t, got, "scope %q", scope)
	}
}

func TestResolveTeamScope_SubmitterTeamFailsClosed(t *testing.T) {
	teams := &stubTeams{userTeamsErr: errors.New("connection refused")}

	got, err := resolveTeamScope(teams, "submitter_team", uuid.New(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, got)
}

func TestResolveTeamScope_SubmitterTeamReturnsFirstTeam(t *testing.T) {
	want := uuid.New()
	teams := &stubTeams{userTeams: []models.ProjectTeamRole{{TeamID: want}}}

	got, err := resolveTeamScope(teams, "submitter_team", uuid.New(), uuid.New())

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want, *got)
}

func TestResolveTeamScope_OtherTeamPicksNonSubmitterTeam(t *testing.T) {
	mine, theirs := uuid.New(), uuid.New()
	teams := &stubTeams{
		userTeams: []models.ProjectTeamRole{{TeamID: mine}},
		allTeams:  []models.ProjectTeamRole{{TeamID: mine}, {TeamID: theirs}},
	}

	got, err := resolveTeamScope(teams, "other_team", uuid.New(), uuid.New())

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, theirs, *got)
}

// The three cases below came across from
// internal/services/route_approval_internal_test.go when
// RouteService.resolveTeamScope was deleted (Phase 2D Task 7). They pin the
// fail-closed paths and the one deliberately permissive path that the route
// copy had, against the surviving implementation.

func TestResolveTeamScope_SubmitterTeamNoTeamsFailsClosed(t *testing.T) {
	teams := &stubTeams{userTeams: []models.ProjectTeamRole{}}

	got, err := resolveTeamScope(teams, "submitter_team", uuid.New(), uuid.New())

	require.Error(t, err, "a submitter in no team must not silently drop the restriction")
	assert.Nil(t, got)
}

func TestResolveTeamScope_OtherTeamSubmitterLookupErrorFailsClosed(t *testing.T) {
	teams := &stubTeams{userTeamsErr: errors.New("connection refused")}

	got, err := resolveTeamScope(teams, "other_team", uuid.New(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, got)
}

func TestResolveTeamScope_OtherTeamListErrorFailsClosed(t *testing.T) {
	teams := &stubTeams{
		userTeams:   []models.ProjectTeamRole{{TeamID: uuid.New()}},
		allTeamsErr: errors.New("connection refused"),
	}

	got, err := resolveTeamScope(teams, "other_team", uuid.New(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, got)
}

func TestResolveTeamScope_OtherTeamNoOtherTeamExistsStaysOpen(t *testing.T) {
	only := models.ProjectTeamRole{TeamID: uuid.New()}
	teams := &stubTeams{
		userTeams: []models.ProjectTeamRole{only},
		allTeams:  []models.ProjectTeamRole{only},
	}

	got, err := resolveTeamScope(teams, "other_team", uuid.New(), uuid.New())

	require.NoError(t, err)
	assert.Nil(t, got,
		"a project with only the submitter's team has no other team to require; "+
			"this stays permissive by design, not by accident")
}

func TestResolveTeamScope_UnknownScopeErrors(t *testing.T) {
	got, err := resolveTeamScope(&stubTeams{}, "nonsense", uuid.New(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, got)
}

func TestPlanStages_CorruptPolicyErrors(t *testing.T) {
	e := &Engine{
		policies: stubPolicies{byAction: map[string]*models.ApprovalPolicy{
			"create": {Stages: json.RawMessage(`{not json`)},
		}},
		teams: &stubTeams{},
	}

	stages, err := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityRoute, models.ApprovalActionCreate)

	require.Error(t, err)
	assert.Nil(t, stages)
}

func TestPlanStages_NoPolicyFallsBackToSingleStage(t *testing.T) {
	e := &Engine{
		policies: stubPolicies{byAction: map[string]*models.ApprovalPolicy{}},
		teams:    &stubTeams{},
	}

	stages, err := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityRoute, models.ApprovalActionCreate)

	require.NoError(t, err)
	require.Len(t, stages, 1)
	assert.Equal(t, 1, stages[0].StageOrder)
	assert.Equal(t, string(models.PermRouteApprove), stages[0].RequiredPermission)
	assert.Nil(t, stages[0].RequiredTeamID,
		"the no-policy fallback must not invent a team restriction, and must not "+
			"drop one either -- this assertion is what guards the fallback against "+
			"silently widening the gate")
	assert.Equal(t, 1, stages[0].MinApprovers)
	assert.Equal(t, models.ApprovalStatusPending, stages[0].Status)
}

func TestPlanStages_ClientAttachmentNoPolicyErrors(t *testing.T) {
	// Unlike route, client_attachment must NOT synthesise a fallback stage
	// when no policy exists. ApprovalPolicyRepository.SeedDefaults seeds
	// every project with a TWO-stage client_attachment policy
	// (client.approve/other_team, then client.approve/any) specifically so
	// a submitter's own team cannot be the sole approver of its own
	// attachment change. A synthesised single-stage fallback would silently
	// replace that two-stage cross-team gate with a weaker one.
	// ClientAttachmentService.createApproval (pre-2D) never fell back
	// either -- it returned this exact error when no policy was found.
	e := &Engine{
		policies: stubPolicies{byAction: map[string]*models.ApprovalPolicy{}},
		teams:    &stubTeams{},
	}

	stages, err := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityClientAttachment, models.ApprovalActionAttach)

	require.Error(t, err)
	assert.Nil(t, stages)
	assert.Contains(t, err.Error(), "no approval policy found for client_attachment")
}

func TestPlanStages_ClientAttachmentEmptyStagesErrors(t *testing.T) {
	// Same divergence as above, but for the case where a policy exists yet
	// has no stage templates. ClientAttachmentService.createApproval
	// returned this distinct error rather than falling back; PlanStages
	// preserves it instead of collapsing both cases into one message.
	e := &Engine{
		policies: stubPolicies{byAction: map[string]*models.ApprovalPolicy{
			"": {Stages: json.RawMessage(`[]`)},
		}},
		teams: &stubTeams{},
	}

	stages, err := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityClientAttachment, models.ApprovalActionAttach)

	require.Error(t, err)
	assert.Nil(t, stages)
	assert.Contains(t, err.Error(), "approval policy has no stages defined")
}

func TestPlanStages_TeamResolutionFailurePropagates(t *testing.T) {
	tmpl, err := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "route.approve", TeamScope: "submitter_team", MinApprovers: 2},
	})
	require.NoError(t, err)

	e := &Engine{
		policies: stubPolicies{byAction: map[string]*models.ApprovalPolicy{
			"create": {Stages: tmpl},
		}},
		teams: &stubTeams{userTeamsErr: errors.New("connection refused")},
	}

	stages, err := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityRoute, models.ApprovalActionCreate)

	require.Error(t, err)
	assert.Nil(t, stages)
}

func TestPlanStages_AppliesEffectiveMinApprovers(t *testing.T) {
	tmpl, err := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "route.approve", TeamScope: "any", MinApprovers: 0},
	})
	require.NoError(t, err)

	e := &Engine{
		policies: stubPolicies{byAction: map[string]*models.ApprovalPolicy{
			"create": {Stages: tmpl},
		}},
		teams: &stubTeams{},
	}

	stages, err := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityRoute, models.ApprovalActionCreate)

	require.NoError(t, err)
	require.Len(t, stages, 1)
	assert.Equal(t, 1, stages[0].MinApprovers, "0 must normalise to 1")
}

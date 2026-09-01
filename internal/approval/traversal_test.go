package approval

// These tests are ports of the characterization tests in
// internal/services/approval_characterization_test.go, with the receiver
// swapped from *ApprovalService / *ClientAttachmentService to *Engine.
// Where the two pre-2D copies disagreed, the engine takes the failing-closed
// variant and the test asserts THAT one; every such test names the
// divergence it resolves (numbering follows
// .superpowers/sdd/2026-08-31-backend-v2-phase-2d/traversal-diff.md).

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------
// Stubs
// ---------------------------------------------------------------------

type stubApprovals struct {
	approval  *models.Approval
	getErr    error
	getCalls  int
	created   *models.Approval
	createErr error

	updated        []*models.Approval
	updateErr      error
	updatedStages  []*models.ApprovalStage
	updateStageErr error
}

func (s *stubApprovals) GetByID(uuid.UUID) (*models.Approval, error) {
	s.getCalls++
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.approval, nil
}

func (s *stubApprovals) Create(a *models.Approval) error {
	s.created = a
	return s.createErr
}

func (s *stubApprovals) Update(a *models.Approval) error {
	s.updated = append(s.updated, a)
	return s.updateErr
}

func (s *stubApprovals) UpdateStage(st *models.ApprovalStage) error {
	s.updatedStages = append(s.updatedStages, st)
	return s.updateStageErr
}

type stubStageReviews struct {
	existing  []models.ApprovalStageReview
	listErr   error
	created   []*models.ApprovalStageReview
	createErr error
	count     int64
	countErr  error
}

func (s *stubStageReviews) Create(r *models.ApprovalStageReview) error {
	s.created = append(s.created, r)
	return s.createErr
}

func (s *stubStageReviews) ListByStageID(uuid.UUID) ([]models.ApprovalStageReview, error) {
	return s.existing, s.listErr
}

func (s *stubStageReviews) CountByStageAndDecision(uuid.UUID, string) (int64, error) {
	return s.count, s.countErr
}

type stubProjects struct {
	project    *models.Project
	getErr     error
	isAdmin    bool
	isAdminErr error
}

func (s *stubProjects) GetByID(uuid.UUID) (*models.Project, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.project, nil
}

func (s *stubProjects) IsAdmin(uuid.UUID, uuid.UUID) (bool, error) {
	return s.isAdmin, s.isAdminErr
}

type recordingCompleter struct {
	approved, rejected, cancelled int
	err                           error

	// canCancel drives the optional CancelAuthorizer extension; see
	// cancelAuthorizingCompleter below.
	canCancel bool
}

func (c *recordingCompleter) OnApproved(*models.Approval) error  { c.approved++; return c.err }
func (c *recordingCompleter) OnRejected(*models.Approval) error  { c.rejected++; return c.err }
func (c *recordingCompleter) OnCancelled(*models.Approval) error { c.cancelled++; return c.err }

// cancelAuthorizingCompleter is the shape the route completer will take in
// Task 7: a Completer that also grants the "member of the route's owning
// team may cancel" right the engine cannot evaluate on its own.
type cancelAuthorizingCompleter struct{ recordingCompleter }

func (c *cancelAuthorizingCompleter) CanCancel(*models.Approval, *models.User) bool {
	return c.canCancel
}

// newTestEngine wires an engine with a route completer already registered.
func newTestEngine(
	approvals *stubApprovals,
	reviews *stubStageReviews,
	teams *stubTeams,
	projects *stubProjects,
	completer Completer,
) *Engine {
	e := New(approvals, reviews, stubPolicies{}, teams, projects)
	if completer != nil {
		e.Register(models.ApprovalEntityRoute, completer)
	}
	return e
}

// ---------------------------------------------------------------------
// ApproveStage
// ---------------------------------------------------------------------

// Port of TestAS_ApproveStage_RejectsNonPendingApproval /
// TestCAS_ApproveStage_RejectsNonPendingApproval (both copies agree).
func TestEngine_ApproveStage_RejectsNonPendingApproval(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	approvals := &stubApprovals{approval: &models.Approval{
		ID:         approvalID,
		EntityType: models.ApprovalEntityRoute,
		Status:     models.ApprovalStatusApproved,
	}}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, &recordingCompleter{})

	got, err := e.ApproveStage(approvalID, stageID, &models.User{ID: uuid.New()})

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "approval is not pending")
}

// DIVERGENCE 1 -> ApprovalService. Port of
// TestAS_ApproveStage_GetByIDErrorPropagatesRaw; the CAS counterpart
// (TestCAS_ApproveStage_GetByIDErrorReplacedWithGenericMessage) asserted the
// generic "approval not found" and is deliberately NOT carried over.
func TestEngine_ApproveStage_GetByIDErrorPropagatesRaw(t *testing.T) {
	underlying := errors.New("connection refused")
	approvals := &stubApprovals{getErr: underlying}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, &recordingCompleter{})

	got, err := e.ApproveStage(uuid.New(), uuid.New(), &models.User{ID: uuid.New()})

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, underlying, err,
		"the repository error is propagated unchanged, not masked as \"approval not found\"")
}

// DIVERGENCE 2 -> ApprovalService. Port of
// TestAS_ApproveStage_StageNotFoundTakesPriorityOverSelfApproval. The CAS
// counterpart asserted "submitter cannot approve their own submission" here
// because it checked self-approval before it ever looked the stage up.
func TestEngine_ApproveStage_StageNotFoundTakesPriorityOverSelfApproval(t *testing.T) {
	approvalID := uuid.New()
	submitter := &models.User{ID: uuid.New()}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: submitter.ID,
		Stages:      []models.ApprovalStage{}, // the stage ID matches nothing
	}}
	// SelfApprovalAllowed=false: if the engine reached the self-approval
	// check it would report that instead.
	projects := &stubProjects{project: &models.Project{SelfApprovalAllowed: false}}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, projects, &recordingCompleter{})

	got, err := e.ApproveStage(approvalID, uuid.New(), submitter)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "stage not found in this approval",
		"the stage is validated before self-approval, per divergence 2")
}

// Port of TestAS_ApproveStage_SubmitterCannotSelfApproveByDefault /
// TestCAS_... (both copies agree on the outcome).
func TestEngine_ApproveStage_SubmitterCannotSelfApproveByDefault(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	submitter := &models.User{ID: uuid.New()}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: submitter.ID,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}}
	projects := &stubProjects{project: &models.Project{SelfApprovalAllowed: false}}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, projects, &recordingCompleter{})

	got, err := e.ApproveStage(approvalID, stageID, submitter)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "submitter cannot approve their own submission")
}

// INTENDED CHANGE (traversal-diff item 7). Both copies wrapped this lookup
// in `if s.projectRepo != nil`, so an unwired repository denied
// self-approval by accident rather than by decision. ProjectLookup is not
// optional now, so the project's own setting always decides.
func TestEngine_ApproveStage_SubmitterCanSelfApproveWhenProjectAllows(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	submitter := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: submitter.ID,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}}
	projects := &stubProjects{project: &models.Project{SelfApprovalAllowed: true}}
	reviews := &stubStageReviews{count: 1}
	completer := &recordingCompleter{}

	e := newTestEngine(approvals, reviews, &stubTeams{}, projects, completer)

	got, err := e.ApproveStage(approvalID, stageID, submitter)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, models.ApprovalStatusApproved, got.Status)
	assert.Equal(t, 1, completer.approved)
}

// A self-approval attempt whose project lookup FAILS is denied: the error is
// swallowed and `allowed` stays false, which is the fail-closed direction
// both pre-2D copies took.
func TestEngine_ApproveStage_SelfApprovalDeniedWhenProjectLookupFails(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	submitter := &models.User{ID: uuid.New()}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: submitter.ID,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}}
	projects := &stubProjects{getErr: errors.New("connection refused")}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, projects, &recordingCompleter{})

	_, err := e.ApproveStage(approvalID, stageID, submitter)

	require.Error(t, err)
	assert.EqualError(t, err, "submitter cannot approve their own submission")
}

// Both copies return the same message for a stage ID that belongs to no
// stage of this approval (traversal-diff item 8).
func TestEngine_ApproveStage_RejectsUnknownStage(t *testing.T) {
	approvalID := uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: uuid.New(), StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, &recordingCompleter{})

	got, err := e.ApproveStage(approvalID, uuid.New(), reviewer)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "stage not found in this approval")
}

// DIVERGENCE 3 -> ApprovalService. Port of
// TestAS_ApproveStage_EmptyRequiredPermissionStillDeniesReviewer. The CAS
// counterpart (TestCAS_ValidateStageReviewer_EmptyRequiredPermissionSkipsCheck)
// asserted that an empty permission skips the check entirely -- a fail-open
// that is deliberately NOT carried over.
func TestEngine_ApproveStage_EmptyRequiredPermissionStillDeniesReviewer(t *testing.T) {
	approvalID, stageID, projectID := uuid.New(), uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleUser}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(), // not the reviewer
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending, RequiredPermission: ""},
		},
	}}
	teams := &stubTeams{hasPerm: false}

	e := newTestEngine(approvals, &stubStageReviews{}, teams, &stubProjects{isAdmin: false}, &recordingCompleter{})

	got, err := e.ApproveStage(approvalID, stageID, reviewer)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "reviewer does not have the required permission")
	assert.Equal(t, []models.Permission{models.Permission("")}, teams.permCalls,
		"the permission check must run even for an empty RequiredPermission")
}

// The permission lookup's own error propagates rather than being flattened
// into a denial message (ApprovalService behaviour; CAS wrapped it as
// "failed to check reviewer permission").
func TestEngine_ApproveStage_PermissionLookupErrorPropagates(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleUser}
	underlying := errors.New("connection refused")

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending, RequiredPermission: "route.approve"},
		},
	}}

	e := newTestEngine(approvals, &stubStageReviews{},
		&stubTeams{hasPermErr: underlying}, &stubProjects{}, &recordingCompleter{})

	_, err := e.ApproveStage(approvalID, stageID, reviewer)

	require.Error(t, err)
	assert.Equal(t, underlying, err)
}

// Both copies agree: a stage with a RequiredTeamID is reviewable only by a
// member of that team.
func TestEngine_ApproveStage_RequiresStageTeamMembership(t *testing.T) {
	approvalID, stageID, teamID := uuid.New(), uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleUser}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{
				ID:                 stageID,
				StageOrder:         1,
				Status:             models.ApprovalStatusPending,
				RequiredPermission: "route.approve",
				RequiredTeamID:     &teamID,
			},
		},
	}}
	teams := &stubTeams{hasPerm: true, isMember: false}

	e := newTestEngine(approvals, &stubStageReviews{}, teams, &stubProjects{}, &recordingCompleter{})

	got, err := e.ApproveStage(approvalID, stageID, reviewer)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "reviewer is not a member of the required team")
}

// Sequential stages. The stage slice is deliberately supplied OUT of
// StageOrder sequence: traversal-diff item 11 chose CAS's attribute-based
// filter over AS's sort-then-walk-by-index, so ordering of the slice must
// not matter.
func TestEngine_ApproveStage_RequiresPreviousStagesApproved(t *testing.T) {
	approvalID := uuid.New()
	first, second := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: second, StageOrder: 2, Status: models.ApprovalStatusPending},
			{ID: first, StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}}

	e := newTestEngine(approvals, &stubStageReviews{count: 1}, &stubTeams{}, &stubProjects{}, &recordingCompleter{})

	got, err := e.ApproveStage(approvalID, second, reviewer)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "previous stages must be approved first")
}

// Both copies agree: one reviewer, one vote per stage.
func TestEngine_ApproveStage_RejectsDuplicateReviewer(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending, MinApprovers: 2},
		},
	}}
	reviews := &stubStageReviews{
		existing: []models.ApprovalStageReview{{StageID: stageID, ReviewerID: reviewer.ID, Decision: "approved"}},
	}

	e := newTestEngine(approvals, reviews, &stubTeams{}, &stubProjects{}, &recordingCompleter{})

	got, err := e.ApproveStage(approvalID, stageID, reviewer)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "you have already reviewed this stage")
}

// MinApprovers>1: the review is recorded but neither the stage nor the
// approval advances, and the completer is not called.
//
// INTENDED CHANGE: both pre-2D copies ran this whole block only
// `if s.stageReviewRepo != nil`, so an unwired review store completed such a
// stage on the first approval. StageReviewStore is not optional here.
func TestEngine_ApproveStage_DoesNotCompleteUntilMinApproversMet(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending, MinApprovers: 2},
		},
	}}
	reviews := &stubStageReviews{count: 1} // one of the two required
	completer := &recordingCompleter{}

	e := newTestEngine(approvals, reviews, &stubTeams{}, &stubProjects{}, completer)

	got, err := e.ApproveStage(approvalID, stageID, reviewer)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Len(t, reviews.created, 1, "the vote is recorded")
	assert.Empty(t, approvals.updatedStages, "the stage is not advanced")
	assert.Empty(t, approvals.updated, "the approval is not advanced")
	assert.Equal(t, models.ApprovalStatusPending, got.Stages[0].Status)
	assert.Equal(t, 0, completer.approved)
}

// The other half of MinApprovers>1: the quota is MET. A second, DISTINCT
// reviewer brings the count to 2, which must advance the stage -- and, when
// that stage is the last one, complete the approval and call the completer.
//
// Without this, every multi-approver case in the suite stopped at
// count:1 and a quota-met stage that failed to advance would leave real
// approvals hanging for ever with no test noticing.
func TestEngine_ApproveStage_CompletesStageWhenMinApproversMet(t *testing.T) {
	firstReviewer := uuid.New()
	secondReviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}
	require.NotEqual(t, firstReviewer, secondReviewer.ID, "the two reviewers must be distinct")

	// The first reviewer's vote is already on record, so the de-dup check in
	// ApproveStage must let the second reviewer through; the store then
	// reports the quota as met.
	newReviews := func(stageID uuid.UUID) *stubStageReviews {
		return &stubStageReviews{
			existing: []models.ApprovalStageReview{
				{StageID: stageID, ReviewerID: firstReviewer, Decision: "approved"},
			},
			count: 2,
		}
	}

	t.Run("final stage completes the approval", func(t *testing.T) {
		approvalID, stageID := uuid.New(), uuid.New()
		approvals := &stubApprovals{approval: &models.Approval{
			ID:          approvalID,
			EntityType:  models.ApprovalEntityRoute,
			Status:      models.ApprovalStatusPending,
			SubmittedBy: uuid.New(),
			Stages: []models.ApprovalStage{
				{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending, MinApprovers: 2},
			},
		}}
		reviews := newReviews(stageID)
		completer := &recordingCompleter{}

		e := newTestEngine(approvals, reviews, &stubTeams{}, &stubProjects{}, completer)

		got, err := e.ApproveStage(approvalID, stageID, secondReviewer)

		require.NoError(t, err)
		require.NotNil(t, got)

		// The second vote was recorded, not swallowed by the de-dup check.
		require.Len(t, reviews.created, 1)
		assert.Equal(t, secondReviewer.ID, reviews.created[0].ReviewerID)
		assert.Equal(t, "approved", reviews.created[0].Decision)

		// The stage advanced.
		require.Len(t, approvals.updatedStages, 1, "the quota-met stage must be persisted as approved")
		assert.Equal(t, models.ApprovalStatusApproved, approvals.updatedStages[0].Status)
		assert.Equal(t, &secondReviewer.ID, approvals.updatedStages[0].ReviewedBy)
		require.NotNil(t, approvals.updatedStages[0].ReviewedAt)

		// The approval completed, exactly once.
		require.Len(t, approvals.updated, 1)
		assert.Equal(t, models.ApprovalStatusApproved, approvals.updated[0].Status)
		assert.Equal(t, models.ApprovalStatusApproved, got.Status)
		assert.Equal(t, 1, completer.approved)
		assert.Equal(t, 0, completer.rejected)
		assert.Equal(t, 0, completer.cancelled)
	})

	t.Run("intermediate stage advances without completing the approval", func(t *testing.T) {
		approvalID := uuid.New()
		first, second := uuid.New(), uuid.New()
		approvals := &stubApprovals{approval: &models.Approval{
			ID:          approvalID,
			EntityType:  models.ApprovalEntityRoute,
			Status:      models.ApprovalStatusPending,
			SubmittedBy: uuid.New(),
			Stages: []models.ApprovalStage{
				{ID: first, StageOrder: 1, Status: models.ApprovalStatusPending, MinApprovers: 2},
				{ID: second, StageOrder: 2, Status: models.ApprovalStatusPending, MinApprovers: 2},
			},
		}}
		reviews := newReviews(first)
		completer := &recordingCompleter{}

		e := newTestEngine(approvals, reviews, &stubTeams{}, &stubProjects{}, completer)

		got, err := e.ApproveStage(approvalID, first, secondReviewer)

		require.NoError(t, err)
		require.NotNil(t, got)
		require.Len(t, approvals.updatedStages, 1)
		assert.Equal(t, models.ApprovalStatusApproved, approvals.updatedStages[0].Status)
		assert.Equal(t, first, approvals.updatedStages[0].ID, "stage 1, not stage 2")
		assert.Empty(t, approvals.updated, "stage 2 is still pending, so the approval is untouched")
		assert.Equal(t, models.ApprovalStatusPending, got.Status)
		assert.Equal(t, 0, completer.approved)
	})
}

// The last stage of the last approval: stage and approval both move to
// approved and the entity's completer runs exactly once.
func TestEngine_ApproveStage_CallsCompleterOnFinalStage(t *testing.T) {
	approvalID := uuid.New()
	first, second := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: first, StageOrder: 1, Status: models.ApprovalStatusApproved},
			{ID: second, StageOrder: 2, Status: models.ApprovalStatusPending},
		},
	}}
	reviews := &stubStageReviews{count: 1}
	completer := &recordingCompleter{}

	e := newTestEngine(approvals, reviews, &stubTeams{}, &stubProjects{}, completer)

	got, err := e.ApproveStage(approvalID, second, reviewer)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, approvals.updatedStages, 1)
	assert.Equal(t, models.ApprovalStatusApproved, approvals.updatedStages[0].Status)
	assert.Equal(t, &reviewer.ID, approvals.updatedStages[0].ReviewedBy)
	require.NotNil(t, approvals.updatedStages[0].ReviewedAt)
	assert.Equal(t, models.ApprovalStatusApproved, got.Status)
	assert.Equal(t, 1, completer.approved)
	assert.Equal(t, 0, completer.rejected)
	assert.Equal(t, 0, completer.cancelled)
}

// An intermediate stage completes without completing the approval.
func TestEngine_ApproveStage_IntermediateStageDoesNotCompleteApproval(t *testing.T) {
	approvalID := uuid.New()
	first, second := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: first, StageOrder: 1, Status: models.ApprovalStatusPending},
			{ID: second, StageOrder: 2, Status: models.ApprovalStatusPending},
		},
	}}
	completer := &recordingCompleter{}

	e := newTestEngine(approvals, &stubStageReviews{count: 1}, &stubTeams{}, &stubProjects{}, completer)

	got, err := e.ApproveStage(approvalID, first, reviewer)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Len(t, approvals.updatedStages, 1)
	assert.Empty(t, approvals.updated, "the approval itself is untouched")
	assert.Equal(t, 0, completer.approved)
}

// INTENDED CHANGE. ApprovalService's onApprovalComplete/Rejected/Cancelled
// had a `default: return nil` arm (and a `clientAttachmentService != nil`
// guard) that reported success while doing nothing, leaving the entity
// stranded. An unregistered entity type is now an error, raised BEFORE any
// state is written.
func TestEngine_ApproveStage_UnregisteredEntityTypeErrors(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityClientAttachment, // never registered below
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}}
	reviews := &stubStageReviews{count: 1}

	e := newTestEngine(approvals, reviews, &stubTeams{}, &stubProjects{}, &recordingCompleter{})

	got, err := e.ApproveStage(approvalID, stageID, reviewer)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "no completer registered for entity type")
	assert.Empty(t, approvals.updatedStages, "nothing is written before the completer is resolved")
	assert.Empty(t, reviews.created)
}

// ---------------------------------------------------------------------
// RejectStage
// ---------------------------------------------------------------------

// DIVERGENCE 4 -> ApprovalService. Port of
// TestAS_RejectStage_EmptyCommentErrors; the CAS counterpart
// (TestCAS_RejectStage_EmptyCommentSucceeds) is deliberately NOT carried
// over. The guard fires before the approval is fetched.
func TestEngine_RejectStage_EmptyCommentErrors(t *testing.T) {
	approvals := &stubApprovals{}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, &recordingCompleter{})

	got, err := e.RejectStage(uuid.New(), uuid.New(), &models.User{ID: uuid.New()}, "")

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "rejection comment is required")
	assert.Equal(t, 0, approvals.getCalls, "it fails before fetching the approval")
}

// DIVERGENCE 1 -> ApprovalService, on the reject path.
func TestEngine_RejectStage_GetByIDErrorPropagatesRaw(t *testing.T) {
	underlying := errors.New("connection refused")
	approvals := &stubApprovals{getErr: underlying}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, &recordingCompleter{})

	_, err := e.RejectStage(uuid.New(), uuid.New(), &models.User{ID: uuid.New()}, "needs rework")

	require.Error(t, err)
	assert.Equal(t, underlying, err)
}

func TestEngine_RejectStage_CallsCompleterOnRejected(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending},
			{ID: uuid.New(), StageOrder: 2, Status: models.ApprovalStatusPending},
		},
	}}
	reviews := &stubStageReviews{}
	completer := &recordingCompleter{}

	e := newTestEngine(approvals, reviews, &stubTeams{}, &stubProjects{}, completer)

	got, err := e.RejectStage(approvalID, stageID, reviewer, "needs rework")

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, approvals.updatedStages, 1)
	assert.Equal(t, models.ApprovalStatusRejected, approvals.updatedStages[0].Status)
	assert.Equal(t, "needs rework", approvals.updatedStages[0].Comment)
	assert.Equal(t, models.ApprovalStatusRejected, got.Status,
		"one rejected stage rejects the whole approval, even with stage 2 still pending")
	assert.Equal(t, 1, completer.rejected)
	assert.Equal(t, 0, completer.approved)
	require.Len(t, reviews.created, 1)
	assert.Equal(t, "rejected", reviews.created[0].Decision)
}

// DIVERGENCE 5 -> ApprovalService. Port of
// TestCAS_RejectStage_AttachmentUpdateFailureIsSwallowed with the assertion
// INVERTED: CAS inlined the attachment update and discarded its error, so
// RejectStage reported success while the entity never changed. Here the
// side effect is the completer's and its error is the caller's.
func TestEngine_RejectStage_CompleterErrorPropagates(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}
	underlying := errors.New("db write failed")

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}}
	completer := &recordingCompleter{err: underlying}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, completer)

	got, err := e.RejectStage(approvalID, stageID, reviewer, "needs rework")

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, underlying, err)
	assert.Equal(t, 1, completer.rejected)
}

// The audit review write stays best-effort, exactly as in both pre-2D
// copies: a failed audit row must not block a legitimate rejection.
func TestEngine_RejectStage_AuditReviewWriteIsBestEffort(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}}
	completer := &recordingCompleter{}

	e := newTestEngine(approvals, &stubStageReviews{createErr: errors.New("db write failed")},
		&stubTeams{}, &stubProjects{}, completer)

	got, err := e.RejectStage(approvalID, stageID, reviewer, "needs rework")

	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, 1, completer.rejected)
}

// Divergence 2 applies to RejectStage as well as ApproveStage.
func TestEngine_RejectStage_StageNotFoundTakesPriorityOverSelfApproval(t *testing.T) {
	approvalID := uuid.New()
	submitter := &models.User{ID: uuid.New()}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: submitter.ID,
		Stages:      []models.ApprovalStage{},
	}}
	projects := &stubProjects{project: &models.Project{SelfApprovalAllowed: false}}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, projects, &recordingCompleter{})

	_, err := e.RejectStage(approvalID, uuid.New(), submitter, "needs rework")

	require.Error(t, err)
	assert.EqualError(t, err, "stage not found in this approval")
}

func TestEngine_RejectStage_SubmitterCannotSelfRejectByDefault(t *testing.T) {
	approvalID, stageID := uuid.New(), uuid.New()
	submitter := &models.User{ID: uuid.New()}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: submitter.ID,
		Stages: []models.ApprovalStage{
			{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending},
		},
	}}
	projects := &stubProjects{project: &models.Project{SelfApprovalAllowed: false}}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, projects, &recordingCompleter{})

	_, err := e.RejectStage(approvalID, stageID, submitter, "needs rework")

	require.Error(t, err)
	assert.EqualError(t, err, "submitter cannot reject their own submission",
		"the message says reject, not approve")
}

// ---------------------------------------------------------------------
// Cancel
// ---------------------------------------------------------------------

func TestEngine_Cancel_CallsCompleterOnCancelled(t *testing.T) {
	approvalID := uuid.New()
	submitter := &models.User{ID: uuid.New()}

	approvals := &stubApprovals{approval: &models.Approval{
		ID:          approvalID,
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: submitter.ID,
	}}
	completer := &recordingCompleter{}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, completer)

	got, err := e.Cancel(approvalID, submitter)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, models.ApprovalStatusCancelled, got.Status)
	assert.Equal(t, 1, completer.cancelled)
	assert.Equal(t, 0, completer.approved)
	assert.Equal(t, 0, completer.rejected)
}

// An already-approved approval may still be cancelled (it has not been
// deployed yet); a terminal one may not.
func TestEngine_Cancel_AllowsApprovedButNotTerminal(t *testing.T) {
	submitter := &models.User{ID: uuid.New()}

	t.Run("approved is cancellable", func(t *testing.T) {
		approvals := &stubApprovals{approval: &models.Approval{
			EntityType:  models.ApprovalEntityRoute,
			Status:      models.ApprovalStatusApproved,
			SubmittedBy: submitter.ID,
		}}
		completer := &recordingCompleter{}
		e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, completer)

		_, err := e.Cancel(uuid.New(), submitter)

		require.NoError(t, err)
		assert.Equal(t, 1, completer.cancelled)
	})

	for _, status := range []models.ApprovalStatus{models.ApprovalStatusRejected, models.ApprovalStatusCancelled} {
		t.Run(string(status)+" is not cancellable", func(t *testing.T) {
			approvals := &stubApprovals{approval: &models.Approval{
				EntityType:  models.ApprovalEntityRoute,
				Status:      status,
				SubmittedBy: submitter.ID,
			}}
			completer := &recordingCompleter{}
			e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, completer)

			got, err := e.Cancel(uuid.New(), submitter)

			require.Error(t, err)
			assert.Nil(t, got)
			assert.EqualError(t, err, "approval cannot be cancelled in its current state")
			assert.Equal(t, 0, completer.cancelled)
		})
	}
}

func TestEngine_Cancel_RejectsUnrelatedUser(t *testing.T) {
	approvals := &stubApprovals{approval: &models.Approval{
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
	}}
	completer := &recordingCompleter{}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{},
		&stubProjects{isAdmin: false}, completer)

	got, err := e.Cancel(uuid.New(), &models.User{ID: uuid.New(), Role: models.UserRoleUser})

	require.Error(t, err)
	assert.Nil(t, got)
	assert.EqualError(t, err, "you do not have permission to cancel this approval")
	assert.Empty(t, approvals.updated)
	assert.Equal(t, 0, completer.cancelled)
}

func TestEngine_Cancel_ProjectAdminMayCancel(t *testing.T) {
	approvals := &stubApprovals{approval: &models.Approval{
		EntityType:  models.ApprovalEntityRoute,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: uuid.New(),
	}}
	completer := &recordingCompleter{}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{},
		&stubProjects{isAdmin: true}, completer)

	_, err := e.Cancel(uuid.New(), &models.User{ID: uuid.New(), Role: models.UserRoleUser})

	require.NoError(t, err)
	assert.Equal(t, 1, completer.cancelled)
}

// The route-owner-team-member cancellation right of
// ApprovalService.CancelApproval needs a route lookup the engine does not
// own, so it is delegated to the completer through the optional
// CancelAuthorizer extension (traversal-diff item 10).
func TestEngine_Cancel_CompleterMayGrantExtraRight(t *testing.T) {
	user := &models.User{ID: uuid.New(), Role: models.UserRoleUser}

	newApprovals := func() *stubApprovals {
		return &stubApprovals{approval: &models.Approval{
			EntityType:  models.ApprovalEntityRoute,
			Status:      models.ApprovalStatusPending,
			SubmittedBy: uuid.New(),
		}}
	}

	t.Run("granted", func(t *testing.T) {
		completer := &cancelAuthorizingCompleter{}
		completer.canCancel = true
		e := newTestEngine(newApprovals(), &stubStageReviews{}, &stubTeams{}, &stubProjects{}, completer)

		_, err := e.Cancel(uuid.New(), user)

		require.NoError(t, err)
		assert.Equal(t, 1, completer.cancelled)
	})

	t.Run("refused", func(t *testing.T) {
		completer := &cancelAuthorizingCompleter{}
		completer.canCancel = false
		e := newTestEngine(newApprovals(), &stubStageReviews{}, &stubTeams{}, &stubProjects{}, completer)

		_, err := e.Cancel(uuid.New(), user)

		require.Error(t, err)
		assert.EqualError(t, err, "you do not have permission to cancel this approval")
		assert.Equal(t, 0, completer.cancelled)
	})
}

func TestEngine_Cancel_UnregisteredEntityTypeErrors(t *testing.T) {
	submitter := &models.User{ID: uuid.New()}
	approvals := &stubApprovals{approval: &models.Approval{
		EntityType:  models.ApprovalEntityClientAttachment,
		Status:      models.ApprovalStatusPending,
		SubmittedBy: submitter.ID,
	}}

	e := newTestEngine(approvals, &stubStageReviews{}, &stubTeams{}, &stubProjects{}, &recordingCompleter{})

	got, err := e.Cancel(uuid.New(), submitter)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "no completer registered for entity type")
	assert.Empty(t, approvals.updated)
}

// ---------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------

func TestEngine_Submit_PlansStagesAndPersists(t *testing.T) {
	projectID, entityID, submitter := uuid.New(), uuid.New(), uuid.New()

	tmpl, err := json.Marshal([]models.PolicyStageTemplate{
		{Order: 1, RequiredPermission: "route.approve", TeamScope: "any", MinApprovers: 2},
	})
	require.NoError(t, err)

	approvals := &stubApprovals{}
	e := New(approvals, &stubStageReviews{},
		stubPolicies{byAction: map[string]*models.ApprovalPolicy{"create": {Stages: tmpl}}},
		&stubTeams{}, &stubProjects{})

	got, err := e.Submit(Spec{
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    entityID,
		Action:      models.ApprovalActionCreate,
		SubmittedBy: submitter,
	})

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Same(t, got, approvals.created, "the persisted approval is the one returned")
	assert.Equal(t, models.ApprovalStatusPending, got.Status)
	require.Len(t, got.Stages, 1)
	assert.Equal(t, 1, got.Stages[0].StageOrder)
	assert.Equal(t, "route.approve", got.Stages[0].RequiredPermission)
	assert.Equal(t, 2, got.Stages[0].MinApprovers)
	assert.Equal(t, models.ApprovalStatusPending, got.Stages[0].Status)
}

// Every field the four pre-2D `&models.Approval{}` construction sites set
// (route_write.go create/update/delete and
// ClientAttachmentService.createApproval) must survive the Spec.
func TestEngine_Submit_CarriesEveryConstructionSiteField(t *testing.T) {
	spec := Spec{
		ProjectID:         uuid.New(),
		EntityType:        models.ApprovalEntityRoute,
		EntityID:          uuid.New(),
		Action:            models.ApprovalActionUpdate,
		SubmittedBy:       uuid.New(),
		ConfigSnapshot:    json.RawMessage(`{"config":"new"}`),
		PreviousConfig:    json.RawMessage(`{"config":"old"}`),
		AIReview:          json.RawMessage(`{"verdict":"ok"}`),
		ChangeDescription: "widen the timeout",
	}

	approvals := &stubApprovals{}
	e := New(approvals, &stubStageReviews{},
		stubPolicies{byAction: map[string]*models.ApprovalPolicy{}},
		&stubTeams{}, &stubProjects{})

	got, err := e.Submit(spec)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, spec.ProjectID, got.ProjectID)
	assert.Equal(t, spec.EntityType, got.EntityType)
	assert.Equal(t, spec.EntityID, got.EntityID)
	assert.Equal(t, spec.Action, got.Action)
	assert.Equal(t, spec.SubmittedBy, got.SubmittedBy)
	assert.JSONEq(t, string(spec.ConfigSnapshot), string(got.ConfigSnapshot))
	assert.JSONEq(t, string(spec.PreviousConfig), string(got.PreviousConfig))
	assert.JSONEq(t, string(spec.AIReview), string(got.AIReview))
	assert.Equal(t, spec.ChangeDescription, got.ChangeDescription)
	assert.Equal(t, models.ApprovalStatusPending, got.Status)
}

// Planning runs BEFORE the write, so an unhonourable policy aborts the
// submission instead of creating an approval behind a weaker gate.
func TestEngine_Submit_StagePlanningFailureAbortsSubmission(t *testing.T) {
	approvals := &stubApprovals{}
	e := New(approvals, &stubStageReviews{},
		stubPolicies{byAction: map[string]*models.ApprovalPolicy{
			"create": {Stages: json.RawMessage(`{not json`)},
		}},
		&stubTeams{}, &stubProjects{})

	got, err := e.Submit(Spec{
		ProjectID:   uuid.New(),
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    uuid.New(),
		Action:      models.ApprovalActionCreate,
		SubmittedBy: uuid.New(),
	})

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Nil(t, approvals.created, "nothing is persisted")
}

// The client_attachment no-policy rule (Task 5's per-entity fallback)
// reaches Submit unchanged: no stages, no approval, an error.
func TestEngine_Submit_ClientAttachmentWithoutPolicyAborts(t *testing.T) {
	approvals := &stubApprovals{}
	e := New(approvals, &stubStageReviews{},
		stubPolicies{byAction: map[string]*models.ApprovalPolicy{}},
		&stubTeams{}, &stubProjects{})

	got, err := e.Submit(Spec{
		ProjectID:   uuid.New(),
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    uuid.New(),
		Action:      models.ApprovalActionAttach,
		SubmittedBy: uuid.New(),
	})

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "no approval policy found for client_attachment")
	assert.Nil(t, approvals.created)
}

func TestEngine_Submit_CreateFailurePropagates(t *testing.T) {
	underlying := errors.New("db write failed")
	approvals := &stubApprovals{createErr: underlying}
	e := New(approvals, &stubStageReviews{},
		stubPolicies{byAction: map[string]*models.ApprovalPolicy{}},
		&stubTeams{}, &stubProjects{})

	got, err := e.Submit(Spec{
		ProjectID:   uuid.New(),
		EntityType:  models.ApprovalEntityRoute,
		EntityID:    uuid.New(),
		Action:      models.ApprovalActionCreate,
		SubmittedBy: uuid.New(),
	})

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, underlying, err)
}

// ---------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------

func TestEngine_Register_ResolvesPerEntityType(t *testing.T) {
	routes, attachments := &recordingCompleter{}, &recordingCompleter{}

	e := New(&stubApprovals{}, &stubStageReviews{}, stubPolicies{}, &stubTeams{}, &stubProjects{})
	e.Register(models.ApprovalEntityRoute, routes)
	e.Register(models.ApprovalEntityClientAttachment, attachments)

	got, err := e.completerFor(models.ApprovalEntityRoute)
	require.NoError(t, err)
	assert.Same(t, routes, got)

	got, err = e.completerFor(models.ApprovalEntityClientAttachment)
	require.NoError(t, err)
	assert.Same(t, attachments, got)

	_, err = e.completerFor(models.ApprovalEntityType("nonsense"))
	require.Error(t, err)
}

// The review store's own failures abort the approval rather than being
// treated as "no reviews yet", which would let a MinApprovers>1 stage
// through on a single vote. Both pre-2D copies wrapped these the same way.
func TestEngine_ApproveStage_ReviewStoreFailuresAbort(t *testing.T) {
	stageID := uuid.New()
	reviewer := &models.User{ID: uuid.New(), Role: models.UserRoleOwner}

	newApprovals := func() *stubApprovals {
		return &stubApprovals{approval: &models.Approval{
			EntityType:  models.ApprovalEntityRoute,
			Status:      models.ApprovalStatusPending,
			SubmittedBy: uuid.New(),
			Stages: []models.ApprovalStage{
				{ID: stageID, StageOrder: 1, Status: models.ApprovalStatusPending, MinApprovers: 2},
			},
		}}
	}

	cases := map[string]struct {
		reviews *stubStageReviews
		wantMsg string
	}{
		"list fails":   {&stubStageReviews{listErr: errors.New("connection refused")}, "failed to check existing reviews"},
		"create fails": {&stubStageReviews{createErr: errors.New("connection refused")}, "failed to record review"},
		"count fails":  {&stubStageReviews{countErr: errors.New("connection refused")}, "failed to count reviews"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			approvals := newApprovals()
			completer := &recordingCompleter{}
			e := newTestEngine(approvals, tc.reviews, &stubTeams{}, &stubProjects{}, completer)

			got, err := e.ApproveStage(uuid.New(), stageID, reviewer)

			require.Error(t, err)
			assert.Nil(t, got)
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.Empty(t, approvals.updatedStages)
			assert.Equal(t, 0, completer.approved)
		})
	}
}

// ---------------------------------------------------------------------
// Construction
// ---------------------------------------------------------------------

// RULING R7. Every dependency is required. A nil one must fail loudly at
// construction rather than degrade silently at runtime -- a nil PolicyStore
// used to make PlanStages skip the policy lookup and synthesise the single
// default stage, quietly replacing a project's real multi-stage gate.
func TestNew_PanicsOnNilDependency(t *testing.T) {
	full := func() (ApprovalStore, StageReviewStore, PolicyStore, TeamLookup, ProjectLookup) {
		return &stubApprovals{}, &stubStageReviews{}, stubPolicies{}, &stubTeams{}, &stubProjects{}
	}

	cases := []struct {
		name string
		want string
		nils func() (ApprovalStore, StageReviewStore, PolicyStore, TeamLookup, ProjectLookup)
	}{
		{"approvals", "approvals ApprovalStore", func() (ApprovalStore, StageReviewStore, PolicyStore, TeamLookup, ProjectLookup) {
			_, s, p, tm, pr := full()
			return nil, s, p, tm, pr
		}},
		{"stages", "stages StageReviewStore", func() (ApprovalStore, StageReviewStore, PolicyStore, TeamLookup, ProjectLookup) {
			a, _, p, tm, pr := full()
			return a, nil, p, tm, pr
		}},
		{"policies", "policies PolicyStore", func() (ApprovalStore, StageReviewStore, PolicyStore, TeamLookup, ProjectLookup) {
			a, s, _, tm, pr := full()
			return a, s, nil, tm, pr
		}},
		{"teams", "teams TeamLookup", func() (ApprovalStore, StageReviewStore, PolicyStore, TeamLookup, ProjectLookup) {
			a, s, p, _, pr := full()
			return a, s, p, nil, pr
		}},
		{"projects", "projects ProjectLookup", func() (ApprovalStore, StageReviewStore, PolicyStore, TeamLookup, ProjectLookup) {
			a, s, p, tm, _ := full()
			return a, s, p, tm, nil
		}},
	}

	for _, tc := range cases {
		t.Run("nil "+tc.name, func(t *testing.T) {
			a, s, p, tm, pr := tc.nils()

			assert.PanicsWithValue(t,
				"approval.New: missing required dependency: "+tc.want,
				func() { New(a, s, p, tm, pr) },
				"the panic must name the missing dependency")
		})
	}

	t.Run("all nil names them all", func(t *testing.T) {
		assert.PanicsWithValue(t,
			"approval.New: missing required dependency: approvals ApprovalStore, "+
				"stages StageReviewStore, policies PolicyStore, teams TeamLookup, projects ProjectLookup",
			func() { New(nil, nil, nil, nil, nil) })
	})

	t.Run("fully wired does not panic", func(t *testing.T) {
		a, s, p, tm, pr := full()
		assert.NotPanics(t, func() {
			e := New(a, s, p, tm, pr)
			require.NotNil(t, e)
			assert.NotNil(t, e.completers, "New initialises the completer map")
		})
	})
}

// A configured policy's stages win over the synthesised route fallback.
//
// This test does NOT pin RULING R7. It cannot: it passes a non-nil policy
// store, so the removed `if e.policies != nil` wrapper would have passed it
// too. R7 -- "a nil policy store must not silently degrade to the
// single-stage fallback" -- is pinned at construction instead, by
// TestNew_PanicsOnNilDependency/nil_policies: a nil store can no longer
// reach PlanStages at all. What this test pins is the complementary half:
// when a policy IS available, PlanStages uses it rather than the fallback.
func TestPlanStages_UsesPolicyStagesNotFallback(t *testing.T) {
	policies := &countingPolicies{}
	e := New(&stubApprovals{}, &stubStageReviews{}, policies, &stubTeams{}, &stubProjects{})

	stages, err := e.PlanStages(uuid.New(), uuid.New(),
		models.ApprovalEntityRoute, models.ApprovalActionCreate)

	require.NoError(t, err)
	require.Len(t, stages, 1, "the real policy's single stage, not the synthesised fallback")
	assert.Equal(t, "route.deploy", stages[0].RequiredPermission,
		"the stage came from the policy store, so the lookup really ran")
	assert.Equal(t, 1, policies.calls)
}

// countingPolicies returns a real policy for any lookup and counts calls.
type countingPolicies struct{ calls int }

func (p *countingPolicies) GetByProjectAndEntity(uuid.UUID, string, *string) (*models.ApprovalPolicy, error) {
	p.calls++
	return &models.ApprovalPolicy{
		Stages: json.RawMessage(`[{"order":1,"required_permission":"route.deploy","team_scope":"any","min_approvers":1}]`),
	}, nil
}

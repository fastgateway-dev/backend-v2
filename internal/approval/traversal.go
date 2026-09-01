package approval

// Traversal: moving an approval through its stages, and creating one in the
// first place.
//
// Before Phase 2D this logic existed twice, in ApprovalService (routes) and
// ClientAttachmentService (client attachments), and the copies had drifted.
// The measured differences are catalogued in
// .superpowers/sdd/2026-08-31-backend-v2-phase-2d/traversal-diff.md; each
// one is resolved here in favour of the variant that fails CLOSED, with a
// comment naming the divergence. Where the diff records no difference, the
// shared behaviour is reproduced as-is -- this is a consolidation, not a
// redesign.

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// Spec describes a submission. It carries every field the four pre-2D
// `&models.Approval{}` construction sites set: RouteService.CreateRoute
// (route_write.go), RouteService.UpdateRoute, RouteService.DeleteRoute and
// ClientAttachmentService.createApproval. Status and Stages are not part of
// the spec -- the engine owns both.
type Spec struct {
	ProjectID   uuid.UUID
	EntityType  models.ApprovalEntityType
	EntityID    uuid.UUID
	Action      models.ApprovalAction
	SubmittedBy uuid.UUID

	ConfigSnapshot json.RawMessage
	PreviousConfig json.RawMessage
	AIReview       json.RawMessage

	ChangeDescription string
}

// Submit plans the approval's stages and persists it. Stage planning runs
// before the write, so a policy that cannot be honoured aborts the
// submission rather than creating an approval with a weaker gate.
func (e *Engine) Submit(spec Spec) (*models.Approval, error) {
	stages, err := e.PlanStages(spec.ProjectID, spec.SubmittedBy, spec.EntityType, spec.Action)
	if err != nil {
		return nil, err
	}

	a := &models.Approval{
		ProjectID:         spec.ProjectID,
		EntityType:        spec.EntityType,
		EntityID:          spec.EntityID,
		Action:            spec.Action,
		ConfigSnapshot:    spec.ConfigSnapshot,
		PreviousConfig:    spec.PreviousConfig,
		SubmittedBy:       spec.SubmittedBy,
		Status:            models.ApprovalStatusPending,
		ChangeDescription: spec.ChangeDescription,
		AIReview:          spec.AIReview,
		Stages:            stages,
	}

	if err := e.approvals.Create(a); err != nil {
		return nil, err
	}
	return a, nil
}

// ApproveStage records one reviewer's approval of a stage. The stage only
// moves to approved once MinApprovers distinct reviewers have approved it;
// the approval only moves to approved once every stage has.
func (e *Engine) ApproveStage(approvalID, stageID uuid.UUID, reviewer *models.User) (*models.Approval, error) {
	// DIVERGENCE 1 -> ApprovalService. The repository error is propagated
	// verbatim. ClientAttachmentService replaced every failure with
	// errors.New("approval not found"), which made a connection error
	// indistinguishable from a missing record.
	approval, err := e.approvals.GetByID(approvalID)
	if err != nil {
		return nil, err
	}

	if approval.Status != models.ApprovalStatusPending {
		return nil, errors.New("approval is not pending")
	}

	// An entity type with no completer can never be completed, so refuse
	// before touching any state rather than after marking stages approved.
	completer, err := e.completerFor(approval.EntityType)
	if err != nil {
		return nil, err
	}

	stage, err := e.authorizeStageReview(approval, stageID, reviewer, "approve")
	if err != nil {
		return nil, err
	}

	// Multi-approver tracking. Unlike both pre-2D copies this is not
	// guarded by a nil check on the store: an unwired review store used to
	// downgrade every MinApprovers>1 stage to a single approver.
	existing, err := e.stages.ListByStageID(stage.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing reviews: %w", err)
	}
	for _, r := range existing {
		if r.ReviewerID == reviewer.ID {
			return nil, errors.New("you have already reviewed this stage")
		}
	}

	if err := e.stages.Create(&models.ApprovalStageReview{
		StageID:    stage.ID,
		ReviewerID: reviewer.ID,
		Decision:   "approved",
	}); err != nil {
		return nil, fmt.Errorf("failed to record review: %w", err)
	}

	count, err := e.stages.CountByStageAndDecision(stage.ID, "approved")
	if err != nil {
		return nil, fmt.Errorf("failed to count reviews: %w", err)
	}
	if count < int64(models.EffectiveMinApprovers(stage.MinApprovers)) {
		// Not enough approvals yet: the review is recorded, the stage stays
		// pending.
		return e.approvals.GetByID(approvalID)
	}

	now := time.Now()
	stage.Status = models.ApprovalStatusApproved
	stage.ReviewedBy = &reviewer.ID
	stage.ReviewedAt = &now

	if err := e.approvals.UpdateStage(stage); err != nil {
		return nil, err
	}

	if allStagesApproved(approval, stage.ID) {
		approval.Status = models.ApprovalStatusApproved
		if err := e.approvals.Update(approval); err != nil {
			return nil, err
		}
		if err := completer.OnApproved(approval); err != nil {
			return nil, err
		}
	}

	// Re-fetch so the caller sees persisted relationships.
	return e.approvals.GetByID(approvalID)
}

// RejectStage rejects a stage, which rejects the whole approval.
func (e *Engine) RejectStage(approvalID, stageID uuid.UUID, reviewer *models.User, comment string) (*models.Approval, error) {
	// DIVERGENCE 4 -> ApprovalService. A rejection must say why, and the
	// check fails fast before the approval is even fetched.
	// ClientAttachmentService had no such guard and stored an empty comment.
	if comment == "" {
		return nil, errors.New("rejection comment is required")
	}

	// DIVERGENCE 1 -> ApprovalService (see ApproveStage).
	approval, err := e.approvals.GetByID(approvalID)
	if err != nil {
		return nil, err
	}

	if approval.Status != models.ApprovalStatusPending {
		return nil, errors.New("approval is not pending")
	}

	completer, err := e.completerFor(approval.EntityType)
	if err != nil {
		return nil, err
	}

	stage, err := e.authorizeStageReview(approval, stageID, reviewer, "reject")
	if err != nil {
		return nil, err
	}

	// Best-effort audit record, exactly as both pre-2D copies did: a failed
	// audit write must not block a rejection, which is the safe direction.
	_ = e.stages.Create(&models.ApprovalStageReview{
		StageID:    stage.ID,
		ReviewerID: reviewer.ID,
		Decision:   "rejected",
	})

	now := time.Now()
	stage.Status = models.ApprovalStatusRejected
	stage.ReviewedBy = &reviewer.ID
	stage.ReviewedAt = &now
	stage.Comment = comment

	if err := e.approvals.UpdateStage(stage); err != nil {
		return nil, err
	}

	approval.Status = models.ApprovalStatusRejected
	if err := e.approvals.Update(approval); err != nil {
		return nil, err
	}

	// DIVERGENCE 5 -> ApprovalService. Post-rejection side effects are the
	// completer's job and their errors propagate.
	// ClientAttachmentService.RejectStage inlined the attachment update and
	// threw away both the lookup error and the Update error, reporting
	// success while the attachment stayed un-rejected.
	if err := completer.OnRejected(approval); err != nil {
		return nil, err
	}

	return e.approvals.GetByID(approvalID)
}

// Cancel withdraws an approval that has not been deployed yet. Permitted
// by the submitter, an instance owner, a project admin, or whatever extra
// right the entity's completer grants via CancelAuthorizer.
func (e *Engine) Cancel(approvalID uuid.UUID, user *models.User) (*models.Approval, error) {
	approval, err := e.approvals.GetByID(approvalID)
	if err != nil {
		return nil, err
	}

	if approval.Status != models.ApprovalStatusPending && approval.Status != models.ApprovalStatusApproved {
		return nil, errors.New("approval cannot be cancelled in its current state")
	}

	completer, err := e.completerFor(approval.EntityType)
	if err != nil {
		return nil, err
	}

	isSubmitter := approval.SubmittedBy == user.ID
	isOwner := user.Role == models.UserRoleOwner
	isProjectAdmin := false
	if !isOwner {
		// The error is deliberately discarded: a failed admin lookup means
		// "not an admin", which denies. Both pre-2D copies did the same.
		isProjectAdmin, _ = e.projects.IsAdmin(approval.ProjectID, user.ID)
	}

	permitted := isSubmitter || isOwner || isProjectAdmin
	if !permitted {
		// Entity-specific extra right; for routes this is "member of the
		// route's owning team" (pre-2D approval_service.go:527-535).
		if auth, ok := completer.(CancelAuthorizer); ok {
			permitted = auth.CanCancel(approval, user)
		}
	}
	if !permitted {
		return nil, errors.New("you do not have permission to cancel this approval")
	}

	approval.Status = models.ApprovalStatusCancelled
	if err := e.approvals.Update(approval); err != nil {
		return nil, err
	}

	if err := completer.OnCancelled(approval); err != nil {
		return nil, err
	}

	return e.approvals.GetByID(approvalID)
}

// authorizeStageReview resolves the target stage and decides whether the
// reviewer may act on it. ApproveStage and RejectStage share it because
// both pre-2D copies applied the identical checks in both methods; verb is
// "approve" or "reject" and only shapes the self-review message.
func (e *Engine) authorizeStageReview(
	approval *models.Approval,
	stageID uuid.UUID,
	reviewer *models.User,
	verb string,
) (*models.ApprovalStage, error) {
	// DIVERGENCE 2 -> ApprovalService. The stage is found and validated
	// FIRST; self-review is checked afterwards. ClientAttachmentService
	// checked self-review first and so never noticed a bogus stage ID on
	// that path.
	stage := findStage(approval, stageID)
	if stage == nil {
		return nil, errors.New("stage not found in this approval")
	}

	if stage.Status != models.ApprovalStatusPending {
		return nil, errors.New("stage is not pending")
	}

	if approval.SubmittedBy == reviewer.ID {
		// The pre-2D `if s.projectRepo != nil` guard is gone: ProjectLookup
		// is not optional, so this check now always runs. A failed project
		// lookup leaves allowed=false, i.e. it denies -- as both copies did.
		allowed := false
		if project, err := e.projects.GetByID(approval.ProjectID); err == nil && project.SelfApprovalAllowed {
			allowed = true
		}
		if !allowed {
			return nil, fmt.Errorf("submitter cannot %s their own submission", verb)
		}
	}

	// Sequential stages: every lower-order stage must already be approved.
	// The comparison is by StageOrder, not by slice index (traversal-diff
	// item 11): ApprovalService relied on having sorted approval.Stages in
	// place first, ClientAttachmentService filtered by the attribute. The
	// attribute filter is order-independent and cannot be defeated by an
	// out-of-order slice.
	for _, st := range approval.Stages {
		if st.StageOrder < stage.StageOrder && st.Status != models.ApprovalStatusApproved {
			return nil, errors.New("previous stages must be approved first")
		}
	}

	// Instance owners and project admins bypass the permission and team
	// checks -- but not the self-review check above.
	isOwner := reviewer.Role == models.UserRoleOwner
	isProjectAdmin := false
	if !isOwner {
		isProjectAdmin, _ = e.projects.IsAdmin(approval.ProjectID, reviewer.ID)
	}
	if isOwner || isProjectAdmin {
		return stage, nil
	}

	// DIVERGENCE 3 -> ApprovalService. The permission check is
	// UNCONDITIONAL. ClientAttachmentService skipped it entirely when
	// stage.RequiredPermission was empty, which let any project member
	// review such a stage -- a fail-open. Here an empty permission is
	// passed through and denied by the lookup, so a misconfigured stage is
	// unreviewable rather than unguarded.
	hasPerm, err := e.teams.HasPermissionInProject(
		approval.ProjectID, reviewer.ID, models.Permission(stage.RequiredPermission))
	if err != nil {
		return nil, err
	}
	if !hasPerm {
		return nil, errors.New("reviewer does not have the required permission")
	}

	if stage.RequiredTeamID != nil {
		isMember, err := e.teams.IsMember(*stage.RequiredTeamID, reviewer.ID)
		if err != nil {
			return nil, err
		}
		if !isMember {
			return nil, errors.New("reviewer is not a member of the required team")
		}
	}

	return stage, nil
}

// findStage returns a pointer into approval.Stages, or nil. Unlike
// ApprovalService.findStage it does not sort the caller's slice in place
// and does not return an index: nothing downstream depends on position
// (see authorizeStageReview and allStagesApproved).
func findStage(approval *models.Approval, stageID uuid.UUID) *models.ApprovalStage {
	for i := range approval.Stages {
		if approval.Stages[i].ID == stageID {
			return &approval.Stages[i]
		}
	}
	return nil
}

// allStagesApproved reports whether every stage is approved, treating
// justApproved as approved. The stage pointer returned by findStage aliases
// the slice element, so this is belt-and-braces -- but it is also what
// makes the function correct if a caller ever passes a detached stage.
func allStagesApproved(approval *models.Approval, justApproved uuid.UUID) bool {
	for _, st := range approval.Stages {
		if st.ID == justApproved {
			continue
		}
		if st.Status != models.ApprovalStatusApproved {
			return false
		}
	}
	return true
}

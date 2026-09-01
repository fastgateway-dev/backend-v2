package services

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// legalTransitions is the complete route status transition table.
//
// PROVENANCE. It is derived from the 24 assignment sites that existed before
// Phase 2D, enumerated in
// .superpowers/sdd/2026-08-31-backend-v2-phase-2d/transitions.md.
//
// That enumeration records 12 of those 24 sites with `From = ANY`. `ANY` is a
// STATIC fact -- "this line has no runtime check on route.Status" -- and NOT a
// claim that every status is reachable there. Transcribing the ANY rows
// literally would legalize almost every state pair and the machine would
// validate nothing, which is the exact failure this table exists to prevent.
//
// So each unguarded site was instead resolved to the set of statuses a route
// can ACTUALLY hold when that line executes. Those derivations, and the
// reasoning behind each, live in the "Derived from-sets" section of
// transitions.md and are mirrored row-by-row in observedTransitions
// (route_state_internal_test.go), which
// TestLegalTransitions_CoversEveryObservedTransition checks this table against.
//
// Every entry below therefore traces to a site. Adding one is a behaviour
// change and needs its own justification in transitions.md; it is not
// something to do while migrating a call site.
var legalTransitions = map[models.RouteStatus][]models.RouteStatus{
	// A route awaiting its creation approval. The approval can complete
	// (-> approved, route_approval.go OnApproved/create; route_write.go Create
	// on the approvals-disabled path) or be rejected (-> rejected,
	// OnRejected/create).
	//
	// pending_update and pending_delete are both here because Update
	// (route_write.go:611-615) and Delete (:899-903) gate on byte-identical
	// preconditions -- "no pending approval for this route" and nothing else
	// -- so whatever one admits, the other admits. An orphaned pending_create
	// route (Create persisted the row, then approvals.Submit failed) satisfies
	// that precondition, and must stay both revisable and deletable.
	// TestRouteService_Delete_PendingCreateRoute exercises the delete half;
	// the update half has no test today, which is a coverage gap, not a
	// difference in the code.
	//
	// A cancelled creation approval does not appear here: OnCancelled/create
	// deletes the row rather than moving its status.
	//
	// pending_deploy was ADDED IN FIX ROUND 1 of Task 10+11. The
	// attach/detach fast paths (client_attachment_service.go) read
	// route.Status nowhere, and project.ApprovalEnabled is toggleable at
	// runtime (ProjectService.UpdateProject), so a route created while
	// approvals were on -- or orphaned at pending_create by a failed
	// approvals.Submit -- can have a client attached to it after approvals
	// are turned off.
	models.RouteStatusPendingCreate: {
		models.RouteStatusApproved,
		models.RouteStatusRejected,
		models.RouteStatusPendingUpdate,
		models.RouteStatusPendingDelete,
		models.RouteStatusPendingDeploy,
	},

	// A route with an update approval in flight. Approval sends it to the
	// deploy queue; rejection and cancellation both put the still-deployed
	// route back to active.
	//
	// pending_delete was ADDED IN THE FINAL FIX WAVE, and it is the same
	// orphan class the pending_create row above already admits. Update
	// (route_write.go:665-675) persists pending_update BEFORE calling
	// approvals.Submit, so a failed Submit leaves the route at
	// pending_update with NO pending approval -- which is precisely the
	// precondition Delete gates on (:899-903). Deleting such a route worked
	// before Phase 2D and must keep working.
	//
	// Phase 2D makes the orphan MORE likely, not less: internal/approval/
	// planning.go:120-159 now errors on repository failures, unknown scopes
	// and a submitter belonging to no team, where the pre-2D path did not.
	// An instance owner or project admin passes middleware/permissions.go:63
	// with no team membership at all, so an owner submitting under a
	// submitter_team policy orphans deterministically.
	models.RouteStatusPendingUpdate: {
		models.RouteStatusPendingDeploy,
		models.RouteStatusActive,
		models.RouteStatusPendingDelete,
	},

	// Symmetric to pending_update, for a delete approval -- including the
	// orphan case: Delete (route_write.go:911-916) likewise persists
	// pending_delete before submitting, and Update's precondition
	// (:611-615) is byte-identical to Delete's. Whatever one admits, the
	// other admits.
	models.RouteStatusPendingDelete: {
		models.RouteStatusPendingDeploy,
		models.RouteStatusActive,
		models.RouteStatusPendingUpdate,
	},

	// Approved creation, not yet pushed to Kubernetes. Deploy takes it live;
	// Update and Delete are both reachable because their only precondition is
	// "no pending approval for this route", which an approved route satisfies.
	//
	// pending_deploy was ADDED IN FIX ROUND 1 of Task 10+11, and this is the
	// pair that was actually breaking production traffic. In a project with
	// ApprovalEnabled=false, Create leaves the route at approved
	// (route_write.go Create/approvals-disabled) -- NOT active -- and the
	// attach fast paths then move it to pending_deploy without reading
	// route.Status at all. The ordinary flow create -> attach client ->
	// deploy therefore failed at the attach. See the "Fix round 1" section of
	// task-10-11-report.md.
	models.RouteStatusApproved: {
		models.RouteStatusActive,
		models.RouteStatusPendingUpdate,
		models.RouteStatusPendingDelete,
		models.RouteStatusPendingDeploy,
	},

	// Live in Kubernetes. Everything that changes a live route's shape --
	// the cascade in ClientService, client attach/detach, and an approved
	// attachment -- routes through pending_deploy; Update and Delete open
	// their respective approvals.
	//
	// active is no longer the ONLY origin of pending_deploy: fix round 1 of
	// Task 10+11 established that the attach/detach fast paths reach it from
	// approved, rejected and pending_create too. See those rows.
	models.RouteStatusActive: {
		models.RouteStatusPendingDeploy,
		models.RouteStatusPendingUpdate,
		models.RouteStatusPendingDelete,
	},

	// A rejected creation. The route was never deployed, but it is still a
	// row the owner can revise (-> pending_update) or clean up
	// (-> pending_delete). It cannot go straight back to active or approved:
	// nothing re-approves a route without a fresh approval.
	//
	// pending_deploy was ADDED IN FIX ROUND 1 of Task 10+11, for the same
	// reason as the pending_create row: a rejected route can only exist in a
	// project where approvals were once on, and ApprovalEnabled is toggleable
	// at runtime, so the unguarded attach fast paths reach it. The route is
	// still not re-approved by this -- pending_deploy only queues it for a
	// deploy that the route team must still trigger.
	models.RouteStatusRejected: {
		models.RouteStatusPendingUpdate,
		models.RouteStatusPendingDelete,
		models.RouteStatusPendingDeploy,
	},

	// Approved changes queued for deployment. Deploy takes them live; a
	// further Update or Delete may be submitted on top (no approval is
	// pending, so route_write.go's precondition is satisfied).
	models.RouteStatusPendingDeploy: {
		models.RouteStatusActive,
		models.RouteStatusPendingUpdate,
		models.RouteStatusPendingDelete,
	},
}

// routeStateMachine is the only code permitted to assign route.Status.
// Before Phase 2D that happened at 24 sites across 5 files with no
// validation, so any service could move a route to any state.
type routeStateMachine struct {
	repo repository.RouteRepositoryInterface
}

// To validates and persists a status transition. reason is recorded in the
// error on rejection and is what makes an illegal transition diagnosable.
// A no-op transition (next == current) is allowed and does not write.
//
// CONTRACT -- To owns route.Status and nothing else.
//
// A caller that has ALSO mutated other fields on route must not rely on To as
// its only write. On the no-op path To returns nil without calling
// repo.Update, so those mutations are silently discarded. That is deliberate:
// a state machine that writes when no state changed would make every
// no-op transition a hidden persistence point, and callers could no longer
// tell a write from a nothing-happened.
//
// There are two live examples, both of which persist explicitly when the route
// already sits at the target status; see the comments at each.
//
//   - RouteService.OnApproved applies the approved config snapshot to route
//     before transitioning. Pre-2D that path did an unconditional
//     routeRepo.Update, and losing the snapshot would be silent data loss
//     rather than a transition narrowing.
//   - RouteService.Update (route_write.go) applies the caller's Description
//     and Labels edits before transitioning to pending_update. An orphaned
//     route already at pending_update -- Create/Update persists the status
//     before calling approvals.Submit, so a failed submit leaves one -- would
//     otherwise have those edits dropped on a retry.
func (m *routeStateMachine) To(route *models.Route, next models.RouteStatus, reason string) error {
	if route == nil {
		return fmt.Errorf("route state: nil route (%s)", reason)
	}
	if route.Status == next {
		return nil
	}
	allowed, ok := legalTransitions[route.Status]
	if !ok {
		return fmt.Errorf("route state: no transitions defined from %q (route %s, %s)",
			route.Status, route.ID, reason)
	}
	for _, candidate := range allowed {
		if candidate == next {
			route.Status = next
			return m.repo.Update(route)
		}
	}
	return fmt.Errorf("route state: illegal transition %q -> %q (route %s, %s)",
		route.Status, next, route.ID, reason)
}

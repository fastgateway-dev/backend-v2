package services

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// TransitionSite names one call site of routeStateMachine.To.
//
// PHASE 2E TASK 11 (ruling R12). Before this task legalTransitions was keyed
// on (from, to) only, so ANY site could perform ANY transition that some
// OTHER site was entitled to produce. Phase 2D recorded two consequences of
// that and could not fix either without this key:
//
//   - transitions.md, "Known residual gaps" item 3 -- the detach fast path
//     accepted the approved/rejected/pending_create origins that only the two
//     ATTACH fast paths can actually reach.
//   - The concrete hazard the final review found: OnRejected/update and
//     OnCancelled/update could take a pending_deploy route to active, because
//     pending_deploy -> active is legal for the DEPLOY site. That silently
//     discards a queued redeploy -- the route stops being scheduled for a
//     push it still needs.
//
// The constant's string value is its own identifier so that a rejection names
// the site in the error; the doc comment on each carries the source location.
type TransitionSite string

const (
	// SiteRouteCreateFastPath is route_write.go Create, approvals-disabled
	// branch. The route was just persisted at pending_create.
	SiteRouteCreateFastPath TransitionSite = "SiteRouteCreateFastPath"

	// SiteRouteUpdate is route_write.go Update, the pending_update
	// assignment. Enumeration site #19.
	SiteRouteUpdate TransitionSite = "SiteRouteUpdate"

	// SiteRouteUpdateFastPath is route_write.go Update, approvals-disabled
	// branch. Enumeration site #20.
	SiteRouteUpdateFastPath TransitionSite = "SiteRouteUpdateFastPath"

	// SiteRouteDelete is route_write.go Delete, the pending_delete
	// assignment. Enumeration site #21.
	SiteRouteDelete TransitionSite = "SiteRouteDelete"

	// SiteRouteDeleteFastPath is route_write.go Delete, approvals-disabled
	// branch. Enumeration site #22.
	SiteRouteDeleteFastPath TransitionSite = "SiteRouteDeleteFastPath"

	// SiteApprovalApproved is route_approval.go RouteService.OnApproved.
	// Enumeration sites #1, #2, #3.
	SiteApprovalApproved TransitionSite = "SiteApprovalApproved"

	// SiteApprovalRejected is route_approval.go RouteService.OnRejected.
	// Enumeration sites #4, #5, #6.
	SiteApprovalRejected TransitionSite = "SiteApprovalRejected"

	// SiteApprovalCancelled is route_approval.go RouteService.OnCancelled,
	// the update/delete case. Enumeration site #7. (A cancelled create
	// deletes the row instead of moving its status.)
	SiteApprovalCancelled TransitionSite = "SiteApprovalCancelled"

	// SiteAttachFromRoute is client_attachment_service.go AttachFromRoute,
	// approvals-disabled branch. Enumeration site #8.
	SiteAttachFromRoute TransitionSite = "SiteAttachFromRoute"

	// SiteAttachFromClient is client_attachment_service.go AttachFromClient,
	// approvals-disabled branch. Enumeration site #9.
	SiteAttachFromClient TransitionSite = "SiteAttachFromClient"

	// SiteRequestDetach is client_attachment_service.go RequestDetach,
	// approvals-disabled branch. Enumeration site #10.
	SiteRequestDetach TransitionSite = "SiteRequestDetach"

	// SiteAttachmentApproved is client_attachment_service.go
	// updateRouteStatus, reached only from ClientAttachmentService.OnApproved
	// under its route.Status == active guard.
	SiteAttachmentApproved TransitionSite = "SiteAttachmentApproved"

	// SiteClientCascade is client_service.go cascadeToAttachedRoutes, the
	// single implementation behind the five cascade* methods. Guarded on
	// route.Status == active.
	SiteClientCascade TransitionSite = "SiteClientCascade"

	// SiteDeploy is route_deploy.go Deploy. Enumeration sites #23, #24.
	SiteDeploy TransitionSite = "SiteDeploy"
)

// legalTransitions is the complete route status transition table, keyed by
// (site, from, to).
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
// TestLegalTransitions_CoversEveryObservedTransition checks this table against
// PER SITE, and TestLegalTransitions_HasNoUnobservedEntry checks in the other
// direction, also per site. The two together pin exact set equality.
//
// Every entry below therefore traces to a site -- now literally, not just in
// the comments. Adding one is a behaviour change and needs its own
// justification in transitions.md; it is not something to do while migrating
// a call site.
//
// PHASE 2E TASK 11 re-keyed this map. The UNION over sites is unchanged: the
// same 24 distinct (from, to) pairs are legal somewhere as before. What
// changed is that each is now legal only where it is actually produced.
var legalTransitions = map[TransitionSite]map[models.RouteStatus][]models.RouteStatus{

	// --- route_write.go ---------------------------------------------------

	// Create's approvals-disabled branch. The route was persisted at
	// pending_create by the struct literal a few lines above, so this is the
	// only pair the site can produce.
	SiteRouteCreateFastPath: {
		models.RouteStatusPendingCreate: {models.RouteStatusApproved},
	},

	// Update, site #19. Its ONLY precondition is "no pending approval for
	// this route" (route_write.go:609-613); it reads route.Status nowhere
	// else. Delete's precondition is byte-identical (:887-891), so whatever
	// one admits the other admits -- which is what forces the two orphan
	// origins below.
	//
	//   - pending_create: Create persists the row, then approvals.Submit can
	//     fail, leaving an orphan with no approval attached. It must stay
	//     revisable, not merely deletable (fix round 1 of Task 10+11).
	//   - pending_delete: the same orphan class one step out. Delete persists
	//     pending_delete BEFORE submitting, so a failed Submit leaves a route
	//     Update must still reach (final fix wave of Phase 2D).
	//   - approved / active / rejected / pending_deploy: none carries a
	//     PENDING approval, so all four satisfy the precondition. rejected is
	//     the intended revise-and-resubmit recovery; pending_deploy carries an
	//     already-approved approval, so a further update stacks on it.
	//
	// pending_update -> pending_update is a no-op and never consults this
	// table; Update persists such an orphan explicitly so its Description and
	// Labels edits are not dropped.
	SiteRouteUpdate: {
		models.RouteStatusPendingCreate: {models.RouteStatusPendingUpdate},
		models.RouteStatusPendingDelete: {models.RouteStatusPendingUpdate},
		models.RouteStatusApproved:      {models.RouteStatusPendingUpdate},
		models.RouteStatusActive:        {models.RouteStatusPendingUpdate},
		models.RouteStatusRejected:      {models.RouteStatusPendingUpdate},
		models.RouteStatusPendingDeploy: {models.RouteStatusPendingUpdate},
	},

	// Update's approvals-disabled branch, site #20. It runs after the
	// pending_update assignment above has already persisted, so the route is
	// at pending_update and nothing else.
	SiteRouteUpdateFastPath: {
		models.RouteStatusPendingUpdate: {models.RouteStatusPendingDeploy},
	},

	// Delete, site #21. Exactly the mirror of SiteRouteUpdate: the two
	// preconditions are byte-identical and neither function reads
	// route.Status anywhere else. pending_update is here for the same orphan
	// reason pending_delete is under SiteRouteUpdate.
	// TestRouteService_Delete_PendingCreateRoute corroborates the
	// pending_create origin but is not its justification.
	SiteRouteDelete: {
		models.RouteStatusPendingCreate: {models.RouteStatusPendingDelete},
		models.RouteStatusPendingUpdate: {models.RouteStatusPendingDelete},
		models.RouteStatusApproved:      {models.RouteStatusPendingDelete},
		models.RouteStatusActive:        {models.RouteStatusPendingDelete},
		models.RouteStatusRejected:      {models.RouteStatusPendingDelete},
		models.RouteStatusPendingDeploy: {models.RouteStatusPendingDelete},
	},

	// Delete's approvals-disabled branch, site #22. Symmetric to
	// SiteRouteUpdateFastPath.
	SiteRouteDeleteFastPath: {
		models.RouteStatusPendingDelete: {models.RouteStatusPendingDeploy},
	},

	// --- route_approval.go ------------------------------------------------

	// OnApproved, sites #1/#2/#3. Argument A1: the callback runs only when an
	// approval for THIS route reaches a terminal state, and the Create /
	// Update / Delete that submitted it persisted the matching pending_*
	// status first. So the action and the from-status move together --
	// create from pending_create, update from pending_update, delete from
	// pending_delete.
	SiteApprovalApproved: {
		models.RouteStatusPendingCreate: {models.RouteStatusApproved},
		models.RouteStatusPendingUpdate: {models.RouteStatusPendingDeploy},
		models.RouteStatusPendingDelete: {models.RouteStatusPendingDeploy},
	},

	// OnRejected, sites #4/#5/#6. Same A1 chain. A rejected update or delete
	// returns the still-deployed route to active.
	//
	// THIS IS THE SITE THE RE-KEYING WAS FOR. pending_deploy is deliberately
	// absent: pending_deploy -> active is legal at SiteDeploy, and under the
	// old global key that made it legal here too. A rejected approval landing
	// on a route with a queued redeploy would have flipped it to active and
	// silently discarded the queued redeploy.
	SiteApprovalRejected: {
		models.RouteStatusPendingCreate: {models.RouteStatusRejected},
		models.RouteStatusPendingUpdate: {models.RouteStatusActive},
		models.RouteStatusPendingDelete: {models.RouteStatusActive},
	},

	// OnCancelled, site #7. One case, two actions, so two origins. A
	// cancelled create deletes the row rather than moving its status, so it
	// contributes no entry. Same pending_deploy exclusion as OnRejected, for
	// the same reason.
	SiteApprovalCancelled: {
		models.RouteStatusPendingUpdate: {models.RouteStatusActive},
		models.RouteStatusPendingDelete: {models.RouteStatusActive},
	},

	// --- client_attachment_service.go -------------------------------------

	// AttachFromRoute's approvals-disabled fast path, site #8. Argument A3:
	// the function reads route.Status NOWHERE (verified line by line), so its
	// from-set is "what status can a route hold in a project with
	// ApprovalEnabled == false", and ApprovalEnabled is a runtime toggle
	// (ProjectService.UpdateProject). transitions.md derives all six non-self
	// statuses:
	//
	//   - approved is the ORDINARY case and the one that broke in Task 10+11:
	//     Create on the approvals-disabled path leaves the route at approved,
	//     never active, so create -> attach -> deploy failed at the attach.
	//   - active is a deployed route, the original mainline.
	//   - rejected and pending_create arrive via the toggle (and
	//     pending_create additionally as a failed-Submit orphan).
	//   - pending_update / pending_delete: the same toggle. The global table
	//     already carried both pairs from sites #2/#3 and #20/#22, so the
	//     fixture recorded them there; under per-site keying they must be
	//     stated here too, because this site can genuinely reach them.
	//   - pending_deploy -> pending_deploy is a no-op, handled by To.
	SiteAttachFromRoute: {
		models.RouteStatusActive:        {models.RouteStatusPendingDeploy},
		models.RouteStatusApproved:      {models.RouteStatusPendingDeploy},
		models.RouteStatusRejected:      {models.RouteStatusPendingDeploy},
		models.RouteStatusPendingCreate: {models.RouteStatusPendingDeploy},
		models.RouteStatusPendingUpdate: {models.RouteStatusPendingDeploy},
		models.RouteStatusPendingDelete: {models.RouteStatusPendingDeploy},
	},

	// AttachFromClient's fast path, site #9. Identical body for this purpose;
	// the two entry points differ only in which side submits.
	SiteAttachFromClient: {
		models.RouteStatusActive:        {models.RouteStatusPendingDeploy},
		models.RouteStatusApproved:      {models.RouteStatusPendingDeploy},
		models.RouteStatusRejected:      {models.RouteStatusPendingDeploy},
		models.RouteStatusPendingCreate: {models.RouteStatusPendingDeploy},
		models.RouteStatusPendingUpdate: {models.RouteStatusPendingDeploy},
		models.RouteStatusPendingDelete: {models.RouteStatusPendingDeploy},
	},

	// RequestDetach's fast path, site #10. This site's from-set is genuinely
	// NARROWER than the two attach sites', and per-site keying is what finally
	// lets that be enforced -- transitions.md, "Known residual gaps" item 3
	// recorded it as admitted-but-wrong under the global key.
	//
	// It is guarded on attachment.Status == active; an attachment only becomes
	// active inside a successful Deploy, which sets route.Status = active in
	// the same call. So the route was active at that moment and can only have
	// moved on from there -- to pending_update or pending_delete (residual gap
	// item 2: an attachment stays active across a later Update, so detach on
	// an in-flight route is reachable). It can NEVER be back at approved,
	// rejected or pending_create, and those three are correspondingly absent.
	SiteRequestDetach: {
		models.RouteStatusActive:        {models.RouteStatusPendingDeploy},
		models.RouteStatusPendingUpdate: {models.RouteStatusPendingDeploy},
		models.RouteStatusPendingDelete: {models.RouteStatusPendingDeploy},
	},

	// updateRouteStatus, reached only from ClientAttachmentService.OnApproved
	// under an explicit route.Status == active guard. (The assignment that
	// used to sit in OnApproved was dead -- updateRouteStatus re-fetches its
	// own copy -- and was deleted in Task 10+11. The guard is what makes this
	// site {active}.)
	SiteAttachmentApproved: {
		models.RouteStatusActive: {models.RouteStatusPendingDeploy},
	},

	// --- client_service.go ------------------------------------------------

	// cascadeToAttachedRoutes, the single implementation behind
	// cascadeIPChangeToRoutes, cascadeMethodChangeToRoutes,
	// cascadeHeaderChangeToRoutes, cascadeAPIKeyChangeToRoutes and
	// cascadeJWTChangeToRoutes. It skips any route that is not active, so
	// only routes live in Kubernetes are re-queued.
	SiteClientCascade: {
		models.RouteStatusActive: {models.RouteStatusPendingDeploy},
	},

	// --- route_deploy.go --------------------------------------------------

	// Deploy, sites #23/#24. Its entry guard admits approved (a first deploy)
	// and pending_deploy (a queued redeploy) and nothing else; the delete
	// action returns earlier, having removed the row.
	SiteDeploy: {
		models.RouteStatusApproved:      {models.RouteStatusActive},
		models.RouteStatusPendingDeploy: {models.RouteStatusActive},
	},
}

// routeStateMachine is the only code permitted to assign route.Status.
// Before Phase 2D that happened at 24 sites across 5 files with no
// validation, so any service could move a route to any state.
type routeStateMachine struct {
	repo repository.RouteRepositoryInterface
}

// To validates and persists a status transition for one named call site.
// reason is recorded in the error on rejection and is what makes an illegal
// transition diagnosable. A no-op transition (next == current) is allowed and
// does not write.
//
// site selects the row of legalTransitions to validate against. A transition
// that is legal SOMEWHERE is not thereby legal HERE -- see the
// SiteApprovalRejected entry for the case that motivated the key.
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
func (m *routeStateMachine) To(site TransitionSite, route *models.Route, next models.RouteStatus, reason string) error {
	if route == nil {
		return fmt.Errorf("route state: nil route at site %s (%s)", site, reason)
	}
	if route.Status == next {
		return nil
	}
	byStatus, ok := legalTransitions[site]
	if !ok {
		return fmt.Errorf("route state: unknown transition site %s (route %s, %s)",
			site, route.ID, reason)
	}
	allowed, ok := byStatus[route.Status]
	if !ok {
		return fmt.Errorf("route state: no transitions defined from %q at site %s (route %s, %s)",
			route.Status, site, route.ID, reason)
	}
	for _, candidate := range allowed {
		if candidate == next {
			route.Status = next
			return m.repo.Update(route)
		}
	}
	return fmt.Errorf("route state: illegal transition %q -> %q at site %s (route %s, %s)",
		route.Status, next, site, route.ID, reason)
}

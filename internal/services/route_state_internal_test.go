package services

import (
	"encoding/json"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// routeStatusAny is a sentinel used only within this fixture to mark a
// transition whose originating status is NOT constrained by any guard in
// the current code -- see the "Unguarded sites" section of
// .superpowers/sdd/2026-08-31-backend-v2-phase-2d/transitions.md. It is not
// a real models.RouteStatus value ever written by production code; it
// exists so this table can still record "one entry per distinct pair" for
// sites where the pre-2D code performs no check at all before writing
// route.Status. Task 9 must treat rows with this From value as flagged
// findings, not as a licence to legalize a transition from every status.
//
// TASK 9 RESOLUTION (controller ruling R5). No row below carries it any
// more. `ANY` states that a line has no runtime guard; it does not state
// that every status is reachable at that line. Each of the 12 unguarded
// sites was resolved to the set of statuses a route can actually hold when
// the line executes -- the "Derived from-sets" section of transitions.md
// records the reasoning site by site, and every derived row below carries a
// one-line version in Why. The constant survives so
// TestObservedTransitions_NoUnresolvedANY can pin that resolution: if a
// future enumeration reintroduces an ANY row, that test fails rather than
// the row quietly widening legalTransitions to "everything reaches
// everything".
const routeStatusAny models.RouteStatus = "ANY"

// observedTransitions is the (from, to) set the 24 pre-2D assignment sites
// can produce, enumerated in
// .superpowers/sdd/2026-08-31-backend-v2-phase-2d/transitions.md.
//
// Task 9's legalTransitions table is built from THIS. Rows whose Derived
// field is true replace an `ANY` row from the original enumeration with the
// statuses actually reachable at that site; rows with Derived false are
// transcribed unchanged from a site that already had a real runtime guard.
//
// Line numbers in Site are as of Task 9. The approval callbacks moved out of
// ApprovalService into RouteService.OnApproved/OnRejected/OnCancelled
// (route_approval.go) in Tasks 7-8, so transitions.md's
// approval_service.go:4xx/5xx references no longer resolve; its analysis
// still does.
var observedTransitions = []struct {
	From    models.RouteStatus
	To      models.RouteStatus
	Site    string
	Derived bool
	Why     string
}{
	// --- #1 route_approval.go:65, OnApproved case Create (was ANY) --------
	{
		From: models.RouteStatusPendingCreate, To: models.RouteStatusApproved,
		Site: "route_approval.go:65 OnApproved/create", Derived: true,
		Why: "reached only when a route-creation approval completes; Create() persists pending_create before submitting that approval, and Update/Delete are refused while one is pending",
	},

	// --- #2 route_approval.go:65, OnApproved case Update (was ANY) --------
	{
		From: models.RouteStatusPendingUpdate, To: models.RouteStatusPendingDeploy,
		Site: "route_approval.go:65 OnApproved/update", Derived: true,
		Why: "Update() persists pending_update before submitting the update approval this callback completes",
	},

	// --- #3 route_approval.go:65, OnApproved case Delete (was ANY) --------
	{
		From: models.RouteStatusPendingDelete, To: models.RouteStatusPendingDeploy,
		Site: "route_approval.go:65 OnApproved/delete", Derived: true,
		Why: "Delete() persists pending_delete before submitting the delete approval this callback completes",
	},

	// --- #4 route_approval.go:89, OnRejected case Create (was ANY) --------
	{
		From: models.RouteStatusPendingCreate, To: models.RouteStatusRejected,
		Site: "route_approval.go:89 OnRejected/create", Derived: true,
		Why: "same causal chain as #1: only a pending route-creation approval can be rejected here",
	},

	// --- #5 route_approval.go:89, OnRejected case Update (was ANY) --------
	{
		From: models.RouteStatusPendingUpdate, To: models.RouteStatusActive,
		Site: "route_approval.go:89 OnRejected/update", Derived: true,
		Why: "same causal chain as #2; a rejected update returns the still-deployed route to active",
	},

	// --- #6 route_approval.go:89, OnRejected case Delete (was ANY) --------
	{
		From: models.RouteStatusPendingDelete, To: models.RouteStatusActive,
		Site: "route_approval.go:89 OnRejected/delete", Derived: true,
		Why: "same causal chain as #3; a rejected delete returns the still-deployed route to active",
	},

	// --- #7 route_approval.go:111, OnCancelled update/delete (was ANY) ----
	// One case, two actions, so two origins.
	{
		From: models.RouteStatusPendingUpdate, To: models.RouteStatusActive,
		Site: "route_approval.go:111 OnCancelled/update", Derived: true,
		Why: "only an in-flight update approval can be cancelled, and Update() persisted pending_update before submitting it",
	},
	{
		From: models.RouteStatusPendingDelete, To: models.RouteStatusActive,
		Site: "route_approval.go:111 OnCancelled/delete", Derived: true,
		Why: "only an in-flight delete approval can be cancelled, and Delete() persisted pending_delete before submitting it",
	},

	// --- #8 AttachFromRoute/approvals-disabled (was ANY) -----------------
	//
	// CORRECTED IN FIX ROUND 1 of Task 10+11. The Task 9 derivation was
	// {active}, argued from the approvals-ENABLED counterpart
	// (ClientAttachmentService.OnApproved) being guarded on
	// route.Status == active, and flagged at the time as a consistency
	// argument rather than a reachability proof. It was wrong, and it broke
	// an ordinary production flow:
	//
	//   - Neither AttachFromRoute nor AttachFromClient reads route.Status
	//     anywhere in its body. Verified line by line, not inferred.
	//   - In a project with ApprovalEnabled=false, RouteService.Create leaves
	//     the route at APPROVED, not active. So create route -> attach client
	//     -> deploy hit `illegal transition "approved" -> "pending_deploy"`.
	//   - ApprovalEnabled is toggleable at runtime
	//     (ProjectService.UpdateProject), so a route may have reached ANY
	//     status while approvals were on and then meet the fast path once
	//     they are off. That is what puts rejected and pending_create here.
	//
	// The from-set is therefore every status the route can hold at all,
	// minus pending_deploy itself (a no-op, which To handles without
	// consulting the table). pending_update and pending_delete are listed
	// under #2/#3 and #20/#22 already, so only the three new origins appear
	// below. Tests:
	// TestClientAttachmentService_AttachFromRoute_FastPath_* in
	// client_attachment_service_test.go, added in the same round because
	// these paths had no coverage whatsoever.
	{
		From: models.RouteStatusActive, To: models.RouteStatusPendingDeploy,
		Site: "client_attachment_service.go AttachFromRoute/approvals-disabled", Derived: true,
		Why: "the mainline: attaching a client to a live route",
	},
	{
		From: models.RouteStatusApproved, To: models.RouteStatusPendingDeploy,
		Site: "client_attachment_service.go AttachFromRoute/approvals-disabled", Derived: true,
		Why: "Create in an approvals-disabled project leaves the route at approved, and this function reads route.Status nowhere; create -> attach -> deploy is the ordinary flow",
	},
	{
		From: models.RouteStatusRejected, To: models.RouteStatusPendingDeploy,
		Site: "client_attachment_service.go AttachFromRoute/approvals-disabled", Derived: true,
		Why: "a rejected route implies approvals were on when it was created; ApprovalEnabled is toggleable at runtime, so the unguarded fast path reaches it once they are off",
	},
	{
		From: models.RouteStatusPendingCreate, To: models.RouteStatusPendingDeploy,
		Site: "client_attachment_service.go AttachFromRoute/approvals-disabled", Derived: true,
		Why: "same runtime toggle as the rejected row, and additionally an orphan: Create persists pending_create before calling approvals.Submit, so a failed submit leaves one",
	},

	// --- #9 AttachFromClient/approvals-disabled (was ANY) ----------------
	// Identical body to #8 for this purpose, and the same corrected set.
	{
		From: models.RouteStatusActive, To: models.RouteStatusPendingDeploy,
		Site: "client_attachment_service.go AttachFromClient/approvals-disabled", Derived: true,
		Why: "same as #8; AttachFromClient and AttachFromRoute differ only in which side submits",
	},
	{
		From: models.RouteStatusApproved, To: models.RouteStatusPendingDeploy,
		Site: "client_attachment_service.go AttachFromClient/approvals-disabled", Derived: true,
		Why: "same as #8",
	},
	{
		From: models.RouteStatusRejected, To: models.RouteStatusPendingDeploy,
		Site: "client_attachment_service.go AttachFromClient/approvals-disabled", Derived: true,
		Why: "same as #8",
	},
	{
		From: models.RouteStatusPendingCreate, To: models.RouteStatusPendingDeploy,
		Site: "client_attachment_service.go AttachFromClient/approvals-disabled", Derived: true,
		Why: "same as #8",
	},

	// --- #10 RequestDetach/approvals-disabled (was ANY) ------------------
	//
	// This site's own from-set is genuinely NARROWER than #8/#9's: it is
	// guarded on attachment.Status == active, an attachment only becomes
	// active inside a successful Deploy, and Deploy sets route.Status =
	// active in the same call. From active the route can only move on to
	// pending_deploy, pending_update or pending_delete -- never back to
	// approved, rejected or pending_create. The table is keyed globally on
	// (from, to), though, so the three origins added for #8/#9 are legal here
	// too. That is a known limitation of global keying, recorded rather than
	// papered over; per-site keying is not something to introduce while
	// fixing a production break.
	{
		From: models.RouteStatusActive, To: models.RouteStatusPendingDeploy,
		Site: "client_attachment_service.go RequestDetach/approvals-disabled", Derived: true,
		Why: "guarded on attachment.Status == active, and an attachment only becomes active inside a successful Deploy, which sets route.Status = active in the same call",
	},

	// --- #19 route_write.go:645, Update (was ANY) ------------------------
	// Update's only precondition is "no pending approval for this route"
	// (route_write.go:611-615). Delete's is byte-identical (:899-903), and
	// neither path checks route.Status anywhere else, so the two sites admit
	// exactly the same from-set.
	//
	// CORRECTED IN THE FINAL FIX WAVE. This block used to exclude
	// pending_update and pending_delete on the grounds that each "exists
	// only alongside a pending approval". That claim is disproved by the
	// very sentence it was written beside: an orphaned pending_create route
	// -- Create persisted the row, then approvals.Submit failed -- was
	// already admitted here for exactly the reason that a status row can
	// outlive the approval that was meant to accompany it. Update
	// (route_write.go:665-675) and Delete (:911-916) persist the new status
	// BEFORE calling approvals.Submit in precisely the same shape, so a
	// failed Submit leaves a pending_update or pending_delete orphan with no
	// pending approval, satisfying the precondition. Pre-2D both were
	// writable; excluding them made Delete on an orphaned pending_update
	// route, and Update on an orphaned pending_delete route, fail with
	// `illegal transition`.
	//
	// Phase 2D widened the orphan window rather than closing it:
	// internal/approval/planning.go:120-159 now errors on repository
	// failures, unknown scopes and a submitter belonging to no team. An
	// instance owner or project admin passes middleware/permissions.go:63
	// with no team membership at all, so an owner submitting under a
	// submitter_team policy orphans deterministically.
	//
	// Tests: TestRouteService_Update_OrphanedPendingDeleteRoute and
	// TestRouteService_Delete_OrphanedPendingUpdateRoute
	// (route_service_test.go), both of which failed with `illegal
	// transition` before these two rows were added.
	{
		From: models.RouteStatusPendingDelete, To: models.RouteStatusPendingUpdate,
		Site: "route_write.go:645 Update", Derived: true,
		Why: "an orphaned pending_delete route (Delete persisted the status, then approvals.Submit failed) carries no pending approval, so Update reaches the assignment; same orphan class as the pending_create row below",
	},
	{
		From: models.RouteStatusPendingCreate, To: models.RouteStatusPendingUpdate,
		Site: "route_write.go:645 Update", Derived: true,
		Why: "identical precondition to Delete (route_write.go:608-612 vs :881-885, byte-for-byte); an orphaned pending_create route must stay revisable, not only deletable",
	},
	{
		From: models.RouteStatusApproved, To: models.RouteStatusPendingUpdate,
		Site: "route_write.go:645 Update", Derived: true,
		Why: "approved carries no pending approval, so Update reaches the assignment",
	},
	{
		From: models.RouteStatusActive, To: models.RouteStatusPendingUpdate,
		Site: "route_write.go:645 Update", Derived: true,
		Why: "the mainline: updating a live route",
	},
	{
		From: models.RouteStatusRejected, To: models.RouteStatusPendingUpdate,
		Site: "route_write.go:645 Update", Derived: true,
		Why: "a rejected creation carries no pending approval; revising and resubmitting it is the intended recovery",
	},
	{
		From: models.RouteStatusPendingDeploy, To: models.RouteStatusPendingUpdate,
		Site: "route_write.go:645 Update", Derived: true,
		Why: "the approval that produced pending_deploy is approved, not pending, so a further update can be submitted on top",
	},

	// --- #21 route_write.go:894, Delete (was ANY) ------------------------
	// Exactly the same from-set as #19: the two preconditions are
	// byte-identical and neither function reads route.Status anywhere else.
	// That symmetry is what forces the pending_update row below -- see the
	// "CORRECTED IN THE FINAL FIX WAVE" note on #19 for the full argument.
	{
		From: models.RouteStatusPendingUpdate, To: models.RouteStatusPendingDelete,
		Site: "route_write.go:894 Delete", Derived: true,
		Why: "an orphaned pending_update route (Update persisted the status, then approvals.Submit failed) carries no pending approval, so Delete reaches the assignment; mirror of the pending_delete row under #19",
	},
	{
		From: models.RouteStatusPendingCreate, To: models.RouteStatusPendingDelete,
		Site: "route_write.go:894 Delete", Derived: true,
		Why: "identical precondition to Update; TestRouteService_Delete_PendingCreateRoute corroborates but is not the justification",
	},
	{
		From: models.RouteStatusApproved, To: models.RouteStatusPendingDelete,
		Site: "route_write.go:894 Delete", Derived: true,
		Why: "approved carries no pending approval, so Delete reaches the assignment",
	},
	{
		From: models.RouteStatusActive, To: models.RouteStatusPendingDelete,
		Site: "route_write.go:894 Delete", Derived: true,
		Why: "the mainline: deleting a live route",
	},
	{
		From: models.RouteStatusRejected, To: models.RouteStatusPendingDelete,
		Site: "route_write.go:894 Delete", Derived: true,
		Why: "cleaning up a rejected creation",
	},
	{
		From: models.RouteStatusPendingDeploy, To: models.RouteStatusPendingDelete,
		Site: "route_write.go:894 Delete", Derived: true,
		Why: "same as the pending_deploy row for #19: no approval is pending",
	},

	// --- Guarded sites, transcribed unchanged ---------------------------
	// The assignment that used to sit in ClientAttachmentService.OnApproved
	// was DEAD (it mutated a local the function never persisted) and was
	// deleted in Task 10+11. The transition survives: OnApproved delegates to
	// updateRouteStatus, which re-fetches the route and is the write that
	// always actually happened. The guard OnApproved applies before calling
	// it -- route.Status == active -- is what makes this row {active}.
	{From: models.RouteStatusActive, To: models.RouteStatusPendingDeploy, Site: "client_attachment_service.go updateRouteStatus (via OnApproved)"},
	{From: models.RouteStatusActive, To: models.RouteStatusPendingDeploy, Site: "client_service.go:301 cascadeIPChangeToRoutes"},
	{From: models.RouteStatusActive, To: models.RouteStatusPendingDeploy, Site: "client_service.go:445 cascadeMethodChangeToRoutes"},
	{From: models.RouteStatusActive, To: models.RouteStatusPendingDeploy, Site: "client_service.go:467 cascadeHeaderChangeToRoutes"},
	{From: models.RouteStatusActive, To: models.RouteStatusPendingDeploy, Site: "client_service.go:608 cascadeAPIKeyChangeToRoutes"},
	{From: models.RouteStatusActive, To: models.RouteStatusPendingDeploy, Site: "client_service.go:796 cascadeJWTChangeToRoutes"},
	{From: models.RouteStatusPendingCreate, To: models.RouteStatusApproved, Site: "route_write.go:343 Create/approvals-disabled"},
	{From: models.RouteStatusPendingUpdate, To: models.RouteStatusPendingDeploy, Site: "route_write.go:734 Update/approvals-disabled"},
	{From: models.RouteStatusPendingDelete, To: models.RouteStatusPendingDeploy, Site: "route_write.go:941 Delete/approvals-disabled"},
	{From: models.RouteStatusApproved, To: models.RouteStatusActive, Site: "route_deploy.go:124 Deploy/create"},
	{From: models.RouteStatusPendingDeploy, To: models.RouteStatusActive, Site: "route_deploy.go:195 Deploy/update"},
}

// The table must permit everything the pre-2D code could do, once each
// unguarded site's `ANY` has been resolved to the statuses actually
// reachable there. A gap here is a behaviour change smuggled in as a
// refactor.
func TestLegalTransitions_CoversEveryObservedTransition(t *testing.T) {
	for _, tr := range observedTransitions {
		allowed, ok := legalTransitions[tr.From]
		if !assert.Truef(t, ok, "no transitions defined from %q (observed at %s)", tr.From, tr.Site) {
			continue
		}
		assert.Containsf(t, allowed, tr.To,
			"transition %q -> %q observed at %s is missing from legalTransitions",
			tr.From, tr.To, tr.Site)
	}
}

// The converse of the coverage test: nothing may sit in legalTransitions
// that no site produces. Without this the table could be widened silently.
func TestLegalTransitions_HasNoUnobservedEntry(t *testing.T) {
	observed := map[models.RouteStatus]map[models.RouteStatus]string{}
	for _, tr := range observedTransitions {
		if observed[tr.From] == nil {
			observed[tr.From] = map[models.RouteStatus]string{}
		}
		observed[tr.From][tr.To] = tr.Site
	}

	for from, tos := range legalTransitions {
		for _, to := range tos {
			assert.Containsf(t, observed[from], to,
				"legalTransitions permits %q -> %q but no site in observedTransitions produces it",
				from, to)
		}
	}
}

// Pins controller ruling R5: `ANY` records the absence of a guard, not a
// claim that every status is reachable. Every such row must be resolved to a
// concrete from-set before it reaches legalTransitions.
func TestObservedTransitions_NoUnresolvedANY(t *testing.T) {
	for _, tr := range observedTransitions {
		assert.NotEqualf(t, routeStatusAny, tr.From,
			"unresolved ANY row at %s: derive the reachable from-set (see the "+
				"\"Derived from-sets\" section of transitions.md) instead of "+
				"transcribing ANY into legalTransitions", tr.Site)
		if tr.Derived {
			assert.NotEmptyf(t, tr.Why, "derived row at %s must record its reasoning", tr.Site)
		}
	}
}

// Every status needs at least one outgoing transition, otherwise To()
// reports "no transitions defined from" -- a dead end that would strand any
// route reaching it.
func TestLegalTransitions_EveryStatusHasAnExit(t *testing.T) {
	all := []models.RouteStatus{
		models.RouteStatusPendingCreate,
		models.RouteStatusPendingUpdate,
		models.RouteStatusPendingDelete,
		models.RouteStatusApproved,
		models.RouteStatusActive,
		models.RouteStatusRejected,
		models.RouteStatusPendingDeploy,
	}
	for _, s := range all {
		assert.NotEmptyf(t, legalTransitions[s], "status %q has no outgoing transition", s)
	}
}

// ---------------------------------------------------------------------------
// routeStateMachine.To
//
// internal/mocks depends on internal/services (for compile-time interface
// checks), so a package-services internal test file cannot import
// internal/mocks without an import cycle -- see the same note in
// route_approval_internal_test.go. metricsTestRouteRepo
// (metrics_service_test.go) is the local stub satisfying
// repository.RouteRepositoryInterface.
// ---------------------------------------------------------------------------

func TestRouteStateMachine_RejectsIllegalTransition(t *testing.T) {
	routeRepo := new(metricsTestRouteRepo)
	m := &routeStateMachine{repo: routeRepo}

	route := &models.Route{ID: uuid.New(), Status: models.RouteStatusRejected}

	err := m.To(route, models.RouteStatusActive, "test")

	require.Error(t, err)
	assert.Equal(t, models.RouteStatusRejected, route.Status, "status must not mutate on rejection")
	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestRouteStateMachine_NoOpTransitionDoesNotWrite(t *testing.T) {
	routeRepo := new(metricsTestRouteRepo)
	m := &routeStateMachine{repo: routeRepo}

	route := &models.Route{ID: uuid.New(), Status: models.RouteStatusActive}

	err := m.To(route, models.RouteStatusActive, "test")

	require.NoError(t, err)
	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestRouteStateMachine_LegalTransitionPersists(t *testing.T) {
	routeID := uuid.New()

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Status == models.RouteStatusPendingDeploy
	})).Return(nil)

	m := &routeStateMachine{repo: routeRepo}
	route := &models.Route{ID: routeID, Status: models.RouteStatusActive}

	err := m.To(route, models.RouteStatusPendingDeploy, "client credential rotated")

	require.NoError(t, err)
	routeRepo.AssertExpectations(t)
}

func TestRouteStateMachine_NilRouteIsAnError(t *testing.T) {
	m := &routeStateMachine{repo: new(metricsTestRouteRepo)}

	err := m.To(nil, models.RouteStatusActive, "test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil route")
}

// A status outside the seven models.RouteStatus values (an empty string on a
// zero-value row, say) has no entry in the table and must be reported as
// such rather than silently accepted.
func TestRouteStateMachine_UnknownFromStatusIsAnError(t *testing.T) {
	routeRepo := new(metricsTestRouteRepo)
	m := &routeStateMachine{repo: routeRepo}

	route := &models.Route{ID: uuid.New(), Status: models.RouteStatus("")}

	err := m.To(route, models.RouteStatusActive, "test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no transitions defined from")
	routeRepo.AssertNotCalled(t, "Update", mock.Anything)
}

// The narrowings ruling R5 buys, stated as a test. Each pair below was
// silently possible before Phase 2D -- an unguarded site would have written
// it without complaint -- and is now an error. 12 pairs. See the
// "Narrowings versus ANY" section of the Task 9 report, as corrected by the
// "Fix round 1" section of the Task 10+11 report and again by the final fix
// wave.
//
// It was 17. Fix round 1 removed all three `-> pending_deploy` rows:
// pending_create, approved and rejected are genuinely reachable at the
// attach/detach fast paths, which read route.Status nowhere, so narrowing
// them was not a safety win but a live bug -- `approved -> pending_deploy`
// broke the ordinary create -> attach -> deploy flow in any project with
// approvals disabled.
//
// Then 14. The final fix wave removed the last two, `pending_delete ->
// pending_update` and `pending_update -> pending_delete`, for the same class
// of reason: Update and Delete each persist the new status before calling
// approvals.Submit, so a failed Submit orphans the route at that status with
// no pending approval -- and the other operation, whose precondition is
// byte-identical, must still reach it. Both were writable pre-2D. See the
// #19 block above.
func TestRouteStateMachine_NarrowingsVersusANY(t *testing.T) {
	narrowed := []struct {
		from models.RouteStatus
		to   models.RouteStatus
	}{
		// ANY -> approved (OnApproved/create)
		{models.RouteStatusPendingUpdate, models.RouteStatusApproved},
		{models.RouteStatusPendingDelete, models.RouteStatusApproved},
		{models.RouteStatusActive, models.RouteStatusApproved},
		{models.RouteStatusRejected, models.RouteStatusApproved},
		{models.RouteStatusPendingDeploy, models.RouteStatusApproved},

		// ANY -> pending_deploy (OnApproved/update+delete, attach, detach)
		// contributes NOTHING to this list any more. All three of its former
		// rows -- pending_create, approved, rejected -> pending_deploy -- were
		// removed in fix round 1; see the comment above.

		// ANY -> rejected (OnRejected/create)
		{models.RouteStatusPendingUpdate, models.RouteStatusRejected},
		{models.RouteStatusPendingDelete, models.RouteStatusRejected},
		{models.RouteStatusApproved, models.RouteStatusRejected},
		{models.RouteStatusActive, models.RouteStatusRejected},
		{models.RouteStatusPendingDeploy, models.RouteStatusRejected},

		// ANY -> active (OnRejected/OnCancelled update+delete)
		{models.RouteStatusPendingCreate, models.RouteStatusActive},
		{models.RouteStatusRejected, models.RouteStatusActive},

		// ANY -> pending_update (route_write.go Update) and
		// ANY -> pending_delete (route_write.go Delete) contribute NOTHING to
		// this list any more.
		//
		// pending_create -> pending_update went in fix round 1: Update and
		// Delete gate on byte-identical preconditions, so excluding it made
		// the route deletable but not revisable on the strength of which
		// tests happen to exist rather than what the code admits.
		//
		// pending_delete -> pending_update and pending_update ->
		// pending_delete went in the final fix wave, on the same argument
		// carried one step further: both statuses are persisted BEFORE
		// approvals.Submit is called, so a failed Submit orphans the route
		// there with no pending approval, and the byte-identical
		// precondition on the opposite operation is then satisfied.
	}

	for _, n := range narrowed {
		routeRepo := new(metricsTestRouteRepo)
		m := &routeStateMachine{repo: routeRepo}
		route := &models.Route{ID: uuid.New(), Status: n.from}

		err := m.To(route, n.to, "narrowing check")

		assert.Errorf(t, err, "%q -> %q was silently possible pre-2D and must now be rejected", n.from, n.to)
		assert.Equalf(t, n.from, route.Status, "status must not mutate on rejection (%q -> %q)", n.from, n.to)
		routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	}
}

// ---------------------------------------------------------------------------
// To's CONTRACT: it owns route.Status and nothing else.
//
// OnApproved applies the approved config snapshot to the route BEFORE
// transitioning. To does not write on a no-op transition, so if the route
// already sits at the target status the snapshot would be applied in memory
// and thrown away -- silent data loss, and NOT one of the narrowings this
// task set out to make. OnApproved persists explicitly on that path; these
// tests pin it.
// ---------------------------------------------------------------------------

func routeApprovalSnapshotJSON(t *testing.T, rt models.RouteType) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig: &models.RouteConfig{RouteType: rt},
	})
	require.NoError(t, err)
	return raw
}

func TestOnApproved_AtTargetStatus_StillPersistsSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action models.ApprovalAction
		status models.RouteStatus
	}{
		{"create already approved", models.ApprovalActionCreate, models.RouteStatusApproved},
		{"update already pending_deploy", models.ApprovalActionUpdate, models.RouteStatusPendingDeploy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routeID := uuid.New()
			route := &models.Route{
				ID:     routeID,
				Status: tc.status,
				Config: models.RouteConfig{RouteType: models.RouteTypeBackend},
			}

			routeRepo := new(metricsTestRouteRepo)
			routeRepo.On("GetByID", routeID).Return(route, nil)
			routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
				// The snapshot must have landed, and the status must not have
				// moved: this is a no-op transition, not a narrowing.
				return r.ID == routeID && r.Config.RouteType == models.RouteTypeRedirect && r.Status == tc.status
			})).Return(nil)

			svc := NewRouteService(routeRepo, nil, nil, nil, nil, routeplan.WAFConfig{})

			err := svc.OnApproved(&models.Approval{
				ID:             uuid.New(),
				EntityType:     models.ApprovalEntityRoute,
				EntityID:       routeID,
				Action:         tc.action,
				ConfigSnapshot: routeApprovalSnapshotJSON(t, models.RouteTypeRedirect),
			})

			require.NoError(t, err)
			assert.Equal(t, models.RouteTypeRedirect, route.Config.RouteType, "approved config snapshot must be applied")
			assert.Equal(t, tc.status, route.Status, "a no-op transition must not move the status")
			routeRepo.AssertExpectations(t)
		})
	}
}

// The moving case, for contrast: when the status does change, To's own write
// carries the snapshot and OnApproved must not write twice.
func TestOnApproved_MovingStatus_PersistsSnapshotExactlyOnce(t *testing.T) {
	routeID := uuid.New()
	route := &models.Route{
		ID:     routeID,
		Status: models.RouteStatusPendingUpdate,
		Config: models.RouteConfig{RouteType: models.RouteTypeBackend},
	}

	routeRepo := new(metricsTestRouteRepo)
	routeRepo.On("GetByID", routeID).Return(route, nil)
	routeRepo.On("Update", mock.MatchedBy(func(r *models.Route) bool {
		return r.ID == routeID && r.Config.RouteType == models.RouteTypeRedirect &&
			r.Status == models.RouteStatusPendingDeploy
	})).Return(nil).Once()

	svc := NewRouteService(routeRepo, nil, nil, nil, nil, routeplan.WAFConfig{})

	err := svc.OnApproved(&models.Approval{
		ID:             uuid.New(),
		EntityType:     models.ApprovalEntityRoute,
		EntityID:       routeID,
		Action:         models.ApprovalActionUpdate,
		ConfigSnapshot: routeApprovalSnapshotJSON(t, models.RouteTypeRedirect),
	})

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDeploy, route.Status)
	routeRepo.AssertExpectations(t)
}

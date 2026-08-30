//go:build e2e

package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
)

// NEW (task-17): approval enforcement had ZERO e2e coverage before this
// file -- the Python regression client (regression/helpers/api.py) never
// exposed a reject-stage call, so no negative case was ever exercised (see
// e2e/harness/api.go's RejectApproval doc comment). These three tests close
// that gap; per the task brief they are the highest priority in this
// package because they verify a core authorization guarantee.
//
// TestApprovalUnapprovedRouteNotServed and
// TestApprovalRejectedRouteNotDeployedOrServed both need to prove a
// NEGATIVE: that a specific path is not served. A bare 404 on a freshly
// created route is ambiguous by itself -- harness.WaitForRouteLive's own
// doc comment explains that Envoy Gateway can answer 404 for an
// as-yet-unprogrammed route that WILL eventually go live. Both tests
// resolve that ambiguity the same way: they first create and fully deploy
// a SIBLING route (via harness.Fixture, same domain) and prove IT is live
// with waitForHTTPStatus. Once that succeeds, the gateway/domain as a
// whole is proven to be reconciling routes normally, so a 404 on the
// test route's own distinct path is no longer "still warming up" -- the
// only way that route's HTTPRoute can exist in Kubernetes at all is via a
// successful Deploy call, which both tests separately prove was rejected.

// TestApprovalEditorCannotApproveOwnRoute proves the submitter-cannot-
// approve-their-own-change guarantee (internal/services/approval_service.go
// ApproveStage's "submitter cannot approve their own submission" check,
// gated on the project's SelfApprovalAllowed setting, which the seeded e2e
// project leaves at its default of false -- see internal/models/project.go).
//
// The route is created directly via env.Editor.CreateRoute (not
// harness.Fixture, which always approves-and-deploys) so it has a
// genuinely pending approval, submitted by the editor, for the editor to
// then attempt to approve.
func TestApprovalEditorCannotApproveOwnRoute(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, _, cfg := simpleRouteConfig(t)
	route, err := env.Editor.CreateRoute(ctx, env.ProjectID, env.DomainID, cfg)
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	t.Cleanup(func() { cleanupPendingOrRejectedRoute(t, route) })

	err = env.Editor.ApproveAllStages(ctx, env.ProjectID, route.ID.String())
	if err == nil {
		t.Fatalf("editor approved their own route %s (%s): expected the API to reject this", route.Name, route.ID)
	}
	var statusErr *harness.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("approve own route: error %v is not a *harness.StatusError", err)
	}
	if statusErr.StatusCode != 400 {
		t.Fatalf("approve own route: got status %d, want 400 (body: %s)", statusErr.StatusCode, statusErr.Body)
	}
	if !strings.Contains(statusErr.Body, "cannot approve their own submission") {
		t.Fatalf("approve own route: got body %q, want it to mention the submitter cannot approve their own submission", statusErr.Body)
	}
}

// TestApprovalUnapprovedRouteNotServed proves that a route which has never
// been approved (1) cannot be deployed -- RouteService.Deploy rejects any
// route whose status isn't "approved" or "pending_deploy" -- and (2) is
// genuinely not served by the gateway, not merely "not yet live".
//
// If this test fails on the gateway-side assertion (i.e. the path DOES
// serve traffic despite Deploy having been rejected), that is a genuine
// security finding: report it, do not adjust the test to pass.
func TestApprovalUnapprovedRouteNotServed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+60*time.Second)
	defer cancel()

	// Sibling: fully approved and deployed, proves the domain is live.
	fx := harness.NewFixture(t, env)
	_, siblingPath, siblingCfg := simpleRouteConfig(t)
	fx.Route(siblingCfg)
	siblingProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", siblingPath)
	}
	if _, err := waitForHTTPStatus(ctx, siblingProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("sibling route at %s never became live (cannot trust a 404 on the unapproved route without this): %v", siblingPath, err)
	}

	// Test route: created but never approved.
	_, testPath, cfg := simpleRouteConfig(t)
	route, err := env.Editor.CreateRoute(ctx, env.ProjectID, env.DomainID, cfg)
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	t.Cleanup(func() { cleanupPendingOrRejectedRoute(t, route) })

	// (1) Deploy must be rejected.
	err = env.Editor.DeployRoute(ctx, env.ProjectID, env.DomainID, route.ID.String())
	if err == nil {
		t.Fatalf("deploy of unapproved route %s (%s) succeeded, want rejection", route.Name, route.ID)
	}
	var statusErr *harness.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("deploy unapproved route: error %v is not a *harness.StatusError", err)
	}
	if statusErr.StatusCode != 400 {
		t.Fatalf("deploy unapproved route: got status %d, want 400 (body: %s)", statusErr.StatusCode, statusErr.Body)
	}

	// (2) The gateway must not serve it. The sibling proof above makes this
	// 404 meaningful: the domain reconciles routes normally, so the only
	// explanation for this specific path 404ing is that no HTTPRoute for
	// it was ever programmed -- exactly what (1) already established.
	testProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", testPath)
	}
	resp := requireStatus(t, ctx, testProbe, 404)
	t.Logf("unapproved route %s: gateway correctly returned 404 for %s (body: %s)", route.Name, testPath, truncate(resp.Body, 200))
}

// TestApprovalRejectedRouteNotDeployedOrServed proves that a route whose
// create approval was explicitly REJECTED (as opposed to simply never
// reviewed, see TestApprovalUnapprovedRouteNotServed) also cannot be
// deployed and is not served. Rejection is a distinct code path from "never
// approved" (internal/services/approval_service.go's onRouteApprovalRejected
// sets route.Status to "rejected", a terminal state Deploy explicitly does
// not accept), so it earns its own test rather than being folded into the
// unapproved case.
func TestApprovalRejectedRouteNotDeployedOrServed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+60*time.Second)
	defer cancel()

	fx := harness.NewFixture(t, env)
	_, siblingPath, siblingCfg := simpleRouteConfig(t)
	fx.Route(siblingCfg)
	siblingProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", siblingPath)
	}
	if _, err := waitForHTTPStatus(ctx, siblingProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("sibling route at %s never became live (cannot trust a 404 on the rejected route without this): %v", siblingPath, err)
	}

	_, testPath, cfg := simpleRouteConfig(t)
	route, err := env.Editor.CreateRoute(ctx, env.ProjectID, env.DomainID, cfg)
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	t.Cleanup(func() { cleanupPendingOrRejectedRoute(t, route) })

	if err := env.Approver.RejectApproval(ctx, env.ProjectID, route.ID.String(), "e2e: TestApprovalRejectedRouteNotDeployedOrServed"); err != nil {
		t.Fatalf("reject route %s (%s): %v", route.Name, route.ID, err)
	}

	err = env.Editor.DeployRoute(ctx, env.ProjectID, env.DomainID, route.ID.String())
	if err == nil {
		t.Fatalf("deploy of rejected route %s (%s) succeeded, want rejection", route.Name, route.ID)
	}
	var statusErr *harness.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("deploy rejected route: error %v is not a *harness.StatusError", err)
	}
	if statusErr.StatusCode != 400 {
		t.Fatalf("deploy rejected route: got status %d, want 400 (body: %s)", statusErr.StatusCode, statusErr.Body)
	}

	testProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", testPath)
	}
	resp := requireStatus(t, ctx, testProbe, 404)
	t.Logf("rejected route %s: gateway correctly returned 404 for %s (body: %s)", route.Name, testPath, truncate(resp.Body, 200))
}

// cleanupPendingOrRejectedRoute deletes a route that was created but never
// successfully deployed (its create approval is either still pending or
// was rejected).
//
// Two things make this more involved than harness.Fixture.Route's own
// cleanup (which assumes the route it's tearing down was already fully
// approved and deployed):
//
//  1. RouteService.Delete refuses to run while a pending approval exists
//     for the route ("there is already a pending approval for this
//     route") -- a route left with a PENDING create approval (as
//     TestApprovalEditorCannotApproveOwnRoute and
//     TestApprovalUnapprovedRouteNotServed both leave behind) must have
//     that approval resolved first. This rejects it as admin, who is
//     never the submitter (the route was always created via
//     env.Editor.CreateRoute), so the submitter-cannot-reject-their-own-
//     submission check never applies here. A route whose create approval
//     was already rejected by the test itself (TestApprovalRejected...)
//     has no pending approval left at this point, so RejectApproval
//     simply reports "no pending approval found" and this step is a
//     no-op.
//  2. RouteService.Delete -- regardless of the route's prior state --
//     always creates a new pending "delete" approval when the project has
//     approvals enabled (true for the seeded e2e project), exactly like
//     harness.Fixture.Route's own cleanup already has to handle for a
//     normal deployed route. That approval is submitted as env.Editor
//     (who has delete permission on routes owned by their own team) and
//     approved as env.Approver -- deliberately NOT both done as the same
//     user (e.g. env.Admin for both), which would hit the identical
//     submitter-cannot-approve-their-own-submission check
//     TestApprovalEditorCannotApproveOwnRoute exists to prove is
//     enforced.
func cleanupPendingOrRejectedRoute(t *testing.T, route harness.Route) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := env.Admin.RejectApproval(ctx, env.ProjectID, route.ID.String(), "e2e cleanup"); err != nil &&
		!strings.Contains(err.Error(), "no pending approval found") {
		t.Errorf("cleanup: reject pending create approval for route %s (%s): %v", route.Name, route.ID, err)
		return
	}

	if err := env.Editor.DeleteRoute(ctx, env.ProjectID, env.DomainID, route.ID.String()); err != nil {
		t.Errorf("cleanup: delete route %s (%s): %v", route.Name, route.ID, err)
		return
	}
	if err := env.Approver.ApproveAllStages(ctx, env.ProjectID, route.ID.String()); err != nil &&
		!strings.Contains(err.Error(), "no pending approval found") {
		t.Errorf("cleanup: approve delete for route %s (%s): %v", route.Name, route.ID, err)
		return
	}
	if err := env.Editor.DeployRoute(ctx, env.ProjectID, env.DomainID, route.ID.String()); err != nil {
		t.Errorf("cleanup: deploy delete for route %s (%s): %v", route.Name, route.ID, err)
	}
}

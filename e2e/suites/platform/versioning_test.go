//go:build e2e

package platform

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// NEW (task-17): route version history and rollback had ZERO e2e coverage
// before this file -- e2e/harness/api.go's ListRouteVersions/RollbackRoute
// have no Python predecessor at all (see their doc comments).
//
// Both tests here build on the same "v1 -> v2" setup: a route is created
// (harness.Fixture.Route deploys it, which is what actually snapshots
// version 1 -- see internal/services/route_version_service.go's
// CreateVersion, called from RouteService.Deploy), then updated and
// redeployed a second time (snapshotting version 2). Every version's
// distinguishing feature is a ResponseHeaderModifier setting
// "X-E2E-Version" to a version-specific value (versionedRouteConfig, in
// main_test.go) -- this lets both the STORED config (RouteVersion.
// ConfigSnapshot, a JSON-encoded models.RouteApprovalSnapshot) and what
// the GATEWAY actually serves be checked with one simple, unambiguous
// signal, the same technique e2e/suites/httproute's header_modifier_test.go
// already uses to prove header rewriting end-to-end.

// deployNewVersion updates route to headerValue's config via env.Editor,
// approves the resulting pending "update" approval via env.Approver, and
// deploys it via env.Editor -- the same submitter/reviewer split every
// other create flow in this package uses, which is what actually snapshots
// a new RouteVersion row (see the file doc comment).
func deployNewVersion(t *testing.T, ctx context.Context, route harness.Route, headerValue string) harness.Route {
	t.Helper()

	cfg := route.Config
	cfg.ResponseHeaderModifier = &models.HeaderModifier{
		Set: []models.HeaderValue{{Name: "X-E2E-Version", Value: headerValue}},
	}
	input := services.UpdateRouteInput{
		Description:       route.Description,
		Config:            cfg,
		ChangeDescription: "e2e: bump to " + headerValue,
	}
	if _, err := env.Editor.UpdateRoute(ctx, env.ProjectID, env.DomainID, route.ID.String(), input); err != nil {
		t.Fatalf("update route %s (%s) to %s: %v", route.Name, route.ID, headerValue, err)
	}
	if err := env.Approver.ApproveAllStages(ctx, env.ProjectID, route.ID.String()); err != nil {
		t.Fatalf("approve update of route %s (%s) to %s: %v", route.Name, route.ID, headerValue, err)
	}
	if err := env.Editor.DeployRoute(ctx, env.ProjectID, env.DomainID, route.ID.String()); err != nil {
		t.Fatalf("deploy update of route %s (%s) to %s: %v", route.Name, route.ID, headerValue, err)
	}
	deployed, err := env.Editor.GetRoute(ctx, env.ProjectID, env.DomainID, route.ID.String())
	if err != nil {
		t.Fatalf("fetch route %s (%s) after deploying %s: %v", route.Name, route.ID, headerValue, err)
	}
	return deployed
}

// snapshotHeaderValue unmarshals a RouteVersion's ConfigSnapshot (a JSON-
// encoded models.RouteApprovalSnapshot, see route_version_service.go's
// CreateVersion) and returns the "X-E2E-Version" value its
// ResponseHeaderModifier was set to, failing t if the snapshot doesn't
// carry one.
func snapshotHeaderValue(t *testing.T, rv models.RouteVersion) string {
	t.Helper()
	var snap models.RouteApprovalSnapshot
	if err := json.Unmarshal(rv.ConfigSnapshot, &snap); err != nil {
		t.Fatalf("version %d: unmarshal configSnapshot: %v (raw: %s)", rv.Version, err, string(rv.ConfigSnapshot))
	}
	if snap.RouteConfig == nil || snap.RouteConfig.ResponseHeaderModifier == nil {
		t.Fatalf("version %d: configSnapshot has no responseHeaderModifier (raw: %s)", rv.Version, string(rv.ConfigSnapshot))
	}
	for _, h := range snap.RouteConfig.ResponseHeaderModifier.Set {
		if h.Name == "X-E2E-Version" {
			return h.Value
		}
	}
	t.Fatalf("version %d: configSnapshot's responseHeaderModifier has no X-E2E-Version entry (raw: %s)", rv.Version, string(rv.ConfigSnapshot))
	return ""
}

// TestRouteVersioningListReturnsBothVersions ports the versioning brief's
// "create -> v1; update -> v2; list returns both" step. The two returned
// RouteVersion rows are checked for more than just count=2: their
// ConfigSnapshot content is decoded and confirmed to actually hold the
// distinct v1/v2 header values, proving the snapshots are real per-version
// data and not, say, two identical copies of the current config.
func TestRouteVersioningListReturnsBothVersions(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+60*time.Second)
	defer cancel()

	_, _, cfg := versionedRouteConfig(t, "v1")
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	route = deployNewVersion(t, ctx, route, "v2")

	versions, total, err := env.Editor.ListRouteVersions(ctx, env.ProjectID, env.DomainID, route.ID.String(), 1, 20)
	if err != nil {
		t.Fatalf("list versions for route %s (%s): %v", route.Name, route.ID, err)
	}
	if total != 2 {
		t.Fatalf("route %s (%s): got total=%d versions, want 2", route.Name, route.ID, total)
	}
	if len(versions) != 2 {
		t.Fatalf("route %s (%s): got %d versions in the page, want 2 (total reported: %d)", route.Name, route.ID, len(versions), total)
	}

	byVersion := map[int]string{}
	for _, rv := range versions {
		byVersion[rv.Version] = snapshotHeaderValue(t, rv)
	}
	if got, ok := byVersion[1]; !ok || got != "v1" {
		t.Fatalf("route %s (%s): version 1 has X-E2E-Version=%q (present=%v), want %q", route.Name, route.ID, got, ok, "v1")
	}
	if got, ok := byVersion[2]; !ok || got != "v2" {
		t.Fatalf("route %s (%s): version 2 has X-E2E-Version=%q (present=%v), want %q", route.Name, route.ID, got, ok, "v2")
	}
}

// TestRouteVersioningRollbackRestoresServedConfig ports the versioning
// brief's "rollback restores the earlier config; and the rolled-back
// config is what the gateway actually serves" step.
//
// RouteVersionService.Rollback does not apply the historical config
// directly -- it resubmits it through the normal update-approval flow (see
// route_version_service.go's Rollback: "Submit through normal update flow
// (which creates an approval)"), so this approves and deploys the
// resulting pending approval exactly like any other update before checking
// the result. Both the STORED route config (via GetRoute) and the actual
// GATEWAY response are checked, per the task brief's explicit emphasis
// that this is what makes rollback a genuine, not merely cosmetic,
// restoration.
func TestRouteVersioningRollbackRestoresServedConfig(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+90*time.Second)
	defer cancel()

	_, path, cfg := versionedRouteConfig(t, "v1")
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("route %s (%s): v1 never became live before update: %v", route.Name, route.ID, err)
	}

	route = deployNewVersion(t, ctx, route, "v2")
	if resp, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("route %s (%s): v2 never became live after update: %v", route.Name, route.ID, err)
	} else if got := resp.Header.Get("X-E2E-Version"); got != "v2" {
		t.Fatalf("route %s (%s): after updating to v2, gateway served X-E2E-Version=%q, want %q", route.Name, route.ID, got, "v2")
	}

	if _, err := env.Editor.RollbackRoute(ctx, env.ProjectID, env.DomainID, route.ID.String(), 1); err != nil {
		t.Fatalf("rollback route %s (%s) to version 1: %v", route.Name, route.ID, err)
	}
	if err := env.Approver.ApproveAllStages(ctx, env.ProjectID, route.ID.String()); err != nil {
		t.Fatalf("approve rollback of route %s (%s): %v", route.Name, route.ID, err)
	}
	if err := env.Editor.DeployRoute(ctx, env.ProjectID, env.DomainID, route.ID.String()); err != nil {
		t.Fatalf("deploy rollback of route %s (%s): %v", route.Name, route.ID, err)
	}

	// Stored config: the route's own current config must match v1's.
	rolledBack, err := env.Editor.GetRoute(ctx, env.ProjectID, env.DomainID, route.ID.String())
	if err != nil {
		t.Fatalf("fetch route %s (%s) after rollback: %v", route.Name, route.ID, err)
	}
	if rolledBack.Config.ResponseHeaderModifier == nil || len(rolledBack.Config.ResponseHeaderModifier.Set) == 0 ||
		rolledBack.Config.ResponseHeaderModifier.Set[0].Value != "v1" {
		t.Fatalf("route %s (%s): stored config after rollback has responseHeaderModifier=%+v, want X-E2E-Version=v1",
			route.Name, route.ID, rolledBack.Config.ResponseHeaderModifier)
	}

	// Served config: the gateway must actually reflect it after redeploy.
	resp, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200)
	if err != nil {
		t.Fatalf("route %s (%s): never became live again after rollback redeploy: %v", route.Name, route.ID, err)
	}
	if got := resp.Header.Get("X-E2E-Version"); got != "v1" {
		t.Fatalf("route %s (%s): after rollback+redeploy, gateway served X-E2E-Version=%q, want %q (rollback did not actually restore the earlier config on the wire)",
			route.Name, route.ID, got, "v1")
	}
}

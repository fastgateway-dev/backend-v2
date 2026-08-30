//go:build e2e

package grpcroute

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCMissingMatches ports
// grpc_validation/test_validation_negative.py:test_grpc_missing_matches.
// Despite the file's name, this asserts ACCEPTANCE: validateRouteConfig's
// "path matching is required" rule (internal/services/route_service.go)
// only applies when protocol != grpc, so an empty matches list is treated
// as a valid catch-all for a gRPC route and route creation must succeed.
//
// This deliberately does NOT use harness.Fixture (which approves AND
// deploys, leaving the route live in the cluster for the rest of the
// test's run): an empty Matches list has no discriminating header (see
// uniqueMatch / the package doc comment), so once deployed it becomes a
// genuine match-everything GRPCRoute with no way to scope it to only this
// test's own traffic. Every other parallel test in this package sends
// real gRPC calls scoped only by their own discriminator header, and a
// live naked catch-all would intercept them too -- e.g. TestGRPCHeaderMatch's
// negative probe (a call carrying its own discriminator but not its
// required "x-grpc-match" header) is supposed to get Unimplemented/
// NotFound/Unavailable because no route should match it, but this
// catch-all would happily answer OK instead.
//
// The assertion under test only needs route creation to be ACCEPTED (not
// rejected) and the created route to be readable back via GetRoute --
// neither requires the route to ever be deployed to Kubernetes. So this
// creates and approves the route (approval is required before it can
// later be deleted -- RouteService.Delete refuses while a create approval
// is still pending) but skips DeployRoute entirely, and cleans up via a
// delete/approve/deploy sequence mirroring harness.Fixture.Route's own
// cleanup (deleting a route with nothing actually in Kubernetes is a
// documented no-op there: e.g. KubernetesService.DeleteGRPCRoute treats
// "not found" as success).
func TestGRPCMissingMatches(t *testing.T) {
	t.Parallel()

	name := harness.UniqueName(t)

	cfg := services.CreateRouteInput{
		Name:     name,
		Protocol: models.RouteProtocolGRPC,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	route, err := env.Editor.CreateRoute(ctx, env.ProjectID, env.DomainID, cfg)
	if err != nil {
		t.Fatalf("grpc missing matches: create route (want acceptance): %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Mirror harness.Fixture.Route's cleanup role split: Editor
		// submits the delete, Approver approves it, Editor deploys it.
		if err := env.Editor.DeleteRoute(cleanupCtx, env.ProjectID, env.DomainID, route.ID.String()); err != nil {
			t.Errorf("grpc missing matches cleanup: delete route %s (%s): %v", route.Name, route.ID, err)
			return
		}
		if err := env.Approver.ApproveAllStages(cleanupCtx, env.ProjectID, route.ID.String()); err != nil &&
			!strings.Contains(err.Error(), "no pending approval found") {
			t.Errorf("grpc missing matches cleanup: approve delete for route %s (%s): %v", route.Name, route.ID, err)
			return
		}
		if err := env.Editor.DeployRoute(cleanupCtx, env.ProjectID, env.DomainID, route.ID.String()); err != nil {
			t.Errorf("grpc missing matches cleanup: deploy delete for route %s (%s): %v", route.Name, route.ID, err)
		}
	})

	// Approve the create so the delete cleanup above is allowed to run
	// (RouteService.Delete rejects a route with an already-pending
	// approval) -- but never deploy it, so the naked catch-all is never
	// actually pushed to Kubernetes. See the doc comment above.
	if err := env.Approver.ApproveAllStages(ctx, env.ProjectID, route.ID.String()); err != nil {
		t.Fatalf("grpc missing matches: approve route %s (%s): %v", route.Name, route.ID, err)
	}

	if _, err := env.Editor.GetRoute(ctx, env.ProjectID, env.DomainID, route.ID.String()); err != nil {
		t.Fatalf("grpc missing matches: fetch created route: %v", err)
	}
}

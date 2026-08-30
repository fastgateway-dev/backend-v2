//go:build e2e

package grpcroute

import (
	"context"
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

	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := env.Editor.GetRoute(ctx, env.ProjectID, env.DomainID, route.ID.String()); err != nil {
		t.Fatalf("grpc missing matches: fetch deployed route: %v", err)
	}
}

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

// TestGRPCRouteTimeoutAccepted ports
// grpc_validation/test_reject_route_timeout.py. Despite its Python name,
// this is NOT a rejection test: its own docstring says "The API accepts
// route-level timeouts for gRPC routes (they are applied via BTP)" and the
// original body asserts route creation SUCCEEDS. There is in fact no
// route-level "timeouts" field anywhere in this backend's models
// (models.RouteConfig, services.CreateRouteInput) or in the Gateway API
// GRPCRoute manifest builder (BuildGRPCRouteObject,
// internal/services/kubernetes_service.go) -- an extra "timeouts" JSON key
// would simply be ignored by ShouldBindJSON, same as it always has been.
// This port therefore verifies the one thing actually under test: that a
// gRPC route with an otherwise-valid grpcService match creates, approves,
// and deploys successfully.
func TestGRPCRouteTimeoutAccepted(t *testing.T) {
	t.Parallel()

	name, match, _ := uniqueMatch(t, "Exact", "grpc.validation.TimeoutService", "")

	cfg := services.CreateRouteInput{
		Name:     name,
		Protocol: models.RouteProtocolGRPC,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{match},
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
		t.Fatalf("route timeout accepted: fetch deployed route: %v", err)
	}
}

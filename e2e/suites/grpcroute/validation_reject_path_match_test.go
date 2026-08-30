//go:build e2e

package grpcroute

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCRejectPathMatch ports grpc_validation/test_reject_path_match.py:
// a gRPC route matched by HTTP path (instead of grpcService/grpcMethod)
// must be rejected at creation (validateGRPCRouteConfig,
// internal/services/route_service.go).
func TestGRPCRejectPathMatch(t *testing.T) {
	t.Parallel()

	name := harness.UniqueName(t)

	cfg := services.CreateRouteInput{
		Name:     name,
		Protocol: models.RouteProtocolGRPC,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "PathPrefix", Value: "/grpc-path"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
	}

	expectCreateRejected(t, cfg)
}

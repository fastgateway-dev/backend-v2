//go:build e2e

package grpcroute

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCRejectURLRewrite ports grpc_validation/test_reject_url_rewrite.py:
// a gRPC route with a urlRewrite filter must be rejected at creation
// (validateGRPCRouteConfig, internal/services/route_service.go).
func TestGRPCRejectURLRewrite(t *testing.T) {
	t.Parallel()

	name, match, _ := uniqueMatch(t, "Exact", echoServiceName, "")

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
			URLRewrite: &models.URLRewrite{
				Path: &models.PathRewrite{Type: "ReplacePrefixMatch", ReplacePrefixMatch: "/"},
			},
		},
	}

	expectCreateRejected(t, cfg)
}

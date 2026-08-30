//go:build e2e

package grpcroute

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCRejectRedirect ports grpc_validation/test_reject_redirect.py: a
// gRPC route with routeType "redirect" must be rejected at creation
// (validateGRPCRouteConfig, internal/services/route_service.go).
func TestGRPCRejectRedirect(t *testing.T) {
	t.Parallel()

	name, match, _ := uniqueMatch(t, "Exact", echoServiceName, "")

	cfg := services.CreateRouteInput{
		Name:     name,
		Protocol: models.RouteProtocolGRPC,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeRedirect,
			Matches:   []models.RouteMatch{match},
			Redirect: &models.RedirectConfig{
				Scheme:     "https",
				Hostname:   "new.example.com",
				StatusCode: 301,
			},
		},
	}

	expectCreateRejected(t, cfg)
}

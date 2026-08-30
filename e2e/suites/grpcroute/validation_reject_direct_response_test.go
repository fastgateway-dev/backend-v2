//go:build e2e

package grpcroute

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCRejectDirectResponse ports
// grpc_validation/test_reject_direct_response.py: a gRPC route with
// routeType "directResponse" must be rejected at creation
// (validateGRPCRouteConfig, internal/services/route_service.go).
func TestGRPCRejectDirectResponse(t *testing.T) {
	t.Parallel()

	name, match, _ := uniqueMatch(t, "Exact", echoServiceName, "")

	cfg := services.CreateRouteInput{
		Name:     name,
		Protocol: models.RouteProtocolGRPC,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeDirectResponse,
			Matches:   []models.RouteMatch{match},
			DirectResponse: &models.DirectResponseConfig{
				StatusCode:  200,
				ContentType: "application/json",
				Body:        &models.DirectResponseBody{Type: "Inline", Inline: "{}"},
			},
		},
	}

	expectCreateRejected(t, cfg)
}

//go:build e2e

package grpcroute

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestHTTPRejectGRPCServiceMatch ports
// grpc_validation/test_http_reject_grpc_service_match.py: an HTTP-protocol
// route (protocol left unset, defaulting to http) that includes a
// grpcService match must be rejected at creation (validateHTTPRouteConfig,
// internal/services/route_service.go).
func TestHTTPRejectGRPCServiceMatch(t *testing.T) {
	t.Parallel()

	name := harness.UniqueName(t)

	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: echoServiceName}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
		},
	}

	expectCreateRejected(t, cfg)
}

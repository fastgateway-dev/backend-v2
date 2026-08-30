//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCServiceRegexMatch ports
// grpc_route_matching/test_service_regex.py: a grpcService
// RegularExpression match ("echo\..*") matches echo.EchoService and routes
// traffic correctly.
func TestGRPCServiceRegexMatch(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "RegularExpression", `echo\..*`, "")

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
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	const wantBody = "hello-service-regex"
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, wantBody, callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("service regex match: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("service regex match: got code %v, want %v", res.Code, codes.OK)
	}

	res, resp, err := echoCall(ctx, wantBody, callOpt)
	if err != nil {
		t.Fatalf("service regex match: request: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("service regex match: got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != wantBody {
		t.Fatalf("service regex match: got echoed body %q, want %q", resp.Body, wantBody)
	}
}

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

// TestGRPCCORS ports grpc_security/test_cors.py.
//
// KNOWN LIMITATION: CORS is a browser-enforced mechanism (preflight
// OPTIONS requests, Origin/Access-Control-* headers); a grpc-go client
// never performs a CORS preflight and has no concept of it, so there is no
// meaningful client-observable allow/deny signal to assert here (same as
// the Python original, which only ever checked the call didn't outright
// fail). This verifies the route deploys with a CORS policy attached and
// still serves normal gRPC traffic.
func TestGRPCCORS(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	maxAge := 3600

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
		SecurityPolicy: &services.SecurityPolicyInput{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://example.com"},
				AllowMethods: []string{"GET", "POST"},
				AllowHeaders: []string{"Content-Type"},
				MaxAge:       &maxAge,
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("cors: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("cors: got code %v, want %v", res.Code, codes.OK)
	}
}

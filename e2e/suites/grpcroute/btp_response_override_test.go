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

// TestGRPCBTPResponseOverride ports
// grpc_btp_features/test_response_override.py.
//
// KNOWN LIMITATION: ResponseOverride matches on HTTP status codes
// (models.ResponseOverrideMatch.StatusCodes), but a successful gRPC call
// always carries HTTP :status 200 -- the gRPC outcome (OK, NotFound, ...)
// travels in the grpc-status trailer, a separate mechanism the status-code
// matcher doesn't inspect. The Python config's rule (match on HTTP 404)
// therefore can never fire against a normal, successful Echo call; this
// mirrors httproute's TestResponseOverride, which documented a similar gap
// (no statusCode-override field at all) in its own KNOWN GAP comment. This
// port verifies what the old assertion (`returncode == 0 or "rpc error" in
// stderr`, true of almost any outcome) did not: the route deploys with
// this policy attached and still serves the underlying Echo RPC correctly.
func TestGRPCBTPResponseOverride(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	statusVal := 404

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
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
			ResponseOverride: []models.ResponseOverrideRule{
				{
					Match: models.ResponseOverrideMatch{
						StatusCodes: []models.StatusCodeMatch{{Type: "Value", Value: &statusVal}},
					},
					Response: models.ResponseOverrideResponse{
						ContentType: "application/json",
						Body:        models.ResponseOverrideBody{Type: "Inline", Inline: `{"error":"not found"}`},
					},
				},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	const wantBody = "hello-response-override"
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, wantBody, callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("response override: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("response override: got code %v, want %v", res.Code, codes.OK)
	}

	res, resp, err := echoCall(ctx, wantBody, callOpt)
	if err != nil {
		t.Fatalf("response override: request: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("response override: got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != wantBody {
		t.Fatalf("response override: got echoed body %q, want %q", resp.Body, wantBody)
	}
}

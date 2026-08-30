//go:build e2e

package grpcroute

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCBTPRequestBuffer ports grpc_btp_features/test_request_buffer.py.
//
// What this asserts is deliberately NOT the HTTP sibling's assertion.
// httproute/request_buffer_test.go proves an over-limit body is rejected
// with 413; the gRPC equivalent cannot, because Envoy Gateway documents
// request buffering as incompatible with gRPC: "Request buffering requires
// Envoy to fully receive the request before forwarding it upstream. This
// does not work with streaming or upgrade-based traffic such as gRPC
// streaming and WebSocket"
// (https://gateway.envoyproxy.io/docs/tasks/traffic/request-buffering/).
// A real CI run confirmed it: an over-limit 4096-byte body was polled for
// three minutes against a BackendTrafficPolicy whose Accepted condition
// was True the whole time, and every call returned codes.OK. Asserting a
// rejection here would be asserting a behaviour upstream says will not
// happen.
//
// So this test asserts the thing that DOES matter and that the same
// upstream page warns about: attaching requestBuffer to a gRPC route must
// not break it. The documented failure mode is that "enabling request
// buffering for streaming routes can cause requests to hang indefinitely,
// as the request may never be forwarded upstream" -- a hang, a truncated
// body, or a DeadlineExceeded on either side of the limit would all fail
// here. That is a genuine regression guard, unlike the Python original's
// `returncode == 0 or "rpc error" in stderr`, which was true of nearly any
// outcome including a total hang.
//
// If Envoy Gateway ever does start enforcing the limit for unary gRPC, the
// over-limit assertion below fails and this test must be revisited -- by
// design, so the change is noticed rather than absorbed.
func TestGRPCBTPRequestBuffer(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")

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
			RequestBuffer: &models.RequestBufferConfig{Limit: "1Ki"},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	underBody := strings.Repeat("a", 100)
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, underBody, callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("request buffer: route never became live: %v", err)
	}
	res, resp, err := echoCall(ctx, underBody, callOpt)
	if err != nil {
		t.Fatalf("request buffer: under-limit request: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("request buffer: under-limit body (100 bytes < 1Ki) got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != underBody {
		t.Fatalf("request buffer: under-limit echoed body length %d, want %d", len(resp.Body), len(underBody))
	}

	// Over the limit: still OK, still echoed intact, and -- the point of
	// the test -- it must ANSWER. The buffer filter does not reject unary
	// gRPC (see the doc comment), but the documented hazard is that a
	// buffered streaming route hangs and never forwards upstream. The
	// harness's own gRPC deadline turns such a hang into a
	// DeadlineExceeded rather than a wedged test.
	overBody := strings.Repeat("a", 4096)
	res, resp, err = echoCall(ctx, overBody, callOpt)
	if err != nil {
		t.Fatalf("request buffer: over-limit request: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("request buffer: over-limit body (4096 bytes > 1Ki) got code %v, want %v -- "+
			"attaching requestBuffer to a gRPC route must not break it (a hang would surface as %v)",
			res.Code, codes.OK, codes.DeadlineExceeded)
	}
	if resp.Body != overBody {
		t.Fatalf("request buffer: over-limit echoed body length %d, want %d -- buffering must not truncate the request",
			len(resp.Body), len(overBody))
	}
}

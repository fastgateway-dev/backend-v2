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
// The Python config's limit ("10Mi") is impractical to exercise end-to-end;
// mirroring httproute's TestRequestBuffer, this port uses a much smaller
// limit ("1Ki" = 1024 bytes) and sends echo.Message bodies clearly on
// either side of it: 100 bytes (under) should echo back normally, and 4096
// bytes (over) should be rejected by Envoy's buffer filter itself.
//
// Unlike the HTTP sibling (which correctly asserts a plain 413 -- see
// httproute/request_buffer_test.go), the buffer filter always answers an
// over-limit request with HTTP status 413 regardless of the request's
// content-type, and Envoy's httpToGrpcStatus translation table only maps
// 400/401/403/404/429/502/503/504 to their gRPC equivalents; 413 isn't in
// that table, so it falls through to codes.Unknown, not
// codes.ResourceExhausted.
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

	// codes.Unknown, not codes.ResourceExhausted: Envoy's buffer filter
	// rejects the over-limit request with HTTP 413, and Envoy's
	// httpToGrpcStatus table only translates 400/401/403/404/429/
	// 502/503/504 -- 413 falls through to Unknown. See the doc comment
	// above.
	//
	// Polled rather than read once: the buffer limit lives in a
	// BackendTrafficPolicy, a separate object Envoy Gateway programs after
	// the GRPCRoute, so an over-limit request sent immediately after the
	// route goes live is answered by an Envoy that has no buffer filter
	// yet and echoes it back OK. The poll is bounded -- a limit that is
	// never enforced still fails the test.
	overBody := strings.Repeat("a", 4096)
	overCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, overBody, callOpt)
		return res, err
	}
	res, err = waitForGRPCResult(ctx, overCall, func(r *harness.GRPCResult) bool {
		return r.Code == codes.Unknown
	}, routeLiveTimeout)
	if err != nil {
		got := codes.Code(0)
		if res != nil {
			got = res.Code
		}
		t.Fatalf("request buffer: over-limit body (4096 bytes > 1Ki) got code %v, want %v: %v", got, codes.Unknown, err)
	}
}

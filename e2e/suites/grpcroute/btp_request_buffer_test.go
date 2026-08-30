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
// bytes (over) should be rejected by Envoy's buffer filter itself --
// which, for gRPC content-type requests, surfaces as codes.ResourceExhausted
// rather than an HTTP 413.
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

	overBody := strings.Repeat("a", 4096)
	res, _, err = echoCall(ctx, overBody, callOpt)
	if err != nil {
		t.Fatalf("request buffer: over-limit request: %v", err)
	}
	if res.Code != codes.ResourceExhausted {
		t.Fatalf("request buffer: over-limit body (4096 bytes > 1Ki) got code %v, want %v", res.Code, codes.ResourceExhausted)
	}
}

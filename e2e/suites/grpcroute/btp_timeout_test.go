//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/e2e/testdata/pb/delay"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCBTPTimeout ports grpc_btp_features/test_timeout.py.
//
// The Python config set only tcp.connectTimeout, which never fires against
// a live pod (the TCP handshake succeeds immediately; delay.DelayService's
// Delay RPC only delays the *response*, not the connection) -- mirroring
// httproute's TestTimeoutBTP, this port uses BackendTrafficPolicy's
// HTTP.RequestTimeout instead (the only timeout setting that can actually
// produce a timeout against a delayed response), routes at podinfo's real
// delay.DelayService (see e2e/testdata/protos/podinfo_delay.proto) with a
// 5-second delay, and asserts codes.Unavailable against a 2-second request
// timeout.
//
// codes.Unavailable, not codes.DeadlineExceeded, is the right expectation:
// an Envoy-enforced BTP request timeout is Envoy replying with plain HTTP
// 504, and Envoy's httpToGrpcStatus table maps 504 -> Unavailable (see
// btp_request_buffer_test.go's doc comment for the full table).
// DeadlineExceeded is what the *client's* grpc-timeout metadata produces
// when IT expires first -- which is exactly why delayCall below sets a 15s
// client-side timeout, comfortably longer than both the 5s backend delay
// and the 2s BTP timeout, so the client's own deadline can never be the one
// that fires. Because Unavailable alone is also what an unreachable
// upstream would produce, the elapsed-time assertion below is what actually
// proves this is the 2s BTP timeout firing rather than some other failure.
func TestGRPCBTPTimeout(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", "delay.DelayService", "")

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
			Timeout: &models.BTPTimeoutConfig{
				HTTP: &models.BTPHTTPTimeoutConfig{RequestTimeout: "2s"},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	delayCall := func(ctx context.Context, seconds int64) (*harness.GRPCResult, error) {
		req := &delay.DelayRequest{Seconds: seconds}
		resp := &delay.DelayResponse{}
		// Client-side timeout must comfortably exceed the 2s BTP request
		// timeout so Envoy's own timeout is what actually fires.
		return env.GW.GRPCTyped(ctx, "delay.DelayService", "Delay", req, resp, callOpt, harness.WithGRPCTimeout(15*time.Second))
	}

	// Readiness probe uses a short (well under 2s) delay so it converges to
	// OK once the route is live, without itself racing the timeout under
	// test.
	res, err := waitForGRPCLive(ctx, func(ctx context.Context) (*harness.GRPCResult, error) {
		return delayCall(ctx, 0)
	}, routeLiveTimeout)
	if err != nil {
		t.Fatalf("timeout: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("timeout: readiness probe (0s delay) got code %v, want %v", res.Code, codes.OK)
	}

	start := time.Now()
	res, err = delayCall(ctx, 5)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("timeout: delayed request: %v", err)
	}
	if res.Code != codes.Unavailable {
		t.Fatalf("timeout: got code %v, want %v (backend delays 5s, BTP request timeout is 2s; Envoy's 504 -> Unavailable mapping)", res.Code, codes.Unavailable)
	}
	// Unavailable alone doesn't distinguish "the 2s BTP timeout fired" from
	// "the upstream was unreachable" -- both produce the same code. Elapsed
	// time is what tells them apart: a real BTP timeout returns close to 2s
	// after the request was sent, well before the backend's 5s delay would
	// have elapsed on its own.
	const maxElapsedForTimeout = 4 * time.Second
	if elapsed >= maxElapsedForTimeout {
		t.Fatalf("timeout: request took %s, want well under %s (closer to the 2s BTP timeout than the 5s backend delay)", elapsed, maxElapsedForTimeout)
	}
}

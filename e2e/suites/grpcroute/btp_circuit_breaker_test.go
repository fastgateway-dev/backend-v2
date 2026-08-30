//go:build e2e

package grpcroute

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/e2e/testdata/pb/delay"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCBTPCircuitBreaker ports grpc_btp_features/test_circuit_breaker.py.
//
// The old assertion (`returncode == 0 or "rpc error" in stderr`) is true of
// almost any outcome, including a totally broken deployment, so the
// circuit breaker's actual effect was never exercised. This port lowers
// maxParallelRequests/maxPendingRequests to 1 each (the Python config's
// limits of 100/50/25/5 are far higher than any burst a single test could
// plausibly generate) and fires 20 concurrent calls, mirroring httproute's
// TestCircuitBreaker: at that ratio, Envoy itself must reject some calls
// with a circuit-breaker Unavailable rather than ever reaching the backend
// for all of them.
//
// The 20 calls must ACTUALLY overlap for that to hold, which a burst of
// echo.EchoService/Echo calls can't guarantee: harness.Gateway's gRPC path
// opens a brand-new grpc.ClientConn (TCP + TLS + HTTP/2 preface) per call,
// so the 20 goroutines' dials stagger by tens of milliseconds while
// podinfo's Echo completes in ~1ms -- by the time the last goroutine's call
// actually reaches the backend, the first is long done, peak upstream
// concurrency never exceeds ~1, and maxPendingRequests never overflows.
// This port instead bursts against podinfo's real
// delay.DelayService/Delay RPC (see e2e/testdata/protos/podinfo_delay.proto
// and grpcroute/btp_timeout_test.go, which uses the same service) with a
// multi-second server-side delay: every goroutine's dial-and-handshake
// overhead (milliseconds) is now negligible next to the seconds each call
// stays in flight, so all 20 calls are genuinely concurrent for almost the
// entire delay window -- comfortably enough to overflow
// maxPendingRequests=1.
func TestGRPCBTPCircuitBreaker(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", "delay.DelayService", "")
	maxParallel := int64(1)
	maxPending := int64(1)

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
			CircuitBreaker: &models.CircuitBreakerConfig{
				MaxParallelRequests: &maxParallel,
				MaxPendingRequests:  &maxPending,
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	// Client-side timeout must comfortably exceed the burst delay (2s)
	// below so the client's own deadline never fires first -- mirrors
	// btp_timeout_test.go's delayCall.
	delayCall := func(ctx context.Context, seconds int64) (*harness.GRPCResult, error) {
		req := &delay.DelayRequest{Seconds: seconds}
		resp := &delay.DelayResponse{}
		return env.GW.GRPCTyped(ctx, "delay.DelayService", "Delay", req, resp, callOpt, harness.WithGRPCTimeout(15*time.Second))
	}

	// A single serial call is never itself subject to the parallel-request
	// limit, so this converges to OK once the route is live. Uses a 0s
	// delay so readiness doesn't itself race the burst's timing.
	res, err := waitForGRPCLive(ctx, func(ctx context.Context) (*harness.GRPCResult, error) {
		return delayCall(ctx, 0)
	}, routeLiveTimeout)
	if err != nil {
		t.Fatalf("circuit breaker: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("circuit breaker: readiness probe got code %v, want %v", res.Code, codes.OK)
	}

	const burst = 20
	const burstDelaySeconds = 2

	// One burst is retried until the breaker trips, rather than fired once
	// and asserted on: the CircuitBreaker lives in a BackendTrafficPolicy,
	// a separate object Envoy Gateway programs AFTER the GRPCRoute, so a
	// burst sent the moment the route goes live hits an Envoy with no
	// breaker configured and all 20 calls legitimately return OK. That
	// race is what made this test fail non-deterministically in ~2.4s
	// while passing whenever the route happened to take longer to settle.
	// The retry is bounded by routeLiveTimeout -- a breaker that never
	// engages still fails the test, with every code seen on the last
	// attempt reported.
	fireBurst := func() []codes.Code {
		var wg sync.WaitGroup
		var mu sync.Mutex
		codesSeen := make([]codes.Code, 0, burst)
		for i := 0; i < burst; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := delayCall(ctx, burstDelaySeconds)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					codesSeen = append(codesSeen, codes.Unknown)
					return
				}
				codesSeen = append(codesSeen, res.Code)
			}()
		}
		wg.Wait()
		return codesSeen
	}

	deadline := time.Now().Add(routeLiveTimeout)
	var codesSeen []codes.Code
	for {
		codesSeen = fireBurst()
		for _, c := range codesSeen {
			if c == codes.Unavailable {
				return
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
	}
	t.Fatalf("circuit breaker: got codes %v from %d concurrent requests, want at least one %v (circuit breaker never tripped within %s)",
		codesSeen, burst, codes.Unavailable, routeLiveTimeout)
}

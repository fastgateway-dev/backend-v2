//go:build e2e

package grpcroute

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
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
// plausibly generate) and fires 20 concurrent Echo calls, mirroring
// httproute's TestCircuitBreaker: at that ratio, Envoy itself must reject
// some calls with a circuit-breaker Unavailable rather than ever reaching
// the backend for all of them.
func TestGRPCBTPCircuitBreaker(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
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

	// A single serial call is never itself subject to the parallel-request
	// limit, so this converges to OK once the route is live.
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("circuit breaker: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("circuit breaker: readiness probe got code %v, want %v", res.Code, codes.OK)
	}

	const burst = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	codesSeen := make([]codes.Code, 0, burst)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _, err := echoCall(ctx, "hello", callOpt)
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

	gotUnavailable := 0
	for _, c := range codesSeen {
		if c == codes.Unavailable {
			gotUnavailable++
		}
	}
	if gotUnavailable == 0 {
		t.Fatalf("circuit breaker: got codes %v from %d concurrent requests, want at least one %v (circuit breaker never tripped)",
			codesSeen, burst, codes.Unavailable)
	}
}

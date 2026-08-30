//go:build e2e

package httproute

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestCircuitBreaker ports tests/http_route_features/test_circuit_breaker.py.
//
// The Python config's limits (maxConnections:100, maxPendingRequests:50,
// maxParallelRequests:25, maxParallelRetries:5) are far higher than any
// burst a single test could plausibly generate, so the circuit breaker
// could never actually trip -- consistent with the old assertion only ever
// checking status in (200, 404). This port lowers maxParallelRequests and
// maxPendingRequests to 1 each and fires 20 *concurrent* requests at a
// backend that always answers 500 (podinfo's /status/500, reached by
// rewriting the route's unique prefix to that literal path), so the burst
// reliably overflows the breaker and Envoy itself must reject some
// requests with 503 -- distinguishable from the 500 the backend returns
// when actually reached.
func TestCircuitBreaker(t *testing.T) {
	t.Parallel()
	podinfoMu.Lock()
	defer podinfoMu.Unlock()

	name, path := uniquePath(t)
	maxParallel := int64(1)
	maxPending := int64(1)

	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/status/500"),
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

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("circuit breaker: route never became live: %v", err)
	}

	const burst = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	statuses := make([]int, 0, burst)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := env.GW.HTTP(ctx, "GET", path)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				statuses = append(statuses, -1)
				return
			}
			statuses = append(statuses, resp.StatusCode)
		}()
	}
	wg.Wait()

	got503 := 0
	for _, s := range statuses {
		if s == 503 {
			got503++
		}
	}
	if got503 == 0 {
		t.Fatalf("circuit breaker: got statuses %v from %d concurrent requests, want at least one 503 (circuit breaker never tripped)", statuses, burst)
	}
}

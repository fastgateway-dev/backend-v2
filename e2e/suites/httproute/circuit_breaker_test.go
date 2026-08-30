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
//
// maxConnections must ALSO be set (to 1). Envoy's max_requests circuit
// breaker (from maxParallelRequests) governs concurrency on an HTTP/2
// upstream by counting concurrent streams on shared connections; podinfo
// is plain HTTP/1.1, where each request opens its own connection and
// concurrency is instead bounded by max_connections (Envoy's default:
// 1024). Without an explicit maxConnections, 20 concurrent requests each
// get their own connection, nothing overflows, and every response comes
// back as podinfo's own 500 rather than a circuit-breaker 503.
func TestCircuitBreaker(t *testing.T) {
	t.Parallel()
	podinfoMu.Lock()
	defer podinfoMu.Unlock()

	name, path := uniquePath(t)
	maxParallel := int64(1)
	maxPending := int64(1)
	maxConnections := int64(1)

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
				MaxConnections:      &maxConnections,
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

	// Polled rather than read once: the policy that produces this outcome
	// is a separate Kubernetes object Envoy Gateway programs AFTER the
	// route, so the route serves traffic un-policied for a short window
	// after deploy -- and WaitForRouteLive/waitForGRPCLive return on the
	// first answer they see, which in that window is the un-policied one.
	// harness.Fixture already waits for the policy to report Accepted;
	// this closes the remaining xDS-push tail. Bounded by routeLiveTimeout,
	// so a policy that never takes effect still fails the test.
	fireBurst := func() []int {
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
		return statuses
	}

	deadline := time.Now().Add(routeLiveTimeout)
	var statuses []int
	for {
		statuses = fireBurst()
		for _, s := range statuses {
			if s == 503 {
				return
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
	}
	t.Fatalf("circuit breaker: got statuses %v from %d concurrent requests, want at least one 503 (circuit breaker never tripped within %s)", statuses, burst, routeLiveTimeout)
}

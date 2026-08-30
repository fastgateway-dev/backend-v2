//go:build e2e

package httproute

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestRetry ports test_retry.py.
//
// The old assertion never checked a retry actually happened -- just status
// in (200, 404). This port points the route at podinfo's /status/503 (via
// urlRewrite), which always fails, so the final response must be 503 no
// matter what, AND checks podinfo's pod logs show at least 4 attempts (1
// initial + numRetries=3), proving Envoy actually retried rather than
// giving up after one try.
func TestRetry(t *testing.T) {
	t.Parallel()
	podinfoMu.Lock()
	defer podinfoMu.Unlock()

	name, path := uniquePath(t)
	numRetries := int32(3)

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
			URLRewrite: rewriteTo("/status/503"),
		},
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
			Retry: &models.RetryConfig{
				NumRetries: &numRetries,
				RetryOn: &models.RetryOn{
					Triggers:        []string{"5xx", "reset", "connect-failure"},
					HTTPStatusCodes: []int{502, 503, 504},
				},
				PerRetryPolicy: &models.PerRetryPolicy{
					Timeout: strPtr("5s"),
					BackOff: &models.BackOffPolicy{
						BaseInterval: strPtr("100ms"),
						MaxInterval:  strPtr("1s"),
					},
				},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	// The route always hits podinfo's /status/503, so the first non-404
	// response IS the real test: it carries the outcome of the full retry
	// sequence (1 initial attempt + numRetries=3 retries).
	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("retry: route never became live: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Fatalf("retry: got final status %d, want 503 (backend always returns 503)", resp.StatusCode)
	}

	logs, err := env.Kube.PodLogs(ctx, backendNamespace, podinfoLabel, 200)
	if err != nil {
		t.Fatalf("retry: fetch podinfo pod logs: %v", err)
	}
	attempts := strings.Count(logs, "/status/503")
	if attempts < 4 {
		t.Fatalf("retry: podinfo logs show %d attempt(s) at /status/503, want at least 4 (1 initial + numRetries=3); log tail: %q", attempts, lastNChars(logs, 500))
	}
}

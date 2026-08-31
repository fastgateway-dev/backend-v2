//go:build e2e

package grpcroute

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"google.golang.org/grpc/codes"
)

// TestGRPCBTPRateLimit ports grpc_btp_features/test_rate_limit.py.
//
// The old assertion was a permanent no-op: `assert got_limited or True` can
// never fail regardless of what got_limited holds (task-12-brief Step 2).
// This port asserts codes.Unavailable is ACTUALLY observed somewhere in the
// request burst; a burst of 20 calls against a 3/minute limit gives ample
// headroom over the old 10-call burst.
//
// codes.Unavailable, not codes.ResourceExhausted, is the right expectation:
// Envoy Gateway's ratelimit filter replies with plain HTTP 429, and Envoy's
// httpToGrpcStatus table maps 429 -> Unavailable (see
// btp_request_buffer_test.go's doc comment for the full table).
// ResourceExhausted would require the filter's
// rate_limited_as_resource_exhausted option, which Envoy Gateway does not
// expose. Because Unavailable alone doesn't distinguish real rate limiting
// from a generic 503, the assertion below also requires a rate-limit
// response header (x-ratelimit-limit) to be present on the limited call.
func TestGRPCBTPRateLimit(t *testing.T) {
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
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			RateLimit: &models.RateLimitConfig{
				Global: &models.GlobalRateLimitConfig{
					Rules: []models.RateLimitRule{{Limit: models.RateLimitValue{Requests: 3, Unit: "Minute"}}},
				},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt)
		return res, err
	}
	if _, err := waitForGRPCLive(ctx, call, routeLiveTimeout); err != nil {
		t.Fatalf("rate limit: route never became live: %v", err)
	}

	// Polled rather than read once: the policy that produces this outcome
	// is a separate Kubernetes object Envoy Gateway programs AFTER the
	// route, so the route serves traffic un-policied for a short window
	// after deploy -- and WaitForRouteLive/waitForGRPCLive return on the
	// first answer they see, which in that window is the un-policied one.
	// harness.Fixture already waits for the policy to report Accepted;
	// this closes the remaining xDS-push tail. Bounded by routeLiveTimeout,
	// so a policy that never takes effect still fails the test.
	const burst = 20
	deadline := time.Now().Add(routeLiveTimeout)
	var lastCode codes.Code
	var lastHeaders string
	for {
		for i := 0; i < burst; i++ {
			res, _, err := echoCall(ctx, "hello", callOpt)
			if err != nil {
				t.Fatalf("rate limit: request %d: %v", i, err)
			}
			lastCode = res.Code
			if res.Code == codes.Unavailable && rateLimitHeaderPresent(res) {
				return
			}
			lastHeaders = fmt.Sprintf("header=%v trailer=%v", res.Header, res.Trailer)
		}
		if !time.Now().Before(deadline) {
			break
		}
	}
	t.Fatalf("rate limit: no request among %d got code %v carrying a rate-limit header (e.g. x-ratelimit-limit) within %s, want at least one (last observed code: %v, last headers: %s)",
		burst, codes.Unavailable, routeLiveTimeout, lastCode, lastHeaders)
}

// rateLimitHeaderPresent reports whether res carries one of the
// x-ratelimit-* response headers Envoy's ratelimit filter attaches to a
// limited response (a real e2e run observed x-ratelimit-limit,
// x-ratelimit-remaining, and x-ratelimit-reset arriving as gRPC trailers).
// Checking both Header and Trailer keeps this robust to exactly where a
// given Envoy version places them; requiring one of them is what
// distinguishes an actual rate-limit rejection (codes.Unavailable) from any
// other cause of the same code, such as a generically unreachable upstream.
func rateLimitHeaderPresent(res *harness.GRPCResult) bool {
	for _, key := range []string{"x-ratelimit-limit", "x-ratelimit-remaining", "x-ratelimit-reset"} {
		if len(res.Header.Get(key)) > 0 || len(res.Trailer.Get(key)) > 0 {
			return true
		}
	}
	return false
}

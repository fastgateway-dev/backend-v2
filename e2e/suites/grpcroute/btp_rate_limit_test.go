//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCBTPRateLimit ports grpc_btp_features/test_rate_limit.py.
//
// The old assertion was a permanent no-op: `assert got_limited or True` can
// never fail regardless of what got_limited holds (task-12-brief Step 2).
// This port asserts codes.ResourceExhausted is ACTUALLY observed somewhere
// in the request burst; a burst of 20 calls against a 3/minute limit gives
// ample headroom over the old 10-call burst.
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
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
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

	gotLimited := false
	const burst = 20
	var lastCode codes.Code
	for i := 0; i < burst; i++ {
		res, _, err := echoCall(ctx, "hello", callOpt)
		if err != nil {
			t.Fatalf("rate limit: request %d: %v", i, err)
		}
		lastCode = res.Code
		if res.Code == codes.ResourceExhausted {
			gotLimited = true
			break
		}
	}
	if !gotLimited {
		t.Fatalf("rate limit: no request among %d got code %v, want at least one %v (last observed code: %v)",
			burst, codes.ResourceExhausted, codes.ResourceExhausted, lastCode)
	}
}

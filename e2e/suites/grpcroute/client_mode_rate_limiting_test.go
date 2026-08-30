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

// TestGRPCClientModeRateLimit ports
// grpc_client_mode/test_rate_limiting.py: a client attached with API key
// auth plus a per-client rate limit (3/minute). The first authenticated
// request must succeed, and a burst afterward must eventually get
// codes.ResourceExhausted (or codes.Unavailable, per the harness's own
// list of typed codes -- Envoy's global rate-limit service can surface
// either depending on how the limiter itself responds). The Python
// original already asserted this for real (`assert got_rate_limited,
// "Expected rate limiting after exceeding limit"`, no `or True` escape
// hatch), so this port carries the burst size forward unweakened.
func TestGRPCClientModeRateLimit(t *testing.T) {
	t.Parallel()

	routeName, match, routeOpt := uniqueMatch(t, "Exact", echoServiceName, "")

	cfg := services.CreateRouteInput{
		Name:         routeName,
		Protocol:     models.RouteProtocolGRPC,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			Matches:              []models.RouteMatch{match},
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	client, err := createClient(ctx, harness.UniqueName(t), teamID(t))
	if err != nil {
		t.Fatalf("client mode rate limit: create client: %v", err)
	}
	cleanupClient(t, client.ID.String())

	apiKey, err := generateClientAPIKey(ctx, client.ID.String(), "x-api-key")
	if err != nil {
		t.Fatalf("client mode rate limit: generate api key: %v", err)
	}

	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:     client.ID,
		EnableAPIKey: true,
		RateLimitConfig: &models.RateLimitConfig{
			Global: &models.GlobalRateLimitConfig{
				Rules: []models.RateLimitRule{{Limit: models.RateLimitValue{Requests: 3, Unit: "Minute"}}},
			},
		},
	}); err != nil {
		t.Fatalf("client mode rate limit: attach client: %v", err)
	}

	authOpts := []harness.GRPCOpt{
		routeOpt,
		harness.WithGRPCMetadata("x-api-key", apiKey),
		harness.WithGRPCMetadata("x-client-id", client.ID.String()),
	}
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", authOpts...)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("client mode rate limit: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("client mode rate limit: first authenticated request got code %v, want %v", res.Code, codes.OK)
	}

	gotLimited := false
	const burst = 20
	var lastCode codes.Code
	for i := 0; i < burst; i++ {
		res, _, err := echoCall(ctx, "hello", authOpts...)
		if err != nil {
			t.Fatalf("client mode rate limit: request %d: %v", i, err)
		}
		lastCode = res.Code
		if res.Code == codes.ResourceExhausted || res.Code == codes.Unavailable {
			gotLimited = true
			break
		}
	}
	if !gotLimited {
		t.Fatalf("client mode rate limit: no request among %d got %v or %v, want rate limiting after exceeding 3/minute (last observed code: %v)",
			burst, codes.ResourceExhausted, codes.Unavailable, lastCode)
	}
}

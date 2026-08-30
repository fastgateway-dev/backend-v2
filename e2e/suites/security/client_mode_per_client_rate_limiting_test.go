//go:build e2e

package security

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestClientModePerClientRateLimit ports
// client_mode/test_per_client_rate_limiting.py. The Python source already
// had a real, unweakened assertion here (`assert got_429, "Expected rate
// limiting after exceeding limit"` -- no `or True` escape hatch, and not
// one of task-13-brief's "8 tautologies"), so this port carries its
// burst-and-observe design forward rather than rewriting it: attach a
// client with API-key auth plus a 3-requests/minute rate limit, confirm
// the first authenticated request succeeds, then fire a burst and require
// that at least one response is actually 429.
func TestClientModePerClientRateLimit(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

	cfg := services.CreateRouteInput{
		Name:         name,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
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

	authProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-api-key", apiKey),
			harness.WithHeader("x-client-id", client.ID.String()),
		)
	}
	if _, err := waitForHTTPStatus(ctx, authProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("client mode rate limit: first authenticated request: %v", err)
	}

	got429 := false
	const burst = 20
	var lastStatus int
	for i := 0; i < burst; i++ {
		resp, err := authProbe(ctx)
		if err != nil {
			t.Fatalf("client mode rate limit: request %d: %v", i, err)
		}
		lastStatus = resp.StatusCode
		if resp.StatusCode == 429 {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatalf("client mode rate limit: no request among %d got 429, want rate limiting after exceeding 3/minute (last observed status: %d)", burst, lastStatus)
	}
}

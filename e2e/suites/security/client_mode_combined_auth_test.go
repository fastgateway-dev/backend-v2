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

// TestClientModeCombinedAuth ports client_mode/test_combined_auth.py,
// fixing the tautology task-13-brief names explicitly, and strengthens
// the negative case beyond both the Python source and task-13-brief's own
// table entry ("either one alone -> 401/403").
//
// The client here has BOTH IP allowlisting (CIDR 0.0.0.0/0) and API key
// auth enabled, combined with AND semantics (see
// RouteService.buildAPIKeySecurityPolicyConfig's "AND logic" comment,
// internal/services/route_service.go:6895 -- when requireIP is set, the
// per-client authorization rule's ClientCIDRs are layered onto the SAME
// rule as the API-key check, not evaluated as an independent
// either-suffices condition). Because the CIDR is deliberately 0.0.0.0/0
// (the only way to also exercise a genuine POSITIVE case, since the test
// runner's real source IP is not known in advance -- an excluded CIDR
// would make authentication succeed impossible entirely), the IP factor
// always passes on its own and can't be selectively "left out" the way a
// header-borne credential can. The meaningful negative case this
// combination actually admits is: does the request still get denied when
// api-key is missing/wrong, even though IP unconditionally passes? A
// combined check that silently degraded to "IP-allowlist-only" (ignoring
// the API-key requirement whenever IP already passes) would fail this and
// pass a bare "no credentials at all" check identically -- so this
// asserts BOTH a missing api-key AND a wrong one, each with a valid
// x-client-id, are still denied.
func TestClientModeCombinedAuth(t *testing.T) {
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
		t.Fatalf("client mode combined auth: create client: %v", err)
	}
	cleanupClient(t, client.ID.String())

	apiKey, err := generateClientAPIKey(ctx, client.ID.String(), "x-api-key")
	if err != nil {
		t.Fatalf("client mode combined auth: generate api key: %v", err)
	}
	if err := addClientIP(ctx, client.ID.String(), "0.0.0.0/0", "allow all"); err != nil {
		t.Fatalf("client mode combined auth: add client IP: %v", err)
	}

	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:          client.ID,
		EnableIPAllowlist: true,
		EnableAPIKey:      true,
	}); err != nil {
		t.Fatalf("client mode combined auth: attach client: %v", err)
	}

	// Positive first: both credentials (IP always passes; api-key
	// valid) -> 200. Proves the route and attachment have converged
	// before either negative probe is trusted (see the package doc
	// comment).
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-api-key", apiKey),
			harness.WithHeader("x-client-id", client.ID.String()),
		)
	}
	if _, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("client mode combined auth: with valid api key + client id (allowed IP): %v", err)
	}

	// Negative: IP alone (api-key omitted entirely) must still be denied.
	missingKeyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-client-id", client.ID.String()))
	}
	requireStatus(t, ctx, missingKeyProbe, 401, 403)

	// Negative: IP alone + a WRONG api-key must still be denied (rules
	// out a check that only verifies the header's PRESENCE, not its
	// value).
	wrongKeyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-api-key", "not-the-real-key"),
			harness.WithHeader("x-client-id", client.ID.String()),
		)
	}
	requireStatus(t, ctx, wrongKeyProbe, 401, 403)
}

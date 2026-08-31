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
// RouteService.buildAPIKeySecurityPolicyConfig, which delegates to
// routeplan.applyClientSecurityFeatures's "AND logic" comment,
// internal/routeplan/securitypolicy_features.go:307 -- when requireIP is
// set, the per-client authorization rule's ClientCIDRs are layered onto the SAME
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

	// NEGATIVE first, not positive. A 200 cannot prove convergence here:
	// it is also exactly what this route returns while the SecurityPolicy
	// is still being programmed, since an unpolicied route just forwards
	// to the backend. Gating on 200 therefore lets the negative probe --
	// which retries only on transport errors, not on a wrong status --
	// read that same unconverged 200 and fail. The denial is the only
	// status the unconverged state CANNOT produce, so it is the only
	// sound gate.
	// Negative: IP alone (api-key omitted entirely) must still be denied.
	missingKeyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-client-id", client.ID.String()))
	}
	if _, err := waitForHTTPStatus(ctx, missingKeyProbe, routeLiveTimeout, 401, 403); err != nil {
		t.Fatalf("client mode combined auth: with the api key omitted: %v", err)
	}

	// Positive: both credentials (IP always passes; api-key valid) -> 200.
	// Proves the attachment does not simply deny everything.
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-api-key", apiKey),
			harness.WithHeader("x-client-id", client.ID.String()),
		)
	}
	// Bounded poll, not a single call. The negative above already proved
	// the policy is enforcing, so this cannot pass by catching an
	// unconverged route -- but a lone call can still lose a race the
	// enforcement gate does not cover. Envoy fetches a JWKS lazily on
	// first use and answers 401 "Jwks remote fetch is failed" while that
	// fetch is in flight, which is exactly how this failed in CI. A
	// credential that is never accepted still fails, at the timeout.
	if _, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("client mode combined auth: with valid api key + client id: %v", err)
	}

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

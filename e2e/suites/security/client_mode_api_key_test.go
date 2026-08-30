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

// TestClientModeAPIKey ports client_mode/test_api_key.py, fixing the
// tautology task-13-brief names explicitly: the Python original's final
// assertion (`resp.status_code in (200, 404)`, guaranteed by the
// preceding retry_until's own accepted_status) could never fail.
//
// The negative probe deliberately keeps x-client-id (a real, attached
// client) and omits ONLY x-api-key -- not a bare unauthenticated request.
// See the package doc comment: a bare-unauthenticated probe only proves
// the route's defaultTrafficPolicy=deny catch-all rejects traffic with no
// client context at all; it says nothing about whether the api-key
// credential itself is checked once a client IS identified. Keeping
// x-client-id here means a client attachment whose api-key check was
// silently bypassed (accept anything, or nothing, once x-client-id names
// a real client) would be caught by this test.
func TestClientModeAPIKey(t *testing.T) {
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
		t.Fatalf("client mode api key: create client: %v", err)
	}
	cleanupClient(t, client.ID.String())

	apiKey, err := generateClientAPIKey(ctx, client.ID.String(), "x-api-key")
	if err != nil {
		t.Fatalf("client mode api key: generate api key: %v", err)
	}

	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:     client.ID,
		EnableAPIKey: true,
	}); err != nil {
		t.Fatalf("client mode api key: attach client: %v", err)
	}

	// Negative first, not positive: the per-client HTTPRoute this
	// attachment creates matches on the x-client-id header ALONE --
	// x-api-key is enforced solely by the SecurityPolicy attached to it
	// (see internal/services/route_service.go's per-client route
	// generation). So while that SecurityPolicy hasn't converged yet, a
	// request carrying x-client-id but no x-api-key reaches 200 exactly
	// like one carrying both would -- the positive probe's 200 proves
	// nothing, because the identical unconverged state produces it too. A
	// 401/403 CANNOT come from that state; it can only come from the
	// SecurityPolicy actually being attached and its filter actually
	// rejecting the missing key. Waiting here for the denial is therefore
	// what proves the attachment's policy has converged, not just the
	// per-client route.
	denyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-client-id", client.ID.String()))
	}
	if _, err := waitForHTTPStatus(ctx, denyProbe, routeLiveTimeout, 401, 403); err != nil {
		t.Fatalf("client mode api key: without api key: %v", err)
	}

	// Now that the negative probe has proven the SecurityPolicy is
	// genuinely enforcing, the correct key alongside the client id
	// reaching 200 proves it actually accepts a real credential rather
	// than rejecting everything.
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
		t.Fatalf("client mode api key: with a valid api key + client id: %v", err)
	}
}

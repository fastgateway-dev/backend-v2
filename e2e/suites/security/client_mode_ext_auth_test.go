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

// TestClientModeExtAuth ports client_mode/test_ext_auth.py, fixing the
// tautology task-13-brief names explicitly. The Python source's own
// design already carried a valid x-api-key + x-client-id on BOTH the
// positive and negative probes, varying only x-ext-auth-allow -- this is
// exactly the "credential-specific negative probe" discipline the package
// doc comment describes (proving the ext-auth decision itself is what
// gates the request, with authentication held constant), transcribed as
// designed.
func TestClientModeExtAuth(t *testing.T) {
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
		t.Fatalf("client mode ext auth: create client: %v", err)
	}
	cleanupClient(t, client.ID.String())

	apiKey, err := generateClientAPIKey(ctx, client.ID.String(), "x-api-key")
	if err != nil {
		t.Fatalf("client mode ext auth: generate api key: %v", err)
	}

	failOpen := false
	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:     client.ID,
		EnableAPIKey: true,
		ExtAuth: &models.ExtAuthConfig{
			Type: "http",
			HTTP: &models.ExtAuthHTTPConfig{
				BackendRef: models.ExtAuthBackendRef{Name: externalAuthService, Namespace: backendNamespace, Port: externalAuthPort},
				Path:       "/auth",
			},
			HeadersToExtAuth: []string{"x-ext-auth-allow"},
			FailOpen:         &failOpen,
		},
	}); err != nil {
		t.Fatalf("client mode ext auth: attach client: %v", err)
	}

	authHeaders := func(allow string) []harness.ReqOpt {
		return []harness.ReqOpt{
			harness.WithHeader("x-api-key", apiKey),
			harness.WithHeader("x-client-id", client.ID.String()),
			harness.WithHeader("x-ext-auth-allow", allow),
		}
	}

	// NEGATIVE first, not positive. A 200 cannot prove convergence here:
	// it is also exactly what this route returns while the SecurityPolicy
	// is still being programmed, since an unpolicied route just forwards
	// to the backend. Gating on 200 therefore lets the negative probe --
	// which retries only on transport errors, not on a wrong status --
	// read that same unconverged 200 and fail. The denial is the only
	// status the unconverged state CANNOT produce, so it is the only
	// sound gate.
	denyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, authHeaders("false")...)
	}
	if _, err := waitForHTTPStatus(ctx, denyProbe, routeLiveTimeout, 403); err != nil {
		t.Fatalf("client mode ext auth: with x-ext-auth-allow=false: %v", err)
	}

	// Positive: proves the attachment does not simply deny everything.
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, authHeaders("true")...)
	}
	// Bounded poll, not a single call. The negative above already proved
	// the policy is enforcing, so this cannot pass by catching an
	// unconverged route -- but a lone call can still lose a race the
	// enforcement gate does not cover. Envoy fetches a JWKS lazily on
	// first use and answers 401 "Jwks remote fetch is failed" while that
	// fetch is in flight, which is exactly how this failed in CI. A
	// credential that is never accepted still fails, at the timeout.
	if _, err := waitForHTTPStatus(ctx, allowProbe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("client mode ext auth: with x-ext-auth-allow=true: %v", err)
	}
}

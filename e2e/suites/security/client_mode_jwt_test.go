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

// TestClientModeJWT ports client_mode/test_jwt.py, fixing the tautology
// task-13-brief names explicitly. Unlike security_general_mode/test_jwt.py,
// the Python source here already used the correct in-cluster FQDN issuer
// (jwt-server.default.svc.cluster.local:9000, matching jwtIssuerURL()) --
// no "wrong reason" issuer bug to fix on this side, per task-13-brief's
// "check the other JWT tests for the same trap" instruction.
//
// The negative probe keeps x-client-id (a real, attached client) and
// omits only the Authorization/bearer token -- matching the Python
// source's own design here (it already sent x-client-id without a token)
// and the package doc comment's "credential-specific negative probe"
// discipline: this proves JWT validation itself is enforced once a client
// is identified, not merely that unattributed traffic is denied.
func TestClientModeJWT(t *testing.T) {
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
		t.Fatalf("client mode jwt: create client: %v", err)
	}
	cleanupClient(t, client.ID.String())

	if err := configureClientJWT(ctx, client.ID.String(), jwtIssuerURL(), jwtIssuerURL()+"/jwks", []string{"my-api"}); err != nil {
		t.Fatalf("client mode jwt: configure client JWT: %v", err)
	}

	if _, err := attachAndDeploy(ctx, route.ID.String(), services.AttachFromRouteInput{
		ClientID:  client.ID,
		EnableJWT: true,
	}); err != nil {
		t.Fatalf("client mode jwt: attach client: %v", err)
	}

	token, err := generateJWTToken(ctx, "my-api")
	if err != nil {
		t.Fatalf("client mode jwt: generate token from jwt-server: %v", err)
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
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-client-id", client.ID.String()))
	}
	if _, err := waitForHTTPStatus(ctx, denyProbe, routeLiveTimeout, 401, 403); err != nil {
		t.Fatalf("client mode jwt: without a token: %v", err)
	}

	// Positive: proves the JWT check accepts a valid token rather than
	// denying everything.
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("Authorization", "Bearer "+token),
			harness.WithHeader("x-client-id", client.ID.String()),
		)
	}
	requireStatus(t, ctx, allowProbe, 200)
}

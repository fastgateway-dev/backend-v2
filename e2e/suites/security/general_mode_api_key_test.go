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

const (
	// apiKeyAuthSecretName is the fixed Kubernetes Secret created by
	// e2e/deps/create-secrets.sh in FastGatewayNamespace
	// ("fastgateway-system"). Envoy Gateway's APIKeyAuth SecurityPolicy
	// expects an Opaque Secret with each API key stored under a data key
	// named for its client ID -- see that script's comment, verified
	// against Envoy Gateway's own apikey-auth docs
	// (https://gateway.envoyproxy.io/v1.7/tasks/security/apikey-auth/):
	// "The secret is an Opaque secret, with each API key stored under a
	// key corresponding to the client ID." The data key itself
	// ("e2e-client") is never presented by a caller -- only the value is.
	apiKeyAuthSecretName = "e2e-apikey-auth"

	// apiKeyAuthAPIKey is the raw API key value stored under
	// apiKeyAuthSecretName's "e2e-client" data key. Per Envoy Gateway's
	// apikey-auth docs, a client authenticates by sending this exact
	// value in the configured header (here, "x-api-key") -- there is no
	// client-id/key pairing or "Bearer"-style prefix on a plain header
	// extraction; that prefix stripping only applies when extracting from
	// the Authorization header specifically.
	apiKeyAuthAPIKey = "e2e-test-api-key-not-for-production"
)

// TestGeneralModeAPIKeyDenied ports
// security_general_mode/test_api_key.py:test_api_key_denied_without_key,
// and extends it with the positive case (and a wrong-key negative case)
// now that e2e/deps/create-secrets.sh seeds a real APIKeyAuth credential
// Secret (apiKeyAuthSecretName, in FastGatewayNamespace). The name is kept
// as-is even though it now asserts more than denial, since it's
// cross-referenced by name from this package's main_test.go doc comment
// and from external_authorization_http_default_headers_test.go.
//
// Envoy Gateway fails closed when a SecurityPolicy cannot be translated
// (e.g. its credentialRefs Secret doesn't exist): it installs a 500 direct
// response for the route rather than leaving it unprotected. Before this
// fixture existed, secretName pointed at a per-test unique name that
// nothing ever created, so every probe here -- positive and negative
// alike -- observed a 500 (or, over gRPC, codes.Unknown) instead of the
// intended 200/401/403, and the denial-only assertion the previous version
// of this test made could never actually fail for the right reason. See
// e2e/suites/grpcroute/security_api_key_test.go's TestGRPCAPIKeyDenied for
// the identical mechanism and gRPC's status-code mapping.
func TestGeneralModeAPIKeyDenied(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

	cfg := services.CreateRouteInput{
		Name:         name,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			// nginx-service only ever serves "/" -- without this rewrite
			// the positive probe's own unique path would get nginx's
			// legitimate 404, indistinguishable from "route not
			// programmed yet". See the package doc comment and rewriteTo.
			URLRewrite: rewriteTo("/"),
		},
		SecurityPolicy: &services.SecurityPolicyInput{
			APIKeyAuth: &services.APIKeyAuthInput{SecretName: apiKeyAuthSecretName, HeaderName: "x-api-key"},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	// Negative first, not positive: an HTTPRoute that has landed but whose
	// SecurityPolicy hasn't converged yet answers 200 to a request with NO
	// key exactly as readily as to one with a valid key -- 200 on the
	// positive probe proves nothing about whether the APIKeyAuth filter is
	// even attached, since that same unconverged state produces it too. A
	// 401/403 CANNOT come from that state; it can only come from the
	// SecurityPolicy actually being attached and its filter actually
	// rejecting the missing credential. Waiting here for the denial is
	// therefore what proves the policy has converged, not just the route.
	noKeyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	if _, err := waitForHTTPStatus(ctx, noKeyProbe, routeLiveTimeout, 401, 403); err != nil {
		t.Fatalf("api key: without key: %v", err)
	}

	// Now that the negative probe has proven the SecurityPolicy is
	// genuinely enforcing, a correct key reaching 200 proves it actually
	// accepts a real credential rather than rejecting everything (e.g. a
	// still-unresolvable credentialRefs Secret, which fails closed with
	// 500 -- see the doc comment above).
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-api-key", apiKeyAuthAPIKey))
	}
	requireStatus(t, ctx, allowProbe, 200)

	// A key that doesn't match any credential in the Secret must also be
	// rejected. Convergence is already proven above, so this can be a
	// single-shot check like the positive one.
	wrongKeyProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-api-key", "wrong-"+apiKeyAuthAPIKey))
	}
	requireStatus(t, ctx, wrongKeyProbe, 401, 403)
}

//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/e2e/testdata/pb/echo"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"google.golang.org/grpc/codes"
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

// TestGRPCAPIKeyDenied ports grpc_security/test_api_key.py, and extends it
// with the positive case (and a wrong-key negative case) now that
// e2e/deps/create-secrets.sh seeds a real APIKeyAuth credential Secret
// (apiKeyAuthSecretName, in FastGatewayNamespace). The name is kept as-is
// even though it now asserts more than denial, since it's cross-referenced
// by name from e2e/suites/security/main_test.go's package doc comment.
//
// Envoy Gateway fails closed when a SecurityPolicy cannot be translated
// (e.g. its credentialRefs Secret doesn't exist): it installs a 500 direct
// response for the route rather than leaving it unprotected -- for gRPC
// traffic through Envoy's grpc-web/http bridge this surfaces to a gRPC
// client as codes.Unknown, never a real auth-related code. Before this
// fixture existed, secretName pointed at a per-test unique name that
// nothing ever created, so every probe here -- positive and negative
// alike -- observed that failure instead of the intended
// OK/Unauthenticated, and the denial-only assertion the previous version
// of this test made could never actually fail for the right reason.
func TestGRPCAPIKeyDenied(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")

	cfg := services.CreateRouteInput{
		Name:         name,
		Protocol:     models.RouteProtocolGRPC,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{match},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
		SecurityPolicy: &routeplan.SecurityPolicyInput{
			APIKeyAuth: &routeplan.APIKeyAuthInput{SecretName: apiKeyAuthSecretName, HeaderName: "x-api-key"},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	// Negative first (mirrors TestGRPCJWT/TestGRPCClientModeAPIKey in this
	// package): the SecurityPolicy denies by default before any credential
	// is attached, so this also confirms the route itself is live -- an
	// unprogrammed route or an untranslated SecurityPolicy would surface
	// as codes.Unknown here, not codes.Unauthenticated.
	noKeyCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt)
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, noKeyCall, routeLiveTimeout, codes.Unauthenticated); err != nil {
		t.Fatalf("api key denied: without x-api-key: %v", err)
	}

	// Negative: a key that doesn't match any credential in the Secret.
	wrongKeyCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt, harness.WithGRPCMetadata("x-api-key", "wrong-"+apiKeyAuthAPIKey))
		return res, err
	}
	if _, err := waitForGRPCCodeIn(ctx, wrongKeyCall, routeLiveTimeout, codes.Unauthenticated); err != nil {
		t.Fatalf("api key denied: with wrong x-api-key: %v", err)
	}

	// Positive: the correct API key must be allowed.
	authOpt := harness.WithGRPCMetadata("x-api-key", apiKeyAuthAPIKey)
	// Polled for codes.OK rather than called once: the negatives above
	// prove the route and its policy are live, but a single call can still
	// land on a transient codes.Unavailable (Envoy reports "no healthy
	// upstream" while an upstream connection is being (re)established),
	// which a one-shot read turns into a spurious failure. Auth that is
	// genuinely broken answers Unauthenticated/PermissionDenied forever,
	// so the poll still fails in that case.
	var resp *echo.Message
	authedCall := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, msg, err := echoCall(ctx, "hello-authed", callOpt, authOpt)
		resp = msg
		return res, err
	}
	res, err := waitForGRPCResult(ctx, authedCall, func(r *harness.GRPCResult) bool {
		return r.Code == codes.OK
	}, routeLiveTimeout)
	if err != nil {
		got := codes.Code(0)
		if res != nil {
			got = res.Code
		}
		t.Fatalf("api key: with valid key got code %v, want %v: %v", got, codes.OK, err)
	}
	if resp.Body != "hello-authed" {
		t.Fatalf("api key: got echoed body %q, want %q", resp.Body, "hello-authed")
	}
}

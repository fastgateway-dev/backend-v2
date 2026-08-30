//go:build e2e

package security

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestExternalAuthorizationHTTPDefaultHeaders ports
// external_authorization/test_http_default_headers.py:test_ext_auth_denied_no_header.
//
// This route's SecurityPolicy deliberately omits headersToExtAuth. Per
// e2e/servers/external-auth's own comment, Envoy's HTTP-type ext-authz
// forwards only a fixed default set of headers (Host, Method, Path,
// Content-Length, Authorization) to the ext-auth backend unless
// headersToExtAuth explicitly widens that set -- and e2e/servers/
// external-auth/main.go's handleAuth gates its allow/deny decision solely
// on "x-ext-auth-allow", which is never in that default set. There is
// therefore no request this specific configuration can ever allow: this
// is not an incomplete negative-only test (like
// TestGeneralModeAPIKeyDenied's documented gap), it is the complete and
// correct assertion for what this configuration exists to prove -- that
// a custom header is NOT forwarded to ext-auth without headersToExtAuth.
// TestExternalAuthorizationHTTPHeadersAllowed/Denied (same package) is
// what proves the ALLOW path works once headersToExtAuth is configured.
//
// To make that point as strongly as possible, this port explicitly sends
// x-ext-auth-allow=true (the Python original sent no ext-auth header at
// all) and still asserts 403: even an explicit attempt to signal "allow"
// is denied, because the header never reaches the ext-auth backend.
func TestExternalAuthorizationHTTPDefaultHeaders(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)
	failOpen := false

	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
		},
		SecurityPolicy: &services.SecurityPolicyInput{
			ExtAuth: &models.ExtAuthConfig{
				Type: "http",
				HTTP: &models.ExtAuthHTTPConfig{
					BackendRef: models.ExtAuthBackendRef{Name: externalAuthService, Namespace: backendNamespace, Port: externalAuthPort},
					Path:       "/auth",
				},
				FailOpen: &failOpen,
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-ext-auth-allow", "true"))
	}
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 403); err != nil {
		t.Fatalf("external authorization (http, default headers): %v", err)
	}
}

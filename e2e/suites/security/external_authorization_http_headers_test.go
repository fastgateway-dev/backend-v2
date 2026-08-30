//go:build e2e

package security

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// extAuthWithHeaders builds an HTTP ext-auth SecurityPolicy that widens
// the forwarded header set with headersToExtAuth -- the configuration
// TestExternalAuthorizationHTTPDefaultHeaders (same package) proves is
// REQUIRED before "x-ext-auth-allow" ever reaches e2e/servers/
// external-auth. Matches
// external_authorization/test_http_headers_to_ext_auth.py's ALLOWED_CONFIG
// / DENIED_CONFIG (identical securityPolicy in both).
func extAuthWithHeaders() *services.SecurityPolicyInput {
	failOpen := false
	return &services.SecurityPolicyInput{
		ExtAuth: &models.ExtAuthConfig{
			Type: "http",
			HTTP: &models.ExtAuthHTTPConfig{
				BackendRef: models.ExtAuthBackendRef{Name: externalAuthService, Namespace: backendNamespace, Port: externalAuthPort},
				Path:       "/auth",
			},
			HeadersToExtAuth: []string{"x-ext-auth-allow"},
			FailOpen:         &failOpen,
		},
	}
}

// TestExternalAuthorizationHTTPHeadersAllowed ports
// external_authorization/test_http_headers_to_ext_auth.py:test_ext_auth_allowed.
// The Python original tolerated `resp.status_code in (200, 404)`; this
// port uses rewriteTo("/") to get an unambiguous 200 from nginx and
// asserts exactly that.
func TestExternalAuthorizationHTTPHeadersAllowed(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

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
			URLRewrite: rewriteTo("/"),
		},
		SecurityPolicy: extAuthWithHeaders(),
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-ext-auth-allow", "true"))
	}
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 200); err != nil {
		t.Fatalf("external authorization (http, headersToExtAuth): allowed: %v", err)
	}
}

// TestExternalAuthorizationHTTPHeadersDenied ports
// external_authorization/test_http_headers_to_ext_auth.py:test_ext_auth_denied.
// Already had a real assertion (`== 403`, not a tolerant list) in the
// Python source; transcribed as-is, on its own route (matching the source's
// separate DENIED_CONFIG), since TestExternalAuthorizationHTTPHeadersAllowed
// (same package, same mechanism) already independently proves the allow
// path works when headersToExtAuth is configured.
func TestExternalAuthorizationHTTPHeadersDenied(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

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
		SecurityPolicy: extAuthWithHeaders(),
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-ext-auth-allow", "false"))
	}
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 403); err != nil {
		t.Fatalf("external authorization (http, headersToExtAuth): denied: %v", err)
	}
}

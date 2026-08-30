//go:build e2e

// Package traffic ports the Python regression suite's
// tests/rate_limiting/*.py (7 tests), tests/extensions/*.py (2 tests),
// tests/external_backends/*.py (2 tests), and tests/route_matching/*.py
// (5 tests) -- 16 tests total -- to Go (task-15). These exercise
// per-route BackendTrafficPolicy rate limiting, EnvoyExtensionPolicy
// (Lua/Wasm) response injection, external (non-Kubernetes-Service)
// backends, and Gateway API route-matching (headers, method, query
// parameters).
//
// # Tautologies fixed
//
// Five tests in this package ended, in the Python source, with a
// retry_until(..., accepted_status=[...]) call whose result was then
// re-asserted as membership in that very same set -- a tautology,
// identical in shape to e2e/suites/domain's:
//
//   - extensions/test_wasm.py: `assert resp.headers.get("x-wasm-custom")
//     == "FOO" or resp.status_code in (200, 404)` -- the trailing `or`
//     makes the header check provably unnecessary; this port asserts the
//     header alone, with no escape hatch.
//   - external_backends/test_fqdn.py and test_ip.py: `assert
//     resp.status_code in (200, 404)`, fixed to a genuine 200 PLUS proof
//     the response body actually originated from the addressed backend
//     (see external_backend_fqdn_test.go and external_backend_ip_test.go).
//   - route_matching/test_headers.py:test_header_match_hit and
//     test_query_parameters.py:test_query_param_match: `assert
//     resp.status_code in (200, 404)`, fixed to a genuine 200 (miss cases
//     already asserted a real 404 in the Python source and are ported
//     unchanged).
//
// A sixth, not called out by task-15-brief's table but the same shape
// under the "central rule" (never transcribe a tautology):
// route_matching/test_method.py:test_method_match_post asserted `status_code
// in (200, 404, 405)` -- three co-accepted outcomes with no way to tell
// which occurred. Fixed to a genuine 200 here too (a POST to nginx's "/"
// succeeds under nginx's default config, which does not restrict HTTP
// methods).
//
// extensions/test_lua.py and rate_limiting's test_basic/test_header_based/
// test_per_ip/test_capabilities/test_validation were already real
// assertions (`assert resp.headers.get(...) == "FOO"`, `assert
// got_429`, `assert "rateLimitAvailable" in caps`,
// pytest.raises(httpx.HTTPStatusError)) and are ported with no assertion
// change.
//
// # No domain mutation, no isolation concerns
//
// Unlike e2e/suites/domain, nothing in this package touches domain-level
// (Gateway-listener-scoped) settings -- every test here configures a
// per-ROUTE BackendTrafficPolicy or EnvoyExtensionPolicy, or a route's own
// backend definition, all scoped to that route's own unique path. Every
// test in this package therefore calls t.Parallel(), exactly like
// e2e/suites/httproute and grpcroute.
//
// This package does not scale the shared podinfo or nginx Deployments.
//
// # Shared cluster backends (namespace "default")
//
//   - nginx-service:80 -- a stock nginx, always serves the same static
//     "Welcome to nginx!" page at "/" and 404s anything else. Used by
//     every test in this package except the two external_backends tests,
//     which address nginx-service directly by FQDN/IP instead of via the
//     Kubernetes Service abstraction.
package traffic

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// env is the shared harness environment: authenticated API clients, the
// gateway data-plane client, and the Kubernetes client. Built once in
// TestMain and reused by every test in this package (mirrors
// e2e/harness/fixture.go's documented TestMain pattern, same as
// e2e/suites/httproute, grpcroute, security, and domain).
var env *harness.Env

const (
	// backendNamespace is where the shared test backend (nginx-service)
	// and the FastGateway-managed HTTPRoute/policy objects for the seeded
	// "default-public" domain all live.
	backendNamespace = "default"

	nginxService = "nginx-service"
	nginxPort    = 80
	// nginxLabel selects the nginx pod(s) for the Kubernetes API (see
	// e2e/deps/nginx.yaml: matchLabels run=nginx) -- used only by
	// external_backend_ip_test.go to resolve the pod's IP.
	nginxLabel = "run=nginx"

	// routeLiveTimeout bounds how long a test waits for a freshly deployed
	// route to actually be served (any non-404 status) by the gateway.
	routeLiveTimeout = 90 * time.Second
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	env, err = harness.NewEnv(ctx)
	if err != nil {
		log.Fatalf("traffic e2e: build harness env: %v", err)
	}
	os.Exit(m.Run())
}

// teamID returns the seeded "dev" team ID as a uuid.UUID. NewEnv already
// resolved it as a string, so a parse failure here would indicate a
// harness bug rather than a test-time condition.
func teamID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(env.TeamID)
	if err != nil {
		t.Fatalf("parse team ID %q: %v", env.TeamID, err)
	}
	return id
}

// uniquePath returns a route name (via harness.UniqueName) and the
// "/"-prefixed gateway path derived from it, so a test's traffic is
// unambiguous even when many tests run in parallel against the same
// shared Gateway/domain. Mirrors e2e/suites/httproute, security, and
// domain's helper of the same name.
func uniquePath(t *testing.T) (name, path string) {
	t.Helper()
	name = harness.UniqueName(t)
	return name, "/" + name
}

// rewriteTo builds a urlRewrite filter that replaces the route's matched
// prefix with backendPath before forwarding to nginx/podinfo, which only
// ever serve fixed literal paths. Mirrors e2e/suites/httproute, security,
// and domain's helper of the same name.
func rewriteTo(backendPath string) *models.URLRewrite {
	return &models.URLRewrite{
		Path: &models.PathRewrite{
			Type:               "ReplacePrefixMatch",
			ReplacePrefixMatch: backendPath,
		},
	}
}

// expectCreateRejected asserts that creating cfg as a route fails with a
// 400 or 422 status. Mirrors e2e/suites/grpcroute's identical helper
// (main_test.go), used here by rate_limit_validation_test.go to port
// rate_limiting/test_validation.py's three pytest.raises(httpx.
// HTTPStatusError) negative cases.
func expectCreateRejected(t *testing.T, cfg any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := env.Editor.CreateRoute(ctx, env.ProjectID, env.DomainID, cfg)
	if err == nil {
		t.Fatalf("create route succeeded, want rejection (400 or 422)")
	}
	var statusErr *harness.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("create route error %v is not *harness.StatusError", err)
	}
	if statusErr.StatusCode != http.StatusBadRequest && statusErr.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("create route got status %d, want %d or %d", statusErr.StatusCode, http.StatusBadRequest, http.StatusUnprocessableEntity)
	}
}

// backendRouteConfig builds the common shape shared by every rate-limiting
// and extensions test in this package: a single-backend "backend"-type
// route matched by a unique path prefix, forwarded to nginx-service and
// rewritten to "/" (nginx's only real page).
func backendRouteConfig(t *testing.T) (name, path string, cfg services.CreateRouteInput) {
	t.Helper()
	name, path = uniquePath(t)
	cfg = services.CreateRouteInput{
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
	}
	return name, path, cfg
}

// truncate trims b to at most n bytes for embedding in failure messages
// without flooding test output.
func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

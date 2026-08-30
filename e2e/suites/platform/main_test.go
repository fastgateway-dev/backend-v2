//go:build e2e

// Package platform ports the Python regression suite's
// tests/observability/*.py (8 tests), tests/topology/*.py (6 tests), and
// tests/import/*.py (6 tests) -- 20 tests total (task-16) -- to Go, and adds
// NEW coverage (task-17) for four areas that had zero e2e tests before this
// package existed: approval enforcement, route versioning/rollback, audit
// logging, and API tokens.
//
// # Task 16: porting conventions
//
// Observability asserts on the EnvoyProxy Kubernetes resource directly via
// harness.Kube.GetUnstructured -- never by shelling out to kubectl, unlike
// the Python originals (regression/tests/observability/*.py's
// _kubectl_get helper). The two tests that hardcode the Envoy Gateway
// namespace read it from services.EnvoyGatewayNamespace (the backend's own
// constant, internal/services/kubernetes_service.go) instead of a second
// copy of the literal "envoy-gateway-system" -- there is no harness.Config
// field for it (unlike GatewayDomain/Namespace), since it is not
// operator-configurable: the backend itself hardcodes where Envoy Gateway
// expects EnvoyProxy CRDs.
//
// The route-metrics and domain-metrics tests (observability_metrics_*.go)
// PATCH the project's metrics endpoint config, which is project-wide shared
// mutable state (internal/models/project.go's MetricsEndpointURL/
// MetricsAuthType). Every test that touches it captures the project's
// current values first and restores them in t.Cleanup, and none of them
// call t.Parallel() -- Go runs every non-parallel top-level test in a
// package strictly one-at-a-time, and only starts any t.Parallel() test's
// body once every non-parallel test has completed, so omitting
// t.Parallel() from just these tests is sufficient to serialize them
// against each other AND against every other (parallel) test in this
// package without a package-wide lock. See observability_metrics_domain_test.go,
// observability_metrics_route_test.go, and
// observability_metrics_test_connection_test.go.
//
// The metrics tests require a mock Prometheus reachable at MOCK_PROM_URL
// (harness.Config.MockPromURL). Unlike jwt-server/external-auth/
// grpc-external-auth (all provisioned by e2e/deps/*.yaml, committed to this
// repo), no mock-prometheus fixture exists anywhere under e2e/deps -- the
// Python predecessor's MOCK_PROM_URL default ("http://mock-prometheus:9090")
// implies it was provisioned by external CI infrastructure not present in
// this repository. Rather than guess at an unverifiable in-cluster default
// (this task has no cluster to check against), these tests skip cleanly via
// t.Skip when MOCK_PROM_URL is unset.
//
// # Task 17: new coverage
//
// approvals_test.go, versioning_test.go, audit_test.go, and
// apitokens_test.go have no Python source at all -- see each file's own doc
// comment for what it proves and why. Per the task brief, approvals come
// first: TestApprovalEditorCannotApproveOwnRoute,
// TestApprovalUnapprovedRouteNotServed, and
// TestApprovalRejectedRouteNotDeployedOrServed close the most serious
// coverage gap in the product (zero prior tests of the core
// submitter-cannot-approve-their-own-change guarantee).
//
// # Shared conventions with the five already-ported suites
//
// This package follows e2e/suites/{httproute,grpcroute,security,domain,
// traffic}'s established conventions: harness.NewEnv built once in
// TestMain, harness.UniqueName for every created resource,
// harness.WaitForRouteLive for data-plane readiness (never a fixed sleep),
// t.Parallel() wherever a test's state is genuinely independent, and a
// local waitForHTTPStatus/requireStatus pair (mirroring
// e2e/suites/security and e2e/suites/domain's identical helpers) for tests
// that need a POSITIVE probe proven live before a NEGATIVE probe's result
// can be trusted -- most directly relevant here to the two approval tests
// that assert a route is genuinely NOT served (see their own doc comments
// for how a bare 404 is made unambiguous).
//
// This package never scales the shared podinfo or nginx Deployments
// (namespace "default").
package platform

import (
	"context"
	"fmt"
	"log"
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
// e2e/harness/fixture.go's documented TestMain pattern, same as every
// already-ported suite).
var env *harness.Env

const (
	// backendNamespace is where the shared test backend (nginx-service)
	// and the FastGateway-managed HTTPRoute/policy objects for the seeded
	// "default-public" domain all live.
	backendNamespace = "default"

	nginxService = "nginx-service"
	nginxPort    = 80

	// routeLiveTimeout bounds how long a test waits for a freshly deployed
	// route to actually be served (any non-404 status) by the gateway.
	routeLiveTimeout = 90 * time.Second
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	env, err = harness.NewEnv(ctx)
	if err != nil {
		log.Fatalf("platform e2e: build harness env: %v", err)
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
// shared Gateway/domain. Mirrors every already-ported suite's helper of
// the same name.
func uniquePath(t *testing.T) (name, path string) {
	t.Helper()
	name = harness.UniqueName(t)
	return name, "/" + name
}

// rewriteTo builds a urlRewrite filter that replaces the route's matched
// prefix with backendPath before forwarding to nginx-service, which only
// ever serves "/". Mirrors every already-ported suite's helper of the same
// name.
func rewriteTo(backendPath string) *models.URLRewrite {
	return &models.URLRewrite{
		Path: &models.PathRewrite{
			Type:               "ReplacePrefixMatch",
			ReplacePrefixMatch: backendPath,
		},
	}
}

// simpleRouteConfig builds a minimal single-backend "backend"-type route
// (matched by a unique path prefix, forwarded to nginx-service and
// rewritten to "/") for tests whose focus is elsewhere (versioning,
// approvals, topology, ...) and that just need SOME real, deployable
// route. Mirrors e2e/suites/traffic's backendRouteConfig.
func simpleRouteConfig(t *testing.T) (name, path string, cfg services.CreateRouteInput) {
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

// versionedRouteConfig is simpleRouteConfig plus a ResponseHeaderModifier
// setting "X-E2E-Version" to headerValue -- a deterministic, easy-to-probe
// way to tell two route configs (or a route's config before/after an
// update or rollback) apart both in the stored config (RouteVersion.
// ConfigSnapshot) and in what the gateway actually serves. Used by
// versioning_test.go.
func versionedRouteConfig(t *testing.T, headerValue string) (name, path string, cfg services.CreateRouteInput) {
	t.Helper()
	name, path, cfg = simpleRouteConfig(t)
	cfg.Config.ResponseHeaderModifier = &models.HeaderModifier{
		Set: []models.HeaderValue{{Name: "X-E2E-Version", Value: headerValue}},
	}
	return name, path, cfg
}

// waitForHTTPStatus polls probe (2s-interval loop) until it returns a
// response whose status is exactly one of want, or returns an error once
// timeout elapses. Unlike harness.WaitForRouteLive (which returns as soon
// as it sees ANY non-404 status), this keeps polling past any status that
// isn't actually wanted -- including a transient wrong one produced
// mid-rollout. This is the POSITIVE half of every proof in this package
// that a route (or a sibling route establishing the gateway is alive at
// all) behaves as expected before a negative/absence assertion is trusted.
// Mirrors e2e/suites/security and e2e/suites/domain's identical helper.
func waitForHTTPStatus(
	ctx context.Context,
	probe func(context.Context) (*harness.Response, error),
	timeout time.Duration,
	want ...int,
) (*harness.Response, error) {
	isWant := func(code int) bool {
		for _, w := range want {
			if code == w {
				return true
			}
		}
		return false
	}

	deadline := time.Now().Add(timeout)
	var last *harness.Response
	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := probe(ctx)
		if err != nil {
			lastErr = err
		} else {
			last = resp
			if isWant(resp.StatusCode) {
				return resp, nil
			}
			lastErr = fmt.Errorf("got status %d, want one of %v (body: %s)", resp.StatusCode, want, truncate(resp.Body, 300))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last != nil {
		return last, fmt.Errorf("status did not settle to any of %v within %s: %w", want, timeout, lastErr)
	}
	return nil, fmt.Errorf("route did not become live within %s: %w", timeout, lastErr)
}

// requireStatus issues probe once (retrying only on transport-level
// errors, up to 3 attempts) and fails t immediately if the response status
// is not one of want. Call this ONLY after the relevant positive path has
// already been proven live via waitForHTTPStatus -- see that function's
// doc comment and e2e/suites/security's identical helper for the full
// rationale.
func requireStatus(t *testing.T, ctx context.Context, probe func(context.Context) (*harness.Response, error), want ...int) *harness.Response {
	t.Helper()

	var resp *harness.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = probe(ctx)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("request: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		t.Fatalf("request failed after retries: %v", err)
	}
	for _, w := range want {
		if resp.StatusCode == w {
			return resp
		}
	}
	t.Fatalf("got status %d, want one of %v (body: %s)", resp.StatusCode, want, truncate(resp.Body, 500))
	return nil
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

//go:build e2e

// Package domain ports the Python regression suite's
// tests/domain_settings/*.py (11 tests) and tests/backend_tls/*.py (2
// tests) -- 13 tests total -- to Go (task-14). These exercise domain-level
// (Envoy Gateway ClientTrafficPolicy) settings -- mutual TLS in
// strict/optional/multiple-CA modes, client IP detection via
// X-Forwarded-For, and TCP keepalive -- plus per-backend TLS/mTLS
// (BackendTLSPolicy) on individual routes.
//
// # Central rule: eleven formerly-tautological assertions fixed
//
// Every one of the 7 domain_settings tests whose Python original ended
// with
//
//	resp = retry_until(fn, accepted_status=[200, 404])
//	assert resp.status_code in (200, 404)
//
// (a tautology: retry_until already guarantees that membership before the
// assertion even runs) is ported here with a genuine assertion instead --
// almost always a real 200, proven by creating an actual fixture route
// (nginx or podinfo, rewritten so the route's unique test path resolves to
// something the backend actually serves). The domain_settings Python
// source, uniquely among every already-ported suite, issued NO route
// config at all and probed bare "/" on the shared domain -- which is
// exactly why "200 or 404" was the only outcome it could ever assert. See
// each file's own doc comment for the precise real assertion. The other 4
// domain_settings tests (mtls_strict_rejects_no_cert,
// mtls_strict_rejects_wrong_cert, mtls_optional_rejects_wrong_cert,
// mtls_multiple_ca_rejects_no_cert) already used pytest.raises in the
// Python source -- a real, non-tautological assertion -- and are ported
// using requireTLSFailure below, the same technique
// e2e/suites/security uses for its own mTLS negative case.
//
// The 2 backend_tls tests were never tautological (their Python originals
// call retry_until with accepted_status=[200] only, so "assert
// status_code == 200" was already a real assertion) and are ported
// faithfully with no assertion change.
//
// # Isolation: full package serialization, no t.Parallel() anywhere
//
// Domain mTLS, client IP detection, and TCP keepalive are all
// Gateway-listener-scoped (Envoy Gateway's ClientTrafficPolicy) -- they
// apply to the shared "api.fastgateway.local" domain as a whole, not to
// any one route. task-14-brief offers two isolation strategies: serialize
// this package, or give it a dedicated domain created in TestMain. This
// port chooses full serialization -- no test in this package calls
// t.Parallel(), so `go test` runs every one of them strictly one after
// another in declaration order, which is the default behavior for any Go
// test package where nothing opts into parallelism -- for two reasons:
//
//  1. Unlike e2e/suites/security's TestClientModeMTLS (the only existing
//     precedent for mutating this same shared domain), which deliberately
//     stays in "optional" mode specifically so concurrent sibling traffic
//     is never rejected, this package's whole point is to exercise
//     genuine STRICT mode (mtls_strict's 3 tests, and 2 of
//     mtls_multiple_ca's 3). Strict domain mTLS fails the TLS handshake
//     itself for ANY concurrent request that lacks a trusted client
//     certificate -- there is no safe subset of this package's own other
//     tests that could run alongside it without spuriously failing.
//     Serialization is not merely a safety margin here; it is a
//     precondition for the feature under test to be exercisable at all.
//  2. A dedicated domain (created fresh in TestMain) was considered and
//     rejected as the riskier option given this task has no cluster to
//     verify against (see the top-level task instructions). cmd/e2e-seed
//     shows that creating a Domain requires a DomainTemplateID whose
//     ExposureType is "LoadBalancer" -- i.e. every domain provisions its
//     own Gateway/Service, plausibly its own external IP -- and a
//     TLSSecretName that must already reference a valid, existing K8s
//     Secret for that domain's own hostname. e2e/deps/create-secrets.sh
//     only provisions ONE such secret ("domain-tls") for the ONE hostname
//     harness.Config.GatewayDomain already expects; harness.Gateway also
//     dials cfg.GatewayIP directly with cfg.GatewayDomain forced as both
//     Host header and TLS SNI, so a second domain would need either a
//     second real IP/TLS-secret pair (not provisioned anywhere in e2e/deps,
//     and out of scope for this task to add) or reusing the existing
//     secret under a second hostname trusted only via a client-side SNI
//     override -- an approach with no precedent anywhere in this codebase
//     and no cluster available here to confirm it actually reconciles.
//     Package-level serialization uses only already-proven harness/API
//     surface and fails loudly (a hung/timed-out test, never a silently
//     wrong pass) if this assumption is ever violated by a future test
//     being added with t.Parallel().
//
// CI runs e2e packages serially (`-p 1`, per the top-level instructions),
// so this package's own single-package serialization is the only ordering
// guarantee that matters here; it does not need to coordinate with
// e2e/suites/httproute, grpcroute, or security.
//
// # Shared cluster backends (namespace "default")
//
//   - nginx-service:80 -- a stock nginx, always serves the same static
//     "Welcome to nginx!" page at "/" and 404s anything else. Used by every
//     test in this package except client_ip_detection.
//   - podinfo:9898 -- used only by client_ip_detection, for its
//     header-echoing "/headers" endpoint.
//
// This package never scales the shared podinfo or nginx Deployments.
package domain

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// env is the shared harness environment: authenticated API clients, the
// gateway data-plane client, and the Kubernetes client. Built once in
// TestMain and reused by every test in this package (mirrors
// e2e/harness/fixture.go's documented TestMain pattern, same as
// e2e/suites/httproute, grpcroute, and security).
var env *harness.Env

const (
	// backendNamespace is where the shared test backends (nginx-service,
	// podinfo) and the FastGateway-managed HTTPRoute/policy objects for
	// the seeded "default-public" domain all live.
	backendNamespace = "default"

	nginxService = "nginx-service"
	nginxPort    = 80

	podinfoService = "podinfo"
	podinfoPort    = 9898

	// routeLiveTimeout bounds how long a test waits for a freshly deployed
	// route (and, where relevant, domain-level settings) to actually be
	// served by the gateway with its true, converged behavior.
	routeLiveTimeout = 90 * time.Second
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	env, err = harness.NewEnv(ctx)
	if err != nil {
		log.Fatalf("domain e2e: build harness env: %v", err)
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
// unambiguous. Mirrors e2e/suites/httproute and security's helper of the
// same name.
func uniquePath(t *testing.T) (name, path string) {
	t.Helper()
	name = harness.UniqueName(t)
	return name, "/" + name
}

// rewriteTo builds a urlRewrite filter that replaces the route's matched
// prefix with backendPath before forwarding to nginx/podinfo. Mirrors
// e2e/suites/httproute and security's helper of the same name.
func rewriteTo(backendPath string) *models.URLRewrite {
	return &models.URLRewrite{
		Path: &models.PathRewrite{
			Type:               "ReplacePrefixMatch",
			ReplacePrefixMatch: backendPath,
		},
	}
}

// waitForHTTPStatus polls probe (2s-interval loop) until it returns a
// response whose status is exactly one of want, or returns an error once
// timeout elapses. This is the POSITIVE half of every mTLS assertion in
// this package: establishing it first, before any negative probe, proves
// the route AND the domain-level mTLS listener config have both converged
// -- exactly mirroring e2e/suites/security's identical helper and its doc
// comment's rationale.
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
// errors, up to 3 attempts, for resilience against ordinary network
// flakiness -- never on an unexpected status code) and fails t immediately
// if the response status is not one of want. Call this ONLY after the
// route/domain's positive path has already been proven live via
// waitForHTTPStatus: at that point an unexpected status here is a genuine
// regression, never "still warming up". Mirrors e2e/suites/security's
// identical helper.
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

// requireTLSFailure issues probe once and fails t unless it returns a
// non-nil transport-level error -- i.e. the TLS handshake (or the
// connection generally) itself failed, as opposed to completing and
// yielding an ordinary HTTP response. A nil error (regardless of the
// resulting status code, 200 included) fails the test: that would mean the
// connection succeeded despite presenting an untrusted/missing credential,
// which is exactly the enforcement bypass this assertion exists to catch.
// Mirrors e2e/suites/security's identical helper.
func requireTLSFailure(t *testing.T, ctx context.Context, probe func(context.Context) (*harness.Response, error)) {
	t.Helper()
	resp, err := probe(ctx)
	if err == nil {
		t.Fatalf("expected a TLS handshake failure, got a normal HTTP response instead (status %d, nil error) -- enforcement is not rejecting this credential", resp.StatusCode)
	}
	t.Logf("got expected transport-layer failure: %v", err)
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

// certDir resolves to e2e/testdata/certificate regardless of the test
// binary's working directory. Mirrors e2e/suites/security's identical var.
var certDir = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "certificate")
}()

// loadPEM reads a PEM file under certDir (e.g. loadPEM(t, "root-ca-3",
// "client-1.crt")) and fails t if it cannot be read.
func loadPEM(t *testing.T, parts ...string) string {
	t.Helper()
	full := append([]string{certDir}, parts...)
	b, err := os.ReadFile(filepath.Join(full...))
	if err != nil {
		t.Fatalf("read cert fixture %s: %v", filepath.Join(parts...), err)
	}
	return string(b)
}

// int32Ptr returns a pointer to v, for the *int32 config fields in
// TCPKeepaliveConfig.
func int32Ptr(v int32) *int32 { return &v }

// strPtr returns a pointer to s, for the *string duration fields in
// TCPKeepaliveConfig.
func strPtr(s string) *string { return &s }

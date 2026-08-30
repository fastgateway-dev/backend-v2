//go:build e2e

// Package security ports the Python regression suite's
// tests/security_general_mode (8 tests), tests/client_mode (7 tests), and
// tests/external_authorization (4 tests) -- 19 tests total -- to Go. These
// exercise FastGateway's security guarantees: API-key auth, JWT auth,
// mTLS, IP allowlisting, external authorization (ext-auth), WAF blocking,
// and per-client rate limiting.
//
// # Why this package exists (task-13)
//
// In the Python source, every one of the 7 client_mode tests and
// security_general_mode/test_waf.py ended with
//
//	resp = retry_until(fn, accepted_status=[200, 404])
//	assert resp.status_code in (200, 404)
//
// retry_until already raises unless the response status is in
// accepted_status, so by the time that assertion runs its outcome is
// guaranteed -- it can never fail. None of those 8 tests verified that
// API-key auth, JWT auth, mTLS, IP allowlisting, or WAF blocking actually
// enforce anything: a route with authentication completely disabled would
// pass every one of them identically. Every test in this package instead
// asserts BOTH an explicit positive case (valid credential -> 200) and an
// explicit negative case (missing/invalid credential -> denied), with the
// negative half checked as strictly as the positive half.
//
// A subtler trap applies even to the tests that already had a real
// assertion. Most of client_mode's Python originals probed the negative
// case with a BARE unauthenticated request (no x-client-id at all). That
// only proves the route's defaultTrafficPolicy=deny catch-all rejects
// traffic with no client context -- it says nothing about whether the
// specific credential (the API key, the JWT, the cert) is actually
// checked once a client IS identified. A client attachment whose api-key
// check is completely bypassed (accepts anything, or nothing, as long as
// x-client-id names a real client) would still pass a bare-unauthenticated
// negative probe. So wherever the mechanism allows it, this package's
// negative probes carry a valid x-client-id (or otherwise establish a real
// client/route context) and omit or invalidate only the ONE credential
// under test -- see e.g. TestClientModeAPIKey and TestClientModeCombinedAuth.
//
// # Ordering discipline: positive before negative
//
// A freshly deployed route is not immediately live -- Envoy Gateway must
// reconcile the HTTPRoute and its SecurityPolicy/ClientTrafficPolicy and
// push config to the proxy. Before that finishes, the gateway can answer
// 404 (route not programmed) or a transient wrong status (e.g. a
// SecurityPolicy that hasn't converged yet). For HTTP specifically, 404 is
// not distinguishable at the wire level from a route that IS live but
// whose backend legitimately 404s for an unrecognized path (see rewriteTo
// below) -- and worse, retrying a negative probe until it happens to read
// 403 can be fooled by a transient, wrong-reason 403 during rollout,
// exactly mirroring the "passes for the wrong reason" class of bug this
// task exists to fix.
//
// Most tests in this package therefore prove the route (and, where
// relevant, the client attachment) is fully live and behaving correctly
// FIRST, via waitForHTTPStatus on the POSITIVE request -- polling past any
// wrong status, including 404, until the real 200 is observed. Only once
// that is established does it issue the negative probe via requireStatus,
// which does NOT tolerate 404 (or any other unexpected status) as "still
// warming up": at that point a 404 would itself be a genuine regression
// (the route flapping back to unprogrammed), not a normal transient state.
//
// # Exception: when the positive probe can't distinguish enforcement from its absence
//
// The route landing (HTTPRoute programmed) and its SecurityPolicy/
// EnvoyExtensionPolicy landing (deploySecurityPolicy /
// deployEnvoyExtensionPolicy, in internal/services/route_service.go) are
// two separate Kubernetes writes that reconcile independently -- there is
// a real window where the route is live but the policy is not. For most
// mechanisms the positive probe still can't be served correctly during
// that window (e.g. mTLS: no cert presented, no handshake), so gating on
// it first is safe. But for JWT, API-key, and WAF specifically, an
// unconverged route (HTTPRoute live, policy not yet attached) answers the
// POSITIVE probe with the exact same 200 an unauthenticated/benign
// request also gets -- the policy simply isn't there yet to reject
// anything. A 200 on the positive probe is therefore not evidence the
// policy has converged; only the negative probe's denial (401/403, or for
// WAF a blocked payload's 403) can only be produced once the policy is
// genuinely attached and enforcing. TestGeneralModeJWT,
// TestGeneralModeAPIKeyDenied, TestClientModeAPIKey, and
// TestGeneralModeWAFBlocksSQLInjection therefore invert the order: they
// gate via waitForHTTPStatus on the probe that can only succeed once
// truly converged (the denial, or for WAF the block), then assert the
// other side with requireStatus. See each test's own doc comment.
//
// # Known limitation: security_general_mode/test_api_key.go
//
// General-mode apiKeyAuth's credential source is a Kubernetes Secret in
// Envoy Gateway's own APIKeyAuth CRD format (referenced only by name --
// see services.APIKeyAuthInput). No fixture anywhere in e2e/deps (see
// e2e/deps/create-secrets.sh) seeds one with a valid key, and the exact
// Secret schema Envoy Gateway expects is not exercised anywhere else in
// this repository, so it cannot be reliably synthesized from within the
// test without a cluster to verify against. This exact gap was already
// hit and documented by the already-ported grpcroute suite (see
// e2e/suites/grpcroute/security_api_key_test.go's "KNOWN LIMITATION"
// comment) for the identical general-mode mechanism -- this port carries
// the same, honest limitation forward rather than guessing at an
// unverifiable Secret format. See TestGeneralModeAPIKeyDenied.
//
// # mTLS negative case: transport-layer failure, not just HTTP 403
//
// TestClientModeMTLS asserts a genuine TLS-handshake-level failure (a
// non-nil transport error, never a 200) for a certificate signed by an
// untrusted CA, in addition to the app-layer 403 the Python source already
// asserted for a request with no certificate at all. See that test's doc
// comment for why domain mTLS must stay in "optional" mode (shared-domain
// safety) and how presenting an untrusted-CA certificate under "optional"
// mode still produces a real transport-layer rejection.
//
// # Shared cluster backends (namespace "default")
//
//   - nginx-service:80 -- a stock nginx, always serves the same static
//     "Welcome to nginx!" page at "/" and 404s anything else. Every test
//     here uses it (none of the 19 source tests reference podinfo), so
//     this package never touches the shared podinfo Deployment.
//   - jwt-server:9000 -- issues and validates JWTs for both general-mode
//     and client-mode JWT tests; see jwtServerURL and generateJWTToken.
//   - external-auth:9001 -- HTTP ext-auth backend; allows only when the
//     (forwarded) "x-ext-auth-allow" header equals "true".
//   - grpc-external-auth:9003 -- gRPC ext-auth backend with the same
//     allow/deny contract, reached from an HTTP-protocol route.
//
// This package does not scale or otherwise mutate the shared podinfo or
// nginx Deployments (see task-13's explicit instruction on this point).
// TestClientModeMTLS does mutate domain-level settings (mTLS on the
// shared "api.fastgateway.local" domain) -- exactly as the Python source
// it ports already did -- but only ever into "optional" mode (never
// "strict"), so concurrent unauthenticated traffic from sibling tests is
// never rejected by it, and every mutation is restored via t.Cleanup. See
// that test's own doc comment for the full risk analysis.
package security

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
// e2e/suites/httproute and e2e/suites/grpcroute).
var env *harness.Env

const (
	// backendNamespace is where the shared test backends (nginx-service,
	// jwt-server, external-auth, grpc-external-auth) and the
	// FastGateway-managed HTTPRoute/policy objects for the seeded
	// "default-public" domain all live.
	backendNamespace = "default"

	nginxService = "nginx-service"
	nginxPort    = 80

	externalAuthService = "external-auth"
	externalAuthPort    = 9001

	grpcExternalAuthService = "grpc-external-auth"
	grpcExternalAuthPort    = 9003

	// routeLiveTimeout bounds how long a test waits for a freshly deployed
	// route (and, where relevant, client attachment) to actually be
	// served by the gateway with its true, converged behavior.
	//
	// 180s, not 90s: a second real CI run measured actual route+policy
	// convergence latency at 76-90s against the old 90s budget -- no
	// margin at all, and enough to flake outright when reconciliation ran
	// even slightly long. Don't trim this back without new measurements.
	routeLiveTimeout = 180 * time.Second

	// defaultJWTMintURL is where the TEST PROCESS (running on the CI
	// runner, not inside the cluster) mints JWTs from by default, when
	// JWT_SERVER_URL is unset -- a CI port-forward exposes jwt-server at
	// this runner-local address. See jwtServerURL.
	defaultJWTMintURL = "http://localhost:9000"

	// defaultJWTIssuerURL mirrors regression/config.py's JWT_SERVER_URL
	// default and e2e/suites/grpcroute/main_test.go's own constant: the
	// in-cluster FQDN, reachable from the envoy-gateway-system namespace
	// where the Envoy proxy pods run. A short name like "jwt-server:9000"
	// (or defaultJWTMintURL) does NOT resolve there -- see the package doc
	// comment's "wrong reason" discussion and TestGeneralModeJWT. This is
	// also the exact value jwt-server stamps as "iss" into every minted
	// token (JWT_SERVER_HOST in e2e/deps/jwt-server.yaml), so it must be
	// used verbatim as a SecurityPolicy's issuer/JWKS URL. See
	// jwtIssuerURL.
	defaultJWTIssuerURL = "http://jwt-server.default.svc.cluster.local:9000"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	env, err = harness.NewEnv(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "security e2e: build harness env: %v\n", err)
		os.Exit(1)
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
// shared Gateway/domain. Mirrors e2e/suites/httproute/main_test.go's
// helper of the same name.
func uniquePath(t *testing.T) (name, path string) {
	t.Helper()
	name = harness.UniqueName(t)
	return name, "/" + name
}

// rewriteTo builds a urlRewrite filter that replaces the route's matched
// prefix with backendPath before forwarding to nginx-service, which only
// ever serves "/". Without this, an authenticated-but-otherwise-plain
// request to a random unique path gets nginx's OWN legitimate 404, which
// is indistinguishable from "route not programmed yet" -- exactly the
// ambiguity the package doc comment describes. Every test whose positive
// case needs an unambiguous 200 uses it.
func rewriteTo(backendPath string) *models.URLRewrite {
	return &models.URLRewrite{
		Path: &models.PathRewrite{
			Type:               "ReplacePrefixMatch",
			ReplacePrefixMatch: backendPath,
		},
	}
}

// jwtServerURL returns the URL the TEST PROCESS itself uses to mint JWTs
// (POSTing to jwt-server's /token endpoint) -- this must be reachable
// from wherever the test binary runs (the CI runner), NOT from inside
// the cluster. It resolves env.Cfg.JWTServerURL (the JWT_SERVER_URL env
// var), falling back to defaultJWTMintURL (harness.Config's own default
// is empty, since not every e2e suite needs a JWT server).
//
// This is deliberately a different URL from jwtIssuerURL: see that
// function's doc comment for why the issuer/JWKS URL handed to Envoy
// must stay the in-cluster FQDN even though token minting happens from
// the runner.
func jwtServerURL() string {
	if env.Cfg.JWTServerURL != "" {
		return env.Cfg.JWTServerURL
	}
	return defaultJWTMintURL
}

// jwtIssuerURL returns the in-cluster FQDN that must be handed to Envoy
// as a SecurityPolicy's JWT issuer/JWKS URL. Envoy's own pods (running in
// envoy-gateway-system) resolve this address; a runner-local address like
// jwtServerURL()'s default would NOT resolve there. It is also the exact
// value jwt-server stamps as "iss" into every token it mints (see
// JWT_SERVER_HOST in e2e/deps/jwt-server.yaml), so the SecurityPolicy's
// issuer must match it exactly regardless of where the test process
// itself reaches jwt-server to mint tokens. Always the in-cluster
// address -- unlike jwtServerURL, it does not consult JWT_SERVER_URL.
func jwtIssuerURL() string {
	return defaultJWTIssuerURL
}

// generateJWTToken mirrors regression/helpers/api.py:generate_jwt_token --
// POSTs {"aud": audience} to the test jwt-server's /token endpoint and
// returns the signed token from its {"token": "..."} response. The
// server stamps its own issuer (JWT_SERVER_HOST) into every token it
// mints, which is exactly jwtServerURL() -- see the package doc comment.
func generateJWTToken(ctx context.Context, audience string) (string, error) {
	body, err := json.Marshal(map[string]string{"aud": audience})
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(jwtServerURL(), "/") + "/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build jwt-server request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("POST %s: status %d", url, resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode jwt-server response: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("jwt-server response had no token")
	}
	return out.Token, nil
}

// waitForHTTPStatus polls probe (same 2s-interval loop as
// harness.WaitForRouteLive) until it returns a response whose status is
// exactly one of want, or returns an error once timeout elapses. Unlike
// harness.WaitForRouteLive -- which returns as soon as it sees ANY
// non-404 status, on the assumption that the response is already final --
// this keeps polling past any status that isn't actually wanted,
// including a transient wrong one produced mid-rollout (e.g. a
// SecurityPolicy or ClientTrafficPolicy that hasn't finished converging).
//
// This is the POSITIVE half of every assertion in this package: see the
// package doc comment for why establishing it first, before any negative
// probe, is what makes the negative probe trustworthy.
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
// if the response status is not one of want.
//
// Call this ONLY after the route's positive path has already been proven
// live via waitForHTTPStatus. At that point a 404 (or any other
// unexpected status) here is not "still warming up" -- it is a genuine
// regression -- so it must never be silently retried away, and it must
// never be treated as equivalent to a real 401/403 denial. Probing
// straight for a denial code without first proving the route is live
// cannot distinguish "correctly denied" from "spuriously denied because
// the policy hasn't converged yet", which is exactly the class of bug
// task-13 exists to catch.
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
// resulting status code, 200 included) fails the test: that would mean
// the connection succeeded despite presenting an untrusted credential,
// which is exactly the enforcement bypass this assertion exists to catch.
//
// The error's exact text is deliberately not pattern-matched: Go's TLS
// stack surfaces certificate-verification failures with different wording
// across versions/platforms (e.g. "remote error: tls: bad certificate",
// "x509: certificate signed by unknown authority"), and asserting on
// substrings would make this brittle for no real safety gain -- the
// security-relevant fact is "no successful response was produced", which
// err != nil already establishes.
func requireTLSFailure(t *testing.T, ctx context.Context, probe func(context.Context) (*harness.Response, error)) {
	t.Helper()
	resp, err := probe(ctx)
	if err == nil {
		t.Fatalf("expected a TLS handshake failure (untrusted client certificate), got a normal HTTP response instead (status %d, nil error) -- enforcement is not rejecting this credential", resp.StatusCode)
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
// binary's working directory, by anchoring off this source file's own
// path (Go's `go test` sets the working directory to the package
// directory by convention, but runtime.Caller is more robust and
// self-documenting than relying on that).
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

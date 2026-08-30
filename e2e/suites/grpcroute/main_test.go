//go:build e2e

// Package grpcroute ports the Python regression suite's
// tests/grpc_btp_features, tests/grpc_validation, tests/grpc_route_matching,
// tests/grpc_security, tests/grpc_client_mode, tests/grpc_extensions, and
// tests/grpc_route_features (40 tests total) to Go. It exercises gRPC routes
// (matching, backend traffic policies, security, client-mode auth,
// extensions, and route features) against a live FastGateway + Envoy
// Gateway deployment, using the generated protobuf stubs in
// e2e/testdata/pb instead of hand-encoded bytes or a shelled-out grpcurl.
//
// Shared cluster backends (see e2e/deps/podinfo.yaml and e2e/deps/nginx.yaml,
// namespace "default"):
//   - podinfo:9999 (grpc)  -- podinfo's real gRPC server, implementing
//     echo.EchoService, delay.DelayService, env.EnvService, header.HeaderService,
//     info.InfoService, token.TokenService, and version.VersionService (see
//     e2e/testdata/protos and the `protos` Makefile target). Every RPC on it
//     succeeds normally -- there is no gRPC analogue of podinfo's HTTP
//     /status/{code} debug endpoint, so a handful of ports below (retry,
//     health check, load balancing) are necessarily lighter-weight smoke
//     tests rather than fault-driven ones; see their doc comments.
//   - nginx-service:80     -- a stock nginx, used only as a non-gRPC mirror/
//     failover target (it never receives primary gRPC traffic).
//
// Traffic isolation under t.Parallel(): unlike HTTP's PathPrefix match,
// Gateway API's GRPCRouteMatch has no per-request routing key that a test
// can make unique on its own -- most tests here bind the SAME literal
// grpcService ("echo.EchoService" and friends) that every other parallel
// test in this package also binds. Without an extra, mutually exclusive
// match condition, Envoy would have to arbitrarily pick ONE winning
// GRPCRoute among many equally eligible ones, and every other test's
// traffic and policy assertions would be undefined. Every test that sends
// real traffic therefore adds a discriminating header (see uniqueMatch
// below) to its route's match and to every call it makes -- the gRPC
// equivalent of HTTP's uniquePath.
//
// This package does not scale or otherwise mutate the shared "podinfo"
// Deployment (no equivalent of httproute's podinfoMu-guarded
// scale-to-0/scale-to-3 tests): httproute's podinfoMu only serializes
// tests within httproute's OWN test binary, and `go test -tags e2e
// ./e2e/...` runs different packages' test binaries concurrently with no
// cross-package coordination available. Mutating shared replica state from
// here could silently corrupt httproute's own health-check/load-balancing
// assertions (or vice versa) with no way to detect it. See
// btp_health_check_active_test.go, btp_health_check_passive_test.go, and
// btp_load_balancing_test.go for the resulting (deliberately lighter)
// verification and task-12-report.md for the full rationale.
package grpcroute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/e2e/testdata/pb/echo"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// env is the shared harness environment: authenticated API clients, the
// gateway data-plane client, and the Kubernetes client. Built once in
// TestMain and reused by every test in this package (mirrors
// e2e/harness/fixture.go's documented TestMain pattern, same as
// e2e/suites/httproute/main_test.go).
var env *harness.Env

const (
	// backendNamespace is where the shared test backends (podinfo,
	// nginx-service, grpc-external-auth, jwt-server) and the
	// FastGateway-managed GRPCRoute/policy objects for the seeded
	// "default-public" domain all live.
	backendNamespace = "default"

	podinfoService  = "podinfo"
	podinfoGRPCPort = 9999

	nginxService = "nginx-service"
	nginxPort    = 80

	grpcExternalAuthService = "grpc-external-auth"
	grpcExternalAuthPort    = 9003

	// routeLiveTimeout bounds how long a test waits for a freshly deployed
	// GRPCRoute to actually be served (a "settled" gRPC status, see
	// waitForGRPCLive) by the gateway.
	//
	// 180s, not 90s: a second real CI run measured actual route+policy
	// convergence latency at 76-90s against the old 90s budget -- no
	// margin at all, and enough to flake outright when reconciliation ran
	// even slightly long. Don't trim this back without new measurements.
	routeLiveTimeout = 180 * time.Second

	// discriminatorHeader is the extra match condition every gRPC test
	// route requires and every gRPC call in this package sends, keyed to a
	// value unique to the test. See the package doc comment.
	discriminatorHeader = "x-e2e-route"

	// defaultJWTMintURL is where the TEST PROCESS (running on the CI
	// runner, not inside the cluster) mints JWTs from by default, when
	// JWT_SERVER_URL is unset -- a CI port-forward exposes jwt-server at
	// this runner-local address. See jwtServerURL.
	defaultJWTMintURL = "http://localhost:9000"

	// defaultJWTIssuerURL mirrors regression/config.py's JWT_SERVER_URL
	// default (harness.Config's own default is "", since not every suite
	// needs it -- see e2e/harness/config.go): the in-cluster FQDN,
	// reachable from the envoy-gateway-system namespace where the Envoy
	// proxy pods run. A short name like "jwt-server:9000" (or
	// defaultJWTMintURL) does NOT resolve there. This is also the exact
	// value jwt-server stamps as "iss" into every minted token
	// (JWT_SERVER_HOST in e2e/deps/jwt-server.yaml), so it must be used
	// verbatim as a SecurityPolicy's issuer/JWKS URL. See jwtIssuerURL.
	defaultJWTIssuerURL = "http://jwt-server.default.svc.cluster.local:9000"

	echoServiceName = "echo.EchoService"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	env, err = harness.NewEnv(ctx)
	if err != nil {
		log.Fatalf("grpcroute e2e: build harness env: %v", err)
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

// jwtServerURL returns the URL the TEST PROCESS itself uses to mint JWTs
// (POSTing to jwt-server's /token endpoint) -- this must be reachable
// from wherever the test binary runs (the CI runner), NOT from inside
// the cluster. It resolves env.Cfg.JWTServerURL (the JWT_SERVER_URL env
// var), falling back to defaultJWTMintURL, since harness.Config's own
// default is empty (not every e2e suite needs a JWT server).
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
// returns the signed token from its {"token": "..."} response.
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

// grpcNotReady reports whether code is one Envoy plausibly returns before a
// freshly deployed GRPCRoute has been reconciled ("no route programmed
// yet"), as opposed to a legitimate policy-driven outcome (PermissionDenied,
// Unauthenticated, ResourceExhausted, DeadlineExceeded, Aborted, OK, ...).
// gRPC-over-HTTP/2 has no single dedicated "route not found" status; Envoy
// maps its own local replies for an unmatched request through the standard
// HTTP-to-gRPC status table, which can surface as Unimplemented or NotFound,
// and "no healthy upstream yet" surfaces as Unavailable -- all three are
// treated as "keep polling" here, mirroring harness.WaitForRouteLive's
// "any 404 means not programmed yet" rule for HTTP.
func grpcNotReady(code codes.Code) bool {
	switch code {
	case codes.Unimplemented, codes.NotFound, codes.Unavailable:
		return true
	default:
		return false
	}
}

// waitForGRPCLive polls call until it returns a code that isn't one of
// grpcNotReady's "route not programmed yet" signals, or timeout elapses.
// This is the gRPC counterpart to harness.WaitForRouteLive (which is typed
// to *harness.Response and so can't be reused directly here), and replaces
// the old grpc_security/grpc_extensions Python suites' fixed time.sleep(5)
// followed by a single attempt -- the most flake-prone pattern in the
// predecessor suite (task-12-brief Step 3). Callers whose test expects a
// terminal status that happens to overlap grpcNotReady's set (there are
// none in this package -- see the package doc comment) must not use this
// helper for that call.
func waitForGRPCLive(
	ctx context.Context,
	call func(context.Context) (*harness.GRPCResult, error),
	timeout time.Duration,
) (*harness.GRPCResult, error) {
	deadline := time.Now().Add(timeout)
	var last *harness.GRPCResult
	var lastErr error
	var graceDeadline time.Time

	for time.Now().Before(deadline) {
		res, err := call(ctx)
		switch {
		case err != nil:
			lastErr = err
		case grpcNotReady(res.Code):
			last = res
			lastErr = fmt.Errorf("code=%s message=%q (route likely not programmed yet)", res.Code, res.Message)
		case res.Code == codes.Unknown && withinUnknownGrace(&graceDeadline):
			// codes.Unknown is what an Envoy HTTP 500 becomes: 500 is
			// not in Envoy's httpToGrpcStatus table, so it falls through
			// to Unknown. Gateway API mandates a 500 for a parentRef
			// whose backendRef is not resolvable yet, which for a
			// freshly deployed GRPCRoute (particularly one carrying a
			// RequestMirror filter, whose shadow cluster is programmed
			// after the primary) is a transient state, not the route's
			// real answer. Tolerate it for the same bounded window
			// harness.WaitForRouteLive gives an HTTP 5xx, then report it
			// like any other final code so a genuinely broken route
			// still fails rather than spinning out the full timeout.
			last = res
			lastErr = fmt.Errorf("code=%s message=%q (backend not resolved yet)", res.Code, res.Message)
		default:
			return res, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last != nil {
		return last, fmt.Errorf("route did not settle within %s: %w", timeout, lastErr)
	}
	return nil, fmt.Errorf("route did not become live within %s: %w", timeout, lastErr)
}

// grpcBackendGraceWindow is the gRPC counterpart to
// harness.backendGraceWindow: how long waitForGRPCLive tolerates a
// codes.Unknown (Envoy HTTP 500, "backendRef not resolvable yet") before
// accepting it as the route's genuine answer. The clock starts when the
// first such code is seen, not at waitForGRPCLive's own start.
var grpcBackendGraceWindow = 15 * time.Second

// withinUnknownGrace starts the grace clock on first call and reports
// whether it is still open.
func withinUnknownGrace(deadline *time.Time) bool {
	if deadline.IsZero() {
		*deadline = time.Now().Add(grpcBackendGraceWindow)
	}
	return time.Now().Before(*deadline)
}

// waitForGRPCResult polls call until want reports the result is the one
// the caller actually means to assert on, returning it. On timeout it
// returns the LAST observed result together with an error, so the caller's
// failure message reports what the gateway really did.
//
// This is the gRPC form of harness.WaitForResponse and exists for the same
// reason: waitForGRPCLive returns on the first "route seems programmed"
// answer, which is the wrong gate for any assertion that depends on a
// BackendTrafficPolicy / EnvoyExtensionPolicy / SecurityPolicy, since
// those are separate objects Envoy Gateway programs after the route
// itself. harness.Fixture now waits for those policies to report Accepted,
// but Accepted is a Kubernetes-API fact -- it does not prove Envoy has
// finished the xDS push -- so assertions on a policy's observable effect
// poll for that effect here rather than reading it once and hoping.
func waitForGRPCResult(
	ctx context.Context,
	call func(context.Context) (*harness.GRPCResult, error),
	want func(*harness.GRPCResult) bool,
	timeout time.Duration,
) (*harness.GRPCResult, error) {
	deadline := time.Now().Add(timeout)
	var last *harness.GRPCResult
	var lastErr error

	for time.Now().Before(deadline) {
		res, err := call(ctx)
		if err != nil {
			lastErr = err
		} else {
			last = res
			if want(res) {
				return res, nil
			}
			lastErr = fmt.Errorf("got code %s, which is not the expected outcome", res.Code)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return last, fmt.Errorf("expected result never observed within %s: %w", timeout, lastErr)
}

// uniqueMatch returns a route name (via harness.UniqueName) and a
// RouteMatch combining a grpcService condition (and, when method != "", a
// grpcMethod condition) with the discriminating header described in the
// package doc comment. Pass service == "" for a true catch-all match (only
// the discriminating header is required). callOpt must be passed to every
// GW.GRPC/GW.GRPCTyped call the test makes, so its traffic actually reaches
// its own route instead of a sibling parallel test's.
func uniqueMatch(t *testing.T, matchType, service, method string) (name string, match models.RouteMatch, callOpt harness.GRPCOpt) {
	t.Helper()
	name = harness.UniqueName(t)
	match = models.RouteMatch{
		Headers: []models.HeaderMatch{{Name: discriminatorHeader, Type: "Exact", Value: name}},
	}
	if service != "" {
		match.GRPCService = &models.GRPCMethodMatch{Type: matchType, Value: service}
	}
	if method != "" {
		match.GRPCMethod = &models.GRPCMethodMatch{Type: "Exact", Value: method}
	}
	return name, match, harness.WithGRPCMetadata(discriminatorHeader, name)
}

// waitForGRPCCodeIn polls call until it returns one of the given codes, or
// timeout elapses (returning the last observed result and a descriptive
// error). Unlike waitForGRPCLive -- which returns as soon as the FIRST
// "route seems programmed" response arrives, on the assumption that
// response is already the real, final one -- this is for security/
// client-mode tests whose expected final state is a specific policy-driven
// denial code: a GRPCRoute can reconcile microseconds before its
// SecurityPolicy or client-attachment sibling object does, producing a
// transient codes.OK that waitForGRPCLive alone would mistake for "ready
// and this IS the answer". Polling for the actual wanted code(s) directly
// avoids that race.
func waitForGRPCCodeIn(
	ctx context.Context,
	call func(context.Context) (*harness.GRPCResult, error),
	timeout time.Duration,
	want ...codes.Code,
) (*harness.GRPCResult, error) {
	isWant := func(c codes.Code) bool {
		for _, w := range want {
			if c == w {
				return true
			}
		}
		return false
	}

	deadline := time.Now().Add(timeout)
	var last *harness.GRPCResult
	var lastErr error

	for time.Now().Before(deadline) {
		res, err := call(ctx)
		if err != nil {
			lastErr = err
		} else {
			last = res
			if isWant(res.Code) {
				return res, nil
			}
			lastErr = fmt.Errorf("got code %s, want one of %v", res.Code, want)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last != nil {
		return last, fmt.Errorf("code did not settle to any of %v within %s: %w", want, timeout, lastErr)
	}
	return nil, fmt.Errorf("route did not become live within %s: %w", timeout, lastErr)
}

// expectCreateRejected asserts that creating a route from cfg (typically a
// services.CreateRouteInput) fails with HTTP 400 or 422 -- the outcome
// every grpc_validation "reject" test expects. It does not use
// harness.Fixture (which fails the test via t.Fatalf on a create error);
// creation is expected to error here, so there is nothing to approve,
// deploy, or clean up.
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

// echoCall invokes echo.EchoService/Echo with the given body and opts,
// returning the typed result and the decoded response message together
// (harness.GRPCTyped decodes directly into resp rather than returning
// bytes, so tests that need both the status code and the echoed body must
// capture resp themselves -- this is a small convenience for the common
// case).
func echoCall(ctx context.Context, body string, opts ...harness.GRPCOpt) (*harness.GRPCResult, *echo.Message, error) {
	req := &echo.Message{Body: body}
	resp := &echo.Message{}
	res, err := env.GW.GRPCTyped(ctx, echoServiceName, "Echo", req, resp, opts...)
	return res, resp, err
}

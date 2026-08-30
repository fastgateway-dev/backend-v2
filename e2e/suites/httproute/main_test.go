//go:build e2e

// Package httproute ports the Python regression suite's
// tests/http_route_features/*.py (21 tests) to Go. It exercises the HTTP
// route features (backends, redirects, direct responses, header/URL
// rewriting, mirroring, retries, timeouts, circuit breaking, compression,
// CORS, health checks, load balancing, request buffering, response
// override, and fault injection) against a live FastGateway + Envoy
// Gateway deployment.
//
// Shared cluster backends (see e2e/deps/nginx.yaml and
// e2e/deps/podinfo.yaml, namespace "default"):
//   - nginx-service:80  -- a stock nginx, always serves the same static
//     "Welcome to nginx!" page at "/" and 404s anything else.
//   - podinfo:9898      -- a controllable backend (ghcr.io/stefanprodan/podinfo)
//     with /status/{code}, /delay/{seconds}, /headers, and a JSON body at
//     "/" containing "hostname" (its own pod name).
//
// Every route created by these tests points its client-facing PathPrefix
// match at a name unique to the test (harness.UniqueName), and most
// backend-routeType tests also add a urlRewrite so the path actually
// forwarded to nginx/podinfo is one those backends recognize (nginx only
// ever serves "/"; podinfo's debug endpoints are fixed literal paths).
// This matters because harness.WaitForRouteLive treats every 404 as "route
// not programmed yet" and keeps retrying past it -- without the rewrite, a
// backend's OWN 404 for an unrecognized random path is indistinguishable
// from that and the probe would spin for the full timeout. The one test
// that genuinely wants a terminal 404 (response_override) does not use
// WaitForRouteLive for exactly this reason; see its comment.
package httproute

import (
	"context"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// env is the shared harness environment: authenticated API clients, the
// gateway data-plane client, and the Kubernetes client. Built once in
// TestMain and reused by every test in this package (mirrors
// e2e/harness/fixture.go's documented TestMain pattern).
var env *harness.Env

// podinfoMu serializes every test whose traffic or assertions depend on
// the shared "podinfo" Deployment (namespace "default") being at a known
// replica count / health state:
//
//   - health_check_active and health_check_combined scale it to 0 and
//     restore it via a deferred func (not t.Cleanup -- Go runs a test's
//     defers before its registered t.Cleanup funcs, and the restore must
//     happen while podinfoMu is still held, i.e. before the deferred
//     podinfoMu.Unlock() runs).
//   - load_balancing scales it to 3 and restores it the same way.
//   - mirror, retry, circuit_breaker, health_check_passive, backend_weight,
//     timeout_route, and timeout_btp send it real traffic (some via a
//     rewritten path hitting /status/{code} or /delay/{seconds}) and read
//     its pod logs or expect it to answer normally -- any of them running
//     concurrently with a scale-to-0/3 test would flake, and retry's and
//     circuit_breaker's and health_check_passive's log/response assertions
//     would also be unreliable if interleaved with each other's traffic to
//     the same pod.
//
// A package-level mutex (rather than restructuring these into one grouped
// parent test with t.Run subtests) was chosen because the 10 affected
// tests already live in separate files as independent top-level test
// functions, each already calling t.Parallel(): serializing just their
// podinfo-touching critical section via a shared lock is far less invasive
// than regrouping them, and every other test in the package (the other 11)
// is unaffected and keeps running fully in parallel.
var podinfoMu sync.Mutex

const (
	// backendNamespace is where the shared test backends (nginx-service,
	// podinfo) and the FastGateway-managed HTTPRoute/policy objects for
	// the seeded "default-public" domain all live.
	backendNamespace = "default"

	podinfoService = "podinfo"
	podinfoPort    = 9898
	// podinfoLabel selects podinfo's pod(s) for Kube.PodLogs (see
	// e2e/deps/podinfo.yaml: matchLabels app=podinfo).
	podinfoLabel = "app=podinfo"

	nginxService = "nginx-service"
	nginxPort    = 80

	// routeLiveTimeout bounds how long a test waits for a freshly deployed
	// route to actually be served (any non-404 status) by the gateway.
	//
	// 180s, not 90s: a second real CI run measured actual route+policy
	// convergence latency at 76-90s against the old 90s budget -- no
	// margin at all, and enough to flake outright when reconciliation ran
	// even slightly long. Don't trim this back without new measurements.
	routeLiveTimeout = 180 * time.Second
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	env, err = harness.NewEnv(ctx)
	if err != nil {
		log.Fatalf("httproute e2e: build harness env: %v", err)
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
// shared Gateway/domain.
func uniquePath(t *testing.T) (name, path string) {
	t.Helper()
	name = harness.UniqueName(t)
	return name, "/" + name
}

// rewriteTo builds a urlRewrite filter that replaces the route's matched
// prefix with backendPath before forwarding to the backend. See the
// package doc comment for why most backend-routeType tests need this.
func rewriteTo(backendPath string) *models.URLRewrite {
	return &models.URLRewrite{
		Path: &models.PathRewrite{
			Type:               "ReplacePrefixMatch",
			ReplacePrefixMatch: backendPath,
		},
	}
}

// strPtr returns a pointer to s, for the many optional *string config
// fields (durations, etc.) in the BackendTrafficPolicy models.
func strPtr(s string) *string { return &s }

// lastNChars trims s to at most its last n characters, for embedding
// large log/body blobs in failure messages without flooding test output.
func lastNChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

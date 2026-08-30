//go:build e2e

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// requireMockProm returns the mock Prometheus endpoint, failing the test
// when MOCK_PROM_URL is unset.
//
// This used to skip. The fixture now ships in this repo
// (e2e/testdata/cmd/mock-prometheus) and CI starts it alongside the
// backend, so an unset variable is a broken environment rather than an
// unsupported one -- and skipping on it is how these four tests spent
// every run reporting nothing while the metrics selectors were, in fact,
// broken for every route.
func requireMockProm(t *testing.T) string {
	t.Helper()
	if env.Cfg.MockPromURL == "" {
		t.Fatal("MOCK_PROM_URL is not set: start e2e/testdata/cmd/mock-prometheus and point MOCK_PROM_URL at it " +
			"(CI does this in the \"Run mock-prometheus\" step)")
	}
	return env.Cfg.MockPromURL
}

// mockPromQuery is one query the mock Prometheus recorded, as returned by
// its /__queries endpoint.
type mockPromQuery struct {
	Path  string `json:"path"`
	Query string `json:"query"`
	Start string `json:"start"`
	End   string `json:"end"`
	Step  string `json:"step"`
}

// mockPromDo issues a request against the mock Prometheus's own control
// endpoints (/__reset, /__set-clusters, /__queries). These are not part of
// the Prometheus API -- they exist so a test can control what the mock
// answers and inspect what the BACKEND asked it.
func mockPromDo(t *testing.T, ctx context.Context, method, path string, body any, out any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("mock-prometheus %s %s: encode body: %v", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(env.Cfg.MockPromURL, "/")+path, reader)
	if err != nil {
		t.Fatalf("mock-prometheus %s %s: build request: %v", method, path, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mock-prometheus %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("mock-prometheus %s %s: status %d", method, path, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("mock-prometheus %s %s: decode response: %v", method, path, err)
		}
	}
}

// resetMockProm clears the mock's recorded queries and configured cluster
// names so one test's assertions cannot be satisfied by another's traffic.
// Safe because every metrics test is non-parallel (see
// setProjectMetricsConfig).
func resetMockProm(t *testing.T, ctx context.Context) {
	t.Helper()
	mockPromDo(t, ctx, http.MethodPost, "/__reset", nil, nil)
}

// setMockPromClusters tells the mock which envoy_cluster_name labels to
// return from instant queries.
func setMockPromClusters(t *testing.T, ctx context.Context, clusters []string) {
	t.Helper()
	mockPromDo(t, ctx, http.MethodPost, "/__set-clusters", map[string][]string{"clusters": clusters}, nil)
}

// mockPromQueries returns every query the backend has issued since the last
// reset.
func mockPromQueries(t *testing.T, ctx context.Context) []mockPromQuery {
	t.Helper()
	var out []mockPromQuery
	mockPromDo(t, ctx, http.MethodGet, "/__queries", nil, &out)
	return out
}

// k8sRouteObjectName returns the name of the Kubernetes route object the
// backend created for routeID -- "<route name>-<8 hex of the route UUID>".
//
// Tests cannot get this from the API: models.Route.K8sRouteName is tagged
// `json:"-"`. It matters here because Envoy Gateway builds its cluster
// names from the OBJECT name, so it is the only way to construct the
// cluster name a real Prometheus would carry.
func k8sRouteObjectName(t *testing.T, ctx context.Context, protocol, routeID string) string {
	t.Helper()
	obj, err := env.Kube.GetUnstructuredByLabel(ctx, harness.RouteGVR(protocol), env.Cfg.Namespace,
		"fastgateway.dev/route-id="+routeID)
	if err != nil {
		t.Fatalf("resolve Kubernetes route object for route %s: %v", routeID, err)
	}
	return obj.GetName()
}

// setProjectMetricsConfig PATCHes the seeded e2e project's metrics
// endpoint config (project-wide shared mutable state -- see the package
// doc comment) and registers a t.Cleanup that restores whatever values
// were in place before this call, fetched via GetProjectByName first.
// Every test that calls this must NOT call t.Parallel(): Go only starts a
// parallel test's body once every non-parallel test in the package has
// finished, so simply omitting t.Parallel() from every metrics-config-
// touching test serializes them against each other and against every
// other (parallel) test that reads project metrics, with no extra locking
// needed.
func setProjectMetricsConfig(t *testing.T, endpointURL, authType string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	before, err := env.Admin.GetProjectByName(ctx, env.Cfg.ProjectName)
	if err != nil {
		t.Fatalf("fetch project %q baseline metrics config: %v", env.Cfg.ProjectName, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		restoreURL, restoreAuth := before.MetricsEndpointURL, before.MetricsAuthType
		input := services.UpdateProjectInput{MetricsEndpointURL: &restoreURL, MetricsAuthType: &restoreAuth}
		if _, err := env.Admin.Do(cleanupCtx, http.MethodPatch, "/projects/"+env.ProjectID, input, nil); err != nil {
			t.Errorf("cleanup: restore project metrics config (endpoint=%q authType=%q): %v", restoreURL, restoreAuth, err)
		}
	})

	url, authT := endpointURL, authType
	input := services.UpdateProjectInput{MetricsEndpointURL: &url, MetricsAuthType: &authT}
	if _, err := env.Admin.Do(ctx, http.MethodPatch, "/projects/"+env.ProjectID, input, nil); err != nil {
		t.Fatalf("set project metrics config (endpoint=%q authType=%q): %v", endpointURL, authType, err)
	}
}

// mockPromRangeValue mirrors the RangeValue constant in
// e2e/cmd/mock-prometheus: the fixed value every point of every range
// query carries. Duplicated rather than imported because that program is
// package main.
const mockPromRangeValue = 42

// mockPromTopValues mirrors mock-prometheus's InstantValues: the values it
// assigns to configured clusters, in order.
var mockPromTopValues = []float64{30, 20, 10}

// formatQueries renders recorded queries for a failure message.
func formatQueries(queries []mockPromQuery) string {
	var b strings.Builder
	for _, q := range queries {
		fmt.Fprintf(&b, "\n      %s %s", q.Path, q.Query)
	}
	return b.String()
}

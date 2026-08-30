//go:build e2e

package platform

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// getTestConnection POSTs /projects/:projectId/metrics/test-connection and
// decodes the result. Unlike the metrics/topology endpoints elsewhere in
// this package, MetricsHandler.TestConnection always returns HTTP 200 --
// the "ok" boolean in the body is what carries success/failure.
func getTestConnection(t *testing.T, ctx context.Context) services.TestConnectionResult {
	t.Helper()
	var result services.TestConnectionResult
	path := "/projects/" + env.ProjectID + "/metrics/test-connection"
	if _, err := env.Admin.Do(ctx, http.MethodPost, path, nil, &result); err != nil {
		t.Fatalf("post metrics test-connection: %v", err)
	}
	return result
}

// TestMetricsTestConnectionSuccess ports
// observability/test_metrics_test_connection.py:test_metrics_test_connection_success.
// Already a real assertion in the Python source; ported unchanged in
// spirit. Does NOT call t.Parallel() -- see the package doc comment.
func TestMetricsTestConnectionSuccess(t *testing.T) {
	promURL := requireMockProm(t)
	setProjectMetricsConfig(t, promURL, "none")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := getTestConnection(t, ctx)
	if !result.OK {
		t.Fatalf("metrics test-connection against %s: ok=false, error=%q, want ok=true", promURL, result.Error)
	}
}

// TestMetricsTestConnectionUnreachable ports
// observability/test_metrics_test_connection.py:test_metrics_test_connection_unreachable.
// Already a real assertion in the Python source; ported unchanged in
// spirit. Does NOT call t.Parallel() -- see the package doc comment. Does
// NOT require MOCK_PROM_URL: it deliberately configures an unreachable
// endpoint instead.
func TestMetricsTestConnectionUnreachable(t *testing.T) {
	setProjectMetricsConfig(t, "http://nope.invalid:9090", "none")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := getTestConnection(t, ctx)
	if result.OK {
		t.Fatalf("metrics test-connection against an unreachable endpoint: ok=true, want ok=false")
	}
	if result.Error == "" {
		t.Fatalf("metrics test-connection against an unreachable endpoint: ok=false but error message is empty")
	}
}

// TestMetricsTestConnectionNotConfigured ports
// observability/test_metrics_test_connection.py:test_metrics_test_connection_not_configured.
// Already a real assertion in the Python source; ported unchanged in
// spirit. Does NOT call t.Parallel() -- see the package doc comment. Does
// NOT require MOCK_PROM_URL: it deliberately clears the endpoint.
func TestMetricsTestConnectionNotConfigured(t *testing.T) {
	setProjectMetricsConfig(t, "", "none")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := getTestConnection(t, ctx)
	if result.OK {
		t.Fatalf("metrics test-connection with no endpoint configured: ok=true, want ok=false")
	}
	if !strings.Contains(strings.ToLower(result.Error), "not configured") {
		t.Fatalf("metrics test-connection with no endpoint configured: error=%q, want it to mention \"not configured\"", result.Error)
	}
}

//go:build e2e

package platform

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestMetricsDomainAggregateAndTop5 ports
// observability/test_metrics_domain.py:test_domain_metrics_returns_aggregate_and_top5.
// Already a real assertion in the Python source (required-key presence
// checks, no status-membership tautology); ported unchanged in spirit.
//
// Does NOT call t.Parallel(): it mutates the project-wide metrics config.
// See the package doc comment and observability_metrics_helpers_test.go's
// setProjectMetricsConfig.
func TestMetricsDomainAggregateAndTop5(t *testing.T) {
	promURL := requireMockProm(t)
	setProjectMetricsConfig(t, promURL, "none")

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	_, _, cfg := simpleRouteConfig(t)
	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	var result services.DomainMetricsResult
	path := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/metrics?range=1h"
	if _, err := env.Admin.Do(ctx, http.MethodGet, path, nil, &result); err != nil {
		t.Fatalf("get domain metrics: %v", err)
	}

	if result.TopRoutesByRps == nil {
		t.Fatalf("domain metrics: topRoutesByRps is nil, want a (possibly empty) list")
	}
	if result.TopRoutesByErrorRate == nil {
		t.Fatalf("domain metrics: topRoutesByErrorRate is nil, want a (possibly empty) list")
	}
	if result.TimeRange.Step == "" {
		t.Fatalf("domain metrics: timeRange.step is empty, want a real step value")
	}
}

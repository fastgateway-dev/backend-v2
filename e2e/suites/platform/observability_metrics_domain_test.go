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
	resetMockProm(t, ctx)

	_, _, cfg := simpleRouteConfig(t)
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	// Hand the mock the cluster name a REAL Prometheus would carry for this
	// route, built from the Kubernetes object name exactly as Envoy Gateway
	// builds it (internal/gatewayapi/helpers.go's irRoutePrefix). The
	// backend maps an instant sample back to a route by matching
	// envoy_cluster_name against a key it constructs independently, so
	// requiring the route to come back below is what proves those two
	// constructions agree.
	//
	// They did not: the lookup used models.Route.Name, while Envoy Gateway
	// uses the object name "<route name>-<8 hex of the route UUID>". Both
	// top-route lists came back empty for every domain, and the previous
	// version of this test asserted only "not nil" -- which an empty list
	// satisfies.
	objectName := k8sRouteObjectName(t, ctx, "http", route.ID.String())
	cluster := "httproute/" + env.Cfg.Namespace + "/" + objectName + "/rule/0"
	setMockPromClusters(t, ctx, []string{cluster})

	var result services.DomainMetricsResult
	path := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/metrics?range=1h"
	if _, err := env.Admin.Do(ctx, http.MethodGet, path, nil, &result); err != nil {
		t.Fatalf("get domain metrics: %v", err)
	}

	if result.TimeRange.Step == "" {
		t.Fatalf("domain metrics: timeRange.step is empty, want a real step value")
	}

	for name, entries := range map[string][]services.TopRouteEntry{
		"topRoutesByRps":       result.TopRoutesByRps,
		"topRoutesByErrorRate": result.TopRoutesByErrorRate,
	} {
		if len(entries) == 0 {
			t.Fatalf("domain metrics: %s is empty even though mock-prometheus returned a sample for %q -- "+
				"the backend's cluster-name lookup does not match the name Envoy Gateway emits", name, cluster)
		}
		found := false
		for _, e := range entries {
			if e.RouteID == route.ID {
				found = true
				if e.RouteName != route.Name {
					t.Errorf("domain metrics: %s entry for route %s has routeName=%q, want %q",
						name, route.ID, e.RouteName, route.Name)
				}
				if e.Value != mockPromTopValues[0] {
					t.Errorf("domain metrics: %s entry for route %s has value=%v, want %v",
						name, route.ID, e.Value, mockPromTopValues[0])
				}
			}
		}
		if !found {
			t.Errorf("domain metrics: %s does not contain route %s (%s); got %+v",
				name, route.Name, route.ID, entries)
		}
	}
}

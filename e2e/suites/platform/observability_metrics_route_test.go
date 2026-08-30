//go:build e2e

package platform

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestMetricsRouteReturnsTierAPanels ports
// observability/test_metrics_route.py:test_route_metrics_returns_tier_a_panels.
// Already a real assertion in the Python source; ported unchanged in
// spirit. Does NOT call t.Parallel() -- see the package doc comment.
func TestMetricsRouteReturnsTierAPanels(t *testing.T) {
	promURL := requireMockProm(t)
	setProjectMetricsConfig(t, promURL, "none")

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()
	resetMockProm(t, ctx)

	_, _, cfg := simpleRouteConfig(t)
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	var result services.RouteMetricsResult
	path := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/routes/" + route.ID.String() + "/metrics?range=1h"
	if _, err := env.Admin.Do(ctx, http.MethodGet, path, nil, &result); err != nil {
		t.Fatalf("get route %s (%s) metrics: %v", route.Name, route.ID, err)
	}

	if result.TimeRange.Step != "30s" {
		t.Fatalf("route %s (%s) metrics: timeRange.step=%q, want %q", route.Name, route.ID, result.TimeRange.Step, "30s")
	}

	// Real values, not key presence. The Python original checked `"2xx" in
	// body["rps"]`, which a struct decode already guarantees at compile
	// time and which an all-empty response satisfies just as well as a
	// correct one. mock-prometheus answers every range query with a fixed
	// value, so the panels can be checked against it -- and an empty panel
	// (what a wrong cluster selector produces) now fails.
	for name, points := range map[string][]services.PromPoint{
		"rps.2xx":     result.Rps.Class2xx,
		"latency.p95": result.Latency.P95,
	} {
		if len(points) == 0 {
			t.Fatalf("route %s (%s) metrics: %s has no points -- an empty panel is what a cluster selector "+
				"that matches nothing produces, and is indistinguishable from 'no traffic' unless asserted",
				route.Name, route.ID, name)
		}
		if got := points[0].Value; got != mockPromRangeValue {
			t.Fatalf("route %s (%s) metrics: %s[0].value=%v, want %v (mock-prometheus's fixed value)",
				route.Name, route.ID, name, got, mockPromRangeValue)
		}
	}

	// The panels being populated proves the backend queried SOMETHING. This
	// proves it queried the right thing: Envoy Gateway names a cluster after
	// the Kubernetes OBJECT ("<route name>-<8 hex>"), not the route's
	// display name, so a selector built from the display name matches no
	// series in a real Prometheus and every panel silently reads zero.
	objectName := k8sRouteObjectName(t, ctx, "http", route.ID.String())
	wantCluster := "httproute/" + env.Cfg.Namespace + "/" + objectName + "/rule/"
	queries := mockPromQueries(t, ctx)
	if len(queries) == 0 {
		t.Fatalf("route %s (%s) metrics: mock-prometheus recorded no queries at all", route.Name, route.ID)
	}
	matched := false
	for _, q := range queries {
		if strings.Contains(q.Query, wantCluster) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("route %s (%s) metrics: no query selected the route's real Envoy cluster %q.\n"+
			"Queries issued: %s", route.Name, route.ID, wantCluster, formatQueries(queries))
	}
}

// TestMetricsRouteInvalidRange ports
// observability/test_metrics_route.py:test_route_metrics_invalid_range.
// Already a real assertion in the Python source (status code + error
// code); ported unchanged in spirit. Does NOT call t.Parallel() -- see the
// package doc comment.
func TestMetricsRouteInvalidRange(t *testing.T) {
	promURL := requireMockProm(t)
	setProjectMetricsConfig(t, promURL, "none")

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	_, _, cfg := simpleRouteConfig(t)
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	path := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/routes/" + route.ID.String() + "/metrics?range=bogus"
	_, err := env.Admin.Do(ctx, http.MethodGet, path, nil, nil)
	if err == nil {
		t.Fatalf("get route %s (%s) metrics with range=bogus: request succeeded, want 400", route.Name, route.ID)
	}
	var statusErr *harness.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("get route %s (%s) metrics with range=bogus: error %v is not a *harness.StatusError", route.Name, route.ID, err)
	}
	if statusErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("get route %s (%s) metrics with range=bogus: got status %d, want %d (body: %s)", route.Name, route.ID, statusErr.StatusCode, http.StatusBadRequest, statusErr.Body)
	}
}

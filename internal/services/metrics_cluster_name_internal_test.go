package services

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// These pin the Envoy cluster-name format the metrics selectors depend on.
// Getting it wrong is silent: Prometheus answers an unmatched selector with
// an empty result, which is indistinguishable from "this route has had no
// traffic yet", so every panel renders as zero and nothing reports an
// error. That is exactly how the previous formulation survived -- it used
// models.Route.Name where Envoy Gateway uses the Kubernetes object name.
//
// The authority is Envoy Gateway's internal/gatewayapi/helpers.go:
//
//	func irRoutePrefix(route RouteContext) string {
//	    return fmt.Sprintf("%s/%s/%s/", strings.ToLower(string(route.GetRouteType())),
//	                       route.GetNamespace(), route.GetName())
//	}
//
// route.GetName() is the reconciled object's name, i.e. K8sRouteName.

func TestRouteClusterName_UsesKubernetesObjectName(t *testing.T) {
	route := models.Route{
		Name:         "my-route",
		K8sRouteName: "my-route-a1b2c3d4",
		Protocol:     models.RouteProtocolHTTP,
	}
	got := routeClusterName("fastgateway-system", route)
	want := "httproute/fastgateway-system/my-route-a1b2c3d4/rule/0"
	if got != want {
		t.Fatalf("routeClusterName = %q, want %q", got, want)
	}
	if strings.Contains(got, "/my-route/") {
		t.Errorf("cluster name used the model name instead of the Kubernetes object name: %q", got)
	}
}

func TestRouteClusterName_GRPCUsesGrpcroutePrefix(t *testing.T) {
	route := models.Route{
		Name:         "my-grpc",
		K8sRouteName: "my-grpc-deadbeef",
		Protocol:     models.RouteProtocolGRPC,
	}
	got := routeClusterName("fastgateway-system", route)
	want := "grpcroute/fastgateway-system/my-grpc-deadbeef/rule/0"
	if got != want {
		t.Fatalf("routeClusterName = %q, want %q", got, want)
	}
}

func TestRouteClusterName_FallsBackToNameForLegacyRows(t *testing.T) {
	// Rows written before k8s_route_name existed carry "". Falling back to
	// Name is wrong-but-harmless; leaving the segment EMPTY would produce
	// "httproute/ns//rule/0", whose selector form matches every route in
	// the namespace and would silently attribute the whole domain's
	// traffic to one route.
	route := models.Route{Name: "legacy", Protocol: models.RouteProtocolHTTP}
	got := routeClusterName("ns", route)
	if got != "httproute/ns/legacy/rule/0" {
		t.Fatalf("routeClusterName = %q, want %q", got, "httproute/ns/legacy/rule/0")
	}
	if strings.Contains(got, "//") {
		t.Errorf("empty name segment would match every route in the namespace: %q", got)
	}
}

func TestBuildRouteClusterSelector_MatchesTheClusterName(t *testing.T) {
	route := models.Route{
		Name:         "my-route",
		K8sRouteName: "my-route-a1b2c3d4",
		Protocol:     models.RouteProtocolHTTP,
	}
	sel := buildRouteClusterSelector("fastgateway-system", route)
	want := `envoy_cluster_name=~"httproute/fastgateway-system/my-route-a1b2c3d4/rule/.*"`
	if sel != want {
		t.Fatalf("buildRouteClusterSelector = %q, want %q", sel, want)
	}
}

func TestBuildRouteClusterSelector_GRPC(t *testing.T) {
	route := models.Route{
		Name:         "my-grpc",
		K8sRouteName: "my-grpc-deadbeef",
		Protocol:     models.RouteProtocolGRPC,
	}
	sel := buildRouteClusterSelector("ns", route)
	if !strings.Contains(sel, `grpcroute/ns/my-grpc-deadbeef/rule/.*`) {
		t.Fatalf("gRPC selector should target grpcroute clusters, got %q", sel)
	}
}

func TestBuildDomainClusterSelector_CoversBothRouteKinds(t *testing.T) {
	// A domain can carry HTTP and gRPC routes at once. An httproute-only
	// selector drops every gRPC route from the domain aggregate without
	// saying so.
	sel := buildDomainClusterSelector("fastgateway-system")
	for _, kind := range []string{"httproute", "grpcroute"} {
		if !strings.Contains(sel, kind) {
			t.Errorf("domain selector %q does not cover %s clusters", sel, kind)
		}
	}
}

func TestMapTopSamples_MatchesRealEnvoyClusterNames(t *testing.T) {
	// End-to-end for the lookup: build the map exactly as
	// GetDomainMetrics does, feed it the cluster name Envoy Gateway would
	// emit, and require the route to be found. Under the old
	// model-name-based key this returned an empty list.
	routeID := uuid.New()
	route := models.Route{
		Name:         "checkout",
		K8sRouteName: "checkout-0f0f0f0f",
		Protocol:     models.RouteProtocolHTTP,
	}
	route.ID = routeID

	lookup := map[string]models.Route{
		routeClusterName("fastgateway-system", route): route,
	}
	res := &PromInstantResult{Samples: []PromSample{{
		Labels: map[string]string{"envoy_cluster_name": "httproute/fastgateway-system/checkout-0f0f0f0f/rule/0"},
		Value:  12.5,
	}}}

	got := mapTopSamples(res, lookup)
	if len(got) != 1 {
		t.Fatalf("mapTopSamples returned %d entries, want 1 -- the Envoy cluster name did not match the lookup key", len(got))
	}
	if got[0].RouteID != routeID || got[0].RouteName != "checkout" || got[0].Value != 12.5 {
		t.Fatalf("mapTopSamples = %+v, want routeID=%s name=checkout value=12.5", got[0], routeID)
	}
}

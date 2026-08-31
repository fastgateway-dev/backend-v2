package services

import (
	"path/filepath"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/stretchr/testify/require"
)

// TestGoldenHTTPRouteDeploy snapshots the HTTPRoute the DEPLOY path produces.
// This is the authoritative path -- it is what the cluster actually runs.
func TestGoldenHTTPRouteDeploy(t *testing.T) {
	svc := &RouteService{} // buildHTTPRouteConfig does not use its receiver
	for _, f := range goldenFixtures() {
		if f.Route.Protocol != "" && f.Route.Protocol != "http" {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			cfg := svc.buildHTTPRouteConfig(f.Route, f.Domain)
			assertGolden(t, filepath.Join("httproute-deploy", f.Name), kubernetes.BuildHTTPRouteObject(cfg))
		})
	}
}

// TestGoldenHTTPRoutePreview snapshots the HTTPRoute the PREVIEW/YAML path
// produces. For fixtures with a KnownDrift these files would record WRONG
// output on purpose, capturing a preview/deploy divergence deliberately --
// no fixture currently sets KnownDrift, since the divergences that
// mechanism existed for have been fixed (see BuildHTTPRouteConfig's doc
// comment in internal/routeplan/httproute.go).
func TestGoldenHTTPRoutePreview(t *testing.T) {
	for _, f := range goldenFixtures() {
		if f.Route.Protocol != "" && f.Route.Protocol != "http" {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			cfg := routeplan.BuildHTTPRouteConfigForYAML(f.Route, f.Domain)
			assertGolden(t, filepath.Join("httproute-preview", f.Name), kubernetes.BuildHTTPRouteObject(cfg))
		})
	}
}

// TestDifferentialHTTPRoute is a re-divergence guard, not a live comparison.
//
// Both (*RouteService).buildHTTPRouteConfig and buildHTTPRouteConfigForYAML
// delegate to the single buildHTTPRouteConfigUnified function, so this test
// currently compares f(x) against f(x): the require.Equal below is
// trivially true and cannot fail as things stand.
//
// Its value is prospective: if a future change (e.g. moving this assembly
// elsewhere in internal/routeplan) reintroduces two separate assembly paths
// that drift apart again, this test starts failing and catches it. Keep it,
// and keep the two entry points delegating to one function -- that is what
// keeps the assertion trivially true rather than silently wrong.
//
// KnownDrift is still supported by this loop for exactly the inverse
// situation -- a deliberately-reintroduced, tracked divergence -- but no
// fixture currently sets it, and none should without a corresponding defect.
func TestDifferentialHTTPRoute(t *testing.T) {
	svc := &RouteService{}
	for _, f := range goldenFixtures() {
		if f.Route.Protocol != "" && f.Route.Protocol != "http" {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			deploy := svc.buildHTTPRouteConfig(f.Route, f.Domain)
			preview := routeplan.BuildHTTPRouteConfigForYAML(f.Route, f.Domain)

			if f.KnownDrift != "" {
				require.NotEqualf(t, deploy, preview,
					"fixture %q is marked with KnownDrift %q, but the two paths now AGREE.\n"+
						"If you fixed the drift, clear the KnownDrift field on this fixture "+
						"and regenerate the preview golden.", f.Name, f.KnownDrift)
				return
			}

			require.Equalf(t, deploy, preview,
				"deploy and preview assembly disagree for fixture %q.\n"+
					"Either this is a new drift (add a KnownDrift and open a defect), "+
					"or the collapse broke something.", f.Name)
		})
	}
}

func TestGoldenGRPCRouteDeploy(t *testing.T) {
	svc := &RouteService{}
	for _, f := range goldenFixtures() {
		if f.Route.Protocol != models.RouteProtocolGRPC {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			cfg := svc.buildGRPCRouteConfig(f.Route, f.Domain)
			assertGolden(t, filepath.Join("grpcroute-deploy", f.Name), kubernetes.BuildGRPCRouteObject(cfg))
		})
	}
}

func TestGoldenGRPCRoutePreview(t *testing.T) {
	for _, f := range goldenFixtures() {
		if f.Route.Protocol != models.RouteProtocolGRPC {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			cfg := routeplan.BuildGRPCRouteConfigForYAML(f.Route, f.Domain)
			assertGolden(t, filepath.Join("grpcroute-preview", f.Name), kubernetes.BuildGRPCRouteObject(cfg))
		})
	}
}

// TestDifferentialGRPCRoute is a re-divergence guard, not a live comparison.
//
// Both (*RouteService).buildGRPCRouteConfig and buildGRPCRouteConfigForYAML
// delegate to the single routeplan.BuildGRPCRouteConfig function, so this
// test currently compares f(x) against f(x): the require.Equal below is
// trivially true and cannot fail as things stand.
//
// Its value is prospective: if a future change reintroduces two separate
// assembly paths that drift apart again, this test starts failing and
// catches it. Keep it, and keep the two entry points delegating to one
// function -- that is what keeps the assertion trivially true rather than
// silently wrong.
func TestDifferentialGRPCRoute(t *testing.T) {
	svc := &RouteService{}
	for _, f := range goldenFixtures() {
		if f.Route.Protocol != models.RouteProtocolGRPC {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			deploy := svc.buildGRPCRouteConfig(f.Route, f.Domain)
			preview := routeplan.BuildGRPCRouteConfigForYAML(f.Route, f.Domain)
			if f.KnownDrift != "" {
				require.NotEqualf(t, deploy, preview, "fixture %q marked KnownDrift %q but paths agree", f.Name, f.KnownDrift)
				return
			}
			require.Equalf(t, deploy, preview, "deploy and preview GRPCRoute assembly disagree for %q", f.Name)
		})
	}
}

// --- generateDirectResponseYAMLs: the HTTPRouteFilter + ConfigMap that the
// extensionRef filter (set on the deploy path only when DirectResponse != nil,
// see BuildHTTPRouteConfig's doc comment) actually points at. It was already
// unit-tested (route_service_yaml_internal_test.go:240-276 --
// TestInternalGenerateDirectResponseYAMLs_WithBody/_NoBody/_Nil), so this is a
// snapshot gap rather than an untested-code gap; snapshots are what protect
// this assembly as it moves around internal/routeplan, so it gets golden
// coverage here.
//
// Only the Inline body shape is covered. The model also defines
// models.DirectResponseBodyTypeValueRef (models/route.go), but
// models.DirectResponseBody carries no field naming an existing ConfigMap --
// it has only Type and Inline. generateDirectResponseYAMLs (route_service.go)
// sets hrfConfig.DirectResponse.Body (Type: "ValueRef", pointing at a
// ConfigMap it generates itself) only when Body.Inline is non-empty,
// regardless of Body.Type. A fixture with Type: ValueRef and no Inline
// content would leave hrfConfig.DirectResponse.Body nil and produce output
// byte-identical to the no-body case already covered by
// TestInternalGenerateDirectResponseYAMLs_NoBody -- it is not a distinct
// output shape today, so it is not forced into a golden here.
func fixtureDirectResponseHRFRoute() *models.Route {
	route := fixtureRoute("direct-hrf")
	route.Config.RouteType = models.RouteTypeDirectResponse
	route.Config.Backends = nil
	route.Config.DirectResponse = &models.DirectResponseConfig{
		StatusCode:  503,
		ContentType: "text/plain",
		Body: &models.DirectResponseBody{
			Type:   models.DirectResponseBodyTypeInline,
			Inline: "maintenance",
		},
	}
	return route
}

func TestGoldenDirectResponseYAMLs(t *testing.T) {
	route, domain := fixtureDirectResponseHRFRoute(), fixtureDomain()
	hrfYAML, cmYAML := routeplan.GenerateDirectResponseYAMLs(route, domain)

	require.NotEmpty(t, hrfYAML, "HTTPRouteFilter YAML must not be empty for an inline body")
	require.NotEmpty(t, cmYAML, "ConfigMap YAML must not be empty for an inline body")

	assertGolden(t, filepath.Join("directresponse-preview", "inline-httproutefilter"), hrfYAML)
	assertGolden(t, filepath.Join("directresponse-preview", "inline-configmap"), cmYAML)
}

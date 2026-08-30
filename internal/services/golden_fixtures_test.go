package services

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// manifestFixture is one route+domain input, snapshotted through every
// manifest-assembly path.
//
// KnownDrift is non-empty when the deploy and preview paths are KNOWN to
// disagree for this fixture today. It names the defect from spec §2.4. The
// differential test inverts its assertion for such fixtures -- it asserts the
// drift is still there. Task 7 fixes the drifts and clears these fields.
type manifestFixture struct {
	Name       string
	Route      *models.Route
	Domain     *models.Domain
	KnownDrift string
}

// fixtureDomain is the shared domain for every fixture. Fixed UUIDs keep the
// golden output stable.
func fixtureDomain() *models.Domain {
	return &models.Domain{
		ID:             uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ProjectID:      uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Name:           "example.com",
		Hostname:       "example.com",
		Namespace:      "gateway-ns",
		K8sGatewayName: "eg",
	}
}

// fixtureRoute returns a minimal HTTP route. Callers mutate the returned value
// to build variants -- each call returns a fresh copy.
func fixtureRoute(name string) *models.Route {
	return &models.Route{
		ID:           uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DomainID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Name:         name,
		Protocol:     models.RouteProtocolHTTP,
		K8sRouteName: name + "-11111111",
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: "/api"}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "my-svc", Namespace: "default", Port: 8080},
			},
		},
	}
}

func goldenFixtures() []manifestFixture {
	var fixtures []manifestFixture

	// 1. Plain Kubernetes backend. The control case: deploy and preview must agree.
	fixtures = append(fixtures, manifestFixture{
		Name:   "plain-backend",
		Route:  fixtureRoute("plain-backend"),
		Domain: fixtureDomain(),
	})

	// 2. Direct response, bare.
	directBare := fixtureRoute("direct-bare")
	directBare.Config.RouteType = models.RouteTypeDirectResponse
	directBare.Config.Backends = nil
	directBare.Config.DirectResponse = &models.DirectResponseConfig{
		StatusCode:  503,
		ContentType: "text/plain",
		Body: &models.DirectResponseBody{
			Type:   models.DirectResponseBodyTypeInline,
			Inline: "maintenance",
		},
	}
	fixtures = append(fixtures, manifestFixture{
		Name:   "direct-response-bare",
		Route:  directBare,
		Domain: fixtureDomain(),
	})

	// 3. Direct response WITH a URL rewrite and a request header modifier.
	directFull := fixtureRoute("direct-full")
	directFull.Config.RouteType = models.RouteTypeDirectResponse
	directFull.Config.Backends = nil
	directFull.Config.DirectResponse = &models.DirectResponseConfig{
		StatusCode:  418,
		ContentType: "application/json",
		Body: &models.DirectResponseBody{
			Type:   models.DirectResponseBodyTypeInline,
			Inline: `{"teapot":true}`,
		},
	}
	directFull.Config.URLRewrite = &models.URLRewrite{
		Path: &models.PathRewrite{Type: "ReplacePrefixMatch", ReplacePrefixMatch: "/rewritten"},
	}
	directFull.Config.RequestHeaderModifier = &models.HeaderModifier{
		Set: []models.HeaderValue{{Name: "x-fixture", Value: "direct-full"}},
	}
	fixtures = append(fixtures, manifestFixture{
		Name:   "direct-response-with-rewrite-and-headers",
		Route:  directFull,
		Domain: fixtureDomain(),
	})

	// 4. Direct response WITH a response header modifier. The renderer
	//    (kubernetes_service.go BuildHTTPRouteObject, the config.HTTPRouteFilterName != ""
	//    branch) deliberately KEEPS the response header modifier for
	//    direct-response routes while nil-ing the request modifier and
	//    rewrite -- unlike those two, ResponseHeaderModifier has no
	//    `DirectResponse == nil` guard in buildHTTPRouteConfigUnified at all.
	//    Nothing pinned that asymmetry before; a future refactor could flatten
	//    it by accident.
	directWithRespHeaders := fixtureRoute("direct-resp-headers")
	directWithRespHeaders.Config.RouteType = models.RouteTypeDirectResponse
	directWithRespHeaders.Config.Backends = nil
	directWithRespHeaders.Config.DirectResponse = &models.DirectResponseConfig{
		StatusCode:  503,
		ContentType: "text/plain",
		Body: &models.DirectResponseBody{
			Type:   models.DirectResponseBodyTypeInline,
			Inline: "maintenance",
		},
	}
	directWithRespHeaders.Config.ResponseHeaderModifier = &models.HeaderModifier{
		Set: []models.HeaderValue{{Name: "x-maintenance", Value: "true"}},
	}
	fixtures = append(fixtures, manifestFixture{
		Name:   "direct-response-with-response-header-modifier",
		Route:  directWithRespHeaders,
		Domain: fixtureDomain(),
	})

	// 5. Direct response WITH multiple matches. HTTPRouteFilterName is set
	//    once at the HTTPRouteConfig level, but BuildHTTPRouteObject emits the
	//    extensionRef filter PER RULE (it builds filters inside the
	//    `for _, rule := range config.Rules` loop). A multi-match
	//    direct-response route therefore fans the same extensionRef filter out
	//    across every rule, and nothing pinned that fan-out before.
	directMultiMatch := fixtureRoute("direct-multi-match")
	directMultiMatch.Config.RouteType = models.RouteTypeDirectResponse
	directMultiMatch.Config.Backends = nil
	directMultiMatch.Config.Matches = []models.RouteMatch{
		{Path: &models.PathMatch{Type: "Prefix", Value: "/a"}, Method: "GET"},
		{
			Path:    &models.PathMatch{Type: "Exact", Value: "/b"},
			Headers: []models.HeaderMatch{{Name: "x-tenant", Type: "Exact", Value: "acme"}},
		},
	}
	directMultiMatch.Config.DirectResponse = &models.DirectResponseConfig{
		StatusCode:  503,
		ContentType: "text/plain",
		Body: &models.DirectResponseBody{
			Type:   models.DirectResponseBodyTypeInline,
			Inline: "maintenance",
		},
	}
	fixtures = append(fixtures, manifestFixture{
		Name:   "direct-response-multi-match",
		Route:  directMultiMatch,
		Domain: fixtureDomain(),
	})

	// --- route shapes -------------------------------------------------------

	redirect := fixtureRoute("redirect")
	redirect.Config.RouteType = models.RouteTypeRedirect
	redirect.Config.Backends = nil
	redirect.Config.Redirect = &models.RedirectConfig{
		Scheme:     "https",
		StatusCode: 301,
	}
	fixtures = append(fixtures, manifestFixture{Name: "redirect", Route: redirect, Domain: fixtureDomain()})

	rewrite := fixtureRoute("rewrite")
	rewrite.Config.URLRewrite = &models.URLRewrite{
		Path: &models.PathRewrite{Type: "ReplacePrefixMatch", ReplacePrefixMatch: "/v2"},
	}
	fixtures = append(fixtures, manifestFixture{Name: "url-rewrite", Route: rewrite, Domain: fixtureDomain()})

	headers := fixtureRoute("headers")
	headers.Config.RequestHeaderModifier = &models.HeaderModifier{
		Set:    []models.HeaderValue{{Name: "x-set", Value: "a"}},
		Add:    []models.HeaderValue{{Name: "x-add", Value: "b"}},
		Remove: []string{"x-remove"},
	}
	headers.Config.ResponseHeaderModifier = &models.HeaderModifier{
		Set: []models.HeaderValue{{Name: "x-resp", Value: "c"}},
	}
	fixtures = append(fixtures, manifestFixture{Name: "header-modifiers", Route: headers, Domain: fixtureDomain()})

	mirrors := fixtureRoute("mirrors")
	mirrors.Config.Mirrors = []models.MirrorBackend{
		{Type: models.BackendTypeKubernetes, Service: "shadow-svc", Namespace: "default", Port: 8080},
	}
	fixtures = append(fixtures, manifestFixture{Name: "mirrors", Route: mirrors, Domain: fixtureDomain()})

	weighted := fixtureRoute("weighted")
	weighted.Config.Backends = []models.RouteBackend{
		{Type: models.BackendTypeKubernetes, Service: "a-svc", Namespace: "default", Port: 80, Weight: 80},
		{Type: models.BackendTypeKubernetes, Service: "b-svc", Namespace: "default", Port: 80, Weight: 20},
	}
	fixtures = append(fixtures, manifestFixture{Name: "weighted-backends", Route: weighted, Domain: fixtureDomain()})

	external := fixtureRoute("external")
	external.Config.Backends = []models.RouteBackend{
		{
			Type:        models.BackendTypeExternal,
			AddressType: models.ExternalAddressTypeFQDN,
			Address:     "api.upstream.test",
			Port:        443,
		},
	}
	fixtures = append(fixtures, manifestFixture{Name: "external-backend", Route: external, Domain: fixtureDomain()})

	multiMatch := fixtureRoute("multi-match")
	multiMatch.Config.Matches = []models.RouteMatch{
		{Path: &models.PathMatch{Type: "Prefix", Value: "/a"}, Method: "GET"},
		{
			Path:    &models.PathMatch{Type: "Exact", Value: "/b"},
			Headers: []models.HeaderMatch{{Name: "x-tenant", Type: "Exact", Value: "acme"}},
		},
	}
	fixtures = append(fixtures, manifestFixture{Name: "multi-match", Route: multiMatch, Domain: fixtureDomain()})

	// --- gRPC ---------------------------------------------------------------

	grpc := fixtureRoute("grpc")
	grpc.Protocol = models.RouteProtocolGRPC
	grpc.Config.Matches = []models.RouteMatch{
		{
			GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "echo.Echo"},
			GRPCMethod:  &models.GRPCMethodMatch{Type: "Exact", Value: "Ping"},
		},
	}
	fixtures = append(fixtures, manifestFixture{Name: "grpc-basic", Route: grpc, Domain: fixtureDomain()})

	// 2. gRPC service-only match, using RegularExpression instead of Exact.
	//    Exercises the `match.GRPCMethod != nil` false branch (only
	//    GRPCService is set) and the RegularExpression match-type value
	//    flowing through both buildGRPCRouteConfig(ForYAML) and
	//    BuildGRPCRouteObject's methodMatch.Type assignment.
	grpcServiceOnly := fixtureRoute("grpc-service-only")
	grpcServiceOnly.Protocol = models.RouteProtocolGRPC
	grpcServiceOnly.Config.Matches = []models.RouteMatch{
		{
			GRPCService: &models.GRPCMethodMatch{Type: "RegularExpression", Value: "^echo\\..*"},
		},
	}
	fixtures = append(fixtures, manifestFixture{Name: "grpc-service-only-regex", Route: grpcServiceOnly, Domain: fixtureDomain()})

	// 3. gRPC method-only match (no GRPCService), combined with header
	//    matches. Exercises the `match.GRPCService != nil` false branch and
	//    the `len(match.Headers) > 0` branch (with more than one header).
	grpcMethodOnlyHeaders := fixtureRoute("grpc-method-only-headers")
	grpcMethodOnlyHeaders.Protocol = models.RouteProtocolGRPC
	grpcMethodOnlyHeaders.Config.Matches = []models.RouteMatch{
		{
			GRPCMethod: &models.GRPCMethodMatch{Type: "Exact", Value: "Ping"},
			Headers: []models.HeaderMatch{
				{Name: "x-tenant", Type: "Exact", Value: "acme"},
				{Name: "x-trace", Type: "RegularExpression", Value: "^[0-9]+$"},
			},
		},
	}
	fixtures = append(fixtures, manifestFixture{Name: "grpc-method-only-with-headers", Route: grpcMethodOnlyHeaders, Domain: fixtureDomain()})

	// 4. Multiple gRPC matches on one route. Exercises the
	//    `for _, match := range route.Config.Matches` loop building more
	//    than one GRPCRouteRule.
	grpcMultiMatch := fixtureRoute("grpc-multi-match")
	grpcMultiMatch.Protocol = models.RouteProtocolGRPC
	grpcMultiMatch.Config.Matches = []models.RouteMatch{
		{
			GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "echo.Echo"},
			GRPCMethod:  &models.GRPCMethodMatch{Type: "Exact", Value: "Ping"},
		},
		{
			GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "echo.Echo"},
			GRPCMethod:  &models.GRPCMethodMatch{Type: "Exact", Value: "Pong"},
		},
	}
	fixtures = append(fixtures, manifestFixture{Name: "grpc-multi-match", Route: grpcMultiMatch, Domain: fixtureDomain()})

	// 5. Multiple, weighted, plain Kubernetes backends on a gRPC route.
	//    Exercises the backend loop's weight assignment and the plain
	//    (non-external, non-failover, non-TLS) BackendRef branch with more
	//    than one entry.
	grpcWeighted := fixtureRoute("grpc-weighted")
	grpcWeighted.Protocol = models.RouteProtocolGRPC
	grpcWeighted.Config.Matches = []models.RouteMatch{
		{
			GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "echo.Echo"},
			GRPCMethod:  &models.GRPCMethodMatch{Type: "Exact", Value: "Ping"},
		},
	}
	grpcWeighted.Config.Backends = []models.RouteBackend{
		{Type: models.BackendTypeKubernetes, Service: "grpc-a-svc", Namespace: "default", Port: 9000, Weight: 70},
		{Type: models.BackendTypeKubernetes, Service: "grpc-b-svc", Namespace: "default", Port: 9000, Weight: 30},
	}
	fixtures = append(fixtures, manifestFixture{Name: "grpc-weighted-backends", Route: grpcWeighted, Domain: fixtureDomain()})

	// 6. Mixed backends where one is marked Fallback (failover) and another
	//    is an external backend. HasFailover() being true flips EVERY
	//    backend -- including the plain Kubernetes one -- onto the
	//    "%s-backend-%d" / gateway.envoyproxy.io Backend external-ref
	//    branch, so this pins that all-or-nothing behaviour plus the
	//    explicit BackendTypeExternal branch in the same rule.
	grpcFailoverExternal := fixtureRoute("grpc-failover-external")
	grpcFailoverExternal.Protocol = models.RouteProtocolGRPC
	grpcFailoverExternal.Config.Matches = []models.RouteMatch{
		{
			GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "echo.Echo"},
			GRPCMethod:  &models.GRPCMethodMatch{Type: "Exact", Value: "Ping"},
		},
	}
	grpcFailoverExternal.Config.Backends = []models.RouteBackend{
		{Type: models.BackendTypeKubernetes, Service: "grpc-primary-svc", Namespace: "default", Port: 9000, Weight: 100},
		{
			Type:        models.BackendTypeExternal,
			AddressType: models.ExternalAddressTypeFQDN,
			Address:     "grpc.upstream.test",
			Port:        9443,
			Fallback:    true,
		},
	}
	fixtures = append(fixtures, manifestFixture{Name: "grpc-failover-external-backends", Route: grpcFailoverExternal, Domain: fixtureDomain()})

	// 7. No Matches defined at all, only Backends. Exercises the
	//    `len(rules) == 0 && len(route.Config.Backends) > 0` fallback
	//    branch that synthesizes a single match-all rule, and the
	//    `hasMatch == false` render path in BuildGRPCRouteObject (no
	//    Matches set on the rendered GRPCRouteRule).
	grpcNoMatch := fixtureRoute("grpc-no-match")
	grpcNoMatch.Protocol = models.RouteProtocolGRPC
	grpcNoMatch.Config.Matches = nil
	grpcNoMatch.Config.Backends = []models.RouteBackend{
		{Type: models.BackendTypeKubernetes, Service: "grpc-catchall-svc", Namespace: "default", Port: 9000},
	}
	fixtures = append(fixtures, manifestFixture{Name: "grpc-no-match-backend-only", Route: grpcNoMatch, Domain: fixtureDomain()})

	// 8. gRPC route with request/response header modifiers and a mirror.
	//    Exercises the RequestHeaderModifier, ResponseHeaderModifier, and
	//    Mirrors branches that run after the rules loop -- code paths not
	//    reached by any other gRPC fixture.
	grpcHeadersMirrors := fixtureRoute("grpc-headers-mirrors")
	grpcHeadersMirrors.Protocol = models.RouteProtocolGRPC
	grpcHeadersMirrors.Config.Matches = []models.RouteMatch{
		{
			GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "echo.Echo"},
			GRPCMethod:  &models.GRPCMethodMatch{Type: "Exact", Value: "Ping"},
		},
	}
	grpcHeadersMirrors.Config.RequestHeaderModifier = &models.HeaderModifier{
		Set: []models.HeaderValue{{Name: "x-set", Value: "a"}},
		Add: []models.HeaderValue{{Name: "x-add", Value: "b"}},
	}
	grpcHeadersMirrors.Config.ResponseHeaderModifier = &models.HeaderModifier{
		Set: []models.HeaderValue{{Name: "x-resp", Value: "c"}},
	}
	grpcHeadersMirrors.Config.Mirrors = []models.MirrorBackend{
		{Type: models.BackendTypeKubernetes, Service: "grpc-shadow-svc", Namespace: "default", Port: 9000},
	}
	fixtures = append(fixtures, manifestFixture{Name: "grpc-header-modifiers-and-mirrors", Route: grpcHeadersMirrors, Domain: fixtureDomain()})

	return fixtures
}

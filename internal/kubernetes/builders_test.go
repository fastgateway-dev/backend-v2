package kubernetes_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ─── helpers ────────────────────────────────────────────────────────────────

func k8sIntPtr(v int) *int             { return &v }
func k8sInt64Ptr(v int64) *int64       { return &v }
func k8sUint32Ptr(v uint32) *uint32    { return &v }
func k8sInt32Ptr(v int32) *int32       { return &v }
func k8sFloat32Ptr(v float32) *float32 { return &v }
func k8sStringPtr(v string) *string    { return &v }
func k8sBoolPtr(v bool) *bool          { return &v }

// Short aliases used by kubernetes_service_test.go
func int32Ptr(v int32) *int32        { return &v }
func int32PtrFromInt(v int32) *int32 { return &v }
func int64Ptr(v int64) *int64        { return &v }
func uint32Ptr(v uint32) *uint32     { return &v }
func stringPtr(v string) *string     { return &v }
func boolPtr(v bool) *bool           { return &v }

// ─── stringSliceToInterfaceSlice ─────────────────────────────────────────────
// This is a package-level unexported function, so we test it indirectly through
// the exported builders that use it. We verify behaviour via CORS, JWT etc.

// ─── BuildHTTPRouteObject ────────────────────────────────────────────────────

func TestBuildHTTPRouteObject_BasicPathMatch(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "test-route",
		Namespace:   "default",
		GatewayName: "my-gw",
		GatewayID:   "gw-uuid",
		RouteID:     "route-uuid",
		Hostname:    "example.com",
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:  "PathPrefix",
				PathValue: "/api",
				BackendRefs: []kubernetes.BackendRef{
					{Name: "svc1", Port: 8080},
				},
			},
		},
	}

	route := kubernetes.BuildHTTPRouteObject(config)
	if route == nil {
		t.Fatal("expected non-nil HTTPRoute")
	}
	if route.Name != "test-route" {
		t.Errorf("name = %s, want test-route", route.Name)
	}
	if route.Namespace != "default" {
		t.Errorf("namespace = %s, want default", route.Namespace)
	}
	if route.Labels["fastgateway.dev/route-id"] != "route-uuid" {
		t.Error("missing route-id label")
	}
	if route.Labels["fastgateway.dev/gateway-id"] != "gw-uuid" {
		t.Error("missing gateway-id label")
	}
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "example.com" {
		t.Error("unexpected hostnames")
	}
	if len(route.Spec.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(route.Spec.Rules))
	}
	rule := route.Spec.Rules[0]
	if len(rule.Matches) != 1 {
		t.Fatal("expected 1 match")
	}
	if rule.Matches[0].Path == nil {
		t.Fatal("expected path match")
	}
	if string(*rule.Matches[0].Path.Type) != "PathPrefix" {
		t.Errorf("path type = %s, want PathPrefix", string(*rule.Matches[0].Path.Type))
	}
	if *rule.Matches[0].Path.Value != "/api" {
		t.Errorf("path value = %s, want /api", *rule.Matches[0].Path.Value)
	}
	if len(rule.BackendRefs) != 1 {
		t.Fatal("expected 1 backend ref")
	}
	if string(rule.BackendRefs[0].Name) != "svc1" {
		t.Error("wrong backend name")
	}
}

func TestBuildHTTPRouteObject_MethodMatch(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "method-route",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		Rules: []kubernetes.HTTPRouteRule{
			{
				Method: "GET",
				BackendRefs: []kubernetes.BackendRef{
					{Name: "svc", Port: 80},
				},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	if route.Spec.Rules[0].Matches[0].Method == nil {
		t.Fatal("expected method match")
	}
	if string(*route.Spec.Rules[0].Matches[0].Method) != "GET" {
		t.Error("wrong method")
	}
}

func TestBuildHTTPRouteObject_HeaderMatch(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "hdr-route",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		Rules: []kubernetes.HTTPRouteRule{
			{
				Headers: []kubernetes.HeaderMatch{
					{Name: "X-Custom", Type: "Exact", Value: "foo"},
					{Name: "X-Re", Type: "RegularExpression", Value: "bar.*"},
				},
				BackendRefs: []kubernetes.BackendRef{{Name: "svc", Port: 80}},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	hdrs := route.Spec.Rules[0].Matches[0].Headers
	if len(hdrs) != 2 {
		t.Fatalf("expected 2 header matches, got %d", len(hdrs))
	}
	if string(hdrs[0].Name) != "X-Custom" || hdrs[0].Value != "foo" {
		t.Error("first header match wrong")
	}
}

func TestBuildHTTPRouteObject_QueryParamMatch(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "qp-route",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		Rules: []kubernetes.HTTPRouteRule{
			{
				QueryParams: []kubernetes.QueryParamMatch{
					{Name: "version", Type: "Exact", Value: "v2"},
				},
				BackendRefs: []kubernetes.BackendRef{{Name: "svc", Port: 80}},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	qps := route.Spec.Rules[0].Matches[0].QueryParams
	if len(qps) != 1 {
		t.Fatalf("expected 1 query param match, got %d", len(qps))
	}
	if string(qps[0].Name) != "version" || qps[0].Value != "v2" {
		t.Error("query param match wrong")
	}
}

func TestBuildHTTPRouteObject_MultipleBackendsWithWeight(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "multi-be",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:  "PathPrefix",
				PathValue: "/",
				BackendRefs: []kubernetes.BackendRef{
					{Name: "svc1", Namespace: "ns1", Port: 8080, Weight: 80},
					{Name: "svc2", Port: 8081, Weight: 20},
				},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	refs := route.Spec.Rules[0].BackendRefs
	if len(refs) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(refs))
	}
	if refs[0].BackendRef.Weight == nil || *refs[0].BackendRef.Weight != 80 {
		t.Error("first backend weight wrong")
	}
	if refs[0].BackendRef.Namespace == nil {
		t.Error("first backend namespace should be set")
	}
}

func TestBuildHTTPRouteObject_ExternalBackend(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "ext-be",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:  "Exact",
				PathValue: "/ext",
				BackendRefs: []kubernetes.BackendRef{
					{
						Name:       "ext-backend",
						Port:       443,
						IsExternal: true,
						Group:      "gateway.envoyproxy.io",
						Kind:       "Backend",
					},
				},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	ref := route.Spec.Rules[0].BackendRefs[0]
	if ref.BackendRef.Group == nil || string(*ref.BackendRef.Group) != "gateway.envoyproxy.io" {
		t.Error("external backend group wrong")
	}
	if ref.BackendRef.Kind == nil || string(*ref.BackendRef.Kind) != "Backend" {
		t.Error("external backend kind wrong")
	}
}

func TestBuildHTTPRouteObject_Redirect(t *testing.T) {
	port := 443
	config := &kubernetes.HTTPRouteConfig{
		Name:        "redir",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		Redirect: &kubernetes.HTTPRedirectConfig{
			Scheme:     "https",
			Hostname:   "new.com",
			Port:       &port,
			StatusCode: 301,
			Path: &kubernetes.HTTPPathRewrite{
				Type:            "ReplaceFullPath",
				ReplaceFullPath: "/new-path",
			},
		},
		Rules: []kubernetes.HTTPRouteRule{
			{PathType: "PathPrefix", PathValue: "/old"},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	filters := route.Spec.Rules[0].Filters
	found := false
	for _, f := range filters {
		if f.Type == gatewayv1.HTTPRouteFilterRequestRedirect {
			found = true
			if f.RequestRedirect.Scheme == nil || *f.RequestRedirect.Scheme != "https" {
				t.Error("redirect scheme wrong")
			}
			if f.RequestRedirect.StatusCode == nil || *f.RequestRedirect.StatusCode != 301 {
				t.Error("redirect status wrong")
			}
			if f.RequestRedirect.Path == nil || f.RequestRedirect.Path.ReplaceFullPath == nil {
				t.Error("redirect path wrong")
			}
		}
	}
	if !found {
		t.Error("no redirect filter found")
	}
	// Redirect routes should have no backend refs
	if len(route.Spec.Rules[0].BackendRefs) != 0 {
		t.Error("redirect route should have no backends")
	}
}

func TestBuildHTTPRouteObject_DirectResponse(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:                "dr-route",
		Namespace:           "ns",
		GatewayName:         "gw",
		Hostname:            "x.com",
		HTTPRouteFilterName: "my-filter",
		Rules: []kubernetes.HTTPRouteRule{
			{PathType: "PathPrefix", PathValue: "/static"},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	filters := route.Spec.Rules[0].Filters
	found := false
	for _, f := range filters {
		if f.Type == gatewayv1.HTTPRouteFilterExtensionRef {
			found = true
			if string(f.ExtensionRef.Name) != "my-filter" {
				t.Error("extension ref name wrong")
			}
		}
	}
	if !found {
		t.Error("no extension ref filter found")
	}
	if len(route.Spec.Rules[0].BackendRefs) != 0 {
		t.Error("direct response route should have no backends")
	}
}

func TestBuildHTTPRouteObject_MirrorFilters(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "mirror-route",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		Mirrors: []kubernetes.MirrorRef{
			{Name: "mirror-svc", Namespace: "mirror-ns", Port: 9090},
		},
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:    "PathPrefix",
				PathValue:   "/",
				BackendRefs: []kubernetes.BackendRef{{Name: "svc", Port: 80}},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	filters := route.Spec.Rules[0].Filters
	found := false
	for _, f := range filters {
		if f.Type == gatewayv1.HTTPRouteFilterRequestMirror {
			found = true
			if string(f.RequestMirror.BackendRef.Name) != "mirror-svc" {
				t.Error("mirror backend name wrong")
			}
		}
	}
	if !found {
		t.Error("no mirror filter found")
	}
}

func TestBuildHTTPRouteObject_HeaderModifiers(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "hmod-route",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		RequestHeaderModifier: &kubernetes.HTTPHeaderModifier{
			Set:    []kubernetes.HTTPHeaderValue{{Name: "X-Set", Value: "val"}},
			Add:    []kubernetes.HTTPHeaderValue{{Name: "X-Add", Value: "added"}},
			Remove: []string{"X-Remove"},
		},
		ResponseHeaderModifier: &kubernetes.HTTPHeaderModifier{
			Set: []kubernetes.HTTPHeaderValue{{Name: "X-Resp", Value: "resp"}},
		},
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:    "PathPrefix",
				PathValue:   "/",
				BackendRefs: []kubernetes.BackendRef{{Name: "svc", Port: 80}},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	filters := route.Spec.Rules[0].Filters
	hasReq, hasResp := false, false
	for _, f := range filters {
		if f.Type == gatewayv1.HTTPRouteFilterRequestHeaderModifier {
			hasReq = true
			if len(f.RequestHeaderModifier.Set) != 1 {
				t.Error("request header set wrong")
			}
			if len(f.RequestHeaderModifier.Add) != 1 {
				t.Error("request header add wrong")
			}
			if len(f.RequestHeaderModifier.Remove) != 1 {
				t.Error("request header remove wrong")
			}
		}
		if f.Type == gatewayv1.HTTPRouteFilterResponseHeaderModifier {
			hasResp = true
		}
	}
	if !hasReq {
		t.Error("missing request header modifier filter")
	}
	if !hasResp {
		t.Error("missing response header modifier filter")
	}
}

func TestBuildHTTPRouteObject_URLRewrite(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "rewrite-route",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		URLRewrite: &kubernetes.HTTPURLRewrite{
			Hostname: k8sStringPtr("new-host.com"),
			Path: &kubernetes.HTTPPathRewrite{
				Type:               "ReplacePrefixMatch",
				ReplacePrefixMatch: "/new",
			},
		},
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:    "PathPrefix",
				PathValue:   "/old",
				BackendRefs: []kubernetes.BackendRef{{Name: "svc", Port: 80}},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	found := false
	for _, f := range route.Spec.Rules[0].Filters {
		if f.Type == gatewayv1.HTTPRouteFilterURLRewrite {
			found = true
			if f.URLRewrite.Hostname == nil || string(*f.URLRewrite.Hostname) != "new-host.com" {
				t.Error("rewrite hostname wrong")
			}
			if f.URLRewrite.Path == nil {
				t.Fatal("rewrite path nil")
			}
			if f.URLRewrite.Path.ReplacePrefixMatch == nil || *f.URLRewrite.Path.ReplacePrefixMatch != "/new" {
				t.Error("rewrite prefix wrong")
			}
		}
	}
	if !found {
		t.Error("no URL rewrite filter found")
	}
}

func TestBuildHTTPRouteObject_NoMatchNoBackend(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "empty-rule",
		Namespace:   "ns",
		GatewayName: "gw",
		Hostname:    "x.com",
		Rules: []kubernetes.HTTPRouteRule{
			{}, // no path, no method, no headers, no query params, no backends
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	if len(route.Spec.Rules[0].Matches) != 0 {
		t.Error("expected no matches for empty rule")
	}
}

func TestBuildHTTPRouteObject_TypeMeta(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name: "tm", Namespace: "ns", GatewayName: "gw", Hostname: "x.com",
		Rules: []kubernetes.HTTPRouteRule{{BackendRefs: []kubernetes.BackendRef{{Name: "s", Port: 80}}}},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	if route.TypeMeta.Kind != "HTTPRoute" {
		t.Errorf("kind = %s, want HTTPRoute", route.TypeMeta.Kind)
	}
	if route.TypeMeta.APIVersion != "gateway.networking.k8s.io/v1" {
		t.Error("wrong api version")
	}
}

// ─── BuildGRPCRouteObject ───────────────────────────────────────────────────

func TestBuildGRPCRouteObject_Nil(t *testing.T) {
	if kubernetes.BuildGRPCRouteObject(nil) != nil {
		t.Error("expected nil for nil config")
	}
}

func TestBuildGRPCRouteObject_ServiceAndMethodMatch(t *testing.T) {
	config := &kubernetes.GRPCRouteConfig{
		Name:        "grpc-route",
		Namespace:   "ns",
		GatewayName: "gw",
		GatewayID:   "gw-id",
		RouteID:     "route-id",
		Hostname:    "grpc.example.com",
		Rules: []kubernetes.GRPCRouteRule{
			{
				GRPCService: &kubernetes.GRPCMethodMatchConfig{Type: "Exact", Value: "my.Service"},
				GRPCMethod:  &kubernetes.GRPCMethodMatchConfig{Type: "Exact", Value: "GetItem"},
				BackendRefs: []kubernetes.BackendRef{{Name: "grpc-svc", Port: 50051}},
			},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	if route == nil {
		t.Fatal("expected non-nil GRPCRoute")
	}
	if route.Kind != "GRPCRoute" {
		t.Error("wrong kind")
	}
	if len(route.Spec.Rules) != 1 {
		t.Fatal("expected 1 rule")
	}
	match := route.Spec.Rules[0].Matches[0]
	if match.Method == nil {
		t.Fatal("expected method match")
	}
	if match.Method.Service == nil || *match.Method.Service != "my.Service" {
		t.Error("service match wrong")
	}
	if match.Method.Method == nil || *match.Method.Method != "GetItem" {
		t.Error("method match wrong")
	}
}

func TestBuildGRPCRouteObject_HeaderMatch(t *testing.T) {
	config := &kubernetes.GRPCRouteConfig{
		Name: "grpc-hdr", Namespace: "ns", GatewayName: "gw", Hostname: "h.com",
		Rules: []kubernetes.GRPCRouteRule{
			{
				Headers:     []kubernetes.HeaderMatch{{Name: "x-tenant", Type: "Exact", Value: "acme"}},
				BackendRefs: []kubernetes.BackendRef{{Name: "svc", Port: 50051}},
			},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	hdrs := route.Spec.Rules[0].Matches[0].Headers
	if len(hdrs) != 1 || string(hdrs[0].Name) != "x-tenant" {
		t.Error("header match wrong")
	}
}

func TestBuildGRPCRouteObject_Mirrors(t *testing.T) {
	config := &kubernetes.GRPCRouteConfig{
		Name: "grpc-mirror", Namespace: "ns", GatewayName: "gw", Hostname: "h.com",
		Mirrors: []kubernetes.MirrorRef{{Name: "mirror", Namespace: "mns", Port: 50052}},
		Rules: []kubernetes.GRPCRouteRule{
			{BackendRefs: []kubernetes.BackendRef{{Name: "svc", Port: 50051}}},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	found := false
	for _, f := range route.Spec.Rules[0].Filters {
		if f.Type == gatewayv1.GRPCRouteFilterRequestMirror {
			found = true
		}
	}
	if !found {
		t.Error("no mirror filter found")
	}
}

func TestBuildGRPCRouteObject_RequestHeaderModifier(t *testing.T) {
	config := &kubernetes.GRPCRouteConfig{
		Name: "grpc-hmod", Namespace: "ns", GatewayName: "gw", Hostname: "h.com",
		RequestHeaderModifier: &kubernetes.HTTPHeaderModifier{
			Set: []kubernetes.HTTPHeaderValue{{Name: "X-Set", Value: "val"}},
		},
		Rules: []kubernetes.GRPCRouteRule{
			{BackendRefs: []kubernetes.BackendRef{{Name: "svc", Port: 50051}}},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	found := false
	for _, f := range route.Spec.Rules[0].Filters {
		if f.Type == gatewayv1.GRPCRouteFilterRequestHeaderModifier {
			found = true
		}
	}
	if !found {
		t.Error("no request header modifier filter")
	}
}

func TestBuildGRPCRouteObject_ExternalBackend(t *testing.T) {
	config := &kubernetes.GRPCRouteConfig{
		Name: "grpc-ext", Namespace: "ns", GatewayName: "gw", Hostname: "h.com",
		Rules: []kubernetes.GRPCRouteRule{
			{
				BackendRefs: []kubernetes.BackendRef{
					{Name: "ext", Port: 443, IsExternal: true, Group: "gateway.envoyproxy.io", Kind: "Backend"},
				},
			},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	ref := route.Spec.Rules[0].BackendRefs[0]
	if ref.BackendRef.Group == nil || string(*ref.BackendRef.Group) != "gateway.envoyproxy.io" {
		t.Error("external group wrong")
	}
}

func TestBuildGRPCRouteObject_DefaultMatchType(t *testing.T) {
	// When Type is empty, it should default to Exact
	config := &kubernetes.GRPCRouteConfig{
		Name: "grpc-default", Namespace: "ns", GatewayName: "gw", Hostname: "h.com",
		Rules: []kubernetes.GRPCRouteRule{
			{
				GRPCService: &kubernetes.GRPCMethodMatchConfig{Type: "", Value: "my.Svc"},
				BackendRefs: []kubernetes.BackendRef{{Name: "svc", Port: 50051}},
			},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	match := route.Spec.Rules[0].Matches[0]
	if match.Method.Type == nil || *match.Method.Type != gatewayv1.GRPCMethodMatchExact {
		t.Error("expected default Exact match type")
	}
}

// ─── BuildSecurityPolicy ────────────────────────────────────────────────────

func TestBuildSecurityPolicy_Nil(t *testing.T) {
	if kubernetes.BuildSecurityPolicy(nil) != nil {
		t.Error("expected nil")
	}
}

func TestBuildSecurityPolicy_NoFeatures(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
	}
	if kubernetes.BuildSecurityPolicy(config) != nil {
		t.Error("expected nil when no features")
	}
}

func TestBuildSecurityPolicy_CORSOnly(t *testing.T) {
	maxAge := 3600
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-cors",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		CORS: &kubernetes.CORSPolicyConfig{
			AllowOrigins:     []string{"http://example.com", "http://*.foo.com"},
			AllowMethods:     []string{"GET", "POST"},
			AllowHeaders:     []string{"Authorization"},
			ExposeHeaders:    []string{"X-Custom"},
			MaxAge:           &maxAge,
			AllowCredentials: k8sBoolPtr(true),
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil SecurityPolicy")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	cors, ok := spec["cors"].(map[string]interface{})
	if !ok {
		t.Fatal("missing cors in spec")
	}
	origins := cors["allowOrigins"].([]interface{})
	if len(origins) != 2 {
		t.Errorf("expected 2 origins, got %d", len(origins))
	}
	if cors["maxAge"] != "3600s" {
		t.Errorf("maxAge = %v, want 3600s", cors["maxAge"])
	}
	if cors["allowCredentials"] != true {
		t.Error("allowCredentials wrong")
	}
}

func TestBuildSecurityPolicy_CORSEmptyOrigins(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-cors-empty",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		CORS:      &kubernetes.CORSPolicyConfig{}, // empty - no origins, methods, headers
	}
	if kubernetes.BuildSecurityPolicy(config) != nil {
		t.Error("expected nil when CORS has no config")
	}
}

func TestBuildSecurityPolicy_JWTAuth(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-jwt",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		JWT: &kubernetes.JWTAuthPolicyConfig{
			Providers: []kubernetes.JWTProviderPolicyConfig{
				{
					Name:      "my-provider",
					Issuer:    "https://issuer.example.com",
					JWKSURL:   "https://issuer.example.com/.well-known/jwks.json",
					Audiences: []string{"my-api"},
					ClaimToHeaders: []kubernetes.JWTClaimToHeaderPolicyConfig{
						{Claim: "sub", Header: "X-User"},
					},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	jwt := spec["jwt"].(map[string]interface{})
	providers := jwt["providers"].([]interface{})
	if len(providers) != 1 {
		t.Fatal("expected 1 provider")
	}
	p := providers[0].(map[string]interface{})
	if p["name"] != "my-provider" {
		t.Error("provider name wrong")
	}
	if p["issuer"] != "https://issuer.example.com" {
		t.Error("issuer wrong")
	}
	remoteJWKS := p["remoteJWKS"].(map[string]interface{})
	if remoteJWKS["uri"] != "https://issuer.example.com/.well-known/jwks.json" {
		t.Error("JWKS URL wrong")
	}
	claimToHeaders := p["claimToHeaders"].([]interface{})
	if len(claimToHeaders) != 1 {
		t.Error("claimToHeaders wrong")
	}
}

func TestBuildSecurityPolicy_JWTNoProviders(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-jwt-empty",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		JWT:       &kubernetes.JWTAuthPolicyConfig{},
	}
	if kubernetes.BuildSecurityPolicy(config) != nil {
		t.Error("expected nil when JWT has no providers")
	}
}

func TestBuildSecurityPolicy_APIKeyAuth(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-apikey",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		APIKeyAuth: &kubernetes.APIKeyAuthPolicyConfig{
			CredentialRefs: []kubernetes.SecretRefConfig{
				{Name: "secret1", Namespace: "ns"},
				{Name: "secret2"},
			},
			ExtractFrom: []kubernetes.APIKeyExtractFromConfig{
				{Headers: []string{"X-API-Key", "Authorization"}},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	apiKeyAuth := spec["apiKeyAuth"].(map[string]interface{})
	refs := apiKeyAuth["credentialRefs"].([]interface{})
	if len(refs) != 2 {
		t.Fatalf("expected 2 credential refs, got %d", len(refs))
	}
	// First ref has namespace
	r0 := refs[0].(map[string]interface{})
	if r0["namespace"] != "ns" {
		t.Error("first ref namespace wrong")
	}
	// Second ref has no namespace
	r1 := refs[1].(map[string]interface{})
	if _, ok := r1["namespace"]; ok {
		t.Error("second ref should not have namespace")
	}
	extractFrom := apiKeyAuth["extractFrom"].([]interface{})
	if len(extractFrom) != 1 {
		t.Error("extractFrom wrong")
	}
}

func TestBuildSecurityPolicy_APIKeyNoCredentialRefs(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:       "sp-apikey-empty",
		Namespace:  "ns",
		TargetRef:  kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		APIKeyAuth: &kubernetes.APIKeyAuthPolicyConfig{},
	}
	if kubernetes.BuildSecurityPolicy(config) != nil {
		t.Error("expected nil when API key has no credential refs")
	}
}

func TestBuildSecurityPolicy_OIDC(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-oidc",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		OIDC: &kubernetes.OIDCPolicyConfig{
			Issuer:           "https://accounts.google.com",
			ClientID:         "my-client-id",
			ClientSecretName: "oidc-secret",
			ClientSecretNS:   "ns",
			RedirectURL:      "https://example.com/callback",
			LogoutPath:       "/logout",
			Scopes:           []string{"openid", "profile"},
			CookieDomain:     "example.com",
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	oidc := spec["oidc"].(map[string]interface{})
	if oidc["clientID"] != "my-client-id" {
		t.Error("clientID wrong")
	}
	if oidc["redirectURL"] != "https://example.com/callback" {
		t.Error("redirect URL wrong")
	}
	if oidc["cookieDomain"] != "example.com" {
		t.Error("cookie domain wrong")
	}
	scopes := oidc["scopes"].([]interface{})
	if len(scopes) != 2 {
		t.Error("scopes wrong")
	}
}

func TestBuildSecurityPolicy_Authorization_IPAllowlist(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-auth",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action:      "Allow",
					ClientCIDRs: []string{"10.0.0.0/8", "192.168.1.0/24"},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	if auth["defaultAction"] != "Deny" {
		t.Error("default action wrong")
	}
	rules := auth["rules"].([]interface{})
	if len(rules) != 1 {
		t.Fatal("expected 1 rule")
	}
	rule := rules[0].(map[string]interface{})
	if rule["action"] != "Allow" {
		t.Error("action wrong")
	}
	principal := rule["principal"].(map[string]interface{})
	cidrs := principal["clientCIDRs"].([]interface{})
	if len(cidrs) != 2 {
		t.Error("expected 2 CIDRs")
	}
}

func TestBuildSecurityPolicy_Authorization_DenyAllNoRules(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-deny-all",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules:         []kubernetes.AuthorizationRulePolicyConfig{},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil for deny-all")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	if auth["defaultAction"] != "Deny" {
		t.Error("default action wrong")
	}
	if _, ok := auth["rules"]; ok {
		t.Error("deny-all should not have rules key")
	}
}

func TestBuildSecurityPolicy_Authorization_JWTClaims(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-jwt-claims",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action: "Allow",
					JWT: &kubernetes.JWTPrincipalPolicyConfig{
						Provider: "my-provider",
						Claims: []kubernetes.JWTClaimRulePolicyConfig{
							{Name: "role", Values: []string{"admin"}, ValueType: ""},
							{Name: "scope", Values: []string{"read"}, ValueType: "Exact"},
						},
					},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	rules := auth["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	principal := rule["principal"].(map[string]interface{})
	jwtPrincipal := principal["jwt"].(map[string]interface{})
	if jwtPrincipal["provider"] != "my-provider" {
		t.Error("JWT provider wrong")
	}
	claims := jwtPrincipal["claims"].([]interface{})
	if len(claims) != 2 {
		t.Fatal("expected 2 claims")
	}
	// "scope" claim should always be StringArray regardless of input valueType
	scopeClaim := claims[1].(map[string]interface{})
	if scopeClaim["valueType"] != "StringArray" {
		t.Errorf("scope claim valueType = %v, want StringArray", scopeClaim["valueType"])
	}
	// "role" claim with empty valueType should not have valueType key
	roleClaim := claims[0].(map[string]interface{})
	if _, ok := roleClaim["valueType"]; ok {
		t.Error("role claim should not have valueType for default/empty")
	}
}

func TestBuildSecurityPolicy_ExtAuth_HTTP(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-extauth",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		ExtAuth: &models.ExtAuthConfig{
			Type: "http",
			HTTP: &models.ExtAuthHTTPConfig{
				BackendRef:       models.ExtAuthBackendRef{Name: "auth-svc", Namespace: "auth-ns", Port: 9090},
				Path:             "/check",
				HeadersToBackend: []string{"Authorization", "Cookie"},
			},
			FailOpen:         k8sBoolPtr(true),
			HeadersToExtAuth: []string{"Authorization"},
			WithRequestBody:  &models.ExtAuthRequestBody{MaxBytes: 1024},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	extAuth := spec["extAuth"].(map[string]interface{})
	if extAuth["failOpen"] != true {
		t.Error("failOpen wrong")
	}
	httpConf := extAuth["http"].(map[string]interface{})
	if httpConf["path"] != "/check" {
		t.Error("path wrong")
	}
	headersToBackend := httpConf["headersToBackend"].([]interface{})
	if len(headersToBackend) != 2 {
		t.Error("headersToBackend wrong")
	}
	bodyToExtAuth := extAuth["bodyToExtAuth"].(map[string]interface{})
	if bodyToExtAuth["maxRequestBytes"] != uint32(1024) {
		t.Errorf("maxRequestBytes = %v", bodyToExtAuth["maxRequestBytes"])
	}
}

func TestBuildSecurityPolicy_ExtAuth_GRPC(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-extauth-grpc",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		ExtAuth: &models.ExtAuthConfig{
			Type: "grpc",
			GRPC: &models.ExtAuthGRPCConfig{
				BackendRef: models.ExtAuthBackendRef{Name: "grpc-auth", Port: 50051},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	extAuth := spec["extAuth"].(map[string]interface{})
	grpcConf := extAuth["grpc"].(map[string]interface{})
	refs := grpcConf["backendRefs"].([]interface{})
	if len(refs) != 1 {
		t.Error("expected 1 backend ref")
	}
}

func TestBuildSecurityPolicy_CombinedCORSAndJWT(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-combined",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		CORS: &kubernetes.CORSPolicyConfig{
			AllowOrigins: []string{"*"},
			AllowMethods: []string{"GET"},
		},
		JWT: &kubernetes.JWTAuthPolicyConfig{
			Providers: []kubernetes.JWTProviderPolicyConfig{
				{Name: "p1", Issuer: "https://issuer.com", JWKSURL: "https://issuer.com/jwks"},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	if _, ok := spec["cors"]; !ok {
		t.Error("missing cors")
	}
	if _, ok := spec["jwt"]; !ok {
		t.Error("missing jwt")
	}
}

func TestBuildSecurityPolicy_Metadata(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-meta",
		Namespace: "test-ns",
		GatewayID: "gw-123",
		RouteID:   "rt-456",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		CORS: &kubernetes.CORSPolicyConfig{
			AllowOrigins: []string{"*"},
			AllowMethods: []string{"GET"},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	meta := sp.Object["metadata"].(map[string]interface{})
	if meta["name"] != "sp-meta" {
		t.Error("name wrong")
	}
	if meta["namespace"] != "test-ns" {
		t.Error("namespace wrong")
	}
	labels := meta["labels"].(map[string]interface{})
	if labels["fastgateway.dev/route-id"] != "rt-456" {
		t.Error("route-id label wrong")
	}
	if sp.Object["apiVersion"] != "gateway.envoyproxy.io/v1alpha1" {
		t.Error("api version wrong")
	}
	if sp.Object["kind"] != "SecurityPolicy" {
		t.Error("kind wrong")
	}
}

// ─── BuildBackendTrafficPolicy ──────────────────────────────────────────────

func TestBuildBackendTrafficPolicy_Nil(t *testing.T) {
	if kubernetes.BuildBackendTrafficPolicy(nil) != nil {
		t.Error("expected nil")
	}
}

func TestBuildBackendTrafficPolicy_NoFeatures(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
	}
	if kubernetes.BuildBackendTrafficPolicy(config) != nil {
		t.Error("expected nil when no features")
	}
}

func TestBuildBackendTrafficPolicy_Retry(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-retry",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Retry: &kubernetes.RetryPolicyConfig{
			NumRetries: k8sInt32Ptr(3),
			RetryOn: &kubernetes.RetryOnPolicyConfig{
				HTTPStatusCodes: []int{502, 503},
				Triggers:        []string{"connect-failure", "reset"},
			},
			PerRetry: &kubernetes.PerRetryPolicyConfig{
				Timeout: k8sStringPtr("5s"),
				BackOff: &kubernetes.BackOffPolicyConfig{
					BaseInterval: k8sStringPtr("1s"),
					MaxInterval:  k8sStringPtr("10s"),
				},
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	if btp == nil {
		t.Fatal("expected non-nil")
	}
	spec := btp.Object["spec"].(map[string]interface{})
	retry := spec["retry"].(map[string]interface{})
	if retry["numRetries"] != int32(3) {
		t.Error("numRetries wrong")
	}
	retryOn := retry["retryOn"].(map[string]interface{})
	codes := retryOn["httpStatusCodes"].([]interface{})
	if len(codes) != 2 {
		t.Error("httpStatusCodes wrong")
	}
	perRetry := retry["perRetry"].(map[string]interface{})
	if perRetry["timeout"] != "5s" {
		t.Error("timeout wrong")
	}
	backOff := perRetry["backOff"].(map[string]interface{})
	if backOff["baseInterval"] != "1s" {
		t.Error("baseInterval wrong")
	}
}

func TestBuildBackendTrafficPolicy_RateLimit(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-rl",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		RateLimit: &kubernetes.RateLimitPolicyConfig{
			Global: &kubernetes.GlobalRateLimitPolicyConfig{
				Rules: []kubernetes.RateLimitRulePolicyConfig{
					{
						Limit: kubernetes.RateLimitValuePolicyConfig{Requests: 100, Unit: "Minute"},
						ClientSelectors: []kubernetes.RateLimitSelectorPolicyConfig{
							{
								Headers: []kubernetes.RateLimitHeaderMatchPolicyConfig{
									{Name: "X-Client", Value: "premium", Type: "Exact", Invert: false},
								},
								SourceCIDR: &kubernetes.RateLimitSourceCIDRPolicyConfig{Value: "10.0.0.0/8", Type: "Distinct"},
								Path:       &kubernetes.RateLimitPathMatchPolicyConfig{Value: "/api", Type: "PathPrefix"},
								Methods:    []string{"GET", "POST"},
							},
						},
					},
				},
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	if btp == nil {
		t.Fatal("expected non-nil")
	}
	spec := btp.Object["spec"].(map[string]interface{})
	rl := spec["rateLimit"].(map[string]interface{})
	global := rl["global"].(map[string]interface{})
	rules := global["rules"].([]interface{})
	if len(rules) != 1 {
		t.Fatal("expected 1 rule")
	}
	rule := rules[0].(map[string]interface{})
	limit := rule["limit"].(map[string]interface{})
	if limit["requests"] != int64(100) {
		t.Error("requests wrong")
	}
	if limit["unit"] != "Minute" {
		t.Error("unit wrong")
	}
	selectors := rule["clientSelectors"].([]interface{})
	sel := selectors[0].(map[string]interface{})
	cidr := sel["sourceCIDR"].(map[string]interface{})
	if cidr["value"] != "10.0.0.0/8" {
		t.Error("sourceCIDR value wrong")
	}
}

func TestBuildBackendTrafficPolicy_Timeout(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-timeout",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Timeout: &kubernetes.BTPTimeoutPolicyConfig{
			TCP: &kubernetes.BTPTCPTimeoutPolicyConfig{ConnectTimeout: "5s"},
			HTTP: &kubernetes.BTPHTTPTimeoutPolicyConfig{
				RequestTimeout:        "30s",
				ConnectionIdleTimeout: "60s",
				MaxConnectionDuration: "120s",
				MaxStreamDuration:     "90s",
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	if btp == nil {
		t.Fatal("expected non-nil")
	}
	spec := btp.Object["spec"].(map[string]interface{})
	timeout := spec["timeout"].(map[string]interface{})
	tcp := timeout["tcp"].(map[string]interface{})
	if tcp["connectTimeout"] != "5s" {
		t.Error("tcp connect timeout wrong")
	}
	http := timeout["http"].(map[string]interface{})
	if http["requestTimeout"] != "30s" {
		t.Error("http request timeout wrong")
	}
	if http["maxStreamDuration"] != "90s" {
		t.Error("http max stream duration wrong")
	}
}

func TestBuildBackendTrafficPolicy_CircuitBreaker(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-cb",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		CircuitBreaker: &kubernetes.CircuitBreakerPolicyConfig{
			MaxConnections:           k8sInt64Ptr(1024),
			MaxPendingRequests:       k8sInt64Ptr(128),
			MaxParallelRequests:      k8sInt64Ptr(64),
			MaxParallelRetries:       k8sInt64Ptr(32),
			MaxRequestsPerConnection: k8sInt64Ptr(100),
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	if btp == nil {
		t.Fatal("expected non-nil")
	}
	spec := btp.Object["spec"].(map[string]interface{})
	cb := spec["circuitBreaker"].(map[string]interface{})
	if cb["maxConnections"] != int64(1024) {
		t.Error("maxConnections wrong")
	}
	if cb["maxPendingRequests"] != int64(128) {
		t.Error("maxPendingRequests wrong")
	}
}

func TestBuildBackendTrafficPolicy_HealthCheck_HTTP(t *testing.T) {
	method := "GET"
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-hc",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		HealthCheck: &kubernetes.HealthCheckPolicyConfig{
			Active: &kubernetes.ActiveHealthCheckPolicyConfig{
				Timeout:            k8sStringPtr("5s"),
				Interval:           k8sStringPtr("10s"),
				UnhealthyThreshold: k8sUint32Ptr(3),
				HealthyThreshold:   k8sUint32Ptr(2),
				Type:               "HTTP",
				HTTP: &kubernetes.HTTPActiveHealthCheckPolicyConfig{
					Path:             "/health",
					Method:           &method,
					ExpectedStatuses: []int{200, 204},
				},
			},
			PanicThreshold: k8sUint32Ptr(50),
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	hc := spec["healthCheck"].(map[string]interface{})
	active := hc["active"].(map[string]interface{})
	if active["type"] != "HTTP" {
		t.Error("type wrong")
	}
	if active["timeout"] != "5s" {
		t.Error("timeout wrong")
	}
	httpHC := active["http"].(map[string]interface{})
	if httpHC["path"] != "/health" {
		t.Error("path wrong")
	}
	if httpHC["method"] != "GET" {
		t.Error("method wrong")
	}
	statuses := httpHC["expectedStatuses"].([]interface{})
	if len(statuses) != 2 {
		t.Error("expectedStatuses wrong")
	}
	if hc["panicThreshold"] != uint32(50) {
		t.Error("panicThreshold wrong")
	}
}

func TestBuildBackendTrafficPolicy_HealthCheck_TCP(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-hc-tcp",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		HealthCheck: &kubernetes.HealthCheckPolicyConfig{
			Active: &kubernetes.ActiveHealthCheckPolicyConfig{
				Type: "TCP",
				TCP: &kubernetes.TCPActiveHealthCheckPolicyConfig{
					SendText:    k8sStringPtr("ping"),
					ReceiveText: k8sStringPtr("pong"),
				},
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	hc := spec["healthCheck"].(map[string]interface{})
	active := hc["active"].(map[string]interface{})
	tcp := active["tcp"].(map[string]interface{})
	send := tcp["send"].(map[string]interface{})
	if send["text"] != "ping" {
		t.Error("send text wrong")
	}
	recv := tcp["receive"].(map[string]interface{})
	if recv["text"] != "pong" {
		t.Error("receive text wrong")
	}
}

func TestBuildBackendTrafficPolicy_HealthCheck_GRPC(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-hc-grpc",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		HealthCheck: &kubernetes.HealthCheckPolicyConfig{
			Active: &kubernetes.ActiveHealthCheckPolicyConfig{
				Type: "GRPC",
				GRPC: &kubernetes.GRPCActiveHealthCheckPolicyConfig{
					Service: k8sStringPtr("grpc.health.v1.Health"),
				},
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	hc := spec["healthCheck"].(map[string]interface{})
	active := hc["active"].(map[string]interface{})
	grpc := active["grpc"].(map[string]interface{})
	if grpc["service"] != "grpc.health.v1.Health" {
		t.Error("grpc service wrong")
	}
}

func TestBuildBackendTrafficPolicy_HealthCheck_Passive(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-hc-passive",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		HealthCheck: &kubernetes.HealthCheckPolicyConfig{
			Passive: &kubernetes.PassiveHealthCheckPolicyConfig{
				ConsecutiveGatewayErrors:       k8sUint32Ptr(5),
				Consecutive5xxErrors:           k8sUint32Ptr(3),
				Interval:                       k8sStringPtr("30s"),
				BaseEjectionTime:               k8sStringPtr("60s"),
				MaxEjectionPercent:             k8sInt32Ptr(50),
				SplitExternalLocalOriginErrors: k8sBoolPtr(true),
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	hc := spec["healthCheck"].(map[string]interface{})
	passive := hc["passive"].(map[string]interface{})
	if passive["consecutiveGatewayErrors"] != uint32(5) {
		t.Error("consecutiveGatewayErrors wrong")
	}
	if passive["consecutive5XxErrors"] != uint32(3) {
		t.Error("consecutive5XxErrors wrong")
	}
	if passive["splitExternalLocalOriginErrors"] != true {
		t.Error("splitExternalLocalOriginErrors wrong")
	}
}

func TestBuildBackendTrafficPolicy_LoadBalancer_RoundRobin(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-lb",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		LoadBalancer: &kubernetes.LoadBalancerPolicyConfig{
			Type: "RoundRobin",
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	lb := spec["loadBalancer"].(map[string]interface{})
	if lb["type"] != "RoundRobin" {
		t.Error("type wrong")
	}
}

func TestBuildBackendTrafficPolicy_LoadBalancer_ConsistentHash_Header(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-lb-ch",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		LoadBalancer: &kubernetes.LoadBalancerPolicyConfig{
			Type: "ConsistentHash",
			ConsistentHash: &kubernetes.ConsistentHashPolicyConfig{
				Type: "Header",
				Header: &kubernetes.ConsistentHashHeaderPolicyConfig{
					Name: "X-Session",
				},
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	lb := spec["loadBalancer"].(map[string]interface{})
	ch := lb["consistentHash"].(map[string]interface{})
	if ch["type"] != "Header" {
		t.Error("consistent hash type wrong")
	}
	header := ch["header"].(map[string]interface{})
	if header["name"] != "X-Session" {
		t.Error("header name wrong")
	}
}

func TestBuildBackendTrafficPolicy_LoadBalancer_ConsistentHash_Cookie(t *testing.T) {
	ttl := "3600s"
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-lb-cookie",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		LoadBalancer: &kubernetes.LoadBalancerPolicyConfig{
			Type: "ConsistentHash",
			ConsistentHash: &kubernetes.ConsistentHashPolicyConfig{
				Type: "Cookie",
				Cookie: &kubernetes.ConsistentHashCookiePolicyConfig{
					Name:       "session",
					TTL:        &ttl,
					Attributes: map[string]string{"SameSite": "Strict"},
				},
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	lb := spec["loadBalancer"].(map[string]interface{})
	ch := lb["consistentHash"].(map[string]interface{})
	cookie := ch["cookie"].(map[string]interface{})
	if cookie["name"] != "session" {
		t.Error("cookie name wrong")
	}
	if cookie["ttl"] != "3600s" {
		t.Error("cookie ttl wrong")
	}
	attrs := cookie["attributes"].(map[string]interface{})
	if attrs["SameSite"] != "Strict" {
		t.Error("cookie attributes wrong")
	}
}

func TestBuildBackendTrafficPolicy_Compression(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-comp",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Compression: []kubernetes.CompressionPolicyConfig{
			{Type: "Gzip"},
			{Type: "Brotli"},
			{Type: "Zstd"},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	compressor := spec["compressor"].([]interface{})
	if len(compressor) != 3 {
		t.Fatalf("expected 3 compressors, got %d", len(compressor))
	}
	c0 := compressor[0].(map[string]interface{})
	if c0["type"] != "Gzip" {
		t.Error("first compressor type wrong")
	}
	if _, ok := c0["gzip"]; !ok {
		t.Error("gzip config missing")
	}
}

func TestBuildBackendTrafficPolicy_FaultInjection(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-fi",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		FaultInjection: &kubernetes.FaultInjectionPolicyConfig{
			Delay: &kubernetes.FaultInjectionDelayPolicyConfig{
				FixedDelay: "2s",
				Percentage: k8sFloat32Ptr(50.0),
			},
			Abort: &kubernetes.FaultInjectionAbortPolicyConfig{
				HTTPStatus: k8sIntPtr(503),
				Percentage: k8sFloat32Ptr(10.0),
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	fi := spec["faultInjection"].(map[string]interface{})
	delay := fi["delay"].(map[string]interface{})
	if delay["fixedDelay"] != "2s" {
		t.Error("fixedDelay wrong")
	}
	if delay["percentage"] != float32(50.0) {
		t.Error("delay percentage wrong")
	}
	abort := fi["abort"].(map[string]interface{})
	if abort["httpStatus"] != 503 {
		t.Error("httpStatus wrong")
	}
}

func TestBuildBackendTrafficPolicy_RequestBuffer(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-rb",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		RequestBuffer: &kubernetes.RequestBufferPolicyConfig{
			Limit: "32Ki",
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	rb := spec["requestBuffer"].(map[string]interface{})
	if rb["limit"] != "32Ki" {
		t.Error("limit wrong")
	}
}

func TestBuildBackendTrafficPolicy_ResponseOverride(t *testing.T) {
	statusCode := 404
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-ro",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		ResponseOverride: []kubernetes.ResponseOverridePolicyConfig{
			{
				Match: kubernetes.ResponseOverrideMatchPolicyConfig{
					StatusCodes: []kubernetes.StatusCodeMatchPolicyConfig{
						{Type: "Value", Value: &statusCode},
						{Type: "Range", Range: &kubernetes.StatusCodeRangePolicyConfig{Start: 500, End: 599}},
					},
				},
				Response: kubernetes.ResponseOverrideResponsePolicyConfig{
					ContentType: "application/json",
					Body: kubernetes.ResponseOverrideBodyPolicyConfig{
						Type:   "Inline",
						Inline: `{"error": "not found"}`,
					},
				},
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	ro := spec["responseOverride"].([]interface{})
	if len(ro) != 1 {
		t.Fatal("expected 1 response override")
	}
	rule := ro[0].(map[string]interface{})
	match := rule["match"].(map[string]interface{})
	statusCodes := match["statusCodes"].([]interface{})
	if len(statusCodes) != 2 {
		t.Error("expected 2 status codes")
	}
	resp := rule["response"].(map[string]interface{})
	if resp["contentType"] != "application/json" {
		t.Error("contentType wrong")
	}
}

func TestBuildBackendTrafficPolicy_Metadata(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp-meta",
		Namespace: "test-ns",
		GatewayID: "gw-123",
		RouteID:   "rt-456",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Retry:     &kubernetes.RetryPolicyConfig{NumRetries: k8sInt32Ptr(1)},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	if btp.Object["kind"] != "BackendTrafficPolicy" {
		t.Error("kind wrong")
	}
	if btp.Object["apiVersion"] != "gateway.envoyproxy.io/v1alpha1" {
		t.Error("api version wrong")
	}
	meta := btp.Object["metadata"].(map[string]interface{})
	labels := meta["labels"].(map[string]interface{})
	if labels["fastgateway.dev/gateway-id"] != "gw-123" {
		t.Error("gateway-id label wrong")
	}
}

// ─── BuildClientTrafficPolicy ───────────────────────────────────────────────

func TestBuildClientTrafficPolicy_Nil(t *testing.T) {
	if kubernetes.BuildClientTrafficPolicy(nil) != nil {
		t.Error("expected nil")
	}
}

func TestBuildClientTrafficPolicy_NoConfig(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp",
		Namespace: "ns",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
	}
	if kubernetes.BuildClientTrafficPolicy(config) != nil {
		t.Error("expected nil when no features configured")
	}
}

func TestBuildClientTrafficPolicy_TCPKeepalive(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp-ka",
		Namespace: "ns",
		GatewayID: "gw-id",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		TCPKeepalive: &kubernetes.TCPKeepalivePolicyConfig{
			Probes:   k8sInt32Ptr(5),
			IdleTime: k8sStringPtr("60s"),
			Interval: k8sStringPtr("10s"),
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	if ctp == nil {
		t.Fatal("expected non-nil")
	}
	spec := ctp.Object["spec"].(map[string]interface{})
	ka := spec["tcpKeepalive"].(map[string]interface{})
	if ka["probes"] != int32(5) {
		t.Error("probes wrong")
	}
	if ka["idleTime"] != "60s" {
		t.Error("idleTime wrong")
	}
}

func TestBuildClientTrafficPolicy_ProxyProtocol(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:                "ctp-pp",
		Namespace:           "ns",
		TargetRef:           kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		EnableProxyProtocol: true,
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	if spec["enableProxyProtocol"] != true {
		t.Error("enableProxyProtocol wrong")
	}
}

func TestBuildClientTrafficPolicy_Connection(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp-conn",
		Namespace: "ns",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		Connection: &kubernetes.ConnectionPolicyConfig{
			BufferLimit:    k8sStringPtr("32Ki"),
			MaxConnections: k8sInt32Ptr(1000),
			CloseDelay:     k8sStringPtr("5s"),
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	conn := spec["connection"].(map[string]interface{})
	if conn["bufferLimit"] != "32Ki" {
		t.Error("bufferLimit wrong")
	}
	connLimit := conn["connectionLimit"].(map[string]interface{})
	if connLimit["value"] != int32(1000) {
		t.Error("maxConnections wrong")
	}
}

func TestBuildClientTrafficPolicy_Timeout(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp-to",
		Namespace: "ns",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		Timeout: &kubernetes.TimeoutPolicyConfig{
			HTTP: &kubernetes.HTTPTimeoutPolicyConfig{
				RequestReceivedTimeout: k8sStringPtr("30s"),
				IdleTimeout:            k8sStringPtr("120s"),
			},
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	timeout := spec["timeout"].(map[string]interface{})
	httpTO := timeout["http"].(map[string]interface{})
	if httpTO["requestReceivedTimeout"] != "30s" {
		t.Error("requestReceivedTimeout wrong")
	}
	if httpTO["idleTimeout"] != "120s" {
		t.Error("idleTimeout wrong")
	}
}

func TestBuildClientTrafficPolicy_HTTP3(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp-h3",
		Namespace: "ns",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		HTTP3:     &kubernetes.HTTP3PolicyConfig{Enabled: true},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	if _, ok := spec["http3"]; !ok {
		t.Error("http3 missing")
	}
}

func TestBuildClientTrafficPolicy_ClientIPDetection(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp-ip",
		Namespace: "ns",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		ClientIPDetection: &kubernetes.ClientIPDetectionPolicyConfig{
			XForwardedFor: &kubernetes.XForwardedForPolicyConfig{NumTrustedHops: 2},
			CustomHeader: &kubernetes.CustomHeaderPolicyConfig{
				Name:       "X-Real-IP",
				FailClosed: true,
			},
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	ipDet := spec["clientIPDetection"].(map[string]interface{})
	xff := ipDet["xForwardedFor"].(map[string]interface{})
	if xff["numTrustedHops"] != 2 {
		t.Error("numTrustedHops wrong")
	}
	ch := ipDet["customHeader"].(map[string]interface{})
	if ch["name"] != "X-Real-IP" {
		t.Error("custom header name wrong")
	}
	if ch["failClosed"] != true {
		t.Error("failClosed wrong")
	}
}

func TestBuildClientTrafficPolicy_TLS(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp-tls",
		Namespace: "ns",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		TLS: &kubernetes.TLSPolicyConfig{
			MinVersion: k8sStringPtr("TLS1.2"),
			MaxVersion: k8sStringPtr("TLS1.3"),
			Ciphers:    []string{"TLS_AES_128_GCM_SHA256"},
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})
	if tls["minVersion"] != "1.2" {
		t.Errorf("minVersion = %v, want 1.2", tls["minVersion"])
	}
	if tls["maxVersion"] != "1.3" {
		t.Errorf("maxVersion = %v, want 1.3", tls["maxVersion"])
	}
}

func TestBuildClientTrafficPolicy_ClientValidation(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp-mtls",
		Namespace: "ns",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		ClientValidation: &kubernetes.ClientValidationPolicyConfig{
			Optional: true,
			CACertificateRefs: []kubernetes.SecretRefPolicyConfig{
				{Group: "", Kind: "Secret", Name: "ca-cert"},
			},
			SANMatchers: []kubernetes.SANMatcherPolicyConfig{
				{Type: "DNS", Match: "example.com"},
			},
			CertificateHashes: []string{"abc123"},
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})
	cv := tls["clientValidation"].(map[string]interface{})
	if cv["optional"] != true {
		t.Error("optional wrong")
	}
	caRefs := cv["caCertificateRefs"].([]interface{})
	if len(caRefs) != 1 {
		t.Error("caCertificateRefs wrong")
	}
	sans := cv["subjectAltNames"].([]interface{})
	if len(sans) != 1 {
		t.Error("subjectAltNames wrong")
	}
	san := sans[0].(map[string]interface{})
	if san["type"] != "DNS" {
		t.Error("SAN type wrong")
	}
}

func TestBuildClientTrafficPolicy_Headers_XFCC(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp-xfcc",
		Namespace: "ns",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		Headers: &kubernetes.HeadersPolicyConfig{
			XForwardedClientCert: &kubernetes.XFCCPolicyConfig{
				Mode:             "AppendForward",
				CertDetailsToAdd: []string{"Hash", "DNS"},
			},
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	headers := spec["headers"].(map[string]interface{})
	xfcc := headers["xForwardedClientCert"].(map[string]interface{})
	if xfcc["mode"] != "AppendForward" {
		t.Error("mode wrong")
	}
	details := xfcc["certDetailsToAdd"].([]interface{})
	if len(details) != 2 || details[0] != "Hash" || details[1] != "DNS" {
		t.Errorf("certDetailsToAdd = %v, want [Hash DNS] as []interface{}", details)
	}
	// A []string here would make this panic: unstructured values must be
	// JSON-native or runtime.DeepCopyJSONValue rejects them.
	ctp.DeepCopy()
}

func TestBuildClientTrafficPolicy_Metadata(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:                "ctp-meta",
		Namespace:           "test-ns",
		GatewayID:           "gw-789",
		TargetRef:           kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "gw"},
		EnableProxyProtocol: true,
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	if ctp.Object["kind"] != "ClientTrafficPolicy" {
		t.Error("kind wrong")
	}
	meta := ctp.Object["metadata"].(map[string]interface{})
	labels := meta["labels"].(map[string]interface{})
	if labels["fastgateway.dev/gateway-id"] != "gw-789" {
		t.Error("gateway-id label wrong")
	}
}

// ─── BuildEnvoyExtensionPolicy ──────────────────────────────────────────────

func TestBuildEnvoyExtensionPolicy_Nil(t *testing.T) {
	if kubernetes.BuildEnvoyExtensionPolicy(nil) != nil {
		t.Error("expected nil")
	}
}

func TestBuildEnvoyExtensionPolicy_NoContent(t *testing.T) {
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep",
		Namespace: "ns",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
	}
	if kubernetes.BuildEnvoyExtensionPolicy(config) != nil {
		t.Error("expected nil when no lua or wasm")
	}
}

func TestBuildEnvoyExtensionPolicy_Lua_Inline(t *testing.T) {
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-lua",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Lua: []kubernetes.LuaExtensionPolicyConfig{
			{Type: "Inline", Inline: "function envoy_on_request(handle) end"},
		},
	}
	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	if eep == nil {
		t.Fatal("expected non-nil")
	}
	spec := eep.Object["spec"].(map[string]interface{})
	lua := spec["lua"].([]map[string]interface{})
	if len(lua) != 1 {
		t.Fatal("expected 1 lua config")
	}
	if lua[0]["type"] != "Inline" {
		t.Error("lua type wrong")
	}
	if lua[0]["inline"] != "function envoy_on_request(handle) end" {
		t.Error("lua inline wrong")
	}
}

func TestBuildEnvoyExtensionPolicy_Lua_ValueRef(t *testing.T) {
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-lua-ref",
		Namespace: "ns",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Lua: []kubernetes.LuaExtensionPolicyConfig{
			{
				Type: "ValueRef",
				ValueRef: &kubernetes.ValueRefPolicyConfig{
					Kind: "ConfigMap", Name: "lua-script", Namespace: "ns",
				},
			},
		},
	}
	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	spec := eep.Object["spec"].(map[string]interface{})
	lua := spec["lua"].([]map[string]interface{})
	vr := lua[0]["valueRef"].(map[string]interface{})
	if vr["kind"] != "ConfigMap" {
		t.Error("valueRef kind wrong")
	}
}

func TestBuildEnvoyExtensionPolicy_Wasm_HTTP(t *testing.T) {
	wasmConfig := `{"key":"value"}`
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-wasm",
		Namespace: "ns",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Wasm: []kubernetes.WasmExtensionPolicyConfig{
			{
				Name:   "my-wasm",
				RootID: "root-1",
				Config: &wasmConfig,
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "HTTP",
					HTTP: &kubernetes.WasmHTTPSourcePolicyConfig{
						URL:    "https://example.com/wasm.wasm",
						SHA256: "abc123",
					},
				},
			},
		},
	}
	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	spec := eep.Object["spec"].(map[string]interface{})
	wasm := spec["wasm"].([]map[string]interface{})
	if len(wasm) != 1 {
		t.Fatal("expected 1 wasm")
	}
	if wasm[0]["name"] != "my-wasm" {
		t.Error("wasm name wrong")
	}
	if wasm[0]["rootID"] != "root-1" {
		t.Error("wasm rootID wrong")
	}
	code := wasm[0]["code"].(map[string]interface{})
	if code["type"] != "HTTP" {
		t.Error("code type wrong")
	}
	httpCode := code["http"].(map[string]interface{})
	if httpCode["url"] != "https://example.com/wasm.wasm" {
		t.Error("http url wrong")
	}
}

func TestBuildEnvoyExtensionPolicy_Wasm_Image(t *testing.T) {
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-wasm-img",
		Namespace: "ns",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Wasm: []kubernetes.WasmExtensionPolicyConfig{
			{
				Name: "img-wasm",
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "Image",
					Image: &kubernetes.WasmImageSourcePolicyConfig{
						URL:    "oci://registry.example.com/wasm:v1",
						SHA256: "sha256hash",
						PullSecret: &kubernetes.ValueRefPolicyConfig{
							Kind: "Secret", Name: "pull-secret", Group: "", Namespace: "ns",
						},
					},
				},
			},
		},
	}
	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	spec := eep.Object["spec"].(map[string]interface{})
	wasm := spec["wasm"].([]map[string]interface{})
	code := wasm[0]["code"].(map[string]interface{})
	img := code["image"].(map[string]interface{})
	if img["url"] != "oci://registry.example.com/wasm:v1" {
		t.Error("image url wrong")
	}
	ps := img["pullSecretRef"].(map[string]interface{})
	if ps["name"] != "pull-secret" {
		t.Error("pull secret name wrong")
	}
}

// ─── BuildBackend ───────────────────────────────────────────────────────────

func TestBuildBackend_FQDN(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-fqdn",
		Namespace:   "ns",
		RouteID:     "rt-id",
		GatewayID:   "gw-id",
		AddressType: "fqdn",
		Address:     "api.example.com",
		Port:        443,
	}
	be := kubernetes.BuildBackend(config)
	if be == nil {
		t.Fatal("expected non-nil")
	}
	spec := be.Object["spec"].(map[string]interface{})
	endpoints := spec["endpoints"].([]interface{})
	ep := endpoints[0].(map[string]interface{})
	fqdn := ep["fqdn"].(map[string]interface{})
	if fqdn["hostname"] != "api.example.com" {
		t.Error("hostname wrong")
	}
	if fqdn["port"] != int32(443) {
		t.Error("port wrong")
	}
	if be.Object["kind"] != "Backend" {
		t.Error("kind wrong")
	}
}

func TestBuildBackend_IP(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-ip",
		Namespace:   "ns",
		RouteID:     "rt-id",
		GatewayID:   "gw-id",
		AddressType: "ip",
		Address:     "10.0.0.1",
		Port:        8080,
	}
	be := kubernetes.BuildBackend(config)
	spec := be.Object["spec"].(map[string]interface{})
	endpoints := spec["endpoints"].([]interface{})
	ep := endpoints[0].(map[string]interface{})
	ip := ep["ip"].(map[string]interface{})
	if ip["address"] != "10.0.0.1" {
		t.Error("address wrong")
	}
}

func TestBuildBackend_Fallback(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-fb",
		Namespace:   "ns",
		AddressType: "fqdn",
		Address:     "fallback.example.com",
		Port:        80,
		Fallback:    true,
	}
	be := kubernetes.BuildBackend(config)
	spec := be.Object["spec"].(map[string]interface{})
	if spec["fallback"] != true {
		t.Error("fallback should be true")
	}
}

func TestBuildBackend_NoFallback(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-nofb",
		Namespace:   "ns",
		AddressType: "fqdn",
		Address:     "normal.example.com",
		Port:        80,
		Fallback:    false,
	}
	be := kubernetes.BuildBackend(config)
	spec := be.Object["spec"].(map[string]interface{})
	if _, ok := spec["fallback"]; ok {
		t.Error("fallback should not be set when false")
	}
}

func TestBuildBackend_TLS(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-tls",
		Namespace:   "ns",
		AddressType: "fqdn",
		Address:     "tls.example.com",
		Port:        443,
		TLS: &kubernetes.BackendTLSPolicyConfig{
			CACertificateRefs: []kubernetes.BackendCertificateRefConfig{
				{Kind: "Secret", Name: "ca-secret", Namespace: "ns"},
			},
			ClientCertificateRef: &kubernetes.BackendSecretRefConfig{
				Name: "client-cert", Namespace: "ns",
			},
		},
	}
	be := kubernetes.BuildBackend(config)
	spec := be.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})
	caRefs := tls["caCertificateRefs"].([]interface{})
	if len(caRefs) != 1 {
		t.Error("caCertificateRefs wrong")
	}
	clientRef := tls["clientCertificateRef"].(map[string]interface{})
	if clientRef["name"] != "client-cert" {
		t.Error("client cert name wrong")
	}
	// SNI should be set to the FQDN address for backend cert SAN validation
	if tls["sni"] != "tls.example.com" {
		t.Errorf("sni wrong: got %v, want tls.example.com", tls["sni"])
	}
}

func TestBuildBackend_TLS_InsecureSkipVerify(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-insecure",
		Namespace:   "ns",
		AddressType: "fqdn",
		Address:     "svc.default.svc.cluster.local",
		Port:        443,
		TLS: &kubernetes.BackendTLSPolicyConfig{
			InsecureSkipVerify: true,
		},
	}
	be := kubernetes.BuildBackend(config)
	spec := be.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})

	if tls["insecureSkipVerify"] != true {
		t.Error("insecureSkipVerify should be true")
	}
	if _, ok := tls["caCertificateRefs"]; ok {
		t.Error("caCertificateRefs should not be present when insecureSkipVerify")
	}
	if tls["sni"] != "svc.default.svc.cluster.local" {
		t.Errorf("sni wrong: got %v", tls["sni"])
	}
}

func TestBuildBackend_TLS_SNIOverride(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-sni",
		Namespace:   "ns",
		AddressType: "fqdn",
		Address:     "backend.default.svc.cluster.local",
		Port:        443,
		TLS: &kubernetes.BackendTLSPolicyConfig{
			CACertificateRefs: []kubernetes.BackendCertificateRefConfig{
				{Kind: "Secret", Name: "ca-secret", Namespace: "ns"},
			},
			SNI: "custom-sni.example.com",
		},
	}
	be := kubernetes.BuildBackend(config)
	spec := be.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})

	if tls["sni"] != "custom-sni.example.com" {
		t.Errorf("sni should use override: got %v", tls["sni"])
	}
}

func TestBuildBackend_TLS_SNIAutoDerive_IP(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-ip",
		Namespace:   "ns",
		AddressType: "ip",
		Address:     "10.0.0.1",
		Port:        443,
		TLS: &kubernetes.BackendTLSPolicyConfig{
			InsecureSkipVerify: true,
		},
	}
	be := kubernetes.BuildBackend(config)
	spec := be.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})

	if _, ok := tls["sni"]; ok {
		t.Error("sni should not be auto-derived for IP backends")
	}
}

func TestBuildBackend_TLS_MTLS_InsecureSkipVerify(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-mtls-insecure",
		Namespace:   "ns",
		AddressType: "fqdn",
		Address:     "backend.default.svc.cluster.local",
		Port:        443,
		TLS: &kubernetes.BackendTLSPolicyConfig{
			InsecureSkipVerify: true,
			ClientCertificateRef: &kubernetes.BackendSecretRefConfig{
				Name: "client-cert", Namespace: "ns",
			},
		},
	}
	be := kubernetes.BuildBackend(config)
	spec := be.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})

	if tls["insecureSkipVerify"] != true {
		t.Error("insecureSkipVerify should be true")
	}
	if _, ok := tls["caCertificateRefs"]; ok {
		t.Error("caCertificateRefs should not be present")
	}
	clientRef := tls["clientCertificateRef"].(map[string]interface{})
	if clientRef["name"] != "client-cert" {
		t.Error("client cert should still be present for mTLS")
	}
	if tls["sni"] != "backend.default.svc.cluster.local" {
		t.Errorf("sni wrong: got %v", tls["sni"])
	}
}

func TestBuildBackend_Labels(t *testing.T) {
	config := &kubernetes.BackendConfig{
		Name:        "be-labels",
		Namespace:   "ns",
		RouteID:     "rt-id",
		GatewayID:   "gw-id",
		AddressType: "fqdn",
		Address:     "h.com",
		Port:        80,
	}
	be := kubernetes.BuildBackend(config)
	meta := be.Object["metadata"].(map[string]interface{})
	labels := meta["labels"].(map[string]interface{})
	if labels["fastgateway.dev/route-id"] != "rt-id" {
		t.Error("route-id label wrong")
	}
	if labels["app.kubernetes.io/managed-by"] != "fastgateway" {
		t.Error("managed-by label wrong")
	}
}

// ─── BuildGatewayObject ─────────────────────────────────────────────────────

func TestBuildGatewayObject_Nil(t *testing.T) {
	if kubernetes.BuildGatewayObject(nil) != nil {
		t.Error("expected nil")
	}
}

func TestBuildGatewayObject_TLSOnly(t *testing.T) {
	config := &kubernetes.GatewayConfig{
		Name:             "gw",
		Namespace:        "ns",
		GatewayClassName: "eg",
		Hostname:         "example.com",
		TLSMode:          "tls_only",
		HTTPSPort:        443,
		TLSSecretName:    "tls-secret",
	}
	gw := kubernetes.BuildGatewayObject(config)
	spec := gw.Object["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	if len(listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(listeners))
	}
	l := listeners[0].(map[string]interface{})
	if l["name"] != "https" {
		t.Error("expected https listener")
	}
}

func TestBuildGatewayObject_Both(t *testing.T) {
	config := &kubernetes.GatewayConfig{
		Name:             "gw",
		Namespace:        "ns",
		GatewayClassName: "eg",
		Hostname:         "example.com",
		TLSMode:          "both",
		HTTPPort:         80,
		HTTPSPort:        443,
		TLSSecretName:    "tls-secret",
	}
	gw := kubernetes.BuildGatewayObject(config)
	spec := gw.Object["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	if len(listeners) != 2 {
		t.Fatalf("expected 2 listeners, got %d", len(listeners))
	}
}

func TestBuildGatewayObject_NoTLS(t *testing.T) {
	config := &kubernetes.GatewayConfig{
		Name:             "gw",
		Namespace:        "ns",
		GatewayClassName: "eg",
		Hostname:         "example.com",
		TLSMode:          "no_tls",
		HTTPPort:         80,
	}
	gw := kubernetes.BuildGatewayObject(config)
	spec := gw.Object["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	if len(listeners) != 1 {
		t.Fatal("expected 1 listener")
	}
	l := listeners[0].(map[string]interface{})
	if l["name"] != "http" {
		t.Error("expected http listener")
	}
}

func TestBuildGatewayObject_Annotations(t *testing.T) {
	config := &kubernetes.GatewayConfig{
		Name:             "gw",
		Namespace:        "ns",
		GatewayClassName: "eg",
		Hostname:         "example.com",
		TLSMode:          "no_tls",
		HTTPPort:         80,
		Annotations:      map[string]string{"key": "value"},
	}
	gw := kubernetes.BuildGatewayObject(config)
	meta := gw.Object["metadata"].(map[string]interface{})
	ann := meta["annotations"].(map[string]interface{})
	if ann["key"] != "value" {
		t.Error("annotation wrong")
	}
}

func TestBuildGatewayObject_Passthrough(t *testing.T) {
	config := &kubernetes.GatewayConfig{
		Name:             "gw",
		Namespace:        "ns",
		GatewayClassName: "eg",
		Hostname:         "example.com",
		TLSMode:          "tls_only",
		HTTPSPort:        443,
		TLSSecretName:    "tls-secret",
		TLSPolicy:        "passthrough",
	}
	gw := kubernetes.BuildGatewayObject(config)
	spec := gw.Object["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	l := listeners[0].(map[string]interface{})
	tls := l["tls"].(map[string]interface{})
	if tls["mode"] != "Passthrough" {
		t.Errorf("tls mode = %v, want Passthrough", tls["mode"])
	}
}

// ─── BuildHTTPRouteFilter ───────────────────────────────────────────────────

func TestBuildHTTPRouteFilter_DirectResponse_Inline(t *testing.T) {
	config := &kubernetes.HTTPRouteFilterConfig{
		Name:      "hrf",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		DirectResponse: &kubernetes.DirectResponseFilterConfig{
			StatusCode:  200,
			ContentType: "text/plain",
			Body: &kubernetes.DirectResponseBodyFilterConfig{
				Type:   "Inline",
				Inline: "Hello World",
			},
		},
	}
	hrf := kubernetes.BuildHTTPRouteFilter(config)
	if hrf == nil {
		t.Fatal("expected non-nil")
	}
	spec := hrf.Object["spec"].(map[string]interface{})
	dr := spec["directResponse"].(map[string]interface{})
	if dr["statusCode"] != 200 {
		t.Error("statusCode wrong")
	}
	if dr["contentType"] != "text/plain" {
		t.Error("contentType wrong")
	}
	body := dr["body"].(map[string]interface{})
	if body["inline"] != "Hello World" {
		t.Error("body inline wrong")
	}
}

func TestBuildHTTPRouteFilter_DirectResponse_ValueRef(t *testing.T) {
	config := &kubernetes.HTTPRouteFilterConfig{
		Name:      "hrf-ref",
		Namespace: "ns",
		DirectResponse: &kubernetes.DirectResponseFilterConfig{
			StatusCode: 404,
			Body: &kubernetes.DirectResponseBodyFilterConfig{
				Type: "ValueRef",
				ValueRef: &kubernetes.DirectResponseValueRef{
					Group: "", Kind: "ConfigMap", Name: "error-body",
				},
			},
		},
	}
	hrf := kubernetes.BuildHTTPRouteFilter(config)
	spec := hrf.Object["spec"].(map[string]interface{})
	dr := spec["directResponse"].(map[string]interface{})
	body := dr["body"].(map[string]interface{})
	vr := body["valueRef"].(map[string]interface{})
	if vr["name"] != "error-body" {
		t.Error("valueRef name wrong")
	}
}

// ─── BuildExtAuthBackend ────────────────────────────────────────────────────

func TestBuildExtAuthBackend_Nil(t *testing.T) {
	if kubernetes.BuildExtAuthBackend(nil) != nil {
		t.Error("expected nil")
	}
}

func TestBuildExtAuthBackend_Basic(t *testing.T) {
	config := &kubernetes.ExtAuthBackendConfig{
		Name:      "ext-auth-be",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		Service: models.ExtAuthBackendRef{
			Name:      "auth-service",
			Namespace: "auth-ns",
			Port:      9090,
		},
	}
	be := kubernetes.BuildExtAuthBackend(config)
	if be == nil {
		t.Fatal("expected non-nil")
	}
	if be.Object["kind"] != "Backend" {
		t.Error("kind wrong")
	}
	spec := be.Object["spec"].(map[string]interface{})
	endpoints := spec["endpoints"].([]interface{})
	ep := endpoints[0].(map[string]interface{})
	fqdn := ep["fqdn"].(map[string]interface{})
	if fqdn["hostname"] != "auth-service.auth-ns.svc.cluster.local" {
		t.Errorf("hostname = %v", fqdn["hostname"])
	}
}

// ─── GenerateExtAuthBackendName ─────────────────────────────────────────────

func TestGenerateExtAuthBackendName_GeneralMode(t *testing.T) {
	name := kubernetes.GenerateExtAuthBackendName("12345678-abcd-efgh-ijkl", "")
	if name != "fg-extauth-12345678" {
		t.Errorf("name = %s, want fg-extauth-12345678", name)
	}
}

func TestGenerateExtAuthBackendName_ClientMode(t *testing.T) {
	name := kubernetes.GenerateExtAuthBackendName("12345678-abcd-efgh-ijkl", "abcdef01-2345-6789")
	if name != "fg-extauth-12345678-abcdef01" {
		t.Errorf("name = %s, want fg-extauth-12345678-abcdef01", name)
	}
}

// ─── BuildExtAuthBackend (additional paths) ─────────────────────────────────

func TestBuildExtAuthBackend_WithClientID(t *testing.T) {
	config := &kubernetes.ExtAuthBackendConfig{
		Name:      "ext-auth-be",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		ClientID:  "client-123",
		Service: models.ExtAuthBackendRef{
			Name:      "auth-service",
			Namespace: "auth-ns",
			Port:      9090,
		},
	}
	be := kubernetes.BuildExtAuthBackend(config)
	if be == nil {
		t.Fatal("expected non-nil")
	}
	labels := be.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})
	if labels["fastgateway.dev/client-id"] != "client-123" {
		t.Errorf("client-id label = %v, want client-123", labels["fastgateway.dev/client-id"])
	}
	if labels["fastgateway.dev/type"] != "ext-auth" {
		t.Error("expected ext-auth type label")
	}
}

func TestBuildExtAuthBackend_ServiceNamespaceFallback(t *testing.T) {
	// When Service.Namespace is empty, should fall back to config.Namespace
	config := &kubernetes.ExtAuthBackendConfig{
		Name:      "ext-auth-be",
		Namespace: "default-ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		Service: models.ExtAuthBackendRef{
			Name: "auth-service",
			Port: 9090,
		},
	}
	be := kubernetes.BuildExtAuthBackend(config)
	spec := be.Object["spec"].(map[string]interface{})
	endpoints := spec["endpoints"].([]interface{})
	ep := endpoints[0].(map[string]interface{})
	fqdn := ep["fqdn"].(map[string]interface{})
	if fqdn["hostname"] != "auth-service.default-ns.svc.cluster.local" {
		t.Errorf("hostname = %v, want auth-service.default-ns.svc.cluster.local", fqdn["hostname"])
	}
}

// ─── BuildGatewayClassObject ────────────────────────────────────────────────

func TestBuildGatewayClassObject_NoParametersRef(t *testing.T) {
	config := &kubernetes.GatewayClassConfig{
		Name:           "my-gc",
		ControllerName: "gateway.envoyproxy.io/gatewayclass-controller",
	}
	gc := kubernetes.BuildGatewayClassObject(config)
	if gc == nil {
		t.Fatal("expected non-nil")
	}
	if gc.Object["kind"] != "GatewayClass" {
		t.Error("kind wrong")
	}
	if gc.Object["apiVersion"] != "gateway.networking.k8s.io/v1" {
		t.Error("apiVersion wrong")
	}
	meta := gc.Object["metadata"].(map[string]interface{})
	if meta["name"] != "my-gc" {
		t.Errorf("name = %v, want my-gc", meta["name"])
	}
	labels := meta["labels"].(map[string]interface{})
	if labels["app.kubernetes.io/managed-by"] != "fastgateway" {
		t.Error("missing managed-by label")
	}
	spec := gc.Object["spec"].(map[string]interface{})
	if spec["controllerName"] != "gateway.envoyproxy.io/gatewayclass-controller" {
		t.Error("controllerName wrong")
	}
	if _, ok := spec["parametersRef"]; ok {
		t.Error("should not have parametersRef when name is empty")
	}
}

func TestBuildGatewayClassObject_WithParametersRef(t *testing.T) {
	config := &kubernetes.GatewayClassConfig{
		Name:              "my-gc",
		ControllerName:    "gateway.envoyproxy.io/gatewayclass-controller",
		ParametersRefName: "my-envoy-proxy",
	}
	gc := kubernetes.BuildGatewayClassObject(config)
	spec := gc.Object["spec"].(map[string]interface{})
	paramRef := spec["parametersRef"].(map[string]interface{})
	if paramRef["group"] != "gateway.envoyproxy.io" {
		t.Errorf("group = %v", paramRef["group"])
	}
	if paramRef["kind"] != "EnvoyProxy" {
		t.Errorf("kind = %v", paramRef["kind"])
	}
	if paramRef["name"] != "my-envoy-proxy" {
		t.Errorf("name = %v", paramRef["name"])
	}
	if paramRef["namespace"] != "envoy-gateway-system" {
		t.Errorf("namespace = %v", paramRef["namespace"])
	}
}

// ─── BuildEnvoyProxyObject ──────────────────────────────────────────────────

func TestBuildEnvoyProxyObject_MinimalClusterIP(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:        "my-proxy",
		Namespace:   "egw-system",
		ServiceType: "ClusterIP",
	}
	ep := kubernetes.BuildEnvoyProxyObject(config)
	if ep == nil {
		t.Fatal("expected non-nil")
	}
	if ep.Object["kind"] != "EnvoyProxy" {
		t.Error("kind wrong")
	}
	if ep.Object["apiVersion"] != "gateway.envoyproxy.io/v1alpha1" {
		t.Error("apiVersion wrong")
	}
	spec := ep.Object["spec"].(map[string]interface{})
	provider := spec["provider"].(map[string]interface{})
	k8s := provider["kubernetes"].(map[string]interface{})
	svc := k8s["envoyService"].(map[string]interface{})
	if svc["type"] != "ClusterIP" {
		t.Errorf("service type = %v, want ClusterIP", svc["type"])
	}
	if _, ok := k8s["envoyDeployment"]; ok {
		t.Error("should not have envoyDeployment with minimal config")
	}
	if _, ok := k8s["envoyHpa"]; ok {
		t.Error("should not have envoyHpa with minimal config")
	}
	if _, ok := spec["mergeGateways"]; ok {
		t.Error("should not have mergeGateways when false")
	}
}

func TestBuildEnvoyProxyObject_LoadBalancerWithPolicy(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:                  "my-proxy",
		Namespace:             "ns",
		ServiceType:           "LoadBalancer",
		ExternalTrafficPolicy: "Local",
		LoadBalancerClass:     "service.k8s.aws/nlb",
		Annotations:           map[string]string{"key": "val"},
	}
	ep := kubernetes.BuildEnvoyProxyObject(config)
	spec := ep.Object["spec"].(map[string]interface{})
	k8s := spec["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})
	svc := k8s["envoyService"].(map[string]interface{})
	if svc["externalTrafficPolicy"] != "Local" {
		t.Errorf("externalTrafficPolicy = %v, want Local", svc["externalTrafficPolicy"])
	}
	if svc["loadBalancerClass"] != "service.k8s.aws/nlb" {
		t.Errorf("loadBalancerClass = %v", svc["loadBalancerClass"])
	}
	ann := svc["annotations"].(map[string]interface{})
	if ann["key"] != "val" {
		t.Error("annotation missing")
	}
}

func TestBuildEnvoyProxyObject_ClusterIPNoTrafficPolicy(t *testing.T) {
	// ExternalTrafficPolicy should not be set for ClusterIP even if configured
	config := &kubernetes.EnvoyProxyConfig{
		Name:                  "my-proxy",
		Namespace:             "ns",
		ServiceType:           "ClusterIP",
		ExternalTrafficPolicy: "Local",
		LoadBalancerClass:     "some-class",
	}
	ep := kubernetes.BuildEnvoyProxyObject(config)
	spec := ep.Object["spec"].(map[string]interface{})
	k8s := spec["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})
	svc := k8s["envoyService"].(map[string]interface{})
	if _, ok := svc["externalTrafficPolicy"]; ok {
		t.Error("should not have externalTrafficPolicy for ClusterIP")
	}
	if _, ok := svc["loadBalancerClass"]; ok {
		t.Error("should not have loadBalancerClass for ClusterIP")
	}
}

func TestBuildEnvoyProxyObject_FixedReplicas(t *testing.T) {
	replicas := int32(3)
	config := &kubernetes.EnvoyProxyConfig{
		Name:        "my-proxy",
		Namespace:   "ns",
		ServiceType: "ClusterIP",
		ScalingConfig: &models.ScalingConfig{
			Type:     "fixed",
			Replicas: &replicas,
		},
	}
	ep := kubernetes.BuildEnvoyProxyObject(config)
	spec := ep.Object["spec"].(map[string]interface{})
	k8s := spec["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})
	dep := k8s["envoyDeployment"].(map[string]interface{})
	if dep["replicas"] != int32(3) {
		t.Errorf("replicas = %v, want 3", dep["replicas"])
	}
	if _, ok := k8s["envoyHpa"]; ok {
		t.Error("should not have envoyHpa for fixed scaling")
	}
}

func TestBuildEnvoyProxyObject_HPAScaling(t *testing.T) {
	minR := int32(2)
	maxR := int32(10)
	config := &kubernetes.EnvoyProxyConfig{
		Name:        "my-proxy",
		Namespace:   "ns",
		ServiceType: "ClusterIP",
		ScalingConfig: &models.ScalingConfig{
			Type:        "hpa",
			MinReplicas: &minR,
			MaxReplicas: &maxR,
		},
	}
	ep := kubernetes.BuildEnvoyProxyObject(config)
	spec := ep.Object["spec"].(map[string]interface{})
	k8s := spec["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})
	hpa := k8s["envoyHpa"].(map[string]interface{})
	if hpa["minReplicas"] != int32(2) {
		t.Errorf("minReplicas = %v, want 2", hpa["minReplicas"])
	}
	if hpa["maxReplicas"] != int32(10) {
		t.Errorf("maxReplicas = %v, want 10", hpa["maxReplicas"])
	}
	// envoyDeployment should not be set for hpa-only config (no pod annotations or resources)
	if _, ok := k8s["envoyDeployment"]; ok {
		t.Error("should not have envoyDeployment for HPA-only config")
	}
}

func TestBuildEnvoyProxyObject_ContainerResources(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:        "my-proxy",
		Namespace:   "ns",
		ServiceType: "ClusterIP",
		ContainerResources: &models.ContainerResourcesConfig{
			Requests: &models.ResourceValues{CPU: "100m", Memory: "128Mi"},
			Limits:   &models.ResourceValues{CPU: "500m", Memory: "512Mi"},
		},
	}
	ep := kubernetes.BuildEnvoyProxyObject(config)
	spec := ep.Object["spec"].(map[string]interface{})
	k8s := spec["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})
	dep := k8s["envoyDeployment"].(map[string]interface{})
	container := dep["container"].(map[string]interface{})
	resources := container["resources"].(map[string]interface{})
	requests := resources["requests"].(map[string]interface{})
	if requests["cpu"] != "100m" {
		t.Errorf("requests cpu = %v", requests["cpu"])
	}
	if requests["memory"] != "128Mi" {
		t.Errorf("requests memory = %v", requests["memory"])
	}
	limits := resources["limits"].(map[string]interface{})
	if limits["cpu"] != "500m" {
		t.Errorf("limits cpu = %v", limits["cpu"])
	}
	if limits["memory"] != "512Mi" {
		t.Errorf("limits memory = %v", limits["memory"])
	}
}

func TestBuildEnvoyProxyObject_PodAnnotations(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:           "my-proxy",
		Namespace:      "ns",
		ServiceType:    "ClusterIP",
		PodAnnotations: map[string]string{"prometheus.io/scrape": "true"},
	}
	ep := kubernetes.BuildEnvoyProxyObject(config)
	spec := ep.Object["spec"].(map[string]interface{})
	k8s := spec["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})
	dep := k8s["envoyDeployment"].(map[string]interface{})
	pod := dep["pod"].(map[string]interface{})
	ann := pod["annotations"].(map[string]interface{})
	if ann["prometheus.io/scrape"] != "true" {
		t.Error("pod annotation missing")
	}
}

func TestBuildEnvoyProxyObject_MergeGateways(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:          "my-proxy",
		Namespace:     "ns",
		ServiceType:   "ClusterIP",
		MergeGateways: true,
	}
	ep := kubernetes.BuildEnvoyProxyObject(config)
	spec := ep.Object["spec"].(map[string]interface{})
	if spec["mergeGateways"] != true {
		t.Error("mergeGateways should be true")
	}
}

// ─── BuildDirectResponseConfigMap ───────────────────────────────────────────

func TestBuildDirectResponseConfigMap_Basic(t *testing.T) {
	config := &kubernetes.DirectResponseConfigMapConfig{
		Name:        "dr-cm",
		Namespace:   "ns",
		GatewayID:   "gw-id",
		RouteID:     "rt-id",
		BodyContent: "<html>Error</html>",
	}
	cm := kubernetes.BuildDirectResponseConfigMap(config)
	if cm == nil {
		t.Fatal("expected non-nil")
	}
	if cm.Object["kind"] != "ConfigMap" {
		t.Error("kind wrong")
	}
	if cm.Object["apiVersion"] != "v1" {
		t.Error("apiVersion wrong")
	}
	data := cm.Object["data"].(map[string]interface{})
	if data["response.body"] != "<html>Error</html>" {
		t.Error("body content wrong")
	}
	meta := cm.Object["metadata"].(map[string]interface{})
	labels := meta["labels"].(map[string]interface{})
	if labels["fastgateway.dev/route-id"] != "rt-id" {
		t.Error("route-id label wrong")
	}
}

// ─── BuildHTTPRouteObject (additional paths) ────────────────────────────────

func TestBuildHTTPRouteObject_DirectResponseRoute(t *testing.T) {
	// When HTTPRouteFilterName is set, should produce ExtensionRef filter
	config := &kubernetes.HTTPRouteConfig{
		Name:                "dr-route",
		Namespace:           "ns",
		GatewayName:         "my-gw",
		GatewayID:           "gw-id",
		RouteID:             "rt-id",
		Hostname:            "example.com",
		HTTPRouteFilterName: "my-hrf",
		ResponseHeaderModifier: &kubernetes.HTTPHeaderModifier{
			Set: []kubernetes.HTTPHeaderValue{{Name: "X-Custom", Value: "val"}},
		},
		Rules: []kubernetes.HTTPRouteRule{
			{PathType: "PathPrefix", PathValue: "/health"},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	if route == nil {
		t.Fatal("expected non-nil")
	}
	rule := route.Spec.Rules[0]
	// Should have no backend refs
	if len(rule.BackendRefs) > 0 {
		t.Error("direct response route should have no backend refs")
	}
	// Should have response header modifier and extension ref filters
	foundExtRef := false
	foundResMod := false
	for _, f := range rule.Filters {
		if f.Type == gatewayv1.HTTPRouteFilterExtensionRef {
			foundExtRef = true
			if string(f.ExtensionRef.Name) != "my-hrf" {
				t.Errorf("extension ref name = %s, want my-hrf", f.ExtensionRef.Name)
			}
			if string(f.ExtensionRef.Kind) != "HTTPRouteFilter" {
				t.Errorf("extension ref kind = %s", f.ExtensionRef.Kind)
			}
		}
		if f.Type == gatewayv1.HTTPRouteFilterResponseHeaderModifier {
			foundResMod = true
		}
	}
	if !foundExtRef {
		t.Error("missing ExtensionRef filter")
	}
	if !foundResMod {
		t.Error("missing ResponseHeaderModifier filter")
	}
}

func TestBuildHTTPRouteObject_DirectResponseOmitsRequestModifier(t *testing.T) {
	// Direct response route should NOT include request header modifier
	config := &kubernetes.HTTPRouteConfig{
		Name:                "dr-route",
		Namespace:           "ns",
		GatewayName:         "my-gw",
		Hostname:            "example.com",
		HTTPRouteFilterName: "my-hrf",
		RequestHeaderModifier: &kubernetes.HTTPHeaderModifier{
			Set: []kubernetes.HTTPHeaderValue{{Name: "X-Req", Value: "val"}},
		},
		Rules: []kubernetes.HTTPRouteRule{
			{PathType: "PathPrefix", PathValue: "/"},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	for _, f := range route.Spec.Rules[0].Filters {
		if f.Type == gatewayv1.HTTPRouteFilterRequestHeaderModifier {
			t.Error("direct response route should not have RequestHeaderModifier")
		}
	}
}

func TestBuildHTTPRouteObject_RedirectWithPath(t *testing.T) {
	port := 443
	config := &kubernetes.HTTPRouteConfig{
		Name:        "redir-route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "example.com",
		Redirect: &kubernetes.HTTPRedirectConfig{
			Scheme:     "https",
			Hostname:   "new.example.com",
			Port:       &port,
			StatusCode: 301,
			Path: &kubernetes.HTTPPathRewrite{
				Type:            "ReplaceFullPath",
				ReplaceFullPath: "/new-path",
			},
		},
		Rules: []kubernetes.HTTPRouteRule{
			{PathType: "PathPrefix", PathValue: "/old"},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	rule := route.Spec.Rules[0]
	if len(rule.BackendRefs) > 0 {
		t.Error("redirect route should have no backend refs")
	}
	foundRedirect := false
	for _, f := range rule.Filters {
		if f.Type == gatewayv1.HTTPRouteFilterRequestRedirect {
			foundRedirect = true
			if *f.RequestRedirect.Scheme != "https" {
				t.Errorf("scheme = %v", *f.RequestRedirect.Scheme)
			}
			if string(*f.RequestRedirect.Hostname) != "new.example.com" {
				t.Error("hostname wrong")
			}
			if int(*f.RequestRedirect.Port) != 443 {
				t.Error("port wrong")
			}
			if *f.RequestRedirect.StatusCode != 301 {
				t.Error("status code wrong")
			}
			if f.RequestRedirect.Path.Type != gatewayv1.FullPathHTTPPathModifier {
				t.Error("path type wrong")
			}
			if *f.RequestRedirect.Path.ReplaceFullPath != "/new-path" {
				t.Error("path value wrong")
			}
		}
	}
	if !foundRedirect {
		t.Error("missing redirect filter")
	}
}

func TestBuildHTTPRouteObject_RedirectPrefixMatch(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "redir-route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "example.com",
		Redirect: &kubernetes.HTTPRedirectConfig{
			StatusCode: 302,
			Path: &kubernetes.HTTPPathRewrite{
				Type:               "ReplacePrefixMatch",
				ReplacePrefixMatch: "/v2",
			},
		},
		Rules: []kubernetes.HTTPRouteRule{
			{PathType: "PathPrefix", PathValue: "/v1"},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	for _, f := range route.Spec.Rules[0].Filters {
		if f.Type == gatewayv1.HTTPRouteFilterRequestRedirect {
			if f.RequestRedirect.Path.Type != gatewayv1.PrefixMatchHTTPPathModifier {
				t.Error("path type wrong, want PrefixMatchHTTPPathModifier")
			}
			if *f.RequestRedirect.Path.ReplacePrefixMatch != "/v2" {
				t.Error("path value wrong")
			}
		}
	}
}

func TestBuildHTTPRouteObject_BackendWithWeight0(t *testing.T) {
	// Weight=0 should not be set (let K8s default)
	config := &kubernetes.HTTPRouteConfig{
		Name:        "route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "example.com",
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:  "PathPrefix",
				PathValue: "/",
				BackendRefs: []kubernetes.BackendRef{
					{Name: "svc1", Port: 80, Weight: 0},
				},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	if route.Spec.Rules[0].BackendRefs[0].Weight != nil {
		t.Error("weight=0 should not set Weight pointer")
	}
}

func TestBuildHTTPRouteObject_BackendWithNamespace(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "example.com",
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:  "PathPrefix",
				PathValue: "/",
				BackendRefs: []kubernetes.BackendRef{
					{Name: "svc1", Port: 80, Namespace: "other-ns"},
				},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	ref := route.Spec.Rules[0].BackendRefs[0]
	if ref.Namespace == nil || string(*ref.Namespace) != "other-ns" {
		t.Error("namespace not set on backend ref")
	}
}

// ─── BuildSecurityPolicy (OIDC additional paths) ────────────────────────────

func TestBuildSecurityPolicy_OIDC_WithScopes(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "my-route"},
		OIDC: &kubernetes.OIDCPolicyConfig{
			Issuer:           "https://accounts.google.com",
			ClientID:         "my-client-id",
			ClientSecretName: "oidc-secret",
			ClientSecretNS:   "ns",
			RedirectURL:      "https://example.com/callback",
			LogoutPath:       "/logout",
			Scopes:           []string{"openid", "email", "profile"},
			CookieDomain:     ".example.com",
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	oidc := spec["oidc"].(map[string]interface{})
	scopes := oidc["scopes"].([]interface{})
	if len(scopes) != 3 {
		t.Fatalf("scopes length = %d, want 3", len(scopes))
	}
	if scopes[0] != "openid" || scopes[1] != "email" || scopes[2] != "profile" {
		t.Error("scopes values wrong")
	}
	if oidc["cookieDomain"] != ".example.com" {
		t.Errorf("cookieDomain = %v", oidc["cookieDomain"])
	}
}

func TestBuildSecurityPolicy_OIDC_NoCookieDomain(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		OIDC: &kubernetes.OIDCPolicyConfig{
			Issuer:           "https://issuer",
			ClientID:         "cid",
			ClientSecretName: "sec",
			ClientSecretNS:   "ns",
			RedirectURL:      "https://example.com/cb",
			LogoutPath:       "/logout",
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	oidc := spec["oidc"].(map[string]interface{})
	if _, ok := oidc["cookieDomain"]; ok {
		t.Error("should not have cookieDomain when empty")
	}
	if _, ok := oidc["scopes"]; ok {
		t.Error("should not have scopes when empty")
	}
}

// ─── BuildSecurityPolicy (Authorization JWT claims) ─────────────────────────

func TestBuildSecurityPolicy_Authorization_JWTClaims_ScopeForceStringArray(t *testing.T) {
	// "scope" claim should always be StringArray regardless of user-specified valueType
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action: "Allow",
					JWT: &kubernetes.JWTPrincipalPolicyConfig{
						Provider: "my-provider",
						Claims: []kubernetes.JWTClaimRulePolicyConfig{
							{Name: "scope", Values: []string{"read", "write"}, ValueType: ""},
							{Name: "role", Values: []string{"admin"}, ValueType: "StringArray"},
						},
					},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp == nil {
		t.Fatal("expected non-nil")
	}
	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	rules := auth["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	principal := rule["principal"].(map[string]interface{})
	jwt := principal["jwt"].(map[string]interface{})
	claims := jwt["claims"].([]interface{})

	// First claim: "scope" should force StringArray
	scopeClaim := claims[0].(map[string]interface{})
	if scopeClaim["valueType"] != "StringArray" {
		t.Errorf("scope claim valueType = %v, want StringArray", scopeClaim["valueType"])
	}

	// Second claim: "role" with StringArray should keep StringArray
	roleClaim := claims[1].(map[string]interface{})
	if roleClaim["valueType"] != "StringArray" {
		t.Errorf("role claim valueType = %v, want StringArray", roleClaim["valueType"])
	}
}

func TestBuildSecurityPolicy_Authorization_JWTClaims_DefaultValueType(t *testing.T) {
	// Non-scope claims with default/empty valueType should NOT have valueType set
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action: "Allow",
					JWT: &kubernetes.JWTPrincipalPolicyConfig{
						Provider: "my-provider",
						Claims: []kubernetes.JWTClaimRulePolicyConfig{
							{Name: "role", Values: []string{"admin"}, ValueType: ""},
						},
					},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	rules := auth["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	principal := rule["principal"].(map[string]interface{})
	jwt := principal["jwt"].(map[string]interface{})
	claims := jwt["claims"].([]interface{})
	roleClaim := claims[0].(map[string]interface{})
	if _, ok := roleClaim["valueType"]; ok {
		t.Error("default valueType should not set valueType field")
	}
}

func TestBuildSecurityPolicy_Authorization_CIDRsAndJWT(t *testing.T) {
	// Rule with both clientCIDRs and JWT principal
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action:      "Allow",
					ClientCIDRs: []string{"10.0.0.0/8"},
					JWT: &kubernetes.JWTPrincipalPolicyConfig{
						Provider: "prov",
					},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	rules := auth["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	principal := rule["principal"].(map[string]interface{})
	cidrs := principal["clientCIDRs"].([]interface{})
	if len(cidrs) != 1 || cidrs[0] != "10.0.0.0/8" {
		t.Error("clientCIDRs wrong")
	}
	jwt := principal["jwt"].(map[string]interface{})
	if jwt["provider"] != "prov" {
		t.Error("jwt provider wrong")
	}
}

func TestBuildSecurityPolicy_Authorization_SkipEmptyPrincipal(t *testing.T) {
	// Rule with neither CIDRs nor JWT should be skipped
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{Action: "Allow"}, // no principal
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	// Deny-all with no valid rules should still produce defaultAction
	if auth["defaultAction"] != "Deny" {
		t.Error("defaultAction wrong")
	}
	if _, ok := auth["rules"]; ok {
		t.Error("should not have rules when all principals are empty")
	}
}

func TestBuildSecurityPolicy_Authorization_AllowDefaultAction(t *testing.T) {
	// If defaultAction != "Deny" and no rules, should return nil
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Allow",
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	if sp != nil {
		t.Error("should return nil when no security features")
	}
}

// ─── BuildSecurityPolicy (API Key Auth additional paths) ────────────────────

func TestBuildSecurityPolicy_APIKey_WithExtractFrom(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		APIKeyAuth: &kubernetes.APIKeyAuthPolicyConfig{
			CredentialRefs: []kubernetes.SecretRefConfig{
				{Name: "api-key-secret", Namespace: "ns"},
			},
			ExtractFrom: []kubernetes.APIKeyExtractFromConfig{
				{Headers: []string{"X-Api-Key", "Authorization"}},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	apiKey := spec["apiKeyAuth"].(map[string]interface{})
	refs := apiKey["credentialRefs"].([]interface{})
	if len(refs) != 1 {
		t.Fatal("expected 1 credential ref")
	}
	ref := refs[0].(map[string]interface{})
	if ref["namespace"] != "ns" {
		t.Error("namespace wrong")
	}
	extractFrom := apiKey["extractFrom"].([]interface{})
	if len(extractFrom) != 1 {
		t.Fatal("expected 1 extractFrom")
	}
	ef := extractFrom[0].(map[string]interface{})
	headers := ef["headers"].([]interface{})
	if len(headers) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(headers))
	}
	if headers[0] != "X-Api-Key" || headers[1] != "Authorization" {
		t.Error("header values wrong")
	}
}

func TestBuildSecurityPolicy_APIKey_NoNamespace(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		APIKeyAuth: &kubernetes.APIKeyAuthPolicyConfig{
			CredentialRefs: []kubernetes.SecretRefConfig{
				{Name: "api-key-secret"},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	apiKey := spec["apiKeyAuth"].(map[string]interface{})
	refs := apiKey["credentialRefs"].([]interface{})
	ref := refs[0].(map[string]interface{})
	if _, ok := ref["namespace"]; ok {
		t.Error("should not have namespace when empty")
	}
}

// ─── BuildSecurityPolicy (JWT additional paths) ─────────────────────────────

func TestBuildSecurityPolicy_JWT_WithAudiencesAndClaimToHeaders(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		JWT: &kubernetes.JWTAuthPolicyConfig{
			Providers: []kubernetes.JWTProviderPolicyConfig{
				{
					Name:      "my-jwt",
					Issuer:    "https://issuer.example.com",
					JWKSURL:   "https://issuer.example.com/.well-known/jwks.json",
					Audiences: []string{"aud1", "aud2"},
					ClaimToHeaders: []kubernetes.JWTClaimToHeaderPolicyConfig{
						{Claim: "sub", Header: "x-user-id"},
						{Claim: "email", Header: "x-user-email"},
					},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	jwt := spec["jwt"].(map[string]interface{})
	providers := jwt["providers"].([]interface{})
	p := providers[0].(map[string]interface{})
	audiences := p["audiences"].([]interface{})
	if len(audiences) != 2 {
		t.Fatalf("audiences length = %d, want 2", len(audiences))
	}
	if audiences[0] != "aud1" || audiences[1] != "aud2" {
		t.Error("audiences values wrong")
	}
	cth := p["claimToHeaders"].([]interface{})
	if len(cth) != 2 {
		t.Fatalf("claimToHeaders length = %d, want 2", len(cth))
	}
	first := cth[0].(map[string]interface{})
	if first["claim"] != "sub" || first["header"] != "x-user-id" {
		t.Error("claimToHeaders[0] wrong")
	}
}

func TestBuildSecurityPolicy_JWT_NoJWKSURL(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		JWT: &kubernetes.JWTAuthPolicyConfig{
			Providers: []kubernetes.JWTProviderPolicyConfig{
				{Name: "my-jwt", Issuer: "https://issuer.example.com"},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	jwt := spec["jwt"].(map[string]interface{})
	providers := jwt["providers"].([]interface{})
	p := providers[0].(map[string]interface{})
	if _, ok := p["remoteJWKS"]; ok {
		t.Error("should not have remoteJWKS when JWKSURL is empty")
	}
	if _, ok := p["audiences"]; ok {
		t.Error("should not have audiences when empty")
	}
	if _, ok := p["claimToHeaders"]; ok {
		t.Error("should not have claimToHeaders when empty")
	}
}

// ─── BuildSecurityPolicy (ExtAuth additional paths) ─────────────────────────

func TestBuildSecurityPolicy_ExtAuth_WithBodyAndHeaders(t *testing.T) {
	failOpen := true
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		ExtAuth: &models.ExtAuthConfig{
			Type: "http",
			HTTP: &models.ExtAuthHTTPConfig{
				BackendRef:       models.ExtAuthBackendRef{Name: "auth-svc", Port: 9090, Namespace: "auth-ns"},
				Path:             "/check",
				HeadersToBackend: []string{"Authorization", "X-Custom"},
			},
			FailOpen:         &failOpen,
			HeadersToExtAuth: []string{"Cookie"},
			WithRequestBody:  &models.ExtAuthRequestBody{MaxBytes: 4096},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	extAuth := spec["extAuth"].(map[string]interface{})
	if extAuth["failOpen"] != true {
		t.Error("failOpen should be true")
	}
	headersToExt := extAuth["headersToExtAuth"].([]interface{})
	if len(headersToExt) != 1 || headersToExt[0] != "Cookie" {
		t.Error("headersToExtAuth wrong")
	}
	bodyToExt := extAuth["bodyToExtAuth"].(map[string]interface{})
	if bodyToExt["maxRequestBytes"] != uint32(4096) {
		t.Errorf("maxRequestBytes = %v", bodyToExt["maxRequestBytes"])
	}
	httpConfig := extAuth["http"].(map[string]interface{})
	if httpConfig["path"] != "/check" {
		t.Error("path wrong")
	}
	headersToBackend := httpConfig["headersToBackend"].([]interface{})
	if len(headersToBackend) != 2 {
		t.Fatalf("headersToBackend length = %d, want 2", len(headersToBackend))
	}
	backendRefs := httpConfig["backendRefs"].([]interface{})
	ref := backendRefs[0].(map[string]interface{})
	if ref["namespace"] != "auth-ns" {
		t.Error("namespace wrong on backend ref")
	}
}

func TestBuildSecurityPolicy_ExtAuth_GRPC_WithNamespace(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		ExtAuth: &models.ExtAuthConfig{
			Type: "grpc",
			GRPC: &models.ExtAuthGRPCConfig{
				BackendRef: models.ExtAuthBackendRef{Name: "grpc-auth", Port: 50051, Namespace: "grpc-ns"},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	spec := sp.Object["spec"].(map[string]interface{})
	extAuth := spec["extAuth"].(map[string]interface{})
	grpcConfig := extAuth["grpc"].(map[string]interface{})
	backendRefs := grpcConfig["backendRefs"].([]interface{})
	ref := backendRefs[0].(map[string]interface{})
	if ref["namespace"] != "grpc-ns" {
		t.Errorf("namespace = %v, want grpc-ns", ref["namespace"])
	}
}

// ─── BuildBackendTrafficPolicy (rate limit with selectors) ──────────────────

func TestBuildBackendTrafficPolicy_RateLimit_WithSelectors(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "my-route"},
		RateLimit: &kubernetes.RateLimitPolicyConfig{
			Global: &kubernetes.GlobalRateLimitPolicyConfig{
				Rules: []kubernetes.RateLimitRulePolicyConfig{
					{
						Limit: kubernetes.RateLimitValuePolicyConfig{Requests: 100, Unit: "Minute"},
						ClientSelectors: []kubernetes.RateLimitSelectorPolicyConfig{
							{
								Headers: []kubernetes.RateLimitHeaderMatchPolicyConfig{
									{Name: "X-User-Type", Value: "premium", Type: "Exact", Invert: false},
									{Name: "X-Bot", Invert: true},
								},
								SourceCIDR: &kubernetes.RateLimitSourceCIDRPolicyConfig{Value: "10.0.0.0/8", Type: "Distinct"},
								Path:       &kubernetes.RateLimitPathMatchPolicyConfig{Value: "/api", Type: "PathPrefix"},
								Methods:    []string{"GET", "POST"},
							},
						},
					},
				},
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	if btp == nil {
		t.Fatal("expected non-nil")
	}
	spec := btp.Object["spec"].(map[string]interface{})
	rl := spec["rateLimit"].(map[string]interface{})
	global := rl["global"].(map[string]interface{})
	rules := global["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	limit := rule["limit"].(map[string]interface{})
	if limit["requests"] != int64(100) {
		t.Errorf("requests = %v", limit["requests"])
	}
	if limit["unit"] != "Minute" {
		t.Errorf("unit = %v", limit["unit"])
	}
	selectors := rule["clientSelectors"].([]interface{})
	sel := selectors[0].(map[string]interface{})
	headers := sel["headers"].([]interface{})
	if len(headers) != 2 {
		t.Fatalf("headers length = %d, want 2", len(headers))
	}
	h0 := headers[0].(map[string]interface{})
	if h0["name"] != "X-User-Type" {
		t.Error("header name wrong")
	}
	if h0["value"] != "premium" {
		t.Error("header value wrong")
	}
	if h0["type"] != "Exact" {
		t.Error("header type wrong")
	}
	h1 := headers[1].(map[string]interface{})
	if h1["invert"] != true {
		t.Error("invert should be true on second header")
	}
	cidr := sel["sourceCIDR"].(map[string]interface{})
	if cidr["value"] != "10.0.0.0/8" {
		t.Error("CIDR value wrong")
	}
	if cidr["type"] != "Distinct" {
		t.Error("CIDR type wrong")
	}
	path := sel["path"].(map[string]interface{})
	if path["value"] != "/api" {
		t.Error("path value wrong")
	}
	methods := sel["methods"].([]interface{})
	if len(methods) != 2 || methods[0] != "GET" || methods[1] != "POST" {
		t.Error("methods wrong")
	}
}

// ─── BuildClientTrafficPolicy (TLS version conversion) ─────────────────────

func TestBuildClientTrafficPolicy_TLSVersionConversion(t *testing.T) {
	minV := "TLS1.2"
	maxV := "TLSv1.3"
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp",
		Namespace: "ns",
		GatewayID: "gw-id",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: "my-gw"},
		TLS: &kubernetes.TLSPolicyConfig{
			MinVersion: &minV,
			MaxVersion: &maxV,
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	if ctp == nil {
		t.Fatal("expected non-nil")
	}
	spec := ctp.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})
	if tls["minVersion"] != "1.2" {
		t.Errorf("minVersion = %v, want 1.2", tls["minVersion"])
	}
	if tls["maxVersion"] != "1.3" {
		t.Errorf("maxVersion = %v, want 1.3", tls["maxVersion"])
	}
}

func TestBuildClientTrafficPolicy_TLSAutoVersion(t *testing.T) {
	minV := "Auto"
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp",
		Namespace: "ns",
		GatewayID: "gw-id",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "g", Kind: "Gateway", Name: "gw"},
		TLS: &kubernetes.TLSPolicyConfig{
			MinVersion: &minV,
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})
	if tls["minVersion"] != "Auto" {
		t.Errorf("minVersion = %v, want Auto", tls["minVersion"])
	}
}

func TestBuildClientTrafficPolicy_TLSCiphersAndCurves(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp",
		Namespace: "ns",
		GatewayID: "gw-id",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "g", Kind: "Gateway", Name: "gw"},
		TLS: &kubernetes.TLSPolicyConfig{
			Ciphers:             []string{"TLS_AES_128_GCM_SHA256"},
			ECDHCurves:          []string{"X25519"},
			SignatureAlgorithms: []string{"RSA-PSS-RSAE-SHA256"},
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})
	// []interface{}, not []string: an unstructured.Unstructured may only
	// hold JSON-native values, and a raw []string makes
	// runtime.DeepCopyJSONValue panic ("cannot deep copy []string") on
	// every path that copies the object. See the DeepCopy assertion below.
	ciphers := tls["ciphers"].([]interface{})
	if len(ciphers) != 1 || ciphers[0] != "TLS_AES_128_GCM_SHA256" {
		t.Error("ciphers wrong")
	}
	curves := tls["ecdhCurves"].([]interface{})
	if len(curves) != 1 || curves[0] != "X25519" {
		t.Error("ecdhCurves wrong")
	}
	sigAlgs := tls["signatureAlgorithms"].([]interface{})
	if len(sigAlgs) != 1 || sigAlgs[0] != "RSA-PSS-RSAE-SHA256" {
		t.Error("signatureAlgorithms wrong")
	}
	// The regression this guards: a []string here took down every
	// AddDomainMTLSCA request with a panic once the ClientTrafficPolicy
	// update path started copying the object.
	ctp.DeepCopy()
}

// ─── BuildClientTrafficPolicy (ClientValidation without TLS) ────────────────

func TestBuildClientTrafficPolicy_ClientValidationWithoutTLS(t *testing.T) {
	// ClientValidation without TLS config should create TLS spec section
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp",
		Namespace: "ns",
		GatewayID: "gw-id",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "g", Kind: "Gateway", Name: "gw"},
		ClientValidation: &kubernetes.ClientValidationPolicyConfig{
			Optional: true,
			CACertificateRefs: []kubernetes.SecretRefPolicyConfig{
				{Group: "", Kind: "Secret", Name: "ca-cert"},
			},
			SANMatchers: []kubernetes.SANMatcherPolicyConfig{
				{Type: "DNS", Match: "*.example.com"},
			},
			CertificateHashes: []string{"abc123"},
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	if ctp == nil {
		t.Fatal("expected non-nil")
	}
	spec := ctp.Object["spec"].(map[string]interface{})
	tls := spec["tls"].(map[string]interface{})
	cv := tls["clientValidation"].(map[string]interface{})
	if cv["optional"] != true {
		t.Error("optional should be true")
	}
	caRefs := cv["caCertificateRefs"].([]interface{})
	if len(caRefs) != 1 {
		t.Fatal("expected 1 CA cert ref")
	}
	caRef := caRefs[0].(map[string]interface{})
	if caRef["name"] != "ca-cert" {
		t.Error("CA cert ref name wrong")
	}
	sans := cv["subjectAltNames"].([]interface{})
	if len(sans) != 1 {
		t.Fatal("expected 1 SAN matcher")
	}
	san := sans[0].(map[string]interface{})
	if san["type"] != "DNS" {
		t.Error("SAN type wrong")
	}
	matchVal := san["match"].(map[string]interface{})
	if matchVal["exact"] != "*.example.com" {
		t.Error("SAN match wrong")
	}
	hashes := cv["certificateHashes"].([]interface{})
	if len(hashes) != 1 || hashes[0] != "abc123" {
		t.Error("certificate hashes wrong")
	}
	ctp.DeepCopy()
}

// ─── BuildClientTrafficPolicy (Connection with all fields) ──────────────────

func TestBuildClientTrafficPolicy_ConnectionAllFields(t *testing.T) {
	bufLimit := "64Ki"
	closeDelay := "5s"
	maxDuration := "3600s"
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp",
		Namespace: "ns",
		GatewayID: "gw-id",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "g", Kind: "Gateway", Name: "gw"},
		Connection: &kubernetes.ConnectionPolicyConfig{
			BufferLimit:              &bufLimit,
			MaxConnections:           int32Ptr(1000),
			CloseDelay:               &closeDelay,
			MaxConnectionDuration:    &maxDuration,
			MaxRequestsPerConnection: int32Ptr(100),
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	conn := spec["connection"].(map[string]interface{})
	if conn["bufferLimit"] != "64Ki" {
		t.Error("bufferLimit wrong")
	}
	connLimit := conn["connectionLimit"].(map[string]interface{})
	if connLimit["value"] != int32(1000) {
		t.Errorf("maxConnections = %v", connLimit["value"])
	}
	if connLimit["closeDelay"] != "5s" {
		t.Error("closeDelay wrong")
	}
	if connLimit["maxConnectionDuration"] != "3600s" {
		t.Error("maxConnectionDuration wrong")
	}
	if connLimit["maxRequestsPerConnection"] != int32(100) {
		t.Error("maxRequestsPerConnection wrong")
	}
}

// ─── BuildClientTrafficPolicy (CustomHeader IP detection) ──────────────────

func TestBuildClientTrafficPolicy_ClientIPDetection_CustomHeader(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp",
		Namespace: "ns",
		GatewayID: "gw-id",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "g", Kind: "Gateway", Name: "gw"},
		ClientIPDetection: &kubernetes.ClientIPDetectionPolicyConfig{
			CustomHeader: &kubernetes.CustomHeaderPolicyConfig{
				Name:       "X-Real-IP",
				FailClosed: true,
			},
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	ipDetect := spec["clientIPDetection"].(map[string]interface{})
	ch := ipDetect["customHeader"].(map[string]interface{})
	if ch["name"] != "X-Real-IP" {
		t.Error("custom header name wrong")
	}
	if ch["failClosed"] != true {
		t.Error("failClosed should be true")
	}
}

func TestBuildClientTrafficPolicy_ClientIPDetection_CustomHeaderNoFailClosed(t *testing.T) {
	config := &kubernetes.ClientTrafficPolicyConfig{
		Name:      "ctp",
		Namespace: "ns",
		GatewayID: "gw-id",
		TargetRef: kubernetes.ClientTrafficPolicyTargetRef{Group: "g", Kind: "Gateway", Name: "gw"},
		ClientIPDetection: &kubernetes.ClientIPDetectionPolicyConfig{
			CustomHeader: &kubernetes.CustomHeaderPolicyConfig{
				Name: "X-Forwarded-For",
			},
		},
	}
	ctp := kubernetes.BuildClientTrafficPolicy(config)
	spec := ctp.Object["spec"].(map[string]interface{})
	ipDetect := spec["clientIPDetection"].(map[string]interface{})
	ch := ipDetect["customHeader"].(map[string]interface{})
	if _, ok := ch["failClosed"]; ok {
		t.Error("should not have failClosed when false")
	}
}

// ─── BuildGRPCRouteObject (additional paths) ────────────────────────────────

func TestBuildGRPCRouteObject_ResponseHeaderModifier(t *testing.T) {
	config := &kubernetes.GRPCRouteConfig{
		Name:        "grpc-route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		GatewayID:   "gw-id",
		RouteID:     "rt-id",
		Hostname:    "grpc.example.com",
		ResponseHeaderModifier: &kubernetes.HTTPHeaderModifier{
			Set: []kubernetes.HTTPHeaderValue{{Name: "X-Response", Value: "modified"}},
		},
		Rules: []kubernetes.GRPCRouteRule{
			{
				BackendRefs: []kubernetes.BackendRef{{Name: "svc1", Port: 50051}},
			},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	if route == nil {
		t.Fatal("expected non-nil")
	}
	rule := route.Spec.Rules[0]
	foundResMod := false
	for _, f := range rule.Filters {
		if f.Type == gatewayv1.GRPCRouteFilterResponseHeaderModifier {
			foundResMod = true
			if f.ResponseHeaderModifier == nil {
				t.Fatal("ResponseHeaderModifier is nil")
			}
			if len(f.ResponseHeaderModifier.Set) != 1 {
				t.Error("expected 1 set header")
			}
		}
	}
	if !foundResMod {
		t.Error("missing ResponseHeaderModifier filter")
	}
}

func TestBuildGRPCRouteObject_BothHeaderModifiers(t *testing.T) {
	config := &kubernetes.GRPCRouteConfig{
		Name:        "grpc-route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "grpc.example.com",
		RequestHeaderModifier: &kubernetes.HTTPHeaderModifier{
			Add: []kubernetes.HTTPHeaderValue{{Name: "X-Req", Value: "val"}},
		},
		ResponseHeaderModifier: &kubernetes.HTTPHeaderModifier{
			Remove: []string{"Server"},
		},
		Rules: []kubernetes.GRPCRouteRule{
			{
				BackendRefs: []kubernetes.BackendRef{{Name: "svc1", Port: 50051}},
			},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	rule := route.Spec.Rules[0]
	foundReq := false
	foundRes := false
	for _, f := range rule.Filters {
		if f.Type == gatewayv1.GRPCRouteFilterRequestHeaderModifier {
			foundReq = true
		}
		if f.Type == gatewayv1.GRPCRouteFilterResponseHeaderModifier {
			foundRes = true
		}
	}
	if !foundReq {
		t.Error("missing RequestHeaderModifier filter")
	}
	if !foundRes {
		t.Error("missing ResponseHeaderModifier filter")
	}
}

func TestBuildGRPCRouteObject_NoFiltersWhenNoModifiers(t *testing.T) {
	config := &kubernetes.GRPCRouteConfig{
		Name:        "grpc-route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "grpc.example.com",
		Rules: []kubernetes.GRPCRouteRule{
			{
				BackendRefs: []kubernetes.BackendRef{{Name: "svc1", Port: 50051}},
			},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	rule := route.Spec.Rules[0]
	if len(rule.Filters) != 0 {
		t.Errorf("expected 0 filters, got %d", len(rule.Filters))
	}
}

func TestBuildGRPCRouteObject_MirrorWithNamespace(t *testing.T) {
	config := &kubernetes.GRPCRouteConfig{
		Name:        "grpc-route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "grpc.example.com",
		Mirrors: []kubernetes.MirrorRef{
			{Name: "mirror-svc", Namespace: "other-ns", Port: 50051},
		},
		Rules: []kubernetes.GRPCRouteRule{
			{
				BackendRefs: []kubernetes.BackendRef{{Name: "svc1", Port: 50051}},
			},
		},
	}
	route := kubernetes.BuildGRPCRouteObject(config)
	rule := route.Spec.Rules[0]
	foundMirror := false
	for _, f := range rule.Filters {
		if f.Type == gatewayv1.GRPCRouteFilterRequestMirror {
			foundMirror = true
			if f.RequestMirror.BackendRef.Namespace == nil {
				t.Fatal("mirror namespace is nil")
			}
			if string(*f.RequestMirror.BackendRef.Namespace) != "other-ns" {
				t.Error("mirror namespace wrong")
			}
		}
	}
	if !foundMirror {
		t.Error("missing RequestMirror filter")
	}
}

// ─── BuildHTTPRouteObject (URL rewrite only) ────────────────────────────────

func TestBuildHTTPRouteObject_URLRewriteHostnameOnly(t *testing.T) {
	hostname := "new.example.com"
	config := &kubernetes.HTTPRouteConfig{
		Name:        "route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "example.com",
		URLRewrite:  &kubernetes.HTTPURLRewrite{Hostname: &hostname},
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:    "PathPrefix",
				PathValue:   "/",
				BackendRefs: []kubernetes.BackendRef{{Name: "svc1", Port: 80}},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	rule := route.Spec.Rules[0]
	foundRewrite := false
	for _, f := range rule.Filters {
		if f.Type == gatewayv1.HTTPRouteFilterURLRewrite {
			foundRewrite = true
			if f.URLRewrite.Hostname == nil {
				t.Fatal("URLRewrite hostname is nil")
			}
			if string(*f.URLRewrite.Hostname) != "new.example.com" {
				t.Error("hostname wrong")
			}
		}
	}
	if !foundRewrite {
		t.Error("missing URLRewrite filter")
	}
}

func TestBuildHTTPRouteObject_URLRewriteNilFields(t *testing.T) {
	// When both hostname and path are nil, should not produce URLRewrite filter
	config := &kubernetes.HTTPRouteConfig{
		Name:        "route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "example.com",
		URLRewrite:  &kubernetes.HTTPURLRewrite{},
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:    "PathPrefix",
				PathValue:   "/",
				BackendRefs: []kubernetes.BackendRef{{Name: "svc1", Port: 80}},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	rule := route.Spec.Rules[0]
	for _, f := range rule.Filters {
		if f.Type == gatewayv1.HTTPRouteFilterURLRewrite {
			t.Error("should not have URLRewrite filter when both fields are nil")
		}
	}
}

// ─── BuildHTTPRouteObject (mirrors with namespace) ──────────────────────────

func TestBuildHTTPRouteObject_MirrorWithNamespace(t *testing.T) {
	config := &kubernetes.HTTPRouteConfig{
		Name:        "route",
		Namespace:   "ns",
		GatewayName: "my-gw",
		Hostname:    "example.com",
		Mirrors: []kubernetes.MirrorRef{
			{Name: "mirror-svc", Port: 8080},
			{Name: "mirror-svc2", Namespace: "cross-ns", Port: 8081},
		},
		Rules: []kubernetes.HTTPRouteRule{
			{
				PathType:    "PathPrefix",
				PathValue:   "/",
				BackendRefs: []kubernetes.BackendRef{{Name: "svc1", Port: 80}},
			},
		},
	}
	route := kubernetes.BuildHTTPRouteObject(config)
	rule := route.Spec.Rules[0]
	mirrorCount := 0
	for _, f := range rule.Filters {
		if f.Type == gatewayv1.HTTPRouteFilterRequestMirror {
			mirrorCount++
			name := string(f.RequestMirror.BackendRef.Name)
			if name == "mirror-svc" {
				if f.RequestMirror.BackendRef.Namespace != nil {
					t.Error("first mirror should not have namespace")
				}
			}
			if name == "mirror-svc2" {
				if f.RequestMirror.BackendRef.Namespace == nil {
					t.Fatal("second mirror should have namespace")
				}
				if string(*f.RequestMirror.BackendRef.Namespace) != "cross-ns" {
					t.Error("second mirror namespace wrong")
				}
			}
		}
	}
	if mirrorCount != 2 {
		t.Errorf("expected 2 mirror filters, got %d", mirrorCount)
	}
}

// ─── BuildBackendTrafficPolicy (BTP timeout all fields) ─────────────────────

func TestBuildBackendTrafficPolicy_Timeout_AllFields(t *testing.T) {
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		Timeout: &kubernetes.BTPTimeoutPolicyConfig{
			TCP: &kubernetes.BTPTCPTimeoutPolicyConfig{ConnectTimeout: "10s"},
			HTTP: &kubernetes.BTPHTTPTimeoutPolicyConfig{
				RequestTimeout:        "30s",
				ConnectionIdleTimeout: "60s",
				MaxConnectionDuration: "3600s",
				MaxStreamDuration:     "300s",
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	timeout := spec["timeout"].(map[string]interface{})
	tcp := timeout["tcp"].(map[string]interface{})
	if tcp["connectTimeout"] != "10s" {
		t.Error("connectTimeout wrong")
	}
	http := timeout["http"].(map[string]interface{})
	if http["requestTimeout"] != "30s" {
		t.Error("requestTimeout wrong")
	}
	if http["connectionIdleTimeout"] != "60s" {
		t.Error("connectionIdleTimeout wrong")
	}
	if http["maxConnectionDuration"] != "3600s" {
		t.Error("maxConnectionDuration wrong")
	}
	if http["maxStreamDuration"] != "300s" {
		t.Error("maxStreamDuration wrong")
	}
}

// ─── BuildBackendTrafficPolicy (fault injection abort gRPC) ─────────────────

func TestBuildBackendTrafficPolicy_FaultInjection_GRPCAbort(t *testing.T) {
	grpcStatus := 14
	pct := float32(50.0)
	config := &kubernetes.BackendTrafficPolicyConfig{
		Name:      "btp",
		Namespace: "ns",
		TargetRef: kubernetes.BackendTrafficPolicyTargetRef{Group: "g", Kind: "HTTPRoute", Name: "r"},
		FaultInjection: &kubernetes.FaultInjectionPolicyConfig{
			Abort: &kubernetes.FaultInjectionAbortPolicyConfig{
				GRPCStatus: &grpcStatus,
				Percentage: &pct,
			},
		},
	}
	btp := kubernetes.BuildBackendTrafficPolicy(config)
	spec := btp.Object["spec"].(map[string]interface{})
	fi := spec["faultInjection"].(map[string]interface{})
	abort := fi["abort"].(map[string]interface{})
	if abort["grpcStatus"] != 14 {
		t.Errorf("grpcStatus = %v, want 14", abort["grpcStatus"])
	}
	if abort["percentage"] != float32(50.0) {
		t.Errorf("percentage = %v", abort["percentage"])
	}
}

// ─── BuildGatewayObject (additional paths) ──────────────────────────────────

func TestBuildGatewayObject_HTTPOnly(t *testing.T) {
	config := &kubernetes.GatewayConfig{
		Name:             "gw",
		Namespace:        "ns",
		GatewayClassName: "eg",
		Hostname:         "example.com",
		TLSMode:          "no_tls",
		HTTPPort:         80,
	}
	gw := kubernetes.BuildGatewayObject(config)
	if gw == nil {
		t.Fatal("expected non-nil")
	}
	spec := gw.Object["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	if len(listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(listeners))
	}
	l := listeners[0].(map[string]interface{})
	if l["protocol"] != "HTTP" {
		t.Errorf("protocol = %v, want HTTP", l["protocol"])
	}
	if l["port"] != int64(80) {
		t.Errorf("port = %v, want 80", l["port"])
	}
}

// ─── BuildHTTPRouteFilter (no direct response) ─────────────────────────────

func TestBuildHTTPRouteFilter_NilDirectResponse(t *testing.T) {
	config := &kubernetes.HTTPRouteFilterConfig{
		Name:      "hrf",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
	}
	hrf := kubernetes.BuildHTTPRouteFilter(config)
	if hrf == nil {
		t.Fatal("expected non-nil")
	}
	spec := hrf.Object["spec"].(map[string]interface{})
	if _, ok := spec["directResponse"]; ok {
		t.Error("should not have directResponse when nil")
	}
}

// ─── BuildEnvoyExtensionPolicy (additional paths) ───────────────────────────

func TestBuildEnvoyExtensionPolicy_Wasm_ImageWithPullSecret(t *testing.T) {
	wasmConfig := `{"key": "value"}`
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "my-route"},
		Wasm: []kubernetes.WasmExtensionPolicyConfig{
			{
				Name:   "my-wasm",
				RootID: "root",
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "Image",
					Image: &kubernetes.WasmImageSourcePolicyConfig{
						URL:    "oci://registry.example.com/wasm:latest",
						SHA256: "abc123",
						PullSecret: &kubernetes.ValueRefPolicyConfig{
							Group: "", Kind: "Secret", Name: "pull-secret",
						},
					},
				},
				Config: &wasmConfig,
			},
		},
	}
	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	if eep == nil {
		t.Fatal("expected non-nil")
	}
	spec := eep.Object["spec"].(map[string]interface{})
	wasms := spec["wasm"].([]map[string]interface{})
	if len(wasms) != 1 {
		t.Fatal("expected 1 wasm entry")
	}
	w := wasms[0]
	code := w["code"].(map[string]interface{})
	image := code["image"].(map[string]interface{})
	if image["sha256"] != "abc123" {
		t.Error("sha256 wrong")
	}
	pullSecretRef := image["pullSecretRef"].(map[string]interface{})
	if pullSecretRef["name"] != "pull-secret" {
		t.Error("pull secret name wrong")
	}
}

// ─── BuildExtProcBackend ─────────────────────────────────────────────────────

func TestBuildExtProcBackend(t *testing.T) {
	config := &kubernetes.ExtProcBackendConfig{
		Name:      "my-ext-proc-backend",
		Namespace: "test-ns",
		GatewayID: "gw-123",
		RouteID:   "rt-456",
		Service: kubernetes.ExtProcBackendRefPolicyConfig{
			Name:      "ext-proc-svc",
			Namespace: "ext-ns",
			Port:      9001,
		},
	}

	backend := kubernetes.BuildExtProcBackend(config)
	require.NotNil(t, backend)

	assert.Equal(t, "gateway.envoyproxy.io/v1alpha1", backend.Object["apiVersion"])
	assert.Equal(t, "Backend", backend.Object["kind"])

	metadata := backend.Object["metadata"].(map[string]interface{})
	assert.Equal(t, "my-ext-proc-backend", metadata["name"])
	assert.Equal(t, "test-ns", metadata["namespace"])

	labels := metadata["labels"].(map[string]interface{})
	assert.Equal(t, "fastgateway", labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "gw-123", labels["fastgateway.dev/gateway-id"])
	assert.Equal(t, "rt-456", labels["fastgateway.dev/route-id"])
	assert.Equal(t, "ext-proc", labels["fastgateway.dev/type"])

	spec := backend.Object["spec"].(map[string]interface{})
	endpoints := spec["endpoints"].([]interface{})
	require.Len(t, endpoints, 1)

	ep := endpoints[0].(map[string]interface{})
	fqdn := ep["fqdn"].(map[string]interface{})
	assert.Equal(t, "ext-proc-svc.ext-ns.svc.cluster.local", fqdn["hostname"])
	assert.Equal(t, int64(9001), fqdn["port"])
}

func TestBuildExtProcBackend_Nil(t *testing.T) {
	backend := kubernetes.BuildExtProcBackend(nil)
	assert.Nil(t, backend)
}

// ─── GenerateExtProcBackendName ──────────────────────────────────────────────

func TestGenerateExtProcBackendName(t *testing.T) {
	name := kubernetes.GenerateExtProcBackendName("route-abc-123")
	assert.Equal(t, "ext-proc-backend-route-abc-123", name)
}

// ─── BuildEnvoyExtensionPolicy – ext-proc paths ─────────────────────────────

func TestBuildEnvoyExtensionPolicy_ExtProc(t *testing.T) {
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-extproc",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "my-route"},
		ExtProc: []kubernetes.ExtProcPolicyConfig{
			{
				BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
					Name:      "ext-svc",
					Namespace: "ext-ns",
					Port:      9001,
				},
				ProcessingMode: &kubernetes.ExtProcProcessingModeConfig{
					Request:  &kubernetes.ExtProcBodyModeConfig{Body: "Buffered"},
					Response: &kubernetes.ExtProcBodyModeConfig{Body: "Streamed"},
				},
				FailOpen: true,
			},
		},
	}

	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	require.NotNil(t, eep)

	spec := eep.Object["spec"].(map[string]interface{})
	extProcList := spec["extProc"].([]interface{})
	require.Len(t, extProcList, 1)

	entry := extProcList[0].(map[string]interface{})

	// backendRefs
	refs := entry["backendRefs"].([]interface{})
	require.Len(t, refs, 1)
	ref := refs[0].(map[string]interface{})
	assert.Equal(t, "Backend", ref["kind"])
	assert.Equal(t, kubernetes.GenerateExtProcBackendName("rt-id"), ref["name"])
	assert.Equal(t, "ns", ref["namespace"])

	// processingMode
	pm := entry["processingMode"].(map[string]interface{})
	reqMode := pm["request"].(map[string]interface{})
	assert.Equal(t, "Buffered", reqMode["body"])
	respMode := pm["response"].(map[string]interface{})
	assert.Equal(t, "Streamed", respMode["body"])

	// failOpen
	assert.Equal(t, true, entry["failOpen"])
}

func TestBuildEnvoyExtensionPolicy_ExtProc_Minimal(t *testing.T) {
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-minimal",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "my-route"},
		ExtProc: []kubernetes.ExtProcPolicyConfig{
			{
				BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
					Name:      "ext-svc",
					Namespace: "ext-ns",
					Port:      9001,
				},
				// No ProcessingMode, no FailOpen
			},
		},
	}

	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	require.NotNil(t, eep)

	spec := eep.Object["spec"].(map[string]interface{})
	extProcList := spec["extProc"].([]interface{})
	require.Len(t, extProcList, 1)

	entry := extProcList[0].(map[string]interface{})
	_, hasPM := entry["processingMode"]
	assert.False(t, hasPM, "minimal ext-proc should not have processingMode")
	_, hasFO := entry["failOpen"]
	assert.False(t, hasFO, "minimal ext-proc should not have failOpen")
}

func TestBuildEnvoyExtensionPolicy_ExtProc_WithLuaAndWasm(t *testing.T) {
	wasmCfg := `{"key":"val"}`
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-all",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "my-route"},
		Lua: []kubernetes.LuaExtensionPolicyConfig{
			{Type: "Inline", Inline: "print('hello')"},
		},
		Wasm: []kubernetes.WasmExtensionPolicyConfig{
			{
				Name:   "my-wasm",
				RootID: "root",
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "HTTP",
					HTTP: &kubernetes.WasmHTTPSourcePolicyConfig{
						URL:    "https://example.com/filter.wasm",
						SHA256: "deadbeef",
					},
				},
				Config: &wasmCfg,
			},
		},
		ExtProc: []kubernetes.ExtProcPolicyConfig{
			{
				BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
					Name:      "ext-svc",
					Namespace: "ext-ns",
					Port:      9001,
				},
				FailOpen: true,
			},
		},
	}

	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	require.NotNil(t, eep)

	spec := eep.Object["spec"].(map[string]interface{})
	assert.Contains(t, spec, "lua", "should contain lua")
	assert.Contains(t, spec, "wasm", "should contain wasm")
	assert.Contains(t, spec, "extProc", "should contain extProc")

	luaList := spec["lua"].([]map[string]interface{})
	assert.Len(t, luaList, 1)

	wasmList := spec["wasm"].([]map[string]interface{})
	assert.Len(t, wasmList, 1)

	extProcList := spec["extProc"].([]interface{})
	assert.Len(t, extProcList, 1)
}

func TestBuildEnvoyExtensionPolicy_ExtProc_WithWaf(t *testing.T) {
	wasmCfg := `{"rules":"SecRule"}`
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-waf-extproc",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "my-route"},
		Wasm: []kubernetes.WasmExtensionPolicyConfig{
			{
				Name:   "waf",
				RootID: "waf-root",
				Code: kubernetes.WasmCodeSourcePolicyConfig{
					Type: "HTTP",
					HTTP: &kubernetes.WasmHTTPSourcePolicyConfig{
						URL:    "https://example.com/waf.wasm",
						SHA256: "wafhash",
					},
				},
				Config: &wasmCfg,
			},
		},
		ExtProc: []kubernetes.ExtProcPolicyConfig{
			{
				BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
					Name:      "ext-svc",
					Namespace: "ext-ns",
					Port:      9001,
				},
				ProcessingMode: &kubernetes.ExtProcProcessingModeConfig{
					Request: &kubernetes.ExtProcBodyModeConfig{Body: "Buffered"},
				},
			},
		},
	}

	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	require.NotNil(t, eep)

	spec := eep.Object["spec"].(map[string]interface{})
	assert.Contains(t, spec, "wasm")
	assert.Contains(t, spec, "extProc")

	wasmList := spec["wasm"].([]map[string]interface{})
	assert.Len(t, wasmList, 1)
	assert.Equal(t, "waf", wasmList[0]["name"])

	extProcList := spec["extProc"].([]interface{})
	require.Len(t, extProcList, 1)
	entry := extProcList[0].(map[string]interface{})
	pm := entry["processingMode"].(map[string]interface{})
	reqMode := pm["request"].(map[string]interface{})
	assert.Equal(t, "Buffered", reqMode["body"])
}

func TestBuildEnvoyExtensionPolicy_ExtProc_NoneBodyMode(t *testing.T) {
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-none-body",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "my-route"},
		ExtProc: []kubernetes.ExtProcPolicyConfig{
			{
				BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
					Name:      "ext-svc",
					Namespace: "ext-ns",
					Port:      9001,
				},
				ProcessingMode: &kubernetes.ExtProcProcessingModeConfig{
					Request:  &kubernetes.ExtProcBodyModeConfig{Body: "None"},
					Response: &kubernetes.ExtProcBodyModeConfig{Body: "None"},
				},
			},
		},
	}

	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	require.NotNil(t, eep)

	spec := eep.Object["spec"].(map[string]interface{})
	extProcList := spec["extProc"].([]interface{})
	require.Len(t, extProcList, 1)

	entry := extProcList[0].(map[string]interface{})
	_, hasPM := entry["processingMode"]
	assert.False(t, hasPM, "None body mode should not produce processingMode at all")
}

func TestBuildEnvoyExtensionPolicy_ExtProcOnly(t *testing.T) {
	config := &kubernetes.EnvoyExtensionPolicyK8sConfig{
		Name:      "eep-extproc-only",
		Namespace: "ns",
		GatewayID: "gw-id",
		RouteID:   "rt-id",
		TargetRef: kubernetes.EnvoyExtensionPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "my-route"},
		ExtProc: []kubernetes.ExtProcPolicyConfig{
			{
				BackendRef: kubernetes.ExtProcBackendRefPolicyConfig{
					Name:      "ext-svc",
					Namespace: "ext-ns",
					Port:      9001,
				},
				FailOpen: true,
			},
		},
	}

	eep := kubernetes.BuildEnvoyExtensionPolicy(config)
	require.NotNil(t, eep)

	assert.Equal(t, "gateway.envoyproxy.io/v1alpha1", eep.Object["apiVersion"])
	assert.Equal(t, "EnvoyExtensionPolicy", eep.Object["kind"])

	metadata := eep.Object["metadata"].(map[string]interface{})
	assert.Equal(t, "eep-extproc-only", metadata["name"])
	assert.Equal(t, "ns", metadata["namespace"])

	labels := metadata["labels"].(map[string]interface{})
	assert.Equal(t, "fastgateway", labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "gw-id", labels["fastgateway.dev/gateway-id"])
	assert.Equal(t, "rt-id", labels["fastgateway.dev/route-id"])

	spec := eep.Object["spec"].(map[string]interface{})

	// no lua or wasm
	_, hasLua := spec["lua"]
	assert.False(t, hasLua, "should not have lua")
	_, hasWasm := spec["wasm"]
	assert.False(t, hasWasm, "should not have wasm")

	// extProc present
	extProcList := spec["extProc"].([]interface{})
	require.Len(t, extProcList, 1)
	entry := extProcList[0].(map[string]interface{})
	assert.Equal(t, true, entry["failOpen"])

	// targetRefs
	targetRefs := spec["targetRefs"].([]map[string]interface{})
	require.Len(t, targetRefs, 1)
	assert.Equal(t, "gateway.networking.k8s.io", targetRefs[0]["group"])
	assert.Equal(t, "HTTPRoute", targetRefs[0]["kind"])
	assert.Equal(t, "my-route", targetRefs[0]["name"])
}

// ─── Header & Method Authorization Tests ─────────────────────────────────────

func TestBuildSecurityPolicy_Authorization_HeadersOnly(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-headers",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action: "Allow",
					Headers: []kubernetes.HeaderMatchPolicyConfig{
						{Name: "x-team", Values: []string{"backend", "frontend"}},
						{Name: "x-env", Values: []string{"production"}},
					},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	require.NotNil(t, sp)

	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	assert.Equal(t, "Deny", auth["defaultAction"])

	rules := auth["rules"].([]interface{})
	require.Len(t, rules, 1)

	rule := rules[0].(map[string]interface{})
	assert.Equal(t, "Allow", rule["action"])

	principal := rule["principal"].(map[string]interface{})
	// Should NOT have clientCIDRs
	_, hasCIDRs := principal["clientCIDRs"]
	assert.False(t, hasCIDRs, "should not have clientCIDRs when only headers are set")

	headers := principal["headers"].([]interface{})
	require.Len(t, headers, 2)
	h0 := headers[0].(map[string]interface{})
	assert.Equal(t, "x-team", h0["name"])
	h0Values := h0["values"].([]interface{})
	assert.Equal(t, []interface{}{"backend", "frontend"}, h0Values)

	h1 := headers[1].(map[string]interface{})
	assert.Equal(t, "x-env", h1["name"])

	// Should NOT have operation
	_, hasOp := rule["operation"]
	assert.False(t, hasOp, "should not have operation when no methods are set")
}

func TestBuildSecurityPolicy_Authorization_MethodsOnly(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-methods",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action:  "Allow",
					Methods: []string{"GET", "POST"},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	require.NotNil(t, sp)

	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	rules := auth["rules"].([]interface{})
	require.Len(t, rules, 1)

	rule := rules[0].(map[string]interface{})
	// Should have operation with methods
	operation := rule["operation"].(map[string]interface{})
	methods := operation["methods"].([]interface{})
	assert.Equal(t, []interface{}{"GET", "POST"}, methods)

	// Should have empty principal (no CIDRs, no headers)
	principal := rule["principal"].(map[string]interface{})
	_, hasCIDRs := principal["clientCIDRs"]
	assert.False(t, hasCIDRs)
	_, hasHeaders := principal["headers"]
	assert.False(t, hasHeaders)
}

func TestBuildSecurityPolicy_Authorization_CIDRsHeadersMethods(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-combined",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action:      "Allow",
					ClientCIDRs: []string{"10.0.0.0/8"},
					Headers: []kubernetes.HeaderMatchPolicyConfig{
						{Name: "x-user-id", Values: []string{"admin"}},
					},
					Methods: []string{"GET", "DELETE"},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	require.NotNil(t, sp)

	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	rules := auth["rules"].([]interface{})
	require.Len(t, rules, 1)

	rule := rules[0].(map[string]interface{})
	principal := rule["principal"].(map[string]interface{})

	// All three should be present
	cidrs := principal["clientCIDRs"].([]interface{})
	assert.Len(t, cidrs, 1)
	assert.Equal(t, "10.0.0.0/8", cidrs[0])

	headers := principal["headers"].([]interface{})
	require.Len(t, headers, 1)
	assert.Equal(t, "x-user-id", headers[0].(map[string]interface{})["name"])

	operation := rule["operation"].(map[string]interface{})
	methods := operation["methods"].([]interface{})
	assert.Equal(t, []interface{}{"GET", "DELETE"}, methods)
}

func TestBuildSecurityPolicy_Authorization_HeadersAndJWT(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-headers-jwt",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action: "Allow",
					Headers: []kubernetes.HeaderMatchPolicyConfig{
						{Name: "x-team", Values: []string{"api"}},
					},
					JWT: &kubernetes.JWTPrincipalPolicyConfig{
						Provider: "my-jwt",
						Claims: []kubernetes.JWTClaimRulePolicyConfig{
							{Name: "role", Values: []string{"admin"}},
						},
					},
					Methods: []string{"POST"},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	require.NotNil(t, sp)

	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	rules := auth["rules"].([]interface{})
	require.Len(t, rules, 1)

	rule := rules[0].(map[string]interface{})
	principal := rule["principal"].(map[string]interface{})

	// Headers present
	headers := principal["headers"].([]interface{})
	require.Len(t, headers, 1)

	// JWT present
	jwt := principal["jwt"].(map[string]interface{})
	assert.Equal(t, "my-jwt", jwt["provider"])

	// Methods present
	operation := rule["operation"].(map[string]interface{})
	methods := operation["methods"].([]interface{})
	assert.Equal(t, []interface{}{"POST"}, methods)
}

func TestBuildSecurityPolicy_Authorization_SkipRuleWithNoContent(t *testing.T) {
	config := &kubernetes.SecurityPolicyConfig{
		Name:      "sp-skip-empty",
		Namespace: "ns",
		TargetRef: kubernetes.SecurityPolicyTargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: "rt"},
		Authorization: &kubernetes.AuthorizationPolicyConfig{
			DefaultAction: "Deny",
			Rules: []kubernetes.AuthorizationRulePolicyConfig{
				{
					Action: "Allow",
					// No CIDRs, no headers, no methods, no JWT — should be skipped
				},
				{
					Action:  "Allow",
					Methods: []string{"GET"},
				},
			},
		},
	}
	sp := kubernetes.BuildSecurityPolicy(config)
	require.NotNil(t, sp)

	spec := sp.Object["spec"].(map[string]interface{})
	auth := spec["authorization"].(map[string]interface{})
	rules := auth["rules"].([]interface{})
	// First rule should be skipped, only second should remain
	require.Len(t, rules, 1)
	rule := rules[0].(map[string]interface{})
	operation := rule["operation"].(map[string]interface{})
	methods := operation["methods"].([]interface{})
	assert.Equal(t, []interface{}{"GET"}, methods)
}

// ─── TLS Secret Picker Tests ───────────────────────────────────────────────

func TestBuildGatewayObject_CrossNamespaceTLSSecret(t *testing.T) {
	config := &kubernetes.GatewayConfig{
		Name:               "test-gw",
		Namespace:          "fastgateway-system",
		GatewayClassName:   "eg",
		Hostname:           "example.com",
		TLSMode:            "tls_only",
		HTTPPort:           80,
		HTTPSPort:          443,
		TLSSecretName:      "my-cert",
		TLSSecretNamespace: "production",
		TLSPolicy:          "terminate",
	}

	gw := kubernetes.BuildGatewayObject(config)
	require.NotNil(t, gw)

	spec := gw.Object["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	require.Len(t, listeners, 1)

	listener := listeners[0].(map[string]interface{})
	tls := listener["tls"].(map[string]interface{})
	certRefs := tls["certificateRefs"].([]interface{})
	require.Len(t, certRefs, 1)

	certRef := certRefs[0].(map[string]interface{})
	assert.Equal(t, "Secret", certRef["kind"])
	assert.Equal(t, "my-cert", certRef["name"])
	assert.Equal(t, "production", certRef["namespace"])
}

func TestBuildGatewayObject_SameNamespaceTLSSecret(t *testing.T) {
	config := &kubernetes.GatewayConfig{
		Name:             "test-gw",
		Namespace:        "fastgateway-system",
		GatewayClassName: "eg",
		Hostname:         "example.com",
		TLSMode:          "tls_only",
		HTTPPort:         80,
		HTTPSPort:        443,
		TLSSecretName:    "my-cert",
		TLSPolicy:        "terminate",
		// TLSSecretNamespace is empty — same namespace
	}

	gw := kubernetes.BuildGatewayObject(config)
	require.NotNil(t, gw)

	spec := gw.Object["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	require.Len(t, listeners, 1)

	listener := listeners[0].(map[string]interface{})
	tls := listener["tls"].(map[string]interface{})
	certRefs := tls["certificateRefs"].([]interface{})
	certRef := certRefs[0].(map[string]interface{})

	assert.Equal(t, "Secret", certRef["kind"])
	assert.Equal(t, "my-cert", certRef["name"])
	_, hasNamespace := certRef["namespace"]
	assert.False(t, hasNamespace, "should not include namespace when same as gateway namespace")
}

func TestBuildGatewayObject_FastgatewaySystemNamespaceOmitted(t *testing.T) {
	config := &kubernetes.GatewayConfig{
		Name:               "test-gw",
		Namespace:          "fastgateway-system",
		GatewayClassName:   "eg",
		Hostname:           "example.com",
		TLSMode:            "tls_only",
		HTTPPort:           80,
		HTTPSPort:          443,
		TLSSecretName:      "my-cert",
		TLSSecretNamespace: "fastgateway-system",
		TLSPolicy:          "terminate",
	}

	gw := kubernetes.BuildGatewayObject(config)
	require.NotNil(t, gw)

	spec := gw.Object["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	listener := listeners[0].(map[string]interface{})
	tls := listener["tls"].(map[string]interface{})
	certRefs := tls["certificateRefs"].([]interface{})
	certRef := certRefs[0].(map[string]interface{})

	_, hasNamespace := certRef["namespace"]
	assert.False(t, hasNamespace, "should not include namespace when it equals the gateway namespace")
}

func TestBuildAccessLog_FileStdout_Text(t *testing.T) {
	cfg := &models.TelemetryAccessLogConfig{
		Format: models.TelemetryAccessLogFormat{
			Type: "text",
			Text: "[%START_TIME%]",
		},
		Sink: models.TelemetryAccessLogSink{
			Type: "file",
			File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"},
		},
	}
	got := kubernetes.BuildAccessLog(cfg)
	want := map[string]interface{}{
		"settings": []interface{}{
			map[string]interface{}{
				"format": map[string]interface{}{
					"type": "Text",
					"text": "[%START_TIME%]",
				},
				"sinks": []interface{}{
					map[string]interface{}{
						"type": "File",
						"file": map[string]interface{}{
							"path": "/dev/stdout",
						},
					},
				},
			},
		},
	}
	assert.Equal(t, want, got)
}

func TestBuildAccessLog_FileStderr(t *testing.T) {
	cfg := &models.TelemetryAccessLogConfig{
		Format: models.TelemetryAccessLogFormat{Type: "text", Text: "X"},
		Sink: models.TelemetryAccessLogSink{
			Type: "file",
			File: &models.TelemetryAccessLogFileSink{Path: "/dev/stderr"},
		},
	}
	got := kubernetes.BuildAccessLog(cfg)
	sinks := got["settings"].([]interface{})[0].(map[string]interface{})["sinks"].([]interface{})
	assert.Equal(t, "/dev/stderr", sinks[0].(map[string]interface{})["file"].(map[string]interface{})["path"])
}

func TestBuildAccessLog_OTel(t *testing.T) {
	cfg := &models.TelemetryAccessLogConfig{
		Format: models.TelemetryAccessLogFormat{Type: "text", Text: "X"},
		Sink: models.TelemetryAccessLogSink{
			Type: "otel",
			OTel: &models.TelemetryAccessLogOTelSink{
				Namespace: "obs", Service: "col", Port: 4317,
			},
		},
	}
	got := kubernetes.BuildAccessLog(cfg)
	sinks := got["settings"].([]interface{})[0].(map[string]interface{})["sinks"].([]interface{})
	require.Len(t, sinks, 1)
	sink := sinks[0].(map[string]interface{})
	assert.Equal(t, "OpenTelemetry", sink["type"])
	refs := sink["openTelemetry"].(map[string]interface{})["backendRefs"].([]interface{})
	require.Len(t, refs, 1)
	ref := refs[0].(map[string]interface{})
	assert.Equal(t, "obs", ref["namespace"])
	assert.Equal(t, "col", ref["name"])
	assert.EqualValues(t, 4317, ref["port"])
}

func TestBuildAccessLog_JsonFormat(t *testing.T) {
	cfg := &models.TelemetryAccessLogConfig{
		Format: models.TelemetryAccessLogFormat{
			Type: "json",
			JSON: map[string]string{"method": "%REQ(:METHOD)%", "status": "%RESPONSE_CODE%"},
		},
		Sink: models.TelemetryAccessLogSink{
			Type: "file",
			File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"},
		},
	}
	got := kubernetes.BuildAccessLog(cfg)
	format := got["settings"].([]interface{})[0].(map[string]interface{})["format"].(map[string]interface{})
	assert.Equal(t, "JSON", format["type"])
	assert.Equal(t, "%REQ(:METHOD)%", format["json"].(map[string]interface{})["method"])
	assert.Equal(t, "%RESPONSE_CODE%", format["json"].(map[string]interface{})["status"])
}

func TestBuildAccessLog_Disabled(t *testing.T) {
	cfg := &models.TelemetryAccessLogConfig{
		Format: models.TelemetryAccessLogFormat{Type: "disabled"},
		Sink: models.TelemetryAccessLogSink{
			Type: "file",
			File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"},
		},
	}
	got := kubernetes.BuildAccessLog(cfg)
	settings := got["settings"].([]interface{})
	require.Len(t, settings, 1)
	format := settings[0].(map[string]interface{})["format"].(map[string]interface{})
	assert.Equal(t, "Disabled", format["type"])
	_, hasText := format["text"]
	_, hasJSON := format["json"]
	assert.False(t, hasText)
	assert.False(t, hasJSON)
}

func TestBuildAccessLog_Nil(t *testing.T) {
	assert.Nil(t, kubernetes.BuildAccessLog(nil))
}

func TestBuildTracing_BasicSampling(t *testing.T) {
	cfg := &models.TelemetryTracingConfig{
		SamplingRate: 10,
		Provider: models.TelemetryServiceRef{
			Namespace: "obs", Service: "col", Port: 4317,
		},
	}
	got := kubernetes.BuildTracing(cfg)
	assert.EqualValues(t, 10.0, got["samplingRate"])
	provider := got["provider"].(map[string]interface{})
	refs := provider["backendRefs"].([]interface{})
	require.Len(t, refs, 1)
	ref := refs[0].(map[string]interface{})
	assert.Equal(t, "col", ref["name"])
	assert.Equal(t, "obs", ref["namespace"])
	assert.EqualValues(t, 4317, ref["port"])
	_, hasTags := got["customTags"]
	assert.False(t, hasTags, "no customTags entry when none configured")
}

func TestBuildTracing_LiteralTag(t *testing.T) {
	cfg := &models.TelemetryTracingConfig{
		SamplingRate: 1,
		Provider:     models.TelemetryServiceRef{Namespace: "obs", Service: "col", Port: 4317},
		CustomTags: []models.TelemetryTracingTag{
			{Type: "literal", Tag: "env", Value: "prod"},
		},
	}
	got := kubernetes.BuildTracing(cfg)
	// EG CRD shape: customTags is a map keyed by tag name, not an array.
	tags := got["customTags"].(map[string]interface{})
	require.Len(t, tags, 1)
	tag := tags["env"].(map[string]interface{})
	assert.Equal(t, "Literal", tag["type"])
	assert.Equal(t, "prod", tag["literal"].(map[string]interface{})["value"])
}

func TestBuildTracing_RequestHeaderTag(t *testing.T) {
	cfg := &models.TelemetryTracingConfig{
		SamplingRate: 1,
		Provider:     models.TelemetryServiceRef{Namespace: "obs", Service: "col", Port: 4317},
		CustomTags: []models.TelemetryTracingTag{
			{Type: "requestHeader", Tag: "tenant_id", Header: "x-tenant-id", DefaultValue: "unknown"},
		},
	}
	got := kubernetes.BuildTracing(cfg)
	tags := got["customTags"].(map[string]interface{})
	require.Len(t, tags, 1)
	tag := tags["tenant_id"].(map[string]interface{})
	assert.Equal(t, "RequestHeader", tag["type"])
	rh := tag["requestHeader"].(map[string]interface{})
	assert.Equal(t, "x-tenant-id", rh["name"])
	assert.Equal(t, "unknown", rh["defaultValue"])
}

func TestBuildTracing_RequestHeaderTag_NoDefault(t *testing.T) {
	cfg := &models.TelemetryTracingConfig{
		SamplingRate: 1,
		Provider:     models.TelemetryServiceRef{Namespace: "obs", Service: "col", Port: 4317},
		CustomTags: []models.TelemetryTracingTag{
			{Type: "requestHeader", Tag: "tenant_id", Header: "x-tenant-id"},
		},
	}
	got := kubernetes.BuildTracing(cfg)
	tag := got["customTags"].(map[string]interface{})["tenant_id"].(map[string]interface{})
	rh := tag["requestHeader"].(map[string]interface{})
	_, hasDefault := rh["defaultValue"]
	assert.False(t, hasDefault, "defaultValue omitted when blank")
}

func TestBuildTracing_OffSampling(t *testing.T) {
	cfg := &models.TelemetryTracingConfig{
		SamplingRate: 0,
		Provider:     models.TelemetryServiceRef{Namespace: "obs", Service: "col", Port: 4317},
	}
	got := kubernetes.BuildTracing(cfg)
	assert.EqualValues(t, 0.0, got["samplingRate"])
}

func TestBuildTracing_Nil(t *testing.T) {
	assert.Nil(t, kubernetes.BuildTracing(nil))
}

func TestBuildMetrics_PromDisabled(t *testing.T) {
	cfg := &models.TelemetryMetricsConfig{
		Prometheus: &models.TelemetryPrometheusConfig{Disable: true},
	}
	got := kubernetes.BuildMetrics(cfg)
	prom := got["prometheus"].(map[string]interface{})
	assert.Equal(t, true, prom["disable"])
}

func TestBuildMetrics_VirtualHostStatsOn(t *testing.T) {
	cfg := &models.TelemetryMetricsConfig{EnableVirtualHostStats: true}
	got := kubernetes.BuildMetrics(cfg)
	assert.Equal(t, true, got["enableVirtualHostStats"])
	_, hasPerEndpoint := got["enablePerEndpointStats"]
	assert.False(t, hasPerEndpoint, "false flag is omitted")
}

func TestBuildMetrics_PerEndpointStatsOn(t *testing.T) {
	cfg := &models.TelemetryMetricsConfig{EnablePerEndpointStats: true}
	got := kubernetes.BuildMetrics(cfg)
	assert.Equal(t, true, got["enablePerEndpointStats"])
}

func TestBuildMetrics_OTelSink(t *testing.T) {
	cfg := &models.TelemetryMetricsConfig{
		Sinks: []models.TelemetryMetricsSink{
			{Type: "openTelemetry", Namespace: "obs", Service: "col", Port: 4317},
		},
	}
	got := kubernetes.BuildMetrics(cfg)
	sinks := got["sinks"].([]interface{})
	require.Len(t, sinks, 1)
	sink := sinks[0].(map[string]interface{})
	assert.Equal(t, "OpenTelemetry", sink["type"])
	refs := sink["openTelemetry"].(map[string]interface{})["backendRefs"].([]interface{})
	ref := refs[0].(map[string]interface{})
	assert.Equal(t, "col", ref["name"])
	assert.Equal(t, "obs", ref["namespace"])
	assert.EqualValues(t, 4317, ref["port"])
}

func TestBuildMetrics_AllOff_ReturnsEmptyMap(t *testing.T) {
	cfg := &models.TelemetryMetricsConfig{}
	got := kubernetes.BuildMetrics(cfg)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestBuildMetrics_Nil(t *testing.T) {
	assert.Nil(t, kubernetes.BuildMetrics(nil))
}

func TestBuildEnvoyProxyObject_TelemetryNil(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:        "ep-1",
		Namespace:   "envoy-gateway-system",
		ServiceType: "ClusterIP",
	}
	obj := kubernetes.BuildEnvoyProxyObject(config)
	spec := obj.Object["spec"].(map[string]interface{})
	_, has := spec["telemetry"]
	assert.False(t, has, "spec.telemetry not emitted when all three are nil")
}

func TestBuildEnvoyProxyObject_TelemetryAccessLogOnly(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:        "ep-1",
		Namespace:   "envoy-gateway-system",
		ServiceType: "ClusterIP",
		TelemetryAccessLog: &models.TelemetryAccessLogConfig{
			Format: models.TelemetryAccessLogFormat{Type: "text", Text: "X"},
			Sink: models.TelemetryAccessLogSink{
				Type: "file",
				File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"},
			},
		},
	}
	obj := kubernetes.BuildEnvoyProxyObject(config)
	spec := obj.Object["spec"].(map[string]interface{})
	tele, ok := spec["telemetry"].(map[string]interface{})
	require.True(t, ok, "spec.telemetry must be present")
	_, hasAL := tele["accessLog"]
	assert.True(t, hasAL)
	_, hasTracing := tele["tracing"]
	assert.False(t, hasTracing)
	_, hasMetrics := tele["metrics"]
	assert.False(t, hasMetrics)
}

func TestBuildEnvoyProxyObject_TelemetryAllThreeSet(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:        "ep-1",
		Namespace:   "envoy-gateway-system",
		ServiceType: "ClusterIP",
		TelemetryAccessLog: &models.TelemetryAccessLogConfig{
			Format: models.TelemetryAccessLogFormat{Type: "text", Text: "X"},
			Sink: models.TelemetryAccessLogSink{
				Type: "file",
				File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"},
			},
		},
		TelemetryTracing: &models.TelemetryTracingConfig{
			SamplingRate: 1,
			Provider:     models.TelemetryServiceRef{Namespace: "obs", Service: "col", Port: 4317},
		},
		TelemetryMetrics: &models.TelemetryMetricsConfig{EnablePerEndpointStats: true},
	}
	obj := kubernetes.BuildEnvoyProxyObject(config)
	tele := obj.Object["spec"].(map[string]interface{})["telemetry"].(map[string]interface{})
	assert.Contains(t, tele, "accessLog")
	assert.Contains(t, tele, "tracing")
	assert.Contains(t, tele, "metrics")
}

// ─── BuildPodPlacement ────────────────────────────────────────────────────────

func TestBuildPodPlacement_NodeSelectorAndPriorityClass(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		NodeSelector:      map[string]string{"name": "nodepool-01"},
		PriorityClassName: "system-cluster-critical",
	}
	got := kubernetes.BuildPodPlacement(cfg, "fastgateway-template-1")
	assert.Equal(t, map[string]interface{}{
		"name": "nodepool-01",
	}, got["nodeSelector"])
	// priorityClassName is intentionally NOT emitted (EG's EnvoyProxy CRD does
	// not expose it); it stays in the model for forward-compat only.
	_, hasPriorityClass := got["priorityClassName"]
	assert.False(t, hasPriorityClass, "priorityClassName is intentionally not emitted")
	_, hasTolerations := got["tolerations"]
	assert.False(t, hasTolerations, "no tolerations key when none configured")
	_, hasTopo := got["topologySpreadConstraints"]
	assert.False(t, hasTopo, "no topology key when none configured")
}

func TestBuildPodPlacement_Nil(t *testing.T) {
	assert.Nil(t, kubernetes.BuildPodPlacement(nil, "fastgateway-template-1"))
}

func TestBuildPodPlacement_AllEmpty(t *testing.T) {
	// A non-nil cfg with all default zero values should return nil so
	// BuildEnvoyProxyObject does not emit empty envoyDeployment.pod sub-blocks.
	cfg := &models.PodPlacementConfig{}
	got := kubernetes.BuildPodPlacement(cfg, "fastgateway-template-1")
	assert.Nil(t, got)
}

func TestBuildStrategy_EmptyTypeReturnsNil(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{}
	assert.Nil(t, kubernetes.BuildStrategy(cfg))
}

func TestBuildPodPlacement_TolerationsEqualNoSchedule(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		Tolerations: []models.TolerationConfig{
			{Key: "dedicated", Operator: "Equal", Value: "gateway", Effect: "NoSchedule"},
		},
	}
	got := kubernetes.BuildPodPlacement(cfg, "gc-1")
	tols := got["tolerations"].([]interface{})
	require.Len(t, tols, 1)
	row := tols[0].(map[string]interface{})
	assert.Equal(t, "dedicated", row["key"])
	assert.Equal(t, "Equal", row["operator"])
	assert.Equal(t, "gateway", row["value"])
	assert.Equal(t, "NoSchedule", row["effect"])
	_, hasSeconds := row["tolerationSeconds"]
	assert.False(t, hasSeconds)
}

func TestBuildPodPlacement_TolerationsEqualNoExecuteWithSeconds(t *testing.T) {
	seconds := int64(300)
	cfg := &models.PodPlacementConfig{
		Tolerations: []models.TolerationConfig{
			{Key: "spot", Operator: "Equal", Value: "true", Effect: "NoExecute", TolerationSeconds: &seconds},
		},
	}
	got := kubernetes.BuildPodPlacement(cfg, "gc-1")
	row := got["tolerations"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "NoExecute", row["effect"])
	assert.EqualValues(t, 300, row["tolerationSeconds"])
}

func TestBuildPodPlacement_TolerationsExistsAnyEffect(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		Tolerations: []models.TolerationConfig{
			{Operator: "Exists"},
		},
	}
	got := kubernetes.BuildPodPlacement(cfg, "gc-1")
	row := got["tolerations"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "Exists", row["operator"])
	_, hasKey := row["key"]
	assert.False(t, hasKey, "key omitted when empty")
	_, hasValue := row["value"]
	assert.False(t, hasValue)
	_, hasEffect := row["effect"]
	assert.False(t, hasEffect)
}

func TestBuildPodPlacement_TopologySpread_AutoLabelSelector(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		TopologySpreadConstraints: []models.TopologySpreadConstraintConfig{
			{MaxSkew: 1, TopologyKey: "topology.kubernetes.io/zone", WhenUnsatisfiable: "ScheduleAnyway"},
		},
	}
	got := kubernetes.BuildPodPlacement(cfg, "fg-template-abc")
	cs := got["topologySpreadConstraints"].([]interface{})
	require.Len(t, cs, 1)
	row := cs[0].(map[string]interface{})
	assert.EqualValues(t, 1, row["maxSkew"])
	assert.Equal(t, "topology.kubernetes.io/zone", row["topologyKey"])
	assert.Equal(t, "ScheduleAnyway", row["whenUnsatisfiable"])
	ls := row["labelSelector"].(map[string]interface{})
	matchLabels := ls["matchLabels"].(map[string]interface{})
	assert.Equal(t, "fg-template-abc", matchLabels["gateway.envoyproxy.io/owning-gatewayclass"])
}

func TestBuildPodPlacement_TopologySpread_DoNotSchedule(t *testing.T) {
	cfg := &models.PodPlacementConfig{
		TopologySpreadConstraints: []models.TopologySpreadConstraintConfig{
			{MaxSkew: 2, TopologyKey: "kubernetes.io/hostname", WhenUnsatisfiable: "DoNotSchedule"},
		},
	}
	got := kubernetes.BuildPodPlacement(cfg, "gc-1")
	row := got["topologySpreadConstraints"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "DoNotSchedule", row["whenUnsatisfiable"])
}

func TestBuildPDB_MinAvailable_Int(t *testing.T) {
	cfg := &models.PDBConfig{Kind: "minAvailable", Amount: "2"}
	got := kubernetes.BuildPDB(cfg)
	assert.EqualValues(t, 2, got["minAvailable"])
	_, hasMax := got["maxUnavailable"]
	assert.False(t, hasMax)
}

func TestBuildPDB_MinAvailable_Percent(t *testing.T) {
	cfg := &models.PDBConfig{Kind: "minAvailable", Amount: "50%"}
	got := kubernetes.BuildPDB(cfg)
	assert.Equal(t, "50%", got["minAvailable"])
}

func TestBuildPDB_MaxUnavailable_Int(t *testing.T) {
	cfg := &models.PDBConfig{Kind: "maxUnavailable", Amount: "1"}
	got := kubernetes.BuildPDB(cfg)
	assert.EqualValues(t, 1, got["maxUnavailable"])
}

func TestBuildPDB_MaxUnavailable_Percent(t *testing.T) {
	cfg := &models.PDBConfig{Kind: "maxUnavailable", Amount: "25%"}
	got := kubernetes.BuildPDB(cfg)
	assert.Equal(t, "25%", got["maxUnavailable"])
}

func TestBuildPDB_Nil(t *testing.T) {
	assert.Nil(t, kubernetes.BuildPDB(nil))
}

func TestBuildStrategy_Recreate(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{Type: "Recreate"}
	got := kubernetes.BuildStrategy(cfg)
	assert.Equal(t, "Recreate", got["type"])
	_, hasRolling := got["rollingUpdate"]
	assert.False(t, hasRolling)
}

func TestBuildStrategy_RollingDefault_NoOverrides(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{Type: "RollingUpdate"}
	got := kubernetes.BuildStrategy(cfg)
	assert.Equal(t, "RollingUpdate", got["type"])
	_, hasRolling := got["rollingUpdate"]
	assert.False(t, hasRolling, "no rollingUpdate sub-block when no overrides — K8s defaults apply")
}

func TestBuildStrategy_RollingCustom_PercentValues(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{
		Type: "RollingUpdate",
		RollingUpdate: &models.RollingUpdateConfig{
			MaxSurge:       "25%",
			MaxUnavailable: "50%",
		},
	}
	got := kubernetes.BuildStrategy(cfg)
	rolling := got["rollingUpdate"].(map[string]interface{})
	assert.Equal(t, "25%", rolling["maxSurge"])
	assert.Equal(t, "50%", rolling["maxUnavailable"])
}

func TestBuildStrategy_RollingCustom_IntValues(t *testing.T) {
	cfg := &models.DeploymentStrategyConfig{
		Type: "RollingUpdate",
		RollingUpdate: &models.RollingUpdateConfig{
			MaxSurge:       "2",
			MaxUnavailable: "1",
		},
	}
	got := kubernetes.BuildStrategy(cfg)
	rolling := got["rollingUpdate"].(map[string]interface{})
	assert.EqualValues(t, 2, rolling["maxSurge"])
	assert.EqualValues(t, 1, rolling["maxUnavailable"])
}

func TestBuildStrategy_Nil(t *testing.T) {
	assert.Nil(t, kubernetes.BuildStrategy(nil))
}

func TestBuildEnvoyProxyObject_PodPlacementOnly(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:             "ep-1",
		Namespace:        "envoy-gateway-system",
		ServiceType:      "ClusterIP",
		GatewayClassName: "gc-1",
		PodPlacement: &models.PodPlacementConfig{
			NodeSelector: map[string]string{"name": "nodepool-01"},
		},
	}
	obj := kubernetes.BuildEnvoyProxyObject(config)
	dep := obj.Object["spec"].(map[string]interface{})["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})["envoyDeployment"].(map[string]interface{})
	pod := dep["pod"].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"name": "nodepool-01"}, pod["nodeSelector"])
}

func TestBuildEnvoyProxyObject_PodPlacement_MergesWithAnnotations(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:             "ep-1",
		Namespace:        "envoy-gateway-system",
		ServiceType:      "ClusterIP",
		GatewayClassName: "gc-1",
		PodAnnotations:   map[string]string{"k": "v"},
		PodPlacement: &models.PodPlacementConfig{
			NodeSelector: map[string]string{"name": "nodepool-01"},
		},
	}
	obj := kubernetes.BuildEnvoyProxyObject(config)
	dep := obj.Object["spec"].(map[string]interface{})["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})["envoyDeployment"].(map[string]interface{})
	pod := dep["pod"].(map[string]interface{})
	assert.NotNil(t, pod["annotations"])
	assert.Equal(t, map[string]interface{}{"name": "nodepool-01"}, pod["nodeSelector"])
}

func TestBuildEnvoyProxyObject_PDBOnly(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:        "ep-1",
		Namespace:   "envoy-gateway-system",
		ServiceType: "ClusterIP",
		PDBConfig:   &models.PDBConfig{Kind: "minAvailable", Amount: "50%"},
	}
	obj := kubernetes.BuildEnvoyProxyObject(config)
	k8s := obj.Object["spec"].(map[string]interface{})["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})
	pdb := k8s["envoyPDB"].(map[string]interface{})
	assert.Equal(t, "50%", pdb["minAvailable"])
	_, hasDep := k8s["envoyDeployment"]
	assert.False(t, hasDep)
}

func TestBuildEnvoyProxyObject_StrategyOnly_TriggersEnvoyDeployment(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:               "ep-1",
		Namespace:          "envoy-gateway-system",
		ServiceType:        "ClusterIP",
		DeploymentStrategy: &models.DeploymentStrategyConfig{Type: "Recreate"},
	}
	obj := kubernetes.BuildEnvoyProxyObject(config)
	dep := obj.Object["spec"].(map[string]interface{})["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})["envoyDeployment"].(map[string]interface{})
	strat := dep["strategy"].(map[string]interface{})
	assert.Equal(t, "Recreate", strat["type"])
}

func TestBuildEnvoyProxyObject_PodSchedulingNil_NoNewKeys(t *testing.T) {
	config := &kubernetes.EnvoyProxyConfig{
		Name:        "ep-1",
		Namespace:   "envoy-gateway-system",
		ServiceType: "ClusterIP",
	}
	obj := kubernetes.BuildEnvoyProxyObject(config)
	k8s := obj.Object["spec"].(map[string]interface{})["provider"].(map[string]interface{})["kubernetes"].(map[string]interface{})
	_, hasDep := k8s["envoyDeployment"]
	assert.False(t, hasDep)
	_, hasPDB := k8s["envoyPDB"]
	assert.False(t, hasPDB)
}

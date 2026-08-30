package services

import (
	"strings"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── helpers ────────────────────────────────────────────────────────────────

func ptrInt(v int) *int             { return &v }
func ptrInt32(v int32) *int32       { return &v }
func ptrInt64(v int64) *int64       { return &v }
func ptrUint32(v uint32) *uint32    { return &v }
func ptrFloat32(v float32) *float32 { return &v }
func ptrString(v string) *string    { return &v }
func ptrBool(v bool) *bool          { return &v }

func testRoute() *models.Route {
	return &models.Route{
		ID:           uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DomainID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Name:         "test-route",
		Protocol:     models.RouteProtocolHTTP,
		K8sRouteName: "test-route-11111111",
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

func testDomain() *models.Domain {
	return &models.Domain{
		ID:             uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ProjectID:      uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Name:           "example.com",
		Hostname:       "example.com",
		Namespace:      "gateway-ns",
		K8sGatewayName: "eg",
	}
}

// ─── normalizeCIDR ──────────────────────────────────────────────────────────

func TestInternalNormalizeCIDR(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"IPv4 with prefix", "10.0.0.0/24", "10.0.0.0/24"},
		{"IPv4 without prefix", "192.168.1.1", "192.168.1.1/32"},
		{"IPv6 with prefix", "fd00::/64", "fd00::/64"},
		{"IPv6 without prefix", "::1", "::1/128"},
		{"invalid IP returns as-is", "not-an-ip", "not-an-ip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeCIDR(tt.input))
		})
	}
}

// generateRouteK8sName was deleted; route_service.go now delegates to
// kubernetes.RouteK8sName, which is covered by internal/kubernetes/naming_test.go
// (TestRouteK8sName, TestRouteK8sNameAlwaysWithinLimit).

// ─── isValidK8sName ────────────────────────────────────────────────────────

func TestInternalIsValidK8sName(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"valid simple", "my-route", true},
		{"valid with numbers", "route-123", true},
		{"empty", "", false},
		{"uppercase", "My-Route", false},
		{"starts with number", "1route", false},
		{"starts with dash", "-route", false},
		{"ends with dash", "route-", false},
		{"underscore", "my_route", false},
		{"single char", "a", true},
		{"too long", strings.Repeat("a", 64), false},
		{"63 chars", strings.Repeat("a", 63), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, isValidK8sName(tt.input))
		})
	}
}

// ─── getRouteKind ──────────────────────────────────────────────────────────

func TestInternalGetRouteKind(t *testing.T) {
	assert.Equal(t, "GRPCRoute", getRouteKind(models.RouteProtocolGRPC))
	assert.Equal(t, "HTTPRoute", getRouteKind(models.RouteProtocolHTTP))
	assert.Equal(t, "HTTPRoute", getRouteKind(models.RouteProtocol("unknown")))
}

// ─── WAFConfig.ImageURL ─────────────────────────────────────────────────────

func TestInternalWAFConfigImageURL_Default(t *testing.T) {
	url := WAFConfig{}.ImageURL()
	assert.Equal(t, "ghcr.io/corazawaf/coraza-proxy-wasm:0.6.0", url)
}

func TestInternalWAFConfigImageURL_Custom(t *testing.T) {
	url := WAFConfig{Image: "custom-image", Tag: "1.0.0"}.ImageURL()
	assert.Equal(t, "custom-image:1.0.0", url)
}

// ─── generateHTTPRouteYAML ─────────────────────────────────────────────────

func TestInternalGenerateHTTPRouteYAML_Basic(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateHTTPRouteYAML(route, domain)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "apiVersion")
	assert.Contains(t, result, "kind: HTTPRoute")
	assert.Contains(t, result, "test-route-11111111")
	assert.Contains(t, result, "example.com")
	assert.Contains(t, result, "my-svc")
}

func TestInternalGenerateHTTPRouteYAML_GRPC(t *testing.T) {
	route := testRoute()
	route.Protocol = models.RouteProtocolGRPC
	route.Config.Matches = []models.RouteMatch{
		{
			GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "helloworld.Greeter"},
			GRPCMethod:  &models.GRPCMethodMatch{Type: "Exact", Value: "SayHello"},
		},
	}
	domain := testDomain()
	result := generateHTTPRouteYAML(route, domain)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "kind: GRPCRoute")
	assert.Contains(t, result, "helloworld.Greeter")
}

func TestInternalGenerateHTTPRouteYAML_MultipleMatches(t *testing.T) {
	route := testRoute()
	route.Config.Matches = []models.RouteMatch{
		{Path: &models.PathMatch{Type: "Prefix", Value: "/api/v1"}},
		{Path: &models.PathMatch{Type: "Exact", Value: "/health"}},
	}
	domain := testDomain()
	result := generateHTTPRouteYAML(route, domain)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "/api/v1")
	assert.Contains(t, result, "/health")
}

func TestInternalGenerateHTTPRouteYAML_HeaderAndQueryMatch(t *testing.T) {
	route := testRoute()
	route.Config.Matches = []models.RouteMatch{
		{
			Path:        &models.PathMatch{Type: "Prefix", Value: "/api"},
			Headers:     []models.HeaderMatch{{Name: "X-Custom", Type: "Exact", Value: "test"}},
			QueryParams: []models.QueryParamMatch{{Name: "version", Type: "Exact", Value: "2"}},
		},
	}
	domain := testDomain()
	result := generateHTTPRouteYAML(route, domain)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "X-Custom")
}

// ─── generateBackendYAMLs ──────────────────────────────────────────────────

func TestInternalGenerateBackendYAMLs_NoExternal(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateBackendYAMLs(route, domain)
	assert.Empty(t, result)
}

func TestInternalGenerateBackendYAMLs_External(t *testing.T) {
	route := testRoute()
	route.Config.Backends = []models.RouteBackend{
		{
			Type:        models.BackendTypeExternal,
			AddressType: models.ExternalAddressTypeFQDN,
			Address:     "api.external.com",
			Port:        443,
		},
	}
	domain := testDomain()
	result := generateBackendYAMLs(route, domain)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "kind: Backend")
	assert.Contains(t, result, "api.external.com")
}

func TestInternalGenerateBackendYAMLs_Failover(t *testing.T) {
	route := testRoute()
	route.Config.Backends = []models.RouteBackend{
		{Type: models.BackendTypeKubernetes, Service: "primary", Namespace: "default", Port: 8080},
		{Type: models.BackendTypeKubernetes, Service: "fallback", Namespace: "default", Port: 8080, Fallback: true},
	}
	domain := testDomain()
	result := generateBackendYAMLs(route, domain)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "kind: Backend")
	assert.Contains(t, result, "primary.default.svc.cluster.local")
	assert.Contains(t, result, "fallback.default.svc.cluster.local")
}

// ─── generateDirectResponseYAMLs ───────────────────────────────────────────

func TestInternalGenerateDirectResponseYAMLs_Nil(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	hrf, cm := generateDirectResponseYAMLs(route, domain)
	assert.Empty(t, hrf)
	assert.Empty(t, cm)
}

func TestInternalGenerateDirectResponseYAMLs_WithBody(t *testing.T) {
	route := testRoute()
	route.Config.DirectResponse = &models.DirectResponseConfig{
		StatusCode:  200,
		ContentType: "text/plain",
		Body: &models.DirectResponseBody{
			Type:   models.DirectResponseBodyTypeInline,
			Inline: "Hello World",
		},
	}
	domain := testDomain()
	hrf, cm := generateDirectResponseYAMLs(route, domain)

	require.NotEmpty(t, hrf)
	require.NotEmpty(t, cm)
	assert.Contains(t, hrf, "HTTPRouteFilter")
	assert.Contains(t, cm, "ConfigMap")
}

func TestInternalGenerateDirectResponseYAMLs_NoBody(t *testing.T) {
	route := testRoute()
	route.Config.DirectResponse = &models.DirectResponseConfig{
		StatusCode:  404,
		ContentType: "text/plain",
	}
	domain := testDomain()
	hrf, cm := generateDirectResponseYAMLs(route, domain)

	require.NotEmpty(t, hrf)
	assert.Empty(t, cm) // No body means no ConfigMap
	assert.Contains(t, hrf, "HTTPRouteFilter")
}

// ─── generateSecurityPolicyYAML ────────────────────────────────────────────

func TestInternalGenerateSecurityPolicyYAML_NilInput(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateSecurityPolicyYAML(route, domain, nil, nil)
	assert.Empty(t, result)
}

func TestInternalGenerateSecurityPolicyYAML_CORSOnly(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &SecurityPolicyInput{
		CORS: &models.CORSConfig{
			AllowOrigins: []string{"https://example.com"},
			AllowMethods: []string{"GET", "POST"},
		},
	}
	result := generateSecurityPolicyYAML(route, domain, input, nil)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "SecurityPolicy")
	assert.Contains(t, result, "https://example.com")
}

func TestInternalGenerateSecurityPolicyYAML_ClientCIDRs(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateSecurityPolicyYAML(route, domain, nil, []string{"10.0.0.1", "192.168.1.0/24"})

	require.NotEmpty(t, result)
	assert.Contains(t, result, "SecurityPolicy")
	assert.Contains(t, result, "10.0.0.1/32")
	assert.Contains(t, result, "192.168.1.0/24")
}

func TestInternalGenerateSecurityPolicyYAML_GeneralAuth(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &SecurityPolicyInput{
		Authorization: &AuthorizationInput{AllowedCIDRs: []string{"10.0.0.0/8"}},
	}
	result := generateSecurityPolicyYAML(route, domain, input, nil)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "10.0.0.0/8")
	assert.Contains(t, result, "Deny")
}

func TestInternalGenerateSecurityPolicyYAML_APIKeyAuth(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &SecurityPolicyInput{
		APIKeyAuth: &APIKeyAuthInput{
			SecretName: "my-api-keys",
			HeaderName: "X-API-Key",
		},
	}
	result := generateSecurityPolicyYAML(route, domain, input, nil)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "SecurityPolicy")
	assert.Contains(t, result, "my-api-keys")
}

func TestInternalGenerateSecurityPolicyYAML_JWT(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &SecurityPolicyInput{
		JWT: &JWTInput{
			Issuer:  "https://issuer.example.com",
			JWKSURL: "https://issuer.example.com/.well-known/jwks.json",
		},
	}
	result := generateSecurityPolicyYAML(route, domain, input, nil)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "SecurityPolicy")
	assert.Contains(t, result, "https://issuer.example.com")
}

func TestInternalGenerateSecurityPolicyYAML_OIDC(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &SecurityPolicyInput{
		OIDC: &OIDCInput{
			Issuer:           "https://accounts.google.com",
			ClientID:         "my-client-id",
			ClientSecretName: "oidc-secret",
			RedirectURL:      "https://example.com/callback",
			LogoutPath:       "/logout",
		},
	}
	result := generateSecurityPolicyYAML(route, domain, input, nil)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "SecurityPolicy")
	assert.Contains(t, result, "https://accounts.google.com")
	assert.Contains(t, result, "my-client-id")
}

func TestInternalGenerateSecurityPolicyYAML_ExtAuth(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &SecurityPolicyInput{
		ExtAuth: &models.ExtAuthConfig{
			Type: "http",
			HTTP: &models.ExtAuthHTTPConfig{
				BackendRef: models.ExtAuthBackendRef{
					Name:      "auth-svc",
					Namespace: "auth-ns",
					Port:      9090,
				},
				Path: "/check",
			},
		},
	}
	result := generateSecurityPolicyYAML(route, domain, input, nil)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "SecurityPolicy")
	assert.Contains(t, result, "extAuth")
}

func TestInternalGenerateSecurityPolicyYAML_CORSPlusAuth(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &SecurityPolicyInput{
		CORS: &models.CORSConfig{
			AllowOrigins: []string{"*"},
			AllowMethods: []string{"GET"},
		},
		Authorization: &AuthorizationInput{AllowedCIDRs: []string{"10.0.0.0/8"}},
	}
	result := generateSecurityPolicyYAML(route, domain, input, nil)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "cors")
	assert.Contains(t, result, "10.0.0.0/8")
}

// ─── generateSecurityPolicyYAMLFromDB ──────────────────────────────────────

func TestInternalGenerateSecurityPolicyYAMLFromDB_Nil(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateSecurityPolicyYAMLFromDB(route, domain, nil)
	assert.Empty(t, result)
}

func TestInternalGenerateSecurityPolicyYAMLFromDB_CORS(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.SecurityPolicy{
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{
				AllowOrigins: []string{"https://app.example.com"},
				AllowMethods: []string{"GET", "POST"},
			},
		},
	}
	result := generateSecurityPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "SecurityPolicy")
	assert.Contains(t, result, "https://app.example.com")
}

func TestInternalGenerateSecurityPolicyYAMLFromDB_Authorization(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.SecurityPolicy{
		Config: models.SecurityPolicyConfig{
			Authorization: &models.AuthorizationConfig{
				DefaultAction: "Deny",
				Rules: []models.AuthorizationRule{
					{Action: "Allow", Principal: models.AuthorizationPrincipal{ClientCIDRs: []string{"10.0.0.0/8"}}},
				},
			},
		},
	}
	result := generateSecurityPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "10.0.0.0/8")
}

// ─── generateBackendTrafficPolicyYAML ──────────────────────────────────────

func TestInternalGenerateBTPYAML_Nil(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateBackendTrafficPolicyYAML(route, domain, nil)
	assert.Empty(t, result)
}

func TestInternalGenerateBTPYAML_EmptyInput(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateBackendTrafficPolicyYAML(route, domain, &BackendTrafficPolicyInput{})
	assert.Empty(t, result)
}

func TestInternalGenerateBTPYAML_Compression(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &BackendTrafficPolicyInput{
		Compression: []models.CompressionConfig{
			{Type: models.CompressionTypeGzip},
		},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "Gzip")
}

func TestInternalGenerateBTPYAML_Retry(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &BackendTrafficPolicyInput{
		Retry: &models.RetryConfig{
			NumRetries: ptrInt32(3),
			RetryOn:    &models.RetryOn{Triggers: []string{"5xx"}},
		},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "retry")
}

func TestInternalGenerateBTPYAML_LoadBalancer(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &BackendTrafficPolicyInput{
		LoadBalancer: &models.LoadBalancerConfig{
			Type: models.LoadBalancerTypeRoundRobin,
		},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "RoundRobin")
}

func TestInternalGenerateBTPYAML_CircuitBreaker(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &BackendTrafficPolicyInput{
		CircuitBreaker: &models.CircuitBreakerConfig{
			MaxConnections: ptrInt64(100),
		},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "circuitBreaker")
}

func TestInternalGenerateBTPYAML_HealthCheck(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &BackendTrafficPolicyInput{
		HealthCheck: &models.HealthCheckConfig{
			Active: &models.ActiveHealthCheckConfig{
				Type:     "HTTP",
				Interval: ptrString("10s"),
				HTTP:     &models.HTTPActiveHealthCheckConfig{Path: "/healthz"},
			},
		},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "healthCheck")
}

func TestInternalGenerateBTPYAML_FaultInjection(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &BackendTrafficPolicyInput{
		FaultInjection: &models.FaultInjectionConfig{
			Delay: &models.FaultInjectionDelayConfig{
				FixedDelay: "1s",
				Percentage: ptrFloat32(50),
			},
		},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "faultInjection")
}

func TestInternalGenerateBTPYAML_RateLimit(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &BackendTrafficPolicyInput{
		RateLimit: &models.RateLimitConfig{
			Global: &models.GlobalRateLimitConfig{
				Rules: []models.RateLimitRule{
					{Limit: models.RateLimitValue{Requests: 100, Unit: "Minute"}},
				},
			},
		},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "rateLimit")
}

func TestInternalGenerateBTPYAML_Timeout(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &BackendTrafficPolicyInput{
		Timeout: &models.BTPTimeoutConfig{
			TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"},
			HTTP: &models.BTPHTTPTimeoutConfig{
				RequestTimeout: "30s",
			},
		},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "timeout")
}

func TestInternalGenerateBTPYAML_ResponseOverride(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	statusVal := 503
	input := &BackendTrafficPolicyInput{
		ResponseOverride: []models.ResponseOverrideRule{
			{
				Match: models.ResponseOverrideMatch{
					StatusCodes: []models.StatusCodeMatch{
						{Type: "Value", Value: &statusVal},
					},
				},
				Response: models.ResponseOverrideResponse{
					ContentType: "text/plain",
					Body:        models.ResponseOverrideBody{Type: "Inline", Inline: "Service Unavailable"},
				},
			},
		},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "responseOverride")
}

// ─── generateBackendTrafficPolicyYAMLFromDB ────────────────────────────────

func TestInternalGenerateBTPYAMLFromDB_Nil(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, nil)
	assert.Empty(t, result)
}

func TestInternalGenerateBTPYAMLFromDB_EmptyConfig(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.BackendTrafficPolicy{Config: models.BackendTrafficPolicyConfig{}}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)
	assert.Empty(t, result)
}

func TestInternalGenerateBTPYAMLFromDB_WithRetry(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{
			Retry: &models.RetryConfig{
				NumRetries: ptrInt32(3),
				RetryOn:    &models.RetryOn{Triggers: []string{"5xx", "gateway-error"}},
				PerRetryPolicy: &models.PerRetryPolicy{
					Timeout: ptrString("2s"),
					BackOff: &models.BackOffPolicy{
						BaseInterval: ptrString("100ms"),
						MaxInterval:  ptrString("1s"),
					},
				},
			},
		},
	}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "BackendTrafficPolicy")
	assert.Contains(t, result, "retry")
}

// ─── generateEnvoyExtensionPolicyYAML ──────────────────────────────────────

func TestInternalGenerateEEPYAML_Nil(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateEnvoyExtensionPolicyYAML(route, domain, nil)
	assert.Empty(t, result)
}

func TestInternalGenerateEEPYAML_EmptyInput(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateEnvoyExtensionPolicyYAML(route, domain, &EnvoyExtensionPolicyInput{})
	assert.Empty(t, result)
}

func TestInternalGenerateEEPYAML_Lua(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &EnvoyExtensionPolicyInput{
		Lua: &models.LuaExtensionConfig{
			Type:   "Inline",
			Inline: `function envoy_on_request(handle) end`,
		},
	}
	result := generateEnvoyExtensionPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "EnvoyExtensionPolicy")
	assert.Contains(t, result, "envoy_on_request")
}

func TestInternalGenerateEEPYAML_Wasm(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	input := &EnvoyExtensionPolicyInput{
		Wasm: &models.WasmExtensionConfig{
			Name: "my-wasm",
			Code: models.WasmCodeSource{
				Type:  "Image",
				Image: &models.WasmImageSource{URL: "ghcr.io/example/wasm:v1"},
			},
		},
	}
	result := generateEnvoyExtensionPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "EnvoyExtensionPolicy")
	assert.Contains(t, result, "my-wasm")
}

// ─── generateEnvoyExtensionPolicyYAMLWithWaf ───────────────────────────────

func TestInternalGenerateEEPYAMLWithWaf_NilInputs(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateEnvoyExtensionPolicyYAMLWithWaf(route, domain, nil, nil, WAFConfig{})
	assert.Empty(t, result)
}

func TestInternalGenerateEEPYAMLWithWaf_WafOnly(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	wafInput := &WafPolicyInput{
		Mode:     "block",
		Rulesets: []string{"owasp-crs"},
	}
	result := generateEnvoyExtensionPolicyYAMLWithWaf(route, domain, nil, wafInput, WAFConfig{})

	require.NotEmpty(t, result)
	assert.Contains(t, result, "EnvoyExtensionPolicy")
	assert.Contains(t, result, "coraza-waf")
}

func TestInternalGenerateEEPYAMLWithWaf_LuaPlusWaf(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	extInput := &EnvoyExtensionPolicyInput{
		Lua: &models.LuaExtensionConfig{
			Type:   "Inline",
			Inline: `function envoy_on_request(handle) end`,
		},
	}
	wafInput := &WafPolicyInput{
		Mode:     "detect",
		Rulesets: []string{"owasp-crs"},
	}
	result := generateEnvoyExtensionPolicyYAMLWithWaf(route, domain, extInput, wafInput, WAFConfig{})

	require.NotEmpty(t, result)
	assert.Contains(t, result, "EnvoyExtensionPolicy")
	assert.Contains(t, result, "envoy_on_request")
	assert.Contains(t, result, "coraza-waf")
}

// ─── Mapping functions ─────────────────────────────────────────────────────

func TestInternalMapRetryConfigToPolicy(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.Nil(t, mapRetryConfigToPolicy(nil))
	})
	t.Run("basic", func(t *testing.T) {
		result := mapRetryConfigToPolicy(&models.RetryConfig{
			NumRetries: ptrInt32(3),
			RetryOn:    &models.RetryOn{HTTPStatusCodes: []int{502, 503}, Triggers: []string{"5xx"}},
			PerRetryPolicy: &models.PerRetryPolicy{
				Timeout: ptrString("1s"),
				BackOff: &models.BackOffPolicy{
					BaseInterval: ptrString("25ms"),
					MaxInterval:  ptrString("250ms"),
				},
			},
		})
		require.NotNil(t, result)
		assert.Equal(t, ptrInt32(3), result.NumRetries)
		require.NotNil(t, result.RetryOn)
		assert.Equal(t, []int{502, 503}, result.RetryOn.HTTPStatusCodes)
		require.NotNil(t, result.PerRetry)
		assert.Equal(t, ptrString("1s"), result.PerRetry.Timeout)
		require.NotNil(t, result.PerRetry.BackOff)
		assert.Equal(t, ptrString("25ms"), result.PerRetry.BackOff.BaseInterval)
	})
}

func TestInternalMapLoadBalancerConfigToPolicy(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.Nil(t, mapLoadBalancerConfigToPolicy(nil))
	})
	t.Run("round robin", func(t *testing.T) {
		result := mapLoadBalancerConfigToPolicy(&models.LoadBalancerConfig{
			Type: models.LoadBalancerTypeRoundRobin,
		})
		require.NotNil(t, result)
		assert.Equal(t, "RoundRobin", result.Type)
	})
	t.Run("consistent hash header", func(t *testing.T) {
		result := mapLoadBalancerConfigToPolicy(&models.LoadBalancerConfig{
			Type: models.LoadBalancerTypeConsistentHash,
			ConsistentHash: &models.ConsistentHashConfig{
				Type:   models.ConsistentHashTypeHeader,
				Header: &models.ConsistentHashHeader{Name: "X-Session"},
			},
		})
		require.NotNil(t, result)
		assert.Equal(t, "ConsistentHash", result.Type)
		require.NotNil(t, result.ConsistentHash)
		assert.Equal(t, "Header", result.ConsistentHash.Type)
		require.NotNil(t, result.ConsistentHash.Header)
		assert.Equal(t, "X-Session", result.ConsistentHash.Header.Name)
	})
	t.Run("consistent hash cookie", func(t *testing.T) {
		result := mapLoadBalancerConfigToPolicy(&models.LoadBalancerConfig{
			Type: models.LoadBalancerTypeConsistentHash,
			ConsistentHash: &models.ConsistentHashConfig{
				Type:   models.ConsistentHashTypeCookie,
				Cookie: &models.ConsistentHashCookie{Name: "session", TTL: ptrString("60s")},
			},
		})
		require.NotNil(t, result)
		require.NotNil(t, result.ConsistentHash)
		require.NotNil(t, result.ConsistentHash.Cookie)
		assert.Equal(t, "session", result.ConsistentHash.Cookie.Name)
		assert.Equal(t, ptrString("60s"), result.ConsistentHash.Cookie.TTL)
	})
}

func TestInternalMapCircuitBreakerConfigToPolicy(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.Nil(t, mapCircuitBreakerConfigToPolicy(nil))
	})
	t.Run("with values", func(t *testing.T) {
		result := mapCircuitBreakerConfigToPolicy(&models.CircuitBreakerConfig{
			MaxConnections:      ptrInt64(100),
			MaxPendingRequests:  ptrInt64(50),
			MaxParallelRequests: ptrInt64(10),
		})
		require.NotNil(t, result)
		assert.Equal(t, ptrInt64(100), result.MaxConnections)
		assert.Equal(t, ptrInt64(50), result.MaxPendingRequests)
		assert.Equal(t, ptrInt64(10), result.MaxParallelRequests)
	})
}

func TestInternalMapHealthCheckConfigToPolicy(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.Nil(t, mapHealthCheckConfigToPolicy(nil))
	})
	t.Run("active HTTP", func(t *testing.T) {
		result := mapHealthCheckConfigToPolicy(&models.HealthCheckConfig{
			Active: &models.ActiveHealthCheckConfig{
				Type:     "HTTP",
				Interval: ptrString("10s"),
				HTTP:     &models.HTTPActiveHealthCheckConfig{Path: "/healthz"},
			},
		})
		require.NotNil(t, result)
		require.NotNil(t, result.Active)
		assert.Equal(t, "HTTP", result.Active.Type)
		require.NotNil(t, result.Active.HTTP)
		assert.Equal(t, "/healthz", result.Active.HTTP.Path)
	})
	t.Run("passive", func(t *testing.T) {
		result := mapHealthCheckConfigToPolicy(&models.HealthCheckConfig{
			Passive: &models.PassiveHealthCheckConfig{
				Consecutive5xxErrors: ptrUint32(5),
				Interval:             ptrString("30s"),
			},
		})
		require.NotNil(t, result)
		require.NotNil(t, result.Passive)
		assert.Equal(t, ptrUint32(5), result.Passive.Consecutive5xxErrors)
	})
}

func TestInternalMapFaultInjectionConfigToPolicy(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.Nil(t, mapFaultInjectionConfigToPolicy(nil))
	})
	t.Run("delay and abort", func(t *testing.T) {
		httpStatus := 503
		result := mapFaultInjectionConfigToPolicy(&models.FaultInjectionConfig{
			Delay: &models.FaultInjectionDelayConfig{FixedDelay: "500ms", Percentage: ptrFloat32(25)},
			Abort: &models.FaultInjectionAbortConfig{HTTPStatus: &httpStatus, Percentage: ptrFloat32(10)},
		})
		require.NotNil(t, result)
		require.NotNil(t, result.Delay)
		assert.Equal(t, "500ms", result.Delay.FixedDelay)
		require.NotNil(t, result.Abort)
		assert.Equal(t, &httpStatus, result.Abort.HTTPStatus)
	})
}

func TestInternalMapRateLimitConfigToPolicy(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.Nil(t, mapRateLimitConfigToPolicy(nil))
	})
	t.Run("nil global", func(t *testing.T) {
		assert.Nil(t, mapRateLimitConfigToPolicy(&models.RateLimitConfig{}))
	})
	t.Run("with rules", func(t *testing.T) {
		result := mapRateLimitConfigToPolicy(&models.RateLimitConfig{
			Global: &models.GlobalRateLimitConfig{
				Rules: []models.RateLimitRule{
					{
						Limit: models.RateLimitValue{Requests: 100, Unit: "Minute"},
						ClientSelectors: []models.RateLimitSelector{
							{
								Headers:    []models.RateLimitHeaderMatch{{Name: "X-User", Type: "Distinct"}},
								SourceCIDR: &models.RateLimitSourceCIDR{Value: "10.0.0.0/8", Type: "Exact"},
								Path:       &models.RateLimitPathMatch{Value: "/api", Type: "PathPrefix"},
								Methods:    []string{"GET", "POST"},
							},
						},
					},
				},
			},
		})
		require.NotNil(t, result)
		require.NotNil(t, result.Global)
		require.Len(t, result.Global.Rules, 1)
		assert.Equal(t, 100, result.Global.Rules[0].Limit.Requests)
		assert.Equal(t, "Minute", result.Global.Rules[0].Limit.Unit)
		require.Len(t, result.Global.Rules[0].ClientSelectors, 1)
		sel := result.Global.Rules[0].ClientSelectors[0]
		require.Len(t, sel.Headers, 1)
		assert.Equal(t, "X-User", sel.Headers[0].Name)
		require.NotNil(t, sel.SourceCIDR)
		assert.Equal(t, "10.0.0.0/8", sel.SourceCIDR.Value)
		require.NotNil(t, sel.Path)
		assert.Equal(t, "/api", sel.Path.Value)
		assert.Equal(t, []string{"GET", "POST"}, sel.Methods)
	})
}

func TestInternalMapResponseOverrideToPolicy(t *testing.T) {
	statusVal := 503
	rules := []models.ResponseOverrideRule{
		{
			Match: models.ResponseOverrideMatch{
				StatusCodes: []models.StatusCodeMatch{
					{Type: "Value", Value: &statusVal},
					{Type: "Range", Range: &models.StatusCodeRange{Start: 500, End: 599}},
				},
			},
			Response: models.ResponseOverrideResponse{
				ContentType: "application/json",
				Body: models.ResponseOverrideBody{
					Type:   "Inline",
					Inline: `{"error":"unavailable"}`,
				},
			},
		},
	}
	result := mapResponseOverrideToPolicy(rules)
	require.Len(t, result, 1)
	require.Len(t, result[0].Match.StatusCodes, 2)
	assert.Equal(t, "Value", result[0].Match.StatusCodes[0].Type)
	assert.Equal(t, &statusVal, result[0].Match.StatusCodes[0].Value)
	assert.Equal(t, "Range", result[0].Match.StatusCodes[1].Type)
	require.NotNil(t, result[0].Match.StatusCodes[1].Range)
	assert.Equal(t, 500, result[0].Match.StatusCodes[1].Range.Start)
	assert.Equal(t, "application/json", result[0].Response.ContentType)
	assert.Equal(t, "Inline", result[0].Response.Body.Type)
}

func TestInternalMapResponseOverrideToPolicy_WithValueRef(t *testing.T) {
	statusVal := 404
	rules := []models.ResponseOverrideRule{
		{
			Match: models.ResponseOverrideMatch{
				StatusCodes: []models.StatusCodeMatch{{Type: "Value", Value: &statusVal}},
			},
			Response: models.ResponseOverrideResponse{
				ContentType: "text/html",
				Body: models.ResponseOverrideBody{
					Type: "ValueRef",
					ValueRef: &models.ValueRef{
						Kind: "ConfigMap",
						Name: "error-pages",
					},
				},
			},
		},
	}
	result := mapResponseOverrideToPolicy(rules)
	require.Len(t, result, 1)
	assert.Equal(t, "ValueRef", result[0].Response.Body.Type)
	require.NotNil(t, result[0].Response.Body.ValueRef)
	assert.Equal(t, "ConfigMap", result[0].Response.Body.ValueRef.Kind)
	assert.Equal(t, "error-pages", result[0].Response.Body.ValueRef.Name)
}

func TestInternalMapTimeoutConfigToPolicy(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.Nil(t, mapTimeoutConfigToPolicy(nil))
	})
	t.Run("tcp and http", func(t *testing.T) {
		result := mapTimeoutConfigToPolicy(&models.BTPTimeoutConfig{
			TCP:  &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"},
			HTTP: &models.BTPHTTPTimeoutConfig{RequestTimeout: "30s", ConnectionIdleTimeout: "60s", MaxConnectionDuration: "300s", MaxStreamDuration: "120s"},
		})
		require.NotNil(t, result)
		require.NotNil(t, result.TCP)
		assert.Equal(t, "5s", result.TCP.ConnectTimeout)
		require.NotNil(t, result.HTTP)
		assert.Equal(t, "30s", result.HTTP.RequestTimeout)
		assert.Equal(t, "60s", result.HTTP.ConnectionIdleTimeout)
		assert.Equal(t, "300s", result.HTTP.MaxConnectionDuration)
		assert.Equal(t, "120s", result.HTTP.MaxStreamDuration)
	})
}

// ─── Config Builder Functions ──────────────────────────────────────────────

func TestInternalBuildHTTPRouteConfigForYAML(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	config := buildHTTPRouteConfigForYAML(route, domain)

	require.NotNil(t, config)
	assert.Equal(t, "test-route-11111111", config.Name)
	assert.Equal(t, domain.Namespace, config.Namespace)
	assert.Equal(t, "eg", config.GatewayName)
	assert.Equal(t, "example.com", config.Hostname)
	require.Len(t, config.Rules, 1)
	assert.Equal(t, "/api", config.Rules[0].PathValue)
	require.Len(t, config.Rules[0].BackendRefs, 1)
	assert.Equal(t, "my-svc", config.Rules[0].BackendRefs[0].Name)
}

func TestInternalBuildHTTPRouteConfigForYAML_WithRedirect(t *testing.T) {
	route := testRoute()
	route.Config.Redirect = &models.RedirectConfig{
		Scheme:     "https",
		StatusCode: 301,
	}
	route.Config.Backends = nil
	domain := testDomain()
	config := buildHTTPRouteConfigForYAML(route, domain)

	require.NotNil(t, config)
	require.NotNil(t, config.Redirect)
	// No backends when redirect
	assert.Empty(t, config.Rules[0].BackendRefs)
}

func TestInternalBuildHTTPRouteConfigForYAML_WithMirrors(t *testing.T) {
	route := testRoute()
	route.Config.Mirrors = []models.MirrorBackend{
		{Type: models.BackendTypeKubernetes, Service: "mirror-svc", Namespace: "mirror-ns", Port: 9090},
	}
	domain := testDomain()
	config := buildHTTPRouteConfigForYAML(route, domain)

	require.NotNil(t, config)
	require.Len(t, config.Mirrors, 1)
	assert.Equal(t, "mirror-svc", config.Mirrors[0].Name)
}

func TestInternalBuildGRPCRouteConfigForYAML(t *testing.T) {
	route := testRoute()
	route.Protocol = models.RouteProtocolGRPC
	route.Config.Matches = []models.RouteMatch{
		{
			GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "helloworld.Greeter"},
			GRPCMethod:  &models.GRPCMethodMatch{Type: "Exact", Value: "SayHello"},
		},
	}
	domain := testDomain()
	config := buildGRPCRouteConfigForYAML(route, domain)

	require.NotNil(t, config)
	assert.Equal(t, "test-route-11111111", config.Name)
	assert.Equal(t, domain.Namespace, config.Namespace)
	require.Len(t, config.Rules, 1)
	require.NotNil(t, config.Rules[0].GRPCService)
	assert.Equal(t, "helloworld.Greeter", config.Rules[0].GRPCService.Value)
	require.NotNil(t, config.Rules[0].GRPCMethod)
	assert.Equal(t, "SayHello", config.Rules[0].GRPCMethod.Value)
}

func TestInternalSecurityPolicyConfigFromDB_Nil(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	assert.Nil(t, securityPolicyConfigFromDB(route, domain, nil))
}

func TestInternalSecurityPolicyConfigFromDB_EmptyConfig(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.SecurityPolicy{Config: models.SecurityPolicyConfig{}}
	assert.Nil(t, securityPolicyConfigFromDB(route, domain, policy))
}

func TestInternalSecurityPolicyConfigFromDB_CORS(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	maxAge := 3600
	policy := &models.SecurityPolicy{
		Config: models.SecurityPolicyConfig{
			CORS: &models.CORSConfig{
				AllowOrigins:     []string{"https://app.example.com"},
				AllowMethods:     []string{"GET", "POST"},
				AllowHeaders:     []string{"Content-Type"},
				ExposeHeaders:    []string{"X-Request-Id"},
				MaxAge:           &maxAge,
				AllowCredentials: ptrBool(true),
			},
		},
	}
	config := securityPolicyConfigFromDB(route, domain, policy)
	require.NotNil(t, config)
	require.NotNil(t, config.CORS)
	assert.Equal(t, []string{"https://app.example.com"}, config.CORS.AllowOrigins)
	assert.Equal(t, &maxAge, config.CORS.MaxAge)
	assert.Equal(t, ptrBool(true), config.CORS.AllowCredentials)
}

func TestInternalSecurityPolicyConfigFromDB_JWT(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.SecurityPolicy{
		Config: models.SecurityPolicyConfig{
			JWT: &models.JWTConfig{
				Providers: []models.JWTProvider{
					{
						Name:      "auth0",
						Issuer:    "https://auth0.example.com/",
						Audiences: []string{"api"},
						RemoteJWKS: &models.RemoteJWKS{
							URI: "https://auth0.example.com/.well-known/jwks.json",
						},
						ClaimToHeaders: []models.SPJWTClaimToHeader{
							{Claim: "sub", Header: "X-User-ID"},
						},
					},
				},
			},
		},
	}
	config := securityPolicyConfigFromDB(route, domain, policy)
	require.NotNil(t, config)
	require.NotNil(t, config.JWT)
	require.Len(t, config.JWT.Providers, 1)
	assert.Equal(t, "auth0", config.JWT.Providers[0].Name)
	assert.Equal(t, "https://auth0.example.com/.well-known/jwks.json", config.JWT.Providers[0].JWKSURL)
	require.Len(t, config.JWT.Providers[0].ClaimToHeaders, 1)
	assert.Equal(t, "sub", config.JWT.Providers[0].ClaimToHeaders[0].Claim)
}

func TestInternalSecurityPolicyConfigFromDB_OIDC(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.SecurityPolicy{
		Config: models.SecurityPolicyConfig{
			OIDC: &models.OIDCConfig{
				Provider: &models.OIDCProvider{
					Issuer: "https://accounts.google.com",
				},
				ClientID:     "client-id",
				ClientSecret: &models.SecretRef{Name: "oidc-secret", Namespace: "my-ns"},
				RedirectURL:  "https://app.example.com/callback",
				LogoutPath:   "/logout",
				Scopes:       []string{"openid", "email"},
				CookieDomain: "example.com",
			},
		},
	}
	config := securityPolicyConfigFromDB(route, domain, policy)
	require.NotNil(t, config)
	require.NotNil(t, config.OIDC)
	assert.Equal(t, "https://accounts.google.com", config.OIDC.Issuer)
	assert.Equal(t, "client-id", config.OIDC.ClientID)
	assert.Equal(t, "oidc-secret", config.OIDC.ClientSecretName)
	assert.Equal(t, "my-ns", config.OIDC.ClientSecretNS)
	assert.Equal(t, "example.com", config.OIDC.CookieDomain)
}

func TestInternalSecurityPolicyConfigFromDB_OIDC_DefaultNS(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.SecurityPolicy{
		Config: models.SecurityPolicyConfig{
			OIDC: &models.OIDCConfig{
				Provider:     &models.OIDCProvider{Issuer: "https://issuer.example.com"},
				ClientID:     "cid",
				ClientSecret: &models.SecretRef{Name: "secret"},
				RedirectURL:  "https://app.example.com/cb",
				LogoutPath:   "/logout",
			},
		},
	}
	config := securityPolicyConfigFromDB(route, domain, policy)
	require.NotNil(t, config)
	require.NotNil(t, config.OIDC)
	assert.Equal(t, FastGatewayNamespace, config.OIDC.ClientSecretNS)
}

func TestInternalSecurityPolicyConfigFromDB_ExtAuth(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.SecurityPolicy{
		Config: models.SecurityPolicyConfig{
			ExtAuth: &models.ExtAuthConfig{
				Type: "http",
				HTTP: &models.ExtAuthHTTPConfig{
					BackendRef: models.ExtAuthBackendRef{Name: "auth-svc", Port: 9090},
					Path:       "/check",
				},
			},
		},
	}
	config := securityPolicyConfigFromDB(route, domain, policy)
	require.NotNil(t, config)
	require.NotNil(t, config.ExtAuth)
	assert.Equal(t, "http", config.ExtAuth.Type)
}

func TestInternalSecurityPolicyConfigFromDB_APIKeyAuth(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.SecurityPolicy{
		Config: models.SecurityPolicyConfig{
			APIKeyAuth: &models.APIKeyAuthConfig{
				CredentialRefs: []models.SecretRef{{Name: "api-keys", Namespace: "ns1"}},
				ExtractFrom:    []models.APIKeyExtractFrom{{Headers: []string{"X-API-Key"}}},
			},
		},
	}
	config := securityPolicyConfigFromDB(route, domain, policy)
	require.NotNil(t, config)
	require.NotNil(t, config.APIKeyAuth)
	require.Len(t, config.APIKeyAuth.CredentialRefs, 1)
	assert.Equal(t, "api-keys", config.APIKeyAuth.CredentialRefs[0].Name)
}

// ─── generateEnvoyExtensionPolicyYAMLFromDB ────────────────────────────────

func TestInternalGenerateEEPYAMLFromDB_Nil(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateEnvoyExtensionPolicyYAMLFromDB(route, domain, nil)
	assert.Empty(t, result)
}

func TestInternalGenerateEEPYAMLFromDB_EmptyConfig(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.EnvoyExtensionPolicy{Config: models.EnvoyExtensionPolicyConfig{}}
	result := generateEnvoyExtensionPolicyYAMLFromDB(route, domain, policy)
	assert.Empty(t, result)
}

func TestInternalGenerateEEPYAMLFromDB_WithLua(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.EnvoyExtensionPolicy{
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{
				Type:   "Inline",
				Inline: `function envoy_on_response(handle) end`,
			},
		},
	}
	result := generateEnvoyExtensionPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "EnvoyExtensionPolicy")
	assert.Contains(t, result, "envoy_on_response")
}

func TestInternalGenerateEEPYAMLFromDB_WithWasm(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.EnvoyExtensionPolicy{
		Config: models.EnvoyExtensionPolicyConfig{
			Wasm: &models.WasmExtensionConfig{
				Name: "my-filter",
				Code: models.WasmCodeSource{
					Type:  "Image",
					Image: &models.WasmImageSource{URL: "ghcr.io/example/wasm:v1"},
				},
			},
		},
	}
	result := generateEnvoyExtensionPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "EnvoyExtensionPolicy")
	assert.Contains(t, result, "my-filter")
}

// ─── generateEnvoyExtensionPolicyYAMLFromSnapshot ──────────────────────────

func TestInternalGenerateEEPYAMLFromSnapshot_Empty(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	result := generateEnvoyExtensionPolicyYAMLFromSnapshot(route, domain, nil, nil, WAFConfig{})
	assert.Empty(t, result)
}

func TestInternalGenerateEEPYAMLFromSnapshot_WithWaf(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	wafPolicy := &models.WafPolicy{
		Config: models.WafPolicyConfig{
			Mode:     "block",
			Rulesets: []string{"owasp-crs"},
		},
	}
	result := generateEnvoyExtensionPolicyYAMLFromSnapshot(route, domain, nil, wafPolicy, WAFConfig{})

	require.NotEmpty(t, result)
	assert.Contains(t, result, "EnvoyExtensionPolicy")
	assert.Contains(t, result, "coraza-waf")
}

func TestInternalGenerateEEPYAMLFromSnapshot_ExtAndWaf(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	extPolicy := &models.EnvoyExtensionPolicy{
		Config: models.EnvoyExtensionPolicyConfig{
			Lua: &models.LuaExtensionConfig{
				Type:   "Inline",
				Inline: `function envoy_on_request(handle) end`,
			},
		},
	}
	wafPolicy := &models.WafPolicy{
		Config: models.WafPolicyConfig{
			Mode:     "detect",
			Rulesets: []string{"owasp-crs"},
		},
	}
	result := generateEnvoyExtensionPolicyYAMLFromSnapshot(route, domain, extPolicy, wafPolicy, WAFConfig{})

	require.NotEmpty(t, result)
	assert.Contains(t, result, "EnvoyExtensionPolicy")
	assert.Contains(t, result, "envoy_on_request")
	assert.Contains(t, result, "coraza-waf")
}

// ─── BTP YAML from DB with various features ────────────────────────────────

func TestInternalGenerateBTPYAMLFromDB_Compression(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{
			Compression: []models.CompressionConfig{
				{Type: models.CompressionTypeGzip},
				{Type: models.CompressionTypeBrotli},
				{Type: models.CompressionTypeZstd},
			},
		},
	}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "Gzip")
	assert.Contains(t, result, "Brotli")
	assert.Contains(t, result, "Zstd")
}

func TestInternalGenerateBTPYAMLFromDB_LoadBalancer(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{
			LoadBalancer: &models.LoadBalancerConfig{Type: models.LoadBalancerTypeLeastRequest},
		},
	}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "LeastRequest")
}

func TestInternalGenerateBTPYAMLFromDB_FaultInjection(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{
			FaultInjection: &models.FaultInjectionConfig{
				Delay: &models.FaultInjectionDelayConfig{FixedDelay: "100ms"},
			},
		},
	}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "faultInjection")
}

func TestInternalGenerateBTPYAMLFromDB_RateLimit(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{
			RateLimit: &models.RateLimitConfig{
				Global: &models.GlobalRateLimitConfig{
					Rules: []models.RateLimitRule{
						{Limit: models.RateLimitValue{Requests: 50, Unit: "Second"}},
					},
				},
			},
		},
	}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "rateLimit")
}

func TestInternalGenerateBTPYAMLFromDB_Timeout(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{
			Timeout: &models.BTPTimeoutConfig{
				HTTP: &models.BTPHTTPTimeoutConfig{RequestTimeout: "10s"},
			},
		},
	}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "timeout")
}

func TestInternalGenerateBTPYAMLFromDB_ResponseOverride(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	statusVal := 500
	policy := &models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{
			ResponseOverride: []models.ResponseOverrideRule{
				{
					Match: models.ResponseOverrideMatch{
						StatusCodes: []models.StatusCodeMatch{
							{Type: "Value", Value: &statusVal},
						},
					},
					Response: models.ResponseOverrideResponse{
						ContentType: "text/plain",
						Body:        models.ResponseOverrideBody{Type: "Inline", Inline: "Error"},
					},
				},
			},
		},
	}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "responseOverride")
}

func TestInternalGenerateBTPYAMLFromDB_CircuitBreaker(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{
			CircuitBreaker: &models.CircuitBreakerConfig{
				MaxConnections: ptrInt64(200),
			},
		},
	}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "circuitBreaker")
}

func TestInternalGenerateBTPYAMLFromDB_HealthCheck(t *testing.T) {
	route := testRoute()
	domain := testDomain()
	policy := &models.BackendTrafficPolicy{
		Config: models.BackendTrafficPolicyConfig{
			HealthCheck: &models.HealthCheckConfig{
				Active: &models.ActiveHealthCheckConfig{
					Type: "HTTP",
					HTTP: &models.HTTPActiveHealthCheckConfig{Path: "/health"},
				},
			},
		},
	}
	result := generateBackendTrafficPolicyYAMLFromDB(route, domain, policy)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "healthCheck")
}

// ─── GRPC route kind in policies ───────────────────────────────────────────

func TestInternalGenerateBTPYAML_GRPCRouteKind(t *testing.T) {
	route := testRoute()
	route.Protocol = models.RouteProtocolGRPC
	route.Config.Matches = []models.RouteMatch{
		{GRPCService: &models.GRPCMethodMatch{Type: "Exact", Value: "svc.Service"}},
	}
	domain := testDomain()
	input := &BackendTrafficPolicyInput{
		Retry: &models.RetryConfig{NumRetries: ptrInt32(2)},
	}
	result := generateBackendTrafficPolicyYAML(route, domain, input)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "GRPCRoute")
}

func TestInternalGenerateSecurityPolicyYAML_GRPCRouteKind(t *testing.T) {
	route := testRoute()
	route.Protocol = models.RouteProtocolGRPC
	domain := testDomain()
	input := &SecurityPolicyInput{
		CORS: &models.CORSConfig{AllowOrigins: []string{"*"}},
	}
	result := generateSecurityPolicyYAML(route, domain, input, nil)

	require.NotEmpty(t, result)
	assert.Contains(t, result, "GRPCRoute")
}

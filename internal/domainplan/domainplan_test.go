package domainplan

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ─── helpers ────────────────────────────────────────────────────────────────

func testDomainForBTP() *models.Domain {
	return &models.Domain{
		ID:             uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		ProjectID:      uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
		Name:           "btp-test.example.com",
		Hostname:       "btp-test.example.com",
		Namespace:      "gateway-ns",
		K8sGatewayName: "my-gw",
	}
}

// =========================================================================
// TestBuildDomainBTPConfig
// =========================================================================

func TestBuildDomainBTPConfig_NilConfig(t *testing.T) {
	domain := testDomainForBTP()

	result := BuildBackendTrafficPolicyConfig(domain, nil)
	assert.Nil(t, result)
}

func TestBuildDomainBTPConfig_EmptyConfig(t *testing.T) {
	domain := testDomainForBTP()

	cfg := &models.BackendTrafficPolicyConfig{}
	result := BuildBackendTrafficPolicyConfig(domain, cfg)
	assert.Nil(t, result)
}

func TestBuildDomainBTPConfig_TargetsGateway(t *testing.T) {
	domain := testDomainForBTP()

	numRetries := int32(3)
	cfg := &models.BackendTrafficPolicyConfig{
		Retry: &models.RetryConfig{NumRetries: &numRetries},
	}

	result := BuildBackendTrafficPolicyConfig(domain, cfg)
	require.NotNil(t, result)

	// Should target the Gateway, not HTTPRoute
	assert.Equal(t, "Gateway", result.TargetRef.Kind)
	assert.Equal(t, "gateway.networking.k8s.io", result.TargetRef.Group)
	assert.Equal(t, domain.K8sGatewayName, result.TargetRef.Name)

	// Name should be gatewayName + "-btp"
	assert.Equal(t, domain.K8sGatewayName+"-btp", result.Name)

	// RouteID should be empty for domain-level policies
	assert.Equal(t, "", result.RouteID)

	// DomainID should match domain.ID
	assert.Equal(t, domain.ID.String(), result.DomainID)

	// Namespace should match the domain's namespace
	assert.Equal(t, domain.Namespace, result.Namespace)
}

func TestBuildDomainBTPConfig_Compression(t *testing.T) {
	domain := testDomainForBTP()

	cfg := &models.BackendTrafficPolicyConfig{
		Compression: []models.CompressionConfig{
			{Type: models.CompressionTypeGzip, Gzip: &models.GzipConfig{}},
			{Type: models.CompressionTypeBrotli, Brotli: &models.BrotliConfig{}},
		},
	}

	result := BuildBackendTrafficPolicyConfig(domain, cfg)
	require.NotNil(t, result)
	require.Len(t, result.Compression, 2)

	assert.Equal(t, "Gzip", result.Compression[0].Type)
	assert.NotNil(t, result.Compression[0].Gzip)
	assert.Nil(t, result.Compression[0].Brotli)
	assert.Nil(t, result.Compression[0].Zstd)

	assert.Equal(t, "Brotli", result.Compression[1].Type)
	assert.NotNil(t, result.Compression[1].Brotli)
	assert.Nil(t, result.Compression[1].Gzip)
	assert.Nil(t, result.Compression[1].Zstd)
}

func TestBuildDomainBTPConfig_Retry(t *testing.T) {
	domain := testDomainForBTP()

	numRetries := int32(5)
	timeout := "2s"
	baseInterval := "100ms"
	maxInterval := "1s"
	cfg := &models.BackendTrafficPolicyConfig{
		Retry: &models.RetryConfig{
			NumRetries: &numRetries,
			RetryOn: &models.RetryOn{
				HTTPStatusCodes: []int{502, 503},
				Triggers:        []string{"5xx", "reset"},
			},
			PerRetryPolicy: &models.PerRetryPolicy{
				Timeout: &timeout,
				BackOff: &models.BackOffPolicy{
					BaseInterval: &baseInterval,
					MaxInterval:  &maxInterval,
				},
			},
		},
	}

	result := BuildBackendTrafficPolicyConfig(domain, cfg)
	require.NotNil(t, result)
	require.NotNil(t, result.Retry)

	assert.Equal(t, int32(5), *result.Retry.NumRetries)
	require.NotNil(t, result.Retry.RetryOn)
	assert.Equal(t, []int{502, 503}, result.Retry.RetryOn.HTTPStatusCodes)
	assert.Equal(t, []string{"5xx", "reset"}, result.Retry.RetryOn.Triggers)

	require.NotNil(t, result.Retry.PerRetry)
	assert.Equal(t, &timeout, result.Retry.PerRetry.Timeout)
	require.NotNil(t, result.Retry.PerRetry.BackOff)
	assert.Equal(t, &baseInterval, result.Retry.PerRetry.BackOff.BaseInterval)
	assert.Equal(t, &maxInterval, result.Retry.PerRetry.BackOff.MaxInterval)
}

func TestBuildDomainBTPConfig_LoadBalancer(t *testing.T) {
	domain := testDomainForBTP()

	cfg := &models.BackendTrafficPolicyConfig{
		LoadBalancer: &models.LoadBalancerConfig{
			Type: models.LoadBalancerTypeRoundRobin,
		},
	}

	result := BuildBackendTrafficPolicyConfig(domain, cfg)
	require.NotNil(t, result)
	require.NotNil(t, result.LoadBalancer)
	assert.Equal(t, "RoundRobin", result.LoadBalancer.Type)
	assert.Nil(t, result.LoadBalancer.ConsistentHash)
}

func TestBuildDomainBTPConfig_CircuitBreaker(t *testing.T) {
	domain := testDomainForBTP()

	maxConn := int64(100)
	maxPending := int64(50)
	maxParallel := int64(25)
	maxRetries := int64(3)
	maxPerConn := int64(10)
	cfg := &models.BackendTrafficPolicyConfig{
		CircuitBreaker: &models.CircuitBreakerConfig{
			MaxConnections:           &maxConn,
			MaxPendingRequests:       &maxPending,
			MaxParallelRequests:      &maxParallel,
			MaxParallelRetries:       &maxRetries,
			MaxRequestsPerConnection: &maxPerConn,
		},
	}

	result := BuildBackendTrafficPolicyConfig(domain, cfg)
	require.NotNil(t, result)
	require.NotNil(t, result.CircuitBreaker)

	assert.Equal(t, int64(100), *result.CircuitBreaker.MaxConnections)
	assert.Equal(t, int64(50), *result.CircuitBreaker.MaxPendingRequests)
	assert.Equal(t, int64(25), *result.CircuitBreaker.MaxParallelRequests)
	assert.Equal(t, int64(3), *result.CircuitBreaker.MaxParallelRetries)
	assert.Equal(t, int64(10), *result.CircuitBreaker.MaxRequestsPerConnection)
}

// =========================================================================
// TestBuildDomainExtensionPolicyConfig
// =========================================================================

func TestBuildDomainExtensionPolicyConfig_NilConfig(t *testing.T) {
	domain := testDomainForBTP()

	result := BuildEnvoyExtensionPolicyConfig(domain, nil)
	assert.Nil(t, result)
}

func TestBuildDomainExtensionPolicyConfig_EmptyConfig(t *testing.T) {
	domain := testDomainForBTP()

	cfg := &models.EnvoyExtensionPolicyConfig{}
	result := BuildEnvoyExtensionPolicyConfig(domain, cfg)
	assert.Nil(t, result)
}

func TestBuildDomainExtensionPolicyConfig_TargetsGateway(t *testing.T) {
	domain := testDomainForBTP()

	cfg := &models.EnvoyExtensionPolicyConfig{
		Lua: &models.LuaExtensionConfig{
			Type:   "Inline",
			Inline: "function envoy_on_request(handle) end",
		},
	}

	result := BuildEnvoyExtensionPolicyConfig(domain, cfg)
	require.NotNil(t, result)

	// Should target the Gateway
	assert.Equal(t, "Gateway", result.TargetRef.Kind)
	assert.Equal(t, "gateway.networking.k8s.io", result.TargetRef.Group)
	assert.Equal(t, domain.K8sGatewayName, result.TargetRef.Name)

	// Name should be gatewayName + "-eep"
	assert.Equal(t, domain.K8sGatewayName+"-eep", result.Name)

	// DomainID should match domain.ID
	assert.Equal(t, domain.ID.String(), result.DomainID)

	// RouteID should be empty
	assert.Equal(t, "", result.RouteID)

	// Namespace should match the domain's namespace
	assert.Equal(t, domain.Namespace, result.Namespace)
}

func TestBuildDomainExtensionPolicyConfig_LuaInline(t *testing.T) {
	domain := testDomainForBTP()

	luaScript := "function envoy_on_request(handle) handle:logInfo('hello') end"
	cfg := &models.EnvoyExtensionPolicyConfig{
		Lua: &models.LuaExtensionConfig{
			Type:   "Inline",
			Inline: luaScript,
		},
	}

	result := BuildEnvoyExtensionPolicyConfig(domain, cfg)
	require.NotNil(t, result)
	require.Len(t, result.Lua, 1)

	assert.Equal(t, "Inline", result.Lua[0].Type)
	assert.Equal(t, luaScript, result.Lua[0].Inline)
	assert.Nil(t, result.Lua[0].ValueRef)
}

func TestBuildDomainExtensionPolicyConfig_WasmHTTP(t *testing.T) {
	domain := testDomainForBTP()

	wasmConfig := `{"key":"value"}`
	cfg := &models.EnvoyExtensionPolicyConfig{
		Wasm: &models.WasmExtensionConfig{
			Name:   "my-wasm-filter",
			RootID: "my-root",
			Code: models.WasmCodeSource{
				Type: "HTTP",
				HTTP: &models.WasmHTTPSource{
					URL:    "https://example.com/filter.wasm",
					SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
				},
			},
			Config: &wasmConfig,
		},
	}

	result := BuildEnvoyExtensionPolicyConfig(domain, cfg)
	require.NotNil(t, result)
	require.Len(t, result.Wasm, 1)

	wasm := result.Wasm[0]
	assert.Equal(t, "my-wasm-filter", wasm.Name)
	assert.Equal(t, "my-root", wasm.RootID)
	assert.Equal(t, "HTTP", wasm.Code.Type)
	require.NotNil(t, wasm.Code.HTTP)
	assert.Equal(t, "https://example.com/filter.wasm", wasm.Code.HTTP.URL)
	assert.Equal(t, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", wasm.Code.HTTP.SHA256)
	assert.Nil(t, wasm.Code.Image)
	require.NotNil(t, wasm.Config)
	assert.Equal(t, wasmConfig, *wasm.Config)
}

func TestBuildDomainExtensionPolicyConfig_ExtProc(t *testing.T) {
	domain := testDomainForBTP()

	cfg := &models.EnvoyExtensionPolicyConfig{
		ExtProc: &models.ExtProcExtensionConfig{
			BackendRef: models.ExtProcBackendRef{
				Name:      "ext-processor",
				Namespace: "processing-ns",
				Port:      9001,
			},
			ProcessingMode: &models.ExtProcProcessingMode{
				Request:  &models.ExtProcBodyMode{Body: "Buffered"},
				Response: &models.ExtProcBodyMode{Body: "Streamed"},
			},
			FailOpen: true,
		},
	}

	result := BuildEnvoyExtensionPolicyConfig(domain, cfg)
	require.NotNil(t, result)
	require.Len(t, result.ExtProc, 1)

	ep := result.ExtProc[0]
	assert.Equal(t, "ext-processor", ep.BackendRef.Name)
	assert.Equal(t, "processing-ns", ep.BackendRef.Namespace)
	assert.Equal(t, 9001, ep.BackendRef.Port)
	assert.True(t, ep.FailOpen)

	require.NotNil(t, ep.ProcessingMode)
	require.NotNil(t, ep.ProcessingMode.Request)
	assert.Equal(t, "Buffered", ep.ProcessingMode.Request.Body)
	require.NotNil(t, ep.ProcessingMode.Response)
	assert.Equal(t, "Streamed", ep.ProcessingMode.Response.Body)
}

// F2, closed in Phase 2H. BuildGatewayConfig mapped nine domain fields and
// skipped TLSSecretNamespace, so the domain YAML PREVIEW rendered a
// certificateRef with no namespace while the deploying path
// (domain_service.go:297) set it and worked. Preview understated reality.
//
// The Phase 2F fixture comment recorded this backwards, as a branch that was
// "DEAD on the domain path". It was dead on the preview path only.
func TestBuildGatewayConfig_MapsTLSSecretNamespace(t *testing.T) {
	domain := fixtureDomain()
	domain.TLSSecretName = "wildcard-tls"
	domain.TLSSecretNamespace = "shared-certs"

	got := BuildGatewayConfig(domain, bothTemplate(), nil)

	require.Equal(t, "shared-certs", got.TLSSecretNamespace,
		"F2: preview must emit the same cross-namespace certificateRef that deploy does")
}

// TestBuildGatewayConfig_ManagedCertificateWinsOverLegacySecret covers Phase
// 3b Task 4: when a Domain has a managed certificate attached, the Gateway
// listener must terminate TLS with the managed cert's deterministic tenant
// Secret (cert-<id> in fastgateway-system, pushed there by the Phase 3a
// distribution controller for every ready cert) instead of any legacy BYO
// TLSSecretName/TLSSecretNamespace.
func TestBuildGatewayConfig_ManagedCertificateWinsOverLegacySecret(t *testing.T) {
	certID := uuid.New()
	domain := fixtureDomain()
	domain.ManagedCertificateID = &certID
	domain.TLSSecretName = "legacy-secret"
	domain.Namespace = "fastgateway-system"

	got := BuildGatewayConfig(domain, bothTemplate(), nil)

	assert.Equal(t, "cert-"+certID.String(), got.TLSSecretName,
		"managed cert must win over the legacy BYO secret name")
	assert.Equal(t, "fastgateway-system", got.TLSSecretNamespace,
		"managed cert's tenant secret always lives in fastgateway-system")
}

// TestBuildGatewayConfig_NoManagedCertificateKeepsLegacySecret pins the
// unchanged behavior for domains with no managed cert attached: the legacy
// BYO TLSSecretName/TLSSecretNamespace pass through untouched.
func TestBuildGatewayConfig_NoManagedCertificateKeepsLegacySecret(t *testing.T) {
	domain := fixtureDomain()
	domain.ManagedCertificateID = nil
	domain.TLSSecretName = "legacy-secret"
	domain.TLSSecretNamespace = "team-ns"

	got := BuildGatewayConfig(domain, bothTemplate(), nil)

	assert.Equal(t, "legacy-secret", got.TLSSecretName)
	assert.Equal(t, "team-ns", got.TLSSecretNamespace)
}

// bothTemplate is a template carrying the listeners a migrated "both" template
// has: http:80 and https:443 (Terminate).
func bothTemplate() *models.DomainTemplate {
	return &models.DomainTemplate{Listeners: models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
	}}
}

func TestBuildGatewayConfig_BoundListeners(t *testing.T) {
	d := fixtureDomain()
	d.K8sGatewayClass = "gc"
	d.BoundListeners = []string{"http", "https"}
	d.TLSSecretName = "api-tls"

	cfg := BuildGatewayConfig(d, bothTemplate(), nil)

	require.Equal(t, []kubernetes.HostnameListener{
		{Name: "http", Protocol: "HTTP", Port: 80},
		{Name: "https", Protocol: "HTTPS", Port: 443, TLSMode: "Terminate"},
	}, cfg.HostnameListeners)
}

// Resolution follows the TEMPLATE's listener order, not the order of
// BoundListeners, so a migrated "both" domain always yields http then https.
func TestBuildGatewayConfig_BoundListenersFollowTemplateOrder(t *testing.T) {
	d := fixtureDomain()
	d.BoundListeners = []string{"https", "http"}

	cfg := BuildGatewayConfig(d, bothTemplate(), nil)

	require.Len(t, cfg.HostnameListeners, 2)
	assert.Equal(t, "http", cfg.HostnameListeners[0].Name)
	assert.Equal(t, "https", cfg.HostnameListeners[1].Name)
}

// Only the bound subset is emitted.
func TestBuildGatewayConfig_BoundSubset(t *testing.T) {
	d := fixtureDomain()
	d.BoundListeners = []string{"https"}

	cfg := BuildGatewayConfig(d, bothTemplate(), nil)

	require.Len(t, cfg.HostnameListeners, 1)
	assert.Equal(t, "https", cfg.HostnameListeners[0].Name)
}

// A Gateway with zero listeners is invalid: when none of the bound names
// resolve (stale binding after a template edit), fall back to the template's
// hostname-routed listeners rather than emitting an empty list.
func TestBuildGatewayConfig_NeverEmptyWhenBound(t *testing.T) {
	d := fixtureDomain()
	d.BoundListeners = []string{"renamed-away"}

	cfg := BuildGatewayConfig(d, bothTemplate(), nil)

	require.NotEmpty(t, cfg.HostnameListeners)
	assert.Equal(t, "http", cfg.HostnameListeners[0].Name)
	assert.Equal(t, "https", cfg.HostnameListeners[1].Name)
}

// L4 (TCP/UDP) listeners are not hostname-routed and never become
// HostnameListeners.
func TestBuildGatewayConfig_SkipsL4Listeners(t *testing.T) {
	tmpl := bothTemplate()
	tmpl.Listeners = append(tmpl.Listeners,
		models.TemplateListener{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535})
	d := fixtureDomain()
	d.BoundListeners = []string{"http", "tcpudp"}

	cfg := BuildGatewayConfig(d, tmpl, nil)

	require.Len(t, cfg.HostnameListeners, 1)
	assert.Equal(t, "http", cfg.HostnameListeners[0].Name)
}

// The protocol is carried through verbatim: a non-HTTPS protocol (here TLS)
// must not be coerced to "HTTP" at the resolution layer.
func TestBuildGatewayConfig_ProtocolNotCoerced(t *testing.T) {
	tmpl := &models.DomainTemplate{Listeners: models.Listeners{
		{Name: "tls", Protocol: models.ListenerTLS, Port: 8443, TLSMode: models.TLSListenerPassthrough},
	}}
	d := fixtureDomain()
	d.BoundListeners = []string{"tls"}

	cfg := BuildGatewayConfig(d, tmpl, nil)

	require.Len(t, cfg.HostnameListeners, 1)
	assert.Equal(t, "TLS", cfg.HostnameListeners[0].Protocol)
	assert.Equal(t, "Passthrough", cfg.HostnameListeners[0].TLSMode)
}

// The byte-identical invariant protects the RENDERED Gateway: a migrated
// "both" domain must render http then https, with the Terminate tls block,
// exactly as the pre-listener-model builder did.
func TestBuildGatewayConfig_RenderedGatewayByteIdenticalForMigratedBoth(t *testing.T) {
	d := fixtureDomain()
	d.K8sGatewayClass = "envoy-gateway-class"
	d.BoundListeners = models.MigrateDomainBoundListeners("both")
	d.TLSSecretName = "example-com-tls"

	cfg := BuildGatewayConfig(d, bothTemplate(), nil)
	obj := kubernetes.BuildGatewayObject(cfg)
	require.NotNil(t, obj)

	got, found, err := unstructured.NestedSlice(obj.Object, "spec", "listeners")
	require.NoError(t, err)
	require.True(t, found)

	want := []interface{}{
		map[string]interface{}{
			"name":     "http",
			"port":     int64(80),
			"protocol": "HTTP",
			"hostname": "example.com",
		},
		map[string]interface{}{
			"name":     "https",
			"port":     int64(443),
			"protocol": "HTTPS",
			"hostname": "example.com",
			"tls": map[string]interface{}{
				"mode": "Terminate",
				"certificateRefs": []interface{}{
					map[string]interface{}{
						"kind": "Secret",
						"name": "example-com-tls",
					},
				},
			},
		},
	}
	require.Equal(t, want, got)
}

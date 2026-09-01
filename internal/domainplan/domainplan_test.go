package domainplan

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

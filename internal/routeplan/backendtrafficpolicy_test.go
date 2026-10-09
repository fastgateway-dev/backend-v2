package routeplan_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
)

func TestGetRouteKind_L4(t *testing.T) {
	assert.Equal(t, "TCPRoute", routeplan.GetRouteKind(models.RouteProtocolTCP))
	assert.Equal(t, "UDPRoute", routeplan.GetRouteKind(models.RouteProtocolUDP))
	assert.Equal(t, "HTTPRoute", routeplan.GetRouteKind(models.RouteProtocolHTTP))
	assert.Equal(t, "GRPCRoute", routeplan.GetRouteKind(models.RouteProtocolGRPC))
}

func i64(v int64) *int64   { return &v }
func u32(v uint32) *uint32 { return &v }

// kitchenSinkBTP sets every BTP field, HTTP-only and L4-capable alike, so the
// emit-gating tests can prove which ones survive.
func kitchenSinkBTP() models.BackendTrafficPolicyConfig {
	return models.BackendTrafficPolicyConfig{
		Compression:      []models.CompressionConfig{{Type: models.CompressionTypeGzip}},
		Retry:            &models.RetryConfig{},
		LoadBalancer:     &models.LoadBalancerConfig{Type: models.LoadBalancerTypeConsistentHash, ConsistentHash: &models.ConsistentHashConfig{Type: models.ConsistentHashTypeHeader, Header: &models.ConsistentHashHeader{Name: "x-user"}}},
		CircuitBreaker:   &models.CircuitBreakerConfig{MaxConnections: i64(100), MaxPendingRequests: i64(10), MaxParallelRequests: i64(20), MaxParallelRetries: i64(3), MaxRequestsPerConnection: i64(50)},
		HealthCheck:      &models.HealthCheckConfig{Passive: &models.PassiveHealthCheckConfig{ConsecutiveGatewayErrors: u32(3)}, Active: &models.ActiveHealthCheckConfig{Type: "HTTP", HTTP: &models.HTTPActiveHealthCheckConfig{Path: "/h"}}},
		FaultInjection:   &models.FaultInjectionConfig{},
		RateLimit:        &models.RateLimitConfig{},
		RequestBuffer:    &models.RequestBufferConfig{Limit: "1Mi"},
		ResponseOverride: []models.ResponseOverrideRule{{}},
		Timeout:          &models.BTPTimeoutConfig{TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"}, HTTP: &models.BTPHTTPTimeoutConfig{RequestTimeout: "10s"}},
	}
}

func TestBuildBackendTrafficPolicyConfig_TCP_EmitsOnlyL4Subset(t *testing.T) {
	route := l4TestRoute(models.RouteProtocolTCP, 5432)
	domain := &models.Domain{ID: uuid.New(), Namespace: "streams"}
	policy := &models.BackendTrafficPolicy{Config: kitchenSinkBTP()}

	cfg := routeplan.BuildBackendTrafficPolicyConfig(&route, domain, policy)
	require.NotNil(t, cfg)

	assert.Equal(t, "TCPRoute", cfg.TargetRef.Kind)
	assert.Equal(t, "pg-route", cfg.TargetRef.Name)

	// HTTP-only fields are dropped.
	assert.Empty(t, cfg.Compression)
	assert.Nil(t, cfg.Retry)
	assert.Nil(t, cfg.FaultInjection)
	assert.Nil(t, cfg.RateLimit)
	assert.Nil(t, cfg.RequestBuffer)
	assert.Empty(t, cfg.ResponseOverride)

	// Circuit breaker: only connection-oriented counters.
	require.NotNil(t, cfg.CircuitBreaker)
	assert.Equal(t, int64(100), *cfg.CircuitBreaker.MaxConnections)
	assert.Equal(t, int64(50), *cfg.CircuitBreaker.MaxRequestsPerConnection)
	assert.Nil(t, cfg.CircuitBreaker.MaxPendingRequests)
	assert.Nil(t, cfg.CircuitBreaker.MaxParallelRequests)
	assert.Nil(t, cfg.CircuitBreaker.MaxParallelRetries)

	// Load balancer: consistent hash is coerced to source IP.
	require.NotNil(t, cfg.LoadBalancer)
	require.NotNil(t, cfg.LoadBalancer.ConsistentHash)
	assert.Equal(t, "SourceIP", cfg.LoadBalancer.ConsistentHash.Type)
	assert.Nil(t, cfg.LoadBalancer.ConsistentHash.Header)
	assert.Nil(t, cfg.LoadBalancer.ConsistentHash.Cookie)

	// Health check: passive kept, HTTP active dropped.
	require.NotNil(t, cfg.HealthCheck)
	assert.NotNil(t, cfg.HealthCheck.Passive)
	assert.Nil(t, cfg.HealthCheck.Active)

	// Timeout: TCP only.
	require.NotNil(t, cfg.Timeout)
	require.NotNil(t, cfg.Timeout.TCP)
	assert.Equal(t, "5s", cfg.Timeout.TCP.ConnectTimeout)
	assert.Nil(t, cfg.Timeout.HTTP)
}

func TestBuildBackendTrafficPolicyConfig_TCP_KeepsActiveTCPHealthCheck(t *testing.T) {
	route := l4TestRoute(models.RouteProtocolTCP, 5432)
	domain := &models.Domain{ID: uuid.New(), Namespace: "streams"}
	policy := &models.BackendTrafficPolicy{Config: models.BackendTrafficPolicyConfig{
		HealthCheck: &models.HealthCheckConfig{Active: &models.ActiveHealthCheckConfig{Type: "TCP", TCP: &models.TCPActiveHealthCheckConfig{}}},
	}}

	cfg := routeplan.BuildBackendTrafficPolicyConfig(&route, domain, policy)
	require.NotNil(t, cfg)
	require.NotNil(t, cfg.HealthCheck)
	require.NotNil(t, cfg.HealthCheck.Active)
	assert.Equal(t, "TCP", cfg.HealthCheck.Active.Type)
}

func TestBuildBackendTrafficPolicyConfig_UDP_EmitsLoadBalancerOnly(t *testing.T) {
	route := l4TestRoute(models.RouteProtocolUDP, 5353)
	domain := &models.Domain{ID: uuid.New(), Namespace: "streams"}
	policy := &models.BackendTrafficPolicy{Config: kitchenSinkBTP()}

	cfg := routeplan.BuildBackendTrafficPolicyConfig(&route, domain, policy)
	require.NotNil(t, cfg)

	assert.Equal(t, "UDPRoute", cfg.TargetRef.Kind)
	require.NotNil(t, cfg.LoadBalancer)
	assert.Equal(t, "SourceIP", cfg.LoadBalancer.ConsistentHash.Type)

	assert.Empty(t, cfg.Compression)
	assert.Nil(t, cfg.Retry)
	assert.Nil(t, cfg.CircuitBreaker)
	assert.Nil(t, cfg.HealthCheck)
	assert.Nil(t, cfg.FaultInjection)
	assert.Nil(t, cfg.RateLimit)
	assert.Nil(t, cfg.RequestBuffer)
	assert.Empty(t, cfg.ResponseOverride)
	assert.Nil(t, cfg.Timeout)
}

func TestBuildBackendTrafficPolicyConfig_HTTP_Unaffected(t *testing.T) {
	route := l4TestRoute(models.RouteProtocolHTTP, 0)
	domain := &models.Domain{ID: uuid.New(), Namespace: "ns"}
	policy := &models.BackendTrafficPolicy{Config: kitchenSinkBTP()}

	cfg := routeplan.BuildBackendTrafficPolicyConfig(&route, domain, policy)
	require.NotNil(t, cfg)

	assert.Equal(t, "HTTPRoute", cfg.TargetRef.Kind)
	assert.NotEmpty(t, cfg.Compression)
	assert.NotNil(t, cfg.Retry)
	assert.Equal(t, "Header", cfg.LoadBalancer.ConsistentHash.Type)
	assert.NotNil(t, cfg.CircuitBreaker.MaxPendingRequests)
	assert.NotNil(t, cfg.Timeout.HTTP)
}

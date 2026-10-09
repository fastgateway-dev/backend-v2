package services

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeL4Checker records CheckPortCollision calls and returns a canned error.
type fakeL4Checker struct {
	err   error
	calls int

	streamID  uuid.UUID
	transport string
	port      int
	exclude   *uuid.UUID
}

func (f *fakeL4Checker) CheckPortCollision(streamID uuid.UUID, transport string, port int, excludeRouteID *uuid.UUID) error {
	f.calls++
	f.streamID, f.transport, f.port, f.exclude = streamID, transport, port, excludeRouteID
	return f.err
}

// l4Config builds an L4 config that passes ValidateL4RouteConfig.
func l4Config(port int) *models.RouteConfig {
	return &models.RouteConfig{ListenerPort: port, Backends: []models.RouteBackend{
		{Type: models.BackendTypeKubernetes, Service: "pg", Namespace: "db", Port: 5432},
	}}
}

func TestValidateRouteShape_L4_RunsReservedThenCollision(t *testing.T) {
	streamID, routeID := uuid.New(), uuid.New()
	fake := &fakeL4Checker{}
	w := &routeWrite{l4Ports: fake}

	err := w.validateRouteShapeAndConflicts(l4Config(5432), nil, models.RouteProtocolUDP, uuid.Nil, &streamID, &routeID)
	require.NoError(t, err)
	assert.Equal(t, 1, fake.calls)
	assert.Equal(t, streamID, fake.streamID)
	assert.Equal(t, "UDP", fake.transport)
	assert.Equal(t, 5432, fake.port)
	require.NotNil(t, fake.exclude)
	assert.Equal(t, routeID, *fake.exclude)
}

func TestValidateRouteShape_L4_ReservedPortRejectedBeforeCollisionCheck(t *testing.T) {
	streamID := uuid.New()
	for _, port := range []int{19000, 19001, 60000, 70000} {
		fake := &fakeL4Checker{}
		w := &routeWrite{l4Ports: fake}
		err := w.validateRouteShapeAndConflicts(l4Config(port), nil, models.RouteProtocolTCP, uuid.Nil, &streamID, nil)
		require.Error(t, err, "port %d", port)
		assert.True(t, errors.Is(err, ErrReservedPort) || errors.Is(err, ErrInvalidListenerPort), "port %d: %v", port, err)
		assert.Zero(t, fake.calls, "static rejection must not hit the DB-backed check (port %d)", port)
	}
}

func TestValidateRouteShape_L4_PropagatesCollisionAndReserved(t *testing.T) {
	streamID := uuid.New()
	for _, sentinel := range []error{ErrPortCollision, ErrReservedPort} {
		fake := &fakeL4Checker{err: sentinel}
		w := &routeWrite{l4Ports: fake}
		err := w.validateRouteShapeAndConflicts(l4Config(5432), nil, models.RouteProtocolTCP, uuid.Nil, &streamID, nil)
		assert.ErrorIs(t, err, sentinel)
	}
}

func TestValidateRouteShape_L4_FailsClosedWithoutCheckerOrStream(t *testing.T) {
	streamID := uuid.New()
	w := &routeWrite{}
	assert.Error(t, w.validateRouteShapeAndConflicts(l4Config(5432), nil, models.RouteProtocolTCP, uuid.Nil, &streamID, nil),
		"an unwired checker must reject, not silently skip the collision guard")

	w = &routeWrite{l4Ports: &fakeL4Checker{}}
	assert.Error(t, w.validateRouteShapeAndConflicts(l4Config(5432), nil, models.RouteProtocolTCP, uuid.Nil, nil, nil),
		"an L4 route needs a stream")
}

func TestValidateRouteConfig_L4_SkipsPathMatchRequirement(t *testing.T) {
	// Without the carve-out an empty Matches would fail with "path matching is required".
	assert.NoError(t, validateRouteConfig(l4Config(5432), models.RouteProtocolTCP))
	assert.NoError(t, validateRouteConfig(l4Config(5432), models.RouteProtocolUDP))
	// ...and the L4 validator is what runs instead.
	cfg := l4Config(5432)
	cfg.Backends = nil
	assert.ErrorIs(t, validateRouteConfig(cfg, models.RouteProtocolTCP), ErrL4MissingBackend)
	// HTTP routes still require a path.
	assert.Error(t, validateRouteConfig(l4Config(5432), models.RouteProtocolHTTP))
}

func TestValidateRouteShape_L4_ShapeAndListenerBothRun(t *testing.T) {
	streamID := uuid.New()

	// Shape violation is reported before the DB-backed collision check runs.
	fake := &fakeL4Checker{}
	w := &routeWrite{l4Ports: fake}
	cfg := l4Config(5432)
	cfg.Matches = []models.RouteMatch{{Path: &models.PathMatch{Type: "Prefix", Value: "/"}}}
	err := w.validateRouteShapeAndConflicts(cfg, nil, models.RouteProtocolTCP, uuid.Nil, &streamID, nil)
	assert.ErrorIs(t, err, ErrL4RejectsL7Field)
	assert.Zero(t, fake.calls)

	// A valid shape still reaches the listener collision check.
	fake = &fakeL4Checker{err: ErrPortCollision}
	w = &routeWrite{l4Ports: fake}
	err = w.validateRouteShapeAndConflicts(l4Config(5432), nil, models.RouteProtocolTCP, uuid.Nil, &streamID, nil)
	assert.ErrorIs(t, err, ErrPortCollision)
	assert.Equal(t, 1, fake.calls)

	// Missing port is a shape error, not a collision-check call.
	fake = &fakeL4Checker{}
	w = &routeWrite{l4Ports: fake}
	err = w.validateRouteShapeAndConflicts(l4Config(0), nil, models.RouteProtocolTCP, uuid.Nil, &streamID, nil)
	assert.ErrorIs(t, err, ErrL4MissingListenerPort)
	assert.Zero(t, fake.calls)
}

func TestValidateRouteShape_L4_RejectsHTTPOnlyBackendTrafficPolicy(t *testing.T) {
	streamID := uuid.New()
	cases := map[string]*routeplan.BackendTrafficPolicyInput{
		"rate limit":        {RateLimit: &models.RateLimitConfig{}},
		"retry":             {Retry: &models.RetryConfig{}},
		"compression":       {Compression: []models.CompressionConfig{{}}},
		"fault injection":   {FaultInjection: &models.FaultInjectionConfig{}},
		"request buffer":    {RequestBuffer: &models.RequestBufferConfig{}},
		"response override": {ResponseOverride: []models.ResponseOverrideRule{{}}},
	}
	for name, btp := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeL4Checker{}
			w := &routeWrite{l4Ports: fake}
			err := w.validateRouteShapeAndConflicts(l4Config(5432), btp, models.RouteProtocolTCP, uuid.Nil, &streamID, nil)
			assert.ErrorIs(t, err, ErrL4RejectsL7Field)
			assert.Zero(t, fake.calls)
		})
	}
	// Non-HTTP-only policy fields (load balancer, circuit breaker, health check, timeout) are allowed.
	w := &routeWrite{l4Ports: &fakeL4Checker{}}
	err := w.validateRouteShapeAndConflicts(l4Config(5432), &routeplan.BackendTrafficPolicyInput{LoadBalancer: &models.LoadBalancerConfig{}}, models.RouteProtocolTCP, uuid.Nil, &streamID, nil)
	assert.NoError(t, err)
}

func TestValidateL4PolicyInputs(t *testing.T) {
	assert.NoError(t, validateL4PolicyInputs("", nil, nil, nil, nil))
	assert.NoError(t, validateL4PolicyInputs(models.SecurityModeGeneral, nil, &routeplan.EnvoyExtensionPolicyInput{}, nil, nil))
	assert.ErrorIs(t, validateL4PolicyInputs(models.SecurityModeClient, nil, nil, nil, nil), ErrL4RejectsL7Field)
	assert.ErrorIs(t, validateL4PolicyInputs("", &routeplan.SecurityPolicyInput{}, nil, nil, nil), ErrL4RejectsL7Field)
	assert.ErrorIs(t, validateL4PolicyInputs("", nil, &routeplan.EnvoyExtensionPolicyInput{Lua: &models.LuaExtensionConfig{}}, nil, nil), ErrL4RejectsL7Field)
	assert.ErrorIs(t, validateL4PolicyInputs("", nil, nil, &routeplan.WafPolicyInput{}, nil), ErrL4RejectsL7Field)
	assert.ErrorIs(t, validateL4PolicyInputs("", nil, nil, nil, &routeplan.BackendTrafficPolicyInput{Retry: &models.RetryConfig{}}), ErrL4RejectsL7Field)
}

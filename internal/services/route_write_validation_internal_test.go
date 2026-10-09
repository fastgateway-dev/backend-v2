package services

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
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

func l4Config(port int) *models.RouteConfig {
	return &models.RouteConfig{ListenerPort: port}
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

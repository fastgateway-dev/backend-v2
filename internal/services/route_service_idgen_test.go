package services

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestRouteServiceIDGeneratorIsInjectable pins the seam that makes preview
// snapshots possible. Without it, the preview path mints a random ID whose
// first 8 hex characters reach every generated resource name.
func TestRouteServiceIDGeneratorIsInjectable(t *testing.T) {
	fixed := uuid.MustParse("99999999-9999-9999-9999-999999999999")
	s := &RouteService{idgen: func() uuid.UUID { return fixed }}

	require.Equal(t, fixed, s.newID())
	require.Equal(t, fixed, s.newID(), "injected generator must be stable across calls")

	var zero RouteService
	require.NotEqual(t, uuid.Nil, zero.newID(), "nil idgen must fall back to uuid.New")
	require.NotEqual(t, zero.newID(), zero.newID(), "fallback must still be random")
}

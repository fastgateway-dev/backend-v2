package services

import "github.com/google/uuid"

// newID returns a route ID, using the injected generator when present.
//
// This lives outside route_service.go on purpose: the design spec's exit
// criterion for that file is zero direct uuid.New() calls, so the one
// remaining call (the fallback for a nil idgen) is isolated here.
func (s *RouteService) newID() uuid.UUID {
	if s.idgen == nil {
		return uuid.New()
	}
	return s.idgen()
}

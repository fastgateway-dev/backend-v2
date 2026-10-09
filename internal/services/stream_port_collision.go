// Merge-aware listener port-collision check for L4 (TCP/UDP) stream routes.
//
// A Gateway listener is identified by (transport, port) with transport in
// {TCP, UDP}; HTTP/HTTPS/TLS listeners are TCP-transport, so tcp:53 and
// udp:53 coexist but tcp:443 collides with a Domain's HTTPS port.
//
// Scope follows the Gateway Template's mergeGateways:
//   - false: every Stream (and Domain) owns its own Gateway/LB, so only the
//     stream's own L4 routes can collide with each other.
//   - true:  all Gateways of the template are merged into one listener set, so
//     the candidate is checked against every Stream's L4 routes AND every
//     Domain's HTTP/HTTPS ports on that template.

package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// PortUse is an occupied listener identity: (Transport, Port), Transport
// "TCP" or "UDP". HTTP/HTTPS/TLS listeners are counted as TCP.
type PortUse = repository.PortUse

// ErrPortCollision is returned by CheckPortCollision when the requested
// (transport, port) is already used by another listener in scope.
var ErrPortCollision = errors.New("listener port is already in use")

// StreamPortStore is the slice of StreamRepository the port-collision check
// uses. *repository.StreamRepository satisfies it structurally.
type StreamPortStore interface {
	// UsedPortsByStream returns the L4 route ports of one stream.
	UsedPortsByStream(streamID uuid.UUID, excludeRouteID *uuid.UUID) ([]PortUse, error)
	// UsedL4Ports returns the L4 route ports of every stream on a template.
	UsedL4Ports(templateID uuid.UUID, excludeRouteID *uuid.UUID) ([]PortUse, error)
}

// DomainPortReader returns the HTTP/HTTPS listener ports (as TCP) of every
// domain on a template. *repository.DomainRepository satisfies it structurally.
type DomainPortReader interface {
	UsedPortsByTemplate(templateID uuid.UUID) ([]PortUse, error)
}

// SetPortSources wires the repositories CheckPortCollision reads. It is kept
// out of NewStreamService so the lifecycle-only constructor stays unchanged.
func (s *StreamService) SetPortSources(streams StreamPortStore, domains DomainPortReader) {
	s.portStore = streams
	s.domainPorts = domains
}

// CheckPortCollision validates that (transport, port) is free for a new or
// updated L4 route on the stream, scoped by the template's mergeGateways (see
// the file comment). excludeRouteID skips the route being updated so it does
// not collide with itself. It returns ErrPortCollision (wrapped, naming the
// conflict) on collision, nil otherwise.
func (s *StreamService) CheckPortCollision(streamID uuid.UUID, transport string, port int, excludeRouteID *uuid.UUID) error {
	if s.portStore == nil || s.domainPorts == nil {
		return errors.New("stream port sources are not configured")
	}
	transport = strings.ToUpper(transport)
	if transport != "TCP" && transport != "UDP" {
		return fmt.Errorf("invalid transport %q: must be TCP or UDP", transport)
	}

	stream, err := s.getStream(streamID)
	if err != nil {
		return err
	}
	tmpl, err := s.templateRepo.GetByID(stream.GatewayTemplateID)
	if err != nil {
		return fmt.Errorf("failed to load gateway template: %w", err)
	}

	if !tmpl.MergeGateways {
		used, err := s.portStore.UsedPortsByStream(streamID, excludeRouteID)
		if err != nil {
			return fmt.Errorf("failed to load stream ports: %w", err)
		}
		if hasPortUse(used, transport, port) {
			return fmt.Errorf("%w: %s/%d is used by another route on stream %q", ErrPortCollision, transport, port, stream.Name)
		}
		return nil
	}

	streamUsed, err := s.portStore.UsedL4Ports(tmpl.ID, excludeRouteID)
	if err != nil {
		return fmt.Errorf("failed to load template stream ports: %w", err)
	}
	if hasPortUse(streamUsed, transport, port) {
		return fmt.Errorf("%w: %s/%d is used by a stream route on merged gateway template %q", ErrPortCollision, transport, port, tmpl.Name)
	}
	domainUsed, err := s.domainPorts.UsedPortsByTemplate(tmpl.ID)
	if err != nil {
		return fmt.Errorf("failed to load template domain ports: %w", err)
	}
	if hasPortUse(domainUsed, transport, port) {
		return fmt.Errorf("%w: %s/%d is used by a domain HTTP/HTTPS listener on merged gateway template %q", ErrPortCollision, transport, port, tmpl.Name)
	}
	return nil
}

// hasPortUse reports whether used contains the exact (transport, port).
func hasPortUse(used []PortUse, transport string, port int) bool {
	for _, u := range used {
		if u.Transport == transport && u.Port == port {
			return true
		}
	}
	return false
}

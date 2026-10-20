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

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// PortUse is an occupied listener identity: (Transport, Port), Transport
// "TCP" or "UDP". HTTP/HTTPS/TLS listeners are counted as TCP.
type PortUse = repository.PortUse

// ErrPortCollision is returned by CheckPortCollision when the requested
// (transport, port) is already used by another listener in scope.
var ErrPortCollision = errors.New("listener port is already in use")

// ErrReservedPort is returned when a listener port is reserved by the platform
// (Envoy internal ports or - on a merged Gateway Template that serves domains -
// the template's HTTP/HTTPS ports).
var ErrReservedPort = errors.New("listener port is reserved")

// ErrInvalidListenerPort is returned when a listener port is outside 1-65535.
var ErrInvalidListenerPort = errors.New("listener port must be between 1 and 65535")

// ReservedPorts is the set of ports an L4 listener may not claim.
type ReservedPorts struct {
	// EnvoyInternal are ports the Envoy proxy itself binds (admin/stats).
	EnvoyInternal []int
	// DomainDefaults are the HTTP/HTTPS ports of a merged, domain-enabled
	// template. They are reserved even before any Domain exists on it.
	DomainDefaults []int
}

// DefaultReservedPorts is the template-independent reserved set.
var DefaultReservedPorts = ReservedPorts{
	EnvoyInternal: []int{19000, 19001},
}

// ValidateListenerPort rejects a port outside 1-65535 (ErrInvalidListenerPort)
// or in the reserved set (ErrReservedPort).
func ValidateListenerPort(port int, reserved ReservedPorts) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: got %d", ErrInvalidListenerPort, port)
	}
	for _, p := range reserved.EnvoyInternal {
		if p == port {
			return fmt.Errorf("%w: %d is used internally by Envoy", ErrReservedPort, port)
		}
	}
	for _, p := range reserved.DomainDefaults {
		if p == port {
			return fmt.Errorf("%w: %d is the HTTP/HTTPS port of a domain-enabled merged gateway template", ErrReservedPort, port)
		}
	}
	return nil
}

// templateDomainDefaults returns the ports of the template's hostname-routed
// (HTTP/HTTPS/TLS) listeners that a merged template reserves for domains, or nil
// when the template is not merged or declares none.
func templateDomainDefaults(tmpl *models.DomainTemplate) []int {
	if !tmpl.MergeGateways {
		return nil
	}
	var ports []int
	for _, l := range tmpl.Listeners.HostnameRouted() {
		ports = append(ports, l.Port)
	}
	return ports
}

// ErrPortOutOfRange is returned when a stream route's listener port falls
// outside the template's TCP/UDP range, or the template declares no range.
var ErrPortOutOfRange = errors.New("listener port is outside the template's TCP/UDP range")

// templatePortRange returns the template's shared TCP/UDP port range, if any.
func templatePortRange(tmpl *models.DomainTemplate) (int, int, bool) {
	return tmpl.Listeners.StreamRange()
}

// checkPortInRange rejects a stream route port outside the template's TCP/UDP
// range. A template with no range (domain-only) cannot host stream routes.
func checkPortInRange(port int, tmpl *models.DomainTemplate) error {
	lo, hi, ok := templatePortRange(tmpl)
	if !ok {
		return fmt.Errorf("%w: template %q has no TCP/UDP range", ErrPortOutOfRange, tmpl.Name)
	}
	if port < lo || port > hi {
		return fmt.Errorf("%w: %d not in %d-%d", ErrPortOutOfRange, port, lo, hi)
	}
	return nil
}

// StreamPortStore is the slice of StreamRepository the port-collision and
// claim checks use. *repository.StreamRepository satisfies it structurally.
type StreamPortStore interface {
	// UsedPortsByStream returns the L4 ports a stream's routes hold: route rows
	// plus pending create/update approvals.
	UsedPortsByStream(streamID uuid.UUID, excludeRouteID *uuid.UUID) ([]PortUse, error)
	// UsedL4Ports returns the L4 route ports of every stream on a template.
	// CheckPortCollision still reads it; it goes away with that check.
	UsedL4Ports(templateID uuid.UUID, excludeRouteID *uuid.UUID) ([]PortUse, error)
	// ClaimedPortsByTemplate returns the ports every stream on a template has
	// claimed, skipping excludeStreamID.
	ClaimedPortsByTemplate(templateID uuid.UUID, excludeStreamID *uuid.UUID) ([]PortUse, error)
	// L4PortOwnersByStream and PendingL4PortOwnersByStream name the routes
	// behind UsedPortsByStream (rows, and pending approvals) for error messages.
	L4PortOwnersByStream(streamID uuid.UUID) ([]L4PortOwner, error)
	PendingL4PortOwnersByStream(streamID uuid.UUID) ([]L4PortOwner, error)
}

// DomainPortReader returns the HTTP/HTTPS listener ports (as TCP) of every
// domain on a template. *repository.DomainRepository satisfies it structurally.
type DomainPortReader interface {
	UsedPortsByTemplate(templateID uuid.UUID) ([]PortUse, error)
}

// L4PortReader returns the ports every stream on a template has claimed.
// *repository.StreamRepository satisfies it structurally. It is the domain-
// and template-side view of the stream half of the collision scope.
type L4PortReader interface {
	ClaimedPortsByTemplate(templateID uuid.UUID, excludeStreamID *uuid.UUID) ([]PortUse, error)
}

// checkDomainPortsAgainstStreams rejects a domain whose bound listener ports hit a
// stream-claimed TCP port on the same merged template (the domain-side mirror
// of validateStreamPorts). Unmerged templates give every domain its own Gateway,
// so there is nothing shared to collide with. A merged template with an
// unwired reader fails closed rather than skipping the guard.
func checkDomainPortsAgainstStreams(streams L4PortReader, tmpl *models.DomainTemplate, ports []int) error {
	if !tmpl.MergeGateways {
		return nil
	}
	if streams == nil {
		return errors.New("stream port sources are not configured")
	}
	used, err := streams.ClaimedPortsByTemplate(tmpl.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to load template stream ports: %w", err)
	}
	for _, port := range ports {
		if hasPortUse(used, "TCP", port) {
			return fmt.Errorf("%w: TCP/%d is claimed by a stream on merged gateway template %q", ErrPortCollision, port, tmpl.Name)
		}
	}
	return nil
}

// checkMergedTemplateSet validates the listener set a merged template would
// have after its listeners change: no stream-claimed port may equal an existing
// domain's HTTP/HTTPS port, nor the template's own hostname-routed listener
// ports. Returns ErrPortCollision (wrapped).
func checkMergedTemplateSet(streams L4PortReader, domains DomainPortReader, tmpl *models.DomainTemplate) error {
	streamUsed, err := streams.ClaimedPortsByTemplate(tmpl.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to load template stream ports: %w", err)
	}
	domainUsed, err := domains.UsedPortsByTemplate(tmpl.ID)
	if err != nil {
		return fmt.Errorf("failed to load template domain ports: %w", err)
	}
	for _, u := range domainUsed {
		if hasPortUse(streamUsed, u.Transport, u.Port) {
			return fmt.Errorf("%w: %s/%d is claimed by both a stream and a domain on merged gateway template %q", ErrPortCollision, u.Transport, u.Port, tmpl.Name)
		}
	}
	for _, port := range templateDomainDefaults(tmpl) {
		if hasPortUse(streamUsed, "TCP", port) {
			return fmt.Errorf("%w: TCP/%d is claimed by a stream and is the HTTP/HTTPS port of merged gateway template %q", ErrPortCollision, port, tmpl.Name)
		}
	}
	return nil
}

// L4PortChecker is the slice of StreamService route writes use to reject an L4
// route whose (transport, port) collides or is reserved. *StreamService
// satisfies it.
type L4PortChecker interface {
	CheckPortCollision(streamID uuid.UUID, transport string, port int, excludeRouteID *uuid.UUID) error
}

var _ L4PortChecker = (*StreamService)(nil)

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

	if err := ValidateListenerPort(port, DefaultReservedPorts); err != nil {
		return err
	}
	if err := checkPortInRange(port, tmpl); err != nil {
		return err
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
	// An actual collision is reported above; what is left is the template's
	// default HTTP/HTTPS ports, reserved (as TCP) even before a Domain exists
	// so a stream cannot squat a port the first Domain would need.
	if transport == "TCP" {
		if err := ValidateListenerPort(port, ReservedPorts{DomainDefaults: templateDomainDefaults(tmpl)}); err != nil {
			return err
		}
	}
	return nil
}

// Stream port claims. A stream claims (transport, port) pairs; routes then
// pick from the claimed set. validateStreamPorts and checkUsedClaimsKept are
// the validators stream create/update call.

// ErrNoStreamPorts is returned when a stream's claim set is empty.
var ErrNoStreamPorts = errors.New("a stream must claim at least one port")

// ErrInvalidStreamPort is returned for a claim with a transport other than
// TCP/UDP, or a (transport, port) listed twice.
var ErrInvalidStreamPort = errors.New("invalid stream port")

// ErrStreamPortInUse is returned when an update drops a claim that a route (or
// a pending route approval) still uses.
var ErrStreamPortInUse = errors.New("stream port is in use by a route")

// validateStreamPorts checks a stream's proposed claim set against the
// template and returns it normalized (upper-case transports, TCP before UDP,
// then by port). selfID is the stream being updated (nil on create), so its
// own claims never collide with themselves.
//
// Scope follows mergeGateways, as in CheckPortCollision: a dedicated template
// gives the stream its own Gateway, so only the static rules apply; a merged
// one also collides with other streams' claims, domain ports, and (as TCP) the
// template's own hostname-routed listener ports.
func (s *StreamService) validateStreamPorts(tmpl *models.DomainTemplate, selfID *uuid.UUID, ports models.StreamPorts) (models.StreamPorts, error) {
	if len(ports) == 0 {
		return nil, ErrNoStreamPorts
	}
	norm := ports.Normalize()
	for i, c := range norm {
		if c.Transport != "TCP" && c.Transport != "UDP" {
			return nil, fmt.Errorf("%w: transport %q must be TCP or UDP", ErrInvalidStreamPort, c.Transport)
		}
		if i > 0 && norm[i-1] == c {
			return nil, fmt.Errorf("%w: %s/%d is listed more than once", ErrInvalidStreamPort, c.Transport, c.Port)
		}
	}
	for _, c := range norm {
		if err := ValidateListenerPort(c.Port, DefaultReservedPorts); err != nil {
			return nil, err
		}
		if err := checkPortInRange(c.Port, tmpl); err != nil {
			return nil, err
		}
	}
	if !tmpl.MergeGateways {
		return norm, nil
	}

	if s.portStore == nil || s.domainPorts == nil {
		return nil, errors.New("stream port sources are not configured")
	}
	claimed, err := s.portStore.ClaimedPortsByTemplate(tmpl.ID, selfID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template stream ports: %w", err)
	}
	domainUsed, err := s.domainPorts.UsedPortsByTemplate(tmpl.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template domain ports: %w", err)
	}
	for _, c := range norm {
		if hasPortUse(claimed, c.Transport, c.Port) {
			return nil, fmt.Errorf("%w: %s/%d is claimed by another stream on merged gateway template %q", ErrPortCollision, c.Transport, c.Port, tmpl.Name)
		}
		if hasPortUse(domainUsed, c.Transport, c.Port) {
			return nil, fmt.Errorf("%w: %s/%d is used by a domain HTTP/HTTPS listener on merged gateway template %q", ErrPortCollision, c.Transport, c.Port, tmpl.Name)
		}
		// What is left is the template's default HTTP/HTTPS ports, reserved (as
		// TCP) even before a Domain exists.
		if c.Transport == "TCP" {
			if err := ValidateListenerPort(c.Port, ReservedPorts{DomainDefaults: templateDomainDefaults(tmpl)}); err != nil {
				return nil, err
			}
		}
	}
	return norm, nil
}

// checkUsedClaimsKept rejects a claim set that drops a port a route still
// uses: a route row of the stream, or a pending create/update approval for one
// of its routes. It returns ErrStreamPortInUse (wrapped) naming the port and
// the route, marking the use "pending" when only an approval holds it.
func (s *StreamService) checkUsedClaimsKept(stream *models.Stream, next models.StreamPorts) error {
	if s.portStore == nil {
		return errors.New("stream port sources are not configured")
	}
	used, err := s.portStore.UsedPortsByStream(stream.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to load stream ports: %w", err)
	}
	keep := next.Normalize()
	for _, u := range used {
		if keep.Has(u.Transport, u.Port) {
			continue
		}
		return s.portInUseError(stream, u)
	}
	return nil
}

// portInUseError builds the ErrStreamPortInUse for a dropped, used claim. The
// owner lookups only name the route; a failed lookup degrades to an unnamed
// message rather than hiding the conflict.
func (s *StreamService) portInUseError(stream *models.Stream, u PortUse) error {
	rows, _ := s.portStore.L4PortOwnersByStream(stream.ID)
	for _, o := range rows {
		if o.Transport == u.Transport && o.Port == u.Port {
			return fmt.Errorf("%w: %s/%d is used by route %q", ErrStreamPortInUse, u.Transport, u.Port, o.RouteName)
		}
	}
	pending, _ := s.portStore.PendingL4PortOwnersByStream(stream.ID)
	for _, o := range pending {
		if o.Transport == u.Transport && o.Port == u.Port {
			return fmt.Errorf("%w: %s/%d is used by route %q (pending change)", ErrStreamPortInUse, u.Transport, u.Port, o.RouteName)
		}
	}
	return fmt.Errorf("%w: %s/%d is used by a route of stream %q", ErrStreamPortInUse, u.Transport, u.Port, stream.Name)
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

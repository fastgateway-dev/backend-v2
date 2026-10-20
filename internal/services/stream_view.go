// StreamViewService: the read-only views that make a Stream legible - its
// entry point (dedicated or shared IP, who shares it, which ports are taken
// by what) and the enriched stream list.
//
// Nothing here writes or applies anything. The entry point reads its port map
// from the same sources CheckPortCollision validates against (the owner-aware
// twins of UsedPortsByStream / UsedL4Ports / UsedPortsByTemplate, plus
// DefaultReservedPorts and templateDomainDefaults), so the map can never say
// "free" for a port that create would reject. TestStreamEntrypoint_AgreesWith
// CheckPortCollision pins that.

package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// Entry point modes. A template with mergeGateways gives every stream (and
// domain) on it one shared Envoy, Service and IP; otherwise each stream has
// its own.
const (
	EntrypointModeDedicated = "dedicated"
	EntrypointModeShared    = "shared"
)

// Port owner kinds and reserved-port reasons in the entry point port map.
const (
	PortOwnerStreamRoute = "stream_route"
	PortOwnerDomain      = "domain"

	ReservedReasonEnvoyInternal    = "envoy_internal"
	ReservedReasonTemplateListener = "template_listener"
)

// L4PortOwner is one listener an L4 route holds, with its route and stream.
type L4PortOwner = repository.L4PortOwner

// DomainPortOwner is one hostname-routed listener port a domain binds.
type DomainPortOwner = repository.DomainPortOwner

// StreamEntrypoint is GET .../streams/:streamId/entrypoint and
// GET .../domain-templates/:domainTemplateId/entrypoint.
type StreamEntrypoint struct {
	Mode         string                   `json:"mode"`
	TemplateID   uuid.UUID                `json:"templateId"`
	TemplateName string                   `json:"templateName"`
	GatewayClass string                   `json:"gatewayClass"`
	Address      string                   `json:"address"`
	PortRange    *EntrypointPortRange     `json:"portRange"`
	SharedWith   EntrypointSharedWith     `json:"sharedWith"`
	Ports        []EntrypointPort         `json:"ports"`
	Reserved     []EntrypointReservedPort `json:"reserved"`
}

// EntrypointPortRange is the template's TCP/UDP range (inclusive).
type EntrypointPortRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// EntrypointSharedWith lists who else is on a shared entry point. Both lists
// are empty for a dedicated one.
type EntrypointSharedWith struct {
	Streams []EntrypointStreamRef `json:"streams"`
	Domains []EntrypointDomainRef `json:"domains"`
}

// EntrypointStreamRef names another stream on the shared entry point.
type EntrypointStreamRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// EntrypointDomainRef names a domain on the shared entry point.
type EntrypointDomainRef struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Hostname string    `json:"hostname"`
}

// EntrypointPort is one taken (transport, port) and what holds it.
type EntrypointPort struct {
	Transport string              `json:"transport"`
	Port      int                 `json:"port"`
	Owner     EntrypointPortOwner `json:"owner"`
}

// EntrypointPortOwner is either a stream route (Kind "stream_route": the
// stream and route fields are set) or a domain (Kind "domain": the domain
// fields are set).
type EntrypointPortOwner struct {
	Kind       string     `json:"kind"`
	StreamID   *uuid.UUID `json:"streamId,omitempty"`
	StreamName string     `json:"streamName,omitempty"`
	RouteID    *uuid.UUID `json:"routeId,omitempty"`
	RouteName  string     `json:"routeName,omitempty"`
	Status     string     `json:"status,omitempty"`
	DomainID   *uuid.UUID `json:"domainId,omitempty"`
	DomainName string     `json:"domainName,omitempty"`
}

// EntrypointReservedPort is a (transport, port) no route may claim, and why.
type EntrypointReservedPort struct {
	Transport string `json:"transport"`
	Port      int    `json:"port"`
	Reason    string `json:"reason"`
}

// StreamViewStore is the slice of StreamRepository the stream views read.
// *repository.StreamRepository satisfies it structurally.
type StreamViewStore interface {
	ListByProjectID(projectID uuid.UUID) ([]models.Stream, error)
	ListByGatewayTemplateID(templateID uuid.UUID) ([]models.Stream, error)
	L4PortOwnersByStream(streamID uuid.UUID) ([]L4PortOwner, error)
	L4PortOwnersByTemplate(templateID uuid.UUID) ([]L4PortOwner, error)
	L4PortOwnersByStreamIDs(streamIDs []uuid.UUID) ([]L4PortOwner, error)
}

// DomainPortOwnerReader is the slice of DomainRepository the entry point
// reads. *repository.DomainRepository satisfies it structurally.
type DomainPortOwnerReader interface {
	ListByTemplateID(templateID uuid.UUID) ([]models.Domain, error)
	PortOwnersByTemplate(templateID uuid.UUID) ([]DomainPortOwner, error)
}

// StreamViewServiceDeps carries everything StreamViewService needs. Every
// field is required.
type StreamViewServiceDeps struct {
	Streams   StreamViewStore
	Templates StreamTemplateReader
	Domains   DomainPortOwnerReader
	Gateways  GatewayStatusReader
}

// StreamViewService builds the read-only stream views.
type StreamViewService struct {
	streams   StreamViewStore
	templates StreamTemplateReader
	domains   DomainPortOwnerReader
	gateways  GatewayStatusReader
}

// NewStreamViewService builds a StreamViewService. It panics if a dependency
// is missing, like NewApprovalService.
func NewStreamViewService(deps StreamViewServiceDeps) *StreamViewService {
	var missing []string
	if deps.Streams == nil {
		missing = append(missing, "Streams")
	}
	if deps.Templates == nil {
		missing = append(missing, "Templates")
	}
	if deps.Domains == nil {
		missing = append(missing, "Domains")
	}
	if deps.Gateways == nil {
		missing = append(missing, "Gateways")
	}
	if len(missing) > 0 {
		panic("services.NewStreamViewService: missing required dependency: " + strings.Join(missing, ", "))
	}
	return &StreamViewService{streams: deps.Streams, templates: deps.Templates, domains: deps.Domains, gateways: deps.Gateways}
}

// entrypointMode is "shared" if and only if the template merges gateways.
func entrypointMode(tmpl *models.DomainTemplate) string {
	if tmpl.MergeGateways {
		return EntrypointModeShared
	}
	return EntrypointModeDedicated
}

// StreamEntrypoint describes the entry point of an existing stream. The
// caller has already checked the stream belongs to the project. A template
// lookup failure is an error: the mode is never guessed.
func (s *StreamViewService) StreamEntrypoint(ctx context.Context, stream *models.Stream) (*StreamEntrypoint, error) {
	tmpl, err := s.templates.GetByID(stream.GatewayTemplateID)
	if err != nil {
		return nil, fmt.Errorf("failed to load gateway template: %w", err)
	}
	return s.buildEntrypoint(ctx, tmpl, stream)
}

// TemplateEntrypoint describes the entry point a new stream on the template
// would get. A missing template, or one in another project, is
// ErrStreamTemplateNotFound / ErrStreamTemplateWrongProject (both 404).
func (s *StreamViewService) TemplateEntrypoint(ctx context.Context, projectID, templateID uuid.UUID) (*StreamEntrypoint, error) {
	tmpl, err := s.templates.GetByID(templateID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrStreamTemplateNotFound
		}
		return nil, fmt.Errorf("failed to load gateway template: %w", err)
	}
	if tmpl.ProjectID != projectID {
		return nil, ErrStreamTemplateWrongProject
	}
	return s.buildEntrypoint(ctx, tmpl, nil)
}

// buildEntrypoint is the one builder behind both endpoints. self is the
// stream being described, or nil for the template variant (a stream that
// does not exist yet, with an empty port space of its own).
func (s *StreamViewService) buildEntrypoint(ctx context.Context, tmpl *models.DomainTemplate, self *models.Stream) (*StreamEntrypoint, error) {
	ep := &StreamEntrypoint{
		Mode:         entrypointMode(tmpl),
		TemplateID:   tmpl.ID,
		TemplateName: tmpl.Name,
		GatewayClass: tmpl.K8sGatewayClassName,
		SharedWith:   EntrypointSharedWith{Streams: []EntrypointStreamRef{}, Domains: []EntrypointDomainRef{}},
		Ports:        []EntrypointPort{},
		Reserved:     reservedPorts(tmpl),
	}
	if lo, hi, ok := templatePortRange(tmpl); ok {
		ep.PortRange = &EntrypointPortRange{Min: lo, Max: hi}
	}

	if ep.Mode == EntrypointModeDedicated {
		if self != nil {
			owners, err := s.streams.L4PortOwnersByStream(self.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to load stream ports: %w", err)
			}
			ep.Ports = appendStreamRoutePorts(ep.Ports, owners)
			addr, err := s.gatewayAddress(ctx, self.ProjectID, self.Namespace, self.K8sGatewayName)
			if err != nil {
				return nil, err
			}
			ep.Address = addr
		}
		sortEntrypointPorts(ep.Ports)
		return ep, nil
	}

	// Shared: the port scope is the whole template, exactly as
	// CheckPortCollision's merged branch.
	owners, err := s.streams.L4PortOwnersByTemplate(tmpl.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template stream ports: %w", err)
	}
	ep.Ports = appendStreamRoutePorts(ep.Ports, owners)
	domainOwners, err := s.domains.PortOwnersByTemplate(tmpl.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template domain ports: %w", err)
	}
	for _, o := range domainOwners {
		id := o.DomainID
		ep.Ports = append(ep.Ports, EntrypointPort{Transport: o.Transport, Port: o.Port,
			Owner: EntrypointPortOwner{Kind: PortOwnerDomain, DomainID: &id, DomainName: o.DomainName}})
	}
	sortEntrypointPorts(ep.Ports)

	members, err := s.streams.ListByGatewayTemplateID(tmpl.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template streams: %w", err)
	}
	others := make([]models.Stream, 0, len(members))
	for _, m := range members {
		if self != nil && m.ID == self.ID {
			continue
		}
		others = append(others, m)
		ep.SharedWith.Streams = append(ep.SharedWith.Streams, EntrypointStreamRef{ID: m.ID, Name: m.Name})
	}
	domains, err := s.domains.ListByTemplateID(tmpl.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template domains: %w", err)
	}
	for _, d := range domains {
		ep.SharedWith.Domains = append(ep.SharedWith.Domains, EntrypointDomainRef{ID: d.ID, Name: d.Name, Hostname: d.Hostname})
	}

	// Address: this stream's, else the first member stream (by name) that
	// has one, else the first deployed domain on the template that has one.
	// Every Gateway on a merged template, domains' included, reflects the one
	// shared Service, so a template that only serves domains so far still
	// has a known IP.
	candidates := others
	if self != nil {
		candidates = append([]models.Stream{*self}, others...)
	}
	for i := range candidates {
		addr, err := s.gatewayAddress(ctx, candidates[i].ProjectID, candidates[i].Namespace, candidates[i].K8sGatewayName)
		if err != nil {
			return nil, err
		}
		if addr != "" {
			ep.Address = addr
			return ep, nil
		}
	}
	for _, d := range domains {
		if d.K8sGatewayName == "" {
			continue // not deployed yet: it has no Gateway to read
		}
		addr, err := s.gatewayAddress(ctx, d.ProjectID, d.Namespace, d.K8sGatewayName)
		if err != nil {
			return nil, err
		}
		if addr != "" {
			ep.Address = addr
			return ep, nil
		}
	}
	return ep, nil
}

// gatewayAddress reads a Gateway's first status address. A Gateway that does
// not exist yet, or has no address yet, is "" (assigned on first deploy); any
// other read failure is an error.
func (s *StreamViewService) gatewayAddress(ctx context.Context, projectID uuid.UUID, namespace, name string) (string, error) {
	obj, err := s.gateways.GetGateway(ctx, projectID, namespace, name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read Gateway %s/%s: %w", namespace, name, err)
	}
	addr, ok := resolveGatewayAddress(obj)
	if !ok {
		return "", nil
	}
	return addr.Value, nil
}

// reservedPorts lists what CheckPortCollision reserves: DefaultReservedPorts
// for both transports (ValidateListenerPort rejects them for TCP and UDP),
// plus, on a merged template, its hostname-routed listener ports as TCP.
func reservedPorts(tmpl *models.DomainTemplate) []EntrypointReservedPort {
	var out []EntrypointReservedPort
	seen := map[EntrypointReservedPort]bool{}
	add := func(transport string, port int, reason string) {
		r := EntrypointReservedPort{Transport: transport, Port: port, Reason: reason}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, transport := range []string{"TCP", "UDP"} {
		for _, p := range DefaultReservedPorts.EnvoyInternal {
			add(transport, p, ReservedReasonEnvoyInternal)
		}
	}
	for _, p := range templateDomainDefaults(tmpl) {
		add("TCP", p, ReservedReasonTemplateListener)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Transport < out[j].Transport
	})
	return out
}

func appendStreamRoutePorts(ports []EntrypointPort, owners []L4PortOwner) []EntrypointPort {
	for _, o := range owners {
		streamID, routeID := o.StreamID, o.RouteID
		ports = append(ports, EntrypointPort{Transport: o.Transport, Port: o.Port, Owner: EntrypointPortOwner{
			Kind: PortOwnerStreamRoute, StreamID: &streamID, StreamName: o.StreamName,
			RouteID: &routeID, RouteName: o.RouteName, Status: o.Status,
		}})
	}
	return ports
}

// sortEntrypointPorts orders the map by port, then transport, then owner.
func sortEntrypointPorts(ports []EntrypointPort) {
	sort.SliceStable(ports, func(i, j int) bool {
		a, b := ports[i], ports[j]
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if a.Transport != b.Transport {
			return a.Transport < b.Transport
		}
		if a.Owner.Kind != b.Owner.Kind {
			return a.Owner.Kind < b.Owner.Kind
		}
		return a.Owner.StreamName+a.Owner.RouteName+a.Owner.DomainName < b.Owner.StreamName+b.Owner.RouteName+b.Owner.DomainName
	})
}

// StreamListItem is one row of GET /projects/:projectId/streams: the stream
// plus its entry point mode, template name and the ports its routes hold.
// The embedded Stream keeps every existing field at the top level.
type StreamListItem struct {
	models.Stream
	Mode                string       `json:"mode"`
	GatewayTemplateName string       `json:"gatewayTemplateName"`
	RouteCount          int          `json:"routeCount"`
	Ports               []StreamPort `json:"ports"`
}

// StreamPort is a (transport, port) one of a stream's L4 routes holds.
type StreamPort struct {
	Transport string `json:"transport"`
	Port      int    `json:"port"`
}

// ListWithSummary lists the project's streams, ordered by name, each with
// its mode, template name, non-rejected L4 route count and their ports. It
// issues one grouped route query and one template read per distinct
// template - never a query per stream. A template lookup failure is an
// error: the mode is never guessed.
func (s *StreamViewService) ListWithSummary(projectID uuid.UUID) ([]StreamListItem, error) {
	streams, err := s.streams.ListByProjectID(projectID)
	if err != nil {
		return nil, err
	}
	items := make([]StreamListItem, 0, len(streams))
	if len(streams) == 0 {
		return items, nil
	}

	ids := make([]uuid.UUID, 0, len(streams))
	for _, st := range streams {
		ids = append(ids, st.ID)
	}
	owners, err := s.streams.L4PortOwnersByStreamIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("failed to load stream ports: %w", err)
	}
	portsByStream := make(map[uuid.UUID][]StreamPort, len(streams))
	for _, o := range owners {
		portsByStream[o.StreamID] = append(portsByStream[o.StreamID], StreamPort{Transport: o.Transport, Port: o.Port})
	}

	templates := make(map[uuid.UUID]*models.DomainTemplate)
	for _, st := range streams {
		tmpl, ok := templates[st.GatewayTemplateID]
		if !ok {
			tmpl, err = s.templates.GetByID(st.GatewayTemplateID)
			if err != nil {
				return nil, fmt.Errorf("failed to load gateway template %s for stream %q: %w", st.GatewayTemplateID, st.Name, err)
			}
			templates[st.GatewayTemplateID] = tmpl
		}
		ports := portsByStream[st.ID]
		if ports == nil {
			ports = []StreamPort{}
		}
		sort.Slice(ports, func(i, j int) bool {
			if ports[i].Port != ports[j].Port {
				return ports[i].Port < ports[j].Port
			}
			return ports[i].Transport < ports[j].Transport
		})
		items = append(items, StreamListItem{
			Stream:              st,
			Mode:                entrypointMode(tmpl),
			GatewayTemplateName: tmpl.Name,
			RouteCount:          len(ports),
			Ports:               ports,
		})
	}
	return items, nil
}

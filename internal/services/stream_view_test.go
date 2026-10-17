package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// worldRoute is one L4 route row of a portWorld.
type worldRoute struct {
	id        uuid.UUID
	streamID  uuid.UUID
	name      string
	transport string
	port      int
	status    models.RouteStatus
}

// portWorld is one in-memory fixture that answers BOTH the collision
// sources CheckPortCollision reads (StreamStore, StreamPortStore,
// DomainPortReader) and the owner-aware sources the entry point reads
// (StreamViewStore, DomainPortOwnerReader). Every answer is derived from the
// same rows with the repository's rule (rejected routes hold no port), so
// the agreement test compares the two consumers, not two data sets. The
// repository tests pin that the real SQL of the two query families agrees.
type portWorld struct {
	tmpl    *models.DomainTemplate
	streams []models.Stream
	routes  []worldRoute
	domains []models.Domain

	ownerCalls struct{ byStream, byTemplate, byStreamIDs int }
}

func (w *portWorld) streamByID(id uuid.UUID) (*models.Stream, bool) {
	for i := range w.streams {
		if w.streams[i].ID == id {
			cp := w.streams[i]
			return &cp, true
		}
	}
	return nil, false
}

func (w *portWorld) liveRoutes(keep func(worldRoute) bool) []worldRoute {
	var out []worldRoute
	for _, r := range w.routes {
		if r.status != models.RouteStatusRejected && keep(r) {
			out = append(out, r)
		}
	}
	return out
}

func (w *portWorld) owners(rs []worldRoute) []services.L4PortOwner {
	out := []services.L4PortOwner{}
	for _, r := range rs {
		s, _ := w.streamByID(r.streamID)
		out = append(out, services.L4PortOwner{Transport: r.transport, Port: r.port, StreamID: r.streamID,
			StreamName: s.Name, RouteID: r.id, RouteName: r.name, Status: string(r.status)})
	}
	return out
}

func (w *portWorld) onTemplate(r worldRoute) bool {
	s, ok := w.streamByID(r.streamID)
	return ok && s.GatewayTemplateID == w.tmpl.ID
}

// --- services.StreamStore (for NewStreamService) ---

func (w *portWorld) Create(s *models.Stream) error { w.streams = append(w.streams, *s); return nil }
func (w *portWorld) GetByID(id uuid.UUID) (*models.Stream, error) {
	if s, ok := w.streamByID(id); ok {
		return s, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (w *portWorld) ListByProjectID(projectID uuid.UUID) ([]models.Stream, error) {
	var out []models.Stream
	for _, s := range w.streams {
		if s.ProjectID == projectID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (w *portWorld) ExistsByName(uuid.UUID, string) (bool, error) { return false, nil }
func (w *portWorld) Update(*models.Stream) error                  { return nil }
func (w *portWorld) Delete(uuid.UUID) error                       { return nil }

// --- services.StreamPortStore / DomainPortReader (CheckPortCollision) ---

func (w *portWorld) UsedPortsByStream(streamID uuid.UUID, exclude *uuid.UUID) ([]services.PortUse, error) {
	var out []services.PortUse
	for _, r := range w.liveRoutes(func(r worldRoute) bool { return r.streamID == streamID && (exclude == nil || r.id != *exclude) }) {
		out = append(out, services.PortUse{Transport: r.transport, Port: r.port})
	}
	return out, nil
}
func (w *portWorld) UsedL4Ports(_ uuid.UUID, exclude *uuid.UUID) ([]services.PortUse, error) {
	var out []services.PortUse
	for _, r := range w.liveRoutes(func(r worldRoute) bool { return w.onTemplate(r) && (exclude == nil || r.id != *exclude) }) {
		out = append(out, services.PortUse{Transport: r.transport, Port: r.port})
	}
	return out, nil
}
func (w *portWorld) UsedPortsByTemplate(uuid.UUID) ([]services.PortUse, error) {
	var out []services.PortUse
	for _, o := range w.domainOwners() {
		out = append(out, services.PortUse{Transport: o.Transport, Port: o.Port})
	}
	return out, nil
}

// --- services.StreamViewStore / DomainPortOwnerReader (entry point) ---

func (w *portWorld) ListByGatewayTemplateID(templateID uuid.UUID) ([]models.Stream, error) {
	var out []models.Stream
	for _, s := range w.streams {
		if s.GatewayTemplateID == templateID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (w *portWorld) L4PortOwnersByStream(streamID uuid.UUID) ([]services.L4PortOwner, error) {
	w.ownerCalls.byStream++
	return w.owners(w.liveRoutes(func(r worldRoute) bool { return r.streamID == streamID })), nil
}
func (w *portWorld) L4PortOwnersByTemplate(uuid.UUID) ([]services.L4PortOwner, error) {
	w.ownerCalls.byTemplate++
	return w.owners(w.liveRoutes(w.onTemplate)), nil
}
func (w *portWorld) L4PortOwnersByStreamIDs(ids []uuid.UUID) ([]services.L4PortOwner, error) {
	w.ownerCalls.byStreamIDs++
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	return w.owners(w.liveRoutes(func(r worldRoute) bool { return want[r.streamID] })), nil
}
func (w *portWorld) ListByTemplateID(uuid.UUID) ([]models.Domain, error) { return w.domains, nil }
func (w *portWorld) PortOwnersByTemplate(uuid.UUID) ([]services.DomainPortOwner, error) {
	return w.domainOwners(), nil
}

func (w *portWorld) domainOwners() []services.DomainPortOwner {
	portByName := map[string]int{}
	for _, l := range w.tmpl.Listeners.HostnameRouted() {
		portByName[l.Name] = l.Port
	}
	out := []services.DomainPortOwner{}
	for _, d := range w.domains {
		for _, name := range d.BoundListeners {
			if p, ok := portByName[name]; ok {
				out = append(out, services.DomainPortOwner{Transport: "TCP", Port: p, DomainID: d.ID, DomainName: d.Name})
			}
		}
	}
	return out
}

// fakeGatewayReader answers GetGateway from a map of stream Gateway name to
// address ("" means the Gateway exists with no address yet). A name in errs
// fails with that error; any other name is NotFound.
type fakeGatewayReader struct {
	addrs map[string]string
	errs  map[string]error
	calls []string
}

func (f *fakeGatewayReader) GetGateway(_ context.Context, _ uuid.UUID, _, name string) (*unstructured.Unstructured, error) {
	f.calls = append(f.calls, name)
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	addr, ok := f.addrs[name]
	if !ok {
		return nil, k8serrors.NewNotFound(schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "gateways"}, name)
	}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{"metadata": map[string]interface{}{"name": name}}}
	if addr != "" {
		obj.Object["status"] = map[string]interface{}{"addresses": []interface{}{map[string]interface{}{"type": "IPAddress", "value": addr}}}
	}
	return obj, nil
}

// newPortWorld is the shared fixture: a template with HTTP:80 (outside the
// range), HTTPS:19443 (inside it) and a TCP/UDP range 18990-60010 that also
// covers the reserved 19000, 19001 and 60000; three streams ("test-stream"
// owns routes, "cache" owns routes, "fresh" owns none) and two domains.
func newPortWorld(merge bool) (*portWorld, *models.Stream, *models.Stream, *models.Stream) {
	projectID := uuid.New()
	tmpl := &models.DomainTemplate{
		ID: uuid.New(), ProjectID: projectID, Name: "shared-envoy", K8sGatewayClassName: "shared-envoy-clusterip",
		MergeGateways: merge,
		Listeners: models.Listeners{
			{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
			{Name: "https", Protocol: models.ListenerHTTPS, Port: 19443, TLSMode: models.TLSListenerTerminate},
			{Name: "stream", Protocol: models.ListenerTCP, PortRangeMin: 18990, PortRangeMax: 60010},
		},
	}
	mk := func(name string) models.Stream {
		return models.Stream{ID: uuid.New(), ProjectID: projectID, Name: name, Namespace: "fastgateway-system",
			GatewayTemplateID: tmpl.ID, K8sGatewayName: "str-" + name, K8sGatewayClass: tmpl.K8sGatewayClassName}
	}
	self, cache, fresh := mk("test-stream"), mk("cache"), mk("fresh")
	w := &portWorld{tmpl: tmpl, streams: []models.Stream{self, cache, fresh}}
	route := func(s models.Stream, name, transport string, port int, status models.RouteStatus) {
		w.routes = append(w.routes, worldRoute{id: uuid.New(), streamID: s.ID, name: name, transport: transport, port: port, status: status})
	}
	route(self, "postgres", "TCP", 19100, models.RouteStatusActive)
	route(self, "dns", "UDP", 19100, models.RouteStatusPendingCreate)
	route(self, "old", "TCP", 19101, models.RouteStatusRejected)
	route(cache, "redis", "TCP", 19102, models.RouteStatusActive)
	route(cache, "syslog", "UDP", 19103, models.RouteStatusPendingUpdate)
	w.domains = []models.Domain{
		{ID: uuid.New(), ProjectID: projectID, Name: "api", Hostname: "api.example.com", DomainTemplateID: &tmpl.ID, BoundListeners: models.StringList{"http", "https"}},
		{ID: uuid.New(), ProjectID: projectID, Name: "web", Hostname: "web.example.com", DomainTemplateID: &tmpl.ID, BoundListeners: models.StringList{"https"}},
	}
	return w, &w.streams[0], &w.streams[1], &w.streams[2]
}

func newViewService(w *portWorld, gw *fakeGatewayReader) *services.StreamViewService {
	return services.NewStreamViewService(services.StreamViewServiceDeps{
		Streams: w, Templates: &fakeTemplateReader{tmpl: w.tmpl}, Domains: w, Gateways: gw,
	})
}

func newCollisionService(t *testing.T, w *portWorld) *services.StreamService {
	t.Helper()
	svc := services.NewStreamService(w, &fakeTemplateReader{tmpl: w.tmpl}, &fakeRouteCounter{}, mocks.NewMockGatewayApplier(t), allowAllNamespaces{})
	svc.SetPortSources(w, w)
	return svc
}

func portKeys(ep *services.StreamEntrypoint) []string {
	var out []string
	for _, p := range ep.Ports {
		out = append(out, p.Transport+"/"+strconv.Itoa(p.Port)+" "+p.Owner.Kind)
	}
	return out
}

func TestNewStreamViewService_RequiresEveryDependency(t *testing.T) {
	w, _, _, _ := newPortWorld(false)
	full := func() services.StreamViewServiceDeps {
		return services.StreamViewServiceDeps{Streams: w, Templates: &fakeTemplateReader{tmpl: w.tmpl}, Domains: w, Gateways: &fakeGatewayReader{}}
	}
	require.NotPanics(t, func() { services.NewStreamViewService(full()) })
	for name, breakIt := range map[string]func(*services.StreamViewServiceDeps){
		"Streams":   func(d *services.StreamViewServiceDeps) { d.Streams = nil },
		"Templates": func(d *services.StreamViewServiceDeps) { d.Templates = nil },
		"Domains":   func(d *services.StreamViewServiceDeps) { d.Domains = nil },
		"Gateways":  func(d *services.StreamViewServiceDeps) { d.Gateways = nil },
	} {
		d := full()
		breakIt(&d)
		assert.PanicsWithValue(t, "services.NewStreamViewService: missing required dependency: "+name,
			func() { services.NewStreamViewService(d) })
	}
}

func TestStreamEntrypoint_Dedicated(t *testing.T) {
	w, self, _, _ := newPortWorld(false)
	gw := &fakeGatewayReader{addrs: map[string]string{"str-test-stream": "10.0.0.7", "str-cache": "10.0.0.8"}}

	ep, err := newViewService(w, gw).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	assert.Equal(t, services.EntrypointModeDedicated, ep.Mode)
	assert.Equal(t, w.tmpl.ID, ep.TemplateID)
	assert.Equal(t, "shared-envoy", ep.TemplateName)
	assert.Equal(t, "shared-envoy-clusterip", ep.GatewayClass)
	assert.Equal(t, "10.0.0.7", ep.Address, "dedicated: this stream's own Gateway address")
	assert.Equal(t, &services.EntrypointPortRange{Min: 18990, Max: 60010}, ep.PortRange)
	assert.Empty(t, ep.SharedWith.Streams)
	assert.Empty(t, ep.SharedWith.Domains)
	assert.Equal(t, []string{"TCP/19100 stream_route", "UDP/19100 stream_route"}, portKeys(ep),
		"dedicated: only this stream's routes; the rejected route holds no port")
	assert.Equal(t, []services.EntrypointReservedPort{
		{Transport: "TCP", Port: 19000, Reason: "envoy_internal"}, {Transport: "UDP", Port: 19000, Reason: "envoy_internal"},
		{Transport: "TCP", Port: 19001, Reason: "envoy_internal"}, {Transport: "UDP", Port: 19001, Reason: "envoy_internal"},
		{Transport: "TCP", Port: 60000, Reason: "placeholder"}, {Transport: "UDP", Port: 60000, Reason: "placeholder"},
	}, ep.Reserved, "dedicated: no template_listener reservations")
	assert.Equal(t, []string{"str-test-stream"}, gw.calls, "dedicated reads only its own Gateway")
}

func TestStreamEntrypoint_Shared(t *testing.T) {
	w, self, cache, fresh := newPortWorld(true)
	gw := &fakeGatewayReader{addrs: map[string]string{"str-test-stream": "10.0.0.5"}}

	ep, err := newViewService(w, gw).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	assert.Equal(t, services.EntrypointModeShared, ep.Mode)
	assert.Equal(t, "10.0.0.5", ep.Address)
	assert.Equal(t, []services.EntrypointStreamRef{{ID: cache.ID, Name: "cache"}, {ID: fresh.ID, Name: "fresh"}}, ep.SharedWith.Streams,
		"sharedWith excludes the stream itself")
	require.Len(t, ep.SharedWith.Domains, 2)
	assert.Equal(t, services.EntrypointDomainRef{ID: w.domains[0].ID, Name: "api", Hostname: "api.example.com"}, ep.SharedWith.Domains[0])
	assert.Equal(t, []string{
		"TCP/80 domain",
		"TCP/19100 stream_route", "UDP/19100 stream_route",
		"TCP/19102 stream_route", "UDP/19103 stream_route",
		"TCP/19443 domain", "TCP/19443 domain",
	}, portKeys(ep), "shared: every stream's routes plus every domain's bound listener ports")
	assert.Contains(t, ep.Reserved, services.EntrypointReservedPort{Transport: "TCP", Port: 80, Reason: "template_listener"})
	assert.Contains(t, ep.Reserved, services.EntrypointReservedPort{Transport: "TCP", Port: 19443, Reason: "template_listener"})
	assert.NotContains(t, ep.Reserved, services.EntrypointReservedPort{Transport: "UDP", Port: 19443, Reason: "template_listener"},
		"template listeners are reserved for TCP only, as CheckPortCollision does")

	owner := ep.Ports[1].Owner
	require.NotNil(t, owner.StreamID)
	assert.Equal(t, self.ID, *owner.StreamID)
	assert.Equal(t, "test-stream", owner.StreamName)
	assert.Equal(t, "postgres", owner.RouteName)
	assert.Equal(t, "active", owner.Status)
	assert.Nil(t, owner.DomainID)
}

func TestStreamEntrypoint_SharedAddressFallbackOrder(t *testing.T) {
	cases := []struct {
		name  string
		addrs map[string]string
		want  string
		calls []string
	}{
		{"own address wins", map[string]string{"str-test-stream": "10.0.0.5", "str-cache": "10.0.0.6"}, "10.0.0.5", []string{"str-test-stream"}},
		{"own Gateway has no address yet: first member by name", map[string]string{"str-test-stream": "", "str-cache": "10.0.0.6", "str-fresh": "10.0.0.9"}, "10.0.0.6", []string{"str-test-stream", "str-cache"}},
		{"own Gateway missing: next member with one", map[string]string{"str-fresh": "10.0.0.9"}, "10.0.0.9", []string{"str-test-stream", "str-cache", "str-fresh"}},
		{"nobody has one: empty (assigned on first deploy)", map[string]string{}, "", []string{"str-test-stream", "str-cache", "str-fresh"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, self, _, _ := newPortWorld(true)
			gw := &fakeGatewayReader{addrs: tc.addrs}
			ep, err := newViewService(w, gw).StreamEntrypoint(context.Background(), self)
			require.NoError(t, err)
			assert.Equal(t, tc.want, ep.Address)
			assert.Equal(t, tc.calls, gw.calls)
		})
	}
}

func TestStreamEntrypoint_GatewayReadFailureIsAnError(t *testing.T) {
	w, self, _, _ := newPortWorld(false)
	gw := &fakeGatewayReader{errs: map[string]error{"str-test-stream": errors.New("connection refused")}}

	ep, err := newViewService(w, gw).StreamEntrypoint(context.Background(), self)
	assert.Nil(t, ep)
	assert.ErrorContains(t, err, "connection refused")
}

func TestStreamEntrypoint_TemplateLookupFailureIsAnError(t *testing.T) {
	w, self, _, _ := newPortWorld(true)
	svc := services.NewStreamViewService(services.StreamViewServiceDeps{
		Streams: w, Templates: &fakeTemplateReader{err: errors.New("db down")}, Domains: w, Gateways: &fakeGatewayReader{},
	})

	ep, err := svc.StreamEntrypoint(context.Background(), self)
	assert.Nil(t, ep, "never fall back to a guessed mode")
	assert.ErrorContains(t, err, "db down")
	assert.NotErrorIs(t, err, services.ErrStreamTemplateNotFound, "a broken stream->template link is a 500, not a 404")
}

func TestStreamEntrypoint_NoStreamRange_PortRangeNull(t *testing.T) {
	w, self, _, _ := newPortWorld(false)
	w.tmpl.Listeners = models.Listeners{{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate}}

	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)
	assert.Nil(t, ep.PortRange)
	raw, err := json.Marshal(ep)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"portRange":null`)
}

func TestTemplateEntrypoint(t *testing.T) {
	t.Run("dedicated: empty port space, no address", func(t *testing.T) {
		w, _, _, _ := newPortWorld(false)
		gw := &fakeGatewayReader{addrs: map[string]string{"str-test-stream": "10.0.0.7"}}
		ep, err := newViewService(w, gw).TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
		require.NoError(t, err)
		assert.Equal(t, services.EntrypointModeDedicated, ep.Mode)
		assert.Equal(t, []services.EntrypointPort{}, ep.Ports)
		assert.Equal(t, "", ep.Address)
		assert.Empty(t, gw.calls)
		assert.Zero(t, w.ownerCalls.byStream+w.ownerCalls.byTemplate)
	})
	t.Run("shared: whole template, every stream in sharedWith, any member address", func(t *testing.T) {
		w, _, _, _ := newPortWorld(true)
		gw := &fakeGatewayReader{addrs: map[string]string{"str-fresh": "10.0.0.9"}}
		ep, err := newViewService(w, gw).TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
		require.NoError(t, err)
		assert.Equal(t, services.EntrypointModeShared, ep.Mode)
		assert.Len(t, ep.SharedWith.Streams, 3, "no stream is excluded before one exists")
		assert.Len(t, ep.Ports, 7)
		assert.Equal(t, "10.0.0.9", ep.Address)
	})
	t.Run("missing template is ErrStreamTemplateNotFound", func(t *testing.T) {
		w, _, _, _ := newPortWorld(true)
		svc := services.NewStreamViewService(services.StreamViewServiceDeps{
			Streams: w, Templates: &fakeTemplateReader{err: gorm.ErrRecordNotFound}, Domains: w, Gateways: &fakeGatewayReader{},
		})
		_, err := svc.TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, uuid.New())
		assert.ErrorIs(t, err, services.ErrStreamTemplateNotFound)
	})
	t.Run("other project's template is ErrStreamTemplateWrongProject", func(t *testing.T) {
		w, _, _, _ := newPortWorld(true)
		_, err := newViewService(w, &fakeGatewayReader{}).TemplateEntrypoint(context.Background(), uuid.New(), w.tmpl.ID)
		assert.ErrorIs(t, err, services.ErrStreamTemplateWrongProject)
	})
	t.Run("other template lookup failure is an error", func(t *testing.T) {
		w, _, _, _ := newPortWorld(true)
		svc := services.NewStreamViewService(services.StreamViewServiceDeps{
			Streams: w, Templates: &fakeTemplateReader{err: errors.New("db down")}, Domains: w, Gateways: &fakeGatewayReader{},
		})
		_, err := svc.TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
		assert.ErrorContains(t, err, "db down")
		assert.NotErrorIs(t, err, services.ErrStreamTemplateNotFound)
	})
}

// listedOrOutside is the entry point's answer for (transport, port): taken by
// a route/domain, reserved, or outside the template's range.
func listedOrOutside(ep *services.StreamEntrypoint, transport string, port int) bool {
	if ep.PortRange == nil || port < ep.PortRange.Min || port > ep.PortRange.Max {
		return true
	}
	for _, p := range ep.Ports {
		if p.Transport == transport && p.Port == port {
			return true
		}
	}
	for _, r := range ep.Reserved {
		if r.Transport == transport && r.Port == port {
			return true
		}
	}
	return false
}

// TestStreamEntrypoint_AgreesWithCheckPortCollision is the spec's agreement
// invariant: for every port in [min-1, max+1] and both transports, the
// entry point lists the port (or it is outside portRange) if and only if
// CheckPortCollision rejects it for that stream. The template variant is
// checked against a stream with no routes ("fresh"), which is exactly the
// port space a new stream starts with.
func TestStreamEntrypoint_AgreesWithCheckPortCollision(t *testing.T) {
	for _, merge := range []bool{false, true} {
		w, self, _, fresh := newPortWorld(merge)
		views := newViewService(w, &fakeGatewayReader{})
		collisions := newCollisionService(t, w)

		streamEP, err := views.StreamEntrypoint(context.Background(), self)
		require.NoError(t, err)
		templateEP, err := views.TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
		require.NoError(t, err)

		for _, check := range []struct {
			name     string
			ep       *services.StreamEntrypoint
			streamID uuid.UUID
		}{
			{"stream variant", streamEP, self.ID},
			{"template variant", templateEP, fresh.ID},
		} {
			lo, hi := check.ep.PortRange.Min, check.ep.PortRange.Max
			var mismatches []string
			for _, transport := range []string{"TCP", "UDP"} {
				for port := lo - 1; port <= hi+1; port++ {
					rejected := collisions.CheckPortCollision(check.streamID, transport, port, nil) != nil
					if listed := listedOrOutside(check.ep, transport, port); listed != rejected {
						mismatches = append(mismatches, transport+"/"+strconv.Itoa(port))
						if len(mismatches) > 10 {
							break
						}
					}
				}
			}
			assert.Emptyf(t, mismatches, "merge=%v %s: entry point and CheckPortCollision disagree on %v", merge, check.name, mismatches)
		}
	}
}

func TestStreamEntrypoint_JSONContract(t *testing.T) {
	streamID, routeID, domainID, tmplID := uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		uuid.MustParse("22222222-2222-2222-2222-222222222222"), uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		uuid.MustParse("44444444-4444-4444-4444-444444444444")
	ep := services.StreamEntrypoint{
		Mode: "shared", TemplateID: tmplID, TemplateName: "shared-envoy", GatewayClass: "shared-envoy-clusterip",
		Address:   "10.0.0.5",
		PortRange: &services.EntrypointPortRange{Min: 9000, Max: 9100},
		SharedWith: services.EntrypointSharedWith{
			Streams: []services.EntrypointStreamRef{{ID: streamID, Name: "cache"}},
			Domains: []services.EntrypointDomainRef{{ID: domainID, Name: "api", Hostname: "api.example.com"}},
		},
		Ports: []services.EntrypointPort{
			{Transport: "TCP", Port: 5432, Owner: services.EntrypointPortOwner{Kind: "stream_route", StreamID: &streamID, StreamName: "test-stream", RouteID: &routeID, RouteName: "postgres", Status: "active"}},
			{Transport: "TCP", Port: 443, Owner: services.EntrypointPortOwner{Kind: "domain", DomainID: &domainID, DomainName: "api"}},
		},
		Reserved: []services.EntrypointReservedPort{{Transport: "TCP", Port: 19000, Reason: "envoy_internal"}},
	}
	raw, err := json.Marshal(ep)
	require.NoError(t, err)
	assert.JSONEq(t, `{
	  "mode": "shared",
	  "templateId": "44444444-4444-4444-4444-444444444444",
	  "templateName": "shared-envoy",
	  "gatewayClass": "shared-envoy-clusterip",
	  "address": "10.0.0.5",
	  "portRange": {"min": 9000, "max": 9100},
	  "sharedWith": {
	    "streams": [{"id": "11111111-1111-1111-1111-111111111111", "name": "cache"}],
	    "domains": [{"id": "33333333-3333-3333-3333-333333333333", "name": "api", "hostname": "api.example.com"}]
	  },
	  "ports": [
	    {"transport": "TCP", "port": 5432, "owner": {"kind": "stream_route", "streamId": "11111111-1111-1111-1111-111111111111",
	      "streamName": "test-stream", "routeId": "22222222-2222-2222-2222-222222222222", "routeName": "postgres", "status": "active"}},
	    {"transport": "TCP", "port": 443, "owner": {"kind": "domain", "domainId": "33333333-3333-3333-3333-333333333333", "domainName": "api"}}
	  ],
	  "reserved": [{"transport": "TCP", "port": 19000, "reason": "envoy_internal"}]
	}`, string(raw))
}

func TestStreamEntrypoint_ReservedTemplateListenerPortListedOnce(t *testing.T) {
	w, self, _, _ := newPortWorld(true)
	w.tmpl.Listeners = models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 19443},
		{Name: "tls", Protocol: models.ListenerTLS, Port: 19443},
		{Name: "stream", Protocol: models.ListenerTCP, PortRangeMin: 18990, PortRangeMax: 60010},
	}

	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	n := 0
	for _, r := range ep.Reserved {
		if r.Port == 19443 {
			n++
		}
	}
	assert.Equal(t, 1, n, "two hostname listeners on one port reserve it once")
}

func TestStreamEntrypoint_Dedicated_GatewayNotDeployedYet(t *testing.T) {
	w, self, _, _ := newPortWorld(false)

	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err, "a stream whose Gateway does not exist yet is not an error")
	assert.Equal(t, "", ep.Address, "shown as assigned on first deploy")
	assert.Len(t, ep.Ports, 2)
}

// countingTemplates is a StreamTemplateReader that counts reads per template.
type countingTemplates struct {
	byID  map[uuid.UUID]*models.DomainTemplate
	calls map[uuid.UUID]int
}

func (c *countingTemplates) GetByID(id uuid.UUID) (*models.DomainTemplate, error) {
	c.calls[id]++
	if t, ok := c.byID[id]; ok {
		return t, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func TestStreamListWithSummary(t *testing.T) {
	w, self, cache, fresh := newPortWorld(true)
	dedicated := &models.DomainTemplate{ID: uuid.New(), ProjectID: w.tmpl.ProjectID, Name: "edge-l4"}
	solo := models.Stream{ID: uuid.New(), ProjectID: w.tmpl.ProjectID, Name: "solo", GatewayTemplateID: dedicated.ID}
	w.streams = append(w.streams, solo)
	w.routes = append(w.routes, worldRoute{id: uuid.New(), streamID: solo.ID, name: "mqtt", transport: "TCP", port: 1883, status: models.RouteStatusActive})
	templates := &countingTemplates{byID: map[uuid.UUID]*models.DomainTemplate{w.tmpl.ID: w.tmpl, dedicated.ID: dedicated}, calls: map[uuid.UUID]int{}}
	svc := services.NewStreamViewService(services.StreamViewServiceDeps{Streams: w, Templates: templates, Domains: w, Gateways: &fakeGatewayReader{}})

	items, err := svc.ListWithSummary(w.tmpl.ProjectID)
	require.NoError(t, err)

	require.Len(t, items, 4)
	byName := map[string]services.StreamListItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	assert.Equal(t, []string{"cache", "fresh", "solo", "test-stream"}, []string{items[0].Name, items[1].Name, items[2].Name, items[3].Name})

	mine := byName["test-stream"]
	assert.Equal(t, self.ID, mine.ID)
	assert.Equal(t, services.EntrypointModeShared, mine.Mode)
	assert.Equal(t, "shared-envoy", mine.GatewayTemplateName)
	assert.Equal(t, 2, mine.RouteCount, "the rejected route is not counted")
	assert.Equal(t, []services.StreamPort{{Transport: "TCP", Port: 19100}, {Transport: "UDP", Port: 19100}}, mine.Ports)

	assert.Equal(t, cache.ID, byName["cache"].ID)
	assert.Equal(t, 2, byName["cache"].RouteCount)
	assert.Equal(t, fresh.ID, byName["fresh"].ID)
	assert.Equal(t, 0, byName["fresh"].RouteCount)
	assert.Equal(t, []services.StreamPort{}, byName["fresh"].Ports)
	assert.Equal(t, services.EntrypointModeDedicated, byName["solo"].Mode)
	assert.Equal(t, "edge-l4", byName["solo"].GatewayTemplateName)

	// No N+1: one grouped route query, one read per distinct template.
	assert.Equal(t, 1, w.ownerCalls.byStreamIDs)
	assert.Zero(t, w.ownerCalls.byStream)
	assert.Zero(t, w.ownerCalls.byTemplate)
	assert.Equal(t, map[uuid.UUID]int{w.tmpl.ID: 1, dedicated.ID: 1}, templates.calls)
}

func TestStreamListWithSummary_EmptyProject(t *testing.T) {
	w, _, _, _ := newPortWorld(false)
	items, err := newViewService(w, &fakeGatewayReader{}).ListWithSummary(uuid.New())
	require.NoError(t, err)
	assert.Equal(t, []services.StreamListItem{}, items)
	assert.Zero(t, w.ownerCalls.byStreamIDs, "no streams, no route query")
}

func TestStreamListWithSummary_TemplateFailureIsAnError(t *testing.T) {
	w, _, _, _ := newPortWorld(false)
	svc := services.NewStreamViewService(services.StreamViewServiceDeps{
		Streams: w, Templates: &fakeTemplateReader{err: errors.New("db down")}, Domains: w, Gateways: &fakeGatewayReader{},
	})
	items, err := svc.ListWithSummary(w.tmpl.ProjectID)
	assert.Nil(t, items, "never a list with guessed modes")
	assert.ErrorContains(t, err, "db down")
}

func TestStreamListItem_JSONContract(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	raw, err := json.Marshal(services.StreamListItem{
		Stream: models.Stream{ID: id, Name: "test-stream", Status: "active"},
		Mode:   "shared", GatewayTemplateName: "shared-envoy", RouteCount: 1,
		Ports: []services.StreamPort{{Transport: "TCP", Port: 5432}},
	})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", got["id"])
	assert.Equal(t, "test-stream", got["name"])
	assert.Equal(t, "active", got["status"])
	assert.Equal(t, "shared", got["mode"])
	assert.Equal(t, "shared-envoy", got["gatewayTemplateName"])
	assert.Equal(t, float64(1), got["routeCount"])
	assert.Equal(t, []any{map[string]any{"transport": "TCP", "port": float64(5432)}}, got["ports"])
}

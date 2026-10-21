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

// portWorld is one in-memory fixture that answers BOTH the claim/used-port
// sources (StreamStore, StreamPortStore, DomainPortReader) and the owner-aware
// sources the entry point reads (StreamViewStore, DomainPortOwnerReader).
// Every answer is derived from the same rows with the repository's rule
// (rejected routes hold no port). The repository tests pin that the real SQL
// of the two query families agrees.
type portWorld struct {
	tmpl    *models.DomainTemplate
	streams []models.Stream
	routes  []worldRoute
	domains []models.Domain
	// pending are the pending-approval proposals PendingL4PortOwnersByStream
	// reports, filtered by stream.
	pending []services.L4PortOwner

	ownerCalls struct{ byStream, pendingByStream, byStreamIDs int }
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
func (w *portWorld) ListAll() ([]models.Stream, error) {
	out := append([]models.Stream(nil), w.streams...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (w *portWorld) ExistsByName(uuid.UUID, string) (bool, error) { return false, nil }
func (w *portWorld) Update(*models.Stream) error                  { return nil }
func (w *portWorld) UpdateStatus(uuid.UUID, string, string) error { return nil }
func (w *portWorld) Delete(uuid.UUID) error                       { return nil }

// --- services.StreamPortStore / DomainPortReader ---

func (w *portWorld) UsedPortsByStream(streamID uuid.UUID, exclude *uuid.UUID) ([]services.PortUse, error) {
	var out []services.PortUse
	for _, r := range w.liveRoutes(func(r worldRoute) bool { return r.streamID == streamID && (exclude == nil || r.id != *exclude) }) {
		out = append(out, services.PortUse{Transport: r.transport, Port: r.port})
	}
	return out, nil
}
func (w *portWorld) ClaimedPortsByTemplate(_ uuid.UUID, exclude *uuid.UUID) ([]services.PortUse, error) {
	var out []services.PortUse
	for _, s := range w.streams {
		if s.GatewayTemplateID != w.tmpl.ID || (exclude != nil && s.ID == *exclude) {
			continue
		}
		for _, p := range s.Ports {
			out = append(out, services.PortUse{Transport: p.Transport, Port: p.Port})
		}
	}
	return out, nil
}
func (w *portWorld) PendingL4PortOwnersByStream(streamID uuid.UUID) ([]services.L4PortOwner, error) {
	w.ownerCalls.pendingByStream++
	out := []services.L4PortOwner{}
	for _, o := range w.pending {
		if o.StreamID == streamID {
			o.Pending = true
			out = append(out, o)
		}
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
// covers the reserved 19000 and 19001; three streams ("test-stream" and
// "cache" claim ports and own routes on them, "fresh" claims none) and two
// domains.
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
	self.Ports = models.StreamPorts{{Transport: "TCP", Port: 19100}, {Transport: "UDP", Port: 19100}}
	cache.Ports = models.StreamPorts{{Transport: "TCP", Port: 19102}, {Transport: "UDP", Port: 19103}}
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
	assert.Equal(t, []string{"TCP/19100 stream", "UDP/19100 stream"}, portKeys(ep),
		"dedicated: only this stream's claims")
	assert.Equal(t, []services.EntrypointReservedPort{
		{Transport: "TCP", Port: 19000, Reason: "envoy_internal"}, {Transport: "UDP", Port: 19000, Reason: "envoy_internal"},
		{Transport: "TCP", Port: 19001, Reason: "envoy_internal"}, {Transport: "UDP", Port: 19001, Reason: "envoy_internal"},
	}, ep.Reserved, "dedicated: no template_listener reservations")
	assert.Equal(t, []string{"str-test-stream"}, gw.calls, "dedicated reads only its own Gateway")
}

func TestStreamEntrypoint_ReservedHasNoPlaceholder(t *testing.T) {
	w, self, _, _ := newPortWorld(false)
	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	got := map[string][]int{}
	for _, r := range ep.Reserved {
		assert.NotEqual(t, 60000, r.Port, "60000 is a normal port now")
		assert.NotEqual(t, "placeholder", r.Reason)
		got[r.Transport] = append(got[r.Transport], r.Port)
	}
	assert.Equal(t, map[string][]int{"TCP": {19000, 19001}, "UDP": {19000, 19001}}, got)
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
		"TCP/19100 stream", "UDP/19100 stream",
		"TCP/19102 stream", "UDP/19103 stream",
		"TCP/19443 domain", "TCP/19443 domain",
	}, portKeys(ep), "shared: every stream's claims plus every domain's bound listener ports")
	assert.Contains(t, ep.Reserved, services.EntrypointReservedPort{Transport: "TCP", Port: 80, Reason: "template_listener"})
	assert.Contains(t, ep.Reserved, services.EntrypointReservedPort{Transport: "TCP", Port: 19443, Reason: "template_listener"})
	assert.NotContains(t, ep.Reserved, services.EntrypointReservedPort{Transport: "UDP", Port: 19443, Reason: "template_listener"},
		"template listeners are reserved for TCP only, as the claim validation does")

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
		assert.Zero(t, w.ownerCalls.byStream+w.ownerCalls.pendingByStream)
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

func ownerOf(t *testing.T, ep *services.StreamEntrypoint, transport string, port int) services.EntrypointPortOwner {
	t.Helper()
	for _, p := range ep.Ports {
		if p.Transport == transport && p.Port == port && p.Owner.Kind == services.PortOwnerStream {
			return p.Owner
		}
	}
	t.Fatalf("no stream owner listed for %s/%d", transport, port)
	return services.EntrypointPortOwner{}
}

func TestStreamEntrypoint_Dedicated_ListsClaimsWithRoutes(t *testing.T) {
	w, self, _, _ := newPortWorld(false)
	self.Ports = models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 5433}, {Transport: "UDP", Port: 53}}
	pgID, cacheID := uuid.New(), uuid.New()
	w.routes = []worldRoute{{id: pgID, streamID: self.ID, name: "pg", transport: "TCP", port: 5432, status: models.RouteStatusActive}}
	w.pending = []services.L4PortOwner{{Transport: "TCP", Port: 5433, StreamID: self.ID, StreamName: self.Name,
		RouteID: cacheID, RouteName: "cache", Status: string(models.RouteStatusPendingUpdate)}}

	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	assert.Equal(t, []string{"UDP/53 stream", "TCP/5432 stream", "TCP/5433 stream"}, portKeys(ep),
		"every claim is listed, route or not")

	pg := ownerOf(t, ep, "TCP", 5432)
	require.NotNil(t, pg.StreamID)
	assert.Equal(t, self.ID, *pg.StreamID)
	assert.Equal(t, self.Name, pg.StreamName)
	require.NotNil(t, pg.RouteID)
	assert.Equal(t, pgID, *pg.RouteID)
	assert.Equal(t, "pg", pg.RouteName)
	assert.Equal(t, "active", pg.Status)
	assert.False(t, pg.Pending)

	cache := ownerOf(t, ep, "TCP", 5433)
	require.NotNil(t, cache.RouteID)
	assert.Equal(t, cacheID, *cache.RouteID)
	assert.Equal(t, "cache", cache.RouteName)
	assert.True(t, cache.Pending, "an approval proposes this port")

	dns := ownerOf(t, ep, "UDP", 53)
	assert.Equal(t, self.Name, dns.StreamName)
	assert.Nil(t, dns.RouteID)
	assert.Empty(t, dns.RouteName)
	assert.Empty(t, dns.Status)
	assert.False(t, dns.Pending)
}

func TestStreamEntrypoint_Dedicated_LiveRouteWinsOverPendingProposal(t *testing.T) {
	w, self, _, _ := newPortWorld(false)
	self.Ports = models.StreamPorts{{Transport: "TCP", Port: 5432}}
	w.routes = []worldRoute{{id: uuid.New(), streamID: self.ID, name: "pg", transport: "TCP", port: 5432, status: models.RouteStatusActive}}
	w.pending = []services.L4PortOwner{{Transport: "TCP", Port: 5432, StreamID: self.ID, StreamName: self.Name,
		RouteID: uuid.New(), RouteName: "other", Status: "active"}}

	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	require.Len(t, ep.Ports, 1, "one claim, one entry")
	assert.Equal(t, "pg", ep.Ports[0].Owner.RouteName)
	assert.False(t, ep.Ports[0].Owner.Pending)
}

func TestStreamEntrypoint_Shared_ListsOtherStreamsClaimsAndDomains(t *testing.T) {
	w, self, cache, _ := newPortWorld(true)

	ep, err := newViewService(w, &fakeGatewayReader{}).StreamEntrypoint(context.Background(), self)
	require.NoError(t, err)

	// The other stream's claim is named, but carries no route detail (its
	// redis route exists in the fixture and must not leak).
	other := ownerOf(t, ep, "TCP", 19102)
	require.NotNil(t, other.StreamID)
	assert.Equal(t, cache.ID, *other.StreamID)
	assert.Equal(t, "cache", other.StreamName)
	assert.Nil(t, other.RouteID)
	assert.Empty(t, other.RouteName)
	assert.Empty(t, other.Status)
	assert.False(t, other.Pending)
	assert.Empty(t, ownerOf(t, ep, "UDP", 19103).RouteName)

	// The viewed stream's own claims carry their route.
	assert.Equal(t, "postgres", ownerOf(t, ep, "TCP", 19100).RouteName)

	var domains []string
	for _, p := range ep.Ports {
		if p.Owner.Kind == services.PortOwnerDomain {
			require.NotNil(t, p.Owner.DomainID)
			assert.Nil(t, p.Owner.StreamID)
			domains = append(domains, p.Transport+"/"+strconv.Itoa(p.Port)+" "+p.Owner.DomainName)
		}
	}
	assert.Equal(t, []string{"TCP/80 api", "TCP/19443 api", "TCP/19443 web"}, domains)
	assert.Zero(t, w.ownerCalls.byStreamIDs)
}

func TestTemplateEntrypoint_Shared_ListsAllClaims(t *testing.T) {
	w, _, _, _ := newPortWorld(true)

	ep, err := newViewService(w, &fakeGatewayReader{}).TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"TCP/80 domain",
		"TCP/19100 stream", "UDP/19100 stream",
		"TCP/19102 stream", "UDP/19103 stream",
		"TCP/19443 domain", "TCP/19443 domain",
	}, portKeys(ep), "every stream's claims plus every domain's ports")
	for _, p := range ep.Ports {
		assert.Nil(t, p.Owner.RouteID, "no stream is being viewed, so no route detail")
		assert.False(t, p.Owner.Pending)
	}
	assert.Equal(t, "test-stream", ownerOf(t, ep, "TCP", 19100).StreamName)
	assert.Equal(t, "cache", ownerOf(t, ep, "TCP", 19102).StreamName)
	assert.Zero(t, w.ownerCalls.byStream+w.ownerCalls.pendingByStream, "route owners are only read for a viewed stream")
}

func newClaimsService(t *testing.T, w *portWorld) *services.StreamService {
	t.Helper()
	svc := services.NewStreamService(w, &fakeTemplateReader{tmpl: w.tmpl}, &fakeRouteCounter{}, mocks.NewMockGatewayApplier(t), allowAllNamespaces{})
	svc.SetPortSources(w, w)
	return svc
}

// TestTemplateEntrypoint_AgreesWithClaimValidation: what the template entry
// point lists as taken is what claim validation refuses. For every port in
// [min-1, max+1] and both transports on a shared template, a new stream
// (selfID nil) may claim the port if and only if it is in range and neither
// listed in ports nor reserved - and each refusal carries the matching error.
func TestTemplateEntrypoint_AgreesWithClaimValidation(t *testing.T) {
	w, _, _, _ := newPortWorld(true)
	ep, err := newViewService(w, &fakeGatewayReader{}).TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
	require.NoError(t, err)
	require.NotNil(t, ep.PortRange)
	svc := newClaimsService(t, w)

	listed := func(transport string, port int) bool {
		for _, p := range ep.Ports {
			if p.Transport == transport && p.Port == port {
				return true
			}
		}
		return false
	}
	reserved := func(transport string, port int) bool {
		for _, r := range ep.Reserved {
			if r.Transport == transport && r.Port == port {
				return true
			}
		}
		return false
	}

	sawStream, sawDomain, sawReserved, sawFree := false, false, false, false
	var mismatches []string
	for _, transport := range []string{"TCP", "UDP"} {
		for port := ep.PortRange.Min - 1; port <= ep.PortRange.Max+1; port++ {
			_, err := services.ValidateStreamPortsForTest(svc, w.tmpl, nil, models.StreamPorts{{Transport: transport, Port: port}})
			key := transport + "/" + strconv.Itoa(port)
			switch {
			case port < ep.PortRange.Min || port > ep.PortRange.Max:
				if !errors.Is(err, services.ErrPortOutOfRange) {
					mismatches = append(mismatches, key+" outside the range must be ErrPortOutOfRange")
				}
			case listed(transport, port):
				if !errors.Is(err, services.ErrPortCollision) {
					mismatches = append(mismatches, key+" is listed as taken but was not a collision: "+errString(err))
				}
				for _, p := range ep.Ports {
					if p.Transport == transport && p.Port == port {
						sawStream = sawStream || p.Owner.Kind == services.PortOwnerStream
						sawDomain = sawDomain || p.Owner.Kind == services.PortOwnerDomain
					}
				}
			case reserved(transport, port):
				sawReserved = true
				if !errors.Is(err, services.ErrReservedPort) {
					mismatches = append(mismatches, key+" is listed as reserved but was not ErrReservedPort: "+errString(err))
				}
			default:
				sawFree = true
				if err != nil {
					mismatches = append(mismatches, key+" is free but was refused: "+errString(err))
				}
			}
			if len(mismatches) > 10 {
				break
			}
		}
	}
	assert.Empty(t, mismatches)
	assert.True(t, sawStream && sawDomain && sawReserved && sawFree, "the fixture must exercise a stream claim, a domain port, a reserved port and a free port")
}

func errString(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
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
			{Transport: "TCP", Port: 5432, Owner: services.EntrypointPortOwner{Kind: "stream", StreamID: &streamID, StreamName: "test-stream", RouteID: &routeID, RouteName: "postgres", Status: "active"}},
			{Transport: "TCP", Port: 5433, Owner: services.EntrypointPortOwner{Kind: "stream", StreamID: &streamID, StreamName: "test-stream", RouteID: &routeID, RouteName: "cache", Status: "active", Pending: true}},
			{Transport: "UDP", Port: 53, Owner: services.EntrypointPortOwner{Kind: "stream", StreamID: &streamID, StreamName: "test-stream"}},
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
	    {"transport": "TCP", "port": 5432, "owner": {"kind": "stream", "streamId": "11111111-1111-1111-1111-111111111111",
	      "streamName": "test-stream", "routeId": "22222222-2222-2222-2222-222222222222", "routeName": "postgres", "status": "active"}},
	    {"transport": "TCP", "port": 5433, "owner": {"kind": "stream", "streamId": "11111111-1111-1111-1111-111111111111",
	      "streamName": "test-stream", "routeId": "22222222-2222-2222-2222-222222222222", "routeName": "cache", "status": "active", "pending": true}},
	    {"transport": "UDP", "port": 53, "owner": {"kind": "stream", "streamId": "11111111-1111-1111-1111-111111111111", "streamName": "test-stream"}},
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
	assert.Equal(t, models.StreamPorts{{Transport: "TCP", Port: 19100}, {Transport: "UDP", Port: 19100}}, mine.Ports)

	assert.Equal(t, cache.ID, byName["cache"].ID)
	assert.Equal(t, 2, byName["cache"].RouteCount)
	assert.Equal(t, fresh.ID, byName["fresh"].ID)
	assert.Equal(t, 0, byName["fresh"].RouteCount)
	assert.Equal(t, models.StreamPorts{}, byName["fresh"].Ports)
	assert.Equal(t, services.EntrypointModeDedicated, byName["solo"].Mode)
	assert.Equal(t, "edge-l4", byName["solo"].GatewayTemplateName)

	// No N+1: one grouped route query, one read per distinct template.
	assert.Equal(t, 1, w.ownerCalls.byStreamIDs)
	assert.Zero(t, w.ownerCalls.byStream)
	assert.Zero(t, w.ownerCalls.pendingByStream)
	assert.Equal(t, map[uuid.UUID]int{w.tmpl.ID: 1, dedicated.ID: 1}, templates.calls)
}

func TestListWithSummary_PortsAreClaims(t *testing.T) {
	w, _, _, _ := newPortWorld(false)
	// Claims given out of order, in mixed case, on a stream with no routes at
	// all: the list reports the claims, normalized, and routeCount is the
	// route count.
	claimsOnly := models.Stream{ID: uuid.New(), ProjectID: w.tmpl.ProjectID, Name: "claims-only", GatewayTemplateID: w.tmpl.ID,
		Ports: models.StreamPorts{{Transport: "UDP", Port: 53}, {Transport: "tcp", Port: 9000}, {Transport: "TCP", Port: 5432}}}
	w.streams = append(w.streams, claimsOnly)

	items, err := newViewService(w, &fakeGatewayReader{}).ListWithSummary(w.tmpl.ProjectID)
	require.NoError(t, err)

	byName := map[string]services.StreamListItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	got := byName["claims-only"]
	assert.Equal(t, models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 9000}, {Transport: "UDP", Port: 53}}, got.Ports,
		"claims, TCP before UDP, then by port")
	assert.Equal(t, 0, got.RouteCount, "routeCount counts routes, not claims")
	assert.Equal(t, 2, byName["test-stream"].RouteCount)

	// A stream whose claim column is NULL/absent still lists [] and not null.
	byName2 := byName["fresh"]
	raw, err := json.Marshal(byName2)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"ports":[]`)
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
		Stream: models.Stream{ID: id, Name: "test-stream", Status: "active", Ports: models.StreamPorts{{Transport: "TCP", Port: 5432}}},
		Mode:   "shared", GatewayTemplateName: "shared-envoy", RouteCount: 1,
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

// Every Gateway on a merged template reports the one shared Service address,
// domains' Gateways included. So a shared template that already serves
// domains but has no stream (or whose streams have no address yet) still has
// a known IP; the create panel must not say "assigned on first deploy".
// Domains not deployed yet (no Gateway name) are skipped.
func TestStreamEntrypoint_SharedAddressFallsBackToDomainGateways(t *testing.T) {
	t.Run("template variant, no streams yet", func(t *testing.T) {
		w, _, _, _ := newPortWorld(true)
		w.streams = nil
		w.domains[0].K8sGatewayName, w.domains[0].Namespace = "gw-api", "edge"
		w.domains[1].K8sGatewayName, w.domains[1].Namespace = "gw-web", "edge"
		gw := &fakeGatewayReader{addrs: map[string]string{"gw-web": "10.0.0.7"}}

		ep, err := newViewService(w, gw).TemplateEntrypoint(context.Background(), w.tmpl.ProjectID, w.tmpl.ID)
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.7", ep.Address)
		assert.Equal(t, []string{"gw-api", "gw-web"}, gw.calls)
	})
	t.Run("stream variant, no member stream has an address", func(t *testing.T) {
		w, self, _, _ := newPortWorld(true)
		w.domains[1].K8sGatewayName, w.domains[1].Namespace = "gw-web", "edge"
		gw := &fakeGatewayReader{addrs: map[string]string{"gw-web": "10.0.0.7"}}

		ep, err := newViewService(w, gw).StreamEntrypoint(context.Background(), self)
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.7", ep.Address)
		assert.Equal(t, []string{"str-test-stream", "str-cache", "str-fresh", "gw-web"}, gw.calls,
			"streams first; the undeployed domain (no Gateway name) is skipped")
	})
}

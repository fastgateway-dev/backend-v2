package services_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeStreamStore struct {
	streams map[uuid.UUID]*models.Stream
	created []*models.Stream
	deleted []uuid.UUID
}

// streamRangeListeners is the listener set of a stream-capable template: a
// single TCP/UDP port range. httpsListeners (a domain-only template) lacks it.
var streamRangeListeners = models.Listeners{{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535}}

func newFakeStreamStore() *fakeStreamStore {
	return &fakeStreamStore{streams: map[uuid.UUID]*models.Stream{}}
}

func (f *fakeStreamStore) Create(s *models.Stream) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	f.streams[s.ID] = s
	f.created = append(f.created, s)
	return nil
}

func (f *fakeStreamStore) GetByID(id uuid.UUID) (*models.Stream, error) {
	s, ok := f.streams[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakeStreamStore) ListByProjectID(projectID uuid.UUID) ([]models.Stream, error) {
	var out []models.Stream
	for _, s := range f.streams {
		if s.ProjectID == projectID {
			out = append(out, *s)
		}
	}
	return out, nil
}

func (f *fakeStreamStore) ExistsByName(projectID uuid.UUID, name string) (bool, error) {
	for _, s := range f.streams {
		if s.ProjectID == projectID && s.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStreamStore) Update(s *models.Stream) error {
	f.streams[s.ID] = s
	return nil
}

func (f *fakeStreamStore) Delete(id uuid.UUID) error {
	f.deleted = append(f.deleted, id)
	delete(f.streams, id)
	return nil
}

type fakeTemplateReader struct {
	tmpl *models.DomainTemplate
	err  error
}

func (f *fakeTemplateReader) GetByID(uuid.UUID) (*models.DomainTemplate, error) {
	return f.tmpl, f.err
}

type fakeRouteCounter struct {
	n   int64
	err error
}

func (f *fakeRouteCounter) CountByStreamID(uuid.UUID) (int64, error) { return f.n, f.err }

// allowAllNamespaces whitelists every namespace with the deploy_gateway capability.
type allowAllNamespaces struct{}

func (allowAllNamespaces) GetByProjectAndNamespace(projectID uuid.UUID, ns string) (*models.ProjectNamespace, error) {
	return &models.ProjectNamespace{ProjectID: projectID, Namespace: ns, Capabilities: []string{models.NamespaceCapabilityDeployGateway}}, nil
}

// fakeNamespaces returns a fixed namespace/error.
type fakeNamespaces struct {
	ns  *models.ProjectNamespace
	err error
}

func (f fakeNamespaces) GetByProjectAndNamespace(uuid.UUID, string) (*models.ProjectNamespace, error) {
	return f.ns, f.err
}

func TestStreamGatewayName_KindPrefixed(t *testing.T) {
	assert.Equal(t, "str-foo", services.StreamGatewayName("foo"))
	assert.Equal(t, "str-my-gw", services.StreamGatewayName("My_GW"))
	assert.Equal(t, "str-a-b", services.StreamGatewayName("A B"))
	// Domain gateway names are derived from hostnames (no "str-" kind prefix
	// unless the hostname itself starts with it), so a Stream named "foo"
	// never collides with a Domain whose gateway is "foo".
	assert.NotEqual(t, "foo", services.StreamGatewayName("foo"))
	// Always a valid DNS label (<= 63 chars).
	long := services.StreamGatewayName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	assert.LessOrEqual(t, len(long), 63)
	assert.NotEqual(t, '-', rune(long[len(long)-1]))
}

// defaultClaims is the claim set most Create tests submit.
var defaultClaims = models.StreamPorts{{Transport: "TCP", Port: 5432}}

func newStreamSvc(t *testing.T, store *fakeStreamStore, tmpl *fakeTemplateReader, routes *fakeRouteCounter) *services.StreamService {
	applier := mocks.NewMockGatewayApplier(t)
	applier.EXPECT().UpdateGateway(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	applier.EXPECT().DeleteGateway(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	return services.NewStreamService(store, tmpl, routes, applier, allowAllNamespaces{})
}

func TestStreamService_Create_RejectsNonStreamTemplate(t *testing.T) {
	store := newFakeStreamStore()
	projectID := uuid.New()
	tmpl := &fakeTemplateReader{tmpl: &models.DomainTemplate{ProjectID: projectID, Listeners: httpsListeners, K8sGatewayClassName: "gc"}}
	svc := newStreamSvc(t, store, tmpl, &fakeRouteCounter{})

	_, err := svc.Create(projectID, services.CreateStreamInput{Name: "db", Namespace: "ns", GatewayTemplateID: uuid.New(), Ports: defaultClaims}, &models.User{ID: uuid.New()})
	assert.ErrorIs(t, err, services.ErrTemplateNotStreamEnabled)
	assert.Empty(t, store.created)
}

func TestStreamService_Create_CopiesGatewayClassAndNames(t *testing.T) {
	store := newFakeStreamStore()
	tmplID := uuid.New()
	projectID := uuid.New()
	tmpl := &fakeTemplateReader{tmpl: &models.DomainTemplate{ID: tmplID, ProjectID: projectID, Listeners: streamRangeListeners, K8sGatewayClassName: "public-lb"}}
	svc := newStreamSvc(t, store, tmpl, &fakeRouteCounter{})
	user := &models.User{ID: uuid.New()}

	got, err := svc.Create(projectID, services.CreateStreamInput{Name: "DB", Namespace: "ns", GatewayTemplateID: tmplID, Ports: defaultClaims}, user)
	require.NoError(t, err)
	assert.Equal(t, projectID, got.ProjectID)
	assert.Equal(t, "DB", got.Name)
	assert.Equal(t, "ns", got.Namespace)
	assert.Equal(t, tmplID, got.GatewayTemplateID)
	assert.Equal(t, "public-lb", got.K8sGatewayClass)
	assert.Equal(t, "str-db", got.K8sGatewayName)
	require.NotNil(t, got.CreatedBy)
	assert.Equal(t, user.ID, *got.CreatedBy)
	assert.Len(t, store.created, 1)
}

func TestStreamService_Create_TemplateNotFound(t *testing.T) {
	svc := newStreamSvc(t, newFakeStreamStore(), &fakeTemplateReader{err: gorm.ErrRecordNotFound}, &fakeRouteCounter{})
	_, err := svc.Create(uuid.New(), services.CreateStreamInput{Name: "db", GatewayTemplateID: uuid.New(), Ports: defaultClaims}, nil)
	assert.Error(t, err)
}

func TestStreamService_Create_RejectsTemplateFromOtherProject(t *testing.T) {
	store := newFakeStreamStore()
	tmpl := &fakeTemplateReader{tmpl: &models.DomainTemplate{ProjectID: uuid.New(), Listeners: streamRangeListeners}}
	svc := newStreamSvc(t, store, tmpl, &fakeRouteCounter{})
	_, err := svc.Create(uuid.New(), services.CreateStreamInput{Name: "db", GatewayTemplateID: uuid.New(), Ports: defaultClaims}, nil)
	assert.Error(t, err)
	assert.Empty(t, store.created)
}

func TestStreamService_Update_NameOnly_TemplateImmutable(t *testing.T) {
	store := newFakeStreamStore()
	id, tmplID := uuid.New(), uuid.New()
	store.streams[id] = &models.Stream{ID: id, Name: "old", GatewayTemplateID: tmplID, K8sGatewayName: "str-old", K8sGatewayClass: "gc"}
	svc := newStreamSvc(t, store, &fakeTemplateReader{}, &fakeRouteCounter{})

	newName := "new"
	got, err := svc.Update(id, services.UpdateStreamInput{Name: &newName})
	require.NoError(t, err)
	assert.Equal(t, "new", got.Name)
	assert.Equal(t, tmplID, got.GatewayTemplateID)
	assert.Equal(t, "gc", got.K8sGatewayClass)
	// The live Gateway name is not renamed: it would orphan the deployed Gateway.
	assert.Equal(t, "str-old", got.K8sGatewayName)
}

func TestStreamService_Update_NotFound(t *testing.T) {
	svc := newStreamSvc(t, newFakeStreamStore(), &fakeTemplateReader{}, &fakeRouteCounter{})
	n := "x"
	_, err := svc.Update(uuid.New(), services.UpdateStreamInput{Name: &n})
	assert.ErrorIs(t, err, services.ErrStreamNotFound)
}

func TestStreamService_Delete_BlockedWithRoutes(t *testing.T) {
	store := newFakeStreamStore()
	id := uuid.New()
	store.streams[id] = &models.Stream{ID: id}
	svc := newStreamSvc(t, store, &fakeTemplateReader{}, &fakeRouteCounter{n: 1})

	err := svc.Delete(id)
	assert.ErrorIs(t, err, services.ErrStreamHasRoutes)
	assert.Empty(t, store.deleted)
}

func TestStreamService_Delete_OK(t *testing.T) {
	store := newFakeStreamStore()
	id := uuid.New()
	store.streams[id] = &models.Stream{ID: id}
	svc := newStreamSvc(t, store, &fakeTemplateReader{}, &fakeRouteCounter{})

	require.NoError(t, svc.Delete(id))
	assert.Equal(t, []uuid.UUID{id}, store.deleted)
}

func TestStreamService_Delete_NotFound(t *testing.T) {
	svc := newStreamSvc(t, newFakeStreamStore(), &fakeTemplateReader{}, &fakeRouteCounter{})
	assert.ErrorIs(t, svc.Delete(uuid.New()), services.ErrStreamNotFound)
}

func TestStreamService_Delete_CountError(t *testing.T) {
	store := newFakeStreamStore()
	id := uuid.New()
	store.streams[id] = &models.Stream{ID: id}
	boom := errors.New("boom")
	svc := newStreamSvc(t, store, &fakeTemplateReader{}, &fakeRouteCounter{err: boom})
	assert.ErrorIs(t, svc.Delete(id), boom)
	assert.Empty(t, store.deleted)
}

func streamEnabledSetup() (*fakeStreamStore, *fakeTemplateReader, uuid.UUID, uuid.UUID) {
	projectID, tmplID := uuid.New(), uuid.New()
	tmpl := &fakeTemplateReader{tmpl: &models.DomainTemplate{ID: tmplID, ProjectID: projectID, Listeners: streamRangeListeners, K8sGatewayClassName: "gc"}}
	return newFakeStreamStore(), tmpl, projectID, tmplID
}

func newStreamSvcWithNS(t *testing.T, store *fakeStreamStore, tmpl *fakeTemplateReader, ns services.StreamNamespaceReader) *services.StreamService {
	applier := mocks.NewMockGatewayApplier(t)
	applier.EXPECT().UpdateGateway(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	return services.NewStreamService(store, tmpl, &fakeRouteCounter{}, applier, ns)
}

func TestStreamService_Create_RejectsUnregisteredNamespace(t *testing.T) {
	store, tmpl, projectID, tmplID := streamEnabledSetup()
	svc := newStreamSvcWithNS(t, store, tmpl, fakeNamespaces{err: gorm.ErrRecordNotFound})
	_, err := svc.Create(projectID, services.CreateStreamInput{Name: "db", Namespace: "other", GatewayTemplateID: tmplID, Ports: defaultClaims}, nil)
	assert.ErrorIs(t, err, services.ErrStreamNamespaceNotRegistered)
	assert.Empty(t, store.created)
}

func TestStreamService_Create_RejectsNamespaceWithoutDeployCapability(t *testing.T) {
	store, tmpl, projectID, tmplID := streamEnabledSetup()
	ns := &models.ProjectNamespace{Capabilities: []string{models.NamespaceCapabilityBackendService}}
	svc := newStreamSvcWithNS(t, store, tmpl, fakeNamespaces{ns: ns})
	_, err := svc.Create(projectID, services.CreateStreamInput{Name: "db", Namespace: "other", GatewayTemplateID: tmplID, Ports: defaultClaims}, nil)
	assert.ErrorIs(t, err, services.ErrStreamNamespaceNotDeployable)
	assert.Empty(t, store.created)
}

func TestStreamService_Create_DefaultNamespaceNeedsNoRegistration(t *testing.T) {
	store, tmpl, projectID, tmplID := streamEnabledSetup()
	svc := newStreamSvcWithNS(t, store, tmpl, fakeNamespaces{err: gorm.ErrRecordNotFound})
	_, err := svc.Create(projectID, services.CreateStreamInput{Name: "db", Namespace: kubernetes.FastGatewayNamespace, GatewayTemplateID: tmplID, Ports: defaultClaims}, nil)
	require.NoError(t, err)
}

func TestStreamService_Create_DuplicateNameRejected(t *testing.T) {
	store, tmpl, projectID, tmplID := streamEnabledSetup()
	store.streams[uuid.New()] = &models.Stream{ProjectID: projectID, Name: "db"}
	svc := newStreamSvcWithNS(t, store, tmpl, allowAllNamespaces{})
	_, err := svc.Create(projectID, services.CreateStreamInput{Name: "db", Namespace: "ns", GatewayTemplateID: tmplID, Ports: defaultClaims}, nil)
	assert.ErrorIs(t, err, services.ErrStreamNameTaken)
	assert.Empty(t, store.created)
}

type pgDupErr struct{}

func (pgDupErr) Error() string {
	return "ERROR: duplicate key value violates unique constraint (SQLSTATE 23505)"
}
func (pgDupErr) SQLState() string { return "23505" }

type racingStore struct {
	*fakeStreamStore
	createErr error
}

func (r racingStore) Create(*models.Stream) error { return r.createErr }

func TestStreamService_Create_RaceDuplicateMapsToNameTaken(t *testing.T) {
	for _, dup := range []error{pgDupErr{}, gorm.ErrDuplicatedKey} {
		store, tmpl, projectID, tmplID := streamEnabledSetup()
		applier := mocks.NewMockGatewayApplier(t)
		svc := services.NewStreamService(racingStore{store, dup}, tmpl, &fakeRouteCounter{}, applier, allowAllNamespaces{})
		_, err := svc.Create(projectID, services.CreateStreamInput{Name: "db", Namespace: "ns", GatewayTemplateID: tmplID, Ports: defaultClaims}, nil)
		assert.ErrorIs(t, err, services.ErrStreamNameTaken)
	}
}

func TestStreamService_Update_RenameToTakenNameRejected(t *testing.T) {
	store := newFakeStreamStore()
	projectID := uuid.New()
	id := uuid.New()
	store.streams[id] = &models.Stream{ID: id, ProjectID: projectID, Name: "old"}
	store.streams[uuid.New()] = &models.Stream{ProjectID: projectID, Name: "taken"}
	svc := newStreamSvc(t, store, &fakeTemplateReader{}, &fakeRouteCounter{})

	taken := "taken"
	_, err := svc.Update(id, services.UpdateStreamInput{Name: &taken})
	assert.ErrorIs(t, err, services.ErrStreamNameTaken)

	same := "old"
	_, err = svc.Update(id, services.UpdateStreamInput{Name: &same})
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Stream owns its Gateway: listeners come from the claimed ports
// ---------------------------------------------------------------------------

// gatewayEnv is a StreamService over fakes, a strict Gateway applier mock
// (an unexpected call fails the test) and a mock port store.
type gatewayEnv struct {
	svc       *services.StreamService
	store     *fakeStreamStore
	applier   *mocks.MockGatewayApplier
	portStore *mocks.MockStreamPortStore
	projectID uuid.UUID
	tmplID    uuid.UUID
}

func newGatewayEnv(t *testing.T) *gatewayEnv {
	t.Helper()
	store, tmpl, projectID, tmplID := streamEnabledSetup()
	e := &gatewayEnv{
		store:     store,
		applier:   mocks.NewMockGatewayApplier(t),
		portStore: mocks.NewMockStreamPortStore(t),
		projectID: projectID,
		tmplID:    tmplID,
	}
	e.svc = services.NewStreamService(store, tmpl, &fakeRouteCounter{}, e.applier, allowAllNamespaces{})
	e.svc.SetPortSources(e.portStore, mocks.NewMockDomainPortReader(t))
	return e
}

// seed stores a stream that already claims ports.
func (e *gatewayEnv) seed(ports models.StreamPorts) *models.Stream {
	s := &models.Stream{
		ID: uuid.New(), ProjectID: e.projectID, Name: "db", Namespace: "ns", GatewayTemplateID: e.tmplID,
		K8sGatewayName: "str-db", K8sGatewayClass: "gc", Ports: ports,
	}
	e.store.streams[s.ID] = s
	return s
}

// expectUpdateGateway captures the config handed to UpdateGateway.
func (e *gatewayEnv) expectUpdateGateway(err error) **kubernetes.GatewayConfig {
	got := new(*kubernetes.GatewayConfig)
	e.applier.EXPECT().UpdateGateway(mock.Anything, e.projectID, mock.Anything).
		Run(func(_ context.Context, _ uuid.UUID, cfg *kubernetes.GatewayConfig) { *got = cfg }).
		Return(err).Once()
	return got
}

func (e *gatewayEnv) expectNoUsedPorts(streamID uuid.UUID) {
	e.portStore.EXPECT().UsedPortsByStream(streamID, (*uuid.UUID)(nil)).Return(nil, nil)
}

func listenerTriples(cfg *kubernetes.GatewayConfig) []string {
	var out []string
	for _, l := range cfg.Listeners {
		out = append(out, fmt.Sprintf("%s/%s/%d", l.Name, l.Protocol, l.Port))
	}
	return out
}

func TestStreamCreate_AppliesGatewayFromClaims(t *testing.T) {
	e := newGatewayEnv(t)
	got := e.expectUpdateGateway(nil)

	// Lower-case and unordered on purpose: the persisted set is the normalized one.
	stream, err := e.svc.Create(e.projectID, services.CreateStreamInput{
		Name: "db", Namespace: "ns", GatewayTemplateID: e.tmplID,
		Ports: models.StreamPorts{{Transport: "udp", Port: 53}, {Transport: "tcp", Port: 5432}},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, *got)
	assert.Equal(t, "str-db", (*got).Name)
	assert.Equal(t, "ns", (*got).Namespace)
	assert.Equal(t, "gc", (*got).GatewayClassName)
	assert.Equal(t, []string{"l4-tcp-5432/TCP/5432", "l4-udp-53/UDP/53"}, listenerTriples(*got))

	want := models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 53}}
	assert.Equal(t, want, stream.Ports)
	require.Len(t, e.store.created, 1)
	assert.Equal(t, want, e.store.created[0].Ports)
	assert.Equal(t, "active", e.store.streams[stream.ID].Status)
	assert.Equal(t, "Gateway applied successfully", e.store.streams[stream.ID].StatusMessage)
}

func TestStreamCreate_RejectsInvalidClaims(t *testing.T) {
	cases := map[string]struct {
		ports models.StreamPorts
		want  error
	}{
		"empty":         {nil, services.ErrNoStreamPorts},
		"bad transport": {models.StreamPorts{{Transport: "SCTP", Port: 80}}, services.ErrInvalidStreamPort},
		"duplicate":     {models.StreamPorts{{Transport: "TCP", Port: 80}, {Transport: "tcp", Port: 80}}, services.ErrInvalidStreamPort},
		"reserved":      {models.StreamPorts{{Transport: "TCP", Port: 19000}}, services.ErrReservedPort},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newGatewayEnv(t) // strict applier: any K8s call fails the test
			_, err := e.svc.Create(e.projectID, services.CreateStreamInput{
				Name: "db", Namespace: "ns", GatewayTemplateID: e.tmplID, Ports: tc.ports,
			}, nil)
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, e.store.created, "nothing is persisted")
		})
	}
}

func TestStreamUpdate_PortsReapplyGateway(t *testing.T) {
	e := newGatewayEnv(t)
	s := e.seed(models.StreamPorts{{Transport: "TCP", Port: 5432}})
	e.expectNoUsedPorts(s.ID)
	got := e.expectUpdateGateway(nil)

	next := models.StreamPorts{{Transport: "UDP", Port: 53}, {Transport: "TCP", Port: 5432}}
	updated, err := e.svc.Update(s.ID, services.UpdateStreamInput{Ports: &next})
	require.NoError(t, err)

	require.NotNil(t, *got)
	assert.Equal(t, []string{"l4-tcp-5432/TCP/5432", "l4-udp-53/UDP/53"}, listenerTriples(*got))
	want := models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 53}}
	assert.Equal(t, want, updated.Ports)
	assert.Equal(t, want, e.store.streams[s.ID].Ports)
	assert.Equal(t, "active", updated.Status)
}

func TestStreamUpdate_DropUsedClaimIs409(t *testing.T) {
	e := newGatewayEnv(t)
	s := e.seed(models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 53}})
	e.portStore.EXPECT().UsedPortsByStream(s.ID, (*uuid.UUID)(nil)).
		Return([]services.PortUse{{Transport: "TCP", Port: 5432}}, nil)
	e.portStore.EXPECT().L4PortOwnersByStream(s.ID).Return([]services.L4PortOwner{
		{Transport: "TCP", Port: 5432, RouteName: "primary", Status: "active"},
	}, nil).Maybe()

	next := models.StreamPorts{{Transport: "UDP", Port: 53}}
	_, err := e.svc.Update(s.ID, services.UpdateStreamInput{Ports: &next})
	assert.ErrorIs(t, err, services.ErrStreamPortInUse)
	assert.Equal(t, models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 53}}, e.store.streams[s.ID].Ports,
		"the claim set is unchanged")
}

func TestStreamUpdate_ReorderedClaimsKeepUsedPorts(t *testing.T) {
	e := newGatewayEnv(t)
	s := e.seed(models.StreamPorts{{Transport: "TCP", Port: 5432}})
	e.portStore.EXPECT().UsedPortsByStream(s.ID, (*uuid.UUID)(nil)).
		Return([]services.PortUse{{Transport: "TCP", Port: 5432}}, nil)
	got := e.expectUpdateGateway(nil)

	next := models.StreamPorts{{Transport: "UDP", Port: 53}, {Transport: "tcp", Port: 5432}}
	_, err := e.svc.Update(s.ID, services.UpdateStreamInput{Ports: &next})
	require.NoError(t, err)
	assert.Equal(t, []string{"l4-tcp-5432/TCP/5432", "l4-udp-53/UDP/53"}, listenerTriples(*got))
}

func TestStreamUpdate_InvalidClaimsAreRejectedBeforeAnyWrite(t *testing.T) {
	for name, next := range map[string]models.StreamPorts{
		"empty":     {},
		"bad":       {{Transport: "ICMP", Port: 1}},
		"reserved":  {{Transport: "TCP", Port: 19001}},
		"duplicate": {{Transport: "UDP", Port: 53}, {Transport: "UDP", Port: 53}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newGatewayEnv(t)
			s := e.seed(models.StreamPorts{{Transport: "TCP", Port: 5432}})
			_, err := e.svc.Update(s.ID, services.UpdateStreamInput{Ports: &next})
			require.Error(t, err)
			assert.Equal(t, models.StreamPorts{{Transport: "TCP", Port: 5432}}, e.store.streams[s.ID].Ports)
		})
	}
}

func TestStreamUpdate_NameOnlyOnLegacyEmptyStream(t *testing.T) {
	e := newGatewayEnv(t) // strict applier and port store: nothing may be called
	s := e.seed(nil)

	n := "renamed"
	got, err := e.svc.Update(s.ID, services.UpdateStreamInput{Name: &n})
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.Name)
	assert.Empty(t, got.Ports)
}

func TestStreamUpdate_ApplyFailureRecordedOnStatus(t *testing.T) {
	e := newGatewayEnv(t)
	s := e.seed(models.StreamPorts{{Transport: "TCP", Port: 5432}})
	e.expectNoUsedPorts(s.ID)
	e.expectUpdateGateway(errors.New("boom"))

	next := models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 53}}
	got, err := e.svc.Update(s.ID, services.UpdateStreamInput{Ports: &next})
	require.NoError(t, err, "an apply failure is recorded on the stream, not returned")
	assert.Equal(t, "error", got.Status)
	assert.True(t, strings.HasPrefix(got.StatusMessage, "Failed to apply Gateway:"), got.StatusMessage)
	assert.Contains(t, got.StatusMessage, "boom")
	assert.Equal(t, "error", e.store.streams[s.ID].Status)
	assert.Equal(t, next.Normalize(), e.store.streams[s.ID].Ports, "the claims are saved even though the apply failed")
}

func TestStreamCreate_ApplyFailureRecordedOnStatus(t *testing.T) {
	e := newGatewayEnv(t)
	e.expectUpdateGateway(errors.New("boom"))

	stream, err := e.svc.Create(e.projectID, services.CreateStreamInput{
		Name: "db", Namespace: "ns", GatewayTemplateID: e.tmplID, Ports: defaultClaims,
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "error", stream.Status)
	assert.True(t, strings.HasPrefix(stream.StatusMessage, "Failed to apply Gateway:"), stream.StatusMessage)
	assert.Equal(t, "error", e.store.streams[stream.ID].Status)
}

func TestApplyGateway_EmptyClaimsDeletesGateway(t *testing.T) {
	e := newGatewayEnv(t)
	s := e.seed(nil)
	e.applier.EXPECT().DeleteGateway(mock.Anything, e.projectID, "ns", "str-db").Return(nil).Once()

	require.NoError(t, e.svc.ApplyGateway(context.Background(), s))
	assert.Equal(t, "active", s.Status)
	assert.Equal(t, "No ports claimed; no Gateway deployed", s.StatusMessage)
	assert.Equal(t, "active", e.store.streams[s.ID].Status)
}

func TestApplyGateway_EmptyClaimsAndGatewayAlreadyGone(t *testing.T) {
	e := newGatewayEnv(t)
	s := e.seed(nil)
	notFound := apierrors.NewNotFound(schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "gateways"}, "str-db")
	// The cluster client wraps the API error with %w.
	e.applier.EXPECT().DeleteGateway(mock.Anything, e.projectID, "ns", "str-db").
		Return(fmt.Errorf("failed to delete gateway: %w", notFound)).Once()

	require.NoError(t, e.svc.ApplyGateway(context.Background(), s))
	assert.Equal(t, "active", s.Status)
	assert.Equal(t, "No ports claimed; no Gateway deployed", s.StatusMessage)
}

func TestApplyGateway_EmptyClaimsDeleteFailureIsAnError(t *testing.T) {
	e := newGatewayEnv(t)
	s := e.seed(nil)
	e.applier.EXPECT().DeleteGateway(mock.Anything, e.projectID, "ns", "str-db").Return(errors.New("boom")).Once()

	require.Error(t, e.svc.ApplyGateway(context.Background(), s))
	assert.Equal(t, "error", s.Status)
	assert.True(t, strings.HasPrefix(s.StatusMessage, "Failed to apply Gateway:"), s.StatusMessage)
}

func TestStreamDelete_DeletesGateway(t *testing.T) {
	e := newGatewayEnv(t)
	s := e.seed(models.StreamPorts{{Transport: "TCP", Port: 5432}})
	var order []string
	e.applier.EXPECT().DeleteGateway(mock.Anything, e.projectID, "ns", "str-db").
		Run(func(context.Context, uuid.UUID, string, string) {
			assert.Empty(t, e.store.deleted, "the Gateway goes before the row")
			order = append(order, "gateway")
		}).Return(nil).Once()

	require.NoError(t, e.svc.Delete(s.ID))
	assert.Equal(t, []string{"gateway"}, order)
	assert.Equal(t, []uuid.UUID{s.ID}, e.store.deleted)
}

func TestStreamDelete_GatewayDeleteFailureStillDeletesRow(t *testing.T) {
	e := newGatewayEnv(t)
	s := e.seed(models.StreamPorts{{Transport: "TCP", Port: 5432}})
	e.applier.EXPECT().DeleteGateway(mock.Anything, e.projectID, "ns", "str-db").Return(errors.New("boom")).Once()

	require.NoError(t, e.svc.Delete(s.ID))
	assert.Equal(t, []uuid.UUID{s.ID}, e.store.deleted)
}

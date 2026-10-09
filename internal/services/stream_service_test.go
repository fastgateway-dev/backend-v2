package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type fakeStreamStore struct {
	streams map[uuid.UUID]*models.Stream
	created []*models.Stream
	deleted []uuid.UUID
}

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

func newStreamSvc(store *fakeStreamStore, tmpl *fakeTemplateReader, routes *fakeRouteCounter) *services.StreamService {
	return services.NewStreamService(store, tmpl, routes)
}

func TestStreamService_Create_RejectsNonStreamTemplate(t *testing.T) {
	store := newFakeStreamStore()
	projectID := uuid.New()
	tmpl := &fakeTemplateReader{tmpl: &models.DomainTemplate{ProjectID: projectID, EnableStream: false, K8sGatewayClassName: "gc"}}
	svc := newStreamSvc(store, tmpl, &fakeRouteCounter{})

	_, err := svc.Create(projectID, services.CreateStreamInput{Name: "db", Namespace: "ns", GatewayTemplateID: uuid.New()}, &models.User{ID: uuid.New()})
	assert.ErrorIs(t, err, services.ErrTemplateNotStreamEnabled)
	assert.Empty(t, store.created)
}

func TestStreamService_Create_CopiesGatewayClassAndNames(t *testing.T) {
	store := newFakeStreamStore()
	tmplID := uuid.New()
	projectID := uuid.New()
	tmpl := &fakeTemplateReader{tmpl: &models.DomainTemplate{ID: tmplID, ProjectID: projectID, EnableStream: true, K8sGatewayClassName: "public-lb"}}
	svc := newStreamSvc(store, tmpl, &fakeRouteCounter{})
	user := &models.User{ID: uuid.New()}

	got, err := svc.Create(projectID, services.CreateStreamInput{Name: "DB", Namespace: "ns", GatewayTemplateID: tmplID}, user)
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
	svc := newStreamSvc(newFakeStreamStore(), &fakeTemplateReader{err: gorm.ErrRecordNotFound}, &fakeRouteCounter{})
	_, err := svc.Create(uuid.New(), services.CreateStreamInput{Name: "db", GatewayTemplateID: uuid.New()}, nil)
	assert.Error(t, err)
}

func TestStreamService_Create_RejectsTemplateFromOtherProject(t *testing.T) {
	store := newFakeStreamStore()
	tmpl := &fakeTemplateReader{tmpl: &models.DomainTemplate{ProjectID: uuid.New(), EnableStream: true}}
	svc := newStreamSvc(store, tmpl, &fakeRouteCounter{})
	_, err := svc.Create(uuid.New(), services.CreateStreamInput{Name: "db", GatewayTemplateID: uuid.New()}, nil)
	assert.Error(t, err)
	assert.Empty(t, store.created)
}

func TestStreamService_Update_NameOnly_TemplateImmutable(t *testing.T) {
	store := newFakeStreamStore()
	id, tmplID := uuid.New(), uuid.New()
	store.streams[id] = &models.Stream{ID: id, Name: "old", GatewayTemplateID: tmplID, K8sGatewayName: "str-old", K8sGatewayClass: "gc"}
	svc := newStreamSvc(store, &fakeTemplateReader{}, &fakeRouteCounter{})

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
	svc := newStreamSvc(newFakeStreamStore(), &fakeTemplateReader{}, &fakeRouteCounter{})
	n := "x"
	_, err := svc.Update(uuid.New(), services.UpdateStreamInput{Name: &n})
	assert.ErrorIs(t, err, services.ErrStreamNotFound)
}

func TestStreamService_Delete_BlockedWithRoutes(t *testing.T) {
	store := newFakeStreamStore()
	id := uuid.New()
	store.streams[id] = &models.Stream{ID: id}
	svc := newStreamSvc(store, &fakeTemplateReader{}, &fakeRouteCounter{n: 1})

	err := svc.Delete(id)
	assert.ErrorIs(t, err, services.ErrStreamHasRoutes)
	assert.Empty(t, store.deleted)
}

func TestStreamService_Delete_OK(t *testing.T) {
	store := newFakeStreamStore()
	id := uuid.New()
	store.streams[id] = &models.Stream{ID: id}
	svc := newStreamSvc(store, &fakeTemplateReader{}, &fakeRouteCounter{})

	require.NoError(t, svc.Delete(id))
	assert.Equal(t, []uuid.UUID{id}, store.deleted)
}

func TestStreamService_Delete_NotFound(t *testing.T) {
	svc := newStreamSvc(newFakeStreamStore(), &fakeTemplateReader{}, &fakeRouteCounter{})
	assert.ErrorIs(t, svc.Delete(uuid.New()), services.ErrStreamNotFound)
}

func TestStreamService_Delete_CountError(t *testing.T) {
	store := newFakeStreamStore()
	id := uuid.New()
	store.streams[id] = &models.Stream{ID: id}
	boom := errors.New("boom")
	svc := newStreamSvc(store, &fakeTemplateReader{}, &fakeRouteCounter{err: boom})
	assert.ErrorIs(t, svc.Delete(id), boom)
	assert.Empty(t, store.deleted)
}

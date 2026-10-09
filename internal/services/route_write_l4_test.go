package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// l4WriteFixture wires a RouteService for L4 route create/update/delete: a
// mock Stream reader, a mock port checker, and mock project/policy lookups.
// The domain repository is deliberately bare: an L4 route has no Domain, so
// any domainRepo call fails loudly (also the nil-DomainID deref guard).
type l4WriteFixture struct {
	svc          *services.RouteService
	routeRepo    *mocks.MockRouteRepository
	approvalRepo *mocks.MockUnifiedApprovalRepository
	policyRepo   *mocks.MockApprovalPolicyRepository
	teamRepo     *mocks.MockTeamRepository
	streamRepo   *mocks.MockStreamReader
	ports        *mocks.MockL4PortChecker
	projectRepo  *mocks.MockProjectRepository
	stream       *models.Stream
	teamID       uuid.UUID
}

func newL4WriteFixture(t *testing.T, approvalEnabled bool) *l4WriteFixture {
	t.Helper()
	f := &l4WriteFixture{
		streamRepo:  new(mocks.MockStreamReader),
		ports:       new(mocks.MockL4PortChecker),
		projectRepo: new(mocks.MockProjectRepository),
		teamID:      uuid.New(),
	}
	f.svc, f.routeRepo, f.approvalRepo, f.policyRepo, _, f.teamRepo = newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.Streams = f.streamRepo
		d.ProjectRepo = f.projectRepo
	})
	f.svc.SetL4PortChecker(f.ports)
	f.stream = &models.Stream{ID: uuid.New(), ProjectID: uuid.New(), Name: "db", Namespace: "fastgateway-system"}
	f.streamRepo.On("GetByID", f.stream.ID).Return(f.stream, nil).Maybe()
	f.projectRepo.On("GetByID", f.stream.ProjectID).Return(&models.Project{ApprovalEnabled: approvalEnabled}, nil).Maybe()
	f.teamRepo.On("GetByID", f.teamID).Return(&models.Team{ID: f.teamID, Name: "platform"}, nil).Maybe()
	f.policyRepo.On("GetByProjectAndEntity", f.stream.ProjectID, "route", mock.Anything).Return(nil, models.ErrPolicyNotFound).Maybe()
	return f
}

func (f *l4WriteFixture) input(name string, proto models.RouteProtocol, port int) *services.CreateRouteInput {
	return &services.CreateRouteInput{
		Name: name, Protocol: proto, TeamID: f.teamID,
		Config: models.RouteConfig{ListenerPort: port, Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "pg", Namespace: "default", Port: 5432},
		}},
	}
}

func TestRouteWrite_Create_OwnerAmbiguous(t *testing.T) {
	f := newL4WriteFixture(t, true)
	user := uuid.New()

	// Neither a domain nor a stream.
	_, err := f.svc.Create(uuid.Nil, f.input("pg", models.RouteProtocolTCP, 5432), user)
	assert.ErrorIs(t, err, services.ErrRouteOwnerAmbiguous)

	// Both a domain and a stream.
	in := f.input("pg", models.RouteProtocolTCP, 5432)
	in.StreamID = &f.stream.ID
	_, err = f.svc.Create(uuid.New(), in, user)
	assert.ErrorIs(t, err, services.ErrRouteOwnerAmbiguous)

	f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestRouteWrite_CreateForStream_Success_SubmitsRouteApproval(t *testing.T) {
	f := newL4WriteFixture(t, true)
	user := uuid.New()

	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "pg").Return(false, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 5432, (*uuid.UUID)(nil)).Return(nil)
	var persisted *models.Route
	f.routeRepo.On("Create", mock.AnythingOfType("*models.Route")).Run(func(args mock.Arguments) {
		persisted = args.Get(0).(*models.Route)
	}).Return(nil)
	f.approvalRepo.On("Create", mock.MatchedBy(func(a *models.Approval) bool {
		return a.EntityType == models.ApprovalEntityRoute && a.Action == models.ApprovalActionCreate &&
			a.ProjectID == f.stream.ProjectID
	})).Return(nil)

	route, err := f.svc.CreateForStream(f.stream.ProjectID, f.stream.ID, f.input("pg", models.RouteProtocolTCP, 5432), user)

	require.NoError(t, err)
	require.NotNil(t, persisted)
	require.NotNil(t, route.StreamID)
	assert.Equal(t, f.stream.ID, *route.StreamID)
	assert.Nil(t, route.DomainID, "an L4 route has a stream owner, never a domain")
	assert.Equal(t, models.RouteProtocolTCP, route.Protocol)
	assert.Equal(t, models.RouteStatusPendingCreate, route.Status)
	assert.NotNil(t, route.PendingApproval)
	assert.True(t, route.IsL4())
	f.approvalRepo.AssertExpectations(t)
}

func TestRouteWrite_CreateForStream_UDP(t *testing.T) {
	f := newL4WriteFixture(t, true)
	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "dns").Return(false, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "UDP", 5353, (*uuid.UUID)(nil)).Return(nil)
	f.routeRepo.On("Create", mock.Anything).Return(nil)
	f.approvalRepo.On("Create", mock.Anything).Return(nil)

	route, err := f.svc.CreateForStream(f.stream.ProjectID, f.stream.ID, f.input("dns", models.RouteProtocolUDP, 5353), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteProtocolUDP, route.Protocol)
}

func TestRouteWrite_CreateForStream_ApprovalsDisabled_FastPath(t *testing.T) {
	f := newL4WriteFixture(t, false)
	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "pg").Return(false, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 5432, (*uuid.UUID)(nil)).Return(nil)
	f.routeRepo.On("Create", mock.Anything).Return(nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)

	route, err := f.svc.CreateForStream(f.stream.ProjectID, f.stream.ID, f.input("pg", models.RouteProtocolTCP, 5432), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusApproved, route.Status)
	f.approvalRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestRouteWrite_CreateForStream_StreamNotFound(t *testing.T) {
	f := newL4WriteFixture(t, true)
	missing := uuid.New()
	f.streamRepo.On("GetByID", missing).Return(nil, gorm.ErrRecordNotFound)

	_, err := f.svc.CreateForStream(f.stream.ProjectID, missing, f.input("pg", models.RouteProtocolTCP, 5432), uuid.New())

	assert.ErrorIs(t, err, services.ErrStreamNotFound)
	f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestRouteWrite_CreateForStream_WrongProject_ReportedAsNotFound(t *testing.T) {
	f := newL4WriteFixture(t, true)

	_, err := f.svc.CreateForStream(uuid.New(), f.stream.ID, f.input("pg", models.RouteProtocolTCP, 5432), uuid.New())

	assert.ErrorIs(t, err, services.ErrStreamNotFound, "a stream in another project must not be probeable")
	f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestRouteWrite_CreateForStream_RequiresL4Protocol(t *testing.T) {
	f := newL4WriteFixture(t, true)
	for _, proto := range []models.RouteProtocol{"", models.RouteProtocolHTTP, models.RouteProtocolGRPC} {
		in := f.input("pg", proto, 5432)
		_, err := f.svc.CreateForStream(f.stream.ProjectID, f.stream.ID, in, uuid.New())
		require.Error(t, err, "protocol %q", proto)
	}
	f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestRouteWrite_CreateForStream_NameTaken(t *testing.T) {
	f := newL4WriteFixture(t, true)
	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "pg").Return(true, nil)

	_, err := f.svc.CreateForStream(f.stream.ProjectID, f.stream.ID, f.input("pg", models.RouteProtocolTCP, 5432), uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestRouteWrite_CreateForStream_PortCollisionPrecheck(t *testing.T) {
	f := newL4WriteFixture(t, true)
	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "pg").Return(false, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 5432, (*uuid.UUID)(nil)).Return(services.ErrPortCollision)

	_, err := f.svc.CreateForStream(f.stream.ProjectID, f.stream.ID, f.input("pg", models.RouteProtocolTCP, 5432), uuid.New())

	assert.ErrorIs(t, err, services.ErrPortCollision)
	f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
}

// sqlStateErr stands in for pgconn.PgError, which exposes SQLState().
type sqlStateErr struct{ code string }

func (e sqlStateErr) Error() string {
	return "ERROR: duplicate key value violates unique constraint \"idx_route_stream_proto_port\" (SQLSTATE " + e.code + ")"
}
func (e sqlStateErr) SQLState() string { return e.code }

// A concurrent same-port create passes the collision pre-check and loses at
// the unique index; that must surface as ErrPortCollision (409), not a 500.
func TestRouteWrite_CreateForStream_UniqueViolationOnPersist_MapsToPortCollision(t *testing.T) {
	for name, persistErr := range map[string]error{
		"sqlstate 23505":      sqlStateErr{code: "23505"},
		"gorm duplicated key": gorm.ErrDuplicatedKey,
	} {
		t.Run(name, func(t *testing.T) {
			f := newL4WriteFixture(t, true)
			f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "pg").Return(false, nil)
			f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 5432, (*uuid.UUID)(nil)).Return(nil)
			f.routeRepo.On("Create", mock.Anything).Return(persistErr)

			_, err := f.svc.CreateForStream(f.stream.ProjectID, f.stream.ID, f.input("pg", models.RouteProtocolTCP, 5432), uuid.New())

			assert.ErrorIs(t, err, services.ErrPortCollision)
			f.approvalRepo.AssertNotCalled(t, "Create", mock.Anything)
		})
	}
}

func TestRouteWrite_CreateForStream_OtherPersistErrorIsNotMapped(t *testing.T) {
	f := newL4WriteFixture(t, true)
	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "pg").Return(false, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 5432, (*uuid.UUID)(nil)).Return(nil)
	boom := errors.New("connection reset")
	f.routeRepo.On("Create", mock.Anything).Return(boom)

	_, err := f.svc.CreateForStream(f.stream.ProjectID, f.stream.ID, f.input("pg", models.RouteProtocolTCP, 5432), uuid.New())

	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, services.ErrPortCollision)
}

func (f *l4WriteFixture) existingRoute(status models.RouteStatus) *models.Route {
	sid := f.stream.ID
	return &models.Route{
		ID: uuid.New(), StreamID: &sid, TeamID: f.teamID, Name: "pg", K8sRouteName: "pg-abcd1234",
		Protocol: models.RouteProtocolTCP, SecurityMode: models.SecurityModeGeneral, Status: status,
		Config: models.RouteConfig{ListenerPort: 5432, Backends: []models.RouteBackend{
			{Type: models.BackendTypeKubernetes, Service: "pg", Namespace: "default", Port: 5432},
		}},
	}
}

// Update must not dereference the nil DomainID of an L4 route; it resolves the
// stream instead, excludes the route from its own collision check, and submits
// the same route approval an HTTP update does.
func TestRouteWrite_Update_L4Route_DoesNotPanicAndSubmitsApproval(t *testing.T) {
	f := newL4WriteFixture(t, true)
	route := f.existingRoute(models.RouteStatusActive)
	f.routeRepo.On("GetByID", route.ID).Return(route, nil)
	f.approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, route.ID).Return(nil, errors.New("none"))
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 6432, &route.ID).Return(nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)
	f.approvalRepo.On("Create", mock.MatchedBy(func(a *models.Approval) bool {
		return a.EntityType == models.ApprovalEntityRoute && a.Action == models.ApprovalActionUpdate &&
			a.ProjectID == f.stream.ProjectID
	})).Return(nil)

	cfg := f.input("pg", models.RouteProtocolTCP, 6432).Config
	var updated *models.Route
	var err error
	require.NotPanics(t, func() {
		updated, err = f.svc.Update(route.ID, &services.UpdateRouteInput{Config: cfg}, uuid.New())
	})

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingUpdate, updated.Status)
	assert.NotNil(t, updated.PendingApproval)
	f.approvalRepo.AssertExpectations(t)
}

func TestRouteWrite_Update_L4Route_PortCollisionRejected(t *testing.T) {
	f := newL4WriteFixture(t, true)
	route := f.existingRoute(models.RouteStatusActive)
	f.routeRepo.On("GetByID", route.ID).Return(route, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 6432, &route.ID).Return(services.ErrPortCollision)

	_, err := f.svc.Update(route.ID, &services.UpdateRouteInput{Config: f.input("pg", models.RouteProtocolTCP, 6432).Config}, uuid.New())

	assert.ErrorIs(t, err, services.ErrPortCollision)
	f.routeRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestRouteWrite_Delete_L4Route_DoesNotPanicAndSubmitsApproval(t *testing.T) {
	f := newL4WriteFixture(t, true)
	route := f.existingRoute(models.RouteStatusActive)
	f.routeRepo.On("GetByID", route.ID).Return(route, nil)
	f.approvalRepo.On("GetPendingByEntityID", models.ApprovalEntityRoute, route.ID).Return(nil, errors.New("none"))
	f.routeRepo.On("Update", mock.Anything).Return(nil)
	f.approvalRepo.On("Create", mock.MatchedBy(func(a *models.Approval) bool {
		return a.EntityType == models.ApprovalEntityRoute && a.Action == models.ApprovalActionDelete &&
			a.ProjectID == f.stream.ProjectID
	})).Return(nil)

	var deleted *models.Route
	var err error
	require.NotPanics(t, func() { deleted, err = f.svc.Delete(route.ID, uuid.New()) })

	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusPendingDelete, deleted.Status)
	assert.NotNil(t, deleted.PendingApproval)
}

func TestRouteQuery_ListByStreamID(t *testing.T) {
	f := newL4WriteFixture(t, true)
	routes := []models.Route{{ID: uuid.New(), Name: "pg"}}
	f.routeRepo.On("ListByStreamID", f.stream.ID, 1, 20, (*uuid.UUID)(nil), "").Return(routes, int64(1), nil)

	got, total, err := f.svc.ListByStreamID(f.stream.ProjectID, f.stream.ID, 1, 20, nil, "")

	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, got, 1)
}

func TestRouteQuery_ListByStreamID_WrongProject(t *testing.T) {
	f := newL4WriteFixture(t, true)

	_, _, err := f.svc.ListByStreamID(uuid.New(), f.stream.ID, 1, 20, nil, "")

	assert.ErrorIs(t, err, services.ErrStreamNotFound)
	f.routeRepo.AssertNotCalled(t, "ListByStreamID", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRouteQuery_GetForStream(t *testing.T) {
	f := newL4WriteFixture(t, true)
	route := f.existingRoute(models.RouteStatusActive)
	otherStream := uuid.New()
	other := f.existingRoute(models.RouteStatusActive)
	other.StreamID = &otherStream
	f.routeRepo.On("GetByIDWithApproval", route.ID).Return(route, nil)
	f.routeRepo.On("GetByIDWithApproval", other.ID).Return(other, nil)

	got, err := f.svc.GetForStream(f.stream.ProjectID, f.stream.ID, route.ID)
	require.NoError(t, err)
	assert.Equal(t, route.ID, got.ID)

	// A route of another stream, or a stream of another project, is not found.
	_, err = f.svc.GetForStream(f.stream.ProjectID, f.stream.ID, other.ID)
	assert.ErrorIs(t, err, services.ErrRouteNotFound)
	_, err = f.svc.GetForStream(uuid.New(), f.stream.ID, route.ID)
	assert.ErrorIs(t, err, services.ErrStreamNotFound)
}

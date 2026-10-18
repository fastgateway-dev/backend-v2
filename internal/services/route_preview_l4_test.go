package services_test

import (
	"fmt"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// tcpBTPInput is tcpBTPConfig as a create/update input.
func tcpBTPInput() *routeplan.BackendTrafficPolicyInput {
	cfg := tcpBTPConfig()
	return &routeplan.BackendTrafficPolicyInput{CircuitBreaker: cfg.CircuitBreaker, Timeout: cfg.Timeout}
}

func TestPreviewCreateForStream_RendersRouteAndPolicyWithoutWriting(t *testing.T) {
	f := newL4WriteFixture(t, true)
	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "pg").Return(false, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 9050, (*uuid.UUID)(nil)).Return(nil)
	in := f.input("pg", models.RouteProtocolTCP, 9050)
	in.BackendTrafficPolicy = tcpBTPInput()

	res, err := f.svc.PreviewCreateForStream(f.stream.ProjectID, f.stream.ID, in)
	require.NoError(t, err)

	assert.Contains(t, res.ProposedYAML, "kind: TCPRoute")
	assert.Contains(t, res.ProposedYAML, "sectionName: l4-tcp-9050")
	assert.Contains(t, res.ProposedYAML, "name: pg-", "the K8s name is derived from the route name, as create does")
	assert.Contains(t, res.ProposedBackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
	assert.Contains(t, res.ProposedBackendTrafficPolicyYAML, "maxConnections: 100")
	assert.Contains(t, res.ProposedBackendTrafficPolicyYAML, "namespace: fastgateway-system")
	f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
	f.approvalRepo.AssertNotCalled(t, "Create", mock.Anything)
	f.ports.AssertExpectations(t)
}

func TestPreviewCreateForStream_NoPolicyInputMeansNoPolicyYAML(t *testing.T) {
	f := newL4WriteFixture(t, true)
	f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, "dns").Return(false, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "UDP", 9053, (*uuid.UUID)(nil)).Return(nil)

	res, err := f.svc.PreviewCreateForStream(f.stream.ProjectID, f.stream.ID, f.input("dns", models.RouteProtocolUDP, 9053))
	require.NoError(t, err)
	assert.Contains(t, res.ProposedYAML, "kind: UDPRoute")
	assert.Empty(t, res.ProposedBackendTrafficPolicyYAML)
}

// TestPreviewCreateForStream_SameErrorsAsCreateForStream runs every bad input
// through both PreviewCreateForStream and CreateForStream on fresh, identical
// fixtures and requires the same error (so the handler's routeWriteErrorStatus
// maps both to the same 400/404/409): a preview never shows a manifest that
// create would reject.
func TestPreviewCreateForStream_SameErrorsAsCreateForStream(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(in *services.CreateRouteInput)
		project func(f *l4WriteFixture) uuid.UUID
		checker error
		exists  bool
		is      error
	}{
		{name: "bad name", mutate: func(in *services.CreateRouteInput) { in.Name = "Bad Name" }},
		{name: "not tcp/udp", mutate: func(in *services.CreateRouteInput) { in.Protocol = models.RouteProtocolHTTP }, is: services.ErrRouteProtocolNotL4},
		{name: "reserved port", mutate: func(in *services.CreateRouteInput) { in.Config.ListenerPort = 19000 }, is: services.ErrReservedPort},
		{name: "invalid port", mutate: func(in *services.CreateRouteInput) { in.Config.ListenerPort = 70000 }, is: services.ErrInvalidListenerPort},
		{name: "out of template range", checker: fmt.Errorf("%w: 9050 not in 10000-11000", services.ErrPortOutOfRange), is: services.ErrPortOutOfRange},
		{name: "collision", checker: fmt.Errorf("%w: TCP/9050 is used by a stream route", services.ErrPortCollision), is: services.ErrPortCollision},
		{name: "L7 policy on an L4 route", mutate: func(in *services.CreateRouteInput) { in.SecurityPolicy = &routeplan.SecurityPolicyInput{} }, is: services.ErrL4RejectsL7Field},
		{name: "stream of another project", project: func(*l4WriteFixture) uuid.UUID { return uuid.New() }, is: services.ErrStreamNotFound},
		{name: "name already taken on the stream", exists: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := func(call func(f *l4WriteFixture, projectID uuid.UUID, in *services.CreateRouteInput) error) error {
				f := newL4WriteFixture(t, true)
				f.routeRepo.On("ExistsByStreamAndName", f.stream.ID, mock.Anything).Return(tc.exists, nil).Maybe()
				f.ports.On("CheckPortCollision", f.stream.ID, mock.Anything, mock.Anything, (*uuid.UUID)(nil)).Return(tc.checker).Maybe()
				in := f.input("pg", models.RouteProtocolTCP, 9050)
				if tc.mutate != nil {
					tc.mutate(in)
				}
				projectID := f.stream.ProjectID
				if tc.project != nil {
					projectID = tc.project(f)
				}
				err := call(f, projectID, in)
				f.routeRepo.AssertNotCalled(t, "Create", mock.Anything)
				return err
			}
			previewErr := run(func(f *l4WriteFixture, projectID uuid.UUID, in *services.CreateRouteInput) error {
				_, err := f.svc.PreviewCreateForStream(projectID, f.stream.ID, in)
				return err
			})
			createErr := run(func(f *l4WriteFixture, projectID uuid.UUID, in *services.CreateRouteInput) error {
				_, err := f.svc.CreateForStream(projectID, f.stream.ID, in, uuid.New())
				return err
			})

			require.Error(t, createErr)
			require.Error(t, previewErr)
			assert.Equal(t, createErr.Error(), previewErr.Error())
			if tc.is != nil {
				assert.ErrorIs(t, createErr, tc.is)
				assert.ErrorIs(t, previewErr, tc.is)
			}
		})
	}
}

func TestPreviewUpdateForStream_ShowsRouteAndPolicyDiffAndRechecksPort(t *testing.T) {
	f := newL4WriteFixture(t, true)
	route := f.existingRoute(models.RouteStatusActive)
	f.routeRepo.On("GetByID", route.ID).Return(route, nil)
	f.ports.On("CheckPortCollision", f.stream.ID, "TCP", 6432, &route.ID).Return(nil)
	cfg := f.input("pg", models.RouteProtocolTCP, 6432).Config

	res, err := f.svc.PreviewUpdateForStream(route.ID, &services.UpdateRouteInput{Config: cfg, BackendTrafficPolicy: tcpBTPInput()})
	require.NoError(t, err)

	assert.Contains(t, res.CurrentYAML, "sectionName: l4-tcp-5432")
	assert.Contains(t, res.ProposedYAML, "sectionName: l4-tcp-6432")
	assert.Empty(t, res.CurrentBackendTrafficPolicyYAML, "the route has no stored policy yet")
	assert.Contains(t, res.ProposedBackendTrafficPolicyYAML, "maxConnections: 100")
	f.ports.AssertCalled(t, "CheckPortCollision", f.stream.ID, "TCP", 6432, &route.ID)
	f.routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	f.approvalRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestPreviewUpdateForStream_SameErrorsAsUpdate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		port    int
		checker error
		is      error
	}{
		{"collision on the new port", 6432, fmt.Errorf("%w: TCP/6432", services.ErrPortCollision), services.ErrPortCollision},
		{"reserved port", 60000, nil, services.ErrReservedPort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(call func(f *l4WriteFixture, routeID uuid.UUID, in *services.UpdateRouteInput) error) error {
				f := newL4WriteFixture(t, true)
				route := f.existingRoute(models.RouteStatusActive)
				f.routeRepo.On("GetByID", route.ID).Return(route, nil)
				f.ports.On("CheckPortCollision", f.stream.ID, "TCP", tc.port, &route.ID).Return(tc.checker).Maybe()
				err := call(f, route.ID, &services.UpdateRouteInput{Config: f.input("pg", models.RouteProtocolTCP, tc.port).Config})
				f.routeRepo.AssertNotCalled(t, "Update", mock.Anything)
				return err
			}
			previewErr := run(func(f *l4WriteFixture, id uuid.UUID, in *services.UpdateRouteInput) error {
				_, err := f.svc.PreviewUpdateForStream(id, in)
				return err
			})
			updateErr := run(func(f *l4WriteFixture, id uuid.UUID, in *services.UpdateRouteInput) error {
				_, err := f.svc.Update(id, in, uuid.New())
				return err
			})
			require.Error(t, updateErr)
			assert.Equal(t, updateErr.Error(), previewErr.Error())
			assert.ErrorIs(t, previewErr, tc.is)
		})
	}
}

func TestPreviewUpdateForStream_NonL4RouteIsNotFound(t *testing.T) {
	f := newL4WriteFixture(t, true)
	domainID := uuid.New()
	route := &models.Route{ID: uuid.New(), DomainID: &domainID, Protocol: models.RouteProtocolHTTP}
	f.routeRepo.On("GetByID", route.ID).Return(route, nil)

	_, err := f.svc.PreviewUpdateForStream(route.ID, &services.UpdateRouteInput{})
	assert.ErrorIs(t, err, services.ErrRouteNotFound)
}

func TestRouteYAML_L4_PreviewUpdate_StoredPolicyIsCurrent(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolTCP, 5432)
	btpRepo.On("GetByRouteID", route.ID).Return(&models.BackendTrafficPolicy{ID: uuid.New(), RouteID: &route.ID, Config: tcpBTPConfig()}, nil)

	// The update drops the policy: current shows it, proposed does not.
	res, err := f.svc.PreviewUpdate(route.ID, &services.UpdateRouteInput{Config: route.Config})
	require.NoError(t, err)
	assert.Contains(t, res.CurrentBackendTrafficPolicyYAML, "maxConnections: 100")
	assert.Empty(t, res.ProposedBackendTrafficPolicyYAML)
}

func TestRouteYAML_L4_PreviewDelete_IncludesPolicy(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolTCP, 5432)
	btpRepo.On("GetByRouteID", route.ID).Return(&models.BackendTrafficPolicy{ID: uuid.New(), RouteID: &route.ID, Config: tcpBTPConfig()}, nil)

	res, err := f.svc.PreviewDelete(route.ID)
	require.NoError(t, err)
	assert.Contains(t, res.CurrentYAML, "kind: TCPRoute")
	assert.Contains(t, res.CurrentBackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
}

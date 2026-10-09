package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// l4Fixture wires a RouteService whose stream reader and Kubernetes roles are
// mocks. It deliberately leaves the domain repository bare: an L4 route has no
// DomainID, so any domainRepo call during an L4 deploy fails loudly (a mock
// with no expectation panics) - which is also the nil-DomainID deref guard.
type l4Fixture struct {
	svc          *services.RouteService
	routeRepo    *mocks.MockRouteRepository
	approvalRepo *mocks.MockUnifiedApprovalRepository
	streamRepo   *mocks.MockStreamReader
	k8s          *mocks.MockKubernetesService
	btpRepo      *mocks.MockBackendTrafficPolicyRepository
	btp          map[uuid.UUID]*models.BackendTrafficPolicy
	stream       *models.Stream
}

func newL4Fixture(t *testing.T) *l4Fixture {
	t.Helper()
	f := &l4Fixture{
		streamRepo: new(mocks.MockStreamReader),
		k8s:        new(mocks.MockKubernetesService),
		btpRepo:    new(mocks.MockBackendTrafficPolicyRepository),
		btp:        map[uuid.UUID]*models.BackendTrafficPolicy{},
	}
	f.svc, f.routeRepo, f.approvalRepo, _, _, _ = newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.Streams = f.streamRepo
		d.K8sGateways = f.k8s
		d.K8sL4Routes = f.k8s
		d.K8sPolicies = f.k8s
		d.BackendTrafficPolicyRepo = f.btpRepo
	})
	f.stream = &models.Stream{
		ID: uuid.New(), ProjectID: uuid.New(), Name: "db", Namespace: "fastgateway-system",
		K8sGatewayName: "str-db", K8sGatewayClass: "public-lb",
	}
	f.streamRepo.On("GetByID", f.stream.ID).Return(f.stream, nil)
	return f
}

func (f *l4Fixture) route(name string, proto models.RouteProtocol, port int, status models.RouteStatus) *models.Route {
	sid := f.stream.ID
	return &models.Route{
		ID: uuid.New(), StreamID: &sid, Name: name, K8sRouteName: name + "-abcd1234",
		Protocol: proto, Status: status,
		Config: models.RouteConfig{
			ListenerPort: port,
			Backends:     []models.RouteBackend{{Type: models.BackendTypeKubernetes, Service: name, Namespace: "default", Port: 5432}},
		},
	}
}

// withBTP gives the route a stored BackendTrafficPolicy; call it before
// expectDeploy. Routes without one see "no policy" from the repository.
func (f *l4Fixture) withBTP(route *models.Route, cfg models.BackendTrafficPolicyConfig) *models.BackendTrafficPolicy {
	p := &models.BackendTrafficPolicy{ID: uuid.New(), RouteID: &route.ID, Config: cfg}
	f.btp[route.ID] = p
	return p
}

func (f *l4Fixture) expectDeploy(route *models.Route, action models.ApprovalAction) {
	if p, ok := f.btp[route.ID]; ok {
		f.btpRepo.On("GetByRouteID", route.ID).Return(p, nil).Maybe()
	} else {
		f.btpRepo.On("GetByRouteID", route.ID).Return(nil, nil).Maybe()
	}
	f.routeRepo.On("GetByID", route.ID).Return(route, nil)
	f.approvalRepo.On("GetLatestApprovedByEntityID", models.ApprovalEntityRoute, route.ID).
		Return(&models.Approval{Action: action}, nil)
}

func listenerPorts(cfg *kubernetes.GatewayConfig) map[string]int {
	m := map[string]int{}
	for _, l := range cfg.Listeners {
		m[l.Name] = l.Port
	}
	return m
}

func TestRouteDeploy_L4_CreateSecondTCPRoute_RecomputesFullListenerSet(t *testing.T) {
	f := newL4Fixture(t)
	existing := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusActive)
	newRoute := f.route("redis", models.RouteProtocolTCP, 6379, models.RouteStatusApproved)

	f.expectDeploy(newRoute, models.ApprovalActionCreate)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{*existing}, nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)

	var gw *kubernetes.GatewayConfig
	f.k8s.On("UpdateGateway", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).
		Run(func(args mock.Arguments) { gw = args.Get(2).(*kubernetes.GatewayConfig) }).Return(nil)
	var tcp *kubernetes.TCPRouteConfig
	f.k8s.On("CreateTCPRoute", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.TCPRouteConfig")).
		Run(func(args mock.Arguments) { tcp = args.Get(2).(*kubernetes.TCPRouteConfig) }).Return(nil)

	got, err := f.svc.Deploy(newRoute.ID, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, got.Status)

	require.NotNil(t, gw)
	assert.Equal(t, "str-db", gw.Name)
	assert.Equal(t, map[string]int{"l4-tcp-5432": 5432, "l4-tcp-6379": 6379}, listenerPorts(gw),
		"the Gateway must carry the full recomputed listener set, existing + new")
	require.NotNil(t, tcp)
	assert.Equal(t, "redis-abcd1234", tcp.Name)
	assert.Equal(t, "l4-tcp-6379", tcp.SectionName)
	f.k8s.AssertNotCalled(t, "CreateGateway", mock.Anything, mock.Anything, mock.Anything)
	f.k8s.AssertNotCalled(t, "CreateHTTPRoute", mock.Anything, mock.Anything, mock.Anything)
}

func TestRouteDeploy_L4_GatewayAppliedBeforeRoute(t *testing.T) {
	f := newL4Fixture(t)
	route := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusApproved)
	f.expectDeploy(route, models.ApprovalActionCreate)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)

	var order []string
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { order = append(order, "gateway") }).Return(nil)
	f.k8s.On("CreateTCPRoute", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { order = append(order, "route") }).Return(nil)

	_, err := f.svc.Deploy(route.ID, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, []string{"gateway", "route"}, order)
}

func TestRouteDeploy_L4_UpdateDoesNotDuplicateCurrentRoute(t *testing.T) {
	f := newL4Fixture(t)
	// The route being updated is already in the live set (with its old port);
	// the recompute must use the new config exactly once.
	current := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusPendingDeploy)
	stale := *current
	stale.Status = models.RouteStatusActive
	stale.Config.ListenerPort = 5000
	other := f.route("dns", models.RouteProtocolUDP, 53, models.RouteStatusActive)

	f.expectDeploy(current, models.ApprovalActionUpdate)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{stale, *other}, nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)

	var gw *kubernetes.GatewayConfig
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.AnythingOfType("*kubernetes.GatewayConfig")).
		Run(func(args mock.Arguments) { gw = args.Get(2).(*kubernetes.GatewayConfig) }).Return(nil)
	f.k8s.On("UpdateTCPRoute", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.TCPRouteConfig")).Return(nil)

	_, err := f.svc.Deploy(current.ID, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"l4-tcp-5432": 5432, "l4-udp-53": 53}, listenerPorts(gw))
	f.k8s.AssertCalled(t, "UpdateTCPRoute", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.TCPRouteConfig"))
}

func TestRouteDeploy_L4_CreateUDPRoute(t *testing.T) {
	f := newL4Fixture(t)
	route := f.route("dns", models.RouteProtocolUDP, 53, models.RouteStatusApproved)
	f.expectDeploy(route, models.ApprovalActionCreate)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	var udp *kubernetes.UDPRouteConfig
	f.k8s.On("CreateUDPRoute", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.UDPRouteConfig")).
		Run(func(args mock.Arguments) { udp = args.Get(2).(*kubernetes.UDPRouteConfig) }).Return(nil)

	got, err := f.svc.Deploy(route.ID, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, models.RouteStatusActive, got.Status)
	require.NotNil(t, udp)
	assert.Equal(t, "l4-udp-53", udp.SectionName)
	f.k8s.AssertNotCalled(t, "CreateTCPRoute", mock.Anything, mock.Anything, mock.Anything)
}

func TestRouteDeploy_L4_DeleteExcludesCurrentRouteAndRemovesCRD(t *testing.T) {
	f := newL4Fixture(t)
	removing := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusPendingDeploy)
	keep := f.route("redis", models.RouteProtocolTCP, 6379, models.RouteStatusActive)

	f.expectDeploy(removing, models.ApprovalActionDelete)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{*removing, *keep}, nil)
	f.approvalRepo.On("DeleteByEntityID", models.ApprovalEntityRoute, removing.ID).Return(nil)
	f.routeRepo.On("Delete", removing.ID).Return(nil)

	var gw *kubernetes.GatewayConfig
	f.k8s.On("UpdateGateway", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).
		Run(func(args mock.Arguments) { gw = args.Get(2).(*kubernetes.GatewayConfig) }).Return(nil)
	f.k8s.On("DeleteTCPRoute", mock.Anything, f.stream.ProjectID, f.stream.Namespace, removing.K8sRouteName).Return(nil)

	_, err := f.svc.Deploy(removing.ID, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"l4-tcp-6379": 6379}, listenerPorts(gw))
	f.k8s.AssertCalled(t, "DeleteTCPRoute", mock.Anything, f.stream.ProjectID, f.stream.Namespace, removing.K8sRouteName)
	f.routeRepo.AssertCalled(t, "Delete", removing.ID)
}

func TestRouteDeploy_L4_DeleteLastRouteLeavesPlaceholderListener(t *testing.T) {
	f := newL4Fixture(t)
	removing := f.route("dns", models.RouteProtocolUDP, 53, models.RouteStatusPendingDeploy)
	f.expectDeploy(removing, models.ApprovalActionDelete)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{*removing}, nil)
	f.approvalRepo.On("DeleteByEntityID", models.ApprovalEntityRoute, removing.ID).Return(nil)
	f.routeRepo.On("Delete", removing.ID).Return(nil)

	var gw *kubernetes.GatewayConfig
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.AnythingOfType("*kubernetes.GatewayConfig")).
		Run(func(args mock.Arguments) { gw = args.Get(2).(*kubernetes.GatewayConfig) }).Return(nil)
	f.k8s.On("DeleteUDPRoute", mock.Anything, f.stream.ProjectID, f.stream.Namespace, removing.K8sRouteName).Return(nil)

	_, err := f.svc.Deploy(removing.ID, uuid.New())
	require.NoError(t, err)
	require.Len(t, gw.Listeners, 1)
	assert.Equal(t, "l4-placeholder", gw.Listeners[0].Name)
}

func TestRouteDeploy_L4_GatewayFailureSkipsRouteAndKeepsStatus(t *testing.T) {
	f := newL4Fixture(t)
	route := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusApproved)
	f.expectDeploy(route, models.ApprovalActionCreate)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("boom"))

	_, err := f.svc.Deploy(route.ID, uuid.New())
	require.Error(t, err)
	f.k8s.AssertNotCalled(t, "CreateTCPRoute", mock.Anything, mock.Anything, mock.Anything)
	f.routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	assert.Equal(t, models.RouteStatusApproved, route.Status)
}

func TestRouteDeploy_L4_StreamLookupFailure(t *testing.T) {
	f := newL4Fixture(t)
	route := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusApproved)
	other := uuid.New()
	route.StreamID = &other
	f.streamRepo.On("GetByID", other).Return(nil, errors.New("not found"))
	f.expectDeploy(route, models.ApprovalActionCreate)

	_, err := f.svc.Deploy(route.ID, uuid.New())
	require.Error(t, err)
	f.k8s.AssertNotCalled(t, "UpdateGateway", mock.Anything, mock.Anything, mock.Anything)
}

func TestRouteDeploy_L4_MissingStreamID(t *testing.T) {
	f := newL4Fixture(t)
	route := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusApproved)
	route.StreamID = nil
	f.expectDeploy(route, models.ApprovalActionCreate)

	_, err := f.svc.Deploy(route.ID, uuid.New())
	require.Error(t, err)
}

func tcpBTPConfig() models.BackendTrafficPolicyConfig {
	maxConns := int64(100)
	return models.BackendTrafficPolicyConfig{
		CircuitBreaker: &models.CircuitBreakerConfig{MaxConnections: &maxConns},
		Timeout:        &models.BTPTimeoutConfig{TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"}},
	}
}

func TestRouteDeploy_L4_CreateTCPWithBTP_AppliesBackendTrafficPolicy(t *testing.T) {
	f := newL4Fixture(t)
	route := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusApproved)
	f.withBTP(route, tcpBTPConfig())
	f.expectDeploy(route, models.ApprovalActionCreate)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)

	var order []string
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	f.k8s.On("CreateTCPRoute", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { order = append(order, "route") }).Return(nil)
	var btp *kubernetes.BackendTrafficPolicyConfig
	f.k8s.On("UpdateBackendTrafficPolicy", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig")).
		Run(func(args mock.Arguments) {
			order = append(order, "btp")
			btp = args.Get(2).(*kubernetes.BackendTrafficPolicyConfig)
		}).Return(nil)

	_, err := f.svc.Deploy(route.ID, uuid.New())
	require.NoError(t, err)

	require.NotNil(t, btp)
	assert.Equal(t, []string{"route", "btp"}, order, "the policy is applied after the route it targets")
	assert.Equal(t, f.stream.Namespace, btp.Namespace, "the policy lives in the stream namespace, next to the TCPRoute")
	assert.Equal(t, "TCPRoute", btp.TargetRef.Kind)
	assert.Equal(t, route.K8sRouteName, btp.TargetRef.Name)
	require.NotNil(t, btp.CircuitBreaker)
	assert.Equal(t, int64(100), *btp.CircuitBreaker.MaxConnections)
	require.NotNil(t, btp.Timeout)
	require.NotNil(t, btp.Timeout.TCP)
	assert.Equal(t, "5s", btp.Timeout.TCP.ConnectTimeout)
}

func TestRouteDeploy_L4_UpdateUDPWithBTP_AppliesLoadBalancerOnly(t *testing.T) {
	f := newL4Fixture(t)
	route := f.route("dns", models.RouteProtocolUDP, 53, models.RouteStatusPendingDeploy)
	f.withBTP(route, models.BackendTrafficPolicyConfig{
		LoadBalancer: &models.LoadBalancerConfig{Type: models.LoadBalancerTypeRoundRobin},
		Timeout:      &models.BTPTimeoutConfig{TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"}},
	})
	f.expectDeploy(route, models.ApprovalActionUpdate)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	f.k8s.On("UpdateUDPRoute", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	var btp *kubernetes.BackendTrafficPolicyConfig
	f.k8s.On("UpdateBackendTrafficPolicy", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig")).
		Run(func(args mock.Arguments) { btp = args.Get(2).(*kubernetes.BackendTrafficPolicyConfig) }).Return(nil)

	_, err := f.svc.Deploy(route.ID, uuid.New())
	require.NoError(t, err)

	require.NotNil(t, btp)
	assert.Equal(t, f.stream.Namespace, btp.Namespace)
	assert.Equal(t, "UDPRoute", btp.TargetRef.Kind)
	require.NotNil(t, btp.LoadBalancer)
	assert.Nil(t, btp.Timeout, "UDP carries the load balancer only")
}

func TestRouteDeploy_L4_WithoutBTP_AppliesNone(t *testing.T) {
	for _, action := range []models.ApprovalAction{models.ApprovalActionCreate, models.ApprovalActionUpdate} {
		t.Run(string(action), func(t *testing.T) {
			f := newL4Fixture(t)
			route := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusApproved)
			f.expectDeploy(route, action)
			f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)
			f.routeRepo.On("Update", mock.Anything).Return(nil)
			f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			f.k8s.On("CreateTCPRoute", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			f.k8s.On("UpdateTCPRoute", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

			_, err := f.svc.Deploy(route.ID, uuid.New())
			require.NoError(t, err)
			f.k8s.AssertNotCalled(t, "UpdateBackendTrafficPolicy", mock.Anything, mock.Anything, mock.Anything)
			f.k8s.AssertNotCalled(t, "CreateBackendTrafficPolicy", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestRouteDeploy_L4_BTPApplyFailureFailsDeploy(t *testing.T) {
	f := newL4Fixture(t)
	route := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusApproved)
	f.withBTP(route, tcpBTPConfig())
	f.expectDeploy(route, models.ApprovalActionCreate)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	f.k8s.On("CreateTCPRoute", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	f.k8s.On("UpdateBackendTrafficPolicy", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("boom"))

	_, err := f.svc.Deploy(route.ID, uuid.New())
	require.Error(t, err)
	f.routeRepo.AssertNotCalled(t, "Update", mock.Anything)
	assert.Equal(t, models.RouteStatusApproved, route.Status)
}

func TestRouteDeploy_L4_DeleteRemovesBackendTrafficPolicy(t *testing.T) {
	f := newL4Fixture(t)
	removing := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusPendingDeploy)
	policy := f.withBTP(removing, tcpBTPConfig())
	f.expectDeploy(removing, models.ApprovalActionDelete)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{*removing}, nil)
	f.approvalRepo.On("DeleteByEntityID", models.ApprovalEntityRoute, removing.ID).Return(nil)
	f.routeRepo.On("Delete", removing.ID).Return(nil)
	f.btpRepo.On("Delete", policy.ID).Return(nil)
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	f.k8s.On("DeleteTCPRoute", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	f.k8s.On("DeleteBackendTrafficPolicy", mock.Anything, f.stream.ProjectID, f.stream.Namespace, kubernetes.BackendTrafficPolicyName(removing.K8sRouteName)).Return(nil)

	_, err := f.svc.Deploy(removing.ID, uuid.New())
	require.NoError(t, err)
	f.k8s.AssertCalled(t, "DeleteBackendTrafficPolicy", mock.Anything, f.stream.ProjectID, f.stream.Namespace, kubernetes.BackendTrafficPolicyName(removing.K8sRouteName))
	f.btpRepo.AssertCalled(t, "Delete", policy.ID)
}

func TestRouteDeploy_L4_DeleteWithoutBTP_DeletesNone(t *testing.T) {
	f := newL4Fixture(t)
	removing := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusPendingDeploy)
	f.expectDeploy(removing, models.ApprovalActionDelete)
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{*removing}, nil)
	f.approvalRepo.On("DeleteByEntityID", models.ApprovalEntityRoute, removing.ID).Return(nil)
	f.routeRepo.On("Delete", removing.ID).Return(nil)
	f.k8s.On("UpdateGateway", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	f.k8s.On("DeleteTCPRoute", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	_, err := f.svc.Deploy(removing.ID, uuid.New())
	require.NoError(t, err)
	f.k8s.AssertNotCalled(t, "DeleteBackendTrafficPolicy", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

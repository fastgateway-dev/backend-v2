package services_test

import (
	"encoding/json"
	"errors"
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

// l4ApprovalFixture is an ApprovalService whose L4 renderer is a real
// RouteService (so the diff is the deploy builders' output) and whose stream
// reader is a mock.
type l4ApprovalFixture struct {
	svc          *services.ApprovalService
	approvalRepo *mocks.MockUnifiedApprovalRepository
	routeRepo    *mocks.MockRouteRepository
	streams      *mocks.MockApprovalStreamReader
	stream       *models.Stream
}

func newL4ApprovalFixture(t *testing.T) *l4ApprovalFixture {
	t.Helper()
	renderer, _, _, _, _, _ := newTestRouteService()
	f := &l4ApprovalFixture{
		approvalRepo: new(mocks.MockUnifiedApprovalRepository),
		routeRepo:    new(mocks.MockRouteRepository),
		streams:      new(mocks.MockApprovalStreamReader),
		stream: &models.Stream{ID: uuid.New(), ProjectID: uuid.New(), Name: "test-stream", Namespace: "edge",
			K8sGatewayName: "str-test-stream", K8sGatewayClass: "shared-envoy-clusterip"},
	}
	f.svc = services.NewApprovalService(services.ApprovalServiceDeps{
		ApprovalRepo: f.approvalRepo,
		PolicyRepo:   new(mocks.MockApprovalPolicyRepository),
		RouteRepo:    f.routeRepo,
		DomainRepo:   new(mocks.MockDomainRepository), // bare: an L4 diff must never read a domain
		Approvals:    newApprovalEngine(f.approvalRepo, nil, nil, f.routeRepo),
		Streams:      f.streams,
		L4Manifests:  renderer,
	})
	f.streams.On("GetByID", f.stream.ID).Return(f.stream, nil).Maybe()
	return f
}

func (f *l4ApprovalFixture) route(port int) *models.Route {
	sid := f.stream.ID
	r := &models.Route{ID: uuid.New(), StreamID: &sid, Name: "postgres", K8sRouteName: "postgres-abcd1234",
		Protocol: models.RouteProtocolTCP, Status: models.RouteStatusPendingUpdate,
		Config: l4Config(port)}
	f.routeRepo.On("GetByID", r.ID).Return(r, nil)
	return r
}

func l4Config(port int) models.RouteConfig {
	return models.RouteConfig{ListenerPort: port, Backends: []models.RouteBackend{
		{Type: models.BackendTypeKubernetes, Service: "pg", Namespace: "default", Port: 5432},
	}}
}

func snapshotJSON(t *testing.T, cfg *models.RouteConfig, btp *models.BackendTrafficPolicyConfig) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(models.RouteApprovalSnapshot{RouteConfig: cfg, BackendTrafficPolicy: btp})
	require.NoError(t, err)
	return raw
}

func (f *l4ApprovalFixture) approval(route *models.Route, action models.ApprovalAction, snapshot, previous json.RawMessage) uuid.UUID {
	id := uuid.New()
	f.approvalRepo.On("GetByID", id).Return(&models.Approval{
		ID: id, EntityType: models.ApprovalEntityRoute, EntityID: route.ID, Action: action,
		ConfigSnapshot: snapshot, PreviousConfig: previous, ChangeDescription: "bump port",
	}, nil)
	return id
}

func TestApprovalService_GetDiff_L4Create(t *testing.T) {
	f := newL4ApprovalFixture(t)
	route := f.route(5432)
	cfg, btp := l4Config(5432), tcpBTPConfig()
	id := f.approval(route, models.ApprovalActionCreate, snapshotJSON(t, &cfg, &btp), nil)

	diff, err := f.svc.GetDiff(id)
	require.NoError(t, err, "an L4 diff is a 200, not a 500")

	assert.Equal(t, "create", diff.Action)
	assert.Equal(t, "TCPRoute", diff.RouteKind)
	assert.Empty(t, diff.CurrentYAML)
	assert.Contains(t, diff.ProposedYAML, "kind: TCPRoute")
	assert.Contains(t, diff.ProposedYAML, "sectionName: l4-tcp-5432")
	assert.Contains(t, diff.ProposedYAML, "name: str-test-stream")
	assert.Contains(t, diff.ProposedBackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
	assert.Contains(t, diff.ProposedBackendTrafficPolicyYAML, "namespace: edge")
	assert.Empty(t, diff.CurrentBackendTrafficPolicyYAML)
	assert.Equal(t, "bump port", diff.ChangeDescription)
}

func TestApprovalService_GetDiff_L4Update_CurrentFromPreviousSnapshot(t *testing.T) {
	f := newL4ApprovalFixture(t)
	// The route row already carries the proposed port (as after approval);
	// current must come from the previous snapshot, not the row.
	route := f.route(6432)
	prevCfg, prevBTP := l4Config(5432), tcpBTPConfig()
	newCfg := l4Config(6432)
	id := f.approval(route, models.ApprovalActionUpdate, snapshotJSON(t, &newCfg, nil), snapshotJSON(t, &prevCfg, &prevBTP))

	diff, err := f.svc.GetDiff(id)
	require.NoError(t, err)

	assert.Equal(t, "TCPRoute", diff.RouteKind)
	assert.Contains(t, diff.CurrentYAML, "sectionName: l4-tcp-5432")
	assert.Contains(t, diff.ProposedYAML, "sectionName: l4-tcp-6432")
	assert.Contains(t, diff.CurrentBackendTrafficPolicyYAML, "maxConnections: 100", "the policy the update removes")
	assert.Empty(t, diff.ProposedBackendTrafficPolicyYAML)
}

func TestApprovalService_GetDiff_L4Delete_CurrentFromLiveRoute(t *testing.T) {
	f := newL4ApprovalFixture(t)
	route := f.route(5432)
	route.Protocol = models.RouteProtocolUDP
	btp := models.BackendTrafficPolicyConfig{LoadBalancer: &models.LoadBalancerConfig{Type: models.LoadBalancerTypeRoundRobin}}
	cfg := route.Config
	id := f.approval(route, models.ApprovalActionDelete, snapshotJSON(t, &cfg, &btp), nil)

	diff, err := f.svc.GetDiff(id)
	require.NoError(t, err)

	assert.Equal(t, "delete", diff.Action)
	assert.Equal(t, "UDPRoute", diff.RouteKind)
	assert.Contains(t, diff.CurrentYAML, "kind: UDPRoute")
	assert.Contains(t, diff.CurrentBackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
	assert.Empty(t, diff.ProposedYAML)
	assert.Empty(t, diff.ProposedBackendTrafficPolicyYAML)
}

func TestApprovalService_GetDiff_L4StreamLookupFailureIsAnError(t *testing.T) {
	f := newL4ApprovalFixture(t)
	missing := uuid.New()
	r := &models.Route{ID: uuid.New(), StreamID: &missing, Protocol: models.RouteProtocolTCP, Config: l4Config(5432)}
	f.routeRepo.On("GetByID", r.ID).Return(r, nil)
	f.streams.On("GetByID", missing).Return(nil, errors.New("record not found"))
	cfg := l4Config(5432)
	id := f.approval(r, models.ApprovalActionCreate, snapshotJSON(t, &cfg, nil), nil)

	diff, err := f.svc.GetDiff(id)
	assert.Nil(t, diff, "never a partial diff")
	assert.ErrorContains(t, err, "failed to load stream for L4 route")
}

func TestApprovalService_GetDiff_RouteKindForDomainRoutes(t *testing.T) {
	for proto, kind := range map[models.RouteProtocol]string{
		models.RouteProtocolHTTP: "HTTPRoute",
		models.RouteProtocolGRPC: "GRPCRoute",
	} {
		approvalRepo, routeRepo, domainRepo := new(mocks.MockUnifiedApprovalRepository), new(mocks.MockRouteRepository), new(mocks.MockDomainRepository)
		svc := newTestApprovalService(approvalRepo, nil, nil, routeRepo, nil, domainRepo, routeplan.WAFConfig{})
		domainID, routeID, approvalID := uuid.New(), uuid.New(), uuid.New()
		cfg := models.RouteConfig{Matches: []models.RouteMatch{{Path: &models.PathMatch{Value: "/x"}}}}
		approvalRepo.On("GetByID", approvalID).Return(&models.Approval{ID: approvalID, EntityType: models.ApprovalEntityRoute,
			EntityID: routeID, Action: models.ApprovalActionCreate, ConfigSnapshot: snapshotJSON(t, &cfg, nil)}, nil)
		routeRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, DomainID: &domainID, Protocol: proto, K8sRouteName: "r"}, nil)
		domainRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, Hostname: "example.com", Namespace: "fastgateway-system"}, nil)

		diff, err := svc.GetDiff(approvalID)
		require.NoError(t, err)
		assert.Equal(t, kind, diff.RouteKind)
	}
}

func TestApprovalService_ListByProjectID_EnrichesStream(t *testing.T) {
	f := newL4ApprovalFixture(t)
	projectID, domainID := uuid.New(), uuid.New()
	sid := f.stream.ID
	l4, l7 := uuid.New(), uuid.New()
	f.approvalRepo.On("ListByProjectID", projectID, 1, 20, "", "").Return([]models.Approval{
		{ID: uuid.New(), EntityType: models.ApprovalEntityRoute, EntityID: l4},
		{ID: uuid.New(), EntityType: models.ApprovalEntityRoute, EntityID: l7},
	}, int64(2), nil)
	f.routeRepo.On("GetByIDs", mock.AnythingOfType("[]uuid.UUID")).Return([]models.Route{
		{ID: l4, Name: "postgres", StreamID: &sid, Protocol: models.RouteProtocolTCP},
		{ID: l7, Name: "api", DomainID: &domainID},
	}, nil)
	f.streams.On("GetByIDs", []uuid.UUID{sid}).Return([]models.Stream{*f.stream}, nil)

	// The fixture's domain repo is bare; this one answers the batch lookup the list does.
	svc := services.NewApprovalService(services.ApprovalServiceDeps{
		ApprovalRepo: f.approvalRepo, PolicyRepo: new(mocks.MockApprovalPolicyRepository), RouteRepo: f.routeRepo,
		DomainRepo: domainRepoWithHost(domainID, "api.example.com"), Approvals: newApprovalEngine(f.approvalRepo, nil, nil, f.routeRepo),
		Streams: f.streams, L4Manifests: new(mocks.MockL4ManifestRenderer),
	})
	got, _, err := svc.ListByProjectID(projectID, 1, 20, "", "")
	require.NoError(t, err)

	require.NotNil(t, got[0].StreamID)
	assert.Equal(t, sid, *got[0].StreamID)
	assert.Equal(t, "test-stream", got[0].StreamName)
	assert.Equal(t, "postgres", got[0].EntityName)
	assert.Empty(t, got[0].DomainName)
	assert.Nil(t, got[1].StreamID)
	assert.Equal(t, "api.example.com", got[1].DomainName)
	f.streams.AssertNumberOfCalls(t, "GetByIDs", 1)
}

func domainRepoWithHost(id uuid.UUID, host string) *mocks.MockDomainRepository {
	repo := new(mocks.MockDomainRepository)
	repo.On("GetByIDs", mock.AnythingOfType("[]uuid.UUID")).Return([]models.Domain{{ID: id, Hostname: host}}, nil)
	return repo
}

func TestApprovalService_GetByID_EnrichesStream(t *testing.T) {
	f := newL4ApprovalFixture(t)
	route := f.route(5432)
	id := uuid.New()
	f.approvalRepo.On("GetByID", id).Return(&models.Approval{ID: id, EntityType: models.ApprovalEntityRoute, EntityID: route.ID}, nil)

	got, err := f.svc.GetByID(id)
	require.NoError(t, err)
	require.NotNil(t, got.StreamID)
	assert.Equal(t, f.stream.ID, *got.StreamID)
	assert.Equal(t, "test-stream", got.StreamName)
}

func TestApproval_StreamFields_JSONContract(t *testing.T) {
	sid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	raw, err := json.Marshal(models.Approval{StreamID: &sid, StreamName: "test-stream"})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", got["streamId"])
	assert.Equal(t, "test-stream", got["streamName"])

	raw, err = json.Marshal(models.Approval{})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "streamId", "omitted for L7 and non-route approvals")
	assert.NotContains(t, string(raw), "streamName")

	raw, err = json.Marshal(services.ApprovalDiffResult{Action: "create", RouteKind: "TCPRoute"})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"routeKind":"TCPRoute"`)
}

func TestApprovalService_GetDiff_L4CorruptSnapshotIsAnError(t *testing.T) {
	f := newL4ApprovalFixture(t)
	route := f.route(5432)
	id := f.approval(route, models.ApprovalActionCreate, json.RawMessage(`{"routeConfig":`), nil)

	diff, err := f.svc.GetDiff(id)
	assert.Nil(t, diff, "never a partial diff")
	assert.Error(t, err)
}

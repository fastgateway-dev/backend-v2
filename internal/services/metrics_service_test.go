package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// Test doubles
// -----------------------------------------------------------------------------

// fakePromClient is a hand-rolled stub for PromQueryClient. Reused by Tasks 5/6/7.
type fakePromClient struct {
	instantErr error
	instantRes *PromInstantResult
	// instantResponses maps query substring → response; checked before instantRes.
	instantResponses map[string]*PromInstantResult
	// instantQueries records every instant query issued, for PromQL assertions.
	mu             sync.Mutex
	instantQueries []string

	rangeErr error
	// rangeResponses maps query substring → response
	rangeResponses map[string]*PromRangeResult
}

func (f *fakePromClient) QueryInstant(ctx context.Context, query string) (*PromInstantResult, error) {
	f.mu.Lock()
	f.instantQueries = append(f.instantQueries, query)
	f.mu.Unlock()
	if f.instantErr != nil {
		return nil, f.instantErr
	}
	for sub, res := range f.instantResponses {
		if strings.Contains(query, sub) {
			return res, nil
		}
	}
	return f.instantRes, nil
}

func (f *fakePromClient) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*PromRangeResult, error) {
	if f.rangeErr != nil {
		return nil, f.rangeErr
	}
	for sub, res := range f.rangeResponses {
		if strings.Contains(query, sub) {
			return res, nil
		}
	}
	return &PromRangeResult{}, nil
}

// metricsTestProjectRepo is a local stub satisfying repository.ProjectRepositoryInterface.
//
// We can't use mocks.MockProjectRepository here: the mocks package imports
// services (for mock_services.go), so importing mocks from a *_test.go file
// inside `package services` would create a test-time import cycle.
type metricsTestProjectRepo struct {
	mock.Mock
}

func (m *metricsTestProjectRepo) Create(project *models.Project) error {
	args := m.Called(project)
	return args.Error(0)
}

func (m *metricsTestProjectRepo) GetByID(id uuid.UUID) (*models.Project, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

func (m *metricsTestProjectRepo) GetByIDWithCounts(id uuid.UUID) (*models.Project, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

func (m *metricsTestProjectRepo) List(page, limit int) ([]models.Project, int64, error) {
	args := m.Called(page, limit)
	return args.Get(0).([]models.Project), args.Get(1).(int64), args.Error(2)
}

func (m *metricsTestProjectRepo) ListByUserAccess(userID uuid.UUID, userRole models.UserRole, page, limit int, search string, labels map[string]string) ([]models.Project, int64, error) {
	args := m.Called(userID, userRole, page, limit, search, labels)
	return args.Get(0).([]models.Project), args.Get(1).(int64), args.Error(2)
}

func (m *metricsTestProjectRepo) Update(project *models.Project) error {
	args := m.Called(project)
	return args.Error(0)
}

func (m *metricsTestProjectRepo) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *metricsTestProjectRepo) AddAdmin(projectID, userID uuid.UUID) error {
	args := m.Called(projectID, userID)
	return args.Error(0)
}

func (m *metricsTestProjectRepo) RemoveAdmin(projectID, userID uuid.UUID) error {
	args := m.Called(projectID, userID)
	return args.Error(0)
}

func (m *metricsTestProjectRepo) ListAdmins(projectID uuid.UUID) ([]models.User, error) {
	args := m.Called(projectID)
	return args.Get(0).([]models.User), args.Error(1)
}

func (m *metricsTestProjectRepo) IsAdmin(projectID, userID uuid.UUID) (bool, error) {
	args := m.Called(projectID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *metricsTestProjectRepo) Count() (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

func (m *metricsTestProjectRepo) FindByConnectionType(connectionType string) (*models.Project, error) {
	args := m.Called(connectionType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Project), args.Error(1)
}

// -----------------------------------------------------------------------------
// TestConnection tests
// -----------------------------------------------------------------------------

func newMetricsServiceForTest(fake *fakePromClient) (*MetricsService, *metricsTestProjectRepo) {
	repo := &metricsTestProjectRepo{}
	svc := &MetricsService{
		projectRepo: repo,
		config:      &config.Config{EncryptionKey: "0123456789abcdef0123456789abcdef"},
		clientFactory: func(p *models.Project, key string) (PromQueryClient, error) {
			return fake, nil
		},
	}
	return svc, repo
}

func TestMetricsService_TestConnection_Success(t *testing.T) {
	svc, repo := newMetricsServiceForTest(&fakePromClient{
		instantRes: &PromInstantResult{Samples: []PromSample{{Value: 1}}},
	})

	projectID := uuid.New()
	repo.On("GetByID", projectID).Return(&models.Project{
		ID:                 projectID,
		MetricsEndpointURL: "http://prom:9090",
		MetricsAuthType:    "none",
	}, nil)

	res, err := svc.TestConnection(context.Background(), projectID)
	require.NoError(t, err)
	assert.True(t, res.OK)
	assert.Empty(t, res.Error)
}

func TestMetricsService_TestConnection_NotConfigured(t *testing.T) {
	svc, repo := newMetricsServiceForTest(&fakePromClient{})
	projectID := uuid.New()
	repo.On("GetByID", projectID).Return(&models.Project{ID: projectID}, nil)

	res, err := svc.TestConnection(context.Background(), projectID)
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Error, "not configured")
}

func TestMetricsService_TestConnection_PromError(t *testing.T) {
	svc, repo := newMetricsServiceForTest(&fakePromClient{
		instantErr: errors.New("prom http 401: unauthorized"),
	})
	projectID := uuid.New()
	repo.On("GetByID", projectID).Return(&models.Project{
		ID:                 projectID,
		MetricsEndpointURL: "http://prom:9090",
		MetricsAuthType:    "bearer",
	}, nil)

	res, err := svc.TestConnection(context.Background(), projectID)
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Error, "401")
}

// -----------------------------------------------------------------------------
// GetRouteMetrics tests
// -----------------------------------------------------------------------------

// metricsTestRouteRepo is a local stub satisfying repository.RouteRepositoryInterface.
// Lives here for the same reason as metricsTestProjectRepo:
// backend/internal/mocks depends on internal/services, so test files in
// package services cannot import internal/mocks without an import cycle.
type metricsTestRouteRepo struct{ mock.Mock }

func (m *metricsTestRouteRepo) Create(route *models.Route) error {
	args := m.Called(route)
	return args.Error(0)
}

func (m *metricsTestRouteRepo) GetByID(id uuid.UUID) (*models.Route, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *metricsTestRouteRepo) GetByIDs(ids []uuid.UUID) ([]models.Route, error) {
	args := m.Called(ids)
	return args.Get(0).([]models.Route), args.Error(1)
}

func (m *metricsTestRouteRepo) GetByIDWithApproval(id uuid.UUID) (*models.Route, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Route), args.Error(1)
}

func (m *metricsTestRouteRepo) ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error) {
	args := m.Called(domainID, page, limit, teamID, status, search, searchField, labels)
	return args.Get(0).([]models.Route), args.Get(1).(int64), args.Error(2)
}

func (m *metricsTestRouteRepo) ListByProjectID(projectID uuid.UUID, page, limit int, filters repository.RouteListFilters) ([]models.Route, int64, error) {
	args := m.Called(projectID, page, limit, filters)
	return args.Get(0).([]models.Route), args.Get(1).(int64), args.Error(2)
}

func (m *metricsTestRouteRepo) Update(route *models.Route) error {
	args := m.Called(route)
	return args.Error(0)
}

func (m *metricsTestRouteRepo) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *metricsTestRouteRepo) ExistsByName(domainID uuid.UUID, name string) (bool, error) {
	args := m.Called(domainID, name)
	return args.Bool(0), args.Error(1)
}

func (m *metricsTestRouteRepo) ExistsByStreamAndName(streamID uuid.UUID, name string) (bool, error) {
	args := m.Called(streamID, name)
	return args.Bool(0), args.Error(1)
}

func (m *metricsTestRouteRepo) ListByStreamID(streamID uuid.UUID, page, limit int, teamID *uuid.UUID, status string) ([]models.Route, int64, error) {
	args := m.Called(streamID, page, limit, teamID, status)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]models.Route), args.Get(1).(int64), args.Error(2)
}

func (m *metricsTestRouteRepo) GetActiveRoutesByDomainID(domainID uuid.UUID) ([]models.Route, error) {
	args := m.Called(domainID)
	return args.Get(0).([]models.Route), args.Error(1)
}

func (m *metricsTestRouteRepo) CountByDomainID(domainID uuid.UUID) (int, error) {
	args := m.Called(domainID)
	return args.Int(0), args.Error(1)
}

func (m *metricsTestRouteRepo) CountByStreamID(streamID uuid.UUID) (int64, error) {
	args := m.Called(streamID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *metricsTestRouteRepo) ListActiveByStreamID(streamID uuid.UUID) ([]models.Route, error) {
	args := m.Called(streamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Route), args.Error(1)
}

// metricsTestDomainRepo is a local stub satisfying repository.DomainRepositoryInterface.
type metricsTestDomainRepo struct{ mock.Mock }

func (m *metricsTestDomainRepo) Create(domain *models.Domain) error {
	args := m.Called(domain)
	return args.Error(0)
}

func (m *metricsTestDomainRepo) GetByID(id uuid.UUID) (*models.Domain, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Domain), args.Error(1)
}

func (m *metricsTestDomainRepo) GetByIDs(ids []uuid.UUID) ([]models.Domain, error) {
	args := m.Called(ids)
	return args.Get(0).([]models.Domain), args.Error(1)
}

func (m *metricsTestDomainRepo) ListByProjectID(projectID uuid.UUID, page, limit int, search string, status string, labels map[string]string) ([]models.Domain, int64, error) {
	args := m.Called(projectID, page, limit, search, status, labels)
	return args.Get(0).([]models.Domain), args.Get(1).(int64), args.Error(2)
}

func (m *metricsTestDomainRepo) ListByManagedCertificateID(uuid.UUID) ([]models.Domain, error) {
	return nil, nil
}

func (m *metricsTestDomainRepo) ListByManagedCertificateIDs([]uuid.UUID) ([]models.Domain, error) {
	return nil, nil
}

func (m *metricsTestDomainRepo) Update(domain *models.Domain) error {
	args := m.Called(domain)
	return args.Error(0)
}

func (m *metricsTestDomainRepo) Delete(id uuid.UUID) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *metricsTestDomainRepo) ExistsByHostname(projectID uuid.UUID, hostname string) (bool, error) {
	args := m.Called(projectID, hostname)
	return args.Bool(0), args.Error(1)
}

func (m *metricsTestDomainRepo) ListByTemplateID(templateID uuid.UUID) ([]models.Domain, error) {
	args := m.Called(templateID)
	return args.Get(0).([]models.Domain), args.Error(1)
}

func (m *metricsTestDomainRepo) CountByProjectID(projectID uuid.UUID) (int, error) {
	args := m.Called(projectID)
	return args.Int(0), args.Error(1)
}

func newMetricsServiceWithRoute(fake *fakePromClient) (*MetricsService, *metricsTestProjectRepo, *metricsTestRouteRepo, *metricsTestDomainRepo) {
	svc, pRepo := newMetricsServiceForTest(fake)
	rRepo := &metricsTestRouteRepo{}
	dRepo := &metricsTestDomainRepo{}
	svc.routeRepo = rRepo
	svc.domainRepo = dRepo
	return svc, pRepo, rRepo, dRepo
}

func TestMetricsService_GetRouteMetrics_Success(t *testing.T) {
	fake := &fakePromClient{
		rangeResponses: map[string]*PromRangeResult{
			`envoy_response_code_class="2"`: {Series: []PromSeries{{Points: []PromPoint{
				{Time: time.Unix(1712923200, 0), Value: 80.0},
				{Time: time.Unix(1712923230, 0), Value: 82.0},
			}}}},
			`envoy_response_code_class="4"`: {Series: []PromSeries{{Points: []PromPoint{
				{Time: time.Unix(1712923200, 0), Value: 1.0},
			}}}},
			`envoy_response_code_class="5"`: {Series: []PromSeries{{Points: []PromPoint{
				{Time: time.Unix(1712923200, 0), Value: 0.5},
			}}}},
			`histogram_quantile(0.5`: {Series: []PromSeries{{Points: []PromPoint{
				{Time: time.Unix(1712923200, 0), Value: 12.0},
			}}}},
			`histogram_quantile(0.95`: {Series: []PromSeries{{Points: []PromPoint{
				{Time: time.Unix(1712923200, 0), Value: 48.0},
			}}}},
			`histogram_quantile(0.99`: {Series: []PromSeries{{Points: []PromPoint{
				{Time: time.Unix(1712923200, 0), Value: 120.0},
			}}}},
		},
	}
	svc, pRepo, rRepo, dRepo := newMetricsServiceWithRoute(fake)

	projectID := uuid.New()
	routeID := uuid.New()
	domainID := uuid.New()

	pRepo.On("GetByID", projectID).Return(&models.Project{
		ID:                 projectID,
		MetricsEndpointURL: "http://prom:9090",
		MetricsAuthType:    "none",
	}, nil)
	rRepo.On("GetByID", routeID).Return(&models.Route{
		ID:       routeID,
		Name:     "api-users",
		DomainID: &domainID,
	}, nil)
	dRepo.On("GetByID", domainID).Return(&models.Domain{
		ID:        domainID,
		Namespace: "fastgateway-system",
	}, nil)

	res, err := svc.GetRouteMetrics(context.Background(), projectID, routeID, "1h")
	require.NoError(t, err)
	assert.Equal(t, "30s", res.TimeRange.Step)
	assert.Greater(t, res.TotalRequests, 0.0)
	require.NotEmpty(t, res.Latency.P95)
	assert.Greater(t, res.Latency.P95[0].Value, 0.0)
	assert.NotEmpty(t, res.Rps.Class2xx)
}

// An L4 route has a Stream and no Domain: route metrics are not available for
// it, and GetRouteMetrics must say so instead of dereferencing its nil DomainID.
func TestMetricsService_GetRouteMetrics_L4Route_NotAvailable(t *testing.T) {
	svc, pRepo, rRepo, _ := newMetricsServiceWithRoute(&fakePromClient{})

	projectID, routeID, streamID := uuid.New(), uuid.New(), uuid.New()
	pRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, MetricsEndpointURL: "http://prom:9090", MetricsAuthType: "none"}, nil)
	rRepo.On("GetByID", routeID).Return(&models.Route{
		ID: routeID, Name: "pg", StreamID: &streamID, Protocol: models.RouteProtocolTCP,
	}, nil)

	require.NotPanics(t, func() {
		res, err := svc.GetRouteMetrics(context.Background(), projectID, routeID, "1h")
		assert.Nil(t, res)
		assert.ErrorIs(t, err, ErrMetricsNotAvailableForL4)
	})
}

func TestMetricsService_GetRouteMetrics_InvalidRange(t *testing.T) {
	svc, _, _, _ := newMetricsServiceWithRoute(&fakePromClient{})
	_, err := svc.GetRouteMetrics(context.Background(), uuid.New(), uuid.New(), "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid range")
}

func TestMetricsService_GetRouteMetrics_ProjectNotConfigured(t *testing.T) {
	svc, pRepo, rRepo, dRepo := newMetricsServiceWithRoute(&fakePromClient{})

	projectID := uuid.New()
	routeID := uuid.New()
	domainID := uuid.New()

	pRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID}, nil)
	rRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, Name: "x", DomainID: &domainID}, nil)
	dRepo.On("GetByID", domainID).Return(&models.Domain{ID: domainID, Namespace: "fastgateway-system"}, nil)

	_, err := svc.GetRouteMetrics(context.Background(), projectID, routeID, "1h")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// -----------------------------------------------------------------------------
// GetDomainMetrics tests
// -----------------------------------------------------------------------------

func TestMetricsService_GetDomainMetrics_Success(t *testing.T) {
	// Domain-wide RPS 2xx: 200, 4xx: 1, 5xx: 2
	// Per-route top-5 query returns two routes with values
	fake := &fakePromClient{
		rangeResponses: map[string]*PromRangeResult{
			`envoy_response_code_class="2"`: {Series: []PromSeries{{Points: []PromPoint{{Time: time.Unix(1712923200, 0), Value: 200}}}}},
			`envoy_response_code_class="4"`: {Series: []PromSeries{{Points: []PromPoint{{Time: time.Unix(1712923200, 0), Value: 1}}}}},
			`envoy_response_code_class="5"`: {Series: []PromSeries{{Points: []PromPoint{{Time: time.Unix(1712923200, 0), Value: 2}}}}},
			`histogram_quantile(0.5`:        {Series: []PromSeries{{Points: []PromPoint{{Time: time.Unix(1712923200, 0), Value: 10}}}}},
			`histogram_quantile(0.95`:       {Series: []PromSeries{{Points: []PromPoint{{Time: time.Unix(1712923200, 0), Value: 40}}}}},
			`histogram_quantile(0.99`:       {Series: []PromSeries{{Points: []PromPoint{{Time: time.Unix(1712923200, 0), Value: 100}}}}},
		},
		instantRes: &PromInstantResult{
			Samples: []PromSample{
				{Labels: map[string]string{"envoy_cluster_name": "httproute/fastgateway-system/api-users/rule/0"}, Value: 200},
				{Labels: map[string]string{"envoy_cluster_name": "httproute/fastgateway-system/checkout/rule/0"}, Value: 50},
			},
		},
	}
	svc, pRepo, rRepo, dRepo := newMetricsServiceWithRoute(fake)

	projectID := uuid.New()
	domainID := uuid.New()
	routeID1 := uuid.New()
	routeID2 := uuid.New()

	pRepo.On("GetByID", projectID).Return(&models.Project{
		ID:                 projectID,
		MetricsEndpointURL: "http://prom:9090",
		MetricsAuthType:    "none",
	}, nil)
	dRepo.On("GetByID", domainID).Return(&models.Domain{
		ID:        domainID,
		Namespace: "fastgateway-system",
	}, nil)

	rRepo.On("ListByDomainID", domainID, 1, 10000, (*uuid.UUID)(nil), "", "", "", map[string]string(nil)).
		Return([]models.Route{
			{ID: routeID1, Name: "api-users", DomainID: &domainID},
			{ID: routeID2, Name: "checkout", DomainID: &domainID},
		}, int64(2), nil)

	res, err := svc.GetDomainMetrics(context.Background(), projectID, domainID, "1h")
	require.NoError(t, err)
	assert.Equal(t, "30s", res.TimeRange.Step)
	assert.Greater(t, res.TotalRequests, 0.0)
	require.NotEmpty(t, res.TopRoutesByRps)
	assert.Equal(t, "api-users", res.TopRoutesByRps[0].RouteName)
	assert.Equal(t, routeID1, res.TopRoutesByRps[0].RouteID)
	assert.Equal(t, float64(200), res.TopRoutesByRps[0].Value)
}

// -----------------------------------------------------------------------------
// StreamL4Metrics tests
// -----------------------------------------------------------------------------

// metricsTestStreamRepo stubs the stream lookup MetricsService uses.
type metricsTestStreamRepo struct{ mock.Mock }

func (m *metricsTestStreamRepo) GetByID(id uuid.UUID) (*models.Stream, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Stream), args.Error(1)
}

func newL4MetricsService(fake *fakePromClient) (*MetricsService, *metricsTestProjectRepo, *metricsTestRouteRepo, *metricsTestStreamRepo) {
	svc, pRepo, rRepo, _ := newMetricsServiceWithRoute(fake)
	sRepo := &metricsTestStreamRepo{}
	svc.SetStreamRepo(sRepo)
	return svc, pRepo, rRepo, sRepo
}

func vec(v float64) *PromInstantResult {
	return &PromInstantResult{Samples: []PromSample{{Value: v}}}
}

func TestMetricsService_StreamL4Metrics_Success(t *testing.T) {
	fake := &fakePromClient{
		instantRes: &PromInstantResult{}, // anything unmatched: no series
		instantResponses: map[string]*PromInstantResult{
			// pg-1a2b3c4d is the TCP listener; dns-5e6f7a8b the UDP one.
			`envoy_cluster_upstream_cx_active{envoy_cluster_name=~"tcproute/team-a/pg-1a2b3c4d/rule/.*"}`:             vec(7),
			`rate(envoy_tcp_downstream_cx_total{envoy_tcp_prefix=~"tcproute/team-a/pg-1a2b3c4d(/.*)?"}[5m])`:          vec(1.5),
			`rate(envoy_tcp_downstream_cx_rx_bytes_total{envoy_tcp_prefix=~"tcproute/team-a/pg-1a2b3c4d(/.*)?"}[5m])`: vec(2048),
			`rate(envoy_tcp_downstream_cx_tx_bytes_total{envoy_tcp_prefix=~"tcproute/team-a/pg-1a2b3c4d(/.*)?"}[5m])`: vec(4096),
			`envoy_udp_downstream_sess_active{envoy_udp_prefix=~"udproute/team-a/dns-5e6f7a8b(/.*)?"}`:                vec(3),
			`rate(envoy_udp_downstream_sess_total{envoy_udp_prefix=~"udproute/team-a/dns-5e6f7a8b(/.*)?"}[5m])`:       vec(0.5),
			`rate(envoy_udp_downstream_sess_rx_bytes{envoy_udp_prefix=~"udproute/team-a/dns-5e6f7a8b(/.*)?"}[5m])`:    vec(100),
			`rate(envoy_udp_downstream_sess_tx_bytes{envoy_udp_prefix=~"udproute/team-a/dns-5e6f7a8b(/.*)?"}[5m])`:    vec(200),
		},
	}
	svc, pRepo, rRepo, sRepo := newL4MetricsService(fake)

	projectID, streamID := uuid.New(), uuid.New()
	tcpID, udpID := uuid.New(), uuid.New()
	tcpPort, udpPort := 5432, 53
	pRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, MetricsEndpointURL: "http://prom:9090", MetricsAuthType: "none"}, nil)
	sRepo.On("GetByID", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Namespace: "team-a"}, nil)
	rRepo.On("ListActiveByStreamID", streamID).Return([]models.Route{
		{ID: tcpID, Name: "pg", K8sRouteName: "pg-1a2b3c4d", StreamID: &streamID, Protocol: models.RouteProtocolTCP, ListenerPort: &tcpPort},
		{ID: udpID, Name: "dns", K8sRouteName: "dns-5e6f7a8b", StreamID: &streamID, Protocol: models.RouteProtocolUDP, ListenerPort: &udpPort},
	}, nil)

	res, err := svc.StreamL4Metrics(context.Background(), projectID.String(), streamID.String())
	require.NoError(t, err)
	require.Len(t, res.Listeners, 2)

	byID := map[uuid.UUID]L4ListenerMetrics{}
	for _, l := range res.Listeners {
		byID[l.RouteID] = l
	}
	tcp := byID[tcpID]
	assert.Equal(t, "tcp", tcp.Protocol)
	assert.Equal(t, 5432, tcp.Port)
	assert.Equal(t, 7.0, tcp.ActiveConnections)
	assert.Equal(t, 1.5, tcp.ConnectionRate)
	assert.Equal(t, 2048.0, tcp.BytesIn)
	assert.Equal(t, 4096.0, tcp.BytesOut)

	udp := byID[udpID]
	assert.Equal(t, "udp", udp.Protocol)
	assert.Equal(t, 53, udp.Port)
	assert.Equal(t, 3.0, udp.ActiveConnections)
	assert.Equal(t, 0.5, udp.ConnectionRate)
	assert.Equal(t, 100.0, udp.BytesIn)
	assert.Equal(t, 200.0, udp.BytesOut)

	// Aggregate is the sum across listeners.
	assert.Equal(t, 10.0, res.ActiveConnections)
	assert.Equal(t, 2.0, res.ConnectionRate)
	assert.Equal(t, 2148.0, res.BytesIn)
	assert.Equal(t, 4296.0, res.BytesOut)
}

func TestMetricsService_StreamL4Metrics_NoSeriesIsZero(t *testing.T) {
	svc, pRepo, rRepo, sRepo := newL4MetricsService(&fakePromClient{instantRes: &PromInstantResult{}})

	projectID, streamID := uuid.New(), uuid.New()
	port := 5432
	pRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, MetricsEndpointURL: "http://prom:9090", MetricsAuthType: "none"}, nil)
	sRepo.On("GetByID", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Namespace: "team-a"}, nil)
	rRepo.On("ListActiveByStreamID", streamID).Return([]models.Route{
		{ID: uuid.New(), Name: "pg", StreamID: &streamID, Protocol: models.RouteProtocolTCP, ListenerPort: &port},
	}, nil)

	res, err := svc.StreamL4Metrics(context.Background(), projectID.String(), streamID.String())
	require.NoError(t, err)
	require.Len(t, res.Listeners, 1)
	assert.Zero(t, res.Listeners[0].ActiveConnections)
	assert.Zero(t, res.BytesOut)
}

func TestMetricsService_StreamL4Metrics_EmptyStream(t *testing.T) {
	svc, pRepo, rRepo, sRepo := newL4MetricsService(&fakePromClient{})
	projectID, streamID := uuid.New(), uuid.New()
	pRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, MetricsEndpointURL: "http://prom:9090", MetricsAuthType: "none"}, nil)
	sRepo.On("GetByID", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Namespace: "team-a"}, nil)
	rRepo.On("ListActiveByStreamID", streamID).Return([]models.Route{}, nil)

	res, err := svc.StreamL4Metrics(context.Background(), projectID.String(), streamID.String())
	require.NoError(t, err)
	assert.NotNil(t, res.Listeners)
	assert.Empty(t, res.Listeners)
}

func TestMetricsService_StreamL4Metrics_WrongProjectIsNotFound(t *testing.T) {
	svc, pRepo, _, sRepo := newL4MetricsService(&fakePromClient{})
	projectID, streamID := uuid.New(), uuid.New()
	pRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, MetricsEndpointURL: "http://prom:9090", MetricsAuthType: "none"}, nil)
	sRepo.On("GetByID", streamID).Return(&models.Stream{ID: streamID, ProjectID: uuid.New(), Namespace: "team-a"}, nil)

	_, err := svc.StreamL4Metrics(context.Background(), projectID.String(), streamID.String())
	assert.ErrorIs(t, err, ErrStreamNotFound)
}

func TestMetricsService_StreamL4Metrics_NotConfiguredAndBadIDs(t *testing.T) {
	svc, pRepo, _, _ := newL4MetricsService(&fakePromClient{})
	projectID := uuid.New()
	pRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID}, nil)

	_, err := svc.StreamL4Metrics(context.Background(), projectID.String(), uuid.NewString())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")

	_, err = svc.StreamL4Metrics(context.Background(), "nope", uuid.NewString())
	require.Error(t, err)
	_, err = svc.StreamL4Metrics(context.Background(), projectID.String(), "nope")
	require.Error(t, err)
}

func TestMetricsService_StreamL4Metrics_PromError(t *testing.T) {
	svc, pRepo, rRepo, sRepo := newL4MetricsService(&fakePromClient{instantErr: errors.New("prom http 500")})
	projectID, streamID := uuid.New(), uuid.New()
	port := 80
	pRepo.On("GetByID", projectID).Return(&models.Project{ID: projectID, MetricsEndpointURL: "http://prom:9090", MetricsAuthType: "none"}, nil)
	sRepo.On("GetByID", streamID).Return(&models.Stream{ID: streamID, ProjectID: projectID, Namespace: "team-a"}, nil)
	rRepo.On("ListActiveByStreamID", streamID).Return([]models.Route{
		{ID: uuid.New(), Name: "pg", StreamID: &streamID, Protocol: models.RouteProtocolTCP, ListenerPort: &port},
	}, nil)

	_, err := svc.StreamL4Metrics(context.Background(), projectID.String(), streamID.String())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

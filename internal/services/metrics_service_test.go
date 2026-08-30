package services

import (
	"context"
	"errors"
	"strings"
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

	rangeErr error
	// rangeResponses maps query substring → response
	rangeResponses map[string]*PromRangeResult
}

func (f *fakePromClient) QueryInstant(ctx context.Context, query string) (*PromInstantResult, error) {
	if f.instantErr != nil {
		return nil, f.instantErr
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

func (m *metricsTestRouteRepo) GetActiveRoutesByDomainID(domainID uuid.UUID) ([]models.Route, error) {
	args := m.Called(domainID)
	return args.Get(0).([]models.Route), args.Error(1)
}

func (m *metricsTestRouteRepo) CountByDomainID(domainID uuid.UUID) (int, error) {
	args := m.Called(domainID)
	return args.Int(0), args.Error(1)
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
		DomainID: domainID,
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
	rRepo.On("GetByID", routeID).Return(&models.Route{ID: routeID, Name: "x", DomainID: domainID}, nil)
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
			{ID: routeID1, Name: "api-users", DomainID: domainID},
			{ID: routeID2, Name: "checkout", DomainID: domainID},
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

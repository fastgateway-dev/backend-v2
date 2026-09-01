package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unusedRouteUpdater stands in for *RouteService as RouteVersionService's
// RouteUpdater. Phase 2E Task 5 made it a required constructor parameter;
// before that it arrived through SetRouteService, and every test below left
// it nil -- none of them exercises Rollback, the only method that reads it,
// so it must never be called and panics loudly if it is.
// Phase 2E Task 9 deleted the guard at route_version_service.go Rollback
// ("route service not configured"): it was unreachable once RouteUpdater
// became required.
type unusedRouteUpdater struct{}

func (unusedRouteUpdater) Update(uuid.UUID, *services.UpdateRouteInput, uuid.UUID) (*models.Route, error) {
	panic("unusedRouteUpdater.Update: no test in this file exercises Rollback")
}

// newTestRouteVersionService stands in for services.NewRouteVersionService
// now that every dependency is required (Phase 2E Task 3). Every test below
// built its RouteVersionService with just (versionRepo, routeRepo); none of
// them exercises CreateVersion or Rollback, the only methods that read
// SecurityPolicyRepo/BackendTrafficPolicyRepo/EnvoyExtensionPolicyRepo/
// WafPolicyRepo/RouteUpdater, so a bare mock standing in for each is never
// called.
func newTestRouteVersionService(
	versionRepo *mocks.MockRouteVersionRepository,
	routeRepo *mocks.MockRouteRepository,
) *services.RouteVersionService {
	return services.NewRouteVersionService(services.RouteVersionServiceDeps{
		VersionRepo:              versionRepo,
		RouteRepo:                routeRepo,
		SecurityPolicyRepo:       new(mocks.MockSecurityPolicyRepository),
		BackendTrafficPolicyRepo: new(mocks.MockBackendTrafficPolicyRepository),
		EnvoyExtensionPolicyRepo: new(mocks.MockEnvoyExtensionPolicyRepository),
		WafPolicyRepo:            new(mocks.MockWafPolicyRepository),
		RouteUpdater:             unusedRouteUpdater{},
	})
}

// TestRouteServiceVersionSeamIsNarrow pins the two interfaces that replaced
// RouteService.SetRouteVersionService and RouteVersionService.SetRouteService
// (Phase 2E Task 5). Both assertions are compile-time: they fail to build if
// either interface stops existing or the real type stops satisfying it.
func TestRouteServiceVersionSeamIsNarrow(t *testing.T) {
	var _ services.RouteUpdater = (*services.RouteService)(nil)
	var _ services.RouteVersionRecorder = (*services.RouteVersionService)(nil)
	var _ services.RouteUpdater = services.RouteUpdaterFunc(nil)
}

// ---------------------------------------------------------------------------
// ListVersions
// ---------------------------------------------------------------------------

func TestRouteVersionService_ListVersions_Success(t *testing.T) {
	versionRepo := new(mocks.MockRouteVersionRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestRouteVersionService(versionRepo, routeRepo)

	routeID := uuid.New()
	versions := []models.RouteVersion{
		{ID: uuid.New(), RouteID: routeID, Version: 1},
		{ID: uuid.New(), RouteID: routeID, Version: 2},
	}
	versionRepo.On("ListByRouteID", routeID, 1, 10).Return(versions, int64(2), nil)

	result, total, err := svc.ListVersions(routeID, 1, 10)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(2), total)
	versionRepo.AssertExpectations(t)
}

func TestRouteVersionService_ListVersions_Empty(t *testing.T) {
	versionRepo := new(mocks.MockRouteVersionRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestRouteVersionService(versionRepo, routeRepo)

	routeID := uuid.New()
	versionRepo.On("ListByRouteID", routeID, 1, 10).Return([]models.RouteVersion{}, int64(0), nil)

	result, total, err := svc.ListVersions(routeID, 1, 10)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, int64(0), total)
}

func TestRouteVersionService_ListVersions_Error(t *testing.T) {
	versionRepo := new(mocks.MockRouteVersionRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestRouteVersionService(versionRepo, routeRepo)

	routeID := uuid.New()
	versionRepo.On("ListByRouteID", routeID, 1, 10).Return([]models.RouteVersion(nil), int64(0), errors.New("db error"))

	_, _, err := svc.ListVersions(routeID, 1, 10)

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// GetVersion
// ---------------------------------------------------------------------------

func TestRouteVersionService_GetVersion_Success(t *testing.T) {
	versionRepo := new(mocks.MockRouteVersionRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestRouteVersionService(versionRepo, routeRepo)

	routeID := uuid.New()
	expected := &models.RouteVersion{ID: uuid.New(), RouteID: routeID, Version: 3}
	versionRepo.On("GetByRouteIDAndVersion", routeID, 3).Return(expected, nil)

	result, err := svc.GetVersion(routeID, 3)

	require.NoError(t, err)
	assert.Equal(t, 3, result.Version)
	versionRepo.AssertExpectations(t)
}

func TestRouteVersionService_GetVersion_NotFound(t *testing.T) {
	versionRepo := new(mocks.MockRouteVersionRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := newTestRouteVersionService(versionRepo, routeRepo)

	routeID := uuid.New()
	versionRepo.On("GetByRouteIDAndVersion", routeID, 99).Return(nil, errors.New("not found"))

	_, err := svc.GetVersion(routeID, 99)

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// NewRouteVersionService
// ---------------------------------------------------------------------------

func fullRouteVersionServiceDeps() services.RouteVersionServiceDeps {
	return services.RouteVersionServiceDeps{
		VersionRepo:              new(mocks.MockRouteVersionRepository),
		RouteRepo:                new(mocks.MockRouteRepository),
		SecurityPolicyRepo:       new(mocks.MockSecurityPolicyRepository),
		BackendTrafficPolicyRepo: new(mocks.MockBackendTrafficPolicyRepository),
		EnvoyExtensionPolicyRepo: new(mocks.MockEnvoyExtensionPolicyRepository),
		WafPolicyRepo:            new(mocks.MockWafPolicyRepository),
		RouteUpdater:             unusedRouteUpdater{},
	}
}

func TestNewRouteVersionService_RequiresEveryDependency(t *testing.T) {
	require.NotPanics(t, func() { services.NewRouteVersionService(fullRouteVersionServiceDeps()) })

	cases := map[string]func(*services.RouteVersionServiceDeps){
		"VersionRepo":              func(d *services.RouteVersionServiceDeps) { d.VersionRepo = nil },
		"RouteRepo":                func(d *services.RouteVersionServiceDeps) { d.RouteRepo = nil },
		"SecurityPolicyRepo":       func(d *services.RouteVersionServiceDeps) { d.SecurityPolicyRepo = nil },
		"BackendTrafficPolicyRepo": func(d *services.RouteVersionServiceDeps) { d.BackendTrafficPolicyRepo = nil },
		"EnvoyExtensionPolicyRepo": func(d *services.RouteVersionServiceDeps) { d.EnvoyExtensionPolicyRepo = nil },
		"WafPolicyRepo":            func(d *services.RouteVersionServiceDeps) { d.WafPolicyRepo = nil },
		"RouteUpdater":             func(d *services.RouteVersionServiceDeps) { d.RouteUpdater = nil },
	}
	for name, breakIt := range cases {
		t.Run("nil "+name, func(t *testing.T) {
			d := fullRouteVersionServiceDeps()
			breakIt(&d)
			assert.PanicsWithValue(t,
				"services.NewRouteVersionService: missing required dependency: "+name,
				func() { services.NewRouteVersionService(d) })
		})
	}
}

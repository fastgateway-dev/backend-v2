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

// ---------------------------------------------------------------------------
// ListVersions
// ---------------------------------------------------------------------------

func TestRouteVersionService_ListVersions_Success(t *testing.T) {
	versionRepo := new(mocks.MockRouteVersionRepository)
	routeRepo := new(mocks.MockRouteRepository)
	svc := services.NewRouteVersionService(versionRepo, routeRepo)

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
	svc := services.NewRouteVersionService(versionRepo, routeRepo)

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
	svc := services.NewRouteVersionService(versionRepo, routeRepo)

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
	svc := services.NewRouteVersionService(versionRepo, routeRepo)

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
	svc := services.NewRouteVersionService(versionRepo, routeRepo)

	routeID := uuid.New()
	versionRepo.On("GetByRouteIDAndVersion", routeID, 99).Return(nil, errors.New("not found"))

	_, err := svc.GetVersion(routeID, 99)

	require.Error(t, err)
}

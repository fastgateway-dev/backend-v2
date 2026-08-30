package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RouteVersionRepository handles route version database operations
type RouteVersionRepository struct {
	db *gorm.DB
}

// NewRouteVersionRepository creates a new route version repository
func NewRouteVersionRepository(db *gorm.DB) *RouteVersionRepository {
	return &RouteVersionRepository{db: db}
}

// Create creates a new route version record
func (r *RouteVersionRepository) Create(version *models.RouteVersion) error {
	return r.db.Create(version).Error
}

// GetByRouteIDAndVersion gets a specific version of a route
func (r *RouteVersionRepository) GetByRouteIDAndVersion(routeID uuid.UUID, version int) (*models.RouteVersion, error) {
	var rv models.RouteVersion
	err := r.db.Preload("Deployer").
		Where("route_id = ? AND version = ?", routeID, version).
		First(&rv).Error
	if err != nil {
		return nil, err
	}
	return &rv, nil
}

// ListByRouteID lists all versions for a route, newest first, with pagination
func (r *RouteVersionRepository) ListByRouteID(routeID uuid.UUID, page, limit int) ([]models.RouteVersion, int64, error) {
	var versions []models.RouteVersion
	var total int64

	if err := r.db.Model(&models.RouteVersion{}).Where("route_id = ?", routeID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	err := r.db.Where("route_id = ?", routeID).
		Preload("Deployer").
		Order("version DESC").
		Offset(offset).
		Limit(limit).
		Find(&versions).Error

	return versions, total, err
}

// GetMaxVersion gets the highest version number for a route (0 if none)
func (r *RouteVersionRepository) GetMaxVersion(routeID uuid.UUID) (int, error) {
	var maxVersion *int
	err := r.db.Model(&models.RouteVersion{}).
		Where("route_id = ?", routeID).
		Select("MAX(version)").
		Scan(&maxVersion).Error
	if err != nil {
		return 0, err
	}
	if maxVersion == nil {
		return 0, nil
	}
	return *maxVersion, nil
}

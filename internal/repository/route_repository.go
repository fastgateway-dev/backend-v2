package repository

import (
	"encoding/json"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RouteRepository handles route database operations
type RouteRepository struct {
	db *gorm.DB
}

// NewRouteRepository creates a new route repository
func NewRouteRepository(db *gorm.DB) *RouteRepository {
	return &RouteRepository{db: db}
}

// Create creates a new route
func (r *RouteRepository) Create(route *models.Route) error {
	return r.db.Create(route).Error
}

// GetByID gets a route by ID
func (r *RouteRepository) GetByID(id uuid.UUID) (*models.Route, error) {
	var route models.Route
	err := r.db.Preload("Team").Where("id = ?", id).First(&route).Error
	if err != nil {
		return nil, err
	}
	return &route, nil
}

// GetByIDs gets multiple routes by their IDs
func (r *RouteRepository) GetByIDs(ids []uuid.UUID) ([]models.Route, error) {
	var routes []models.Route
	if len(ids) == 0 {
		return routes, nil
	}
	err := r.db.Where("id IN ?", ids).Find(&routes).Error
	return routes, err
}

// GetByIDWithApproval gets a route by ID with pending or approved approval
func (r *RouteRepository) GetByIDWithApproval(id uuid.UUID) (*models.Route, error) {
	var route models.Route
	err := r.db.Preload("Team").Where("id = ?", id).First(&route).Error
	if err != nil {
		return nil, err
	}

	// Get pending approval if any
	var approval models.Approval
	err = r.db.Where("entity_type = ? AND entity_id = ? AND status = ?", models.ApprovalEntityRoute, id, models.ApprovalStatusPending).First(&approval).Error
	if err == nil {
		route.PendingApproval = &approval
	} else if route.Status == models.RouteStatusApproved {
		// If route is approved (not yet deployed), get the latest approved approval to know the action
		err = r.db.Where("entity_type = ? AND entity_id = ? AND status = ?", models.ApprovalEntityRoute, id, models.ApprovalStatusApproved).
			Order("created_at DESC").First(&approval).Error
		if err == nil {
			route.PendingApproval = &approval
		}
	}

	return &route, nil
}

// ListByDomainID lists routes for a domain with pagination
// searchField can be: "all", "name", "path", "owner" (empty defaults to "all")
// search is the search term (case-insensitive partial match)
func (r *RouteRepository) ListByDomainID(domainID uuid.UUID, page, limit int, teamID *uuid.UUID, status string, search string, searchField string, labels map[string]string) ([]models.Route, int64, error) {
	var routes []models.Route
	var total int64

	query := r.db.Model(&models.Route{}).Where("domain_id = ?", domainID)

	if teamID != nil {
		query = query.Where("team_id = ?", *teamID)
	}

	if status != "" {
		query = query.Where("status = ?", status)
	}

	// Apply search filter
	if search != "" {
		searchPattern := "%" + search + "%"
		switch searchField {
		case "name":
			query = query.Where("LOWER(name) LIKE LOWER(?)", searchPattern)
		case "path":
			// Search in JSONB config -> matches -> path -> value
			query = query.Where("EXISTS (SELECT 1 FROM jsonb_array_elements(config->'matches') AS m WHERE LOWER(m->'path'->>'value') LIKE LOWER(?))", searchPattern)
		case "owner":
			// Join with teams table to search by team name
			query = query.Joins("LEFT JOIN teams ON teams.id = routes.team_id").
				Where("LOWER(teams.name) LIKE LOWER(?)", searchPattern)
		default: // "all" or empty - search across all fields
			query = query.Joins("LEFT JOIN teams ON teams.id = routes.team_id").
				Where("LOWER(routes.name) LIKE LOWER(?) OR LOWER(teams.name) LIKE LOWER(?) OR EXISTS (SELECT 1 FROM jsonb_array_elements(config->'matches') AS m WHERE LOWER(m->'path'->>'value') LIKE LOWER(?))",
					searchPattern, searchPattern, searchPattern)
		}
	}

	if len(labels) > 0 {
		labelsJSON, err := json.Marshal(labels)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal labels filter: %w", err)
		}
		query = query.Where("routes.labels @> ?", string(labelsJSON))
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	err = query.Preload("Team").Offset(offset).Limit(limit).Order("name ASC").Find(&routes).Error
	if err != nil {
		return nil, 0, err
	}

	return routes, total, nil
}

// Update updates a route
func (r *RouteRepository) Update(route *models.Route) error {
	return r.db.Save(route).Error
}

// Delete deletes a route
func (r *RouteRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.Route{}, "id = ?", id).Error
}

// ExistsByName checks if a route with the given name exists in the domain
func (r *RouteRepository) ExistsByName(domainID uuid.UUID, name string) (bool, error) {
	var count int64
	err := r.db.Model(&models.Route{}).
		Where("domain_id = ? AND name = ?", domainID, name).
		Count(&count).Error
	return count > 0, err
}

// GetActiveRoutesByDomainID gets all active routes for a domain
func (r *RouteRepository) GetActiveRoutesByDomainID(domainID uuid.UUID) ([]models.Route, error) {
	var routes []models.Route
	err := r.db.Where("domain_id = ? AND status = ?", domainID, models.RouteStatusActive).Find(&routes).Error
	return routes, err
}

// CountByDomainID returns the number of routes in a domain
func (r *RouteRepository) CountByDomainID(domainID uuid.UUID) (int, error) {
	var count int64
	if err := r.db.Model(&models.Route{}).Where("domain_id = ?", domainID).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// ListByProjectID lists routes across all domains in a project with optional
// filters. When BackendService/BackendNamespace are set, it matches against
// the JSONB config.backends array (and optionally config.mirrors).
func (r *RouteRepository) ListByProjectID(projectID uuid.UUID, page, limit int, f RouteListFilters) ([]models.Route, int64, error) {
	var routes []models.Route
	var total int64

	query := r.db.Model(&models.Route{}).
		Joins("JOIN domains ON domains.id = routes.domain_id").
		Where("domains.project_id = ?", projectID)

	if f.DomainID != nil {
		query = query.Where("routes.domain_id = ?", *f.DomainID)
	}
	if f.TeamID != nil {
		query = query.Where("routes.team_id = ?", *f.TeamID)
	}
	if len(f.Statuses) > 0 {
		query = query.Where("routes.status IN ?", f.Statuses)
	}

	if f.BackendService != "" && f.BackendNamespace != "" {
		// Build a JSONB array containing exactly one object with the
		// service+namespace pair we are looking for. The @> operator
		// returns true if the array on the left contains the object on
		// the right, regardless of order or extra fields.
		needle, err := json.Marshal([]map[string]string{{
			"service":   f.BackendService,
			"namespace": f.BackendNamespace,
		}})
		if err != nil {
			return nil, 0, fmt.Errorf("encode backend filter: %w", err)
		}
		if f.IncludeMirrors {
			query = query.Where(
				"(routes.config -> 'backends')::jsonb @> ?::jsonb OR (routes.config -> 'mirrors')::jsonb @> ?::jsonb",
				string(needle), string(needle),
			)
		} else {
			query = query.Where(
				"(routes.config -> 'backends')::jsonb @> ?::jsonb",
				string(needle),
			)
		}
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	offset := (page - 1) * limit

	err := query.
		Preload("Team").
		Preload("Domain").
		Order("routes.created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&routes).Error
	if err != nil {
		return nil, 0, err
	}

	return routes, total, nil
}

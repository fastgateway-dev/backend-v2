package repository

import (
	"encoding/json"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ProjectRepository handles project database operations
type ProjectRepository struct {
	db *gorm.DB
}

// NewProjectRepository creates a new project repository
func NewProjectRepository(db *gorm.DB) *ProjectRepository {
	return &ProjectRepository{db: db}
}

// Create creates a new project
func (r *ProjectRepository) Create(project *models.Project) error {
	return r.db.Create(project).Error
}

// GetByID gets a project by ID
func (r *ProjectRepository) GetByID(id uuid.UUID) (*models.Project, error) {
	var project models.Project
	err := r.db.Where("id = ?", id).First(&project).Error
	if err != nil {
		return nil, err
	}
	return &project, nil
}

// GetByIDWithCounts gets a project by ID with domain and route counts
func (r *ProjectRepository) GetByIDWithCounts(id uuid.UUID) (*models.Project, error) {
	var project models.Project
	err := r.db.Where("id = ?", id).First(&project).Error
	if err != nil {
		return nil, err
	}

	// Get domain count
	var domainCount int64
	r.db.Model(&models.Domain{}).Where("project_id = ?", id).Count(&domainCount)
	project.DomainCount = int(domainCount)

	// Get route count
	var routeCount int64
	r.db.Model(&models.Route{}).
		Joins("JOIN domains ON routes.domain_id = domains.id").
		Where("domains.project_id = ?", id).
		Count(&routeCount)
	project.RouteCount = int(routeCount)

	return &project, nil
}

// List lists projects with pagination
func (r *ProjectRepository) List(page, limit int) ([]models.Project, int64, error) {
	var projects []models.Project
	var total int64

	err := r.db.Model(&models.Project{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	err = r.db.Offset(offset).Limit(limit).Order("created_at DESC").Find(&projects).Error
	if err != nil {
		return nil, 0, err
	}

	// Get counts for each project
	for i := range projects {
		var domainCount int64
		r.db.Model(&models.Domain{}).Where("project_id = ?", projects[i].ID).Count(&domainCount)
		projects[i].DomainCount = int(domainCount)

		var routeCount int64
		r.db.Model(&models.Route{}).
			Joins("JOIN domains ON routes.domain_id = domains.id").
			Where("domains.project_id = ?", projects[i].ID).
			Count(&routeCount)
		projects[i].RouteCount = int(routeCount)
	}

	return projects, total, nil
}

// ListByUserAccess lists projects accessible by a user
func (r *ProjectRepository) ListByUserAccess(userID uuid.UUID, userRole models.UserRole, page, limit int, search string, labels map[string]string) ([]models.Project, int64, error) {
	var projects []models.Project
	var total int64

	query := r.db.Model(&models.Project{})

	// Owners can see all projects
	if userRole != models.UserRoleOwner {
		// Regular users can only see projects they are admin of or have team membership in
		query = query.Where(`
			id IN (SELECT project_id FROM project_admins WHERE user_id = ?)
			OR id IN (
				SELECT ptr.project_id FROM project_team_roles ptr
				JOIN team_members tm ON ptr.team_id = tm.team_id
				WHERE tm.user_id = ?
			)
		`, userID, userID)
	}

	if search != "" {
		searchPattern := "%" + search + "%"
		query = query.Where("LOWER(name) LIKE LOWER(?)", searchPattern)
	}

	if len(labels) > 0 {
		labelsJSON, err := json.Marshal(labels)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal labels filter: %w", err)
		}
		query = query.Where("labels @> ?", string(labelsJSON))
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	err = query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&projects).Error
	if err != nil {
		return nil, 0, err
	}

	// Get counts for each project
	for i := range projects {
		var domainCount int64
		r.db.Model(&models.Domain{}).Where("project_id = ?", projects[i].ID).Count(&domainCount)
		projects[i].DomainCount = int(domainCount)

		var routeCount int64
		r.db.Model(&models.Route{}).
			Joins("JOIN domains ON routes.domain_id = domains.id").
			Where("domains.project_id = ?", projects[i].ID).
			Count(&routeCount)
		projects[i].RouteCount = int(routeCount)
	}

	return projects, total, nil
}

// Update updates a project
func (r *ProjectRepository) Update(project *models.Project) error {
	return r.db.Save(project).Error
}

// Delete deletes a project
func (r *ProjectRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.Project{}, "id = ?", id).Error
}

// AddAdmin adds an admin to a project
func (r *ProjectRepository) AddAdmin(projectID, userID uuid.UUID) error {
	admin := &models.ProjectAdmin{
		ProjectID: projectID,
		UserID:    userID,
	}
	return r.db.Create(admin).Error
}

// RemoveAdmin removes an admin from a project
func (r *ProjectRepository) RemoveAdmin(projectID, userID uuid.UUID) error {
	return r.db.Delete(&models.ProjectAdmin{}, "project_id = ? AND user_id = ?", projectID, userID).Error
}

// ListAdmins lists admins of a project
func (r *ProjectRepository) ListAdmins(projectID uuid.UUID) ([]models.User, error) {
	var users []models.User
	err := r.db.Table("users").
		Joins("JOIN project_admins ON users.id = project_admins.user_id").
		Where("project_admins.project_id = ?", projectID).
		Find(&users).Error
	return users, err
}

// IsAdmin checks if a user is an admin of a project
func (r *ProjectRepository) IsAdmin(projectID, userID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.ProjectAdmin{}).
		Where("project_id = ? AND user_id = ?", projectID, userID).
		Count(&count).Error
	return count > 0, err
}

// Count returns the total number of projects
func (r *ProjectRepository) Count() (int, error) {
	var count int64
	if err := r.db.Model(&models.Project{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// FindByConnectionType finds a project by connection type
func (r *ProjectRepository) FindByConnectionType(connectionType string) (*models.Project, error) {
	var project models.Project
	err := r.db.Where("connection_type = ?", connectionType).First(&project).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &project, nil
}

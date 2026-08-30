package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// ProjectNamespaceRepository handles project namespace database operations
type ProjectNamespaceRepository struct {
	db *gorm.DB
}

// NewProjectNamespaceRepository creates a new project namespace repository
func NewProjectNamespaceRepository(db *gorm.DB) *ProjectNamespaceRepository {
	return &ProjectNamespaceRepository{db: db}
}

// Create creates a new project namespace
func (r *ProjectNamespaceRepository) Create(ns *models.ProjectNamespace) error {
	return r.db.Create(ns).Error
}

// GetByID gets a project namespace by ID
func (r *ProjectNamespaceRepository) GetByID(id uuid.UUID) (*models.ProjectNamespace, error) {
	var ns models.ProjectNamespace
	err := r.db.Where("id = ?", id).First(&ns).Error
	if err != nil {
		return nil, err
	}
	return &ns, nil
}

// GetByProjectAndNamespace gets a project namespace by project ID and namespace name
func (r *ProjectNamespaceRepository) GetByProjectAndNamespace(projectID uuid.UUID, namespace string) (*models.ProjectNamespace, error) {
	var ns models.ProjectNamespace
	err := r.db.Where("project_id = ? AND namespace = ?", projectID, namespace).First(&ns).Error
	if err != nil {
		return nil, err
	}
	return &ns, nil
}

// ListByProjectID lists all namespaces for a project
func (r *ProjectNamespaceRepository) ListByProjectID(projectID uuid.UUID) ([]models.ProjectNamespace, error) {
	var namespaces []models.ProjectNamespace
	err := r.db.Where("project_id = ?", projectID).Order("namespace ASC").Find(&namespaces).Error
	if err != nil {
		return nil, err
	}
	return namespaces, nil
}

// ListByCapability lists project namespaces that include the given capability.
func (r *ProjectNamespaceRepository) ListByCapability(projectID uuid.UUID, capability string) ([]models.ProjectNamespace, error) {
	var namespaces []models.ProjectNamespace
	err := r.db.Where("project_id = ? AND capabilities @> ?", projectID, pq.StringArray{capability}).
		Order("namespace ASC").
		Find(&namespaces).Error
	if err != nil {
		return nil, err
	}
	return namespaces, nil
}

// Update updates a project namespace
func (r *ProjectNamespaceRepository) Update(ns *models.ProjectNamespace) error {
	return r.db.Save(ns).Error
}

// Delete deletes a project namespace
func (r *ProjectNamespaceRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.ProjectNamespace{}, "id = ?", id).Error
}

// ExistsByProjectAndNamespace checks if a namespace exists for a project
func (r *ProjectNamespaceRepository) ExistsByProjectAndNamespace(projectID uuid.UUID, namespace string) (bool, error) {
	var count int64
	err := r.db.Model(&models.ProjectNamespace{}).
		Where("project_id = ? AND namespace = ?", projectID, namespace).
		Count(&count).Error
	return count > 0, err
}

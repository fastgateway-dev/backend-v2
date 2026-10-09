package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// StreamRepository handles stream database operations
type StreamRepository struct {
	db *gorm.DB
}

// NewStreamRepository creates a new stream repository
func NewStreamRepository(db *gorm.DB) *StreamRepository {
	return &StreamRepository{db: db}
}

// Create creates a new stream
func (r *StreamRepository) Create(stream *models.Stream) error {
	return r.db.Create(stream).Error
}

// GetByID gets a stream by ID
func (r *StreamRepository) GetByID(id uuid.UUID) (*models.Stream, error) {
	var stream models.Stream
	if err := r.db.Where("id = ?", id).First(&stream).Error; err != nil {
		return nil, err
	}
	return &stream, nil
}

// ListByProjectID lists all streams in a project, ordered by name
func (r *StreamRepository) ListByProjectID(projectID uuid.UUID) ([]models.Stream, error) {
	var streams []models.Stream
	err := r.db.Where("project_id = ?", projectID).Order("name ASC").Find(&streams).Error
	return streams, err
}

// Update saves all fields of a stream
func (r *StreamRepository) Update(stream *models.Stream) error {
	return r.db.Save(stream).Error
}

// Delete deletes a stream by ID
func (r *StreamRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.Stream{}, "id = ?", id).Error
}

// CountByGatewayTemplateID counts streams that use the given gateway template
func (r *StreamRepository) CountByGatewayTemplateID(templateID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.Model(&models.Stream{}).Where("gateway_template_id = ?", templateID).Count(&count).Error
	return count, err
}

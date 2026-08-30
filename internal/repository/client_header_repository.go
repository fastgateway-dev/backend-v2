package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ClientHeaderRepository handles client header database operations
type ClientHeaderRepository struct {
	db *gorm.DB
}

// NewClientHeaderRepository creates a new ClientHeaderRepository
func NewClientHeaderRepository(db *gorm.DB) *ClientHeaderRepository {
	return &ClientHeaderRepository{db: db}
}

// Create creates a new client header
func (r *ClientHeaderRepository) Create(header *models.ClientHeader) error {
	return r.db.Create(header).Error
}

// GetByID returns a client header by ID
func (r *ClientHeaderRepository) GetByID(id uuid.UUID) (*models.ClientHeader, error) {
	var header models.ClientHeader
	err := r.db.Preload("Creator").Where("id = ?", id).First(&header).Error
	if err != nil {
		return nil, err
	}
	return &header, nil
}

// Delete deletes a client header
func (r *ClientHeaderRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.ClientHeader{}, "id = ?", id).Error
}

// ListByClientID returns all headers for a client
func (r *ClientHeaderRepository) ListByClientID(clientID uuid.UUID) ([]models.ClientHeader, error) {
	var headers []models.ClientHeader
	err := r.db.Preload("Creator").
		Where("client_id = ?", clientID).
		Order("created_at ASC").
		Find(&headers).Error
	return headers, err
}

// CountByClientID returns the count of headers for a client
func (r *ClientHeaderRepository) CountByClientID(clientID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.Model(&models.ClientHeader{}).Where("client_id = ?", clientID).Count(&count).Error
	return count, err
}

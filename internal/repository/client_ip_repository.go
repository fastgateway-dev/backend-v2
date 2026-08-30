package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ClientIPRepository handles client IP address database operations
type ClientIPRepository struct {
	db *gorm.DB
}

// NewClientIPRepository creates a new ClientIPRepository
func NewClientIPRepository(db *gorm.DB) *ClientIPRepository {
	return &ClientIPRepository{db: db}
}

// Create creates a new client IP address
func (r *ClientIPRepository) Create(ip *models.ClientIPAddress) error {
	return r.db.Create(ip).Error
}

// GetByID returns a client IP address by ID
func (r *ClientIPRepository) GetByID(id uuid.UUID) (*models.ClientIPAddress, error) {
	var ip models.ClientIPAddress
	err := r.db.Preload("Creator").Where("id = ?", id).First(&ip).Error
	if err != nil {
		return nil, err
	}
	return &ip, nil
}

// Delete deletes a client IP address
func (r *ClientIPRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.ClientIPAddress{}, "id = ?", id).Error
}

// ListByClientID returns all IP addresses for a client
func (r *ClientIPRepository) ListByClientID(clientID uuid.UUID) ([]models.ClientIPAddress, error) {
	var ips []models.ClientIPAddress
	err := r.db.Preload("Creator").
		Where("client_id = ?", clientID).
		Order("created_at ASC").
		Find(&ips).Error
	return ips, err
}

// CountByClientID returns the count of IP addresses for a client
func (r *ClientIPRepository) CountByClientID(clientID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.Model(&models.ClientIPAddress{}).Where("client_id = ?", clientID).Count(&count).Error
	return count, err
}

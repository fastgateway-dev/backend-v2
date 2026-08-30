package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ClientRepository handles client database operations
type ClientRepository struct {
	db *gorm.DB
}

// NewClientRepository creates a new ClientRepository
func NewClientRepository(db *gorm.DB) *ClientRepository {
	return &ClientRepository{db: db}
}

// Create creates a new client
func (r *ClientRepository) Create(client *models.Client) error {
	return r.db.Create(client).Error
}

// GetByID returns a client by ID with relationships preloaded
func (r *ClientRepository) GetByID(id uuid.UUID) (*models.Client, error) {
	var client models.Client
	err := r.db.Preload("Team").Preload("Creator").Where("id = ?", id).First(&client).Error
	if err != nil {
		return nil, err
	}

	// Get IP address count
	var ipCount int64
	r.db.Model(&models.ClientIPAddress{}).Where("client_id = ?", id).Count(&ipCount)
	client.IPAddressCount = int(ipCount)

	// Get header count
	var headerCount int64
	r.db.Model(&models.ClientHeader{}).Where("client_id = ?", id).Count(&headerCount)
	client.HeaderCount = int(headerCount)

	return &client, nil
}

// Update updates a client
func (r *ClientRepository) Update(client *models.Client) error {
	return r.db.Save(client).Error
}

// Delete deletes a client and its related records
func (r *ClientRepository) Delete(id uuid.UUID) error {
	// Delete IP addresses first
	if err := r.db.Delete(&models.ClientIPAddress{}, "client_id = ?", id).Error; err != nil {
		return err
	}
	// Delete headers
	if err := r.db.Delete(&models.ClientHeader{}, "client_id = ?", id).Error; err != nil {
		return err
	}
	return r.db.Delete(&models.Client{}, "id = ?", id).Error
}

// List returns paginated clients with optional team filter
func (r *ClientRepository) List(page, limit int, teamID *uuid.UUID) ([]models.Client, int64, error) {
	var clients []models.Client
	var total int64

	query := r.db.Model(&models.Client{})

	if teamID != nil {
		query = query.Where("team_id = ?", *teamID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	err := query.Preload("Team").Preload("Creator").
		Order("name ASC").
		Offset(offset).Limit(limit).
		Find(&clients).Error
	if err != nil {
		return nil, 0, err
	}

	// Get IP address and header counts for each client
	for i := range clients {
		var ipCount int64
		r.db.Model(&models.ClientIPAddress{}).Where("client_id = ?", clients[i].ID).Count(&ipCount)
		clients[i].IPAddressCount = int(ipCount)

		var headerCount int64
		r.db.Model(&models.ClientHeader{}).Where("client_id = ?", clients[i].ID).Count(&headerCount)
		clients[i].HeaderCount = int(headerCount)
	}

	return clients, total, nil
}

// ExistsByName checks if a client with the given name exists
func (r *ClientRepository) ExistsByName(name string) (bool, error) {
	var count int64
	err := r.db.Model(&models.Client{}).Where("name = ?", name).Count(&count).Error
	return count > 0, err
}

// ExistsByNameExcluding checks if a client with the given name exists, excluding a specific client ID
func (r *ClientRepository) ExistsByNameExcluding(name string, excludeID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.Client{}).Where("name = ? AND id != ?", name, excludeID).Count(&count).Error
	return count > 0, err
}

// ListByTeamIDs returns all clients belonging to the given teams
func (r *ClientRepository) ListByTeamIDs(teamIDs []uuid.UUID) ([]models.Client, error) {
	var clients []models.Client
	err := r.db.Preload("Team").
		Where("team_id IN ?", teamIDs).
		Order("name ASC").
		Find(&clients).Error
	return clients, err
}

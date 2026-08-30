package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ClientAttachmentRepository handles client-route attachment database operations
type ClientAttachmentRepository struct {
	db *gorm.DB
}

// NewClientAttachmentRepository creates a new ClientAttachmentRepository
func NewClientAttachmentRepository(db *gorm.DB) *ClientAttachmentRepository {
	return &ClientAttachmentRepository{db: db}
}

// Create creates a new client-route attachment
func (r *ClientAttachmentRepository) Create(attachment *models.ClientRouteAttachment) error {
	return r.db.Create(attachment).Error
}

// GetByID returns an attachment by ID with relationships preloaded
func (r *ClientAttachmentRepository) GetByID(id uuid.UUID) (*models.ClientRouteAttachment, error) {
	var attachment models.ClientRouteAttachment
	err := r.db.Preload("Client").Preload("Client.Team").
		Preload("Route").Preload("Route.Team").
		Preload("Creator").
		Where("id = ?", id).First(&attachment).Error
	if err != nil {
		return nil, err
	}
	return &attachment, nil
}

// Update updates an attachment
func (r *ClientAttachmentRepository) Update(attachment *models.ClientRouteAttachment) error {
	return r.db.Save(attachment).Error
}

// Delete deletes an attachment
func (r *ClientAttachmentRepository) Delete(id uuid.UUID) error {
	return r.db.Delete(&models.ClientRouteAttachment{}, "id = ?", id).Error
}

// GetByClientAndRoute returns an attachment by client and route IDs
func (r *ClientAttachmentRepository) GetByClientAndRoute(clientID, routeID uuid.UUID) (*models.ClientRouteAttachment, error) {
	var attachment models.ClientRouteAttachment
	err := r.db.Preload("Client").Preload("Client.Team").
		Preload("Route").Preload("Route.Team").
		Preload("Creator").
		Where("client_id = ? AND route_id = ?", clientID, routeID).
		First(&attachment).Error
	if err != nil {
		return nil, err
	}
	return &attachment, nil
}

// ListByClientID returns all attachments for a client
func (r *ClientAttachmentRepository) ListByClientID(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	var attachments []models.ClientRouteAttachment
	err := r.db.Preload("Client").Preload("Client.Team").
		Preload("Route").Preload("Route.Team").Preload("Route.Domain").
		Preload("Creator").
		Where("client_id = ? AND status != ?", clientID, models.AttachmentStatusRemoved).
		Order("created_at DESC, id").
		Find(&attachments).Error
	return attachments, err
}

// ListByRouteID returns all attachments for a route
func (r *ClientAttachmentRepository) ListByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	var attachments []models.ClientRouteAttachment
	err := r.db.Preload("Client").Preload("Client.Team").
		Preload("Route").Preload("Route.Team").
		Preload("Creator").
		Where("route_id = ? AND status != ?", routeID, models.AttachmentStatusRemoved).
		Order("created_at DESC, id").
		Find(&attachments).Error
	return attachments, err
}

// ListActiveByRouteID returns all active attachments for a route
func (r *ClientAttachmentRepository) ListActiveByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	var attachments []models.ClientRouteAttachment
	err := r.db.Preload("Client").Preload("Client.Team").
		Where("route_id = ? AND status = ?", routeID, models.AttachmentStatusActive).
		Order("created_at, id").
		Find(&attachments).Error
	return attachments, err
}

// ListApprovedByRouteID returns all approved (pending deploy) attachments for a route
func (r *ClientAttachmentRepository) ListApprovedByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	var attachments []models.ClientRouteAttachment
	err := r.db.Where("route_id = ? AND status = ?", routeID, models.AttachmentStatusApproved).
		Order("created_at, id").
		Find(&attachments).Error
	return attachments, err
}

// UpdateStatusByRouteID updates the status of attachments for a route
func (r *ClientAttachmentRepository) UpdateStatusByRouteID(routeID uuid.UUID, fromStatus, toStatus models.AttachmentStatus) error {
	return r.db.Model(&models.ClientRouteAttachment{}).
		Where("route_id = ? AND status = ?", routeID, fromStatus).
		Update("status", toStatus).Error
}

// CountByClientID returns the count of non-removed attachments for a client
func (r *ClientAttachmentRepository) CountByClientID(clientID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.Model(&models.ClientRouteAttachment{}).
		Where("client_id = ? AND status != ?", clientID, models.AttachmentStatusRemoved).
		Count(&count).Error
	return count, err
}

// ListActiveByClientIDWithIPAllowlist returns active attachments with IP allowlisting for a client
func (r *ClientAttachmentRepository) ListActiveByClientIDWithIPAllowlist(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	var attachments []models.ClientRouteAttachment
	err := r.db.Preload("Route").
		Where("client_id = ? AND status = ? AND enable_ip_allowlist = ?", clientID, models.AttachmentStatusActive, true).
		Order("created_at, id").
		Find(&attachments).Error
	return attachments, err
}

// ListActiveByClientIDWithAPIKey returns active attachments with API key enabled for a client
func (r *ClientAttachmentRepository) ListActiveByClientIDWithAPIKey(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	var attachments []models.ClientRouteAttachment
	err := r.db.Preload("Route").
		Where("client_id = ? AND status = ? AND enable_api_key = ?", clientID, models.AttachmentStatusActive, true).
		Order("created_at, id").
		Find(&attachments).Error
	return attachments, err
}

// ListActiveByClientIDWithJWT returns active attachments with JWT enabled for a client
func (r *ClientAttachmentRepository) ListActiveByClientIDWithJWT(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	var attachments []models.ClientRouteAttachment
	err := r.db.Preload("Route").
		Where("client_id = ? AND status = ? AND enable_jwt = ?", clientID, models.AttachmentStatusActive, true).
		Order("created_at, id").
		Find(&attachments).Error
	return attachments, err
}

// CountMTLSAttachmentsByClientID counts active mTLS attachments for a client
func (r *ClientAttachmentRepository) CountMTLSAttachmentsByClientID(clientID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.Model(&models.ClientRouteAttachment{}).
		Where("client_id = ? AND enable_mtls = ? AND status = ?", clientID, true, models.AttachmentStatusActive).
		Count(&count).Error
	return count, err
}

// CountMTLSAttachmentsByDomainID counts active mTLS attachments for routes on a domain
func (r *ClientAttachmentRepository) CountMTLSAttachmentsByDomainID(domainID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.Model(&models.ClientRouteAttachment{}).
		Joins("JOIN routes ON routes.id = client_route_attachments.route_id").
		Where("routes.domain_id = ? AND client_route_attachments.enable_mtls = ? AND client_route_attachments.status = ?",
			domainID, true, models.AttachmentStatusActive).
		Count(&count).Error
	return count, err
}

// GetMTLSClientsForDomain gets all clients with active mTLS attachments on a domain
func (r *ClientAttachmentRepository) GetMTLSClientsForDomain(domainID uuid.UUID) ([]models.Client, error) {
	var clients []models.Client
	err := r.db.Model(&models.Client{}).
		Joins("JOIN client_route_attachments ON client_route_attachments.client_id = clients.id").
		Joins("JOIN routes ON routes.id = client_route_attachments.route_id").
		Where("routes.domain_id = ? AND client_route_attachments.enable_mtls = ? AND client_route_attachments.status = ? AND clients.mtls_enabled = ?",
			domainID, true, models.AttachmentStatusActive, true).
		Distinct().
		Find(&clients).Error
	return clients, err
}

// ListActiveByClientIDWithMTLS returns active attachments with mTLS enabled for a client
func (r *ClientAttachmentRepository) ListActiveByClientIDWithMTLS(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	var attachments []models.ClientRouteAttachment
	err := r.db.Preload("Route").
		Where("client_id = ? AND status = ? AND enable_mtls = ?", clientID, models.AttachmentStatusActive, true).
		Order("created_at, id").
		Find(&attachments).Error
	return attachments, err
}

// ListActiveByClientIDWithHeaderAuth returns active attachments with header auth for a client
func (r *ClientAttachmentRepository) ListActiveByClientIDWithHeaderAuth(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	var attachments []models.ClientRouteAttachment
	err := r.db.Preload("Route").
		Where("client_id = ? AND status = ? AND enable_header_auth = ?", clientID, models.AttachmentStatusActive, true).
		Order("created_at, id").
		Find(&attachments).Error
	return attachments, err
}

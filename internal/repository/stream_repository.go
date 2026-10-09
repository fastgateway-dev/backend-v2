package repository

import (
	"strings"

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

// ExistsByName reports whether a stream with the given name exists in the project
func (r *StreamRepository) ExistsByName(projectID uuid.UUID, name string) (bool, error) {
	var count int64
	err := r.db.Model(&models.Stream{}).Where("project_id = ? AND name = ?", projectID, name).Count(&count).Error
	return count > 0, err
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

// PortUse is one occupied Gateway listener identity: (transport, port).
// Transport is "TCP" or "UDP"; HTTP/HTTPS/TLS listeners count as TCP.
type PortUse struct {
	Transport string
	Port      int
}

// l4PortRow is the scan target for the L4 port queries.
type l4PortRow struct {
	Protocol string
	Port     int
}

func portUsesFromRows(rows []l4PortRow) []PortUse {
	uses := make([]PortUse, 0, len(rows))
	for _, row := range rows {
		uses = append(uses, PortUse{Transport: strings.ToUpper(row.Protocol), Port: row.Port})
	}
	return uses
}

// UsedPortsByStream returns the (transport, port) of every L4 route on the
// stream, optionally skipping excludeRouteID (the route being updated).
//
// Rejected routes never reach the cluster and are not counted; every other
// status (including not-yet-approved ones) holds its port so two pending
// routes cannot claim the same listener.
func (r *StreamRepository) UsedPortsByStream(streamID uuid.UUID, excludeRouteID *uuid.UUID) ([]PortUse, error) {
	q := r.db.Table("routes").
		Select("protocol, (config->>'listenerPort')::int AS port").
		Where("stream_id = ? AND protocol IN ? AND status <> ? AND config->>'listenerPort' IS NOT NULL",
			streamID, []string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}, models.RouteStatusRejected)
	if excludeRouteID != nil {
		q = q.Where("id <> ?", *excludeRouteID)
	}
	var rows []l4PortRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return portUsesFromRows(rows), nil
}

// UsedL4Ports returns the (transport, port) of every L4 route on every stream
// that uses the given gateway template, optionally skipping excludeRouteID.
// It is the stream half of the merged-Gateways collision scope. Rejected
// routes are not counted (see UsedPortsByStream).
func (r *StreamRepository) UsedL4Ports(templateID uuid.UUID, excludeRouteID *uuid.UUID) ([]PortUse, error) {
	q := r.db.Table("routes").
		Select("routes.protocol AS protocol, (routes.config->>'listenerPort')::int AS port").
		Joins("JOIN streams ON streams.id = routes.stream_id").
		Where("streams.gateway_template_id = ? AND routes.protocol IN ? AND routes.status <> ? AND routes.config->>'listenerPort' IS NOT NULL",
			templateID, []string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}, models.RouteStatusRejected)
	if excludeRouteID != nil {
		q = q.Where("routes.id <> ?", *excludeRouteID)
	}
	var rows []l4PortRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return portUsesFromRows(rows), nil
}

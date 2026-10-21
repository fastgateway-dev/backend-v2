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

// GetByIDs gets the streams with the given IDs in one query (approval
// enrichment uses it, so listing approvals never issues a query per stream).
// An empty input returns nil without querying.
func (r *StreamRepository) GetByIDs(ids []uuid.UUID) ([]models.Stream, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var streams []models.Stream
	err := r.db.Where("id IN ?", ids).Find(&streams).Error
	return streams, err
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

// pendingApprovalFrom is the FROM/WHERE shared by every query that reads the
// listener port a pending route approval proposes: the approval's route
// supplies the stream and the protocol, the snapshot supplies the port.
const pendingApprovalFrom = `FROM approvals a
	 JOIN routes r ON r.id = a.entity_id
	 JOIN streams ON streams.id = r.stream_id
	 WHERE r.protocol IN ? AND a.entity_type = 'route' AND a.status = 'pending'
	   AND a.action IN ('create','update')
	   AND a.config_snapshot->'routeConfig'->>'listenerPort' IS NOT NULL`

// UsedPortsByStream returns the (transport, port) a stream's L4 routes use,
// optionally skipping excludeRouteID (the route being written).
//
// A port is used when a route row of the stream holds it (any status but
// rejected: rejected routes never reach the cluster, every other status
// including not-yet-approved ones holds its port so two pending routes cannot
// claim the same listener) or when a pending create/update approval for a
// route of the stream proposes it (approving it would write that port).
func (r *StreamRepository) UsedPortsByStream(streamID uuid.UUID, excludeRouteID *uuid.UUID) ([]PortUse, error) {
	l4 := []string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}
	rowsSQL := `SELECT r.protocol AS protocol, (r.config->>'listenerPort')::int AS port
		FROM routes r
		WHERE r.stream_id = ? AND r.protocol IN ? AND r.status <> ? AND r.config->>'listenerPort' IS NOT NULL`
	args := []interface{}{streamID, l4, models.RouteStatusRejected}
	if excludeRouteID != nil {
		rowsSQL += " AND r.id <> ?"
		args = append(args, *excludeRouteID)
	}
	pendingSQL := `SELECT r.protocol AS protocol, (a.config_snapshot->'routeConfig'->>'listenerPort')::int AS port ` +
		pendingApprovalFrom + " AND streams.id = ?"
	args = append(args, l4, streamID)
	if excludeRouteID != nil {
		pendingSQL += " AND r.id <> ?"
		args = append(args, *excludeRouteID)
	}
	var rows []l4PortRow
	if err := r.db.Raw(rowsSQL+" UNION "+pendingSQL, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return portUsesFromRows(rows), nil
}

// ClaimedPortsByTemplate returns the (transport, port) every stream on the
// gateway template has claimed, optionally skipping excludeStreamID (the
// stream being written). It is the claim half of the merged-Gateways
// collision scope.
func (r *StreamRepository) ClaimedPortsByTemplate(templateID uuid.UUID, excludeStreamID *uuid.UUID) ([]PortUse, error) {
	sql := `SELECT c->>'transport' AS protocol, (c->>'port')::int AS port
		FROM streams, jsonb_array_elements(streams.ports) AS c
		WHERE streams.gateway_template_id = ?`
	args := []interface{}{templateID}
	if excludeStreamID != nil {
		sql += " AND streams.id <> ?"
		args = append(args, *excludeStreamID)
	}
	var rows []l4PortRow
	if err := r.db.Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return portUsesFromRows(rows), nil
}

// ListAll returns every stream, ordered by name. Startup backfills use it.
func (r *StreamRepository) ListAll() ([]models.Stream, error) {
	var streams []models.Stream
	err := r.db.Order("name ASC, id ASC").Find(&streams).Error
	return streams, err
}

// ListByGatewayTemplateID lists every stream created from the given gateway
// template, ordered by name. The entry-point view uses it for the streams
// that share a merged template's Gateway.
func (r *StreamRepository) ListByGatewayTemplateID(templateID uuid.UUID) ([]models.Stream, error) {
	var streams []models.Stream
	err := r.db.Where("gateway_template_id = ?", templateID).Order("name ASC").Find(&streams).Error
	return streams, err
}

// L4PortOwner is one listener an L4 route holds, together with the route and
// the stream that own it. Transport is "TCP" or "UDP".
type L4PortOwner struct {
	Transport  string
	Port       int
	StreamID   uuid.UUID
	StreamName string
	RouteID    uuid.UUID
	RouteName  string
	Status     string
	// Pending marks a port the route does not hold yet: a pending approval
	// proposes it. Status is then the route's current status.
	Pending bool
}

// l4PortOwnerRow is the scan target for the owner-aware L4 port queries.
type l4PortOwnerRow struct {
	Protocol   string
	Port       int
	StreamID   uuid.UUID
	StreamName string
	RouteID    uuid.UUID
	RouteName  string
	Status     string
}

// l4PortOwners runs one owner-aware L4 port query. where must carry the
// rejected/listenerPort conditions of UsedPortsByStream so the owner view and
// the used-port check can never disagree.
func (r *StreamRepository) l4PortOwners(where string, args ...interface{}) ([]L4PortOwner, error) {
	var rows []l4PortOwnerRow
	err := r.db.Table("routes").
		Select("routes.protocol AS protocol, (routes.config->>'listenerPort')::int AS port, "+
			"streams.id AS stream_id, streams.name AS stream_name, "+
			"routes.id AS route_id, routes.name AS route_name, routes.status AS status").
		Joins("JOIN streams ON streams.id = routes.stream_id").
		Where(where, args...).
		Order("port ASC, routes.protocol ASC, routes.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return ownersFromRows(rows, false), nil
}

// ownersFromRows converts scanned owner rows, marking them all pending or not.
func ownersFromRows(rows []l4PortOwnerRow, pending bool) []L4PortOwner {
	owners := make([]L4PortOwner, 0, len(rows))
	for _, row := range rows {
		owners = append(owners, L4PortOwner{
			Transport:  strings.ToUpper(row.Protocol),
			Port:       row.Port,
			StreamID:   row.StreamID,
			StreamName: row.StreamName,
			RouteID:    row.RouteID,
			RouteName:  row.RouteName,
			Status:     row.Status,
			Pending:    pending,
		})
	}
	return owners
}

// L4PortOwnersByStream is UsedPortsByStream with owners: the listener of
// every non-rejected L4 route on the stream. Same WHERE clause.
func (r *StreamRepository) L4PortOwnersByStream(streamID uuid.UUID) ([]L4PortOwner, error) {
	return r.l4PortOwners("routes.stream_id = ? AND routes.protocol IN ? AND routes.status <> ? AND routes.config->>'listenerPort' IS NOT NULL",
		streamID, []string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}, models.RouteStatusRejected)
}

// L4PortOwnersByTemplate returns, with owners, the listener of every
// non-rejected L4 route on every stream of the template.
func (r *StreamRepository) L4PortOwnersByTemplate(templateID uuid.UUID) ([]L4PortOwner, error) {
	return r.l4PortOwners("streams.gateway_template_id = ? AND routes.protocol IN ? AND routes.status <> ? AND routes.config->>'listenerPort' IS NOT NULL",
		templateID, []string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}, models.RouteStatusRejected)
}

// L4PortOwnersByStreamIDs is L4PortOwnersByStream for many streams in one
// query (the stream list uses it, so listing never issues a query per
// stream). An empty input returns an empty result without querying.
func (r *StreamRepository) L4PortOwnersByStreamIDs(streamIDs []uuid.UUID) ([]L4PortOwner, error) {
	if len(streamIDs) == 0 {
		return []L4PortOwner{}, nil
	}
	return r.l4PortOwners("routes.stream_id IN ? AND routes.protocol IN ? AND routes.status <> ? AND routes.config->>'listenerPort' IS NOT NULL",
		streamIDs, []string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}, models.RouteStatusRejected)
}

// PendingL4PortOwnersByStream is the pending-approval half of
// UsedPortsByStream with owners: every listener port a pending create/update
// approval proposes for an L4 route of the stream, marked Pending. The
// route's own row (if any) is reported by L4PortOwnersByStream.
func (r *StreamRepository) PendingL4PortOwnersByStream(streamID uuid.UUID) ([]L4PortOwner, error) {
	var rows []l4PortOwnerRow
	err := r.db.Raw(`SELECT DISTINCT r.protocol AS protocol,
			(a.config_snapshot->'routeConfig'->>'listenerPort')::int AS port,
			streams.id AS stream_id, streams.name AS stream_name,
			r.id AS route_id, r.name AS route_name, r.status AS status `+
		pendingApprovalFrom+` AND streams.id = ? ORDER BY port ASC, protocol ASC, route_id ASC`,
		[]string{string(models.RouteProtocolTCP), string(models.RouteProtocolUDP)}, streamID).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return ownersFromRows(rows, true), nil
}

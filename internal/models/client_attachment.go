package models

import (
	"time"

	"github.com/google/uuid"
)

// AttachmentStatus represents the status of a client-route attachment
type AttachmentStatus string

const (
	AttachmentStatusPendingAttach AttachmentStatus = "pending_attach"
	AttachmentStatusPendingUpdate AttachmentStatus = "pending_update"
	AttachmentStatusPendingDetach AttachmentStatus = "pending_detach"
	AttachmentStatusApproved      AttachmentStatus = "approved"
	AttachmentStatusActive        AttachmentStatus = "active"
	AttachmentStatusRemoved       AttachmentStatus = "removed"
	AttachmentStatusRejected      AttachmentStatus = "rejected"
)

// ClientRouteAttachment represents a client attached to a route with security config
type ClientRouteAttachment struct {
	ID                uuid.UUID        `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ClientID          uuid.UUID        `gorm:"type:uuid;not null" json:"clientId"`
	RouteID           uuid.UUID        `gorm:"type:uuid;not null" json:"routeId"`
	EnableIPAllowlist bool             `gorm:"not null;default:false" json:"enableIpAllowlist"`
	EnableAPIKey      bool             `gorm:"column:enable_api_key;not null;default:false" json:"enableApiKey"`
	EnableJWT         bool             `gorm:"column:enable_jwt;not null;default:false" json:"enableJwt"`
	EnableBasicAuth   bool             `gorm:"not null;default:false" json:"enableBasicAuth"`
	EnableMTLS        bool             `gorm:"column:enable_mtls;not null;default:false" json:"enableMtls"`
	EnableHeaderAuth  bool             `gorm:"not null;default:false" json:"enableHeaderAuth"`
	RateLimitConfig   *RateLimitConfig `gorm:"column:rate_limit_config;type:jsonb" json:"rateLimitConfig,omitempty"`
	ExtAuth           *ExtAuthConfig   `gorm:"column:ext_auth;type:jsonb" json:"extAuth,omitempty"`
	Status            AttachmentStatus `gorm:"not null;default:'pending_attach'" json:"status"`
	CreatedBy         uuid.UUID        `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt         time.Time        `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt         time.Time        `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Client  *Client `gorm:"foreignKey:ClientID" json:"client,omitempty"`
	Route   *Route  `gorm:"foreignKey:RouteID" json:"route,omitempty"`
	Creator *User   `gorm:"foreignKey:CreatedBy" json:"creator,omitempty"`

	// Computed (not in DB) - unified approval replaces the old dual-approval
	PendingApproval *Approval `gorm:"-" json:"pendingApproval,omitempty"`
}

// TableName returns the table name for ClientRouteAttachment
func (ClientRouteAttachment) TableName() string {
	return "client_route_attachments"
}

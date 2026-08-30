package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// RouteVersion represents a historical version of a route's deployed state
type RouteVersion struct {
	ID                uuid.UUID       `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	RouteID           uuid.UUID       `json:"routeId" gorm:"type:uuid;not null"`
	Version           int             `json:"version" gorm:"not null"`
	ConfigSnapshot    json.RawMessage `json:"configSnapshot" gorm:"type:jsonb;not null"`
	RouteDescription  string          `json:"routeDescription,omitempty"`
	Protocol          RouteProtocol   `json:"protocol" gorm:"type:varchar(20);not null"`
	SecurityMode      SecurityMode    `json:"securityMode" gorm:"type:varchar(50);not null"`
	ChangeDescription string          `json:"changeDescription,omitempty"`
	ApprovalID        *uuid.UUID      `json:"approvalId,omitempty" gorm:"type:uuid"`
	DeployedBy        uuid.UUID       `json:"deployedBy" gorm:"type:uuid;not null"`
	CreatedAt         time.Time       `json:"createdAt"`

	// Relationships (for preloading)
	Deployer *User `json:"deployer,omitempty" gorm:"foreignKey:DeployedBy"`
}

func (RouteVersion) TableName() string {
	return "route_versions"
}

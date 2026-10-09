package models

import (
	"time"

	"github.com/google/uuid"
)

// Stream represents an L4 (TCP/UDP) stream gateway (maps to a K8s Gateway).
type Stream struct {
	ID                uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID         uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:idx_stream_project_name" json:"projectId"`
	Name              string     `gorm:"not null;uniqueIndex:idx_stream_project_name" json:"name"`
	Namespace         string     `gorm:"not null" json:"namespace"`
	GatewayTemplateID uuid.UUID  `gorm:"type:uuid;not null" json:"gatewayTemplateId"`
	K8sGatewayName    string     `json:"k8sGatewayName"`
	K8sGatewayClass   string     `json:"k8sGatewayClass"`
	Status            string     `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage     string     `json:"statusMessage"`
	CreatedBy         *uuid.UUID `gorm:"type:uuid" json:"createdBy,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// TableName returns the table name for the Stream model.
func (Stream) TableName() string { return "streams" }

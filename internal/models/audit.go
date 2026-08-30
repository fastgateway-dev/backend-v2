package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// AuditLog represents an audit log entry
type AuditLog struct {
	ID           uuid.UUID    `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID    *uuid.UUID   `gorm:"type:uuid" json:"projectId,omitempty"`
	UserID       *uuid.UUID   `gorm:"type:uuid" json:"userId,omitempty"`
	Username     string       `gorm:"not null" json:"username"`
	Action       string       `gorm:"not null" json:"action"`
	ResourceType string       `gorm:"not null" json:"resourceType"`
	ResourceID   *uuid.UUID   `gorm:"type:uuid" json:"resourceId,omitempty"`
	ResourceName string       `json:"resourceName,omitempty"`
	Details      AuditDetails `gorm:"type:jsonb" json:"details,omitempty"`
	IPAddress    string       `json:"ipAddress,omitempty"`
	UserAgent    string       `json:"userAgent,omitempty"`
	CreatedAt    time.Time    `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships (optional, for eager loading)
	Project *Project `gorm:"foreignKey:ProjectID" json:"-"`
	User    *User    `gorm:"foreignKey:UserID" json:"-"`
}

// TableName returns the table name for AuditLog
func (AuditLog) TableName() string {
	return "audit_logs"
}

// AuditDetails represents additional details in an audit log
type AuditDetails map[string]interface{}

// Value implements the driver.Valuer interface for AuditDetails
func (ad AuditDetails) Value() (driver.Value, error) {
	if ad == nil {
		return nil, nil
	}
	return json.Marshal(ad)
}

// Scan implements the sql.Scanner interface for AuditDetails
func (ad *AuditDetails) Scan(value interface{}) error {
	if value == nil {
		*ad = nil
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(bytes, ad)
}

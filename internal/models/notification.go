package models

import (
	"time"

	"github.com/google/uuid"
)

// Notification represents an in-app notification for a user
type Notification struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null" json:"userId"`
	Type      string    `gorm:"not null;default:'mention'" json:"type"`
	Title     string    `gorm:"not null" json:"title"`
	Link      string    `gorm:"not null" json:"link"`
	IsRead    bool      `gorm:"not null;default:false" json:"isRead"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"createdAt"`
}

// TableName returns the table name for Notification
func (Notification) TableName() string {
	return "notifications"
}

package models

import (
	"time"

	"github.com/google/uuid"
)

// ApprovalComment represents a comment on an approval
type ApprovalComment struct {
	ID         uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ApprovalID uuid.UUID `gorm:"type:uuid;not null" json:"approvalId"`
	UserID     uuid.UUID `gorm:"type:uuid;not null" json:"userId"`
	Body       string    `gorm:"not null" json:"body"`
	CreatedAt  time.Time `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName returns the table name for ApprovalComment
func (ApprovalComment) TableName() string {
	return "approval_comments"
}

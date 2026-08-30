package models

import (
	"time"

	"github.com/google/uuid"
)

// TeamEmailInvite represents a pending team invitation by email
type TeamEmailInvite struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	TeamID    uuid.UUID `gorm:"type:uuid;not null" json:"teamId"`
	Email     string    `gorm:"not null" json:"email"`
	InvitedBy uuid.UUID `gorm:"type:uuid;not null" json:"invitedBy"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	Team    Team `gorm:"foreignKey:TeamID" json:"team,omitempty"`
	Inviter User `gorm:"foreignKey:InvitedBy" json:"inviter,omitempty"`
}

func (TeamEmailInvite) TableName() string {
	return "team_email_invites"
}

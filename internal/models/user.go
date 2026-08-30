package models

import (
	"time"

	"github.com/google/uuid"
)

// UserRole represents the system-level role of a user
type UserRole string

const (
	UserRoleOwner UserRole = "owner"
	UserRoleUser  UserRole = "user"
)

// User represents a user in the system
type User struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Username        string    `gorm:"uniqueIndex;not null" json:"username"`
	Email           string    `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash    *string   `gorm:"column:password_hash" json:"-"`
	Role            UserRole  `gorm:"not null;default:'user'" json:"role"`
	IsActive        bool      `gorm:"not null;default:true" json:"isActive"`
	AuthProvider    string    `gorm:"not null;default:'local'" json:"authProvider"`
	ProviderSubject *string   `gorm:"column:provider_subject" json:"providerSubject,omitempty"`
	CreatedAt       time.Time `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt       time.Time `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	APITokens    []APIToken     `gorm:"foreignKey:UserID" json:"-"`
	TeamMembers  []TeamMember   `gorm:"foreignKey:UserID" json:"-"`
	ProjectAdmin []ProjectAdmin `gorm:"foreignKey:UserID" json:"-"`
}

// TableName returns the table name for User
func (User) TableName() string {
	return "users"
}

// APIToken represents an API token for programmatic access
type APIToken struct {
	ID         uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	UserID     uuid.UUID  `gorm:"type:uuid;not null" json:"userId"`
	Name       string     `gorm:"not null" json:"name"`
	TokenHash  string     `gorm:"uniqueIndex;not null" json:"-"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	CreatedAt  time.Time  `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	User User `gorm:"foreignKey:UserID" json:"-"`
}

// TableName returns the table name for APIToken
func (APIToken) TableName() string {
	return "api_tokens"
}

package models

import (
	"time"

	"github.com/google/uuid"
)

// SystemSettings stores runtime-configurable system settings (singleton)
type SystemSettings struct {
	ID                 uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	BaseURL            string    `gorm:"column:base_url;not null;default:''" json:"baseUrl"`
	JWTExpiry          string    `gorm:"column:jwt_expiry;not null;default:''" json:"jwtExpiry"`
	RefreshTokenExpiry string    `gorm:"column:refresh_token_expiry;not null;default:''" json:"refreshTokenExpiry"`
	LogLevel           string    `gorm:"column:log_level;not null;default:''" json:"logLevel"`
	CreatedAt          time.Time `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt          time.Time `gorm:"not null;default:now()" json:"updatedAt"`
}

func (SystemSettings) TableName() string {
	return "system_settings"
}

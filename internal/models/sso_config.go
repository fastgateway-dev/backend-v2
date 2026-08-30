package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// SSOConfig represents the system-level SSO configuration (singleton)
type SSOConfig struct {
	ID                    uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Enabled               bool           `gorm:"not null;default:false" json:"enabled"`
	ProviderName          string         `gorm:"not null;default:''" json:"providerName"`
	IssuerURL             string         `gorm:"column:issuer_url;not null;default:''" json:"issuerUrl"`
	ClientID              string         `gorm:"column:client_id;not null;default:''" json:"clientId"`
	ClientSecretEncrypted string         `gorm:"column:client_secret_encrypted;not null;default:''" json:"-"`
	Scopes                pq.StringArray `gorm:"type:text[];not null;default:'{openid,email,profile}'" json:"scopes"`
	AllowedDomains        pq.StringArray `gorm:"type:text[]" json:"allowedDomains"`
	AllowedEmails         pq.StringArray `gorm:"type:text[]" json:"allowedEmails"`
	AutoRegister          bool           `gorm:"not null;default:true" json:"autoRegister"`
	ForceSSO              bool           `gorm:"column:force_sso;not null;default:false" json:"forceSSO"`
	CreatedAt             time.Time      `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt             time.Time      `gorm:"not null;default:now()" json:"updatedAt"`
}

func (SSOConfig) TableName() string {
	return "sso_config"
}

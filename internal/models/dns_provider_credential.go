package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// DNSCredentialData is the encrypted, provider-specific credential payload,
// stored as JSONB. For cloudflare: {"apiToken": "<encrypted>"}.
type DNSCredentialData map[string]string

func (d DNSCredentialData) Value() (driver.Value, error) {
	if d == nil {
		return "{}", nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (d *DNSCredentialData) Scan(value interface{}) error {
	if value == nil {
		*d = make(DNSCredentialData)
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan DNSCredentialData: unexpected type")
	}
	return json.Unmarshal(bytes, d)
}

// DNSProviderCredential is a platform-global (owner-managed) DNS provider account.
// Standalone by design so a future DNS-management feature can reuse it.
type DNSProviderCredential struct {
	ID           uuid.UUID         `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name         string            `gorm:"not null" json:"name"`
	ProviderType string            `gorm:"column:provider_type;not null" json:"providerType"`
	// Values inside are individually encrypted by the service layer; never serialized.
	Credentials  DNSCredentialData `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	CreatedBy    uuid.UUID         `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt    time.Time         `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt    time.Time         `gorm:"not null;default:now()" json:"updatedAt"`
}

func (DNSProviderCredential) TableName() string { return "dns_provider_credentials" }

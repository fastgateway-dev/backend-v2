package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type IssuerType string

const (
	IssuerTypeSelfSignedCA IssuerType = "self_signed_ca"
	IssuerTypeACME         IssuerType = "acme"
)

type IssuerStatus string

const (
	IssuerStatusPending IssuerStatus = "pending"
	IssuerStatusReady   IssuerStatus = "ready"
	IssuerStatusError   IssuerStatus = "error"
)

// IssuerConfig is the polymorphic JSONB config. CA fields are used when
// Type=self_signed_ca; ACME fields when Type=acme. Resolved cert-manager object
// names are recorded so later phases and deletes can find them.
type IssuerConfig struct {
	// self_signed_ca
	CommonName   string `json:"commonName,omitempty"`
	KeyAlgorithm string `json:"keyAlgorithm,omitempty"`
	KeySize      int    `json:"keySize,omitempty"`
	DurationDays int    `json:"durationDays,omitempty"`
	CASecretName string `json:"caSecretName,omitempty"`

	// acme
	Server          string     `json:"server,omitempty"`
	Email           string     `json:"email,omitempty"`
	EABKeyID        string     `json:"eabKeyId,omitempty"`
	DNSCredentialID *uuid.UUID `json:"dnsCredentialId,omitempty"`

	// resolved cert-manager object names (both types)
	IssuerName string `json:"issuerName,omitempty"`
	// for acme: the account key + EAB HMAC secret names
	AccountSecretName string `json:"accountSecretName,omitempty"`
	SolverSecretName  string `json:"solverSecretName,omitempty"`
	// for acme: the EAB HMAC key Secret name, set only when the issuer was
	// created with EAB credentials (e.g. ZeroSSL); needed so Delete can clean
	// it up alongside the other acme secrets.
	EABSecretName string `json:"eabSecretName,omitempty"`
}

func (c IssuerConfig) Value() (driver.Value, error) { return json.Marshal(c) }
func (c *IssuerConfig) Scan(value interface{}) error {
	if value == nil {
		*c = IssuerConfig{}
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		if s, ok2 := value.(string); ok2 {
			b = []byte(s)
		} else {
			return errors.New("failed to scan IssuerConfig")
		}
	}
	return json.Unmarshal(b, c)
}

type CertificateIssuer struct {
	ID            uuid.UUID    `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name          string       `gorm:"not null" json:"name"`
	Type          IssuerType   `gorm:"not null" json:"type"`
	Status        IssuerStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage string       `gorm:"column:status_message" json:"statusMessage,omitempty"`
	Config        IssuerConfig `gorm:"type:jsonb;not null;default:'{}'" json:"config"`
	CreatedBy     uuid.UUID    `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt     time.Time    `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt     time.Time    `gorm:"not null;default:now()" json:"updatedAt"`
}

func (CertificateIssuer) TableName() string { return "certificate_issuers" }

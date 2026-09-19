package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type ManagedCertUsage string

const (
	ManagedCertUsageServer ManagedCertUsage = "server"
	ManagedCertUsageClient ManagedCertUsage = "client"
)

type ManagedCertStatus string

const (
	ManagedCertStatusPending ManagedCertStatus = "pending"
	ManagedCertStatusIssuing ManagedCertStatus = "issuing"
	ManagedCertStatusReady   ManagedCertStatus = "ready"
	ManagedCertStatusError   ManagedCertStatus = "error"
)

type ManagedCertConfig struct {
	DNSNames        []string `json:"dnsNames,omitempty"`
	Subject         string   `json:"subject,omitempty"`
	SecretName      string   `json:"secretName,omitempty"`
	CertificateName string   `json:"certificateName,omitempty"`
	KeyAlgorithm    string   `json:"keyAlgorithm,omitempty"`
	KeySize         int      `json:"keySize,omitempty"`
	DurationDays    int      `json:"durationDays,omitempty"`
}

func (c ManagedCertConfig) Value() (driver.Value, error) { return json.Marshal(c) }
func (c *ManagedCertConfig) Scan(value interface{}) error {
	if value == nil {
		*c = ManagedCertConfig{}
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		if s, ok2 := value.(string); ok2 {
			b = []byte(s)
		} else {
			return errors.New("failed to scan ManagedCertConfig")
		}
	}
	return json.Unmarshal(b, c)
}

type ManagedCertificate struct {
	ID            uuid.UUID         `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID     uuid.UUID         `gorm:"type:uuid;not null;index" json:"projectId"`
	Name          string            `gorm:"not null" json:"name"`
	IssuerID      uuid.UUID         `gorm:"type:uuid;not null;index" json:"issuerId"`
	Usage         ManagedCertUsage  `gorm:"not null" json:"usage"`
	Config        ManagedCertConfig `gorm:"type:jsonb;not null;default:'{}'" json:"config"`
	Status        ManagedCertStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage string            `gorm:"column:status_message" json:"statusMessage,omitempty"`
	Fingerprint   string            `gorm:"column:fingerprint" json:"fingerprint,omitempty"`
	NotAfter      *time.Time        `gorm:"column:not_after" json:"notAfter,omitempty"`
	CreatedBy     uuid.UUID         `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt     time.Time         `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt     time.Time         `gorm:"not null;default:now()" json:"updatedAt"`
}

func (ManagedCertificate) TableName() string { return "managed_certificates" }

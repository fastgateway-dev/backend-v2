package models

import (
	"time"

	"github.com/google/uuid"
)

type DNSZoneStatus string

const (
	DNSZoneStatusReady   DNSZoneStatus = "ready"
	DNSZoneStatusError   DNSZoneStatus = "error"
	DNSZoneStatusPending DNSZoneStatus = "pending"
)

// DNSHostedZone is a DNS zone (domain apex) managed under a DNS provider
// credential. A single hosted zone can back multiple DomainDNSRecords.
type DNSHostedZone struct {
	ID                   uuid.UUID     `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name                 string        `gorm:"not null" json:"name"`
	ProviderCredentialID uuid.UUID     `gorm:"type:uuid;not null;index" json:"providerCredentialId"`
	ProviderZoneID       string        `gorm:"column:provider_zone_id" json:"-"`
	Status               DNSZoneStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage        string        `gorm:"column:status_message" json:"statusMessage,omitempty"`
	CreatedBy            uuid.UUID     `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt            time.Time     `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt            time.Time     `gorm:"not null;default:now()" json:"updatedAt"`
}

func (DNSHostedZone) TableName() string { return "dns_hosted_zones" }

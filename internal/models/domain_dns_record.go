package models

import (
	"time"

	"github.com/google/uuid"
)

type DNSRecordStatus string

const (
	DNSRecordStatusPending DNSRecordStatus = "pending"
	DNSRecordStatusReady   DNSRecordStatus = "ready"
	DNSRecordStatusError   DNSRecordStatus = "error"
)

type DNSRecordType string

const (
	DNSRecordTypeAuto  DNSRecordType = "auto"
	DNSRecordTypeA     DNSRecordType = "A"
	DNSRecordTypeAAAA  DNSRecordType = "AAAA"
	DNSRecordTypeCNAME DNSRecordType = "CNAME"
)

// DomainDNSRecord is the one FastGateway-managed DNS record for a domain.
// Hostname is always the domain's hostname (not stored). ResolvedTarget is a
// display cache of the last gateway address we resolved.
type DomainDNSRecord struct {
	ID             uuid.UUID       `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	DomainID       uuid.UUID       `gorm:"type:uuid;not null;uniqueIndex" json:"domainId"`
	HostedZoneID   uuid.UUID       `gorm:"type:uuid;not null;index" json:"hostedZoneId"`
	RecordType     DNSRecordType   `gorm:"column:record_type;not null;default:'auto'" json:"recordType"`
	TTL            *int            `gorm:"column:ttl" json:"ttl,omitempty"`
	Proxied        bool            `gorm:"not null;default:false" json:"proxied"`
	ResolvedTarget string          `gorm:"column:resolved_target" json:"resolvedTarget,omitempty"`
	Status         DNSRecordStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage  string          `gorm:"column:status_message" json:"statusMessage,omitempty"`
	CreatedBy      uuid.UUID       `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt      time.Time       `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt      time.Time       `gorm:"not null;default:now()" json:"updatedAt"`
}

func (DomainDNSRecord) TableName() string { return "domain_dns_records" }

// DNSRecordListItem is a read projection for the project-wide DNS records list:
// a domain's managed DNS record joined with its domain hostname (the record's
// name) and the name of the hosted zone it lives in. It is not a table.
type DNSRecordListItem struct {
	DomainDNSRecord
	DomainHostname string `json:"domainHostname"`
	ZoneName       string `json:"zoneName"`
}

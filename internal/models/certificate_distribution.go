package models

import (
	"time"

	"github.com/google/uuid"
)

type CertDistStatus string

const (
	CertDistStatusPending CertDistStatus = "pending"
	CertDistStatusSynced  CertDistStatus = "synced"
	CertDistStatusError   CertDistStatus = "error"
)

type CertificateDistribution struct {
	ID                    uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ManagedCertificateID  uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex" json:"managedCertificateId"`
	ProjectID             uuid.UUID      `gorm:"type:uuid;not null;index" json:"projectId"`
	Status                CertDistStatus `gorm:"not null;default:'pending'" json:"status"`
	LastPushedFingerprint string         `gorm:"column:last_pushed_fingerprint" json:"lastPushedFingerprint,omitempty"`
	Message               string         `gorm:"column:message" json:"message,omitempty"`
	LastSyncedAt          *time.Time     `gorm:"column:last_synced_at" json:"lastSyncedAt,omitempty"`
	CreatedAt             time.Time      `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt             time.Time      `gorm:"not null;default:now()" json:"updatedAt"`
}

func (CertificateDistribution) TableName() string { return "certificate_distributions" }

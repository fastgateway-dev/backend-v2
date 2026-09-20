package models

import (
	"time"

	"github.com/google/uuid"
)

// CertificateExportGrant is a short-lived, single-use permission for one
// user to export a managed certificate's private key material once. Unlike
// the brief's original URL-token design, a grant carries no secret of its
// own -- it is looked up by (ManagedCertificateID, GrantedTo) because every
// API call is already authenticated, so a bearer token in a URL query
// string would only add a sensitive value with no offsetting benefit.
// ConsumeForCert (see CertificateExportGrantRepositoryInterface) atomically
// finds the caller's own unconsumed, unexpired grant and marks it consumed,
// making it single-use.
type CertificateExportGrant struct {
	ID                   uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ManagedCertificateID uuid.UUID  `gorm:"type:uuid;not null;index" json:"managedCertificateId"`
	GrantedTo            uuid.UUID  `gorm:"type:uuid;not null" json:"grantedTo"`
	ExpiresAt            time.Time  `gorm:"not null" json:"expiresAt"`
	ConsumedAt           *time.Time `gorm:"column:consumed_at" json:"consumedAt,omitempty"`
	CreatedAt            time.Time  `gorm:"not null;default:now()" json:"createdAt"`
}

func (CertificateExportGrant) TableName() string { return "certificate_export_grants" }

package models

import (
	"time"

	"github.com/google/uuid"
)

// IssuerProjectGrant records that a platform-global CertificateIssuer has
// been made visible to a project: until a grant row exists for
// (IssuerID, ProjectID), the issuer is invisible to that project (Phase 2's
// ManagedCertificate create flow will look up grants to decide which issuers
// a project may select). The unique index prevents duplicate grants; ON
// DELETE CASCADE on both FKs (see the migration) means a grant disappears
// automatically if either the issuer or the project is deleted.
type IssuerProjectGrant struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	IssuerID  uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_issuer_project" json:"issuerId"`
	ProjectID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_issuer_project" json:"projectId"`
	CreatedBy uuid.UUID `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"createdAt"`
}

func (IssuerProjectGrant) TableName() string { return "issuer_project_grants" }

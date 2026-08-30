package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Project namespace capabilities. Storage is an array so new capabilities
// can be added without schema migrations.
const (
	NamespaceCapabilityDeployGateway  = "deploy_gateway"
	NamespaceCapabilityBackendService = "backend_service"
	NamespaceCapabilityTLSSecret      = "tls_secret"
)

// AllowedNamespaceCapabilities is the set of capabilities the API accepts.
var AllowedNamespaceCapabilities = []string{
	NamespaceCapabilityDeployGateway,
	NamespaceCapabilityBackendService,
	NamespaceCapabilityTLSSecret,
}

// ProjectNamespace represents a namespace a project can use for deployment
// of Gateway/Route CRDs and/or as a target for backend Services and TLS
// Secrets. Allowed roles are encoded in Capabilities.
type ProjectNamespace struct {
	ID                    uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID             uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex:idx_project_namespace" json:"projectId"`
	Namespace             string         `gorm:"not null;uniqueIndex:idx_project_namespace" json:"namespace"`
	Capabilities          pq.StringArray `gorm:"type:text[];not null;default:'{}'" json:"capabilities"`
	ReferenceGrantCreated bool           `gorm:"column:reference_grant_created;not null;default:false" json:"referenceGrantCreated"`
	CreatedAt             time.Time      `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt             time.Time      `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Project Project `gorm:"foreignKey:ProjectID" json:"-"`
}

// HasCapability reports whether the namespace is registered for the given role.
func (n *ProjectNamespace) HasCapability(c string) bool {
	for _, x := range n.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}

// ReferenceGrantKindsForCapabilities maps capability values to the Gateway-API
// ReferenceGrant `to` kinds that should appear in the namespace's grant.
// Returns nil when the capabilities require no grant.
func ReferenceGrantKindsForCapabilities(caps []string) []string {
	var kinds []string
	for _, c := range caps {
		switch c {
		case NamespaceCapabilityBackendService:
			kinds = append(kinds, "Service")
		case NamespaceCapabilityTLSSecret:
			kinds = append(kinds, "Secret")
		}
	}
	return kinds
}

// TableName returns the table name for ProjectNamespace
func (ProjectNamespace) TableName() string {
	return "project_namespaces"
}

package models

import (
	"time"

	"github.com/google/uuid"
)

// TLSPolicy represents the TLS policy for a domain
type TLSPolicy string

const (
	TLSPolicyTerminate   TLSPolicy = "terminate"
	TLSPolicyPassthrough TLSPolicy = "passthrough"
)

// DomainStatus represents the status of a domain
type DomainStatus string

const (
	DomainStatusPending DomainStatus = "pending"
	DomainStatusActive  DomainStatus = "active"
	DomainStatusError   DomainStatus = "error"
)

// Domain represents an API Gateway domain (maps to K8s Gateway)
type Domain struct {
	ID                 uuid.UUID    `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID          uuid.UUID    `gorm:"type:uuid;not null;uniqueIndex:idx_domain_project_hostname" json:"projectId"`
	DomainTemplateID   *uuid.UUID   `gorm:"type:uuid" json:"domainTemplateId,omitempty"`
	Name               string       `gorm:"not null" json:"name"`
	Hostname           string       `gorm:"not null;uniqueIndex:idx_domain_project_hostname" json:"hostname"`
	HTTPPort           int          `gorm:"column:http_port;not null;default:80" json:"httpPort"`
	HTTPSPort          int          `gorm:"column:https_port;not null;default:443" json:"httpsPort"`
	TLSMode            string       `gorm:"column:tls_mode;not null;default:'tls_only'" json:"tlsMode"`
	Namespace          string       `gorm:"not null;default:'fastgateway-system'" json:"namespace"`
	TLSSecretName      string       `json:"tlsSecretName"`
	TLSSecretNamespace string       `json:"tlsSecretNamespace,omitempty"`
	TLSPolicy          TLSPolicy    `gorm:"not null;default:'terminate'" json:"tlsPolicy"`
	K8sGatewayName     string       `gorm:"column:k8s_gateway_name" json:"k8sGatewayName"`
	K8sGatewayClass    string       `gorm:"column:k8s_gateway_class_name" json:"k8sGatewayClassName"`
	Status             DomainStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage      string       `json:"statusMessage,omitempty"`
	CreatedBy          uuid.UUID    `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt          time.Time    `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt          time.Time    `gorm:"not null;default:now()" json:"updatedAt"`
	Labels             Labels       `gorm:"type:jsonb;default:'{}'" json:"labels,omitempty"`

	// Relationships
	Project        Project         `gorm:"foreignKey:ProjectID" json:"-"`
	DomainTemplate *DomainTemplate `gorm:"foreignKey:DomainTemplateID" json:"-"`
	Creator        User            `gorm:"foreignKey:CreatedBy" json:"-"`
	Routes         []Route         `gorm:"foreignKey:DomainID" json:"-"`

	// Computed fields (not stored in DB)
	RouteCount int `gorm:"-" json:"routeCount,omitempty"`
}

// TableName returns the table name for Domain
func (Domain) TableName() string {
	return "domains"
}

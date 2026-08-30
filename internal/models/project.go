package models

import (
	"time"

	"github.com/google/uuid"
)

// Project represents a Kubernetes cluster connection
type Project struct {
	ID                    uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name                  string     `gorm:"not null" json:"name"`
	Description           string     `json:"description"`
	ConnectionType        string     `gorm:"column:connection_type;not null;default:'api_token'" json:"connectionType"`
	K8sAPIURL             string     `gorm:"column:k8s_api_url" json:"k8sApiUrl"`
	K8sTokenEncrypted     string     `gorm:"column:k8s_token_encrypted" json:"-"`
	K8sCACert             string     `gorm:"column:k8s_ca_cert" json:"-"`
	K8sTLSSkipVerify      bool       `gorm:"column:k8s_tls_skip_verify;not null;default:true" json:"k8sTlsSkipVerify"`
	K8sClientCert         string     `gorm:"column:k8s_client_cert" json:"-"`
	K8sClientKeyEncrypted string     `gorm:"column:k8s_client_key_encrypted" json:"-"`
	IsConnected           bool       `gorm:"not null;default:false" json:"isConnected"`
	LastConnectedAt       *time.Time `json:"lastConnectedAt"`
	CreatedBy             uuid.UUID  `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt             time.Time  `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt             time.Time  `gorm:"not null;default:now()" json:"updatedAt"`
	Labels                Labels     `gorm:"type:jsonb;default:'{}'" json:"labels,omitempty"`
	ApprovalEnabled       bool       `gorm:"column:approval_enabled;not null;default:true" json:"approvalEnabled"`
	SelfApprovalAllowed   bool       `gorm:"column:self_approval_allowed;not null;default:false" json:"selfApprovalAllowed"`

	// Observability (Prometheus / VictoriaMetrics)
	MetricsEndpointURL       string `gorm:"column:metrics_endpoint_url;not null;default:''" json:"metricsEndpointUrl"`
	MetricsAuthType          string `gorm:"column:metrics_auth_type;not null;default:'none'" json:"metricsAuthType"`
	MetricsUsername          string `gorm:"column:metrics_username;not null;default:''" json:"metricsUsername"`
	MetricsPasswordEncrypted string `gorm:"column:metrics_password_encrypted;not null;default:''" json:"-"`
	MetricsTokenEncrypted    string `gorm:"column:metrics_token_encrypted;not null;default:''" json:"-"`
	MetricsTLSSkipVerify     bool   `gorm:"column:metrics_tls_skip_verify;not null;default:false" json:"metricsTlsSkipVerify"`
	MetricsCACert            string `gorm:"column:metrics_ca_cert;not null;default:''" json:"metricsCaCert"`

	// Relationships
	Creator          User              `gorm:"foreignKey:CreatedBy" json:"-"`
	Admins           []ProjectAdmin    `gorm:"foreignKey:ProjectID" json:"-"`
	ProjectTeamRoles []ProjectTeamRole `gorm:"foreignKey:ProjectID" json:"-"`
	Domains          []Domain          `gorm:"foreignKey:ProjectID" json:"-"`

	// Computed fields (not stored in DB)
	DomainCount int `gorm:"-" json:"domainCount,omitempty"`
	RouteCount  int `gorm:"-" json:"routeCount,omitempty"`
}

// TableName returns the table name for Project
func (Project) TableName() string {
	return "projects"
}

// ProjectAdmin represents an admin assignment to a project
type ProjectAdmin struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_project_admin" json:"projectId"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_project_admin" json:"userId"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	Project Project `gorm:"foreignKey:ProjectID" json:"-"`
	User    User    `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName returns the table name for ProjectAdmin
func (ProjectAdmin) TableName() string {
	return "project_admins"
}

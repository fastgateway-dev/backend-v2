package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ExposureType represents the service type for the gateway
type ExposureType string

const (
	ExposureTypeLoadBalancer ExposureType = "LoadBalancer" // LoadBalancer service
	ExposureTypeClusterIP    ExposureType = "ClusterIP"    // ClusterIP service (in-cluster only)
)

// TLSMode represents the TLS configuration mode
type TLSMode string

const (
	TLSModeOnly TLSMode = "tls_only" // HTTPS only
	TLSModeNone TLSMode = "no_tls"   // HTTP only
	TLSModeBoth TLSMode = "both"     // HTTP + HTTPS
)

// ExternalTrafficPolicy represents the external traffic policy for LoadBalancer
type ExternalTrafficPolicy string

const (
	ExternalTrafficPolicyCluster ExternalTrafficPolicy = "Cluster"
	ExternalTrafficPolicyLocal   ExternalTrafficPolicy = "Local"
)

// DomainTemplateStatus represents the status of a domain template
type DomainTemplateStatus string

const (
	DomainTemplateStatusPending DomainTemplateStatus = "pending"
	DomainTemplateStatusActive  DomainTemplateStatus = "active"
	DomainTemplateStatusError   DomainTemplateStatus = "error"
)

// Annotations represents a map of annotations for Gateway resources
type Annotations map[string]string

// Value implements the driver.Valuer interface for Annotations
func (a Annotations) Value() (driver.Value, error) {
	if a == nil {
		return "{}", nil
	}
	return json.Marshal(a)
}

// Scan implements the sql.Scanner interface for Annotations
func (a *Annotations) Scan(value interface{}) error {
	if value == nil {
		*a = make(Annotations)
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan Annotations: unsupported type")
	}

	// Handle empty string or empty JSON object
	if len(bytes) == 0 || string(bytes) == "{}" {
		*a = make(Annotations)
		return nil
	}

	return json.Unmarshal(bytes, a)
}

// ContainerResourcesConfig represents container resource requests and limits
type ContainerResourcesConfig struct {
	Requests *ResourceValues `json:"requests,omitempty"`
	Limits   *ResourceValues `json:"limits,omitempty"`
}

// ResourceValues represents CPU and memory values
type ResourceValues struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// Value implements the driver.Valuer interface
func (c ContainerResourcesConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface
func (c *ContainerResourcesConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan ContainerResourcesConfig: unsupported type")
	}
	return json.Unmarshal(bytes, c)
}

// ScalingConfig represents scaling configuration (fixed replicas or HPA)
type ScalingConfig struct {
	Type        string `json:"type"`                  // "fixed" or "hpa"
	Replicas    *int32 `json:"replicas,omitempty"`    // for type=fixed
	MinReplicas *int32 `json:"minReplicas,omitempty"` // for type=hpa
	MaxReplicas *int32 `json:"maxReplicas,omitempty"` // for type=hpa
}

// Value implements the driver.Valuer interface
func (s ScalingConfig) Value() (driver.Value, error) {
	return json.Marshal(s)
}

// Scan implements the sql.Scanner interface
func (s *ScalingConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan ScalingConfig: unsupported type")
	}
	return json.Unmarshal(bytes, s)
}

// DomainTemplate represents a template for creating domains with predefined settings
type DomainTemplate struct {
	ID             uuid.UUID    `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ProjectID      uuid.UUID    `gorm:"type:uuid;not null;uniqueIndex:idx_domaintemplate_project_name" json:"projectId"`
	Name           string       `gorm:"not null;uniqueIndex:idx_domaintemplate_project_name" json:"name"`
	Description    string       `json:"description"`
	ControllerName string       `gorm:"not null;default:'gateway.envoyproxy.io/gatewayclass-controller'" json:"controllerName"`
	ExposureType   ExposureType `gorm:"not null;default:'public'" json:"exposureType"`
	TLSMode        TLSMode      `gorm:"column:tls_mode;not null;default:'tls_only'" json:"tlsMode"`

	// Advanced settings
	HTTPPort              int                       `gorm:"column:http_port;not null;default:80" json:"httpPort"`
	HTTPSPort             int                       `gorm:"column:https_port;not null;default:443" json:"httpsPort"`
	TLSPolicy             TLSPolicy                 `gorm:"column:tls_policy;not null;default:'terminate'" json:"tlsPolicy"`
	ExternalTrafficPolicy ExternalTrafficPolicy     `gorm:"column:external_traffic_policy" json:"externalTrafficPolicy,omitempty"`
	LoadBalancerClass     string                    `gorm:"column:load_balancer_class" json:"loadBalancerClass,omitempty"`
	Annotations           Annotations               `gorm:"type:jsonb;default:'{}'" json:"annotations"`
	PodAnnotations        Annotations               `gorm:"column:pod_annotations;type:jsonb;default:'{}'" json:"podAnnotations"`
	ContainerResources    *ContainerResourcesConfig `gorm:"column:container_resources;type:jsonb" json:"containerResources,omitempty"`
	ScalingConfig         *ScalingConfig            `gorm:"column:scaling_config;type:jsonb" json:"scalingConfig,omitempty"`
	MergeGateways         bool                      `gorm:"column:merge_gateways;not null;default:false" json:"mergeGateways"`

	// Telemetry (per spec.telemetry on EnvoyProxy CRD)
	TelemetryAccessLog *TelemetryAccessLogConfig `gorm:"column:telemetry_access_log;type:jsonb" json:"telemetryAccessLog,omitempty"`
	TelemetryTracing   *TelemetryTracingConfig   `gorm:"column:telemetry_tracing;type:jsonb"   json:"telemetryTracing,omitempty"`
	TelemetryMetrics   *TelemetryMetricsConfig   `gorm:"column:telemetry_metrics;type:jsonb"   json:"telemetryMetrics,omitempty"`

	// Pod scheduling, PDB, deployment strategy (per spec.provider.kubernetes on EnvoyProxy CRD)
	PodPlacement       *PodPlacementConfig       `gorm:"column:pod_placement;type:jsonb"       json:"podPlacement,omitempty"`
	PDBConfig          *PDBConfig                `gorm:"column:pdb_config;type:jsonb"          json:"pdbConfig,omitempty"`
	DeploymentStrategy *DeploymentStrategyConfig `gorm:"column:deployment_strategy;type:jsonb" json:"deploymentStrategy,omitempty"`

	// Status and metadata
	Status              DomainTemplateStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage       string               `json:"statusMessage,omitempty"`
	K8sGatewayClassName string               `gorm:"column:k8s_gateway_class_name" json:"k8sGatewayClassName"`
	K8sEnvoyProxyName   string               `gorm:"column:k8s_envoy_proxy_name" json:"k8sEnvoyProxyName,omitempty"`
	CreatedBy           uuid.UUID            `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt           time.Time            `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt           time.Time            `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Project Project `gorm:"foreignKey:ProjectID" json:"-"`
	Creator User    `gorm:"foreignKey:CreatedBy" json:"-"`
}

// TableName returns the table name for DomainTemplate
func (DomainTemplate) TableName() string {
	return "domain_templates"
}

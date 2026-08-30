package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RouteStatus represents the status of a route
type RouteStatus string

const (
	RouteStatusPendingCreate RouteStatus = "pending_create" // Waiting for approval
	RouteStatusPendingUpdate RouteStatus = "pending_update" // Update waiting for approval
	RouteStatusPendingDelete RouteStatus = "pending_delete" // Deletion waiting for approval
	RouteStatusApproved      RouteStatus = "approved"       // Approved, waiting for deployment by owner
	RouteStatusActive        RouteStatus = "active"         // Deployed to Kubernetes
	RouteStatusRejected      RouteStatus = "rejected"       // Rejected by approver
	RouteStatusPendingDeploy RouteStatus = "pending_deploy" // Approved update/delete, waiting for deployment
)

// RouteProtocol represents the protocol of a route
type RouteProtocol string

const (
	RouteProtocolHTTP RouteProtocol = "http"
	RouteProtocolGRPC RouteProtocol = "grpc"
)

// SecurityStatus represents the security status of a route
type SecurityStatus string

const (
	// SecurityStatusNone means no clients attached (neutral)
	SecurityStatusNone SecurityStatus = "none"
	// SecurityStatusWarning means clients attached but default policy allows bypass
	SecurityStatusWarning SecurityStatus = "warning"
	// SecurityStatusProtected means clients attached and default policy is secure
	SecurityStatusProtected SecurityStatus = "protected"
)

// SecurityMode represents the security configuration mode of a route
type SecurityMode string

const (
	SecurityModeGeneral SecurityMode = "general"
	SecurityModeClient  SecurityMode = "client"
)

// Route represents an API route (maps to K8s HTTPRoute/GRPCRoute)
type Route struct {
	ID           uuid.UUID     `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	DomainID     uuid.UUID     `gorm:"type:uuid;not null;uniqueIndex:idx_route_domain_name" json:"domainId"`
	TeamID       uuid.UUID     `gorm:"type:uuid;not null" json:"teamId"`
	Name         string        `gorm:"not null;uniqueIndex:idx_route_domain_name" json:"name"`
	Description  string        `json:"description"`
	Protocol     RouteProtocol `gorm:"not null;default:'http'" json:"protocol"`
	SecurityMode SecurityMode  `gorm:"column:security_mode;not null;default:'general'" json:"securityMode"`
	Config       RouteConfig   `gorm:"type:jsonb" json:"config"`
	Status       RouteStatus   `gorm:"not null;default:'pending_create'" json:"status"`
	K8sRouteName string        `gorm:"column:k8s_route_name" json:"-"`
	CreatedBy    uuid.UUID     `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt    time.Time     `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt    time.Time     `gorm:"not null;default:now()" json:"updatedAt"`
	Labels       Labels        `gorm:"type:jsonb;default:'{}'" json:"labels,omitempty"`

	// Relationships
	Domain  Domain `gorm:"foreignKey:DomainID" json:"-"`
	Team    *Team  `gorm:"foreignKey:TeamID" json:"team,omitempty"`
	Creator *User  `gorm:"foreignKey:CreatedBy" json:"creator,omitempty"`

	// Pending approval (if any)
	PendingApproval *Approval `gorm:"-" json:"pendingApproval,omitempty"`

	// Computed fields (not in DB)
	ClientCount    int            `gorm:"-" json:"clientCount,omitempty"`    // Number of active client attachments
	SecurityStatus SecurityStatus `gorm:"-" json:"securityStatus,omitempty"` // Security status based on clients and default policy
}

// TableName returns the table name for Route
func (Route) TableName() string {
	return "routes"
}

// RouteType represents the type of route (backend or redirect)
type RouteType string

const (
	RouteTypeBackend        RouteType = "backend"
	RouteTypeRedirect       RouteType = "redirect"
	RouteTypeDirectResponse RouteType = "directResponse"
)

// DefaultTrafficPolicy represents the policy for requests without client header
type DefaultTrafficPolicy string

const (
	// DefaultTrafficPolicyAllowAll allows all requests without client header (default, not recommended with clients)
	DefaultTrafficPolicyAllowAll DefaultTrafficPolicy = "allow_all"
	// DefaultTrafficPolicyDeny returns 403 for requests without client header
	DefaultTrafficPolicyDeny DefaultTrafficPolicy = "deny"
	// DefaultTrafficPolicyRequireIPAllowlist requires requests to come from allowed IPs
	DefaultTrafficPolicyRequireIPAllowlist DefaultTrafficPolicy = "require_ip_allowlist"
)

// RouteConfig represents the configuration of a route
type RouteConfig struct {
	RouteType              RouteType             `json:"routeType,omitempty"` // "backend", "redirect", or "directResponse", defaults to "backend"
	Matches                []RouteMatch          `json:"matches"`
	Backends               []RouteBackend        `json:"backends,omitempty"`       // Used when routeType is "backend"
	Mirrors                []MirrorBackend       `json:"mirrors,omitempty"`        // Mirror destinations for request mirroring
	Redirect               *RedirectConfig       `json:"redirect,omitempty"`       // Used when routeType is "redirect"
	DirectResponse         *DirectResponseConfig `json:"directResponse,omitempty"` // Used when routeType is "directResponse"
	RequestHeaderModifier  *HeaderModifier       `json:"requestHeaderModifier,omitempty"`
	ResponseHeaderModifier *HeaderModifier       `json:"responseHeaderModifier,omitempty"`
	URLRewrite             *URLRewrite           `json:"urlRewrite,omitempty"`

	// Default traffic policy for requests without x-client-id header (when clients are attached)
	DefaultTrafficPolicy DefaultTrafficPolicy `json:"defaultTrafficPolicy,omitempty"` // "allow_all", "deny", "require_ip_allowlist"
	DefaultAllowedCIDRs  []string             `json:"defaultAllowedCIDRs,omitempty"`  // CIDRs for "require_ip_allowlist" policy
	// Note: CORS is now handled via SecurityPolicy (Envoy Gateway specific)
}

// HasFailover returns true if any backend is marked as fallback
func (c *RouteConfig) HasFailover() bool {
	for _, b := range c.Backends {
		if b.Fallback {
			return true
		}
	}
	return false
}

// RedirectConfig represents HTTP redirect configuration
type RedirectConfig struct {
	Scheme     string       `json:"scheme,omitempty"`     // "http" or "https"
	Hostname   string       `json:"hostname,omitempty"`   // New hostname
	Port       *int         `json:"port,omitempty"`       // New port
	StatusCode int          `json:"statusCode,omitempty"` // 301 or 302
	Path       *PathRewrite `json:"path,omitempty"`       // Path rewrite for redirect
}

// DirectResponseBodyType represents the type of direct response body
type DirectResponseBodyType string

const (
	DirectResponseBodyTypeInline   DirectResponseBodyType = "Inline"
	DirectResponseBodyTypeValueRef DirectResponseBodyType = "ValueRef"
)

// DirectResponseConfig represents direct response configuration
type DirectResponseConfig struct {
	StatusCode  int                 `json:"statusCode"`            // HTTP status code (100-599)
	ContentType string              `json:"contentType,omitempty"` // e.g., "text/plain", "application/json"
	Body        *DirectResponseBody `json:"body,omitempty"`        // Response body
}

// DirectResponseBody represents the body of a direct response
type DirectResponseBody struct {
	Type   DirectResponseBodyType `json:"type"`             // "Inline" or "ValueRef"
	Inline string                 `json:"inline,omitempty"` // Inline body content (max 4096 bytes)
}

// Validate validates the direct response configuration
func (d *DirectResponseConfig) Validate() error {
	if d.StatusCode < 100 || d.StatusCode > 599 {
		return fmt.Errorf("statusCode must be between 100 and 599, got %d", d.StatusCode)
	}
	if d.Body != nil {
		if d.Body.Type != DirectResponseBodyTypeInline && d.Body.Type != DirectResponseBodyTypeValueRef {
			return fmt.Errorf("body type must be 'Inline' or 'ValueRef', got '%s'", d.Body.Type)
		}
		if d.Body.Type == DirectResponseBodyTypeInline && len(d.Body.Inline) > 4096 {
			return fmt.Errorf("inline body exceeds maximum size of 4096 bytes, got %d bytes", len(d.Body.Inline))
		}
	}
	return nil
}

// URLRewrite represents URL rewrite configuration
type URLRewrite struct {
	Hostname *string      `json:"hostname,omitempty"` // Rewrite Host header
	Path     *PathRewrite `json:"path,omitempty"`     // Rewrite path
}

// PathRewrite represents path rewrite configuration
type PathRewrite struct {
	Type               string `json:"type"`                         // ReplacePrefixMatch or ReplaceFullPath
	ReplacePrefixMatch string `json:"replacePrefixMatch,omitempty"` // Value for ReplacePrefixMatch
	ReplaceFullPath    string `json:"replaceFullPath,omitempty"`    // Value for ReplaceFullPath
}

// HeaderModifier represents header modification rules
type HeaderModifier struct {
	Set    []HeaderValue `json:"set,omitempty"`    // Set header (overwrite if exists)
	Add    []HeaderValue `json:"add,omitempty"`    // Add header (append if exists)
	Remove []string      `json:"remove,omitempty"` // Remove header by name
}

// HeaderValue represents a header name-value pair
type HeaderValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Value implements the driver.Valuer interface for RouteConfig
func (rc RouteConfig) Value() (driver.Value, error) {
	return json.Marshal(rc)
}

// Scan implements the sql.Scanner interface for RouteConfig
func (rc *RouteConfig) Scan(value interface{}) error {
	if value == nil {
		*rc = RouteConfig{}
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(bytes, rc)
}

// RouteMatch represents a matching rule for a route
type RouteMatch struct {
	Path        *PathMatch        `json:"path,omitempty"`
	Headers     []HeaderMatch     `json:"headers,omitempty"`
	Method      string            `json:"method,omitempty"`
	QueryParams []QueryParamMatch `json:"queryParams,omitempty"`
	// gRPC-specific matching (only for protocol=grpc)
	GRPCService *GRPCMethodMatch `json:"grpcService,omitempty"`
	GRPCMethod  *GRPCMethodMatch `json:"grpcMethod,omitempty"`
}

// PathMatch represents a path matching rule
type PathMatch struct {
	Type  string `json:"type"` // Exact, Prefix, RegularExpression
	Value string `json:"value"`
}

// HeaderMatch represents a header matching rule
type HeaderMatch struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // Exact, RegularExpression
	Value string `json:"value"`
}

// QueryParamMatch represents a query parameter matching rule
type QueryParamMatch struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // Exact, RegularExpression
	Value string `json:"value"`
}

// GRPCMethodMatch describes how to match a gRPC service or method
type GRPCMethodMatch struct {
	Type  string `json:"type,omitempty"`  // "Exact" or "RegularExpression"
	Value string `json:"value,omitempty"` // e.g., "helloworld.Greeter" or "SayHello"
}

// BackendType represents the type of backend
type BackendType string

const (
	BackendTypeKubernetes BackendType = "kubernetes"
	BackendTypeExternal   BackendType = "external"
)

// ExternalAddressType represents the type of external address
type ExternalAddressType string

const (
	ExternalAddressTypeFQDN ExternalAddressType = "fqdn"
	ExternalAddressTypeIP   ExternalAddressType = "ip"
)

// RouteBackend represents a backend service for a route
type RouteBackend struct {
	Type      BackendType `json:"type"` // kubernetes, external
	Service   string      `json:"service,omitempty"`
	Namespace string      `json:"namespace,omitempty"`
	Port      int         `json:"port"`
	Weight    int         `json:"weight,omitempty"`
	Fallback  bool        `json:"fallback,omitempty"` // If true, this backend only receives traffic when primary backends are unhealthy

	// External backend fields (used when Type is "external")
	AddressType ExternalAddressType `json:"addressType,omitempty"` // fqdn, ip
	Address     string              `json:"address,omitempty"`     // FQDN hostname or IP address
	TLS         *BackendTLSConfig   `json:"tls,omitempty"`         // TLS configuration for backends
}

// BackendTLSMode represents the TLS mode for backend connections
type BackendTLSMode string

const (
	BackendTLSModeSimple BackendTLSMode = "simple"
	BackendTLSModeMTLS   BackendTLSMode = "mtls"
)

// BackendTLSConfig configures TLS for backend connections
// Simple mode: verify backend's certificate using CA refs (or skip with insecureSkipVerify)
// mTLS mode: verify backend's certificate + present client certificate
type BackendTLSConfig struct {
	Mode                 BackendTLSMode   `json:"mode"`                           // "simple" or "mtls"
	InsecureSkipVerify   bool             `json:"insecureSkipVerify,omitempty"`   // skip backend cert verification
	SNI                  string           `json:"sni,omitempty"`                  // optional SNI override, auto-derived if empty
	CACertificateRefs    []CertificateRef `json:"caCertificateRefs,omitempty"`    // required unless insecureSkipVerify
	ClientCertificateRef *SecretRef       `json:"clientCertificateRef,omitempty"` // required for mtls mode
}

// CertificateRef references a Kubernetes Secret or ConfigMap containing certificates
type CertificateRef struct {
	Kind      string `json:"kind"` // "Secret" or "ConfigMap"
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"` // defaults to fastgateway-system
}

// Validate validates the BackendTLSConfig
func (t *BackendTLSConfig) Validate() error {
	if t == nil {
		return nil
	}

	// Mode is required
	if t.Mode == "" {
		return fmt.Errorf("mode is required when tls is configured")
	}
	if t.Mode != BackendTLSModeSimple && t.Mode != BackendTLSModeMTLS {
		return fmt.Errorf("mode must be 'simple' or 'mtls', got '%s'", t.Mode)
	}

	// insecureSkipVerify: CA refs must not be set
	if t.InsecureSkipVerify {
		if len(t.CACertificateRefs) > 0 {
			return fmt.Errorf("caCertificateRefs must not be set when insecureSkipVerify is true")
		}
	} else {
		// CA refs required when not skipping verification
		if len(t.CACertificateRefs) == 0 {
			return fmt.Errorf("caCertificateRefs is required when insecureSkipVerify is false")
		}
		// Validate each CA certificate ref
		for i, ref := range t.CACertificateRefs {
			if ref.Kind != "Secret" && ref.Kind != "ConfigMap" {
				return fmt.Errorf("caCertificateRefs[%d].kind must be 'Secret' or 'ConfigMap', got '%s'", i, ref.Kind)
			}
			if ref.Name == "" {
				return fmt.Errorf("caCertificateRefs[%d].name is required", i)
			}
		}
	}

	// Mode-specific validation
	if t.Mode == BackendTLSModeSimple {
		if t.ClientCertificateRef != nil {
			return fmt.Errorf("clientCertificateRef is not allowed in simple mode")
		}
	} else if t.Mode == BackendTLSModeMTLS {
		if t.ClientCertificateRef == nil {
			return fmt.Errorf("clientCertificateRef is required for mtls mode")
		}
		if t.ClientCertificateRef.Name == "" {
			return fmt.Errorf("clientCertificateRef.name is required when clientCertificateRef is set")
		}
	}

	return nil
}

// MirrorBackend represents a mirror destination for request mirroring
// Mirrors duplicate traffic to additional services without affecting the primary response
type MirrorBackend struct {
	Type      BackendType `json:"type"`                // kubernetes only for now
	Service   string      `json:"service,omitempty"`   // Service name
	Namespace string      `json:"namespace,omitempty"` // Namespace
	Port      int         `json:"port"`                // Port number
}

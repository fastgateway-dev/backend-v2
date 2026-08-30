package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SecurityPolicy represents security configuration for routes (Envoy Gateway specific)
// This is a separate resource that targets HTTPRoutes via Envoy Gateway's SecurityPolicy CRD
type SecurityPolicy struct {
	ID        uuid.UUID            `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	RouteID   uuid.UUID            `gorm:"type:uuid;not null;uniqueIndex" json:"routeId"`
	ProjectID uuid.UUID            `gorm:"type:uuid;not null;index" json:"projectId"`
	Config    SecurityPolicyConfig `gorm:"type:jsonb" json:"config"`
	CreatedAt time.Time            `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt time.Time            `gorm:"not null;default:now()" json:"updatedAt"`

	// Relationships
	Route   *Route   `gorm:"foreignKey:RouteID" json:"route,omitempty"`
	Project *Project `gorm:"foreignKey:ProjectID" json:"project,omitempty"`
}

// TableName returns the table name for SecurityPolicy
func (SecurityPolicy) TableName() string {
	return "security_policies"
}

// SecurityPolicyConfig holds all security feature configurations
type SecurityPolicyConfig struct {
	CORS          *CORSConfig          `json:"cors,omitempty"`
	Authorization *AuthorizationConfig `json:"authorization,omitempty"`
	APIKeyAuth    *APIKeyAuthConfig    `json:"apiKeyAuth,omitempty"`
	BasicAuth     *BasicAuthConfig     `json:"basicAuth,omitempty"`
	JWT           *JWTConfig           `json:"jwt,omitempty"`
	OIDC          *OIDCConfig          `json:"oidc,omitempty"`
	ExtAuth       *ExtAuthConfig       `json:"extAuth,omitempty"`
}

// HasAnyConfig returns true if any security feature is configured
func (c *SecurityPolicyConfig) HasAnyConfig() bool {
	return c.CORS != nil || c.Authorization != nil || c.APIKeyAuth != nil || c.BasicAuth != nil || c.JWT != nil || c.OIDC != nil || c.ExtAuth != nil
}

// Value implements the driver.Valuer interface for SecurityPolicyConfig
func (c SecurityPolicyConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for SecurityPolicyConfig
func (c *SecurityPolicyConfig) Scan(value interface{}) error {
	if value == nil {
		*c = SecurityPolicyConfig{}
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(bytes, c)
}

// CORSConfig represents CORS configuration for SecurityPolicy
type CORSConfig struct {
	AllowOrigins     []string `json:"allowOrigins,omitempty"`     // Origins allowed to make requests
	AllowMethods     []string `json:"allowMethods,omitempty"`     // HTTP methods allowed (GET, POST, PUT, DELETE, etc.)
	AllowHeaders     []string `json:"allowHeaders,omitempty"`     // Headers allowed in requests
	ExposeHeaders    []string `json:"exposeHeaders,omitempty"`    // Headers exposed to the browser
	MaxAge           *int     `json:"maxAge,omitempty"`           // Max age in seconds for preflight cache
	AllowCredentials *bool    `json:"allowCredentials,omitempty"` // Whether to allow credentials
}

// APIKeyAuthConfig represents API Key authentication configuration
type APIKeyAuthConfig struct {
	// CredentialRefs references K8s secrets containing API keys
	CredentialRefs []SecretRef `json:"credentialRefs"`
	// ExtractFrom specifies headers to extract the API key from
	ExtractFrom []APIKeyExtractFrom `json:"extractFrom"`
}

// APIKeyExtractFrom specifies where to extract API keys from
type APIKeyExtractFrom struct {
	Headers []string `json:"headers,omitempty"`
}

// BasicAuthConfig represents Basic Authentication configuration (future)
type BasicAuthConfig struct {
	// SecretRef references a Kubernetes secret containing htpasswd data
	SecretRef *SecretRef `json:"secretRef,omitempty"`
}

// JWTConfig represents JWT authentication configuration (future)
type JWTConfig struct {
	Providers []JWTProvider `json:"providers,omitempty"`
}

// JWTProvider represents a JWT provider configuration
type JWTProvider struct {
	Name           string               `json:"name"`
	Issuer         string               `json:"issuer"`
	Audiences      []string             `json:"audiences,omitempty"`
	RemoteJWKS     *RemoteJWKS          `json:"remoteJWKS,omitempty"`
	ClaimToHeaders []SPJWTClaimToHeader `json:"claimToHeaders,omitempty"`
}

// SPJWTClaimToHeader maps a JWT claim to an HTTP header (for SecurityPolicy model)
// Named with SP prefix to avoid conflict with client.JWTClaimToHeader
type SPJWTClaimToHeader struct {
	Claim  string `json:"claim"`  // JWT claim name
	Header string `json:"header"` // HTTP header to add
}

// RemoteJWKS represents remote JWKS configuration
type RemoteJWKS struct {
	URI string `json:"uri"`
}

// OIDCConfig represents OIDC authentication configuration
type OIDCConfig struct {
	Provider     *OIDCProvider `json:"provider,omitempty"`
	ClientID     string        `json:"clientId,omitempty"`
	ClientSecret *SecretRef    `json:"clientSecret,omitempty"`
	RedirectURL  string        `json:"redirectURL,omitempty"`
	LogoutPath   string        `json:"logoutPath,omitempty"`
	Scopes       []string      `json:"scopes,omitempty"`
	CookieDomain string        `json:"cookieDomain,omitempty"`
}

// OIDCProvider represents an OIDC provider configuration
type OIDCProvider struct {
	Issuer           string `json:"issuer"`
	AuthorizationURL string `json:"authorizationURL,omitempty"`
	TokenURL         string `json:"tokenURL,omitempty"`
}

// ExtAuthConfig represents external authorization configuration
type ExtAuthConfig struct {
	Type                       string              `json:"type"` // "http" or "grpc"
	HTTP                       *ExtAuthHTTPConfig  `json:"http,omitempty"`
	GRPC                       *ExtAuthGRPCConfig  `json:"grpc,omitempty"`
	FailOpen                   *bool               `json:"failOpen,omitempty"`
	HeadersToExtAuth           []string            `json:"headersToExtAuth,omitempty"`
	HeadersToDownstreamOnDeny  []string            `json:"headersToDownstreamOnDeny,omitempty"`
	HeadersToDownstreamOnAllow []string            `json:"headersToDownstreamOnAllow,omitempty"`
	HeadersToUpstreamOnAllow   []string            `json:"headersToUpstreamOnAllow,omitempty"`
	WithRequestBody            *ExtAuthRequestBody `json:"withRequestBody,omitempty"`
}

// Value implements the driver.Valuer interface for ExtAuthConfig
func (c ExtAuthConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for ExtAuthConfig
func (c *ExtAuthConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(bytes, c)
}

// Validate validates the ExtAuthConfig
func (c *ExtAuthConfig) Validate() error {
	if c.Type != "http" && c.Type != "grpc" {
		return fmt.Errorf("type must be 'http' or 'grpc', got %q", c.Type)
	}
	if c.Type == "http" {
		if c.HTTP == nil {
			return errors.New("http config is required when type is 'http'")
		}
		if err := c.HTTP.Validate(); err != nil {
			return fmt.Errorf("http: %w", err)
		}
	}
	if c.Type == "grpc" {
		if c.GRPC == nil {
			return errors.New("grpc config is required when type is 'grpc'")
		}
		if err := c.GRPC.Validate(); err != nil {
			return fmt.Errorf("grpc: %w", err)
		}
	}
	if c.WithRequestBody != nil {
		if c.WithRequestBody.MaxBytes > 10*1024*1024 { // 10MB limit
			return errors.New("withRequestBody.maxBytes cannot exceed 10MB")
		}
	}
	return nil
}

// ExtAuthHTTPConfig represents HTTP external auth configuration
type ExtAuthHTTPConfig struct {
	BackendRef       ExtAuthBackendRef `json:"backendRef"`
	Path             string            `json:"path"`
	HeadersToBackend []string          `json:"headersToBackend,omitempty"`
}

// Validate validates the ExtAuthHTTPConfig
func (c *ExtAuthHTTPConfig) Validate() error {
	if err := c.BackendRef.Validate(); err != nil {
		return fmt.Errorf("backendRef: %w", err)
	}
	if c.Path == "" {
		return errors.New("path is required for HTTP ext-auth")
	}
	if !strings.HasPrefix(c.Path, "/") {
		return errors.New("path must start with '/'")
	}
	return nil
}

// ExtAuthGRPCConfig represents gRPC external auth configuration
type ExtAuthGRPCConfig struct {
	BackendRef ExtAuthBackendRef `json:"backendRef"`
}

// Validate validates the ExtAuthGRPCConfig
func (c *ExtAuthGRPCConfig) Validate() error {
	if err := c.BackendRef.Validate(); err != nil {
		return fmt.Errorf("backendRef: %w", err)
	}
	return nil
}

// ExtAuthBackendRef references a backend service for ext-auth
type ExtAuthBackendRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Port      int    `json:"port"`
}

// Validate validates the ExtAuthBackendRef
func (r *ExtAuthBackendRef) Validate() error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", r.Port)
	}
	return nil
}

// ExtAuthRequestBody represents request body configuration for ext-auth
type ExtAuthRequestBody struct {
	MaxBytes uint32 `json:"maxBytes"`
}

// SecretRef references a Kubernetes secret
type SecretRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// BackendRef references a backend service
type BackendRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Port      int    `json:"port,omitempty"`
}

// AuthorizationConfig represents authorization configuration for SecurityPolicy
// This is computed at deploy time from active client attachments with IP allowlisting
type AuthorizationConfig struct {
	DefaultAction string              `json:"defaultAction"` // "Deny" when IP allowlisting is enabled
	Rules         []AuthorizationRule `json:"rules"`
}

// AuthorizationRule represents a single authorization rule
type AuthorizationRule struct {
	Action    string                  `json:"action"` // "Allow"
	Principal AuthorizationPrincipal  `json:"principal"`
	Operation *AuthorizationOperation `json:"operation,omitempty"`
}

// AuthorizationPrincipal represents the principal in an authorization rule
type AuthorizationPrincipal struct {
	ClientCIDRs []string                   `json:"clientCIDRs,omitempty"`
	JWT         *JWTPrincipal              `json:"jwt,omitempty"`
	Headers     []AuthorizationHeaderMatch `json:"headers,omitempty"`
}

// AuthorizationHeaderMatch represents a header name/values match for authorization
type AuthorizationHeaderMatch struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// AuthorizationOperation represents operation-level authorization (HTTP methods)
type AuthorizationOperation struct {
	Methods []string `json:"methods,omitempty"`
}

// JWTPrincipal represents JWT claim-based authorization principal
type JWTPrincipal struct {
	Provider string         `json:"provider"` // Must match a provider name in jwt.providers
	Claims   []JWTClaimRule `json:"claims,omitempty"`
}

// JWTClaimRule represents a single JWT claim requirement for authorization
type JWTClaimRule struct {
	Name      string   `json:"name"`                // Claim name (e.g., "scope", "role")
	Values    []string `json:"values"`              // Required values
	ValueType string   `json:"valueType,omitempty"` // "Exact" (default) or "StringContains"
}

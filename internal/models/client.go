package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// JWTAudiences represents a list of JWT audience values
type JWTAudiences []string

// Value implements the driver.Valuer interface for JWTAudiences
func (a JWTAudiences) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	return json.Marshal(a)
}

// Scan implements the sql.Scanner interface for JWTAudiences
func (a *JWTAudiences) Scan(value interface{}) error {
	if value == nil {
		*a = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan JWTAudiences: unsupported type")
	}

	return json.Unmarshal(bytes, a)
}

// JWTRequiredClaim represents a claim requirement for JWT authorization
type JWTRequiredClaim struct {
	Name      string   `json:"name"`                // Claim name (e.g., "scope", "role")
	Values    []string `json:"values"`              // Required values
	ValueType string   `json:"valueType,omitempty"` // "Exact" (default) or "StringContains"
}

// JWTRequiredClaims represents a list of JWT required claims
type JWTRequiredClaims []JWTRequiredClaim

// Value implements the driver.Valuer interface for JWTRequiredClaims
func (c JWTRequiredClaims) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for JWTRequiredClaims
func (c *JWTRequiredClaims) Scan(value interface{}) error {
	if value == nil {
		*c = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan JWTRequiredClaims: unsupported type")
	}

	return json.Unmarshal(bytes, c)
}

// JWTClaimToHeader maps a JWT claim to an HTTP header
type JWTClaimToHeader struct {
	Claim  string `json:"claim"`  // JWT claim name
	Header string `json:"header"` // HTTP header to add
}

// JWTClaimToHeaders represents a list of JWT claim to header mappings
type JWTClaimToHeaders []JWTClaimToHeader

// Value implements the driver.Valuer interface for JWTClaimToHeaders
func (c JWTClaimToHeaders) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for JWTClaimToHeaders
func (c *JWTClaimToHeaders) Scan(value interface{}) error {
	if value == nil {
		*c = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan JWTClaimToHeaders: unsupported type")
	}

	return json.Unmarshal(bytes, c)
}

// MTLSSANList represents a list of SAN entries for mTLS client identification
type MTLSSANList []MTLSSANEntry

// Value implements the driver.Valuer interface for MTLSSANList
func (s MTLSSANList) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	return json.Marshal(s)
}

// Scan implements the sql.Scanner interface for MTLSSANList
func (s *MTLSSANList) Scan(value interface{}) error {
	if value == nil {
		*s = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan MTLSSANList: unsupported type")
	}

	return json.Unmarshal(bytes, s)
}

// StringList represents a list of strings for JSONB storage
type StringList []string

// Value implements the driver.Valuer interface for StringList
func (s StringList) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	return json.Marshal(s)
}

// Scan implements the sql.Scanner interface for StringList
func (s *StringList) Scan(value interface{}) error {
	if value == nil {
		*s = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan StringList: unsupported type")
	}

	return json.Unmarshal(bytes, s)
}

// Client represents an API client (external party or internal system)
// Clients are global entities (like teams) and belong to a team
type Client struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	TeamID       uuid.UUID `gorm:"type:uuid;not null" json:"teamId"`
	Name         string    `gorm:"uniqueIndex;not null" json:"name"`
	Description  string    `json:"description"`
	ContactName  string    `json:"contactName"`
	ContactEmail string    `json:"contactEmail"`
	CreatedBy    uuid.UUID `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt    time.Time `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt    time.Time `gorm:"not null;default:now()" json:"updatedAt"`

	// API Key Authentication
	APIKeyEnabled      bool       `gorm:"column:api_key_enabled;not null;default:false" json:"apiKeyEnabled"`
	APIKeyHash         string     `gorm:"column:api_key_hash" json:"-"`                                                 // Bcrypt hash for audit
	APIKeyEncrypted    string     `gorm:"column:api_key_encrypted" json:"-"`                                            // Encrypted key for deploy
	APIKeyPrefix       string     `gorm:"column:api_key_prefix" json:"apiKeyPrefix,omitempty"`                          // e.g., "fg_live_xxxx"
	APIKeyHeaderName   string     `gorm:"column:api_key_header_name;default:'x-api-key'" json:"apiKeyHeaderName"`       // Header for API key value
	ClientIDHeaderName string     `gorm:"column:client_id_header_name;default:'x-client-id'" json:"clientIdHeaderName"` // Header for client ID (routing)
	APIKeyCreatedAt    *time.Time `gorm:"column:api_key_created_at" json:"apiKeyCreatedAt,omitempty"`
	APIKeyCreatedBy    *uuid.UUID `gorm:"column:api_key_created_by;type:uuid" json:"apiKeyCreatedBy,omitempty"`

	// JWT Authentication
	JWTEnabled        bool              `gorm:"column:jwt_enabled;not null;default:false" json:"jwtEnabled"`
	JWTIssuer         string            `gorm:"column:jwt_issuer" json:"jwtIssuer,omitempty"`
	JWTJWKSURL        string            `gorm:"column:jwt_jwks_url" json:"jwtJwksUrl,omitempty"`
	JWTAudiences      JWTAudiences      `gorm:"column:jwt_audiences;type:jsonb" json:"jwtAudiences,omitempty"`
	JWTRequiredClaims JWTRequiredClaims `gorm:"column:jwt_required_claims;type:jsonb" json:"jwtRequiredClaims,omitempty"`
	JWTClaimToHeaders JWTClaimToHeaders `gorm:"column:jwt_claim_to_headers;type:jsonb" json:"jwtClaimToHeaders,omitempty"`
	JWTCreatedAt      *time.Time        `gorm:"column:jwt_created_at" json:"jwtCreatedAt,omitempty"`
	JWTCreatedBy      *uuid.UUID        `gorm:"column:jwt_created_by;type:uuid" json:"jwtCreatedBy,omitempty"`

	// mTLS Authentication
	MTLSEnabled     bool        `gorm:"column:mtls_enabled;not null;default:false" json:"mtlsEnabled"`
	MTLSCAName      string      `gorm:"column:mtls_ca_name" json:"mtlsCaName,omitempty"`
	MTLSCASecret    string      `gorm:"column:mtls_ca_secret" json:"mtlsCaSecret,omitempty"`
	MTLSCASecretKey string      `gorm:"column:mtls_ca_secret_key;default:'ca.crt'" json:"mtlsCaSecretKey,omitempty"`
	MTLSCAPem       string      `gorm:"column:mtls_ca_pem;type:text" json:"-"`
	MTLSSANs        MTLSSANList `gorm:"column:mtls_sans;type:jsonb" json:"mtlsSans,omitempty"`
	MTLSHashes      StringList  `gorm:"column:mtls_hashes;type:jsonb" json:"mtlsHashes,omitempty"`
	MTLSCreatedAt   *time.Time  `gorm:"column:mtls_created_at" json:"mtlsCreatedAt,omitempty"`
	MTLSCreatedBy   *uuid.UUID  `gorm:"column:mtls_created_by;type:uuid" json:"mtlsCreatedBy,omitempty"`

	// Relationships
	Team          *Team `gorm:"foreignKey:TeamID" json:"team,omitempty"`
	Creator       *User `gorm:"foreignKey:CreatedBy" json:"creator,omitempty"`
	APIKeyCreator *User `gorm:"foreignKey:APIKeyCreatedBy" json:"apiKeyCreator,omitempty"`
	JWTCreator    *User `gorm:"foreignKey:JWTCreatedBy" json:"jwtCreator,omitempty"`
	MTLSCreator   *User `gorm:"foreignKey:MTLSCreatedBy" json:"mtlsCreator,omitempty"`

	// Header & Method Authorization
	AllowedMethods StringList `gorm:"column:allowed_methods;type:jsonb" json:"allowedMethods,omitempty"`

	// Computed fields (not in DB)
	IPAddressCount  int `gorm:"-" json:"ipAddressCount,omitempty"`
	HeaderCount     int `gorm:"-" json:"headerCount,omitempty"`
	AttachmentCount int `gorm:"-" json:"attachmentCount,omitempty"`
}

// TableName returns the table name for Client
func (Client) TableName() string {
	return "clients"
}

// ValidateMTLSConfig validates mTLS configuration for a client
func (c *Client) ValidateMTLSConfig() error {
	if !c.MTLSEnabled {
		return nil
	}
	// CA certificate is required when mTLS is enabled
	if c.MTLSCASecret == "" {
		return errors.New("CA certificate is required when mTLS is enabled")
	}
	// At least one SAN or hash is required for client identification
	if len(c.MTLSSANs) == 0 && len(c.MTLSHashes) == 0 {
		return errors.New("at least one SAN or certificate hash is required for client identification")
	}
	// Validate SAN types
	for _, san := range c.MTLSSANs {
		if san.Type != "DNS" && san.Type != "URI" {
			return errors.New("SAN type must be 'DNS' or 'URI'")
		}
		if san.Value == "" {
			return errors.New("SAN value cannot be empty")
		}
	}
	// Validate hash format (should be 64 hex chars for SHA-256)
	for _, hash := range c.MTLSHashes {
		if len(hash) != 64 {
			return errors.New("certificate hash must be 64 hex characters (SHA-256)")
		}
	}
	return nil
}

// ClientIPAddress represents an IP address belonging to a client
type ClientIPAddress struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ClientID    uuid.UUID `gorm:"type:uuid;not null" json:"clientId"`
	CIDR        string    `gorm:"column:cidr;not null" json:"cidr"`
	Description string    `json:"description"`
	CreatedBy   uuid.UUID `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt   time.Time `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	Client  *Client `gorm:"foreignKey:ClientID" json:"-"`
	Creator *User   `gorm:"foreignKey:CreatedBy" json:"creator,omitempty"`
}

// TableName returns the table name for ClientIPAddress
func (ClientIPAddress) TableName() string {
	return "client_ip_addresses"
}

// ClientHeader represents a header entry belonging to a client (mirrors ClientIPAddress pattern)
type ClientHeader struct {
	ID          uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	ClientID    uuid.UUID  `gorm:"type:uuid;not null" json:"clientId"`
	Name        string     `gorm:"not null" json:"name"`
	Values      StringList `gorm:"type:jsonb;not null" json:"values"`
	Description string     `json:"description"`
	CreatedBy   uuid.UUID  `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt   time.Time  `gorm:"not null;default:now()" json:"createdAt"`

	// Relationships
	Client  *Client `gorm:"foreignKey:ClientID" json:"-"`
	Creator *User   `gorm:"foreignKey:CreatedBy" json:"creator,omitempty"`
}

// TableName returns the table name for ClientHeader
func (ClientHeader) TableName() string {
	return "client_headers"
}

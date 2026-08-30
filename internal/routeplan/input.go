package routeplan

import (
	"net"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// Built-in defaults for the coraza-proxy-wasm image, used by WAFConfig.ImageURL
// when a field is left unset. Overriding these is done once, by
// internal/config reading WAF_IMAGE/WAF_TAG and cmd/server/main.go passing the
// result into services.WAFConfig — never by reading the environment here.
const (
	defaultWAFImage = "ghcr.io/corazawaf/coraza-proxy-wasm"
	defaultWAFTag   = "0.6.0"
)

// WAFConfig is the coraza-proxy-wasm image the WAF EnvoyExtensionPolicy points
// at. It is injected rather than read from the environment at call time so that
// manifest generation is a pure function of its inputs.
type WAFConfig struct {
	Image string
	Tag   string
}

// ImageURL renders the image reference used in the EnvoyExtensionPolicy,
// substituting the built-in defaults for any unset field.
func (c WAFConfig) ImageURL() string {
	image := c.Image
	if image == "" {
		image = defaultWAFImage
	}
	tag := c.Tag
	if tag == "" {
		tag = defaultWAFTag
	}
	return image + ":" + tag
}

// NormalizeCIDR ensures a CIDR has a prefix. If it's a plain IP, adds /32 for IPv4 or /128 for IPv6.
func NormalizeCIDR(cidr string) string {
	if strings.Contains(cidr, "/") {
		return cidr // Already has a prefix
	}
	ip := net.ParseIP(cidr)
	if ip == nil {
		return cidr // Invalid IP, return as-is (validation will catch it)
	}
	if ip.To4() != nil {
		return cidr + "/32" // IPv4
	}
	return cidr + "/128" // IPv6
}

// GetRouteKind returns the K8s Kind for SecurityPolicy/BTP targetRef based on protocol
func GetRouteKind(protocol models.RouteProtocol) string {
	if protocol == models.RouteProtocolGRPC {
		return "GRPCRoute"
	}
	return "HTTPRoute"
}

// SecurityPolicyInput represents security policy configuration input
type SecurityPolicyInput struct {
	CORS          *models.CORSConfig    `json:"cors,omitempty"`
	Authorization *AuthorizationInput   `json:"authorization,omitempty"` // General mode: IP allowlisting
	APIKeyAuth    *APIKeyAuthInput      `json:"apiKeyAuth,omitempty"`    // General mode: API key auth
	JWT           *JWTInput             `json:"jwt,omitempty"`           // General mode: JWT validation
	OIDC          *OIDCInput            `json:"oidc,omitempty"`          // General mode: OIDC/SSO login
	ExtAuth       *models.ExtAuthConfig `json:"extAuth,omitempty"`       // Both modes: external authorization
}

// AuthorizationInput represents authorization input for general mode
type AuthorizationInput struct {
	AllowedCIDRs []string                          `json:"allowedCIDRs"`
	Headers      []models.AuthorizationHeaderMatch `json:"headers,omitempty"`
	Methods      []string                          `json:"methods,omitempty"`
}

// APIKeyAuthInput represents API key authentication input for general mode
type APIKeyAuthInput struct {
	SecretName string `json:"secretName"`
	HeaderName string `json:"headerName"`
}

// JWTInput represents JWT validation input for general mode
type JWTInput struct {
	Issuer         string                      `json:"issuer"`
	JWKSURL        string                      `json:"jwksUrl"`
	Audiences      []string                    `json:"audiences,omitempty"`
	ClaimToHeaders []models.SPJWTClaimToHeader `json:"claimToHeaders,omitempty"`
}

// OIDCInput represents OIDC/SSO login input for general mode
type OIDCInput struct {
	Issuer           string   `json:"issuer"`
	ClientID         string   `json:"clientId"`
	ClientSecretName string   `json:"clientSecretName"`
	RedirectURL      string   `json:"redirectURL"`
	LogoutPath       string   `json:"logoutPath"`
	Scopes           []string `json:"scopes,omitempty"`
	CookieDomain     string   `json:"cookieDomain,omitempty"`
}

// BackendTrafficPolicyInput represents backend traffic policy configuration input
type BackendTrafficPolicyInput struct {
	Compression      []models.CompressionConfig    `json:"compression,omitempty"`
	Retry            *models.RetryConfig           `json:"retry,omitempty"`
	LoadBalancer     *models.LoadBalancerConfig    `json:"loadBalancer,omitempty"`
	CircuitBreaker   *models.CircuitBreakerConfig  `json:"circuitBreaker,omitempty"`
	HealthCheck      *models.HealthCheckConfig     `json:"healthCheck,omitempty"`
	FaultInjection   *models.FaultInjectionConfig  `json:"faultInjection,omitempty"`
	RateLimit        *models.RateLimitConfig       `json:"rateLimit,omitempty"`
	RequestBuffer    *models.RequestBufferConfig   `json:"requestBuffer,omitempty"`
	ResponseOverride []models.ResponseOverrideRule `json:"responseOverride,omitempty"`
	Timeout          *models.BTPTimeoutConfig      `json:"timeout,omitempty"`
}

// HasContent checks if the input has any features configured
func (i *BackendTrafficPolicyInput) HasContent() bool {
	return len(i.Compression) > 0 || i.Retry != nil || i.LoadBalancer != nil || i.CircuitBreaker != nil || i.HealthCheck != nil || i.FaultInjection != nil || i.RateLimit != nil || i.RequestBuffer != nil || len(i.ResponseOverride) > 0 || i.Timeout != nil
}

// EnvoyExtensionPolicyInput represents input for Envoy extension policy creation
type EnvoyExtensionPolicyInput struct {
	Lua     *models.LuaExtensionConfig     `json:"lua,omitempty"`
	Wasm    *models.WasmExtensionConfig    `json:"wasm,omitempty"`
	ExtProc *models.ExtProcExtensionConfig `json:"extProc,omitempty"`
}

// HasContent checks if the input has any extensions configured
func (i *EnvoyExtensionPolicyInput) HasContent() bool {
	return i.Lua != nil || i.Wasm != nil || i.ExtProc != nil
}

// WafPolicyInput represents WAF policy input for route operations
type WafPolicyInput struct {
	Mode             string   `json:"mode"`
	Rulesets         []string `json:"rulesets,omitempty"`
	AnomalyThreshold *int     `json:"anomalyThreshold,omitempty"`
	ParanoiaLevel    *int     `json:"paranoiaLevel,omitempty"`
	DisabledRuleIDs  []int    `json:"disabledRuleIDs,omitempty"`
	CustomDirectives []string `json:"customDirectives,omitempty"`
}

// convertPathTypeToGatewayAPI converts frontend path types to Gateway API path types
func convertPathTypeToGatewayAPI(pathType string) string {
	switch pathType {
	case "Prefix":
		return "PathPrefix"
	case "Exact":
		return "Exact"
	case "RegularExpression":
		return "RegularExpression"
	default:
		return "PathPrefix" // Default to PathPrefix
	}
}

// ClientAuthCategory represents the auth type for a client attachment
type ClientAuthCategory struct {
	ClientID           uuid.UUID
	ClientName         string
	EnableIP           bool
	EnableAPIKey       bool
	EnableJWT          bool
	EnableMTLS         bool
	APIKey             string   // Plaintext API key from K8s Secret (only for API key clients)
	APIKeyHeaderName   string   // Header to extract API key from (e.g., "x-api-key")
	ClientIDHeaderName string   // Header for client identification/routing (e.g., "x-client-id")
	IPCIDRs            []string // Client's IP CIDRs (only for IP clients)
	// JWT fields
	JWTIssuer         string                    // JWT issuer (iss claim)
	JWTJWKSURL        string                    // URL to fetch JWKS
	JWTAudiences      []string                  // Allowed audiences (aud claim)
	JWTRequiredClaims []models.JWTRequiredClaim // Required claims for authorization
	JWTClaimToHeaders []models.JWTClaimToHeader // Map claims to headers
	// Header/Method fields
	EnableHeaderAuth bool
	HeaderMatches    []models.AuthorizationHeaderMatch // Client's headers for authorization
	AllowedMethods   []string                          // Client-level allowed methods
	// mTLS fields
	MTLSSANs   []models.MTLSSANEntry // Client SANs for XFCC matching
	MTLSHashes []string              // Client certificate hashes for XFCC matching
	MTLSCAPem  string                // Client CA PEM for creating K8s secret at deploy time
	// Rate limit config from attachment
	RateLimitConfig *models.RateLimitConfig
	// External auth config from attachment
	ExtAuth            *models.ExtAuthConfig
	ExtAuthBackendName string // Name of Backend CRD for ext-auth (set during deployment)
}

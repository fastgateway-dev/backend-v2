package kubernetes

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// SecurityPolicyConfig represents Envoy Gateway SecurityPolicy configuration
type SecurityPolicyConfig struct {
	Name               string                     // Name of the SecurityPolicy (usually {routeName}-security)
	Namespace          string                     // Namespace where the SecurityPolicy will be created
	GatewayID          string                     // Domain/Gateway ID for labeling
	RouteID            string                     // Route ID for labeling
	TargetRef          SecurityPolicyTargetRef    // Reference to the target HTTPRoute
	CORS               *CORSPolicyConfig          // CORS configuration
	Authorization      *AuthorizationPolicyConfig // Authorization configuration (IP allowlisting, JWT claims)
	APIKeyAuth         *APIKeyAuthPolicyConfig    // API Key authentication configuration
	JWT                *JWTAuthPolicyConfig       // JWT authentication configuration
	OIDC               *OIDCPolicyConfig          // OIDC authentication configuration
	ExtAuth            *models.ExtAuthConfig      // External authorization configuration
	ExtAuthBackendName string                     // Name of the Backend CRD for ext-auth
}

// OIDCPolicyConfig holds OIDC configuration for SecurityPolicy CRD
type OIDCPolicyConfig struct {
	Issuer           string
	ClientID         string
	ClientSecretName string
	ClientSecretNS   string
	RedirectURL      string
	LogoutPath       string
	Scopes           []string
	CookieDomain     string
}

// JWTAuthPolicyConfig represents JWT authentication configuration for SecurityPolicy
type JWTAuthPolicyConfig struct {
	Providers []JWTProviderPolicyConfig // JWT providers
}

// JWTProviderPolicyConfig represents a JWT provider configuration
type JWTProviderPolicyConfig struct {
	Name           string                         // Provider name (used in authorization rules)
	Issuer         string                         // Token issuer (iss claim)
	JWKSURL        string                         // URL to fetch JWKS
	Audiences      []string                       // Allowed audiences (aud claim)
	ClaimToHeaders []JWTClaimToHeaderPolicyConfig // Map claims to headers
}

// JWTClaimToHeaderPolicyConfig maps a JWT claim to an HTTP header
type JWTClaimToHeaderPolicyConfig struct {
	Claim  string // JWT claim name
	Header string // HTTP header to add
}

// AuthorizationPolicyConfig represents authorization configuration for SecurityPolicy
type AuthorizationPolicyConfig struct {
	DefaultAction string                          // "Deny" when IP allowlisting is enabled
	Rules         []AuthorizationRulePolicyConfig // Authorization rules
}

// AuthorizationRulePolicyConfig represents a single authorization rule
type AuthorizationRulePolicyConfig struct {
	Action      string                    // "Allow"
	ClientCIDRs []string                  // List of CIDR ranges to allow
	JWT         *JWTPrincipalPolicyConfig // JWT claim-based authorization
	Headers     []HeaderMatchPolicyConfig // Header name/values match
	Methods     []string                  // Allowed HTTP methods
}

// HeaderMatchPolicyConfig represents a header match for K8s authorization
type HeaderMatchPolicyConfig struct {
	Name   string
	Values []string
}

// JWTPrincipalPolicyConfig represents JWT claim-based authorization principal
type JWTPrincipalPolicyConfig struct {
	Provider string                     // Must match a provider name in jwt.providers
	Claims   []JWTClaimRulePolicyConfig // Claim requirements
}

// JWTClaimRulePolicyConfig represents a single JWT claim requirement
type JWTClaimRulePolicyConfig struct {
	Name      string   // Claim name (e.g., "scope", "role")
	Values    []string // Required values
	ValueType string   // CRD-supported: "" (default/String), "StringArray"
}

// SecurityPolicyTargetRef represents the target reference for SecurityPolicy
type SecurityPolicyTargetRef struct {
	Group string
	Kind  string
	Name  string
}

// CORSPolicyConfig represents CORS configuration for SecurityPolicy
type CORSPolicyConfig struct {
	AllowOrigins     []string // Origins allowed (supports wildcards like "http://*.foo.com")
	AllowMethods     []string // HTTP methods allowed
	AllowHeaders     []string // Headers allowed in requests
	ExposeHeaders    []string // Headers exposed to the browser
	MaxAge           *int     // Max age in seconds for preflight cache
	AllowCredentials *bool    // Whether to allow credentials
}

// APIKeyAuthPolicyConfig represents API Key authentication configuration for SecurityPolicy
type APIKeyAuthPolicyConfig struct {
	CredentialRefs []SecretRefConfig         // References to K8s secrets containing API keys
	ExtractFrom    []APIKeyExtractFromConfig // Specifies where to extract API keys from
}

// SecretRefConfig represents a reference to a Kubernetes secret
type SecretRefConfig struct {
	Name      string // Secret name
	Namespace string // Secret namespace
}

// APIKeyExtractFromConfig specifies where to extract API keys from
type APIKeyExtractFromConfig struct {
	Headers []string // Header names to extract API key from
}

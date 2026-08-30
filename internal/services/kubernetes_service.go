package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/k8s/naming"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// KubernetesService handles Kubernetes operations
type KubernetesService struct {
	projectService *ProjectService
	testClient     dynamic.Interface // when set, used by getClientFor instead of building per-project clients (tests only)
}

// NewKubernetesService creates a new Kubernetes service
func NewKubernetesService(projectService *ProjectService) *KubernetesService {
	return &KubernetesService{
		projectService: projectService,
	}
}

// GatewayConfig represents Gateway configuration
type GatewayConfig struct {
	Name               string
	Namespace          string
	GatewayClassName   string
	Hostname           string
	TLSMode            string // tls_only, no_tls, both
	HTTPPort           int
	HTTPSPort          int
	TLSSecretName      string
	TLSSecretNamespace string
	TLSPolicy          string
	Annotations        map[string]string
}

// HTTPRouteConfig represents HTTPRoute configuration
type HTTPRouteConfig struct {
	Name                   string
	Namespace              string
	GatewayName            string
	GatewayID              string // Domain UUID for labeling
	RouteID                string // Route UUID for labeling
	Hostname               string
	Rules                  []HTTPRouteRule
	Mirrors                []MirrorRef // Mirror destinations for request mirroring
	RequestHeaderModifier  *HTTPHeaderModifier
	ResponseHeaderModifier *HTTPHeaderModifier
	URLRewrite             *HTTPURLRewrite
	Redirect               *HTTPRedirectConfig // When set, this is a redirect route (no backends)
	HTTPRouteFilterName    string              // When set, this is a direct response route (uses ExtensionRef filter)
	// Note: CORS is now handled via SecurityPolicy CRD (see SecurityPolicyConfig)
}

// GRPCRouteConfig holds configuration for building a GRPCRoute K8s object
type GRPCRouteConfig struct {
	Name                   string
	Namespace              string
	GatewayName            string
	GatewayID              string
	RouteID                string
	Hostname               string
	Rules                  []GRPCRouteRule
	Mirrors                []MirrorRef
	RequestHeaderModifier  *HTTPHeaderModifier
	ResponseHeaderModifier *HTTPHeaderModifier
}

// GRPCRouteRule defines a single gRPC route matching rule
type GRPCRouteRule struct {
	GRPCService *GRPCMethodMatchConfig // optional
	GRPCMethod  *GRPCMethodMatchConfig // optional
	Headers     []HeaderMatch
	BackendRefs []BackendRef
}

// GRPCMethodMatchConfig holds the type and value for a gRPC service/method match
type GRPCMethodMatchConfig struct {
	Type  string // "Exact" or "RegularExpression"
	Value string
}

// MirrorRef represents a mirror backend reference for HTTPRoute
type MirrorRef struct {
	Name      string
	Namespace string
	Port      int
}

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

// BackendTrafficPolicyConfig represents Envoy Gateway BackendTrafficPolicy configuration
type BackendTrafficPolicyConfig struct {
	Name             string                         // Name of the BackendTrafficPolicy (usually {routeName}-btp)
	Namespace        string                         // Namespace where the BackendTrafficPolicy will be created
	GatewayID        string                         // Domain/Gateway ID for labeling
	RouteID          string                         // Route ID for labeling
	DomainID         string                         // Domain ID for labeling (domain-level policies)
	TargetRef        BackendTrafficPolicyTargetRef  // Reference to the target HTTPRoute
	Compression      []CompressionPolicyConfig      // Compression configuration
	Retry            *RetryPolicyConfig             // Retry configuration
	LoadBalancer     *LoadBalancerPolicyConfig      // Load balancer configuration
	CircuitBreaker   *CircuitBreakerPolicyConfig    // Circuit breaker configuration
	HealthCheck      *HealthCheckPolicyConfig       // Health check configuration
	FaultInjection   *FaultInjectionPolicyConfig    // Fault injection configuration
	RateLimit        *RateLimitPolicyConfig         // Rate limit configuration
	RequestBuffer    *RequestBufferPolicyConfig     // Request buffering configuration
	ResponseOverride []ResponseOverridePolicyConfig // Response override configuration
	Timeout          *BTPTimeoutPolicyConfig        // Timeout configuration
}

// LoadBalancerPolicyConfig represents load balancer configuration for BackendTrafficPolicy
type LoadBalancerPolicyConfig struct {
	Type           string
	ConsistentHash *ConsistentHashPolicyConfig
}

// ConsistentHashPolicyConfig represents consistent hash configuration
type ConsistentHashPolicyConfig struct {
	Type   string
	Header *ConsistentHashHeaderPolicyConfig
	Cookie *ConsistentHashCookiePolicyConfig
}

// ConsistentHashHeaderPolicyConfig represents header-based consistent hashing
type ConsistentHashHeaderPolicyConfig struct {
	Name string
}

// ConsistentHashCookiePolicyConfig represents cookie-based consistent hashing
type ConsistentHashCookiePolicyConfig struct {
	Name       string
	TTL        *string
	Attributes map[string]string
}

// CircuitBreakerPolicyConfig represents circuit breaker configuration for BackendTrafficPolicy
type CircuitBreakerPolicyConfig struct {
	MaxConnections           *int64
	MaxPendingRequests       *int64
	MaxParallelRequests      *int64
	MaxParallelRetries       *int64
	MaxRequestsPerConnection *int64
}

// HealthCheckPolicyConfig represents health check configuration for BackendTrafficPolicy
type HealthCheckPolicyConfig struct {
	Active         *ActiveHealthCheckPolicyConfig
	Passive        *PassiveHealthCheckPolicyConfig
	PanicThreshold *uint32
}

// ActiveHealthCheckPolicyConfig represents active health check configuration
type ActiveHealthCheckPolicyConfig struct {
	Timeout            *string
	Interval           *string
	UnhealthyThreshold *uint32
	HealthyThreshold   *uint32
	Type               string
	HTTP               *HTTPActiveHealthCheckPolicyConfig
	TCP                *TCPActiveHealthCheckPolicyConfig
	GRPC               *GRPCActiveHealthCheckPolicyConfig
}

// HTTPActiveHealthCheckPolicyConfig represents HTTP active health checker
type HTTPActiveHealthCheckPolicyConfig struct {
	Path             string
	Method           *string
	ExpectedStatuses []int
}

// TCPActiveHealthCheckPolicyConfig represents TCP active health checker
type TCPActiveHealthCheckPolicyConfig struct {
	SendText    *string
	ReceiveText *string
}

// GRPCActiveHealthCheckPolicyConfig represents gRPC active health checker
type GRPCActiveHealthCheckPolicyConfig struct {
	Service *string
}

// PassiveHealthCheckPolicyConfig represents passive health check (outlier detection)
type PassiveHealthCheckPolicyConfig struct {
	ConsecutiveGatewayErrors       *uint32
	Consecutive5xxErrors           *uint32
	ConsecutiveLocalOriginFailures *uint32
	Interval                       *string
	BaseEjectionTime               *string
	MaxEjectionPercent             *int32
	SplitExternalLocalOriginErrors *bool
}

// FaultInjectionPolicyConfig represents fault injection configuration for BackendTrafficPolicy
type FaultInjectionPolicyConfig struct {
	Delay *FaultInjectionDelayPolicyConfig
	Abort *FaultInjectionAbortPolicyConfig
}

// FaultInjectionDelayPolicyConfig represents delay fault injection
type FaultInjectionDelayPolicyConfig struct {
	FixedDelay string
	Percentage *float32
}

// FaultInjectionAbortPolicyConfig represents abort fault injection
type FaultInjectionAbortPolicyConfig struct {
	HTTPStatus *int
	GRPCStatus *int
	Percentage *float32
}

// RateLimitPolicyConfig represents rate limit configuration for BackendTrafficPolicy
type RateLimitPolicyConfig struct {
	Global *GlobalRateLimitPolicyConfig
}

// GlobalRateLimitPolicyConfig represents global rate limit policy
type GlobalRateLimitPolicyConfig struct {
	Rules []RateLimitRulePolicyConfig
}

// RateLimitRulePolicyConfig represents a single rate limit rule
type RateLimitRulePolicyConfig struct {
	Limit           RateLimitValuePolicyConfig
	ClientSelectors []RateLimitSelectorPolicyConfig
}

// RateLimitValuePolicyConfig represents rate limit value
type RateLimitValuePolicyConfig struct {
	Requests int
	Unit     string
}

// RateLimitSelectorPolicyConfig represents client selector for rate limiting
type RateLimitSelectorPolicyConfig struct {
	Headers    []RateLimitHeaderMatchPolicyConfig
	SourceCIDR *RateLimitSourceCIDRPolicyConfig
	Path       *RateLimitPathMatchPolicyConfig
	Methods    []string
}

// RateLimitHeaderMatchPolicyConfig represents header match for rate limiting
type RateLimitHeaderMatchPolicyConfig struct {
	Name   string
	Value  string
	Type   string
	Invert bool
}

// RateLimitSourceCIDRPolicyConfig represents source CIDR match
type RateLimitSourceCIDRPolicyConfig struct {
	Value string
	Type  string
}

// RateLimitPathMatchPolicyConfig represents path match for rate limiting
type RateLimitPathMatchPolicyConfig struct {
	Value string
	Type  string
}

// RequestBufferPolicyConfig represents request buffering configuration
type RequestBufferPolicyConfig struct {
	Limit string
}

// BTPTimeoutPolicyConfig represents timeout configuration for BackendTrafficPolicy
type BTPTimeoutPolicyConfig struct {
	TCP  *BTPTCPTimeoutPolicyConfig
	HTTP *BTPHTTPTimeoutPolicyConfig
}

// BTPTCPTimeoutPolicyConfig represents TCP-level timeout settings for BackendTrafficPolicy
type BTPTCPTimeoutPolicyConfig struct {
	ConnectTimeout string
}

// BTPHTTPTimeoutPolicyConfig represents HTTP-level timeout settings for BackendTrafficPolicy
type BTPHTTPTimeoutPolicyConfig struct {
	RequestTimeout        string
	ConnectionIdleTimeout string
	MaxConnectionDuration string
	MaxStreamDuration     string
}

// ResponseOverridePolicyConfig represents response override configuration
type ResponseOverridePolicyConfig struct {
	Match    ResponseOverrideMatchPolicyConfig
	Response ResponseOverrideResponsePolicyConfig
}

// ResponseOverrideMatchPolicyConfig represents match conditions
type ResponseOverrideMatchPolicyConfig struct {
	StatusCodes []StatusCodeMatchPolicyConfig
}

// StatusCodeMatchPolicyConfig represents status code match
type StatusCodeMatchPolicyConfig struct {
	Type  string
	Value *int
	Range *StatusCodeRangePolicyConfig
}

// StatusCodeRangePolicyConfig represents status code range
type StatusCodeRangePolicyConfig struct {
	Start int
	End   int
}

// ResponseOverrideResponsePolicyConfig represents response override response
type ResponseOverrideResponsePolicyConfig struct {
	ContentType string
	Body        ResponseOverrideBodyPolicyConfig
}

// ResponseOverrideBodyPolicyConfig represents response body configuration
type ResponseOverrideBodyPolicyConfig struct {
	Type     string
	Inline   string
	ValueRef *ValueRefPolicyConfig
}

// ValueRefPolicyConfig represents a ConfigMap or Secret reference
type ValueRefPolicyConfig struct {
	Group     string
	Kind      string
	Name      string
	Namespace string
}

// BackendTrafficPolicyTargetRef represents the target reference for BackendTrafficPolicy
type BackendTrafficPolicyTargetRef struct {
	Group string
	Kind  string
	Name  string
}

// EnvoyExtensionPolicyK8sConfig represents Envoy Gateway EnvoyExtensionPolicy configuration
type EnvoyExtensionPolicyK8sConfig struct {
	Name      string
	Namespace string
	GatewayID string
	RouteID   string
	DomainID  string
	TargetRef EnvoyExtensionPolicyTargetRef
	Lua       []LuaExtensionPolicyConfig
	Wasm      []WasmExtensionPolicyConfig
	ExtProc   []ExtProcPolicyConfig
}

// EnvoyExtensionPolicyTargetRef represents the target reference for EnvoyExtensionPolicy
type EnvoyExtensionPolicyTargetRef struct {
	Group string
	Kind  string
	Name  string
}

// LuaExtensionPolicyConfig represents Lua extension configuration
type LuaExtensionPolicyConfig struct {
	Type     string
	Inline   string
	ValueRef *ValueRefPolicyConfig
}

// WasmExtensionPolicyConfig represents Wasm extension configuration
type WasmExtensionPolicyConfig struct {
	Name   string
	RootID string
	Code   WasmCodeSourcePolicyConfig
	Config *string
}

// WasmCodeSourcePolicyConfig represents Wasm code source
type WasmCodeSourcePolicyConfig struct {
	Type  string
	HTTP  *WasmHTTPSourcePolicyConfig
	Image *WasmImageSourcePolicyConfig
}

// WasmHTTPSourcePolicyConfig represents HTTP source for Wasm
type WasmHTTPSourcePolicyConfig struct {
	URL    string
	SHA256 string
}

// WasmImageSourcePolicyConfig represents OCI Image source for Wasm
type WasmImageSourcePolicyConfig struct {
	URL        string
	SHA256     string
	PullSecret *ValueRefPolicyConfig
}

// ExtProcPolicyConfig holds ext-proc config for K8s manifest generation
type ExtProcPolicyConfig struct {
	BackendRef     ExtProcBackendRefPolicyConfig
	ProcessingMode *ExtProcProcessingModeConfig
	FailOpen       bool
}

// ExtProcBackendRefPolicyConfig holds the user-facing service reference
type ExtProcBackendRefPolicyConfig struct {
	Name      string
	Namespace string
	Port      int
}

// ExtProcProcessingModeConfig holds processing mode for K8s
type ExtProcProcessingModeConfig struct {
	Request  *ExtProcBodyModeConfig
	Response *ExtProcBodyModeConfig
}

// ExtProcBodyModeConfig holds body mode for a phase
type ExtProcBodyModeConfig struct {
	Body string
}

// ClientTrafficPolicyConfig represents Envoy Gateway ClientTrafficPolicy configuration
// This targets Gateway objects for downstream client settings
type ClientTrafficPolicyConfig struct {
	Name                string                         // Name of the ClientTrafficPolicy (usually {gatewayName}-ctp)
	Namespace           string                         // Namespace where the ClientTrafficPolicy will be created
	GatewayID           string                         // Domain/Gateway ID for labeling
	TargetRef           ClientTrafficPolicyTargetRef   // Reference to the target Gateway
	TCPKeepalive        *TCPKeepalivePolicyConfig      // TCP keepalive configuration
	EnableProxyProtocol bool                           // Enable PROXY protocol on listener
	Connection          *ConnectionPolicyConfig        // Connection settings
	ClientIPDetection   *ClientIPDetectionPolicyConfig // Client IP detection settings
	Timeout             *TimeoutPolicyConfig           // Timeout settings
	HTTP3               *HTTP3PolicyConfig             // HTTP/3 settings
	TLS                 *TLSPolicyConfig               // TLS settings
	ClientValidation    *ClientValidationPolicyConfig  // mTLS client validation settings
	Headers             *HeadersPolicyConfig           // Headers settings (XFCC)
}

// ClientTrafficPolicyTargetRef represents the target reference for ClientTrafficPolicy
type ClientTrafficPolicyTargetRef struct {
	Group string
	Kind  string
	Name  string
}

// TCPKeepalivePolicyConfig represents TCP keepalive settings for ClientTrafficPolicy
type TCPKeepalivePolicyConfig struct {
	Probes   *int32  // Max keepalive probes before considering dead
	IdleTime *string // Duration before probes start (e.g. "60s")
	Interval *string // Duration between probes (e.g. "10s")
}

// ConnectionPolicyConfig represents connection settings for ClientTrafficPolicy
type ConnectionPolicyConfig struct {
	BufferLimit              *string // Buffer limit (e.g., "32Ki")
	MaxConnections           *int32  // Max concurrent connections
	CloseDelay               *string // Delay before closing rejected connections
	MaxConnectionDuration    *string // Max connection duration
	MaxRequestsPerConnection *int32  // Max requests per connection
}

// TimeoutPolicyConfig represents timeout settings for ClientTrafficPolicy
type TimeoutPolicyConfig struct {
	HTTP *HTTPTimeoutPolicyConfig
}

// HTTPTimeoutPolicyConfig represents HTTP timeout settings
type HTTPTimeoutPolicyConfig struct {
	RequestReceivedTimeout *string // Time to receive complete request headers
	IdleTimeout            *string // Idle connection timeout
}

// HTTP3PolicyConfig represents HTTP/3 settings
type HTTP3PolicyConfig struct {
	Enabled bool
}

// TLSPolicyConfig represents TLS settings for ClientTrafficPolicy
type TLSPolicyConfig struct {
	MinVersion          *string
	MaxVersion          *string
	Ciphers             []string
	ECDHCurves          []string
	SignatureAlgorithms []string
}

// ClientValidationPolicyConfig represents mTLS client validation for ClientTrafficPolicy
type ClientValidationPolicyConfig struct {
	CACertificateRefs []SecretRefPolicyConfig  `json:"caCertificateRefs,omitempty"`
	Optional          bool                     `json:"optional"`
	SANMatchers       []SANMatcherPolicyConfig `json:"sanMatchers,omitempty"`
	CertificateHashes []string                 `json:"certificateHashes,omitempty"`
}

// SecretRefPolicyConfig represents a reference to a K8s Secret
type SecretRefPolicyConfig struct {
	Group string `json:"group"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

// SANMatcherPolicyConfig represents a SAN matcher for mTLS
type SANMatcherPolicyConfig struct {
	Type  string `json:"type"`  // "DNS" or "URI"
	Match string `json:"match"` // Exact match value
}

// XFCCPolicyConfig represents X-Forwarded-Client-Cert header configuration
type XFCCPolicyConfig struct {
	Mode             string   `json:"mode"`             // "Sanitize", "ForwardOnly", "AppendForward"
	CertDetailsToAdd []string `json:"certDetailsToAdd"` // "Hash", "DNS", "URI", "Subject", "Cert", "Chain"
}

// HeadersPolicyConfig represents headers configuration for ClientTrafficPolicy
type HeadersPolicyConfig struct {
	XForwardedClientCert *XFCCPolicyConfig `json:"xForwardedClientCert,omitempty"`
}

// ClientIPDetectionPolicyConfig represents client IP detection settings for ClientTrafficPolicy
type ClientIPDetectionPolicyConfig struct {
	XForwardedFor *XForwardedForPolicyConfig `json:"xForwardedFor,omitempty"`
	CustomHeader  *CustomHeaderPolicyConfig  `json:"customHeader,omitempty"`
}

// XForwardedForPolicyConfig extracts client IP from X-Forwarded-For header
type XForwardedForPolicyConfig struct {
	NumTrustedHops int `json:"numTrustedHops"`
}

// CustomHeaderPolicyConfig extracts client IP from a custom header
type CustomHeaderPolicyConfig struct {
	Name       string `json:"name"`
	FailClosed bool   `json:"failClosed"`
}

// CompressionPolicyConfig represents a compression configuration for BackendTrafficPolicy
type CompressionPolicyConfig struct {
	Type   string // Gzip, Brotli, Zstd
	Gzip   *GzipPolicyConfig
	Brotli *BrotliPolicyConfig
	Zstd   *ZstdPolicyConfig
}

// GzipPolicyConfig represents Gzip-specific compression configuration
type GzipPolicyConfig struct{}

// BrotliPolicyConfig represents Brotli-specific compression configuration
type BrotliPolicyConfig struct{}

// ZstdPolicyConfig represents Zstd-specific compression configuration
type ZstdPolicyConfig struct{}

// RetryPolicyConfig represents retry configuration for BackendTrafficPolicy
type RetryPolicyConfig struct {
	NumRetries *int32
	RetryOn    *RetryOnPolicyConfig
	PerRetry   *PerRetryPolicyConfig
}

// RetryOnPolicyConfig specifies conditions that trigger a retry
type RetryOnPolicyConfig struct {
	HTTPStatusCodes []int
	Triggers        []string
}

// PerRetryPolicyConfig defines per-attempt retry behavior
type PerRetryPolicyConfig struct {
	BackOff *BackOffPolicyConfig
	Timeout *string
}

// BackOffPolicyConfig defines backoff timing between retries
type BackOffPolicyConfig struct {
	BaseInterval *string
	MaxInterval  *string
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

// HTTPRedirectConfig represents HTTP redirect filter configuration
type HTTPRedirectConfig struct {
	Scheme     string           // "http" or "https"
	Hostname   string           // New hostname
	Port       *int             // New port
	StatusCode int              // 301 or 302
	Path       *HTTPPathRewrite // Path rewrite for redirect
}

// HTTPURLRewrite represents URL rewrite configuration
type HTTPURLRewrite struct {
	Hostname *string          // Rewrite Host header
	Path     *HTTPPathRewrite // Rewrite path
}

// HTTPPathRewrite represents path rewrite configuration
type HTTPPathRewrite struct {
	Type               string // ReplacePrefixMatch or ReplaceFullPath
	ReplacePrefixMatch string
	ReplaceFullPath    string
}

// HTTPRouteRule represents a rule in HTTPRoute
type HTTPRouteRule struct {
	PathType    string // Exact, PathPrefix, RegularExpression
	PathValue   string
	Headers     []HeaderMatch
	Method      string
	QueryParams []QueryParamMatch
	BackendRefs []BackendRef
}

// HTTPHeaderModifier represents header modification filters
type HTTPHeaderModifier struct {
	Set    []HTTPHeaderValue // Set header (overwrite if exists)
	Add    []HTTPHeaderValue // Add header (append if exists)
	Remove []string          // Remove header by name
}

// HTTPHeaderValue represents a header name-value pair for modification
type HTTPHeaderValue struct {
	Name  string
	Value string
}

// HeaderMatch represents a header matching rule for HTTPRoute
type HeaderMatch struct {
	Name  string
	Type  string // Exact, RegularExpression
	Value string
}

// QueryParamMatch represents a query param matching rule for HTTPRoute
type QueryParamMatch struct {
	Name  string
	Type  string // Exact, RegularExpression
	Value string
}

// BackendRef represents a backend reference
type BackendRef struct {
	Name      string
	Namespace string
	Port      int
	Weight    int
	// For external backends
	IsExternal bool   // If true, this references a Backend CRD instead of a Service
	Group      string // API group (empty for Service, "gateway.envoyproxy.io" for Backend)
	Kind       string // Kind (empty for Service, "Backend" for external)
}

// BackendConfig represents the configuration for an Envoy Gateway Backend CRD
type BackendConfig struct {
	Name        string
	Namespace   string
	RouteID     string                  // Route UUID for labeling and cleanup
	GatewayID   string                  // Gateway UUID for labeling
	AddressType string                  // "fqdn" or "ip"
	Address     string                  // FQDN hostname or IP address
	Port        int32                   // Port number
	Fallback    bool                    // If true, sets spec.fallback: true for priority-based failover
	TLS         *BackendTLSPolicyConfig // TLS configuration (optional)
}

// BackendTLSPolicyConfig represents TLS configuration for Backend CRD
type BackendTLSPolicyConfig struct {
	InsecureSkipVerify   bool                          // Skip backend cert verification
	SNI                  string                        // User-provided SNI override (empty = auto-derive)
	ClientCertificateRef *BackendSecretRefConfig       // For mTLS
	CACertificateRefs    []BackendCertificateRefConfig // Required for TLS (unless insecureSkipVerify)
}

// BackendSecretRefConfig represents a reference to a Secret
type BackendSecretRefConfig struct {
	Name      string
	Namespace string
}

// BackendCertificateRefConfig represents a reference to a Secret or ConfigMap
type BackendCertificateRefConfig struct {
	Kind      string // "Secret" or "ConfigMap"
	Name      string
	Namespace string
}

// HTTPRouteFilterConfig represents Envoy Gateway HTTPRouteFilter configuration for Direct Response
type HTTPRouteFilterConfig struct {
	Name           string
	Namespace      string
	GatewayID      string // Domain UUID for labeling
	RouteID        string // Route UUID for labeling
	DirectResponse *DirectResponseFilterConfig
}

// DirectResponseFilterConfig represents direct response configuration for HTTPRouteFilter
type DirectResponseFilterConfig struct {
	StatusCode  int
	ContentType string
	Body        *DirectResponseBodyFilterConfig
}

// DirectResponseBodyFilterConfig represents body configuration for HTTPRouteFilter
type DirectResponseBodyFilterConfig struct {
	Type     string                  // "Inline" or "ValueRef"
	Inline   string                  // For Inline type
	ValueRef *DirectResponseValueRef // For ValueRef type
}

// DirectResponseValueRef represents a reference to a ConfigMap
type DirectResponseValueRef struct {
	Group string
	Kind  string
	Name  string
}

// DirectResponseConfigMapConfig represents ConfigMap configuration for Direct Response body
type DirectResponseConfigMapConfig struct {
	Name        string
	Namespace   string
	GatewayID   string // Domain UUID for labeling
	RouteID     string // Route UUID for labeling
	BodyContent string // The response body content
}

// getClient creates a dynamic Kubernetes client for a project
func (s *KubernetesService) getClient(projectID uuid.UUID) (dynamic.Interface, error) {
	project, err := s.projectService.GetByID(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	var config *rest.Config

	switch project.ConnectionType {
	case "in_cluster":
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}

	case "kubeconfig", "api_token", "":
		// Default to api_token behavior for backward compatibility
		config = &rest.Config{
			Host: project.K8sAPIURL,
		}

		// Auth: token or client cert
		if project.K8sTokenEncrypted != "" {
			token, err := s.projectService.GetDecryptedToken(projectID)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt token: %w", err)
			}
			config.BearerToken = token
		} else if project.K8sClientCert != "" {
			config.TLSClientConfig.CertData = []byte(project.K8sClientCert)
			// Decrypt client key
			if project.K8sClientKeyEncrypted != "" {
				clientKey, err := s.projectService.GetDecryptedClientKey(projectID)
				if err != nil {
					return nil, fmt.Errorf("failed to decrypt client key: %w", err)
				}
				config.TLSClientConfig.KeyData = []byte(clientKey)
			}
		}

		// TLS verification
		if project.K8sTLSSkipVerify {
			config.TLSClientConfig.Insecure = true
		} else if project.K8sCACert != "" {
			config.TLSClientConfig.CAData = []byte(project.K8sCACert)
		}
		// else: use system CA bundle (default behavior)

	default:
		return nil, fmt.Errorf("unknown connection type: %s", project.ConnectionType)
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return client, nil
}

// EnsureNamespace ensures the namespace exists, creating it if necessary
func (s *KubernetesService) EnsureNamespace(ctx context.Context, projectID uuid.UUID, namespace string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	// Check if namespace exists
	_, err = client.Resource(gvr).Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		// Namespace already exists
		return nil
	}

	// Create namespace
	ns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
				},
			},
		},
	}

	_, err = client.Resource(gvr).Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	return nil
}

// BuildGatewayObject builds a Gateway unstructured object from the given config.
func BuildGatewayObject(config *GatewayConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	// Build listeners based on TLS mode
	var listeners []interface{}

	// Convert TLS policy to Gateway API format (capitalized)
	tlsPolicyMode := "Terminate" // default
	if strings.ToLower(config.TLSPolicy) == "passthrough" {
		tlsPolicyMode = "Passthrough"
	}

	// HTTP listener helper
	buildHTTPListener := func() map[string]interface{} {
		return map[string]interface{}{
			"name":     "http",
			"port":     int64(config.HTTPPort),
			"protocol": "HTTP",
			"hostname": config.Hostname,
		}
	}

	// HTTPS listener helper
	buildHTTPSListener := func() map[string]interface{} {
		listener := map[string]interface{}{
			"name":     "https",
			"port":     int64(config.HTTPSPort),
			"protocol": "HTTPS",
			"hostname": config.Hostname,
		}
		// Only add TLS config if secret name is provided
		if config.TLSSecretName != "" {
			certRef := map[string]interface{}{
				"kind": "Secret",
				"name": config.TLSSecretName,
			}
			// Add namespace only for cross-namespace references
			if config.TLSSecretNamespace != "" && config.TLSSecretNamespace != config.Namespace {
				certRef["namespace"] = config.TLSSecretNamespace
			}
			listener["tls"] = map[string]interface{}{
				"mode":            tlsPolicyMode,
				"certificateRefs": []interface{}{certRef},
			}
		}
		return listener
	}

	// Build listeners based on TLS mode
	switch config.TLSMode {
	case "tls_only":
		listeners = []interface{}{buildHTTPSListener()}
	case "no_tls":
		listeners = []interface{}{buildHTTPListener()}
	case "both":
		listeners = []interface{}{buildHTTPListener(), buildHTTPSListener()}
	default:
		// Default to TLS only if TLS secret is provided, otherwise HTTP only
		if config.TLSSecretName != "" {
			listeners = []interface{}{buildHTTPSListener()}
		} else {
			listeners = []interface{}{buildHTTPListener()}
		}
	}

	// Build metadata with annotations
	metadata := map[string]interface{}{
		"name":      config.Name,
		"namespace": config.Namespace,
		"labels": map[string]interface{}{
			"app.kubernetes.io/managed-by": "fastgateway",
		},
	}

	// Add annotations if provided
	if len(config.Annotations) > 0 {
		annotations := make(map[string]interface{})
		for k, v := range config.Annotations {
			annotations[k] = v
		}
		metadata["annotations"] = annotations
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "Gateway",
			"metadata":   metadata,
			"spec": map[string]interface{}{
				"gatewayClassName": config.GatewayClassName,
				"listeners":        listeners,
			},
		},
	}
}

// CreateGateway creates a Gateway resource in Kubernetes
func (s *KubernetesService) CreateGateway(ctx context.Context, projectID uuid.UUID, config *GatewayConfig) error {
	// Ensure namespace exists first
	if err := s.EnsureNamespace(ctx, projectID, config.Namespace); err != nil {
		return fmt.Errorf("failed to ensure namespace: %w", err)
	}

	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}

	gateway := BuildGatewayObject(config)

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, gateway, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create gateway: %w", err)
	}

	return nil
}

// DeleteGateway deletes a Gateway resource from Kubernetes
func (s *KubernetesService) DeleteGateway(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete gateway: %w", err)
	}

	return nil
}

// BuildHTTPRouteObject builds a typed Gateway API HTTPRoute object
// This is used for both Kubernetes deployment and YAML preview generation
func BuildHTTPRouteObject(config *HTTPRouteConfig) *gatewayv1.HTTPRoute {
	namespace := gatewayv1.Namespace(config.Namespace)

	// Build rules
	rules := make([]gatewayv1.HTTPRouteRule, 0, len(config.Rules))
	for _, rule := range config.Rules {
		httpRule := gatewayv1.HTTPRouteRule{}

		// Build match
		match := gatewayv1.HTTPRouteMatch{}
		hasMatch := false

		// Path match
		if rule.PathValue != "" {
			pathType := gatewayv1.PathMatchType(rule.PathType)
			match.Path = &gatewayv1.HTTPPathMatch{
				Type:  &pathType,
				Value: &rule.PathValue,
			}
			hasMatch = true
		}

		// Method match
		if rule.Method != "" {
			method := gatewayv1.HTTPMethod(rule.Method)
			match.Method = &method
			hasMatch = true
		}

		// Header matches
		if len(rule.Headers) > 0 {
			headerMatches := make([]gatewayv1.HTTPHeaderMatch, 0, len(rule.Headers))
			for _, h := range rule.Headers {
				headerType := gatewayv1.HeaderMatchType(h.Type)
				headerMatches = append(headerMatches, gatewayv1.HTTPHeaderMatch{
					Type:  &headerType,
					Name:  gatewayv1.HTTPHeaderName(h.Name),
					Value: h.Value,
				})
			}
			match.Headers = headerMatches
			hasMatch = true
		}

		// Query param matches
		if len(rule.QueryParams) > 0 {
			queryMatches := make([]gatewayv1.HTTPQueryParamMatch, 0, len(rule.QueryParams))
			for _, qp := range rule.QueryParams {
				queryType := gatewayv1.QueryParamMatchType(qp.Type)
				queryMatches = append(queryMatches, gatewayv1.HTTPQueryParamMatch{
					Type:  &queryType,
					Name:  gatewayv1.HTTPHeaderName(qp.Name),
					Value: qp.Value,
				})
			}
			match.QueryParams = queryMatches
			hasMatch = true
		}

		if hasMatch {
			httpRule.Matches = []gatewayv1.HTTPRouteMatch{match}
		}

		// Build filters (header modifiers, URL rewrite, redirect) - applied to all rules
		// Note: For direct response routes, we only include response header modifier (request modifier is not applicable)
		var filters []gatewayv1.HTTPRouteFilter
		if config.HTTPRouteFilterName != "" {
			// Direct response route - only response header modifier is applicable
			filters = buildHTTPRouteFilters(nil, config.ResponseHeaderModifier, nil)
		} else {
			filters = buildHTTPRouteFilters(config.RequestHeaderModifier, config.ResponseHeaderModifier, config.URLRewrite)
		}

		// If this is a redirect route, add the redirect filter and skip backend refs
		if config.Redirect != nil {
			redirectFilter := buildRedirectFilter(config.Redirect)
			if redirectFilter != nil {
				filters = append(filters, *redirectFilter)
			}
			// No backend refs for redirect routes
		} else if config.HTTPRouteFilterName != "" {
			// Direct response route - add ExtensionRef filter pointing to HTTPRouteFilter
			extensionRefFilter := gatewayv1.HTTPRouteFilter{
				Type: gatewayv1.HTTPRouteFilterExtensionRef,
				ExtensionRef: &gatewayv1.LocalObjectReference{
					Group: gatewayv1.Group("gateway.envoyproxy.io"),
					Kind:  gatewayv1.Kind("HTTPRouteFilter"),
					Name:  gatewayv1.ObjectName(config.HTTPRouteFilterName),
				},
			}
			filters = append(filters, extensionRefFilter)
			// No backend refs for direct response routes
		} else {
			// Build backend refs (only for non-redirect and non-direct-response routes)
			backendRefs := make([]gatewayv1.HTTPBackendRef, 0, len(rule.BackendRefs))
			for _, backend := range rule.BackendRefs {
				port := gatewayv1.PortNumber(backend.Port)
				backendRef := gatewayv1.HTTPBackendRef{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: gatewayv1.ObjectName(backend.Name),
							Port: &port,
						},
					},
				}
				// Set group and kind for external backends (Backend CRD)
				if backend.IsExternal {
					group := gatewayv1.Group(backend.Group)
					kind := gatewayv1.Kind(backend.Kind)
					backendRef.BackendRef.BackendObjectReference.Group = &group
					backendRef.BackendRef.BackendObjectReference.Kind = &kind
				}
				if backend.Namespace != "" {
					ns := gatewayv1.Namespace(backend.Namespace)
					backendRef.BackendRef.BackendObjectReference.Namespace = &ns
				}
				// Set weight explicitly only if specified (non-zero).
				// weight=0 is used for fallback backends to ensure they don't receive normal traffic.
				// If weight is 0 and it's not explicitly a fallback, omit it so K8s defaults to 1.
				if backend.Weight > 0 {
					weight := int32(backend.Weight)
					backendRef.BackendRef.Weight = &weight
				}
				backendRefs = append(backendRefs, backendRef)
			}
			httpRule.BackendRefs = backendRefs

			// Add mirror filters (only for backend routes)
			if len(config.Mirrors) > 0 {
				mirrorFilters := buildMirrorFilters(config.Mirrors)
				filters = append(filters, mirrorFilters...)
			}
		}

		if len(filters) > 0 {
			httpRule.Filters = filters
		}

		rules = append(rules, httpRule)
	}

	return &gatewayv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Name,
			Namespace: config.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "fastgateway",
				"fastgateway.dev/gateway-id":   config.GatewayID,
				"fastgateway.dev/route-id":     config.RouteID,
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name:      gatewayv1.ObjectName(config.GatewayName),
						Namespace: &namespace,
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(config.Hostname)},
			Rules:     rules,
		},
	}
}

// BuildGRPCRouteObject builds a typed GRPCRoute K8s object from config
func BuildGRPCRouteObject(config *GRPCRouteConfig) *gatewayv1.GRPCRoute {
	if config == nil {
		return nil
	}

	namespace := gatewayv1.Namespace(config.Namespace)

	rules := make([]gatewayv1.GRPCRouteRule, 0, len(config.Rules))
	for _, rule := range config.Rules {
		grpcRule := gatewayv1.GRPCRouteRule{}

		// Build match
		match := gatewayv1.GRPCRouteMatch{}
		hasMatch := false

		// gRPC method match (service + method)
		if rule.GRPCService != nil || rule.GRPCMethod != nil {
			methodMatch := &gatewayv1.GRPCMethodMatch{}
			if rule.GRPCService != nil && rule.GRPCService.Value != "" {
				matchType := gatewayv1.GRPCMethodMatchType(rule.GRPCService.Type)
				if matchType == "" {
					matchType = gatewayv1.GRPCMethodMatchExact
				}
				methodMatch.Type = &matchType
				methodMatch.Service = &rule.GRPCService.Value
			}
			if rule.GRPCMethod != nil && rule.GRPCMethod.Value != "" {
				matchType := gatewayv1.GRPCMethodMatchType(rule.GRPCMethod.Type)
				if matchType == "" {
					matchType = gatewayv1.GRPCMethodMatchExact
				}
				// GRPCMethodMatch has a single Type for both service and method
				if methodMatch.Type == nil {
					methodMatch.Type = &matchType
				}
				methodMatch.Method = &rule.GRPCMethod.Value
			}
			match.Method = methodMatch
			hasMatch = true
		}

		// Header matches
		if len(rule.Headers) > 0 {
			headerMatches := make([]gatewayv1.GRPCHeaderMatch, 0, len(rule.Headers))
			for _, h := range rule.Headers {
				headerType := gatewayv1.GRPCHeaderMatchType(h.Type)
				headerMatches = append(headerMatches, gatewayv1.GRPCHeaderMatch{
					Type:  &headerType,
					Name:  gatewayv1.GRPCHeaderName(h.Name),
					Value: h.Value,
				})
			}
			match.Headers = headerMatches
			hasMatch = true
		}

		if hasMatch {
			grpcRule.Matches = []gatewayv1.GRPCRouteMatch{match}
		}

		// Build filters (request/response header modifiers + mirrors)
		filters := buildGRPCRouteFilters(config.RequestHeaderModifier, config.ResponseHeaderModifier)

		// Add mirror filters
		if len(config.Mirrors) > 0 {
			for _, mirror := range config.Mirrors {
				mirrorNs := gatewayv1.Namespace(mirror.Namespace)
				port := gatewayv1.PortNumber(mirror.Port)
				filters = append(filters, gatewayv1.GRPCRouteFilter{
					Type: gatewayv1.GRPCRouteFilterRequestMirror,
					RequestMirror: &gatewayv1.HTTPRequestMirrorFilter{
						BackendRef: gatewayv1.BackendObjectReference{
							Name:      gatewayv1.ObjectName(mirror.Name),
							Namespace: &mirrorNs,
							Port:      &port,
						},
					},
				})
			}
		}

		if len(filters) > 0 {
			grpcRule.Filters = filters
		}

		// Build backend refs
		if len(rule.BackendRefs) > 0 {
			backendRefs := make([]gatewayv1.GRPCBackendRef, 0, len(rule.BackendRefs))
			for _, backend := range rule.BackendRefs {
				port := gatewayv1.PortNumber(backend.Port)

				ref := gatewayv1.GRPCBackendRef{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: gatewayv1.ObjectName(backend.Name),
							Port: &port,
						},
					},
				}
				if backend.Weight > 0 {
					weight := int32(backend.Weight)
					ref.BackendRef.Weight = &weight
				}

				if backend.IsExternal {
					group := gatewayv1.Group(backend.Group)
					kind := gatewayv1.Kind(backend.Kind)
					ref.BackendRef.Group = &group
					ref.BackendRef.Kind = &kind
				} else if backend.Namespace != "" {
					ns := gatewayv1.Namespace(backend.Namespace)
					ref.BackendRef.Namespace = &ns
				}

				backendRefs = append(backendRefs, ref)
			}
			grpcRule.BackendRefs = backendRefs
		}

		rules = append(rules, grpcRule)
	}

	hostname := gatewayv1.Hostname(config.Hostname)

	grpcRoute := &gatewayv1.GRPCRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "GRPCRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Name,
			Namespace: config.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "fastgateway",
				"fastgateway.dev/gateway-id":   config.GatewayID,
				"fastgateway.dev/route-id":     config.RouteID,
			},
		},
		Spec: gatewayv1.GRPCRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name:      gatewayv1.ObjectName(config.GatewayName),
						Namespace: &namespace,
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{hostname},
			Rules:     rules,
		},
	}

	return grpcRoute
}

// buildGRPCRouteFilters builds GRPCRouteFilter slice for header modification
func buildGRPCRouteFilters(reqMod, resMod *HTTPHeaderModifier) []gatewayv1.GRPCRouteFilter {
	var filters []gatewayv1.GRPCRouteFilter

	if reqMod != nil {
		headerFilter := buildHTTPHeaderFilter(reqMod)
		if headerFilter != nil {
			filters = append(filters, gatewayv1.GRPCRouteFilter{
				Type:                  gatewayv1.GRPCRouteFilterRequestHeaderModifier,
				RequestHeaderModifier: headerFilter,
			})
		}
	}

	if resMod != nil {
		headerFilter := buildHTTPHeaderFilter(resMod)
		if headerFilter != nil {
			filters = append(filters, gatewayv1.GRPCRouteFilter{
				Type:                   gatewayv1.GRPCRouteFilterResponseHeaderModifier,
				ResponseHeaderModifier: headerFilter,
			})
		}
	}

	return filters
}

// buildHTTPRouteFilters builds HTTPRoute filters for header modification and URL rewrite
// Note: CORS filters are handled separately via addCORSFiltersToUnstructured since they're an Envoy Gateway extension
func buildHTTPRouteFilters(reqMod, resMod *HTTPHeaderModifier, urlRewrite *HTTPURLRewrite) []gatewayv1.HTTPRouteFilter {
	var filters []gatewayv1.HTTPRouteFilter

	// Request header modifier
	if reqMod != nil && (len(reqMod.Set) > 0 || len(reqMod.Add) > 0 || len(reqMod.Remove) > 0) {
		filter := gatewayv1.HTTPRouteFilter{
			Type:                  gatewayv1.HTTPRouteFilterRequestHeaderModifier,
			RequestHeaderModifier: buildHTTPHeaderFilter(reqMod),
		}
		filters = append(filters, filter)
	}

	// Response header modifier
	if resMod != nil && (len(resMod.Set) > 0 || len(resMod.Add) > 0 || len(resMod.Remove) > 0) {
		filter := gatewayv1.HTTPRouteFilter{
			Type:                   gatewayv1.HTTPRouteFilterResponseHeaderModifier,
			ResponseHeaderModifier: buildHTTPHeaderFilter(resMod),
		}
		filters = append(filters, filter)
	}

	// URL rewrite
	if urlRewrite != nil && (urlRewrite.Hostname != nil || urlRewrite.Path != nil) {
		filter := gatewayv1.HTTPRouteFilter{
			Type:       gatewayv1.HTTPRouteFilterURLRewrite,
			URLRewrite: buildURLRewriteFilter(urlRewrite),
		}
		filters = append(filters, filter)
	}

	return filters
}

// buildMirrorFilters builds RequestMirror filters for HTTPRoute
func buildMirrorFilters(mirrors []MirrorRef) []gatewayv1.HTTPRouteFilter {
	var filters []gatewayv1.HTTPRouteFilter

	for _, mirror := range mirrors {
		port := gatewayv1.PortNumber(mirror.Port)
		mirrorFilter := gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterRequestMirror,
			RequestMirror: &gatewayv1.HTTPRequestMirrorFilter{
				BackendRef: gatewayv1.BackendObjectReference{
					Name: gatewayv1.ObjectName(mirror.Name),
					Port: &port,
				},
			},
		}

		// Add namespace if specified
		if mirror.Namespace != "" {
			ns := gatewayv1.Namespace(mirror.Namespace)
			mirrorFilter.RequestMirror.BackendRef.Namespace = &ns
		}

		filters = append(filters, mirrorFilter)
	}

	return filters
}

// buildRedirectFilter builds a Gateway API RequestRedirect filter
func buildRedirectFilter(redirect *HTTPRedirectConfig) *gatewayv1.HTTPRouteFilter {
	if redirect == nil {
		return nil
	}

	filter := &gatewayv1.HTTPRouteFilter{
		Type:            gatewayv1.HTTPRouteFilterRequestRedirect,
		RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{},
	}

	// Set scheme
	if redirect.Scheme != "" {
		filter.RequestRedirect.Scheme = &redirect.Scheme
	}

	// Set hostname
	if redirect.Hostname != "" {
		hostname := gatewayv1.PreciseHostname(redirect.Hostname)
		filter.RequestRedirect.Hostname = &hostname
	}

	// Set port
	if redirect.Port != nil {
		port := gatewayv1.PortNumber(*redirect.Port)
		filter.RequestRedirect.Port = &port
	}

	// Set status code
	if redirect.StatusCode > 0 {
		statusCode := redirect.StatusCode
		filter.RequestRedirect.StatusCode = &statusCode
	}

	// Set path rewrite
	if redirect.Path != nil {
		pathMod := &gatewayv1.HTTPPathModifier{}
		switch redirect.Path.Type {
		case "ReplacePrefixMatch":
			pathMod.Type = gatewayv1.PrefixMatchHTTPPathModifier
			pathMod.ReplacePrefixMatch = &redirect.Path.ReplacePrefixMatch
		case "ReplaceFullPath":
			pathMod.Type = gatewayv1.FullPathHTTPPathModifier
			pathMod.ReplaceFullPath = &redirect.Path.ReplaceFullPath
		}
		filter.RequestRedirect.Path = pathMod
	}

	return filter
}

// stringSliceToInterfaceSlice converts []string to []interface{} for unstructured objects
// Kubernetes unstructured library cannot deep copy []string directly
func stringSliceToInterfaceSlice(s []string) []interface{} {
	result := make([]interface{}, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}

// BuildSecurityPolicy builds an Envoy Gateway SecurityPolicy from config
// TODO: Migrate CORS to HTTPRoute filters when GEP-1767 is standard (https://gateway-api.sigs.k8s.io/geps/gep-1767/)
func BuildSecurityPolicy(config *SecurityPolicyConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	spec := map[string]interface{}{
		"targetRef": map[string]interface{}{
			"group": config.TargetRef.Group,
			"kind":  config.TargetRef.Kind,
			"name":  config.TargetRef.Name,
		},
	}

	// Add CORS configuration if present
	if config.CORS != nil {
		corsConfig := buildCORSConfig(config.CORS)
		if corsConfig != nil {
			spec["cors"] = corsConfig
		}
	}

	// Add Authorization configuration if present (IP allowlisting)
	if config.Authorization != nil {
		authConfig := buildAuthorizationConfig(config.Authorization)
		if authConfig != nil {
			spec["authorization"] = authConfig
		}
	}

	// Add API Key Auth configuration if present
	if config.APIKeyAuth != nil {
		apiKeyAuthConfig := buildAPIKeyAuthConfig(config.APIKeyAuth)
		if apiKeyAuthConfig != nil {
			spec["apiKeyAuth"] = apiKeyAuthConfig
		}
	}

	// Add JWT Auth configuration if present
	if config.JWT != nil {
		jwtConfig := buildJWTAuthConfig(config.JWT)
		if jwtConfig != nil {
			spec["jwt"] = jwtConfig
		}
	}

	// Add OIDC configuration if present
	if config.OIDC != nil {
		oidcConfig := buildOIDCConfig(config.OIDC)
		if oidcConfig != nil {
			spec["oidc"] = oidcConfig
		}
	}

	// Add ExtAuth configuration if present
	if config.ExtAuth != nil {
		extAuthConfig := buildExtAuthConfig(config.ExtAuth)
		if extAuthConfig != nil {
			spec["extAuth"] = extAuthConfig
		}
	}

	// Check if any security feature is configured
	_, hasCORS := spec["cors"]
	_, hasAuth := spec["authorization"]
	_, hasAPIKeyAuth := spec["apiKeyAuth"]
	_, hasJWT := spec["jwt"]
	_, hasOIDC := spec["oidc"]
	_, hasExtAuth := spec["extAuth"]
	if !hasCORS && !hasAuth && !hasAPIKeyAuth && !hasJWT && !hasOIDC && !hasExtAuth {
		// No security features configured
		return nil
	}

	securityPolicy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "SecurityPolicy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
					"fastgateway.dev/gateway-id":   config.GatewayID,
					"fastgateway.dev/route-id":     config.RouteID,
				},
			},
			"spec": spec,
		},
	}

	return securityPolicy
}

// buildCORSConfig builds the CORS configuration for SecurityPolicy
// Uses Envoy Gateway's SecurityPolicy CORS format
func buildCORSConfig(cors *CORSPolicyConfig) map[string]interface{} {
	if cors == nil {
		return nil
	}

	// Check if CORS has any configuration
	if len(cors.AllowOrigins) == 0 && len(cors.AllowMethods) == 0 && len(cors.AllowHeaders) == 0 {
		return nil
	}

	corsConfig := make(map[string]interface{})

	// allowOrigins - simple string array for Envoy Gateway SecurityPolicy
	if len(cors.AllowOrigins) > 0 {
		corsConfig["allowOrigins"] = stringSliceToInterfaceSlice(cors.AllowOrigins)
	}

	if len(cors.AllowMethods) > 0 {
		corsConfig["allowMethods"] = stringSliceToInterfaceSlice(cors.AllowMethods)
	}
	if len(cors.AllowHeaders) > 0 {
		corsConfig["allowHeaders"] = stringSliceToInterfaceSlice(cors.AllowHeaders)
	}
	if len(cors.ExposeHeaders) > 0 {
		corsConfig["exposeHeaders"] = stringSliceToInterfaceSlice(cors.ExposeHeaders)
	}
	if cors.MaxAge != nil {
		corsConfig["maxAge"] = fmt.Sprintf("%ds", *cors.MaxAge)
	}
	if cors.AllowCredentials != nil {
		corsConfig["allowCredentials"] = *cors.AllowCredentials
	}

	return corsConfig
}

// buildAuthorizationConfig builds the authorization configuration for SecurityPolicy
// Uses Envoy Gateway's SecurityPolicy authorization format for IP allowlisting and JWT claims
func buildAuthorizationConfig(auth *AuthorizationPolicyConfig) map[string]interface{} {
	if auth == nil {
		return nil
	}
	if len(auth.Rules) == 0 && auth.DefaultAction != "Deny" {
		return nil
	}

	rules := make([]interface{}, 0, len(auth.Rules))
	for _, rule := range auth.Rules {
		// Check if rule has any principal or operation
		hasClientCIDRs := len(rule.ClientCIDRs) > 0
		hasJWT := rule.JWT != nil && rule.JWT.Provider != ""
		hasHeaders := len(rule.Headers) > 0
		hasMethods := len(rule.Methods) > 0

		if !hasClientCIDRs && !hasJWT && !hasHeaders && !hasMethods {
			continue
		}

		principal := map[string]interface{}{}

		// Build clientCIDRs if present
		if hasClientCIDRs {
			cidrs := make([]interface{}, 0, len(rule.ClientCIDRs))
			for _, cidr := range rule.ClientCIDRs {
				cidrs = append(cidrs, cidr)
			}
			principal["clientCIDRs"] = cidrs
		}

		// Build JWT principal if present
		if hasJWT {
			jwtPrincipal := map[string]interface{}{
				"provider": rule.JWT.Provider,
			}

			// Add claims if present
			if len(rule.JWT.Claims) > 0 {
				claims := make([]interface{}, 0, len(rule.JWT.Claims))
				for _, claim := range rule.JWT.Claims {
					claimMap := map[string]interface{}{
						"name":   claim.Name,
						"values": stringSliceToInterfaceSlice(claim.Values),
					}
					// Determine the effective valueType for the CRD.
					// Envoy Gateway CRD supports: "String" (default), "StringArray"
					// FastGateway internal types like "StringContains" are not supported by CRD.
					//
					// IMPORTANT: Envoy Gateway's jwt_authn filter normalizes the "scope" claim
					// via normalize_payload_in_metadata.space_delimited_claims, splitting it from
					// a space-delimited string into an array. Therefore, RBAC matching on "scope"
					// must always use "StringArray" regardless of the user-specified valueType.
					effectiveValueType := claim.ValueType
					if claim.Name == "scope" {
						effectiveValueType = "StringArray"
					}
					switch effectiveValueType {
					case "StringArray":
						claimMap["valueType"] = "StringArray"
					}
					// "Exact", "String", "" all use the default (String), so no need to set
					claims = append(claims, claimMap)
				}
				jwtPrincipal["claims"] = claims
			}

			principal["jwt"] = jwtPrincipal
		}

		// Build headers if present
		if hasHeaders {
			headers := make([]interface{}, 0, len(rule.Headers))
			for _, h := range rule.Headers {
				headers = append(headers, map[string]interface{}{
					"name":   h.Name,
					"values": stringSliceToInterfaceSlice(h.Values),
				})
			}
			principal["headers"] = headers
		}

		ruleMap := map[string]interface{}{
			"action":    rule.Action,
			"principal": principal,
		}

		// Build operation.methods if present
		if hasMethods {
			ruleMap["operation"] = map[string]interface{}{
				"methods": stringSliceToInterfaceSlice(rule.Methods),
			}
		}

		rules = append(rules, ruleMap)
	}

	if len(rules) == 0 {
		if auth.DefaultAction == "Deny" {
			// Deny-all with no rules: block everything
			return map[string]interface{}{
				"defaultAction": auth.DefaultAction,
			}
		}
		return nil
	}

	return map[string]interface{}{
		"defaultAction": auth.DefaultAction,
		"rules":         rules,
	}
}

// buildAPIKeyAuthConfig builds the API Key Auth configuration for SecurityPolicy
// Uses Envoy Gateway's SecurityPolicy apiKeyAuth format
func buildAPIKeyAuthConfig(apiKeyAuth *APIKeyAuthPolicyConfig) map[string]interface{} {
	if apiKeyAuth == nil || len(apiKeyAuth.CredentialRefs) == 0 {
		return nil
	}

	config := map[string]interface{}{}

	// Build credentialRefs
	refs := make([]interface{}, 0, len(apiKeyAuth.CredentialRefs))
	for _, ref := range apiKeyAuth.CredentialRefs {
		refMap := map[string]interface{}{
			"name": ref.Name,
		}
		if ref.Namespace != "" {
			refMap["namespace"] = ref.Namespace
		}
		refs = append(refs, refMap)
	}
	config["credentialRefs"] = refs

	// Build extractFrom
	if len(apiKeyAuth.ExtractFrom) > 0 {
		extractFrom := make([]interface{}, 0, len(apiKeyAuth.ExtractFrom))
		for _, ef := range apiKeyAuth.ExtractFrom {
			efMap := map[string]interface{}{}
			if len(ef.Headers) > 0 {
				efMap["headers"] = stringSliceToInterfaceSlice(ef.Headers)
			}
			if len(efMap) > 0 {
				extractFrom = append(extractFrom, efMap)
			}
		}
		if len(extractFrom) > 0 {
			config["extractFrom"] = extractFrom
		}
	}

	return config
}

// buildJWTAuthConfig builds the JWT Auth configuration for SecurityPolicy
// Uses Envoy Gateway's SecurityPolicy jwt format
func buildJWTAuthConfig(jwt *JWTAuthPolicyConfig) map[string]interface{} {
	if jwt == nil || len(jwt.Providers) == 0 {
		return nil
	}

	providers := make([]interface{}, 0, len(jwt.Providers))
	for _, p := range jwt.Providers {
		provider := map[string]interface{}{
			"name":   p.Name,
			"issuer": p.Issuer,
		}

		// Add remoteJWKS
		if p.JWKSURL != "" {
			provider["remoteJWKS"] = map[string]interface{}{
				"uri": p.JWKSURL,
			}
		}

		// Add audiences if present
		if len(p.Audiences) > 0 {
			provider["audiences"] = stringSliceToInterfaceSlice(p.Audiences)
		}

		// Add claimToHeaders if present
		if len(p.ClaimToHeaders) > 0 {
			claimToHeaders := make([]interface{}, 0, len(p.ClaimToHeaders))
			for _, cth := range p.ClaimToHeaders {
				claimToHeaders = append(claimToHeaders, map[string]interface{}{
					"claim":  cth.Claim,
					"header": cth.Header,
				})
			}
			provider["claimToHeaders"] = claimToHeaders
		}

		providers = append(providers, provider)
	}

	return map[string]interface{}{
		"providers": providers,
	}
}

// buildOIDCConfig builds the OIDC configuration for SecurityPolicy
func buildOIDCConfig(oidc *OIDCPolicyConfig) map[string]interface{} {
	if oidc == nil {
		return nil
	}

	config := map[string]interface{}{
		"provider": map[string]interface{}{
			"issuer": oidc.Issuer,
		},
		"clientID": oidc.ClientID,
		"clientSecret": map[string]interface{}{
			"name":      oidc.ClientSecretName,
			"namespace": oidc.ClientSecretNS,
		},
		"redirectURL": oidc.RedirectURL,
		"logoutPath":  oidc.LogoutPath,
	}

	if len(oidc.Scopes) > 0 {
		config["scopes"] = stringSliceToInterfaceSlice(oidc.Scopes)
	}

	if oidc.CookieDomain != "" {
		config["cookieDomain"] = oidc.CookieDomain
	}

	return config
}

// buildExtAuthConfig builds the extAuth configuration for SecurityPolicy
// Uses direct K8s Service reference as per Envoy Gateway documentation
func buildExtAuthConfig(extAuth *models.ExtAuthConfig) map[string]interface{} {
	if extAuth == nil {
		return nil
	}

	extAuthConfig := map[string]interface{}{}

	// Build HTTP or gRPC service config with direct Service reference
	if extAuth.Type == "http" && extAuth.HTTP != nil {
		backendRef := map[string]interface{}{
			"name": extAuth.HTTP.BackendRef.Name,
			"port": extAuth.HTTP.BackendRef.Port,
		}
		// Add namespace for cross-namespace references
		if extAuth.HTTP.BackendRef.Namespace != "" {
			backendRef["namespace"] = extAuth.HTTP.BackendRef.Namespace
		}
		httpConfig := map[string]interface{}{
			"backendRefs": []interface{}{backendRef},
			"path":        extAuth.HTTP.Path,
		}
		if len(extAuth.HTTP.HeadersToBackend) > 0 {
			httpConfig["headersToBackend"] = stringSliceToInterfaceSlice(extAuth.HTTP.HeadersToBackend)
		}
		extAuthConfig["http"] = httpConfig
	} else if extAuth.Type == "grpc" && extAuth.GRPC != nil {
		backendRef := map[string]interface{}{
			"name": extAuth.GRPC.BackendRef.Name,
			"port": extAuth.GRPC.BackendRef.Port,
		}
		// Add namespace for cross-namespace references
		if extAuth.GRPC.BackendRef.Namespace != "" {
			backendRef["namespace"] = extAuth.GRPC.BackendRef.Namespace
		}
		grpcConfig := map[string]interface{}{
			"backendRefs": []interface{}{backendRef},
		}
		extAuthConfig["grpc"] = grpcConfig
	}

	// Common options
	if extAuth.FailOpen != nil {
		extAuthConfig["failOpen"] = *extAuth.FailOpen
	}
	if len(extAuth.HeadersToExtAuth) > 0 {
		extAuthConfig["headersToExtAuth"] = stringSliceToInterfaceSlice(extAuth.HeadersToExtAuth)
	}
	// Note: headersToDownstreamOnDeny, headersToDownstreamOnAllow, headersToUpstreamOnAllow
	// are stored in the DB model but do NOT exist in the EG SecurityPolicy CRD.
	// Only headersToBackend (inside http config) is supported for response header forwarding.

	// Map withRequestBody to EG's bodyToExtAuth with maxRequestBytes
	if extAuth.WithRequestBody != nil {
		extAuthConfig["bodyToExtAuth"] = map[string]interface{}{
			"maxRequestBytes": extAuth.WithRequestBody.MaxBytes,
		}
	}

	return extAuthConfig
}

// getGRPCRouteGVR returns the GroupVersionResource for Gateway API GRPCRoute
func getGRPCRouteGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "grpcroutes",
	}
}

// getSecurityPolicyGVR returns the GroupVersionResource for Envoy Gateway SecurityPolicy
func getSecurityPolicyGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "securitypolicies",
	}
}

// buildURLRewriteFilter builds an HTTPURLRewriteFilter from our HTTPURLRewrite
func buildURLRewriteFilter(rewrite *HTTPURLRewrite) *gatewayv1.HTTPURLRewriteFilter {
	filter := &gatewayv1.HTTPURLRewriteFilter{}

	// Hostname rewrite
	if rewrite.Hostname != nil && *rewrite.Hostname != "" {
		hostname := gatewayv1.PreciseHostname(*rewrite.Hostname)
		filter.Hostname = &hostname
	}

	// Path rewrite
	if rewrite.Path != nil {
		pathMod := &gatewayv1.HTTPPathModifier{}
		switch rewrite.Path.Type {
		case "ReplacePrefixMatch":
			pathMod.Type = gatewayv1.PrefixMatchHTTPPathModifier
			pathMod.ReplacePrefixMatch = &rewrite.Path.ReplacePrefixMatch
		case "ReplaceFullPath":
			pathMod.Type = gatewayv1.FullPathHTTPPathModifier
			pathMod.ReplaceFullPath = &rewrite.Path.ReplaceFullPath
		}
		filter.Path = pathMod
	}

	return filter
}

// buildHTTPHeaderFilter builds an HTTPHeaderFilter from our HTTPHeaderModifier
func buildHTTPHeaderFilter(mod *HTTPHeaderModifier) *gatewayv1.HTTPHeaderFilter {
	filter := &gatewayv1.HTTPHeaderFilter{}

	// Set headers
	if len(mod.Set) > 0 {
		set := make([]gatewayv1.HTTPHeader, 0, len(mod.Set))
		for _, h := range mod.Set {
			set = append(set, gatewayv1.HTTPHeader{
				Name:  gatewayv1.HTTPHeaderName(h.Name),
				Value: h.Value,
			})
		}
		filter.Set = set
	}

	// Add headers
	if len(mod.Add) > 0 {
		add := make([]gatewayv1.HTTPHeader, 0, len(mod.Add))
		for _, h := range mod.Add {
			add = append(add, gatewayv1.HTTPHeader{
				Name:  gatewayv1.HTTPHeaderName(h.Name),
				Value: h.Value,
			})
		}
		filter.Add = add
	}

	// Remove headers
	if len(mod.Remove) > 0 {
		filter.Remove = mod.Remove
	}

	return filter
}

// CreateHTTPRoute creates an HTTPRoute resource in Kubernetes.
// If the resource already exists (e.g. from a partial previous deploy), it falls back to update.
func (s *KubernetesService) CreateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *HTTPRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	// Build typed HTTPRoute object
	httpRoute := BuildHTTPRouteObject(config)

	// Convert to unstructured for dynamic client
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(httpRoute)
	if err != nil {
		return fmt.Errorf("failed to convert HTTPRoute to unstructured: %w", err)
	}

	unstructuredRoute := &unstructured.Unstructured{Object: unstructuredObj}

	_, createErr := client.Resource(gvr).Namespace(config.Namespace).Create(ctx, unstructuredRoute, metav1.CreateOptions{})
	if createErr != nil {
		if k8serrors.IsAlreadyExists(createErr) {
			// Resource already exists (partial previous deploy), fall back to update
			return s.UpdateHTTPRoute(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create httproute: %w", createErr)
	}
	return nil
}

// UpdateHTTPRoute updates an HTTPRoute resource in Kubernetes using a proper update (not delete+create)
func (s *KubernetesService) UpdateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *HTTPRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	// Get the existing HTTPRoute to preserve resourceVersion and other metadata
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing httproute: %w", err)
	}

	// Build the new typed HTTPRoute object
	httpRoute := BuildHTTPRouteObject(config)

	// Convert to unstructured for dynamic client
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(httpRoute)
	if err != nil {
		return fmt.Errorf("failed to convert HTTPRoute to unstructured: %w", err)
	}

	unstructuredRoute := &unstructured.Unstructured{Object: unstructuredObj}

	// Preserve the resourceVersion from existing object (required for update)
	unstructuredRoute.SetResourceVersion(existing.GetResourceVersion())
	// Preserve UID to ensure we're updating the same object
	unstructuredRoute.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, unstructuredRoute, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update httproute: %w", err)
	}

	return nil
}

// DeleteHTTPRoute deletes an HTTPRoute resource from Kubernetes
func (s *KubernetesService) DeleteHTTPRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// HTTPRoute not found, already deleted
			return nil
		}
		return fmt.Errorf("failed to delete httproute: %w", err)
	}

	return nil
}

// CreateGRPCRoute creates a GRPCRoute in Kubernetes
func (s *KubernetesService) CreateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *GRPCRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getGRPCRouteGVR()

	grpcRoute := BuildGRPCRouteObject(config)
	if grpcRoute == nil {
		return fmt.Errorf("failed to build GRPCRoute object")
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(grpcRoute)
	if err != nil {
		return fmt.Errorf("failed to convert GRPCRoute to unstructured: %w", err)
	}

	obj := &unstructured.Unstructured{Object: unstructuredObj}
	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return s.UpdateGRPCRoute(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create GRPCRoute: %w", err)
	}
	return nil
}

// UpdateGRPCRoute updates a GRPCRoute in Kubernetes
func (s *KubernetesService) UpdateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *GRPCRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getGRPCRouteGVR()

	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing GRPCRoute: %w", err)
	}

	grpcRoute := BuildGRPCRouteObject(config)
	if grpcRoute == nil {
		return fmt.Errorf("failed to build GRPCRoute object")
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(grpcRoute)
	if err != nil {
		return fmt.Errorf("failed to convert GRPCRoute to unstructured: %w", err)
	}

	obj := &unstructured.Unstructured{Object: unstructuredObj}
	obj.SetResourceVersion(existing.GetResourceVersion())
	obj.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update GRPCRoute: %w", err)
	}
	return nil
}

// DeleteGRPCRoute deletes a GRPCRoute from Kubernetes
func (s *KubernetesService) DeleteGRPCRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getGRPCRouteGVR()
	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// GRPCRoute not found, already deleted
			return nil
		}
		return err
	}
	return nil
}

// CreateSecurityPolicy creates an Envoy Gateway SecurityPolicy resource in Kubernetes.
// If the resource already exists (e.g. from a partial previous deploy), it falls back to update.
func (s *KubernetesService) CreateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *SecurityPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	securityPolicy := BuildSecurityPolicy(config)
	if securityPolicy == nil {
		// No security features configured, nothing to create
		return nil
	}

	gvr := getSecurityPolicyGVR()

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, securityPolicy, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Resource already exists (partial previous deploy), fall back to update
			return s.UpdateSecurityPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create securitypolicy: %w", err)
	}

	return nil
}

// UpdateSecurityPolicy updates an Envoy Gateway SecurityPolicy resource in Kubernetes
func (s *KubernetesService) UpdateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *SecurityPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getSecurityPolicyGVR()

	securityPolicy := BuildSecurityPolicy(config)
	if securityPolicy == nil {
		// No security features configured, delete existing if any
		return s.DeleteSecurityPolicy(ctx, projectID, config.Namespace, config.Name)
	}

	// Get the existing SecurityPolicy to preserve resourceVersion
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		// If not found, create it
		if strings.Contains(err.Error(), "not found") {
			return s.CreateSecurityPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to get existing securitypolicy: %w", err)
	}

	// Preserve the resourceVersion from existing object
	securityPolicy.SetResourceVersion(existing.GetResourceVersion())
	securityPolicy.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, securityPolicy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update securitypolicy: %w", err)
	}

	return nil
}

// DeleteSecurityPolicy deletes an Envoy Gateway SecurityPolicy resource from Kubernetes
func (s *KubernetesService) DeleteSecurityPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getSecurityPolicyGVR()

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		// Ignore not found errors
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete securitypolicy: %w", err)
	}

	return nil
}

// getBackendTrafficPolicyGVR returns the GroupVersionResource for Envoy Gateway BackendTrafficPolicy
func getBackendTrafficPolicyGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "backendtrafficpolicies",
	}
}

// BuildBackendTrafficPolicy builds an Envoy Gateway BackendTrafficPolicy from config
func BuildBackendTrafficPolicy(config *BackendTrafficPolicyConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	spec := map[string]interface{}{
		"targetRef": map[string]interface{}{
			"group": config.TargetRef.Group,
			"kind":  config.TargetRef.Kind,
			"name":  config.TargetRef.Name,
		},
	}

	// Add compression configuration if present
	if len(config.Compression) > 0 {
		compressorArray := make([]interface{}, 0, len(config.Compression))
		for _, comp := range config.Compression {
			compEntry := map[string]interface{}{
				"type": comp.Type,
			}
			// Add type-specific config (empty objects for now)
			switch comp.Type {
			case "Gzip":
				compEntry["gzip"] = map[string]interface{}{}
			case "Brotli":
				compEntry["brotli"] = map[string]interface{}{}
			case "Zstd":
				compEntry["zstd"] = map[string]interface{}{}
			}
			compressorArray = append(compressorArray, compEntry)
		}
		spec["compressor"] = compressorArray
	}

	// Add retry configuration if present
	if config.Retry != nil {
		retry := map[string]interface{}{}

		if config.Retry.NumRetries != nil {
			retry["numRetries"] = *config.Retry.NumRetries
		}

		if config.Retry.RetryOn != nil {
			retryOn := map[string]interface{}{}
			if len(config.Retry.RetryOn.HTTPStatusCodes) > 0 {
				codes := make([]interface{}, len(config.Retry.RetryOn.HTTPStatusCodes))
				for i, code := range config.Retry.RetryOn.HTTPStatusCodes {
					codes[i] = int64(code)
				}
				retryOn["httpStatusCodes"] = codes
			}
			if len(config.Retry.RetryOn.Triggers) > 0 {
				triggers := make([]interface{}, len(config.Retry.RetryOn.Triggers))
				for i, t := range config.Retry.RetryOn.Triggers {
					triggers[i] = t
				}
				retryOn["triggers"] = triggers
			}
			if len(retryOn) > 0 {
				retry["retryOn"] = retryOn
			}
		}

		if config.Retry.PerRetry != nil {
			perRetry := map[string]interface{}{}
			if config.Retry.PerRetry.Timeout != nil {
				perRetry["timeout"] = *config.Retry.PerRetry.Timeout
			}
			if config.Retry.PerRetry.BackOff != nil {
				backOff := map[string]interface{}{}
				if config.Retry.PerRetry.BackOff.BaseInterval != nil {
					backOff["baseInterval"] = *config.Retry.PerRetry.BackOff.BaseInterval
				}
				if config.Retry.PerRetry.BackOff.MaxInterval != nil {
					backOff["maxInterval"] = *config.Retry.PerRetry.BackOff.MaxInterval
				}
				if len(backOff) > 0 {
					perRetry["backOff"] = backOff
				}
			}
			if len(perRetry) > 0 {
				retry["perRetry"] = perRetry
			}
		}

		if len(retry) > 0 {
			spec["retry"] = retry
		}
	}

	// Add load balancer configuration if present
	if config.LoadBalancer != nil {
		lb := map[string]interface{}{
			"type": config.LoadBalancer.Type,
		}
		if config.LoadBalancer.ConsistentHash != nil {
			ch := map[string]interface{}{
				"type": config.LoadBalancer.ConsistentHash.Type,
			}
			if config.LoadBalancer.ConsistentHash.Header != nil {
				ch["header"] = map[string]interface{}{
					"name": config.LoadBalancer.ConsistentHash.Header.Name,
				}
			}
			if config.LoadBalancer.ConsistentHash.Cookie != nil {
				cookie := map[string]interface{}{
					"name": config.LoadBalancer.ConsistentHash.Cookie.Name,
				}
				if config.LoadBalancer.ConsistentHash.Cookie.TTL != nil {
					cookie["ttl"] = *config.LoadBalancer.ConsistentHash.Cookie.TTL
				}
				if len(config.LoadBalancer.ConsistentHash.Cookie.Attributes) > 0 {
					attrs := make(map[string]interface{})
					for k, v := range config.LoadBalancer.ConsistentHash.Cookie.Attributes {
						attrs[k] = v
					}
					cookie["attributes"] = attrs
				}
				ch["cookie"] = cookie
			}
			lb["consistentHash"] = ch
		}
		spec["loadBalancer"] = lb
	}

	// Add circuit breaker configuration if present
	if config.CircuitBreaker != nil {
		cb := map[string]interface{}{}
		if config.CircuitBreaker.MaxConnections != nil {
			cb["maxConnections"] = *config.CircuitBreaker.MaxConnections
		}
		if config.CircuitBreaker.MaxPendingRequests != nil {
			cb["maxPendingRequests"] = *config.CircuitBreaker.MaxPendingRequests
		}
		if config.CircuitBreaker.MaxParallelRequests != nil {
			cb["maxParallelRequests"] = *config.CircuitBreaker.MaxParallelRequests
		}
		if config.CircuitBreaker.MaxParallelRetries != nil {
			cb["maxParallelRetries"] = *config.CircuitBreaker.MaxParallelRetries
		}
		if config.CircuitBreaker.MaxRequestsPerConnection != nil {
			cb["maxRequestsPerConnection"] = *config.CircuitBreaker.MaxRequestsPerConnection
		}
		if len(cb) > 0 {
			spec["circuitBreaker"] = cb
		}
	}

	// Add health check configuration if present
	if config.HealthCheck != nil {
		hc := map[string]interface{}{}

		if config.HealthCheck.Active != nil {
			active := map[string]interface{}{
				"type": config.HealthCheck.Active.Type,
			}
			if config.HealthCheck.Active.Timeout != nil {
				active["timeout"] = *config.HealthCheck.Active.Timeout
			}
			if config.HealthCheck.Active.Interval != nil {
				active["interval"] = *config.HealthCheck.Active.Interval
			}
			if config.HealthCheck.Active.UnhealthyThreshold != nil {
				active["unhealthyThreshold"] = *config.HealthCheck.Active.UnhealthyThreshold
			}
			if config.HealthCheck.Active.HealthyThreshold != nil {
				active["healthyThreshold"] = *config.HealthCheck.Active.HealthyThreshold
			}
			// CRD requires the type-specific field to be present based on the type value
			switch config.HealthCheck.Active.Type {
			case "HTTP":
				http := map[string]interface{}{}
				if config.HealthCheck.Active.HTTP != nil {
					http["path"] = config.HealthCheck.Active.HTTP.Path
					if config.HealthCheck.Active.HTTP.Method != nil {
						http["method"] = *config.HealthCheck.Active.HTTP.Method
					}
					if len(config.HealthCheck.Active.HTTP.ExpectedStatuses) > 0 {
						statuses := make([]interface{}, len(config.HealthCheck.Active.HTTP.ExpectedStatuses))
						for i, s := range config.HealthCheck.Active.HTTP.ExpectedStatuses {
							statuses[i] = int64(s)
						}
						http["expectedStatuses"] = statuses
					}
				}
				active["http"] = http
			case "TCP":
				tcp := map[string]interface{}{}
				if config.HealthCheck.Active.TCP != nil {
					if config.HealthCheck.Active.TCP.SendText != nil {
						tcp["send"] = map[string]interface{}{
							"type": "Text",
							"text": *config.HealthCheck.Active.TCP.SendText,
						}
					}
					if config.HealthCheck.Active.TCP.ReceiveText != nil {
						tcp["receive"] = map[string]interface{}{
							"type": "Text",
							"text": *config.HealthCheck.Active.TCP.ReceiveText,
						}
					}
				}
				active["tcp"] = tcp
			case "GRPC":
				grpc := map[string]interface{}{}
				if config.HealthCheck.Active.GRPC != nil {
					if config.HealthCheck.Active.GRPC.Service != nil {
						grpc["service"] = *config.HealthCheck.Active.GRPC.Service
					}
				}
				active["grpc"] = grpc
			}
			hc["active"] = active
		}

		if config.HealthCheck.Passive != nil {
			passive := map[string]interface{}{}
			if config.HealthCheck.Passive.ConsecutiveGatewayErrors != nil {
				passive["consecutiveGatewayErrors"] = *config.HealthCheck.Passive.ConsecutiveGatewayErrors
			}
			if config.HealthCheck.Passive.Consecutive5xxErrors != nil {
				passive["consecutive5XxErrors"] = *config.HealthCheck.Passive.Consecutive5xxErrors
			}
			if config.HealthCheck.Passive.ConsecutiveLocalOriginFailures != nil {
				passive["consecutiveLocalOriginFailures"] = *config.HealthCheck.Passive.ConsecutiveLocalOriginFailures
			}
			if config.HealthCheck.Passive.Interval != nil {
				passive["interval"] = *config.HealthCheck.Passive.Interval
			}
			if config.HealthCheck.Passive.BaseEjectionTime != nil {
				passive["baseEjectionTime"] = *config.HealthCheck.Passive.BaseEjectionTime
			}
			if config.HealthCheck.Passive.MaxEjectionPercent != nil {
				passive["maxEjectionPercent"] = *config.HealthCheck.Passive.MaxEjectionPercent
			}
			if config.HealthCheck.Passive.SplitExternalLocalOriginErrors != nil {
				passive["splitExternalLocalOriginErrors"] = *config.HealthCheck.Passive.SplitExternalLocalOriginErrors
			}
			if len(passive) > 0 {
				hc["passive"] = passive
			}
		}

		if config.HealthCheck.PanicThreshold != nil {
			hc["panicThreshold"] = *config.HealthCheck.PanicThreshold
		}

		if len(hc) > 0 {
			spec["healthCheck"] = hc
		}
	}

	// Add fault injection configuration if present
	if config.FaultInjection != nil {
		fi := map[string]interface{}{}

		if config.FaultInjection.Delay != nil {
			delay := map[string]interface{}{
				"fixedDelay": config.FaultInjection.Delay.FixedDelay,
			}
			if config.FaultInjection.Delay.Percentage != nil {
				delay["percentage"] = *config.FaultInjection.Delay.Percentage
			}
			fi["delay"] = delay
		}

		if config.FaultInjection.Abort != nil {
			abort := map[string]interface{}{}
			if config.FaultInjection.Abort.HTTPStatus != nil {
				abort["httpStatus"] = *config.FaultInjection.Abort.HTTPStatus
			}
			if config.FaultInjection.Abort.GRPCStatus != nil {
				abort["grpcStatus"] = *config.FaultInjection.Abort.GRPCStatus
			}
			if config.FaultInjection.Abort.Percentage != nil {
				abort["percentage"] = *config.FaultInjection.Abort.Percentage
			}
			fi["abort"] = abort
		}

		if len(fi) > 0 {
			spec["faultInjection"] = fi
		}
	}

	// Add rate limit configuration if present
	if config.RateLimit != nil && config.RateLimit.Global != nil {
		rules := make([]interface{}, 0, len(config.RateLimit.Global.Rules))
		for _, rule := range config.RateLimit.Global.Rules {
			ruleMap := map[string]interface{}{
				"limit": map[string]interface{}{
					"requests": int64(rule.Limit.Requests),
					"unit":     rule.Limit.Unit,
				},
			}
			if len(rule.ClientSelectors) > 0 {
				selectors := make([]interface{}, 0, len(rule.ClientSelectors))
				for _, sel := range rule.ClientSelectors {
					selMap := map[string]interface{}{}
					if len(sel.Headers) > 0 {
						headers := make([]interface{}, 0, len(sel.Headers))
						for _, h := range sel.Headers {
							hMap := map[string]interface{}{
								"name": h.Name,
							}
							if h.Value != "" {
								hMap["value"] = h.Value
							}
							if h.Type != "" {
								hMap["type"] = h.Type
							}
							if h.Invert {
								hMap["invert"] = true
							}
							headers = append(headers, hMap)
						}
						selMap["headers"] = headers
					}
					if sel.SourceCIDR != nil {
						cidr := map[string]interface{}{
							"value": sel.SourceCIDR.Value,
						}
						if sel.SourceCIDR.Type != "" {
							cidr["type"] = sel.SourceCIDR.Type
						}
						selMap["sourceCIDR"] = cidr
					}
					if sel.Path != nil {
						selMap["path"] = map[string]interface{}{
							"value": sel.Path.Value,
							"type":  sel.Path.Type,
						}
					}
					if len(sel.Methods) > 0 {
						methods := make([]interface{}, len(sel.Methods))
						for i, m := range sel.Methods {
							methods[i] = m
						}
						selMap["methods"] = methods
					}
					selectors = append(selectors, selMap)
				}
				ruleMap["clientSelectors"] = selectors
			}
			rules = append(rules, ruleMap)
		}
		spec["rateLimit"] = map[string]interface{}{
			"global": map[string]interface{}{
				"rules": rules,
			},
		}
	}

	// Add request buffer configuration if present
	if config.RequestBuffer != nil {
		spec["requestBuffer"] = map[string]interface{}{
			"limit": config.RequestBuffer.Limit,
		}
	}

	// Add response override configuration if present
	if len(config.ResponseOverride) > 0 {
		rules := make([]interface{}, 0, len(config.ResponseOverride))
		for _, rule := range config.ResponseOverride {
			statusCodes := make([]interface{}, 0, len(rule.Match.StatusCodes))
			for _, sc := range rule.Match.StatusCodes {
				scMap := map[string]interface{}{
					"type": sc.Type,
				}
				if sc.Value != nil {
					scMap["value"] = *sc.Value
				}
				if sc.Range != nil {
					scMap["range"] = map[string]interface{}{
						"start": sc.Range.Start,
						"end":   sc.Range.End,
					}
				}
				statusCodes = append(statusCodes, scMap)
			}

			body := map[string]interface{}{
				"type": rule.Response.Body.Type,
			}
			if rule.Response.Body.Inline != "" {
				body["inline"] = rule.Response.Body.Inline
			}
			if rule.Response.Body.ValueRef != nil {
				valueRef := map[string]interface{}{
					"kind": rule.Response.Body.ValueRef.Kind,
					"name": rule.Response.Body.ValueRef.Name,
				}
				if rule.Response.Body.ValueRef.Group != "" {
					valueRef["group"] = rule.Response.Body.ValueRef.Group
				}
				if rule.Response.Body.ValueRef.Namespace != "" {
					valueRef["namespace"] = rule.Response.Body.ValueRef.Namespace
				}
				body["valueRef"] = valueRef
			}

			ruleMap := map[string]interface{}{
				"match": map[string]interface{}{
					"statusCodes": statusCodes,
				},
				"response": map[string]interface{}{
					"contentType": rule.Response.ContentType,
					"body":        body,
				},
			}
			rules = append(rules, ruleMap)
		}
		spec["responseOverride"] = rules
	}

	// Add timeout configuration if present
	if config.Timeout != nil {
		timeout := map[string]interface{}{}
		if config.Timeout.TCP != nil {
			tcp := map[string]interface{}{}
			if config.Timeout.TCP.ConnectTimeout != "" {
				tcp["connectTimeout"] = config.Timeout.TCP.ConnectTimeout
			}
			if len(tcp) > 0 {
				timeout["tcp"] = tcp
			}
		}
		if config.Timeout.HTTP != nil {
			http := map[string]interface{}{}
			if config.Timeout.HTTP.RequestTimeout != "" {
				http["requestTimeout"] = config.Timeout.HTTP.RequestTimeout
			}
			if config.Timeout.HTTP.ConnectionIdleTimeout != "" {
				http["connectionIdleTimeout"] = config.Timeout.HTTP.ConnectionIdleTimeout
			}
			if config.Timeout.HTTP.MaxConnectionDuration != "" {
				http["maxConnectionDuration"] = config.Timeout.HTTP.MaxConnectionDuration
			}
			if config.Timeout.HTTP.MaxStreamDuration != "" {
				http["maxStreamDuration"] = config.Timeout.HTTP.MaxStreamDuration
			}
			if len(http) > 0 {
				timeout["http"] = http
			}
		}
		if len(timeout) > 0 {
			spec["timeout"] = timeout
		}
	}

	// Check if any feature is configured
	hasFeature := false
	if _, ok := spec["compressor"]; ok {
		hasFeature = true
	}
	if _, ok := spec["retry"]; ok {
		hasFeature = true
	}
	if _, ok := spec["loadBalancer"]; ok {
		hasFeature = true
	}
	if _, ok := spec["circuitBreaker"]; ok {
		hasFeature = true
	}
	if _, ok := spec["healthCheck"]; ok {
		hasFeature = true
	}
	if _, ok := spec["faultInjection"]; ok {
		hasFeature = true
	}
	if _, ok := spec["rateLimit"]; ok {
		hasFeature = true
	}
	if _, ok := spec["requestBuffer"]; ok {
		hasFeature = true
	}
	if _, ok := spec["responseOverride"]; ok {
		hasFeature = true
	}
	if _, ok := spec["timeout"]; ok {
		hasFeature = true
	}
	if !hasFeature {
		return nil
	}

	labels := map[string]interface{}{
		"app.kubernetes.io/managed-by": "fastgateway",
		"fastgateway.dev/gateway-id":   config.GatewayID,
	}
	if config.RouteID != "" {
		labels["fastgateway.dev/route-id"] = config.RouteID
	}
	if config.DomainID != "" {
		labels["fastgateway.dev/domain-id"] = config.DomainID
	}

	backendTrafficPolicy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "BackendTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    labels,
			},
			"spec": spec,
		},
	}

	return backendTrafficPolicy
}

// CreateBackendTrafficPolicy creates an Envoy Gateway BackendTrafficPolicy resource in Kubernetes.
// If the resource already exists (e.g. from a partial previous deploy), it falls back to update.
func (s *KubernetesService) CreateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *BackendTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	backendTrafficPolicy := BuildBackendTrafficPolicy(config)
	if backendTrafficPolicy == nil {
		// No features configured, nothing to create
		return nil
	}

	gvr := getBackendTrafficPolicyGVR()

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, backendTrafficPolicy, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Resource already exists (partial previous deploy), fall back to update
			return s.UpdateBackendTrafficPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create backendtrafficpolicy: %w", err)
	}

	return nil
}

// UpdateBackendTrafficPolicy updates an Envoy Gateway BackendTrafficPolicy resource in Kubernetes
func (s *KubernetesService) UpdateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *BackendTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getBackendTrafficPolicyGVR()

	backendTrafficPolicy := BuildBackendTrafficPolicy(config)
	if backendTrafficPolicy == nil {
		// No features configured, delete existing if any
		return s.DeleteBackendTrafficPolicy(ctx, projectID, config.Namespace, config.Name)
	}

	// Get the existing BackendTrafficPolicy to preserve resourceVersion
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		// If not found, create it
		if strings.Contains(err.Error(), "not found") {
			return s.CreateBackendTrafficPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to get existing backendtrafficpolicy: %w", err)
	}

	// Preserve the resourceVersion from existing object
	backendTrafficPolicy.SetResourceVersion(existing.GetResourceVersion())
	backendTrafficPolicy.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, backendTrafficPolicy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update backendtrafficpolicy: %w", err)
	}

	return nil
}

// DeleteBackendTrafficPolicy deletes an Envoy Gateway BackendTrafficPolicy resource from Kubernetes
func (s *KubernetesService) DeleteBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getBackendTrafficPolicyGVR()

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		// Ignore not found errors
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete backendtrafficpolicy: %w", err)
	}

	return nil
}

// getEnvoyExtensionPolicyGVR returns the GroupVersionResource for EnvoyExtensionPolicy
func getEnvoyExtensionPolicyGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyextensionpolicies",
	}
}

// BuildEnvoyExtensionPolicy builds an EnvoyExtensionPolicy CRD
func BuildEnvoyExtensionPolicy(config *EnvoyExtensionPolicyK8sConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	spec := map[string]interface{}{
		"targetRefs": []map[string]interface{}{
			{
				"group": config.TargetRef.Group,
				"kind":  config.TargetRef.Kind,
				"name":  config.TargetRef.Name,
			},
		},
	}

	hasContent := false

	if len(config.Lua) > 0 {
		luaConfigs := make([]map[string]interface{}, 0, len(config.Lua))
		for _, lua := range config.Lua {
			luaMap := map[string]interface{}{
				"type": lua.Type,
			}
			if lua.Type == "Inline" && lua.Inline != "" {
				luaMap["inline"] = lua.Inline
			}
			if lua.Type == "ValueRef" && lua.ValueRef != nil {
				valueRef := map[string]interface{}{
					"kind": lua.ValueRef.Kind,
					"name": lua.ValueRef.Name,
				}
				if lua.ValueRef.Group != "" {
					valueRef["group"] = lua.ValueRef.Group
				}
				if lua.ValueRef.Namespace != "" {
					valueRef["namespace"] = lua.ValueRef.Namespace
				}
				luaMap["valueRef"] = valueRef
			}
			luaConfigs = append(luaConfigs, luaMap)
		}
		spec["lua"] = luaConfigs
		hasContent = true
	}

	if len(config.Wasm) > 0 {
		wasmConfigs := make([]map[string]interface{}, 0, len(config.Wasm))
		for _, wasm := range config.Wasm {
			wasmMap := map[string]interface{}{
				"name": wasm.Name,
			}
			if wasm.RootID != "" {
				wasmMap["rootID"] = wasm.RootID
			}
			if wasm.Config != nil {
				// Parse the JSON config string and set it directly as an object
				// Envoy Gateway will handle the protobuf wrapping internally
				var configObj interface{}
				if err := json.Unmarshal([]byte(*wasm.Config), &configObj); err == nil {
					wasmMap["config"] = configObj
				}
			}

			codeMap := map[string]interface{}{
				"type": wasm.Code.Type,
			}
			if wasm.Code.Type == "HTTP" && wasm.Code.HTTP != nil {
				codeMap["http"] = map[string]interface{}{
					"url":    wasm.Code.HTTP.URL,
					"sha256": wasm.Code.HTTP.SHA256,
				}
			}
			if wasm.Code.Type == "Image" && wasm.Code.Image != nil {
				imageMap := map[string]interface{}{
					"url": wasm.Code.Image.URL,
				}
				if wasm.Code.Image.SHA256 != "" {
					imageMap["sha256"] = wasm.Code.Image.SHA256
				}
				if wasm.Code.Image.PullSecret != nil {
					pullSecretRef := map[string]interface{}{
						"kind": wasm.Code.Image.PullSecret.Kind,
						"name": wasm.Code.Image.PullSecret.Name,
					}
					if wasm.Code.Image.PullSecret.Group != "" {
						pullSecretRef["group"] = wasm.Code.Image.PullSecret.Group
					}
					if wasm.Code.Image.PullSecret.Namespace != "" {
						pullSecretRef["namespace"] = wasm.Code.Image.PullSecret.Namespace
					}
					imageMap["pullSecretRef"] = pullSecretRef
				}
				codeMap["image"] = imageMap
			}
			wasmMap["code"] = codeMap

			wasmConfigs = append(wasmConfigs, wasmMap)
		}
		spec["wasm"] = wasmConfigs
		hasContent = true
	}

	if len(config.ExtProc) > 0 {
		var extProcEntries []interface{}
		for _, ep := range config.ExtProc {
			// Determine Backend CRD name: domain-level vs route-level
			var extProcBackendName string
			if config.RouteID != "" {
				extProcBackendName = GenerateExtProcBackendName(config.RouteID)
			} else if config.DomainID != "" {
				extProcBackendName = GenerateExtProcBackendNameForDomain(config.TargetRef.Name)
			}
			entry := map[string]interface{}{
				"backendRefs": []interface{}{
					map[string]interface{}{
						"group":     "",
						"kind":      "Backend",
						"name":      extProcBackendName,
						"namespace": config.Namespace,
					},
				},
			}

			if ep.ProcessingMode != nil {
				pm := map[string]interface{}{}
				if ep.ProcessingMode.Request != nil && ep.ProcessingMode.Request.Body != "" && ep.ProcessingMode.Request.Body != "None" {
					pm["request"] = map[string]interface{}{
						"body": ep.ProcessingMode.Request.Body,
					}
				}
				if ep.ProcessingMode.Response != nil && ep.ProcessingMode.Response.Body != "" && ep.ProcessingMode.Response.Body != "None" {
					pm["response"] = map[string]interface{}{
						"body": ep.ProcessingMode.Response.Body,
					}
				}
				if len(pm) > 0 {
					entry["processingMode"] = pm
				}
			}

			if ep.FailOpen {
				entry["failOpen"] = true
			}

			extProcEntries = append(extProcEntries, entry)
		}
		spec["extProc"] = extProcEntries
		hasContent = true
	}

	if !hasContent {
		return nil
	}

	eepLabels := map[string]interface{}{
		"app.kubernetes.io/managed-by": "fastgateway",
		"fastgateway.dev/gateway-id":   config.GatewayID,
	}
	if config.RouteID != "" {
		eepLabels["fastgateway.dev/route-id"] = config.RouteID
	}
	if config.DomainID != "" {
		eepLabels["fastgateway.dev/domain-id"] = config.DomainID
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "EnvoyExtensionPolicy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    eepLabels,
			},
			"spec": spec,
		},
	}
}

// CreateEnvoyExtensionPolicy creates an EnvoyExtensionPolicy resource in Kubernetes
func (s *KubernetesService) CreateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getEnvoyExtensionPolicyGVR()

	_, err = client.Resource(gvr).Namespace(policy.GetNamespace()).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return s.UpdateEnvoyExtensionPolicy(ctx, projectID, policy)
		}
		return fmt.Errorf("failed to create envoyextensionpolicy: %w", err)
	}

	return nil
}

// UpdateEnvoyExtensionPolicy updates an EnvoyExtensionPolicy resource in Kubernetes
func (s *KubernetesService) UpdateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getEnvoyExtensionPolicyGVR()

	existing, err := client.Resource(gvr).Namespace(policy.GetNamespace()).Get(ctx, policy.GetName(), metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return s.CreateEnvoyExtensionPolicy(ctx, projectID, policy)
		}
		return fmt.Errorf("failed to get envoyextensionpolicy: %w", err)
	}

	policy.SetResourceVersion(existing.GetResourceVersion())
	_, err = client.Resource(gvr).Namespace(policy.GetNamespace()).Update(ctx, policy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update envoyextensionpolicy: %w", err)
	}

	return nil
}

// DeleteEnvoyExtensionPolicy deletes an EnvoyExtensionPolicy resource from Kubernetes
func (s *KubernetesService) DeleteEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getEnvoyExtensionPolicyGVR()

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete envoyextensionpolicy: %w", err)
	}

	return nil
}

// getBackendGVR returns the GroupVersionResource for Envoy Gateway Backend
func getBackendGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "backends",
	}
}

// BuildBackend builds an Envoy Gateway Backend CRD as unstructured object
func BuildBackend(config *BackendConfig) *unstructured.Unstructured {
	// Build endpoint based on address type
	var endpoint map[string]interface{}
	if config.AddressType == "fqdn" {
		endpoint = map[string]interface{}{
			"fqdn": map[string]interface{}{
				"hostname": config.Address,
				"port":     config.Port,
			},
		}
	} else {
		// IP address
		endpoint = map[string]interface{}{
			"ip": map[string]interface{}{
				"address": config.Address,
				"port":    config.Port,
			},
		}
	}

	// Build spec with endpoints
	spec := map[string]interface{}{
		"endpoints": []interface{}{endpoint},
	}

	// Add fallback flag if enabled (for priority-based failover)
	if config.Fallback {
		spec["fallback"] = true
	}

	// Add TLS configuration if present
	if config.TLS != nil {
		tlsSpec := map[string]interface{}{}

		// Add insecureSkipVerify if true
		if config.TLS.InsecureSkipVerify {
			tlsSpec["insecureSkipVerify"] = true
		}

		// Add CA certificate references (only when not skipping verification)
		if !config.TLS.InsecureSkipVerify && len(config.TLS.CACertificateRefs) > 0 {
			caRefs := make([]interface{}, len(config.TLS.CACertificateRefs))
			for i, ref := range config.TLS.CACertificateRefs {
				ns := ref.Namespace
				if ns == "" {
					ns = FastGatewayNamespace
				}
				caRef := map[string]interface{}{
					"group":     "",
					"kind":      ref.Kind,
					"name":      ref.Name,
					"namespace": ns,
				}
				caRefs[i] = caRef
			}
			tlsSpec["caCertificateRefs"] = caRefs
		}

		// Add client certificate reference for mTLS
		if config.TLS.ClientCertificateRef != nil {
			clientNs := config.TLS.ClientCertificateRef.Namespace
			if clientNs == "" {
				clientNs = FastGatewayNamespace
			}
			clientRef := map[string]interface{}{
				"group":     "",
				"kind":      "Secret",
				"name":      config.TLS.ClientCertificateRef.Name,
				"namespace": clientNs,
			}
			tlsSpec["clientCertificateRef"] = clientRef
		}

		// Set SNI: user override takes precedence, otherwise auto-derive from FQDN
		if config.TLS.SNI != "" {
			tlsSpec["sni"] = config.TLS.SNI
		} else if config.AddressType == "fqdn" && config.Address != "" {
			tlsSpec["sni"] = config.Address
		}

		spec["tls"] = tlsSpec
	}

	backend := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "Backend",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
					"fastgateway.dev/gateway-id":   config.GatewayID,
					"fastgateway.dev/route-id":     config.RouteID,
				},
			},
			"spec": spec,
		},
	}

	return backend
}

// ExtAuthBackendConfig holds configuration for building ext-auth Backend CRD
type ExtAuthBackendConfig struct {
	Name      string // Backend CRD name
	Namespace string
	GatewayID string
	RouteID   string
	ClientID  string // Empty for general mode, set for client mode
	Service   models.ExtAuthBackendRef
}

// BuildExtAuthBackend builds a Backend CRD for external auth service
func BuildExtAuthBackend(config *ExtAuthBackendConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	// Build FQDN endpoint pointing to the K8s service
	serviceNamespace := config.Service.Namespace
	if serviceNamespace == "" {
		serviceNamespace = config.Namespace
	}
	fqdn := fmt.Sprintf("%s.%s.svc.cluster.local", config.Service.Name, serviceNamespace)

	spec := map[string]interface{}{
		"endpoints": []interface{}{
			map[string]interface{}{
				"fqdn": map[string]interface{}{
					"hostname": fqdn,
					"port":     config.Service.Port,
				},
			},
		},
	}

	labels := map[string]interface{}{
		"app.kubernetes.io/managed-by": "fastgateway",
		"fastgateway.dev/gateway-id":   config.GatewayID,
		"fastgateway.dev/route-id":     config.RouteID,
		"fastgateway.dev/type":         "ext-auth",
	}
	if config.ClientID != "" {
		labels["fastgateway.dev/client-id"] = config.ClientID
	}

	backend := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "Backend",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    labels,
			},
			"spec": spec,
		},
	}

	return backend
}

// GenerateExtAuthBackendName generates a unique name for ext-auth Backend CRD
func GenerateExtAuthBackendName(routeID, clientID string) string {
	if clientID != "" {
		// Client mode: include client ID
		return fmt.Sprintf("fg-extauth-%s-%s", routeID[:8], clientID[:8])
	}
	// General mode: just route ID
	return fmt.Sprintf("fg-extauth-%s", routeID[:8])
}

// ExtProcBackendConfig holds config for building an ext-proc Backend CRD
type ExtProcBackendConfig struct {
	Name      string
	Namespace string
	GatewayID string
	RouteID   string
	DomainID  string
	Service   ExtProcBackendRefPolicyConfig
}

// BuildExtProcBackend builds a Backend CRD for an ext-proc service
func BuildExtProcBackend(config *ExtProcBackendConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	fqdn := fmt.Sprintf("%s.%s.svc.cluster.local", config.Service.Name, config.Service.Namespace)

	labels := map[string]interface{}{
		"app.kubernetes.io/managed-by": "fastgateway",
		"fastgateway.dev/gateway-id":   config.GatewayID,
		"fastgateway.dev/type":         "ext-proc",
	}
	if config.RouteID != "" {
		labels["fastgateway.dev/route-id"] = config.RouteID
	}
	if config.DomainID != "" {
		labels["fastgateway.dev/domain-id"] = config.DomainID
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "Backend",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels":    labels,
			},
			"spec": map[string]interface{}{
				"endpoints": []interface{}{
					map[string]interface{}{
						"fqdn": map[string]interface{}{
							"hostname": fqdn,
							"port":     int64(config.Service.Port),
						},
					},
				},
			},
		},
	}
}

// GenerateExtProcBackendName generates the Backend CRD name for an ext-proc service
func GenerateExtProcBackendName(routeID string) string {
	return fmt.Sprintf("ext-proc-backend-%s", routeID)
}

// GenerateExtProcBackendNameForDomain generates the Backend CRD name for a domain-level ext-proc service
func GenerateExtProcBackendNameForDomain(gatewayName string) string {
	return gatewayName + "-eep-extproc"
}

// CreateBackend creates an Envoy Gateway Backend resource in Kubernetes
func (s *KubernetesService) CreateBackend(ctx context.Context, projectID uuid.UUID, config *BackendConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	backend := BuildBackend(config)
	gvr := getBackendGVR()

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, backend, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create backend: %w", err)
	}

	return nil
}

// UpdateBackend updates an Envoy Gateway Backend resource in Kubernetes
func (s *KubernetesService) UpdateBackend(ctx context.Context, projectID uuid.UUID, config *BackendConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getBackendGVR()

	// Get the existing Backend to preserve resourceVersion
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		// If not found, create it
		if strings.Contains(err.Error(), "not found") {
			return s.CreateBackend(ctx, projectID, config)
		}
		return fmt.Errorf("failed to get existing backend: %w", err)
	}

	backend := BuildBackend(config)
	backend.SetResourceVersion(existing.GetResourceVersion())
	backend.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, backend, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update backend: %w", err)
	}

	return nil
}

// DeleteBackend deletes an Envoy Gateway Backend resource from Kubernetes
func (s *KubernetesService) DeleteBackend(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getBackendGVR()

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		// Ignore not found errors
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete backend: %w", err)
	}

	return nil
}

// UpdateBackendUnstructured creates or updates an Envoy Gateway Backend resource from unstructured object
func (s *KubernetesService) UpdateBackendUnstructured(ctx context.Context, projectID uuid.UUID, backend *unstructured.Unstructured) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getBackendGVR()
	namespace := backend.GetNamespace()
	name := backend.GetName()

	// Get the existing Backend to preserve resourceVersion
	existing, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		// If not found, create it
		if strings.Contains(err.Error(), "not found") {
			_, createErr := client.Resource(gvr).Namespace(namespace).Create(ctx, backend, metav1.CreateOptions{})
			if createErr != nil {
				return fmt.Errorf("failed to create backend: %w", createErr)
			}
			return nil
		}
		return fmt.Errorf("failed to get existing backend: %w", err)
	}

	backend.SetResourceVersion(existing.GetResourceVersion())
	backend.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(namespace).Update(ctx, backend, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update backend: %w", err)
	}

	return nil
}

// DeleteBackendsByRoute deletes all Envoy Gateway Backend resources associated with a route
func (s *KubernetesService) DeleteBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getBackendGVR()

	// List backends with the route label
	labelSelector := fmt.Sprintf("fastgateway.dev/route-id=%s", routeID)
	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		// If the CRD doesn't exist, ignore the error
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "the server could not find") {
			return nil
		}
		return fmt.Errorf("failed to list backends: %w", err)
	}

	// Delete each backend
	for _, item := range list.Items {
		err = client.Resource(gvr).Namespace(namespace).Delete(ctx, item.GetName(), metav1.DeleteOptions{})
		if err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("failed to delete backend %s: %w", item.GetName(), err)
		}
	}

	return nil
}

// DeleteStaleBackendsByRoute deletes Backend CRDs for a route that are not in the expectedNames set.
// This allows updating external backends without deleting and recreating unchanged ones.
func (s *KubernetesService) DeleteStaleBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string, expectedNames map[string]bool) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getBackendGVR()

	// List backends with the route label
	labelSelector := fmt.Sprintf("fastgateway.dev/route-id=%s", routeID)
	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		// If the CRD doesn't exist, ignore the error
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "the server could not find") {
			return nil
		}
		return fmt.Errorf("failed to list backends: %w", err)
	}

	// Delete only backends that are no longer expected
	for _, item := range list.Items {
		if !expectedNames[item.GetName()] {
			err = client.Resource(gvr).Namespace(namespace).Delete(ctx, item.GetName(), metav1.DeleteOptions{})
			if err != nil && !strings.Contains(err.Error(), "not found") {
				return fmt.Errorf("failed to delete stale backend %s: %w", item.GetName(), err)
			}
		}
	}

	return nil
}

// TestConnection tests the Kubernetes connection
func (s *KubernetesService) TestConnection(ctx context.Context, projectID uuid.UUID) (bool, string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return false, "", err
	}

	// Try to list namespaces to test connection
	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	_, err = client.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return false, "", fmt.Errorf("failed to connect: %w", err)
	}

	// Get server version
	return true, "Connected", nil
}

// ListNamespaces lists namespaces in the cluster
func (s *KubernetesService) ListNamespaces(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	list, err := client.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	namespaces := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		namespaces = append(namespaces, item.GetName())
	}

	return namespaces, nil
}

// ListServices lists services in a namespace
func (s *KubernetesService) ListServices(ctx context.Context, projectID uuid.UUID, namespace string) ([]map[string]interface{}, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "services",
	}

	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	services := make([]map[string]interface{}, 0, len(list.Items))
	for _, item := range list.Items {
		spec, _, _ := unstructured.NestedMap(item.Object, "spec")
		ports, _, _ := unstructured.NestedSlice(spec, "ports")

		portList := make([]map[string]interface{}, 0)
		for _, p := range ports {
			if portMap, ok := p.(map[string]interface{}); ok {
				portList = append(portList, map[string]interface{}{
					"name":     portMap["name"],
					"port":     portMap["port"],
					"protocol": portMap["protocol"],
				})
			}
		}

		services = append(services, map[string]interface{}{
			"name":      item.GetName(),
			"namespace": item.GetNamespace(),
			"ports":     portList,
		})
	}

	return services, nil
}

// TLSSecretInfo represents a kubernetes.io/tls secret for the API response
type TLSSecretInfo struct {
	Name                 string            `json:"name"`
	Namespace            string            `json:"namespace"`
	ManagedByFastgateway bool              `json:"managedByFastgateway"`
	Labels               map[string]string `json:"labels"`
	CreatedAt            string            `json:"createdAt"`
}

// ListTLSSecrets lists kubernetes.io/tls secrets in the specified namespace
func (s *KubernetesService) ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) ([]TLSSecretInfo, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "type=kubernetes.io/tls",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list TLS secrets: %w", err)
	}

	secrets := make([]TLSSecretInfo, 0, len(list.Items))
	for _, item := range list.Items {
		labels := item.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}

		managedBy := labels["app.kubernetes.io/managed-by"] == "fastgateway"

		createdAt := ""
		if ts := item.GetCreationTimestamp(); !ts.IsZero() {
			createdAt = ts.Format("2006-01-02T15:04:05Z")
		}

		secrets = append(secrets, TLSSecretInfo{
			Name:                 item.GetName(),
			Namespace:            item.GetNamespace(),
			ManagedByFastgateway: managedBy,
			Labels:               labels,
			CreatedAt:            createdAt,
		})
	}

	return secrets, nil
}

// ListGatewayClasses lists available GatewayClasses
func (s *KubernetesService) ListGatewayClasses(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	list, err := client.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list gateway classes: %w", err)
	}

	classes := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		classes = append(classes, item.GetName())
	}

	return classes, nil
}

// PrerequisiteCheck represents the result of a prerequisite check
type PrerequisiteCheck struct {
	NamespaceExists    bool   `json:"namespaceExists"`
	GatewayCRDExists   bool   `json:"gatewayCrdExists"`
	HTTPRouteCRDExists bool   `json:"httprouteCrdExists"`
	ErrorMessage       string `json:"errorMessage,omitempty"`
}

// getClientDirect creates a dynamic Kubernetes client directly from URL and token
func (s *KubernetesService) getClientDirect(apiURL, token string) (dynamic.Interface, error) {
	config := &rest.Config{
		Host:        apiURL,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true, // TODO: Make this configurable
		},
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return client, nil
}

// ValidatePrerequisites checks if the Kubernetes cluster has the required prerequisites
// - fastgateway-system namespace must exist
// - Gateway API CRDs must be installed (Gateway, HTTPRoute)
func (s *KubernetesService) ValidatePrerequisites(ctx context.Context, apiURL, token string) (*PrerequisiteCheck, error) {
	client, err := s.getClientDirect(apiURL, token)
	if err != nil {
		// Check for common connection issues
		errStr := err.Error()
		if strings.Contains(errStr, "127.0.0.1") || strings.Contains(errStr, "localhost") {
			return nil, fmt.Errorf("failed to connect to Kubernetes at %s: If running FastGateway in Docker, use 'host.docker.internal' instead of 'localhost' or '127.0.0.1'. Original error: %w", apiURL, err)
		}
		if strings.Contains(errStr, "connection refused") {
			return nil, fmt.Errorf("connection refused to %s: Ensure the Kubernetes API server is accessible from FastGateway. If running in Docker, use 'host.docker.internal' or your machine's actual IP address. Original error: %w", apiURL, err)
		}
		return nil, fmt.Errorf("failed to connect to Kubernetes at %s: %w", apiURL, err)
	}

	result := &PrerequisiteCheck{}
	var checkErrors []string

	// Check if fastgateway-system namespace exists
	nsGVR := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	_, err = client.Resource(nsGVR).Get(ctx, FastGatewayNamespace, metav1.GetOptions{})
	if err == nil {
		result.NamespaceExists = true
	} else {
		checkErrors = append(checkErrors, fmt.Sprintf("namespace check: %v", err))
	}

	// Check if Gateway CRD exists by trying to list gateways
	gatewayGVR := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}

	_, err = client.Resource(gatewayGVR).List(ctx, metav1.ListOptions{Limit: 1})
	if err == nil {
		result.GatewayCRDExists = true
	} else {
		// Check if it's a "not found" error (CRD not installed) vs permission error
		if strings.Contains(err.Error(), "the server could not find the requested resource") {
			checkErrors = append(checkErrors, "Gateway CRD not installed")
		} else {
			// Might be permission error - assume CRD exists
			result.GatewayCRDExists = true
			checkErrors = append(checkErrors, fmt.Sprintf("gateway list warning: %v", err))
		}
	}

	// Check if HTTPRoute CRD exists by trying to list httproutes
	httpRouteGVR := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	_, err = client.Resource(httpRouteGVR).List(ctx, metav1.ListOptions{Limit: 1})
	if err == nil {
		result.HTTPRouteCRDExists = true
	} else {
		// Check if it's a "not found" error (CRD not installed) vs permission error
		if strings.Contains(err.Error(), "the server could not find the requested resource") {
			checkErrors = append(checkErrors, "HTTPRoute CRD not installed")
		} else {
			// Might be permission error - assume CRD exists
			result.HTTPRouteCRDExists = true
			checkErrors = append(checkErrors, fmt.Sprintf("httproute list warning: %v", err))
		}
	}

	// Build error message if prerequisites are not met
	var missing []string
	if !result.NamespaceExists {
		missing = append(missing, fmt.Sprintf("namespace '%s' does not exist", FastGatewayNamespace))
	}
	if !result.GatewayCRDExists {
		missing = append(missing, "Gateway API CRD (Gateway) is not installed")
	}
	if !result.HTTPRouteCRDExists {
		missing = append(missing, "Gateway API CRD (HTTPRoute) is not installed")
	}

	if len(missing) > 0 {
		result.ErrorMessage = fmt.Sprintf("Prerequisites not met: %s. Please install Gateway API CRDs and create the '%s' namespace before onboarding.",
			joinStrings(missing, "; "), FastGatewayNamespace)
	}

	return result, nil
}

// joinStrings joins strings with a separator
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

// EnvoyGatewayControllerName is the controller name for Envoy Gateway
const EnvoyGatewayControllerName = "gateway.envoyproxy.io/gatewayclass-controller"

// EnvoyGatewayNamespace is the namespace where EnvoyProxy configs are created
const EnvoyGatewayNamespace = "envoy-gateway-system"

// GatewayClassConfig represents GatewayClass configuration
type GatewayClassConfig struct {
	Name              string
	ControllerName    string
	ParametersRefName string // Reference to EnvoyProxy config
}

// EnvoyProxyConfig represents EnvoyProxy configuration
type EnvoyProxyConfig struct {
	Name                  string
	Namespace             string
	ServiceType           string // LoadBalancer or ClusterIP
	Annotations           map[string]string
	ExternalTrafficPolicy string                           // Cluster or Local (only for LoadBalancer)
	LoadBalancerClass     string                           // optional (only for LoadBalancer)
	PodAnnotations        map[string]string                // envoyDeployment.pod.annotations
	ContainerResources    *models.ContainerResourcesConfig // envoyDeployment.container.resources
	ScalingConfig         *models.ScalingConfig            // envoyDeployment.replicas or envoyHpa
	MergeGateways         bool                             // spec.mergeGateways

	// Telemetry — see spec.telemetry on EnvoyProxy CRD.
	TelemetryAccessLog *models.TelemetryAccessLogConfig
	TelemetryTracing   *models.TelemetryTracingConfig
	TelemetryMetrics   *models.TelemetryMetricsConfig

	// GatewayClassName is the name of the GatewayClass that points at this EnvoyProxy
	// via parametersRef. Used by BuildPodPlacement to auto-fill the topology-spread
	// labelSelector to match data-plane pod labels EG applies.
	GatewayClassName string

	// PodPlacement, PDBConfig, DeploymentStrategy — see spec.provider.kubernetes on EnvoyProxy CRD.
	PodPlacement       *models.PodPlacementConfig
	PDBConfig          *models.PDBConfig
	DeploymentStrategy *models.DeploymentStrategyConfig
}

// BuildGatewayClassObject builds a GatewayClass unstructured object from the given config.
func BuildGatewayClassObject(config *GatewayClassConfig) *unstructured.Unstructured {
	spec := map[string]interface{}{
		"controllerName": config.ControllerName,
	}

	// Add parametersRef if EnvoyProxy name is provided
	if config.ParametersRefName != "" {
		spec["parametersRef"] = map[string]interface{}{
			"group":     "gateway.envoyproxy.io",
			"kind":      "EnvoyProxy",
			"name":      config.ParametersRefName,
			"namespace": EnvoyGatewayNamespace,
		}
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "GatewayClass",
			"metadata": map[string]interface{}{
				"name": config.Name,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
				},
			},
			"spec": spec,
		},
	}
}

// CreateGatewayClass creates a GatewayClass resource in Kubernetes
func (s *KubernetesService) CreateGatewayClass(ctx context.Context, projectID uuid.UUID, config *GatewayClassConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	gatewayClass := BuildGatewayClassObject(config)

	_, err = client.Resource(gvr).Create(ctx, gatewayClass, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create gatewayclass: %w", err)
	}

	return nil
}

// DeleteGatewayClass deletes a GatewayClass resource from Kubernetes
func (s *KubernetesService) DeleteGatewayClass(ctx context.Context, projectID uuid.UUID, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	err = client.Resource(gvr).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete gatewayclass: %w", err)
	}

	return nil
}

// BuildEnvoyProxyObject builds an EnvoyProxy unstructured object from the given config.
func BuildEnvoyProxyObject(config *EnvoyProxyConfig) *unstructured.Unstructured {
	// Build kubernetes provider spec
	envoyService := map[string]interface{}{
		"type": config.ServiceType,
	}

	// Add annotations if provided
	if len(config.Annotations) > 0 {
		annotations := make(map[string]interface{})
		for k, v := range config.Annotations {
			annotations[k] = v
		}
		envoyService["annotations"] = annotations
	}

	// Add externalTrafficPolicy for LoadBalancer (if specified)
	if config.ServiceType == "LoadBalancer" && config.ExternalTrafficPolicy != "" {
		envoyService["externalTrafficPolicy"] = config.ExternalTrafficPolicy
	}

	// Add loadBalancerClass for LoadBalancer (if specified)
	if config.ServiceType == "LoadBalancer" && config.LoadBalancerClass != "" {
		envoyService["loadBalancerClass"] = config.LoadBalancerClass
	}

	// Build envoyDeployment spec if any deployment fields are set
	var envoyDeployment map[string]interface{}
	hasDeploymentConfig := len(config.PodAnnotations) > 0 || config.ContainerResources != nil ||
		(config.ScalingConfig != nil && config.ScalingConfig.Type == "fixed") ||
		config.PodPlacement != nil || config.DeploymentStrategy != nil

	if hasDeploymentConfig {
		envoyDeployment = make(map[string]interface{})

		podSubMap := map[string]interface{}{}
		if len(config.PodAnnotations) > 0 {
			podAnnotations := make(map[string]interface{})
			for k, v := range config.PodAnnotations {
				podAnnotations[k] = v
			}
			podSubMap["annotations"] = podAnnotations
		}
		if pp := BuildPodPlacement(config.PodPlacement, config.GatewayClassName); pp != nil {
			for k, v := range pp {
				podSubMap[k] = v
			}
		}
		if len(podSubMap) > 0 {
			envoyDeployment["pod"] = podSubMap
		}

		if config.ContainerResources != nil {
			resources := make(map[string]interface{})
			if config.ContainerResources.Requests != nil {
				requests := make(map[string]interface{})
				if config.ContainerResources.Requests.CPU != "" {
					requests["cpu"] = config.ContainerResources.Requests.CPU
				}
				if config.ContainerResources.Requests.Memory != "" {
					requests["memory"] = config.ContainerResources.Requests.Memory
				}
				if len(requests) > 0 {
					resources["requests"] = requests
				}
			}
			if config.ContainerResources.Limits != nil {
				limits := make(map[string]interface{})
				if config.ContainerResources.Limits.CPU != "" {
					limits["cpu"] = config.ContainerResources.Limits.CPU
				}
				if config.ContainerResources.Limits.Memory != "" {
					limits["memory"] = config.ContainerResources.Limits.Memory
				}
				if len(limits) > 0 {
					resources["limits"] = limits
				}
			}
			if len(resources) > 0 {
				envoyDeployment["container"] = map[string]interface{}{
					"resources": resources,
				}
			}
		}

		if config.ScalingConfig != nil && config.ScalingConfig.Type == "fixed" && config.ScalingConfig.Replicas != nil {
			envoyDeployment["replicas"] = *config.ScalingConfig.Replicas
		}

		if strat := BuildStrategy(config.DeploymentStrategy); strat != nil {
			envoyDeployment["strategy"] = strat
		}
	}

	// Build envoyHpa spec
	var envoyHpa map[string]interface{}
	if config.ScalingConfig != nil && config.ScalingConfig.Type == "hpa" {
		envoyHpa = make(map[string]interface{})
		if config.ScalingConfig.MinReplicas != nil {
			envoyHpa["minReplicas"] = *config.ScalingConfig.MinReplicas
		}
		if config.ScalingConfig.MaxReplicas != nil {
			envoyHpa["maxReplicas"] = *config.ScalingConfig.MaxReplicas
		}
	}

	// Build kubernetes provider spec
	k8sSpec := map[string]interface{}{
		"envoyService": envoyService,
	}
	if envoyDeployment != nil {
		k8sSpec["envoyDeployment"] = envoyDeployment
	}
	if envoyHpa != nil {
		k8sSpec["envoyHpa"] = envoyHpa
	}
	if pdb := BuildPDB(config.PDBConfig); pdb != nil {
		k8sSpec["envoyPDB"] = pdb
	}

	spec := map[string]interface{}{
		"provider": map[string]interface{}{
			"type":       "Kubernetes",
			"kubernetes": k8sSpec,
		},
	}

	if config.MergeGateways {
		spec["mergeGateways"] = true
	}

	telemetry := map[string]interface{}{}
	if al := BuildAccessLog(config.TelemetryAccessLog); al != nil {
		telemetry["accessLog"] = al
	}
	if tr := BuildTracing(config.TelemetryTracing); tr != nil {
		telemetry["tracing"] = tr
	}
	if me := BuildMetrics(config.TelemetryMetrics); me != nil {
		telemetry["metrics"] = me
	}
	if len(telemetry) > 0 {
		spec["telemetry"] = telemetry
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "EnvoyProxy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
				},
			},
			"spec": spec,
		},
	}
}

// BuildAccessLog converts the stored TelemetryAccessLogConfig into the EG CRD shape
// (spec.telemetry.accessLog). Returns a fully-formed map ready to embed.
func BuildAccessLog(cfg *models.TelemetryAccessLogConfig) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	format := buildAccessLogFormat(cfg.Format)
	sinks := buildAccessLogSinks(cfg.Sink)

	setting := map[string]interface{}{
		"format": format,
		"sinks":  sinks,
	}
	return map[string]interface{}{
		"settings": []interface{}{setting},
	}
}

func buildAccessLogFormat(f models.TelemetryAccessLogFormat) map[string]interface{} {
	switch f.Type {
	case "json":
		out := map[string]interface{}{"type": "JSON"}
		jsonMap := make(map[string]interface{}, len(f.JSON))
		for k, v := range f.JSON {
			jsonMap[k] = v
		}
		out["json"] = jsonMap
		return out
	case "disabled":
		return map[string]interface{}{"type": "Disabled"}
	default: // "text"
		return map[string]interface{}{
			"type": "Text",
			"text": f.Text,
		}
	}
}

func buildAccessLogSinks(s models.TelemetryAccessLogSink) []interface{} {
	switch s.Type {
	case "otel":
		if s.OTel == nil {
			return nil
		}
		return []interface{}{
			map[string]interface{}{
				"type": "OpenTelemetry",
				"openTelemetry": map[string]interface{}{
					"backendRefs": []interface{}{
						map[string]interface{}{
							"name":      s.OTel.Service,
							"namespace": s.OTel.Namespace,
							"port":      s.OTel.Port,
						},
					},
				},
			},
		}
	default: // "file"
		path := "/dev/stdout"
		if s.File != nil && s.File.Path != "" {
			path = s.File.Path
		}
		return []interface{}{
			map[string]interface{}{
				"type": "File",
				"file": map[string]interface{}{
					"path": path,
				},
			},
		}
	}
}

// BuildTracing converts the stored TelemetryTracingConfig into the EG CRD shape
// (spec.telemetry.tracing).
func BuildTracing(cfg *models.TelemetryTracingConfig) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	out := map[string]interface{}{
		"samplingRate": cfg.SamplingRate,
		"provider": map[string]interface{}{
			"backendRefs": []interface{}{
				map[string]interface{}{
					"name":      cfg.Provider.Service,
					"namespace": cfg.Provider.Namespace,
					"port":      cfg.Provider.Port,
				},
			},
		},
	}
	// EG CRD shape: customTags is a map keyed by tag name, NOT an array.
	// The DB still stores it as an array (with explicit Tag field) — translate here.
	if len(cfg.CustomTags) > 0 {
		tags := make(map[string]interface{}, len(cfg.CustomTags))
		for _, t := range cfg.CustomTags {
			tags[t.Tag] = buildTracingTag(t)
		}
		out["customTags"] = tags
	}
	return out
}

func buildTracingTag(t models.TelemetryTracingTag) map[string]interface{} {
	switch t.Type {
	case "requestHeader":
		rh := map[string]interface{}{"name": t.Header}
		if t.DefaultValue != "" {
			rh["defaultValue"] = t.DefaultValue
		}
		return map[string]interface{}{
			"type":          "RequestHeader",
			"requestHeader": rh,
		}
	default: // "literal"
		return map[string]interface{}{
			"type":    "Literal",
			"literal": map[string]interface{}{"value": t.Value},
		}
	}
}

// BuildMetrics converts the stored TelemetryMetricsConfig into the EG CRD shape
// (spec.telemetry.metrics).
func BuildMetrics(cfg *models.TelemetryMetricsConfig) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	out := map[string]interface{}{}
	if cfg.Prometheus != nil {
		out["prometheus"] = map[string]interface{}{"disable": cfg.Prometheus.Disable}
	}
	if cfg.EnableVirtualHostStats {
		out["enableVirtualHostStats"] = true
	}
	if cfg.EnablePerEndpointStats {
		out["enablePerEndpointStats"] = true
	}
	if len(cfg.Sinks) > 0 {
		sinks := make([]interface{}, 0, len(cfg.Sinks))
		for _, s := range cfg.Sinks {
			sinks = append(sinks, map[string]interface{}{
				"type": "OpenTelemetry",
				"openTelemetry": map[string]interface{}{
					"backendRefs": []interface{}{
						map[string]interface{}{
							"name":      s.Service,
							"namespace": s.Namespace,
							"port":      s.Port,
						},
					},
				},
			})
		}
		out["sinks"] = sinks
	}
	return out
}

// BuildPodPlacement converts the stored PodPlacementConfig into the keys to merge
// into envoyDeployment.pod (nodeSelector / tolerations / topologySpreadConstraints / priorityClassName).
// gatewayClassName is used to auto-fill the topology-spread labelSelector when the
// stored config doesn't override it.
func BuildPodPlacement(cfg *models.PodPlacementConfig, gatewayClassName string) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	out := map[string]interface{}{}
	if len(cfg.NodeSelector) > 0 {
		ns := make(map[string]interface{}, len(cfg.NodeSelector))
		for k, v := range cfg.NodeSelector {
			ns[k] = v
		}
		out["nodeSelector"] = ns
	}
	if len(cfg.Tolerations) > 0 {
		out["tolerations"] = buildTolerations(cfg.Tolerations)
	}
	if len(cfg.TopologySpreadConstraints) > 0 {
		out["topologySpreadConstraints"] = buildTopologySpreadConstraints(cfg.TopologySpreadConstraints, gatewayClassName)
	}
	// NOTE: priorityClassName is intentionally NOT emitted. The EG EnvoyProxy CRD
	// does not expose this field anywhere — K8s admission silently drops it on the
	// pod spec. We keep PriorityClassName in the model for forward-compat in case
	// EG adds support, but until then the value has no effect on the cluster.
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildTolerations(tols []models.TolerationConfig) []interface{} {
	out := make([]interface{}, 0, len(tols))
	for _, t := range tols {
		row := map[string]interface{}{}
		if t.Key != "" {
			row["key"] = t.Key
		}
		if t.Operator != "" {
			row["operator"] = t.Operator
		}
		if t.Value != "" {
			row["value"] = t.Value
		}
		if t.Effect != "" {
			row["effect"] = t.Effect
		}
		if t.TolerationSeconds != nil {
			row["tolerationSeconds"] = *t.TolerationSeconds
		}
		out = append(out, row)
	}
	return out
}

func buildTopologySpreadConstraints(cs []models.TopologySpreadConstraintConfig, gatewayClassName string) []interface{} {
	out := make([]interface{}, 0, len(cs))
	for _, c := range cs {
		row := map[string]interface{}{
			"maxSkew":           c.MaxSkew,
			"topologyKey":       c.TopologyKey,
			"whenUnsatisfiable": c.WhenUnsatisfiable,
			"labelSelector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"gateway.envoyproxy.io/owning-gatewayclass": gatewayClassName,
				},
			},
		}
		out = append(out, row)
	}
	return out
}

// BuildPDB converts the stored PDBConfig into the EG CRD shape (envoyPDB).
// Amount is parsed as int when it doesn't end with "%"; otherwise emitted as a string
// so K8s consumers see an IntOrString-compatible YAML value.
func BuildPDB(cfg *models.PDBConfig) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	val := parseIntOrPercent(cfg.Amount)
	switch cfg.Kind {
	case "maxUnavailable":
		return map[string]interface{}{"maxUnavailable": val}
	default: // "minAvailable"
		return map[string]interface{}{"minAvailable": val}
	}
}

// parseIntOrPercent returns an int when s parses cleanly as a non-negative integer
// (with no trailing characters); otherwise returns the string verbatim. K8s YAML
// consumers treat both correctly via the IntOrString type. Validation of the
// caller-side allowed range happens in the validators, not here.
func parseIntOrPercent(s string) interface{} {
	if len(s) > 0 && s[len(s)-1] == '%' {
		return s
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return n
	}
	return s
}

// BuildStrategy converts the stored DeploymentStrategyConfig into the EG CRD shape
// (envoyDeployment.strategy). Returns nil when cfg is nil. When Type is RollingUpdate
// and no overrides are set, omits the rollingUpdate sub-block so K8s defaults apply.
func BuildStrategy(cfg *models.DeploymentStrategyConfig) map[string]interface{} {
	if cfg == nil || cfg.Type == "" {
		return nil
	}
	out := map[string]interface{}{
		"type": cfg.Type,
	}
	if cfg.Type == "RollingUpdate" && cfg.RollingUpdate != nil {
		ru := map[string]interface{}{}
		if cfg.RollingUpdate.MaxSurge != "" {
			ru["maxSurge"] = parseIntOrPercent(cfg.RollingUpdate.MaxSurge)
		}
		if cfg.RollingUpdate.MaxUnavailable != "" {
			ru["maxUnavailable"] = parseIntOrPercent(cfg.RollingUpdate.MaxUnavailable)
		}
		if len(ru) > 0 {
			out["rollingUpdate"] = ru
		}
	}
	return out
}

// CreateEnvoyProxy creates an EnvoyProxy resource in Kubernetes
func (s *KubernetesService) CreateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *EnvoyProxyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyproxies",
	}

	envoyProxy := BuildEnvoyProxyObject(config)

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, envoyProxy, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create envoyproxy: %w", err)
	}

	return nil
}

// UpdateEnvoyProxy updates an EnvoyProxy resource in Kubernetes
func (s *KubernetesService) UpdateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *EnvoyProxyConfig) error {
	// Delete and recreate for simplicity
	_ = s.DeleteEnvoyProxy(ctx, projectID, config.Namespace, config.Name)
	return s.CreateEnvoyProxy(ctx, projectID, config)
}

// DeleteEnvoyProxy deletes an EnvoyProxy resource from Kubernetes
func (s *KubernetesService) DeleteEnvoyProxy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyproxies",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete envoyproxy: %w", err)
	}

	return nil
}

// ValidateEnvoyGatewayInstalled checks if Envoy Gateway controller is installed
// by looking for existing GatewayClasses with the Envoy controller name
// or by checking for deployments in envoy-gateway-system namespace
func (s *KubernetesService) ValidateEnvoyGatewayInstalled(ctx context.Context, projectID uuid.UUID) (bool, string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return false, "", err
	}

	// Method 1: Check for GatewayClasses with Envoy controller
	gcGVR := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	list, err := client.Resource(gcGVR).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, item := range list.Items {
			spec, _, _ := unstructured.NestedMap(item.Object, "spec")
			if controllerName, ok := spec["controllerName"].(string); ok {
				if controllerName == EnvoyGatewayControllerName {
					return true, "Envoy Gateway controller found via existing GatewayClass", nil
				}
			}
		}
	}

	// Method 2: Check for any deployments in envoy-gateway-system namespace
	deployGVR := schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}

	deployList, err := client.Resource(deployGVR).Namespace("envoy-gateway-system").List(ctx, metav1.ListOptions{})
	if err == nil && len(deployList.Items) > 0 {
		return true, "Envoy Gateway controller found via deployment in envoy-gateway-system namespace", nil
	}

	// Method 3: Check for envoy-gateway namespace existence as fallback
	nsGVR := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	_, err = client.Resource(nsGVR).Get(ctx, "envoy-gateway-system", metav1.GetOptions{})
	if err == nil {
		// Namespace exists, check if there are any pods running
		podGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "pods",
		}
		podList, err := client.Resource(podGVR).Namespace("envoy-gateway-system").List(ctx, metav1.ListOptions{})
		if err == nil && len(podList.Items) > 0 {
			return true, "Envoy Gateway controller found via pods in envoy-gateway-system namespace", nil
		}
	}

	return false, "Envoy Gateway controller not found. Please install Envoy Gateway first: https://gateway.envoyproxy.io/docs/tasks/quickstart/", nil
}

// ReferenceGrantConfig represents ReferenceGrant configuration
type ReferenceGrantConfig struct {
	Name           string   // Name of the ReferenceGrant
	FromNamespaces []string // Namespaces where Gateways and routes are deployed
	ToNamespace    string   // Namespace where the referenced resources reside
	ToKinds        []string // Core/v1 kinds permitted as targets (e.g. "Service", "Secret"). Empty = both.
}

// CreateReferenceGrant creates a ReferenceGrant allowing resources from multiple namespaces to reference services and/or secrets in the target namespace
func (s *KubernetesService) CreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *ReferenceGrantConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1beta1",
		Resource: "referencegrants",
	}

	// Build "from" entries for each source namespace and resource kind
	type fromEntry struct {
		group string
		kind  string
	}
	kinds := []fromEntry{
		{"gateway.networking.k8s.io", "HTTPRoute"},
		{"gateway.networking.k8s.io", "GRPCRoute"},
		{"gateway.envoyproxy.io", "SecurityPolicy"},
		{"gateway.envoyproxy.io", "EnvoyExtensionPolicy"},
		{"gateway.networking.k8s.io", "Gateway"},
	}

	var fromList []interface{}
	for _, ns := range config.FromNamespaces {
		for _, k := range kinds {
			fromList = append(fromList, map[string]interface{}{
				"group":     k.group,
				"kind":      k.kind,
				"namespace": ns,
			})
		}
	}

	toKinds := config.ToKinds
	if len(toKinds) == 0 {
		toKinds = []string{"Service", "Secret"}
	}
	toList := make([]interface{}, 0, len(toKinds))
	for _, k := range toKinds {
		toList = append(toList, map[string]interface{}{
			"group": "",
			"kind":  k,
		})
	}

	referenceGrant := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1beta1",
			"kind":       "ReferenceGrant",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.ToNamespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
				},
			},
			"spec": map[string]interface{}{
				"from": fromList,
				"to":   toList,
			},
		},
	}

	_, err = client.Resource(gvr).Namespace(config.ToNamespace).Create(ctx, referenceGrant, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create referencegrant: %w", err)
	}

	return nil
}

// DeleteReferenceGrant deletes a ReferenceGrant from Kubernetes
func (s *KubernetesService) DeleteReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1beta1",
		Resource: "referencegrants",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete referencegrant: %w", err)
	}

	return nil
}

// GetReferenceGrant gets a ReferenceGrant from Kubernetes
func (s *KubernetesService) GetReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) (*unstructured.Unstructured, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1beta1",
		Resource: "referencegrants",
	}

	rg, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get referencegrant: %w", err)
	}

	return rg, nil
}

// ReferenceGrantExists checks if a ReferenceGrant exists in Kubernetes
func (s *KubernetesService) ReferenceGrantExists(ctx context.Context, projectID uuid.UUID, namespace, name string) (bool, error) {
	_, err := s.GetReferenceGrant(ctx, projectID, namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RecreateReferenceGrant deletes and recreates a ReferenceGrant with updated config.
// No-op on delete failure if the grant doesn't exist.
func (s *KubernetesService) RecreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *ReferenceGrantConfig) error {
	// Delete existing (ignore not-found errors)
	_ = s.DeleteReferenceGrant(ctx, projectID, config.ToNamespace, config.Name)

	return s.CreateReferenceGrant(ctx, projectID, config)
}

// ==================== HTTPRouteFilter (Direct Response) ====================

// getHTTPRouteFilterGVR returns the GroupVersionResource for HTTPRouteFilter
func getHTTPRouteFilterGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "httproutefilters",
	}
}

// BuildHTTPRouteFilter builds an HTTPRouteFilter resource for Direct Response
func BuildHTTPRouteFilter(config *HTTPRouteFilterConfig) *unstructured.Unstructured {
	spec := map[string]interface{}{}

	if config.DirectResponse != nil {
		dr := map[string]interface{}{
			"statusCode": config.DirectResponse.StatusCode,
		}

		if config.DirectResponse.ContentType != "" {
			dr["contentType"] = config.DirectResponse.ContentType
		}

		if config.DirectResponse.Body != nil {
			body := map[string]interface{}{
				"type": config.DirectResponse.Body.Type,
			}
			if config.DirectResponse.Body.Type == "Inline" && config.DirectResponse.Body.Inline != "" {
				body["inline"] = config.DirectResponse.Body.Inline
			}
			if config.DirectResponse.Body.Type == "ValueRef" && config.DirectResponse.Body.ValueRef != nil {
				body["valueRef"] = map[string]interface{}{
					"group": config.DirectResponse.Body.ValueRef.Group,
					"kind":  config.DirectResponse.Body.ValueRef.Kind,
					"name":  config.DirectResponse.Body.ValueRef.Name,
				}
			}
			dr["body"] = body
		}

		spec["directResponse"] = dr
	}

	httpRouteFilter := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "HTTPRouteFilter",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
					"fastgateway.dev/gateway-id":   config.GatewayID,
					"fastgateway.dev/route-id":     config.RouteID,
				},
			},
			"spec": spec,
		},
	}

	return httpRouteFilter
}

// ApplyHTTPRouteFilter creates or updates an HTTPRouteFilter in Kubernetes
func (s *KubernetesService) ApplyHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, config *HTTPRouteFilterConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getHTTPRouteFilterGVR()
	httpRouteFilter := BuildHTTPRouteFilter(config)

	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, httpRouteFilter, metav1.CreateOptions{})
			return err
		}
		return err
	}

	httpRouteFilter.SetResourceVersion(existing.GetResourceVersion())
	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, httpRouteFilter, metav1.UpdateOptions{})
	return err
}

// DeleteHTTPRouteFilter deletes an HTTPRouteFilter from Kubernetes
func (s *KubernetesService) DeleteHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getHTTPRouteFilterGVR()

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete HTTPRouteFilter: %w", err)
	}
	return nil
}

// ==================== ConfigMap (Direct Response body) ====================

// getConfigMapGVR returns the GroupVersionResource for ConfigMap
func getConfigMapGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}
}

// BuildDirectResponseConfigMap builds a ConfigMap for Direct Response body
func BuildDirectResponseConfigMap(config *DirectResponseConfigMapConfig) *unstructured.Unstructured {
	configMap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
					"fastgateway.dev/gateway-id":   config.GatewayID,
					"fastgateway.dev/route-id":     config.RouteID,
				},
			},
			"data": map[string]interface{}{
				"response.body": config.BodyContent,
			},
		},
	}

	return configMap
}

// ApplyDirectResponseConfigMap creates or updates a ConfigMap for Direct Response in Kubernetes
func (s *KubernetesService) ApplyDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, config *DirectResponseConfigMapConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getConfigMapGVR()
	configMap := BuildDirectResponseConfigMap(config)

	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, configMap, metav1.CreateOptions{})
			return err
		}
		return err
	}

	configMap.SetResourceVersion(existing.GetResourceVersion())
	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, configMap, metav1.UpdateOptions{})
	return err
}

// DeleteDirectResponseConfigMap deletes a ConfigMap from Kubernetes (only FastGateway-managed ones)
func (s *KubernetesService) DeleteDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getConfigMapGVR()

	// Only delete if it's managed by FastGateway
	existing, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	labels := existing.GetLabels()
	if labels == nil || labels["app.kubernetes.io/managed-by"] != "fastgateway" {
		// Not managed by FastGateway, don't delete
		return nil
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete ConfigMap: %w", err)
	}
	return nil
}

// getClientTrafficPolicyGVR returns the GroupVersionResource for Envoy Gateway ClientTrafficPolicy
func getClientTrafficPolicyGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "clienttrafficpolicies",
	}
}

// BuildClientTrafficPolicy builds an Envoy Gateway ClientTrafficPolicy from config
func BuildClientTrafficPolicy(config *ClientTrafficPolicyConfig) *unstructured.Unstructured {
	if config == nil {
		return nil
	}

	spec := map[string]interface{}{
		"targetRef": map[string]interface{}{
			"group": config.TargetRef.Group,
			"kind":  config.TargetRef.Kind,
			"name":  config.TargetRef.Name,
		},
	}

	hasConfig := false

	// Add TCP keepalive configuration if present
	if config.TCPKeepalive != nil {
		tcpKeepalive := map[string]interface{}{}
		if config.TCPKeepalive.Probes != nil {
			tcpKeepalive["probes"] = *config.TCPKeepalive.Probes
		}
		if config.TCPKeepalive.IdleTime != nil && *config.TCPKeepalive.IdleTime != "" {
			tcpKeepalive["idleTime"] = *config.TCPKeepalive.IdleTime
		}
		if config.TCPKeepalive.Interval != nil && *config.TCPKeepalive.Interval != "" {
			tcpKeepalive["interval"] = *config.TCPKeepalive.Interval
		}
		if len(tcpKeepalive) > 0 {
			spec["tcpKeepalive"] = tcpKeepalive
			hasConfig = true
		}
	}

	// Add PROXY protocol configuration if enabled
	if config.EnableProxyProtocol {
		spec["enableProxyProtocol"] = true
		hasConfig = true
	}

	// Add connection configuration if present
	if config.Connection != nil {
		connection := map[string]interface{}{}
		if config.Connection.BufferLimit != nil && *config.Connection.BufferLimit != "" {
			connection["bufferLimit"] = *config.Connection.BufferLimit
		}
		// Connection limit settings go under connectionLimit
		connectionLimit := map[string]interface{}{}
		if config.Connection.MaxConnections != nil {
			connectionLimit["value"] = *config.Connection.MaxConnections
		}
		if config.Connection.CloseDelay != nil && *config.Connection.CloseDelay != "" {
			connectionLimit["closeDelay"] = *config.Connection.CloseDelay
		}
		if config.Connection.MaxConnectionDuration != nil && *config.Connection.MaxConnectionDuration != "" {
			connectionLimit["maxConnectionDuration"] = *config.Connection.MaxConnectionDuration
		}
		if config.Connection.MaxRequestsPerConnection != nil {
			connectionLimit["maxRequestsPerConnection"] = *config.Connection.MaxRequestsPerConnection
		}
		if len(connectionLimit) > 0 {
			connection["connectionLimit"] = connectionLimit
		}
		if len(connection) > 0 {
			spec["connection"] = connection
			hasConfig = true
		}
	}

	// Add timeout configuration if present
	if config.Timeout != nil && config.Timeout.HTTP != nil {
		timeout := map[string]interface{}{}
		httpTimeout := map[string]interface{}{}
		if config.Timeout.HTTP.RequestReceivedTimeout != nil && *config.Timeout.HTTP.RequestReceivedTimeout != "" {
			httpTimeout["requestReceivedTimeout"] = *config.Timeout.HTTP.RequestReceivedTimeout
		}
		if config.Timeout.HTTP.IdleTimeout != nil && *config.Timeout.HTTP.IdleTimeout != "" {
			httpTimeout["idleTimeout"] = *config.Timeout.HTTP.IdleTimeout
		}
		if len(httpTimeout) > 0 {
			timeout["http"] = httpTimeout
			spec["timeout"] = timeout
			hasConfig = true
		}
	}

	// Add HTTP/3 configuration if enabled
	if config.HTTP3 != nil && config.HTTP3.Enabled {
		spec["http3"] = map[string]interface{}{}
		hasConfig = true
	}

	// Add client IP detection configuration if present
	if config.ClientIPDetection != nil {
		clientIPDetection := map[string]interface{}{}
		if config.ClientIPDetection.XForwardedFor != nil {
			clientIPDetection["xForwardedFor"] = map[string]interface{}{
				"numTrustedHops": config.ClientIPDetection.XForwardedFor.NumTrustedHops,
			}
			hasConfig = true
		}
		if config.ClientIPDetection.CustomHeader != nil {
			customHeader := map[string]interface{}{
				"name": config.ClientIPDetection.CustomHeader.Name,
			}
			if config.ClientIPDetection.CustomHeader.FailClosed {
				customHeader["failClosed"] = true
			}
			clientIPDetection["customHeader"] = customHeader
			hasConfig = true
		}
		if len(clientIPDetection) > 0 {
			spec["clientIPDetection"] = clientIPDetection
		}
	}

	// Add TLS configuration if present
	if config.TLS != nil {
		tls := map[string]interface{}{}
		if config.TLS.MinVersion != nil && *config.TLS.MinVersion != "" {
			tls["minVersion"] = convertTLSVersionToK8s(*config.TLS.MinVersion)
		}
		if config.TLS.MaxVersion != nil && *config.TLS.MaxVersion != "" {
			tls["maxVersion"] = convertTLSVersionToK8s(*config.TLS.MaxVersion)
		}
		if len(config.TLS.Ciphers) > 0 {
			tls["ciphers"] = config.TLS.Ciphers
		}
		if len(config.TLS.ECDHCurves) > 0 {
			tls["ecdhCurves"] = config.TLS.ECDHCurves
		}
		if len(config.TLS.SignatureAlgorithms) > 0 {
			tls["signatureAlgorithms"] = config.TLS.SignatureAlgorithms
		}
		if len(tls) > 0 {
			spec["tls"] = tls
			hasConfig = true
		}
	}

	// Add mTLS client validation configuration if present
	if config.ClientValidation != nil {
		// Get or create TLS spec section
		tlsSpec, ok := spec["tls"].(map[string]interface{})
		if !ok {
			tlsSpec = map[string]interface{}{}
		}

		clientValidation := map[string]interface{}{
			"optional": config.ClientValidation.Optional,
		}

		// Add CA certificate references
		if len(config.ClientValidation.CACertificateRefs) > 0 {
			caRefs := make([]interface{}, len(config.ClientValidation.CACertificateRefs))
			for i, ref := range config.ClientValidation.CACertificateRefs {
				caRefs[i] = map[string]interface{}{
					"group": ref.Group,
					"kind":  ref.Kind,
					"name":  ref.Name,
				}
			}
			clientValidation["caCertificateRefs"] = caRefs
		}

		// Add SAN matchers (subjectAltNames in CRD)
		if len(config.ClientValidation.SANMatchers) > 0 {
			sanMatchers := make([]interface{}, len(config.ClientValidation.SANMatchers))
			for i, san := range config.ClientValidation.SANMatchers {
				sanMatchers[i] = map[string]interface{}{
					"type": san.Type,
					"match": map[string]interface{}{
						"exact": san.Match,
					},
				}
			}
			clientValidation["subjectAltNames"] = sanMatchers
		}

		// Add certificate hashes
		if len(config.ClientValidation.CertificateHashes) > 0 {
			clientValidation["certificateHashes"] = config.ClientValidation.CertificateHashes
		}

		tlsSpec["clientValidation"] = clientValidation
		spec["tls"] = tlsSpec
		hasConfig = true
	}

	// Add headers configuration (XFCC) if present
	if config.Headers != nil && config.Headers.XForwardedClientCert != nil {
		xfcc := config.Headers.XForwardedClientCert
		headers := map[string]interface{}{
			"xForwardedClientCert": map[string]interface{}{
				"mode":             xfcc.Mode,
				"certDetailsToAdd": xfcc.CertDetailsToAdd,
			},
		}
		spec["headers"] = headers
		hasConfig = true
	}

	// Only create if there's actual config beyond targetRef
	if !hasConfig {
		return nil
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.envoyproxy.io/v1alpha1",
			"kind":       "ClientTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
					"fastgateway.dev/gateway-id":   config.GatewayID,
				},
			},
			"spec": spec,
		},
	}
}

// CreateClientTrafficPolicy creates an Envoy Gateway ClientTrafficPolicy resource in Kubernetes.
// If the resource already exists, it falls back to update.
func (s *KubernetesService) CreateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *ClientTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	clientTrafficPolicy := BuildClientTrafficPolicy(config)
	if clientTrafficPolicy == nil {
		// No features configured, nothing to create
		return nil
	}

	gvr := getClientTrafficPolicyGVR()

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, clientTrafficPolicy, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Resource already exists, fall back to update
			return s.UpdateClientTrafficPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create clienttrafficpolicy: %w", err)
	}

	return nil
}

// UpdateClientTrafficPolicy updates an Envoy Gateway ClientTrafficPolicy resource in Kubernetes
func (s *KubernetesService) UpdateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *ClientTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getClientTrafficPolicyGVR()

	clientTrafficPolicy := BuildClientTrafficPolicy(config)
	if clientTrafficPolicy == nil {
		// No features configured, delete existing if any
		return s.DeleteClientTrafficPolicy(ctx, projectID, config.Namespace, config.Name)
	}

	// Get the existing ClientTrafficPolicy to preserve resourceVersion
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		// If not found, create it
		if strings.Contains(err.Error(), "not found") {
			return s.CreateClientTrafficPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to get existing clienttrafficpolicy: %w", err)
	}

	// Preserve the resourceVersion from existing object
	clientTrafficPolicy.SetResourceVersion(existing.GetResourceVersion())
	clientTrafficPolicy.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, clientTrafficPolicy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update clienttrafficpolicy: %w", err)
	}

	return nil
}

// DeleteClientTrafficPolicy deletes an Envoy Gateway ClientTrafficPolicy resource from Kubernetes
func (s *KubernetesService) DeleteClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getClientTrafficPolicyGVR()

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		// Ignore not found errors
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete clienttrafficpolicy: %w", err)
	}

	return nil
}

// convertTLSVersionToK8s converts API TLS version format to K8s CRD format
// API accepts: "TLS1.0", "TLS1.1", "TLS1.2", "TLS1.3", "TLSv1.0", "TLSv1.1", "TLSv1.2", "TLSv1.3", "Auto", ""
// K8s CRD expects: "Auto", "1.0", "1.1", "1.2", "1.3"
func convertTLSVersionToK8s(version string) string {
	switch version {
	case "TLS1.0", "TLSv1.0":
		return "1.0"
	case "TLS1.1", "TLSv1.1":
		return "1.1"
	case "TLS1.2", "TLSv1.2":
		return "1.2"
	case "TLS1.3", "TLSv1.3":
		return "1.3"
	case "Auto", "":
		return "Auto"
	default:
		// If already in K8s format or unknown, return as-is
		return version
	}
}

// =============================================================================
// API Key Secret Management
// =============================================================================

// GetAPIKeySecretName returns the Kubernetes secret name for a client's API key
func (s *KubernetesService) GetAPIKeySecretName(clientID uuid.UUID) string {
	return naming.APIKeySecretForClient(clientID.String())
}

// CreateAPIKeySecret creates or updates a Kubernetes Secret containing an API key for a client
func (s *KubernetesService) CreateAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID, apiKey string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	secretName := s.GetAPIKeySecretName(clientID)
	namespace := "fastgateway-system"

	// Build the Secret object
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      secretName,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"fastgateway.dev/managed-by": "fastgateway",
					"fastgateway.dev/client-id":  clientID.String(),
					"fastgateway.dev/type":       "api-key",
				},
			},
			"type": "Opaque",
			"data": map[string]interface{}{
				"api-key": base64.StdEncoding.EncodeToString([]byte(apiKey)),
			},
		},
	}

	// Try to create; if exists, update
	_, err = client.Resource(gvr).Namespace(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Get existing to preserve resourceVersion
			existing, getErr := client.Resource(gvr).Namespace(namespace).Get(ctx, secretName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("failed to get existing secret: %w", getErr)
			}
			secret.SetResourceVersion(existing.GetResourceVersion())
			_, err = client.Resource(gvr).Namespace(namespace).Update(ctx, secret, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update api key secret: %w", err)
			}
		} else {
			return fmt.Errorf("failed to create api key secret: %w", err)
		}
	}

	return nil
}

// GetAPIKeyFromSecret retrieves the API key from a Kubernetes Secret for a client
func (s *KubernetesService) GetAPIKeyFromSecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) (string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return "", err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	secretName := s.GetAPIKeySecretName(clientID)
	namespace := "fastgateway-system"

	secret, err := client.Resource(gvr).Namespace(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get api key secret: %w", err)
	}

	// Extract the data field
	data, found, err := unstructured.NestedMap(secret.Object, "data")
	if err != nil || !found {
		return "", fmt.Errorf("failed to extract data from secret")
	}

	// Get the api-key value (base64 encoded in data)
	apiKeyEncoded, ok := data["api-key"].(string)
	if !ok {
		return "", fmt.Errorf("api-key not found in secret")
	}

	// Decode from base64
	apiKeyBytes, err := base64.StdEncoding.DecodeString(apiKeyEncoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode api key: %w", err)
	}

	return string(apiKeyBytes), nil
}

// DeleteAPIKeySecret deletes a Kubernetes Secret containing an API key for a client
func (s *KubernetesService) DeleteAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	secretName := s.GetAPIKeySecretName(clientID)
	namespace := "fastgateway-system"

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, secretName, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) || strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete api key secret: %w", err)
	}

	return nil
}

// =============================================================================
// mTLS Secret Management
// =============================================================================

// CreateOrUpdateSecret creates or updates a K8s Secret with the given data
func (s *KubernetesService) CreateOrUpdateSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, data map[string][]byte) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	// Convert data to base64
	secretData := make(map[string]interface{})
	for k, v := range data {
		secretData[k] = base64.StdEncoding.EncodeToString(v)
	}

	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
					"fastgateway.dev/type":         "mtls-ca",
				},
			},
			"type": "Opaque",
			"data": secretData,
		},
	}

	// Try to create; if exists, update
	_, err = client.Resource(gvr).Namespace(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Get existing to preserve resourceVersion
			existing, getErr := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("failed to get existing secret: %w", getErr)
			}
			secret.SetResourceVersion(existing.GetResourceVersion())
			_, err = client.Resource(gvr).Namespace(namespace).Update(ctx, secret, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update secret: %w", err)
			}
		} else {
			return fmt.Errorf("failed to create secret: %w", err)
		}
	}

	return nil
}

// DeleteSecret deletes a K8s Secret
func (s *KubernetesService) DeleteSecret(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) && !strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("failed to delete secret: %w", err)
	}
	return nil
}

// GetSecretData retrieves data from a K8s Secret
func (s *KubernetesService) GetSecretData(ctx context.Context, projectID uuid.UUID, namespace, name, key string) ([]byte, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	secret, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}

	data, found, err := unstructured.NestedStringMap(secret.Object, "data")
	if err != nil || !found {
		return nil, fmt.Errorf("secret data not found")
	}

	encoded, ok := data[key]
	if !ok {
		return nil, fmt.Errorf("key '%s' not found in secret", key)
	}

	return base64.StdEncoding.DecodeString(encoded)
}

// IsRateLimitAvailable checks if rate limiting is available by reading the envoy-gateway-config ConfigMap
func (s *KubernetesService) IsRateLimitAvailable(ctx context.Context, projectID uuid.UUID) (bool, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return false, err
	}

	// Read the envoy-gateway-config ConfigMap from envoy-gateway-system namespace
	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	configMap, err := client.Resource(gvr).Namespace("envoy-gateway-system").Get(ctx, "envoy-gateway-config", metav1.GetOptions{})
	if err != nil {
		// ConfigMap not found or not accessible -- rate limiting not available
		return false, nil
	}

	// Parse the ConfigMap data
	data, found, err := unstructured.NestedStringMap(configMap.Object, "data")
	if err != nil || !found {
		return false, nil
	}

	// Check the envoy-gateway.yaml key for rateLimit.backend configuration
	yamlData, exists := data["envoy-gateway.yaml"]
	if !exists {
		return false, nil
	}

	// Simple check: look for "rateLimit:" and "backend:" in the YAML
	// This avoids importing a YAML parser -- the presence of these keys
	// indicates the user has configured the rate limit backend
	return strings.Contains(yamlData, "rateLimit:") && strings.Contains(yamlData, "backend:"), nil
}

// DeleteStaleAPIKeyResources deletes orphaned per-client HTTPRoutes, SecurityPolicies, and BackendTrafficPolicies
// that are no longer needed (i.e., their client prefixes are not in expectedClientPrefixes)
func (s *KubernetesService) DeleteStaleAPIKeyResources(ctx context.Context, projectID uuid.UUID, namespace, routeID, baseRouteName string, expectedClientPrefixes map[string]bool) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	// Use the expected prefixes map directly for fast lookup
	expectedSet := expectedClientPrefixes

	// Delete stale HTTPRoutes
	httpRouteGVR := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}
	if err := s.deleteStalePerClientResources(ctx, client, httpRouteGVR, namespace, routeID, baseRouteName, expectedSet); err != nil {
		return fmt.Errorf("failed to delete stale httproutes: %w", err)
	}

	// Delete stale GRPCRoutes
	grpcRouteGVR := getGRPCRouteGVR()
	if err := s.deleteStalePerClientResources(ctx, client, grpcRouteGVR, namespace, routeID, baseRouteName, expectedSet); err != nil {
		return fmt.Errorf("failed to delete stale grpcroutes: %w", err)
	}

	// Delete stale SecurityPolicies
	securityPolicyGVR := schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "securitypolicies",
	}
	if err := s.deleteStalePerClientResources(ctx, client, securityPolicyGVR, namespace, routeID, baseRouteName, expectedSet); err != nil {
		return fmt.Errorf("failed to delete stale securitypolicies: %w", err)
	}

	// Delete stale BackendTrafficPolicies
	btpGVR := schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "backendtrafficpolicies",
	}
	if err := s.deleteStalePerClientResources(ctx, client, btpGVR, namespace, routeID, baseRouteName, expectedSet); err != nil {
		return fmt.Errorf("failed to delete stale backendtrafficpolicies: %w", err)
	}

	return nil
}

// deleteStalePerClientResources deletes per-client resources that are no longer needed
func (s *KubernetesService) deleteStalePerClientResources(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, routeID, baseRouteName string, expectedPrefixes map[string]bool) error {
	// List resources by route ID label
	labelSelector := fmt.Sprintf("fastgateway.dev/route-id=%s", routeID)
	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list resources: %w", err)
	}

	// Check each resource to see if it's a per-client resource that should be deleted
	for _, item := range list.Items {
		name := item.GetName()

		// Check if this is a per-client resource (contains "-ak-")
		if !naming.IsPerClientResource(name) {
			continue // Skip base resources
		}

		// Extract the client prefix from the name
		clientPrefix := naming.ExtractClientPrefix(name, baseRouteName)
		if clientPrefix == "" {
			continue // Couldn't extract prefix, skip
		}

		// Check if this prefix is expected
		if expectedPrefixes[clientPrefix] {
			continue // This client is still attached, keep the resource
		}

		// Delete the stale resource
		err := client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete stale resource %s: %w", name, err)
		}
	}

	return nil
}

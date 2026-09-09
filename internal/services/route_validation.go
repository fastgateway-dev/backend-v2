package services

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

// validateGRPCRouteConfig validates that gRPC routes don't use HTTP-only features
func validateGRPCRouteConfig(config *models.RouteConfig) error {
	// gRPC only supports backend route type for now
	if config.RouteType == models.RouteTypeRedirect {
		return errors.New("redirect is not supported for gRPC routes")
	}
	if config.RouteType == models.RouteTypeDirectResponse {
		return errors.New("direct response is not supported for gRPC routes")
	}
	if config.URLRewrite != nil {
		return errors.New("URL rewrite is not supported for gRPC routes")
	}
	if config.Redirect != nil {
		return errors.New("redirect config is not supported for gRPC routes")
	}
	if config.DirectResponse != nil {
		return errors.New("direct response config is not supported for gRPC routes")
	}
	// Validate matches don't use HTTP-only fields
	for i, match := range config.Matches {
		if match.Path != nil {
			return fmt.Errorf("match[%d]: path matching is not supported for gRPC routes, use grpcService/grpcMethod", i)
		}
		if match.Method != "" {
			return fmt.Errorf("match[%d]: HTTP method matching is not supported for gRPC routes", i)
		}
		if len(match.QueryParams) > 0 {
			return fmt.Errorf("match[%d]: query parameter matching is not supported for gRPC routes", i)
		}
		// Gateway API GRPCMethodMatch has a single Type for both service and method.
		// If both are specified, their types must match.
		if match.GRPCService != nil && match.GRPCMethod != nil &&
			match.GRPCService.Type != "" && match.GRPCMethod.Type != "" &&
			match.GRPCService.Type != match.GRPCMethod.Type {
			return fmt.Errorf("match[%d]: grpcService type (%s) and grpcMethod type (%s) must be the same, as Gateway API GRPCMethodMatch uses a single type for both", i, match.GRPCService.Type, match.GRPCMethod.Type)
		}
	}
	return nil
}

// validateGRPCBackendTrafficPolicy rejects BackendTrafficPolicy features
// that cannot work on a gRPC route.
//
// requestBuffer is the only one today. Envoy Gateway documents it as
// incompatible with gRPC -- "Request buffering requires Envoy to fully
// receive the request before forwarding it upstream. This does not work
// with streaming or upgrade-based traffic such as gRPC streaming and
// WebSocket"
// (https://gateway.envoyproxy.io/docs/tasks/traffic/request-buffering/) --
// and an end-to-end run confirmed the shape of the failure: the
// BackendTrafficPolicy reports Accepted=True, and over-limit unary calls
// are forwarded upstream anyway, so the configured limit is silently
// ignored. For streaming calls the documented outcome is worse: the
// request may never be forwarded at all and the call hangs.
//
// Accepting the field would therefore promise a request-size cap that
// either does nothing or wedges the route, with nothing in the route's
// status to reveal which. Rejecting it at creation is the only outcome
// that tells the truth.
func validateGRPCBackendTrafficPolicy(btp *routeplan.BackendTrafficPolicyInput) error {
	if btp == nil {
		return nil
	}
	if btp.RequestBuffer != nil {
		return errors.New("requestBuffer is not supported for gRPC routes: Envoy Gateway cannot buffer gRPC traffic, so the limit would be silently ignored on unary calls and can hang streaming calls")
	}
	return nil
}

// validateHTTPRouteConfig validates that HTTP routes don't use gRPC-only features
func validateHTTPRouteConfig(config *models.RouteConfig) error {
	for i, match := range config.Matches {
		if match.GRPCService != nil {
			return fmt.Errorf("match[%d]: grpcService is only supported for gRPC routes", i)
		}
		if match.GRPCMethod != nil {
			return fmt.Errorf("match[%d]: grpcMethod is only supported for gRPC routes", i)
		}
	}
	return nil
}

// validateRouteConfig validates essential route configuration based on route type.
// This ensures routes have the minimum required configuration to function properly.
func validateRouteConfig(config *models.RouteConfig, protocol models.RouteProtocol) error {
	routeType := config.RouteType

	// Backend routes: require at least one backend
	if routeType == "" || routeType == models.RouteTypeBackend {
		if len(config.Backends) == 0 {
			return errors.New("at least one backend is required")
		}
	}

	// Backend and redirect routes: require path matching (HTTP only)
	// gRPC routes use grpcService/grpcMethod instead of path
	if routeType != models.RouteTypeDirectResponse && protocol != models.RouteProtocolGRPC {
		for i, match := range config.Matches {
			if match.Path == nil || match.Path.Value == "" {
				return fmt.Errorf("path matching is required for match rule %d", i+1)
			}
		}
		// Also validate if no matches at all (empty matches array)
		if len(config.Matches) == 0 {
			return errors.New("path matching is required")
		}
	}

	// Redirect routes: require redirect config
	if routeType == models.RouteTypeRedirect {
		if config.Redirect == nil {
			return errors.New("redirect configuration is required for redirect routes")
		}
	}

	return nil
}

// BackendTLSWarnings returns warnings for TLS configuration
func BackendTLSWarnings(config *models.RouteConfig) []string {
	var warnings []string
	for _, backend := range config.Backends {
		if backend.TLS != nil && backend.TLS.InsecureSkipVerify {
			warnings = append(warnings, "insecureSkipVerify skips backend certificate verification. Use only for development/testing or when you trust the network.")
			break // one warning is enough
		}
	}
	return warnings
}

// DirectResponsePercentWarnings returns warnings when a DirectResponse inline
// body contains a literal '%'. On Envoy Gateway v1.8+, '%...%' in the body is
// interpolated as an Envoy command operator. Existing routes with '%' in the
// inline body will produce different output after the cluster admin upgrades
// EG, so we surface a warning at submit time. Detection is intentionally
// simple: any '%' triggers a warning. Authors who genuinely want command
// operators can dismiss it; authors with literal '%' can escape as '%%' or
// switch to a ValueRef body.
func DirectResponsePercentWarnings(config *models.RouteConfig) []string {
	if config == nil || config.DirectResponse == nil || config.DirectResponse.Body == nil {
		return nil
	}
	b := config.DirectResponse.Body
	if b.Type != models.DirectResponseBodyTypeInline {
		return nil
	}
	if !strings.Contains(b.Inline, "%") {
		return nil
	}
	return []string{
		"DirectResponse inline body contains '%'. On Envoy Gateway v1.8+, '%...%' is interpolated as an Envoy command operator. Escape literal '%' as '%%' to keep current behavior, or use a ValueRef body for complex content.",
	}
}

// routeMatchersEqual checks if two RouteMatch structs are equal.
// Comparison is case-insensitive for path values and header values.
// Headers and QueryParams are compared order-independently.
func routeMatchersEqual(a, b models.RouteMatch) bool {
	if !pathMatchEqual(a.Path, b.Path) {
		return false
	}
	if !strings.EqualFold(a.Method, b.Method) {
		return false
	}
	if !headerMatchesEqual(a.Headers, b.Headers) {
		return false
	}
	if !queryParamMatchesEqual(a.QueryParams, b.QueryParams) {
		return false
	}
	if !grpcMatchEqual(a.GRPCService, b.GRPCService) {
		return false
	}
	if !grpcMatchEqual(a.GRPCMethod, b.GRPCMethod) {
		return false
	}
	return true
}

func pathMatchEqual(a, b *models.PathMatch) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Type, b.Type) && strings.EqualFold(a.Value, b.Value)
}

func grpcMatchEqual(a, b *models.GRPCMethodMatch) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Type, b.Type) && strings.EqualFold(a.Value, b.Value)
}

func headerMatchesEqual(a, b []models.HeaderMatch) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	type key struct{ name, typ, value string }
	set := make(map[key]bool, len(a))
	for _, h := range a {
		set[key{strings.ToLower(h.Name), strings.ToLower(h.Type), strings.ToLower(h.Value)}] = true
	}
	for _, h := range b {
		if !set[key{strings.ToLower(h.Name), strings.ToLower(h.Type), strings.ToLower(h.Value)}] {
			return false
		}
	}
	return true
}

func queryParamMatchesEqual(a, b []models.QueryParamMatch) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	type key struct{ name, typ, value string }
	set := make(map[key]bool, len(a))
	for _, q := range a {
		set[key{strings.ToLower(q.Name), strings.ToLower(q.Type), strings.ToLower(q.Value)}] = true
	}
	for _, q := range b {
		if !set[key{strings.ToLower(q.Name), strings.ToLower(q.Type), strings.ToLower(q.Value)}] {
			return false
		}
	}
	return true
}

// ConflictResult represents a route that conflicts with a given matcher.
type ConflictResult struct {
	RouteID   uuid.UUID `json:"routeId"`
	RouteName string    `json:"routeName"`
}

// validateSecurityModeGeneral validates that general mode security input is valid
func validateSecurityModeGeneral(input *routeplan.SecurityPolicyInput) error {
	if input == nil {
		return nil
	}

	if input.OIDC != nil {
		if input.OIDC.Issuer == "" {
			return errors.New("OIDC issuer is required")
		}
		if input.OIDC.ClientID == "" {
			return errors.New("OIDC clientId is required")
		}
		if input.OIDC.ClientSecretName == "" {
			return errors.New("OIDC clientSecretName is required")
		}
		if input.OIDC.RedirectURL == "" {
			return errors.New("OIDC redirectURL is required")
		}
		if !strings.HasPrefix(input.OIDC.RedirectURL, "https://") {
			return errors.New("OIDC redirectURL must use HTTPS")
		}
		if input.OIDC.LogoutPath == "" {
			return errors.New("OIDC logoutPath is required")
		}
	}

	if input.JWT != nil {
		if input.JWT.Issuer == "" {
			return errors.New("JWT issuer is required")
		}
		if input.JWT.JWKSURL == "" {
			return errors.New("JWT jwksUrl is required")
		}
	}

	if input.APIKeyAuth != nil {
		if input.APIKeyAuth.SecretName == "" {
			return errors.New("API key secretName is required")
		}
		if input.APIKeyAuth.HeaderName == "" {
			return errors.New("API key headerName is required")
		}
	}

	if input.Authorization != nil {
		hasCIDRs := len(input.Authorization.AllowedCIDRs) > 0
		hasHeaders := len(input.Authorization.Headers) > 0
		hasMethods := len(input.Authorization.Methods) > 0
		if !hasCIDRs && !hasHeaders && !hasMethods {
			return errors.New("authorization requires at least one of: allowedCIDRs, headers, or methods")
		}
		for _, cidr := range input.Authorization.AllowedCIDRs {
			normalized := routeplan.NormalizeCIDR(cidr)
			if _, _, err := net.ParseCIDR(normalized); err != nil {
				return fmt.Errorf("invalid CIDR or IP address: %s", cidr)
			}
		}
		for _, h := range input.Authorization.Headers {
			if strings.TrimSpace(h.Name) == "" {
				return errors.New("authorization header name must not be empty")
			}
			if len(h.Values) == 0 {
				return errors.New("authorization header must have at least one value")
			}
		}
		validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true}
		for _, m := range input.Authorization.Methods {
			if !validMethods[strings.ToUpper(m)] {
				return fmt.Errorf("invalid HTTP method: %s", m)
			}
		}
	}

	// Validate ExtAuth if present
	if input.ExtAuth != nil {
		if err := input.ExtAuth.Validate(); err != nil {
			return fmt.Errorf("extAuth: %w", err)
		}
	}

	return nil
}

// validateSecurityModeClient validates that client mode doesn't have general-only fields
func validateSecurityModeClient(input *routeplan.SecurityPolicyInput) error {
	if input == nil {
		return nil
	}
	if input.Authorization != nil {
		return errors.New("IP allowlisting via authorization is not available in client security mode; use client attachments instead")
	}
	if input.APIKeyAuth != nil {
		return errors.New("API key auth is not available in client security mode; use client attachments instead")
	}
	if input.JWT != nil {
		return errors.New("JWT auth is not available in client security mode; use client attachments instead")
	}
	if input.OIDC != nil {
		return errors.New("OIDC is not available in client security mode; use general security mode")
	}
	// ExtAuth is allowed in client mode (per-attachment config takes precedence)
	if input.ExtAuth != nil {
		if err := input.ExtAuth.Validate(); err != nil {
			return fmt.Errorf("extAuth: %w", err)
		}
	}
	return nil
}

// isValidK8sName checks if a name is valid for Kubernetes resources
// Must be lowercase alphanumeric with dashes, start with letter, end with alphanumeric
//
// This validator is load-bearing beyond input validation: kubernetes.RouteK8sName
// (used to derive the K8s resource name) sanitizes its input differently than this
// function rejects it (e.g. it lowercases/replaces underscores and trims a trailing
// dash instead of erroring). The two only agree because every caller of
// kubernetes.RouteK8sName validates with isValidK8sName first, so the inputs that
// would make them disagree (leading dash, trailing dash, underscore, uppercase)
// never reach it. Relaxing this validator without checking kubernetes.RouteK8sName's
// sanitize() behavior first can silently change generated resource names.
func isValidK8sName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}

	// Must start with a lowercase letter
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}

	// Must end with alphanumeric
	last := name[len(name)-1]
	if !((last >= 'a' && last <= 'z') || (last >= '0' && last <= '9')) {
		return false
	}

	// Check all characters
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}

	return true
}

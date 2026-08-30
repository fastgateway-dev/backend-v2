// Package naming provides resource naming conventions for FastGateway Kubernetes resources.
// This ensures consistent naming patterns and handles length constraints.
package kubernetes

import (
	"fmt"
	"strings"
)

// Constants for naming patterns
const (
	// PerClientSuffix is the suffix used for per-client HTTPRoutes
	PerClientSuffix = "-ak-"

	// SecurityPolicySuffix is the suffix for SecurityPolicy resources
	SecurityPolicySuffix = "-security"

	// BackendTrafficPolicySuffix is the suffix for BackendTrafficPolicy resources
	BackendTrafficPolicySuffix = "-btp"

	// EnvoyExtensionPolicySuffix is the suffix for EnvoyExtensionPolicy resources
	EnvoyExtensionPolicySuffix = "-eep"

	// HTTPRouteFilterSuffix is the suffix for HTTPRouteFilter resources
	HTTPRouteFilterSuffix = "-hrf"

	// APIKeySecretPrefix is the prefix for API key secrets
	APIKeySecretPrefix = "fastgateway-apikey-"

	// ClientPrefixLength is the length of client ID prefix used in naming
	ClientPrefixLength = 8

	// MaxK8sNameLength is the maximum length for Kubernetes resource names
	MaxK8sNameLength = 63
)

// HTTPRouteName returns the name for a base HTTPRoute
func HTTPRouteName(baseName string) string {
	return baseName
}

// PerClientHTTPRouteName returns the name for a per-client HTTPRoute
// Format: {baseName}-ak-{clientPrefix}
func PerClientHTTPRouteName(baseName, clientID string) string {
	clientPrefix := ClientPrefixName(clientID)
	return fmt.Sprintf("%s%s%s", baseName, PerClientSuffix, clientPrefix)
}

// SecurityPolicyName returns the name for a base SecurityPolicy
// Format: {routeName}-security
func SecurityPolicyName(routeName string) string {
	return fmt.Sprintf("%s%s", routeName, SecurityPolicySuffix)
}

// PerClientSecurityPolicyName returns the name for a per-client SecurityPolicy
// Format: {routeName}-ak-{clientPrefix}-security
func PerClientSecurityPolicyName(routeName, clientID string) string {
	clientPrefix := ClientPrefixName(clientID)
	return fmt.Sprintf("%s%s%s%s", routeName, PerClientSuffix, clientPrefix, SecurityPolicySuffix)
}

// BackendTrafficPolicyName returns the name for a base BackendTrafficPolicy
// Format: {routeName}-btp
func BackendTrafficPolicyName(routeName string) string {
	return fmt.Sprintf("%s%s", routeName, BackendTrafficPolicySuffix)
}

// PerClientBackendTrafficPolicyName returns the name for a per-client BackendTrafficPolicy
// Format: {routeName}-ak-{clientPrefix}-btp
func PerClientBackendTrafficPolicyName(routeName, clientID string) string {
	clientPrefix := ClientPrefixName(clientID)
	return fmt.Sprintf("%s%s%s%s", routeName, PerClientSuffix, clientPrefix, BackendTrafficPolicySuffix)
}

// EnvoyExtensionPolicyName returns the name for an EnvoyExtensionPolicy.
// The routeName passed in may already include a per-client prefix (e.g. from
// PerClientHTTPRouteName), in which case this yields the per-client policy name.
// Format: {routeName}-eep
func EnvoyExtensionPolicyName(routeName string) string {
	return fmt.Sprintf("%s%s", routeName, EnvoyExtensionPolicySuffix)
}

// HTTPRouteFilterName returns the name for an HTTPRouteFilter
// Format: {routeName}-hrf
func HTTPRouteFilterName(routeName string) string {
	return fmt.Sprintf("%s%s", routeName, HTTPRouteFilterSuffix)
}

// APIKeySecretName returns the name for an API key secret
// Format: fastgateway-apikey-{routeID}-{clientPrefix}
func APIKeySecretName(routeID, clientID string) string {
	clientPrefix := ClientPrefixName(clientID)
	return fmt.Sprintf("%s%s-%s", APIKeySecretPrefix, routeID, clientPrefix)
}

// APIKeySecretForClientName returns the name for a client's API key secret (stored in central namespace)
// Format: fastgateway-apikey-{clientPrefix}
func APIKeySecretForClientName(clientID string) string {
	clientPrefix := ClientPrefixName(clientID)
	return fmt.Sprintf("%s%s", APIKeySecretPrefix, clientPrefix)
}

// ClientPrefixName returns the shortened prefix for a client ID
func ClientPrefixName(clientID string) string {
	if len(clientID) > ClientPrefixLength {
		return clientID[:ClientPrefixLength]
	}
	return clientID
}

// RouteK8sName generates a valid Kubernetes name from route name and ID
// Ensures the name is within the 63 character limit
func RouteK8sName(routeName, routeID string) string {
	// Start with sanitized route name
	name := sanitize(routeName)

	// Use first 8 chars of route ID as suffix for uniqueness
	shortID := routeID
	if len(routeID) > 8 {
		shortID = routeID[:8]
	}

	// Check if we need to truncate
	fullName := fmt.Sprintf("%s-%s", name, shortID)
	if len(fullName) <= MaxK8sNameLength {
		return fullName
	}

	// Truncate route name to fit
	maxNameLen := MaxK8sNameLength - len(shortID) - 1 // -1 for the dash
	if maxNameLen < 1 {
		return shortID // Fallback to just the ID
	}

	return fmt.Sprintf("%s-%s", name[:maxNameLen], shortID)
}

// IsPerClientResource checks if a resource name is for a per-client resource
func IsPerClientResource(name string) bool {
	return strings.Contains(name, PerClientSuffix)
}

// ExtractClientPrefix extracts the client prefix from a per-client resource name
// Returns empty string if not a per-client resource
func ExtractClientPrefix(name, baseName string) string {
	if !IsPerClientResource(name) {
		return ""
	}

	// Name format: {baseName}-ak-{clientPrefix} or {baseName}-ak-{clientPrefix}-security
	prefix := baseName + PerClientSuffix
	if !strings.HasPrefix(name, prefix) {
		return ""
	}

	remainder := strings.TrimPrefix(name, prefix)

	// Remove any suffix (-security, -btp, etc.)
	if idx := strings.Index(remainder, "-"); idx != -1 {
		remainder = remainder[:idx]
	}

	return remainder
}

// sanitize converts a name to a valid Kubernetes resource name
// - converts to lowercase
// - replaces spaces and underscores with dashes
// - removes invalid characters
func sanitize(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")

	// Keep only alphanumeric and dashes
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}

	// Remove leading/trailing dashes
	return strings.Trim(result.String(), "-")
}

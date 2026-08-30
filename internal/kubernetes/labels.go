// Package labels provides label builder functions for FastGateway Kubernetes resources.
// This ensures consistent labeling across all resources.
package kubernetes

import "fmt"

// Label keys used by FastGateway
const (
	// ManagedBy indicates the resource is managed by FastGateway
	KeyManagedBy = "fastgateway.dev/managed-by"
	// GatewayID links the resource to a specific gateway
	KeyGatewayID = "fastgateway.dev/gateway-id"
	// RouteID links the resource to a specific route
	KeyRouteID = "fastgateway.dev/route-id"
	// ProjectID links the resource to a specific project
	KeyProjectID = "fastgateway.dev/project-id"
	// ClientID links the resource to a specific client
	KeyClientID = "fastgateway.dev/client-id"
	// Type indicates the resource type (e.g., "api-key")
	KeyType = "fastgateway.dev/type"

	// Standard Kubernetes labels
	KeyAppManagedBy = "app.kubernetes.io/managed-by"

	// Values
	ValueFastGateway = "fastgateway"
	ValueAPIKey      = "api-key"
)

// ForRoute creates labels for route-scoped resources
func ForRoute(gatewayID, routeID string) map[string]string {
	return map[string]string{
		KeyAppManagedBy: ValueFastGateway,
		KeyGatewayID:    gatewayID,
		KeyRouteID:      routeID,
	}
}

// ForRouteInterface creates labels for route-scoped resources (for unstructured maps)
func ForRouteInterface(gatewayID, routeID string) map[string]interface{} {
	return map[string]interface{}{
		KeyAppManagedBy: ValueFastGateway,
		KeyGatewayID:    gatewayID,
		KeyRouteID:      routeID,
	}
}

// ForGateway creates labels for gateway-scoped resources
func ForGateway(gatewayID string) map[string]string {
	return map[string]string{
		KeyAppManagedBy: ValueFastGateway,
		KeyGatewayID:    gatewayID,
	}
}

// ForGatewayInterface creates labels for gateway-scoped resources (for unstructured maps)
func ForGatewayInterface(gatewayID string) map[string]interface{} {
	return map[string]interface{}{
		KeyAppManagedBy: ValueFastGateway,
		KeyGatewayID:    gatewayID,
	}
}

// ForAPIKeySecret creates labels for API key secrets
func ForAPIKeySecret(clientID string) map[string]string {
	return map[string]string{
		KeyManagedBy: ValueFastGateway,
		KeyClientID:  clientID,
		KeyType:      ValueAPIKey,
	}
}

// ForAPIKeySecretInterface creates labels for API key secrets (for unstructured maps)
func ForAPIKeySecretInterface(clientID string) map[string]interface{} {
	return map[string]interface{}{
		KeyManagedBy: ValueFastGateway,
		KeyClientID:  clientID,
		KeyType:      ValueAPIKey,
	}
}

// Selector returns a label selector string for the given key-value pair
func Selector(key, value string) string {
	return fmt.Sprintf("%s=%s", key, value)
}

// SelectorRouteID returns a label selector for route ID
func SelectorRouteID(routeID string) string {
	return Selector(KeyRouteID, routeID)
}

// SelectorGatewayID returns a label selector for gateway ID
func SelectorGatewayID(gatewayID string) string {
	return Selector(KeyGatewayID, gatewayID)
}

// SelectorClientID returns a label selector for client ID
func SelectorClientID(clientID string) string {
	return Selector(KeyClientID, clientID)
}

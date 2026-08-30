package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForRoute(t *testing.T) {
	tests := []struct {
		name      string
		gatewayID string
		routeID   string
		expected  map[string]string
	}{
		{
			name:      "basic route labels",
			gatewayID: "gw-123",
			routeID:   "rt-456",
			expected: map[string]string{
				KeyAppManagedBy: ValueFastGateway,
				KeyGatewayID:    "gw-123",
				KeyRouteID:      "rt-456",
			},
		},
		{
			name:      "empty IDs",
			gatewayID: "",
			routeID:   "",
			expected: map[string]string{
				KeyAppManagedBy: ValueFastGateway,
				KeyGatewayID:    "",
				KeyRouteID:      "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ForRoute(tt.gatewayID, tt.routeID)
			assert.Equal(t, tt.expected, result)
			assert.Len(t, result, 3)
		})
	}
}

func TestForRouteInterface(t *testing.T) {
	result := ForRouteInterface("gw-abc", "rt-def")

	assert.Equal(t, ValueFastGateway, result[KeyAppManagedBy])
	assert.Equal(t, "gw-abc", result[KeyGatewayID])
	assert.Equal(t, "rt-def", result[KeyRouteID])
	assert.Len(t, result, 3)
}

func TestForGateway(t *testing.T) {
	tests := []struct {
		name      string
		gatewayID string
		expected  map[string]string
	}{
		{
			name:      "basic gateway labels",
			gatewayID: "gw-789",
			expected: map[string]string{
				KeyAppManagedBy: ValueFastGateway,
				KeyGatewayID:    "gw-789",
			},
		},
		{
			name:      "empty gateway ID",
			gatewayID: "",
			expected: map[string]string{
				KeyAppManagedBy: ValueFastGateway,
				KeyGatewayID:    "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ForGateway(tt.gatewayID)
			assert.Equal(t, tt.expected, result)
			assert.Len(t, result, 2)
		})
	}
}

func TestForGatewayInterface(t *testing.T) {
	result := ForGatewayInterface("gw-xyz")

	assert.Equal(t, ValueFastGateway, result[KeyAppManagedBy])
	assert.Equal(t, "gw-xyz", result[KeyGatewayID])
	assert.Len(t, result, 2)
}

func TestForAPIKeySecret(t *testing.T) {
	result := ForAPIKeySecret("client-001")

	assert.Equal(t, map[string]string{
		KeyManagedBy: ValueFastGateway,
		KeyClientID:  "client-001",
		KeyType:      ValueAPIKey,
	}, result)
	assert.Len(t, result, 3)
}

func TestForAPIKeySecretInterface(t *testing.T) {
	result := ForAPIKeySecretInterface("client-002")

	assert.Equal(t, ValueFastGateway, result[KeyManagedBy])
	assert.Equal(t, "client-002", result[KeyClientID])
	assert.Equal(t, ValueAPIKey, result[KeyType])
	assert.Len(t, result, 3)
}

func TestSelector(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "standard selector",
			key:      "app",
			value:    "myapp",
			expected: "app=myapp",
		},
		{
			name:     "label key selector",
			key:      KeyRouteID,
			value:    "route-123",
			expected: "fastgateway.dev/route-id=route-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, Selector(tt.key, tt.value))
		})
	}
}

func TestSelectorRouteID(t *testing.T) {
	result := SelectorRouteID("rt-abc")
	assert.Equal(t, "fastgateway.dev/route-id=rt-abc", result)
}

func TestSelectorGatewayID(t *testing.T) {
	result := SelectorGatewayID("gw-def")
	assert.Equal(t, "fastgateway.dev/gateway-id=gw-def", result)
}

func TestSelectorClientID(t *testing.T) {
	result := SelectorClientID("cl-ghi")
	assert.Equal(t, "fastgateway.dev/client-id=cl-ghi", result)
}

func TestLabelConstants(t *testing.T) {
	assert.Equal(t, "fastgateway.dev/managed-by", KeyManagedBy)
	assert.Equal(t, "fastgateway.dev/gateway-id", KeyGatewayID)
	assert.Equal(t, "fastgateway.dev/route-id", KeyRouteID)
	assert.Equal(t, "fastgateway.dev/project-id", KeyProjectID)
	assert.Equal(t, "fastgateway.dev/client-id", KeyClientID)
	assert.Equal(t, "fastgateway.dev/type", KeyType)
	assert.Equal(t, "app.kubernetes.io/managed-by", KeyAppManagedBy)
	assert.Equal(t, "fastgateway", ValueFastGateway)
	assert.Equal(t, "api-key", ValueAPIKey)
}

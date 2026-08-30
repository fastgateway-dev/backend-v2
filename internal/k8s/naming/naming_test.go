package naming

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHTTPRoute(t *testing.T) {
	assert.Equal(t, "my-route", HTTPRoute("my-route"))
	assert.Equal(t, "", HTTPRoute(""))
}

func TestPerClientHTTPRoute(t *testing.T) {
	tests := []struct {
		name     string
		baseName string
		clientID string
		expected string
	}{
		{
			name:     "normal client ID longer than prefix length",
			baseName: "my-route",
			clientID: "client-1234567890",
			expected: "my-route-ak-client-1",
		},
		{
			name:     "short client ID",
			baseName: "my-route",
			clientID: "abc",
			expected: "my-route-ak-abc",
		},
		{
			name:     "exact prefix length client ID",
			baseName: "my-route",
			clientID: "12345678",
			expected: "my-route-ak-12345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PerClientHTTPRoute(tt.baseName, tt.clientID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSecurityPolicy(t *testing.T) {
	assert.Equal(t, "my-route-security", SecurityPolicy("my-route"))
	assert.Equal(t, "-security", SecurityPolicy(""))
}

func TestPerClientSecurityPolicy(t *testing.T) {
	result := PerClientSecurityPolicy("my-route", "client-1234567890")
	assert.Equal(t, "my-route-ak-client-1-security", result)

	result2 := PerClientSecurityPolicy("my-route", "short")
	assert.Equal(t, "my-route-ak-short-security", result2)
}

func TestBackendTrafficPolicy(t *testing.T) {
	assert.Equal(t, "my-route-btp", BackendTrafficPolicy("my-route"))
	assert.Equal(t, "-btp", BackendTrafficPolicy(""))
}

func TestPerClientBackendTrafficPolicy(t *testing.T) {
	result := PerClientBackendTrafficPolicy("my-route", "client-1234567890")
	assert.Equal(t, "my-route-ak-client-1-btp", result)

	result2 := PerClientBackendTrafficPolicy("my-route", "short")
	assert.Equal(t, "my-route-ak-short-btp", result2)
}

func TestAPIKeySecret(t *testing.T) {
	result := APIKeySecret("route-abc", "client-1234567890")
	assert.Equal(t, "fastgateway-apikey-route-abc-client-1", result)

	result2 := APIKeySecret("route-abc", "cl")
	assert.Equal(t, "fastgateway-apikey-route-abc-cl", result2)
}

func TestAPIKeySecretForClient(t *testing.T) {
	result := APIKeySecretForClient("client-1234567890")
	assert.Equal(t, "fastgateway-apikey-client-1", result)

	result2 := APIKeySecretForClient("short")
	assert.Equal(t, "fastgateway-apikey-short", result2)
}

func TestClientPrefix(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		expected string
	}{
		{
			name:     "longer than prefix length",
			clientID: "abcdefghijklmnop",
			expected: "abcdefgh",
		},
		{
			name:     "exactly prefix length",
			clientID: "abcdefgh",
			expected: "abcdefgh",
		},
		{
			name:     "shorter than prefix length",
			clientID: "abc",
			expected: "abc",
		},
		{
			name:     "empty string",
			clientID: "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClientPrefix(tt.clientID)
			assert.Equal(t, tt.expected, result)
			assert.LessOrEqual(t, len(result), ClientPrefixLength)
		})
	}
}

func TestRouteK8sName(t *testing.T) {
	tests := []struct {
		name      string
		routeName string
		routeID   string
		expected  string
	}{
		{
			name:      "simple name",
			routeName: "my-route",
			routeID:   "abc12345",
			expected:  "my-route-abc12345",
		},
		{
			name:      "name with spaces and uppercase",
			routeName: "My Route Name",
			routeID:   "id123456",
			expected:  "my-route-name-id123456",
		},
		{
			name:      "name with underscores",
			routeName: "my_route_name",
			routeID:   "id123456",
			expected:  "my-route-name-id123456",
		},
		{
			name:      "name with special characters",
			routeName: "my@route!name",
			routeID:   "id123456",
			expected:  "myroutename-id123456",
		},
		{
			name:      "long route ID gets truncated to 8 chars",
			routeName: "route",
			routeID:   "abcdefghijklmnop",
			expected:  "route-abcdefgh",
		},
		{
			name:      "short route ID stays as-is",
			routeName: "route",
			routeID:   "abc",
			expected:  "route-abc",
		},
		{
			name:      "very long name gets truncated to fit 63 chars",
			routeName: strings.Repeat("a", 100),
			routeID:   "12345678",
			expected:  strings.Repeat("a", 54) + "-12345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RouteK8sName(tt.routeName, tt.routeID)
			assert.Equal(t, tt.expected, result)
			assert.LessOrEqual(t, len(result), MaxK8sNameLength)
		})
	}
}

func TestRouteK8sNameAlwaysWithinLimit(t *testing.T) {
	// Property-based: any input should produce a name <= 63 chars
	inputs := []struct {
		name string
		id   string
	}{
		{strings.Repeat("x", 200), strings.Repeat("y", 200)},
		{"a", "b"},
		{"", "id12345678"},
		{strings.Repeat("long-name-", 10), "abcdefgh"},
	}

	for _, input := range inputs {
		result := RouteK8sName(input.name, input.id)
		assert.LessOrEqual(t, len(result), MaxK8sNameLength,
			"name=%q id=%q produced %q (len %d)", input.name, input.id, result, len(result))
	}
}

func TestIsPerClientResource(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"per-client route", "my-route-ak-client1", true},
		{"per-client security", "my-route-ak-client1-security", true},
		{"per-client btp", "my-route-ak-client1-btp", true},
		{"base route", "my-route", false},
		{"base security", "my-route-security", false},
		{"empty string", "", false},
		{"contains suffix in middle", "some-ak-thing", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsPerClientResource(tt.input))
		})
	}
}

func TestExtractClientPrefix(t *testing.T) {
	tests := []struct {
		name     string
		resName  string
		baseName string
		expected string
	}{
		{
			name:     "extract from per-client route",
			resName:  "my-route-ak-client12",
			baseName: "my-route",
			expected: "client12",
		},
		{
			name:     "extract from per-client security policy",
			resName:  "my-route-ak-client12-security",
			baseName: "my-route",
			expected: "client12",
		},
		{
			name:     "extract from per-client btp",
			resName:  "my-route-ak-client12-btp",
			baseName: "my-route",
			expected: "client12",
		},
		{
			name:     "not a per-client resource",
			resName:  "my-route",
			baseName: "my-route",
			expected: "",
		},
		{
			name:     "wrong base name",
			resName:  "other-route-ak-client12",
			baseName: "my-route",
			expected: "",
		},
		{
			name:     "empty inputs",
			resName:  "",
			baseName: "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractClientPrefix(tt.resName, tt.baseName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "-ak-", PerClientSuffix)
	assert.Equal(t, "-security", SecurityPolicySuffix)
	assert.Equal(t, "-btp", BackendTrafficPolicySuffix)
	assert.Equal(t, "fastgateway-apikey-", APIKeySecretPrefix)
	assert.Equal(t, 8, ClientPrefixLength)
	assert.Equal(t, 63, MaxK8sNameLength)
}

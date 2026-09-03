package routeplan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoldenEnvoyExtensionPolicyConfigs snapshots
// BuildEnvoyExtensionPolicyK8sConfig, the builder that unifies the two
// EnvoyExtensionPolicyK8sConfig call sites in route_deploy.go and
// route_clients_apikey.go (Phase 2H).
//
// Regenerate with:
//
//	go test ./internal/routeplan/ -run TestGolden -update-golden
func TestGoldenEnvoyExtensionPolicyConfigs(t *testing.T) {
	for _, tc := range envoyExtensionPolicyFixtures() {
		t.Run(tc.Name, func(t *testing.T) {
			got := tc.Build()
			require.NotNil(t, got, "fixture %s built a nil config", tc.Name)
			assertGolden(t, tc.Name, got)
		})
	}
}

// TestGoldenFixtureNamesAreUnique guards against two fixtures silently sharing
// one golden file -- the second would overwrite the first under -update-golden
// and the pair would assert against each other's output.
func TestGoldenFixtureNamesAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, tc := range envoyExtensionPolicyFixtures() {
		require.False(t, seen[tc.Name], "duplicate fixture name %q", tc.Name)
		seen[tc.Name] = true
	}
}

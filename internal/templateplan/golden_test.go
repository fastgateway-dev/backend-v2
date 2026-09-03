package templateplan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoldenTemplateManifests snapshots every domain-template manifest
// builder in this package: BuildGatewayClassConfig and BuildEnvoyProxyConfig.
//
// Before Phase 2H these two builders did not exist as standalone functions --
// the shapes they produce were copy-pasted struct literals inline in
// DomainTemplateService (Create, Update, GetManifests, PreviewChanges,
// PreviewCreate). These are their first snapshots.
//
// Regenerate with:
//
//	go test ./internal/templateplan/ -run TestGolden -update-golden
func TestGoldenTemplateManifests(t *testing.T) {
	for _, tc := range templateFixtures() {
		t.Run(tc.Name, func(t *testing.T) {
			got := tc.Build()
			// A builder that returned a typed nil would marshal to "null" and
			// produce a golden that pins nothing. Neither builder in this
			// package has a nil-return path today, but the guard is cheap
			// insurance against that changing silently.
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
	for _, tc := range templateFixtures() {
		require.False(t, seen[tc.Name], "duplicate fixture name %q", tc.Name)
		seen[tc.Name] = true
	}
}

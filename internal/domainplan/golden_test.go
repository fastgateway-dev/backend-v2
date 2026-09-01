package domainplan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoldenDomainManifests snapshots every domain-level manifest builder.
//
// Before Phase 2F this path -- the four builders extracted from
// DomainService -- had NO golden coverage at all: the 72 goldens under
// internal/services/testdata/golden/ cover the route path only. These are its
// first snapshots.
//
// Two of them deliberately pin KNOWN DEFECTS rather than correct behaviour:
//
//	gateway-tls-secret-namespace-dropped-f2  -- F2, dead cross-namespace certRef
//	ctp-mtls-enabled-no-ca-refs-f3           -- F3, mTLS fail-open
//
// See the comments on those fixtures in fixtures_test.go. Neither is fixed
// here; pinning them stops them drifting further, and when either is fixed the
// golden MUST change.
//
// Regenerate with:
//
//	go test ./internal/domainplan/ -run TestGolden -update-golden
func TestGoldenDomainManifests(t *testing.T) {
	for _, tc := range domainFixtures() {
		t.Run(tc.Name, func(t *testing.T) {
			got := tc.Build()
			// A builder that returned a typed nil would marshal to "null" and
			// produce a golden that pins nothing. The nil-return paths are
			// covered by the unit tests in domainplan_test.go instead.
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
	for _, tc := range domainFixtures() {
		require.False(t, seen[tc.Name], "duplicate fixture name %q", tc.Name)
		seen[tc.Name] = true
	}
}

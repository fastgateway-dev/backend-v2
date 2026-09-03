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
// None of these goldens deliberately pins a KNOWN DEFECT any more. The last
// one that did, gateway-tls-secret-namespace-dropped-f2 (F2, the dropped
// cross-namespace certRef), was closed in Phase 2H: BuildGatewayConfig now
// maps TLSSecretNamespace, the golden was regenerated, and the fixture and
// golden were renamed to gateway-tls-secret-namespace to drop the defect
// marker. See the comment on that fixture in fixtures_test.go.
//
// ctp-mtls-enabled-no-ca-refs (formerly ctp-mtls-enabled-no-ca-refs-f3) used
// to pin F3, the mTLS rendering fail-open, the same way. Phase 2G fixed F3 in
// clienttrafficpolicy.go, so this golden was regenerated and renamed to drop
// the "-f3" defect marker; it now pins the FIXED (fail-closed) behaviour.
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

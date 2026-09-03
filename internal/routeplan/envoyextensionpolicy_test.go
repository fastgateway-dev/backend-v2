package routeplan

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/stretchr/testify/require"
)

// wantSnapshotYAML is captured from GenerateEnvoyExtensionPolicyYAMLFromSnapshot
// before delegation, and again confirmed unchanged after -- its rendering body
// was already byte-for-byte identical to BuildEnvoyExtensionPolicyK8sConfig's
// body (Phase 2H's builder), so delegating to it is a no-op on output.
const wantSnapshotYAML = `apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyExtensionPolicy
metadata:
  labels:
    app.kubernetes.io/managed-by: fastgateway
    fastgateway.dev/gateway-id: 22222222-2222-2222-2222-222222222222
    fastgateway.dev/route-id: 11111111-1111-1111-1111-111111111111
  name: example-route-eep
  namespace: gateway-ns
spec:
  lua:
  - inline: return 1
    type: Inline
  targetRefs:
  - group: gateway.networking.k8s.io
    kind: HTTPRoute
    name: example-route
`

// wantSnapshotWafYAML is the WAF-branch counterpart of wantSnapshotYAML,
// pinning that the coraza-waf wasm entry survives delegation too.
const wantSnapshotWafYAML = `apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyExtensionPolicy
metadata:
  labels:
    app.kubernetes.io/managed-by: fastgateway
    fastgateway.dev/gateway-id: 22222222-2222-2222-2222-222222222222
    fastgateway.dev/route-id: 11111111-1111-1111-1111-111111111111
  name: example-route-eep
  namespace: gateway-ns
spec:
  targetRefs:
  - group: gateway.networking.k8s.io
    kind: HTTPRoute
    name: example-route
  wasm:
  - code:
      image:
        url: example.com/coraza-proxy-wasm:9.9.9
      type: Image
    config:
      default_directives: default
      directives_map:
        default:
        - SecRuleEngine On
        - SecAction "id:900000,phase:1,pass,t:none,nolog,setvar:tx.blocking_paranoia_level=1"
        - SecAction "id:900110,phase:1,pass,t:none,nolog,setvar:tx.inbound_anomaly_score_threshold=5"
    name: coraza-waf
`

// CHARACTERIZATION. Pins that delegating the wrapper to
// BuildEnvoyExtensionPolicyK8sConfig produces byte-identical YAML to the
// inline body it replaces. The `want` value is captured from the CURRENT
// implementation before the change; see the report for how.
func TestGenerateEnvoyExtensionPolicyYAMLFromSnapshot_UnchangedByDelegation(t *testing.T) {
	route := fixtureRoute()
	domain := fixtureRouteDomain()
	policy := fixtureLuaPolicy()

	got := GenerateEnvoyExtensionPolicyYAMLFromSnapshot(route, domain, policy, nil, fixtureWAFConfig())

	require.NotEmpty(t, got)
	require.Equal(t, wantSnapshotYAML, got)
}

// WAF case, so the WAF branch of the delegation is pinned as well as the Lua
// branch.
func TestGenerateEnvoyExtensionPolicyYAMLFromSnapshot_UnchangedByDelegation_Waf(t *testing.T) {
	route := fixtureRoute()
	domain := fixtureRouteDomain()

	got := GenerateEnvoyExtensionPolicyYAMLFromSnapshot(route, domain, nil, fixtureWafPolicy(), fixtureWAFConfig())

	require.NotEmpty(t, got)
	require.Equal(t, wantSnapshotWafYAML, got)
}

// TestGenerateEnvoyExtensionPolicyYAMLFromSnapshot_EmptyConfigIsEmpty closes a
// coverage gap identified while deleting GenerateEnvoyExtensionPolicyYAMLFromDB
// and its tests: TestInternalGenerateEEPYAMLFromDB_EmptyConfig exercised a
// non-nil *models.EnvoyExtensionPolicy whose Config is the zero value (so
// IsEmpty() is true), and no surviving test passed that same shape through
// GenerateEnvoyExtensionPolicyYAMLFromSnapshot. See the report's coverage
// mapping for the full rationale.
func TestGenerateEnvoyExtensionPolicyYAMLFromSnapshot_EmptyConfigIsEmpty(t *testing.T) {
	route := fixtureRoute()
	domain := fixtureRouteDomain()
	policy := &models.EnvoyExtensionPolicy{Config: models.EnvoyExtensionPolicyConfig{}}

	got := GenerateEnvoyExtensionPolicyYAMLFromSnapshot(route, domain, policy, nil, fixtureWAFConfig())

	require.Empty(t, got)
}

// TestGenerateEnvoyExtensionPolicyYAMLWithWaf_EmptyInputIsEmpty closes the
// analogous gap for GenerateEnvoyExtensionPolicyYAML (deleted):
// TestInternalGenerateEEPYAML_EmptyInput exercised a non-nil
// *EnvoyExtensionPolicyInput{} (HasContent() false), and no surviving test
// passed that same shape through GenerateEnvoyExtensionPolicyYAMLWithWaf.
func TestGenerateEnvoyExtensionPolicyYAMLWithWaf_EmptyInputIsEmpty(t *testing.T) {
	route := fixtureRoute()
	domain := fixtureRouteDomain()

	got := GenerateEnvoyExtensionPolicyYAMLWithWaf(route, domain, &EnvoyExtensionPolicyInput{}, nil, fixtureWAFConfig())

	require.Empty(t, got)
}

// TestGenerateAPIKeyEnvoyExtensionPolicyYAML_NilPolicyIsEmpty and
// TestGenerateAPIKeyEnvoyExtensionPolicyYAML_Lua cover
// GenerateAPIKeyEnvoyExtensionPolicyYAML, which previously had zero test
// coverage anywhere in the repository (grep confirms no *_test.go referenced
// it before this phase).
func TestGenerateAPIKeyEnvoyExtensionPolicyYAML_NilPolicyIsEmpty(t *testing.T) {
	route := fixtureRoute()
	domain := fixtureRouteDomain()

	got := GenerateAPIKeyEnvoyExtensionPolicyYAML(route, domain, nil, route.K8sRouteName+"-ak-abcd1234")

	require.Empty(t, got)
}

func TestGenerateAPIKeyEnvoyExtensionPolicyYAML_Lua(t *testing.T) {
	route := fixtureRoute()
	domain := fixtureRouteDomain()
	clientRouteName := route.K8sRouteName + "-ak-abcd1234"

	got := GenerateAPIKeyEnvoyExtensionPolicyYAML(route, domain, fixtureLuaPolicy(), clientRouteName)

	require.NotEmpty(t, got)
	require.Contains(t, got, "EnvoyExtensionPolicy")
	require.Contains(t, got, clientRouteName)
}

// TestGoldenEnvoyExtensionPolicyWrapperYAML golden-tests the rendered YAML
// text of every surviving EnvoyExtensionPolicy wrapper. The 4 pre-existing
// routeplan goldens (envoyExtensionPolicyFixtures, in golden_test.go) only
// cover BuildEnvoyExtensionPolicyK8sConfig's config struct; these fixtures
// close the gap for the wrappers' actual string output, which nothing
// golden-tested before this phase.
//
// Regenerate with:
//
//	go test ./internal/routeplan/ -run TestGolden -update-golden
func TestGoldenEnvoyExtensionPolicyWrapperYAML(t *testing.T) {
	for _, tc := range envoyExtensionPolicyYAMLFixtures() {
		t.Run(tc.Name, func(t *testing.T) {
			got := tc.Build()
			require.NotEmpty(t, got, "fixture %s produced empty YAML", tc.Name)
			assertGolden(t, tc.Name, got)
		})
	}
}

// TestGoldenEnvoyExtensionPolicyWrapperYAMLFixtureNamesAreUnique guards
// against two wrapper fixtures silently sharing one golden file.
func TestGoldenEnvoyExtensionPolicyWrapperYAMLFixtureNamesAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, tc := range envoyExtensionPolicyYAMLFixtures() {
		require.False(t, seen[tc.Name], "duplicate fixture name %q", tc.Name)
		seen[tc.Name] = true
	}
}

//go:build e2e

package security

import "testing"

// TestGeneralModeOIDC ports security_general_mode/test_oidc.py, which is a
// pytest.mark.skip placeholder in the Python source
// ("OIDC requires external IdP — placeholder"; the test body is just
// `pass`). No IdP is available in this cluster/e2e setup, so this port
// carries the same skip forward rather than fabricating one.
func TestGeneralModeOIDC(t *testing.T) {
	t.Skip("OIDC requires an external IdP -- placeholder, matching security_general_mode/test_oidc.py")
}

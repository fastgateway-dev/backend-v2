//go:build e2e

package harness

import (
	"strconv"
	"strings"
	"testing"
)

// L4Supported reports whether the running Envoy Gateway version is >= 1.8, the
// verified line for L4 (TCP/UDP) BackendTrafficPolicy. An unknown or
// unparseable version returns false (skip), so an unlabeled environment never
// gives false confidence.
func L4Supported(egVersion string) bool {
	maj, min, ok := parseMajorMinor(egVersion)
	if !ok {
		return false
	}
	return maj > 1 || (maj == 1 && min >= 8)
}

// SkipIfL4Unsupported skips the test when the Envoy Gateway version is below the
// verified L4 line (see L4Supported).
func SkipIfL4Unsupported(t *testing.T, egVersion string) {
	t.Helper()
	if !L4Supported(egVersion) {
		t.Skipf("L4 streams require Envoy Gateway >= 1.8 (verified line); got %q — skipping", egVersion)
	}
}

func parseMajorMinor(v string) (int, int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}

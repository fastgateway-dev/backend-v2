//go:build e2e

package platform

import (
	"strings"
	"testing"
)

// firstDifference and stripGeneratedSuffix decide whether every preview
// assertion in this package passes or fails. If they are wrong in the
// permissive direction they pass everything silently, which is the exact
// failure mode the preview tests exist to prevent -- so they get their own
// tests. These are pure functions and touch no cluster.

func TestStripGeneratedSuffix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-route-a1b2c3d4", "my-route"},
		{"my-route-00000000", "my-route"},
		{"e2e-testpreview-1234abcd", "e2e-testpreview"},
		// Not an 8-char hex suffix: leave alone.
		{"my-route", "my-route"},
		{"my-route-abc", "my-route-abc"},
		{"my-route-a1b2c3d45", "my-route-a1b2c3d45"},
		{"my-route-ghijklmn", "my-route-ghijklmn"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripGeneratedSuffix(c.in); got != c.want {
			t.Errorf("stripGeneratedSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFirstDifference_AcceptsDeployedOnlyDefaults(t *testing.T) {
	// The CRD fills in group/kind on a backendRef. That must NOT fail:
	// the preview promised what would be sent, not what defaulting adds.
	preview := map[string]any{
		"spec": map[string]any{
			"rules": []any{map[string]any{
				"backendRefs": []any{map[string]any{"name": "nginx-service", "port": int64(80)}},
			}},
		},
	}
	deployed := map[string]any{
		"spec": map[string]any{
			"rules": []any{map[string]any{
				"backendRefs": []any{map[string]any{
					"name": "nginx-service", "port": int64(80),
					"group": "", "kind": "Service", "weight": int64(1),
				}},
			}},
		},
	}
	if d := firstDifference("", preview, deployed); d != "" {
		t.Fatalf("CRD defaults on the deployed side must be tolerated, got: %s", d)
	}
}

func TestFirstDifference_RejectsMissingPreviewField(t *testing.T) {
	// The reverse direction is the one that matters: the preview showed a
	// header modifier the cluster never got.
	preview := map[string]any{
		"spec": map[string]any{"rules": []any{map[string]any{"filters": []any{"x"}}}},
	}
	deployed := map[string]any{
		"spec": map[string]any{"rules": []any{map[string]any{}}},
	}
	d := firstDifference("", preview, deployed)
	if d == "" {
		t.Fatal("a preview field absent from the deployed object must fail")
	}
	if !strings.Contains(d, "filters") {
		t.Errorf("failure should name the missing field, got: %s", d)
	}
}

func TestFirstDifference_RejectsChangedValue(t *testing.T) {
	preview := map[string]any{"spec": map[string]any{"hostnames": []any{"api.example.com"}}}
	deployed := map[string]any{"spec": map[string]any{"hostnames": []any{"other.example.com"}}}
	d := firstDifference("", preview, deployed)
	if d == "" {
		t.Fatal("a differing value must fail")
	}
	if !strings.Contains(d, "api.example.com") {
		t.Errorf("failure should show the preview value, got: %s", d)
	}
}

func TestFirstDifference_RejectsDifferentListLength(t *testing.T) {
	// A preview showing two backends for a route that gets three is wrong
	// in exactly the way a reviewer would care about.
	preview := map[string]any{"backendRefs": []any{"a", "b"}}
	deployed := map[string]any{"backendRefs": []any{"a", "b", "c"}}
	if d := firstDifference("", preview, deployed); d == "" {
		t.Fatal("differing list lengths must fail")
	}
}

func TestFirstDifference_TolerationOfExplicitNulls(t *testing.T) {
	// sigs.k8s.io/yaml renders an unset pointer as an explicit null; the
	// API server drops the key entirely. That pair is not a discrepancy.
	preview := map[string]any{"status": nil, "spec": map[string]any{"x": int64(1)}}
	deployed := map[string]any{"spec": map[string]any{"x": int64(1)}}
	if d := firstDifference("", preview, deployed); d != "" {
		t.Fatalf("an explicit null in the preview must be tolerated, got: %s", d)
	}
	// But a non-nil preview field that is missing still fails.
	preview2 := map[string]any{"spec": map[string]any{"x": int64(1)}}
	deployed2 := map[string]any{}
	if firstDifference("", preview2, deployed2) == "" {
		t.Fatal("a missing non-nil preview field must fail")
	}
}

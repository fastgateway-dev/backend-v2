//go:build e2e

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
)

// The preview endpoints exist to answer one question before a change is
// approved: "what will actually be applied to the cluster?" A test that
// only checks the returned YAML parses, or contains the route's name,
// cannot tell a correct answer from a plausible-looking wrong one -- and a
// wrong answer here is worse than no answer, because a reviewer approves
// on the strength of it.
//
// So these helpers compare the preview against the object the cluster
// really ends up with, field by field.

// volatileMetadata are the metadata keys that cannot match between a
// preview and a deployed object, and must be dropped before comparing.
//
// A preview is generated for a route that does not exist yet, so
// RouteService.PreviewCreate mints a THROWAWAY uuid (see its tempRouteID)
// and derives the object name and the fastgateway.dev/route-id label from
// it. Those two are expected to differ. The rest -- uid, resourceVersion,
// generation, creationTimestamp, managedFields -- are assigned by the API
// server and exist only on the deployed side.
var volatileMetadata = []string{
	"uid", "resourceVersion", "generation", "creationTimestamp", "managedFields",
}

// parseYAMLObject decodes one YAML manifest into an unstructured object.
func parseYAMLObject(t *testing.T, label, manifest string) *unstructured.Unstructured {
	t.Helper()
	if strings.TrimSpace(manifest) == "" {
		t.Fatalf("%s: manifest is empty", label)
	}
	var obj map[string]interface{}
	if err := yaml.Unmarshal([]byte(manifest), &obj); err != nil {
		t.Fatalf("%s: parse manifest: %v\n%s", label, err, manifest)
	}
	return &unstructured.Unstructured{Object: obj}
}

// normalizeForComparison strips everything that legitimately differs
// between a preview and a live object: server-assigned metadata, status,
// and the generated-id-bearing name and route-id label.
//
// It returns the normalized object plus the name suffix it removed, so a
// caller can assert separately that both sides carried a suffix at all
// (an empty one would mean the preview is not modelling the real object
// name).
func normalizeForComparison(t *testing.T, obj *unstructured.Unstructured) *unstructured.Unstructured {
	t.Helper()
	out := obj.DeepCopy()

	unstructured.RemoveNestedField(out.Object, "status")
	for _, key := range volatileMetadata {
		unstructured.RemoveNestedField(out.Object, "metadata", key)
	}

	// The object name ends in "-<8 hex of the route uuid>", and the
	// route-id label is that uuid. Both differ by construction; blank them
	// rather than dropping them, so a MISSING name still fails.
	if name := out.GetName(); name != "" {
		out.SetName(stripGeneratedSuffix(name))
	}
	labels := out.GetLabels()
	if labels != nil {
		if _, ok := labels["fastgateway.dev/route-id"]; ok {
			labels["fastgateway.dev/route-id"] = "<generated>"
		}
		out.SetLabels(labels)
	}
	return out
}

// stripGeneratedSuffix replaces every 8-hex-digit segment of a Kubernetes
// object name with a placeholder, leaving the operator-chosen parts intact.
//
// Not just the trailing one: the generated id is not always last. A route's
// SecurityPolicy is named "<route>-<8 hex>-security", so the id sits in the
// middle, and a trailing-only rule left the two sides differing on a
// segment that is expected to differ.
//
// Replacing rather than deleting keeps the name's shape, so a preview that
// omitted the id entirely still fails. Both sides get the same treatment,
// so every non-generated segment must still match.
func stripGeneratedSuffix(name string) string {
	parts := strings.Split(name, "-")
	for i, part := range parts {
		if isHex8(part) {
			parts[i] = "<hex>"
		}
	}
	return strings.Join(parts, "-")
}

// isHex8 reports whether s is exactly 8 lowercase hex digits -- the shape
// of the id segments generateRouteK8sName and harness.UniqueName produce.
func isHex8(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// numericValue reports a JSON scalar's numeric value, and whether it was
// numeric at all.
//
// This exists because the two sides arrive as different Go types for the
// same number. sigs.k8s.io/yaml converts YAML to JSON and decodes into
// map[string]interface{}, so a port is a float64; the dynamic client's
// unstructured object holds an int64. reflect.DeepEqual says those differ
// while both print as "80" -- which is exactly how the first CI run
// reported it: "preview=80 deployed=80".
func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// requireSameObject fails t unless every field the preview declares is
// present, and equal, in the deployed object.
//
// SUBSET, not equality, and the asymmetry is the point. A CRD applies
// defaults on admission -- a backendRef gains `group: ""` and
// `kind: Service`, a path match gains its `type` -- so the live object
// legitimately carries fields the submitted manifest never had. Requiring
// equality would fail on those and teach everyone to loosen the test.
//
// The direction that matters is the one checked here: everything the
// reviewer was shown must actually be what the cluster got. A preview that
// drops a section, renames a backend, or changes a weight fails; a cluster
// that fills in a default it was always going to fill in does not.
func requireSameObject(t *testing.T, what string, want, got *unstructured.Unstructured) {
	t.Helper()
	w := normalizeForComparison(t, want)
	g := normalizeForComparison(t, got)
	if mismatch := firstDifference("", w.Object, g.Object); mismatch != "" {
		t.Errorf("%s: preview does not match what was deployed.\n    %s\n--- preview ---\n%s\n--- deployed ---\n%s",
			what, mismatch, mustJSON(t, w.Object), mustJSON(t, g.Object))
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("<unmarshalable: %v>", err)
	}
	return string(b)
}

// firstDifference walks the preview against the deployed object and
// reports the first path where the deployed object fails to carry what the
// preview declared, so a failure names the offending field instead of
// leaving the reader to diff two blobs by eye.
//
// Keys present only in the DEPLOYED object are ignored: those are CRD
// defaults (see requireSameObject).
func firstDifference(path string, want, got any) string {
	switch w := want.(type) {
	case map[string]interface{}:
		g, ok := got.(map[string]interface{})
		if !ok {
			return fmt.Sprintf("%s: preview has an object, deployed has %T", path, got)
		}
		keys := make([]string, 0, len(w))
		for k := range w {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			wv := w[k]
			gv, inG := g[k]
			if !inG {
				// A preview field the cluster does not have at all. Nil
				// is the one benign case: sigs.k8s.io/yaml renders an
				// unset pointer as an explicit null, which the API server
				// simply drops.
				if wv == nil {
					continue
				}
				return fmt.Sprintf("%s/%s: preview declares %v, deployed object has no such field", path, k, wv)
			}
			if d := firstDifference(path+"/"+k, wv, gv); d != "" {
				return d
			}
		}
		return ""
	case []interface{}:
		g, ok := got.([]interface{})
		if !ok {
			return fmt.Sprintf("%s: preview has a list, deployed has %T", path, got)
		}
		if len(w) != len(g) {
			return fmt.Sprintf("%s: preview has %d items, deployed has %d", path, len(w), len(g))
		}
		// Element counts must match exactly even though fields may not:
		// a preview showing two backends for a route that gets three is
		// wrong in the way that matters.
		for i := range w {
			if d := firstDifference(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); d != "" {
				return d
			}
		}
		return ""
	default:
		// Numbers first: see numericValue for why the two sides carry
		// different Go types for the same value.
		if wn, ok := numericValue(want); ok {
			if gn, ok := numericValue(got); ok {
				if wn != gn {
					return fmt.Sprintf("%s: preview=%v deployed=%v", path, want, got)
				}
				return ""
			}
			return fmt.Sprintf("%s: preview has number %v, deployed has %T (%v)", path, want, got, got)
		}
		if !reflect.DeepEqual(want, got) {
			return fmt.Sprintf("%s: preview=%v (%T) deployed=%v (%T)", path, want, want, got, got)
		}
		return ""
	}
}

// deployedObject fetches the live object of gvr owned by routeID.
func deployedObject(t *testing.T, ctx context.Context, gvr schema.GroupVersionResource, routeID string) *unstructured.Unstructured {
	t.Helper()
	obj, err := env.Kube.GetUnstructuredByLabel(ctx, gvr, env.Cfg.Namespace, "fastgateway.dev/route-id="+routeID)
	if err != nil {
		t.Fatalf("fetch deployed %s for route %s: %v", gvr.Resource, routeID, err)
	}
	return obj
}

// httpRouteGVR is the Gateway API HTTPRoute resource, for the preview
// tests' cluster reads.
var httpRouteGVR = harness.RouteGVR("http")

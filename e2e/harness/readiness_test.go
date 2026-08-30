//go:build e2e

package harness

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// These exercise WaitForRouteLive's pure control flow against a fake probe
// -- no cluster required, so they can (and must) run before any cluster
// exists. See go test -tags e2e ./e2e/harness/ -run TestWaitForRouteLive -v.

func TestWaitForRouteLive_ReturnsWhenServed(t *testing.T) {
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		if calls < 3 {
			return &Response{StatusCode: 404}, nil
		}
		return &Response{StatusCode: 200}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	if calls != 3 {
		t.Fatalf("probe called %d times, want 3", calls)
	}
}

func TestWaitForRouteLive_TimesOutOn404(t *testing.T) {
	probe := func(context.Context) (*Response, error) {
		return &Response{StatusCode: 404}, nil
	}
	_, err := WaitForRouteLive(context.Background(), probe, 3*time.Second)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestWaitForRouteLive_ReturnsNonServingStatusImmediately(t *testing.T) {
	// A non-404 status that isn't one of the "backend not resolved yet"
	// codes (500/502/503; see TestWaitForRouteLive_Retries5xxWithinGraceWindow
	// below) is returned immediately without retrying -- 404 alone means
	// "not programmed yet".
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		return &Response{StatusCode: 403}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("got %d, want 403", resp.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("probe called %d times, want 1", calls)
	}
}

// TestWaitForRouteLive_Retries5xxWithinGraceWindow proves a 500/502/503 is
// treated like a 404 -- retried, not returned -- as long as it keeps
// happening within backendGraceWindow. This is what protects
// traffic/external_backend_fqdn_test.go and external_backend_ip_test.go:
// an external (BackendTypeExternal) backendRef routes through a separate
// Backend CRD that reconciles after the HTTPRoute itself is programmed,
// and Gateway API mandates a 500 for any unresolved backendRef in that
// window.
func TestWaitForRouteLive_Retries5xxWithinGraceWindow(t *testing.T) {
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		if calls < 3 {
			return &Response{StatusCode: 500}, nil
		}
		return &Response{StatusCode: 200}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	if calls != 3 {
		t.Fatalf("probe called %d times, want 3", calls)
	}
}

// TestWaitForRouteLive_AcceptsPersistent5xxAfterGraceWindow proves that a
// 5xx which never clears is still eventually reported as the route's real,
// final status once backendGraceWindow elapses -- rather than being
// retried for the caller's ENTIRE timeout. This is what
// httproute/retry_test.go (final 503 after retries are exhausted) and
// httproute/fault_injection_test.go (fault-injected 503) rely on: their
// backend's error is genuine and permanent, not a transient
// backend-not-resolved-yet condition, so it must still surface.
func TestWaitForRouteLive_AcceptsPersistent5xxAfterGraceWindow(t *testing.T) {
	old := backendGraceWindow
	backendGraceWindow = 2 * time.Second
	defer func() { backendGraceWindow = old }()

	probe := func(context.Context) (*Response, error) {
		return &Response{StatusCode: 503}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Fatalf("got %d, want 503 (persistent backend error should eventually be reported as final)", resp.StatusCode)
	}
}

// TestWaitForRouteLive_HandlesNilResponseWithNilError proves a probe that
// returns (nil, nil) is treated as "not ready yet" and retried instead of
// panicking on a nil dereference of resp.StatusCode.
func TestWaitForRouteLive_HandlesNilResponseWithNilError(t *testing.T) {
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		if calls < 2 {
			return nil, nil
		}
		return &Response{StatusCode: 200}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
}

func TestWaitForRouteLive_RetriesOnProbeError(t *testing.T) {
	calls := 0
	probe := func(context.Context) (*Response, error) {
		calls++
		if calls < 2 {
			return nil, errProbeUnreachable
		}
		return &Response{StatusCode: 200}, nil
	}
	resp, err := WaitForRouteLive(context.Background(), probe, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
}

func TestWaitForRouteLive_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	probe := func(context.Context) (*Response, error) {
		cancel()
		return &Response{StatusCode: 404}, nil
	}
	_, err := WaitForRouteLive(ctx, probe, 30*time.Second)
	if err == nil {
		t.Fatal("expected an error from a cancelled context, got nil")
	}
}

type probeError struct{ msg string }

func (e *probeError) Error() string { return e.msg }

var errProbeUnreachable = &probeError{msg: "dial tcp: connection refused"}

// These exercise WaitForPolicyAccepted's pure control flow against a fake
// dynamic client -- no cluster required. See go test -tags e2e
// ./e2e/harness/ -run TestWaitForPolicyAccepted -v.

// newFakeKubeWith builds a *Kube backed by a fake dynamic client preloaded
// with objects, registered for List calls under gvr/listKind. Mirrors
// internal/services/kubernetes_service_versions_test.go's
// newFakeServiceWith.
func newFakeKubeWith(gvr schema.GroupVersionResource, listKind string, objects ...runtime.Object) *Kube {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		gvr: listKind,
	}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objects...)
	return &Kube{Dynamic: client}
}

// securityPolicyObject builds a minimal unstructured SecurityPolicy
// carrying the fastgateway.dev/route-id label WaitForPolicyAccepted
// resolves by, and (if ancestorConditions is non-nil) a
// status.ancestors[0].conditions populated from it.
func securityPolicyObject(ns, name, routeID string, ancestorConditions []map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "gateway.envoyproxy.io/v1alpha1",
		"kind":       "SecurityPolicy",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": ns,
			"labels": map[string]interface{}{
				"fastgateway.dev/route-id": routeID,
			},
		},
	}}
	if ancestorConditions != nil {
		conditions := make([]interface{}, 0, len(ancestorConditions))
		for _, c := range ancestorConditions {
			cond := map[string]interface{}{}
			for k, v := range c {
				cond[k] = v
			}
			conditions = append(conditions, cond)
		}
		obj.Object["status"] = map[string]interface{}{
			"ancestors": []interface{}{
				map[string]interface{}{
					"conditions": conditions,
				},
			},
		}
	}
	return obj
}

func TestWaitForPolicyAccepted_ReturnsWhenAccepted(t *testing.T) {
	obj := securityPolicyObject("fastgateway", "route-a-sp", "route-a", []map[string]any{
		{"type": "Accepted", "status": "True", "reason": "Accepted", "message": "SecurityPolicy has been accepted."},
	})
	k := newFakeKubeWith(SecurityPolicyGVR, "SecurityPolicyList", obj)

	err := WaitForPolicyAccepted(context.Background(), k, SecurityPolicyGVR, "fastgateway", "route-a", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForPolicyAccepted_TimesOutWhenObjectNeverExists(t *testing.T) {
	// No SecurityPolicy carries the label at all -- this is the state a
	// route lives in before deploySecurityPolicy runs, or if it never
	// runs. GetUnstructuredByLabel returns "no ... found", which must
	// surface as a timeout, not a panic or false accept.
	k := newFakeKubeWith(SecurityPolicyGVR, "SecurityPolicyList")

	err := WaitForPolicyAccepted(context.Background(), k, SecurityPolicyGVR, "fastgateway", "route-missing", 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "route-missing") {
		t.Fatalf("error %q should mention the route ID", err.Error())
	}
}

func TestWaitForPolicyAccepted_TimesOutWhenStatusNotYetPopulated(t *testing.T) {
	// The object exists (created) but Envoy Gateway hasn't reconciled it
	// yet, so status.ancestors is still empty. This is exactly the
	// unconverged window the fix targets: the object is present, but not
	// yet accepted.
	obj := securityPolicyObject("fastgateway", "route-a-sp", "route-a", nil)
	k := newFakeKubeWith(SecurityPolicyGVR, "SecurityPolicyList", obj)

	err := WaitForPolicyAccepted(context.Background(), k, SecurityPolicyGVR, "fastgateway", "route-a", 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "no status.ancestors reported yet") {
		t.Fatalf("error %q should explain status.ancestors was never populated", err.Error())
	}
}

func TestWaitForPolicyAccepted_TimesOutWhenConditionFalse_ReportsReasonAndMessage(t *testing.T) {
	obj := securityPolicyObject("fastgateway", "route-a-sp", "route-a", []map[string]any{
		{"type": "Accepted", "status": "False", "reason": "TargetNotFound", "message": "targetRef HTTPRoute route-a not found"},
	})
	k := newFakeKubeWith(SecurityPolicyGVR, "SecurityPolicyList", obj)

	err := WaitForPolicyAccepted(context.Background(), k, SecurityPolicyGVR, "fastgateway", "route-a", 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// The error must carry the reason and message so a caller can tell
	// "never accepted" apart from "accepted but not enforcing" -- they
	// need different follow-up.
	for _, want := range []string{"TargetNotFound", "targetRef HTTPRoute route-a not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should contain %q", err.Error(), want)
		}
	}
}

func TestWaitForPolicyAccepted_ReturnsAfterEventualAcceptance(t *testing.T) {
	// Simulates the real convergence path: the object starts unaccepted,
	// then a later Get (after the fake tracker is updated) reports
	// Accepted=True. WaitForPolicyAccepted must keep polling rather than
	// giving up on the first observation.
	obj := securityPolicyObject("fastgateway", "route-a-sp", "route-a", []map[string]any{
		{"type": "Accepted", "status": "False", "reason": "Pending", "message": "reconciling"},
	})
	k := newFakeKubeWith(SecurityPolicyGVR, "SecurityPolicyList", obj)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(500 * time.Millisecond)
		accepted := securityPolicyObject("fastgateway", "route-a-sp", "route-a", []map[string]any{
			{"type": "Accepted", "status": "True", "reason": "Accepted", "message": "SecurityPolicy has been accepted."},
		})
		_, _ = k.Dynamic.Resource(SecurityPolicyGVR).Namespace("fastgateway").Update(ctx, accepted, metav1.UpdateOptions{})
	}()

	err := WaitForPolicyAccepted(ctx, k, SecurityPolicyGVR, "fastgateway", "route-a", 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForPolicyAccepted_MultipleAncestorsRequiresAllTrue(t *testing.T) {
	// A policy attached via multiple parentRefs (e.g. sectioned
	// targetRefs) must have every ancestor accepted, not just one --
	// mirrors WaitForHTTPRouteAccepted's parentRefConditionStatus
	// semantics for status.parents.
	obj := securityPolicyObject("fastgateway", "route-a-sp", "route-a", nil)
	obj.Object["status"] = map[string]interface{}{
		"ancestors": []interface{}{
			map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Accepted", "status": "True", "reason": "Accepted", "message": "ok"},
				},
			},
			map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Accepted", "status": "False", "reason": "Pending", "message": "reconciling"},
				},
			},
		},
	}
	k := newFakeKubeWith(SecurityPolicyGVR, "SecurityPolicyList", obj)

	err := WaitForPolicyAccepted(context.Background(), k, SecurityPolicyGVR, "fastgateway", "route-a", 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error when one of two ancestors is not accepted, got nil")
	}
}

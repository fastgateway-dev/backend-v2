//go:build e2e

package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// httpRouteGVR identifies the Gateway API HTTPRoute resource for the
// dynamic client.
var httpRouteGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

// SecurityPolicyGVR, BackendTrafficPolicyGVR and EnvoyExtensionPolicyGVR
// identify the three Envoy Gateway policy CRDs the backend attaches to a
// route (RouteService.deploySecurityPolicy / deployBackendTrafficPolicy /
// deployEnvoyExtensionPolicy). They mirror the GVRs the backend itself
// builds from (internal/services/kubernetes_service.go's
// getSecurityPolicyGVR / getBackendTrafficPolicyGVR /
// getEnvoyExtensionPolicyGVR): group "gateway.envoyproxy.io", version
// "v1alpha1". Exported for use with WaitForPolicyAccepted from suite
// packages.
var (
	SecurityPolicyGVR = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "securitypolicies",
	}
	BackendTrafficPolicyGVR = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "backendtrafficpolicies",
	}
	EnvoyExtensionPolicyGVR = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyextensionpolicies",
	}
)

// backendGraceWindow bounds how long a 500/502/503 response is tolerated as
// "backend not resolved yet" rather than accepted as the route's genuine,
// final answer. Gateway API mandates a 500 response for any parentRef whose
// backendRef is not yet resolvable (see external_backend_fqdn_test.go /
// external_backend_ip_test.go, which use BackendTypeExternal and therefore
// go through a separate Backend CRD that reconciles after the HTTPRoute
// itself is programmed); that window is normally a few seconds.
//
// The clock starts only once such a status is first observed (not from
// WaitForRouteLive's own start), so routes whose backend legitimately and
// consistently answers with a 5xx of its own -- e.g.
// httproute/retry_test.go's final 503 after retries are exhausted, or
// httproute/fault_injection_test.go's fault-injected 503 -- still get that
// real status reported once the window elapses, rather than spinning for
// the caller's entire timeout.
//
// Declared as a var (not a const) so tests can shrink it instead of
// waiting out the real production window.
var backendGraceWindow = 15 * time.Second

// WaitForRouteLive polls until the gateway actually serves the route.
//
// A route accepted by the API is not immediately servable: Envoy Gateway must
// reconcile the HTTPRoute and push config to the proxy. Until then the gateway
// answers 404. Waiting here is what lets every caller assert its real expected
// status instead of tolerating 404 -- which is what made 33 tests in the
// predecessor suite unable to fail.
//
// A 500/502/503 is treated the same way, but only for up to
// backendGraceWindow from when it first appears: those statuses are also
// what an UNRESOLVED backendRef produces (see backendGraceWindow's doc),
// so treating the very first one as "live" risks reporting that transient
// error as the route's real behavior. Once the window elapses, a
// persistent 5xx is returned like any other final response.
func WaitForRouteLive(
	ctx context.Context,
	probe func(context.Context) (*Response, error),
	timeout time.Duration,
) (*Response, error) {
	deadline := time.Now().Add(timeout)
	var last string
	var graceDeadline time.Time

	for time.Now().Before(deadline) {
		resp, err := probe(ctx)
		switch {
		case err != nil || resp == nil:
			last = fmt.Sprintf("error: %v", err)
		case resp.StatusCode == 404:
			last = "HTTP 404 (route not programmed yet)"
		case resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503:
			if graceDeadline.IsZero() {
				graceDeadline = time.Now().Add(backendGraceWindow)
			}
			if time.Now().Before(graceDeadline) {
				last = fmt.Sprintf("HTTP %d (backend not resolved yet)", resp.StatusCode)
			} else {
				return resp, nil
			}
		default:
			return resp, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("route did not become live within %s (last: %s)", timeout, last)
}

// WaitForHTTPRouteAccepted polls the HTTPRoute owned by routeID's status
// until every parentRef reports both "Accepted" and "ResolvedRefs" as
// True, or returns an error carrying the observed condition messages once
// timeout elapses.
//
// The HTTPRoute is resolved by the "fastgateway.dev/route-id" label
// (RouteService.buildHTTPRouteConfig / BuildHTTPRoute set it to
// route.ID.String()), not by name: the backend's actual object name is
// route.K8sRouteName ("<name>-<8 hex chars of the route UUID>"), a field
// tagged `json:"-"` on models.Route and therefore never available to an
// API client. ns must be the domain's own namespace (models.Domain.Namespace,
// e.g. Env.Cfg.Namespace) -- the HTTPRoute lives there, not in the
// backend's route namespace.
//
// This is the control-plane counterpart to WaitForRouteLive: it gates on
// the Gateway API's own status conditions instead of only probing the data
// plane, which is what the predecessor suite's 113 retry_until calls and 52
// raw sleeps never did.
func WaitForHTTPRouteAccepted(ctx context.Context, k *Kube, ns, routeID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	labelSelector := fmt.Sprintf("fastgateway.dev/route-id=%s", routeID)

	for time.Now().Before(deadline) {
		obj, err := k.GetUnstructuredByLabel(ctx, httpRouteGVR, ns, labelSelector)
		if err != nil {
			last = fmt.Sprintf("error: %v", err)
		} else {
			acceptedOK, acceptedMsg := parentRefConditionStatus(obj, "Accepted")
			resolvedOK, resolvedMsg := parentRefConditionStatus(obj, "ResolvedRefs")
			if acceptedOK && resolvedOK {
				return nil
			}
			last = fmt.Sprintf("%s; %s", acceptedMsg, resolvedMsg)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("HTTPRoute for route %s (label %s) in %s not accepted within %s (last: %s)", routeID, labelSelector, ns, timeout, last)
}

// parentRefConditionStatus reports whether every parentRef entry in
// status.parents[] carries a condType condition with status "True". It
// returns false with no parents reported yet (status not populated), and
// false if any parentRef is missing the condition or reports non-True.
func parentRefConditionStatus(obj *unstructured.Unstructured, condType string) (bool, string) {
	parents, found, err := unstructured.NestedSlice(obj.Object, "status", "parents")
	if err != nil || !found || len(parents) == 0 {
		return false, fmt.Sprintf("%s=<no status.parents reported yet>", condType)
	}

	allTrue := true
	var messages []string
	for i, p := range parents {
		parent, ok := p.(map[string]any)
		if !ok {
			continue
		}
		conditions, found, _ := unstructured.NestedSlice(parent, "conditions")
		if !found {
			allTrue = false
			messages = append(messages, fmt.Sprintf("parent[%d].%s=<no conditions>", i, condType))
			continue
		}

		matched := false
		for _, c := range conditions {
			cond, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if cond["type"] != condType {
				continue
			}
			matched = true
			status, _ := cond["status"].(string)
			msg, _ := cond["message"].(string)
			if status != "True" {
				allTrue = false
			}
			messages = append(messages, fmt.Sprintf("parent[%d].%s=%s(%s)", i, condType, status, msg))
		}
		if !matched {
			allTrue = false
			messages = append(messages, fmt.Sprintf("parent[%d].%s=<missing>", i, condType))
		}
	}
	return allTrue, strings.Join(messages, ", ")
}

// WaitForPolicyAccepted polls the Envoy Gateway policy object (a
// SecurityPolicy, BackendTrafficPolicy, or EnvoyExtensionPolicy -- pass
// the matching GVR: SecurityPolicyGVR, BackendTrafficPolicyGVR, or
// EnvoyExtensionPolicyGVR) owned by routeID's status until every ancestor
// reports "Accepted" as True, or returns an error carrying the observed
// conditions (reason + message) once timeout elapses.
//
// The policy is resolved by the "fastgateway.dev/route-id" label, the
// same mechanism WaitForHTTPRouteAccepted uses for HTTPRoute -- see
// RouteService's deploySecurityPolicy / deployBackendTrafficPolicy /
// deployEnvoyExtensionPolicy (internal/services/route_service.go) and the
// labels BuildSecurityPolicy / BuildBackendTrafficPolicy /
// BuildEnvoyExtensionPolicy stamp on the object
// (internal/services/kubernetes_service.go). ns must be the domain's own
// namespace, matching WaitForHTTPRouteAccepted's ns argument.
//
// This exists because deploying a route and its policies as separate
// Kubernetes objects (CreateHTTPRoute, then deploySecurityPolicy /
// deployBackendTrafficPolicy / deployEnvoyExtensionPolicy) leaves a window
// where Envoy Gateway has programmed the HTTPRoute but not yet the
// policy: the route serves traffic completely unprotected/unpolicied
// during that window. WaitForHTTPRouteAccepted alone cannot detect this --
// it only tells you the route exists, not that its policy is attached and
// enforcing. Callers that assert route behavior contingent on a policy
// (a SecurityPolicy denying unauthenticated requests, a
// BackendTrafficPolicy enforcing a timeout or rate limit) must wait on
// this in addition, or their gate is satisfied by exactly the unconverged
// state that makes the assertion fail.
//
// Note "Accepted" on its own only proves the policy object was admitted
// by the API and targets a real ancestor -- it does not by itself prove
// Envoy has finished pushing the corresponding xDS config. Callers with a
// data-plane probe available (e.g. expecting a 401/403 once a
// SecurityPolicy is enforcing) should still gate the final assertion on
// that probe reaching its expected denial; WaitForPolicyAccepted narrows
// the race rather than eliminating every last millisecond of it. A
// timeout error here distinguishes "policy never accepted" (no Accepted
// condition, or status False/Unknown -- see the returned reason) from
// "policy accepted but caller's own probe still isn't observing it", since
// the two need different follow-up.
func WaitForPolicyAccepted(ctx context.Context, k *Kube, gvr schema.GroupVersionResource, ns, routeID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	labelSelector := fmt.Sprintf("fastgateway.dev/route-id=%s", routeID)

	for time.Now().Before(deadline) {
		obj, err := k.GetUnstructuredByLabel(ctx, gvr, ns, labelSelector)
		if err != nil {
			last = fmt.Sprintf("error: %v", err)
		} else {
			ok, msg := ancestorConditionStatus(obj, "Accepted")
			if ok {
				return nil
			}
			last = msg
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("%s for route %s (label %s) in %s not accepted within %s (last: %s)", gvr.Resource, routeID, labelSelector, ns, timeout, last)
}

// ancestorConditionStatus reports whether every ancestor entry in
// status.ancestors[] (the GEP-713 PolicyStatus shape Envoy Gateway's
// policy CRDs use) carries a condType condition with status "True". It
// returns false with no ancestors reported yet (status not populated),
// and false if any ancestor is missing the condition or reports
// non-True. The returned message includes each condition's reason and
// message text, since "not accepted" and "accepted but not enforcing"
// surface as different reasons and need different follow-up.
func ancestorConditionStatus(obj *unstructured.Unstructured, condType string) (bool, string) {
	ancestors, found, err := unstructured.NestedSlice(obj.Object, "status", "ancestors")
	if err != nil || !found || len(ancestors) == 0 {
		return false, fmt.Sprintf("%s=<no status.ancestors reported yet>", condType)
	}

	allTrue := true
	var messages []string
	for i, a := range ancestors {
		ancestor, ok := a.(map[string]any)
		if !ok {
			continue
		}
		conditions, found, _ := unstructured.NestedSlice(ancestor, "conditions")
		if !found {
			allTrue = false
			messages = append(messages, fmt.Sprintf("ancestor[%d].%s=<no conditions>", i, condType))
			continue
		}

		matched := false
		for _, c := range conditions {
			cond, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if cond["type"] != condType {
				continue
			}
			matched = true
			status, _ := cond["status"].(string)
			reason, _ := cond["reason"].(string)
			msg, _ := cond["message"].(string)
			if status != "True" {
				allTrue = false
			}
			messages = append(messages, fmt.Sprintf("ancestor[%d].%s=%s(reason=%s, message=%s)", i, condType, status, reason, msg))
		}
		if !matched {
			allTrue = false
			messages = append(messages, fmt.Sprintf("ancestor[%d].%s=<missing>", i, condType))
		}
	}
	return allTrue, strings.Join(messages, ", ")
}

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

// WaitForRouteLive polls until the gateway actually serves the route.
//
// A route accepted by the API is not immediately servable: Envoy Gateway must
// reconcile the HTTPRoute and push config to the proxy. Until then the gateway
// answers 404. Waiting here is what lets every caller assert its real expected
// status instead of tolerating 404 -- which is what made 33 tests in the
// predecessor suite unable to fail.
func WaitForRouteLive(
	ctx context.Context,
	probe func(context.Context) (*Response, error),
	timeout time.Duration,
) (*Response, error) {
	deadline := time.Now().Add(timeout)
	var last string

	for time.Now().Before(deadline) {
		resp, err := probe(ctx)
		switch {
		case err != nil:
			last = fmt.Sprintf("error: %v", err)
		case resp.StatusCode == 404:
			last = "HTTP 404 (route not programmed yet)"
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

// WaitForHTTPRouteAccepted polls the named HTTPRoute's status until every
// parentRef reports both "Accepted" and "ResolvedRefs" as True, or returns
// an error carrying the observed condition messages once timeout elapses.
//
// This is the control-plane counterpart to WaitForRouteLive: it gates on
// the Gateway API's own status conditions instead of only probing the data
// plane, which is what the predecessor suite's 113 retry_until calls and 52
// raw sleeps never did.
func WaitForHTTPRouteAccepted(ctx context.Context, k *Kube, ns, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string

	for time.Now().Before(deadline) {
		obj, err := k.GetUnstructured(ctx, httpRouteGVR, ns, name)
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
	return fmt.Errorf("HTTPRoute %s/%s not accepted within %s (last: %s)", ns, name, timeout, last)
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

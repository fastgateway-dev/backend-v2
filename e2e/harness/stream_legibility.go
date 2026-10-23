//go:build e2e

package harness

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// GetStreamEntrypoint fetches GET /projects/:projectId/streams/:streamId/entrypoint.
func (a *API) GetStreamEntrypoint(ctx context.Context, projectID, streamID string) (services.StreamEntrypoint, error) {
	var out services.StreamEntrypoint
	_, err := a.Do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/streams/%s/entrypoint", projectID, streamID), nil, &out)
	return out, err
}

// GetStreamYAMLs fetches GET /projects/:projectId/streams/:streamId/yamls.
func (a *API) GetStreamYAMLs(ctx context.Context, projectID, streamID string) (services.StreamYAMLs, error) {
	var out services.StreamYAMLs
	_, err := a.Do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/streams/%s/yamls", projectID, streamID), nil, &out)
	return out, err
}

// ListStreamRouteVersions lists an L4 route's version history through the
// stream-scoped endpoint (same flat envelope as ListRouteVersions).
func (a *API) ListStreamRouteVersions(ctx context.Context, projectID, streamID, routeID string) ([]models.RouteVersion, int64, error) {
	var env routeVersionEnvelope
	path := fmt.Sprintf("/projects/%s/streams/%s/routes/%s/versions", projectID, streamID, routeID)
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &env); err != nil {
		return nil, 0, err
	}
	return env.Data, env.Total, nil
}

// UpdateStreamRoute submits an update of an L4 route (approval-gated).
func (a *API) UpdateStreamRoute(ctx context.Context, projectID, streamID, routeID string, body any) (Route, error) {
	var out Route
	_, err := a.Do(ctx, http.MethodPut, fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID), body, &out)
	return out, err
}

// PendingRouteApprovalID returns the ID of the pending approval for routeID.
func (a *API) PendingRouteApprovalID(ctx context.Context, projectID, routeID string) (string, error) {
	approvals, err := a.pendingApprovals(ctx, projectID, "")
	if err != nil {
		return "", err
	}
	for _, appr := range approvals {
		if appr.EntityID.String() == routeID {
			return appr.ID.String(), nil
		}
	}
	return "", fmt.Errorf("no pending approval found for route %s", routeID)
}

// GetApprovalDiff fetches GET /projects/:projectId/approvals/:approvalId/diff.
func (a *API) GetApprovalDiff(ctx context.Context, projectID, approvalID string) (services.ApprovalDiffResult, error) {
	var out services.ApprovalDiffResult
	_, err := a.Do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/approvals/%s/diff", projectID, approvalID), nil, &out)
	return out, err
}

// GatewayListener is the identity of one Gateway listener.
type GatewayListener struct {
	Name     string
	Protocol string
	Port     int64
}

// GatewayListeners returns spec.listeners of a Gateway object as sorted
// (name, protocol, port) identities. Only the identity is compared: the API
// server defaults other listener fields (e.g. allowedRoutes) on the live
// object, which the rendered manifest does not carry.
func GatewayListeners(obj map[string]interface{}) ([]GatewayListener, error) {
	raw, found, err := unstructured.NestedSlice(obj, "spec", "listeners")
	if err != nil || !found {
		return nil, fmt.Errorf("gateway has no spec.listeners (found=%v): %v", found, err)
	}
	out := make([]GatewayListener, 0, len(raw))
	for _, item := range raw {
		l, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("listener is %T, want an object", item)
		}
		name, _, _ := unstructured.NestedString(l, "name")
		proto, _, _ := unstructured.NestedString(l, "protocol")
		port, _, _ := unstructured.NestedFieldNoCopy(l, "port")
		var p int64
		switch v := port.(type) {
		case int64:
			p = v
		case float64:
			p = int64(v)
		case int:
			p = int64(v)
		}
		out = append(out, GatewayListener{Name: name, Protocol: proto, Port: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GatewayYAMLListeners is GatewayListeners for a rendered Gateway YAML.
func GatewayYAMLListeners(gatewayYAML string) ([]GatewayListener, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal([]byte(gatewayYAML), &obj); err != nil {
		return nil, fmt.Errorf("parse gateway YAML: %w", err)
	}
	return GatewayListeners(obj)
}

// WaitForGatewayListeners polls the live Gateway until its listeners (name,
// protocol, port) equal want (sorted by name, as GatewayListeners returns
// them), or fails once timeout elapses with the last observed listeners.
func WaitForGatewayListeners(ctx context.Context, k *Kube, ns, name string, want []GatewayListener, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		obj, err := k.GetUnstructured(ctx, kubernetes.GatewayGVR, ns, name)
		if err != nil {
			last = fmt.Sprintf("error: %v", err)
		} else if got, lerr := GatewayListeners(obj.Object); lerr != nil {
			last = fmt.Sprintf("error: %v", lerr)
		} else if reflect.DeepEqual(got, want) {
			return nil
		} else {
			last = fmt.Sprintf("%v", got)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("gateway %s/%s listeners never became %v within %s (last: %s)", ns, name, want, timeout, last)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

// WaitForGatewayConditions polls the live Gateway until every condType in
// condTypes (e.g. "Accepted", "Programmed") has status True, or fails once
// timeout elapses with the last observed state.
func WaitForGatewayConditions(ctx context.Context, k *Kube, ns, name string, timeout time.Duration, condTypes ...string) error {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		obj, err := k.GetUnstructured(ctx, kubernetes.GatewayGVR, ns, name)
		if err != nil {
			last = fmt.Sprintf("error: %v", err)
		} else {
			conds, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
			status := map[string]string{}
			for _, c := range conds {
				if m, ok := c.(map[string]interface{}); ok {
					t, _ := m["type"].(string)
					st, _ := m["status"].(string)
					status[t] = st
				}
			}
			all := true
			for _, ct := range condTypes {
				if status[ct] != "True" {
					all = false
				}
			}
			if all {
				return nil
			}
			last = fmt.Sprintf("%v", status)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("gateway %s/%s conditions %v never all True within %s (last: %s)", ns, name, condTypes, timeout, last)
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

// WaitForGatewayListenerConditions polls the live Gateway until every listener
// in names appears in status.listeners with Accepted=True and Programmed=True
// (and ResolvedRefs=True when the controller reports it), or fails once timeout
// elapses with the last observed per-listener conditions. A single listener's
// rejection is reported here, not in the Gateway-level conditions.
func WaitForGatewayListenerConditions(ctx context.Context, k *Kube, ns, name string, names []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		obj, err := k.GetUnstructured(ctx, kubernetes.GatewayGVR, ns, name)
		if err != nil {
			last = fmt.Sprintf("error: %v", err)
		} else {
			listeners, _, _ := unstructured.NestedSlice(obj.Object, "status", "listeners")
			observed := map[string]map[string]string{}
			for _, l := range listeners {
				lm, ok := l.(map[string]interface{})
				if !ok {
					continue
				}
				ln, _ := lm["name"].(string)
				conds := map[string]string{}
				cs, _ := lm["conditions"].([]interface{})
				for _, c := range cs {
					if cm, ok := c.(map[string]interface{}); ok {
						t, _ := cm["type"].(string)
						st, _ := cm["status"].(string)
						conds[t] = st
					}
				}
				observed[ln] = conds
			}
			all := true
			for _, n := range names {
				conds, ok := observed[n]
				if !ok || conds["Accepted"] != "True" || conds["Programmed"] != "True" {
					all = false
					break
				}
				if rr, has := conds["ResolvedRefs"]; has && rr != "True" {
					all = false
					break
				}
			}
			if all {
				return nil
			}
			last = fmt.Sprintf("%v", observed)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("gateway %s/%s listeners %v never all Accepted+Programmed within %s (last status.listeners conditions: %s)", ns, name, names, timeout, last)
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

// WaitForGatewayGone polls until the Gateway no longer exists, or fails once
// timeout elapses.
func WaitForGatewayGone(ctx context.Context, k *Kube, ns, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		_, err := k.GetUnstructured(ctx, kubernetes.GatewayGVR, ns, name)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			last = fmt.Sprintf("error: %v", err)
		} else {
			last = "still present"
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("gateway %s/%s not deleted within %s (last: %s)", ns, name, timeout, last)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

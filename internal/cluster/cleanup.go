package cluster

import (
	"context"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/google/uuid"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// DeleteStaleAPIKeyResources deletes orphaned per-client HTTPRoutes, SecurityPolicies, and BackendTrafficPolicies
// that are no longer needed (i.e., their client prefixes are not in expectedClientPrefixes)
func (s *Client) DeleteStaleAPIKeyResources(ctx context.Context, projectID uuid.UUID, namespace, routeID, baseRouteName string, expectedClientPrefixes map[string]bool) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	// Use the expected prefixes map directly for fast lookup
	expectedSet := expectedClientPrefixes

	// Delete stale HTTPRoutes
	httpRouteGVR := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}
	if err := s.deleteStalePerClientResources(ctx, client, httpRouteGVR, namespace, routeID, baseRouteName, expectedSet); err != nil {
		return fmt.Errorf("failed to delete stale httproutes: %w", err)
	}

	// Delete stale GRPCRoutes
	grpcRouteGVR := getGRPCRouteGVR()
	if err := s.deleteStalePerClientResources(ctx, client, grpcRouteGVR, namespace, routeID, baseRouteName, expectedSet); err != nil {
		return fmt.Errorf("failed to delete stale grpcroutes: %w", err)
	}

	// Delete stale SecurityPolicies
	securityPolicyGVR := schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "securitypolicies",
	}
	if err := s.deleteStalePerClientResources(ctx, client, securityPolicyGVR, namespace, routeID, baseRouteName, expectedSet); err != nil {
		return fmt.Errorf("failed to delete stale securitypolicies: %w", err)
	}

	// Delete stale BackendTrafficPolicies
	btpGVR := schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "backendtrafficpolicies",
	}
	if err := s.deleteStalePerClientResources(ctx, client, btpGVR, namespace, routeID, baseRouteName, expectedSet); err != nil {
		return fmt.Errorf("failed to delete stale backendtrafficpolicies: %w", err)
	}

	return nil
}

// deleteStalePerClientResources deletes per-client resources that are no longer needed
func (s *Client) deleteStalePerClientResources(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, routeID, baseRouteName string, expectedPrefixes map[string]bool) error {
	// List resources by route ID label
	labelSelector := kubernetes.SelectorRouteID(routeID)
	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list resources: %w", err)
	}

	// Check each resource to see if it's a per-client resource that should be deleted
	for _, item := range list.Items {
		name := item.GetName()

		// Check if this is a per-client resource (contains "-ak-")
		if !kubernetes.IsPerClientResource(name) {
			continue // Skip base resources
		}

		// Extract the client prefix from the name
		clientPrefix := kubernetes.ExtractClientPrefix(name, baseRouteName)
		if clientPrefix == "" {
			continue // Couldn't extract prefix, skip
		}

		// Check if this prefix is expected
		if expectedPrefixes[clientPrefix] {
			continue // This client is still attached, keep the resource
		}

		// Delete the stale resource
		err := client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete stale resource %s: %w", name, err)
		}
	}

	return nil
}

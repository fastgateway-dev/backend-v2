package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CreateBackend creates an Envoy Gateway Backend resource in Kubernetes
func (s *Client) CreateBackend(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	backend := kubernetes.BuildBackend(config)
	gvr := kubernetes.BackendGVR

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, backend, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create backend: %w", err)
	}

	return nil
}

// UpdateBackend updates an Envoy Gateway Backend resource in Kubernetes
func (s *Client) UpdateBackend(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.BackendGVR

	// Get the existing Backend to preserve resourceVersion
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		// If not found, create it
		if strings.Contains(err.Error(), "not found") {
			return s.CreateBackend(ctx, projectID, config)
		}
		return fmt.Errorf("failed to get existing backend: %w", err)
	}

	backend := kubernetes.BuildBackend(config)
	backend.SetResourceVersion(existing.GetResourceVersion())
	backend.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, backend, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update backend: %w", err)
	}

	return nil
}

// DeleteBackend deletes an Envoy Gateway Backend resource from Kubernetes
func (s *Client) DeleteBackend(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.BackendGVR

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		// Ignore not found errors
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete backend: %w", err)
	}

	return nil
}

// UpdateBackendUnstructured creates or updates an Envoy Gateway Backend resource from unstructured object
func (s *Client) UpdateBackendUnstructured(ctx context.Context, projectID uuid.UUID, backend *unstructured.Unstructured) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.BackendGVR
	namespace := backend.GetNamespace()
	name := backend.GetName()

	// Get the existing Backend to preserve resourceVersion
	existing, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		// If not found, create it
		if strings.Contains(err.Error(), "not found") {
			_, createErr := client.Resource(gvr).Namespace(namespace).Create(ctx, backend, metav1.CreateOptions{})
			if createErr != nil {
				return fmt.Errorf("failed to create backend: %w", createErr)
			}
			return nil
		}
		return fmt.Errorf("failed to get existing backend: %w", err)
	}

	backend.SetResourceVersion(existing.GetResourceVersion())
	backend.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(namespace).Update(ctx, backend, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update backend: %w", err)
	}

	return nil
}

// DeleteBackendsByRoute deletes all Envoy Gateway Backend resources associated with a route
func (s *Client) DeleteBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.BackendGVR

	// List backends with the route label
	labelSelector := kubernetes.SelectorRouteID(routeID)
	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		// If the CRD doesn't exist, ignore the error
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "the server could not find") {
			return nil
		}
		return fmt.Errorf("failed to list backends: %w", err)
	}

	// Delete each backend
	for _, item := range list.Items {
		err = client.Resource(gvr).Namespace(namespace).Delete(ctx, item.GetName(), metav1.DeleteOptions{})
		if err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("failed to delete backend %s: %w", item.GetName(), err)
		}
	}

	return nil
}

// DeleteStaleBackendsByRoute deletes Backend CRDs for a route that are not in the expectedNames set.
// This allows updating external backends without deleting and recreating unchanged ones.
func (s *Client) DeleteStaleBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string, expectedNames map[string]bool) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.BackendGVR

	// List backends with the route label
	labelSelector := kubernetes.SelectorRouteID(routeID)
	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		// If the CRD doesn't exist, ignore the error
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "the server could not find") {
			return nil
		}
		return fmt.Errorf("failed to list backends: %w", err)
	}

	// Delete only backends that are no longer expected
	for _, item := range list.Items {
		if !expectedNames[item.GetName()] {
			err = client.Resource(gvr).Namespace(namespace).Delete(ctx, item.GetName(), metav1.DeleteOptions{})
			if err != nil && !strings.Contains(err.Error(), "not found") {
				return fmt.Errorf("failed to delete stale backend %s: %w", item.GetName(), err)
			}
		}
	}

	return nil
}

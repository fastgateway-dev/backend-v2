package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/google/uuid"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// getGRPCRouteGVR returns the GroupVersionResource for Gateway API GRPCRoute
func getGRPCRouteGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "grpcroutes",
	}
}

// CreateHTTPRoute creates an HTTPRoute resource in Kubernetes.
// If the resource already exists (e.g. from a partial previous deploy), it falls back to update.
func (s *Client) CreateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	// Build typed HTTPRoute object
	httpRoute := kubernetes.BuildHTTPRouteObject(config)

	// Convert to unstructured for dynamic client
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(httpRoute)
	if err != nil {
		return fmt.Errorf("failed to convert HTTPRoute to unstructured: %w", err)
	}

	unstructuredRoute := &unstructured.Unstructured{Object: unstructuredObj}

	_, createErr := client.Resource(gvr).Namespace(config.Namespace).Create(ctx, unstructuredRoute, metav1.CreateOptions{})
	if createErr != nil {
		if k8serrors.IsAlreadyExists(createErr) {
			// Resource already exists (partial previous deploy), fall back to update
			return s.UpdateHTTPRoute(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create httproute: %w", createErr)
	}
	return nil
}

// UpdateHTTPRoute updates an HTTPRoute resource in Kubernetes using a proper update (not delete+create)
func (s *Client) UpdateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	// Get the existing HTTPRoute to preserve resourceVersion and other metadata
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing httproute: %w", err)
	}

	// Build the new typed HTTPRoute object
	httpRoute := kubernetes.BuildHTTPRouteObject(config)

	// Convert to unstructured for dynamic client
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(httpRoute)
	if err != nil {
		return fmt.Errorf("failed to convert HTTPRoute to unstructured: %w", err)
	}

	unstructuredRoute := &unstructured.Unstructured{Object: unstructuredObj}

	// Preserve the resourceVersion from existing object (required for update)
	unstructuredRoute.SetResourceVersion(existing.GetResourceVersion())
	// Preserve UID to ensure we're updating the same object
	unstructuredRoute.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, unstructuredRoute, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update httproute: %w", err)
	}

	return nil
}

// DeleteHTTPRoute deletes an HTTPRoute resource from Kubernetes
func (s *Client) DeleteHTTPRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// HTTPRoute not found, already deleted
			return nil
		}
		return fmt.Errorf("failed to delete httproute: %w", err)
	}

	return nil
}

// CreateGRPCRoute creates a GRPCRoute in Kubernetes
func (s *Client) CreateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.GRPCRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getGRPCRouteGVR()

	grpcRoute := kubernetes.BuildGRPCRouteObject(config)
	if grpcRoute == nil {
		return fmt.Errorf("failed to build GRPCRoute object")
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(grpcRoute)
	if err != nil {
		return fmt.Errorf("failed to convert GRPCRoute to unstructured: %w", err)
	}

	obj := &unstructured.Unstructured{Object: unstructuredObj}
	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return s.UpdateGRPCRoute(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create GRPCRoute: %w", err)
	}
	return nil
}

// UpdateGRPCRoute updates a GRPCRoute in Kubernetes
func (s *Client) UpdateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *kubernetes.GRPCRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getGRPCRouteGVR()

	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing GRPCRoute: %w", err)
	}

	grpcRoute := kubernetes.BuildGRPCRouteObject(config)
	if grpcRoute == nil {
		return fmt.Errorf("failed to build GRPCRoute object")
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(grpcRoute)
	if err != nil {
		return fmt.Errorf("failed to convert GRPCRoute to unstructured: %w", err)
	}

	obj := &unstructured.Unstructured{Object: unstructuredObj}
	obj.SetResourceVersion(existing.GetResourceVersion())
	obj.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update GRPCRoute: %w", err)
	}
	return nil
}

// DeleteGRPCRoute deletes a GRPCRoute from Kubernetes
func (s *Client) DeleteGRPCRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getGRPCRouteGVR()
	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// GRPCRoute not found, already deleted
			return nil
		}
		return err
	}
	return nil
}

// ReferenceGrantConfig represents ReferenceGrant configuration
type ReferenceGrantConfig struct {
	Name           string   // Name of the ReferenceGrant
	FromNamespaces []string // Namespaces where Gateways and routes are deployed
	ToNamespace    string   // Namespace where the referenced resources reside
	ToKinds        []string // Core/v1 kinds permitted as targets (e.g. "Service", "Secret"). Empty = both.
}

// CreateReferenceGrant creates a ReferenceGrant allowing resources from multiple namespaces to reference services and/or secrets in the target namespace
func (s *Client) CreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *ReferenceGrantConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1beta1",
		Resource: "referencegrants",
	}

	// Build "from" entries for each source namespace and resource kind
	type fromEntry struct {
		group string
		kind  string
	}
	kinds := []fromEntry{
		{"gateway.networking.k8s.io", "HTTPRoute"},
		{"gateway.networking.k8s.io", "GRPCRoute"},
		{"gateway.envoyproxy.io", "SecurityPolicy"},
		{"gateway.envoyproxy.io", "EnvoyExtensionPolicy"},
		{"gateway.networking.k8s.io", "Gateway"},
	}

	var fromList []interface{}
	for _, ns := range config.FromNamespaces {
		for _, k := range kinds {
			fromList = append(fromList, map[string]interface{}{
				"group":     k.group,
				"kind":      k.kind,
				"namespace": ns,
			})
		}
	}

	toKinds := config.ToKinds
	if len(toKinds) == 0 {
		toKinds = []string{"Service", "Secret"}
	}
	toList := make([]interface{}, 0, len(toKinds))
	for _, k := range toKinds {
		toList = append(toList, map[string]interface{}{
			"group": "",
			"kind":  k,
		})
	}

	referenceGrant := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1beta1",
			"kind":       "ReferenceGrant",
			"metadata": map[string]interface{}{
				"name":      config.Name,
				"namespace": config.ToNamespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
				},
			},
			"spec": map[string]interface{}{
				"from": fromList,
				"to":   toList,
			},
		},
	}

	_, err = client.Resource(gvr).Namespace(config.ToNamespace).Create(ctx, referenceGrant, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create referencegrant: %w", err)
	}

	return nil
}

// DeleteReferenceGrant deletes a ReferenceGrant from Kubernetes
func (s *Client) DeleteReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1beta1",
		Resource: "referencegrants",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete referencegrant: %w", err)
	}

	return nil
}

// GetReferenceGrant gets a ReferenceGrant from Kubernetes
func (s *Client) GetReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) (*unstructured.Unstructured, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1beta1",
		Resource: "referencegrants",
	}

	rg, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get referencegrant: %w", err)
	}

	return rg, nil
}

// ReferenceGrantExists checks if a ReferenceGrant exists in Kubernetes
func (s *Client) ReferenceGrantExists(ctx context.Context, projectID uuid.UUID, namespace, name string) (bool, error) {
	_, err := s.GetReferenceGrant(ctx, projectID, namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RecreateReferenceGrant deletes and recreates a ReferenceGrant with updated config.
// No-op on delete failure if the grant doesn't exist.
func (s *Client) RecreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *ReferenceGrantConfig) error {
	// Delete existing (ignore not-found errors)
	_ = s.DeleteReferenceGrant(ctx, projectID, config.ToNamespace, config.Name)

	return s.CreateReferenceGrant(ctx, projectID, config)
}

// ==================== HTTPRouteFilter (Direct Response) ====================

// ApplyHTTPRouteFilter creates or updates an HTTPRouteFilter in Kubernetes
func (s *Client) ApplyHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, config *kubernetes.HTTPRouteFilterConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.HTTPRouteFilterGVR
	httpRouteFilter := kubernetes.BuildHTTPRouteFilter(config)

	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, httpRouteFilter, metav1.CreateOptions{})
			return err
		}
		return err
	}

	httpRouteFilter.SetResourceVersion(existing.GetResourceVersion())
	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, httpRouteFilter, metav1.UpdateOptions{})
	return err
}

// DeleteHTTPRouteFilter deletes an HTTPRouteFilter from Kubernetes
func (s *Client) DeleteHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.HTTPRouteFilterGVR

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete HTTPRouteFilter: %w", err)
	}
	return nil
}

// ==================== ConfigMap (Direct Response body) ====================

// ApplyDirectResponseConfigMap creates or updates a ConfigMap for Direct Response in Kubernetes
func (s *Client) ApplyDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, config *kubernetes.DirectResponseConfigMapConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.ConfigMapGVR
	configMap := kubernetes.BuildDirectResponseConfigMap(config)

	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, configMap, metav1.CreateOptions{})
			return err
		}
		return err
	}

	configMap.SetResourceVersion(existing.GetResourceVersion())
	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, configMap, metav1.UpdateOptions{})
	return err
}

// DeleteDirectResponseConfigMap deletes a ConfigMap from Kubernetes (only FastGateway-managed ones)
func (s *Client) DeleteDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.ConfigMapGVR

	// Only delete if it's managed by FastGateway
	existing, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	labels := existing.GetLabels()
	if labels == nil || labels["app.kubernetes.io/managed-by"] != "fastgateway" {
		// Not managed by FastGateway, don't delete
		return nil
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete ConfigMap: %w", err)
	}
	return nil
}

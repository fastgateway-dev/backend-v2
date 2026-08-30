package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/google/uuid"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

// KubernetesService handles Kubernetes operations
type KubernetesService struct {
	projectService *ProjectService
	testClient     dynamic.Interface // when set, used by getClientFor instead of building per-project clients (tests only)
}

// NewKubernetesService creates a new Kubernetes service
func NewKubernetesService(projectService *ProjectService) *KubernetesService {
	return &KubernetesService{
		projectService: projectService,
	}
}

// getClient creates a dynamic Kubernetes client for a project
func (s *KubernetesService) getClient(projectID uuid.UUID) (dynamic.Interface, error) {
	project, err := s.projectService.GetByID(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	var config *rest.Config

	switch project.ConnectionType {
	case "in_cluster":
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}

	case "kubeconfig", "api_token", "":
		// Default to api_token behavior for backward compatibility
		config = &rest.Config{
			Host: project.K8sAPIURL,
		}

		// Auth: token or client cert
		if project.K8sTokenEncrypted != "" {
			token, err := s.projectService.GetDecryptedToken(projectID)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt token: %w", err)
			}
			config.BearerToken = token
		} else if project.K8sClientCert != "" {
			config.TLSClientConfig.CertData = []byte(project.K8sClientCert)
			// Decrypt client key
			if project.K8sClientKeyEncrypted != "" {
				clientKey, err := s.projectService.GetDecryptedClientKey(projectID)
				if err != nil {
					return nil, fmt.Errorf("failed to decrypt client key: %w", err)
				}
				config.TLSClientConfig.KeyData = []byte(clientKey)
			}
		}

		// TLS verification
		if project.K8sTLSSkipVerify {
			config.TLSClientConfig.Insecure = true
		} else if project.K8sCACert != "" {
			config.TLSClientConfig.CAData = []byte(project.K8sCACert)
		}
		// else: use system CA bundle (default behavior)

	default:
		return nil, fmt.Errorf("unknown connection type: %s", project.ConnectionType)
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return client, nil
}

// EnsureNamespace ensures the namespace exists, creating it if necessary
func (s *KubernetesService) EnsureNamespace(ctx context.Context, projectID uuid.UUID, namespace string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	// Check if namespace exists
	_, err = client.Resource(gvr).Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		// Namespace already exists
		return nil
	}

	// Create namespace
	ns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
				},
			},
		},
	}

	_, err = client.Resource(gvr).Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	return nil
}

// CreateGateway creates a Gateway resource in Kubernetes
func (s *KubernetesService) CreateGateway(ctx context.Context, projectID uuid.UUID, config *GatewayConfig) error {
	// Ensure namespace exists first
	if err := s.EnsureNamespace(ctx, projectID, config.Namespace); err != nil {
		return fmt.Errorf("failed to ensure namespace: %w", err)
	}

	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}

	gateway := BuildGatewayObject(config)

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, gateway, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create gateway: %w", err)
	}

	return nil
}

// DeleteGateway deletes a Gateway resource from Kubernetes
func (s *KubernetesService) DeleteGateway(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete gateway: %w", err)
	}

	return nil
}

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
func (s *KubernetesService) CreateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *HTTPRouteConfig) error {
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
	httpRoute := BuildHTTPRouteObject(config)

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
func (s *KubernetesService) UpdateHTTPRoute(ctx context.Context, projectID uuid.UUID, config *HTTPRouteConfig) error {
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
	httpRoute := BuildHTTPRouteObject(config)

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
func (s *KubernetesService) DeleteHTTPRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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
func (s *KubernetesService) CreateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *GRPCRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getGRPCRouteGVR()

	grpcRoute := BuildGRPCRouteObject(config)
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
func (s *KubernetesService) UpdateGRPCRoute(ctx context.Context, projectID uuid.UUID, config *GRPCRouteConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getGRPCRouteGVR()

	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing GRPCRoute: %w", err)
	}

	grpcRoute := BuildGRPCRouteObject(config)
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
func (s *KubernetesService) DeleteGRPCRoute(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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

// CreateSecurityPolicy creates an Envoy Gateway SecurityPolicy resource in Kubernetes.
// If the resource already exists (e.g. from a partial previous deploy), it falls back to update.
func (s *KubernetesService) CreateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *SecurityPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	securityPolicy := BuildSecurityPolicy(config)
	if securityPolicy == nil {
		// No security features configured, nothing to create
		return nil
	}

	gvr := kubernetes.SecurityPolicyGVR

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, securityPolicy, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Resource already exists (partial previous deploy), fall back to update
			return s.UpdateSecurityPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create securitypolicy: %w", err)
	}

	return nil
}

// UpdateSecurityPolicy updates an Envoy Gateway SecurityPolicy resource in Kubernetes
func (s *KubernetesService) UpdateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *SecurityPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.SecurityPolicyGVR

	securityPolicy := BuildSecurityPolicy(config)
	if securityPolicy == nil {
		// No security features configured, delete existing if any
		return s.DeleteSecurityPolicy(ctx, projectID, config.Namespace, config.Name)
	}

	// Get the existing SecurityPolicy to preserve resourceVersion
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		// If not found, create it
		if strings.Contains(err.Error(), "not found") {
			return s.CreateSecurityPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to get existing securitypolicy: %w", err)
	}

	// Preserve the resourceVersion from existing object
	securityPolicy.SetResourceVersion(existing.GetResourceVersion())
	securityPolicy.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, securityPolicy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update securitypolicy: %w", err)
	}

	return nil
}

// DeleteSecurityPolicy deletes an Envoy Gateway SecurityPolicy resource from Kubernetes
func (s *KubernetesService) DeleteSecurityPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.SecurityPolicyGVR

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		// Ignore not found errors
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete securitypolicy: %w", err)
	}

	return nil
}

// CreateBackendTrafficPolicy creates an Envoy Gateway BackendTrafficPolicy resource in Kubernetes.
// If the resource already exists (e.g. from a partial previous deploy), it falls back to update.
func (s *KubernetesService) CreateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *BackendTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	backendTrafficPolicy := BuildBackendTrafficPolicy(config)
	if backendTrafficPolicy == nil {
		// No features configured, nothing to create
		return nil
	}

	gvr := kubernetes.BackendTrafficPolicyGVR

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, backendTrafficPolicy, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Resource already exists (partial previous deploy), fall back to update
			return s.UpdateBackendTrafficPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create backendtrafficpolicy: %w", err)
	}

	return nil
}

// UpdateBackendTrafficPolicy updates an Envoy Gateway BackendTrafficPolicy resource in Kubernetes
func (s *KubernetesService) UpdateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *BackendTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.BackendTrafficPolicyGVR

	backendTrafficPolicy := BuildBackendTrafficPolicy(config)
	if backendTrafficPolicy == nil {
		// No features configured, delete existing if any
		return s.DeleteBackendTrafficPolicy(ctx, projectID, config.Namespace, config.Name)
	}

	// Get the existing BackendTrafficPolicy to preserve resourceVersion
	existing, err := client.Resource(gvr).Namespace(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		// If not found, create it
		if strings.Contains(err.Error(), "not found") {
			return s.CreateBackendTrafficPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to get existing backendtrafficpolicy: %w", err)
	}

	// Preserve the resourceVersion from existing object
	backendTrafficPolicy.SetResourceVersion(existing.GetResourceVersion())
	backendTrafficPolicy.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, backendTrafficPolicy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update backendtrafficpolicy: %w", err)
	}

	return nil
}

// DeleteBackendTrafficPolicy deletes an Envoy Gateway BackendTrafficPolicy resource from Kubernetes
func (s *KubernetesService) DeleteBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.BackendTrafficPolicyGVR

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		// Ignore not found errors
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete backendtrafficpolicy: %w", err)
	}

	return nil
}

// getEnvoyExtensionPolicyGVR returns the GroupVersionResource for EnvoyExtensionPolicy
func getEnvoyExtensionPolicyGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyextensionpolicies",
	}
}

// CreateEnvoyExtensionPolicy creates an EnvoyExtensionPolicy resource in Kubernetes
func (s *KubernetesService) CreateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getEnvoyExtensionPolicyGVR()

	_, err = client.Resource(gvr).Namespace(policy.GetNamespace()).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return s.UpdateEnvoyExtensionPolicy(ctx, projectID, policy)
		}
		return fmt.Errorf("failed to create envoyextensionpolicy: %w", err)
	}

	return nil
}

// UpdateEnvoyExtensionPolicy updates an EnvoyExtensionPolicy resource in Kubernetes
func (s *KubernetesService) UpdateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getEnvoyExtensionPolicyGVR()

	existing, err := client.Resource(gvr).Namespace(policy.GetNamespace()).Get(ctx, policy.GetName(), metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return s.CreateEnvoyExtensionPolicy(ctx, projectID, policy)
		}
		return fmt.Errorf("failed to get envoyextensionpolicy: %w", err)
	}

	policy.SetResourceVersion(existing.GetResourceVersion())
	_, err = client.Resource(gvr).Namespace(policy.GetNamespace()).Update(ctx, policy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update envoyextensionpolicy: %w", err)
	}

	return nil
}

// DeleteEnvoyExtensionPolicy deletes an EnvoyExtensionPolicy resource from Kubernetes
func (s *KubernetesService) DeleteEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := getEnvoyExtensionPolicyGVR()

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete envoyextensionpolicy: %w", err)
	}

	return nil
}

// CreateBackend creates an Envoy Gateway Backend resource in Kubernetes
func (s *KubernetesService) CreateBackend(ctx context.Context, projectID uuid.UUID, config *BackendConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	backend := BuildBackend(config)
	gvr := kubernetes.BackendGVR

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, backend, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create backend: %w", err)
	}

	return nil
}

// UpdateBackend updates an Envoy Gateway Backend resource in Kubernetes
func (s *KubernetesService) UpdateBackend(ctx context.Context, projectID uuid.UUID, config *BackendConfig) error {
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

	backend := BuildBackend(config)
	backend.SetResourceVersion(existing.GetResourceVersion())
	backend.SetUID(existing.GetUID())

	_, err = client.Resource(gvr).Namespace(config.Namespace).Update(ctx, backend, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update backend: %w", err)
	}

	return nil
}

// DeleteBackend deletes an Envoy Gateway Backend resource from Kubernetes
func (s *KubernetesService) DeleteBackend(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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
func (s *KubernetesService) UpdateBackendUnstructured(ctx context.Context, projectID uuid.UUID, backend *unstructured.Unstructured) error {
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
func (s *KubernetesService) DeleteBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string) error {
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
func (s *KubernetesService) DeleteStaleBackendsByRoute(ctx context.Context, projectID uuid.UUID, namespace, routeID string, expectedNames map[string]bool) error {
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

// TestConnection tests the Kubernetes connection
func (s *KubernetesService) TestConnection(ctx context.Context, projectID uuid.UUID) (bool, string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return false, "", err
	}

	// Try to list namespaces to test connection
	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	_, err = client.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return false, "", fmt.Errorf("failed to connect: %w", err)
	}

	// Get server version
	return true, "Connected", nil
}

// ListNamespaces lists namespaces in the cluster
func (s *KubernetesService) ListNamespaces(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	list, err := client.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	namespaces := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		namespaces = append(namespaces, item.GetName())
	}

	return namespaces, nil
}

// ListServices lists services in a namespace
func (s *KubernetesService) ListServices(ctx context.Context, projectID uuid.UUID, namespace string) ([]map[string]interface{}, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "services",
	}

	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	services := make([]map[string]interface{}, 0, len(list.Items))
	for _, item := range list.Items {
		spec, _, _ := unstructured.NestedMap(item.Object, "spec")
		ports, _, _ := unstructured.NestedSlice(spec, "ports")

		portList := make([]map[string]interface{}, 0)
		for _, p := range ports {
			if portMap, ok := p.(map[string]interface{}); ok {
				portList = append(portList, map[string]interface{}{
					"name":     portMap["name"],
					"port":     portMap["port"],
					"protocol": portMap["protocol"],
				})
			}
		}

		services = append(services, map[string]interface{}{
			"name":      item.GetName(),
			"namespace": item.GetNamespace(),
			"ports":     portList,
		})
	}

	return services, nil
}

// TLSSecretInfo represents a kubernetes.io/tls secret for the API response
type TLSSecretInfo struct {
	Name                 string            `json:"name"`
	Namespace            string            `json:"namespace"`
	ManagedByFastgateway bool              `json:"managedByFastgateway"`
	Labels               map[string]string `json:"labels"`
	CreatedAt            string            `json:"createdAt"`
}

// ListTLSSecrets lists kubernetes.io/tls secrets in the specified namespace
func (s *KubernetesService) ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) ([]TLSSecretInfo, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	list, err := client.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "type=kubernetes.io/tls",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list TLS secrets: %w", err)
	}

	secrets := make([]TLSSecretInfo, 0, len(list.Items))
	for _, item := range list.Items {
		labels := item.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}

		managedBy := labels["app.kubernetes.io/managed-by"] == "fastgateway"

		createdAt := ""
		if ts := item.GetCreationTimestamp(); !ts.IsZero() {
			createdAt = ts.Format("2006-01-02T15:04:05Z")
		}

		secrets = append(secrets, TLSSecretInfo{
			Name:                 item.GetName(),
			Namespace:            item.GetNamespace(),
			ManagedByFastgateway: managedBy,
			Labels:               labels,
			CreatedAt:            createdAt,
		})
	}

	return secrets, nil
}

// ListGatewayClasses lists available GatewayClasses
func (s *KubernetesService) ListGatewayClasses(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	list, err := client.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list gateway classes: %w", err)
	}

	classes := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		classes = append(classes, item.GetName())
	}

	return classes, nil
}

// PrerequisiteCheck represents the result of a prerequisite check
type PrerequisiteCheck struct {
	NamespaceExists    bool   `json:"namespaceExists"`
	GatewayCRDExists   bool   `json:"gatewayCrdExists"`
	HTTPRouteCRDExists bool   `json:"httprouteCrdExists"`
	ErrorMessage       string `json:"errorMessage,omitempty"`
}

// getClientDirect creates a dynamic Kubernetes client directly from URL and token
func (s *KubernetesService) getClientDirect(apiURL, token string) (dynamic.Interface, error) {
	config := &rest.Config{
		Host:        apiURL,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true, // TODO: Make this configurable
		},
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return client, nil
}

// ValidatePrerequisites checks if the Kubernetes cluster has the required prerequisites
// - fastgateway-system namespace must exist
// - Gateway API CRDs must be installed (Gateway, HTTPRoute)
func (s *KubernetesService) ValidatePrerequisites(ctx context.Context, apiURL, token string) (*PrerequisiteCheck, error) {
	client, err := s.getClientDirect(apiURL, token)
	if err != nil {
		// Check for common connection issues
		errStr := err.Error()
		if strings.Contains(errStr, "127.0.0.1") || strings.Contains(errStr, "localhost") {
			return nil, fmt.Errorf("failed to connect to Kubernetes at %s: If running FastGateway in Docker, use 'host.docker.internal' instead of 'localhost' or '127.0.0.1'. Original error: %w", apiURL, err)
		}
		if strings.Contains(errStr, "connection refused") {
			return nil, fmt.Errorf("connection refused to %s: Ensure the Kubernetes API server is accessible from FastGateway. If running in Docker, use 'host.docker.internal' or your machine's actual IP address. Original error: %w", apiURL, err)
		}
		return nil, fmt.Errorf("failed to connect to Kubernetes at %s: %w", apiURL, err)
	}

	result := &PrerequisiteCheck{}
	var checkErrors []string

	// Check if fastgateway-system namespace exists
	nsGVR := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	_, err = client.Resource(nsGVR).Get(ctx, FastGatewayNamespace, metav1.GetOptions{})
	if err == nil {
		result.NamespaceExists = true
	} else {
		checkErrors = append(checkErrors, fmt.Sprintf("namespace check: %v", err))
	}

	// Check if Gateway CRD exists by trying to list gateways
	gatewayGVR := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}

	_, err = client.Resource(gatewayGVR).List(ctx, metav1.ListOptions{Limit: 1})
	if err == nil {
		result.GatewayCRDExists = true
	} else {
		// Check if it's a "not found" error (CRD not installed) vs permission error
		if strings.Contains(err.Error(), "the server could not find the requested resource") {
			checkErrors = append(checkErrors, "Gateway CRD not installed")
		} else {
			// Might be permission error - assume CRD exists
			result.GatewayCRDExists = true
			checkErrors = append(checkErrors, fmt.Sprintf("gateway list warning: %v", err))
		}
	}

	// Check if HTTPRoute CRD exists by trying to list httproutes
	httpRouteGVR := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	_, err = client.Resource(httpRouteGVR).List(ctx, metav1.ListOptions{Limit: 1})
	if err == nil {
		result.HTTPRouteCRDExists = true
	} else {
		// Check if it's a "not found" error (CRD not installed) vs permission error
		if strings.Contains(err.Error(), "the server could not find the requested resource") {
			checkErrors = append(checkErrors, "HTTPRoute CRD not installed")
		} else {
			// Might be permission error - assume CRD exists
			result.HTTPRouteCRDExists = true
			checkErrors = append(checkErrors, fmt.Sprintf("httproute list warning: %v", err))
		}
	}

	// Build error message if prerequisites are not met
	var missing []string
	if !result.NamespaceExists {
		missing = append(missing, fmt.Sprintf("namespace '%s' does not exist", FastGatewayNamespace))
	}
	if !result.GatewayCRDExists {
		missing = append(missing, "Gateway API CRD (Gateway) is not installed")
	}
	if !result.HTTPRouteCRDExists {
		missing = append(missing, "Gateway API CRD (HTTPRoute) is not installed")
	}

	if len(missing) > 0 {
		result.ErrorMessage = fmt.Sprintf("Prerequisites not met: %s. Please install Gateway API CRDs and create the '%s' namespace before onboarding.",
			joinStrings(missing, "; "), FastGatewayNamespace)
	}

	return result, nil
}

// joinStrings joins strings with a separator
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

// CreateGatewayClass creates a GatewayClass resource in Kubernetes
func (s *KubernetesService) CreateGatewayClass(ctx context.Context, projectID uuid.UUID, config *GatewayClassConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	gatewayClass := BuildGatewayClassObject(config)

	_, err = client.Resource(gvr).Create(ctx, gatewayClass, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create gatewayclass: %w", err)
	}

	return nil
}

// DeleteGatewayClass deletes a GatewayClass resource from Kubernetes
func (s *KubernetesService) DeleteGatewayClass(ctx context.Context, projectID uuid.UUID, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	err = client.Resource(gvr).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete gatewayclass: %w", err)
	}

	return nil
}

// CreateEnvoyProxy creates an EnvoyProxy resource in Kubernetes
func (s *KubernetesService) CreateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *EnvoyProxyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyproxies",
	}

	envoyProxy := BuildEnvoyProxyObject(config)

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, envoyProxy, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create envoyproxy: %w", err)
	}

	return nil
}

// UpdateEnvoyProxy updates an EnvoyProxy resource in Kubernetes
func (s *KubernetesService) UpdateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *EnvoyProxyConfig) error {
	// Delete and recreate for simplicity
	_ = s.DeleteEnvoyProxy(ctx, projectID, config.Namespace, config.Name)
	return s.CreateEnvoyProxy(ctx, projectID, config)
}

// DeleteEnvoyProxy deletes an EnvoyProxy resource from Kubernetes
func (s *KubernetesService) DeleteEnvoyProxy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyproxies",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete envoyproxy: %w", err)
	}

	return nil
}

// ValidateEnvoyGatewayInstalled checks if Envoy Gateway controller is installed
// by looking for existing GatewayClasses with the Envoy controller name
// or by checking for deployments in envoy-gateway-system namespace
func (s *KubernetesService) ValidateEnvoyGatewayInstalled(ctx context.Context, projectID uuid.UUID) (bool, string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return false, "", err
	}

	// Method 1: Check for GatewayClasses with Envoy controller
	gcGVR := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	list, err := client.Resource(gcGVR).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, item := range list.Items {
			spec, _, _ := unstructured.NestedMap(item.Object, "spec")
			if controllerName, ok := spec["controllerName"].(string); ok {
				if controllerName == EnvoyGatewayControllerName {
					return true, "Envoy Gateway controller found via existing GatewayClass", nil
				}
			}
		}
	}

	// Method 2: Check for any deployments in envoy-gateway-system namespace
	deployGVR := schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}

	deployList, err := client.Resource(deployGVR).Namespace("envoy-gateway-system").List(ctx, metav1.ListOptions{})
	if err == nil && len(deployList.Items) > 0 {
		return true, "Envoy Gateway controller found via deployment in envoy-gateway-system namespace", nil
	}

	// Method 3: Check for envoy-gateway namespace existence as fallback
	nsGVR := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	_, err = client.Resource(nsGVR).Get(ctx, "envoy-gateway-system", metav1.GetOptions{})
	if err == nil {
		// Namespace exists, check if there are any pods running
		podGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "pods",
		}
		podList, err := client.Resource(podGVR).Namespace("envoy-gateway-system").List(ctx, metav1.ListOptions{})
		if err == nil && len(podList.Items) > 0 {
			return true, "Envoy Gateway controller found via pods in envoy-gateway-system namespace", nil
		}
	}

	return false, "Envoy Gateway controller not found. Please install Envoy Gateway first: https://gateway.envoyproxy.io/docs/tasks/quickstart/", nil
}

// ReferenceGrantConfig represents ReferenceGrant configuration
type ReferenceGrantConfig struct {
	Name           string   // Name of the ReferenceGrant
	FromNamespaces []string // Namespaces where Gateways and routes are deployed
	ToNamespace    string   // Namespace where the referenced resources reside
	ToKinds        []string // Core/v1 kinds permitted as targets (e.g. "Service", "Secret"). Empty = both.
}

// CreateReferenceGrant creates a ReferenceGrant allowing resources from multiple namespaces to reference services and/or secrets in the target namespace
func (s *KubernetesService) CreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *ReferenceGrantConfig) error {
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
func (s *KubernetesService) DeleteReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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
func (s *KubernetesService) GetReferenceGrant(ctx context.Context, projectID uuid.UUID, namespace, name string) (*unstructured.Unstructured, error) {
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
func (s *KubernetesService) ReferenceGrantExists(ctx context.Context, projectID uuid.UUID, namespace, name string) (bool, error) {
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
func (s *KubernetesService) RecreateReferenceGrant(ctx context.Context, projectID uuid.UUID, config *ReferenceGrantConfig) error {
	// Delete existing (ignore not-found errors)
	_ = s.DeleteReferenceGrant(ctx, projectID, config.ToNamespace, config.Name)

	return s.CreateReferenceGrant(ctx, projectID, config)
}

// ==================== HTTPRouteFilter (Direct Response) ====================

// ApplyHTTPRouteFilter creates or updates an HTTPRouteFilter in Kubernetes
func (s *KubernetesService) ApplyHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, config *HTTPRouteFilterConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.HTTPRouteFilterGVR
	httpRouteFilter := BuildHTTPRouteFilter(config)

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
func (s *KubernetesService) DeleteHTTPRouteFilter(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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
func (s *KubernetesService) ApplyDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, config *DirectResponseConfigMapConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.ConfigMapGVR
	configMap := BuildDirectResponseConfigMap(config)

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
func (s *KubernetesService) DeleteDirectResponseConfigMap(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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

// CreateClientTrafficPolicy creates an Envoy Gateway ClientTrafficPolicy resource in Kubernetes.
// If the resource already exists, it falls back to update.
func (s *KubernetesService) CreateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *ClientTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	clientTrafficPolicy := BuildClientTrafficPolicy(config)
	if clientTrafficPolicy == nil {
		// No features configured, nothing to create
		return nil
	}

	gvr := kubernetes.ClientTrafficPolicyGVR

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, clientTrafficPolicy, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Resource already exists, fall back to update
			return s.UpdateClientTrafficPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to create clienttrafficpolicy: %w", err)
	}

	return nil
}

// updateUnstructuredWithRetry performs a read-modify-write update of the
// object called name, retrying on a Kubernetes optimistic-concurrency
// conflict.
//
// Kubernetes rejects an Update whose resourceVersion is stale
// ("Operation cannot be fulfilled ...: the object has been modified"),
// which is what happens whenever two operations touch the same object
// concurrently. Objects scoped to a DOMAIN rather than a route --
// the ClientTrafficPolicy, the mTLS CA Secret, a client's API-key Secret
// -- are exactly that: every mTLS CA add/remove, TLS change and
// client-connection change on one domain rewrites the same object, so two
// admins working on a domain at the same time (or, in the e2e suite, two
// parallel tests) reliably produced a user-visible 400. Re-reading the
// current resourceVersion and retrying resolves it; desired is rebuilt
// from the caller's config each attempt, so the last writer still wins,
// it just no longer fails spuriously.
func updateUnstructuredWithRetry(ctx context.Context, ri dynamic.ResourceInterface, name string, desired *unstructured.Unstructured) error {
	// desired is stamped in place rather than deep-copied per attempt.
	// Unstructured.DeepCopy goes through runtime.DeepCopyJSONValue, which
	// panics on any value that is not a JSON-native type -- and these
	// objects are hand-built maps that can still hold a []string
	// (see BuildClientTrafficPolicy's TLS ciphers). Only resourceVersion
	// and uid change between attempts, so mutating the caller's object is
	// both sufficient and what the pre-retry code already did.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := ri.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		desired.SetResourceVersion(existing.GetResourceVersion())
		desired.SetUID(existing.GetUID())
		_, err = ri.Update(ctx, desired, metav1.UpdateOptions{})
		return err
	})
}

// UpdateClientTrafficPolicy updates an Envoy Gateway ClientTrafficPolicy resource in Kubernetes
func (s *KubernetesService) UpdateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *ClientTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.ClientTrafficPolicyGVR

	clientTrafficPolicy := BuildClientTrafficPolicy(config)
	if clientTrafficPolicy == nil {
		// No features configured, delete existing if any
		return s.DeleteClientTrafficPolicy(ctx, projectID, config.Namespace, config.Name)
	}

	// Re-reads resourceVersion on every attempt: this object is
	// domain-scoped, so concurrent mTLS/TLS/client-connection changes on
	// the same domain race each other. See updateUnstructuredWithRetry.
	ri := client.Resource(gvr).Namespace(config.Namespace)
	if err := updateUnstructuredWithRetry(ctx, ri, config.Name, clientTrafficPolicy); err != nil {
		if k8serrors.IsNotFound(err) {
			return s.CreateClientTrafficPolicy(ctx, projectID, config)
		}
		return fmt.Errorf("failed to update clienttrafficpolicy: %w", err)
	}

	return nil
}

// DeleteClientTrafficPolicy deletes an Envoy Gateway ClientTrafficPolicy resource from Kubernetes
func (s *KubernetesService) DeleteClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.ClientTrafficPolicyGVR

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		// Ignore not found errors
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete clienttrafficpolicy: %w", err)
	}

	return nil
}

// =============================================================================
// API Key Secret Management
// =============================================================================

// GetAPIKeySecretName returns the Kubernetes secret name for a client's API key
func (s *KubernetesService) GetAPIKeySecretName(clientID uuid.UUID) string {
	return kubernetes.APIKeySecretForClientName(clientID.String())
}

// CreateAPIKeySecret creates or updates a Kubernetes Secret containing an API key for a client
func (s *KubernetesService) CreateAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID, apiKey string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	secretName := s.GetAPIKeySecretName(clientID)
	namespace := "fastgateway-system"

	// Build the Secret object
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      secretName,
				"namespace": namespace,
				"labels":    kubernetes.ForAPIKeySecretInterface(clientID.String()),
			},
			"type": "Opaque",
			"data": map[string]interface{}{
				"api-key": base64.StdEncoding.EncodeToString([]byte(apiKey)),
			},
		},
	}

	// Try to create; if exists, update
	_, err = client.Resource(gvr).Namespace(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Retries on conflict: this Secret is rewritten by every
			// change to the client, so concurrent updates race.
			ri := client.Resource(gvr).Namespace(namespace)
			if err := updateUnstructuredWithRetry(ctx, ri, secretName, secret); err != nil {
				return fmt.Errorf("failed to update api key secret: %w", err)
			}
		} else {
			return fmt.Errorf("failed to create api key secret: %w", err)
		}
	}

	return nil
}

// GetAPIKeyFromSecret retrieves the API key from a Kubernetes Secret for a client
func (s *KubernetesService) GetAPIKeyFromSecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) (string, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return "", err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	secretName := s.GetAPIKeySecretName(clientID)
	namespace := "fastgateway-system"

	secret, err := client.Resource(gvr).Namespace(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get api key secret: %w", err)
	}

	// Extract the data field
	data, found, err := unstructured.NestedMap(secret.Object, "data")
	if err != nil || !found {
		return "", fmt.Errorf("failed to extract data from secret")
	}

	// Get the api-key value (base64 encoded in data)
	apiKeyEncoded, ok := data["api-key"].(string)
	if !ok {
		return "", fmt.Errorf("api-key not found in secret")
	}

	// Decode from base64
	apiKeyBytes, err := base64.StdEncoding.DecodeString(apiKeyEncoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode api key: %w", err)
	}

	return string(apiKeyBytes), nil
}

// DeleteAPIKeySecret deletes a Kubernetes Secret containing an API key for a client
func (s *KubernetesService) DeleteAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	secretName := s.GetAPIKeySecretName(clientID)
	namespace := "fastgateway-system"

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, secretName, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) || strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("failed to delete api key secret: %w", err)
	}

	return nil
}

// =============================================================================
// mTLS Secret Management
// =============================================================================

// CreateOrUpdateSecret creates or updates a K8s Secret with the given data
func (s *KubernetesService) CreateOrUpdateSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, data map[string][]byte) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	// Convert data to base64
	secretData := make(map[string]interface{})
	for k, v := range data {
		secretData[k] = base64.StdEncoding.EncodeToString(v)
	}

	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
					"fastgateway.dev/type":         "mtls-ca",
				},
			},
			"type": "Opaque",
			"data": secretData,
		},
	}

	// Try to create; if exists, update
	_, err = client.Resource(gvr).Namespace(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Retries on conflict: this Secret holds ALL of the domain's
			// mTLS CAs, so adding and removing CAs concurrently races.
			ri := client.Resource(gvr).Namespace(namespace)
			if err := updateUnstructuredWithRetry(ctx, ri, name, secret); err != nil {
				return fmt.Errorf("failed to update secret: %w", err)
			}
		} else {
			return fmt.Errorf("failed to create secret: %w", err)
		}
	}

	return nil
}

// DeleteSecret deletes a K8s Secret
func (s *KubernetesService) DeleteSecret(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	err = client.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) && !strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("failed to delete secret: %w", err)
	}
	return nil
}

// GetSecretData retrieves data from a K8s Secret
func (s *KubernetesService) GetSecretData(ctx context.Context, projectID uuid.UUID, namespace, name, key string) ([]byte, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	secret, err := client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}

	data, found, err := unstructured.NestedStringMap(secret.Object, "data")
	if err != nil || !found {
		return nil, fmt.Errorf("secret data not found")
	}

	encoded, ok := data[key]
	if !ok {
		return nil, fmt.Errorf("key '%s' not found in secret", key)
	}

	return base64.StdEncoding.DecodeString(encoded)
}

// IsRateLimitAvailable checks if rate limiting is available by reading the envoy-gateway-config ConfigMap
func (s *KubernetesService) IsRateLimitAvailable(ctx context.Context, projectID uuid.UUID) (bool, error) {
	client, err := s.getClient(projectID)
	if err != nil {
		return false, err
	}

	// Read the envoy-gateway-config ConfigMap from envoy-gateway-system namespace
	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	configMap, err := client.Resource(gvr).Namespace("envoy-gateway-system").Get(ctx, "envoy-gateway-config", metav1.GetOptions{})
	if err != nil {
		// ConfigMap not found or not accessible -- rate limiting not available
		return false, nil
	}

	// Parse the ConfigMap data
	data, found, err := unstructured.NestedStringMap(configMap.Object, "data")
	if err != nil || !found {
		return false, nil
	}

	// Check the envoy-gateway.yaml key for rateLimit.backend configuration
	yamlData, exists := data["envoy-gateway.yaml"]
	if !exists {
		return false, nil
	}

	// Simple check: look for "rateLimit:" and "backend:" in the YAML
	// This avoids importing a YAML parser -- the presence of these keys
	// indicates the user has configured the rate limit backend
	return strings.Contains(yamlData, "rateLimit:") && strings.Contains(yamlData, "backend:"), nil
}

// DeleteStaleAPIKeyResources deletes orphaned per-client HTTPRoutes, SecurityPolicies, and BackendTrafficPolicies
// that are no longer needed (i.e., their client prefixes are not in expectedClientPrefixes)
func (s *KubernetesService) DeleteStaleAPIKeyResources(ctx context.Context, projectID uuid.UUID, namespace, routeID, baseRouteName string, expectedClientPrefixes map[string]bool) error {
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
func (s *KubernetesService) deleteStalePerClientResources(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, namespace, routeID, baseRouteName string, expectedPrefixes map[string]bool) error {
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

// ─── Aliases to the extracted internal/kubernetes package ───────────────────
// The pure manifest builders and their DTOs now live in internal/kubernetes.
// These aliases keep the existing call sites in this package compiling; they are
// removed in a later pass as callers migrate to the kubernetes package directly.

const (
	EnvoyGatewayControllerName = kubernetes.EnvoyGatewayControllerName
	EnvoyGatewayNamespace      = kubernetes.EnvoyGatewayNamespace
)

type (
	GatewayConfig                        = kubernetes.GatewayConfig
	HTTPRouteConfig                      = kubernetes.HTTPRouteConfig
	MirrorRef                            = kubernetes.MirrorRef
	HTTPRedirectConfig                   = kubernetes.HTTPRedirectConfig
	HTTPURLRewrite                       = kubernetes.HTTPURLRewrite
	HTTPPathRewrite                      = kubernetes.HTTPPathRewrite
	HTTPRouteRule                        = kubernetes.HTTPRouteRule
	HTTPHeaderModifier                   = kubernetes.HTTPHeaderModifier
	HTTPHeaderValue                      = kubernetes.HTTPHeaderValue
	HeaderMatch                          = kubernetes.HeaderMatch
	QueryParamMatch                      = kubernetes.QueryParamMatch
	BackendRef                           = kubernetes.BackendRef
	GRPCRouteConfig                      = kubernetes.GRPCRouteConfig
	GRPCRouteRule                        = kubernetes.GRPCRouteRule
	GRPCMethodMatchConfig                = kubernetes.GRPCMethodMatchConfig
	SecurityPolicyConfig                 = kubernetes.SecurityPolicyConfig
	OIDCPolicyConfig                     = kubernetes.OIDCPolicyConfig
	JWTAuthPolicyConfig                  = kubernetes.JWTAuthPolicyConfig
	JWTProviderPolicyConfig              = kubernetes.JWTProviderPolicyConfig
	JWTClaimToHeaderPolicyConfig         = kubernetes.JWTClaimToHeaderPolicyConfig
	AuthorizationPolicyConfig            = kubernetes.AuthorizationPolicyConfig
	AuthorizationRulePolicyConfig        = kubernetes.AuthorizationRulePolicyConfig
	HeaderMatchPolicyConfig              = kubernetes.HeaderMatchPolicyConfig
	JWTPrincipalPolicyConfig             = kubernetes.JWTPrincipalPolicyConfig
	JWTClaimRulePolicyConfig             = kubernetes.JWTClaimRulePolicyConfig
	SecurityPolicyTargetRef              = kubernetes.SecurityPolicyTargetRef
	CORSPolicyConfig                     = kubernetes.CORSPolicyConfig
	APIKeyAuthPolicyConfig               = kubernetes.APIKeyAuthPolicyConfig
	SecretRefConfig                      = kubernetes.SecretRefConfig
	APIKeyExtractFromConfig              = kubernetes.APIKeyExtractFromConfig
	BackendTrafficPolicyConfig           = kubernetes.BackendTrafficPolicyConfig
	LoadBalancerPolicyConfig             = kubernetes.LoadBalancerPolicyConfig
	ConsistentHashPolicyConfig           = kubernetes.ConsistentHashPolicyConfig
	ConsistentHashHeaderPolicyConfig     = kubernetes.ConsistentHashHeaderPolicyConfig
	ConsistentHashCookiePolicyConfig     = kubernetes.ConsistentHashCookiePolicyConfig
	CircuitBreakerPolicyConfig           = kubernetes.CircuitBreakerPolicyConfig
	HealthCheckPolicyConfig              = kubernetes.HealthCheckPolicyConfig
	ActiveHealthCheckPolicyConfig        = kubernetes.ActiveHealthCheckPolicyConfig
	HTTPActiveHealthCheckPolicyConfig    = kubernetes.HTTPActiveHealthCheckPolicyConfig
	TCPActiveHealthCheckPolicyConfig     = kubernetes.TCPActiveHealthCheckPolicyConfig
	GRPCActiveHealthCheckPolicyConfig    = kubernetes.GRPCActiveHealthCheckPolicyConfig
	PassiveHealthCheckPolicyConfig       = kubernetes.PassiveHealthCheckPolicyConfig
	FaultInjectionPolicyConfig           = kubernetes.FaultInjectionPolicyConfig
	FaultInjectionDelayPolicyConfig      = kubernetes.FaultInjectionDelayPolicyConfig
	FaultInjectionAbortPolicyConfig      = kubernetes.FaultInjectionAbortPolicyConfig
	RateLimitPolicyConfig                = kubernetes.RateLimitPolicyConfig
	GlobalRateLimitPolicyConfig          = kubernetes.GlobalRateLimitPolicyConfig
	RateLimitRulePolicyConfig            = kubernetes.RateLimitRulePolicyConfig
	RateLimitValuePolicyConfig           = kubernetes.RateLimitValuePolicyConfig
	RateLimitSelectorPolicyConfig        = kubernetes.RateLimitSelectorPolicyConfig
	RateLimitHeaderMatchPolicyConfig     = kubernetes.RateLimitHeaderMatchPolicyConfig
	RateLimitSourceCIDRPolicyConfig      = kubernetes.RateLimitSourceCIDRPolicyConfig
	RateLimitPathMatchPolicyConfig       = kubernetes.RateLimitPathMatchPolicyConfig
	RequestBufferPolicyConfig            = kubernetes.RequestBufferPolicyConfig
	BTPTimeoutPolicyConfig               = kubernetes.BTPTimeoutPolicyConfig
	BTPTCPTimeoutPolicyConfig            = kubernetes.BTPTCPTimeoutPolicyConfig
	BTPHTTPTimeoutPolicyConfig           = kubernetes.BTPHTTPTimeoutPolicyConfig
	ResponseOverridePolicyConfig         = kubernetes.ResponseOverridePolicyConfig
	ResponseOverrideMatchPolicyConfig    = kubernetes.ResponseOverrideMatchPolicyConfig
	StatusCodeMatchPolicyConfig          = kubernetes.StatusCodeMatchPolicyConfig
	StatusCodeRangePolicyConfig          = kubernetes.StatusCodeRangePolicyConfig
	ResponseOverrideResponsePolicyConfig = kubernetes.ResponseOverrideResponsePolicyConfig
	ResponseOverrideBodyPolicyConfig     = kubernetes.ResponseOverrideBodyPolicyConfig
	ValueRefPolicyConfig                 = kubernetes.ValueRefPolicyConfig
	BackendTrafficPolicyTargetRef        = kubernetes.BackendTrafficPolicyTargetRef
	EnvoyExtensionPolicyK8sConfig        = kubernetes.EnvoyExtensionPolicyK8sConfig
	EnvoyExtensionPolicyTargetRef        = kubernetes.EnvoyExtensionPolicyTargetRef
	LuaExtensionPolicyConfig             = kubernetes.LuaExtensionPolicyConfig
	WasmExtensionPolicyConfig            = kubernetes.WasmExtensionPolicyConfig
	WasmCodeSourcePolicyConfig           = kubernetes.WasmCodeSourcePolicyConfig
	WasmHTTPSourcePolicyConfig           = kubernetes.WasmHTTPSourcePolicyConfig
	WasmImageSourcePolicyConfig          = kubernetes.WasmImageSourcePolicyConfig
	ExtProcPolicyConfig                  = kubernetes.ExtProcPolicyConfig
	ExtProcBackendRefPolicyConfig        = kubernetes.ExtProcBackendRefPolicyConfig
	ExtProcProcessingModeConfig          = kubernetes.ExtProcProcessingModeConfig
	ExtProcBodyModeConfig                = kubernetes.ExtProcBodyModeConfig
	ClientTrafficPolicyConfig            = kubernetes.ClientTrafficPolicyConfig
	ClientTrafficPolicyTargetRef         = kubernetes.ClientTrafficPolicyTargetRef
	TCPKeepalivePolicyConfig             = kubernetes.TCPKeepalivePolicyConfig
	ConnectionPolicyConfig               = kubernetes.ConnectionPolicyConfig
	TimeoutPolicyConfig                  = kubernetes.TimeoutPolicyConfig
	HTTPTimeoutPolicyConfig              = kubernetes.HTTPTimeoutPolicyConfig
	HTTP3PolicyConfig                    = kubernetes.HTTP3PolicyConfig
	TLSPolicyConfig                      = kubernetes.TLSPolicyConfig
	ClientValidationPolicyConfig         = kubernetes.ClientValidationPolicyConfig
	SecretRefPolicyConfig                = kubernetes.SecretRefPolicyConfig
	SANMatcherPolicyConfig               = kubernetes.SANMatcherPolicyConfig
	XFCCPolicyConfig                     = kubernetes.XFCCPolicyConfig
	HeadersPolicyConfig                  = kubernetes.HeadersPolicyConfig
	ClientIPDetectionPolicyConfig        = kubernetes.ClientIPDetectionPolicyConfig
	XForwardedForPolicyConfig            = kubernetes.XForwardedForPolicyConfig
	CustomHeaderPolicyConfig             = kubernetes.CustomHeaderPolicyConfig
	CompressionPolicyConfig              = kubernetes.CompressionPolicyConfig
	GzipPolicyConfig                     = kubernetes.GzipPolicyConfig
	BrotliPolicyConfig                   = kubernetes.BrotliPolicyConfig
	ZstdPolicyConfig                     = kubernetes.ZstdPolicyConfig
	RetryPolicyConfig                    = kubernetes.RetryPolicyConfig
	RetryOnPolicyConfig                  = kubernetes.RetryOnPolicyConfig
	PerRetryPolicyConfig                 = kubernetes.PerRetryPolicyConfig
	BackOffPolicyConfig                  = kubernetes.BackOffPolicyConfig
	BackendConfig                        = kubernetes.BackendConfig
	BackendTLSPolicyConfig               = kubernetes.BackendTLSPolicyConfig
	BackendSecretRefConfig               = kubernetes.BackendSecretRefConfig
	BackendCertificateRefConfig          = kubernetes.BackendCertificateRefConfig
	ExtAuthBackendConfig                 = kubernetes.ExtAuthBackendConfig
	ExtProcBackendConfig                 = kubernetes.ExtProcBackendConfig
	HTTPRouteFilterConfig                = kubernetes.HTTPRouteFilterConfig
	DirectResponseFilterConfig           = kubernetes.DirectResponseFilterConfig
	DirectResponseBodyFilterConfig       = kubernetes.DirectResponseBodyFilterConfig
	DirectResponseValueRef               = kubernetes.DirectResponseValueRef
	DirectResponseConfigMapConfig        = kubernetes.DirectResponseConfigMapConfig
	GatewayClassConfig                   = kubernetes.GatewayClassConfig
	EnvoyProxyConfig                     = kubernetes.EnvoyProxyConfig
)

var (
	BuildGatewayObject                  = kubernetes.BuildGatewayObject
	BuildHTTPRouteObject                = kubernetes.BuildHTTPRouteObject
	BuildGRPCRouteObject                = kubernetes.BuildGRPCRouteObject
	BuildSecurityPolicy                 = kubernetes.BuildSecurityPolicy
	BuildBackendTrafficPolicy           = kubernetes.BuildBackendTrafficPolicy
	BuildEnvoyExtensionPolicy           = kubernetes.BuildEnvoyExtensionPolicy
	BuildClientTrafficPolicy            = kubernetes.BuildClientTrafficPolicy
	BuildBackend                        = kubernetes.BuildBackend
	BuildExtAuthBackend                 = kubernetes.BuildExtAuthBackend
	GenerateExtAuthBackendName          = kubernetes.GenerateExtAuthBackendName
	BuildExtProcBackend                 = kubernetes.BuildExtProcBackend
	GenerateExtProcBackendName          = kubernetes.GenerateExtProcBackendName
	GenerateExtProcBackendNameForDomain = kubernetes.GenerateExtProcBackendNameForDomain
	BuildHTTPRouteFilter                = kubernetes.BuildHTTPRouteFilter
	BuildDirectResponseConfigMap        = kubernetes.BuildDirectResponseConfigMap
	BuildGatewayClassObject             = kubernetes.BuildGatewayClassObject
	BuildEnvoyProxyObject               = kubernetes.BuildEnvoyProxyObject
	BuildPodPlacement                   = kubernetes.BuildPodPlacement
	BuildAccessLog                      = kubernetes.BuildAccessLog
	BuildTracing                        = kubernetes.BuildTracing
	BuildMetrics                        = kubernetes.BuildMetrics
	BuildPDB                            = kubernetes.BuildPDB
	BuildStrategy                       = kubernetes.BuildStrategy
)

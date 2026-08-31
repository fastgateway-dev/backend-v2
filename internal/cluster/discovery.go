package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestConnection tests the Kubernetes connection
func (s *Client) TestConnection(ctx context.Context, projectID uuid.UUID) (bool, string, error) {
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
func (s *Client) ListNamespaces(ctx context.Context, projectID uuid.UUID) ([]string, error) {
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
func (s *Client) ListServices(ctx context.Context, projectID uuid.UUID, namespace string) ([]map[string]interface{}, error) {
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
func (s *Client) ListTLSSecrets(ctx context.Context, projectID uuid.UUID, namespace string) ([]TLSSecretInfo, error) {
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
func (s *Client) ListGatewayClasses(ctx context.Context, projectID uuid.UUID) ([]string, error) {
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

// ValidatePrerequisites checks if the Kubernetes cluster has the required prerequisites
// - fastgateway-system namespace must exist
// - Gateway API CRDs must be installed (Gateway, HTTPRoute)
func (s *Client) ValidatePrerequisites(ctx context.Context, apiURL, token string) (*PrerequisiteCheck, error) {
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

	_, err = client.Resource(nsGVR).Get(ctx, kubernetes.FastGatewayNamespace, metav1.GetOptions{})
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
		missing = append(missing, fmt.Sprintf("namespace '%s' does not exist", kubernetes.FastGatewayNamespace))
	}
	if !result.GatewayCRDExists {
		missing = append(missing, "Gateway API CRD (Gateway) is not installed")
	}
	if !result.HTTPRouteCRDExists {
		missing = append(missing, "Gateway API CRD (HTTPRoute) is not installed")
	}

	if len(missing) > 0 {
		result.ErrorMessage = fmt.Sprintf("Prerequisites not met: %s. Please install Gateway API CRDs and create the '%s' namespace before onboarding.",
			joinStrings(missing, "; "), kubernetes.FastGatewayNamespace)
	}

	return result, nil
}

// ValidateEnvoyGatewayInstalled checks if Envoy Gateway controller is installed
// by looking for existing GatewayClasses with the Envoy controller name
// or by checking for deployments in envoy-gateway-system namespace
func (s *Client) ValidateEnvoyGatewayInstalled(ctx context.Context, projectID uuid.UUID) (bool, string, error) {
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
				if controllerName == kubernetes.EnvoyGatewayControllerName {
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

// IsRateLimitAvailable checks if rate limiting is available by reading the envoy-gateway-config ConfigMap
func (s *Client) IsRateLimitAvailable(ctx context.Context, projectID uuid.UUID) (bool, error) {
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

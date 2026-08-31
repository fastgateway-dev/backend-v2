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
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// CreateSecurityPolicy creates an Envoy Gateway SecurityPolicy resource in Kubernetes.
// If the resource already exists (e.g. from a partial previous deploy), it falls back to update.
func (s *Client) CreateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.SecurityPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	securityPolicy := kubernetes.BuildSecurityPolicy(config)
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
func (s *Client) UpdateSecurityPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.SecurityPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.SecurityPolicyGVR

	securityPolicy := kubernetes.BuildSecurityPolicy(config)
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
func (s *Client) DeleteSecurityPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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
func (s *Client) CreateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	backendTrafficPolicy := kubernetes.BuildBackendTrafficPolicy(config)
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
func (s *Client) UpdateBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.BackendTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.BackendTrafficPolicyGVR

	backendTrafficPolicy := kubernetes.BuildBackendTrafficPolicy(config)
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
func (s *Client) DeleteBackendTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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
func (s *Client) CreateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error {
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
func (s *Client) UpdateEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, policy *unstructured.Unstructured) error {
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
func (s *Client) DeleteEnvoyExtensionPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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

// CreateClientTrafficPolicy creates an Envoy Gateway ClientTrafficPolicy resource in Kubernetes.
// If the resource already exists, it falls back to update.
func (s *Client) CreateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.ClientTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	clientTrafficPolicy := kubernetes.BuildClientTrafficPolicy(config)
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

// UpdateClientTrafficPolicy updates an Envoy Gateway ClientTrafficPolicy resource in Kubernetes
func (s *Client) UpdateClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, config *kubernetes.ClientTrafficPolicyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.ClientTrafficPolicyGVR

	clientTrafficPolicy := kubernetes.BuildClientTrafficPolicy(config)
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
func (s *Client) DeleteClientTrafficPolicy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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

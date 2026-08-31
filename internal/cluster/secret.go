package cluster

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
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// =============================================================================
// API Key Secret Management
// =============================================================================

// GetAPIKeySecretName returns the Kubernetes secret name for a client's API key
func (s *Client) GetAPIKeySecretName(clientID uuid.UUID) string {
	return kubernetes.APIKeySecretForClientName(clientID.String())
}

// CreateAPIKeySecret creates or updates a Kubernetes Secret containing an API key for a client
func (s *Client) CreateAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID, apiKey string) error {
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
func (s *Client) GetAPIKeyFromSecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) (string, error) {
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
func (s *Client) DeleteAPIKeySecret(ctx context.Context, projectID uuid.UUID, clientID uuid.UUID) error {
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
func (s *Client) CreateOrUpdateSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, data map[string][]byte) error {
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
func (s *Client) DeleteSecret(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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
func (s *Client) GetSecretData(ctx context.Context, projectID uuid.UUID, namespace, name, key string) ([]byte, error) {
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

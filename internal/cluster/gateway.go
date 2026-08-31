package cluster

import (
	"context"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// CreateGateway creates a Gateway resource in Kubernetes
func (s *Client) CreateGateway(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayConfig) error {
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

	gateway := kubernetes.BuildGatewayObject(config)

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, gateway, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create gateway: %w", err)
	}

	return nil
}

// DeleteGateway deletes a Gateway resource from Kubernetes
func (s *Client) DeleteGateway(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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

// CreateGatewayClass creates a GatewayClass resource in Kubernetes
func (s *Client) CreateGatewayClass(ctx context.Context, projectID uuid.UUID, config *kubernetes.GatewayClassConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	gatewayClass := kubernetes.BuildGatewayClassObject(config)

	_, err = client.Resource(gvr).Create(ctx, gatewayClass, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create gatewayclass: %w", err)
	}

	return nil
}

// DeleteGatewayClass deletes a GatewayClass resource from Kubernetes
func (s *Client) DeleteGatewayClass(ctx context.Context, projectID uuid.UUID, name string) error {
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
func (s *Client) CreateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *kubernetes.EnvoyProxyConfig) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyproxies",
	}

	envoyProxy := kubernetes.BuildEnvoyProxyObject(config)

	_, err = client.Resource(gvr).Namespace(config.Namespace).Create(ctx, envoyProxy, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create envoyproxy: %w", err)
	}

	return nil
}

// UpdateEnvoyProxy updates an EnvoyProxy resource in Kubernetes
func (s *Client) UpdateEnvoyProxy(ctx context.Context, projectID uuid.UUID, config *kubernetes.EnvoyProxyConfig) error {
	// Delete and recreate for simplicity
	_ = s.DeleteEnvoyProxy(ctx, projectID, config.Namespace, config.Name)
	return s.CreateEnvoyProxy(ctx, projectID, config)
}

// DeleteEnvoyProxy deletes an EnvoyProxy resource from Kubernetes
func (s *Client) DeleteEnvoyProxy(ctx context.Context, projectID uuid.UUID, namespace, name string) error {
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

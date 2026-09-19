package cluster

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// InClusterDynamicClient builds a dynamic client against the pod's own cluster,
// with no Project row required. Used for the control (issuer) cluster.
func InClusterDynamicClient() (dynamic.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	return dynamic.NewForConfig(config)
}

// ControlPlaneClient applies cert-manager CRDs and Secrets to the backend's own
// (control) cluster. Namespaced objects go in `namespace`.
type ControlPlaneClient struct {
	dyn       dynamic.Interface
	namespace string
}

func NewControlPlaneClient(dyn dynamic.Interface, namespace string) *ControlPlaneClient {
	return &ControlPlaneClient{dyn: dyn, namespace: namespace}
}

func (c *ControlPlaneClient) Namespace() string { return c.namespace }

func (c *ControlPlaneClient) ApplyClusterScoped(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	return c.apply(ctx, c.dyn.Resource(gvr), obj)
}

func (c *ControlPlaneClient) ApplyNamespaced(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	obj.SetNamespace(c.namespace)
	return c.apply(ctx, c.dyn.Resource(gvr).Namespace(c.namespace), obj)
}

func (c *ControlPlaneClient) apply(ctx context.Context, ri dynamic.ResourceInterface, obj *unstructured.Unstructured) error {
	_, err := ri.Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if k8serrors.IsAlreadyExists(err) {
		return updateUnstructuredWithRetry(ctx, ri, obj.GetName(), obj)
	}
	return fmt.Errorf("failed to apply %s/%s: %w", obj.GetKind(), obj.GetName(), err)
}

func (c *ControlPlaneClient) Get(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) (*unstructured.Unstructured, error) {
	ri := c.dyn.Resource(gvr)
	if namespaced {
		return ri.Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	}
	return ri.Get(ctx, name, metav1.GetOptions{})
}

func (c *ControlPlaneClient) Delete(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) error {
	ri := c.dyn.Resource(gvr)
	var err error
	if namespaced {
		err = ri.Namespace(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	} else {
		err = ri.Delete(ctx, name, metav1.DeleteOptions{})
	}
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete %s: %w", name, err)
	}
	return nil
}

package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func TestControlPlaneClient_ApplyClusterScoped_CreateThenUpdate(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	cp := NewControlPlaneClient(dyn, "fastgateway-system")
	gvr := kubernetes.CertManagerClusterIssuerGVR

	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
		"metadata": map[string]interface{}{"name": "iss-1"},
		"spec":     map[string]interface{}{"selfSigned": map[string]interface{}{}},
	}}
	require.NoError(t, cp.ApplyClusterScoped(context.Background(), gvr, obj))

	// second apply must not error (update path)
	require.NoError(t, cp.ApplyClusterScoped(context.Background(), gvr, obj))

	got, err := cp.Get(context.Background(), gvr, "iss-1", false)
	require.NoError(t, err)
	assert.Equal(t, "iss-1", got.GetName())
}

func TestControlPlaneClient_ApplyNamespaced_UsesConfiguredNamespace(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	cp := NewControlPlaneClient(dyn, "fastgateway-system")
	gvr := kubernetes.CertManagerCertificateGVR
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
		"metadata": map[string]interface{}{"name": "ca-1"},
		"spec":     map[string]interface{}{"isCA": true},
	}}
	require.NoError(t, cp.ApplyNamespaced(context.Background(), gvr, obj))
	got, err := cp.Get(context.Background(), gvr, "ca-1", true)
	require.NoError(t, err)
	assert.Equal(t, "fastgateway-system", got.GetNamespace())

	_ = corev1.Secret{}
	_ = metav1.ObjectMeta{}
	_ = schema.GroupVersionResource{}
}

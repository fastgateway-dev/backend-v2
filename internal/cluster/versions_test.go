package cluster_test

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestParseImageTagVersion(t *testing.T) {
	cases := []struct {
		image string
		want  string
	}{
		{"docker.io/envoyproxy/gateway:v1.7.0", "1.7.0"},
		{"envoyproxy/gateway:1.7.0", "1.7.0"},
		{"ghcr.io/envoyproxy/gateway:v1.6.2-rc.1", "1.6.2-rc.1"},
		{"envoyproxy/gateway:latest", ""},
		{"envoyproxy/gateway:dev", ""},
		{"envoyproxy/gateway:main", ""},
		{"envoyproxy/gateway@sha256:abc123", ""},
		{"envoyproxy/gateway", ""},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.image, func(t *testing.T) {
			got := cluster.ParseImageTagVersion(c.image)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestParseBundleVersion(t *testing.T) {
	assert.Equal(t, "1.4.1", cluster.ParseBundleVersion("v1.4.1"))
	assert.Equal(t, "1.5.0", cluster.ParseBundleVersion("1.5.0"))
	assert.Equal(t, "", cluster.ParseBundleVersion(""))
	assert.Equal(t, "", cluster.ParseBundleVersion("latest"))
	assert.Equal(t, "", cluster.ParseBundleVersion("v1.4"))
}

// newFakeServiceWith builds a Client whose getClientFor yields a fake
// dynamic client preloaded with the given objects. List kinds must be registered
// up-front for the unstructured fake client to handle List() calls.
func newFakeServiceWith(objects ...runtime.Object) *cluster.Client {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Group: "apps", Version: "v1", Resource: "deployments"}:                               "DeploymentList",
		{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}: "CustomResourceDefinitionList",
	}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objects...)
	return cluster.NewWithClient(client)
}

func TestDetectVersions_HappyPath(t *testing.T) {
	deploy := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      "envoy-gateway",
			"namespace": "envoy-gateway-system",
		},
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "envoy-gateway",
							"image": "docker.io/envoyproxy/gateway:v1.7.0",
						},
					},
				},
			},
		},
	}}
	crd := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata": map[string]interface{}{
			"name": "gateways.gateway.networking.k8s.io",
			"annotations": map[string]interface{}{
				"gateway.networking.k8s.io/bundle-version": "v1.4.1",
			},
		},
	}}

	svc := newFakeServiceWith(deploy, crd)
	got, err := svc.DetectVersions(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "1.7.0", got.EGVersion)
	assert.Equal(t, "docker.io/envoyproxy/gateway:v1.7.0", got.EGImage)
	assert.Equal(t, "deployment/envoy-gateway", got.EGSource)
	assert.Equal(t, "1.4.1", got.GWVersion)
	assert.Equal(t, "crd/gateways.gateway.networking.k8s.io", got.GWSource)
	assert.Empty(t, got.Errors)
}

func TestDetectVersions_FallbackDeploymentName(t *testing.T) {
	deploy := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      "gateway-controller",
			"namespace": "envoy-gateway-system",
		},
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "manager",
							"image": "ghcr.io/envoyproxy/gateway:v1.7.0",
						},
					},
				},
			},
		},
	}}
	svc := newFakeServiceWith(deploy)
	got, err := svc.DetectVersions(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "1.7.0", got.EGVersion)
	assert.Equal(t, "deployment/gateway-controller", got.EGSource)
}

func TestDetectVersions_UnparseableTag(t *testing.T) {
	deploy := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]interface{}{"name": "envoy-gateway", "namespace": "envoy-gateway-system"},
		"spec": map[string]interface{}{
			"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{"image": "envoyproxy/gateway:latest"}},
			}},
		},
	}}
	svc := newFakeServiceWith(deploy)
	got, _ := svc.DetectVersions(context.Background(), uuid.New())
	assert.Equal(t, "", got.EGVersion)
	require.NotEmpty(t, got.Errors)
}

func TestDetectVersions_DeploymentNotFound(t *testing.T) {
	svc := newFakeServiceWith()
	got, _ := svc.DetectVersions(context.Background(), uuid.New())
	assert.Equal(t, "", got.EGVersion)
	assert.Equal(t, "", got.GWVersion)
	require.NotEmpty(t, got.Errors)
}

func TestDetectVersions_CRDMissingAnnotation(t *testing.T) {
	crd := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]interface{}{"name": "gateways.gateway.networking.k8s.io"},
	}}
	svc := newFakeServiceWith(crd)
	got, _ := svc.DetectVersions(context.Background(), uuid.New())
	assert.Equal(t, "", got.GWVersion)
	require.NotEmpty(t, got.Errors)
}

func TestDetectVersions_PartialFailure(t *testing.T) {
	deploy := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]interface{}{"name": "envoy-gateway", "namespace": "envoy-gateway-system"},
		"spec": map[string]interface{}{
			"template": map[string]interface{}{"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{"image": "envoyproxy/gateway:v1.7.0"}},
			}},
		},
	}}
	svc := newFakeServiceWith(deploy)
	got, _ := svc.DetectVersions(context.Background(), uuid.New())
	assert.Equal(t, "1.7.0", got.EGVersion)
	assert.Equal(t, "", got.GWVersion)
	require.NotEmpty(t, got.Errors)
}

func TestDetectVersions_Timeout(t *testing.T) {
	svc := newFakeServiceWith()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, _ := svc.DetectVersions(ctx, uuid.New())
	assert.Equal(t, "", got.EGVersion)
	assert.Equal(t, "", got.GWVersion)
}

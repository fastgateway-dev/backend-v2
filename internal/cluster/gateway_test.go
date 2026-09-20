package cluster

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

var gatewaysGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gateways",
}

func TestUpdateGateway_CreatesWhenMissing(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	c := &Client{testClient: dyn}

	config := &kubernetes.GatewayConfig{
		Name:             "gw-1",
		Namespace:        "project-ns",
		GatewayClassName: "envoy-gateway",
		Hostname:         "example.com",
		TLSMode:          "no_tls",
		HTTPPort:         80,
	}

	err := c.UpdateGateway(context.Background(), uuid.New(), config)
	require.NoError(t, err)

	got, err := dyn.Resource(gatewaysGVR).Namespace("project-ns").Get(context.Background(), "gw-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "gw-1", got.GetName())
	assert.Equal(t, "project-ns", got.GetNamespace())
}

func TestUpdateGateway_UpdatesWhenPresent(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	c := &Client{testClient: dyn}

	config := &kubernetes.GatewayConfig{
		Name:             "gw-2",
		Namespace:        "project-ns",
		GatewayClassName: "envoy-gateway",
		Hostname:         "example.com",
		TLSMode:          "no_tls",
		HTTPPort:         80,
	}

	// First call creates it, since the Gateway does not exist yet.
	require.NoError(t, c.UpdateGateway(context.Background(), uuid.New(), config))

	created, err := dyn.Resource(gatewaysGVR).Namespace("project-ns").Get(context.Background(), "gw-2", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "gw-2", created.GetName())

	// Second call, with a changed field (hostname), must update the existing
	// object without an AlreadyExists error, and the change must be
	// reflected when read back from the fake.
	config.Hostname = "changed.example.com"
	err = c.UpdateGateway(context.Background(), uuid.New(), config)
	require.NoError(t, err)

	updated, err := dyn.Resource(gatewaysGVR).Namespace("project-ns").Get(context.Background(), "gw-2", metav1.GetOptions{})
	require.NoError(t, err)

	listeners, found, err := unstructured.NestedSlice(updated.Object, "spec", "listeners")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, listeners, 1)
	listener, ok := listeners[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "changed.example.com", listener["hostname"])
}

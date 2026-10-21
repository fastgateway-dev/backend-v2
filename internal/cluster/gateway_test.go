package cluster

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"

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
		Name:              "gw-1",
		Namespace:         "project-ns",
		GatewayClassName:  "envoy-gateway",
		Hostname:          "example.com",
		HostnameListeners: []kubernetes.HostnameListener{{Name: "http", Protocol: "HTTP", Port: 80}},
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
		Name:              "gw-2",
		Namespace:         "project-ns",
		GatewayClassName:  "envoy-gateway",
		Hostname:          "example.com",
		HostnameListeners: []kubernetes.HostnameListener{{Name: "http", Protocol: "HTTP", Port: 80}},
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

// The stream's Gateway is written by every claim change and by the startup
// reconcile, so a concurrent writer can bump its resourceVersion between our
// Get and Update. A Conflict must re-read and retry, not fail the write.
func TestUpdateGateway_RetriesOnConflict(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	c := &Client{testClient: dyn}
	config := &kubernetes.GatewayConfig{
		Name:              "gw-3",
		Namespace:         "project-ns",
		GatewayClassName:  "envoy-gateway",
		Hostname:          "example.com",
		HostnameListeners: []kubernetes.HostnameListener{{Name: "http", Protocol: "HTTP", Port: 80}},
	}
	require.NoError(t, c.UpdateGateway(context.Background(), uuid.New(), config))

	conflicts := 0
	dyn.PrependReactor("update", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
		if conflicts == 0 {
			conflicts++
			return true, nil, k8serrors.NewConflict(gatewaysGVR.GroupResource(), "gw-3", assert.AnError)
		}
		return false, nil, nil // fall through to the tracker
	})
	dyn.ClearActions()

	config.Hostname = "changed.example.com"
	require.NoError(t, c.UpdateGateway(context.Background(), uuid.New(), config))
	assert.Equal(t, 1, conflicts)

	gets, updates := 0, 0
	for _, a := range dyn.Actions() {
		if a.GetResource() != gatewaysGVR {
			continue
		}
		switch a.GetVerb() {
		case "get":
			gets++
		case "update":
			updates++
		}
	}
	assert.Equal(t, 2, gets, "the retry re-reads the Gateway for a fresh resourceVersion")
	assert.Equal(t, 2, updates)

	updated, err := dyn.Resource(gatewaysGVR).Namespace("project-ns").Get(context.Background(), "gw-3", metav1.GetOptions{})
	require.NoError(t, err)
	listeners, _, err := unstructured.NestedSlice(updated.Object, "spec", "listeners")
	require.NoError(t, err)
	require.Len(t, listeners, 1)
	assert.Equal(t, "changed.example.com", listeners[0].(map[string]interface{})["hostname"])
}

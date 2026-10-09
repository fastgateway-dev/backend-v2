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
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func tcpCfg(port int) *kubernetes.TCPRouteConfig {
	return &kubernetes.TCPRouteConfig{
		Name:        "tcp-1",
		Namespace:   "project-ns",
		GatewayName: "gw",
		SectionName: "l4-tcp-5432",
		Backends:    []kubernetes.L4Backend{{Service: "db", Namespace: "project-ns", Port: port, Weight: 1}},
	}
}

func udpCfg(port int) *kubernetes.UDPRouteConfig {
	return &kubernetes.UDPRouteConfig{
		Name:        "udp-1",
		Namespace:   "project-ns",
		GatewayName: "gw",
		SectionName: "l4-udp-53",
		Backends:    []kubernetes.L4Backend{{Service: "dns", Namespace: "project-ns", Port: port, Weight: 1}},
	}
}

func TestTCPRoute_CreateThenCreateFallsBackToUpdate(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	c := &Client{testClient: dyn}
	ctx := context.Background()

	require.NoError(t, c.CreateTCPRoute(ctx, uuid.New(), tcpCfg(5432)))
	got, err := dyn.Resource(kubernetes.TCPRouteGVR).Namespace("project-ns").Get(ctx, "tcp-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "TCPRoute", got.GetKind())

	// Second create hits AlreadyExists and must fall back to update.
	require.NoError(t, c.CreateTCPRoute(ctx, uuid.New(), tcpCfg(6543)))
	got, err = dyn.Resource(kubernetes.TCPRouteGVR).Namespace("project-ns").Get(ctx, "tcp-1", metav1.GetOptions{})
	require.NoError(t, err)
	rules, found, err := unstructured.NestedSlice(got.Object, "spec", "rules")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, rules, 1)
}

func TestTCPRoute_UpdateMissingFails(t *testing.T) {
	c := &Client{testClient: dynamicfake.NewSimpleDynamicClient(scheme.Scheme)}
	require.Error(t, c.UpdateTCPRoute(context.Background(), uuid.New(), tcpCfg(5432)))
}

func TestTCPRoute_DeleteIssuesDeleteOnTCPRouteGVR(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	c := &Client{testClient: dyn}
	ctx := context.Background()
	require.NoError(t, c.CreateTCPRoute(ctx, uuid.New(), tcpCfg(5432)))

	require.NoError(t, c.DeleteTCPRoute(ctx, uuid.New(), "project-ns", "tcp-1"))
	_, err := dyn.Resource(kubernetes.TCPRouteGVR).Namespace("project-ns").Get(ctx, "tcp-1", metav1.GetOptions{})
	require.True(t, k8serrors.IsNotFound(err))

	var deleted bool
	for _, a := range dyn.Actions() {
		if a.GetVerb() == "delete" && a.GetResource() == kubernetes.TCPRouteGVR {
			deleted = true
		}
	}
	assert.True(t, deleted, "expected a delete action on TCPRouteGVR")

	// Idempotent: deleting a missing route is not an error.
	require.NoError(t, c.DeleteTCPRoute(ctx, uuid.New(), "project-ns", "tcp-1"))
}

func TestUDPRoute_CreateUpdateDelete(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	c := &Client{testClient: dyn}
	ctx := context.Background()

	require.NoError(t, c.CreateUDPRoute(ctx, uuid.New(), udpCfg(53)))
	got, err := dyn.Resource(kubernetes.UDPRouteGVR).Namespace("project-ns").Get(ctx, "udp-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "UDPRoute", got.GetKind())

	require.NoError(t, c.UpdateUDPRoute(ctx, uuid.New(), udpCfg(5353)))
	require.NoError(t, c.CreateUDPRoute(ctx, uuid.New(), udpCfg(53))) // fallback to update

	require.NoError(t, c.DeleteUDPRoute(ctx, uuid.New(), "project-ns", "udp-1"))
	_, err = dyn.Resource(kubernetes.UDPRouteGVR).Namespace("project-ns").Get(ctx, "udp-1", metav1.GetOptions{})
	require.True(t, k8serrors.IsNotFound(err))
	require.NoError(t, c.DeleteUDPRoute(ctx, uuid.New(), "project-ns", "udp-1"))
}

func TestCreateReferenceGrant_IncludesL4RouteKinds(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	c := &Client{testClient: dyn}
	ctx := context.Background()

	require.NoError(t, c.CreateReferenceGrant(ctx, uuid.New(), &ReferenceGrantConfig{
		Name: "rg", FromNamespaces: []string{"a"}, ToNamespace: "b", ToKinds: []string{"Service"},
	}))
	got, err := c.GetReferenceGrant(ctx, uuid.New(), "b", "rg")
	require.NoError(t, err)
	from, _, err := unstructured.NestedSlice(got.Object, "spec", "from")
	require.NoError(t, err)
	kinds := map[string]bool{}
	for _, f := range from {
		kinds[f.(map[string]interface{})["kind"].(string)] = true
	}
	assert.True(t, kinds["TCPRoute"], "TCPRoute must be a ReferenceGrant from-kind")
	assert.True(t, kinds["UDPRoute"], "UDPRoute must be a ReferenceGrant from-kind")
}

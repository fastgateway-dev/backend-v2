package cluster

import (
	"context"
	"testing"

	"github.com/google/uuid"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func TestGetGateway_ReadsStatusAndReportsNotFound(t *testing.T) {
	gw := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]interface{}{"name": "str-db", "namespace": "ns"},
		"status": map[string]interface{}{
			"addresses": []interface{}{map[string]interface{}{"type": "IPAddress", "value": "10.0.0.5"}},
		},
	}}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{kubernetes.GatewayGVR: "GatewayList"})
	if _, err := fake.Resource(kubernetes.GatewayGVR).Namespace("ns").Create(context.Background(), gw, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed gateway: %v", err)
	}
	c := NewWithClient(fake)

	got, err := c.GetGateway(context.Background(), uuid.New(), "ns", "str-db")
	if err != nil {
		t.Fatalf("get existing gateway: %v", err)
	}
	addrs, _, _ := unstructured.NestedSlice(got.Object, "status", "addresses")
	if len(addrs) != 1 || addrs[0].(map[string]interface{})["value"] != "10.0.0.5" {
		t.Fatalf("status.addresses = %v, want one 10.0.0.5 entry", addrs)
	}

	_, err = c.GetGateway(context.Background(), uuid.New(), "ns", "str-missing")
	if !k8serrors.IsNotFound(err) {
		t.Fatalf("missing gateway: want a NotFound error, got %v", err)
	}
}

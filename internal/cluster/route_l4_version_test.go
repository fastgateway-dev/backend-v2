package cluster

import (
	"context"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func TestCreateTCPRoute_WritesAtResolvedGVR(t *testing.T) {
	scheme := runtime.NewScheme()
	fake := dynamicfake.NewSimpleDynamicClient(scheme)
	c := NewWithClient(fake)
	cfg := &kubernetes.TCPRouteConfig{Name: "r", Namespace: "ns", GatewayName: "gw", SectionName: "l4-tcp-1"}

	if err := c.CreateTCPRoute(context.Background(), uuid.New(), cfg, kubernetes.TCPRouteGVRV1); err != nil {
		t.Fatalf("create v1: %v", err)
	}
	got, err := fake.Resource(kubernetes.TCPRouteGVRV1).Namespace("ns").Get(context.Background(), "r", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected object at v1 GVR: %v", err)
	}
	if av := got.GetAPIVersion(); av != "gateway.networking.k8s.io/v1" {
		t.Errorf("object apiVersion = %q, want .../v1", av)
	}
}

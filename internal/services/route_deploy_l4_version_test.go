package services

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func TestL4RouteGVR(t *testing.T) {
	cases := []struct {
		proto models.RouteProtocol
		useV1 bool
		want  schema.GroupVersionResource
	}{
		{models.RouteProtocolTCP, true, kubernetes.TCPRouteGVRV1},
		{models.RouteProtocolTCP, false, kubernetes.TCPRouteGVR},
		{models.RouteProtocolUDP, true, kubernetes.UDPRouteGVRV1},
		{models.RouteProtocolUDP, false, kubernetes.UDPRouteGVR},
	}
	for _, c := range cases {
		if got := l4RouteGVR(c.proto, c.useV1); got != c.want {
			t.Errorf("l4RouteGVR(%v,%v) = %+v, want %+v", c.proto, c.useV1, got, c.want)
		}
	}
}

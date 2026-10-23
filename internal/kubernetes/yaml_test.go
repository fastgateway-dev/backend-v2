package kubernetes_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func TestMarshalRouteYAML_DropsStatusAndNullTimestamp(t *testing.T) {
	t.Run("TCPRoute", func(t *testing.T) {
		obj := kubernetes.BuildTCPRouteObject(kubernetes.TCPRouteConfig{
			Name: "r", Namespace: "ns", GatewayName: "str-s", SectionName: "l4-tcp-5432",
			Backends: []kubernetes.L4Backend{{Service: "pg", Namespace: "ns", Port: 5432, Weight: 100}},
		}, "gateway.networking.k8s.io/v1alpha2")
		require.IsType(t, &gatewayv1alpha2.TCPRoute{}, obj)

		out, err := kubernetes.MarshalRouteYAML(obj)
		require.NoError(t, err)
		s := string(out)
		assert.Contains(t, s, "kind: TCPRoute")
		assert.Contains(t, s, "sectionName")
		assert.NotContains(t, s, "status:")
		assert.NotContains(t, s, "parents:")
		assert.NotContains(t, s, "creationTimestamp")
	})

	t.Run("HTTPRoute", func(t *testing.T) {
		obj := kubernetes.BuildHTTPRouteObject(&kubernetes.HTTPRouteConfig{
			Name: "r", Namespace: "ns", GatewayName: "gw", Hostname: "a.example.com",
		})
		require.NotNil(t, obj)

		out, err := kubernetes.MarshalRouteYAML(obj)
		require.NoError(t, err)
		s := string(out)
		assert.Contains(t, s, "kind: HTTPRoute")
		assert.Contains(t, s, "a.example.com")
		assert.NotContains(t, s, "status:")
		assert.NotContains(t, s, "parents:")
		assert.NotContains(t, s, "creationTimestamp")
	})
}

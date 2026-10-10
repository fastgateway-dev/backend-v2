package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestBuildGatewayObject_L4Listeners(t *testing.T) {
	obj := BuildGatewayObject(&GatewayConfig{
		Name:             "str-s",
		Namespace:        "ns1",
		GatewayClassName: "pub",
		Listeners: []L4Listener{
			{Name: "l4-tcp-5432", Protocol: "TCP", Port: 5432},
			{Name: "l4-udp-53", Protocol: "UDP", Port: 53},
		},
	})
	require.NotNil(t, obj)
	ls, found, err := unstructured.NestedSlice(obj.Object, "spec", "listeners")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, ls, 2)

	tcp := ls[0].(map[string]interface{})
	assert.Equal(t, "l4-tcp-5432", tcp["name"])
	assert.Equal(t, "TCP", tcp["protocol"])
	assert.Equal(t, int64(5432), tcp["port"])
	assert.NotContains(t, tcp, "hostname")
	assert.NotContains(t, tcp, "tls")
	assert.Equal(t, map[string]interface{}{
		"kinds": []interface{}{map[string]interface{}{"kind": "TCPRoute"}},
	}, tcp["allowedRoutes"])

	udp := ls[1].(map[string]interface{})
	assert.Equal(t, "UDP", udp["protocol"])
	assert.Equal(t, map[string]interface{}{
		"kinds": []interface{}{map[string]interface{}{"kind": "UDPRoute"}},
	}, udp["allowedRoutes"])
}

func TestBuildGatewayObject_NoL4ListenersKeepsDomainBehavior(t *testing.T) {
	obj := BuildGatewayObject(&GatewayConfig{Name: "g", Namespace: "n", GatewayClassName: "c", Hostname: "a.example.com", HostnameListeners: []HostnameListener{{Name: "http", Protocol: "HTTP", Port: 80}}})
	ls, _, _ := unstructured.NestedSlice(obj.Object, "spec", "listeners")
	require.Len(t, ls, 1)
	assert.Equal(t, "HTTP", ls[0].(map[string]interface{})["protocol"])
}

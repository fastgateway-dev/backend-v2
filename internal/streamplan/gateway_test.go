package streamplan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/streamplan"
)

func TestBuildStreamGatewayConfig_ListenersFromClaims(t *testing.T) {
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{
		Name: "s", K8sGatewayName: "str-s", K8sGatewayClass: "pub",
		Ports: models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 53}},
	})
	assert.Equal(t, "str-s", cfg.Name)
	assert.Equal(t, "pub", cfg.GatewayClassName)
	assert.Equal(t, []streamplan.StreamListener{
		{Name: "l4-tcp-5432", Protocol: "TCP", Port: 5432},
		{Name: "l4-udp-53", Protocol: "UDP", Port: 53},
	}, cfg.Listeners, "one listener per claim, in claim order")
}

func TestBuildStreamGatewayConfig_NoClaimsNoListeners(t *testing.T) {
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{Name: "s", K8sGatewayName: "str-s", K8sGatewayClass: "pub"})
	assert.Empty(t, cfg.Listeners)
}

func TestBuildStreamGatewayConfig_NamespaceAndNoHostname(t *testing.T) {
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{Namespace: "ns1", K8sGatewayName: "str-s", K8sGatewayClass: "pub"})
	assert.Equal(t, "ns1", cfg.Namespace)
	assert.Empty(t, cfg.Hostname)
}

func TestListenerName(t *testing.T) {
	assert.Equal(t, "l4-tcp-5432", streamplan.ListenerName("TCP", 5432))
	assert.Equal(t, "l4-udp-53", streamplan.ListenerName("UDP", 53))
}

func TestStreamGatewayConfig_BuildsL4Gateway(t *testing.T) {
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{Namespace: "ns1", K8sGatewayName: "str-s", K8sGatewayClass: "pub",
		Ports: models.StreamPorts{{Transport: "UDP", Port: 53}}})
	obj := kubernetes.BuildGatewayObject(&cfg)
	require.NotNil(t, obj)
	ls, found, err := unstructuredSlice(obj.Object, "spec", "listeners")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, ls, 1)
	l := ls[0].(map[string]interface{})
	assert.Equal(t, "UDP", l["protocol"])
	assert.Equal(t, int64(53), l["port"])
	assert.NotContains(t, l, "hostname")
}

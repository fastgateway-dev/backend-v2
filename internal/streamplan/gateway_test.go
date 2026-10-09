package streamplan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/streamplan"
)

func listenerSet(ls []streamplan.StreamListener) map[string]int {
	m := make(map[string]int, len(ls))
	for _, l := range ls {
		m[l.Name] = l.Port
	}
	return m
}

func TestBuildStreamGatewayConfig_EmptyUsesPlaceholder(t *testing.T) {
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{Name: "s", K8sGatewayName: "str-s", K8sGatewayClass: "pub"}, nil)
	require.Len(t, cfg.Listeners, 1)
	assert.Equal(t, "TCP", cfg.Listeners[0].Protocol)
	assert.Equal(t, streamplan.PlaceholderPort, cfg.Listeners[0].Port)
	assert.Equal(t, "l4-placeholder", cfg.Listeners[0].Name)
	assert.Equal(t, "str-s", cfg.Name)
	assert.Equal(t, "pub", cfg.GatewayClassName)
}

func TestBuildStreamGatewayConfig_ProjectsRoutes(t *testing.T) {
	routes := []models.Route{
		{Protocol: models.RouteProtocolTCP, Config: models.RouteConfig{ListenerPort: 5432}},
		{Protocol: models.RouteProtocolUDP, Config: models.RouteConfig{ListenerPort: 53}},
	}
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{K8sGatewayName: "str-s", K8sGatewayClass: "pub"}, routes)
	ports := listenerSet(cfg.Listeners)
	assert.Equal(t, 5432, ports["l4-tcp-5432"])
	assert.Equal(t, 53, ports["l4-udp-53"])
	assert.NotContains(t, ports, "l4-placeholder")
	assert.NotContains(t, ports, "placeholder")
	assert.Len(t, cfg.Listeners, 2)
}

func TestBuildStreamGatewayConfig_NamespaceAndNoHostname(t *testing.T) {
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{Namespace: "ns1", K8sGatewayName: "str-s", K8sGatewayClass: "pub"}, nil)
	assert.Equal(t, "ns1", cfg.Namespace)
	assert.Empty(t, cfg.Hostname)
}

func TestListenerName(t *testing.T) {
	assert.Equal(t, "l4-tcp-5432", streamplan.ListenerName("TCP", 5432))
	assert.Equal(t, "l4-udp-53", streamplan.ListenerName("UDP", 53))
}

func TestStreamGatewayConfig_BuildsL4Gateway(t *testing.T) {
	cfg := streamplan.BuildStreamGatewayConfig(models.Stream{Namespace: "ns1", K8sGatewayName: "str-s", K8sGatewayClass: "pub"},
		[]models.Route{{Protocol: models.RouteProtocolUDP, Config: models.RouteConfig{ListenerPort: 53}}})
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

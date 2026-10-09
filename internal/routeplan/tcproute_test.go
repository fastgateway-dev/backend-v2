package routeplan_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
)

func l4TestRoute(proto models.RouteProtocol, port int) models.Route {
	return models.Route{
		ID:           uuid.New(),
		Protocol:     proto,
		K8sRouteName: "pg-route",
		Config: models.RouteConfig{
			ListenerPort: port,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Service: "pg-a", Namespace: "db", Port: 5432, Weight: 90},
				{Type: models.BackendTypeKubernetes, Service: "pg-b", Namespace: "db", Port: 5432, Weight: 10},
			},
		},
	}
}

func l4TestStream() models.Stream {
	return models.Stream{ID: uuid.New(), Namespace: "streams", K8sGatewayName: "str-s"}
}

func TestBuildTCPRouteConfig_WeightedBackends(t *testing.T) {
	route := l4TestRoute(models.RouteProtocolTCP, 5432)
	stream := l4TestStream()

	cfg := routeplan.BuildTCPRouteConfig(route, stream)

	assert.Equal(t, "pg-route", cfg.Name)
	assert.Equal(t, "streams", cfg.Namespace)
	assert.Equal(t, "str-s", cfg.GatewayName)
	assert.Equal(t, "l4-tcp-5432", cfg.SectionName)
	require.Len(t, cfg.Backends, 2)
	assert.Equal(t, kubernetes.L4Backend{Service: "pg-a", Namespace: "db", Port: 5432, Weight: 90}, cfg.Backends[0])
	assert.Equal(t, kubernetes.L4Backend{Service: "pg-b", Namespace: "db", Port: 5432, Weight: 10}, cfg.Backends[1])
	assert.Equal(t, route.ID.String(), cfg.Labels[kubernetes.KeyRouteID])
}

func TestBuildUDPRouteConfig_SectionName(t *testing.T) {
	route := l4TestRoute(models.RouteProtocolUDP, 53)
	cfg := routeplan.BuildUDPRouteConfig(route, l4TestStream())

	assert.Equal(t, "l4-udp-53", cfg.SectionName)
	assert.Equal(t, "str-s", cfg.GatewayName)
	require.Len(t, cfg.Backends, 2)
	assert.Equal(t, 90, cfg.Backends[0].Weight)
}

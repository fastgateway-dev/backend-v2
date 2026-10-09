package routeplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/streamplan"
)

// l4Backends converts a route's weighted backends into L4 backends. Only
// in-cluster Kubernetes Service backends are supported for L4 routes; external
// backends are rejected by validation before reaching the plan.
func l4Backends(route models.Route) []kubernetes.L4Backend {
	backends := make([]kubernetes.L4Backend, 0, len(route.Config.Backends))
	for _, b := range route.Config.Backends {
		backends = append(backends, kubernetes.L4Backend{
			Service:   b.Service,
			Namespace: b.Namespace,
			Port:      b.Port,
			Weight:    b.Weight,
		})
	}
	return backends
}

// BuildTCPRouteConfig assembles the TCPRouteConfig for a TCP route attached to
// the stream's Gateway listener.
//
// Pure: no receiver, no repository access, no clock, no environment.
func BuildTCPRouteConfig(route models.Route, stream models.Stream) kubernetes.TCPRouteConfig {
	return kubernetes.TCPRouteConfig{
		Name:        route.K8sRouteName,
		Namespace:   stream.Namespace,
		GatewayName: stream.K8sGatewayName,
		SectionName: streamplan.ListenerName(route.Transport(), route.Config.ListenerPort),
		Backends:    l4Backends(route),
		Labels:      kubernetes.ForRoute(stream.ID.String(), route.ID.String()),
	}
}

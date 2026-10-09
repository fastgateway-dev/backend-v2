package routeplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/streamplan"
)

// BuildUDPRouteConfig assembles the UDPRouteConfig for a UDP route attached to
// the stream's Gateway listener.
//
// Pure: no receiver, no repository access, no clock, no environment.
func BuildUDPRouteConfig(route models.Route, stream models.Stream) kubernetes.UDPRouteConfig {
	return kubernetes.UDPRouteConfig{
		Name:        route.K8sRouteName,
		Namespace:   stream.Namespace,
		GatewayName: stream.K8sGatewayName,
		SectionName: streamplan.ListenerName(route.Transport(), route.Config.ListenerPort),
		Backends:    l4Backends(route),
		Labels:      kubernetes.ForRoute(stream.ID.String(), route.ID.String()),
	}
}

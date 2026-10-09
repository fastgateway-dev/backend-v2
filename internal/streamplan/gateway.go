// Package streamplan builds Kubernetes manifest configuration for L4 streams.
//
// Like domainplan and routeplan it performs no I/O: inputs are models values,
// outputs are kubernetes.*Config values.
package streamplan

import (
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// PlaceholderPort is the reserved port of the placeholder listener emitted
// when a stream has no active L4 routes (a Gateway needs at least one listener).
const PlaceholderPort = 60000

const placeholderListenerName = "l4-placeholder"

// StreamListener is a TCP/UDP Gateway listener (Protocol is "TCP" or "UDP").
type StreamListener = kubernetes.L4Listener

// ListenerName returns the Gateway listener name for a protocol/port, e.g. "l4-tcp-5432".
func ListenerName(protocol string, port int) string {
	return fmt.Sprintf("l4-%s-%d", strings.ToLower(protocol), port)
}

// BuildStreamGatewayConfig projects the stream's active L4 routes onto Gateway
// listeners. With no routes a single placeholder TCP listener is emitted;
// once real routes exist the placeholder is dropped.
func BuildStreamGatewayConfig(stream models.Stream, routes []models.Route) kubernetes.GatewayConfig {
	cfg := kubernetes.GatewayConfig{
		Name:             stream.K8sGatewayName,
		Namespace:        stream.Namespace,
		GatewayClassName: stream.K8sGatewayClass,
	}
	if len(routes) == 0 {
		cfg.Listeners = []StreamListener{{Name: placeholderListenerName, Protocol: "TCP", Port: PlaceholderPort}}
		return cfg
	}
	for _, r := range routes {
		proto := r.Transport()
		port := r.Config.ListenerPort
		cfg.Listeners = append(cfg.Listeners, StreamListener{Name: ListenerName(proto, port), Protocol: proto, Port: port})
	}
	return cfg
}

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

// StreamListener is a TCP/UDP Gateway listener (Protocol is "TCP" or "UDP").
type StreamListener = kubernetes.L4Listener

// ListenerName returns the Gateway listener name for a protocol/port, e.g. "l4-tcp-5432".
func ListenerName(protocol string, port int) string {
	return fmt.Sprintf("l4-%s-%d", strings.ToLower(protocol), port)
}

// BuildStreamGatewayConfig projects the stream's port claims onto Gateway
// listeners: one per claim, in claim order. A stream with no claims yields no
// listeners (the caller deletes the Gateway rather than applying it).
func BuildStreamGatewayConfig(stream models.Stream) kubernetes.GatewayConfig {
	cfg := kubernetes.GatewayConfig{
		Name:             stream.K8sGatewayName,
		Namespace:        stream.Namespace,
		GatewayClassName: stream.K8sGatewayClass,
	}
	for _, c := range stream.Ports {
		cfg.Listeners = append(cfg.Listeners, StreamListener{Name: ListenerName(c.Transport, c.Port), Protocol: c.Transport, Port: c.Port})
	}
	return cfg
}

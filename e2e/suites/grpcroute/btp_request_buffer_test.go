//go:build e2e

package grpcroute

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCBTPRequestBuffer ports grpc_btp_features/test_request_buffer.py,
// but inverts what it proves, because the feature it exercised cannot work.
//
// Envoy Gateway documents request buffering as incompatible with gRPC:
// "Request buffering requires Envoy to fully receive the request before
// forwarding it upstream. This does not work with streaming or
// upgrade-based traffic such as gRPC streaming and WebSocket"
// (https://gateway.envoyproxy.io/docs/tasks/traffic/request-buffering/).
// An end-to-end run confirmed exactly that shape: with
// requestBuffer.limit="1Ki" attached to a gRPC route and its
// BackendTrafficPolicy reporting Accepted=True the whole time, a 4096-byte
// body was polled for three minutes and returned codes.OK on every single
// call -- the limit was simply not enforced. For streaming calls the
// documented outcome is worse still: the request may never be forwarded
// and the call hangs.
//
// A control plane that accepts that field promises a request-size cap it
// does not deliver, with nothing in the route's status to reveal it. So
// RouteService now rejects it (validateGRPCBackendTrafficPolicy), and this
// test asserts the rejection -- the strongest claim available, and one the
// old Python assertion (`returncode == 0 or "rpc error" in stderr`, true of
// almost any outcome including a total hang) could never make.
//
// httproute/request_buffer_test.go still proves the real thing for HTTP:
// an over-limit body is rejected with 413.
func TestGRPCBTPRequestBuffer(t *testing.T) {
	t.Parallel()

	name, match, _ := uniqueMatch(t, "Exact", echoServiceName, "")

	expectCreateRejected(t, services.CreateRouteInput{
		Name:     name,
		Protocol: models.RouteProtocolGRPC,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{match},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			RequestBuffer: &models.RequestBufferConfig{Limit: "1Ki"},
		},
	})
}

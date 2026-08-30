//go:build e2e

package grpcroute

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGRPCBTPCompression ports grpc_btp_features/test_compression.py.
//
// KNOWN LIMITATION: BackendTrafficPolicy.compression configures Envoy's
// HTTP compression filter, which negotiates via the Content-Encoding/
// Accept-Encoding headers -- gRPC's own wire compression (grpc-encoding/
// grpc-accept-encoding) is a separate mechanism the filter does not
// negotiate, and the harness's GRPCTyped client doesn't advertise
// grpc-accept-encoding, so there is no client-observable signal here that
// compression actually engaged (unlike httproute's TestCompression, which
// can assert on the HTTP Content-Encoding response header). This port
// therefore verifies what IS observable and was previously not
// meaningfully checked at all (the old assertion, `returncode == 0 or "rpc
// error" in stderr`, is true of almost any outcome including total
// failure): the route deploys with this policy attached and correctly
// serves gRPC traffic.
func TestGRPCBTPCompression(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")

	cfg := services.CreateRouteInput{
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
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
			Compression: []models.CompressionConfig{
				{Type: models.CompressionTypeGzip},
				{Type: models.CompressionTypeBrotli},
				{Type: models.CompressionTypeZstd},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	const wantBody = "hello-compression"
	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, wantBody, callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("compression: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("compression: got code %v, want %v", res.Code, codes.OK)
	}

	res, resp, err := echoCall(ctx, wantBody, callOpt)
	if err != nil {
		t.Fatalf("compression: request: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("compression: got code %v, want %v", res.Code, codes.OK)
	}
	if resp.Body != wantBody {
		t.Fatalf("compression: got echoed body %q, want %q", resp.Body, wantBody)
	}
}

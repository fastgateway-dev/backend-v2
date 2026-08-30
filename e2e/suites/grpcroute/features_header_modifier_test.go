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

// TestGRPCHeaderModifier ports grpc_route_features/test_header_modifier.py.
//
// The old assertion (`returncode == 0 or "rpc error" in stderr`) never
// checked the modifier actually added anything. This port asserts the
// configured response headers ("X-Grpc-Set", "X-Grpc-Add") are present in
// the gRPC response's initial headers with their configured values -- a
// real, deterministic signal the filter ran, captured separately from the
// message body via harness.GRPCResult.Header.
func TestGRPCHeaderModifier(t *testing.T) {
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
			ResponseHeaderModifier: &models.HeaderModifier{
				Set: []models.HeaderValue{{Name: "X-Grpc-Set", Value: "grpc-set-value"}},
				Add: []models.HeaderValue{{Name: "X-Grpc-Add", Value: "grpc-add-value"}},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	call := func(ctx context.Context) (*harness.GRPCResult, error) {
		res, _, err := echoCall(ctx, "hello", callOpt)
		return res, err
	}
	res, err := waitForGRPCLive(ctx, call, routeLiveTimeout)
	if err != nil {
		t.Fatalf("header modifier: route never became live: %v", err)
	}
	if res.Code != codes.OK {
		t.Fatalf("header modifier: got code %v, want %v", res.Code, codes.OK)
	}

	res, _, err = echoCall(ctx, "hello", callOpt)
	if err != nil {
		t.Fatalf("header modifier: request: %v", err)
	}
	if got := res.Header.Get("x-grpc-set"); len(got) == 0 || got[0] != "grpc-set-value" {
		t.Fatalf("header modifier: response header x-grpc-set = %v, want [%q]", got, "grpc-set-value")
	}
	if got := res.Header.Get("x-grpc-add"); len(got) == 0 || got[0] != "grpc-add-value" {
		t.Fatalf("header modifier: response header x-grpc-add = %v, want [%q]", got, "grpc-add-value")
	}
}

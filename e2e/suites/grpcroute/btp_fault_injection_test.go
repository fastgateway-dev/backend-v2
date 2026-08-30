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

// TestGRPCBTPFaultInjection ports grpc_btp_features/test_fault_injection.py.
//
// The Python config set httpStatus:503 on a gRPC route's abort fault --
// FaultInjectionAbortConfig requires exactly one of httpStatus/grpcStatus
// (models.FaultInjectionAbortConfig.Validate), and httpStatus has no
// meaningful effect on a gRPC response (gRPC status travels in trailers,
// not :status). This port uses grpcStatus instead, set to
// codes.Aborted (10) at 100% -- deterministic and unambiguous with
// waitForGRPCLive's "not ready yet" code set (Unimplemented/NotFound/
// Unavailable), so every call, including the readiness probe itself, must
// observe exactly codes.Aborted.
func TestGRPCBTPFaultInjection(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	grpcStatus := int(codes.Aborted)
	pct := float32(100)

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
			FaultInjection: &models.FaultInjectionConfig{
				Abort: &models.FaultInjectionAbortConfig{
					GRPCStatus: &grpcStatus,
					Percentage: &pct,
				},
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
		t.Fatalf("fault injection: route never became live: %v", err)
	}
	if res.Code != codes.Aborted {
		t.Fatalf("fault injection: got code %v, want %v (abort configured at 100%%)", res.Code, codes.Aborted)
	}
}

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
	// Polled rather than read once: the policy that produces this outcome
	// is a separate Kubernetes object Envoy Gateway programs AFTER the
	// route, so the route serves traffic un-policied for a short window
	// after deploy -- and WaitForRouteLive/waitForGRPCLive return on the
	// first answer they see, which in that window is the un-policied one.
	// harness.Fixture already waits for the policy to report Accepted;
	// this closes the remaining xDS-push tail. Bounded by routeLiveTimeout,
	// so a policy that never takes effect still fails the test.
	res, err := waitForGRPCResult(ctx, call, func(r *harness.GRPCResult) bool {
		return r.Code == codes.Aborted
	}, routeLiveTimeout)
	if err != nil {
		got := codes.Code(0)
		if res != nil {
			got = res.Code
		}
		t.Fatalf("fault injection: got code %v, want %v (abort configured at 100%%): %v", got, codes.Aborted, err)
	}
}

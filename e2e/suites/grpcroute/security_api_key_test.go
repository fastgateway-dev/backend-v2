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

// TestGRPCAPIKeyDenied ports grpc_security/test_api_key.py.
//
// KNOWN LIMITATION (matches the existing HTTP predecessor,
// security_general_mode/test_api_key.py -- the same repo-wide gap, not
// something new to this port): general-mode apiKeyAuth's credential
// source is a Kubernetes Secret in Envoy Gateway's own APIKeyAuth CRD
// format, and no test fixture anywhere in e2e/deps (see
// e2e/deps/create-secrets.sh) seeds one with a valid key. Without a real
// secret, there's no way to exercise the allow path, so -- exactly like
// the pre-existing HTTP test -- this only verifies the denial path: a
// request without any API key must be rejected as Unauthenticated.
func TestGRPCAPIKeyDenied(t *testing.T) {
	t.Parallel()

	name, match, callOpt := uniqueMatch(t, "Exact", echoServiceName, "")
	secretName := harness.UniqueName(t)

	cfg := services.CreateRouteInput{
		Name:         name,
		Protocol:     models.RouteProtocolGRPC,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches:   []models.RouteMatch{match},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoGRPCPort, Weight: 100},
			},
		},
		SecurityPolicy: &services.SecurityPolicyInput{
			APIKeyAuth: &services.APIKeyAuthInput{SecretName: secretName, HeaderName: "x-api-key"},
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
	if _, err := waitForGRPCCodeIn(ctx, call, routeLiveTimeout, codes.Unauthenticated); err != nil {
		t.Fatalf("api key denied: without x-api-key: %v", err)
	}
}

//go:build e2e

package security

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestGeneralModeAPIKeyDenied ports
// security_general_mode/test_api_key.py:test_api_key_denied_without_key.
//
// KNOWN LIMITATION (see the package doc comment's "Known limitation"
// section, and e2e/suites/grpcroute/security_api_key_test.go's identical,
// already-documented gap for the same general-mode mechanism): general
// mode's apiKeyAuth.secretName references a Kubernetes Secret in Envoy
// Gateway's own APIKeyAuth CRD format that nothing in e2e/deps seeds with
// a real key, and the exact Secret schema is not exercised anywhere else
// in this repository to verify against without a cluster. Without a real
// secret there is no way to exercise the allow path, so -- like the
// Python source and like grpcroute's already-merged port of the identical
// problem -- this only verifies the denial path.
func TestGeneralModeAPIKeyDenied(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)
	secretName := harness.UniqueName(t)

	cfg := services.CreateRouteInput{
		Name:         name,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
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

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	// waitForHTTPStatus only returns nil-error once resp.StatusCode is
	// exactly 401 or 403 (see its doc comment) -- no further assertion is
	// needed, or meaningful, once err == nil.
	if _, err := waitForHTTPStatus(ctx, probe, routeLiveTimeout, 401, 403); err != nil {
		t.Fatalf("api key denied: without x-api-key: %v", err)
	}
}

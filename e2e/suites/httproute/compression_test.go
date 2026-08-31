//go:build e2e

package httproute

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestCompression ports tests/http_route_features/test_compression.py.
func TestCompression(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)

	cfg := services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			// nginx's default page is the compressible payload; without
			// this the client's unique path would 404 on nginx.
			URLRewrite: rewriteTo("/"),
		},
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			Compression: []models.CompressionConfig{
				{Type: models.CompressionTypeGzip},
				{Type: models.CompressionTypeBrotli},
				{Type: models.CompressionTypeZstd},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("Accept-Encoding", "gzip"))
	}
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("compression: route never became live: %v", err)
	}

	// Polled rather than read once: the policy that produces this outcome
	// is a separate Kubernetes object Envoy Gateway programs AFTER the
	// route, so the route serves traffic un-policied for a short window
	// after deploy -- and WaitForRouteLive/waitForGRPCLive return on the
	// first answer they see, which in that window is the un-policied one.
	// harness.Fixture already waits for the policy to report Accepted;
	// this closes the remaining xDS-push tail. Bounded by routeLiveTimeout,
	// so a policy that never takes effect still fails the test.
	resp, err := harness.WaitForResponse(ctx, probe, func(r *harness.Response) bool {
		return r.Header.Get("Content-Encoding") == "gzip"
	}, routeLiveTimeout)
	if err != nil {
		got := ""
		if resp != nil {
			got = resp.Header.Get("Content-Encoding")
		}
		t.Fatalf("compression: got Content-Encoding %q, want %q: %v", got, "gzip", err)
	}
}

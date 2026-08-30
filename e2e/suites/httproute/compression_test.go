//go:build e2e

package httproute

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
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

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("Accept-Encoding", "gzip"))
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("compression: route never became live: %v", err)
	}

	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("compression: got Content-Encoding %q, want %q", got, "gzip")
	}
}

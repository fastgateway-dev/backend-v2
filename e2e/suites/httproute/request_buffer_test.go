//go:build e2e

package httproute

import (
	"bytes"
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestRequestBuffer ports test_request_buffer.py.
//
// The Python config's limit ("10Mi") is impractical to exercise
// end-to-end, so this port uses a much smaller limit ("1Ki" = 1024 bytes)
// and sends bodies clearly on either side of it: 100 bytes (under) should
// pass through to the backend and get a normal 200, and 4096 bytes (over)
// should be rejected by Envoy's buffer filter itself with 413 before ever
// reaching the backend.
func TestRequestBuffer(t *testing.T) {
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
			URLRewrite: rewriteTo("/"),
		},
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
			RequestBuffer: &models.RequestBufferConfig{Limit: "1Ki"},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	underBody := bytes.Repeat([]byte("a"), 100)
	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "POST", path, harness.WithBody(underBody))
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("request buffer: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("request buffer: under-limit body (100 bytes < 1Ki) got status %d, want 200", resp.StatusCode)
	}

	overBody := bytes.Repeat([]byte("a"), 4096)
	resp, err = env.GW.HTTP(ctx, "POST", path, harness.WithBody(overBody))
	if err != nil {
		t.Fatalf("request buffer: over-limit request: %v", err)
	}
	if resp.StatusCode != 413 {
		t.Fatalf("request buffer: over-limit body (4096 bytes > 1Ki) got status %d, want 413", resp.StatusCode)
	}
}

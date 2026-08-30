//go:build e2e

package httproute

import (
	"context"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// corsSecurityPolicy builds the shared CORS config used by both CORS
// tests, matching tests/http_route_features/test_cors.py's PREFLIGHT_CONFIG
// and ACTUAL_CONFIG (identical CORS settings in both).
func corsSecurityPolicy() *services.SecurityPolicyInput {
	maxAge := 86400
	allowCreds := true
	return &services.SecurityPolicyInput{
		CORS: &models.CORSConfig{
			AllowOrigins:     []string{"https://example.com"},
			AllowMethods:     []string{"GET", "POST"},
			AllowHeaders:     []string{"Content-Type"},
			ExposeHeaders:    []string{"X-Custom"},
			MaxAge:           &maxAge,
			AllowCredentials: &allowCreds,
		},
	}
}

// TestCORSPreflight ports test_cors.py:test_cors_preflight. Already had a
// real assertion (the response header) in the Python source; transcribed
// as-is. The OPTIONS preflight is answered by Envoy's CORS filter without
// ever reaching the backend, so no urlRewrite is needed here.
func TestCORSPreflight(t *testing.T) {
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
		},
		SecurityPolicy: corsSecurityPolicy(),
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "OPTIONS", path,
			harness.WithHeader("Origin", "https://example.com"),
			harness.WithHeader("Access-Control-Request-Method", "GET"),
		)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("cors preflight: route never became live: %v", err)
	}

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("cors preflight: got Access-Control-Allow-Origin %q, want %q", got, "https://example.com")
	}
}

// TestCORSActualRequest ports test_cors.py:test_cors_actual_request.
// Already had a real assertion (the response header) in the Python source;
// transcribed as-is. Unlike preflight, this is a real GET forwarded to the
// backend, so it needs the urlRewrite to get a determinate 200 from nginx.
func TestCORSActualRequest(t *testing.T) {
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
		SecurityPolicy: corsSecurityPolicy(),
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("Origin", "https://example.com"))
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("cors actual request: route never became live: %v", err)
	}

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("cors actual request: got Access-Control-Allow-Origin %q, want %q", got, "https://example.com")
	}
}

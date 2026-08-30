//go:build e2e

package traffic

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestExternalBackendFQDN ports external_backends/test_fqdn.py, replacing
// the tautological "assert resp.status_code in (200, 404)" with a genuine
// 200 PLUS proof the response body actually came from the addressed
// backend (task-15-brief), the same technique
// e2e/suites/httproute/backend_weight_test.go already uses to distinguish
// nginx's traffic from any other backend: nginx's default page always
// contains "Welcome to nginx". The backend here is addressed by FQDN
// (nginx-service.default.svc.cluster.local) instead of through
// FastGateway's own Kubernetes-Service backend type, exercising the
// "external" backend code path with the SAME underlying nginx Pod --
// distinguishing "reached SOME backend" from "reached the specific
// FQDN-addressed one" is not possible from an external HTTP probe alone,
// since there is only one nginx deployment in this cluster; the body
// check confirms it is nginx (not an Envoy error page, not an empty/wrong
// response) rather than confirming FQDN-vs-Service routing specifically.
func TestExternalBackendFQDN(t *testing.T) {
	t.Parallel()

	const backendAddress = "nginx-service.default.svc.cluster.local"

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
				{
					Type:        models.BackendTypeExternal,
					AddressType: models.ExternalAddressTypeFQDN,
					Address:     backendAddress,
					Port:        nginxPort,
					Weight:      100,
				},
			},
			URLRewrite: rewriteTo("/"),
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("external backend fqdn: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("external backend fqdn: got status %d, want 200 (body: %s)", resp.StatusCode, truncate(resp.Body, 300))
	}
	if body := string(resp.Body); !strings.Contains(body, "Welcome to nginx") {
		t.Fatalf("external backend fqdn: response body does not look like nginx's default page (want it to contain %q, got: %s)", "Welcome to nginx", truncate(resp.Body, 300))
	}
}

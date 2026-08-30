//go:build e2e

package httproute

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestBackendWeight ports tests/http_route_features/test_backend_weight.py.
//
// DEVIATION from the Python source: the original config pointed both
// weighted backendRefs at the SAME nginx-service:80 twice (80/20 weight).
// That is a single Kubernetes Service/EDS cluster, so Envoy Gateway cannot
// weight-split traffic between two identical backendRefs in any way an
// external observer can detect -- there is no signal that would let a test
// tell "the 80 one" from "the 20 one" apart, which is exactly why the old
// assertion never checked anything beyond status in (200, 404). To make
// "both backends observed" a real, checkable assertion, this port weights
// across two DIFFERENT, distinguishable services instead: nginx-service:80
// (80%, identified by its default "Welcome to nginx!" page) and
// podinfo:9898 (20%, identified by its JSON body). The weighted-split
// mechanism under test (multiple backendRefs with different Weight values
// on one route) is unchanged.
func TestBackendWeight(t *testing.T) {
	t.Parallel()
	podinfoMu.Lock()
	defer podinfoMu.Unlock()

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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 80},
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoPort, Weight: 20},
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
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("backend weight: route never became live: %v", err)
	}

	var sawNginx, sawPodinfo bool
	const attempts = 50
	i := 0
	for ; i < attempts; i++ {
		resp, err := env.GW.HTTP(ctx, "GET", path)
		if err != nil {
			t.Fatalf("backend weight: request %d: %v", i, err)
		}
		body := string(resp.Body)
		if strings.Contains(body, "Welcome to nginx") {
			sawNginx = true
		}
		if strings.Contains(body, "hostname") {
			sawPodinfo = true
		}
		if sawNginx && sawPodinfo {
			break
		}
	}

	if !sawNginx || !sawPodinfo {
		t.Fatalf("backend weight: after %d requests got nginx=%v podinfo=%v, want both backends observed", i+1, sawNginx, sawPodinfo)
	}
}

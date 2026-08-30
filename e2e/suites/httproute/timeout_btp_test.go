//go:build e2e

package httproute

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestTimeoutBTP ports test_timeout_btp.py ("BTP Timeout
// (BackendTrafficPolicy-Level)"). See TestTimeoutRoute's doc comment: this
// backend has exactly one working timeout mechanism
// (BackendTrafficPolicy.Timeout), and both timeout ports exercise it. The
// Python config here set only tcp.connectTimeout, which would never fire
// against a live pod (the TCP handshake succeeds immediately; podinfo's
// /delay/N only delays the HTTP response, not the connection) -- so, like
// the route-level test, this uses HTTP.RequestTimeout, the only timeout
// setting that can actually produce a 504 against a delayed response.
func TestTimeoutBTP(t *testing.T) {
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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/delay/5"),
		},
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
			Timeout: &models.BTPTimeoutConfig{
				HTTP: &models.BTPHTTPTimeoutConfig{RequestTimeout: "2s"},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+30*time.Second)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
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
		return r.StatusCode == 504
	}, routeLiveTimeout)
	if err != nil {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("timeout btp: got status %d, want 504 (backend delays 5s, timeout is 2s): %v", got, err)
	}
}

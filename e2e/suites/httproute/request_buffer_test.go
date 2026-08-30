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
//
// The backend is podinfo's "/status/200" rather than nginx: nginx's
// static index page only serves GET/HEAD and answers any POST with a
// real 405, which would be indistinguishable from the buffer filter
// misbehaving. podinfo's "/status/{code}" endpoint (see also
// route_matching_method_test.go, retry_test.go, health_check_passive_
// test.go) accepts POST and always answers with the requested code, so
// the under-limit case's 200 can only be explained by the request
// actually reaching the backend.
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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/status/200"),
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

	// Polled rather than read once: the policy that produces this outcome
	// is a separate Kubernetes object Envoy Gateway programs AFTER the
	// route, so the route serves traffic un-policied for a short window
	// after deploy -- and WaitForRouteLive/waitForGRPCLive return on the
	// first answer they see, which in that window is the un-policied one.
	// harness.Fixture already waits for the policy to report Accepted;
	// this closes the remaining xDS-push tail. Bounded by routeLiveTimeout,
	// so a policy that never takes effect still fails the test.
	overBody := bytes.Repeat([]byte("a"), 4096)
	overProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "POST", path, harness.WithBody(overBody))
	}
	resp, err = harness.WaitForResponse(ctx, overProbe, func(r *harness.Response) bool {
		return r.StatusCode == 413
	}, routeLiveTimeout)
	if err != nil {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("request buffer: over-limit body (4096 bytes > 1Ki) got status %d, want 413: %v", got, err)
	}
}

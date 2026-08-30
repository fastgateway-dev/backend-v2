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

// TestMirror ports test_mirror.py.
//
// The old assertion never checked anything was actually mirrored -- just
// status in (200, 404). This port checks both halves the test's name
// promises: the primary response is really 200, and podinfo (the mirror
// target) really received a copy of the request.
//
// The urlRewrite needed to get a determinate 200 from nginx (which only
// serves "/") also rewrites the MIRRORED copy's path: Envoy's request
// mirroring shadows the request as routed, i.e. after prefix rewrite, so
// the mirrored request arrives at podinfo as "/" too -- indistinguishable
// by path from any other request forced to podinfo's root. Correctness is
// instead verified with a before/after log snapshot: podinfoMu is held for
// this whole test, so no other test can generate podinfo traffic
// concurrently, and any new log content appearing strictly after the
// tracked request is attributable to the mirror.
func TestMirror(t *testing.T) {
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
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			Mirrors: []models.MirrorBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: podinfoService, Port: podinfoPort},
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
		t.Fatalf("mirror: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("mirror: primary response got status %d, want 200", resp.StatusCode)
	}

	before, err := env.Kube.PodLogs(ctx, backendNamespace, podinfoLabel, 500)
	if err != nil {
		t.Fatalf("mirror: fetch podinfo pod logs (baseline): %v", err)
	}

	if _, err := env.GW.HTTP(ctx, "GET", path); err != nil {
		t.Fatalf("mirror: send request to be mirrored: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	var after string
	changed := false
	for time.Now().Before(deadline) {
		after, err = env.Kube.PodLogs(ctx, backendNamespace, podinfoLabel, 500)
		if err == nil && after != before {
			changed = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !changed {
		t.Fatalf("mirror: podinfo pod logs did not change within 30s after the mirrored request (no evidence the request was mirrored); baseline tail: %q", lastNChars(before, 300))
	}
}

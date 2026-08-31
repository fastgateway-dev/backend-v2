//go:build e2e

package httproute

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestResponseOverride ports test_response_override.py.
//
// KNOWN GAP vs the brief: the brief asks for "overridden status code AND
// overridden body content". This backend's ResponseOverride model
// (models.ResponseOverrideResponse, internal/models/backend_traffic_policy.go)
// only has ContentType and Body fields -- there is no statusCode override
// anywhere in the model, the CreateRouteInput/BackendTrafficPolicyInput
// chain, or the k8s manifest builder (internal/services/kubernetes_service.go).
// The original response status is always preserved; only content-type and
// body can be overridden. So this test asserts what is actually
// implementable: the route's unique path has no file on nginx, so nginx
// naturally answers 404, and the responseOverride rule below replaces that
// 404's BODY with the configured JSON while the status stays 404. See
// task-11-report.md for the full explanation.
//
// This also can't use harness.WaitForRouteLive: nginx's genuine 404 (no
// matching file) and Envoy's own "route not programmed yet" 404 are
// bit-identical by status code, so WaitForRouteLive would spin for its
// entire timeout waiting for a non-404 that will never come. Instead this
// polls directly for the overridden BODY, which only appears once the
// route and its BackendTrafficPolicy have actually been reconciled.
func TestResponseOverride(t *testing.T) {
	t.Parallel()

	name, path := uniquePath(t)
	statusVal := 404
	const wantBody = `{"error":"not found"}`

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
			// Deliberately NOT rewritten: this test wants nginx's natural
			// 404 for the unique, never-existing path.
		},
		BackendTrafficPolicy: &routeplan.BackendTrafficPolicyInput{
			ResponseOverride: []models.ResponseOverrideRule{
				{
					Match: models.ResponseOverrideMatch{
						StatusCodes: []models.StatusCodeMatch{{Type: "Value", Value: &statusVal}},
					},
					Response: models.ResponseOverrideResponse{
						ContentType: "application/json",
						Body: models.ResponseOverrideBody{
							Type:   "Inline",
							Inline: wantBody,
						},
					},
				},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout)
	defer cancel()

	deadline := time.Now().Add(routeLiveTimeout)
	var resp *harness.Response
	var err error
	for time.Now().Before(deadline) {
		resp, err = env.GW.HTTP(ctx, "GET", path)
		if err == nil && strings.TrimSpace(string(resp.Body)) == wantBody {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		t.Fatalf("response override: last request error: %v", err)
	}
	if got := strings.TrimSpace(string(resp.Body)); got != wantBody {
		t.Fatalf("response override: got body %q after waiting up to %s, want %q", got, routeLiveTimeout, wantBody)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("response override: got status %d, want 404 (this backend has no statusCode override field -- only content-type/body can be overridden, see task-11-report.md)", resp.StatusCode)
	}
}

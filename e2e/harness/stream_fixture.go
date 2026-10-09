//go:build e2e

package harness

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// StreamGatewayAddr resolves the LoadBalancer ingress IP of the Stream's
// Gateway AND waits for that Gateway's Envoy proxy pod to be Ready. Envoy
// Gateway creates the Service (and a per-stream proxy Deployment) in
// envoy-gateway-system, labelled with the owning Gateway's name and namespace.
// The LB IP is assigned before the proxy pod is Running, so this also gates on
// pod readiness — dialing a not-ready proxy resets the connection, which is why
// TCP traffic failed intermittently while the pod was still starting.
func (e *Env) StreamGatewayAddr(ctx context.Context, s models.Stream, timeout time.Duration) (string, error) {
	sel := fmt.Sprintf(
		"gateway.envoyproxy.io/owning-gateway-name=%s,gateway.envoyproxy.io/owning-gateway-namespace=%s",
		s.K8sGatewayName, s.Namespace,
	)
	ip, err := e.Kube.LoadBalancerIPByLabels(ctx, "envoy-gateway-system", sel, timeout)
	if err != nil {
		return "", err
	}
	if err := e.Kube.WaitPodsReadyByLabel(ctx, "envoy-gateway-system", sel, timeout); err != nil {
		return "", fmt.Errorf("stream gateway %s proxy not ready: %w", s.K8sGatewayName, err)
	}
	return ip, nil
}

// DeleteStreamRoute deletes an L4 route under a stream (approval-gated, same as
// the domain route delete).
func (a *API) DeleteStreamRoute(ctx context.Context, projectID, streamID, routeID string) error {
	_, err := a.Do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%s/streams/%s/routes/%s", projectID, streamID, routeID), nil, nil)
	return err
}

// StreamFixture creates L4 routes under a caller-provided Stream, driving the
// same create -> approve -> deploy -> wait-converged flow as Fixture.Route but
// through the stream-scoped endpoints and gating in the stream's namespace.
type StreamFixture struct {
	t   *testing.T
	env *Env
}

// NewStreamFixture returns a StreamFixture bound to t and env.
func NewStreamFixture(t *testing.T, env *Env) *StreamFixture {
	t.Helper()
	return &StreamFixture{t: t, env: env}
}

// Route creates cfg as an L4 route on stream, approves and deploys it, waits
// for convergence, and registers t.Cleanup to tear it down (route first, so
// the stream can later be deleted). Returns the route ID. Protocol is taken
// from cfg.Protocol. Mirrors Fixture.Route's role split and reject-first
// cleanup (see that method's doc for why).
func (f *StreamFixture) Route(stream models.Stream, cfg services.CreateRouteInput) string {
	t := f.t
	t.Helper()
	ctx := context.Background()
	projectID := f.env.ProjectID
	streamID := stream.ID.String()

	route, err := f.env.Editor.CreateStreamRoute(ctx, projectID, streamID, cfg)
	if err != nil {
		t.Fatalf("stream fixture: create route: %v", err)
	}
	routeID := route.ID.String()

	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := f.env.Admin.RejectApproval(cctx, projectID, routeID, "e2e stream fixture cleanup"); err != nil &&
			!strings.Contains(err.Error(), "no pending approval found") {
			t.Errorf("stream fixture cleanup: reject pending approval for route %s (%s): %v", route.Name, routeID, err)
			return
		}
		if err := f.env.Editor.DeleteStreamRoute(cctx, projectID, streamID, routeID); err != nil {
			t.Errorf("stream fixture cleanup: delete route %s (%s): %v", route.Name, routeID, err)
			return
		}
		if err := f.env.Approver.ApproveAllStages(cctx, projectID, routeID); err != nil &&
			!strings.Contains(err.Error(), "no pending approval found") {
			t.Errorf("stream fixture cleanup: approve delete for route %s (%s): %v", route.Name, routeID, err)
			return
		}
		if err := f.env.Editor.DeployStreamRoute(cctx, projectID, streamID, routeID); err != nil {
			t.Errorf("stream fixture cleanup: deploy delete for route %s (%s): %v", route.Name, routeID, err)
		}
	})

	if err := f.env.Approver.ApproveAllStages(ctx, projectID, routeID); err != nil {
		t.Fatalf("stream fixture: approve route %s (%s): %v", route.Name, routeID, err)
	}
	if err := f.env.Editor.DeployStreamRoute(ctx, projectID, streamID, routeID); err != nil {
		t.Fatalf("stream fixture: deploy route %s (%s): %v", route.Name, routeID, err)
	}

	f.waitConverged(stream, routeID, cfg)
	return routeID
}

// waitConverged gates on the TCPRoute/UDPRoute being Accepted (advisory) and,
// when cfg carries a BackendTrafficPolicy, on that policy being Accepted
// (fatal) — both in the stream's namespace, where the L4 objects live.
func (f *StreamFixture) waitConverged(stream models.Stream, routeID string, cfg services.CreateRouteInput) {
	t := f.t
	t.Helper()
	ns := stream.Namespace

	rctx, cancel := context.WithTimeout(context.Background(), fixtureRouteGateTimeout)
	err := WaitForRouteAccepted(rctx, f.env.Kube, RouteGVR(string(cfg.Protocol)), ns, routeID, fixtureRouteGateTimeout)
	cancel()
	if err != nil {
		t.Logf("stream fixture: route %s not fully accepted (continuing): %v", routeID, err)
	}

	if cfg.BackendTrafficPolicy != nil {
		pctx, cancelP := context.WithTimeout(context.Background(), fixtureConvergeTimeout)
		err := WaitForPoliciesAccepted(pctx, f.env.Kube, BackendTrafficPolicyGVR, ns, routeID, fixtureConvergeTimeout)
		cancelP()
		if err != nil {
			t.Fatalf("stream fixture: BackendTrafficPolicy for route %s not accepted: %v", routeID, err)
		}
	}
}

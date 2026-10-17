//go:build e2e

package stream

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestStreamLegibility_ManifestsEntrypointVersionsAndDiff deploys a TCP route
// on a stream, then checks the read-only views against the live cluster:
// /yamls renders the Gateway listeners the cluster has, /entrypoint lists the
// route's port, /versions has the deploy's snapshot, and the approval diff of
// a following update is a 200 carrying TCPRoute YAML.
func TestStreamLegibility_ManifestsEntrypointVersionsAndDiff(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	cfg := services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15400,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 100},
			},
		},
	}
	routeID := fx.Route(s, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	projectID, streamID := env.ProjectID, s.ID.String()

	// 1. The manifests' Gateway listeners are the live Gateway's listeners.
	yamls, err := env.Editor.GetStreamYAMLs(ctx, projectID, streamID)
	if err != nil {
		t.Fatalf("get stream yamls: %v", err)
	}
	rendered, err := harness.GatewayYAMLListeners(yamls.GatewayYAML)
	if err != nil {
		t.Fatalf("parse rendered gateway: %v", err)
	}
	live, err := env.Kube.GetUnstructured(ctx, kubernetes.GatewayGVR, s.Namespace, s.K8sGatewayName)
	if err != nil {
		t.Fatalf("get live gateway: %v", err)
	}
	liveListeners, err := harness.GatewayListeners(live.Object)
	if err != nil {
		t.Fatalf("parse live gateway: %v", err)
	}
	if !reflect.DeepEqual(rendered, liveListeners) {
		t.Fatalf("stream /yamls listeners %v != live Gateway listeners %v", rendered, liveListeners)
	}
	if len(yamls.Routes) != 1 || yamls.Routes[0].RouteID.String() != routeID || !strings.Contains(yamls.Routes[0].RouteYAML, "kind: TCPRoute") {
		t.Fatalf("stream /yamls routes = %+v, want the one TCPRoute %s", yamls.Routes, routeID)
	}

	// 2. The entry point lists the route's port, owned by the route.
	ep, err := env.Editor.GetStreamEntrypoint(ctx, projectID, streamID)
	if err != nil {
		t.Fatalf("get entrypoint: %v", err)
	}
	owned := false
	for _, p := range ep.Ports {
		if p.Transport == "TCP" && p.Port == 15400 && p.Owner.RouteID != nil && p.Owner.RouteID.String() == routeID {
			owned = true
		}
	}
	if !owned {
		t.Fatalf("entrypoint ports %+v do not list TCP/15400 owned by route %s", ep.Ports, routeID)
	}

	// 3. The deploy recorded a version, reachable under the stream.
	_, total, err := env.Editor.ListStreamRouteVersions(ctx, projectID, streamID, routeID)
	if err != nil {
		t.Fatalf("list stream route versions: %v", err)
	}
	if total < 1 {
		t.Fatalf("stream route versions total = %d, want >= 1", total)
	}

	// 4. The approval diff of an update is a 200 with TCPRoute YAML. The
	// fixture's cleanup rejects this pending approval before deleting.
	updated := cfg.Config
	updated.ListenerPort = 15401
	if _, err := env.Editor.UpdateStreamRoute(ctx, projectID, streamID, routeID, services.UpdateRouteInput{Config: updated}); err != nil {
		t.Fatalf("update stream route: %v", err)
	}
	approvalID, err := env.Admin.PendingRouteApprovalID(ctx, projectID, routeID)
	if err != nil {
		t.Fatalf("find update approval: %v", err)
	}
	diff, err := env.Admin.GetApprovalDiff(ctx, projectID, approvalID)
	if err != nil {
		t.Fatalf("approval diff for an L4 update: want 200, got %v", err)
	}
	if diff.RouteKind != "TCPRoute" ||
		!strings.Contains(diff.CurrentYAML, "sectionName: l4-tcp-15400") ||
		!strings.Contains(diff.ProposedYAML, "sectionName: l4-tcp-15401") {
		t.Fatalf("approval diff = routeKind %q current %q proposed %q, want TCPRoute 15400 -> 15401", diff.RouteKind, diff.CurrentYAML, diff.ProposedYAML)
	}
}

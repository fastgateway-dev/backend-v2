//go:build e2e

package stream

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// claimsRoute returns a TCP route to l4-echo-a on port.
func claimsRoute(t *testing.T, port int) services.CreateRouteInput {
	t.Helper()
	return services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: port,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 100},
			},
		},
	}
}

// wantStatus fails the test unless err is a *harness.StatusError with code.
func wantStatus(t *testing.T, err error, code int, what string) {
	t.Helper()
	se, ok := err.(*harness.StatusError)
	if !ok || se.StatusCode != code {
		t.Fatalf("%s: want %d StatusError, got %v", what, code, err)
	}
}

// TestStreamClaims_UnroutedListenersAccepted proves a stream's Gateway carries
// a listener for every claim — including claims no route uses yet — and that
// the Gateway is Accepted and Programmed with all of them. The one routed
// claim carries traffic, and the live Gateway's listeners and the /yamls
// Gateway listeners both equal exactly the three claims.
func TestStreamClaims_UnroutedListenersAccepted(t *testing.T) {
	s := newStream(t,
		models.StreamPort{Transport: "TCP", Port: 15900},
		models.StreamPort{Transport: "UDP", Port: 15901},
		models.StreamPort{Transport: "TCP", Port: 15902},
	)
	fx := harness.NewStreamFixture(t, env)
	fx.Route(s, claimsRoute(t, 15902))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	projectID, streamID := env.ProjectID, s.ID.String()

	want := []harness.GatewayListener{
		{Name: "l4-tcp-15900", Protocol: "TCP", Port: 15900},
		{Name: "l4-tcp-15902", Protocol: "TCP", Port: 15902},
		{Name: "l4-udp-15901", Protocol: "UDP", Port: 15901},
	}

	// The Gateway exists with exactly the claimed listeners, and Envoy Gateway
	// accepted and programmed it (unrouted listeners included).
	if err := harness.WaitForGatewayListeners(ctx, env.Kube, s.Namespace, s.K8sGatewayName, want, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := harness.WaitForGatewayConditions(ctx, env.Kube, s.Namespace, s.K8sGatewayName, 2*time.Minute, "Accepted", "Programmed"); err != nil {
		t.Fatal(err)
	}

	// The routed claim carries traffic.
	addr, err := env.StreamGatewayAddr(ctx, s, 2*time.Minute)
	if err != nil {
		t.Fatalf("resolve stream gateway LB: %v", err)
	}
	payload := []byte("hello-claims")
	got, err := harness.DialTCP(ctx, addr+":15902", payload, 10*time.Second)
	if err != nil {
		t.Fatalf("dial tcp %s:15902: %v", addr, err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("tcp echo: got %q want %q", got, payload)
	}

	// The manifests' Gateway listeners equal the claims too.
	yamls, err := env.Editor.GetStreamYAMLs(ctx, projectID, streamID)
	if err != nil {
		t.Fatalf("get stream yamls: %v", err)
	}
	rendered, err := harness.GatewayYAMLListeners(yamls.GatewayYAML)
	if err != nil {
		t.Fatalf("parse rendered gateway: %v", err)
	}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("stream /yamls listeners %v != claims %v", rendered, want)
	}
	live, err := env.Kube.GetUnstructured(ctx, kubernetes.GatewayGVR, s.Namespace, s.K8sGatewayName)
	if err != nil {
		t.Fatalf("get live gateway: %v", err)
	}
	liveListeners, err := harness.GatewayListeners(live.Object)
	if err != nil {
		t.Fatalf("parse live gateway: %v", err)
	}
	if !reflect.DeepEqual(liveListeners, want) {
		t.Fatalf("live Gateway listeners %v != claims %v", liveListeners, want)
	}
}

// TestStreamClaims_UnclaimedPortIs400 verifies an L4 route on a port its stream
// does not claim is rejected with 400, for either transport.
func TestStreamClaims_UnclaimedPortIs400(t *testing.T) {
	s := newStream(t, models.StreamPort{Transport: "TCP", Port: 15910})
	ctx := context.Background()

	_, err := env.Editor.CreateStreamRoute(ctx, env.ProjectID, s.ID.String(), claimsRoute(t, 15911))
	wantStatus(t, err, http.StatusBadRequest, "tcp route on unclaimed port 15911")

	// 15910 is claimed as TCP only: the UDP side of it is unclaimed.
	udp := claimsRoute(t, 15910)
	udp.Protocol = models.RouteProtocolUDP
	_, err = env.Editor.CreateStreamRoute(ctx, env.ProjectID, s.ID.String(), udp)
	wantStatus(t, err, http.StatusBadRequest, "udp route on tcp-only claim 15910")
}

// TestStreamClaims_RemovingUsedClaimIs409 verifies dropping a claim a route
// uses is refused with 409 (and leaves the claim set alone), while dropping an
// unused claim succeeds and removes its listener from the live Gateway.
func TestStreamClaims_RemovingUsedClaimIs409(t *testing.T) {
	s := newStream(t,
		models.StreamPort{Transport: "TCP", Port: 15900},
		models.StreamPort{Transport: "TCP", Port: 15902},
	)
	fx := harness.NewStreamFixture(t, env)
	fx.Route(s, claimsRoute(t, 15902))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	projectID, streamID := env.ProjectID, s.ID.String()

	both := []harness.GatewayListener{
		{Name: "l4-tcp-15900", Protocol: "TCP", Port: 15900},
		{Name: "l4-tcp-15902", Protocol: "TCP", Port: 15902},
	}
	if err := harness.WaitForGatewayListeners(ctx, env.Kube, s.Namespace, s.K8sGatewayName, both, time.Minute); err != nil {
		t.Fatal(err)
	}

	// Dropping 15902, which the route uses: 409.
	_, err := env.Admin.UpdateStreamPorts(ctx, projectID, streamID, []models.StreamPort{{Transport: "TCP", Port: 15900}})
	wantStatus(t, err, http.StatusConflict, "drop used claim tcp/15902")

	// Dropping the unused 15900: 200, and the live Gateway loses its listener.
	updated, err := env.Admin.UpdateStreamPorts(ctx, projectID, streamID, []models.StreamPort{{Transport: "TCP", Port: 15902}})
	if err != nil {
		t.Fatalf("drop unused claim tcp/15900: want 200, got %v", err)
	}
	if len(updated.Ports) != 1 || updated.Ports[0].Transport != "TCP" || updated.Ports[0].Port != 15902 {
		t.Fatalf("updated stream ports = %+v, want [TCP/15902]", updated.Ports)
	}
	onlyRouted := []harness.GatewayListener{{Name: "l4-tcp-15902", Protocol: "TCP", Port: 15902}}
	if err := harness.WaitForGatewayListeners(ctx, env.Kube, s.Namespace, s.K8sGatewayName, onlyRouted, time.Minute); err != nil {
		t.Fatalf("live Gateway should have lost l4-tcp-15900: %v", err)
	}
}

// TestStreamClaims_DeleteRemovesGateway verifies deleting a stream (here one
// with claims and no routes) deletes its Kubernetes Gateway.
func TestStreamClaims_DeleteRemovesGateway(t *testing.T) {
	s := newStream(t,
		models.StreamPort{Transport: "TCP", Port: 15920},
		models.StreamPort{Transport: "UDP", Port: 15921},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	want := []harness.GatewayListener{
		{Name: "l4-tcp-15920", Protocol: "TCP", Port: 15920},
		{Name: "l4-udp-15921", Protocol: "UDP", Port: 15921},
	}
	if err := harness.WaitForGatewayListeners(ctx, env.Kube, s.Namespace, s.K8sGatewayName, want, time.Minute); err != nil {
		t.Fatalf("gateway should exist before delete: %v", err)
	}

	if err := env.Admin.DeleteStream(ctx, env.ProjectID, s.ID.String()); err != nil {
		t.Fatalf("delete stream: %v", err)
	}
	if err := harness.WaitForGatewayGone(ctx, env.Kube, s.Namespace, s.K8sGatewayName, time.Minute); err != nil {
		t.Fatal(err)
	}
}

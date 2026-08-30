//go:build e2e

package httproute

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestHealthCheckActive ports test_health_check_active.py.
//
// The old assertion only ever checked status in (200, 404), which is true
// whether or not health checking does anything. This port backs the route
// with podinfo (rather than nginx, per the brief) specifically so it can
// scale the backend to 0 replicas mid-test and assert the gateway starts
// answering 503 (no healthy upstream) -- proof the health-checked backend
// path is real, not just "the route exists". Original replica count is
// restored by a deferred func, run while podinfoMu is still held (see
// main_test.go).
func TestHealthCheckActive(t *testing.T) {
	t.Parallel()
	podinfoMu.Lock()
	defer podinfoMu.Unlock()

	name, path := uniquePath(t)
	timeoutS := "5s"
	intervalS := "10s"
	unhealthy := uint32(3)
	healthy := uint32(2)
	method := "GET"

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
			URLRewrite: rewriteTo("/"),
		},
		BackendTrafficPolicy: &services.BackendTrafficPolicyInput{
			HealthCheck: &models.HealthCheckConfig{
				Active: &models.ActiveHealthCheckConfig{
					Type:               "HTTP",
					Timeout:            &timeoutS,
					Interval:           &intervalS,
					UnhealthyThreshold: &unhealthy,
					HealthyThreshold:   &healthy,
					HTTP: &models.HTTPActiveHealthCheckConfig{
						Path:             "/",
						Method:           &method,
						ExpectedStatuses: []int{200},
					},
				},
			},
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	resp, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout)
	if err != nil {
		t.Fatalf("health check active: route never became live: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("health check active: got status %d while healthy, want 200", resp.StatusCode)
	}

	dep, err := env.Kube.Clientset.AppsV1().Deployments(backendNamespace).Get(ctx, podinfoService, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("health check active: get podinfo deployment: %v", err)
	}
	original := int32(1)
	if dep.Spec.Replicas != nil {
		original = *dep.Spec.Replicas
	}
	// Use defer (not t.Cleanup) so the restore runs before podinfoMu is
	// released: Go runs a test's defers before its registered t.Cleanup
	// funcs, and the deferred podinfoMu.Unlock() above was registered
	// first, so LIFO ordering runs this restore first, then the unlock.
	// See the package doc comment in main_test.go.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := env.Kube.ScaleDeployment(cleanupCtx, backendNamespace, podinfoService, original); err != nil {
			t.Errorf("health check active: restore podinfo replicas to %d: %v", original, err)
			return
		}
		if err := env.Kube.WaitDeploymentAvailable(cleanupCtx, backendNamespace, podinfoService, 60*time.Second); err != nil {
			t.Errorf("health check active: podinfo did not become available after restore: %v", err)
		}
	}()

	if err := env.Kube.ScaleDeployment(ctx, backendNamespace, podinfoService, 0); err != nil {
		t.Fatalf("health check active: scale podinfo to 0: %v", err)
	}

	deadline := time.Now().Add(90 * time.Second)
	var last *harness.Response
	for time.Now().Before(deadline) {
		last, err = env.GW.HTTP(ctx, "GET", path)
		if err == nil && last.StatusCode == 503 {
			return
		}
		time.Sleep(2 * time.Second)
	}
	gotStatus := -1
	if last != nil {
		gotStatus = last.StatusCode
	}
	t.Fatalf("health check active: got status %d after scaling podinfo to 0, want 503 within 90s (last error: %v)", gotStatus, err)
}

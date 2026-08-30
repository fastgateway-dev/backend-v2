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

// TestLoadBalancing ports test_load_balancing.py.
//
// DEVIATIONS from the Python source: (1) the backend is podinfo, not
// nginx-service, per the brief -- podinfo's JSON body reports its own pod
// name (hostname), which is the only way to observe which replica actually
// served a request. (2) the Python config set loadBalancer.type =
// ConsistentHash (hashed on header x-user-id), which -- by design -- pins
// every request from the same client to the SAME backend; that is the
// opposite of what "at least 2 distinct hostnames" requires, so this port
// drops that policy and uses Envoy Gateway's default load balancing
// instead, letting requests actually spread across podinfo's replicas.
func TestLoadBalancing(t *testing.T) {
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
			URLRewrite: rewriteTo("/"),
		},
	}

	fx := harness.NewFixture(t, env)
	fx.Route(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), routeLiveTimeout+2*time.Minute)
	defer cancel()

	dep, err := env.Kube.Clientset.AppsV1().Deployments(backendNamespace).Get(ctx, podinfoService, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("load balancing: get podinfo deployment: %v", err)
	}
	original := int32(1)
	if dep.Spec.Replicas != nil {
		original = *dep.Spec.Replicas
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := env.Kube.ScaleDeployment(cleanupCtx, backendNamespace, podinfoService, original); err != nil {
			t.Errorf("load balancing: restore podinfo replicas to %d: %v", original, err)
			return
		}
		if err := env.Kube.WaitDeploymentAvailable(cleanupCtx, backendNamespace, podinfoService, 60*time.Second); err != nil {
			t.Errorf("load balancing: podinfo did not become available after restore: %v", err)
		}
	})

	if err := env.Kube.ScaleDeployment(ctx, backendNamespace, podinfoService, 3); err != nil {
		t.Fatalf("load balancing: scale podinfo to 3: %v", err)
	}
	if err := env.Kube.WaitDeploymentAvailable(ctx, backendNamespace, podinfoService, 90*time.Second); err != nil {
		t.Fatalf("load balancing: podinfo did not become available at 3 replicas: %v", err)
	}

	probe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path)
	}
	if _, err := harness.WaitForRouteLive(ctx, probe, routeLiveTimeout); err != nil {
		t.Fatalf("load balancing: route never became live: %v", err)
	}

	hostnames := map[string]bool{}
	const attempts = 40
	i := 0
	for ; i < attempts; i++ {
		resp, err := env.GW.HTTP(ctx, "GET", path)
		if err != nil {
			t.Fatalf("load balancing: request %d: %v", i, err)
		}
		var body struct {
			Hostname string `json:"hostname"`
		}
		if err := resp.JSON(&body); err != nil {
			continue
		}
		if body.Hostname != "" {
			hostnames[body.Hostname] = true
		}
		if len(hostnames) >= 2 {
			break
		}
	}

	if len(hostnames) < 2 {
		names := make([]string, 0, len(hostnames))
		for h := range hostnames {
			names = append(names, h)
		}
		t.Fatalf("load balancing: got %d distinct hostname(s) %v after %d requests, want at least 2", len(hostnames), names, i+1)
	}
}

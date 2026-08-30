//go:build e2e

package traffic

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// resolveNginxPodIP finds a Running nginx pod's IP through the
// Kubernetes API (env.Kube.Clientset, the same typed clientset
// e2e/harness/kube.go builds from the ambient kubeconfig), matching
// e2e/deps/nginx.yaml's pod label ("run=nginx"). Per task-15-brief, this
// replaces external_backends/test_ip.py's `kubectl --context=orbstack
// get pods ...` subprocess call -- which hardcoded a context this harness
// never assumes (see e2e/harness/kube.go's own doc comment on
// KUBE_CONTEXT) -- with the harness's own Kubernetes client.
func resolveNginxPodIP(t *testing.T, ctx context.Context) string {
	t.Helper()
	pods, err := env.Kube.Clientset.CoreV1().Pods(backendNamespace).List(ctx, metav1.ListOptions{LabelSelector: nginxLabel})
	if err != nil {
		t.Fatalf("external backend ip: list pods matching %q in %s: %v", nginxLabel, backendNamespace, err)
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
			return pod.Status.PodIP
		}
	}
	t.Fatalf("external backend ip: no Running pod with IP found matching %q in %s (found %d pod(s))", nginxLabel, backendNamespace, len(pods.Items))
	return ""
}

// TestExternalBackendIP ports external_backends/test_ip.py, replacing the
// tautological "assert resp.status_code in (200, 404)" with a genuine 200
// PLUS proof the response body actually came from the addressed backend
// (task-15-brief) -- same body-content technique as
// external_backend_fqdn_test.go. The nginx pod IP is resolved via
// resolveNginxPodIP (harness.Kube), never a kubectl subprocess.
func TestExternalBackendIP(t *testing.T) {
	t.Parallel()

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer setupCancel()
	podIP := resolveNginxPodIP(t, setupCtx)

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
				{
					Type:        models.BackendTypeExternal,
					AddressType: models.ExternalAddressTypeIP,
					Address:     podIP,
					Port:        nginxPort,
					Weight:      100,
				},
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
		t.Fatalf("external backend ip: route never became live (pod IP %s): %v", podIP, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("external backend ip: got status %d, want 200 (pod IP %s, body: %s)", resp.StatusCode, podIP, truncate(resp.Body, 300))
	}
	if body := string(resp.Body); !strings.Contains(body, "Welcome to nginx") {
		t.Fatalf("external backend ip: response body does not look like nginx's default page (want it to contain %q, got: %s)", "Welcome to nginx", truncate(resp.Body, 300))
	}
}

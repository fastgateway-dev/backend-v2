//go:build e2e

package stream

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestZZDebugTCPEnvoyConfig is TEMPORARY debug instrumentation: it provisions a
// TCP stream route and dumps the owning Envoy proxy's listener/cluster/endpoint
// config so we can see why TCP upstreams reset while UDP works. It always
// passes; remove once the TCP data-plane issue is understood.
func TestZZDebugTCPEnvoyConfig(t *testing.T) {
	s := newStream(t)
	fx := harness.NewStreamFixture(t, env)
	fx.Route(s, services.CreateRouteInput{
		Name:     harness.UniqueName(t),
		Protocol: models.RouteProtocolTCP,
		TeamID:   teamID(t),
		Config: models.RouteConfig{
			ListenerPort: 15900,
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: "default", Service: "l4-echo-a", Port: 9100, Weight: 100},
			},
		},
	})

	sel := "gateway.envoyproxy.io/owning-gateway-name=" + s.K8sGatewayName
	podOut, err := exec.Command("kubectl", "-n", "envoy-gateway-system", "get", "pod",
		"-l", sel, "-o", "jsonpath={.items[0].metadata.name}").CombinedOutput()
	pod := strings.TrimSpace(string(podOut))
	t.Logf("DEBUG gateway=%s pod=%q err=%v", s.K8sGatewayName, pod, err)
	if pod == "" {
		t.Logf("DEBUG no envoy pod found; pods:\n%s", runKubectl("-n", "envoy-gateway-system", "get", "pods"))
		return
	}

	pf := exec.Command("kubectl", "-n", "envoy-gateway-system", "port-forward", "pod/"+pod, "19000:19000")
	if err := pf.Start(); err != nil {
		t.Logf("DEBUG port-forward start err: %v", err)
		return
	}
	defer func() { _ = pf.Process.Kill() }()
	time.Sleep(5 * time.Second)

	for _, res := range []string{"dynamic_listeners", "dynamic_active_clusters", "dynamic_endpoint_configs"} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:19000/config_dump?resource="+res, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("DEBUG config_dump %s err: %v", res, err)
			cancel()
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		t.Logf("DEBUG config_dump %s (%d bytes):\n%s", res, len(b), string(b))
	}
}

func runKubectl(args ...string) string {
	out, _ := exec.Command("kubectl", args...).CombinedOutput()
	return string(out)
}

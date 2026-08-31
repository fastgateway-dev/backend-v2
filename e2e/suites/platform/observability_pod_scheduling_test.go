//go:build e2e

package platform

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// envoyProxyGVR identifies Envoy Gateway's EnvoyProxy CRD for the dynamic
// client, matching internal/services/kubernetes_service.go's own
// CreateEnvoyProxy/DeleteEnvoyProxy GVR ("gateway.envoyproxy.io/v1alpha1
// envoyproxies").
var envoyProxyGVR = schema.GroupVersionResource{
	Group:    "gateway.envoyproxy.io",
	Version:  "v1alpha1",
	Resource: "envoyproxies",
}

// createDomainTemplate creates a domain template via POST
// /projects/:projectId/domain-templates (DomainTemplateHandler.Create,
// which -- per domain_template_service.go's Create -- synchronously
// provisions the backing EnvoyProxy CRD before returning, so no polling
// is needed before reading it back) and registers a t.Cleanup that deletes
// it (DomainTemplateService.Delete removes the EnvoyProxy CRD too).
// ClusterIP + no_tls (as used by every test in this file) needs no
// external IP or TLS secret, unlike the LoadBalancer + TLS domains the
// rest of this repo's e2e fixtures use -- see e2e/suites/domain/main_test.go's
// doc comment for why a second LoadBalancer domain was judged too risky to
// provision here.
func createDomainTemplate(t *testing.T, input services.CreateDomainTemplateInput) models.DomainTemplate {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	input.ExposureType = string(models.ExposureTypeClusterIP)
	input.TLSMode = string(models.TLSModeNone)

	var dt models.DomainTemplate
	if _, err := env.Admin.Do(ctx, http.MethodPost, fmt.Sprintf("/projects/%s/domain-templates", env.ProjectID), input, &dt); err != nil {
		t.Fatalf("create domain template %q: %v", input.Name, err)
	}
	if dt.K8sEnvoyProxyName == "" {
		t.Fatalf("domain template %q: response had no k8sEnvoyProxyName", input.Name)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		path := fmt.Sprintf("/projects/%s/domain-templates/%s", env.ProjectID, dt.ID)
		if _, err := env.Admin.Do(cleanupCtx, http.MethodDelete, path, nil, nil); err != nil {
			t.Errorf("cleanup: delete domain template %s (%s): %v", dt.Name, dt.ID, err)
		}
	})

	return dt
}

// getEnvoyProxy fetches the EnvoyProxy CRD backing a domain template via
// harness.Kube.GetUnstructured -- never by shelling out to kubectl, unlike
// the Python original's _kubectl_get helper (see the package doc
// comment). The namespace is services.EnvoyGatewayNamespace, the backend's
// own hardcoded constant (internal/services/kubernetes_service.go) for
// where Envoy Gateway expects EnvoyProxy CRDs -- there is no
// harness.Config field for it since it isn't operator-configurable.
func getEnvoyProxy(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	obj, err := env.Kube.GetUnstructured(ctx, envoyProxyGVR, kubernetes.EnvoyGatewayNamespace, name)
	if err != nil {
		t.Fatalf("get EnvoyProxy %s/%s: %v", kubernetes.EnvoyGatewayNamespace, name, err)
	}
	return obj
}

// nestedOrFatal is unstructured.NestedFieldNoCopy plus a t.Fatalf on a
// missing/malformed path, for the many required-field assertions below.
func nestedString(t *testing.T, obj *unstructured.Unstructured, fields ...string) string {
	t.Helper()
	v, found, err := unstructured.NestedString(obj.Object, fields...)
	if err != nil || !found {
		t.Fatalf("EnvoyProxy %s: field %v: found=%v err=%v (object: %+v)", obj.GetName(), fields, found, err, obj.Object)
	}
	return v
}

// TestEnvoyProxyPodScheduling ports
// observability/test_envoyproxy_pod_scheduling.py:
// test_envoyproxy_pod_scheduling_all_blocks. Already a real assertion in
// the Python source (direct field equality checks on the EnvoyProxy CRD,
// no status-membership tautology); ported unchanged in spirit, reading the
// resource via harness.Kube.GetUnstructured instead of kubectl.
func TestEnvoyProxyPodScheduling(t *testing.T) {
	t.Parallel()

	dt := createDomainTemplate(t, services.CreateDomainTemplateInput{
		Name: harness.UniqueName(t),
		PodPlacement: &models.PodPlacementConfig{
			NodeSelector: map[string]string{"name": "nodepool-01"},
			Tolerations: []models.TolerationConfig{
				{Key: "dedicated", Operator: "Equal", Value: "gateway", Effect: "NoSchedule"},
			},
			TopologySpreadConstraints: []models.TopologySpreadConstraintConfig{
				{MaxSkew: 1, TopologyKey: "topology.kubernetes.io/zone", WhenUnsatisfiable: "ScheduleAnyway"},
			},
		},
		PDBConfig: &models.PDBConfig{Kind: "minAvailable", Amount: "50%"},
		DeploymentStrategy: &models.DeploymentStrategyConfig{
			Type:          "RollingUpdate",
			RollingUpdate: &models.RollingUpdateConfig{MaxSurge: "25%", MaxUnavailable: "1"},
		},
	})

	ep := getEnvoyProxy(t, dt.K8sEnvoyProxyName)

	pod, found, err := unstructured.NestedMap(ep.Object, "spec", "provider", "kubernetes", "envoyDeployment", "pod")
	if err != nil || !found {
		t.Fatalf("EnvoyProxy %s: spec.provider.kubernetes.envoyDeployment.pod: found=%v err=%v", dt.K8sEnvoyProxyName, found, err)
	}

	nodeSelector, _, _ := unstructured.NestedStringMap(pod, "nodeSelector")
	if nodeSelector["name"] != "nodepool-01" {
		t.Fatalf("EnvoyProxy %s: pod.nodeSelector=%v, want name=nodepool-01", dt.K8sEnvoyProxyName, nodeSelector)
	}

	tolerations, found, err := unstructured.NestedSlice(pod, "tolerations")
	if err != nil || !found || len(tolerations) != 1 {
		t.Fatalf("EnvoyProxy %s: pod.tolerations=%v (found=%v err=%v), want exactly 1 entry", dt.K8sEnvoyProxyName, tolerations, found, err)
	}
	tol, _ := tolerations[0].(map[string]interface{})
	if tol["key"] != "dedicated" || tol["effect"] != "NoSchedule" {
		t.Fatalf("EnvoyProxy %s: pod.tolerations[0]=%v, want key=dedicated effect=NoSchedule", dt.K8sEnvoyProxyName, tol)
	}

	constraints, found, err := unstructured.NestedSlice(pod, "topologySpreadConstraints")
	if err != nil || !found || len(constraints) != 1 {
		t.Fatalf("EnvoyProxy %s: pod.topologySpreadConstraints=%v (found=%v err=%v), want exactly 1 entry", dt.K8sEnvoyProxyName, constraints, found, err)
	}
	tsc, _ := constraints[0].(map[string]interface{})
	maxSkew, _ := tsc["maxSkew"].(int64)
	if maxSkew == 0 {
		if f, ok := tsc["maxSkew"].(float64); ok {
			maxSkew = int64(f)
		}
	}
	if maxSkew != 1 || tsc["topologyKey"] != "topology.kubernetes.io/zone" {
		t.Fatalf("EnvoyProxy %s: pod.topologySpreadConstraints[0]=%v, want maxSkew=1 topologyKey=topology.kubernetes.io/zone", dt.K8sEnvoyProxyName, tsc)
	}

	strategyType := nestedString(t, ep, "spec", "provider", "kubernetes", "envoyDeployment", "strategy", "type")
	if strategyType != "RollingUpdate" {
		t.Fatalf("EnvoyProxy %s: strategy.type=%q, want RollingUpdate", dt.K8sEnvoyProxyName, strategyType)
	}
	maxSurge := nestedString(t, ep, "spec", "provider", "kubernetes", "envoyDeployment", "strategy", "rollingUpdate", "maxSurge")
	if maxSurge != "25%" {
		t.Fatalf("EnvoyProxy %s: strategy.rollingUpdate.maxSurge=%q, want 25%%", dt.K8sEnvoyProxyName, maxSurge)
	}
	// maxUnavailable can come back as either a string or an int in the
	// unstructured object depending on how the CRD's IntOrString field was
	// serialized -- accept either representation, mirroring the Python
	// original's `in (1, "1")`.
	rawMaxUnavail, found, err := unstructured.NestedFieldNoCopy(ep.Object, "spec", "provider", "kubernetes", "envoyDeployment", "strategy", "rollingUpdate", "maxUnavailable")
	if err != nil || !found {
		t.Fatalf("EnvoyProxy %s: strategy.rollingUpdate.maxUnavailable: found=%v err=%v", dt.K8sEnvoyProxyName, found, err)
	}
	if fmt.Sprintf("%v", rawMaxUnavail) != "1" {
		t.Fatalf("EnvoyProxy %s: strategy.rollingUpdate.maxUnavailable=%v, want 1", dt.K8sEnvoyProxyName, rawMaxUnavail)
	}

	pdbMinAvailable := nestedString(t, ep, "spec", "provider", "kubernetes", "envoyPDB", "minAvailable")
	if pdbMinAvailable != "50%" {
		t.Fatalf("EnvoyProxy %s: envoyPDB.minAvailable=%q, want 50%%", dt.K8sEnvoyProxyName, pdbMinAvailable)
	}
}

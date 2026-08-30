//go:build e2e

package platform

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestEnvoyProxyTelemetry ports
// observability/test_envoyproxy_telemetry.py:test_envoyproxy_telemetry_all_three.
// Already a real assertion in the Python source; ported unchanged in
// spirit, reading the EnvoyProxy CRD via harness.Kube.GetUnstructured
// instead of kubectl (see the package doc comment and
// observability_pod_scheduling_test.go's createDomainTemplate/
// getEnvoyProxy helpers, reused here).
func TestEnvoyProxyTelemetry(t *testing.T) {
	t.Parallel()

	dt := createDomainTemplate(t, services.CreateDomainTemplateInput{
		Name: harness.UniqueName(t),
		TelemetryAccessLog: &models.TelemetryAccessLogConfig{
			Format: models.TelemetryAccessLogFormat{
				Type: "json",
				JSON: map[string]string{"method": "%REQ(:METHOD)%", "status": "%RESPONSE_CODE%"},
			},
			Sink: models.TelemetryAccessLogSink{
				Type: "file",
				File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"},
			},
		},
		TelemetryTracing: &models.TelemetryTracingConfig{
			SamplingRate: 10,
			Provider:     models.TelemetryServiceRef{Namespace: "observability", Service: "otel-collector", Port: 4317},
			CustomTags: []models.TelemetryTracingTag{
				{Type: "literal", Tag: "env", Value: "test"},
				{Type: "requestHeader", Tag: "tenant", Header: "x-tenant"},
			},
		},
		TelemetryMetrics: &models.TelemetryMetricsConfig{
			EnableVirtualHostStats: true,
			EnablePerEndpointStats: true,
			Prometheus:             &models.TelemetryPrometheusConfig{Disable: false},
		},
	})

	ep := getEnvoyProxy(t, dt.K8sEnvoyProxyName)

	// Access log. unstructured.NestedXxx helpers only traverse maps by
	// string key, not array indices, so the "settings" (and its "sinks")
	// array is fetched once via NestedSlice and then indexed/asserted
	// manually.
	settings, found, err := unstructured.NestedSlice(ep.Object, "spec", "telemetry", "accessLog", "settings")
	if err != nil || !found || len(settings) == 0 {
		t.Fatalf("EnvoyProxy %s: accessLog.settings=%v (found=%v err=%v), want at least 1 entry", dt.K8sEnvoyProxyName, settings, found, err)
	}
	setting, _ := settings[0].(map[string]interface{})
	format, _ := setting["format"].(map[string]interface{})
	if format["type"] != "JSON" {
		t.Fatalf("EnvoyProxy %s: accessLog.settings[0].format.type=%v, want JSON", dt.K8sEnvoyProxyName, format["type"])
	}
	sinks, _ := setting["sinks"].([]interface{})
	if len(sinks) == 0 {
		t.Fatalf("EnvoyProxy %s: accessLog.settings[0].sinks=%v, want at least 1 entry", dt.K8sEnvoyProxyName, setting["sinks"])
	}
	sink, _ := sinks[0].(map[string]interface{})
	if sink["type"] != "File" {
		t.Fatalf("EnvoyProxy %s: accessLog.settings[0].sinks[0].type=%v, want File", dt.K8sEnvoyProxyName, sink["type"])
	}
	sinkFile, _ := sink["file"].(map[string]interface{})
	if sinkFile["path"] != "/dev/stdout" {
		t.Fatalf("EnvoyProxy %s: accessLog.settings[0].sinks[0].file.path=%v, want /dev/stdout", dt.K8sEnvoyProxyName, sinkFile["path"])
	}

	// Tracing. samplingRate is stored as a float64
	// (models.TelemetryTracingConfig.SamplingRate), but a whole-number
	// value like 10 serializes to JSON without a decimal point, and the
	// Kubernetes unstructured JSON decoder reads a bare integer literal
	// back as int64 rather than float64 -- so unstructured.NestedFloat64
	// fails with a type-mismatch error even though the value round-tripped
	// correctly. Read the raw field and accept either numeric type.
	samplingRateVal, found, err := unstructured.NestedFieldNoCopy(ep.Object, "spec", "telemetry", "tracing", "samplingRate")
	if err != nil || !found {
		t.Fatalf("EnvoyProxy %s: tracing.samplingRate: found=%v err=%v", dt.K8sEnvoyProxyName, found, err)
	}
	var samplingRate float64
	switch v := samplingRateVal.(type) {
	case float64:
		samplingRate = v
	case int64:
		samplingRate = float64(v)
	default:
		t.Fatalf("EnvoyProxy %s: tracing.samplingRate=%v is type %T, want float64 or int64", dt.K8sEnvoyProxyName, samplingRateVal, samplingRateVal)
	}
	if samplingRate != 10 {
		t.Fatalf("EnvoyProxy %s: tracing.samplingRate=%v, want 10", dt.K8sEnvoyProxyName, samplingRate)
	}
	refs, found, err := unstructured.NestedSlice(ep.Object, "spec", "telemetry", "tracing", "provider", "backendRefs")
	if err != nil || !found || len(refs) == 0 {
		t.Fatalf("EnvoyProxy %s: tracing.provider.backendRefs=%v (found=%v err=%v), want at least 1 entry", dt.K8sEnvoyProxyName, refs, found, err)
	}
	ref, _ := refs[0].(map[string]interface{})
	if ref["name"] != "otel-collector" || ref["namespace"] != "observability" {
		t.Fatalf("EnvoyProxy %s: tracing.provider.backendRefs[0]=%v, want name=otel-collector namespace=observability", dt.K8sEnvoyProxyName, ref)
	}
	refPort, _ := ref["port"].(int64)
	if refPort == 0 {
		if f, ok := ref["port"].(float64); ok {
			refPort = int64(f)
		}
	}
	if refPort != 4317 {
		t.Fatalf("EnvoyProxy %s: tracing.provider.backendRefs[0].port=%v, want 4317", dt.K8sEnvoyProxyName, ref["port"])
	}

	tags, found, err := unstructured.NestedMap(ep.Object, "spec", "telemetry", "tracing", "customTags")
	if err != nil || !found {
		t.Fatalf("EnvoyProxy %s: tracing.customTags: found=%v err=%v", dt.K8sEnvoyProxyName, found, err)
	}
	envTag, _ := tags["env"].(map[string]interface{})
	if envTag["type"] != "Literal" {
		t.Fatalf("EnvoyProxy %s: tracing.customTags.env.type=%v, want Literal", dt.K8sEnvoyProxyName, envTag["type"])
	}
	envLiteral, _ := envTag["literal"].(map[string]interface{})
	if envLiteral["value"] != "test" {
		t.Fatalf("EnvoyProxy %s: tracing.customTags.env.literal.value=%v, want test", dt.K8sEnvoyProxyName, envLiteral["value"])
	}
	tenantTag, _ := tags["tenant"].(map[string]interface{})
	if tenantTag["type"] != "RequestHeader" {
		t.Fatalf("EnvoyProxy %s: tracing.customTags.tenant.type=%v, want RequestHeader", dt.K8sEnvoyProxyName, tenantTag["type"])
	}
	tenantHeader, _ := tenantTag["requestHeader"].(map[string]interface{})
	if tenantHeader["name"] != "x-tenant" {
		t.Fatalf("EnvoyProxy %s: tracing.customTags.tenant.requestHeader.name=%v, want x-tenant", dt.K8sEnvoyProxyName, tenantHeader["name"])
	}

	// Metrics
	vhostStats, found, err := unstructured.NestedBool(ep.Object, "spec", "telemetry", "metrics", "enableVirtualHostStats")
	if err != nil || !found || !vhostStats {
		t.Fatalf("EnvoyProxy %s: metrics.enableVirtualHostStats=%v (found=%v err=%v), want true", dt.K8sEnvoyProxyName, vhostStats, found, err)
	}
	perEndpointStats, found, err := unstructured.NestedBool(ep.Object, "spec", "telemetry", "metrics", "enablePerEndpointStats")
	if err != nil || !found || !perEndpointStats {
		t.Fatalf("EnvoyProxy %s: metrics.enablePerEndpointStats=%v (found=%v err=%v), want true", dt.K8sEnvoyProxyName, perEndpointStats, found, err)
	}
	promDisable, found, err := unstructured.NestedBool(ep.Object, "spec", "telemetry", "metrics", "prometheus", "disable")
	if err != nil || !found || promDisable {
		t.Fatalf("EnvoyProxy %s: metrics.prometheus.disable=%v (found=%v err=%v), want false", dt.K8sEnvoyProxyName, promDisable, found, err)
	}
}

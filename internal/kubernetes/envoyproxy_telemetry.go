package kubernetes

import (
	"strconv"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// BuildAccessLog converts the stored TelemetryAccessLogConfig into the EG CRD shape
// (spec.telemetry.accessLog). Returns a fully-formed map ready to embed.
func BuildAccessLog(cfg *models.TelemetryAccessLogConfig) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	format := buildAccessLogFormat(cfg.Format)
	sinks := buildAccessLogSinks(cfg.Sink)

	setting := map[string]interface{}{
		"format": format,
		"sinks":  sinks,
	}
	return map[string]interface{}{
		"settings": []interface{}{setting},
	}
}

func buildAccessLogFormat(f models.TelemetryAccessLogFormat) map[string]interface{} {
	switch f.Type {
	case "json":
		out := map[string]interface{}{"type": "JSON"}
		jsonMap := make(map[string]interface{}, len(f.JSON))
		for k, v := range f.JSON {
			jsonMap[k] = v
		}
		out["json"] = jsonMap
		return out
	case "disabled":
		return map[string]interface{}{"type": "Disabled"}
	default: // "text"
		return map[string]interface{}{
			"type": "Text",
			"text": f.Text,
		}
	}
}

func buildAccessLogSinks(s models.TelemetryAccessLogSink) []interface{} {
	switch s.Type {
	case "otel":
		if s.OTel == nil {
			return nil
		}
		return []interface{}{
			map[string]interface{}{
				"type": "OpenTelemetry",
				"openTelemetry": map[string]interface{}{
					"backendRefs": []interface{}{
						map[string]interface{}{
							"name":      s.OTel.Service,
							"namespace": s.OTel.Namespace,
							"port":      s.OTel.Port,
						},
					},
				},
			},
		}
	default: // "file"
		path := "/dev/stdout"
		if s.File != nil && s.File.Path != "" {
			path = s.File.Path
		}
		return []interface{}{
			map[string]interface{}{
				"type": "File",
				"file": map[string]interface{}{
					"path": path,
				},
			},
		}
	}
}

// BuildTracing converts the stored TelemetryTracingConfig into the EG CRD shape
// (spec.telemetry.tracing).
func BuildTracing(cfg *models.TelemetryTracingConfig) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	out := map[string]interface{}{
		"samplingRate": cfg.SamplingRate,
		"provider": map[string]interface{}{
			"backendRefs": []interface{}{
				map[string]interface{}{
					"name":      cfg.Provider.Service,
					"namespace": cfg.Provider.Namespace,
					"port":      cfg.Provider.Port,
				},
			},
		},
	}
	// EG CRD shape: customTags is a map keyed by tag name, NOT an array.
	// The DB still stores it as an array (with explicit Tag field) — translate here.
	if len(cfg.CustomTags) > 0 {
		tags := make(map[string]interface{}, len(cfg.CustomTags))
		for _, t := range cfg.CustomTags {
			tags[t.Tag] = buildTracingTag(t)
		}
		out["customTags"] = tags
	}
	return out
}

func buildTracingTag(t models.TelemetryTracingTag) map[string]interface{} {
	switch t.Type {
	case "requestHeader":
		rh := map[string]interface{}{"name": t.Header}
		if t.DefaultValue != "" {
			rh["defaultValue"] = t.DefaultValue
		}
		return map[string]interface{}{
			"type":          "RequestHeader",
			"requestHeader": rh,
		}
	default: // "literal"
		return map[string]interface{}{
			"type":    "Literal",
			"literal": map[string]interface{}{"value": t.Value},
		}
	}
}

// BuildMetrics converts the stored TelemetryMetricsConfig into the EG CRD shape
// (spec.telemetry.metrics).
func BuildMetrics(cfg *models.TelemetryMetricsConfig) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	out := map[string]interface{}{}
	if cfg.Prometheus != nil {
		out["prometheus"] = map[string]interface{}{"disable": cfg.Prometheus.Disable}
	}
	if cfg.EnableVirtualHostStats {
		out["enableVirtualHostStats"] = true
	}
	if cfg.EnablePerEndpointStats {
		out["enablePerEndpointStats"] = true
	}
	if len(cfg.Sinks) > 0 {
		sinks := make([]interface{}, 0, len(cfg.Sinks))
		for _, s := range cfg.Sinks {
			sinks = append(sinks, map[string]interface{}{
				"type": "OpenTelemetry",
				"openTelemetry": map[string]interface{}{
					"backendRefs": []interface{}{
						map[string]interface{}{
							"name":      s.Service,
							"namespace": s.Namespace,
							"port":      s.Port,
						},
					},
				},
			})
		}
		out["sinks"] = sinks
	}
	return out
}

// BuildPodPlacement converts the stored PodPlacementConfig into the keys to merge
// into envoyDeployment.pod (nodeSelector / tolerations / topologySpreadConstraints / priorityClassName).
// gatewayClassName is used to auto-fill the topology-spread labelSelector when the
// stored config doesn't override it.
func BuildPodPlacement(cfg *models.PodPlacementConfig, gatewayClassName string) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	out := map[string]interface{}{}
	if len(cfg.NodeSelector) > 0 {
		ns := make(map[string]interface{}, len(cfg.NodeSelector))
		for k, v := range cfg.NodeSelector {
			ns[k] = v
		}
		out["nodeSelector"] = ns
	}
	if len(cfg.Tolerations) > 0 {
		out["tolerations"] = buildTolerations(cfg.Tolerations)
	}
	if len(cfg.TopologySpreadConstraints) > 0 {
		out["topologySpreadConstraints"] = buildTopologySpreadConstraints(cfg.TopologySpreadConstraints, gatewayClassName)
	}
	// NOTE: priorityClassName is intentionally NOT emitted. The EG EnvoyProxy CRD
	// does not expose this field anywhere — K8s admission silently drops it on the
	// pod spec. We keep PriorityClassName in the model for forward-compat in case
	// EG adds support, but until then the value has no effect on the cluster.
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildTolerations(tols []models.TolerationConfig) []interface{} {
	out := make([]interface{}, 0, len(tols))
	for _, t := range tols {
		row := map[string]interface{}{}
		if t.Key != "" {
			row["key"] = t.Key
		}
		if t.Operator != "" {
			row["operator"] = t.Operator
		}
		if t.Value != "" {
			row["value"] = t.Value
		}
		if t.Effect != "" {
			row["effect"] = t.Effect
		}
		if t.TolerationSeconds != nil {
			row["tolerationSeconds"] = *t.TolerationSeconds
		}
		out = append(out, row)
	}
	return out
}

func buildTopologySpreadConstraints(cs []models.TopologySpreadConstraintConfig, gatewayClassName string) []interface{} {
	out := make([]interface{}, 0, len(cs))
	for _, c := range cs {
		row := map[string]interface{}{
			"maxSkew":           c.MaxSkew,
			"topologyKey":       c.TopologyKey,
			"whenUnsatisfiable": c.WhenUnsatisfiable,
			"labelSelector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"gateway.envoyproxy.io/owning-gatewayclass": gatewayClassName,
				},
			},
		}
		out = append(out, row)
	}
	return out
}

// BuildPDB converts the stored PDBConfig into the EG CRD shape (envoyPDB).
// Amount is parsed as int when it doesn't end with "%"; otherwise emitted as a string
// so K8s consumers see an IntOrString-compatible YAML value.
func BuildPDB(cfg *models.PDBConfig) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	val := parseIntOrPercent(cfg.Amount)
	switch cfg.Kind {
	case "maxUnavailable":
		return map[string]interface{}{"maxUnavailable": val}
	default: // "minAvailable"
		return map[string]interface{}{"minAvailable": val}
	}
}

// parseIntOrPercent returns an int when s parses cleanly as a non-negative integer
// (with no trailing characters); otherwise returns the string verbatim. K8s YAML
// consumers treat both correctly via the IntOrString type. Validation of the
// caller-side allowed range happens in the validators, not here.
func parseIntOrPercent(s string) interface{} {
	if len(s) > 0 && s[len(s)-1] == '%' {
		return s
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return n
	}
	return s
}

// BuildStrategy converts the stored DeploymentStrategyConfig into the EG CRD shape
// (envoyDeployment.strategy). Returns nil when cfg is nil. When Type is RollingUpdate
// and no overrides are set, omits the rollingUpdate sub-block so K8s defaults apply.
func BuildStrategy(cfg *models.DeploymentStrategyConfig) map[string]interface{} {
	if cfg == nil || cfg.Type == "" {
		return nil
	}
	out := map[string]interface{}{
		"type": cfg.Type,
	}
	if cfg.Type == "RollingUpdate" && cfg.RollingUpdate != nil {
		ru := map[string]interface{}{}
		if cfg.RollingUpdate.MaxSurge != "" {
			ru["maxSurge"] = parseIntOrPercent(cfg.RollingUpdate.MaxSurge)
		}
		if cfg.RollingUpdate.MaxUnavailable != "" {
			ru["maxUnavailable"] = parseIntOrPercent(cfg.RollingUpdate.MaxUnavailable)
		}
		if len(ru) > 0 {
			out["rollingUpdate"] = ru
		}
	}
	return out
}

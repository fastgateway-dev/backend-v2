package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

// TelemetryAccessLogConfig is the stored shape for spec.telemetry.accessLog.
type TelemetryAccessLogConfig struct {
	Format TelemetryAccessLogFormat `json:"format"`
	Sink   TelemetryAccessLogSink   `json:"sink"`
}

// TelemetryAccessLogFormat carries the format type plus the type-specific body.
type TelemetryAccessLogFormat struct {
	Type string            `json:"type"`           // "text" | "json" | "disabled"
	Text string            `json:"text,omitempty"` // present iff type=text
	JSON map[string]string `json:"json,omitempty"` // present iff type=json
}

// TelemetryAccessLogSink carries the sink type plus the type-specific body.
type TelemetryAccessLogSink struct {
	Type string                      `json:"type"` // "file" | "otel"
	File *TelemetryAccessLogFileSink `json:"file,omitempty"`
	OTel *TelemetryAccessLogOTelSink `json:"otel,omitempty"`
}

// TelemetryAccessLogFileSink is the File sink body. Path must be /dev/stdout or /dev/stderr.
type TelemetryAccessLogFileSink struct {
	Path string `json:"path"`
}

// TelemetryAccessLogOTelSink is the OpenTelemetry sink body referencing an in-cluster Service.
type TelemetryAccessLogOTelSink struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      int32  `json:"port"`
}

func (c TelemetryAccessLogConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *TelemetryAccessLogConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan TelemetryAccessLogConfig: unsupported type")
	}
	return json.Unmarshal(bytes, c)
}

// TelemetryTracingConfig is the stored shape for spec.telemetry.tracing.
type TelemetryTracingConfig struct {
	SamplingRate float64               `json:"samplingRate"`
	Provider     TelemetryServiceRef   `json:"provider"`
	CustomTags   []TelemetryTracingTag `json:"customTags,omitempty"`
}

// TelemetryServiceRef is a stored reference to an in-cluster K8s Service.
type TelemetryServiceRef struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      int32  `json:"port"`
}

// TelemetryTracingTag is one row of the customTags array.
// Type is "literal" or "requestHeader". Value populated iff type=literal.
// Header (and optional DefaultValue) populated iff type=requestHeader.
type TelemetryTracingTag struct {
	Type         string `json:"type"`
	Tag          string `json:"tag"`
	Value        string `json:"value,omitempty"`
	Header       string `json:"header,omitempty"`
	DefaultValue string `json:"defaultValue,omitempty"`
}

func (c TelemetryTracingConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *TelemetryTracingConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan TelemetryTracingConfig: unsupported type")
	}
	return json.Unmarshal(bytes, c)
}

// TelemetryMetricsConfig is the stored shape for spec.telemetry.metrics.
type TelemetryMetricsConfig struct {
	Prometheus             *TelemetryPrometheusConfig `json:"prometheus,omitempty"`
	EnableVirtualHostStats bool                       `json:"enableVirtualHostStats"`
	EnablePerEndpointStats bool                       `json:"enablePerEndpointStats"`
	Sinks                  []TelemetryMetricsSink     `json:"sinks,omitempty"`
}

// TelemetryPrometheusConfig holds the Prometheus enable/disable toggle.
type TelemetryPrometheusConfig struct {
	Disable bool `json:"disable"`
}

// TelemetryMetricsSink is currently only "openTelemetry"; one entry max in v1.
type TelemetryMetricsSink struct {
	Type      string `json:"type"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      int32  `json:"port"`
}

func (c TelemetryMetricsConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *TelemetryMetricsConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan TelemetryMetricsConfig: unsupported type")
	}
	return json.Unmarshal(bytes, c)
}

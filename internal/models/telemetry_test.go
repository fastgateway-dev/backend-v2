package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTelemetryAccessLogConfig_RoundTrip_FileText(t *testing.T) {
	in := &TelemetryAccessLogConfig{
		Format: TelemetryAccessLogFormat{
			Type: "text",
			Text: "[%START_TIME%] \"%REQ(:METHOD)% %REQ(X-ENVOY-ORIGINAL-PATH?:PATH)%\"",
		},
		Sink: TelemetryAccessLogSink{
			Type: "file",
			File: &TelemetryAccessLogFileSink{Path: "/dev/stdout"},
		},
	}
	value, err := in.Value()
	require.NoError(t, err)

	var out TelemetryAccessLogConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestTelemetryAccessLogConfig_RoundTrip_OTelJSON(t *testing.T) {
	in := &TelemetryAccessLogConfig{
		Format: TelemetryAccessLogFormat{
			Type: "json",
			JSON: map[string]string{"method": "%REQ(:METHOD)%"},
		},
		Sink: TelemetryAccessLogSink{
			Type: "otel",
			OTel: &TelemetryAccessLogOTelSink{
				Namespace: "observability",
				Service:   "otel-collector",
				Port:      4317,
			},
		},
	}
	value, err := in.Value()
	require.NoError(t, err)

	var out TelemetryAccessLogConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestTelemetryTracingConfig_RoundTrip(t *testing.T) {
	in := &TelemetryTracingConfig{
		SamplingRate: 1.5,
		Provider: TelemetryServiceRef{
			Namespace: "observability", Service: "otel-collector", Port: 4317,
		},
		CustomTags: []TelemetryTracingTag{
			{Type: "literal", Tag: "env", Value: "prod"},
			{Type: "requestHeader", Tag: "tenant_id", Header: "x-tenant-id", DefaultValue: "unknown"},
		},
	}
	value, err := in.Value()
	require.NoError(t, err)

	var out TelemetryTracingConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestTelemetryMetricsConfig_RoundTrip(t *testing.T) {
	in := &TelemetryMetricsConfig{
		Prometheus:             &TelemetryPrometheusConfig{Disable: true},
		EnableVirtualHostStats: true,
		EnablePerEndpointStats: false,
		Sinks: []TelemetryMetricsSink{
			{Type: "openTelemetry", Namespace: "observability", Service: "otel-collector", Port: 4317},
		},
	}
	value, err := in.Value()
	require.NoError(t, err)

	var out TelemetryMetricsConfig
	require.NoError(t, out.Scan(value))
	assert.Equal(t, *in, out)
}

func TestTelemetryConfig_Scan_Nil(t *testing.T) {
	var c TelemetryAccessLogConfig
	assert.NoError(t, c.Scan(nil))
}

func TestTelemetryConfig_Value_EmptyMarshalsCleanly(t *testing.T) {
	c := &TelemetryMetricsConfig{}
	v, err := c.Value()
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(v.([]byte), &raw))
}

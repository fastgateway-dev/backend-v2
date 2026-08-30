package services

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func TestValidateAccessLog_Valid_FileText(t *testing.T) {
	cfg := &models.TelemetryAccessLogConfig{
		Format: models.TelemetryAccessLogFormat{Type: "text", Text: "X"},
		Sink: models.TelemetryAccessLogSink{
			Type: "file",
			File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"},
		},
	}
	assert.NoError(t, ValidateAccessLog(cfg))
}

func TestValidateAccessLog_TextEmpty_Rejected(t *testing.T) {
	cfg := &models.TelemetryAccessLogConfig{
		Format: models.TelemetryAccessLogFormat{Type: "text", Text: ""},
		Sink: models.TelemetryAccessLogSink{
			Type: "file",
			File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"},
		},
	}
	assert.ErrorContains(t, ValidateAccessLog(cfg), "text format body must be non-empty")
}

func TestValidateAccessLog_FilePath_BadValue(t *testing.T) {
	cfg := &models.TelemetryAccessLogConfig{
		Format: models.TelemetryAccessLogFormat{Type: "text", Text: "X"},
		Sink: models.TelemetryAccessLogSink{
			Type: "file",
			File: &models.TelemetryAccessLogFileSink{Path: "/var/log/envoy.log"},
		},
	}
	assert.ErrorContains(t, ValidateAccessLog(cfg), "file path must be /dev/stdout or /dev/stderr")
}

func TestValidateAccessLog_OTel_BadPort(t *testing.T) {
	cfg := &models.TelemetryAccessLogConfig{
		Format: models.TelemetryAccessLogFormat{Type: "text", Text: "X"},
		Sink: models.TelemetryAccessLogSink{
			Type: "otel",
			OTel: &models.TelemetryAccessLogOTelSink{
				Namespace: "obs", Service: "col", Port: 0,
			},
		},
	}
	assert.ErrorContains(t, ValidateAccessLog(cfg), "port")
}

func TestValidateTracing_BadSamplingRate(t *testing.T) {
	cfg := &models.TelemetryTracingConfig{
		SamplingRate: 101,
		Provider:     models.TelemetryServiceRef{Namespace: "o", Service: "s", Port: 4317},
	}
	assert.ErrorContains(t, ValidateTracing(cfg), "samplingRate")
}

func TestValidateTracing_TagNameTooLong(t *testing.T) {
	cfg := &models.TelemetryTracingConfig{
		SamplingRate: 1,
		Provider:     models.TelemetryServiceRef{Namespace: "o", Service: "s", Port: 4317},
		CustomTags: []models.TelemetryTracingTag{
			{Type: "literal", Tag: string(make([]byte, 64)), Value: "v"},
		},
	}
	assert.ErrorContains(t, ValidateTracing(cfg), "tag name")
}

func TestValidateTracing_LiteralValueRequired(t *testing.T) {
	cfg := &models.TelemetryTracingConfig{
		SamplingRate: 1,
		Provider:     models.TelemetryServiceRef{Namespace: "o", Service: "s", Port: 4317},
		CustomTags: []models.TelemetryTracingTag{
			{Type: "literal", Tag: "env", Value: ""},
		},
	}
	assert.ErrorContains(t, ValidateTracing(cfg), "literal tag must have value")
}

func TestValidateTracing_RequestHeaderNameRequired(t *testing.T) {
	cfg := &models.TelemetryTracingConfig{
		SamplingRate: 1,
		Provider:     models.TelemetryServiceRef{Namespace: "o", Service: "s", Port: 4317},
		CustomTags: []models.TelemetryTracingTag{
			{Type: "requestHeader", Tag: "tenant", Header: ""},
		},
	}
	assert.ErrorContains(t, ValidateTracing(cfg), "requestHeader tag must have header name")
}

func TestValidateMetrics_BadSinkPort(t *testing.T) {
	cfg := &models.TelemetryMetricsConfig{
		Sinks: []models.TelemetryMetricsSink{
			{Type: "openTelemetry", Namespace: "o", Service: "s", Port: 70000},
		},
	}
	assert.ErrorContains(t, ValidateMetrics(cfg), "port")
}

func TestValidate_NilInputsAllowed(t *testing.T) {
	assert.NoError(t, ValidateAccessLog(nil))
	assert.NoError(t, ValidateTracing(nil))
	assert.NoError(t, ValidateMetrics(nil))
}

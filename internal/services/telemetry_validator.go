package services

import (
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// ValidateAccessLog enforces invariants on a stored TelemetryAccessLogConfig.
// nil is allowed (means "not configured").
func ValidateAccessLog(cfg *models.TelemetryAccessLogConfig) error {
	if cfg == nil {
		return nil
	}
	switch cfg.Format.Type {
	case "text":
		if cfg.Format.Text == "" {
			return errors.New("access log: text format body must be non-empty")
		}
	case "json":
		if len(cfg.Format.JSON) == 0 {
			return errors.New("access log: json format body must contain at least one key")
		}
		for k, v := range cfg.Format.JSON {
			if k == "" {
				return errors.New("access log: json format body has an empty key")
			}
			if v == "" {
				return fmt.Errorf("access log: json format value for key %q must be non-empty", k)
			}
		}
	case "disabled":
		// no body needed
	default:
		return fmt.Errorf("access log: unknown format type %q", cfg.Format.Type)
	}

	switch cfg.Sink.Type {
	case "file":
		if cfg.Sink.File == nil {
			return errors.New("access log: file sink missing body")
		}
		if cfg.Sink.File.Path != "/dev/stdout" && cfg.Sink.File.Path != "/dev/stderr" {
			return errors.New("access log: file path must be /dev/stdout or /dev/stderr")
		}
	case "otel":
		if cfg.Sink.OTel == nil {
			return errors.New("access log: otel sink missing body")
		}
		if err := validateServiceRef(cfg.Sink.OTel.Namespace, cfg.Sink.OTel.Service, cfg.Sink.OTel.Port); err != nil {
			return fmt.Errorf("access log: %w", err)
		}
	default:
		return fmt.Errorf("access log: unknown sink type %q", cfg.Sink.Type)
	}
	return nil
}

// ValidateTracing enforces invariants on a stored TelemetryTracingConfig.
func ValidateTracing(cfg *models.TelemetryTracingConfig) error {
	if cfg == nil {
		return nil
	}
	if cfg.SamplingRate < 0 || cfg.SamplingRate > 100 {
		return fmt.Errorf("tracing: samplingRate must be in [0, 100], got %v", cfg.SamplingRate)
	}
	if err := validateServiceRef(cfg.Provider.Namespace, cfg.Provider.Service, cfg.Provider.Port); err != nil {
		return fmt.Errorf("tracing provider: %w", err)
	}
	for i, tag := range cfg.CustomTags {
		if tag.Tag == "" {
			return fmt.Errorf("tracing: customTags[%d] tag name must be non-empty", i)
		}
		if len(tag.Tag) > 63 {
			return fmt.Errorf("tracing: customTags[%d] tag name exceeds 63 chars", i)
		}
		switch tag.Type {
		case "literal":
			if tag.Value == "" {
				return fmt.Errorf("tracing: customTags[%d] literal tag must have value", i)
			}
		case "requestHeader":
			if tag.Header == "" {
				return fmt.Errorf("tracing: customTags[%d] requestHeader tag must have header name", i)
			}
		default:
			return fmt.Errorf("tracing: customTags[%d] unknown type %q", i, tag.Type)
		}
	}
	return nil
}

// ValidateMetrics enforces invariants on a stored TelemetryMetricsConfig.
func ValidateMetrics(cfg *models.TelemetryMetricsConfig) error {
	if cfg == nil {
		return nil
	}
	for i, s := range cfg.Sinks {
		if s.Type != "openTelemetry" {
			return fmt.Errorf("metrics sinks[%d]: unknown type %q", i, s.Type)
		}
		if err := validateServiceRef(s.Namespace, s.Service, s.Port); err != nil {
			return fmt.Errorf("metrics sinks[%d]: %w", i, err)
		}
	}
	return nil
}

func validateServiceRef(namespace, service string, port int32) error {
	if namespace == "" {
		return errors.New("namespace must be non-empty")
	}
	if service == "" {
		return errors.New("service must be non-empty")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be in [1, 65535], got %d", port)
	}
	return nil
}

package services

import (
	"errors"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// validateScalingConfig validates a scaling configuration
func validateScalingConfig(sc *models.ScalingConfig) error {
	if sc == nil {
		return nil
	}
	if sc.Type != "fixed" && sc.Type != "hpa" {
		return errors.New("scaling type must be 'fixed' or 'hpa'")
	}
	if sc.Type == "fixed" {
		if sc.Replicas == nil || *sc.Replicas < 1 {
			return errors.New("fixed scaling requires replicas >= 1")
		}
	}
	if sc.Type == "hpa" {
		if sc.MinReplicas == nil || *sc.MinReplicas < 1 {
			return errors.New("HPA scaling requires minReplicas >= 1")
		}
		if sc.MaxReplicas == nil || *sc.MaxReplicas < 1 {
			return errors.New("HPA scaling requires maxReplicas >= 1")
		}
		if *sc.MaxReplicas < *sc.MinReplicas {
			return errors.New("HPA maxReplicas must be >= minReplicas")
		}
	}
	return nil
}

// NormalizeEmptyTelemetryMetrics returns nil if cfg has all default zero values,
// otherwise returns cfg unchanged. Used to keep DB rows clean per spec
// "empty-state semantics".
func NormalizeEmptyTelemetryMetrics(cfg *models.TelemetryMetricsConfig) *models.TelemetryMetricsConfig {
	if cfg == nil {
		return nil
	}
	if cfg.Prometheus == nil &&
		!cfg.EnableVirtualHostStats &&
		!cfg.EnablePerEndpointStats &&
		len(cfg.Sinks) == 0 {
		return nil
	}
	return cfg
}

// ValidateDomainTemplateTelemetry runs all three telemetry validators against
// the domain template's stored config.
func ValidateDomainTemplateTelemetry(dt *models.DomainTemplate) error {
	if err := ValidateAccessLog(dt.TelemetryAccessLog); err != nil {
		return err
	}
	if err := ValidateTracing(dt.TelemetryTracing); err != nil {
		return err
	}
	if err := ValidateMetrics(dt.TelemetryMetrics); err != nil {
		return err
	}
	return nil
}

// NormalizeEmptyPodPlacement returns nil if cfg has all default zero values,
// otherwise returns cfg unchanged. Mirrors the spec "empty-state semantics".
func NormalizeEmptyPodPlacement(cfg *models.PodPlacementConfig) *models.PodPlacementConfig {
	if cfg == nil {
		return nil
	}
	if len(cfg.NodeSelector) == 0 &&
		len(cfg.Tolerations) == 0 &&
		len(cfg.TopologySpreadConstraints) == 0 &&
		cfg.PriorityClassName == "" {
		return nil
	}
	return cfg
}

// ValidateDomainTemplatePodScheduling runs the three pod-scheduling validators against
// the domain template's stored config.
func ValidateDomainTemplatePodScheduling(dt *models.DomainTemplate) error {
	if err := ValidatePodPlacement(dt.PodPlacement); err != nil {
		return err
	}
	if err := ValidatePDB(dt.PDBConfig); err != nil {
		return err
	}
	if err := ValidateStrategy(dt.DeploymentStrategy); err != nil {
		return err
	}
	return nil
}

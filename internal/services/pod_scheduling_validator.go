package services

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

var labelKeyRegex = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?/)?[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)
var labelValueRegex = regexp.MustCompile(`^$|^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)
var pdbPercentRegex = regexp.MustCompile(`^([1-9][0-9]?|100)%$`)
var rollingPercentRegex = regexp.MustCompile(`^([0-9]|[1-9][0-9]?|100)%$`)
var nonNegativeIntRegex = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var positiveIntRegex = regexp.MustCompile(`^[1-9][0-9]*$`)

func ValidatePodPlacement(cfg *models.PodPlacementConfig) error {
	if cfg == nil {
		return nil
	}
	for k, v := range cfg.NodeSelector {
		if !labelKeyRegex.MatchString(k) {
			return fmt.Errorf("nodeSelector key %q is not a valid K8s label key", k)
		}
		if !labelValueRegex.MatchString(v) {
			return fmt.Errorf("nodeSelector value %q is not a valid K8s label value", v)
		}
	}
	for i, t := range cfg.Tolerations {
		switch t.Operator {
		case "Equal":
			if t.Value == "" {
				return fmt.Errorf("tolerations[%d]: value required when operator=Equal", i)
			}
			if t.Key == "" {
				return fmt.Errorf("tolerations[%d]: key required when operator=Equal", i)
			}
		case "Exists":
			if t.Value != "" {
				return fmt.Errorf("tolerations[%d]: value not allowed with operator=Exists", i)
			}
		default:
			return fmt.Errorf("tolerations[%d]: unknown operator %q (want Equal or Exists)", i, t.Operator)
		}
		switch t.Effect {
		case "NoSchedule", "PreferNoSchedule", "NoExecute", "":
			// ok
		default:
			return fmt.Errorf("tolerations[%d]: unknown effect %q", i, t.Effect)
		}
		if t.TolerationSeconds != nil {
			if t.Effect != "NoExecute" {
				return fmt.Errorf("tolerations[%d]: tolerationSeconds only meaningful with effect=NoExecute", i)
			}
			if *t.TolerationSeconds < 0 {
				return fmt.Errorf("tolerations[%d]: tolerationSeconds must be >= 0", i)
			}
		}
	}
	for i, c := range cfg.TopologySpreadConstraints {
		if c.MaxSkew < 1 {
			return fmt.Errorf("topologySpreadConstraints[%d]: maxSkew must be >= 1", i)
		}
		if c.TopologyKey == "" {
			return fmt.Errorf("topologySpreadConstraints[%d]: topologyKey must be non-empty", i)
		}
		switch c.WhenUnsatisfiable {
		case "DoNotSchedule", "ScheduleAnyway":
			// ok
		default:
			return fmt.Errorf("topologySpreadConstraints[%d]: whenUnsatisfiable must be DoNotSchedule or ScheduleAnyway", i)
		}
	}
	if len(cfg.PriorityClassName) > 253 {
		return errors.New("priorityClassName exceeds 253 chars")
	}
	return nil
}

func ValidatePDB(cfg *models.PDBConfig) error {
	if cfg == nil {
		return nil
	}
	if cfg.Kind != "minAvailable" && cfg.Kind != "maxUnavailable" {
		return fmt.Errorf("kind must be minAvailable or maxUnavailable, got %q", cfg.Kind)
	}
	if !positiveIntRegex.MatchString(cfg.Amount) && !pdbPercentRegex.MatchString(cfg.Amount) {
		return fmt.Errorf("value must be a positive integer or a percentage 1%%-100%%, got %q", cfg.Amount)
	}
	return nil
}

func ValidateStrategy(cfg *models.DeploymentStrategyConfig) error {
	if cfg == nil {
		return nil
	}
	if cfg.Type != "RollingUpdate" && cfg.Type != "Recreate" {
		return fmt.Errorf("type must be RollingUpdate or Recreate, got %q", cfg.Type)
	}
	if cfg.Type == "Recreate" && cfg.RollingUpdate != nil {
		return errors.New("rollingUpdate must not be set when type=Recreate")
	}
	if cfg.Type == "RollingUpdate" && cfg.RollingUpdate != nil {
		if err := validateRollingValue("maxSurge", cfg.RollingUpdate.MaxSurge); err != nil {
			return err
		}
		if err := validateRollingValue("maxUnavailable", cfg.RollingUpdate.MaxUnavailable); err != nil {
			return err
		}
		if isZeroIntOrPercent(cfg.RollingUpdate.MaxSurge) && isZeroIntOrPercent(cfg.RollingUpdate.MaxUnavailable) {
			return errors.New("both maxSurge and maxUnavailable cannot be 0")
		}
	}
	return nil
}

func validateRollingValue(field, value string) error {
	if value == "" {
		return nil
	}
	if !nonNegativeIntRegex.MatchString(value) && !rollingPercentRegex.MatchString(value) {
		return fmt.Errorf("%s must be a non-negative integer or a percentage 0%%-100%%, got %q", field, value)
	}
	return nil
}

func isZeroIntOrPercent(s string) bool {
	return s == "0" || s == "0%"
}

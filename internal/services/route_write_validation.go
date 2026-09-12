package services

import (
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

// validateDirectResponseInput enforces the directResponse route-type rules.
func validateDirectResponseInput(config *models.RouteConfig, btp *routeplan.BackendTrafficPolicyInput) error {
	// Validate direct response configuration if provided
	if config.RouteType == models.RouteTypeDirectResponse {
		if config.DirectResponse == nil {
			return errors.New("directResponse configuration is required for directResponse route type")
		}
		if err := config.DirectResponse.Validate(); err != nil {
			return fmt.Errorf("invalid direct response configuration: %w", err)
		}
		// Direct response routes cannot have backends
		if len(config.Backends) > 0 {
			return errors.New("directResponse routes cannot have backends")
		}
		// Direct response routes cannot have URL rewrite
		if config.URLRewrite != nil {
			return errors.New("directResponse routes cannot have URL rewrite")
		}
		// Direct response routes cannot have request header modifier
		if config.RequestHeaderModifier != nil {
			return errors.New("directResponse routes cannot have request header modifier")
		}
		// Direct response routes cannot have backend traffic policy
		if btp != nil && btp.HasContent() {
			return errors.New("directResponse routes cannot have backend traffic policy")
		}
	}

	return nil
}

// validateWriteSecurityMode validates the security policy against the mode.
// rejectUnknown is true on create and false on update; see the Phase 2K
// spec, Finding B.
func validateWriteSecurityMode(mode models.SecurityMode, policy *routeplan.SecurityPolicyInput, rejectUnknown bool) error {
	if mode == "" {
		mode = models.SecurityModeGeneral
	}
	switch mode {
	case models.SecurityModeGeneral:
		return validateSecurityModeGeneral(policy)
	case models.SecurityModeClient:
		return validateSecurityModeClient(policy)
	default:
		if rejectUnknown {
			return fmt.Errorf("invalid security mode: %s (must be 'general' or 'client')", mode)
		}
		return nil
	}
}

// validateRouteTrafficPolicies runs the backend and traffic-policy checks
// shared by Create and Update.
func (w *routeWrite) validateRouteTrafficPolicies(config *models.RouteConfig, btp *routeplan.BackendTrafficPolicyInput, projectID uuid.UUID) error {
	// Validate backend required fields (namespace, service, port)
	if err := w.query.validateBackendRequiredFields(config); err != nil {
		return err
	}

	// Validate backend namespaces are managed by the project
	if err := w.query.validateBackendNamespaces(projectID, config); err != nil {
		return err
	}

	// Validate mirror targets (must be different from primary backends)
	if err := w.query.validateMirrorTargets(config); err != nil {
		return err
	}

	// Validate failover configuration (must have at least one primary when fallback exists)
	if err := w.query.validateFailoverConfig(config); err != nil {
		return err
	}

	// Validate retry configuration if provided
	if btp != nil && btp.Retry != nil {
		if err := btp.Retry.Validate(); err != nil {
			return fmt.Errorf("invalid retry configuration: %w", err)
		}
	}

	// Validate load balancer configuration if provided
	if btp != nil && btp.LoadBalancer != nil {
		if err := btp.LoadBalancer.Validate(); err != nil {
			return fmt.Errorf("invalid load balancer configuration: %w", err)
		}
	}

	// Validate circuit breaker configuration if provided
	if btp != nil && btp.CircuitBreaker != nil {
		if err := btp.CircuitBreaker.Validate(); err != nil {
			return fmt.Errorf("invalid circuit breaker configuration: %w", err)
		}
	}

	// Validate health check configuration if provided
	if btp != nil && btp.HealthCheck != nil {
		if err := btp.HealthCheck.Validate(); err != nil {
			return fmt.Errorf("invalid health check configuration: %w", err)
		}
	}

	// Validate fault injection configuration if provided
	if btp != nil && btp.FaultInjection != nil {
		if err := btp.FaultInjection.Validate(); err != nil {
			return fmt.Errorf("invalid fault injection configuration: %w", err)
		}
	}

	// Validate rate limit configuration if provided
	if btp != nil && btp.RateLimit != nil {
		if err := btp.RateLimit.Validate(); err != nil {
			return fmt.Errorf("invalid rate limit configuration: %w", err)
		}
	}

	// Validate timeout configuration if provided
	if btp != nil && btp.Timeout != nil {
		if err := btp.Timeout.Validate(); err != nil {
			return fmt.Errorf("invalid timeout configuration: %w", err)
		}
	}

	return nil
}

// validateRouteShapeAndConflicts runs the protocol, essential-config and
// matcher-conflict checks shared by Create and Update.
func (w *routeWrite) validateRouteShapeAndConflicts(config *models.RouteConfig, btp *routeplan.BackendTrafficPolicyInput, protocol models.RouteProtocol, domainID uuid.UUID, excludeRouteID *uuid.UUID) error {
	// Validate protocol-specific config
	if protocol == models.RouteProtocolGRPC {
		if err := validateGRPCRouteConfig(config); err != nil {
			return err
		}
		if err := validateGRPCBackendTrafficPolicy(btp); err != nil {
			return err
		}
	} else {
		if err := validateHTTPRouteConfig(config); err != nil {
			return err
		}
	}

	// Validate essential route configuration
	if err := validateRouteConfig(config, protocol); err != nil {
		return err
	}

	// Check for matcher conflicts with existing routes in the domain
	if err := w.query.validateMatcherConflict(domainID, config, excludeRouteID); err != nil {
		return err
	}

	return nil
}

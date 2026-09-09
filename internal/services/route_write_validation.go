package services

import (
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
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

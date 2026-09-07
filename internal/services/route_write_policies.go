package services

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

// persistCreatePolicies creates the SecurityPolicy, BackendTrafficPolicy,
// EnvoyExtensionPolicy, and WafPolicy records for a newly created route.
func (s *RouteService) persistCreatePolicies(route *models.Route, projectID uuid.UUID, securityMode models.SecurityMode, input *CreateRouteInput) error {
	// Create SecurityPolicy if provided
	if input.SecurityPolicy != nil {
		spConfig := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if securityMode == models.SecurityModeGeneral {
			spConfig.Authorization = routeplan.BuildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			spConfig.APIKeyAuth = routeplan.BuildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			spConfig.JWT = routeplan.BuildJWTConfigFromInput(input.SecurityPolicy.JWT)
			spConfig.OIDC = routeplan.BuildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		spConfig.ExtAuth = input.SecurityPolicy.ExtAuth
		securityPolicy := &models.SecurityPolicy{
			RouteID:   route.ID,
			ProjectID: projectID,
			Config:    spConfig,
		}
		if err := s.securityPolicyRepo.Create(securityPolicy); err != nil {
			return fmt.Errorf("failed to create security policy: %w", err)
		}
	}

	// Create BackendTrafficPolicy if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
		backendTrafficPolicy := &models.BackendTrafficPolicy{
			RouteID:   &route.ID,
			ProjectID: projectID,
			Config: models.BackendTrafficPolicyConfig{
				Compression:      input.BackendTrafficPolicy.Compression,
				Retry:            input.BackendTrafficPolicy.Retry,
				LoadBalancer:     input.BackendTrafficPolicy.LoadBalancer,
				CircuitBreaker:   input.BackendTrafficPolicy.CircuitBreaker,
				HealthCheck:      input.BackendTrafficPolicy.HealthCheck,
				FaultInjection:   input.BackendTrafficPolicy.FaultInjection,
				RateLimit:        input.BackendTrafficPolicy.RateLimit,
				RequestBuffer:    input.BackendTrafficPolicy.RequestBuffer,
				ResponseOverride: input.BackendTrafficPolicy.ResponseOverride,
				Timeout:          input.BackendTrafficPolicy.Timeout,
			},
		}
		if err := s.backendTrafficPolicyRepo.Create(backendTrafficPolicy); err != nil {
			return fmt.Errorf("failed to create backend traffic policy: %w", err)
		}
	}

	// Create EnvoyExtensionPolicy if provided
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() {
		extensionPolicy := &models.EnvoyExtensionPolicy{
			RouteID:   &route.ID,
			ProjectID: projectID,
			Config: models.EnvoyExtensionPolicyConfig{
				Lua:     input.ExtensionPolicy.Lua,
				Wasm:    input.ExtensionPolicy.Wasm,
				ExtProc: input.ExtensionPolicy.ExtProc,
			},
		}
		if err := s.envoyExtensionPolicyRepo.Create(extensionPolicy); err != nil {
			return fmt.Errorf("failed to create envoy extension policy: %w", err)
		}
	}

	// Create WAF policy if provided
	if input.WafPolicy != nil {
		wafConfig := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafConfig.Validate(); err != nil {
			return fmt.Errorf("invalid WAF policy config: %w", err)
		}

		wafPolicy := &models.WafPolicy{
			RouteID:   route.ID,
			ProjectID: projectID,
			Config:    wafConfig,
		}
		if err := s.wafPolicyRepo.Create(wafPolicy); err != nil {
			return fmt.Errorf("failed to create WAF policy: %w", err)
		}
	}

	return nil
}

// persistUpdatePolicies updates or creates the SecurityPolicy,
// BackendTrafficPolicy, EnvoyExtensionPolicy, and WafPolicy records for an
// updated route.
func (s *RouteService) persistUpdatePolicies(route *models.Route, projectID uuid.UUID, input *UpdateRouteInput) error {
	// Update or create SecurityPolicy if provided
	if input.SecurityPolicy != nil {
		spConfig := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
			spConfig.Authorization = routeplan.BuildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			spConfig.APIKeyAuth = routeplan.BuildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			spConfig.JWT = routeplan.BuildJWTConfigFromInput(input.SecurityPolicy.JWT)
			spConfig.OIDC = routeplan.BuildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		spConfig.ExtAuth = input.SecurityPolicy.ExtAuth
		securityPolicy := &models.SecurityPolicy{
			RouteID:   route.ID,
			ProjectID: projectID,
			Config:    spConfig,
		}
		if err := s.securityPolicyRepo.Upsert(securityPolicy); err != nil {
			return fmt.Errorf("failed to update security policy: %w", err)
		}
	} else if input.SecurityPolicy == nil {
		// If SecurityPolicy is explicitly nil, delete existing one
		_ = s.securityPolicyRepo.DeleteByRouteID(route.ID)
	}

	// Update or create BackendTrafficPolicy if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
		backendTrafficPolicy := &models.BackendTrafficPolicy{
			RouteID:   &route.ID,
			ProjectID: projectID,
			Config: models.BackendTrafficPolicyConfig{
				Compression:      input.BackendTrafficPolicy.Compression,
				Retry:            input.BackendTrafficPolicy.Retry,
				LoadBalancer:     input.BackendTrafficPolicy.LoadBalancer,
				CircuitBreaker:   input.BackendTrafficPolicy.CircuitBreaker,
				HealthCheck:      input.BackendTrafficPolicy.HealthCheck,
				FaultInjection:   input.BackendTrafficPolicy.FaultInjection,
				RateLimit:        input.BackendTrafficPolicy.RateLimit,
				RequestBuffer:    input.BackendTrafficPolicy.RequestBuffer,
				ResponseOverride: input.BackendTrafficPolicy.ResponseOverride,
				Timeout:          input.BackendTrafficPolicy.Timeout,
			},
		}
		if err := s.backendTrafficPolicyRepo.Upsert(backendTrafficPolicy); err != nil {
			return fmt.Errorf("failed to update backend traffic policy: %w", err)
		}
	} else if input.BackendTrafficPolicy == nil || !input.BackendTrafficPolicy.HasContent() {
		// If BackendTrafficPolicy is explicitly nil or has no content, delete existing one
		_ = s.backendTrafficPolicyRepo.DeleteByRouteID(route.ID)
	}

	// Update or create EnvoyExtensionPolicy if provided
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() {
		extensionPolicy := &models.EnvoyExtensionPolicy{
			RouteID:   &route.ID,
			ProjectID: projectID,
			Config: models.EnvoyExtensionPolicyConfig{
				Lua:     input.ExtensionPolicy.Lua,
				Wasm:    input.ExtensionPolicy.Wasm,
				ExtProc: input.ExtensionPolicy.ExtProc,
			},
		}
		if err := s.envoyExtensionPolicyRepo.Upsert(extensionPolicy); err != nil {
			return fmt.Errorf("failed to update envoy extension policy: %w", err)
		}
	} else if input.ExtensionPolicy == nil || !input.ExtensionPolicy.HasContent() {
		// If ExtensionPolicy is explicitly nil or has no content, delete existing one
		_ = s.envoyExtensionPolicyRepo.DeleteByRouteID(route.ID)
	}

	// Update WAF policy if provided
	if input.WafPolicy != nil {
		wafConfig := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafConfig.Validate(); err != nil {
			return fmt.Errorf("invalid WAF policy config: %w", err)
		}

		wafPolicy := &models.WafPolicy{
			RouteID:   route.ID,
			ProjectID: projectID,
			Config:    wafConfig,
		}
		if err := s.wafPolicyRepo.Upsert(wafPolicy); err != nil {
			return fmt.Errorf("failed to update WAF policy: %w", err)
		}
	}

	return nil
}

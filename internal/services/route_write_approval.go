package services

import (
	"encoding/json"
	"fmt"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

func marshalRouteSnapshot(config *models.RouteConfig, sp *models.SecurityPolicyConfig, btp *models.BackendTrafficPolicyConfig, eep *models.EnvoyExtensionPolicyConfig, waf *models.WafPolicyConfig) json.RawMessage {
	out, _ := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          config,
		SecurityPolicy:       sp,
		BackendTrafficPolicy: btp,
		EnvoyExtensionPolicy: eep,
		WafPolicy:            waf,
	})
	return out
}

// updateApprovalSnapshots carries the proposed and previous policy configs
// for submitUpdateApproval by name, so proposed/previous cannot be swapped
// at the call site without the compiler catching a field mismatch.
type updateApprovalSnapshots struct {
	ProposedSecurityPolicy       *models.SecurityPolicyConfig
	ProposedBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	ProposedEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	ProposedWafPolicy            *models.WafPolicyConfig

	PreviousConfig               models.RouteConfig
	PreviousSecurityPolicy       *models.SecurityPolicyConfig
	PreviousBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	PreviousEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	PreviousWafPolicy            *models.WafPolicyConfig
}

// persistCreatePolicies creates the SecurityPolicy, BackendTrafficPolicy,
// EnvoyExtensionPolicy, and WafPolicy records for a newly created route.
func (w *routeWrite) persistCreatePolicies(route *models.Route, projectID uuid.UUID, securityMode models.SecurityMode, input *CreateRouteInput) error {
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
		if err := w.securityPolicyRepo.Create(securityPolicy); err != nil {
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
		if err := w.backendTrafficPolicyRepo.Create(backendTrafficPolicy); err != nil {
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
		if err := w.envoyExtensionPolicyRepo.Create(extensionPolicy); err != nil {
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
		if err := w.wafPolicyRepo.Create(wafPolicy); err != nil {
			return fmt.Errorf("failed to create WAF policy: %w", err)
		}
	}

	return nil
}

// persistUpdatePolicies updates or creates the SecurityPolicy,
// BackendTrafficPolicy, EnvoyExtensionPolicy, and WafPolicy records for an
// updated route.
func (w *routeWrite) persistUpdatePolicies(route *models.Route, projectID uuid.UUID, input *UpdateRouteInput) error {
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
		if err := w.securityPolicyRepo.Upsert(securityPolicy); err != nil {
			return fmt.Errorf("failed to update security policy: %w", err)
		}
	} else if input.SecurityPolicy == nil {
		// If SecurityPolicy is explicitly nil, delete existing one
		_ = w.securityPolicyRepo.DeleteByRouteID(route.ID)
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
		if err := w.backendTrafficPolicyRepo.Upsert(backendTrafficPolicy); err != nil {
			return fmt.Errorf("failed to update backend traffic policy: %w", err)
		}
	} else if input.BackendTrafficPolicy == nil || !input.BackendTrafficPolicy.HasContent() {
		// If BackendTrafficPolicy is explicitly nil or has no content, delete existing one
		_ = w.backendTrafficPolicyRepo.DeleteByRouteID(route.ID)
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
		if err := w.envoyExtensionPolicyRepo.Upsert(extensionPolicy); err != nil {
			return fmt.Errorf("failed to update envoy extension policy: %w", err)
		}
	} else if input.ExtensionPolicy == nil || !input.ExtensionPolicy.HasContent() {
		// If ExtensionPolicy is explicitly nil or has no content, delete existing one
		_ = w.envoyExtensionPolicyRepo.DeleteByRouteID(route.ID)
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
		if err := w.wafPolicyRepo.Upsert(wafPolicy); err != nil {
			return fmt.Errorf("failed to update WAF policy: %w", err)
		}
	}

	return nil
}

func (w *routeWrite) submitCreateApproval(route *models.Route, domain *models.Domain, input *CreateRouteInput, createdBy uuid.UUID, snapshotSP *models.SecurityPolicyConfig, snapshotBTP *models.BackendTrafficPolicyConfig, snapshotEEP *models.EnvoyExtensionPolicyConfig, snapshotWaf *models.WafPolicyConfig) (*models.Approval, bool, error) {
	// Check if approvals are disabled for this project
	project, err := w.projectRepo.GetByID(domain.ProjectID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check project approval settings: %w", err)
	}
	if !project.ApprovalEnabled {
		// Skip approval — set route directly to approved.
		// route was just persisted at pending_create (struct literal
		// above), so this is pending_create -> approved and To always
		// writes; nothing else has been mutated since routeRepo.Create.
		if err := w.state.To(models.SiteRouteCreateFastPath, route, models.RouteStatusApproved,
			"route created, project approvals disabled"); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	configSnapshot := marshalRouteSnapshot(&input.Config, snapshotSP, snapshotBTP, snapshotEEP, snapshotWaf)

	// Submit plans the stages and persists the approval; the service no
	// longer builds either.
	approval, err := w.approvals.Submit(approvalpkg.Spec{
		ProjectID:         domain.ProjectID,
		EntityType:        models.ApprovalEntityRoute,
		EntityID:          route.ID,
		Action:            models.ApprovalActionCreate,
		ConfigSnapshot:    configSnapshot,
		SubmittedBy:       createdBy,
		ChangeDescription: input.ChangeDescription,
		AIReview:          input.AIReview,
	})
	if err != nil {
		return nil, false, err
	}

	return approval, false, nil
}

func (w *routeWrite) submitUpdateApproval(route *models.Route, domain *models.Domain, input *UpdateRouteInput, submittedBy uuid.UUID, snaps updateApprovalSnapshots) (*models.Approval, bool, error) {
	// Check if approvals are disabled for this project
	project, err := w.projectRepo.GetByID(domain.ProjectID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check project approval settings: %w", err)
	}
	if !project.ApprovalEnabled {
		// Skip approval — set route directly to pending_deploy.
		// route sits at pending_update, persisted above, and no field
		// other than Status has been touched since.
		if err := w.state.To(models.SiteRouteUpdateFastPath, route, models.RouteStatusPendingDeploy,
			"route update submitted, project approvals disabled"); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	configSnapshot := marshalRouteSnapshot(&input.Config, snaps.ProposedSecurityPolicy, snaps.ProposedBackendTrafficPolicy, snaps.ProposedEnvoyExtensionPolicy, snaps.ProposedWafPolicy)

	// Build previous config snapshot
	prevConfigSnapshot := marshalRouteSnapshot(&snaps.PreviousConfig, snaps.PreviousSecurityPolicy, snaps.PreviousBackendTrafficPolicy, snaps.PreviousEnvoyExtensionPolicy, snaps.PreviousWafPolicy)

	approval, err := w.approvals.Submit(approvalpkg.Spec{
		ProjectID:         domain.ProjectID,
		EntityType:        models.ApprovalEntityRoute,
		EntityID:          route.ID,
		Action:            models.ApprovalActionUpdate,
		ConfigSnapshot:    configSnapshot,
		PreviousConfig:    prevConfigSnapshot,
		SubmittedBy:       submittedBy,
		ChangeDescription: input.ChangeDescription,
		AIReview:          input.AIReview,
	})
	if err != nil {
		return nil, false, err
	}

	return approval, false, nil
}

func (w *routeWrite) submitDeleteApproval(route *models.Route, domain *models.Domain, submittedBy uuid.UUID, deletePrevSP *models.SecurityPolicyConfig, deletePrevBTP *models.BackendTrafficPolicyConfig, deletePrevEEP *models.EnvoyExtensionPolicyConfig, deletePrevWaf *models.WafPolicyConfig) (*models.Approval, bool, error) {
	// Check if approvals are disabled for this project
	project, err := w.projectRepo.GetByID(domain.ProjectID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check project approval settings: %w", err)
	}
	if !project.ApprovalEnabled {
		// Skip approval — set route directly to pending_deploy.
		// route sits at pending_delete, persisted above.
		if err := w.state.To(models.SiteRouteDeleteFastPath, route, models.RouteStatusPendingDeploy,
			"route deletion submitted, project approvals disabled"); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	// Build config snapshot (current config being deleted)
	configSnapshot := marshalRouteSnapshot(&route.Config, deletePrevSP, deletePrevBTP, deletePrevEEP, deletePrevWaf)

	approval, err := w.approvals.Submit(approvalpkg.Spec{
		ProjectID:      domain.ProjectID,
		EntityType:     models.ApprovalEntityRoute,
		EntityID:       route.ID,
		Action:         models.ApprovalActionDelete,
		ConfigSnapshot: configSnapshot,
		SubmittedBy:    submittedBy,
	})
	if err != nil {
		return nil, false, err
	}

	return approval, false, nil
}

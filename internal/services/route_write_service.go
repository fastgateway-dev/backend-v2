package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/routestate"
	"github.com/google/uuid"
)

type routeWrite struct {
	routeRepo                repository.RouteRepositoryInterface
	domainRepo               repository.DomainRepositoryInterface
	teamRepo                 repository.TeamRepositoryInterface
	projectRepo              repository.ProjectRepositoryInterface
	approvalRepo             repository.UnifiedApprovalRepositoryInterface
	securityPolicyRepo       repository.SecurityPolicyRepositoryInterface
	backendTrafficPolicyRepo repository.BackendTrafficPolicyRepositoryInterface
	envoyExtensionPolicyRepo repository.EnvoyExtensionPolicyRepositoryInterface
	wafPolicyRepo            repository.WafPolicyRepositoryInterface

	k8sRefGrants ReferenceGrantChecker

	approvals *approvalpkg.Engine

	state *routestate.Machine

	assembler *routeAssembler
	query     *routeQuery
}

// ensureReferenceGrantsForDomain verifies backend namespace ReferenceGrants include
// the domain's namespace. This is a deploy-time safety net.
func (w *routeWrite) ensureReferenceGrantsForDomain(ctx context.Context, route *models.Route, domain *models.Domain) {
	if len(route.Config.Backends) == 0 {
		return
	}
	for _, backend := range route.Config.Backends {
		ns := backend.Namespace
		if ns == "" || ns == domain.Namespace {
			continue
		}
		rgName := generateReferenceGrantName(domain.ProjectID, ns)
		exists, _ := w.k8sRefGrants.ReferenceGrantExists(ctx, domain.ProjectID, ns, rgName)
		if !exists {
			log.Printf("Deploy safety net: ReferenceGrant missing in %s for domain %s, skipping (will be created on next namespace sync)", ns, domain.Namespace)
		}
	}
}

// Create creates a new route (submits for approval)
func (w *routeWrite) Create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID) (*models.Route, error) {
	// Validate route name - no spaces allowed
	if strings.Contains(input.Name, " ") {
		return nil, errors.New("route name cannot contain spaces")
	}

	// Validate route name format - must be lowercase alphanumeric with dashes
	if !isValidK8sName(input.Name) {
		return nil, errors.New("route name must be lowercase alphanumeric with dashes only (e.g., 'user-api')")
	}

	// Check if route name already exists in domain
	exists, err := w.routeRepo.ExistsByName(domainID, input.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("route name already exists in this domain")
	}

	// Verify domain exists
	domain, err := w.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	if err := w.validateRouteTrafficPolicies(&input.Config, input.BackendTrafficPolicy, domain.ProjectID); err != nil {
		return nil, err
	}

	if err := validateDirectResponseInput(&input.Config, input.BackendTrafficPolicy); err != nil {
		return nil, err
	}

	// Verify team exists
	_, err = w.teamRepo.GetByID(input.TeamID)
	if err != nil {
		return nil, errors.New("team not found")
	}

	protocol := input.Protocol
	if protocol == "" {
		protocol = models.RouteProtocolHTTP
	}

	// Default security mode
	securityMode := input.SecurityMode
	if securityMode == "" {
		securityMode = models.SecurityModeGeneral
	}

	if err := validateWriteSecurityMode(securityMode, input.SecurityPolicy, true); err != nil {
		return nil, err
	}

	if err := w.validateRouteShapeAndConflicts(&input.Config, input.BackendTrafficPolicy, protocol, domainID, nil); err != nil {
		return nil, err
	}

	// Generate route UUID first so we can use it for K8s resource name
	routeID := w.assembler.newID()

	// Generate K8s resource name: {route-name}-{first-8-chars-of-route-uuid}.
	// Safe only because input.Name was already validated by isValidK8sName above
	// (see the comment on that function) - kubernetes.RouteK8sName sanitizes
	// differently than isValidK8sName rejects, so they only agree on validated input.
	k8sRouteName := kubernetes.RouteK8sName(input.Name, routeID.String())

	// Validate labels
	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
	}

	route := &models.Route{
		DomainID:     domainID,
		TeamID:       input.TeamID,
		Name:         input.Name,
		Description:  input.Description,
		Protocol:     protocol,
		SecurityMode: securityMode,
		Config:       input.Config,
		Status:       models.RouteStatusPendingCreate,
		K8sRouteName: k8sRouteName,
		CreatedBy:    createdBy,
		Labels:       input.Labels,
	}
	route.ID = routeID // Set the pre-generated UUID

	if err := w.routeRepo.Create(route); err != nil {
		return nil, err
	}

	// Build config snapshot for unified approval
	var snapshotSP *models.SecurityPolicyConfig
	if input.SecurityPolicy != nil {
		snapshot := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if securityMode == models.SecurityModeGeneral {
			snapshot.Authorization = routeplan.BuildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			snapshot.APIKeyAuth = routeplan.BuildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			snapshot.JWT = routeplan.BuildJWTConfigFromInput(input.SecurityPolicy.JWT)
			snapshot.OIDC = routeplan.BuildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		snapshot.ExtAuth = input.SecurityPolicy.ExtAuth
		snapshotSP = &snapshot
	}

	var snapshotBTP *models.BackendTrafficPolicyConfig
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
		snapshotBTP = &models.BackendTrafficPolicyConfig{
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
		}
	}

	var snapshotEEP *models.EnvoyExtensionPolicyConfig
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() {
		snapshotEEP = &models.EnvoyExtensionPolicyConfig{
			Lua:     input.ExtensionPolicy.Lua,
			Wasm:    input.ExtensionPolicy.Wasm,
			ExtProc: input.ExtensionPolicy.ExtProc,
		}
	}

	var snapshotWaf *models.WafPolicyConfig
	if input.WafPolicy != nil {
		wafCfg := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafCfg.Validate(); err == nil {
			snapshotWaf = &wafCfg
		}
	}

	approval, fastPath, err := w.submitCreateApproval(route, domain, input, createdBy, snapshotSP, snapshotBTP, snapshotEEP, snapshotWaf)
	if err != nil {
		return nil, err
	}
	if fastPath {
		return route, nil
	}

	if err := w.persistCreatePolicies(route, domain.ProjectID, securityMode, input); err != nil {
		return nil, err
	}

	route.PendingApproval = approval
	return route, nil
}

// Update updates a route (submits for approval)
func (w *routeWrite) Update(id uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error) {
	route, err := w.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Get domain to validate namespaces
	domain, err := w.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	if err := w.validateRouteTrafficPolicies(&input.Config, input.BackendTrafficPolicy, domain.ProjectID); err != nil {
		return nil, err
	}

	if err := validateWriteSecurityMode(route.SecurityMode, input.SecurityPolicy, false); err != nil {
		return nil, err
	}

	if err := w.validateRouteShapeAndConflicts(&input.Config, input.BackendTrafficPolicy, route.Protocol, route.DomainID, &id); err != nil {
		return nil, err
	}

	if err := validateDirectResponseInput(&input.Config, input.BackendTrafficPolicy); err != nil {
		return nil, err
	}

	// Check if there's already a pending approval
	existing, err := w.approvalRepo.GetPendingByEntityID(models.ApprovalEntityRoute, id)
	if err == nil && existing != nil {
		return nil, errors.New("there is already a pending approval for this route")
	}

	// Store previous config
	previousConfig := route.Config

	// Capture previous SecurityPolicy config (before update)
	var previousSecurityPolicy *models.SecurityPolicyConfig
	if existingSP, err := w.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
		spConfig := existingSP.Config
		previousSecurityPolicy = &spConfig
	}

	// Capture previous BackendTrafficPolicy config (before update)
	var previousBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	if existingBTP, err := w.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
		btpConfig := existingBTP.Config
		previousBackendTrafficPolicy = &btpConfig
	}

	// Capture previous EnvoyExtensionPolicy config (before update)
	var previousEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	if existingEEP, err := w.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
		eepConfig := existingEEP.Config
		previousEnvoyExtensionPolicy = &eepConfig
	}

	// Apply the caller's field changes BEFORE the status transition, so the
	// state machine's write carries them.
	if input.Description != "" {
		route.Description = input.Description
	}
	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
		route.Labels = input.Labels
	}

	// Update route status.
	//
	// routestate.Machine.To owns route.Status and nothing else, and it does
	// NOT write on a no-op transition (see its CONTRACT comment). Description
	// and Labels above are exactly the mutations the pre-2D unconditional
	// routeRepo.Update persisted, so an already-pending_update route — an
	// orphan whose approval submit failed — must still be written explicitly
	// or those edits are silently dropped.
	if route.Status == models.RouteStatusPendingUpdate {
		if err := w.routeRepo.Update(route); err != nil {
			return nil, err
		}
	} else if err := w.state.To(models.SiteRouteUpdate, route, models.RouteStatusPendingUpdate,
		"route update submitted"); err != nil {
		return nil, err
	}

	// Build config snapshot for unified approval
	var updateSnapshotSP *models.SecurityPolicyConfig
	if input.SecurityPolicy != nil {
		snapshot := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
			snapshot.Authorization = routeplan.BuildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			snapshot.APIKeyAuth = routeplan.BuildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			snapshot.JWT = routeplan.BuildJWTConfigFromInput(input.SecurityPolicy.JWT)
			snapshot.OIDC = routeplan.BuildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		snapshot.ExtAuth = input.SecurityPolicy.ExtAuth
		updateSnapshotSP = &snapshot
	}

	var updateSnapshotBTP *models.BackendTrafficPolicyConfig
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
		updateSnapshotBTP = &models.BackendTrafficPolicyConfig{
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
		}
	}

	var updateSnapshotEEP *models.EnvoyExtensionPolicyConfig
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() {
		updateSnapshotEEP = &models.EnvoyExtensionPolicyConfig{
			Lua:     input.ExtensionPolicy.Lua,
			Wasm:    input.ExtensionPolicy.Wasm,
			ExtProc: input.ExtensionPolicy.ExtProc,
		}
	}

	// Build WAF snapshot for proposed config
	var updateSnapshotWaf *models.WafPolicyConfig
	if input.WafPolicy != nil {
		wafCfg := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafCfg.Validate(); err == nil {
			updateSnapshotWaf = &wafCfg
		}
	}

	// Capture previous WAF policy for approval diff
	var previousWafPolicy *models.WafPolicyConfig
	if existingWaf, err := w.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
		prevWaf := existingWaf.Config
		previousWafPolicy = &prevWaf
	}

	approval, fastPath, err := w.submitUpdateApproval(route, domain, input, submittedBy, updateApprovalSnapshots{
		ProposedSecurityPolicy:       updateSnapshotSP,
		ProposedBackendTrafficPolicy: updateSnapshotBTP,
		ProposedEnvoyExtensionPolicy: updateSnapshotEEP,
		ProposedWafPolicy:            updateSnapshotWaf,

		PreviousConfig:               previousConfig,
		PreviousSecurityPolicy:       previousSecurityPolicy,
		PreviousBackendTrafficPolicy: previousBackendTrafficPolicy,
		PreviousEnvoyExtensionPolicy: previousEnvoyExtensionPolicy,
		PreviousWafPolicy:            previousWafPolicy,
	})
	if err != nil {
		return nil, err
	}
	if fastPath {
		return route, nil
	}

	if err := w.persistUpdatePolicies(route, domain.ProjectID, input); err != nil {
		return nil, err
	}

	route.PendingApproval = approval
	return route, nil
}

// Delete requests deletion of a route (submits for approval)
func (w *routeWrite) Delete(id uuid.UUID, submittedBy uuid.UUID) (*models.Route, error) {
	route, err := w.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Check if there's already a pending approval
	existing, err := w.approvalRepo.GetPendingByEntityID(models.ApprovalEntityRoute, id)
	if err == nil && existing != nil {
		return nil, errors.New("there is already a pending approval for this route")
	}

	// Get domain for project ID
	domain, err := w.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, err
	}

	// Update route status. Delete mutates no other route field, so To's
	// no-op path (an already-pending_delete orphan) drops nothing.
	if err := w.state.To(models.SiteRouteDelete, route, models.RouteStatusPendingDelete,
		"route deletion submitted"); err != nil {
		return nil, err
	}

	// Capture current policy configs for the previous config snapshot
	var deletePrevSP *models.SecurityPolicyConfig
	if existingSP, err := w.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
		spConfig := existingSP.Config
		deletePrevSP = &spConfig
	}

	var deletePrevBTP *models.BackendTrafficPolicyConfig
	if existingBTP, err := w.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
		btpConfig := existingBTP.Config
		deletePrevBTP = &btpConfig
	}

	var deletePrevEEP *models.EnvoyExtensionPolicyConfig
	if existingEEP, err := w.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
		eepConfig := existingEEP.Config
		deletePrevEEP = &eepConfig
	}

	var deletePrevWaf *models.WafPolicyConfig
	if existingWaf, err := w.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
		wafConfig := existingWaf.Config
		deletePrevWaf = &wafConfig
	}

	approval, fastPath, err := w.submitDeleteApproval(route, domain, submittedBy, deletePrevSP, deletePrevBTP, deletePrevEEP, deletePrevWaf)
	if err != nil {
		return nil, err
	}
	if fastPath {
		return route, nil
	}

	route.PendingApproval = approval
	return route, nil
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

// OnApproved moves the route to its post-approval state. This is the logic
// that lived in ApprovalService.onRouteApprovalComplete before Phase 2D; it
// belongs with the route, per §6.5 of the master design.
//
// The status mapping is reproduced verbatim: create -> approved,
// update -> pending_deploy, delete -> pending_deploy. create and update also
// apply the approved config snapshot to the route before the status moves,
// exactly as onRouteApprovalComplete did; delete does not.
func (w *routeWrite) OnApproved(a *models.Approval) error {
	route, err := w.routeRepo.GetByID(a.EntityID)
	if err != nil {
		return err
	}

	var next models.RouteStatus
	switch a.Action {
	case models.ApprovalActionCreate:
		if err := applyRouteApprovalSnapshot(route, a.ConfigSnapshot); err != nil {
			return err
		}
		next = models.RouteStatusApproved

	case models.ApprovalActionUpdate:
		if err := applyRouteApprovalSnapshot(route, a.ConfigSnapshot); err != nil {
			return err
		}
		next = models.RouteStatusPendingDeploy

	case models.ApprovalActionDelete:
		next = models.RouteStatusPendingDeploy

	default:
		// Unreachable for a route entity, which only ever carries
		// create/update/delete. It fails closed rather than persisting the
		// route with an unchanged status, which is what the pre-2D switch
		// did when it fell through.
		return fmt.Errorf("route approval: unsupported action %q", a.Action)
	}

	// The create and update cases above applied the approved config snapshot
	// to route. routestate.Machine.To owns route.Status and nothing else, and
	// it does not write on a no-op transition (see its CONTRACT comment), so
	// an already-at-target route would apply the snapshot in memory and throw
	// it away. Pre-2D this path did an unconditional routeRepo.Update; persist
	// explicitly here so the approved config survives regardless of whether
	// the status actually moves.
	if route.Status == next {
		return w.routeRepo.Update(route)
	}

	return w.state.To(models.SiteApprovalApproved, route, next, fmt.Sprintf("approval %s approved (action %s)", a.ID, a.Action))
}

// OnRejected reverts the route when its approval is rejected. Reproduces
// ApprovalService.onRouteApprovalRejected: create -> rejected,
// update -> active, delete -> active.
func (w *routeWrite) OnRejected(a *models.Approval) error {
	route, err := w.routeRepo.GetByID(a.EntityID)
	if err != nil {
		return err
	}

	var next models.RouteStatus
	switch a.Action {
	case models.ApprovalActionCreate:
		next = models.RouteStatusRejected
	case models.ApprovalActionUpdate, models.ApprovalActionDelete:
		next = models.RouteStatusActive
	default:
		return fmt.Errorf("route approval: unsupported action %q", a.Action)
	}

	return w.state.To(models.SiteApprovalRejected, route, next, fmt.Sprintf("approval %s rejected (action %s)", a.ID, a.Action))
}

// OnCancelled reverts the route when its approval is withdrawn. Reproduces
// ApprovalService.onRouteApprovalCancelled: a cancelled create deletes the
// route outright (it was never deployed), while a cancelled update or
// delete returns the still-deployed route to active.
func (w *routeWrite) OnCancelled(a *models.Approval) error {
	switch a.Action {
	case models.ApprovalActionCreate:
		// Route was never deployed, delete it entirely.
		return w.routeRepo.Delete(a.EntityID)

	case models.ApprovalActionUpdate, models.ApprovalActionDelete:
		// Route is already deployed, revert to active.
		route, err := w.routeRepo.GetByID(a.EntityID)
		if err != nil {
			return err
		}
		next := models.RouteStatusActive
		return w.state.To(models.SiteApprovalCancelled, route, next, fmt.Sprintf("approval %s cancelled (action %s)", a.ID, a.Action))

	default:
		return fmt.Errorf("route approval: unsupported action %q", a.Action)
	}
}

// CanCancel implements approval.CancelAuthorizer: a member of the route's
// owning team may cancel a route approval on top of the engine's own
// submitter/owner/project-admin rights. This is the route lookup the engine
// deliberately does not own (pre-2D approval_service.go:535-543).
//
// It fails closed: both the route lookup error and the membership lookup
// error mean "not permitted", which is what the pre-2D code did by
// discarding them.
func (w *routeWrite) CanCancel(a *models.Approval, user *models.User) bool {
	if a.EntityType != models.ApprovalEntityRoute {
		return false
	}
	route, err := w.routeRepo.GetByID(a.EntityID)
	if err != nil {
		return false
	}
	isMember, err := w.teamRepo.IsMember(route.TeamID, user.ID)
	if err != nil {
		return false
	}
	return isMember
}

// GetApprovalIDForEntity returns the most recent approval ID for an entity.
// Checks pending first, then latest approved.
func (w *routeWrite) GetApprovalIDForEntity(entityType models.ApprovalEntityType, entityID uuid.UUID) (*uuid.UUID, error) {
	// Try pending first
	pending, err := w.approvalRepo.GetPendingByEntityID(entityType, entityID)
	if err == nil && pending != nil {
		return &pending.ID, nil
	}
	// Fall back to latest approved
	approved, err := w.approvalRepo.GetLatestApprovedByEntityID(entityType, entityID)
	if err == nil && approved != nil {
		return &approved.ID, nil
	}
	return nil, fmt.Errorf("no approval found")
}
